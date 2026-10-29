package main

import (
	"bytes"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"
)

// Embed the compact U.S. UN/LOCODE city/state/code table in the server binary:
// no external data file is needed on Lightsail.
//
//go:embed data/unlocode_us.csv
var unlocodeData embed.FS

var (
	usLocationCodesOnce sync.Once
	usLocationCodes     map[string]string
	usLocationCodesErr  error
)

func normalizeTFRLocation(s string) string {
	// Keep letters and digits, but don't let punctuation differences cause
	// avoidable misses (e.g. "ST. LOUIS" vs "ST LOUIS").
	return strings.Join(strings.FieldsFunc(strings.ToUpper(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}

func tfrLocationKey(city, state string) string {
	return normalizeTFRLocation(city) + "\x00" + normalizeTFRLocation(state)
}

// parseUSLocationCodes accepts only unambiguous (city,state) -> LOCODE
// mappings. Duplicate rows with the same code are harmless. If multiple
// distinct codes share a city/state, none is selected.
func parseUSLocationCodes(raw []byte) (map[string]string, error) {
	r := csv.NewReader(bytes.NewReader(raw))
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read UN/LOCODE header: %w", err)
	}
	if len(header) != 3 || strings.TrimPrefix(strings.ToLower(strings.TrimSpace(header[0])), "\ufeff") != "city" ||
		strings.ToLower(strings.TrimSpace(header[1])) != "state" || strings.ToLower(strings.TrimSpace(header[2])) != "code" {
		return nil, fmt.Errorf("unexpected UN/LOCODE header: %v", header)
	}
	codes := make(map[string]string)
	ambiguous := make(map[string]struct{})
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read UN/LOCODE row: %w", err)
		}
		if len(row) != 3 {
			return nil, fmt.Errorf("UN/LOCODE row has %d columns, expected 3", len(row))
		}
		city, state, code := row[0], row[1], strings.ToUpper(strings.TrimSpace(row[2]))
		if normalizeTFRLocation(city) == "" || normalizeTFRLocation(state) == "" || len(code) != 3 {
			continue
		}
		key := tfrLocationKey(city, state)
		if _, exists := ambiguous[key]; exists {
			continue
		}
		if previous, exists := codes[key]; exists && previous != code {
			delete(codes, key)
			ambiguous[key] = struct{}{}
			continue
		}
		codes[key] = code
	}
	return codes, nil
}

func lookupTFRCityCode(city, state string) (string, error) {
	usLocationCodesOnce.Do(func() {
		data, err := unlocodeData.ReadFile("data/unlocode_us.csv")
		if err != nil {
			usLocationCodesErr = err
			return
		}
		usLocationCodes, usLocationCodesErr = parseUSLocationCodes(data)
	})
	if usLocationCodesErr != nil {
		return "", usLocationCodesErr
	}
	if city == "" || state == "" {
		return "", nil
	}
	return usLocationCodes[tfrLocationKey(city, state)], nil
}

// Add only the derived city code. Preserve the authoritative FAA city/state,
// original WFS properties and all activation schedules verbatim.
func annotateTFRCityCodes(raw []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var features []map[string]json.RawMessage
	if err := json.Unmarshal(root["features"], &features); err != nil {
		return nil, err
	}
	for _, feature := range features {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(feature["properties"], &props); err != nil {
			return nil, err
		}
		delete(props, "TFR_CITY_CODE") // don't trust an upstream/stale derived field
		var city, state string
		if v := props["TFR_CITY"]; len(v) > 0 {
			if err := json.Unmarshal(v, &city); err != nil {
				return nil, fmt.Errorf("decode TFR_CITY: %w", err)
			}
		}
		if v := props["TFR_STATE"]; len(v) > 0 {
			if err := json.Unmarshal(v, &state); err != nil {
				return nil, fmt.Errorf("decode TFR_STATE: %w", err)
			}
		}
		code, err := lookupTFRCityCode(city, state)
		if err != nil {
			return nil, fmt.Errorf("embedded UN/LOCODE table: %w", err)
		}
		if code != "" {
			encoded, _ := json.Marshal(code) // a string cannot fail to marshal
			props["TFR_CITY_CODE"] = encoded
		}
		feature["properties"], err = json.Marshal(props)
		if err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(features)
	if err != nil {
		return nil, err
	}
	root["features"] = encoded
	return json.Marshal(root)
}
