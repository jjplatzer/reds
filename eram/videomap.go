package eram

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/juliusplatzer/reds/util"
)

const (
	eramMapFilterCount        = 40
	eramMapBCGCount           = 40
	defaultMapGroupBrightness = 12
)

type eramMapPackage struct {
	ARTCC     string         `json:"artcc"`
	GeoMaps   []eramGeoMap   `json:"geoMaps"`
	VideoMaps []eramVideoMap `json:"videoMaps"`
}

type eramGeoMap struct {
	Name        string          `json:"name"`
	LabelLine1  string          `json:"labelLine1"`
	LabelLine2  string          `json:"labelLine2"`
	FilterMenu  []eramMapFilter `json:"filterMenu"`
	BCGMenu     []string        `json:"bcgMenu"`
	VideoMapIDs []string        `json:"videoMapIds"`
}

type eramMapFilter struct {
	LabelLine1 string `json:"labelLine1"`
	LabelLine2 string `json:"labelLine2"`
}

type eramVideoMap struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	TDMOnly bool            `json:"tdmOnly"`
	GeoJSON json.RawMessage `json:"geojson"`
}

type eramMapState struct {
	geoMaps      []eramGeoMap
	activeGeoMap int

	// Bit 0 = CRC filter 1, ..., bit 39 = CRC filter 40.
	filters uint64

	// CRC defaults MapGroup1 through MapGroup40 to 12.
	brightness [eramMapBCGCount]int
}

func eramMapResourcePath(artcc string) string {
	return filepath.ToSlash(filepath.Join(
		"resources",
		"videomaps",
		"eram",
		strings.ToUpper(strings.TrimSpace(artcc))+".json.zst",
	))
}

func (p *ERAMPane) initializeMapState() {
	if p == nil {
		return
	}
	p.maps.activeGeoMap = -1
	for i := range p.maps.brightness {
		p.maps.brightness[i] = defaultMapGroupBrightness
	}
}

func (p *ERAMPane) loadGeoMapMetadata() error {
	if p == nil {
		return nil
	}

	path := eramMapResourcePath(p.artcc)
	r := util.LoadResource(path)
	defer r.Close()

	artcc, geoMaps, err := decodeERAMGeoMapMetadata(json.NewDecoder(r))
	if err != nil {
		return fmt.Errorf("decode ERAM map package %s: %w", path, err)
	}
	if artcc != "" && !strings.EqualFold(artcc, p.artcc) {
		return fmt.Errorf("ERAM map package %s is for %s, expected %s", path, artcc, p.artcc)
	}

	p.maps.geoMaps = append(p.maps.geoMaps[:0], geoMaps...)
	if len(p.maps.geoMaps) > 0 {
		p.maps.activeGeoMap = 0
	} else {
		p.maps.activeGeoMap = -1
	}

	return nil
}

func decodeERAMGeoMapMetadata(dec *json.Decoder) (string, []eramGeoMap, error) {
	token, err := dec.Token()
	if err != nil {
		return "", nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return "", nil, fmt.Errorf("expected top-level object")
	}

	var artcc string
	var geoMaps []eramGeoMap

	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return "", nil, err
		}
		key, ok := token.(string)
		if !ok {
			return "", nil, fmt.Errorf("expected object key")
		}

		switch key {
		case "artcc":
			if err := dec.Decode(&artcc); err != nil {
				return "", nil, err
			}
		case "geoMaps":
			if err := dec.Decode(&geoMaps); err != nil {
				return "", nil, err
			}
		default:
			if err := skipJSONValue(dec); err != nil {
				return "", nil, err
			}
		}
	}

	token, err = dec.Token()
	if err != nil {
		return "", nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '}' {
		return "", nil, fmt.Errorf("expected end of top-level object")
	}

	return artcc, geoMaps, nil
}

func skipJSONValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}

	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		for dec.More() {
			if _, err := dec.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err

	case '[':
		for dec.More() {
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	}

	return nil
}

func (p *ERAMPane) activeGeoMap() *eramGeoMap {
	if p == nil ||
		p.maps.activeGeoMap < 0 ||
		p.maps.activeGeoMap >= len(p.maps.geoMaps) {
		return nil
	}

	return &p.maps.geoMaps[p.maps.activeGeoMap]
}

func mapFilterButtonIndex(id toolbarButtonID) (int, bool) {
	const prefix = "map-filter-"

	value := string(id)
	if !strings.HasPrefix(value, prefix) {
		return 0, false
	}

	n, err := strconv.Atoi(strings.TrimPrefix(value, prefix))
	if err != nil || n < 1 || n > eramMapFilterCount {
		return 0, false
	}

	return n - 1, true
}

func (p *ERAMPane) toggleMapFilter(index int) {
	if p == nil || index < 0 || index >= eramMapFilterCount {
		return
	}

	p.maps.filters ^= uint64(1) << uint(index)
}
