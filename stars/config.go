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
	// §6.6.12 is available only when the facility has an explicit DoD-Only
	// SITE rule. This is deliberately independent of a position color set.
	DoDOnly          bool                    `json:"dodOnly"`
	ARTCC            string                  `json:"artcc"`
	Facility         string                  `json:"facility"`
	Name             string                  `json:"name"`
	DefaultCenter    configPoint             `json:"defaultCenter"`
	Areas            []areaConfig            `json:"areas"`
	ControlPositions []controlPositionConfig `json:"controlPositions"`
	ATPAVolumes      []atpaVolumeConfig      `json:"atpaVolumes"`
	RPCs             []rpcConfig             `json:"rpcs"`
	MapGroups        []mapGroupConfig        `json:"mapGroups"`
	VideoMaps        []videoMapConfig        `json:"videoMaps"`
}

// atpaVolumeConfig is the CRC/vNAS STARS ATPA approach-volume adaptation
// exported by crc2reds. Widths are feet, Length is nautical miles, and the
// heading is magnetic, matching the vNAS facility model. Enum-valued fields
// remain raw JSON so REDS accepts both the string and numeric encodings seen
// in CRC/vNAS data.
type atpaVolumeConfig struct {
	ID                           string                 `json:"id"`
	AirportID                    string                 `json:"airportId"`
	VolumeID                     string                 `json:"volumeId"`
	Name                         string                 `json:"name"`
	RunwayThreshold              configPoint            `json:"runwayThreshold"`
	Ceiling                      int                    `json:"ceiling"`
	Floor                        int                    `json:"floor"`
	MagneticHeading              int                    `json:"magneticHeading"`
	MaximumHeadingDeviation      int                    `json:"maximumHeadingDeviation"`
	Length                       float64                `json:"length"`
	WidthLeft                    int                    `json:"widthLeft"`
	WidthRight                   int                    `json:"widthRight"`
	TwoPointFiveApproachDistance *float64               `json:"twoPointFiveApproachDistance"`
	TwoPointFiveApproachEnabled  bool                   `json:"twoPointFiveApproachEnabled"`
	Scratchpads                  []atpaScratchpadConfig `json:"scratchpads"`
	TCPs                         []atpaTCPDisplayConfig `json:"tcps"`
	ExcludedTCPIDs               []string               `json:"excludedTcpIds"`
}

// rpcConfig mirrors the CRC/vNAS Runway Pair Configuration (RPC) adaptation.
// The ordered slice of RPCs is not sorted: CRC references entries by position.
// CRDA activation, modes, and force-ghost state are runtime preferences, not
// parts of the facility adaptation.
type rpcConfig struct {
	ID                    string          `json:"id"`
	Index                 int             `json:"index"`
	AirportID             string          `json:"airportId"`
	PositionSymbolTie     string          `json:"positionSymbolTie"`
	PositionSymbolStagger string          `json:"positionSymbolStagger"`
	MasterRunway          rpcRunwayConfig `json:"masterRunway"`
	SlaveRunway           rpcRunwayConfig `json:"slaveRunway"`
}

// Widths, distances and headings stay in CRC adaptation units. Converting
// them for calculations is the responsibility of the future CRDA engine.
// Preserve direction as raw JSON: CRC/vNAS uses both numeric and string enums.
type rpcRunwayConfig struct {
	RunwayID                     string          `json:"runwayId"`
	HeadingTolerance             int             `json:"headingTolerance"`
	NearSideHalfWidth            float64         `json:"nearSideHalfWidth"`
	FarSideHalfWidth             float64         `json:"farSideHalfWidth"`
	NearSideDistance             float64         `json:"nearSideDistance"`
	RegionLength                 float64         `json:"regionLength"`
	TargetReferencePoint         configPoint     `json:"targetReferencePoint"`
	TargetReferenceLineHeading   float64         `json:"targetReferenceLineHeading"`
	TargetReferenceLineLength    float64         `json:"targetReferenceLineLength"`
	TargetReferencePointAltitude int             `json:"targetReferencePointAltitude"`
	ImageReferencePoint          configPoint     `json:"imageReferencePoint"`
	ImageReferenceLineHeading    float64         `json:"imageReferenceLineHeading"`
	ImageReferenceLineLength     float64         `json:"imageReferenceLineLength"`
	TieModeOffset                float64         `json:"tieModeOffset"`
	DescentPointDistance         float64         `json:"descentPointDistance"`
	DescentPointAltitude         int             `json:"descentPointAltitude"`
	AbovePathTolerance           int             `json:"abovePathTolerance"`
	BelowPathTolerance           int             `json:"belowPathTolerance"`
	DefaultLeaderDirection       json.RawMessage `json:"defaultLeaderDirection"`
	ScratchpadPatterns           []string        `json:"scratchpadPatterns"`
	SecondaryScratchpadPatterns  []string        `json:"secondaryScratchpadPatterns"`
}

type atpaTCPDisplayConfig struct {
	ID       string          `json:"id"`
	TCPID    string          `json:"tcpId"`
	TCP      string          `json:"tcp"`
	ConeType json.RawMessage `json:"coneType"`
}

type atpaScratchpadConfig struct {
	Entry            string          `json:"entry"`
	ScratchPadNumber json.RawMessage `json:"scratchPadNumber"`
	Type             json.RawMessage `json:"type"`
}

type areaConfig struct {
	ID                 string      `json:"id"`
	Name               string      `json:"name"`
	VisibilityCenter   configPoint `json:"visibilityCenter"`
	SurveillanceRange  float32     `json:"surveillanceRange"`
	UnderlyingAirports []string    `json:"underlyingAirports"`
	SSAAirports        []string    `json:"ssaAirports"`
	// Nil means an older generated configuration. CRC's StarsArea constructor
	// defaults this setting to true; preserve that for existing resources.
	UseVfrPositionSymbol *bool `json:"useVfrPositionSymbol"`
}

func (a areaConfig) vfrPositionSymbolEnabled() bool {
	return a.UseVfrPositionSymbol == nil || *a.UseVfrPositionSymbol
}

type controlPositionConfig struct {
	ID               string      `json:"id"`
	PhysicalFacility string      `json:"physicalFacility"`
	Name             string      `json:"name"`
	RadioName        string      `json:"radioName"`
	Callsign         string      `json:"callsign"`
	ColorSet         string      `json:"colorSet"`
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
