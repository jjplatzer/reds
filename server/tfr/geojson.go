package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// validateGeoJSON validates geometry without normalizing or deduplicating it.
// Several WFS features may intentionally share *both* an id and NOTAM_KEY.
// GeoJSON positions are [longitude, latitude] in EPSG:4326.
func validateGeoJSON(raw []byte, allowEmpty bool) (features, notams int, err error) {
	var fc struct {
		Type     string            `json:"type"`
		Features []json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(raw, &fc); err != nil {
		return 0, 0, fmt.Errorf("invalid GeoJSON: %w", err)
	}
	if fc.Type != "FeatureCollection" || fc.Features == nil {
		return 0, 0, errors.New("WFS response is not a GeoJSON FeatureCollection")
	}
	if len(fc.Features) == 0 && !allowEmpty {
		return 0, 0, errors.New("empty WFS FeatureCollection rejected to protect last good snapshot (TFR_ALLOW_EMPTY=true to permit)")
	}
	if len(fc.Features) > 20000 {
		return 0, 0, errors.New("WFS response exceeds 20000 features")
	}
	distinct := make(map[string]struct{}, len(fc.Features))
	for i, rawFeature := range fc.Features {
		var f struct {
			Type       string          `json:"type"`
			Geometry   json.RawMessage `json:"geometry"`
			Properties struct {
				NOTAMKey string `json:"NOTAM_KEY"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(rawFeature, &f); err != nil {
			return 0, 0, fmt.Errorf("feature %d: %w", i, err)
		}
		if f.Type != "Feature" {
			return 0, 0, fmt.Errorf("feature %d: invalid type", i)
		}
		key := strings.TrimSpace(f.Properties.NOTAMKey)
		if key == "" {
			return 0, 0, fmt.Errorf("feature %d: missing NOTAM_KEY", i)
		}
		if err := validateGeometry(f.Geometry); err != nil {
			return 0, 0, fmt.Errorf("feature %d (%s): %w", i, key, err)
		}
		distinct[key] = struct{}{}
	}
	return len(fc.Features), len(distinct), nil
}

func validateGeometry(raw json.RawMessage) error {
	var header struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return fmt.Errorf("invalid geometry: %w", err)
	}
	switch header.Type {
	case "Polygon":
		var rings [][][]float64
		if err := strictUnmarshal(header.Coordinates, &rings); err != nil {
			return fmt.Errorf("polygon coordinates: %w", err)
		}
		return validateRings(rings)
	case "MultiPolygon":
		var polys [][][][]float64
		if err := strictUnmarshal(header.Coordinates, &polys); err != nil {
			return fmt.Errorf("multipolygon coordinates: %w", err)
		}
		if len(polys) == 0 {
			return errors.New("empty multipolygon")
		}
		for i, rings := range polys {
			if err := validateRings(rings); err != nil {
				return fmt.Errorf("polygon %d: %w", i, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported geometry %q (expected Polygon or MultiPolygon)", header.Type)
	}
}

func strictUnmarshal(raw json.RawMessage, out any) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || len(raw) == 0 {
		return errors.New("missing coordinates")
	}
	return json.Unmarshal(raw, out)
}

func validateRings(rings [][][]float64) error {
	if len(rings) == 0 {
		return errors.New("polygon has no rings")
	}
	for j, ring := range rings {
		if len(ring) < 4 {
			return fmt.Errorf("ring %d has fewer than 4 positions", j)
		}
		for k, position := range ring {
			if len(position) != 2 || math.IsNaN(position[0]) || math.IsNaN(position[1]) ||
				math.IsInf(position[0], 0) || math.IsInf(position[1], 0) ||
				position[0] < -180 || position[0] > 180 || position[1] < -90 || position[1] > 90 {
				return fmt.Errorf("ring %d position %d is not a valid [lon,lat] pair", j, k)
			}
		}
		last := ring[len(ring)-1]
		if ring[0][0] != last[0] || ring[0][1] != last[1] {
			return fmt.Errorf("ring %d is not closed", j)
		}
	}
	return nil
}
