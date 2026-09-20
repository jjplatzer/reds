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

// Preferences contains the per-position STARS display state that will later
// be saved/restored by STARS preference sets. The names mirror VICE's STARS
// Preferences so DCB commands and saved preference sets can use the same state.
type Preferences struct {
	DefaultCenter           configPoint
	UserCenter              configPoint
	UseUserCenter           bool
	Range                   float32
	RangeRingRadius         float32
	UseUserRangeRingsCenter bool
	LeaderLineDirection     string
	LeaderLineLength        int
	Brightness              BrightnessPreferences
	DisplayWeatherLevel     [6]bool
	VideoMapVisible         map[int]bool
	PreviewAreaPosition     [2]float32
}

func newPreferences(cfg selectedConfig) Preferences {
	center := initialSTARSCenter(cfg)
	prefs := Preferences{
		DefaultCenter: center,
		UserCenter:    center,
		Range:         initialSTARSRange(cfg),
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
		VideoMapVisible:     make(map[int]bool),
		PreviewAreaPosition: [2]float32{0.05, 0.25},
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
