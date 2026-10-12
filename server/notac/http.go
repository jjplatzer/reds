package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

func jsonResponse(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func (m *mirror) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s := m.snapshot()
		code := http.StatusOK
		if !s.Ready {
			code = http.StatusServiceUnavailable
		}
		jsonResponse(w, code, map[string]any{
			"ready": s.Ready, "airports": s.Locations,
			"noticeCount": len(s.Records), "lastSync": s.LastSync,
			"asOf": s.APIAsOf, "pages": s.Pages,
		})
	})
	mux.HandleFunc("/v1/notams", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s := m.snapshot()
		if !s.Ready {
			jsonResponse(w, http.StatusServiceUnavailable, map[string]string{"error": "initial snapshot is not complete"})
			return
		}
		airport := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("airport")))
		if airport != "" && !containsLocation(m.cfg.Locations, airport) {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "airport is not in NOTAC_LOCATIONS"})
			return
		}
		now := time.Now().UTC()
		notices := make([]json.RawMessage, 0, len(s.Records))
		ids := make([]string, 0, len(s.Records))
		for id := range s.Records {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			raw := s.Records[id]
			if !isCurrent(raw, now) {
				continue
			}
			if airport != "" {
				var n noticeEnvelope
				if json.Unmarshal(raw, &n) != nil || !strings.EqualFold(airport, n.LocationCode) {
					continue
				}
			}
			notices = append(notices, raw)
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"source": "NOTAC", "airport": airport, "lastSync": s.LastSync,
			"asOf": s.APIAsOf, "count": len(notices), "results": notices,
		})
	})
	return mux
}
