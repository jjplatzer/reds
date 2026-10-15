package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes int64 = 16 << 20

// Avoid continuously retrying a page if NOTAC keeps rate-limiting us.
const maxRateLimitRetries = 8

type notacRateLimitError struct{ retryAfter time.Duration }

func (e *notacRateLimitError) Error() string {
	return fmt.Sprintf("NOTAC returned 429: rate limited (retry after %s)", e.retryAfter)
}

// Retry-After may be either a number of seconds or an HTTP date.
// A missing or malformed header is treated conservatively.
func notacRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if seconds, err := strconv.Atoi(header); err == nil && seconds >= 0 {
		if seconds == 0 {
			return time.Second
		}
		return time.Duration(seconds) * time.Second
	}
	if until, err := http.ParseTime(header); err == nil {
		if d := time.Until(until); d > 0 {
			return d
		}
		return time.Second
	}
	return time.Minute
}

func waitNOTAC(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type deltaPage struct {
	Next       *string           `json:"next"`
	NextCursor string            `json:"next_cursor"`
	AsOf       string            `json:"as_of"`
	Results    []json.RawMessage `json:"results"`
}

type mirror struct {
	cfg     config
	client  *http.Client
	store   *snapshotStore
	baseURL *url.URL
}

func newMirror(cfg config) (*mirror, error) {
	endpoint, err := url.Parse(cfg.BaseURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil {
		return nil, errors.New("invalid NOTAC delta endpoint")
	}
	s, err := readSnapshot(cfg.StatePath, cfg.Locations)
	if err != nil {
		return nil, err
	}
	return &mirror{
		cfg: cfg,
		client: &http.Client{
			Timeout: 35 * time.Second,
			// Never send the Authorization header to a redirect destination.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
		store:   &snapshotStore{s: s},
		baseURL: endpoint,
	}, nil
}

func (m *mirror) snapshot() snapshotFile { return m.store.get() }

func (m *mirror) pollURL(s snapshotFile) (*url.URL, error) {
	var u url.URL
	if s.Next != "" {
		rel, err := url.Parse(s.Next)
		if err != nil {
			return nil, fmt.Errorf("invalid saved NOTAC next URL: %w", err)
		}
		u = *m.baseURL.ResolveReference(rel)
	} else {
		u = *m.baseURL
		q := u.Query()
		if s.Cursor != "" {
			q.Set("cursor", s.Cursor)
		}
		u.RawQuery = q.Encode()
	}
	// API paging links are untrusted network input; never send our bearer
	// credential to another host, scheme, or endpoint.
	if u.Scheme != m.baseURL.Scheme || u.Host != m.baseURL.Host || u.Path != m.baseURL.Path || u.User != nil || u.Fragment != "" {
		return nil, errors.New("NOTAC pagination URL points outside the permitted delta endpoint")
	}
	q := u.Query()
	q.Set("location", strings.Join(m.cfg.Locations, ","))
	u.RawQuery = q.Encode()
	return &u, nil
}

func (m *mirror) fetch(ctx context.Context, u *url.URL) (deltaPage, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return deltaPage{}, "", err
	}
	req.Header.Set("Authorization", "Bearer "+m.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "REDS-NOTAC/1.0")
	res, err := m.client.Do(req)
	if err != nil {
		return deltaPage{}, "", fmt.Errorf("NOTAC request: %w", err)
	}
	defer res.Body.Close()
	remaining := res.Header.Get("X-Credits-Remaining")
	if res.StatusCode != http.StatusOK {
		// Don't log full HTTP URLs or server responses: they can contain
		// credentials or huge payloads. Credit/rate-limit errors are explicit.
		switch res.StatusCode {
		case http.StatusUnauthorized:
			return deltaPage{}, remaining, errors.New("NOTAC returned 401: check NOTAC_API_KEY")
		case http.StatusPaymentRequired:
			return deltaPage{}, remaining, errors.New("NOTAC returned 402: credits exhausted; wait for monthly reset")
		case http.StatusForbidden:
			return deltaPage{}, remaining, errors.New("NOTAC returned 403: token is not permitted")
		case http.StatusTooManyRequests:
			return deltaPage{}, remaining, &notacRateLimitError{retryAfter: notacRetryAfter(res.Header.Get("Retry-After"))}
		default:
			return deltaPage{}, remaining, fmt.Errorf("NOTAC returned HTTP %d", res.StatusCode)
		}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return deltaPage{}, remaining, err
	}
	if int64(len(b)) > maxResponseBytes {
		return deltaPage{}, remaining, errors.New("NOTAC response exceeds 16 MiB limit")
	}
	if !noUnexpectedJSON(b) {
		return deltaPage{}, remaining, errors.New("NOTAC returned invalid JSON")
	}
	var page deltaPage
	if err := json.Unmarshal(b, &page); err != nil {
		return page, remaining, fmt.Errorf("decode NOTAC delta: %w", err)
	}
	// Every valid page must contain a usable continuation cursor. Never
	// advance data with an absent cursor: the next sync would lose changes.
	if page.NextCursor == "" || page.Results == nil {
		return page, remaining, errors.New("NOTAC delta response missing next_cursor or results")
	}
	return page, remaining, nil
}

// fetchWithBackoff retries only transient HTTP 429 responses, respecting NOTAC's
// Retry-After header. All other errors end the poll; no cursor is advanced.
func (m *mirror) fetchWithBackoff(ctx context.Context, u *url.URL) (deltaPage, string, error) {
	for retries := 0; ; retries++ {
		page, remaining, err := m.fetch(ctx, u)
		var limited *notacRateLimitError
		if !errors.As(err, &limited) {
			return page, remaining, err
		}
		if retries >= maxRateLimitRetries {
			return deltaPage{}, remaining, fmt.Errorf("NOTAC rate limit persisted after %d retries; saved progress is intact: %w", retries, err)
		}
		// A small margin avoids retrying at the exact boundary of the limit.
		delay := limited.retryAfter + time.Second
		log.Printf("[NOTAC] rate limited; retrying this page in %s (%d/%d)", delay.Round(time.Second), retries+1, maxRateLimitRetries)
		if err := waitNOTAC(ctx, delay); err != nil {
			return deltaPage{}, remaining, err
		}
	}
}

// sync advances a durable cursor and raw notice snapshot together. If a
// bounded per-run page allowance is reached, the remaining pages continue on
// the next poll; no data is silently skipped.
func (m *mirror) sync(ctx context.Context) error {
	s := m.snapshot()
	for pageNumber := 0; pageNumber < m.cfg.MaxPages; pageNumber++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		// The initial snapshot may span hundreds of pages. A page limit is
		// not a rate limit: pace the requests even before we see a 429.
		if pageNumber > 0 {
			if err := waitNOTAC(ctx, m.cfg.PageDelay); err != nil {
				return err
			}
		}
		u, err := m.pollURL(s)
		if err != nil {
			return err
		}
		page, remaining, err := m.fetchWithBackoff(ctx, u)
		if err != nil {
			return err
		}
		updated := cloneSnapshot(s)
		for i, raw := range page.Results {
			var notice noticeEnvelope
			if err := json.Unmarshal(raw, &notice); err != nil || strings.TrimSpace(notice.ID) == "" {
				return fmt.Errorf("invalid delta notice at row %d: no id or malformed record", i)
			}
			if isWithdrawn(notice.Status) {
				delete(updated.Records, notice.ID)
				continue
			}
			// Location filter is reapplied to every request, but defensive
			// validation protects the airport-specific REDS cache.
			if notice.LocationCode != "" && !containsLocation(m.cfg.Locations, notice.LocationCode) {
				continue
			}
			updated.Records[notice.ID] = append(json.RawMessage(nil), raw...)
		}
		updated.Cursor = page.NextCursor
		updated.APIAsOf = page.AsOf
		updated.CreditsRemaining = remaining
		updated.Pages++
		updated.Next = ""
		if page.Next != nil {
			updated.Next = strings.TrimSpace(*page.Next)
		}
		if updated.Next != "" {
			// Validate continuation before persisting it. Otherwise a bad
			// URL could break restarts after an otherwise successful page.
			if _, err := m.pollURL(updated); err != nil {
				return err
			}
		} else {
			updated.Ready = true
			updated.LastSync = time.Now().UTC().Format(time.RFC3339Nano)
		}
		if err := writeSnapshot(m.cfg.StatePath, updated); err != nil {
			return fmt.Errorf("persist NOTAC cursor and records: %w", err)
		}
		m.store.publish(updated)
		s = updated
		if updated.Next == "" {
			return nil
		}
		if remaining != "" {
			credits, err := strconv.Atoi(remaining)
			if err == nil && credits < 6 {
				return fmt.Errorf("NOTAC credits remaining %d; preserving continuation for next cycle", credits)
			}
		}
	}
	return fmt.Errorf("NOTAC per-run page cap (%d) reached; remaining pages are checkpointed for the next poll", m.cfg.MaxPages)
}

func containsLocation(locations []string, value string) bool {
	for _, loc := range locations {
		if strings.EqualFold(loc, value) {
			return true
		}
	}
	return false
}
