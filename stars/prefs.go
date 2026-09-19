package stars

import (
	stdmath "math"

	"github.com/juliusplatzer/reds/radar"
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

// Preferences contains the per-position STARS display state that will later
// be saved/restored by STARS preference sets. The names mirror VICE's STARS
// Preferences so DCB commands and saved preference sets can use the same state.
type Preferences struct {
	DefaultCenter configPoint
	UserCenter    configPoint
	UseUserCenter bool
	Range         float32
}

func newPreferences(cfg selectedConfig) Preferences {
	center := initialSTARSCenter(cfg)
	return Preferences{
		DefaultCenter: center,
		UserCenter:    center,
		Range:         initialSTARSRange(cfg),
	}
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
