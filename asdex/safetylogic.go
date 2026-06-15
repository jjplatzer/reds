package asdex

import (
	"encoding/json"
	"fmt"
	stdmath "math"
	"path/filepath"
	"sort"
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util"
)

// SafetyLogic currently implements holdbar illumination for landing and
// departure runway operations. Alert messages and aural safety alerts are
// intentionally left out for now.
const (
	landingFinalRangeFeet         = 6076.12
	landingPastThresholdFeet      = 800.0
	landingMaxLateralOffsetFeet   = 1200.0
	landingSpeedThresholdKt       = 40.0
	landingAlignmentMinCos        = 0.9396926207859084
	landingMaxAGLFeet             = 1500.0
	departureSpeedThresholdKt     = 40.0
	departureMaxAGLFeet           = 50.0
	holdBarStationToleranceFeet   = 10.0
	holdBarLineWidthPixels        = 1.25
	pointOnSegmentToleranceFeet   = 5.0
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

	activeOperations map[string]activeRunwayOperation
	litRunways       map[string]bool
	activeAlerts     map[string]SafetyAlert
}

type SafetyAlertType int

const (
	SafetyAlertNone SafetyAlertType = iota
	SafetyAlertLandingClosedRunway
	SafetyAlertDepartureClosedRunway
)

type SafetyAuralAlert int

const (
	SafetyAuralWarning SafetyAuralAlert = iota
	SafetyAuralRunway
	SafetyAuralZero
	SafetyAuralOne
	SafetyAuralTwo
	SafetyAuralThree
	SafetyAuralFour
	SafetyAuralFive
	SafetyAuralSix
	SafetyAuralSeven
	SafetyAuralEight
	SafetyAuralNine
	SafetyAuralLeft
	SafetyAuralRight
	SafetyAuralCenter
	SafetyAuralClosed
)

type SafetyAlert struct {
	ID string

	Type SafetyAlertType

	MessageLines []string
	AircraftIDs  []string
	RunwayIDs    []string

	AuralAlerts []SafetyAuralAlert

	// True only when the alert is newly generated and should trigger audio.
	PlayAuralAlert bool
}

type SafetyAlertChanges struct {
	Upserted []SafetyAlert
	Deleted  []string
}

func (c SafetyAlertChanges) Empty() bool {
	return len(c.Upserted) == 0 && len(c.Deleted) == 0
}

type SafetyRunwayConfiguration struct {
	Name string

	ArrivalRunwayIDs   map[string]bool
	DepartureRunwayIDs map[string]bool
}

func LimitedSafetyRunwayConfiguration() SafetyRunwayConfiguration {
	return SafetyRunwayConfiguration{
		Name:               "LIMITED",
		ArrivalRunwayIDs:   map[string]bool{},
		DepartureRunwayIDs: map[string]bool{},
	}
}

func (cfg SafetyRunwayConfiguration) IsLimited() bool {
	return strings.EqualFold(strings.TrimSpace(cfg.Name), "LIMITED")
}

type SafetyLogicUpdateOptions struct {
	RunwayConfiguration SafetyRunwayConfiguration

	// For now this comes from TempData.RunwayClosed.
	RunwayClosed func(runwayID string) bool

	TargetAlertsInhibited func(targetID string) bool
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

	MinAlongFeet  float32
	MaxAlongFeet  float32
	MinAcrossFeet float32
	MaxAcrossFeet float32
}

type surfaceHoldBar struct {
	ID       string
	RunwayID string

	// LAHSO holdbars need separate activation rules, so normal arrival/departure
	// holdbar illumination ignores them for now.
	LAHSO bool

	PointsFeet []redsmath.Vec2

	RunwayIndex int

	StationMinFeet float32
	StationMaxFeet float32
}

type activeRunwayOperationType int

const (
	activeRunwayOperationLanding activeRunwayOperationType = iota
	activeRunwayOperationDeparture
)

type activeRunwayOperation struct {
	TargetID string
	RunwayID string

	RunwayIndex int
	Type        activeRunwayOperationType

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
		activeOperations:  make(map[string]activeRunwayOperation),
		litRunways:        make(map[string]bool),
		activeAlerts:      make(map[string]SafetyAlert),
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

		id := strings.TrimSpace(src.ID)
		hb := surfaceHoldBar{
			ID:          id,
			RunwayID:    runwayID,
			LAHSO:       isLAHSOHoldBarID(id),
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

func isLAHSOHoldBarID(id string) bool {
	id = strings.ToUpper(strings.TrimSpace(id))
	return strings.HasPrefix(id, "LAHSO")
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
	minAcross := float32(0)
	maxAcross := float32(0)
	halfWidth := float32(0)
	for i, p := range rwy.PolygonFeet {
		rel := p.Sub(rwy.CenterFeet)
		along := safetyDot(rel, axis)
		acrossSigned := safetyDot(rel, normal)
		across := abs32(acrossSigned)
		if i == 0 || along < minAlong {
			minAlong = along
		}
		if i == 0 || along > maxAlong {
			maxAlong = along
		}
		if i == 0 || acrossSigned < minAcross {
			minAcross = acrossSigned
		}
		if i == 0 || acrossSigned > maxAcross {
			maxAcross = acrossSigned
		}
		if across > halfWidth {
			halfWidth = across
		}
	}

	rwy.ThresholdMinFeet = rwy.CenterFeet.Add(axis.Mul(minAlong))
	rwy.ThresholdMaxFeet = rwy.CenterFeet.Add(axis.Mul(maxAlong))
	rwy.LengthFeet = maxAlong - minAlong
	rwy.HalfWidthFeet = halfWidth
	rwy.MinAlongFeet = minAlong
	rwy.MaxAlongFeet = maxAlong
	rwy.MinAcrossFeet = minAcross
	rwy.MaxAcrossFeet = maxAcross
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

func (sl *SafetyLogic) Update(
	targets []*Target,
	opts SafetyLogicUpdateOptions,
) SafetyAlertChanges {
	if sl == nil {
		return SafetyAlertChanges{}
	}
	if sl.activeOperations == nil {
		sl.activeOperations = make(map[string]activeRunwayOperation)
	}
	if sl.litRunways == nil {
		sl.litRunways = make(map[string]bool)
	}
	if sl.activeAlerts == nil {
		sl.activeAlerts = make(map[string]SafetyAlert)
	}
	clear(sl.litRunways)

	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		if target == nil || target.ID == "" {
			continue
		}
		seen[target.ID] = true
		sl.updateOperationForTarget(target)
	}

	for id := range sl.activeOperations {
		if !seen[id] {
			delete(sl.activeOperations, id)
		}
	}

	for _, operation := range sl.activeOperations {
		sl.litRunways[operation.RunwayID] = true
	}

	return sl.updateAlerts(targets, opts)
}

func (sl *SafetyLogic) updateAlerts(
	targets []*Target,
	opts SafetyLogicUpdateOptions,
) SafetyAlertChanges {
	if sl == nil {
		return SafetyAlertChanges{}
	}
	if sl.activeAlerts == nil {
		sl.activeAlerts = make(map[string]SafetyAlert)
	}

	targetByID := targetMapByID(targets)
	generated := sl.generateAlerts(targetByID, opts)

	var changes SafetyAlertChanges

	for id := range sl.activeAlerts {
		if _, stillActive := generated[id]; !stillActive {
			delete(sl.activeAlerts, id)
			changes.Deleted = append(changes.Deleted, id)
		}
	}

	for id, alert := range generated {
		old, existed := sl.activeAlerts[id]
		alert.PlayAuralAlert = !existed

		if !existed || !safetyAlertEqualIgnoringPlay(old, alert) {
			sl.activeAlerts[id] = alert
			changes.Upserted = append(changes.Upserted, alert)
		}
	}

	return changes
}

func (sl *SafetyLogic) generateAlerts(
	targetByID map[string]*Target,
	opts SafetyLogicUpdateOptions,
) map[string]SafetyAlert {
	generated := make(map[string]SafetyAlert)
	if sl == nil {
		return generated
	}

	// LIMITED currently generates closed-runway alerts only. Non-LIMITED
	// runway configuration alert rules will be added when those configs are
	// represented in REDS.
	if !opts.RunwayConfiguration.IsLimited() || opts.RunwayClosed == nil {
		return generated
	}

	for _, operation := range sl.activeOperations {
		if !opts.RunwayClosed(operation.RunwayID) {
			continue
		}

		target := targetByID[operation.TargetID]
		if target == nil {
			continue
		}
		if opts.TargetAlertsInhibited != nil && opts.TargetAlertsInhibited(target.ID) {
			continue
		}

		alert, ok := sl.closedRunwayAlert(target, operation)
		if !ok {
			continue
		}
		generated[alert.ID] = alert
	}

	return generated
}

func targetMapByID(targets []*Target) map[string]*Target {
	out := make(map[string]*Target, len(targets))
	for _, target := range targets {
		if target == nil || target.ID == "" {
			continue
		}
		out[target.ID] = target
	}
	return out
}

func (sl *SafetyLogic) closedRunwayAlert(
	target *Target,
	operation activeRunwayOperation,
) (SafetyAlert, bool) {
	if target == nil || operation.RunwayID == "" {
		return SafetyAlert{}, false
	}

	alertType := SafetyAlertLandingClosedRunway
	if operation.Type == activeRunwayOperationDeparture {
		alertType = SafetyAlertDepartureClosedRunway
	}

	runwayID := strings.ToUpper(strings.TrimSpace(operation.RunwayID))
	targetLabel := safetyAlertTargetLabel(target)

	return SafetyAlert{
		ID:   safetyAlertID(alertType, target.ID, runwayID),
		Type: alertType,

		MessageLines: []string{
			"RWY " + runwayID,
			targetLabel,
			"RWY CLOSED",
		},
		AircraftIDs: []string{target.ID},
		RunwayIDs:   []string{runwayID},

		AuralAlerts: closedRunwayAuralAlerts(runwayID),
	}, true
}

func safetyAlertID(alertType SafetyAlertType, targetID string, runwayID string) string {
	return fmt.Sprintf(
		"SL:%d:%s:%s",
		alertType,
		strings.ToUpper(strings.TrimSpace(targetID)),
		strings.ToUpper(strings.TrimSpace(runwayID)),
	)
}

func safetyAlertTargetLabel(target *Target) string {
	if target == nil {
		return "UNKNOWN"
	}

	if callsign := strings.TrimSpace(target.Callsign); callsign != "" {
		return strings.ToUpper(callsign)
	}
	if beacon := strings.TrimSpace(target.Beacon); beacon != "" {
		return beacon
	}
	if id := strings.TrimSpace(target.ID); id != "" {
		return strings.ToUpper(id)
	}
	return "UNKNOWN"
}

func closedRunwayAuralAlerts(runwayID string) []SafetyAuralAlert {
	out := []SafetyAuralAlert{
		SafetyAuralWarning,
		SafetyAuralRunway,
	}
	out = append(out, runwayIDAuralTokens(runwayID)...)
	out = append(out, SafetyAuralClosed)
	return out
}

func runwayIDAuralTokens(runwayID string) []SafetyAuralAlert {
	runwayID = strings.ToUpper(strings.TrimSpace(runwayID))

	out := make([]SafetyAuralAlert, 0, len(runwayID))
	for _, r := range runwayID {
		switch r {
		case '0':
			out = append(out, SafetyAuralZero)
		case '1':
			out = append(out, SafetyAuralOne)
		case '2':
			out = append(out, SafetyAuralTwo)
		case '3':
			out = append(out, SafetyAuralThree)
		case '4':
			out = append(out, SafetyAuralFour)
		case '5':
			out = append(out, SafetyAuralFive)
		case '6':
			out = append(out, SafetyAuralSix)
		case '7':
			out = append(out, SafetyAuralSeven)
		case '8':
			out = append(out, SafetyAuralEight)
		case '9':
			out = append(out, SafetyAuralNine)
		case 'L':
			out = append(out, SafetyAuralLeft)
		case 'R':
			out = append(out, SafetyAuralRight)
		case 'C':
			out = append(out, SafetyAuralCenter)
		}
	}

	return out
}

func safetyAlertEqualIgnoringPlay(a, b SafetyAlert) bool {
	if a.ID != b.ID || a.Type != b.Type {
		return false
	}
	if !sameStringSlice(a.MessageLines, b.MessageLines) {
		return false
	}
	if !sameStringSlice(a.AircraftIDs, b.AircraftIDs) {
		return false
	}
	if !sameStringSlice(a.RunwayIDs, b.RunwayIDs) {
		return false
	}
	if len(a.AuralAlerts) != len(b.AuralAlerts) {
		return false
	}
	for i := range a.AuralAlerts {
		if a.AuralAlerts[i] != b.AuralAlerts[i] {
			return false
		}
	}
	return true
}

func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (sl *SafetyLogic) ActiveAlerts() []SafetyAlert {
	if sl == nil || len(sl.activeAlerts) == 0 {
		return nil
	}

	out := make([]SafetyAlert, 0, len(sl.activeAlerts))
	for _, alert := range sl.activeAlerts {
		out = append(out, alert)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})

	return out
}

func (sl *SafetyLogic) AircraftIsInAlert(targetID string) bool {
	if sl == nil || targetID == "" {
		return false
	}

	for _, alert := range sl.activeAlerts {
		for _, id := range alert.AircraftIDs {
			if id == targetID {
				return true
			}
		}
	}

	return false
}

func (sl *SafetyLogic) updateOperationForTarget(target *Target) {
	if sl == nil || target == nil || target.ID == "" {
		return
	}
	if len(sl.runways) == 0 {
		delete(sl.activeOperations, target.ID)
		return
	}
	if target.Suspended || target.Coasting || target.Dropped ||
		!targetIsSafetyLogicAircraft(target) {
		delete(sl.activeOperations, target.ID)
		return
	}

	if target.GroundSpeedKt >= landingSpeedThresholdKt {
		if landing, ok := sl.detectApproachLanding(target); ok {
			sl.activeOperations[target.ID] = landing
			return
		}
	}

	if departure, ok := sl.detectDeparture(target); ok {
		sl.activeOperations[target.ID] = departure
		return
	}

	previous, ok := sl.activeOperations[target.ID]
	if !ok {
		return
	}

	switch previous.Type {
	case activeRunwayOperationLanding:
		if sl.continueLandingRollout(target, previous) {
			sl.activeOperations[target.ID] = sl.updatedOperation(target, previous)
			return
		}
	case activeRunwayOperationDeparture:
		if sl.continueDepartureRoll(target, previous) {
			sl.activeOperations[target.ID] = sl.updatedOperation(target, previous)
			return
		}
	}

	delete(sl.activeOperations, target.ID)
}

func targetIsSafetyLogicAircraft(target *Target) bool {
	class := classifyTarget(target)
	return class == targetClassAircraft || class == targetClassHeavyAircraft
}

func (sl *SafetyLogic) detectApproachLanding(target *Target) (activeRunwayOperation, bool) {
	if sl == nil || target == nil {
		return activeRunwayOperation{}, false
	}
	if target.HasAltitude && float64(target.AltitudeFt) > sl.airportAltitudeFt+landingMaxAGLFeet {
		return activeRunwayOperation{}, false
	}

	track := headingUnitVector(target.HeadingDeg)
	var best activeRunwayOperation
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
) (activeRunwayOperation, float32, bool) {
	threshold := rwy.ThresholdMinFeet
	direction := rwy.AxisFeet
	if reverse {
		threshold = rwy.ThresholdMaxFeet
		direction = rwy.AxisFeet.Mul(-1)
	}

	rel := target.PosFeet.Sub(threshold)
	station := safetyDot(rel, direction)
	if station < -landingFinalRangeFeet || station > landingPastThresholdFeet {
		return activeRunwayOperation{}, 0, false
	}
	if abs32(safetyDot(rel, rwy.NormalFeet)) > landingMaxLateralOffsetFeet {
		return activeRunwayOperation{}, 0, false
	}
	if safetyDot(track, direction) < landingAlignmentMinCos {
		return activeRunwayOperation{}, 0, false
	}

	distance := float32(0)
	if station < 0 {
		distance = -station
	}

	return activeRunwayOperation{
		TargetID:           target.ID,
		RunwayID:           rwy.ID,
		RunwayIndex:        runwayIndex,
		Type:               activeRunwayOperationLanding,
		DirectionFeet:      direction,
		StartThresholdFeet: threshold,
		StationFeet:        station,
		SpeedKt:            target.GroundSpeedKt,
	}, distance, true
}

func (sl *SafetyLogic) detectDeparture(target *Target) (activeRunwayOperation, bool) {
	if sl == nil || target == nil {
		return activeRunwayOperation{}, false
	}
	if !sl.targetOnGround(target) || target.GroundSpeedKt < departureSpeedThresholdKt {
		return activeRunwayOperation{}, false
	}

	track := headingUnitVector(target.HeadingDeg)

	var best activeRunwayOperation
	bestAlignment := float32(-2)
	found := false

	for i, rwy := range sl.runways {
		if !pointOnRunway(rwy, target.PosFeet) {
			continue
		}

		alignment := safetyDot(track, rwy.AxisFeet)
		direction := rwy.AxisFeet
		startThreshold := rwy.ThresholdMinFeet
		if alignment < 0 {
			alignment = -alignment
			direction = rwy.AxisFeet.Mul(-1)
			startThreshold = rwy.ThresholdMaxFeet
		}
		if alignment < landingAlignmentMinCos {
			continue
		}

		station := safetyDot(target.PosFeet.Sub(startThreshold), direction)
		if !found || alignment > bestAlignment {
			best = activeRunwayOperation{
				TargetID:           target.ID,
				RunwayID:           rwy.ID,
				RunwayIndex:        i,
				Type:               activeRunwayOperationDeparture,
				DirectionFeet:      direction,
				StartThresholdFeet: startThreshold,
				StationFeet:        station,
				SpeedKt:            target.GroundSpeedKt,
			}
			bestAlignment = alignment
			found = true
		}
	}

	return best, found
}

func (sl *SafetyLogic) targetOnGround(target *Target) bool {
	if sl == nil || target == nil || !target.HasAltitude {
		return false
	}

	return float64(target.AltitudeFt) < sl.airportAltitudeFt+departureMaxAGLFeet
}

func (sl *SafetyLogic) continueLandingRollout(
	target *Target,
	operation activeRunwayOperation,
) bool {
	if sl == nil || target == nil || operation.RunwayIndex < 0 || operation.RunwayIndex >= len(sl.runways) {
		return false
	}
	if target.GroundSpeedKt < landingSpeedThresholdKt {
		return false
	}

	return pointOnRunway(sl.runways[operation.RunwayIndex], target.PosFeet)
}

func (sl *SafetyLogic) continueDepartureRoll(
	target *Target,
	operation activeRunwayOperation,
) bool {
	if sl == nil || target == nil || operation.RunwayIndex < 0 || operation.RunwayIndex >= len(sl.runways) {
		return false
	}
	if target.GroundSpeedKt < departureSpeedThresholdKt || !sl.targetOnGround(target) {
		return false
	}

	rwy := sl.runways[operation.RunwayIndex]
	if !pointOnRunway(rwy, target.PosFeet) {
		return false
	}

	track := headingUnitVector(target.HeadingDeg)
	return safetyDot(track, operation.DirectionFeet) >= landingAlignmentMinCos
}

func (sl *SafetyLogic) updatedOperation(
	target *Target,
	operation activeRunwayOperation,
) activeRunwayOperation {
	operation.StationFeet = safetyDot(target.PosFeet.Sub(operation.StartThresholdFeet), operation.DirectionFeet)
	operation.SpeedKt = target.GroundSpeedKt
	return operation
}

func (sl *SafetyLogic) LitHoldBars() []surfaceHoldBar {
	if sl == nil || len(sl.holdBars) == 0 || len(sl.activeOperations) == 0 {
		return nil
	}

	out := make([]surfaceHoldBar, 0)
	for _, hb := range sl.holdBars {
		if hb.LAHSO {
			continue
		}

		for _, operation := range sl.activeOperations {
			if hb.RunwayIndex != operation.RunwayIndex {
				continue
			}
			if hb.RunwayIndex < 0 || hb.RunwayIndex >= len(sl.runways) {
				continue
			}
			if holdBarAheadOfOperation(hb, operation, sl.runways[operation.RunwayIndex]) {
				out = append(out, hb)
				break
			}
		}
	}
	return out
}

func holdBarAheadOfOperation(
	hb surfaceHoldBar,
	operation activeRunwayOperation,
	rwy surfaceRunway,
) bool {
	alignment := safetyDot(operation.DirectionFeet, rwy.AxisFeet)

	holdBarStart := hb.StationMinFeet
	if alignment < 0 {
		holdBarStart = rwy.LengthFeet - hb.StationMaxFeet
	}

	return operation.StationFeet < holdBarStart-holdBarStationToleranceFeet
}

func (sl *SafetyLogic) DrawHoldBars(
	cb *renderer.CmdBuffer,
	brightness int,
) {
	if sl == nil || cb == nil {
		return
	}

	lit := sl.LitHoldBars()
	if len(lit) == 0 {
		return
	}

	builder := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(builder)

	scratch := make([]renderer.PointVertex, 0, 8)
	for _, hb := range lit {
		if len(hb.PointsFeet) < minHoldBarPolylinePointCount {
			continue
		}

		scratch = scratch[:0]
		for _, point := range hb.PointsFeet {
			scratch = append(scratch, renderer.PointVertex{
				X: point.X,
				Y: point.Y,
			})
		}
		builder.AddLineStrip(scratch)
	}

	cb.SetRGB(applyBrightness(
		renderer.RGB8(0, 255, 0),
		brightness,
		brightnessFloorDefault,
	))
	cb.LineWidth(holdBarLineWidthPixels)
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

func pointOnRunway(rwy surfaceRunway, p redsmath.Vec2) bool {
	if rwy.BoundsFeet.Empty() {
		return false
	}
	if p.X < rwy.BoundsFeet.Min.X-pointOnSegmentToleranceFeet ||
		p.X > rwy.BoundsFeet.Max.X+pointOnSegmentToleranceFeet ||
		p.Y < rwy.BoundsFeet.Min.Y-pointOnSegmentToleranceFeet ||
		p.Y > rwy.BoundsFeet.Max.Y+pointOnSegmentToleranceFeet {
		return false
	}

	return pointInPolygon(rwy.PolygonFeet, p)
}

func pointInPolygon(poly []redsmath.Vec2, p redsmath.Vec2) bool {
	if len(poly) < minRunwayPolygonVertexCount {
		return false
	}

	for i, a := range poly {
		b := poly[(i+1)%len(poly)]
		if pointNearSegment(p, a, b, pointOnSegmentToleranceFeet) {
			return true
		}
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

func pointNearSegment(p, a, b redsmath.Vec2, tolerance float32) bool {
	ab := b.Sub(a)
	len2 := safetyLength2(ab)
	if len2 <= degenerateRunwayAxisLength2 {
		return safetyLength2(p.Sub(a)) <= tolerance*tolerance
	}

	t := safetyDot(p.Sub(a), ab) / len2
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}

	closest := a.Add(ab.Mul(t))
	return safetyLength2(p.Sub(closest)) <= tolerance*tolerance
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
