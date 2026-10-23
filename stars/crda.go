package stars

// CRC-compatible CRDA geometry, target qualification, runtime state, and rendering.
// Controller commands remain in cmdtools.go.
//
// References: CRC Stars.Crda/CrdaManager.cs, GeoPoint.cs, GeoCalc.cs,
// NavCalc.cs, DisplayElementGhostTracks, DisplayElementCrdaDiagrams,
// SystemListCrda; STARS TI 6191.409 CRDA and BRITE sections.

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	redsnet "github.com/juliusplatzer/reds/net"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	crdaMaxGroundSpeedKnots = 350
	crdaEarthRadiusNM       = 3437.670013352 // CRC GeoCalc.GreatCircleDistance
)

type crdaMode uint8

const (
	crdaDisabled crdaMode = iota
	crdaStagger
	crdaTie
)

// Runtime state is deliberately separate from immutable CRC adaptation.
// The caller supplies one element per RPC in the unchanged JSON order.
type crdaPairState struct {
	Mode          crdaMode
	MasterEnabled bool
	SlaveEnabled  bool
}

// crdaTrack is a normalized snapshot of the fields required by CRC.
// CourseTrue is TRUE degrees; MagVar follows CRC convention (magnetic = true
// + MagVar). AdjustedAltitudeHundreds is in units of 100 ft, like STARS
// displayed Mode C (e.g. 20 => 2,000 ft). Do not pass raw TAIS feet here.
// Has* flags distinguish a legitimate zero value from a missing report.
type crdaTrack struct {
	Position                         configPoint
	CourseTrue                       float64
	GroundSpeedKnots                 float64
	AdjustedAltitudeHundreds         int
	Scratchpad1, Scratchpad2         string
	Associated, CoastPhase2          bool
	HasCourse, HasSpeed, HasAltitude bool
	ForceQualification               bool // bypass heading/altitude/scratchpads, NOT region/speed
	ForceGhost                       bool // allow the source runway even when its ghosting is off
}

type crdaGhost struct {
	Position     configPoint
	RPCID        string
	RPCIndex     int
	AirportID    string
	SourceRunway string
	MasterSource bool
	Symbol       string
	Mode         crdaMode
}

type crdaRegion struct {
	Points [4]configPoint // near-left, far-left, far-right, near-right
}

type crdaPair struct {
	adaptation rpcConfig
	regions    [2]crdaRegion
}

type crdaEngine struct {
	pairs  []crdaPair
	magVar float64
}

// newCRDAEngine precomputes the two qualification trapezoids for each RPC.
// No per-target polygon construction is necessary during radar updates.
func newCRDAEngine(rpcs []rpcConfig, magneticVariation float64) (*crdaEngine, error) {
	if !crdaFinite(magneticVariation) {
		return nil, fmt.Errorf("CRDA magnetic variation is not finite")
	}
	e := &crdaEngine{pairs: make([]crdaPair, 0, len(rpcs)), magVar: magneticVariation}
	for i, rpc := range rpcs {
		master, err := crdaBuildRegion(rpc.MasterRunway, magneticVariation)
		if err != nil {
			return nil, fmt.Errorf("CRDA RPC %d master %q: %w", i+1, rpc.MasterRunway.RunwayID, err)
		}
		slave, err := crdaBuildRegion(rpc.SlaveRunway, magneticVariation)
		if err != nil {
			return nil, fmt.Errorf("CRDA RPC %d slave %q: %w", i+1, rpc.SlaveRunway.RunwayID, err)
		}
		e.pairs = append(e.pairs, crdaPair{adaptation: rpc, regions: [2]crdaRegion{master, slave}})
	}
	return e, nil
}

func crdaFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func crdaValidPoint(p configPoint) bool {
	return crdaFinite(p.Lat) && crdaFinite(p.Lon) && p.Lat >= -90 && p.Lat <= 90 && p.Lon >= -180 && p.Lon <= 180
}

func crdaNormalizeHeading(h float64) float64 {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	return h
}

func crdaHeadingDelta(a, b float64) float64 {
	d := math.Abs(crdaNormalizeHeading(a) - crdaNormalizeHeading(b))
	if d > 180 {
		d = 360 - d
	}
	return d
}

// CRC GeoCalc.LongitudeScalingFactor: approximately cos(latitude), scaled by
// 59.9986602615/60. Retain CRC's planar degree model rather than mixing it
// with a different map projection inside ghost translation.
func crdaLongitudeScale(lat float64) float64 {
	return (59.998660261511354 / 60.0) * math.Cos(lat*math.Pi/180)
}

// crdaVector matches GeoPoint.AddVector: distances in angular degrees,
// bearing MAGNETIC, longitude corrected by the supplied reference scale.
func crdaVector(origin configPoint, distanceDeg, bearingMag, scale, magVar float64) configPoint {
	a := (bearingMag - magVar) * math.Pi / 180
	return configPoint{
		Lat: origin.Lat + distanceDeg*math.Cos(a),
		Lon: origin.Lon + distanceDeg*math.Sin(a)/scale,
	}
}

func crdaBearing(origin, point configPoint, scale, magVar float64) float64 {
	dx := (point.Lon - origin.Lon) * scale
	dy := point.Lat - origin.Lat
	return crdaNormalizeHeading(math.Atan2(dx, dy)*180/math.Pi + magVar)
}

func crdaDistanceDegrees(a, b configPoint, scale float64) float64 {
	return math.Hypot((b.Lon-a.Lon)*scale, b.Lat-a.Lat)
}

func crdaGreatCircleNM(a, b configPoint) float64 {
	lat1, lat2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dlat, dlon := lat2-lat1, (b.Lon-a.Lon)*math.Pi/180
	h := math.Pow(math.Sin(dlat/2), 2) + math.Cos(lat1)*math.Cos(lat2)*math.Pow(math.Sin(dlon/2), 2)
	h = math.Max(0, math.Min(1, h))
	return 2 * crdaEarthRadiusNM * math.Atan2(math.Sqrt(h), math.Sqrt(1-h))
}

// crdaBuildRegion implements CRC CalculateQualificationRegion. Reference
// heading is the INBOUND course; the region extends outward on its reciprocal.
func crdaBuildRegion(r rpcRunwayConfig, magVar float64) (crdaRegion, error) {
	if !crdaValidPoint(r.TargetReferencePoint) || !crdaValidPoint(r.ImageReferencePoint) {
		return crdaRegion{}, fmt.Errorf("invalid target/image reference point")
	}
	for _, pair := range []struct {
		name  string
		value float64
	}{
		{"nearSideDistance", r.NearSideDistance}, {"regionLength", r.RegionLength},
		{"nearSideHalfWidth", r.NearSideHalfWidth}, {"farSideHalfWidth", r.FarSideHalfWidth},
		{"targetHeading", r.TargetReferenceLineHeading}, {"imageHeading", r.ImageReferenceLineHeading},
		{"tieOffset", r.TieModeOffset}, {"descentDistance", r.DescentPointDistance},
	} {
		if !crdaFinite(pair.value) {
			return crdaRegion{}, fmt.Errorf("%s is not finite", pair.name)
		}
	}
	if r.NearSideDistance < 0 || r.RegionLength <= 0 || r.NearSideHalfWidth < 0 || r.FarSideHalfWidth < 0 ||
		r.DescentPointDistance < 0 || r.HeadingTolerance < 0 || r.HeadingTolerance > 180 || r.TieModeOffset < 0 {
		return crdaRegion{}, fmt.Errorf("invalid CRDA runway dimensions or heading tolerance")
	}
	scale := crdaLongitudeScale(r.TargetReferencePoint.Lat)
	if scale < 1e-9 {
		return crdaRegion{}, fmt.Errorf("reference point too near pole")
	}
	outbound := crdaNormalizeHeading(r.TargetReferenceLineHeading + 180)
	near := crdaVector(r.TargetReferencePoint, r.NearSideDistance/60, outbound, scale, magVar)
	far := crdaVector(near, r.RegionLength/60, outbound, scale, magVar)
	return crdaRegion{Points: [4]configPoint{
		crdaVector(near, r.NearSideHalfWidth/60, outbound-90, scale, magVar),
		crdaVector(far, r.FarSideHalfWidth/60, outbound-90, scale, magVar),
		crdaVector(far, r.FarSideHalfWidth/60, outbound+90, scale, magVar),
		crdaVector(near, r.NearSideHalfWidth/60, outbound+90, scale, magVar),
	}}, nil
}

// Match CRC GeoRegion.Contains (ray crossing, half-open vertex convention).
func (r crdaRegion) contains(p configPoint) bool {
	if !crdaValidPoint(p) {
		return false
	}
	inside := false
	j := len(r.Points) - 1
	for i := 0; i < len(r.Points); i++ {
		a, b := r.Points[i], r.Points[j]
		if ((a.Lat <= p.Lat && p.Lat < b.Lat) || (b.Lat <= p.Lat && p.Lat < a.Lat)) &&
			p.Lon < (b.Lon-a.Lon)*(p.Lat-a.Lat)/(b.Lat-a.Lat)+a.Lon {
			inside = !inside
		}
		j = i
	}
	return inside
}

// CRC CrdaAltitudeAndHeadingQualify. Altitude adaptation and track altitude
// are in 100-ft increments, not raw feet. The outer limits are an early reject;
// closer to the reference point CRC also uses an interpolated descent path.
func crdaAltitudeAndHeadingQualify(track crdaTrack, runway rpcRunwayConfig, magVar float64) bool {
	if crdaHeadingDelta(track.CourseTrue+magVar, runway.TargetReferenceLineHeading) > float64(runway.HeadingTolerance) {
		return false
	}
	h := float64(track.AdjustedAltitudeHundreds)
	if h > float64(runway.DescentPointAltitude+runway.AbovePathTolerance) ||
		h < float64(runway.TargetReferencePointAltitude-runway.BelowPathTolerance) {
		return false
	}
	if len(runway.ScratchpadPatterns) > 0 && !crdaMatchPatterns(track.Scratchpad1, runway.ScratchpadPatterns) {
		return false
	}
	if len(runway.SecondaryScratchpadPatterns) > 0 && !crdaMatchPatterns(track.Scratchpad2, runway.SecondaryScratchpadPatterns) {
		return false
	}
	dist := crdaGreatCircleNM(runway.TargetReferencePoint, track.Position)
	if dist >= runway.DescentPointDistance {
		return h >= float64(runway.DescentPointAltitude-runway.BelowPathTolerance)
	}
	// dist < descent distance implies that descent distance is positive.
	ref := float64(runway.TargetReferencePointAltitude) +
		float64(runway.DescentPointAltitude-runway.TargetReferencePointAltitude)*dist/runway.DescentPointDistance
	return h <= ref+float64(runway.AbovePathTolerance) && h >= ref-float64(runway.BelowPathTolerance)
}

func crdaMatchPatterns(value string, patterns []string) bool {
	if value == "" {
		return false
	}
	for _, pattern := range patterns {
		if strings.Contains(strings.ToUpper(value), strings.ToUpper(pattern)) {
			return true
		}
	}
	return false
}

// crdaImagePosition matches CRC's radial rotation. Unlike a nearest-point
// path projection, it preserves the parent's bearing offset from the OUTBOUND
// target course. Tie offset is applied to the radial distance in NM.
func crdaImagePosition(r rpcRunwayConfig, parent configPoint, mode crdaMode, magVar float64) configPoint {
	scale := crdaLongitudeScale(r.TargetReferencePoint.Lat)
	d := crdaDistanceDegrees(r.TargetReferencePoint, parent, scale)
	if mode == crdaTie {
		d += r.TieModeOffset / 60
	}
	sourceOutbound := crdaNormalizeHeading(r.TargetReferenceLineHeading + 180)
	imageOutbound := crdaNormalizeHeading(r.ImageReferenceLineHeading + 180)
	bearing := crdaNormalizeHeading(imageOutbound + crdaBearing(r.TargetReferencePoint, parent, scale, magVar) - sourceOutbound)
	return crdaVector(r.ImageReferencePoint, d, bearing, scale, magVar)
}

// ghostForTrack returns CRC's ONE best qualifying ghost, or false. Within one
// RPC, a qualifying master wins over its slave (CRC's else-if). Across RPCs,
// smallest magnetic course mismatch wins, ties follow original RPC order.
// This is independent of rendering, UI activation, and live track updates.
func (e *crdaEngine) ghostForTrack(track crdaTrack, states []crdaPairState, forceAllQualification bool) (crdaGhost, bool) {
	if e == nil || len(states) != len(e.pairs) || !track.Associated || track.CoastPhase2 ||
		!track.HasAltitude || !track.HasCourse || !track.HasSpeed || !crdaValidPoint(track.Position) ||
		!crdaFinite(track.CourseTrue) || !crdaFinite(track.GroundSpeedKnots) ||
		track.GroundSpeedKnots < 0 || track.GroundSpeedKnots > crdaMaxGroundSpeedKnots {
		return crdaGhost{}, false
	}
	bestDelta := math.Inf(1)
	var best crdaGhost
	found := false
	for i := range e.pairs {
		pair := &e.pairs[i]
		state := states[i]
		if state.Mode != crdaStagger && state.Mode != crdaTie {
			continue
		}
		for side := 0; side < 2; side++ {
			runway := pair.adaptation.MasterRunway
			enabled := state.MasterEnabled
			if side == 1 {
				runway, enabled = pair.adaptation.SlaveRunway, state.SlaveEnabled
			}
			if !(enabled || track.ForceGhost) || !pair.regions[side].contains(track.Position) {
				continue
			}
			if !(track.ForceQualification || forceAllQualification) && !crdaAltitudeAndHeadingQualify(track, runway, e.magVar) {
				continue
			}
			delta := crdaHeadingDelta(track.CourseTrue+e.magVar, runway.TargetReferenceLineHeading)
			if !found || delta < bestDelta {
				bestDelta, found = delta, true
				symbol := pair.adaptation.PositionSymbolStagger
				if state.Mode == crdaTie {
					symbol = pair.adaptation.PositionSymbolTie
				}
				best = crdaGhost{
					Position: crdaImagePosition(runway, track.Position, state.Mode, e.magVar),
					RPCID:    pair.adaptation.ID, RPCIndex: pair.adaptation.Index,
					AirportID: pair.adaptation.AirportID, SourceRunway: runway.RunwayID,
					MasterSource: side == 0, Mode: state.Mode, Symbol: symbol,
				}
			}
			// CRC's else-if skips slave after master qualifies within an RPC.
			break
		}
	}
	return best, found
}

// CRDA runtime state and operations. The geometry above is immutable; this
// section owns the per-display runway controls, overrides, and live ghosts.
// References: CRC CrdaManager/InputManager, TI 6191.409 §6.5, VICE
// stars/cmdtools.go.

type crdaRunwayState struct {
	DisplayQualificationRegion bool
	DisplayCourseLines         bool
	// nil selects the adapted/default leader direction (CRC LeaderDirection.Default).
	LeaderDirection *leaderLineDirection
}

type crdaTrackOverride struct {
	Identity           string
	ForceGhost         bool
	ForceQualification bool
	Suppressed         bool
	FullDataBlock      bool
}

// crdaDisplayGhost is the renderer-facing, display-local ghost. ParentKey
// resolves the original TAIS target; its position is never fed back to TAIS.
// Suppressed ghosts are retained, so N + parent slew can restore them.
type crdaDisplayGhost struct {
	ParentKey     string
	Identity      string
	Ghost         crdaGhost
	FullDataBlock bool
	Suppressed    bool
}

type crdaRuntime struct {
	engine     *crdaEngine
	pairs      []crdaPairState
	runways    [][2]crdaRunwayState // [master, slave], same order as adaptation
	inhibited  bool
	forceAll   bool
	ghosts     map[string]crdaDisplayGhost
	overrides  map[string]crdaTrackOverride
	seen       map[string]uint64 // reusable per-frame presence marks
	generation uint64
}

func newCRDARuntime(rpcs []rpcConfig) crdaRuntime {
	return crdaRuntime{
		pairs:     make([]crdaPairState, len(rpcs)),
		runways:   make([][2]crdaRunwayState, len(rpcs)),
		ghosts:    make(map[string]crdaDisplayGhost),
		overrides: make(map[string]crdaTrackOverride),
		seen:      make(map[string]uint64),
	}
}

// refreshCRDAEngine reprojects the adapted trapezoids if the display crosses
// a magnetic-variation tile; operator-selected runway modes are not reset.
func (p *STARSPane) refreshCRDAEngine() error {
	if p == nil || len(p.config.Facility.RPCs) == 0 {
		return nil
	}
	center := p.currentCenter()
	variation, err := radar.MagneticVariationAt(center.Lat, center.Lon)
	if err != nil {
		variation = 0 // same true-north fallback as scopeTransformations
	}
	if p.crda.engine != nil && p.crda.engine.magVar == variation {
		return nil
	}
	engine, err := newCRDAEngine(p.config.Facility.RPCs, variation)
	if err != nil {
		return err
	}
	p.crda.engine = engine
	return nil
}

func (p *STARSPane) crdaAvailable() bool {
	return p != nil && p.crda.engine != nil && len(p.crda.pairs) > 0
}

func (p *STARSPane) crdaReady() error {
	if !p.crdaAvailable() {
		return ErrSTARSIllegalFunction
	}
	if p.crda.inhibited {
		return ErrSTARSCRDAOff
	}
	return nil
}

func (p *STARSPane) crdaDisablePair(index int) {
	p.crda.pairs[index] = crdaPairState{}
	p.crda.runways[index] = [2]crdaRunwayState{}
	for key, ghost := range p.crda.ghosts {
		if ghost.Ghost.RPCIndex == p.config.Facility.RPCs[index].Index &&
			ghost.Ghost.RPCID == p.config.Facility.RPCs[index].ID {
			delete(p.crda.ghosts, key)
			state := p.crda.overrides[key]
			state.Suppressed = false
			state.FullDataBlock = true
			p.crda.overrides[key] = state
		}
	}
}

func (p *STARSPane) crdaToggleInhibited() error {
	if !p.crdaAvailable() {
		return ErrSTARSIllegalFunction
	}
	p.crda.inhibited = !p.crda.inhibited
	if p.crda.inhibited {
		clear(p.crda.ghosts)
		for key, state := range p.crda.overrides {
			// CRC deletes its ghost repository on inhibition. Track-level
			// qualification overrides belong to the parent and survive.
			state.Suppressed = false
			state.FullDataBlock = true
			p.crda.overrides[key] = state
		}
		for i := range p.crda.pairs {
			p.crdaDisablePair(i)
		}
	}
	return nil
}

// CRC indexes RPCs by 1-based adaptation ORDER, not the RPC's numeric label.
func (p *STARSPane) crdaSetPairMode(airport string, oneBasedIndex int, mode crdaMode) (string, error) {
	if err := p.crdaReady(); err != nil {
		return "", err
	}
	index := oneBasedIndex - 1
	if index < 0 || index >= len(p.crda.pairs) ||
		!strings.EqualFold(p.config.Facility.RPCs[index].AirportID, airport) {
		return "", ErrSTARSCommandFormat
	}
	pair := p.config.Facility.RPCs[index]
	if mode == crdaDisabled {
		p.crdaDisablePair(index)
	} else {
		state := &p.crda.pairs[index]
		if state.Mode == crdaDisabled {
			state.MasterEnabled = true // CRC enables master on initial activation.
		}
		state.Mode = mode
	}
	verb := "ENABLED"
	if mode == crdaDisabled {
		verb = "INHIBITED"
	}
	return fmt.Sprintf("%s %s/%s %s", pair.AirportID, pair.MasterRunway.RunwayID, pair.SlaveRunway.RunwayID, verb), nil
}

func (p *STARSPane) crdaFindRunway(airport, runway string, activeOnly bool) (int, int, error) {
	if err := p.crdaReady(); err != nil {
		return 0, 0, err
	}
	for i, pair := range p.config.Facility.RPCs {
		if !strings.EqualFold(pair.AirportID, airport) ||
			(activeOnly && p.crda.pairs[i].Mode == crdaDisabled) {
			continue
		}
		if strings.EqualFold(pair.MasterRunway.RunwayID, runway) {
			return i, 0, nil
		}
		if strings.EqualFold(pair.SlaveRunway.RunwayID, runway) {
			return i, 1, nil
		}
	}
	return 0, 0, ErrSTARSCommandFormat
}

func (p *STARSPane) crdaSetRunwayGhosting(airport, runway string, requested *bool) (bool, error) {
	i, side, err := p.crdaFindRunway(airport, runway, false)
	if err != nil {
		return false, err
	}
	state := &p.crda.pairs[i]
	enabled := state.MasterEnabled
	if side == 1 {
		enabled = state.SlaveEnabled
	}
	if requested == nil {
		enabled = !enabled
	} else {
		enabled = *requested
	}
	if side == 0 {
		state.MasterEnabled = enabled
	} else {
		state.SlaveEnabled = enabled
	}
	if enabled && state.Mode == crdaDisabled {
		state.Mode = crdaStagger
	}
	if !state.MasterEnabled && !state.SlaveEnabled {
		p.crdaDisablePair(i)
	}
	return enabled, nil
}

func (p *STARSPane) crdaToggleRunwayOverlay(airport, runway string, courseLines bool) error {
	i, side, err := p.crdaFindRunway(airport, runway, true)
	if err != nil {
		return err
	}
	state := &p.crda.runways[i][side]
	if courseLines {
		state.DisplayCourseLines = !state.DisplayCourseLines
	} else {
		state.DisplayQualificationRegion = !state.DisplayQualificationRegion
	}
	return nil
}

func (p *STARSPane) crdaSetRunwayLeader(airport, runway string, direction leaderLineDirection) error {
	i, side, err := p.crdaFindRunway(airport, runway, true)
	if err != nil {
		return err
	}
	p.crda.runways[i][side].LeaderDirection = &direction
	return nil
}

// crdaIdentity prevents a reused TAIS key/track number inheriting operator
// overrides from a different aircraft. An associated track's SFPN and ACID
// are stable enough for this display-only state; no extra feed copies needed.
func crdaIdentity(t *redsnet.TaisTarget) string {
	if t == nil {
		return ""
	}
	if t.FlightPlan == nil {
		return strings.TrimSpace(t.Track.TrackNum)
	}
	return fmt.Sprintf("%d:%s:%s", t.FlightPlan.SFPN,
		strings.ToUpper(strings.TrimSpace(t.FlightPlan.ACID)), strings.TrimSpace(t.Track.TrackNum))
}

// crdaInputTrack converts the TAIS field units exactly once. TAIS VX/VY are
// knots east/north, altitude is feet; CRC expects degrees true and hundreds
// of feet. TAIS has no nullable velocity or altitude indicators, so a zero
// velocity vector cannot provide a reliable course and is not qualified.
func crdaInputTrack(t *redsnet.TaisTarget, override crdaTrackOverride) crdaTrack {
	vx, vy := float64(t.Track.VX), float64(t.Track.VY)
	hasCourse := vx != 0 || vy != 0
	course := 0.0
	if hasCourse {
		course = crdaNormalizeHeading(math.Atan2(vx, vy) * 180 / math.Pi)
	}
	track := crdaTrack{
		Position:                 configPoint{Lat: t.Track.Lat, Lon: t.Track.Lon},
		CourseTrue:               course,
		GroundSpeedKnots:         math.Hypot(vx, vy),
		AdjustedAltitudeHundreds: int(math.Round(float64(t.Track.ReportedAltitude) / 100)),
		Associated:               t.FlightPlan != nil && !t.FlightPlan.Deleted,
		CoastPhase2:              t.CoastPhase >= redsnet.TaisCoastPhase2,
		HasCourse:                hasCourse,
		HasSpeed:                 true,
		HasAltitude:              true,
		ForceQualification:       override.ForceQualification,
		ForceGhost:               override.ForceGhost,
	}
	if t.FlightPlan != nil {
		track.Scratchpad1 = t.FlightPlan.ScratchPad1
		track.Scratchpad2 = t.FlightPlan.ScratchPad2
	}
	return track
}

// updateCRDA is pure display processing: it never alters TAIS/client state or
// uses RPOS coordinates. It is called after commands each frame. Ghost FDB /
// suppression settings survive a position update, but not a track deletion or
// a TAIS key reallocation to another aircraft.
func (p *STARSPane) updateCRDA(snapshot redsnet.TaisSnapshot) {
	if p == nil {
		return
	}
	if !snapshot.Ready || !p.crdaAvailable() || p.crda.inhibited {
		clear(p.crda.ghosts)
		if !snapshot.Ready {
			clear(p.crda.overrides)
			clear(p.crda.seen)
		}
		return
	}
	// Reuse the presence map instead of allocating an O(target-count) map on
	// every draw. Only live keys remain after the sweep below.
	p.crda.generation++
	if p.crda.generation == 0 {
		clear(p.crda.seen) // extremely rare uint64 wraparound
		p.crda.generation = 1
	}
	generation := p.crda.generation
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		key := targetDisplayStateKey(target)
		if key == "" {
			continue
		}
		p.crda.seen[key] = generation
		identity := crdaIdentity(target)
		override, hasOverride := p.crda.overrides[key]
		if hasOverride && override.Identity != identity {
			delete(p.crda.overrides, key)
			override, hasOverride = crdaTrackOverride{}, false
		}
		ghost, found := p.crda.engine.ghostForTrack(crdaInputTrack(target, override), p.crda.pairs, p.crda.forceAll)
		if !found {
			delete(p.crda.ghosts, key)
			// CRC discards a disqualified ghost. Its temporary FDB and
			// suppression state must not reappear when it requalifies.
			if hasOverride && (override.Suppressed || !override.FullDataBlock) {
				override.Suppressed = false
				override.FullDataBlock = true
				p.crda.overrides[key] = override
			}
			continue
		}
		if !hasOverride {
			override = crdaTrackOverride{Identity: identity, FullDataBlock: true}
		}
		// Update the entire geometry without losing the controller's FDB or
		// suppression selection; do not retain a second copy of the parent.
		p.crda.ghosts[key] = crdaDisplayGhost{
			ParentKey: key, Identity: identity, Ghost: ghost,
			FullDataBlock: override.FullDataBlock, Suppressed: override.Suppressed,
		}
	}
	for key := range p.crda.ghosts {
		if p.crda.seen[key] != generation {
			delete(p.crda.ghosts, key)
		}
	}
	for key := range p.crda.overrides {
		if p.crda.seen[key] != generation {
			delete(p.crda.overrides, key)
		}
	}
	for key, lastSeen := range p.crda.seen {
		if lastSeen != generation {
			delete(p.crda.seen, key)
		}
	}
}

func (p *STARSPane) crdaOverrideFor(t *redsnet.TaisTarget) (string, crdaTrackOverride) {
	key := targetDisplayStateKey(t)
	identity := crdaIdentity(t)
	state, ok := p.crda.overrides[key]
	if !ok || state.Identity != identity {
		state = crdaTrackOverride{Identity: identity, FullDataBlock: true}
	}
	return key, state
}

// Called by the N and N* slew handlers. Ghost suppression is separate from
// force qualification; selecting a parent with N restores a suppressed ghost.
func (p *STARSPane) crdaSlewParent(t *redsnet.TaisTarget, forceQualification bool) error {
	if err := p.crdaReady(); err != nil {
		return err
	}
	if t == nil {
		return ErrSTARSNoTrack
	}
	if t.FlightPlan == nil || t.FlightPlan.Deleted {
		return ErrSTARSIllegalTrack
	}
	key, state := p.crdaOverrideFor(t)
	if forceQualification {
		state.ForceQualification = !state.ForceQualification
	} else {
		if ghost, ok := p.crda.ghosts[key]; ok && !ghost.Suppressed {
			return ErrSTARSIllegalTrack
		}
		state.Suppressed = false
		state.ForceGhost = true
	}
	p.crda.overrides[key] = state
	return nil
}

func (p *STARSPane) crdaSlewGhost(key string, suppress bool) error {
	if err := p.crdaReady(); err != nil {
		return err
	}
	ghost, ok := p.crda.ghosts[key]
	if !ok || ghost.Suppressed {
		return ErrSTARSIllegalTrack
	}
	state, exists := p.crda.overrides[key]
	if !exists || state.Identity != ghost.Identity {
		state = crdaTrackOverride{Identity: ghost.Identity, FullDataBlock: true}
	}
	if suppress {
		state.Suppressed = true
		state.ForceGhost = false
	} else {
		state.FullDataBlock = !state.FullDataBlock
	}
	p.crda.overrides[key] = state
	ghost.Suppressed = state.Suppressed
	ghost.FullDataBlock = state.FullDataBlock
	p.crda.ghosts[key] = ghost
	return nil
}

// closestCRDAGhost selects the nearest visible ghost position symbol.
// CRC DisplayElementGhostTracks uses a 7-pixel click spot, not the 20-pixel
// real-target slew tolerance. Rendering may extend this to its FDB hitboxes.
func (p *STARSPane) closestCRDAGhost(mouse redsmath.Vec2, transforms radar.LatLonTransformations) (string, bool) {
	if !p.crdaAvailable() || p.crda.inhibited {
		return "", false
	}
	const radius = float32(7)
	best2 := radius * radius
	best := ""
	for key, ghost := range p.crda.ghosts {
		if ghost.Suppressed {
			continue
		}
		point := transforms.WindowFromLatLon(ghost.Ghost.Position.Lat, ghost.Ghost.Position.Lon)
		dx, dy := point.X-mouse.X, point.Y-mouse.Y
		if d2 := dx*dx + dy*dy; d2 < best2 {
			best2, best = d2, key
		}
	}
	return best, best != ""
}

// crdaStatusLines is the status-list data required by the future renderer.
// Match CRC SystemListCrda: one-based numbering among pairs whose airports
// belong to the selected area's SSA airport set; S/T marks active modes.
func (p *STARSPane) crdaStatusLines() []string {
	if !p.crdaAvailable() {
		return nil
	}
	visibleAirports := make(map[string]bool, len(p.config.Area.SSAAirports))
	for _, airport := range p.config.Area.SSAAirports {
		visibleAirports[strings.ToUpper(airport)] = true
	}
	lines := make([]string, 0, len(p.crda.pairs))
	for i, pair := range p.config.Facility.RPCs {
		if !visibleAirports[strings.ToUpper(pair.AirportID)] {
			continue
		}
		mode := " "
		owner := ""
		switch p.crda.pairs[i].Mode {
		case crdaStagger:
			mode, owner = "S", p.ownTCP()
		case crdaTie:
			mode, owner = "T", p.ownTCP()
		}
		lines = append(lines, fmt.Sprintf("%s%d %s %-7s %s", mode, len(lines)+1,
			pair.AirportID, pair.MasterRunway.RunwayID+"/"+pair.SlaveRunway.RunwayID, owner))
	}
	return lines
}

// crdaParentReadout approximates CRC InputManager.DisplayParentTrackInfo using
// the available TAIS fields (ACID, type/suffix, beacon and current TCP). It
// does not invent fields missing from the feed.
func (p *STARSPane) crdaParentReadout(snapshot redsnet.TaisSnapshot, key string) string {
	for i := range snapshot.Targets {
		t := &snapshot.Targets[i]
		if targetDisplayStateKey(t) != key {
			continue
		}
		fields := make([]string, 0, 4)
		if t.FlightPlan != nil {
			if acid := strings.TrimSpace(t.FlightPlan.ACID); acid != "" {
				fields = append(fields, acid)
			}
			if typ := strings.TrimSpace(t.FlightPlan.ACType); typ != "" {
				if suffix := strings.TrimSpace(t.FlightPlan.EquipmentSuffix); suffix != "" {
					typ += "/" + suffix
				}
				fields = append(fields, typ)
			}
		}
		code := strings.TrimSpace(t.Track.ReportedBeaconCode)
		if code == "" && t.FlightPlan != nil {
			code = strings.TrimSpace(t.FlightPlan.AssignedBeaconCode)
		}
		if code != "" {
			fields = append(fields, code)
		}
		if tcp, ok := p.targetOwnerTCP(t); ok {
			fields = append(fields, tcp)
		}
		return strings.Join(fields, " ")
	}
	return ""
}

// crdaGhostLeaderDirection resolves the same precedence as CRC's ghost
// renderer: explicit per-runway N/L setting, then adapted runway default,
// then the entering TCW/TDW's tracked leader direction. JSON adaptations
// contain string compass directions; numeric vNAS enum values are supported.
func (p *STARSPane) crdaGhostLeaderDirection(ghost crdaDisplayGhost) leaderLineDirection {
	fallback := p.currentPrefs().LeaderLineDirection
	for i, pair := range p.config.Facility.RPCs {
		if pair.ID != ghost.Ghost.RPCID || pair.Index != ghost.Ghost.RPCIndex {
			continue
		}
		side := 0
		runway := pair.MasterRunway
		if !ghost.Ghost.MasterSource {
			side = 1
			runway = pair.SlaveRunway
		}
		if direction := p.crda.runways[i][side].LeaderDirection; direction != nil {
			return *direction
		}
		adapted := strings.ToUpper(strings.Trim(string(runway.DefaultLeaderDirection), `"`))
		switch adapted {
		case "N":
			return leaderLineDirectionNorth
		case "NE":
			return leaderLineDirectionNorthEast
		case "E":
			return leaderLineDirectionEast
		case "SE":
			return leaderLineDirectionSouthEast
		case "S":
			return leaderLineDirectionSouth
		case "SW":
			return leaderLineDirectionSouthWest
		case "W":
			return leaderLineDirectionWest
		case "NW":
			return leaderLineDirectionNorthWest
		}
		if value, err := strconv.Atoi(adapted); err == nil {
			if direction, ok := leaderLineDirectionFromKeypad(value); ok {
				return direction
			}
		}
		break
	}
	return fallback
}

// CRDA display rendering. Geographic positions are projected at draw time.

const (
	// Course lines and qualifying trapezoids are scope overlays, not UI.
	zCRDADiagrams renderer.Z = -110
	// CRC renders ghosts above ordinary surveillance and below floating lists.
	zCRDAGhostLeader    renderer.Z = -74
	zCRDAGhostPosition  renderer.Z = -73
	zCRDAGhostDatablock renderer.Z = -72
)

// crdaCourseLineEndpoints follows CRC's RenderCourseLines: each reference
// point has a line extending outbound (reciprocal magnetic heading).  The
// common target-reference longitude scale is intentional CRC behavior.
func crdaCourseLineEndpoints(runway rpcRunwayConfig, magVar float64) (targetEnd, imageEnd configPoint) {
	scale := crdaLongitudeScale(runway.TargetReferencePoint.Lat)
	targetEnd = crdaVector(runway.TargetReferencePoint, runway.TargetReferenceLineLength/60,
		crdaNormalizeHeading(runway.TargetReferenceLineHeading+180), scale, magVar)
	imageEnd = crdaVector(runway.ImageReferencePoint, runway.ImageReferenceLineLength/60,
		crdaNormalizeHeading(runway.ImageReferenceLineHeading+180), scale, magVar)
	return
}

// drawCRDADiagrams draws each individually enabled runway's qualification
// trapezoid and the two course-line segments (plus reference-point rings).
// CRC uses the TLS/LINES brightness, not the ghosts' OTH brightness.
func (p *STARSPane) drawCRDADiagrams(ctx *panes.Context, zcb *renderer.ZCmdBuffer, transforms radar.LatLonTransformations) {
	if p == nil || ctx == nil || zcb == nil || !p.crdaAvailable() || p.crda.inhibited ||
		p.currentPrefs().Brightness.Lines == 0 {
		return
	}

	lines := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(lines)
	point := func(loc configPoint) renderer.PointVertex {
		window := transforms.WindowFromLatLon(loc.Lat, loc.Lon)
		return renderer.PointVertex{X: window.X, Y: window.Y}
	}

	for i, pair := range p.crda.engine.pairs {
		if p.crda.pairs[i].Mode == crdaDisabled {
			continue
		}
		for side := 0; side < 2; side++ {
			state := p.crda.runways[i][side]
			if state.DisplayQualificationRegion {
				// Fixed four-point array: no polygon allocation per frame.
				var corners [4]renderer.PointVertex
				for j, corner := range pair.regions[side].Points {
					corners[j] = point(corner)
				}
				lines.AddLineLoop(corners[:])
			}
			if state.DisplayCourseLines {
				runway := pair.adaptation.MasterRunway
				if side == 1 {
					runway = pair.adaptation.SlaveRunway
				}
				targetEnd, imageEnd := crdaCourseLineEndpoints(runway, p.crda.engine.magVar)
				lines.AddLine(point(runway.TargetReferencePoint), point(targetEnd))
				lines.AddLine(point(runway.ImageReferencePoint), point(imageEnd))
				// CRC's reference-point circle radius is 2.25 screen pixels.
				lines.AddCircle(point(runway.TargetReferencePoint), 2.25, 16)
				lines.AddCircle(point(runway.ImageReferencePoint), 2.25, 16)
			}
		}
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zCRDADiagrams)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.SetRGB(p.currentPrefs().Brightness.Lines.ScaleRGB(p.colors.GhostDatablock))
	cb.LineWidth(max(float32(1), ctx.DPIScale))
	lines.GenerateCommands(cb)
	cb.DisableScissor()
}

// crdaGhostDatablockLines contains exactly the information CRC presents for
// an associated ghost: the parent ACID in an FDB, followed by ground speed
// in tens of knots, flight-rules indicator and wake-turbulence category.
// A PDB omits the ACID. No pseudo-track data is synthesized for the ghost.
func crdaGhostDatablockLines(parent *redsnet.TaisTarget, full bool) (first, second string) {
	if parent == nil || parent.FlightPlan == nil || parent.FlightPlan.Deleted {
		return "", ""
	}
	if full {
		first = strings.TrimSpace(parent.FlightPlan.ACID)
	}
	groundSpeed := math.Hypot(float64(parent.Track.VX), float64(parent.Track.VY))
	if !math.IsNaN(groundSpeed) && !math.IsInf(groundSpeed, 0) {
		second = fmt.Sprintf("%02d", int(groundSpeed/10))
	}
	second = strings.TrimRight(second+targetDatablockFlightRulesIndicator(parent.FlightPlan)+
		targetDatablockCategory(parent.FlightPlan), " ")
	return
}

// crdaGhostLeaderGeometry derives all three positions from the same leader
// direction. The position symbol is not offset; the line starts outside it.
func crdaGhostLeaderGeometry(center redsmath.Vec2, direction leaderLineDirection, length float32) (start, end redsmath.Vec2) {
	unit := leaderLineUnitVector(direction)
	start = redsmath.Vec2{X: center.X + 7*unit.X, Y: center.Y + 7*unit.Y}
	end = redsmath.Vec2{X: center.X + length*unit.X, Y: center.Y + length*unit.Y}
	return
}

// drawCRDAGhosts renders CRC-style ghost symbols, leaders and full/partial
// data blocks. It walks the live parent snapshot once, using the Step 2 ghost
// map directly; no ghost copy or O(tracks * ghosts) lookup is required.
func (p *STARSPane) drawCRDAGhosts(ctx *panes.Context, zcb *renderer.ZCmdBuffer,
	transforms radar.LatLonTransformations, parents redsnet.TaisSnapshot) {
	if p == nil || ctx == nil || zcb == nil || !parents.Ready ||
		p.crda.inhibited || len(p.crda.ghosts) == 0 ||
		p.currentPrefs().Brightness.OtherTracks == 0 ||
		p.systemFont == nil || p.systemOutlineFont == nil {
		return
	}

	positionSize, datablockSize := p.positionSymbolFontSize(), p.datablockFontSize()
	positionTexture := p.systemFontTexture(ctx.Renderer, positionSize)
	outlineTexture := p.systemOutlineFontTexture(ctx.Renderer, positionSize)
	datablockTexture := p.systemFontTexture(ctx.Renderer, datablockSize)
	if positionTexture == 0 || outlineTexture == 0 || datablockTexture == 0 {
		return
	}
	lineHeight := p.systemFont.LineHeight(datablockSize)
	if lineHeight <= 0 {
		return
	}

	fill := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(fill)
	fill.SetFont(p.systemFont)
	outline := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(outline)
	outline.SetFont(p.systemOutlineFont)
	labels := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(labels)
	labels.SetFont(p.systemFont)
	leaders := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(leaders)

	ps := p.currentPrefs()
	color := ps.Brightness.OtherTracks.ScaleRGB(p.colors.GhostDatablock)
	positionStyle := renderer.TextStyle{Size: positionSize, Color: color.ToRGBA()}
	outlineStyle := renderer.TextStyle{Size: positionSize, Color: p.colors.PositionSymbolOutline.ToRGBA()}
	labelStyle := renderer.TextStyle{Size: datablockSize, Color: color.ToRGBA()}
	length := starsLeaderLineLengthPixels(ps.LeaderLineLength)

	for i := range parents.Targets {
		parent := &parents.Targets[i]
		ghost, exists := p.crda.ghosts[targetDisplayStateKey(parent)]
		if !exists || ghost.Suppressed || ghost.Identity != crdaIdentity(parent) ||
			parent.FlightPlan == nil || parent.FlightPlan.Deleted || ghost.Ghost.Symbol == "" {
			continue
		}
		center := transforms.WindowFromLatLon(ghost.Ghost.Position.Lat, ghost.Ghost.Position.Lon)
		if !targetCenterNearPane(ctx, center, length+220) {
			continue
		}
		center.X += 0.5 // consistent with regular outlined target symbols
		center.Y -= 0.5
		addCenteredTargetGlyph(outline, p.systemOutlineFont, ghost.Ghost.Symbol, positionSize, center, outlineStyle)
		addCenteredTargetGlyph(fill, p.systemFont, ghost.Ghost.Symbol, positionSize, center, positionStyle)

		direction := p.crdaGhostLeaderDirection(ghost)
		start, end := crdaGhostLeaderGeometry(center, direction, length)
		if length > 7 { // CRC draws a line only when the leader has positive extent
			leaders.AddLine(renderer.PointVertex{X: start.X, Y: start.Y},
				renderer.PointVertex{X: end.X, Y: end.Y})
		}

		first, second := crdaGhostDatablockLines(parent, ghost.FullDataBlock)
		// CRC anchors a zero-length ghost leader at the symbol itself; do
		// not use the ordinary track's larger no-leader clearance offset.
		anchor := end
		if ghost.FullDataBlock && first != "" {
			p.addDatablockLine(labels, first, anchor, direction, 0, 0, lineHeight, labelStyle)
			p.addDatablockLine(labels, second, anchor, direction, 1, 0, lineHeight, labelStyle)
		} else {
			p.addDatablockLine(labels, second, anchor, direction, 0, 0, lineHeight, labelStyle)
		}
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	leaderCB := zcb.At(zCRDAGhostLeader)
	leaderCB.Viewport(x, y, width, height)
	leaderCB.Scissor(x, y, width, height)
	leaderCB.LoadProjectionMatrix(ctx.ScreenProjection())
	leaderCB.LineWidth(max(float32(1), ctx.DPIScale))
	leaderCB.SetRGB(color)
	leaders.GenerateCommands(leaderCB)
	leaderCB.DisableScissor()

	positionCB := zcb.At(zCRDAGhostPosition)
	positionCB.Viewport(x, y, width, height)
	positionCB.Scissor(x, y, width, height)
	positionCB.LoadProjectionMatrix(ctx.ScreenProjection())
	outline.GenerateCommands(positionCB, outlineTexture)
	fill.GenerateCommands(positionCB, positionTexture)
	positionCB.DisableScissor()

	textCB := zcb.At(zCRDAGhostDatablock)
	textCB.Viewport(x, y, width, height)
	textCB.Scissor(x, y, width, height)
	textCB.LoadProjectionMatrix(ctx.ScreenProjection())
	labels.GenerateCommands(textCB, datablockTexture)
	textCB.DisableScissor()
}

// drawCRDAStatusList shares the STARS list font, LISTS brightness and normal
// pane-relative positioning used by other system lists. The lines themselves
// come from the Step 2 runtime (CRC SystemListCrda ordering and S/T markers).
func (p *STARSPane) drawCRDAStatusList(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || p.systemFont == nil ||
		!p.crdaAvailable() || !p.currentPrefs().CRDAStatusList.Visible ||
		p.currentPrefs().Brightness.Lists == 0 {
		return
	}
	lines := p.crdaStatusLines()
	if len(lines) == 0 {
		return
	}
	w, h := ctx.PaneRect.Width(), ctx.PaneRect.Height()
	if w <= 0 || h <= 0 {
		return
	}
	fontSize := p.listFontSize()
	texture := p.systemFontTexture(ctx.Renderer, fontSize)
	if texture == 0 {
		return
	}
	ps := p.currentPrefs()
	position := redsmath.Vec2{X: ps.CRDAStatusList.Position[0] * w, Y: ps.CRDAStatusList.Position[1] * h}

	td := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(td)
	td.SetFont(p.systemFont)
	td.AddText("CRDA STATUS\n"+strings.Join(lines, "\n"), position, renderer.TextStyle{
		Size: fontSize, Color: ps.Brightness.Lists.ScaleRGB(p.colors.List).ToRGBA(),
	})
	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zLists)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	td.GenerateCommands(cb, texture)
	cb.DisableScissor()
}
