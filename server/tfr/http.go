package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"
)

type statusResponse struct {
	Ready        bool       `json:"ready"`
	Stale        bool       `json:"stale"`
	Source       string     `json:"source"`
	LastSuccess  *time.Time `json:"lastSuccess"`
	FeatureCount int        `json:"featureCount"`
	NOTAMCount   int        `json:"notamCount"`
	PollInterval string     `json:"pollInterval"`
	GeometryOnly bool       `json:"geometryOnly"`
}

func (m *mirror) status(s snapshotFile) statusResponse {
	ready := !s.LastSuccess.IsZero() && len(s.GeoJSON) != 0
	stale := !ready || m.now().Sub(s.LastSuccess) > 3*m.cfg.Interval
	r := statusResponse{
		Ready: ready, Stale: stale,
		Source: m.cfg.SourceURL, FeatureCount: s.FeatureCount,
		NOTAMCount: s.NOTAMCount, PollInterval: m.cfg.Interval.String(),
		GeometryOnly: !bytes.Contains(s.GeoJSON, []byte(`"TFR_PART_SCHEDULES"`)),
	}
	if ready {
		t := s.LastSuccess
		r.LastSuccess = &t
	}
	return r
}

func writeJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

func (m *mirror) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s := m.status(m.snapshot())
		code := http.StatusOK
		if !s.Ready || s.Stale {
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, code, s)
	})
	mux.HandleFunc("/v1/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, m.status(m.snapshot()))
	})
	mux.HandleFunc("/v1/tfrs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s := m.snapshot()
		status := m.status(s)
		if !status.Ready {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no validated FAA TFR snapshot available"})
			return
		}
		w.Header().Set("Content-Type", "application/geo+json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-REDS-TFR-Last-Success", s.LastSuccess.Format(time.RFC3339))
		if status.Stale {
			w.Header().Set("X-REDS-TFR-Stale", "true")
		} else {
			w.Header().Set("X-REDS-TFR-Stale", "false")
		}
		// Serve stale-but-valid geometry rather than inventing an empty dataset.
		// Consumers must inspect the stale flag and must not label it "active".
		_, _ = w.Write(s.GeoJSON)
	})
	return mux
}
