package net

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// The server mirrors published FAA geometry. It does not establish that a
	// TFR is currently active; operative times/altitudes require NOTAM details.
	PublicTFRGeoJSONURL = "https://reds-stdds-live.jjplatzer.com/tfr/v1/tfrs"
	TFRPollInterval     = time.Minute
	tfrMaxBytes         = 32 << 20
	tfrMaxFeatures      = 20000
)

// TFRGeoJSONURL can be overridden for development or local deployments.
func TFRGeoJSONURL() string {
	if v := strings.TrimSpace(os.Getenv("REDS_TFR_GEOJSON_URL")); v != "" {
		return v
	}
	return PublicTFRGeoJSONURL
}

// TFRSnapshot contains the *unaltered* FeatureCollection. Future STARS drawing
// code can parse its coordinates, including holes and multiple features with
// the same NOTAM_KEY or GeoJSON id. GeoJSON is detached from the client cache.
// The source may include future/expired TFRs and is not an active-TFR feed.
type TFRSnapshot struct {
	GeoJSON           []byte
	FeatureCount      int
	NOTAMCount        int
	FetchedAt         time.Time
	SourceLastSuccess time.Time
	SourceStale       bool
	Revision          uint64
}

type TFRClientStatus struct {
	Ready             bool
	FeatureCount      int
	NOTAMCount        int
	FetchedAt         time.Time
	SourceLastSuccess time.Time
	SourceStale       bool
	Revision          uint64
	LastError         error
}

// TFRClient is a per-pane, read-only, periodically refreshed HTTP cache.
// Fetching happens in a background goroutine; Snapshot never blocks on HTTP.
type TFRClient struct {
	url      string
	interval time.Duration
	http     *http.Client
	ctx      context.Context
	cancel   context.CancelFunc

	startOnce sync.Once
	closeOnce sync.Once
	refreshMu sync.Mutex
	mu        sync.RWMutex
	snapshot  TFRSnapshot
	ready     bool
	lastError error
}

// NewTFRClient accepts an HTTPS URL (or HTTP on loopback for local testing).
// Zero interval selects the default one-minute refresh cadence.
func NewTFRClient(rawURL string, interval time.Duration) (*TFRClient, error) {
	if strings.TrimSpace(rawURL) == "" {
		rawURL = TFRGeoJSONURL()
	}
	u, err := url.Parse(rawURL)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid TFR GeoJSON URL")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, errors.New("TFR client requires HTTPS except for loopback HTTP")
		}
	}
	if interval <= 0 {
		interval = TFRPollInterval
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &TFRClient{
		url: rawURL, interval: interval,
		http: &http.Client{Timeout: 15 * time.Second},
		ctx:  ctx, cancel: cancel,
	}, nil
}

func (c *TFRClient) Start() {
	if c == nil {
		return
	}
	c.startOnce.Do(func() { go c.run() })
}

func (c *TFRClient) Close() {
	if c != nil {
		c.closeOnce.Do(c.cancel)
	}
}

func (c *TFRClient) run() {
	if c.ctx.Err() != nil {
		return
	}
	_ = c.Refresh(c.ctx)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			_ = c.Refresh(c.ctx)
		}
	}
}

// Refresh fetches one snapshot and replaces the cache only after full
// validation. Failures retain the last good GeoJSON and update LastError.
func (c *TFRClient) Refresh(ctx context.Context) error {
	if c == nil {
		return errors.New("nil TFR client")
	}
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return c.fail(err)
	}
	req.Header.Set("Accept", "application/geo+json, application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return c.fail(fmt.Errorf("fetch TFR GeoJSON: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.fail(fmt.Errorf("TFR endpoint returned HTTP %d", resp.StatusCode))
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, tfrMaxBytes+1))
	if err != nil {
		return c.fail(fmt.Errorf("read TFR response: %w", err))
	}
	if len(payload) > tfrMaxBytes {
		return c.fail(errors.New("TFR response exceeds 32 MiB"))
	}
	features, notams, err := validateTFRGeoJSON(payload)
	if err != nil {
		return c.fail(err)
	}
	var sourceAt time.Time
	if s := strings.TrimSpace(resp.Header.Get("X-REDS-TFR-Last-Success")); s != "" {
		if sourceAt, err = time.Parse(time.RFC3339Nano, s); err != nil {
			return c.fail(fmt.Errorf("invalid TFR source timestamp: %w", err))
		}
	}
	c.mu.Lock()
	c.snapshot = TFRSnapshot{
		GeoJSON: payload, FeatureCount: features, NOTAMCount: notams,
		FetchedAt: time.Now().UTC(), SourceLastSuccess: sourceAt,
		SourceStale: strings.EqualFold(resp.Header.Get("X-REDS-TFR-Stale"), "true"),
		Revision:    c.snapshot.Revision + 1,
	}
	c.ready = true
	c.lastError = nil
	c.mu.Unlock()
	return nil
}

func (c *TFRClient) fail(err error) error {
	c.mu.Lock()
	c.lastError = err
	c.mu.Unlock()
	return err
}

// Snapshot returns a defensive copy so a renderer cannot mutate cached data.
func (c *TFRClient) Snapshot() (TFRSnapshot, bool) {
	if c == nil {
		return TFRSnapshot{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.ready {
		return TFRSnapshot{}, false
	}
	s := c.snapshot
	s.GeoJSON = bytes.Clone(s.GeoJSON)
	return s, true
}

func (c *TFRClient) Status() TFRClientStatus {
	if c == nil {
		return TFRClientStatus{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return TFRClientStatus{
		Ready: c.ready, FeatureCount: c.snapshot.FeatureCount,
		NOTAMCount: c.snapshot.NOTAMCount, FetchedAt: c.snapshot.FetchedAt,
		SourceLastSuccess: c.snapshot.SourceLastSuccess,
		SourceStale:       c.snapshot.SourceStale, Revision: c.snapshot.Revision,
		LastError: c.lastError,
	}
}

// validateTFRGeoJSON rejects truncated/error responses and implausible geometry.
// It never deduplicates features: the FAA legitimately repeats IDs and keys.
func validateTFRGeoJSON(raw []byte) (int, int, error) {
	var fc struct {
		Type     string            `json:"type"`
		Features []json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(raw, &fc); err != nil {
		return 0, 0, fmt.Errorf("invalid TFR GeoJSON: %w", err)
	}
	if fc.Type != "FeatureCollection" || len(fc.Features) == 0 || len(fc.Features) > tfrMaxFeatures {
		return 0, 0, errors.New("TFR response must be a nonempty GeoJSON FeatureCollection")
	}
	unique := make(map[string]struct{})
	for i, feature := range fc.Features {
		var f struct {
			Type       string          `json:"type"`
			Geometry   json.RawMessage `json:"geometry"`
			Properties struct {
				NOTAMKey string `json:"NOTAM_KEY"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(feature, &f); err != nil || f.Type != "Feature" || strings.TrimSpace(f.Properties.NOTAMKey) == "" {
			return 0, 0, fmt.Errorf("TFR feature %d has invalid type or NOTAM_KEY", i)
		}
		if err := validateTFRGeometry(f.Geometry); err != nil {
			return 0, 0, fmt.Errorf("TFR feature %d: %w", i, err)
		}
		unique[f.Properties.NOTAMKey] = struct{}{}
	}
	return len(fc.Features), len(unique), nil
}

func validateTFRGeometry(raw json.RawMessage) error {
	var g struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return fmt.Errorf("invalid geometry: %w", err)
	}
	switch g.Type {
	case "Polygon":
		var p [][][]float64
		if err := json.Unmarshal(g.Coordinates, &p); err != nil {
			return err
		}
		return validateTFRPolygon(p)
	case "MultiPolygon":
		var polys [][][][]float64
		if err := json.Unmarshal(g.Coordinates, &polys); err != nil {
			return err
		}
		if len(polys) == 0 {
			return errors.New("empty multipolygon")
		}
		for _, p := range polys {
			if err := validateTFRPolygon(p); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported TFR geometry %q", g.Type)
	}
}

func validateTFRPolygon(p [][][]float64) error {
	if len(p) == 0 {
		return errors.New("polygon has no rings")
	}
	for _, ring := range p {
		if len(ring) < 4 {
			return errors.New("polygon ring must have at least four points")
		}
		for _, point := range ring {
			if len(point) != 2 || math.IsNaN(point[0]) || math.IsNaN(point[1]) ||
				math.IsInf(point[0], 0) || math.IsInf(point[1], 0) ||
				point[0] < -180 || point[0] > 180 || point[1] < -90 || point[1] > 90 {
				return errors.New("invalid TFR longitude/latitude coordinate")
			}
		}
		if ring[0][0] != ring[len(ring)-1][0] || ring[0][1] != ring[len(ring)-1][1] {
			return errors.New("TFR polygon ring is not closed")
		}
	}
	return nil
}
