package asdex

import (
	"fmt"
	stdmath "math"
	"strings"
	"unicode"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
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
	tempTextAnchorOffsetPx    = 7
	tempTextStepPx            = 15
	tempTextZeroLengthPx      = 10
)

var (
	tempClosedAreaRGB     = renderer.RGB8(255, 0, 0)
	tempRestrictedAreaRGB = renderer.RGB8(255, 255, 0)
	tempAreaDrawRGB       = renderer.RGB8(255, 255, 255)
	tempAreaHighlightRGB  = renderer.RGB8(0, 0, 255)
	tempTextRGB           = renderer.RGB8(255, 255, 255)
	tempTextHighlightRGB  = renderer.RGB8(0, 0, 255)
)

var tempTextAnchorPoints = []redsmath.Vec2{
	{X: 11, Y: 0},
	{X: 3, Y: -3},
	{X: 3, Y: -12},
	{X: -3, Y: -5},
	{X: -12, Y: -8},
	{X: -7, Y: 0},
	{X: -12, Y: 8},
	{X: -3, Y: 5},
	{X: 3, Y: 12},
	{X: 3, Y: 3},
	{X: 11, Y: 0},
}

type TempDataType int

const (
	TempDataClosedArea TempDataType = iota
	TempDataRestrictedArea
)

type TempData struct {
	closedRunways map[string]bool

	closedAreas     []TempArea
	restrictedAreas []TempArea
	texts           []TempText

	nextAreaID int
	nextTextID int
}

type TempArea struct {
	ID       string
	Type     TempDataType
	Points   []redsmath.Vec2
	Hidden   bool
	Selected bool

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

	count := 0
	for _, area := range td.closedAreas {
		if !area.Hidden {
			count++
		}
	}
	for _, area := range td.restrictedAreas {
		if !area.Hidden {
			count++
		}
	}
	for _, text := range td.texts {
		if !text.Hidden {
			count++
		}
	}
	return count
}

func (td *TempData) AddArea(kind TempDataType, points []redsmath.Vec2) {
	if td == nil || len(points) < minTempAreaNodes+1 {
		return
	}

	vertices, indices := renderer.TessellateRings([][]redsmath.Vec2{points})
	if len(vertices) == 0 || len(indices) == 0 {
		return
	}

	area := TempArea{
		ID:           td.nextTempAreaID(kind),
		Type:         kind,
		Points:       append([]redsmath.Vec2(nil), points...),
		meshVertices: vertices,
		meshIndices:  indices,
	}

	switch kind {
	case TempDataClosedArea:
		td.closedAreas = append(td.closedAreas, area)
	case TempDataRestrictedArea:
		td.restrictedAreas = append(td.restrictedAreas, area)
	}
}

func (td *TempData) nextTempAreaID(kind TempDataType) string {
	if td.nextAreaID <= 0 {
		td.nextAreaID = 1
	}

	prefix := "CLOSED"
	if kind == TempDataRestrictedArea {
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

	cb.SetRGB(applyBrightness(renderer.RGB8(255, 255, 255), brightness, brightnessFloorDefault))
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

	td.drawAreas(cb, transforms, td.closedAreas, tempClosedAreaRGB, brightness)
}

func (td *TempData) DrawRestrictedAreas(
	cb *renderer.CmdBuffer,
	transforms radar.ScopeTransformations,
	brightness int,
) {
	if td == nil {
		return
	}

	td.drawAreas(cb, transforms, td.restrictedAreas, tempRestrictedAreaRGB, brightness)
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

	color := applyBrightness(rgb, brightness, brightnessFloorDefault)
	lineBuilder := renderer.GetLinesBuilder()
	for _, area := range areas {
		if area.Hidden || len(area.Points) < 2 {
			continue
		}

		points := make([]renderer.PointVertex, 0, len(area.Points))
		for _, pt := range area.Points {
			points = append(points, renderer.PointVertex{X: pt.X, Y: pt.Y})
		}
		lineBuilder.AddLineStrip(points)
	}
	cb.SetRGB(color)
	cb.LineWidth(1)
	lineBuilder.GenerateCommands(cb)
	renderer.ReturnLinesBuilder(lineBuilder)

	for _, area := range areas {
		if area.Hidden || len(area.meshVertices) == 0 || len(area.meshIndices) == 0 {
			continue
		}

		triBuilder := renderer.GetTrianglesBuilder()
		triBuilder.AddIndexed(area.meshVertices, area.meshIndices)
		cb.SetRGB(color)
		triBuilder.GenerateCommands(cb, renderer.DrawHatched, tempAreaHatchOffset(area, transforms))
		renderer.ReturnTrianglesBuilder(triBuilder)
	}
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

	builder := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(builder)

	for _, text := range td.texts {
		if text.Hidden {
			continue
		}
		addTempTextAnchor(builder, transforms.WindowFromWorldP(text.Location))
	}

	cb.SetRGB(applyBrightness(tempTextRGB, brightness, brightnessFloorDefault))
	cb.LineWidth(1)
	builder.GenerateCommands(cb)
}

func addTempTextAnchor(builder *renderer.LinesBuilder, center redsmath.Vec2) {
	if builder == nil {
		return
	}

	points := make([]renderer.PointVertex, 0, len(tempTextAnchorPoints))
	for _, pt := range tempTextAnchorPoints {
		points = append(points, renderer.PointVertex{
			X: center.X + pt.X,
			Y: center.Y + pt.Y,
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
) {
	if td == nil || cb == nil || font == nil || textureForSize == nil || len(td.texts) == 0 {
		return
	}

	fontSize := tempTextFontSizeDefault
	textureID := textureForSize(fontSize)
	if textureID == 0 {
		return
	}

	lineBuilder := renderer.GetLinesBuilder()
	tdb := renderer.GetTextDrawBuilder()
	tdb.SetFont(font)

	for _, text := range td.texts {
		if text.Hidden {
			continue
		}

		color := applyBrightness(tempTextColor(text), tempTextBrightness(text), brightnessFloorDefault)
		drawOneTempText(text, transforms, lineBuilder, tdb, font, fontSize, settings, color)
	}

	cb.SetRGB(applyBrightness(tempTextRGB, tempTextBrightnessDefault, brightnessFloorDefault))
	cb.LineWidth(1)
	lineBuilder.GenerateCommands(cb)
	tdb.GenerateCommands(cb, textureID)

	renderer.ReturnTextDrawBuilder(tdb)
	renderer.ReturnLinesBuilder(lineBuilder)
}

func tempTextColor(text TempText) renderer.RGB {
	if text.Highlighted {
		return tempTextHighlightRGB
	}
	return tempTextRGB
}

func tempTextBrightness(text TempText) int {
	if text.Brightness == nil {
		return tempTextBrightnessDefault
	}
	return *text.Brightness
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

	leaderStart := anchor.Add(leaderDelta(tempTextAnchorOffsetPx, heading))
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
		p.closeDcbCurrentSubmenu()
		return true
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
		DcbFunctionHideTempData,
		DcbFunctionDeleteGlobalTempData:
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

func (p *ASDEXPane) startDefineTempArea(kind TempDataType, commandLine string) {
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
		Type:   kind,
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
	cb.SetRGB(applyBrightness(tempAreaDrawRGB, brightnessDefault, brightnessFloorDefault))
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

	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
	p.dcbSpinner = nil
	p.commandEntry.Clear()
	p.datablockEdit = nil
	p.editingTargetID = ""
	p.initControlEntry = nil
	p.termControlEntry = nil
	p.multiFunction = nil
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
	if p == nil {
		return
	}

	p.dcb.SetMenu(DcbMenuClosedRunway)
	p.clearTempDataCommandConflicts()
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA", "CLOSED RUNWAY")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) closeDcbCurrentSubmenu() {
	if p == nil {
		return
	}

	switch p.dcb.Menu() {
	case DcbMenuClosedRunway:
		p.dcb.SetMenu(DcbMenuTempData)
		p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA")
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
	p.dcbSpinner = nil
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
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
	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
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
