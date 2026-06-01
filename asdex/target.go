package asdex

import (
	"encoding/json"
	stdmath "math"
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	redsnet "github.com/juliusplatzer/reds/net"
	"github.com/juliusplatzer/reds/renderer"
)

type SensorSource string

const (
	SourceUnknown SensorSource = ""
	SourceFUS     SensorSource = "FUS"
	SourceADS     SensorSource = "ADS"
	SourceMLT     SensorSource = "MLT"
)

type Target struct {
	ID string

	Callsign string
	Beacon   string
	Fix      string

	// TargetType is nil when unknown. Aircraft use ICAO type codes such as
	// "A320"; vehicles use "VEH".
	TargetType *string

	// CWT is the wake turbulence / CWT category.
	CWT string

	Lat float64
	Lon float64

	PosFeet redsmath.Vec2

	HeadingDeg    float32
	GroundSpeedKt float32

	AltitudeFt  int
	HasAltitude bool

	Source SensorSource

	Scratchpad1 string
	Scratchpad2 string

	ShowDB bool

	Suspended bool
	Coasting  bool
}

func (t *Target) EffectiveShowDB() bool {
	if t == nil || !t.ShowDB {
		return false
	}
	return targetHasDatablock(classifyTarget(t))
}

type TargetHistoryPoint struct {
	PosFeet redsmath.Vec2
}

type TargetStore struct {
	targets map[string]*Target
	order   []string

	history map[string][]TargetHistoryPoint
}

func NewTargetStore() TargetStore {
	return TargetStore{
		targets: make(map[string]*Target),
		history: make(map[string][]TargetHistoryPoint),
	}
}

func (s *TargetStore) Upsert(t Target) {
	if s == nil || t.ID == "" {
		return
	}
	if s.targets == nil {
		s.targets = make(map[string]*Target)
	}
	if s.history == nil {
		s.history = make(map[string][]TargetHistoryPoint)
	}

	if existing := s.targets[t.ID]; existing != nil {
		if existing.PosFeet != t.PosFeet {
			s.history[t.ID] = append(s.history[t.ID], TargetHistoryPoint{
				PosFeet: existing.PosFeet,
			})
			s.trimHistory(t.ID)
		}
		*existing = t
		return
	}

	targetCopy := t
	s.targets[t.ID] = &targetCopy
	s.order = append(s.order, t.ID)
}

func (s *TargetStore) Remove(id string) {
	if s == nil || s.targets == nil {
		return
	}
	if _, ok := s.targets[id]; !ok {
		return
	}

	delete(s.targets, id)
	delete(s.history, id)
	for i, orderedID := range s.order {
		if orderedID == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

func (s *TargetStore) Clear() {
	if s == nil {
		return
	}
	clear(s.targets)
	clear(s.history)
	s.order = s.order[:0]
}

func (s *TargetStore) All() []*Target {
	if s == nil || len(s.targets) == 0 {
		return nil
	}

	out := make([]*Target, 0, len(s.targets))
	for _, id := range s.order {
		if target := s.targets[id]; target != nil {
			out = append(out, target)
		}
	}
	return out
}

func (s *TargetStore) History() map[string][]TargetHistoryPoint {
	if s == nil {
		return nil
	}
	return s.history
}

func (s *TargetStore) trimHistory(id string) {
	points := s.history[id]
	if len(points) > len(targetHistoryValues) {
		s.history[id] = points[len(points)-len(targetHistoryValues):]
	}
}

type TargetDelta struct {
	Target Target
	Delete bool
}

func (s *TargetStore) ApplyDelta(delta TargetDelta) {
	if delta.Delete {
		s.Remove(delta.Target.ID)
		return
	}
	s.Upsert(delta.Target)
}

func (s *TargetStore) ApplySmesFrame(frame redsnet.SmesFrame, vm *VideoMap) {
	if s == nil || frame.Key == "" {
		return
	}
	if frame.Removed {
		s.Remove(frame.Key)
		return
	}

	target := Target{
		ID:     frame.Key,
		ShowDB: true,
	}
	if existing := s.targets[frame.Key]; existing != nil {
		target = *existing
	}

	applySmesChanged(&target, frame.Changed, vm)
	s.Upsert(target)
}

func applySmesChanged(target *Target, changed map[string]json.RawMessage, vm *VideoMap) {
	if target == nil {
		return
	}

	if value, present, clear := changedString(changed, "callsign"); present {
		target.Callsign = clearedString(value, clear)
	}
	if value, present, clear := changedString(changed, "squawk"); present {
		target.Beacon = clearedString(value, clear)
	}
	if value, present, clear := changedString(changed, "exitFix"); present {
		target.Fix = clearedString(value, clear)
	}
	if value, present, clear := changedString(changed, "wake"); present {
		target.CWT = clearedString(value, clear)
	}
	if value, present, clear := changedString(changed, "scratchpad1"); present {
		target.Scratchpad1 = normalizeScratchpad(clearedString(value, clear))
	}
	if value, present, clear := changedString(changed, "scratchpad2"); present {
		target.Scratchpad2 = normalizeScratchpad(clearedString(value, clear))
	}

	if value, present, clear := changedString(changed, "acType"); present {
		if clear || value == "" {
			target.TargetType = nil
		} else {
			target.TargetType = stringPointer(value)
		}
	}
	if value, present, clear := changedString(changed, "tgtType"); present {
		switch {
		case clear:
			target.TargetType = nil
		case strings.EqualFold(value, "vehicle"), strings.EqualFold(value, "VEH"):
			target.TargetType = stringPointer("VEH")
		case strings.EqualFold(value, "unknown") && target.TargetType == nil:
			target.TargetType = nil
		}
	}

	positionChanged := false
	if value, present, clear := changedFloat64(changed, "lat"); present {
		positionChanged = true
		if clear {
			target.Lat = 0
		} else {
			target.Lat = value
		}
	}
	if value, present, clear := changedFloat64(changed, "lon"); present {
		positionChanged = true
		if clear {
			target.Lon = 0
		} else {
			target.Lon = value
		}
	}
	if positionChanged {
		target.PosFeet = redsmath.Vec2{}
		if vm != nil && target.Lat != 0 && target.Lon != 0 {
			target.PosFeet = vm.LonLatToFeet(target.Lon, target.Lat)
		}
	}

	if value, present, clear := changedFloat64(changed, "altitude"); present {
		if clear {
			target.AltitudeFt = 0
			target.HasAltitude = false
		} else {
			target.AltitudeFt = int(stdmath.Round(value))
			target.HasAltitude = true
		}
	}
	if value, present, clear := changedFloat64(changed, "speed"); present {
		if clear {
			target.GroundSpeedKt = 0
		} else {
			target.GroundSpeedKt = float32(value)
		}
	}
	if value, present, clear := changedFloat64(changed, "heading"); present {
		if clear {
			target.HeadingDeg = 0
		} else {
			target.HeadingDeg = float32(value)
		}
	}
}

func changedString(changed map[string]json.RawMessage, key string) (string, bool, bool) {
	raw, present := changed[key]
	if !present {
		return "", false, false
	}
	if isJSONNull(raw) {
		return "", true, true
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, false
	}
	return value, true, false
}

func changedFloat64(changed map[string]json.RawMessage, key string) (float64, bool, bool) {
	raw, present := changed[key]
	if !present {
		return 0, false, false
	}
	if isJSONNull(raw) {
		return 0, true, true
	}

	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false, false
	}
	return value, true, false
}

func isJSONNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}

func clearedString(value string, clear bool) string {
	if clear {
		return ""
	}
	return value
}

func normalizeScratchpad(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "none") {
		return ""
	}
	return value
}

func stringPointer(value string) *string {
	return &value
}

type TargetDrawOptions struct {
	VectorSeconds int

	ShowHistory   bool
	HistoryLength int

	Brightness int

	ScopeRotationDeg int

	VectorVisible func(*Target) bool
}

func DrawTargets(
	targets []*Target,
	history map[string][]TargetHistoryPoint,
	cb *renderer.CmdBuffer,
	opts TargetDrawOptions,
) {
	if cb == nil {
		return
	}
	if opts.Brightness == 0 {
		opts.Brightness = brightnessDefault
	}

	addHistoryDots(targets, history, cb, opts)
	addTargetSymbols(targets, cb, opts.Brightness)
	addTargetVectors(targets, cb, opts)
}

type targetClass int

const (
	targetClassUnknown targetClass = iota
	targetClassVehicle
	targetClassAircraft
	targetClassHeavyAircraft
)

func classifyTarget(t *Target) targetClass {
	if t == nil || t.TargetType == nil || strings.TrimSpace(*t.TargetType) == "" {
		return targetClassUnknown
	}

	if strings.EqualFold(strings.TrimSpace(*t.TargetType), "VEH") {
		callsign := strings.TrimSpace(t.Callsign)
		if callsign != "" && !strings.EqualFold(callsign, "UNKN") {
			return targetClassVehicle
		}
		return targetClassUnknown
	}

	if isHeavyCWT(t.CWT) {
		return targetClassHeavyAircraft
	}
	return targetClassAircraft
}

func targetUsesUnknownPolygon(class targetClass) bool {
	return class == targetClassUnknown || class == targetClassVehicle
}

func targetHasDatablock(class targetClass) bool {
	return class == targetClassVehicle ||
		class == targetClassAircraft ||
		class == targetClassHeavyAircraft
}

func isHeavyCWT(cwt string) bool {
	switch cwt {
	case "A", "B", "C", "D", "E":
		return true
	default:
		return false
	}
}

type targetRGBRole int

const (
	targetRGBNormal targetRGBRole = iota
	targetRGBHeavy
	targetRGBUnknown
	targetRGBVehicle
	targetRGBVector
	targetRGBHighlight
	targetRGBSuspendedOuter
	targetRGBSuspendedInner
	targetRGBSuspendedSelectedInner
)

// Gildea, K. M. (2018), Development of a Standard Palette for Color Coding ATC Displays,
// FAA Office of Aerospace Medicine Technical Report DOT/FAA/AM-18/18, Appendix A,
// Table A1
var targetVehiclePink = renderer.RGB8(246, 132, 216)

func targetRGB(role targetRGBRole, brightness int) renderer.RGB {
	floor := 0

	var base renderer.RGB
	switch role {
	case targetRGBNormal:
		base = renderer.RGB8(248, 248, 248)
	case targetRGBHeavy:
		base = renderer.RGB8(248, 128, 0)
	case targetRGBUnknown:
		base = renderer.RGB8(0, 255, 255)
	case targetRGBVehicle:
		base = targetVehiclePink
	case targetRGBVector:
		base = renderer.RGB8(140, 140, 140)
	case targetRGBHighlight:
		base = renderer.RGB8(255, 255, 255)
	case targetRGBSuspendedOuter:
		base = renderer.RGB8(0, 255, 255)
		floor = 20
	case targetRGBSuspendedInner:
		base = renderer.RGB8(128, 128, 128)
		floor = 20
	case targetRGBSuspendedSelectedInner:
		base = renderer.RGB8(255, 255, 255)
		floor = 20
	default:
		base = renderer.RGB8(248, 248, 248)
	}
	return applyBrightness(base, brightness, floor)
}

func targetClassRGB(class targetClass, brightness int) renderer.RGB {
	switch class {
	case targetClassAircraft:
		return targetRGB(targetRGBNormal, brightness)
	case targetClassHeavyAircraft:
		return targetRGB(targetRGBHeavy, brightness)
	case targetClassVehicle:
		return targetRGB(targetRGBVehicle, brightness)
	default:
		return targetRGB(targetRGBUnknown, brightness)
	}
}

var targetHistoryValues = [...]uint8{219, 187, 161, 138, 118, 101, 87}

func historyRGB(age int, brightness int) renderer.RGB {
	if age < 0 {
		age = 0
	}
	if age >= len(targetHistoryValues) {
		age = len(targetHistoryValues) - 1
	}

	value := targetHistoryValues[age]
	return applyBrightness(renderer.RGB8(value, value, value), brightness, 20)
}

const feetPerDegree = float32(364560.0)

var aircraftPolygon = scaledPolygon([]redsmath.Vec2{
	{X: -0.000142545, Y: 0.0},
	{X: -0.0001607125, Y: 2.09625e-05},
	{X: -0.0001607125, Y: 6.9875e-05},
	{X: -0.000142545, Y: 6.9875e-05},
	{X: -0.0001118, Y: 2.795e-05},
	{X: -5.59e-05, Y: 2.795e-05},
	{X: -8.385e-05, Y: 0.000151293},
	{X: -5.59e-05, Y: 0.000151293},
	{X: 1.3975e-05, Y: 2.795e-05},
	{X: 0.000120185, Y: 2.38175e-05},
	{X: 0.000137155, Y: 1.53625e-05},
	{X: 0.0001439425, Y: 8.385e-06},
	{X: 0.0001439425, Y: -8.385e-06},
	{X: 0.000137155, Y: -1.53625e-05},
	{X: 0.000120185, Y: -2.38175e-05},
	{X: 1.3975e-05, Y: -2.795e-05},
	{X: -5.59e-05, Y: -0.000151293},
	{X: -8.385e-05, Y: -0.000151293},
	{X: -5.59e-05, Y: -2.795e-05},
	{X: -0.0001118, Y: -2.795e-05},
	{X: -0.000142545, Y: -6.9875e-05},
	{X: -0.0001607125, Y: -6.9875e-05},
	{X: -0.0001607125, Y: -2.09625e-05},
})

var unknownPolygon = scaledPolygon([]redsmath.Vec2{
	{X: -2.5e-05, Y: 7.5e-05},
	{X: -0.000125, Y: 0.0},
	{X: -2.5e-05, Y: -7.5e-05},
	{X: 0.000175, Y: 0.0},
})

type polygonMesh struct {
	Vertices []redsmath.Vec2
	Indices  []uint32
}

var aircraftMesh = tessellatedTargetPolygon(aircraftPolygon)

var unknownMesh = polygonMesh{
	Vertices: unknownPolygon,
	Indices:  triangleFanIndices(len(unknownPolygon)),
}

func scaledPolygon(points []redsmath.Vec2) []redsmath.Vec2 {
	out := make([]redsmath.Vec2, len(points))
	for i, point := range points {
		out[i] = point.Mul(feetPerDegree)
	}
	return out
}

func tessellatedTargetPolygon(polygon []redsmath.Vec2) polygonMesh {
	vertices, indices := renderer.TessellateRings([][]redsmath.Vec2{polygon})
	if len(vertices) == 0 || len(indices) == 0 {
		return polygonMesh{
			Vertices: polygon,
			Indices:  triangleFanIndices(len(polygon)),
		}
	}

	out := make([]redsmath.Vec2, len(vertices))
	for i, vertex := range vertices {
		out[i] = redsmath.Vec2{X: vertex.X, Y: vertex.Y}
	}
	return polygonMesh{Vertices: out, Indices: indices}
}

func triangleFanIndices(vertexCount int) []uint32 {
	if vertexCount < 3 {
		return nil
	}
	indices := make([]uint32, 0, (vertexCount-2)*3)
	for i := 1; i+1 < vertexCount; i++ {
		indices = append(indices, 0, uint32(i), uint32(i+1))
	}
	return indices
}

func addTargetSymbols(targets []*Target, cb *renderer.CmdBuffer, brightness int) {
	builders := map[targetClass]*renderer.TrianglesBuilder{
		targetClassUnknown:       renderer.GetTrianglesBuilder(),
		targetClassVehicle:       renderer.GetTrianglesBuilder(),
		targetClassAircraft:      renderer.GetTrianglesBuilder(),
		targetClassHeavyAircraft: renderer.GetTrianglesBuilder(),
	}
	defer func() {
		for _, builder := range builders {
			renderer.ReturnTrianglesBuilder(builder)
		}
	}()

	for _, target := range targets {
		if target == nil || target.Suspended {
			continue
		}

		class := classifyTarget(target)
		mesh := unknownMesh
		scale := float32(1)
		if !targetUsesUnknownPolygon(class) {
			mesh = aircraftMesh
		}
		if class == targetClassHeavyAircraft {
			scale = 1.5
		}

		addTransformedIndexed(
			builders[class],
			mesh.Vertices,
			mesh.Indices,
			target.PosFeet,
			90-target.HeadingDeg,
			scale,
		)
	}

	for _, class := range []targetClass{
		targetClassAircraft,
		targetClassHeavyAircraft,
		targetClassUnknown,
		targetClassVehicle,
	} {
		cb.SetRGB(targetClassRGB(class, brightness))
		builders[class].GenerateCommands(cb, renderer.DrawSolid, 0)
	}
}

func addTransformedIndexed(
	builder *renderer.TrianglesBuilder,
	points []redsmath.Vec2,
	indices []uint32,
	pos redsmath.Vec2,
	rotationDeg float32,
	scale float32,
) {
	if builder == nil {
		return
	}

	transformed := make([]renderer.PointVertex, 0, len(points))
	for _, point := range points {
		q := rotateScaleTranslate(point, pos, rotationDeg, scale)
		transformed = append(transformed, renderer.PointVertex{X: q.X, Y: q.Y})
	}
	builder.AddIndexed(transformed, indices)
}

func addTranslatedIndexed(
	builder *renderer.TrianglesBuilder,
	points []redsmath.Vec2,
	indices []uint32,
	pos redsmath.Vec2,
) {
	addTransformedIndexed(builder, points, indices, pos, 0, 1)
}

func rotateScaleTranslate(
	point redsmath.Vec2,
	pos redsmath.Vec2,
	rotationDeg float32,
	scale float32,
) redsmath.Vec2 {
	radians := float64(rotationDeg) * stdmath.Pi / 180
	cos := float32(stdmath.Cos(radians))
	sin := float32(stdmath.Sin(radians))

	x := point.X * scale
	y := point.Y * scale

	return redsmath.Vec2{
		X: pos.X + x*cos - y*sin,
		Y: pos.Y + x*sin + y*cos,
	}
}

const (
	minTargetVectorSeconds = 1
	maxTargetVectorSeconds = 20
)

func ClampedTargetVectorSeconds(seconds int) int {
	if seconds < minTargetVectorSeconds {
		return minTargetVectorSeconds
	}
	if seconds > maxTargetVectorSeconds {
		return maxTargetVectorSeconds
	}
	return seconds
}

func vectorEndFeet(
	start redsmath.Vec2,
	groundSpeedKt float32,
	trackDeg float32,
	vectorSeconds int,
) redsmath.Vec2 {
	distanceNM := groundSpeedKt * float32(vectorSeconds) / 3600
	distanceFeet := distanceNM * feetPerNM
	radians := float64(trackDeg) * stdmath.Pi / 180

	return redsmath.Vec2{
		X: start.X + distanceFeet*float32(stdmath.Sin(radians)),
		Y: start.Y + distanceFeet*float32(stdmath.Cos(radians)),
	}
}

func addTargetVectors(
	targets []*Target,
	cb *renderer.CmdBuffer,
	opts TargetDrawOptions,
) {
	builder := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(builder)

	vectorSeconds := ClampedTargetVectorSeconds(opts.VectorSeconds)
	for _, target := range targets {
		if target == nil || target.Suspended || target.Coasting || target.GroundSpeedKt <= 0 {
			continue
		}
		if opts.VectorVisible != nil && !opts.VectorVisible(target) {
			continue
		}

		end := vectorEndFeet(
			target.PosFeet,
			target.GroundSpeedKt,
			target.HeadingDeg,
			vectorSeconds,
		)
		builder.AddLine(
			renderer.PointVertex{X: target.PosFeet.X, Y: target.PosFeet.Y},
			renderer.PointVertex{X: end.X, Y: end.Y},
		)
	}

	cb.SetRGB(targetRGB(targetRGBVector, opts.Brightness))
	cb.LineWidth(1)
	builder.GenerateCommands(cb)
}

const historyDotRadiusFeet = 0.003 * feetPerNM

func addHistoryDots(
	targets []*Target,
	history map[string][]TargetHistoryPoint,
	cb *renderer.CmdBuffer,
	opts TargetDrawOptions,
) {
	if !opts.ShowHistory {
		return
	}

	maxHistory := opts.HistoryLength
	if maxHistory < 1 {
		maxHistory = 1
	}
	if maxHistory > len(targetHistoryValues) {
		maxHistory = len(targetHistoryValues)
	}

	dotPolygon := circlePolygon(historyDotRadiusFeet, 12)
	dotIndices := circleFanIndices(12)

	for colorIndex := 0; colorIndex < maxHistory; colorIndex++ {
		builder := renderer.GetTrianglesBuilder()

		for _, target := range targets {
			if target == nil {
				continue
			}

			points := history[target.ID]
			count := len(points)
			if count > maxHistory {
				count = maxHistory
			}

			start := len(points) - count
			for i := 0; i < count; i++ {
				ageFromNewest := count - 1 - i
				if ageFromNewest == colorIndex {
					addTranslatedIndexed(builder, dotPolygon, dotIndices, points[start+i].PosFeet)
				}
			}
		}

		cb.SetRGB(historyRGB(colorIndex, opts.Brightness))
		builder.GenerateCommands(cb, renderer.DrawSolid, 0)
		renderer.ReturnTrianglesBuilder(builder)
	}
}

func circlePolygon(radius float32, segments int) []redsmath.Vec2 {
	if radius <= 0 || segments < 3 {
		return nil
	}

	points := make([]redsmath.Vec2, 1, segments+1)
	for i := 0; i < segments; i++ {
		radians := float64(i) / float64(segments) * 2 * stdmath.Pi
		points = append(points, redsmath.Vec2{
			X: radius * float32(stdmath.Cos(radians)),
			Y: radius * float32(stdmath.Sin(radians)),
		})
	}
	return points
}

func circleFanIndices(segments int) []uint32 {
	if segments < 3 {
		return nil
	}

	indices := make([]uint32, 0, segments*3)
	for i := 0; i < segments; i++ {
		indices = append(indices, 0, uint32(i+1), uint32((i+1)%segments+1))
	}
	return indices
}
