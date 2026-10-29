package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The FAA national TFR list supplies the NOTAM type and two-letter state.
// They are deliberately separate from its long, time-dependent description.
type tfrCategoryLocation struct {
	Category string
	State    string
}

func decodeTFRCategories(raw []byte) (map[string]tfrCategoryLocation, error) {
	var rows []struct {
		NOTAM    string `json:"notam_id"`
		Category string `json:"type"`
		State    string `json:"state"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode FAA TFR categories: %w", err)
	}
	if rows == nil {
		return nil, fmt.Errorf("FAA TFR category list must be an array")
	}
	out := make(map[string]tfrCategoryLocation, len(rows))
	for _, r := range rows {
		id := shortNOTAM(r.NOTAM)
		if id == "" {
			continue
		}
		value := tfrCategoryLocation{
			Category: strings.ToUpper(strings.Join(strings.Fields(r.Category), " ")),
			State:    strings.ToUpper(strings.TrimSpace(r.State)),
		}
		// In the unlikely event of inconsistent duplicate records, don't
		// choose a random classification based on upstream ordering.
		if old, exists := out[id]; exists && old != value {
			out[id] = tfrCategoryLocation{}
			continue
		}
		out[id] = value
	}
	return out, nil
}

// Append classification to the geometry without touching schedules, source
// properties, coordinates or the existing ARTCC assignment.
func annotateTFRCategories(raw []byte, categories map[string]tfrCategoryLocation) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var features []map[string]json.RawMessage
	if err := json.Unmarshal(root["features"], &features); err != nil {
		return nil, err
	}
	for _, f := range features {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(f["properties"], &props); err != nil {
			return nil, err
		}
		var key string
		if err := json.Unmarshal(props["NOTAM_KEY"], &key); err != nil {
			return nil, err
		}
		info := categories[shortNOTAM(key)]
		delete(props, "TFR_CATEGORY")
		delete(props, "TFR_STATE")
		if info.Category != "" {
			b, err := json.Marshal(info.Category)
			if err != nil {
				return nil, err
			}
			props["TFR_CATEGORY"] = b
		}
		if info.State != "" {
			b, err := json.Marshal(info.State)
			if err != nil {
				return nil, err
			}
			props["TFR_STATE"] = b
		}
		b, err := json.Marshal(props)
		if err != nil {
			return nil, err
		}
		f["properties"] = b
	}
	b, err := json.Marshal(features)
	if err != nil {
		return nil, err
	}
	root["features"] = b
	return json.Marshal(root)
}
