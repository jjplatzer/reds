package stars

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/juliusplatzer/reds/util"
)

type configPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// facilityConfig contains the STARS adaptation needed by a selected TCW/TDW.
// Keep the JSON-facing types here so scope, list, map, and target rendering do
// not each decode the generated CRC facility config independently.
type facilityConfig struct {
	ARTCC            string                  `json:"artcc"`
	Facility         string                  `json:"facility"`
	Name             string                  `json:"name"`
	DefaultCenter    configPoint             `json:"defaultCenter"`
	Areas            []areaConfig            `json:"areas"`
	ControlPositions []controlPositionConfig `json:"controlPositions"`
}

type areaConfig struct {
	ID                 string      `json:"id"`
	Name               string      `json:"name"`
	VisibilityCenter   configPoint `json:"visibilityCenter"`
	SurveillanceRange  float32     `json:"surveillanceRange"`
	UnderlyingAirports []string    `json:"underlyingAirports"`
	SSAAirports        []string    `json:"ssaAirports"`
}

type controlPositionConfig struct {
	ID               string      `json:"id"`
	PhysicalFacility string      `json:"physicalFacility"`
	Name             string      `json:"name"`
	RadioName        string      `json:"radioName"`
	Callsign         string      `json:"callsign"`
	AreaID           string      `json:"areaId"`
	TCPID            string      `json:"tcpId"`
	TCP              string      `json:"tcp"`
	VisualCenter     configPoint `json:"visualCenter"`
	Range            float32     `json:"range"`
	Freq             float32     `json:"freq"`
}

type selectedConfig struct {
	Facility        facilityConfig
	ControlPosition controlPositionConfig
	Area            areaConfig
}

func loadSelectedConfig(artcc, tracon, positionID string) (selectedConfig, error) {
	artcc, err := normalizedSTARSResourceCode("ARTCC", artcc)
	if err != nil {
		return selectedConfig{}, err
	}
	tracon, err = normalizedSTARSResourceCode("TRACON", tracon)
	if err != nil {
		return selectedConfig{}, err
	}
	positionID = strings.TrimSpace(positionID)
	if positionID == "" {
		return selectedConfig{}, fmt.Errorf("STARS: empty position ID")
	}

	path := filepath.ToSlash(filepath.Join("resources", "configs", "stars", artcc, tracon+".json"))
	if !util.ResourceExists(path) {
		return selectedConfig{}, fmt.Errorf("STARS: facility config %s not found", path)
	}

	var cfg facilityConfig
	if err := json.Unmarshal(util.LoadResourceBytes(path), &cfg); err != nil {
		return selectedConfig{}, fmt.Errorf("STARS: decode %s: %w", path, err)
	}

	var selected controlPositionConfig
	found := false
	for _, position := range cfg.ControlPositions {
		if position.ID == positionID {
			selected = position
			found = true
			break
		}
	}
	if !found {
		return selectedConfig{}, fmt.Errorf("STARS: position %q not found in %s", positionID, path)
	}
	if selected.AreaID == "" {
		return selectedConfig{}, fmt.Errorf("STARS: position %q has no adapted area in %s", positionID, path)
	}

	for _, area := range cfg.Areas {
		if area.ID == selected.AreaID {
			return selectedConfig{
				Facility:        cfg,
				ControlPosition: selected,
				Area:            area,
			}, nil
		}
	}

	return selectedConfig{}, fmt.Errorf("STARS: position %q references unknown area %q", positionID, selected.AreaID)
}

// adaptedSystemAltimeterAirport resolves the best available system-altimeter
// station from the generated CRC position adaptation. The current REDS config
// does not yet carry CRC's explicit system-altimeter field, so prefer a real
// airport physical facility, then the airport prefix in the position callsign,
// and finally the area's adapted SSA-airport list.
func adaptedSystemAltimeterAirport(artcc, tracon, positionID string) (string, error) {
	cfg, err := loadSelectedConfig(artcc, tracon, positionID)
	if err != nil {
		return "", err
	}
	return cfg.systemAltimeterAirport(), nil
}

func (cfg selectedConfig) systemAltimeterAirport() string {
	if airport := adaptedAirportCode(cfg.ControlPosition.PhysicalFacility); airport != "" {
		return airport
	}
	if prefix, _, ok := strings.Cut(strings.TrimSpace(cfg.ControlPosition.Callsign), "_"); ok {
		if airport := adaptedAirportCode(prefix); airport != "" {
			return airport
		}
	}
	for _, airport := range cfg.Area.SSAAirports {
		if airport := adaptedAirportCode(airport); airport != "" {
			return airport
		}
	}
	return ""
}

func normalizedSTARSResourceCode(kind, code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return "", fmt.Errorf("STARS: empty %s", kind)
	}
	if strings.ContainsAny(code, `/\\`) || code == "." || code == ".." {
		return "", fmt.Errorf("STARS: invalid %s %q", kind, code)
	}
	return code, nil
}
