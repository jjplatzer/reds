package asdex

import (
	"encoding/json"
	"fmt"
	stdmath "math"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util"
)

const (
	closedRunwayXAngleDeg         = 15.0
	closedRunwayBrightnessDefault = 95

	maxTempDataObjects = 100
	maxTempAreaNodes   = 20
	minTempAreaNodes   = 3

	tempMapAreasBrightnessDefault = 95
	tempAreaDrawLineWidth         = 1.0

	tempTextMaxLineLength     = 16
	tempTextFontSizeDefault   = 2
	tempTextBrightnessDefault = 95
	tempTextLineSpacing       = 2
	tempTextStepPx            = 15
	tempTextZeroLengthPx      = 10

	tempDataTextHoverRangeFeet float32 = 150.0
)

var tempTextAnchorGeoOffsets = []struct {
	lat float64
	lon float64
}{
	{lat: 9.9e-05, lon: 4.5e-06},
	{lat: 2.25e-05, lon: 3.15e-05},
	{lat: 2.25e-05, lon: 0.0001125},
	{lat: -2.7e-05, lon: 4.95e-05},
	{lat: -0.0001035, lon: 7.2e-05},
	{lat: -5.85e-05, lon: 4.5e-06},
	{lat: -0.0001035, lon: -6.3e-05},
	{lat: -2.7e-05, lon: -4.05e-05},
	{lat: 2.25e-05, lon: -0.0001035},
	{lat: 2.25e-05, lon: -2.25e-05},
	{lat: 9.9e-05, lon: 4.5e-06},
}

var tempTextAnchorOffsetsFeet = makeTempTextAnchorOffsetsFeet()

func makeTempTextAnchorOffsetsFeet() []redsmath.Vec2 {
	out := make([]redsmath.Vec2, 0, len(tempTextAnchorGeoOffsets))
	for _, p := range tempTextAnchorGeoOffsets {
		out = append(out, redsmath.Vec2{
			// CRC compensates longitude scale when drawing this symbol. In
			// local feet, use one degree = 60 NM for both axes.
			X: float32(p.lon * 60.0 * redsmath.FeetPerNM),
			Y: float32(p.lat * 60.0 * redsmath.FeetPerNM),
		})
	}
	return out
}

type TempDataType int

const (
	TempDataClosedArea TempDataType = iota
	TempDataRestrictedArea
)

type TempDataSelectMode int

const (
	TempDataSelectNone TempDataSelectMode = iota
	TempDataSelectDeleteGlobal
	TempDataSelectHide
)

type TempDataHitType int

const (
	TempDataHitNone TempDataHitType = iota
	TempDataHitText
	TempDataHitClosedArea
	TempDataHitRestrictedArea
)

type TempDataHit struct {
	Type  TempDataHitType
	Index int
	ID    string
}

type TempData struct {
	closedRunways map[string]bool

	closedAreas     []TempArea
	restrictedAreas []TempArea
	// Preview is transient; created graph pavement areas use ordinary TEMP DATA
	// collections so DCB DELETE GLOBAL continues to work for them.
	graphPreviewAreas []TempArea
	texts             []TempText

	nextAreaID int
	nextTextID int
}

type TempArea struct {
	ID     string
	Type   TempDataType
	Points []redsmath.Vec2
	Holes  [][]redsmath.Vec2 // graph-derived geometry may contain pavement islands
	Bounds redsmath.Rect

	Hidden      bool
	Highlighted bool

	meshVertices []renderer.PointVertex
	meshIndices  []uint32
}

type TempAreaDraft struct {
	Type   TempDataType
	Points []redsmath.Vec2
	Mouse  redsmath.Vec2
}

type TempText struct {
	ID string

	Location redsmath.Vec2

	Line1 string
	Line2 string

	Hidden      bool
	Highlighted bool

	ShowDataBlock *bool

	LeaderDirection *LeaderDirection
	LeaderLength    *int
	FontSize        *int
	Brightness      *int
}

type TempTextCommand struct {
	line1 string
	line2 string

	activeLine int
	cursor     int
}

type TempTextPlacementCommand struct {
	line1 string
	line2 string
}

func NewTempData() TempData {
	return TempData{
		closedRunways: make(map[string]bool),
		nextAreaID:    1,
		nextTextID:    1,
	}
}

func NewTempTextCommand() *TempTextCommand {
	return &TempTextCommand{
		activeLine: 1,
		cursor:     0,
	}
}

func (cmd *TempTextCommand) DisplayLines() []string {
	if cmd == nil {
		return nil
	}
	return []string{
		"TEMP DATA",
		"DEFINE TEXT",
		">: " + cmd.line1,
		">: " + cmd.line2,
	}
}

func (cmd *TempTextCommand) CursorLine() int {
	if cmd == nil {
		return 0
	}
	if cmd.activeLine == 2 {
		return 4
	}
	return 3
}

func (cmd *TempTextCommand) CursorColumn() int {
	if cmd == nil {
		return 0
	}
	return 3 + cmd.cursor
}

func (cmd *TempTextCommand) ActiveText() string {
	if cmd == nil {
		return ""
	}
	if cmd.activeLine == 2 {
		return cmd.line2
	}
	return cmd.line1
}

func (cmd *TempTextCommand) SetActiveText(text string) {
	if cmd == nil {
		return
	}
	if cmd.activeLine == 2 {
		cmd.line2 = text
		return
	}
	cmd.line1 = text
}

func (cmd *TempTextCommand) Insert(r rune) {
	if cmd == nil || r < 32 || r == 127 {
		return
	}

	r = unicode.ToUpper(r)
	text := []rune(cmd.ActiveText())
	if len(text) >= tempTextMaxLineLength {
		return
	}

	cmd.cursor = clampInt(cmd.cursor, 0, len(text))
	text = append(text[:cmd.cursor], append([]rune{r}, text[cmd.cursor:]...)...)
	cmd.SetActiveText(string(text))
	cmd.cursor++
}

func (cmd *TempTextCommand) Backspace() {
	if cmd == nil || cmd.cursor <= 0 {
		return
	}

	text := []rune(cmd.ActiveText())
	cmd.cursor = clampInt(cmd.cursor, 0, len(text))
	if cmd.cursor <= 0 {
		return
	}

	cmd.cursor--
	text = append(text[:cmd.cursor], text[cmd.cursor+1:]...)
	cmd.SetActiveText(string(text))
}

func (cmd *TempTextCommand) DeleteForward() {
	if cmd == nil {
		return
	}

	text := []rune(cmd.ActiveText())
	cmd.cursor = clampInt(cmd.cursor, 0, len(text))
	if cmd.cursor >= len(text) {
		return
	}

	text = append(text[:cmd.cursor], text[cmd.cursor+1:]...)
	cmd.SetActiveText(string(text))
}

func (cmd *TempTextCommand) MoveLeft() {
	if cmd != nil && cmd.cursor > 0 {
		cmd.cursor--
	}
}

func (cmd *TempTextCommand) MoveRight() {
	if cmd == nil {
		return
	}
	text := []rune(cmd.ActiveText())
	if cmd.cursor < len(text) {
		cmd.cursor++
	}
}

func (cmd *TempTextCommand) MoveUp() {
	if cmd == nil || cmd.activeLine == 1 {
		return
	}
	cmd.activeLine = 1
	cmd.cursor = clampInt(cmd.cursor, 0, len([]rune(cmd.line1)))
}

func (cmd *TempTextCommand) MoveDown() {
	if cmd == nil || cmd.activeLine == 2 {
		return
	}
	cmd.activeLine = 2
	cmd.cursor = clampInt(cmd.cursor, 0, len([]rune(cmd.line2)))
}

func (cmd *TempTextPlacementCommand) DisplayLines() []string {
	if cmd == nil {
		return nil
	}
	return []string{"TEMP DATA", "DEFINE TEXT"}
}

func (td *TempData) DcbRunwayClosureStates(sl *SafetyLogic) []DcbRunwayClosureState {
	if td == nil || sl == nil {
		return nil
	}

	out := make([]DcbRunwayClosureState, 0, len(sl.runways))
	for _, rwy := range sl.runways {
		out = append(out, DcbRunwayClosureState{
			ID:       rwy.ID,
			IsClosed: td.closedRunways != nil && td.closedRunways[rwy.ID],
		})
	}
	return out
}

func (td *TempData) ToggleRunwayClosedByDcbIndex(sl *SafetyLogic, index int) bool {
	if td == nil || sl == nil || index < 1 || index > len(sl.runways) {
		return false
	}

	rwy := sl.runways[index-1]
	if rwy.ID == "" {
		return false
	}

	if td.closedRunways == nil {
		td.closedRunways = make(map[string]bool)
	}
	if td.closedRunways[rwy.ID] {
		delete(td.closedRunways, rwy.ID)
	} else {
		td.closedRunways[rwy.ID] = true
	}
	return true
}

func (td *TempData) RunwayClosed(id string) bool {
	if td == nil || td.closedRunways == nil {
		return false
	}
	id = strings.ToUpper(strings.TrimSpace(id))
	return td.closedRunways[id]
}

func (td *TempData) VisibleObjectCount() int {
	if td == nil {
		return 0
	}
	ids := make(map[string]bool)
	for _, area := range td.closedAreas {
		if !area.Hidden {
			ids[area.ID] = true
		}
	}
	for _, area := range td.restrictedAreas {
		if !area.Hidden {
			ids[area.ID] = true
		}
	}
	for _, text := range td.texts {
		if !text.Hidden {
			ids[text.ID] = true
		}
	}
	return len(ids)
}

func (td *TempData) AddArea(areaType TempDataType, points []redsmath.Vec2) {
	if td == nil || len(points) < minTempAreaNodes+1 {
		return
	}

	vertices, indices := renderer.TessellateRings([][]redsmath.Vec2{points})
	if len(vertices) == 0 || len(indices) == 0 {
		return
	}

	area := TempArea{
		ID:           td.nextTempAreaID(areaType),
		Type:         areaType,
		Points:       append([]redsmath.Vec2(nil), points...),
		Bounds:       boundsForTempPolygon(points),
		meshVertices: vertices,
		meshIndices:  indices,
	}

	switch areaType {
	case TempDataClosedArea:
		td.closedAreas = append(td.closedAreas, area)
	case TempDataRestrictedArea:
		td.restrictedAreas = append(td.restrictedAreas, area)
	}
}

// SetGraphAreaPreview replaces any earlier graph preview. It never changes
// committed temporary areas and is discarded when the native editor closes.
func (td *TempData) SetGraphAreaPreview(areaType TempDataType, polygons []graphAreaPolygon) int {
	if td == nil {
		return 0
	}
	td.graphPreviewAreas = buildGraphTempAreas(areaType, polygons)
	return len(td.graphPreviewAreas)
}

func (td *TempData) ClearGraphAreaPreview() {
	if td != nil {
		td.graphPreviewAreas = nil
	}
}

// CommitGraphAreas adds the selected graph overlay as a single logical TEMP DATA
// object. Its component pavement polygons share an ID for DCB deletion.
func (td *TempData) CommitGraphAreas(areaType TempDataType, polygons []graphAreaPolygon) int {
	if td == nil {
		return 0
	}
	areas := buildGraphTempAreas(areaType, polygons)
	if len(areas) == 0 {
		return 0
	}
	// One logical closure may consist of several disjoint pavement components. A common ID
	// lets clicking any segment highlight/delete the entire area via DCB.
	id := td.nextTempAreaID(areaType)
	for i := range areas {
		areas[i].ID = id
	}
	switch areaType {
	case TempDataClosedArea:
		td.closedAreas = append(td.closedAreas, areas...)
	case TempDataRestrictedArea:
		td.restrictedAreas = append(td.restrictedAreas, areas...)
	}
	td.graphPreviewAreas = nil
	return len(areas)
}

func buildGraphTempAreas(areaType TempDataType, polygons []graphAreaPolygon) []TempArea {
	out := make([]TempArea, 0, len(polygons))
	for _, polygon := range polygons {
		points := polygon.Outer
		if len(points) < 4 {
			continue
		}
		rings := make([][]redsmath.Vec2, 0, len(polygon.Holes)+1)
		rings = append(rings, polygon.Outer)
		rings = append(rings, polygon.Holes...)
		verts, indices := renderer.TessellateRings(rings)
		if len(verts) == 0 || len(indices) == 0 {
			continue
		}
		out = append(out, TempArea{
			Type: areaType, Points: append([]redsmath.Vec2(nil), points...), Holes: polygon.Holes,
			Bounds: boundsForTempPolygon(points), meshVertices: verts, meshIndices: indices,
		})
	}
	return out
}

func boundsForTempPolygon(points []redsmath.Vec2) redsmath.Rect {
	if len(points) == 0 {
		return redsmath.Rect{}
	}

	minX := points[0].X
	maxX := points[0].X
	minY := points[0].Y
	maxY := points[0].Y
	for _, point := range points[1:] {
		if point.X < minX {
			minX = point.X
		}
		if point.X > maxX {
			maxX = point.X
		}
		if point.Y < minY {
			minY = point.Y
		}
		if point.Y > maxY {
			maxY = point.Y
		}
	}

	return redsmath.NewRect(minX, minY, maxX, maxY)
}

func (td *TempData) nextTempAreaID(areaType TempDataType) string {
	if td.nextAreaID <= 0 {
		td.nextAreaID = 1
	}

	prefix := "CLOSED"
	if areaType == TempDataRestrictedArea {
		prefix = "RESTR"
	}

	id := fmt.Sprintf("%s:%d", prefix, td.nextAreaID)
	td.nextAreaID++
	return id
}

func (td *TempData) AddText(location redsmath.Vec2, line1 string, line2 string) {
	if td == nil {
		return
	}

	line1 = strings.TrimSpace(line1)
	line2 = strings.TrimSpace(line2)
	if line1 == "" {
		return
	}

	text := TempText{
		ID:       td.nextTempTextID(),
		Location: location,
		Line1:    line1,
		Line2:    line2,
	}
	td.texts = append(td.texts, text)
}

func (td *TempData) HitTest(world redsmath.Vec2) TempDataHit {
	if td == nil {
		return TempDataHit{Type: TempDataHitNone, Index: -1}
	}
	if hit := td.hitTestText(world); hit.Type != TempDataHitNone {
		return hit
	}
	if hit := td.hitTestAreas(world); hit.Type != TempDataHitNone {
		return hit
	}
	return TempDataHit{Type: TempDataHitNone, Index: -1}
}

func (td *TempData) hitTestText(world redsmath.Vec2) TempDataHit {
	if td == nil {
		return TempDataHit{Type: TempDataHitNone, Index: -1}
	}

	bestIndex := -1
	bestDistance2 := tempDataTextHoverRangeFeet * tempDataTextHoverRangeFeet
	for i := range td.texts {
		text := &td.texts[i]
		if text.Hidden {
			continue
		}

		distance2 := tempDistance2(text.Location, world)
		if distance2 <= bestDistance2 {
			bestDistance2 = distance2
			bestIndex = i
		}
	}

	if bestIndex < 0 {
		return TempDataHit{Type: TempDataHitNone, Index: -1}
	}
	return TempDataHit{
		Type:  TempDataHitText,
		Index: bestIndex,
		ID:    td.texts[bestIndex].ID,
	}
}

func (td *TempData) hitTestAreas(world redsmath.Vec2) TempDataHit {
	if td == nil {
		return TempDataHit{Type: TempDataHitNone, Index: -1}
	}

	for i := len(td.closedAreas) - 1; i >= 0; i-- {
		if tempAreaContains(td.closedAreas[i], world) {
			return TempDataHit{
				Type:  TempDataHitClosedArea,
				Index: i,
				ID:    td.closedAreas[i].ID,
			}
		}
	}
	for i := len(td.restrictedAreas) - 1; i >= 0; i-- {
		if tempAreaContains(td.restrictedAreas[i], world) {
			return TempDataHit{
				Type:  TempDataHitRestrictedArea,
				Index: i,
				ID:    td.restrictedAreas[i].ID,
			}
		}
	}
	return TempDataHit{Type: TempDataHitNone, Index: -1}
}

func tempAreaContains(area TempArea, world redsmath.Vec2) bool {
	if area.Hidden || area.Bounds.Empty() {
		return false
	}
	if world.X < area.Bounds.Min.X || world.X > area.Bounds.Max.X ||
		world.Y < area.Bounds.Min.Y || world.Y > area.Bounds.Max.Y {
		return false
	}
	if !pointInPolygon(area.Points, world) {
		return false
	}
	for _, hole := range area.Holes {
		if pointInPolygon(hole, world) {
			return false
		}
	}
	return true
}

func tempDistance2(a, b redsmath.Vec2) float32 {
	dx := a.X - b.X
	dy := a.Y - b.Y
	return dx*dx + dy*dy
}

func (td *TempData) ToggleHighlight(hit TempDataHit) bool {
	if td == nil || hit.Index < 0 {
		return false
	}

	switch hit.Type {
	case TempDataHitText:
		if hit.Index >= len(td.texts) {
			return false
		}
		td.texts[hit.Index].Highlighted = !td.texts[hit.Index].Highlighted
		return true
	case TempDataHitClosedArea:
		if hit.Index >= len(td.closedAreas) {
			return false
		}
		id := td.closedAreas[hit.Index].ID
		value := !td.closedAreas[hit.Index].Highlighted
		for i := range td.closedAreas {
			if td.closedAreas[i].ID == id {
				td.closedAreas[i].Highlighted = value
			}
		}
		return true
	case TempDataHitRestrictedArea:
		if hit.Index >= len(td.restrictedAreas) {
			return false
		}
		id := td.restrictedAreas[hit.Index].ID
		value := !td.restrictedAreas[hit.Index].Highlighted
		for i := range td.restrictedAreas {
			if td.restrictedAreas[i].ID == id {
				td.restrictedAreas[i].Highlighted = value
			}
		}
		return true
	default:
		return false
	}
}

func (td *TempData) DeleteHighlightedGlobal() {
	if td == nil {
		return
	}

	td.closedAreas = filterTempAreas(td.closedAreas, func(area TempArea) bool {
		return !area.Highlighted
	})
	td.restrictedAreas = filterTempAreas(td.restrictedAreas, func(area TempArea) bool {
		return !area.Highlighted
	})
	td.texts = filterTempTexts(td.texts, func(text TempText) bool {
		return !text.Highlighted
	})
}

func filterTempAreas(in []TempArea, keep func(TempArea) bool) []TempArea {
	out := in[:0]
	for _, area := range in {
		if keep(area) {
			out = append(out, area)
		}
	}
	return out
}

func filterTempTexts(in []TempText, keep func(TempText) bool) []TempText {
	out := in[:0]
	for _, text := range in {
		if keep(text) {
			out = append(out, text)
		}
	}
	return out
}

func (td *TempData) ClearHighlights() {
	if td == nil {
		return
	}

	for i := range td.closedAreas {
		td.closedAreas[i].Highlighted = false
	}
	for i := range td.restrictedAreas {
		td.restrictedAreas[i].Highlighted = false
	}
	for i := range td.texts {
		td.texts[i].Highlighted = false
	}
}

func (td *TempData) nextTempTextID() string {
	if td.nextTextID <= 0 {
		td.nextTextID = 1
	}

	id := fmt.Sprintf("TEXT:%d", td.nextTextID)
	td.nextTextID++
	return id
}

func (td *TempData) DrawClosedRunways(
	cb *renderer.CmdBuffer,
	sl *SafetyLogic,
	brightness int,
) {
	if td == nil || sl == nil || cb == nil || len(td.closedRunways) == 0 {
		return
	}

	builder := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(builder)

	for _, rwy := range sl.runways {
		if !td.closedRunways[rwy.ID] {
			continue
		}
		buildClosedRunwayXLines(builder, rwy)
	}

	cb.SetRGB(applyBrightness(monitorColors.TempClosedRunway, brightness, brightnessFloorDefault))
	cb.LineWidth(1)
	builder.GenerateCommands(cb)
}

func (td *TempData) DrawClosedAreas(
	cb *renderer.CmdBuffer,
	transforms radar.ScopeTransformations,
	brightness int,
) {
	if td == nil {
		return
	}

	td.drawAreas(cb, transforms, td.closedAreas, monitorColors.TempClosedArea, brightness)
	if len(td.graphPreviewAreas) > 0 && td.graphPreviewAreas[0].Type == TempDataClosedArea {
		td.drawAreas(cb, transforms, td.graphPreviewAreas, monitorColors.TempClosedArea, brightness)
	}
}

func (td *TempData) DrawRestrictedAreas(
	cb *renderer.CmdBuffer,
	transforms radar.ScopeTransformations,
	brightness int,
) {
	if td == nil {
		return
	}

	td.drawAreas(cb, transforms, td.restrictedAreas, monitorColors.TempRestrictedArea, brightness)
	if len(td.graphPreviewAreas) > 0 && td.graphPreviewAreas[0].Type == TempDataRestrictedArea {
		td.drawAreas(cb, transforms, td.graphPreviewAreas, monitorColors.TempRestrictedArea, brightness)
	}
}

func (td *TempData) drawAreas(
	cb *renderer.CmdBuffer,
	transforms radar.ScopeTransformations,
	areas []TempArea,
	rgb renderer.RGB,
	brightness int,
) {
	if td == nil || cb == nil || len(areas) == 0 {
		return
	}

	for _, area := range areas {
		if area.Hidden || len(area.Points) < 2 {
			continue
		}

		color := tempAreaColor(area, rgb, brightness)
		lineBuilder := renderer.GetLinesBuilder()
		for _, ring := range append([][]redsmath.Vec2{area.Points}, area.Holes...) {
			points := make([]renderer.PointVertex, 0, len(ring))
			for _, pt := range ring {
				points = append(points, renderer.PointVertex{X: pt.X, Y: pt.Y})
			}
			lineBuilder.AddLineStrip(points)
		}

		cb.SetRGB(color)
		cb.LineWidth(1)
		lineBuilder.GenerateCommands(cb)
		renderer.ReturnLinesBuilder(lineBuilder)

		if len(area.meshVertices) == 0 || len(area.meshIndices) == 0 {
			continue
		}

		triBuilder := renderer.GetTrianglesBuilder()
		triBuilder.AddIndexed(area.meshVertices, area.meshIndices)
		cb.SetRGB(color)
		triBuilder.GenerateCommands(cb, renderer.DrawHatched, tempAreaHatchOffset(area, transforms))
		renderer.ReturnTrianglesBuilder(triBuilder)
	}
}

func tempAreaColor(area TempArea, normal renderer.RGB, brightness int) renderer.RGB {
	if area.Highlighted {
		return applyBrightness(monitorColors.TempAreaHighlight, brightness, brightnessFloorDefault)
	}
	return applyBrightness(normal, brightness, brightnessFloorDefault)
}

func tempAreaHatchOffset(area TempArea, transforms radar.ScopeTransformations) float32 {
	if len(area.Points) == 0 {
		return 0
	}

	p := transforms.WindowFromWorldP(area.Points[0])
	return -float32(stdmath.Mod(float64(4*p.Y+p.X), 50))
}

func (td *TempData) DrawTempTextAnchors(
	cb *renderer.CmdBuffer,
	transforms radar.ScopeTransformations,
	brightness int,
) {
	if td == nil || cb == nil || len(td.texts) == 0 {
		return
	}

	pixelsPerFoot := tempTextPixelsPerFoot(transforms)
	if pixelsPerFoot <= 0 {
		return
	}

	for _, text := range td.texts {
		if text.Hidden {
			continue
		}
		builder := renderer.GetLinesBuilder()
		addTempTextAnchor(builder, transforms.WindowFromWorldP(text.Location), pixelsPerFoot)

		cb.SetRGB(applyBrightness(tempTextColor(text), brightness, brightnessFloorDefault))
		cb.LineWidth(1)
		builder.GenerateCommands(cb)
		renderer.ReturnLinesBuilder(builder)
	}
}

func tempTextPixelsPerFoot(transforms radar.ScopeTransformations) float32 {
	unit := transforms.WindowFromWorldV(redsmath.Vec2{X: 1, Y: 0})
	return float32(stdmath.Hypot(float64(unit.X), float64(unit.Y)))
}

func addTempTextAnchor(
	builder *renderer.LinesBuilder,
	center redsmath.Vec2,
	pixelsPerFoot float32,
) {
	if builder == nil || pixelsPerFoot <= 0 {
		return
	}

	points := make([]renderer.PointVertex, 0, len(tempTextAnchorOffsetsFeet))
	for _, offset := range tempTextAnchorOffsetsFeet {
		points = append(points, renderer.PointVertex{
			X: center.X + offset.X*pixelsPerFoot,
			// Local feet Y is north/up; window Y is down.
			Y: center.Y - offset.Y*pixelsPerFoot,
		})
	}
	builder.AddLineStrip(points)
}

func (td *TempData) DrawTempTexts(
	cb *renderer.CmdBuffer,
	transforms radar.ScopeTransformations,
	font *renderer.BitmapFont,
	textureForSize func(size int) renderer.TextureID,
	settings DataBlockSettings,
	defaultFontSize int,
	defaultBrightness int,
) {
	if td == nil || cb == nil || font == nil || textureForSize == nil || len(td.texts) == 0 {
		return
	}

	fontSize := clampInt(defaultFontSize, 1, 6)
	textureID := textureForSize(fontSize)
	if textureID == 0 {
		return
	}

	tdb := renderer.GetTextDrawBuilder()
	tdb.SetFont(font)

	for _, text := range td.texts {
		if text.Hidden {
			continue
		}

		color := applyBrightness(tempTextColor(text), tempTextBrightness(text, defaultBrightness), brightnessFloorDefault)
		lineBuilder := renderer.GetLinesBuilder()
		drawOneTempText(text, transforms, lineBuilder, tdb, font, fontSize, settings, color)
		cb.SetRGB(color)
		cb.LineWidth(1)
		lineBuilder.GenerateCommands(cb)
		renderer.ReturnLinesBuilder(lineBuilder)
	}

	tdb.GenerateCommands(cb, textureID)

	renderer.ReturnTextDrawBuilder(tdb)
}

func tempTextColor(text TempText) renderer.RGB {
	if text.Highlighted {
		return monitorColors.TempTextHighlight
	}
	return monitorColors.TempText
}

func tempTextBrightness(text TempText, defaultBrightness int) int {
	if text.Brightness == nil {
		return clampBrightness(defaultBrightness)
	}
	return clampBrightness(*text.Brightness)
}

func drawOneTempText(
	text TempText,
	transforms radar.ScopeTransformations,
	lineBuilder *renderer.LinesBuilder,
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	fontSize int,
	settings DataBlockSettings,
	color renderer.RGB,
) {
	if strings.TrimSpace(text.Line1) == "" {
		return
	}

	anchor := transforms.WindowFromWorldP(text.Location)

	direction := settings.LeaderDirection
	if text.LeaderDirection != nil {
		direction = *text.LeaderDirection
	}

	leaderLength := settings.LeaderLength
	if text.LeaderLength != nil {
		leaderLength = *text.LeaderLength
	}

	if text.FontSize != nil && *text.FontSize > 0 {
		fontSize = *text.FontSize
	}

	heading := leaderHeadingDegrees(direction)
	left := isLeftDatablock(direction)

	leaderLengthPx := leaderLength * tempTextStepPx
	if leaderLengthPx < 0 {
		leaderLengthPx = 0
	}

	leaderStart := anchor
	anchorDistance := float32(leaderLengthPx)
	if leaderLengthPx == 0 {
		anchorDistance = tempTextZeroLengthPx
	}
	leaderEnd := anchor.Add(leaderDelta(anchorDistance, heading))

	if leaderLengthPx > 0 {
		lineBuilder.AddLine(
			renderer.PointVertex{X: leaderStart.X, Y: leaderStart.Y},
			renderer.PointVertex{X: leaderEnd.X, Y: leaderEnd.Y},
		)
	}

	line1Width, height := font.MeasureText(text.Line1, fontSize)
	line2Width, _ := font.MeasureText(text.Line2, fontSize)
	if height <= 0 {
		return
	}

	maxWidth := line1Width
	if line2Width > maxWidth {
		maxWidth = line2Width
	}

	textX := int(leaderEnd.X)
	if left {
		textX += -2 - maxWidth
	} else {
		textX += 2
	}
	textY := int(leaderEnd.Y) - height/2

	style := renderer.TextStyle{
		Size:  fontSize,
		Color: color.ToRGBA(),
	}

	pos := redsmath.Vec2{X: float32(textX), Y: float32(textY)}
	td.AddText(text.Line1, pos, style)

	if strings.TrimSpace(text.Line2) != "" {
		pos.Y += float32(height + tempTextLineSpacing)
		td.AddText(text.Line2, pos, style)
	}
}

func buildClosedRunwayXLines(
	builder *renderer.LinesBuilder,
	rwy surfaceRunway,
) {
	if builder == nil || rwy.LengthFeet <= 0 {
		return
	}

	c0 := runwayCorner(rwy, rwy.MinAlongFeet, rwy.MinAcrossFeet)
	c1 := runwayCorner(rwy, rwy.MaxAlongFeet, rwy.MinAcrossFeet)
	c2 := runwayCorner(rwy, rwy.MaxAlongFeet, rwy.MaxAcrossFeet)
	c3 := runwayCorner(rwy, rwy.MinAlongFeet, rwy.MaxAcrossFeet)

	angle := float32(closedRunwayXAngleDeg * stdmath.Pi / 180)
	addClosedXLine(builder, c0, closedXDirection(rwy, 1, 1, angle), c3, c2)
	addClosedXLine(builder, c1, closedXDirection(rwy, -1, 1, angle), c3, c2)
	addClosedXLine(builder, c2, closedXDirection(rwy, -1, -1, angle), c0, c1)
	addClosedXLine(builder, c3, closedXDirection(rwy, 1, -1, angle), c0, c1)
}

func closedXDirection(
	rwy surfaceRunway,
	alongSign float32,
	acrossSign float32,
	angleRad float32,
) redsmath.Vec2 {
	direction := rwy.AxisFeet.Mul(alongSign * float32(stdmath.Cos(float64(angleRad)))).
		Add(rwy.NormalFeet.Mul(acrossSign * float32(stdmath.Sin(float64(angleRad)))))
	normalized, ok := safetyNormalize(direction)
	if !ok {
		return redsmath.Vec2{}
	}
	return normalized
}

func runwayCorner(rwy surfaceRunway, along float32, across float32) redsmath.Vec2 {
	return rwy.CenterFeet.
		Add(rwy.AxisFeet.Mul(along)).
		Add(rwy.NormalFeet.Mul(across))
}

func addClosedXLine(
	builder *renderer.LinesBuilder,
	start redsmath.Vec2,
	dir redsmath.Vec2,
	edgeA redsmath.Vec2,
	edgeB redsmath.Vec2,
) {
	end, ok := intersectRaySegment(start, dir, edgeA, edgeB)
	if !ok {
		return
	}

	builder.AddLine(
		renderer.PointVertex{X: start.X, Y: start.Y},
		renderer.PointVertex{X: end.X, Y: end.Y},
	)
}

func intersectRaySegment(
	p redsmath.Vec2,
	r redsmath.Vec2,
	q redsmath.Vec2,
	sEnd redsmath.Vec2,
) (redsmath.Vec2, bool) {
	s := sEnd.Sub(q)
	denominator := cross2(r, s)
	if abs32(denominator) < degenerateRunwayAxisLength2 {
		return redsmath.Vec2{}, false
	}

	qmp := q.Sub(p)
	t := cross2(qmp, s) / denominator
	u := cross2(qmp, r) / denominator
	if t < 0 || u < 0 || u > 1 {
		return redsmath.Vec2{}, false
	}

	return p.Add(r.Mul(t)), true
}

func cross2(a, b redsmath.Vec2) float32 {
	return a.X*b.Y - a.Y*b.X
}

func (p *ASDEXPane) activateTempDataDcbHit(hit DcbHit) bool {
	if p == nil {
		return false
	}

	switch hit.Function {
	case DcbFunctionTempData:
		p.openTempDataDcbMenu()
		return true
	case DcbFunctionDone:
		switch p.dcb.Menu() {
		case DcbMenuTempData, DcbMenuClosedRunway:
			p.closeDcbCurrentSubmenu()
			return true
		default:
			return false
		}
	case DcbFunctionClosedRunway:
		p.openTempDataClosedRunwayDcbMenu()
		return true
	case DcbFunctionDefineClosedArea:
		p.startDefineClosedArea()
		return true
	case DcbFunctionDefineRestrictedArea:
		p.startDefineRestrictedArea()
		return true
	case DcbFunctionDefineTempText:
		p.startDefineTempText()
		return true
	case DcbFunctionDeleteGlobalTempData:
		p.startDeleteGlobalTempData()
		return true
	case DcbFunctionCloseRunway:
		if strings.TrimSpace(hit.Label) == "" {
			return true
		}
		p.tempData.ToggleRunwayClosedByDcbIndex(&p.safetyLogic, hit.ConfigID)
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	case DcbFunctionStoredGlobalTempData,
		DcbFunctionShowHiddenTempData,
		DcbFunctionHideTempData:
		return true
	default:
		return false
	}
}

func (p *ASDEXPane) startDefineClosedArea() {
	p.startDefineTempArea(TempDataClosedArea, "DEFINE CLOSED AREA")
}

func (p *ASDEXPane) startDefineRestrictedArea() {
	p.startDefineTempArea(TempDataRestrictedArea, "DEFINE RESTR AREA")
}

func (p *ASDEXPane) startDefineTempText() {
	if p == nil {
		return
	}

	if p.tempData.VisibleObjectCount() >= maxTempDataObjects {
		p.previewArea.SetSystemResponse("ERROR: MAX LIMIT")
		return
	}

	p.clearTempDataCommandConflicts()
	p.tempTextCommand = NewTempTextCommand()
	p.dcbMenuCommand = nil
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startDeleteGlobalTempData() {
	if p == nil {
		return
	}

	p.clearTempDataCommandConflicts()
	p.tempDataSelectMode = TempDataSelectDeleteGlobal
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA", "DELETE GLOBAL")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) consumeTempDataSelectionKeyboard(ctx *panes.Context) bool {
	if p == nil || p.tempDataSelectMode == TempDataSelectNone ||
		ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	if keyboard.WasPressed(platform.KeyEscape) ||
		keyboard.WasPressed(platform.KeyBackspace) ||
		keyboard.WasPressed(platform.KeyDelete) {
		p.cancelTempDataSelection()
		return true
	}
	return false
}

func (p *ASDEXPane) consumeTempDataSelectionInput(
	ctx *panes.Context,
	transforms radar.ScopeTransformations,
) bool {
	if p == nil || p.tempDataSelectMode == TempDataSelectNone ||
		ctx == nil || ctx.Mouse == nil {
		return false
	}

	mouse := ctx.Mouse
	world := transforms.WorldFromWindowP(mouse.Pos)
	if _, windowRect, view, ok := p.scopeWindowAtPoint(mouse.Pos, ctx.PaneSize()); ok {
		scopeTransforms := scopeTransformForWindow(
			windowRect,
			mainReferenceExtent(ctx.PaneSize()),
			view,
			rangeVisibleScaleForContext(ctx),
		)
		world = scopeTransforms.WorldFromWindowP(mouse.Pos.Sub(windowRect.Min))
	}
	switch {
	case mouse.WasReleased(platform.MouseButtonLeft):
		hit := p.tempData.HitTest(world)
		if hit.Type != TempDataHitNone {
			p.tempData.ToggleHighlight(hit)
			p.previewArea.SetSystemResponse("")
		}
		return true
	case mouse.WasReleased(platform.MouseButtonMiddle):
		switch p.tempDataSelectMode {
		case TempDataSelectDeleteGlobal:
			p.tempData.DeleteHighlightedGlobal()
			p.finishTempDataSelection()
			return true
		case TempDataSelectHide:
			return true
		}
	}
	return false
}

func (p *ASDEXPane) cancelTempDataSelection() {
	if p == nil {
		return
	}

	p.tempData.ClearHighlights()
	p.finishTempDataSelection()
}

func (p *ASDEXPane) finishTempDataSelection() {
	if p == nil {
		return
	}

	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.dcb.SetMenu(DcbMenuTempData)
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) handleTempTextKeyboard(ctx *panes.Context) bool {
	if p == nil || p.tempTextCommand == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	cmd := p.tempTextCommand
	switch {
	case keyboard.WasPressed(platform.KeyEscape):
		p.cancelTempTextCommand()
		return true
	case keyboard.WasPressed(platform.KeyBackspace):
		cmd.Backspace()
		return true
	case keyboard.WasPressed(platform.KeyDelete):
		cmd.DeleteForward()
		return true
	case keyboard.WasPressed(platform.KeyLeft):
		cmd.MoveLeft()
		return true
	case keyboard.WasPressed(platform.KeyRight):
		cmd.MoveRight()
		return true
	case keyboard.WasPressed(platform.KeyUp):
		cmd.MoveUp()
		return true
	case keyboard.WasPressed(platform.KeyDown):
		cmd.MoveDown()
		return true
	case keyboard.WasPressed(platform.KeyEnter), keyboard.WasPressed(platform.KeyKeypadEnter):
		if cmd.activeLine == 1 {
			cmd.activeLine = 2
			cmd.cursor = len([]rune(cmd.line2))
			p.previewArea.SetSystemResponse("")
			return true
		}
		p.submitTempTextCommand()
		return true
	}

	handled := false
	for _, r := range keyboard.Text {
		cmd.Insert(r)
		p.previewArea.SetSystemResponse("")
		handled = true
	}
	return handled
}

func (p *ASDEXPane) submitTempTextCommand() {
	if p == nil || p.tempTextCommand == nil {
		return
	}

	line1 := strings.TrimSpace(p.tempTextCommand.line1)
	line2 := strings.TrimSpace(p.tempTextCommand.line2)
	if len([]rune(line1)) > tempTextMaxLineLength ||
		len([]rune(line2)) > tempTextMaxLineLength {
		p.previewArea.SetSystemResponse("ERROR: MAX LIMIT")
		return
	}
	if line1 == "" {
		p.previewArea.SetSystemResponse("INVALID ENTRY")
		return
	}

	p.tempTextPlacement = &TempTextPlacementCommand{
		line1: line1,
		line2: line2,
	}
	p.tempTextCommand = nil
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) handleTempTextPlacementKeyboard(ctx *panes.Context) bool {
	if p == nil || p.tempTextPlacement == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	if keyboard.WasPressed(platform.KeyEscape) ||
		keyboard.WasPressed(platform.KeyBackspace) ||
		keyboard.WasPressed(platform.KeyDelete) {
		p.cancelTempTextPlacement()
		return true
	}
	return false
}

func (p *ASDEXPane) cancelTempTextCommand() {
	if p == nil {
		return
	}

	p.tempTextCommand = nil
	p.dcb.SetMenu(DcbMenuTempData)
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) cancelTempTextPlacement() {
	if p == nil {
		return
	}

	p.tempTextPlacement = nil
	p.dcb.SetMenu(DcbMenuTempData)
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) consumeTempTextPlacementInput(
	ctx *panes.Context,
	transforms radar.ScopeTransformations,
) bool {
	if p == nil || p.tempTextPlacement == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}
	if !ctx.Mouse.WasReleased(platform.MouseButtonLeft) {
		return false
	}

	world := transforms.WorldFromWindowP(ctx.Mouse.Pos)
	p.tempData.AddText(world, p.tempTextPlacement.line1, p.tempTextPlacement.line2)
	p.tempTextPlacement = nil
	p.dcb.SetMenu(DcbMenuTempData)
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
	return true
}

func (p *ASDEXPane) startDefineTempArea(areaType TempDataType, commandLine string) {
	if p == nil {
		return
	}

	if p.tempData.VisibleObjectCount() >= maxTempDataObjects {
		p.previewArea.SetSystemResponse("ERROR: MAX LIMIT")
		return
	}

	p.clearTempDataCommandConflicts()
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA", commandLine)
	p.tempAreaDraft = &TempAreaDraft{
		Type:   areaType,
		Points: make([]redsmath.Vec2, 0, maxTempAreaNodes+1),
	}
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) updateTempAreaDraftMouse(
	ctx *panes.Context,
	transforms radar.ScopeTransformations,
) {
	if p == nil || p.tempAreaDraft == nil || ctx == nil || ctx.Mouse == nil {
		return
	}

	p.tempAreaDraft.Mouse = transforms.WorldFromWindowP(ctx.Mouse.Pos)
}

func (p *ASDEXPane) consumeTempAreaDraftInput(
	ctx *panes.Context,
	transforms radar.ScopeTransformations,
) bool {
	if p == nil || p.tempAreaDraft == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}

	mouse := ctx.Mouse
	world := transforms.WorldFromWindowP(mouse.Pos)
	p.tempAreaDraft.Mouse = world

	switch {
	case mouse.WasReleased(platform.MouseButtonLeft):
		p.addTempAreaDraftPoint(world)
		return true
	case mouse.WasReleased(platform.MouseButtonMiddle):
		p.finishTempAreaDraft()
		return true
	default:
		return false
	}
}

func (p *ASDEXPane) addTempAreaDraftPoint(point redsmath.Vec2) {
	if p == nil || p.tempAreaDraft == nil {
		return
	}

	draft := p.tempAreaDraft
	if len(draft.Points) > 0 {
		last := draft.Points[len(draft.Points)-1]
		if tempSegmentWouldSelfIntersect(last, point, draft.Points) {
			return
		}
	}

	draft.Points = append(draft.Points, point)
	p.previewArea.SetSystemResponse("")

	if len(draft.Points) >= maxTempAreaNodes {
		p.finishTempAreaDraft()
	}
}

func (p *ASDEXPane) finishTempAreaDraft() {
	if p == nil || p.tempAreaDraft == nil {
		return
	}

	draft := p.tempAreaDraft
	if len(draft.Points) < minTempAreaNodes {
		draft.Points = draft.Points[:0]
		p.previewArea.SetSystemResponse("BAD POLYGON,REDRAW POINT")
		return
	}

	last := draft.Points[len(draft.Points)-1]
	first := draft.Points[0]
	if tempClosingSegmentWouldSelfIntersect(last, first, draft.Points) {
		p.previewArea.SetSystemResponse("BAD POLYGON,REDRAW POINT")
		return
	}

	polygon := append([]redsmath.Vec2(nil), draft.Points...)
	polygon = append(polygon, first)
	p.tempData.AddArea(draft.Type, polygon)

	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.tempAreaDraft = nil
	p.dcb.SetMenu(DcbMenuTempData)
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) cancelTempAreaDraft() {
	if p == nil || p.tempAreaDraft == nil {
		return
	}

	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.tempAreaDraft = nil
	p.dcb.SetMenu(DcbMenuTempData)
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) DrawTempAreaDraft(cb *renderer.CmdBuffer) {
	if p == nil || p.tempAreaDraft == nil || cb == nil {
		return
	}

	draft := p.tempAreaDraft
	if len(draft.Points) == 0 {
		return
	}

	points := make([]renderer.PointVertex, 0, len(draft.Points)+1)
	for _, pt := range draft.Points {
		points = append(points, renderer.PointVertex{X: pt.X, Y: pt.Y})
	}
	points = append(points, renderer.PointVertex{X: draft.Mouse.X, Y: draft.Mouse.Y})

	builder := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(builder)

	builder.AddLineStrip(points)
	cb.SetRGB(applyBrightness(monitorColors.TempAreaDraw, brightnessDefault, brightnessFloorDefault))
	cb.LineWidth(tempAreaDrawLineWidth)
	builder.GenerateCommands(cb)
}

func tempSegmentWouldSelfIntersect(
	a redsmath.Vec2,
	b redsmath.Vec2,
	points []redsmath.Vec2,
) bool {
	if len(points) < 2 {
		return false
	}

	for i := 0; i+1 < len(points); i++ {
		if segmentsIntersectStrict(a, b, points[i], points[i+1]) {
			return true
		}
	}
	return false
}

func tempClosingSegmentWouldSelfIntersect(
	last redsmath.Vec2,
	first redsmath.Vec2,
	points []redsmath.Vec2,
) bool {
	if len(points) < minTempAreaNodes {
		return false
	}

	for i := 0; i+1 < len(points)-1; i++ {
		if segmentsIntersectStrict(last, first, points[i], points[i+1]) {
			return true
		}
	}
	return false
}

func segmentsIntersectStrict(a, b, c, d redsmath.Vec2) bool {
	intersection, ok := segmentIntersectionPoint(a, b, c, d)
	if !ok {
		return false
	}

	const tolerance = 1e-3
	if almostEqualVec2(intersection, a, tolerance) ||
		almostEqualVec2(intersection, b, tolerance) {
		return false
	}
	return true
}

func segmentIntersectionPoint(
	a redsmath.Vec2,
	b redsmath.Vec2,
	c redsmath.Vec2,
	d redsmath.Vec2,
) (redsmath.Vec2, bool) {
	r := b.Sub(a)
	s := d.Sub(c)
	denominator := cross2(r, s)
	if abs32(denominator) < degenerateRunwayAxisLength2 {
		return redsmath.Vec2{}, false
	}

	cma := c.Sub(a)
	t := cross2(cma, s) / denominator
	u := cross2(cma, r) / denominator
	if t < 0 || t > 1 || u < 0 || u > 1 {
		return redsmath.Vec2{}, false
	}

	return a.Add(r.Mul(t)), true
}

func almostEqualVec2(a, b redsmath.Vec2, tolerance float32) bool {
	d := a.Sub(b)
	return d.X*d.X+d.Y*d.Y <= tolerance*tolerance
}

func (p *ASDEXPane) clearTempDataCommandConflicts() {
	if p == nil {
		return
	}

	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.tempData.ClearHighlights()
	p.newWindow = nil
	p.dcbSpinner = nil
	p.commandEntry.Clear()
	p.datablockEdit = nil
	p.editingTargetID = ""
	p.initControlEntry = nil
	p.termControlEntry = nil
	p.multiFunction = nil
	p.scratchpadEntry = nil
	p.previewReposition = nil
	p.coastListReposition = nil
	p.mapReposition = nil
	p.mapRotate = nil
}

func (p *ASDEXPane) openTempDataDcbMenu() {
	if p == nil {
		return
	}

	p.dcb.SetMenu(DcbMenuTempData)
	p.clearTempDataCommandConflicts()
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) openTempDataClosedRunwayDcbMenu() {
	p.openClosedRunwayDcbMenu(
		DcbMenuTempData,
		[]string{"TEMP DATA"},
		[]string{"TEMP DATA", "CLOSED RUNWAY"},
	)
}

func (p *ASDEXPane) openSafetyLogicClosedRunwayDcbMenu() {
	p.openClosedRunwayDcbMenu(
		DcbMenuSafetyLogic,
		[]string{"SAFETY LOGIC"},
		[]string{"SAFETY LOGIC", "CLOSED RUNWAY"},
	)
}

func (p *ASDEXPane) openClosedRunwayDcbMenu(
	returnMenu DcbMenu,
	returnLines []string,
	displayLines []string,
) {
	if p == nil {
		return
	}

	p.dcb.SetMenu(DcbMenuClosedRunway)
	p.clearTempDataCommandConflicts()
	p.closedRunwayReturnMenu = returnMenu
	p.closedRunwayReturnLines = append([]string(nil), returnLines...)
	p.dcbMenuCommand = NewDcbMenuCommand(displayLines...)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) closeDcbCurrentSubmenu() {
	if p == nil {
		return
	}

	switch p.dcb.Menu() {
	case DcbMenuClosedRunway:
		p.restoreClosedRunwayReturnMenu()
	case DcbMenuTempData:
		p.dcb.SetMenu(DcbMenuMain)
		p.dcbMenuCommand = nil
	default:
		p.closeDcbSubmenu()
		return
	}

	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.tempData.ClearHighlights()
	p.newWindow = nil
	p.dcbSpinner = nil
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) restoreClosedRunwayReturnMenu() {
	if p == nil {
		return
	}

	returnMenu := p.closedRunwayReturnMenu
	if returnMenu == DcbMenuOff || returnMenu == DcbMenuClosedRunway {
		returnMenu = DcbMenuTempData
	}
	if returnMenu == DcbMenuMain {
		p.dcb.SetMenu(DcbMenuMain)
		p.dcbMenuCommand = nil
		return
	}

	p.dcb.SetMenu(returnMenu)
	if len(p.closedRunwayReturnLines) > 0 {
		p.dcbMenuCommand = NewDcbMenuCommand(p.closedRunwayReturnLines...)
		return
	}

	switch returnMenu {
	case DcbMenuSafetyLogic:
		p.dcbMenuCommand = NewDcbMenuCommand("SAFETY LOGIC")
	case DcbMenuTempData:
		p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA")
	default:
		p.dcbMenuCommand = nil
	}
}

func (p *ASDEXPane) closeDcbSubmenu() {
	if p == nil {
		return
	}

	if p.dcb.Menu() != DcbMenuOff {
		p.dcb.SetMenu(DcbMenuMain)
	}
	p.dcbMenuCommand = nil
	p.dcbSpinner = nil
	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.tempData.ClearHighlights()
	p.newWindow = nil
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) handleDcbMenuKeyboard(ctx *panes.Context) bool {
	if p == nil || p.dcbMenuCommand == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	if keyboard.WasPressed(platform.KeyEscape) ||
		keyboard.WasPressed(platform.KeyBackspace) ||
		keyboard.WasPressed(platform.KeyDelete) {
		if p.tempAreaDraft != nil {
			p.cancelTempAreaDraft()
			return true
		}
		p.closeDcbCurrentSubmenu()
		return true
	}

	return false
}

// -----------------------------------------------------------------------------
// Graph-based temporary map area selection
// -----------------------------------------------------------------------------
// Graph-based temporary areas derived from the existing REDS surface file.
// Graph topology selects which sections are affected. Actual twys polygons,
// rather than buffer approximations of centerlines, supply the painted shape.

// GraphAreaRequest describes a proposed temporary area in airport terms.
// A blank From/To is "No Endpoint"; both blank means the whole named taxiway.
// One blank is ambiguous (there can be several terminal branches) and rejected.
type GraphAreaRequest struct {
	AreaType string `json:"areaType"`
	Taxiway  string `json:"taxiway"`
	From     string `json:"from"`
	To       string `json:"to"`
}

type surfaceTaxiwayPolygon struct {
	ID      string      `json:"id"`
	Polygon [][]float64 `json:"polygon"`
}

type surfaceRouteGraph struct {
	Pavements []surfaceTaxiwayPolygon `json:"-"`
	Nodes     [][]float64             `json:"nodes"`
	Edges     []struct {
		A     int         `json:"a"`
		B     int         `json:"b"`
		Names []string    `json:"names"`
		Shape [][]float64 `json:"shape"`
	} `json:"edges"`
	Anchors []struct {
		Type    string `json:"type"`
		Runway  string `json:"runway"`
		Taxiway string `json:"taxiway"`
		Node    int    `json:"node"`
	} `json:"anchors"`
}

func loadSurfaceRouteGraph(airport string) (*surfaceRouteGraph, error) {
	path := filepath.ToSlash(filepath.Join("asdex", "surface", airport+".json.zst"))
	if !util.ResourceExists(path) {
		return nil, fmt.Errorf("surface data not found: %s", path)
	}
	var source struct {
		Graph    surfaceRouteGraph       `json:"graph"`
		Taxiways []surfaceTaxiwayPolygon `json:"twys"`
	}
	if err := json.Unmarshal(util.LoadResourceBytes(path), &source); err != nil {
		return nil, fmt.Errorf("decode airport graph: %w", err)
	}
	g := &source.Graph
	g.Pavements = source.Taxiways
	if len(g.Nodes) == 0 || len(g.Edges) == 0 {
		return nil, fmt.Errorf("airport %s has no inferred taxiway graph", airport)
	}
	for i, v := range g.Nodes {
		if len(v) != 2 || stdmath.IsNaN(v[0]) || stdmath.IsNaN(v[1]) || stdmath.IsInf(v[0], 0) || stdmath.IsInf(v[1], 0) {
			return nil, fmt.Errorf("invalid graph vertex %d", i)
		}
	}
	for i, e := range g.Edges {
		if e.A < 0 || e.B < 0 || e.A >= len(g.Nodes) || e.B >= len(g.Nodes) {
			return nil, fmt.Errorf("edge %d has invalid endpoints", i)
		}
	}
	return g, nil
}

func graphHasName(names []string, name string) bool {
	for _, n := range names {
		if strings.EqualFold(strings.TrimSpace(n), name) {
			return true
		}
	}
	return false
}

type graphBoundary struct {
	kind, name string
}

func parseGraphBoundary(label string) (graphBoundary, error) {
	label = strings.TrimSpace(label)
	if label == "" || label == "No Endpoint" {
		return graphBoundary{}, nil
	}
	if strings.HasPrefix(label, "TWY ") {
		return graphBoundary{"taxiway", strings.TrimSpace(strings.TrimPrefix(label, "TWY "))}, nil
	}
	if strings.HasPrefix(label, "RWY ") {
		return graphBoundary{"runway", strings.TrimSpace(strings.TrimPrefix(label, "RWY "))}, nil
	}
	return graphBoundary{}, fmt.Errorf("unknown boundary %q", label)
}

// ResolveEdges returns the exact edge indices in the inferred graph. Boundaries
// must identify a single geographic junction each; multiple separate junctions
// are refused rather than arbitrarily choosing one.
func (g *surfaceRouteGraph) ResolveEdges(taxiway, from, to string) ([]int, error) {
	taxiway = strings.TrimSpace(taxiway)
	if taxiway == "" {
		return nil, fmt.Errorf("select a taxiway")
	}
	a, err := parseGraphBoundary(from)
	if err != nil {
		return nil, err
	}
	b, err := parseGraphBoundary(to)
	if err != nil {
		return nil, err
	}
	if (a.kind == "") != (b.kind == "") {
		return nil, fmt.Errorf("select both From and To, or leave both as No Endpoint for the entire taxiway")
	}

	adjacency := make([][]int, len(g.Nodes))
	selected := make([]int, 0)
	for i, e := range g.Edges {
		if graphHasName(e.Names, taxiway) {
			selected = append(selected, i)
			adjacency[e.A] = append(adjacency[e.A], i)
			adjacency[e.B] = append(adjacency[e.B], i)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("taxiway %s has no graph segments", taxiway)
	}
	if a.kind == "" {
		return selected, nil
	}
	if a.kind == b.kind && strings.EqualFold(a.name, b.name) {
		return nil, fmt.Errorf("From and To must identify different boundaries")
	}

	start, err := g.boundaryVertices(taxiway, a, adjacency)
	if err != nil {
		return nil, fmt.Errorf("From: %w", err)
	}
	end, err := g.boundaryVertices(taxiway, b, adjacency)
	if err != nil {
		return nil, fmt.Errorf("To: %w", err)
	}

	endNodes := make(map[int]bool, len(end))
	for _, n := range end {
		endNodes[n] = true
	}
	visited := make([]bool, len(g.Nodes))
	parentNode := make([]int, len(g.Nodes))
	parentEdge := make([]int, len(g.Nodes))
	for i := range parentNode {
		parentNode[i] = -1
		parentEdge[i] = -1
	}
	queue := make([]int, 0, len(g.Nodes))
	for _, v := range start {
		if !visited[v] {
			visited[v] = true
			queue = append(queue, v)
		}
	}
	target := -1
	for head := 0; head < len(queue); head++ {
		v := queue[head]
		if endNodes[v] {
			target = v
			break
		}
		for _, i := range adjacency[v] {
			e := g.Edges[i]
			next := e.A
			if next == v {
				next = e.B
			}
			if !visited[next] {
				visited[next] = true
				parentNode[next] = v
				parentEdge[next] = i
				queue = append(queue, next)
			}
		}
	}
	if target < 0 {
		return nil, fmt.Errorf("no connected route on TWY %s between %s and %s", taxiway, from, to)
	}
	path := make([]int, 0)
	for v := target; parentEdge[v] >= 0; v = parentNode[v] {
		path = append(path, parentEdge[v])
	}
	if len(path) == 0 {
		return nil, fmt.Errorf("the selected boundaries meet at the same junction")
	}
	// Check that the chosen path is unique in the selected subgraph. A second
	// equally valid branch should be resolved manually, not selected silently.
	// (Cycles can otherwise produce a plausible but unintended shortcut.)
	for _, i := range path {
		e := g.Edges[i]
		// Only endpoints with three or more A branches can create route ambiguity.
		if len(adjacency[e.A]) > 2 || len(adjacency[e.B]) > 2 {
			// Reject alternative paths to the same boundary if removing a path
			// edge still allows both selected junction groups to connect.
			if g.pathExistsSkipping(adjacency, start, endNodes, i) {
				return nil, fmt.Errorf("TWY %s has multiple routes between these boundaries; refine the graph/selection", taxiway)
			}
		}
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, nil
}

func (g *surfaceRouteGraph) pathExistsSkipping(adjacency [][]int, starts []int, ends map[int]bool, omit int) bool {
	visited := make([]bool, len(g.Nodes))
	queue := make([]int, 0)
	for _, v := range starts {
		if !visited[v] {
			visited[v] = true
			queue = append(queue, v)
		}
	}
	for h := 0; h < len(queue); h++ {
		v := queue[h]
		if ends[v] {
			return true
		}
		for _, idx := range adjacency[v] {
			if idx == omit {
				continue
			}
			e := g.Edges[idx]
			next := e.A
			if next == v {
				next = e.B
			}
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

func (g *surfaceRouteGraph) boundaryVertices(taxiway string, boundary graphBoundary, adjacency [][]int) ([]int, error) {
	candidates := make(map[int]bool)
	switch boundary.kind {
	case "taxiway":
		for _, e := range g.Edges {
			if !graphHasName(e.Names, boundary.name) {
				continue
			}
			if len(adjacency[e.A]) > 0 {
				candidates[e.A] = true
			}
			if len(adjacency[e.B]) > 0 {
				candidates[e.B] = true
			}
		}
	case "runway":
		for _, a := range g.Anchors {
			if a.Type == "runway" && strings.EqualFold(a.Runway, boundary.name) && strings.EqualFold(a.Taxiway, taxiway) && a.Node >= 0 && a.Node < len(adjacency) && len(adjacency[a.Node]) > 0 {
				candidates[a.Node] = true
			}
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%s %s does not have a validated connection to TWY %s", boundary.kind, boundary.name, taxiway)
	}
	// Nearby skeleton vertices belonging to a single physical intersection
	// count as one boundary. Distinct positions are refused as ambiguous.
	var values []int
	for n := range candidates {
		values = append(values, n)
	}
	// Rasterized polygon skeletons often spread one physical junction across
	// several vertices. Accept a cluster at most 150 m across; anything
	// larger may represent distinct intersections and needs disambiguation.
	const maxJunctionSpanM = 150.0
	for i := 0; i < len(values); i++ {
		origin := g.Nodes[values[i]]
		for j := i + 1; j < len(values); j++ {
			p := g.Nodes[values[j]]
			latRad := (p[1] + origin[1]) * stdmath.Pi / 360
			east := (p[0] - origin[0]) * 111320 * stdmath.Cos(latRad)
			north := (p[1] - origin[1]) * 111320
			if stdmath.Hypot(east, north) > maxJunctionSpanM {
				return nil, fmt.Errorf("%s %s intersects TWY %s at multiple locations", boundary.kind, boundary.name, taxiway)
			}
		}
	}

	return values, nil
}

// This is a spatial selection mask, never the visible geometry. The broad
// width avoids cutting off actual pavement boundaries along the taxiway sides.
// At the selected From/To graph vertices it terminates sharply, limiting a
// bounded closure to the selected portion of the physical pavement polygon.
const graphAreaMaskHalfWidthFeet = float32(360) // ~110 m

func pavementHasTaxiway(id, taxiway string) bool {
	for _, name := range strings.Split(id, "_") {
		if strings.EqualFold(strings.TrimSpace(name), taxiway) {
			return true
		}
	}
	return false
}

func graphAreaMaskRectangle(a, b redsmath.Vec2, halfWidth float32) pavementRing {
	d := b.Sub(a)
	l := float32(stdmath.Hypot(float64(d.X), float64(d.Y)))
	if l < 1 {
		return nil
	}
	n := redsmath.Vec2{X: -d.Y * halfWidth / l, Y: d.X * halfWidth / l}
	corners := []redsmath.Vec2{a.Add(n), b.Add(n), b.Sub(n), a.Sub(n)}
	return paveReadRing(corners)
}

func graphAreaMaskSquare(center redsmath.Vec2, halfWidth float32) pavementRing {
	corners := []redsmath.Vec2{
		{X: center.X - halfWidth, Y: center.Y - halfWidth},
		{X: center.X + halfWidth, Y: center.Y - halfWidth},
		{X: center.X + halfWidth, Y: center.Y + halfWidth},
		{X: center.X - halfWidth, Y: center.Y + halfWidth},
	}
	return paveReadRing(corners)
}

// graphAreaPolygons resolves a named section on the inferred graph, then
// clips and unions the *original* taxiway pavement polygons. Adjacent source
// polygons produce one outer contour, not overlapping artificial rectangles.
func (p *ASDEXPane) graphAreaPolygons(req GraphAreaRequest) ([]graphAreaPolygon, error) {
	if p.surfaceGraph == nil {
		g, err := loadSurfaceRouteGraph(p.airport)
		if err != nil {
			return nil, err
		}
		p.surfaceGraph = g
	}
	g := p.surfaceGraph
	indices, err := g.ResolveEdges(req.Taxiway, req.From, req.To)
	if err != nil {
		return nil, err
	}

	// Preserve the current named FAA/REDS pavement polygons, including
	// junction features such as A_C that belong to both A and C.
	var pavement []pavementRing
	for _, feature := range g.Pavements {
		if !pavementHasTaxiway(feature.ID, req.Taxiway) || len(feature.Polygon) < 3 {
			continue
		}
		points := make([]redsmath.Vec2, 0, len(feature.Polygon))
		for _, coord := range feature.Polygon {
			if len(coord) != 2 {
				continue
			}
			points = append(points, p.videomap.LonLatToFeet(coord[0], coord[1]))
		}
		if ring := paveReadRing(points); len(ring) >= 3 {
			pavement = append(pavement, ring)
		}
	}
	if len(pavement) == 0 {
		return nil, fmt.Errorf("TWY %s has no associated pavement polygons", req.Taxiway)
	}

	// A whole-taxiway closure uses the original polygons without a spatial
	// mask. A bounded closure uses the graph path only as a clipping mask.
	// Neither case draws the centerline corridor itself.
	var mask []pavementRing
	if strings.TrimSpace(req.From) != "" && req.From != "No Endpoint" {
		degrees := make(map[int]int)
		for _, index := range indices {
			edge := g.Edges[index]
			degrees[edge.A]++
			degrees[edge.B]++
		}
		for _, index := range indices {
			edge := g.Edges[index]
			coords := make([][]float64, 0, len(edge.Shape)+2)
			coords = append(coords, g.Nodes[edge.A])
			coords = append(coords, edge.Shape...)
			coords = append(coords, g.Nodes[edge.B])
			var path []redsmath.Vec2
			for _, coord := range coords {
				if len(coord) == 2 {
					path = append(path, p.videomap.LonLatToFeet(coord[0], coord[1]))
				}
			}
			for i := 1; i < len(path); i++ {
				if rect := graphAreaMaskRectangle(path[i-1], path[i], graphAreaMaskHalfWidthFeet); len(rect) > 0 {
					mask = append(mask, rect)
				}
			}
			for i := 1; i < len(path)-1; i++ {
				mask = append(mask, graphAreaMaskSquare(path[i], graphAreaMaskHalfWidthFeet))
			}
		}
		for vertex, degree := range degrees {
			if degree >= 2 {
				pos := g.Nodes[vertex]
				mask = append(mask, graphAreaMaskSquare(p.videomap.LonLatToFeet(pos[0], pos[1]), graphAreaMaskHalfWidthFeet))
			}
		}
		if len(mask) == 0 {
			return nil, fmt.Errorf("selected graph section has no drawable geometry")
		}
	}
	polygons, err := pavementBooleanBoundary(pavement, mask)
	if err == nil {
		return polygons, nil
	}
	// A few airports contain coincident/touching sliver polygons that can
	// defeat contour reconstruction. For a *whole* taxiway, drawing the
	// original pavement is still better than inventing a centerline ribbon.
	// For a bounded section, never fall back to drawing the entire taxiway.
	if len(mask) != 0 {
		return nil, fmt.Errorf("unable to build the bounded pavement outline for TWY %s: %w", req.Taxiway, err)
	}
	fallback := make([]graphAreaPolygon, 0, len(pavement))
	for _, ring := range pavement {
		fallback = append(fallback, graphAreaPolygon{Outer: paveWriteRing(ring)})
	}
	return fallback, nil
}

func (p *ASDEXPane) ApplyGraphArea(req GraphAreaRequest, preview bool) (int, error) {
	var areaType TempDataType
	switch req.AreaType {
	case "Closed Area":
		areaType = TempDataClosedArea
	case "Restriction Area":
		areaType = TempDataRestrictedArea
	default:
		return 0, fmt.Errorf("invalid map area type %q", req.AreaType)
	}
	polygons, err := p.graphAreaPolygons(req)
	if err != nil {
		return 0, err
	}
	var drawn int
	if preview {
		drawn = p.tempData.SetGraphAreaPreview(areaType, polygons)
	} else {
		drawn = p.tempData.CommitGraphAreas(areaType, polygons)
	}
	if drawn == 0 {
		return 0, fmt.Errorf("graph segments could not be tessellated")
	}
	return drawn, nil
}

func (p *ASDEXPane) ClearGraphAreaPreview() {
	if p != nil {
		p.tempData.ClearGraphAreaPreview()
	}
}

// -----------------------------------------------------------------------------
// Temporary map area pavement geometry
// -----------------------------------------------------------------------------
// Polygonal geometry used for rendering graph-selected temporary map areas.
// The graph selects the longitudinal extent, while the actual airport surface
// polygons determine the shape. No artificial taxiway-width ribbon is drawn.

type graphAreaPolygon struct {
	Outer []redsmath.Vec2
	Holes [][]redsmath.Vec2
}

type pavementPoint struct{ x, y float64 }
type pavementRing []pavementPoint

type pavementSegment struct {
	a, b pavementPoint
	cuts []float64
}

type pavementLink struct {
	from, to   pavementPoint
	kfrom, kto pavementKey
	used       bool
}

type pavementKey struct{ x, y int64 }

func snapPavement(p pavementPoint) pavementKey {
	// REDS works in feet. Snapping intersections to 1/100th foot makes the
	// independently calculated crossing points share exact graph vertices.
	return pavementKey{int64(stdmath.Round(p.x * 100)), int64(stdmath.Round(p.y * 100))}
}
func unsnapPavement(k pavementKey) pavementPoint {
	return pavementPoint{float64(k.x) / 100, float64(k.y) / 100}
}
func psub(a, b pavementPoint) pavementPoint               { return pavementPoint{a.x - b.x, a.y - b.y} }
func padd(a, b pavementPoint) pavementPoint               { return pavementPoint{a.x + b.x, a.y + b.y} }
func pmul(a pavementPoint, t float64) pavementPoint       { return pavementPoint{a.x * t, a.y * t} }
func pcross(a, b pavementPoint) float64                   { return a.x*b.y - a.y*b.x }
func pdot(a, b pavementPoint) float64                     { return a.x*b.x + a.y*b.y }
func pinterp(a, b pavementPoint, t float64) pavementPoint { return padd(a, pmul(psub(b, a), t)) }

// pavePointInPolygon uses even-odd fill, with no dependence on ring winding.
func pavePointInPolygon(p pavementPoint, polygon pavementRing) bool {
	inside := false
	for i, j := 0, len(polygon)-1; i < len(polygon); j, i = i, i+1 {
		a, b := polygon[j], polygon[i]
		if (a.y > p.y) != (b.y > p.y) && p.x < (b.x-a.x)*(p.y-a.y)/(b.y-a.y)+a.x {
			inside = !inside
		}
	}
	return inside
}
func paveInsideAny(p pavementPoint, polygons []pavementRing) bool {
	for _, ring := range polygons {
		if pavePointInPolygon(p, ring) {
			return true
		}
	}
	return false
}
func paveArea(r pavementRing) float64 {
	a := 0.0
	for i := range r {
		j := (i + 1) % len(r)
		a += pcross(r[i], r[j])
	}
	return a * 0.5
}
func paveReadRing(input []redsmath.Vec2) pavementRing {
	ring := make(pavementRing, 0, len(input))
	for _, p := range input {
		v := pavementPoint{float64(p.X), float64(p.Y)}
		if len(ring) > 0 && snapPavement(ring[len(ring)-1]) == snapPavement(v) {
			continue
		}
		ring = append(ring, v)
	}
	if len(ring) > 1 && snapPavement(ring[0]) == snapPavement(ring[len(ring)-1]) {
		ring = ring[:len(ring)-1]
	}
	if len(ring) < 3 || stdmath.Abs(paveArea(ring)) < 0.5 {
		return nil
	}
	return ring
}
func paveWriteRing(input pavementRing) []redsmath.Vec2 {
	out := make([]redsmath.Vec2, 0, len(input)+1)
	for _, pt := range input {
		out = append(out, redsmath.Vec2{X: float32(pt.x), Y: float32(pt.y)})
	}
	if len(out) > 0 {
		out = append(out, out[0])
	}
	return out
}

func paveBetween(t float64) bool  { return t >= -1e-9 && t <= 1+1e-9 }
func paveClamp(t float64) float64 { return stdmath.Max(0, stdmath.Min(1, t)) }

func paveSplitSegments(segments []pavementSegment) {
	// The geometry is small (one named taxiway), and bounding-box rejection
	// avoids most intersection calculations. Identical and collinear edges
	// are deliberately split at the same endpoints so overlaps disappear.
	for i := range segments {
		a := &segments[i]
		av := psub(a.b, a.a)
		for j := i + 1; j < len(segments); j++ {
			b := &segments[j]
			if stdmath.Max(a.a.x, a.b.x)+1e-7 < stdmath.Min(b.a.x, b.b.x) ||
				stdmath.Max(b.a.x, b.b.x)+1e-7 < stdmath.Min(a.a.x, a.b.x) ||
				stdmath.Max(a.a.y, a.b.y)+1e-7 < stdmath.Min(b.a.y, b.b.y) ||
				stdmath.Max(b.a.y, b.b.y)+1e-7 < stdmath.Min(a.a.y, a.b.y) {
				continue
			}
			bv := psub(b.b, b.a)
			d := pcross(av, bv)
			ab := psub(b.a, a.a)
			if stdmath.Abs(d) > 1e-8 {
				t := pcross(ab, bv) / d
				u := pcross(ab, av) / d
				if paveBetween(t) && paveBetween(u) {
					a.cuts = append(a.cuts, paveClamp(t))
					b.cuts = append(b.cuts, paveClamp(u))
				}
			} else if stdmath.Abs(pcross(ab, av)) < 1e-5*stdmath.Hypot(av.x, av.y) {
				aa := pdot(av, av)
				bb := pdot(bv, bv)
				if aa < 0.01 || bb < 0.01 {
					continue
				}
				for _, v := range []pavementPoint{b.a, b.b} {
					t := pdot(psub(v, a.a), av) / aa
					if paveBetween(t) {
						a.cuts = append(a.cuts, paveClamp(t))
					}
				}
				for _, v := range []pavementPoint{a.a, a.b} {
					t := pdot(psub(v, b.a), bv) / bb
					if paveBetween(t) {
						b.cuts = append(b.cuts, paveClamp(t))
					}
				}
			}
		}
	}
}

// pavementBooleanBoundary extracts the true union of the source polygons,
// intersected with the optional selection mask. All input boundaries are kept
// where they divide inside from outside, discarding shared/interior seams.
// Both polygon sets are in local feet, not latitude/longitude.
func pavementBooleanBoundary(source, mask []pavementRing) ([]graphAreaPolygon, error) {
	if len(source) == 0 {
		return nil, fmt.Errorf("no taxiway pavement polygons for selection")
	}
	var segments []pavementSegment
	for _, set := range [][]pavementRing{source, mask} {
		for _, ring := range set {
			if len(ring) < 3 {
				continue
			}
			for i := range ring {
				a, b := ring[i], ring[(i+1)%len(ring)]
				if pdot(psub(a, b), psub(a, b)) < 0.01 {
					continue
				}
				segments = append(segments, pavementSegment{a: a, b: b, cuts: []float64{0, 1}})
			}
		}
	}
	if len(segments) > 2800 {
		return nil, fmt.Errorf("taxiway selection has too many polygon edges (%d)", len(segments))
	}
	paveSplitSegments(segments)
	links := make([]pavementLink, 0, len(segments))
	dedup := make(map[[4]int64]bool)
	for _, s := range segments {
		sort.Float64s(s.cuts)
		ab := psub(s.b, s.a)
		lenAB := stdmath.Hypot(ab.x, ab.y)
		if lenAB < 0.1 {
			continue
		}
		offset := pavementPoint{-ab.y / lenAB * 0.04, ab.x / lenAB * 0.04}
		for i := 0; i < len(s.cuts)-1; i++ {
			l, r := s.cuts[i], s.cuts[i+1]
			if (r-l)*lenAB < 0.02 {
				continue
			}
			a, b := pinterp(s.a, s.b, l), pinterp(s.a, s.b, r)
			m := pinterp(a, b, 0.5)
			inside := func(v pavementPoint) bool {
				return paveInsideAny(v, source) && (len(mask) == 0 || paveInsideAny(v, mask))
			}
			left, right := inside(padd(m, offset)), inside(psub(m, offset))
			if left == right {
				continue
			}
			if !left {
				a, b = b, a
			}
			k1, k2 := snapPavement(a), snapPavement(b)
			if k1 == k2 {
				continue
			}
			key := [4]int64{k1.x, k1.y, k2.x, k2.y}
			if dedup[key] {
				continue
			}
			dedup[key] = true
			links = append(links, pavementLink{from: unsnapPavement(k1), to: unsnapPavement(k2), kfrom: k1, kto: k2})
		}
	}
	if len(links) == 0 {
		return nil, fmt.Errorf("selection does not intersect taxiway pavement")
	}
	outgoing := make(map[pavementKey][]int)
	for i, e := range links {
		outgoing[e.kfrom] = append(outgoing[e.kfrom], i)
	}
	rings := make([]pavementRing, 0)
	for start := range links {
		if links[start].used {
			continue
		}
		here := start
		ring := make(pavementRing, 0, 16)
		first := links[start].kfrom
		closed := false
		for n := 0; n <= len(links); n++ {
			edge := &links[here]
			if edge.used {
				break
			}
			edge.used = true
			ring = append(ring, edge.from)
			if edge.kto == first {
				closed = true
				break
			}
			options := outgoing[edge.kto]
			next := -1
			best := stdmath.Inf(1)
			reverse := stdmath.Atan2(edge.from.y-edge.to.y, edge.from.x-edge.to.x)
			for _, k := range options {
				if links[k].used {
					continue
				}
				o := links[k]
				a := stdmath.Atan2(o.to.y-o.from.y, o.to.x-o.from.x)
				// Follow the most clockwise outgoing edge relative to the
				// incoming reverse, keeping the filled region on our left.
				turn := stdmath.Mod(reverse-a+2*stdmath.Pi, 2*stdmath.Pi)
				if turn < best {
					best = turn
					next = k
				}
			}
			if next < 0 {
				break
			}
			here = next
		}
		if !closed {
			return nil, fmt.Errorf("incomplete pavement boundary; input polygons may contain invalid geometry")
		}
		if len(ring) >= 3 && stdmath.Abs(paveArea(ring)) > 1 {
			rings = append(rings, ring)
		}
	}
	if len(rings) == 0 {
		return nil, fmt.Errorf("no valid paved area boundaries")
	}
	sort.Slice(rings, func(i, j int) bool { return stdmath.Abs(paveArea(rings[i])) > stdmath.Abs(paveArea(rings[j])) })
	var polys []graphAreaPolygon
	var outerRings []pavementRing
	var holes []pavementRing
	for _, ring := range rings {
		if paveArea(ring) > 0 {
			outerRings = append(outerRings, ring)
		} else {
			holes = append(holes, ring)
		}
	}
	for _, outer := range outerRings {
		polys = append(polys, graphAreaPolygon{Outer: paveWriteRing(outer)})
	}
	for _, hole := range holes {
		parent := -1
		parentArea := stdmath.Inf(1)
		for i, outer := range outerRings {
			if pavePointInPolygon(hole[0], outer) && stdmath.Abs(paveArea(outer)) < parentArea {
				parent = i
				parentArea = stdmath.Abs(paveArea(outer))
			}
		}
		if parent < 0 {
			return nil, fmt.Errorf("unattached pavement hole; check source geometry")
		}
		polys[parent].Holes = append(polys[parent].Holes, paveWriteRing(hole))
	}
	return polys, nil
}
