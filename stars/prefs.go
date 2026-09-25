package stars

import (
	stdmath "math"

	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	defaultSTARSRange = float32(50)
	minimumSTARSRange = float32(6)

	// TI 6191.409 Rev. 30, 4.4.1 Change display range: the keyboard range is
	// limited to the maximum adapted value, normally 512 nmi for a TCW and
	// 64 nmi for a TDW. REDS currently instantiates a TCW, so use the normal
	// TCW maximum until TDW selection/adaptation is introduced.
	maximumTCWRange = float32(512)
)

// Brightness is the STARS 0-100 illumination factor used by the BRITE
// submenu. TI 6191.409 Rev. 30, 4.10 defines brightness as an illumination
// factor; VICE applies it as a linear scale to the base display color.
type Brightness int

func (b Brightness) ScaleRGB(c renderer.RGB) renderer.RGB {
	s := float32(b) / 100
	return renderer.RGB{R: c.R * s, G: c.G * s, B: c.B * s}
}

// BrightnessPreferences mirrors the standard BRITE submenu categories in
// TI 6191.409 Rev. 30, Figure 4-13 / Table 4-1.
type BrightnessPreferences struct {
	DCB                Brightness
	BackgroundContrast Brightness
	VideoGroupA        Brightness
	VideoGroupB        Brightness
	FullDatablocks     Brightness
	Lists              Brightness
	Positions          Brightness
	LimitedDatablocks  Brightness
	OtherTracks        Brightness
	Lines              Brightness
	RangeRings         Brightness
	Compass            Brightness
	BeaconSymbols      Brightness
	PrimarySymbols     Brightness
	History            Brightness
	Weather            Brightness
	WxContrast         Brightness
}

// videoMapsListSelection identifies the reference-only map category list
// selected from the MAPS submenu. TI 6191.409 Rev. 30, 4.5.2 defines GEO MAPS,
// SYS PROC, AIRPORT, and CURRENT as site-adaptable category-list buttons. REDS
// currently implements the two categories that can be derived without extra
// adaptation metadata: all geographic video maps and the currently displayed
// maps.
type videoMapsListSelection uint8

const (
	videoMapsListGeographic videoMapsListSelection = iota
	videoMapsListCurrent
)

type videoMapsListPreferences struct {
	Position  [2]float32
	Visible   bool
	Selection videoMapsListSelection
}

// leaderLineDirection uses the clockwise ordering shown by the LDR DIR
// adjustment procedure in TI 6191.409 Rev. 30, 4.14.5.
type leaderLineDirection uint8

const (
	leaderLineDirectionNorth leaderLineDirection = iota
	leaderLineDirectionNorthEast
	leaderLineDirectionEast
	leaderLineDirectionSouthEast
	leaderLineDirectionSouth
	leaderLineDirectionSouthWest
	leaderLineDirectionWest
	leaderLineDirectionNorthWest
)

func (d leaderLineDirection) String() string {
	switch d {
	case leaderLineDirectionNorth:
		return "N"
	case leaderLineDirectionNorthEast:
		return "NE"
	case leaderLineDirectionEast:
		return "E"
	case leaderLineDirectionSouthEast:
		return "SE"
	case leaderLineDirectionSouth:
		return "S"
	case leaderLineDirectionSouthWest:
		return "SW"
	case leaderLineDirectionWest:
		return "W"
	case leaderLineDirectionNorthWest:
		return "NW"
	default:
		return "N"
	}
}

// step returns the adjacent orientation. Positive steps move clockwise, which
// is the trackball-forward ordering specified by TI 6191.409 Rev. 30, 4.14.5:
// N, NE, E, SE, S, SW, W, NW.
func (d leaderLineDirection) step(delta int) leaderLineDirection {
	if delta > 0 {
		return leaderLineDirection((int(d) + 1) % 8)
	}
	if delta < 0 {
		return leaderLineDirection((int(d) + 7) % 8)
	}
	return d
}

func leaderLineDirectionFromKeypad(key int) (leaderLineDirection, bool) {
	switch key {
	case 8:
		return leaderLineDirectionNorth, true
	case 9:
		return leaderLineDirectionNorthEast, true
	case 6:
		return leaderLineDirectionEast, true
	case 3:
		return leaderLineDirectionSouthEast, true
	case 2:
		return leaderLineDirectionSouth, true
	case 1:
		return leaderLineDirectionSouthWest, true
	case 4:
		return leaderLineDirectionWest, true
	case 7:
		return leaderLineDirectionNorthWest, true
	default:
		return leaderLineDirectionNorth, false
	}
}

// Preferences contains the per-position STARS display state that will later
// be saved/restored by STARS preference sets. The names mirror VICE's STARS
// Preferences so DCB commands and saved preference sets can use the same state.
type Preferences struct {
	DefaultCenter           configPoint
	UserCenter              configPoint
	UseUserCenter           bool
	Range                   float32
	RangeRingRadius         float32
	RangeRingsUserCenter    configPoint
	UseUserRangeRingsCenter bool
	LeaderLineDirection     leaderLineDirection
	LeaderLineLength        int
	Brightness              BrightnessPreferences
	DisplayWeatherLevel     [6]bool
	VideoMapVisible         map[int]bool
	VideoMapsList           videoMapsListPreferences
	PreviewAreaPosition     [2]float32

	// SelectedBeacons contains Mode 3/A codes or two-digit beacon-code banks
	// selected for enhanced unassociated-track presentation. TI 6191.409
	// section 6.13.11 defines this as an operator selection. Until REDS exposes
	// that command, start with 1200 selected so unassociated valid-Mode-C
	// tracks squawking 1200 get the selected-code position-symbol presentation.
	SelectedBeacons []string
}

func newPreferences(cfg selectedConfig) Preferences {
	center := initialSTARSCenter(cfg)
	prefs := Preferences{
		DefaultCenter:        center,
		UserCenter:           center,
		Range:                initialSTARSRange(cfg),
		RangeRingRadius:      5,
		RangeRingsUserCenter: center,
		// TI 6191.409 4.14.5 defines the eight legal orientations but does
		// not prescribe a startup value. VICE/STARS defaults to north.
		LeaderLineDirection: leaderLineDirectionNorth,
		// TI 6191.409 4.14.3 defines eight selectable values (0-7), but not
		// a startup value. Match VICE's STARS default of 1.
		LeaderLineLength: 1,
		Brightness: BrightnessPreferences{
			// The operator manual defines the allowable ranges but not startup
			// values. Use VICE's STARS defaults so the initial presentation and
			// future saved preference sets match the established implementation.
			DCB:                60,
			BackgroundContrast: 0,
			VideoGroupA:        50,
			VideoGroupB:        40,
			FullDatablocks:     80,
			Lists:              80,
			Positions:          80,
			LimitedDatablocks:  80,
			OtherTracks:        80,
			Lines:              40,
			RangeRings:         20,
			Compass:            40,
			BeaconSymbols:      55,
			PrimarySymbols:     80,
			History:            60,
			Weather:            30,
			WxContrast:         30,
		},

		// VICE's STARS default is (0.05, 0.75) in bottom-left-origin pane
		// coordinates. REDS draws in top-left-origin screen coordinates, so
		// the equivalent Preview Area position is (0.05, 0.25).
		VideoMapVisible: make(map[int]bool),
		VideoMapsList: videoMapsListPreferences{
			// VICE's STARS default is (.85, .5). TI 6191.409 defines the list
			// contents/modality but leaves the initial adapted position to the
			// site, so retain VICE's established default until list-position
			// adaptation is carried by crc2reds.
			Position: [2]float32{0.85, 0.5},
		},
		PreviewAreaPosition: [2]float32{0.05, 0.25},
		SelectedBeacons:     []string{"1200"},
	}
	for i := range prefs.DisplayWeatherLevel {
		prefs.DisplayWeatherLevel[i] = true
	}
	return prefs
}

func initialSTARSCenter(cfg selectedConfig) configPoint {
	if validConfigPoint(cfg.ControlPosition.VisualCenter) {
		return cfg.ControlPosition.VisualCenter
	}
	if validConfigPoint(cfg.Area.VisibilityCenter) {
		return cfg.Area.VisibilityCenter
	}
	return cfg.Facility.DefaultCenter
}

func initialSTARSRange(cfg selectedConfig) float32 {
	r := cfg.ControlPosition.Range
	if r <= 0 {
		r = defaultSTARSRange
	}
	return clampSTARSRange(r)
}

func validConfigPoint(p configPoint) bool {
	return !stdmath.IsNaN(p.Lat) && !stdmath.IsNaN(p.Lon) &&
		p.Lat >= -90 && p.Lat <= 90 && p.Lon >= -180 && p.Lon <= 180 &&
		(p.Lat != 0 || p.Lon != 0)
}

func clampSTARSRange(r float32) float32 {
	if r < minimumSTARSRange {
		return minimumSTARSRange
	}
	if r > maximumTCWRange {
		return maximumTCWRange
	}
	return r
}

func (p *STARSPane) currentPrefs() *Preferences {
	if p == nil {
		return nil
	}
	return &p.prefs
}

func (p *STARSPane) currentCenter() configPoint {
	if p == nil {
		return configPoint{}
	}
	ps := p.currentPrefs()
	if ps.UseUserCenter {
		return ps.UserCenter
	}
	return ps.DefaultCenter
}

func (p *STARSPane) initialLongitudeScaleFactor() float64 {
	if p == nil {
		return 1
	}
	center := p.config.Facility.DefaultCenter
	if !validConfigPoint(center) {
		center = p.currentCenter()
	}
	return radar.LongitudeScaleFactorForLat(center.Lat)
}

func normalizeLongitude(lon float64) float64 {
	for lon > 180 {
		lon -= 360
	}
	for lon <= -180 {
		lon += 360
	}
	return lon
}

func longitudeDelta(lon, reference float64) float64 {
	delta := lon - reference
	for delta > 180 {
		delta -= 360
	}
	for delta <= -180 {
		delta += 360
	}
	return delta
}
