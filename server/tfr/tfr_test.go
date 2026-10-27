package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleGeoJSON = `{"type":"FeatureCollection","features":[
{"type":"Feature","id":"V_TFR_LOC.6/1664","geometry":{"type":"Polygon","coordinates":[[[-74,40],[-73,40],[-73,41],[-74,40]]]},"properties":{"NOTAM_KEY":"6/1664-1-FDC-F","TITLE":"ONE"}},
{"type":"Feature","id":"V_TFR_LOC.6/1664","geometry":{"type":"Polygon","coordinates":[[[-74,40],[-72,40],[-73,41],[-74,40]]]},"properties":{"NOTAM_KEY":"6/1664-1-FDC-F","TITLE":"ONE"}},
{"type":"Feature","id":"V_TFR_LOC.6/0100","geometry":{"type":"MultiPolygon","coordinates":[[[[-80,25],[-79,25],[-79,26],[-80,25]]]]},"properties":{"NOTAM_KEY":"6/0100-1-FDC-F","TITLE":"TWO"}}
]}`

func newTestMirror(t *testing.T, handler http.HandlerFunc) (*mirror, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewServer(handler)
	cfg := config{
		SourceURL: upstream.URL + "/wfs", StatePath: filepath.Join(t.TempDir(), "cache", "state.json"),
		Interval: 10 * time.Minute,
	}
	m, err := newMirror(cfg)
	if err != nil {
		upstream.Close()
		t.Fatal(err)
	}
	t.Cleanup(upstream.Close)
	return m, upstream
}

func TestValidateGeoJSONPreservesComponentsAndHoles(t *testing.T) {
	count, notams, err := validateGeoJSON([]byte(sampleGeoJSON), false)
	if err != nil || count != 3 || notams != 2 {
		t.Fatalf("count=%d notams=%d err=%v", count, notams, err)
	}
	cases := []struct{ name, geojson string }{
		{"wfs error XML", `<ServiceExceptionReport>bad</ServiceExceptionReport>`},
		{"html error", `<html>no</html>`},
		{"empty", `{"type":"FeatureCollection","features":[]}`},
		{"no features", `{"type":"FeatureCollection"}`},
		{"unclosed", `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"NOTAM_KEY":"6/1"},"geometry":{"type":"Polygon","coordinates":[[[-73,40],[-74,40],[-74,41],[-73,41]]]}}]}`},
		{"bad latitude", `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"NOTAM_KEY":"6/1"},"geometry":{"type":"Polygon","coordinates":[[[-73,95],[-74,40],[-74,41],[-73,95]]]}}]}`},
		{"missing notam key", `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[-73,40],[-74,40],[-74,41],[-73,40]]]}}]}`},
		{"unsupported point", `{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"NOTAM_KEY":"6/1"},"geometry":{"type":"Point","coordinates":[-73,40]}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := validateGeoJSON([]byte(tc.geojson), false); err == nil {
				t.Fatal("expected invalid GeoJSON rejection")
			}
		})
	}
	if n, k, err := validateGeoJSON([]byte(`{"type":"FeatureCollection","features":[]}`), true); err != nil || n != 0 || k != 0 {
		t.Fatalf("explicitly allowed empty failed: %v", err)
	}
}

func TestSnapshotLifecycleAndFailureRetention(t *testing.T) {
	fail := false
	m, _ := newTestMirror(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wfs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if fail {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("bad response"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleGeoJSON))
	})
	req := httptest.NewRequest("GET", "/v1/tfrs", nil)
	initial := httptest.NewRecorder()
	m.handler().ServeHTTP(initial, req)
	if initial.Code != http.StatusServiceUnavailable {
		t.Fatalf("before sync: %d", initial.Code)
	}

	fixed := time.Date(2026, 10, 27, 18, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return fixed }
	if err := m.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := m.snapshot()
	if s.FeatureCount != 3 || s.NOTAMCount != 2 || !s.LastSuccess.Equal(fixed) {
		t.Fatalf("bad snapshot: %+v", s)
	}
	// Duplicate GeoJSON feature IDs/NOTAM keys are retained verbatim, not collapsed.
	served := httptest.NewRecorder()
	m.handler().ServeHTTP(served, req)
	if served.Code != http.StatusOK || !bytes.Equal(served.Body.Bytes(), []byte(sampleGeoJSON)) {
		t.Fatal("GeoJSON was not preserved exactly")
	}
	if served.Header().Get("X-REDS-TFR-Stale") != "false" {
		t.Fatal("fresh snapshot incorrectly stale")
	}
	var out struct {
		Features []json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(served.Body.Bytes(), &out); err != nil || len(out.Features) != 3 {
		t.Fatal("lost polygon components")
	}

	// Restart must recover persisted state before the first network request.
	m2, err := newMirror(m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	m2.now = func() time.Time { return fixed }
	if m2.snapshot().FeatureCount != 3 {
		t.Fatal("cache not restored")
	}

	fail = true
	m.now = func() time.Time { return fixed.Add(31 * time.Minute) }
	if err := m.sync(context.Background()); err == nil {
		t.Fatal("expected invalid refresh to fail")
	}
	if !bytes.Equal(m.snapshot().GeoJSON, []byte(sampleGeoJSON)) {
		t.Fatal("invalid refresh replaced cache")
	}
	stale := httptest.NewRecorder()
	m.handler().ServeHTTP(stale, req)
	if stale.Code != http.StatusOK || stale.Header().Get("X-REDS-TFR-Stale") != "true" {
		t.Fatal("stale snapshot not served with warning")
	}
	health := httptest.NewRecorder()
	m.handler().ServeHTTP(health, httptest.NewRequest("GET", "/healthz", nil))
	if health.Code != http.StatusServiceUnavailable {
		t.Fatal("stale health must be degraded")
	}
	status := httptest.NewRecorder()
	m.handler().ServeHTTP(status, httptest.NewRequest("GET", "/v1/status", nil))
	if !strings.Contains(status.Body.String(), `"geometryOnly":true`) {
		t.Fatal("geometry-only status missing")
	}
	if b, err := os.ReadFile(m.cfg.StatePath); err != nil || !bytes.Contains(b, []byte(`"featureCount":3`)) {
		t.Fatal("saved snapshot was corrupted")
	}
}

func TestInvalidSnapshotRefused(t *testing.T) {
	m, _ := newTestMirror(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(sampleGeoJSON)) })
	if err := m.sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	other := m.cfg
	other.SourceURL = "https://example.org/different"
	if _, err := newMirror(other); err == nil {
		t.Fatal("must not load data from a different source")
	}
	if err := os.WriteFile(m.cfg.StatePath, []byte("truncated"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newMirror(m.cfg); err == nil {
		t.Fatal("must not silently ignore corrupt saved snapshot")
	}
}

func TestRejectRedirectAndMethods(t *testing.T) {
	m, _ := newTestMirror(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.org/", http.StatusFound)
	})
	if err := m.sync(context.Background()); err == nil {
		t.Fatal("must reject redirect")
	}
	w := httptest.NewRecorder()
	m.handler().ServeHTTP(w, httptest.NewRequest("POST", "/v1/tfrs", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", w.Code)
	}
}

func TestConfigDefaults(t *testing.T) {
	for _, key := range []string{"TFR_SOURCE_URL", "TFR_STATE_PATH", "TFR_HTTP_ADDR", "TFR_POLL_INTERVAL", "TFR_ALLOW_EMPTY"} {
		t.Setenv(key, "")
	}
	cfg, err := configFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Interval != 10*time.Minute || cfg.HTTPAddr != "127.0.0.1:8790" || cfg.AllowEmpty {
		t.Fatalf("wrong defaults: %+v", cfg)
	}
	t.Setenv("TFR_POLL_INTERVAL", "15s")
	if _, err := configFromEnv(); err == nil {
		t.Fatal("must reject sub-minute polling")
	}
}
