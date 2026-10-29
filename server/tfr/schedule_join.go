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
	"sort"
	"strings"
	"sync"
)

const defaultDetailURL = "https://tfr.faa.gov/download/detail_%s.xml"

// Fetch XML only for drawable NOTAMs. An incomplete upstream refresh is not
// published: the last successful snapshot remains available, marked stale.
func (m *mirror) annotateSchedules(ctx context.Context, raw []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var features []map[string]json.RawMessage
	if err := json.Unmarshal(root["features"], &features); err != nil {
		return nil, err
	}
	if len(features) == 0 {
		return raw, nil
	}
	byID := map[string][]int{}
	for i, f := range features {
		var props struct {
			Key string `json:"NOTAM_KEY"`
		}
		if err := json.Unmarshal(f["properties"], &props); err != nil {
			return nil, err
		}
		byID[shortNOTAM(props.Key)] = append(byID[shortNOTAM(props.Key)], i)
	}
	if len(byID) > 200 {
		return nil, errors.New("too many TFR details in one refresh")
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	type result struct {
		id    string
		areas map[string]activation
		name  string
		err   error
	}
	results := make(chan result, len(ids))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results <- result{id: id, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			template := m.cfg.DetailURL
			if template == "" {
				template = defaultDetailURL
			}
			// Only the verified FAA short NOTAM ID is interpolated into the path.
			safe := strings.ReplaceAll(id, "/", "_")
			if !validDetailID(id) {
				results <- result{id: id, err: errors.New("invalid NOTAM ID")}
				return
			}
			endpoint := fmt.Sprintf(template, url.PathEscape(safe))
			req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
			if e != nil {
				results <- result{id: id, err: e}
				return
			}
			req.Header.Set("Accept", "application/xml, text/xml")
			resp, e := m.httpClient.Do(req)
			if e != nil {
				results <- result{id: id, err: e}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				results <- result{id: id, err: fmt.Errorf("detail HTTP %s", resp.Status)}
				return
			}
			content, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
			if e != nil {
				results <- result{id: id, err: e}
				return
			}
			if len(content) > 2<<20 {
				results <- result{id: id, err: errors.New("detail XML too large")}
				return
			}
			areas, e := extractAreaSchedules(content)
			if e != nil {
				results <- result{id: id, err: e}
				return
			}
			name, e := extractTFRLocalName(content)
			results <- result{id: id, areas: areas, name: name, err: e}
		}(id)
	}
	wg.Wait()
	close(results)
	details := map[string]map[string]activation{}
	localNames := map[string]string{}
	for r := range results {
		if r.err != nil {
			// Do not invent activation for an unavailable detail; publish
			// valid geometry as unknown when other NOTAMs were verified.
			log.Printf("[TFR] XML %s unavailable: %v (schedule unknown)", r.id, r.err)
			continue
		}
		details[r.id] = r.areas
		localNames[r.id] = r.name
	}
	matched := 0
	for id, indices := range byID {
		schedules := details[id]
		for _, i := range indices {
			var rawGeo json.RawMessage = features[i]["geometry"]
			keys, e := wfsGeometryKeys(rawGeo)
			if e != nil {
				return nil, e
			}
			var combined activation
			partSchedules := make([]activation, len(keys))
			for part, key := range keys {
				a, ok := schedules[key]
				if !ok && len(indices) == 1 && len(keys) == 1 {
					// Only a single feature/single area with no XML geometry
					// can be joined without risking an area-order assumption.
					a, ok = schedules["__single_without_geometry__"]
				}
				if !ok {
					continue
				}
				partSchedules[part] = a
				matched++
				combined.Known = true
				combined.Indefinite = combined.Indefinite || a.Indefinite
				combined.Intervals = append(combined.Intervals, a.Intervals...)
			}
			// An unmatched polygon remains explicitly unknown: never infer schedule
			// from a neighboring polygon, NOTAM's broad validity, or feature order.
			props := map[string]json.RawMessage{}
			if e := json.Unmarshal(features[i]["properties"], &props); e != nil {
				return nil, e
			}
			encoded, e := json.Marshal(combined)
			if e != nil {
				return nil, e
			}
			props["TFR_SCHEDULE"] = encoded
			partsRaw, e := json.Marshal(partSchedules)
			if e != nil {
				return nil, e
			}
			props["TFR_PART_SCHEDULES"] = partsRaw
			if name := localNames[id]; name != "" {
				// Keep the source NOTAM and geometry identifiers unchanged.
				nameJSON, e := json.Marshal(name)
				if e != nil {
					return nil, e
				}
				props["TFR_LOCAL_NAME"] = nameJSON
			}
			encoded, e = json.Marshal(props)
			if e != nil {
				return nil, e
			}
			features[i]["properties"] = encoded
		}
	}
	if matched == 0 {
		return nil, errors.New("no FAA XML areas matched WFS geometry; retain previous snapshot")
	}
	b, e := json.Marshal(features)
	if e != nil {
		return nil, e
	}
	root["features"] = b
	return json.Marshal(root)
}
func validDetailID(id string) bool {
	parts := strings.Split(id, "/")
	if len(parts) != 2 || len(parts[0]) != 1 || len(parts[1]) != 4 {
		return false
	}
	for _, p := range parts {
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}
