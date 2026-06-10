package asdex

import (
	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

type ScopeView struct {
	Center       redsmath.Vec2
	RangeSetting int
	// RangeFeet is the CRC ASDE-X range scale: RangeSetting * 100. It is
	// converted to an effective visible half-height by the scope transform.
	RangeFeet float32
	Rotation  float32
}

type ScopeWindowID int

const (
	mainScopeWindowID ScopeWindowID = 0
)

type ScopeWindow struct {
	ID     ScopeWindowID
	Rect   redsmath.Rect
	View   ScopeView
	Hidden bool
}

// WindowDisplayState holds per-scope-window display settings. World objects
// such as targets, temp data, holdbars and runway closures remain global.
type WindowDisplayState struct {
	DB DataBlockSettings

	TargetShowDBOverrides map[string]bool

	LeaderDirectionOverrides map[string]LeaderDirection
	LeaderLengthOverrides    map[string]int

	// Later:
	// TempTextShowDBOverrides map[string]bool
	// TempTextLeaderDirectionOverrides map[string]LeaderDirection
	// TempTextLeaderLengthOverrides map[string]int
	// DbTraitAreas []DataBlockTraitArea
}

func NewWindowDisplayState() *WindowDisplayState {
	return &WindowDisplayState{
		DB: DefaultDataBlockSettings(),
	}
}

// ScopeHoverState is transient window-local hover state. It is not a target
// property because the same target can be rendered in multiple scope windows.
type ScopeHoverState struct {
	WindowID ScopeWindowID
	TargetID string

	MouseWorld redsmath.Vec2
	Revision   uint64
	Valid      bool
}

type ScopeWindowManager struct {
	nextID   ScopeWindowID
	activeID ScopeWindowID

	secondary []ScopeWindow
}

type NewWindowCommand struct {
	firstCorner *redsmath.Vec2
	mouse       redsmath.Vec2
}

const (
	maxSecondaryWindows = 4

	mainWindowBorderWidth      = float32(2)
	secondaryWindowBorderWidth = float32(4)
	proposedWindowBorderWidth  = float32(4)

	windowOverlapPadding = float32(4)

	minSecondaryWindowWidth  = float32(80)
	minSecondaryWindowHeight = float32(80)
)

var (
	windowBorderRGB         = renderer.RGB8(255, 255, 255)
	activeWindowBorderRGB   = renderer.RGB8(0, 255, 0)
	proposedWindowBorderRGB = renderer.RGB8(255, 255, 0)
)

func NewScopeWindowManager() ScopeWindowManager {
	return ScopeWindowManager{
		nextID:   1,
		activeID: mainScopeWindowID,
	}
}

func NewNewWindowCommand() *NewWindowCommand {
	return &NewWindowCommand{}
}

func (cmd *NewWindowCommand) DisplayLines() []string {
	if cmd == nil {
		return nil
	}
	return []string{"NEW WINDOW"}
}

func (wm *ScopeWindowManager) CanAddSecondary() bool {
	return wm != nil && len(wm.secondary) < maxSecondaryWindows
}

func (wm *ScopeWindowManager) AddSecondary(rect redsmath.Rect, view ScopeView) (ScopeWindowID, bool) {
	if wm == nil || !wm.CanAddSecondary() || rect.Empty() {
		return 0, false
	}

	id := wm.nextID
	wm.nextID++
	wm.secondary = append(wm.secondary, ScopeWindow{
		ID:   id,
		Rect: rect,
		View: view,
	})
	return id, true
}

func (wm *ScopeWindowManager) SetActiveWindow(id ScopeWindowID) {
	if wm == nil {
		return
	}

	if id == mainScopeWindowID {
		wm.activeID = id
		return
	}

	for _, win := range wm.secondary {
		if !win.Hidden && win.ID == id {
			wm.activeID = id
			return
		}
	}
}

func (wm *ScopeWindowManager) ActiveWindowID() ScopeWindowID {
	if wm == nil {
		return mainScopeWindowID
	}
	return wm.activeID
}

func (wm *ScopeWindowManager) ProposedWindowIsValid(rect redsmath.Rect, paneSize redsmath.Vec2) bool {
	if wm == nil || rect.Empty() {
		return false
	}
	if rect.Width() < minSecondaryWindowWidth || rect.Height() < minSecondaryWindowHeight {
		return false
	}
	if rect.Min.X < 0 || rect.Min.Y < 0 || rect.Max.X > paneSize.X || rect.Max.Y > paneSize.Y {
		return false
	}

	proposed := inflateRect(rect, windowOverlapPadding)
	for _, win := range wm.secondary {
		if win.Hidden {
			continue
		}
		if rectsIntersect(proposed, inflateRect(win.Rect, windowOverlapPadding)) {
			return false
		}
	}
	return true
}

func (p *ASDEXPane) mainScopeView() ScopeView {
	if p == nil {
		return ScopeView{}
	}
	return ScopeView{
		Center:       p.center,
		RangeSetting: p.rangeSetting,
		RangeFeet:    p.rangeFeet,
		Rotation:     p.rotation,
	}
}

func (p *ASDEXPane) applyMainScopeView(view ScopeView) {
	if p == nil {
		return
	}
	p.center = view.Center
	p.rangeSetting = view.RangeSetting
	p.rangeFeet = view.RangeFeet
	p.rotation = view.Rotation
}

func (p *ASDEXPane) setScopeView(id ScopeWindowID, view ScopeView) {
	if p == nil {
		return
	}
	if id == mainScopeWindowID {
		p.applyMainScopeView(view)
		return
	}
	for i := range p.windows.secondary {
		if p.windows.secondary[i].ID == id {
			p.windows.secondary[i].View = view
			return
		}
	}
}

func (p *ASDEXPane) scopeViewForWindow(id ScopeWindowID) (ScopeView, bool) {
	if p == nil {
		return ScopeView{}, false
	}

	if id == mainScopeWindowID {
		return p.mainScopeView(), true
	}

	for _, win := range p.windows.secondary {
		if !win.Hidden && win.ID == id {
			return win.View, true
		}
	}
	return ScopeView{}, false
}

func (p *ASDEXPane) scopeWindowRectForWindow(
	id ScopeWindowID,
	paneSize redsmath.Vec2,
) (redsmath.Rect, bool) {
	if p == nil {
		return redsmath.Rect{}, false
	}

	if id == mainScopeWindowID {
		return redsmath.RectFromSize(paneSize.X, paneSize.Y), true
	}

	for _, win := range p.windows.secondary {
		if !win.Hidden && win.ID == id {
			return win.Rect, true
		}
	}
	return redsmath.Rect{}, false
}

func (p *ASDEXPane) updateScopeViewForWindow(
	id ScopeWindowID,
	update func(*ScopeView),
) bool {
	if p == nil || update == nil {
		return false
	}

	if id == mainScopeWindowID {
		view := p.mainScopeView()
		update(&view)
		p.applyMainScopeView(view)
		return true
	}

	for i := range p.windows.secondary {
		if p.windows.secondary[i].Hidden || p.windows.secondary[i].ID != id {
			continue
		}

		view := p.windows.secondary[i].View
		update(&view)
		p.windows.secondary[i].View = view
		return true
	}
	return false
}

func (p *ASDEXPane) activeScopeView() ScopeView {
	if p == nil {
		return ScopeView{}
	}

	view, ok := p.scopeViewForWindow(p.activeWindowID())
	if !ok {
		return p.mainScopeView()
	}
	return view
}

func (p *ASDEXPane) updateActiveScopeView(update func(*ScopeView)) bool {
	if p == nil {
		return false
	}
	return p.updateScopeViewForWindow(p.activeWindowID(), update)
}

func (p *ASDEXPane) scopeWindowAtPoint(
	point redsmath.Vec2,
	paneSize redsmath.Vec2,
) (ScopeWindowID, redsmath.Rect, ScopeView, bool) {
	if p == nil {
		return 0, redsmath.Rect{}, ScopeView{}, false
	}

	for i := len(p.windows.secondary) - 1; i >= 0; i-- {
		win := p.windows.secondary[i]
		if win.Hidden {
			continue
		}
		if win.Rect.Contains(point) {
			return win.ID, win.Rect, win.View, true
		}
	}

	mainRect := redsmath.RectFromSize(paneSize.X, paneSize.Y)
	if mainRect.Contains(point) {
		return mainScopeWindowID, mainRect, p.mainScopeView(), true
	}
	return 0, redsmath.Rect{}, ScopeView{}, false
}

func mainReferenceExtent(paneSize redsmath.Vec2) redsmath.Rect {
	return redsmath.RectFromSize(paneSize.X, paneSize.Y)
}

func scopeTransformForWindow(
	rect redsmath.Rect,
	referenceExtent redsmath.Rect,
	view ScopeView,
) radar.ScopeTransformations {
	return radar.GetScopeTransformationsWithReference(
		redsmath.RectFromSize(rect.Width(), rect.Height()),
		referenceExtent,
		view.Center,
		view.RangeFeet,
		view.Rotation,
	)
}

func (p *ASDEXPane) consumeNewWindowInput(
	ctx *panes.Context,
	mainTransforms radar.ScopeTransformations,
) bool {
	if p == nil || p.newWindow == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}

	mouse := ctx.Mouse
	paneSize := ctx.PaneSize()
	if !redsmath.RectFromSize(paneSize.X, paneSize.Y).Contains(mouse.Pos) {
		return false
	}

	p.newWindow.mouse = mouse.Pos
	if !mouse.WasReleased(platform.MouseButtonLeft) {
		return true
	}

	if p.newWindow.firstCorner == nil {
		first := mouse.Pos
		p.newWindow.firstCorner = &first
		p.previewArea.SetSystemResponse("")
		return true
	}

	rect := normalizeWindowRect(*p.newWindow.firstCorner, mouse.Pos)
	if !p.windows.ProposedWindowIsValid(rect, paneSize) {
		p.previewArea.SetSystemResponse("")
		return true
	}

	centerWindow := rect.Min.Add(rect.Size().Mul(0.5))
	centerWorld := mainTransforms.WorldFromWindowP(centerWindow)
	main := p.mainScopeView()
	view := ScopeView{
		Center:       centerWorld,
		RangeSetting: main.RangeSetting,
		RangeFeet:    main.RangeFeet,
		Rotation:     main.Rotation,
	}

	id, ok := p.windows.AddSecondary(rect, view)
	if ok {
		p.setDataBlockSettingsForWindow(
			id,
			p.dataBlockSettingsForWindow(mainScopeWindowID),
		)
	}
	p.newWindow = nil
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
	return true
}

func (p *ASDEXPane) maybeActivateScopeWindowOnLeftPress(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return
	}
	if !ctx.Mouse.WasPressed(platform.MouseButtonLeft) {
		return
	}

	if p.commandMode != CommandModeNone ||
		p.datablockEdit != nil ||
		p.newWindow != nil ||
		p.mapReposition != nil ||
		p.mapRotate != nil ||
		p.listRepositionActive() ||
		p.tempAreaDraft != nil ||
		p.tempTextCommand != nil ||
		p.tempTextPlacement != nil ||
		p.tempDataSelectMode != TempDataSelectNone {
		return
	}

	windowID, _, _, ok := p.scopeWindowAtPoint(ctx.Mouse.Pos, ctx.PaneSize())
	if !ok {
		return
	}
	p.windows.SetActiveWindow(windowID)
}

func (p *ASDEXPane) handleNewWindowKeyboard(ctx *panes.Context) bool {
	if p == nil || p.newWindow == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	if keyboard.WasPressed(platform.KeyEscape) ||
		keyboard.WasPressed(platform.KeyBackspace) ||
		keyboard.WasPressed(platform.KeyDelete) {
		p.newWindow = nil
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	}
	return false
}

func (p *ASDEXPane) renderWindowBorders(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.ScopeTransformations,
) {
	if p == nil || ctx == nil || zcb == nil {
		return
	}

	x, y, w, h := ctx.PaneFramebufferRect()
	cb := zcb.At(windowZ(0, zWindowBorders))
	cb.Viewport(x, y, w, h)
	cb.Scissor(x, y, w, h)
	transforms.LoadWindowViewingMatrices(cb)

	builder := renderer.GetColoredTrianglesBuilder()
	defer renderer.ReturnColoredTrianglesBuilder(builder)

	mainColor := windowBorderRGB
	if p.windows.activeID == mainScopeWindowID {
		mainColor = activeWindowBorderRGB
	}
	addWindowBorderRect(
		builder,
		redsmath.RectFromSize(ctx.PaneSize().X, ctx.PaneSize().Y),
		mainWindowBorderWidth,
		applyBrightness(mainColor, brightnessDefault, brightnessFloorDefault),
	)

	for _, win := range p.windows.secondary {
		if win.Hidden {
			continue
		}

		color := windowBorderRGB
		if p.windows.activeID == win.ID {
			color = activeWindowBorderRGB
		}
		addWindowBorderRect(
			builder,
			win.Rect,
			secondaryWindowBorderWidth,
			applyBrightness(color, brightnessDefault, brightnessFloorDefault),
		)
	}

	builder.GenerateCommands(cb)
	cb.DisableScissor()
}

func (p *ASDEXPane) renderNewWindowPreview(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.ScopeTransformations,
) {
	if p == nil || p.newWindow == nil || p.newWindow.firstCorner == nil ||
		ctx == nil || zcb == nil {
		return
	}

	rect := normalizeWindowRect(*p.newWindow.firstCorner, p.newWindow.mouse)
	if rect.Empty() {
		return
	}

	x, y, w, h := ctx.PaneFramebufferRect()
	cb := zcb.At(windowZ(0, zWindowBorders))
	cb.Viewport(x, y, w, h)
	cb.Scissor(x, y, w, h)
	transforms.LoadWindowViewingMatrices(cb)

	builder := renderer.GetColoredTrianglesBuilder()
	defer renderer.ReturnColoredTrianglesBuilder(builder)

	addWindowBorderRect(
		builder,
		rect,
		proposedWindowBorderWidth,
		applyBrightness(proposedWindowBorderRGB, brightnessDefault, brightnessFloorDefault),
	)
	builder.GenerateCommands(cb)
	cb.DisableScissor()
}

func (p *ASDEXPane) consumeScopeMouseEvents(
	ctx *panes.Context,
	windowRect redsmath.Rect,
	view ScopeView,
	transforms radar.ScopeTransformations,
) (ScopeView, bool) {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return view, false
	}

	mouse := ctx.Mouse
	if !windowRect.Contains(mouse.Pos) {
		return view, false
	}

	localMouse := mouse.Pos.Sub(windowRect.Min)
	changed := false
	if mouse.IsDown(platform.MouseButtonRight) &&
		(!p.rightClickCandidate || p.rightClickDragged) &&
		(mouse.Delta.X != 0 || mouse.Delta.Y != 0) {
		deltaWorld := transforms.WorldFromWindowV(mouse.Delta)
		view.Center = view.Center.Sub(deltaWorld)
		changed = true
	}

	if (mouse.Wheel.X != 0 || mouse.Wheel.Y != 0) &&
		ctx.Keyboard != nil &&
		ctx.Keyboard.IsDown(platform.KeyShift) {
		view.Rotation = normalizeRotation(view.Rotation + wheelRotationDelta(mouse.Wheel))
		return view, true
	}

	if mouse.Wheel.Y != 0 {
		oldRangeFeet := view.RangeFeet
		oldCenter := view.Center
		view.RangeSetting = clampInt(
			view.RangeSetting+wheelRangeDelta(mouse.Wheel.Y),
			asdexMinRangeSetting,
			asdexMaxRangeSetting,
		)
		view.RangeFeet = rangeFeetFromSetting(view.RangeSetting)
		newRangeFeet := view.RangeFeet

		if oldRangeFeet > 0 && newRangeFeet > 0 && newRangeFeet != oldRangeFeet {
			if ctx.Keyboard != nil && ctx.Keyboard.IsDown(platform.KeyAlt) {
				mouseWorld := transforms.WorldFromWindowP(localMouse)
				scale := newRangeFeet / oldRangeFeet
				view.Center = mouseWorld.Add(oldCenter.Sub(mouseWorld).Mul(scale))
			}
			changed = true
		}
	}

	return view, changed
}

func wheelRotationDelta(wheel redsmath.Vec2) float32 {
	switch {
	case wheel.Y > 0:
		return 1
	case wheel.Y < 0:
		return -1
	case wheel.X > 0:
		return 1
	case wheel.X < 0:
		return -1
	default:
		return 0
	}
}

func normalizeWindowRect(a, b redsmath.Vec2) redsmath.Rect {
	minX := min32(a.X, b.X)
	minY := min32(a.Y, b.Y)
	maxX := max32(a.X, b.X)
	maxY := max32(a.Y, b.Y)
	return redsmath.NewRect(minX+2, minY+2, maxX, maxY)
}

func inflateRect(r redsmath.Rect, amount float32) redsmath.Rect {
	return redsmath.NewRect(
		r.Min.X-amount,
		r.Min.Y-amount,
		r.Max.X+amount,
		r.Max.Y+amount,
	)
}

func rectsIntersect(a, b redsmath.Rect) bool {
	return a.Min.X < b.Max.X &&
		a.Max.X > b.Min.X &&
		a.Min.Y < b.Max.Y &&
		a.Max.Y > b.Min.Y
}

func scopeFramebufferRect(ctx *panes.Context, rect redsmath.Rect) (x, y, w, h int) {
	if ctx == nil {
		return 0, 0, 0, 0
	}
	return ctx.LogicalToFramebufferRect(rect.Translate(ctx.PaneRect.Min))
}

func addWindowBorderRect(
	builder *renderer.ColoredTrianglesBuilder,
	rect redsmath.Rect,
	width float32,
	color renderer.RGB,
) {
	if builder == nil || rect.Empty() || width <= 0 {
		return
	}

	addSolidWindowRect(builder, redsmath.NewRect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+width), color)
	addSolidWindowRect(builder, redsmath.NewRect(rect.Min.X, rect.Max.Y-width, rect.Max.X, rect.Max.Y), color)
	addSolidWindowRect(builder, redsmath.NewRect(rect.Min.X, rect.Min.Y, rect.Min.X+width, rect.Max.Y), color)
	addSolidWindowRect(builder, redsmath.NewRect(rect.Max.X-width, rect.Min.Y, rect.Max.X, rect.Max.Y), color)
}

func addSolidWindowRect(
	builder *renderer.ColoredTrianglesBuilder,
	rect redsmath.Rect,
	color renderer.RGB,
) {
	if builder == nil || rect.Empty() {
		return
	}
	builder.AddQuad(
		renderer.PointVertex{X: rect.Min.X, Y: rect.Min.Y},
		renderer.PointVertex{X: rect.Max.X, Y: rect.Min.Y},
		renderer.PointVertex{X: rect.Max.X, Y: rect.Max.Y},
		renderer.PointVertex{X: rect.Min.X, Y: rect.Max.Y},
		color,
	)
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
