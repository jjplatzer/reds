package asdex

import (
	"encoding/json"
	"fmt"
	stdmath "math"
	"path/filepath"
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util"
)

const (
	landingFinalRangeFeet         = 6076.12
	landingPastThresholdFeet      = 800.0
	landingMaxLateralOffsetFeet   = 1200.0
	landingSpeedThresholdKt       = 40.0
	landingAlignmentMinCos        = 0.9396926207859084
	landingMaxAGLFeet             = 1500.0
	holdBarStationToleranceFeet   = 10.0
	degenerateRunwayAxisLength2   = 1e-6
	holdBarsBrightnessDefault     = 95
	minRunwayPolygonVertexCount   = 3
	minHoldBarPolylinePointCount  = 2
	surfaceJSONCoordinateElements = 2
)

type SafetyLogic struct {
	airportAltitudeFt float64

	runways  []surfaceRunway
	holdBars []surfaceHoldBar

	activeLandings map[string]activeLanding
	litRunways     map[string]bool
}

type surfaceRunway struct {
	ID string

	PolygonFeet []redsmath.Vec2
	BoundsFeet  redsmath.Rect

	AxisFeet   redsmath.Vec2
	NormalFeet redsmath.Vec2

	CenterFeet       redsmath.Vec2
	ThresholdMinFeet redsmath.Vec2
	ThresholdMaxFeet redsmath.Vec2

	LengthFeet    float32
	HalfWidthFeet float32
}

type surfaceHoldBar struct {
	ID       string
	RunwayID string

	PointsFeet []redsmath.Vec2

	RunwayIndex int

	StationMinFeet float32
	StationMaxFeet float32
}

type activeLanding struct {
	TargetID string
	RunwayID string

	RunwayIndex int

	DirectionFeet redsmath.Vec2

	StartThresholdFeet redsmath.Vec2

	StationFeet float32

	SpeedKt float32
}

type surfaceJSON struct {
	AltitudeFt float64              `json:"alt"`
	Runways    []surfaceRunwayJSON  `json:"rwys"`
	HoldBars   []surfaceHoldBarJSON `json:"hbs"`
}

type surfaceRunwayJSON struct {
	ID      string      `json:"id"`
	Track   string      `json:"track"`
	Polygon [][]float64 `json:"polygon"`
}

type surfaceHoldBarJSON struct {
	ID      string      `json:"id"`
	Runway  string      `json:"runway"`
	Polygon [][]float64 `json:"polygon"`
}

func LoadSafetyLogic(airport string, vm *VideoMap) (SafetyLogic, error) {
	airport = strings.ToUpper(strings.TrimSpace(airport))
	if airport == "" {
		return SafetyLogic{}, fmt.Errorf("empty ASDE-X safety-logic airport")
	}
	if vm == nil {
		return SafetyLogic{}, fmt.Errorf("ASDE-X safety logic %s: missing videomap projection", airport)
	}

	path := filepath.ToSlash(filepath.Join("asdex", "surface", airport+".json"))
	if !util.ResourceExists(path) {
		return SafetyLogic{}, fmt.Errorf("ASDE-X safety logic %s not found", airport)
	}

	var surface surfaceJSON
	if err := json.Unmarshal(util.LoadResourceBytes(path), &surface); err != nil {
		return SafetyLogic{}, fmt.Errorf("parse ASDE-X safety logic %s: %w", airport, err)
	}

	sl := SafetyLogic{
		airportAltitudeFt: surface.AltitudeFt,
		activeLandings:    make(map[string]activeLanding),
		litRunways:        make(map[string]bool),
	}

	runwayByID := make(map[string]int)
	for _, src := range surface.Runways {
		rwy := surfaceRunway{
			ID:          strings.ToUpper(strings.TrimSpace(src.ID)),
			PolygonFeet: surfacePolylineToFeet(src.Polygon, vm),
		}
		if rwy.ID == "" || len(rwy.PolygonFeet) < minRunwayPolygonVertexCount {
			continue
		}
		populateRunwayFrame(&rwy)
		if rwy.LengthFeet <= 0 {
			continue
		}

		runwayByID[rwy.ID] = len(sl.runways)
		sl.runways = append(sl.runways, rwy)
	}

	for _, src := range surface.HoldBars {
		runwayID := strings.ToUpper(strings.TrimSpace(src.Runway))
		runwayIndex, ok := runwayByID[runwayID]
		if !ok {
			continue
		}

		hb := surfaceHoldBar{
			ID:          strings.TrimSpace(src.ID),
			RunwayID:    runwayID,
			PointsFeet:  surfacePolylineToFeet(src.Polygon, vm),
			RunwayIndex: runwayIndex,
		}
		if hb.ID == "" || len(hb.PointsFeet) < minHoldBarPolylinePointCount {
			continue
		}

		populateHoldBarStations(&hb, sl.runways[runwayIndex])
		sl.holdBars = append(sl.holdBars, hb)
	}

	return sl, nil
}

func surfacePolylineToFeet(coords [][]float64, vm *VideoMap) []redsmath.Vec2 {
	points := make([]redsmath.Vec2, 0, len(coords))
	for _, coord := range coords {
		if len(coord) < surfaceJSONCoordinateElements {
			continue
		}
		points = append(points, vm.LonLatToFeet(coord[0], coord[1]))
	}
	return points
}

func populateRunwayFrame(rwy *surfaceRunway) {
	if rwy == nil || len(rwy.PolygonFeet) < minRunwayPolygonVertexCount {
		return
	}

	axis, ok := runwayAxisFromPolygon(rwy.PolygonFeet)
	if !ok {
		return
	}
	normal := redsmath.Vec2{X: -axis.Y, Y: axis.X}

	rwy.AxisFeet = axis
	rwy.NormalFeet = normal
	rwy.BoundsFeet = boundsForPolygon(rwy.PolygonFeet)

	for _, p := range rwy.PolygonFeet {
		rwy.CenterFeet = rwy.CenterFeet.Add(p)
	}
	rwy.CenterFeet = rwy.CenterFeet.Div(float32(len(rwy.PolygonFeet)))

	minAlong := float32(0)
	maxAlong := float32(0)
	halfWidth := float32(0)
	for i, p := range rwy.PolygonFeet {
		rel := p.Sub(rwy.CenterFeet)
		along := safetyDot(rel, axis)
		across := abs32(safetyDot(rel, normal))
		if i == 0 || along < minAlong {
			minAlong = along
		}
		if i == 0 || along > maxAlong {
			maxAlong = along
		}
		if across > halfWidth {
			halfWidth = across
		}
	}

	rwy.ThresholdMinFeet = rwy.CenterFeet.Add(axis.Mul(minAlong))
	rwy.ThresholdMaxFeet = rwy.CenterFeet.Add(axis.Mul(maxAlong))
	rwy.LengthFeet = maxAlong - minAlong
	rwy.HalfWidthFeet = halfWidth
}

func runwayAxisFromPolygon(poly []redsmath.Vec2) (redsmath.Vec2, bool) {
	if len(poly) < minRunwayPolygonVertexCount {
		return redsmath.Vec2{}, false
	}

	var best redsmath.Vec2
	bestLen2 := float32(0)
	for i, p := range poly {
		next := poly[(i+1)%len(poly)]
		edge := next.Sub(p)
		edgeLen2 := safetyLength2(edge)
		if edgeLen2 > bestLen2 {
			best = edge
			bestLen2 = edgeLen2
		}
	}

	if bestLen2 <= degenerateRunwayAxisLength2 {
		return redsmath.Vec2{}, false
	}
	return safetyNormalize(best)
}

func populateHoldBarStations(hb *surfaceHoldBar, rwy surfaceRunway) {
	if hb == nil || len(hb.PointsFeet) == 0 {
		return
	}

	for i, p := range hb.PointsFeet {
		station := safetyDot(p.Sub(rwy.ThresholdMinFeet), rwy.AxisFeet)
		if i == 0 || station < hb.StationMinFeet {
			hb.StationMinFeet = station
		}
		if i == 0 || station > hb.StationMaxFeet {
			hb.StationMaxFeet = station
		}
	}
}

func (sl *SafetyLogic) Update(targets []*Target) {
	if sl == nil {
		return
	}
	if sl.activeLandings == nil {
		sl.activeLandings = make(map[string]activeLanding)
	}
	if sl.litRunways == nil {
		sl.litRunways = make(map[string]bool)
	}
	clear(sl.litRunways)

	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		if target == nil || target.ID == "" {
			continue
		}
		seen[target.ID] = true
		sl.updateLandingForTarget(target)
	}

	for id := range sl.activeLandings {
		if !seen[id] {
			delete(sl.activeLandings, id)
		}
	}

	for _, landing := range sl.activeLandings {
		sl.litRunways[landing.RunwayID] = true
	}
}

func (sl *SafetyLogic) updateLandingForTarget(target *Target) {
	if sl == nil || target == nil || target.ID == "" {
		return
	}
	if len(sl.runways) == 0 {
		delete(sl.activeLandings, target.ID)
		return
	}
	if target.Suspended || target.Coasting || target.Dropped ||
		!targetIsSafetyLogicAircraft(target) ||
		target.GroundSpeedKt < landingSpeedThresholdKt {
		delete(sl.activeLandings, target.ID)
		return
	}

	if landing, ok := sl.detectApproachLanding(target); ok {
		sl.activeLandings[target.ID] = landing
		return
	}

	previous, ok := sl.activeLandings[target.ID]
	if !ok {
		return
	}
	if sl.continueRollout(target, previous) {
		sl.activeLandings[target.ID] = sl.updatedRollout(target, previous)
		return
	}
	delete(sl.activeLandings, target.ID)
}

func targetIsSafetyLogicAircraft(target *Target) bool {
	class := classifyTarget(target)
	return class == targetClassAircraft || class == targetClassHeavyAircraft
}

func (sl *SafetyLogic) detectApproachLanding(target *Target) (activeLanding, bool) {
	if sl == nil || target == nil {
		return activeLanding{}, false
	}
	if target.HasAltitude && float64(target.AltitudeFt) > sl.airportAltitudeFt+landingMaxAGLFeet {
		return activeLanding{}, false
	}

	track := headingUnitVector(target.HeadingDeg)
	var best activeLanding
	bestDistance := float32(0)
	found := false

	for i, rwy := range sl.runways {
		if landing, distance, ok := approachLandingForRunwayEnd(target, track, rwy, i, false); ok {
			if !found || distance < bestDistance {
				best = landing
				bestDistance = distance
				found = true
			}
		}
		if landing, distance, ok := approachLandingForRunwayEnd(target, track, rwy, i, true); ok {
			if !found || distance < bestDistance {
				best = landing
				bestDistance = distance
				found = true
			}
		}
	}

	return best, found
}

func approachLandingForRunwayEnd(
	target *Target,
	track redsmath.Vec2,
	rwy surfaceRunway,
	runwayIndex int,
	reverse bool,
) (activeLanding, float32, bool) {
	threshold := rwy.ThresholdMinFeet
	direction := rwy.AxisFeet
	if reverse {
		threshold = rwy.ThresholdMaxFeet
		direction = rwy.AxisFeet.Mul(-1)
	}

	rel := target.PosFeet.Sub(threshold)
	station := safetyDot(rel, direction)
	if station < -landingFinalRangeFeet || station > landingPastThresholdFeet {
		return activeLanding{}, 0, false
	}
	if abs32(safetyDot(rel, rwy.NormalFeet)) > landingMaxLateralOffsetFeet {
		return activeLanding{}, 0, false
	}
	if safetyDot(track, direction) < landingAlignmentMinCos {
		return activeLanding{}, 0, false
	}

	distance := float32(0)
	if station < 0 {
		distance = -station
	}

	return activeLanding{
		TargetID:           target.ID,
		RunwayID:           rwy.ID,
		RunwayIndex:        runwayIndex,
		DirectionFeet:      direction,
		StartThresholdFeet: threshold,
		StationFeet:        station,
		SpeedKt:            target.GroundSpeedKt,
	}, distance, true
}

func (sl *SafetyLogic) continueRollout(target *Target, landing activeLanding) bool {
	if sl == nil || target == nil || landing.RunwayIndex < 0 || landing.RunwayIndex >= len(sl.runways) {
		return false
	}
	if target.GroundSpeedKt < landingSpeedThresholdKt {
		return false
	}

	return pointInPolygon(sl.runways[landing.RunwayIndex].PolygonFeet, target.PosFeet)
}

func (sl *SafetyLogic) updatedRollout(target *Target, landing activeLanding) activeLanding {
	landing.StationFeet = safetyDot(target.PosFeet.Sub(landing.StartThresholdFeet), landing.DirectionFeet)
	landing.SpeedKt = target.GroundSpeedKt
	return landing
}

func (sl *SafetyLogic) LitHoldBars() []surfaceHoldBar {
	if sl == nil || len(sl.holdBars) == 0 || len(sl.activeLandings) == 0 {
		return nil
	}

	out := make([]surfaceHoldBar, 0)
	for _, hb := range sl.holdBars {
		for _, landing := range sl.activeLandings {
			if hb.RunwayIndex != landing.RunwayIndex {
				continue
			}
			if hb.RunwayIndex < 0 || hb.RunwayIndex >= len(sl.runways) {
				continue
			}
			if holdBarAheadOfLanding(hb, landing, sl.runways[landing.RunwayIndex]) {
				out = append(out, hb)
				break
			}
		}
	}
	return out
}

func holdBarAheadOfLanding(hb surfaceHoldBar, landing activeLanding, rwy surfaceRunway) bool {
	alignment := safetyDot(landing.DirectionFeet, rwy.AxisFeet)

	holdBarStart := hb.StationMinFeet
	if alignment < 0 {
		holdBarStart = rwy.LengthFeet - hb.StationMaxFeet
	}

	return landing.StationFeet < holdBarStart-holdBarStationToleranceFeet
}

func (sl *SafetyLogic) DrawHoldBars(cb *renderer.CmdBuffer, brightness int) {
	if sl == nil || cb == nil {
		return
	}

	lit := sl.LitHoldBars()
	if len(lit) == 0 {
		return
	}

	builder := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(builder)

	for _, hb := range lit {
		points := make([]renderer.PointVertex, 0, len(hb.PointsFeet))
		for _, p := range hb.PointsFeet {
			points = append(points, renderer.PointVertex{X: p.X, Y: p.Y})
		}
		builder.AddLineStrip(points)
	}

	cb.SetRGB(applyBrightness(renderer.RGB8(0, 255, 0), brightness, brightnessFloorDefault))
	cb.LineWidth(1)
	builder.GenerateCommands(cb)
}

func headingUnitVector(headingDeg float32) redsmath.Vec2 {
	rad := float64(headingDeg) * stdmath.Pi / 180
	return redsmath.Vec2{
		X: float32(stdmath.Sin(rad)),
		Y: float32(stdmath.Cos(rad)),
	}
}

func boundsForPolygon(poly []redsmath.Vec2) redsmath.Rect {
	if len(poly) == 0 {
		return redsmath.Rect{}
	}

	minX, maxX := poly[0].X, poly[0].X
	minY, maxY := poly[0].Y, poly[0].Y
	for _, p := range poly[1:] {
		if p.X < minX {
			minX = p.X
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}
	return redsmath.NewRect(minX, minY, maxX, maxY)
}

func pointInPolygon(poly []redsmath.Vec2, p redsmath.Vec2) bool {
	if len(poly) < minRunwayPolygonVertexCount {
		return false
	}

	inside := false
	j := len(poly) - 1
	for i := range poly {
		pi := poly[i]
		pj := poly[j]
		if (pi.Y > p.Y) != (pj.Y > p.Y) {
			xAtY := (pj.X-pi.X)*(p.Y-pi.Y)/(pj.Y-pi.Y) + pi.X
			if p.X < xAtY {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

func safetyDot(a, b redsmath.Vec2) float32 {
	return a.X*b.X + a.Y*b.Y
}

func safetyLength2(v redsmath.Vec2) float32 {
	return safetyDot(v, v)
}

func safetyNormalize(v redsmath.Vec2) (redsmath.Vec2, bool) {
	len2 := safetyLength2(v)
	if len2 <= degenerateRunwayAxisLength2 {
		return redsmath.Vec2{}, false
	}

	length := float32(stdmath.Sqrt(float64(len2)))
	return v.Div(length), true
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
