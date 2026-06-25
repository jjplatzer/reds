package net

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type PlaybackClient struct {
	baseURL string
	http    *http.Client
}

func NewPlaybackClient(baseURL string) *PlaybackClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = PlaybackBaseURL()
	}

	return &PlaybackClient{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *PlaybackClient) Bootstrap(
	ctx context.Context,
	airport string,
	at time.Time,
) (PlaybackBootstrapResponse, error) {
	var out PlaybackBootstrapResponse
	if c == nil {
		return out, fmt.Errorf("playback bootstrap: nil client")
	}

	endpoint, err := c.endpoint("bootstrap", url.Values{
		"airport": []string{strings.ToUpper(strings.TrimSpace(airport))},
		"at":      []string{at.UTC().Format(time.RFC3339Nano)},
	})
	if err != nil {
		return out, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return out, fmt.Errorf("playback bootstrap: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("playback bootstrap: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return out, fmt.Errorf(
			"playback bootstrap: HTTP %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("playback bootstrap: decode: %w", err)
	}

	return out, nil
}

func (c *PlaybackClient) Range(
	ctx context.Context,
	airport string,
	from time.Time,
	to time.Time,
) ([]SmesFrame, error) {
	if c == nil {
		return nil, fmt.Errorf("playback range: nil client")
	}

	endpoint, err := c.endpoint("range", url.Values{
		"airport": []string{strings.ToUpper(strings.TrimSpace(airport))},
		"from":    []string{from.UTC().Format(time.RFC3339Nano)},
		"to":      []string{to.UTC().Format(time.RFC3339Nano)},
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("playback range: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("playback range: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf(
			"playback range: HTTP %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	var frames []SmesFrame
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var frame SmesFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue
		}
		if frame.Key == "" {
			continue
		}

		frames = append(frames, frame)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("playback range: read: %w", err)
	}

	return frames, nil
}

func (c *PlaybackClient) endpoint(path string, query url.Values) (string, error) {
	if c == nil || c.baseURL == "" {
		return "", fmt.Errorf("playback: empty base URL")
	}

	u, err := url.Parse(c.baseURL + "/" + strings.TrimLeft(path, "/"))
	if err != nil {
		return "", fmt.Errorf("playback: bad URL: %w", err)
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}
