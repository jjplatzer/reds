package stars

import (
	"fmt"
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

const starsNegativeAltitudeFilterLimitFeet = -9900

// altitudeFilterPreferences mirrors VICE's STARS Preferences altitude-filter
// state. TI 6191.409 Rev. 30 section 4.11 maintains independent limits for
// unassociated and associated tracks.
type altitudeFilterPreferences struct {
	Unassociated [2]int // low, high, feet
	Associated   [2]int // low, high, feet
}

func defaultAltitudeFilterPreferences() altitudeFilterPreferences {
	// The manual defines the command modality but not the power-up value.
	// Match VICE's established STARS defaults.
	return altitudeFilterPreferences{
		Unassociated: [2]int{100, 60000},
		Associated:   [2]int{100, 60000},
	}
}

func formatAltitudeFilterLimit(feet int) string {
	if feet == starsNegativeAltitudeFilterLimitFeet {
		return "N99"
	}
	return fmt.Sprintf("%03d", feet/100)
}

func (af altitudeFilterPreferences) previewText() string {
	return fmt.Sprintf("%s %s\n%s %s",
		formatAltitudeFilterLimit(af.Unassociated[0]),
		formatAltitudeFilterLimit(af.Unassociated[1]),
		formatAltitudeFilterLimit(af.Associated[0]),
		formatAltitudeFilterLimit(af.Associated[1]),
	)
}

func (af altitudeFilterPreferences) ssaText() string {
	return fmt.Sprintf("%s %s U %s %s A",
		formatAltitudeFilterLimit(af.Unassociated[0]),
		formatAltitudeFilterLimit(af.Unassociated[1]),
		formatAltitudeFilterLimit(af.Associated[0]),
		formatAltitudeFilterLimit(af.Associated[1]),
	)
}

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

// CharacterSizePreferences is the operator-visible CHAR SIZE state from
// TI 6191.409 Rev. 30 section 4.9.1 / Figure 4-9. All groups permit sizes
// 0-5 except the DCB, which permits only 0-2.
type CharacterSizePreferences struct {
	DCB             int
	Datablocks      int
	Lists           int
	Tools           int
	PositionSymbols int
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

// basicSTARSListPreferences is the per-position state shared by the three
// tower lists. TI 6191.409 4.9.7/4.9.12/4.9.16 makes location, visibility,
// and display capacity local TCW/TDW preferences.
type basicSTARSListPreferences struct {
	Position [2]float32
	Visible  bool
	Lines    int
}

// ssaFilterPreferences is the per-position state controlled by the Main DCB
// <SSA FILTER> submenu in TI 6191.409 Rev. 30, 4.7 / Figure 4-7. The ALL
// bit is intentionally independent of the individual bits: while ALL is on,
// every SSA field is displayed; turning ALL off restores the individual
// selection that was in effect before ALL was selected. This mirrors STARS'
// documented modality and VICE's implementation.
type ssaFilterPreferences struct {
	All                 bool
	Wx                  bool
	Time                bool
	Altimeter           bool
	Status              bool
	ConfigPlan          bool
	Radar               bool
	Codes               bool
	SpecialPurposeCodes bool
	SysOff              bool
	Range               bool
	PredictedTrackLines bool
	AltitudeFilters     bool
	NASInterface        bool
	Intrail             bool
	Intrail25           bool
	AirportWeather      bool
	OperationMode       bool
	TestTarget          bool
	WxHistory           bool
	QuickLookPositions  bool
	DisabledTerminal    bool
	Consolidation       bool
	TCPOff              bool
	ActiveCRDAPairs     bool
	Flow                bool
	AMZ                 bool
	TBFM                bool
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
	// DisplayDCB is controlled by the physical STARS <DCB> key. TI 6191.409
	// Rev. 30 section 2.5 defines it as a toggle of the Display Control Bar.
	// VICE maps that key to Ctrl+F9 on a desktop keyboard.
	DisplayDCB bool

	DefaultCenter           configPoint
	UserCenter              configPoint
	UseUserCenter           bool
	Range                   float32
	RangeRingRadius         float32
	RangeRingsUserCenter    configPoint
	UseUserRangeRingsCenter bool
	LeaderLineDirection     leaderLineDirection
	LeaderLineLength        int
	PTLLength               float32
	PTLOwn                  bool
	PTLAll                  bool
	Brightness              BrightnessPreferences
	CharSize                CharacterSizePreferences
	DisplayWeatherLevel     [6]bool
	VideoMapVisible         map[int]bool
	VideoMapsList           videoMapsListPreferences
	PreviewAreaPosition     [2]float32
	SSAListPosition         [2]float32
	TowerLists              [3]basicSTARSListPreferences
	SSAFilter               ssaFilterPreferences
	AltitudeFilters         altitudeFilterPreferences
	QuickLookAll            bool
	QuickLookAllIsPlus      bool
	QuickLookTCPs           map[string]bool // TCP -> quick-look-plus

	// DisplayLDBBeaconCodes is the per-position state controlled by TI 6191.409
	// Rev. 30 section 6.13.9. It controls whether the reported Mode 3/A code is
	// shown in all Limited Data Blocks for unassociated tracks.
	DisplayLDBBeaconCodes bool

	// DisplayTPASize is the TCW/TDW-wide A/TPA MILEAGE state from 6.21.1
	// and 6.21.11. Single-track *D+ commands can locally override it.
	DisplayTPASize bool

	// DisplayATPAInTrailDist is the TCW/TDW-wide INTRAIL DIST state from
	// 6.21.1 and 6.21.17. Single-track *DE/*DI commands can override it for
	// individual qualifying tracks.
	DisplayATPAInTrailDist bool

	// DisplayATPAWarningAlertCones is the TCW/TDW-wide ALERT CONES state
	// from 6.21.1 and 6.21.13. Figure 6-27 shows it enabled.
	DisplayATPAWarningAlertCones bool

	// DisplayATPAMonitorCones is the TCW/TDW-wide MONITOR CONES state from
	// 6.21.1 and 6.21.15. Figure 6-27 shows it inhibited.
	DisplayATPAMonitorCones bool

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
		// STARS presents the DCB initially; VICE uses the same default.
		DisplayDCB:           true,
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
		// TI 6191.409 6.3.4 permits 0.0-5.0 minutes in 0.5-minute
		// increments but does not prescribe a startup value. Match VICE's
		// established STARS default of 1.0 minute.
		PTLLength: 1.0,
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
		// The operator manual defines the selectable ranges but not power-up
		// values. Match VICE's established STARS defaults.
		CharSize: CharacterSizePreferences{
			DCB:             1,
			Datablocks:      1,
			Lists:           1,
			Tools:           1,
			PositionSymbols: 0,
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
		// VICE stores the SSA at (.05, .90) in bottom-left-origin normalized
		// coordinates. REDS screen coordinates use a top-left origin, so the
		// equivalent default is (.05, .10). TI 6191.409 4.9.4 allows the
		// entering keyboard to relocate this position independently.
		SSAListPosition: [2]float32{ssaDefaultX, ssaDefaultY},
		// VICE defaults all three tower lists to five aircraft lines and hidden.
		// Its normalized pane coordinates use a bottom-left origin; REDS uses
		// top-left screen coordinates, so (.05,.8)/(.05,.9) become .2/.1.
		TowerLists: [3]basicSTARSListPreferences{
			{Position: [2]float32{0.05, 0.50}, Lines: 5},
			{Position: [2]float32{0.05, 0.20}, Lines: 5},
			{Position: [2]float32{0.05, 0.10}, Lines: 5},
		},
		// TI 6191.409 does not prescribe a power-up filter state. VICE starts
		// with ALL selected, which also preserves REDS' pre-filter behavior of
		// showing every SSA field it currently knows how to render.
		SSAFilter:       ssaFilterPreferences{All: true},
		AltitudeFilters: defaultAltitudeFilterPreferences(),
		// The operator manual defines how this state is changed but does not
		// prescribe a startup value. Preserve REDS's existing presentation,
		// which showed the beacon code in every LDB, until preference-set
		// persistence/site adaptation supplies an initial value.
		DisplayLDBBeaconCodes: true,
		// Match VICE/STARS: TPA mileage and ATPA in-trail distance are
		// enabled by default at a newly initialized display.
		DisplayTPASize:               true,
		DisplayATPAInTrailDist:       true,
		DisplayATPAWarningAlertCones: true,
		DisplayATPAMonitorCones:      false,
		SelectedBeacons:              []string{"1200"},
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
