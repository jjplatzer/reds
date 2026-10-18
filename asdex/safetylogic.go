package asdex

import (
	"encoding/json"
	"fmt"
	stdmath "math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util"
)

// SafetyLogic manages runway operations, hold-bar illumination and alerts.
const (
	landingFinalRangeFeet         = 6076.12
	landingPastThresholdFeet      = 800.0
	landingMaxLateralOffsetFeet   = 1200.0
	landingParallelMinAxisCos     = 0.984807753012208
	landingMinLateralOffsetFeet   = 250.0
	landingLateralTieTolerance    = 25.0
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
	// Per-alert deactivation debounce for noisy predicted clearance.
	alertClearSince map[string]time.Time
}

type SafetyAlertType int

const (
	SafetyAlertNone SafetyAlertType = iota
	SafetyAlertLandingClosedRunway
	SafetyAlertDepartureClosedRunway
	SafetyAlertLandingOccupiedRunway
	SafetyAlertConvergingRunways
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
	SafetyAuralOccupied
	SafetyAuralGoAround
	SafetyAuralTestMessage
	SafetyAuralConverging
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

type RunwayConfiguration struct {
	ID                   string
	Number               int
	Name                 string
	ArrivalRunwayIDs     []string
	DepartureRunwayIDs   []string
	HoldShortRunwayPairs []HoldShortRunwayPair
	Default              bool
}

type HoldShortRunwayPair struct {
	ID              string `json:"id"`
	ArrivalRunwayID string `json:"arrivalRunwayId"`
	HoldShortID     string `json:"holdShortId"`
}

type runwayConfigAirportJSON struct {
	RunwayConfigurations []runwayConfigJSON `json:"runwayConfigurations"`
}

type runwayConfigJSON struct {
	ID                   string                `json:"id"`
	Number               int                   `json:"number"`
	Name                 string                `json:"name"`
	ArrivalRunwayIDs     []string              `json:"arrivalRunwayIds"`
	DepartureRunwayIDs   []string              `json:"departureRunwayIds"`
	HoldShortRunwayPairs []HoldShortRunwayPair `json:"holdShortRunwayPairs"`
	Default              bool                  `json:"default"`
}

func runwayConfigID(cfg runwayConfigJSON) string {
	if id := strings.TrimSpace(cfg.ID); id != "" {
		return id
	}
	if strings.EqualFold(strings.TrimSpace(cfg.Name), "LIMITED") {
		return "limited"
	}
	if cfg.Number > 0 {
		return strconv.Itoa(cfg.Number)
	}
	return strings.ToUpper(strings.TrimSpace(cfg.Name))
}

func loadRunwayConfigurations(airport string) ([]RunwayConfiguration, string, error) {
	airport = strings.ToUpper(strings.TrimSpace(airport))
	if airport == "" {
		return nil, "", nil
	}

	path := "resources/configs/asdex/" + airport + ".json"
	if !util.ResourceExists(path) {
		return nil, "", fmt.Errorf("runway config: airport config %s not found", path)
	}

	var raw runwayConfigAirportJSON
	if err := json.Unmarshal(util.LoadResourceBytes(path), &raw); err != nil {
		return nil, "", fmt.Errorf("runway config: decode %s: %w", path, err)
	}

	out := make([]RunwayConfiguration, 0, len(raw.RunwayConfigurations))
	defaultID := ""
	for _, item := range raw.RunwayConfigurations {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}

		cfg := RunwayConfiguration{
			ID:                   runwayConfigID(item),
			Number:               item.Number,
			Name:                 name,
			ArrivalRunwayIDs:     append([]string(nil), item.ArrivalRunwayIDs...),
			DepartureRunwayIDs:   append([]string(nil), item.DepartureRunwayIDs...),
			HoldShortRunwayPairs: append([]HoldShortRunwayPair(nil), item.HoldShortRunwayPairs...),
			Default:              item.Default,
		}
		out = append(out, cfg)
		if cfg.Default && defaultID == "" {
			defaultID = cfg.ID
		}
	}

	if defaultID == "" && len(out) > 0 {
		defaultID = out[len(out)-1].ID
	}

	return out, defaultID, nil
}

type TowerConfiguration struct {
	ID        string
	Name      string
	RunwayIDs []string
	Default   bool
}

type towerConfigAirportJSON struct {
	TowerPositions []towerConfigJSON `json:"towerPositions"`
}

type towerConfigJSON struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	RunwayIDs []string `json:"runwayIds"`
	Default   bool     `json:"default"`
}

func towerConfigID(name string) string {
	return strings.ToUpper(strings.TrimSpace(name))
}

func loadTowerConfigurations(airport string) ([]TowerConfiguration, string, error) {
	airport = strings.ToUpper(strings.TrimSpace(airport))
	if airport == "" {
		return nil, "", nil
	}

	path := "resources/configs/asdex/" + airport + ".json"
	if !util.ResourceExists(path) {
		return nil, "", fmt.Errorf("tower config: airport config %s not found", path)
	}

	var cfg towerConfigAirportJSON
	if err := json.Unmarshal(util.LoadResourceBytes(path), &cfg); err != nil {
		return nil, "", fmt.Errorf("tower config: decode %s: %w", path, err)
	}

	out := make([]TowerConfiguration, 0, len(cfg.TowerPositions))
	defaultID := ""
	for _, item := range cfg.TowerPositions {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}

		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = towerConfigID(name)
		}

		tc := TowerConfiguration{
			ID:        id,
			Name:      name,
			RunwayIDs: append([]string(nil), item.RunwayIDs...),
			Default:   item.Default,
		}
		out = append(out, tc)
		if tc.Default && defaultID == "" {
			defaultID = tc.ID
		}
	}

	if defaultID == "" && len(out) > 0 {
		out[0].Default = true
		defaultID = out[0].ID
	}

	return out, defaultID, nil
}

func (p *ASDEXPane) towerRunwayEnabledPredicate() func(string) bool {
	enabled, configured := p.enabledTowerRunwayIDs()
	if !configured {
		return nil
	}

	return func(runwayID string) bool {
		runwayID = normalizeRunwayEndID(runwayID)
		return runwayID != "" && enabled[runwayID]
	}
}

func (p *ASDEXPane) enabledTowerRunwayIDs() (map[string]bool, bool) {
	if p == nil || len(p.towerConfigurations) == 0 {
		return nil, false
	}

	out := make(map[string]bool)
	for _, cfg := range p.towerConfigurations {
		if cfg.ID != p.defaultTowerConfigID && !p.activeTowerConfigIDs[cfg.ID] {
			continue
		}

		for _, group := range cfg.RunwayIDs {
			for _, runwayID := range strings.Split(group, "-") {
				runwayID = normalizeRunwayEndID(runwayID)
				if runwayID != "" {
					out[runwayID] = true
				}
			}
		}
	}

	return out, true
}

type SafetyLogicUpdateOptions struct {
	RunwayConfiguration SafetyRunwayConfiguration

	// For now this comes from TempData.RunwayClosed.
	RunwayClosed func(runwayID string) bool

	TargetAlertsInhibited func(targetID string) bool

	// Occupied-runway prediction is experimental and must be enabled explicitly.
	OccupiedRunwayAlerts        bool
	OccupiedRunwayClearanceFeet float64   // additional prototype margin beyond both footprints
	Now                         time.Time // simulation time, supports deterministic playback and tests
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

	approachLateralLimitFeet float32
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

type approachLandingCandidate struct {
	operation activeRunwayOperation

	thresholdDistanceFeet float32
	lateralOffsetFeet     float32
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

	path := filepath.ToSlash(filepath.Join("asdex", "surface", airport+".json.zst"))
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
		alertClearSince:   make(map[string]time.Time),
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
	populateApproachLateralLimits(sl.runways)

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

func populateApproachLateralLimits(runways []surfaceRunway) {
	for i := range runways {
		runways[i].approachLateralLimitFeet = landingMaxLateralOffsetFeet

		closestParallelFeet := float32(0)
		for j := range runways {
			if i == j {
				continue
			}

			if abs32(safetyDot(runways[i].AxisFeet, runways[j].AxisFeet)) < landingParallelMinAxisCos {
				continue
			}

			lateral := abs32(safetyDot(
				runways[j].CenterFeet.Sub(runways[i].CenterFeet),
				runways[i].NormalFeet,
			))
			if lateral <= runways[i].HalfWidthFeet+runways[j].HalfWidthFeet {
				continue
			}

			if closestParallelFeet == 0 || lateral < closestParallelFeet {
				closestParallelFeet = lateral
			}
		}

		if closestParallelFeet <= 0 {
			continue
		}

		runways[i].approachLateralLimitFeet = min32(
			landingMaxLateralOffsetFeet,
			max32(landingMinLateralOffsetFeet, closestParallelFeet/2),
		)
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
	if sl.alertClearSince == nil {
		sl.alertClearSince = make(map[string]time.Time)
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

	for id, previous := range sl.activeAlerts {
		if _, stillActive := generated[id]; stillActive {
			delete(sl.alertClearSince, id)
			continue
		}
		// Preserve an occupied-runway alert briefly if *both* aircraft are
		// still observed and neither is inhibited. An actual removal or manual
		// inhibition must clear immediately. Playback can move time backward.
		if (previous.Type == SafetyAlertLandingOccupiedRunway || previous.Type == SafetyAlertConvergingRunways) && opts.OccupiedRunwayAlerts &&
			len(previous.AircraftIDs) == 2 && !opts.Now.IsZero() &&
			conflictPairStillPresent(previous, targetByID, opts) {
			start, exists := sl.alertClearSince[id]
			if !exists || opts.Now.Before(start) {
				sl.alertClearSince[id] = opts.Now
				continue
			}
			if opts.Now.Sub(start) < 2*time.Second {
				continue
			}
		}
		delete(sl.alertClearSince, id)
		delete(sl.activeAlerts, id)
		changes.Deleted = append(changes.Deleted, id)
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

	if opts.OccupiedRunwayAlerts {
		converging := sl.convergingRunwayAlerts(targetByID, opts)
		// A track on an intersecting runway can simultaneously occupy the
		// arriving runway's polygon. Prefer the converging alert for that
		// aircraft pair, rather than issuing two identical aural warnings.
		convergingPairs := make(map[string]bool, len(converging))
		for id, alert := range converging {
			generated[id] = alert
			if len(alert.AircraftIDs) == 2 {
				convergingPairs[safetyAlertPairKey(alert.AircraftIDs[0], alert.AircraftIDs[1])] = true
			}
		}
		for id, alert := range sl.occupiedRunwayAlerts(targetByID, opts) {
			if len(alert.AircraftIDs) == 2 &&
				convergingPairs[safetyAlertPairKey(alert.AircraftIDs[0], alert.AircraftIDs[1])] {
				continue
			}
			generated[id] = alert
		}
	}
	if opts.RunwayClosed == nil {
		return generated
	}

	for _, operation := range sl.activeOperations {
		if !runwayConfigurationAllowsOperation(opts.RunwayConfiguration, operation) {
			continue
		}
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

func runwayConfigurationAllowsOperation(
	cfg SafetyRunwayConfiguration,
	operation activeRunwayOperation,
) bool {
	if cfg.IsLimited() {
		return true
	}

	// Configurations use directional runway ends (e.g. "4L"), while the
	// surface geometry uses a reciprocal pair (e.g. "04L/22R"). Match the
	// end the aircraft is actually using, not either end of the same pavement.
	runwayID := operationRunwayEndID(operation)
	if runwayID == "" {
		return false
	}

	switch operation.Type {
	case activeRunwayOperationLanding:
		return cfg.ArrivalRunwayIDs[runwayID]
	case activeRunwayOperationDeparture:
		return cfg.DepartureRunwayIDs[runwayID]
	default:
		return false
	}
}

// normalizeRunwayEndID compares FAA runway-end identifiers with and without
// leading zeros. Keep nonstandard IDs unchanged rather than guessing.
func normalizeRunwayEndID(id string) string {
	id = strings.ToUpper(strings.TrimSpace(id))
	i := 0
	for i < len(id) && id[i] >= '0' && id[i] <= '9' {
		i++
	}
	if i == 0 {
		return id
	}
	number, err := strconv.Atoi(id[:i])
	if err != nil || number < 1 || number > 36 {
		return id
	}
	switch id[i:] {
	case "", "L", "R", "C":
		return strconv.Itoa(number) + id[i:]
	default:
		return id
	}
}

// operationRunwayEndID chooses the reciprocal end aligned with the detected
// landing/departure direction. Runway numbers approximate magnetic bearings;
// only the relative choice between opposing ends is needed here.
func operationRunwayEndID(operation activeRunwayOperation) string {
	ends := strings.Split(strings.ToUpper(strings.TrimSpace(operation.RunwayID)), "/")
	if len(ends) == 1 {
		return normalizeRunwayEndID(ends[0])
	}

	bestEnd := ""
	bestAlignment := float32(-2)
	for _, end := range ends {
		end = normalizeRunwayEndID(end)
		i := 0
		for i < len(end) && end[i] >= '0' && end[i] <= '9' {
			i++
		}
		if i == 0 {
			continue
		}
		number, err := strconv.Atoi(end[:i])
		if err != nil || number < 1 || number > 36 {
			continue
		}
		alignment := safetyDot(operation.DirectionFeet, headingUnitVector(float32(number*10)))
		if alignment > bestAlignment {
			bestAlignment = alignment
			bestEnd = end
		}
	}
	return bestEnd
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

	// Keep the original combined ID for the closure lookup above, but scope
	// the alert and its voice message to the actual operating runway end.
	runwayID := operationRunwayEndID(operation)
	if runwayID == "" {
		return SafetyAlert{}, false
	}
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
	var best approachLandingCandidate
	found := false

	for i, rwy := range sl.runways {
		if landing, ok := approachLandingForRunwayEnd(target, track, rwy, i, false); ok {
			if !found || approachLandingCandidateBetter(landing, best) {
				best = landing
				found = true
			}
		}
		if landing, ok := approachLandingForRunwayEnd(target, track, rwy, i, true); ok {
			if !found || approachLandingCandidateBetter(landing, best) {
				best = landing
				found = true
			}
		}
	}

	return best.operation, found
}

func approachLandingForRunwayEnd(
	target *Target,
	track redsmath.Vec2,
	rwy surfaceRunway,
	runwayIndex int,
	reverse bool,
) (approachLandingCandidate, bool) {
	threshold := rwy.ThresholdMinFeet
	direction := rwy.AxisFeet
	if reverse {
		threshold = rwy.ThresholdMaxFeet
		direction = rwy.AxisFeet.Mul(-1)
	}

	rel := target.PosFeet.Sub(threshold)
	station := safetyDot(rel, direction)
	if station < -landingFinalRangeFeet || station > landingPastThresholdFeet {
		return approachLandingCandidate{}, false
	}

	lateralOffset := abs32(safetyDot(rel, rwy.NormalFeet))
	if lateralOffset > approachLateralLimitFeet(rwy) {
		return approachLandingCandidate{}, false
	}
	if safetyDot(track, direction) < landingAlignmentMinCos {
		return approachLandingCandidate{}, false
	}

	distance := float32(0)
	if station < 0 {
		distance = -station
	}

	return approachLandingCandidate{
		operation: activeRunwayOperation{
			TargetID:           target.ID,
			RunwayID:           rwy.ID,
			RunwayIndex:        runwayIndex,
			Type:               activeRunwayOperationLanding,
			DirectionFeet:      direction,
			StartThresholdFeet: threshold,
			StationFeet:        station,
			SpeedKt:            target.GroundSpeedKt,
		},
		thresholdDistanceFeet: distance,
		lateralOffsetFeet:     lateralOffset,
	}, true
}

func approachLateralLimitFeet(rwy surfaceRunway) float32 {
	if rwy.approachLateralLimitFeet > 0 {
		return rwy.approachLateralLimitFeet
	}
	return landingMaxLateralOffsetFeet
}

func approachLandingCandidateBetter(candidate, current approachLandingCandidate) bool {
	if candidate.lateralOffsetFeet < current.lateralOffsetFeet-landingLateralTieTolerance {
		return true
	}
	if candidate.lateralOffsetFeet > current.lateralOffsetFeet+landingLateralTieTolerance {
		return false
	}
	return candidate.thresholdDistanceFeet < current.thresholdDistanceFeet
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
		monitorColors.HoldBarActive,
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

// Predictive occupied-runway alerts and trajectory-based runway conflict logic.
// Experimental ASDE-X display aid, NOT an operational runway-separation model.
// All distances are in local map feet and prediction times in seconds. These
// conservative *prototype* thresholds require validation against recordings.
const (
	conflictHorizonSeconds         = 90.0
	conflictApproachRangeFeet      = 3 * 6076.12
	conflictArrivalAlertSeconds    = 45.0
	conflictStationaryAlertSeconds = 35.0
	conflictPositionAgeSeconds     = 8.0
	conflictStaleOccupantSeconds   = 30.0
	conflictAccelerationAgeSeconds = 4.0
	conflictClearanceFeet          = 300.0
	conflictArrivalRadiusFeet      = 75.0
	conflictAircraftRadiusFeet     = 75.0
	conflictVehicleRadiusFeet      = 25.0
	conflictRunwayBufferFeet       = 35.0
	conflictSampleStepSeconds      = 0.5
	conflictDepartureRollKnots     = 20.0
	conflictDepartureAccelFtS2     = 1.0
	conflictTaxiCoastSeconds       = 10.0
	conflictMaxAccelSeconds        = 12.0
	conflictKnotsToFeetPerSecond   = 6076.12 / 3600.0
)

// occupiedRunwayAlerts deliberately separates *current occupation* from a
// predicted encounter. A lined-up stationary aircraft is not assumed to take
// off at an unknown future time. Equally, a speculative takeoff does not cause
// the runway to be classified as clear. The warning window is deliberately
// shorter for a stationary occupant to avoid premature "GO AROUND" alerts.
func (sl *SafetyLogic) occupiedRunwayAlerts(
	targets map[string]*Target, opts SafetyLogicUpdateOptions,
) map[string]SafetyAlert {
	alerts := make(map[string]SafetyAlert)
	if sl == nil || opts.Now.IsZero() {
		return alerts
	}

	for _, operation := range sl.predictiveLandingOperations(targets) {
		if operation.Type != activeRunwayOperationLanding || operation.StationFeet >= 0 ||
			operation.RunwayIndex < 0 || operation.RunwayIndex >= len(sl.runways) ||
			!runwayConfigurationAllowsOperation(opts.RunwayConfiguration, operation) {
			continue
		}
		landing := targets[operation.TargetID]
		if !validConflictTarget(landing, opts.Now, conflictPositionAgeSeconds) ||
			(opts.TargetAlertsInhibited != nil && opts.TargetAlertsInhibited(landing.ID)) {
			continue
		}
		runway := sl.runways[operation.RunwayIndex]
		runwayEnd := operationRunwayEndID(operation)
		if runwayEnd == "" {
			continue
		}

		landingAlongSpeed := float64(landing.GroundSpeedKt) * conflictKnotsToFeetPerSecond *
			float64(safetyDot(headingUnitVector(landing.HeadingDeg), operation.DirectionFeet))
		if landingAlongSpeed < 40*conflictKnotsToFeetPerSecond {
			continue
		}

		for _, other := range targets {
			if other == nil || other.ID == landing.ID || !validConflictTarget(other, opts.Now, conflictStaleOccupantSeconds) ||
				(opts.TargetAlertsInhibited != nil && opts.TargetAlertsInhibited(other.ID)) ||
				!possibleRunwayOccupant(sl, runway, other) {
				continue
			}

			// Time until the landing aircraft reaches the other object's runway
			// station. This avoids warning far too early about an aircraft near
			// the departure end of a long runway.
			otherStation := float64(safetyDot(other.PosFeet.Sub(operation.StartThresholdFeet), operation.DirectionFeet))
			tArrival := (otherStation - float64(operation.StationFeet)) / landingAlongSpeed
			if tArrival < 0 || tArrival > conflictHorizonSeconds {
				continue
			}

			stale := opts.Now.Sub(conflictObservationTime(other)).Seconds() > conflictPositionAgeSeconds
			if !runwayConflictPredicted(landing, other, runway, operation, tArrival, stale, opts.OccupiedRunwayClearanceFeet) {
				continue
			}

			id := fmt.Sprintf("SL:%d:%s:%s:%s", SafetyAlertLandingOccupiedRunway,
				landing.ID, runwayEnd, other.ID)
			audio := append([]SafetyAuralAlert{SafetyAuralWarning, SafetyAuralRunway},
				runwayIDAuralTokens(runwayEnd)...)
			audio = append(audio, SafetyAuralOccupied, SafetyAuralGoAround)
			alerts[id] = SafetyAlert{
				ID: id, Type: SafetyAlertLandingOccupiedRunway,
				MessageLines: []string{
					"RWY " + runwayEnd,
					safetyAlertTargetLabel(landing) + ", " + safetyAlertTargetLabel(other),
					"RWY OCCUPIED, GO AROUND",
				},
				AircraftIDs: []string{landing.ID, other.ID},
				RunwayIDs:   []string{runwayEnd}, AuralAlerts: audio,
			}
		}
	}
	return alerts
}

// Predict arrivals up to 3 NM without expanding the range of the existing
// runway-operation state machine (and therefore hold bars and closure alerts).
// Exactly one runway-end candidate is chosen for each additional arrival.
func (sl *SafetyLogic) predictiveLandingOperations(targets map[string]*Target) []activeRunwayOperation {
	out := make([]activeRunwayOperation, 0, len(sl.activeOperations))
	for _, op := range sl.activeOperations {
		if op.Type == activeRunwayOperationLanding {
			out = append(out, op)
		}
	}
	for _, t := range targets {
		if t == nil || !targetIsSafetyLogicAircraft(t) ||
			t.GroundSpeedKt < landingSpeedThresholdKt ||
			(t.HasAltitude && float64(t.AltitudeFt) > sl.airportAltitudeFt+landingMaxAGLFeet) {
			continue
		}
		if _, exists := sl.activeOperations[t.ID]; exists {
			continue
		}
		dir := headingUnitVector(t.HeadingDeg)
		found := false
		bestOffset := float32(0)
		var best activeRunwayOperation
		for i, r := range sl.runways {
			for _, reverse := range []bool{false, true} {
				threshold, axis := r.ThresholdMinFeet, r.AxisFeet
				if reverse {
					threshold, axis = r.ThresholdMaxFeet, r.AxisFeet.Mul(-1)
				}
				station := safetyDot(t.PosFeet.Sub(threshold), axis)
				if station >= 0 || float64(station) < -conflictApproachRangeFeet ||
					safetyDot(dir, axis) < landingAlignmentMinCos {
					continue
				}
				offset := abs32(safetyDot(t.PosFeet.Sub(threshold), r.NormalFeet))
				if offset > approachLateralLimitFeet(r) {
					continue
				}
				if !found || offset < bestOffset {
					found, bestOffset = true, offset
					best = activeRunwayOperation{
						TargetID: t.ID, RunwayID: r.ID, RunwayIndex: i,
						Type: activeRunwayOperationLanding, DirectionFeet: axis,
						StartThresholdFeet: threshold, StationFeet: station, SpeedKt: t.GroundSpeedKt,
					}
				}
			}
		}
		if found {
			out = append(out, best)
		}
	}
	return out
}

func conflictPairStillPresent(alert SafetyAlert, targets map[string]*Target, opts SafetyLogicUpdateOptions) bool {
	for i, id := range alert.AircraftIDs {
		t := targets[id]
		maxAge := conflictStaleOccupantSeconds
		if i == 0 || alert.Type == SafetyAlertConvergingRunways {
			maxAge = conflictPositionAgeSeconds
		}
		if !validConflictTarget(t, opts.Now, maxAge) ||
			(opts.TargetAlertsInhibited != nil && opts.TargetAlertsInhibited(id)) {
			return false
		}
	}
	return true
}

func validConflictTarget(t *Target, now time.Time, maxAge float64) bool {
	if t == nil || t.ID == "" || t.Suspended || t.Dropped ||
		t.Lat == 0 || t.Lon == 0 || !finiteConflictFloat(t.Lat) || !finiteConflictFloat(t.Lon) ||
		!finiteConflictFloat(float64(t.PosFeet.X)) || !finiteConflictFloat(float64(t.PosFeet.Y)) ||
		!finiteConflictFloat(float64(t.GroundSpeedKt)) {
		return false
	}
	timestamp := conflictObservationTime(t)
	if timestamp.IsZero() {
		return false
	}
	age := now.Sub(timestamp).Seconds()
	return age >= -2 && age <= maxAge
}

func conflictObservationTime(t *Target) time.Time {
	if t == nil {
		return time.Time{}
	}
	if !t.PositionReportTime.IsZero() {
		return t.PositionReportTime
	}
	return t.LastSmesFrameTime
}

func finiteConflictFloat(v float64) bool { return !stdmath.IsNaN(v) && !stdmath.IsInf(v, 0) }

func possibleRunwayOccupant(sl *SafetyLogic, r surfaceRunway, other *Target) bool {
	// Exclude objects known airborne. Do not exclude those with missing
	// altitude: many SMES ground targets lack a usable altitude.
	if other.HasAltitude && float64(other.AltitudeFt) > sl.airportAltitudeFt+300 {
		return false
	}
	if pointOnRunway(r, other.PosFeet) {
		return true
	}
	// Include aircraft whose fuselage/wing footprint overlaps the edge,
	// without treating a whole adjacent parallel taxiway as occupied.
	return distanceToRunwayPolygonFeet(r, other.PosFeet) <= conflictRunwayBufferFeet
}

func distanceToRunwayPolygonFeet(r surfaceRunway, p redsmath.Vec2) float64 {
	if pointOnRunway(r, p) {
		return 0
	}
	return redsmath.DistanceToPolygonEdges(p, r.PolygonFeet)
}

func runwayConflictPredicted(landing, occupant *Target, r surfaceRunway,
	op activeRunwayOperation, timeToStation float64, stale bool, clearanceFeet float64) bool {
	speedKt := float64(occupant.GroundSpeedKt)
	stationary := speedKt < 8 || stale
	if stationary {
		// Intent and brake release are unobservable. Do not speculate that a
		// lined-up aircraft will depart, or command a go-around 90s early.
		return timeToStation <= conflictStationaryAlertSeconds
	}
	if timeToStation > conflictArrivalAlertSeconds {
		return false
	}

	otherRadius := conflictAircraftRadiusFeet
	if classifyTarget(occupant) == targetClassVehicle {
		otherRadius = conflictVehicleRadiusFeet
	}
	if clearanceFeet <= 0 || !finiteConflictFloat(clearanceFeet) {
		clearanceFeet = conflictClearanceFeet
	}
	minimum := clearanceFeet + conflictArrivalRadiusFeet + otherRadius

	// Sample the predicted closest approach only while the landing aircraft
	// is near the runway and the occupant still intersects the protected area.
	// A 0.5-second step is bounded by 180 samples per pair; add one sample
	// radius worth of closing distance to avoid discretization false negatives.
	landingVelocity := float64(landing.GroundSpeedKt) * conflictKnotsToFeetPerSecond
	otherVelocity := speedKt * conflictKnotsToFeetPerSecond
	minimum += (landingVelocity + otherVelocity + 12*conflictMaxAccelSeconds) * conflictSampleStepSeconds / 2

	direction := headingUnitVector(landing.HeadingDeg)
	for timeAhead := 0.0; timeAhead <= conflictHorizonSeconds; timeAhead += conflictSampleStepSeconds {
		lp := redsmath.ProjectConstantAcceleration(landing.PosFeet, direction, landingVelocity, 0, 0, timeAhead)
		station := float64(safetyDot(lp.Sub(op.StartThresholdFeet), op.DirectionFeet))
		if station > float64(r.LengthFeet)+300 {
			break
		}
		if station < -minimum {
			continue
		}
		opos := projectedOccupantPoint(occupant, r, timeAhead)
		if distanceToRunwayPolygonFeet(r, opos) > conflictRunwayBufferFeet {
			continue
		}
		d := stdmath.Hypot(float64(lp.X-opos.X), float64(lp.Y-opos.Y))
		if d < minimum {
			return true
		}
	}
	return false
}

// Projection is intentionally simple and bounded. Measured acceleration is
// trusted only after a directional, accelerating takeoff roll is observed;
// otherwise taxi/crossing velocities may be extrapolated for at most 10s.
func projectedOccupantPoint(t *Target, r surfaceRunway, dt float64) redsmath.Vec2 {
	dir := headingUnitVector(t.HeadingDeg)
	accel := 0.0
	coast := stdmath.Min(dt, conflictTaxiCoastSeconds)
	if float64(t.GroundSpeedKt) >= conflictDepartureRollKnots &&
		stdmath.Abs(float64(safetyDot(dir, r.AxisFeet))) > landingAlignmentMinCos &&
		t.HasAcceleration && !t.MotionAt.IsZero() &&
		!conflictObservationTime(t).Before(t.MotionAt) &&
		conflictObservationTime(t).Sub(t.MotionAt).Seconds() <= conflictAccelerationAgeSeconds &&
		float64(t.AccelFtSec2) >= conflictDepartureAccelFtS2 {
		accel = stdmath.Min(8, float64(t.AccelFtSec2))
		coast = dt
	}
	return redsmath.ProjectConstantAcceleration(t.PosFeet, dir, float64(t.GroundSpeedKt)*conflictKnotsToFeetPerSecond, accel, conflictMaxAccelSeconds, coast)
}

// Safety alert identities use target IDs, not mutable callsigns.
func safetyAlertPairKey(firstID, secondID string) string {
	if firstID > secondID {
		firstID, secondID = secondID, firstID
	}
	return firstID + "\x00" + secondID
}

// Converging-runway conflicts are different from runway occupancy: each aircraft
// has its own operating runway, and their predicted paths must lose separation
// at the SAME time. Handle arrival/arrival, arrival/departure and
// departure/departure pairs. Restricting this to different physical runways
// also prevents duplicate warnings from the existing same-runway detector.
//
// This is an experimental visualization, not an ATC separation or clearance
// algorithm. SMES does not tell us when a lined-up departure will release its
// brakes or when an aircraft will lift off. A stationary departure is therefore
// modeled as stationary, never as a hypothetical future takeoff.
func (sl *SafetyLogic) convergingRunwayAlerts(
	targets map[string]*Target, opts SafetyLogicUpdateOptions,
) map[string]SafetyAlert {
	alerts := make(map[string]SafetyAlert)
	if sl == nil || opts.Now.IsZero() || len(sl.runways) < 2 {
		return alerts
	}

	operations := sl.convergingRunwayOperations(targets, opts)
	// One stable unordered pair per two tracks, independent of map iteration.
	sort.Slice(operations, func(i, j int) bool {
		return operations[i].TargetID < operations[j].TargetID
	})

	for i, first := range operations {
		for _, second := range operations[i+1:] {
			// Compare distinct aircraft using distinct physical runways. In
			// particular, do not exclude two departure operations: their
			// ground rolls can converge before either aircraft lifts off.
			if first.TargetID == second.TargetID || first.RunwayIndex == second.RunwayIndex {
				continue
			}
			r1, r2 := sl.runways[first.RunwayIndex], sl.runways[second.RunwayIndex]
			// Adjacent parallel runways are not converging runways.
			// Exclude only near-identical axes; small but genuine
			// convergence angles must still be evaluated by the CPA.
			if stdmath.Abs(float64(safetyDot(r1.AxisFeet, r2.AxisFeet))) > 0.999999 {
				continue
			}
			a, b := targets[first.TargetID], targets[second.TargetID]
			if !convergingRunwayConflictPredicted(first, second, a, b, r1, r2, opts.OccupiedRunwayClearanceFeet) {
				continue
			}

			// For mixed operations, the departure MUST be first in both the
			// message and the datablock-highlight list. For two departures
			// (or two arrivals), target ID order is stable across updates.
			leading, following := first, second
			if first.Type != activeRunwayOperationDeparture && second.Type == activeRunwayOperationDeparture {
				leading, following = second, first
			}
			leadingTarget, followingTarget := targets[leading.TargetID], targets[following.TargetID]

			// Prefer the arrival runway for the headline, if present. For
			// departure/departure, use the first (ID-sorted) departure's
			// runway. Retain BOTH directional runway IDs for TOWER filtering.
			headline := first
			if first.Type != activeRunwayOperationLanding && second.Type == activeRunwayOperationLanding {
				headline = second
			}
			headlineEnd := operationRunwayEndID(headline)
			firstEnd, secondEnd := operationRunwayEndID(first), operationRunwayEndID(second)
			if headlineEnd == "" || firstEnd == "" || secondEnd == "" {
				continue
			}

			id := fmt.Sprintf("SL:%d:%s:%s:%s:%s", SafetyAlertConvergingRunways,
				first.TargetID, second.TargetID, firstEnd, secondEnd)
			audio := append([]SafetyAuralAlert{SafetyAuralWarning, SafetyAuralRunway},
				runwayIDAuralTokens(headlineEnd)...)
			audio = append(audio, SafetyAuralConverging, SafetyAuralGoAround)
			alerts[id] = SafetyAlert{
				ID: id, Type: SafetyAlertConvergingRunways,
				MessageLines: []string{
					"RWY " + headlineEnd,
					safetyAlertTargetLabel(leadingTarget) + ", " + safetyAlertTargetLabel(followingTarget),
					"RWY CONVERGING, GO AROUND",
				},
				AircraftIDs: []string{leading.TargetID, following.TargetID},
				RunwayIDs:   []string{firstEnd, secondEnd},
				AuralAlerts: audio,
			}
		}
	}
	return alerts
}

// Construct exactly one runway operation per aircraft. Existing detected
// operations take precedence; predictions extend the arrival look-ahead to
// 3 NM without changing hold-bar behavior or the closed-runway state machine.
// A stationary/slow aircraft near the start of a configured departure runway
// is treated as a possible lined-up departure, not an imminent takeoff.
func (sl *SafetyLogic) convergingRunwayOperations(
	targets map[string]*Target, opts SafetyLogicUpdateOptions,
) []activeRunwayOperation {
	ops := make(map[string]activeRunwayOperation)
	for _, op := range sl.predictiveLandingOperations(targets) {
		if runwayConfigurationAllowsOperation(opts.RunwayConfiguration, op) {
			ops[op.TargetID] = op
		}
	}
	for _, op := range sl.activeOperations {
		if op.Type == activeRunwayOperationDeparture &&
			runwayConfigurationAllowsOperation(opts.RunwayConfiguration, op) {
			ops[op.TargetID] = op
		}
	}

	for _, t := range targets {
		if t == nil || !targetIsSafetyLogicAircraft(t) || t.GroundSpeedKt >= departureSpeedThresholdKt {
			continue
		}
		if _, exists := ops[t.ID]; exists {
			continue
		}
		// Never reinterpret an existing operation on a disallowed runway
		// as an inferred departure on a second crossing runway.
		if _, exists := sl.activeOperations[t.ID]; exists {
			continue
		}
		// A known high-altitude aircraft cannot be a lined-up departure.
		// Missing altitude is common for SMES surface targets.
		if t.HasAltitude && float64(t.AltitudeFt) > sl.airportAltitudeFt+departureMaxAGLFeet {
			continue
		}
		var best activeRunwayOperation
		bestAlignment := float32(landingAlignmentMinCos)
		for runwayIndex, r := range sl.runways {
			if !pointOnRunway(r, t.PosFeet) {
				continue
			}
			direction := headingUnitVector(t.HeadingDeg)
			for _, reverse := range []bool{false, true} {
				axis, threshold := r.AxisFeet, r.ThresholdMinFeet
				if reverse {
					axis, threshold = axis.Mul(-1), r.ThresholdMaxFeet
				}
				alignment := safetyDot(direction, axis)
				station := safetyDot(t.PosFeet.Sub(threshold), axis)
				// Restrict pre-roll inference to the departure end. This
				// avoids treating unrelated taxi traffic or landing rollouts
				// farther down a runway as departures.
				if alignment <= bestAlignment || station < -100 ||
					float64(station) > stdmath.Min(2500, 0.35*float64(r.LengthFeet)) {
					continue
				}
				op := activeRunwayOperation{
					TargetID: t.ID, RunwayID: r.ID, RunwayIndex: runwayIndex,
					Type: activeRunwayOperationDeparture, DirectionFeet: axis,
					StartThresholdFeet: threshold, StationFeet: station, SpeedKt: t.GroundSpeedKt,
				}
				if !runwayConfigurationAllowsOperation(opts.RunwayConfiguration, op) {
					continue
				}
				best, bestAlignment = op, alignment
			}
		}
		if best.RunwayID != "" {
			ops[t.ID] = best
		}
	}

	result := make([]activeRunwayOperation, 0, len(ops))
	for id, op := range ops {
		if op.RunwayIndex < 0 || op.RunwayIndex >= len(sl.runways) ||
			!validConflictTarget(targets[id], opts.Now, conflictPositionAgeSeconds) ||
			(opts.TargetAlertsInhibited != nil && opts.TargetAlertsInhibited(id)) {
			continue
		}
		result = append(result, op)
	}
	return result
}

// Determine whether two runway operations have a simultaneous closest point
// of approach inside the protected runway/approach corridors. The per-segment
// minimum is continuous in time (rather than relying on discrete sample hits).
func convergingRunwayConflictPredicted(
	aOp, bOp activeRunwayOperation, a, b *Target,
	aRunway, bRunway surfaceRunway, clearanceFeet float64,
) bool {
	if a == nil || b == nil {
		return false
	}
	if clearanceFeet <= 0 || !finiteConflictFloat(clearanceFeet) {
		clearanceFeet = conflictClearanceFeet
	}
	minimum := clearanceFeet + 2*conflictAircraftRadiusFeet
	horizon := conflictArrivalAlertSeconds
	// No inference of brake release for a lined-up aircraft. Restrict the
	// imminent warning horizon until there is actual observed movement.
	if (aOp.Type == activeRunwayOperationDeparture && a.GroundSpeedKt < 8) ||
		(bOp.Type == activeRunwayOperationDeparture && b.GroundSpeedKt < 8) {
		horizon = stdmath.Min(horizon, conflictStationaryAlertSeconds)
	}

	for t := 0.0; t < horizon; t += conflictSampleStepSeconds {
		dt := stdmath.Min(conflictSampleStepSeconds, horizon-t)
		a0 := projectedConvergingOperationPoint(aOp, a, aRunway, t)
		b0 := projectedConvergingOperationPoint(bOp, b, bRunway, t)
		a1 := projectedConvergingOperationPoint(aOp, a, aRunway, t+dt)
		b1 := projectedConvergingOperationPoint(bOp, b, bRunway, t+dt)
		d, u := redsmath.ClosestApproachOnLinearSegments(a0, a1, b0, b1)
		if d >= minimum {
			continue
		}

		// Check the operation's own corridor at the actual CPA time. This
		// prevents projections from creating runway conflicts far past the
		// threshold or after an aircraft has vacated its own runway.
		tau := t + u*dt
		if convergingPointOnOperation(aOp, aRunway,
			projectedConvergingOperationPoint(aOp, a, aRunway, tau)) &&
			convergingPointOnOperation(bOp, bRunway,
				projectedConvergingOperationPoint(bOp, b, bRunway, tau)) {
			return true
		}
	}
	return false
}

func projectedConvergingOperationPoint(
	op activeRunwayOperation, target *Target, r surfaceRunway, dt float64,
) redsmath.Vec2 {
	speed := float64(target.GroundSpeedKt) * conflictKnotsToFeetPerSecond
	if op.Type == activeRunwayOperationLanding {
		return redsmath.ProjectConstantAcceleration(target.PosFeet, op.DirectionFeet, speed, 0, 0, dt)
	}
	acceleration := 0.0
	if speed >= conflictDepartureRollKnots*conflictKnotsToFeetPerSecond &&
		target.HasAcceleration && !target.MotionAt.IsZero() &&
		!conflictObservationTime(target).Before(target.MotionAt) &&
		conflictObservationTime(target).Sub(target.MotionAt).Seconds() <= conflictAccelerationAgeSeconds &&
		float64(target.AccelFtSec2) >= conflictDepartureAccelFtS2 &&
		stdmath.Abs(float64(safetyDot(headingUnitVector(target.HeadingDeg), r.AxisFeet))) >= landingAlignmentMinCos {
		acceleration = stdmath.Min(8, float64(target.AccelFtSec2))
	}
	return redsmath.ProjectConstantAcceleration(target.PosFeet, op.DirectionFeet,
		speed, acceleration, conflictMaxAccelSeconds, dt)
}

func convergingPointOnOperation(op activeRunwayOperation, r surfaceRunway, p redsmath.Vec2) bool {
	station := float64(safetyDot(p.Sub(op.StartThresholdFeet), op.DirectionFeet))
	if op.Type == activeRunwayOperationLanding {
		if station < -conflictApproachRangeFeet || station > float64(r.LengthFeet)+300 {
			return false
		}
	} else if station < -100 || station > float64(r.LengthFeet)+300 {
		return false
	}
	// Reject projections that have left their own runway corridor (or final
	// approach lane). The margin here is *not* the aircraft-clearance margin.
	lateral := stdmath.Abs(float64(safetyDot(p.Sub(r.CenterFeet), r.NormalFeet)))
	return lateral <= stdmath.Max(300, float64(r.HalfWidthFeet)+conflictRunwayBufferFeet)
}
