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
	MapGroups        []mapGroupConfig        `json:"mapGroups"`
	VideoMaps        []videoMapConfig        `json:"videoMaps"`
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

type mapGroupConfig struct {
	TCPs          []string `json:"tcps"`
	MapIDs        []*int   `json:"mapIds"`
	MainMapIDs    []*int   `json:"mainMapIds"`
	SubmenuMapIDs []*int   `json:"submenuMapIds"`
}

type videoMapConfig struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	ShortName          string `json:"shortName"`
	STARSID            int    `json:"starsId"`
	BrightnessCategory string `json:"brightnessCategory"`
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

// mainDCBMaps returns the six position-adapted video-map buttons in the
// row-major order stored by crc2reds. drawDCB applies STARS' top/bottom button
// traversal when laying the six entries into three half-height columns.
func (cfg selectedConfig) mainDCBMaps() [6]videoMapConfig {
	var out [6]videoMapConfig
	tcp := strings.TrimSpace(cfg.ControlPosition.TCP)
	if tcp == "" {
		return out
	}

	var ids []*int
	for _, group := range cfg.Facility.MapGroups {
		for _, candidate := range group.TCPs {
			if strings.EqualFold(strings.TrimSpace(candidate), tcp) {
				ids = group.MainMapIDs
				break
			}
		}
		if ids != nil {
			break
		}
	}
	if ids == nil {
		return out
	}

	byID := make(map[int]videoMapConfig, len(cfg.Facility.VideoMaps))
	for _, vm := range cfg.Facility.VideoMaps {
		if vm.STARSID != 0 {
			byID[vm.STARSID] = vm
		}
	}
	for i := 0; i < len(out) && i < len(ids); i++ {
		if ids[i] == nil {
			continue
		}
		out[i] = byID[*ids[i]]
	}
	return out
}

func (p *STARSPane) mainDCBMaps() [6]videoMapConfig {
	if p == nil {
		return [6]videoMapConfig{}
	}
	return p.config.mainDCBMaps()
}

// submenuDCBMaps returns the position-adapted MAPS-submenu video-map buttons.
// TI 6191.409 Rev. 30, Table 2-6 permits up to 32 map buttons. CRC's map-group
// adaptation stores the six Main-DCB map slots first, followed by those 32
// submenu slots in top/bottom column order. Older generated REDS configs only
// carried 30 transposed submenu IDs; keep that as a compatibility fallback.
func (cfg selectedConfig) submenuDCBMaps() [32]videoMapConfig {
	var out [32]videoMapConfig
	tcp := strings.TrimSpace(cfg.ControlPosition.TCP)
	if tcp == "" {
		return out
	}

	var group *mapGroupConfig
	for i := range cfg.Facility.MapGroups {
		g := &cfg.Facility.MapGroups[i]
		for _, candidate := range g.TCPs {
			if strings.EqualFold(strings.TrimSpace(candidate), tcp) {
				group = g
				break
			}
		}
		if group != nil {
			break
		}
	}
	if group == nil {
		return out
	}

	byID := make(map[int]videoMapConfig, len(cfg.Facility.VideoMaps))
	for _, vm := range cfg.Facility.VideoMaps {
		if vm.STARSID != 0 {
			byID[vm.STARSID] = vm
		}
	}

	// Prefer CRC's raw 38-slot adaptation (6 Main + 32 submenu) so current
	// configs retain the two slots that older crc2reds versions dropped.
	if len(group.MapIDs) > 6 {
		for col := 0; col < 16; col++ {
			for row := 0; row < 2; row++ {
				src := 6 + 2*col + row
				if src >= len(group.MapIDs) || group.MapIDs[src] == nil {
					continue
				}
				out[row*16+col] = byID[*group.MapIDs[src]]
			}
		}
		return out
	}

	for i := 0; i < len(out) && i < len(group.SubmenuMapIDs); i++ {
		if group.SubmenuMapIDs[i] != nil {
			out[i] = byID[*group.SubmenuMapIDs[i]]
		}
	}
	return out
}

func (p *STARSPane) submenuDCBMaps() [32]videoMapConfig {
	if p == nil {
		return [32]videoMapConfig{}
	}
	return p.config.submenuDCBMaps()
}
