package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// FAA exportTfrList includes the ARTCC facility, which is absent from WFS.
// Preserve WFS geometry and properties and add an explicit facility field.
func shortNOTAM(key string) string {
	key = strings.TrimSpace(key)
	if i := strings.IndexByte(key, '-'); i >= 0 {
		return key[:i]
	}
	return key
}

func decodeTFRFacilities(raw []byte) (map[string]string, error) {
	var rows []struct {
		NOTAM    string `json:"notam_id"`
		Facility string `json:"facility"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode FAA TFR facility list: %w", err)
	}
	if rows == nil {
		return nil, errors.New("FAA TFR facility list must be an array")
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		key := shortNOTAM(r.NOTAM)
		facility := strings.ToUpper(strings.TrimSpace(r.Facility))
		if key == "" || len(facility) != 3 || facility[0] != 'Z' {
			continue
		}
		if prev, exists := out[key]; exists && prev != facility {
			// An ambiguous NOTAM must not silently be assigned to an ARTCC.
			delete(out, key)
			continue
		}
		out[key] = facility
	}
	return out, nil
}

func annotateTFRFacilities(raw []byte, facilities map[string]string) ([]byte, error) {
	var fc struct {
		Type     string `json:"type"`
		Features []struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &fc); err != nil {
		return nil, err
	}
	if fc.Type != "FeatureCollection" {
		return nil, errors.New("invalid WFS FeatureCollection")
	}
	// Work on a raw-message map to preserve the geometry and all other fields.
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var features []map[string]json.RawMessage
	if err := json.Unmarshal(root["features"], &features); err != nil {
		return nil, err
	}
	matched := 0
	for _, f := range features {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(f["properties"], &props); err != nil {
			return nil, err
		}
		var key string
		if err := json.Unmarshal(props["NOTAM_KEY"], &key); err != nil {
			return nil, err
		}
		if artcc, ok := facilities[shortNOTAM(key)]; ok {
			b, _ := json.Marshal(artcc)
			props["ARTCC"] = b
			matched++
		} else {
			delete(props, "ARTCC") // prevent stale/inconsistent metadata
		}
		b, err := json.Marshal(props)
		if err != nil {
			return nil, err
		}
		f["properties"] = b
	}
	if len(features) > 0 && matched == 0 {
		return nil, errors.New("FAA TFR facility list matched no WFS features; retaining previous snapshot")
	}
	b, err := json.Marshal(features)
	if err != nil {
		return nil, err
	}
	root["features"] = b
	return json.Marshal(root)
}
