package asdex

import (
	"fmt"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

type DataBlockAreaType int

const (
	DataBlockAreaTrait DataBlockAreaType = iota
	DataBlockAreaOff
)

type DataBlockAreaTraits struct {
	DataBlocksOff bool

	FullDataBlocks bool

	ShowAltitude    bool
	ShowTargetType  bool
	ShowSensors     bool
	ShowCWT         bool
	ShowFix         bool
	ShowVelocity    bool
	ShowScratchpads bool

	FontSize        int
	Brightness      int
	ShowVector      bool
	LeaderLength    int
	LeaderDirection LeaderDirection
}

type DataBlockArea struct {
	ID     string
	Type   DataBlockAreaType
	Points []redsmath.Vec2
	Bounds redsmath.Rect
	Traits DataBlockAreaTraits
}

type DataBlockAreaDraft struct {
	Type     DataBlockAreaType
	WindowID ScopeWindowID
	Points   []redsmath.Vec2
	Mouse    redsmath.Vec2
}

type DataBlockAreaSelectionMode int

const (
	DataBlockAreaSelectionNone DataBlockAreaSelectionMode = iota
	DataBlockAreaSelectionModifyTrait
	DataBlockAreaSelectionDeleteOne
)

type DataBlockAreaSelection struct {
	Mode      DataBlockAreaSelectionMode
	WindowID  ScopeWindowID
	HoveredID string
}

type DataBlockAreaEditMode int

const (
	DataBlockAreaEditDefineTrait DataBlockAreaEditMode = iota
	DataBlockAreaEditModifyTrait
)

var (
	dbAreaOffRGB   = renderer.RGB8(255, 0, 0)
	dbAreaTraitRGB = renderer.RGB8(0, 255, 0)
	dbAreaDrawRGB  = renderer.RGB8(255, 255, 255)
)

func dbAreaEditModeForMenu(menu DcbMenu) DataBlockAreaEditMode {
	if menu == DcbMenuModifyTraitArea {
		return DataBlockAreaEditModifyTrait
	}
	return DataBlockAreaEditDefineTrait
}

func dbAreaEditReturnContext(menu DcbMenu) (DcbMenu, []string) {
	switch menu {
	case DcbMenuModifyTraitArea:
		return DcbMenuModifyTraitArea, []string{"DB AREA", "MODIFY TRAIT AREA"}
	default:
		return DcbMenuDefineTraitArea, []string{"DB AREA", "DEFINE TRAIT AREA"}
	}
}

func dbAreaEditCommandLines(mode DataBlockAreaEditMode) []string {
	switch mode {
	case DataBlockAreaEditModifyTrait:
		return []string{"DB AREA", "MODIFY TRAIT AREA"}
	default:
		return []string{"DB AREA", "DEFINE TRAIT AREA"}
	}
}

func dbAreaEditMenu(mode DataBlockAreaEditMode) DcbMenu {
	switch mode {
	case DataBlockAreaEditModifyTrait:
		return DcbMenuModifyTraitArea
	default:
		return DcbMenuDefineTraitArea
	}
}

func DefaultDataBlockAreaTraits() DataBlockAreaTraits {
	fields := DefaultDataBlockFieldSettings()
	return DataBlockAreaTraits{
		DataBlocksOff: false,

		FullDataBlocks: true,

		ShowAltitude:    fields.ShowAltitude,
		ShowTargetType:  fields.ShowTargetType,
		ShowSensors:     fields.ShowSensors,
		ShowCWT:         fields.ShowCWT,
		ShowFix:         fields.ShowFix,
		ShowVelocity:    fields.ShowVelocity,
		ShowScratchpads: fields.ShowScratchpads,

		FontSize:        2,
		Brightness:      brightnessDefault,
		ShowVector:      true,
		LeaderLength:    2,
		LeaderDirection: LeaderNE,
	}
}

func NewDataBlockOffArea(id string, points []redsmath.Vec2) DataBlockArea {
	traits := DefaultDataBlockAreaTraits()
	traits.DataBlocksOff = true
	return DataBlockArea{
		ID:     id,
		Type:   DataBlockAreaOff,
		Points: append([]redsmath.Vec2(nil), points...),
		Bounds: boundsForTempPolygon(points),
		Traits: traits,
	}
}

func NewDataBlockTraitArea(id string, points []redsmath.Vec2) DataBlockArea {
	traits := DefaultDataBlockAreaTraits()
	traits.DataBlocksOff = false
	return DataBlockArea{
		ID:     id,
		Type:   DataBlockAreaTrait,
		Points: append([]redsmath.Vec2(nil), points...),
		Bounds: boundsForTempPolygon(points),
		Traits: traits,
	}
}

func (s *WindowDisplayState) nextDataBlockAreaID() string {
	if s == nil {
		return ""
	}
	if s.NextDBAreaID <= 0 {
		s.NextDBAreaID = 1
	}

	id := fmt.Sprintf("DBAREA:%d", s.NextDBAreaID)
	s.NextDBAreaID++
	return id
}

func (s *WindowDisplayState) selectedDataBlockArea() (*DataBlockArea, bool) {
	if s == nil || s.SelectedDBAreaID == "" {
		return nil, false
	}

	for i := range s.DataBlockAreas {
		if s.DataBlockAreas[i].ID == s.SelectedDBAreaID {
			return &s.DataBlockAreas[i], true
		}
	}
	return nil, false
}

func (p *ASDEXPane) startDefineDbOffArea() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	windowID := p.activeWindowID()

	p.dcb.SetMenu(DcbMenuDbArea)
	p.dcbMenuCommand = NewDcbMenuCommand("DB AREA", "DEFINE OFF AREA")
	p.dbAreaDraft = &DataBlockAreaDraft{
		Type:     DataBlockAreaOff,
		WindowID: windowID,
		Points:   make([]redsmath.Vec2, 0, maxTempAreaNodes+1),
	}
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startDefineDbTraitArea() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	windowID := p.activeWindowID()

	p.dcb.SetMenu(DcbMenuDbArea)
	p.dcbMenuCommand = NewDcbMenuCommand("DB AREA", "DEFINE TRAIT AREA")
	p.dbAreaDraft = &DataBlockAreaDraft{
		Type:     DataBlockAreaTrait,
		WindowID: windowID,
		Points:   make([]redsmath.Vec2, 0, maxTempAreaNodes+1),
	}
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startModifyDbTraitArea() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.dbAreaSelection = &DataBlockAreaSelection{
		Mode:     DataBlockAreaSelectionModifyTrait,
		WindowID: p.activeWindowID(),
	}
	p.dcb.SetMenu(DcbMenuDbArea)
	p.dcbMenuCommand = NewDcbMenuCommand("DB AREA", "MODIFY TRAIT AREA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) activeWindowRectAndTransform(
	ctx *panes.Context,
	referenceExtent redsmath.Rect,
) (ScopeWindowID, redsmath.Rect, radar.ScopeTransformations, bool) {
	if p == nil || ctx == nil {
		return 0, redsmath.Rect{}, radar.ScopeTransformations{}, false
	}

	windowID := p.activeWindowID()
	view, ok := p.scopeViewForWindow(windowID)
	if !ok {
		return 0, redsmath.Rect{}, radar.ScopeTransformations{}, false
	}

	rect, ok := p.scopeWindowRectForWindow(windowID, ctx.PaneSize())
	if !ok {
		return 0, redsmath.Rect{}, radar.ScopeTransformations{}, false
	}

	return windowID, rect, scopeTransformForWindow(rect, referenceExtent, view), true
}

func (p *ASDEXPane) consumeDataBlockAreaDraftInput(
	ctx *panes.Context,
	referenceExtent redsmath.Rect,
) bool {
	if p == nil || p.dbAreaDraft == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}

	windowID, windowRect, transforms, ok := p.activeWindowRectAndTransform(ctx, referenceExtent)
	if !ok || windowID != p.dbAreaDraft.WindowID {
		return false
	}

	if !windowRect.Contains(ctx.Mouse.Pos) {
		return true
	}

	localMouse := ctx.Mouse.Pos.Sub(windowRect.Min)
	world := transforms.WorldFromWindowP(localMouse)
	p.dbAreaDraft.Mouse = world

	switch {
	case ctx.Mouse.WasReleased(platform.MouseButtonLeft):
		p.addDataBlockAreaDraftPoint(world)
		return true

	case ctx.Mouse.WasReleased(platform.MouseButtonMiddle):
		p.finishDataBlockAreaDraft()
		return true
	}

	return true
}

func (p *ASDEXPane) addDataBlockAreaDraftPoint(point redsmath.Vec2) {
	if p == nil || p.dbAreaDraft == nil {
		return
	}

	draft := p.dbAreaDraft
	state := p.displayStateForWindow(draft.WindowID)
	if len(draft.Points) > 0 {
		last := draft.Points[len(draft.Points)-1]
		if tempSegmentWouldSelfIntersect(last, point, draft.Points) ||
			dbAreaSegmentIntersectsExisting(last, point, state.DataBlockAreas) {
			return
		}
	}
	if dbAreaPointInsideExisting(point, state.DataBlockAreas) {
		return
	}

	draft.Points = append(draft.Points, point)
	p.previewArea.SetSystemResponse("")

	if len(draft.Points) >= maxTempAreaNodes {
		p.finishDataBlockAreaDraft()
	}
}

func (p *ASDEXPane) finishDataBlockAreaDraft() {
	if p == nil || p.dbAreaDraft == nil {
		return
	}

	draft := p.dbAreaDraft
	state := p.displayStateForWindow(draft.WindowID)
	if len(draft.Points) < minTempAreaNodes {
		draft.Points = draft.Points[:0]
		p.previewArea.SetSystemResponse("BAD POLYGON,REDRAW POINT")
		return
	}

	last := draft.Points[len(draft.Points)-1]
	first := draft.Points[0]
	if tempClosingSegmentWouldSelfIntersect(last, first, draft.Points) ||
		dbAreaSegmentIntersectsExisting(last, first, state.DataBlockAreas) {
		p.previewArea.SetSystemResponse("BAD POLYGON,REDRAW POINT")
		return
	}

	polygon := append([]redsmath.Vec2(nil), draft.Points...)
	polygon = append(polygon, first)
	if dbAreaContainsAnyExistingPoint(polygon, state.DataBlockAreas) {
		p.previewArea.SetSystemResponse("BAD POLYGON,REDRAW POINT")
		return
	}

	id := state.nextDataBlockAreaID()
	if id == "" {
		return
	}

	var area DataBlockArea
	switch draft.Type {
	case DataBlockAreaTrait:
		area = NewDataBlockTraitArea(id, polygon)
	case DataBlockAreaOff:
		area = NewDataBlockOffArea(id, polygon)
	default:
		area = NewDataBlockOffArea(id, polygon)
	}

	state.DataBlockAreas = append(state.DataBlockAreas, area)
	state.SelectedDBAreaID = id

	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	switch draft.Type {
	case DataBlockAreaTrait:
		p.dcb.SetMenu(DcbMenuDefineTraitArea)
		p.dcbMenuCommand = NewDcbMenuCommand("DB AREA", "DEFINE TRAIT AREA")
	case DataBlockAreaOff:
		p.dcb.SetMenu(DcbMenuDbArea)
		p.dcbMenuCommand = NewDcbMenuCommand("DB AREA")
	default:
		p.dcb.SetMenu(DcbMenuDbArea)
		p.dcbMenuCommand = NewDcbMenuCommand("DB AREA")
	}
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) cancelDataBlockAreaDraft() {
	if p == nil || p.dbAreaDraft == nil {
		return
	}

	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.dcb.SetMenu(DcbMenuDbArea)
	p.dcbMenuCommand = NewDcbMenuCommand("DB AREA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) cancelDataBlockAreaSelection() {
	if p == nil || p.dbAreaSelection == nil {
		return
	}

	p.dbAreaSelection = nil
	p.dcb.SetMenu(DcbMenuDbArea)
	p.dcbMenuCommand = NewDcbMenuCommand("DB AREA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) handleDataBlockAreaDraftKeyboard(ctx *panes.Context) bool {
	if p == nil || p.dbAreaDraft == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	if keyboard.WasPressed(platform.KeyEscape) ||
		keyboard.WasPressed(platform.KeyBackspace) ||
		keyboard.WasPressed(platform.KeyDelete) {
		p.cancelDataBlockAreaDraft()
		return true
	}
	return false
}

func (p *ASDEXPane) handleDataBlockAreaSelectionKeyboard(ctx *panes.Context) bool {
	if p == nil || p.dbAreaSelection == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	if keyboard.WasPressed(platform.KeyEscape) ||
		keyboard.WasPressed(platform.KeyBackspace) ||
		keyboard.WasPressed(platform.KeyDelete) {
		p.cancelDataBlockAreaSelection()
		return true
	}
	return false
}

func (p *ASDEXPane) updateDataBlockAreaSelectionHover(
	ctx *panes.Context,
	referenceExtent redsmath.Rect,
) {
	if p == nil || p.dbAreaSelection == nil || ctx == nil || ctx.Mouse == nil {
		return
	}

	selection := p.dbAreaSelection
	selection.HoveredID = ""

	windowID, windowRect, transforms, ok := p.activeWindowRectAndTransform(ctx, referenceExtent)
	if !ok || windowID != selection.WindowID {
		return
	}
	if !windowRect.Contains(ctx.Mouse.Pos) {
		return
	}

	localMouse := ctx.Mouse.Pos.Sub(windowRect.Min)
	world := transforms.WorldFromWindowP(localMouse)
	if area, ok := p.hitTestDataBlockArea(windowID, world, selection.Mode); ok {
		selection.HoveredID = area.ID
	}
}

func (p *ASDEXPane) consumeDataBlockAreaSelectionInput(
	ctx *panes.Context,
	referenceExtent redsmath.Rect,
) bool {
	if p == nil || p.dbAreaSelection == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}

	selection := p.dbAreaSelection
	windowID, windowRect, transforms, ok := p.activeWindowRectAndTransform(ctx, referenceExtent)
	if !ok || windowID != selection.WindowID {
		return false
	}
	if !windowRect.Contains(ctx.Mouse.Pos) {
		return true
	}
	if ctx.Mouse.WasReleased(platform.MouseButtonRight) {
		return true
	}
	if !ctx.Mouse.WasReleased(platform.MouseButtonLeft) &&
		!ctx.Mouse.WasReleased(platform.MouseButtonMiddle) {
		return true
	}

	localMouse := ctx.Mouse.Pos.Sub(windowRect.Min)
	world := transforms.WorldFromWindowP(localMouse)
	area, hit := p.hitTestDataBlockArea(windowID, world, selection.Mode)
	if !hit {
		return true
	}

	switch selection.Mode {
	case DataBlockAreaSelectionModifyTrait:
		p.selectDataBlockTraitAreaForModify(windowID, area.ID)
		return true
	case DataBlockAreaSelectionDeleteOne:
		return true
	default:
		return true
	}
}

func (p *ASDEXPane) selectDataBlockTraitAreaForModify(
	windowID ScopeWindowID,
	areaID string,
) {
	if p == nil || areaID == "" {
		return
	}

	state := p.displayStateForWindow(windowID)
	state.SelectedDBAreaID = areaID
	p.dbAreaSelection = nil
	p.dcb.SetMenu(DcbMenuModifyTraitArea)
	p.dcbMenuCommand = NewDcbMenuCommand("DB AREA", "MODIFY TRAIT AREA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) hitTestDataBlockArea(
	windowID ScopeWindowID,
	point redsmath.Vec2,
	mode DataBlockAreaSelectionMode,
) (*DataBlockArea, bool) {
	if p == nil {
		return nil, false
	}

	state := p.displayStateForWindow(windowID)
	for i := len(state.DataBlockAreas) - 1; i >= 0; i-- {
		area := &state.DataBlockAreas[i]
		if !dataBlockAreaSelectableForMode(*area, mode) {
			continue
		}
		if dataBlockAreaContains(*area, point) {
			return area, true
		}
	}
	return nil, false
}

func dataBlockAreaSelectableForMode(
	area DataBlockArea,
	mode DataBlockAreaSelectionMode,
) bool {
	switch mode {
	case DataBlockAreaSelectionModifyTrait:
		return area.Type == DataBlockAreaTrait && !area.Traits.DataBlocksOff
	case DataBlockAreaSelectionDeleteOne:
		return true
	default:
		return false
	}
}

func dbAreaPointInsideExisting(point redsmath.Vec2, areas []DataBlockArea) bool {
	for _, area := range areas {
		if dataBlockAreaContains(area, point) {
			return true
		}
	}
	return false
}

func dbAreaSegmentIntersectsExisting(a, b redsmath.Vec2, areas []DataBlockArea) bool {
	for _, area := range areas {
		for i := 0; i+1 < len(area.Points); i++ {
			if segmentsIntersectStrict(a, b, area.Points[i], area.Points[i+1]) {
				return true
			}
		}
	}
	return false
}

func dbAreaContainsAnyExistingPoint(points []redsmath.Vec2, existing []DataBlockArea) bool {
	for _, area := range existing {
		for _, point := range area.Points {
			if pointInPolygon(points, point) {
				return true
			}
		}
	}
	return false
}

func dataBlockAreaContains(area DataBlockArea, point redsmath.Vec2) bool {
	if len(area.Points) < minTempAreaNodes {
		return false
	}
	if area.Bounds.Empty() || !area.Bounds.Contains(point) {
		return false
	}
	return pointInPolygon(area.Points, point)
}

func (p *ASDEXPane) showsDataBlockAreas() bool {
	if p == nil {
		return false
	}
	if p.dbAreaDraft != nil || p.dbAreaSelection != nil {
		return true
	}

	switch p.dcb.Menu() {
	case DcbMenuDbArea, DcbMenuDefineTraitArea, DcbMenuModifyTraitArea:
		return true
	default:
		return false
	}
}

func (p *ASDEXPane) drawDataBlockAreas(cb *renderer.CmdBuffer, windowID ScopeWindowID) {
	if p == nil || cb == nil || !p.showsDataBlockAreas() {
		return
	}

	state := p.displayStateForWindow(windowID)
	for _, area := range state.DataBlockAreas {
		if len(area.Points) < 2 {
			continue
		}

		builder := renderer.GetLinesBuilder()
		points := make([]renderer.PointVertex, 0, len(area.Points))
		for _, pt := range area.Points {
			points = append(points, renderer.PointVertex{X: pt.X, Y: pt.Y})
		}
		builder.AddLineStrip(points)

		rgb := dbAreaOffRGB
		if area.Type == DataBlockAreaTrait {
			rgb = dbAreaTraitRGB
		}
		cb.SetRGB(applyBrightness(rgb, brightnessDefault, brightnessFloorDefault))
		cb.LineWidth(1)
		builder.GenerateCommands(cb)
		renderer.ReturnLinesBuilder(builder)
	}
}

func (p *ASDEXPane) drawDataBlockAreaDraft(cb *renderer.CmdBuffer, windowID ScopeWindowID) {
	if p == nil || p.dbAreaDraft == nil || cb == nil {
		return
	}
	if p.dbAreaDraft.WindowID != windowID || len(p.dbAreaDraft.Points) == 0 {
		return
	}

	points := make([]renderer.PointVertex, 0, len(p.dbAreaDraft.Points)+1)
	for _, pt := range p.dbAreaDraft.Points {
		points = append(points, renderer.PointVertex{X: pt.X, Y: pt.Y})
	}
	points = append(points, renderer.PointVertex{
		X: p.dbAreaDraft.Mouse.X,
		Y: p.dbAreaDraft.Mouse.Y,
	})

	builder := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(builder)

	builder.AddLineStrip(points)
	cb.SetRGB(applyBrightness(dbAreaDrawRGB, brightnessDefault, brightnessFloorDefault))
	cb.LineWidth(1)
	builder.GenerateCommands(cb)
}

func (p *ASDEXPane) dataBlockAreaForPoint(
	windowID ScopeWindowID,
	point redsmath.Vec2,
) (*DataBlockArea, bool) {
	if p == nil {
		return nil, false
	}

	state := p.displayStateForWindow(windowID)
	for i := len(state.DataBlockAreas) - 1; i >= 0; i-- {
		area := &state.DataBlockAreas[i]
		if dataBlockAreaContains(*area, point) {
			return area, true
		}
	}
	return nil, false
}

func (p *ASDEXPane) dataBlockTraitAreaForPoint(
	windowID ScopeWindowID,
	point redsmath.Vec2,
) (*DataBlockArea, bool) {
	area, ok := p.dataBlockAreaForPoint(windowID, point)
	if !ok || area.Type != DataBlockAreaTrait || area.Traits.DataBlocksOff {
		return nil, false
	}
	return area, true
}

func applyDataBlockAreaTraits(settings DataBlockSettings, traits DataBlockAreaTraits) DataBlockSettings {
	if traits.DataBlocksOff {
		settings.DataBlocksOff = true
		return settings
	}

	settings.FullDataBlocks = traits.FullDataBlocks
	settings.ShowAltitude = traits.ShowAltitude
	settings.ShowTargetType = traits.ShowTargetType
	settings.ShowSensors = traits.ShowSensors
	settings.ShowCWT = traits.ShowCWT
	settings.ShowFix = traits.ShowFix
	settings.ShowVelocity = traits.ShowVelocity
	settings.ShowScratchpads = traits.ShowScratchpads
	settings.FontSize = traits.FontSize
	settings.Brightness = traits.Brightness
	settings.LeaderLength = traits.LeaderLength
	settings.LeaderDirection = traits.LeaderDirection
	return settings
}

func leaderDirectionDisplayValue(direction LeaderDirection) string {
	switch direction {
	case LeaderSW:
		return "1"
	case LeaderS:
		return "2"
	case LeaderSE:
		return "3"
	case LeaderW:
		return "4"
	case LeaderE:
		return "6"
	case LeaderNW:
		return "7"
	case LeaderN:
		return "8"
	default:
		return "9"
	}
}

func leaderDirectionFromDisplayValue(value int) (LeaderDirection, bool) {
	switch value {
	case 1:
		return LeaderSW, true
	case 2:
		return LeaderS, true
	case 3:
		return LeaderSE, true
	case 4:
		return LeaderW, true
	case 6:
		return LeaderE, true
	case 7:
		return LeaderNW, true
	case 8:
		return LeaderN, true
	case 9:
		return LeaderNE, true
	default:
		return LeaderNE, false
	}
}
