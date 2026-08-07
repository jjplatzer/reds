package eram

import (
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"
)

// CRC implements a number of ERAM display objects as MovableViewBase views:
// left-click enters captured placement mode and middle-click requests a small
// settings menu associated with the view. Keep that interaction machinery
// independent of any one view so later RA/MCA/list views can share it.
type eramViewKind uint8

const (
	eramViewNone eramViewKind = iota
	eramViewTime
)

type eramViewAnchor uint8

const (
	eramViewAnchorTopLeft eramViewAnchor = iota
	eramViewAnchorTopRight
	eramViewAnchorBottomLeft
	eramViewAnchorBottomRight
)

type eramAnchoredLocation struct {
	Offset redsmath.Vec2
	Anchor eramViewAnchor
}

type eramViewMove struct {
	View     eramViewKind
	Position redsmath.Vec2
	Size     redsmath.Vec2
}

type eramViewUIState struct {
	Moving *eramViewMove
	Menu   eramViewMenuState
}

const (
	// CRC TimeViewSettings defaults.
	defaultClockBrightness = 80
	defaultClockFontSize   = 2
	clockBorderWidth       = 1
)

var (
	clockWhite    = renderer.RGB8(243, 243, 243) // EramColor.White
	clockDarkBlue = renderer.RGB8(0, 0, 212)     // EramColor.DarkBlue
)

type eramClockState struct {
	location   eramAnchoredLocation
	brightness int
	fontSize   int
	showBorder bool
	isOpaque   bool
}

func (p *ERAMPane) initializeClockState() {
	if p == nil {
		return
	}
	p.clock = eramClockState{
		// CRC TimeViewSettings.Location = TopLeft (20, 110).
		location: eramAnchoredLocation{
			Offset: redsmath.Vec2{X: 20, Y: 110},
			Anchor: eramViewAnchorTopLeft,
		},
		brightness: defaultClockBrightness,
		fontSize:   defaultClockFontSize,
		showBorder: true,
	}
}

func resolveERAMAnchoredLocation(location eramAnchoredLocation, size, paneSize redsmath.Vec2) redsmath.Vec2 {
	switch location.Anchor {
	case eramViewAnchorTopRight:
		return redsmath.Vec2{X: paneSize.X - location.Offset.X - size.X, Y: location.Offset.Y}
	case eramViewAnchorBottomLeft:
		return redsmath.Vec2{X: location.Offset.X, Y: paneSize.Y - location.Offset.Y - size.Y}
	case eramViewAnchorBottomRight:
		return redsmath.Vec2{X: paneSize.X - location.Offset.X - size.X, Y: paneSize.Y - location.Offset.Y - size.Y}
	default:
		return location.Offset
	}
}

// anchoredLocationForTopLeft mirrors LocatedViewBase.SetLocation in CRC. The
// nearest horizontal/vertical edge becomes the persistence anchor so a moved
// view stays against the same side when the display is resized.
func anchoredLocationForTopLeft(topLeft, size, paneSize redsmath.Vec2) eramAnchoredLocation {
	topLeft = clampERAMViewPosition(topLeft, size, paneSize)

	leftOffset := topLeft.X
	rightOffset := paneSize.X - topLeft.X - size.X
	topOffset := topLeft.Y
	bottomOffset := paneSize.Y - topLeft.Y - size.Y

	anchorRight := size.X < paneSize.X && rightOffset < leftOffset
	anchorBottom := size.Y < paneSize.Y && bottomOffset < topOffset

	if size.X >= paneSize.X {
		leftOffset = 0
		anchorRight = false
	}
	if size.Y >= paneSize.Y {
		topOffset = 0
		anchorBottom = false
	}

	switch {
	case anchorRight && anchorBottom:
		return eramAnchoredLocation{Offset: redsmath.Vec2{X: maxFloat32(0, rightOffset), Y: maxFloat32(0, bottomOffset)}, Anchor: eramViewAnchorBottomRight}
	case anchorRight:
		return eramAnchoredLocation{Offset: redsmath.Vec2{X: maxFloat32(0, rightOffset), Y: maxFloat32(0, topOffset)}, Anchor: eramViewAnchorTopRight}
	case anchorBottom:
		return eramAnchoredLocation{Offset: redsmath.Vec2{X: maxFloat32(0, leftOffset), Y: maxFloat32(0, bottomOffset)}, Anchor: eramViewAnchorBottomLeft}
	default:
		return eramAnchoredLocation{Offset: redsmath.Vec2{X: maxFloat32(0, leftOffset), Y: maxFloat32(0, topOffset)}, Anchor: eramViewAnchorTopLeft}
	}
}

func clampERAMViewPosition(position, size, paneSize redsmath.Vec2) redsmath.Vec2 {
	maxX := maxFloat32(0, paneSize.X-size.X)
	maxY := maxFloat32(0, paneSize.Y-size.Y)
	position.X = minFloat32(maxFloat32(position.X, 0), maxX)
	position.Y = minFloat32(maxFloat32(position.Y, 0), maxY)
	return position
}

func minFloat32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxFloat32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func (p *ERAMPane) clockSize() redsmath.Vec2 {
	if p == nil {
		return redsmath.Vec2{}
	}
	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return redsmath.Vec2{}
	}
	fontSize := p.clock.fontSize
	// Every UTC clock string has exactly seven monospaced characters, so use a
	// stable representative value instead of making geometry depend on time.
	textWidth, textHeight := font.MeasureText("0000 00", fontSize)
	charAdvance, lineHeight := font.CharSize(fontSize)
	if textWidth <= 0 || textHeight <= 0 || charAdvance <= 0 || lineHeight <= 0 {
		return redsmath.Vec2{}
	}
	padX := int(float64(charAdvance) * 0.5)
	padY := int(float64(lineHeight) * 0.5)
	return redsmath.Vec2{
		X: float32(2*clockBorderWidth + 2*padX + textWidth),
		Y: float32(2*clockBorderWidth + 2*padY + textHeight),
	}
}

func (p *ERAMPane) clockBounds(paneSize redsmath.Vec2) redsmath.Rect {
	if p == nil {
		return redsmath.Rect{}
	}
	size := p.clockSize()
	if size.X <= 0 || size.Y <= 0 {
		return redsmath.Rect{}
	}

	topLeft := resolveERAMAnchoredLocation(p.clock.location, size, paneSize)
	// CRC keeps a view settings menu fixed once opened. If changing a setting
	// changes the host width (TIME/FONT is the obvious case), the host edge
	// adjacent to the menu remains pinned and the opposite edge moves instead.
	// VICE models the same behavior with viewPopupPlacement/popupAnchorSide.
	if pinned, ok := p.pinnedViewTopLeft(eramViewMenuTime, topLeft, size, paneSize); ok {
		topLeft = pinned
		p.clock.location = anchoredLocationForTopLeft(topLeft, size, paneSize)
	}
	return redsmath.NewRect(topLeft.X, topLeft.Y, topLeft.X+size.X, topLeft.Y+size.Y)
}

func (p *ERAMPane) viewBounds(kind eramViewKind, paneSize redsmath.Vec2) redsmath.Rect {
	switch kind {
	case eramViewTime:
		return p.clockBounds(paneSize)
	default:
		return redsmath.Rect{}
	}
}

func (p *ERAMPane) setViewTopLeft(kind eramViewKind, topLeft, size, paneSize redsmath.Vec2) {
	switch kind {
	case eramViewTime:
		p.clock.location = anchoredLocationForTopLeft(topLeft, size, paneSize)
	}
}

func (p *ERAMPane) startViewMove(ctx *panes.Context, kind eramViewKind) {
	if p == nil || ctx == nil || kind == eramViewNone {
		return
	}
	bounds := p.viewBounds(kind, ctx.PaneSize())
	if bounds.Empty() {
		return
	}
	p.closeViewMenu()
	p.viewUI.Moving = &eramViewMove{
		View:     kind,
		Position: bounds.Min,
		Size:     bounds.Size(),
	}
	// CRC StartMove repositions the cursor to the view's top-left. REDS has no
	// mouse clipping primitive yet, but cursor warping preserves the placement
	// interaction and the position itself is clamped every frame.
	setPaneMousePosition(ctx, bounds.Min)
}

func (p *ERAMPane) consumeViewMoveInput(ctx *panes.Context) bool {
	if p == nil || ctx == nil || p.viewUI.Moving == nil {
		return false
	}
	move := p.viewUI.Moving
	if ctx.Keyboard != nil && ctx.Keyboard.WasPressed(platform.KeyEscape) {
		p.viewUI.Moving = nil
		return true
	}
	if ctx.Mouse == nil {
		return true
	}

	move.Position = clampERAMViewPosition(ctx.Mouse.Pos, move.Size, ctx.PaneSize())
	if ctx.Mouse.WasPressed(platform.MouseButtonLeft) || ctx.Mouse.WasPressed(platform.MouseButtonMiddle) {
		p.setViewTopLeft(move.View, move.Position, move.Size, ctx.PaneSize())
		p.viewUI.Moving = nil
	}
	// Captured move input owns every mouse button until placement or Escape,
	// matching MovableViewBase's CapturedInput behavior in CRC.
	return true
}

// consumeClockInput implements CRC ViewTime.HandleMouseDown: left-click starts
// MovableViewBase placement, middle-click requests the TIME settings menu, and
// every other click inside the clock is consumed instead of reaching the scope.
func (p *ERAMPane) consumeClockInput(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}
	bounds := p.clockBounds(ctx.PaneSize())
	if bounds.Empty() || !bounds.Contains(ctx.Mouse.Pos) {
		return false
	}
	if ctx.Mouse.WasPressed(platform.MouseButtonLeft) {
		p.startViewMove(ctx, eramViewTime)
		return true
	}
	if ctx.Mouse.WasPressed(platform.MouseButtonMiddle) {
		p.openViewMenu(ctx, eramViewMenuTime)
		return true
	}
	if ctx.Mouse.WasPressed(platform.MouseButtonRight) {
		return true
	}
	return false
}

// drawClock mirrors CRC's ERAM ViewTime presentation. CRC updates the text
// every render from DateTime.UtcNow using the exact format "HHmm ss".
func (p *ERAMPane) drawClock(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Renderer == nil {
		return
	}

	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return
	}

	fontSize := p.clock.fontSize
	texture := p.toolbarTexture(ctx.Renderer, fontSize)
	if texture == 0 {
		return
	}

	text := time.Now().UTC().Format("1504 05")
	textWidth, textHeight := font.MeasureText(text, fontSize)
	charAdvance, lineHeight := font.CharSize(fontSize)
	if textWidth <= 0 || textHeight <= 0 || charAdvance <= 0 || lineHeight <= 0 {
		return
	}

	padX := int(float64(charAdvance) * 0.5)
	padY := int(float64(lineHeight) * 0.5)
	bounds := p.clockBounds(ctx.PaneSize())
	if bounds.Empty() {
		return
	}

	x, y, fbWidth, fbHeight := ctx.PaneFramebufferRect()
	z := zTimeViewSemiTransparent
	if p.clock.isOpaque {
		z = zTimeViewOpaque
	}
	cb := zcb.At(z)
	cb.Viewport(x, y, fbWidth, fbHeight)
	cb.Scissor(x, y, fbWidth, fbHeight)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())

	// CRC's T (semi-transparent) clock lets lower-precedence scope content show
	// through the body. Only O gets the situation-display background. VICE's
	// generic View expresses the same rule as OpaqueOnlyBg for its clock.
	if p.clock.isOpaque {
		cb.DisableBlend()
		drawSolidRect(cb, bounds, applyERAMBrightness(
			clockDarkBlue,
			p.backgroundBrightness,
			p.systemBrightness,
		))
	} else {
		cb.Blend()
	}
	if p.clock.showBorder {
		drawBorderOnly(cb, bounds, applyERAMBrightness(
			clockWhite,
			p.borderBrightness,
			p.systemBrightness,
		), clockBorderWidth)
	}

	td := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(td)
	td.SetFont(font)
	background := renderer.RGBA{}
	if p.clock.isOpaque {
		background = applyERAMBrightness(clockDarkBlue, p.backgroundBrightness, p.systemBrightness).ToRGBA()
	}
	td.AddText(text, redsmath.Vec2{
		X: bounds.Min.X + clockBorderWidth + float32(padX),
		Y: bounds.Min.Y + clockBorderWidth + float32(padY),
	}, renderer.TextStyle{
		Size:       fontSize,
		Color:      applyERAMBrightness(clockWhite, p.clock.brightness, p.systemBrightness).ToRGBA(),
		Background: background,
	})
	// Transparent font backgrounds only work when blending is active. Keeping
	// blending enabled here also makes the glyph antialiasing match the scope.
	cb.Blend()
	td.GenerateCommands(cb, texture)

	cb.DisableScissor()
}

func (p *ERAMPane) drawViewMoveFrame(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || p.viewUI.Moving == nil {
		return
	}
	move := p.viewUI.Moving
	bounds := redsmath.NewRect(move.Position.X, move.Position.Y, move.Position.X+move.Size.X, move.Position.Y+move.Size.Y)
	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zViewMoveFrame)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.DisableBlend()
	drawBorderOnly(cb, bounds, applyERAMBrightness(toolbarWhite, p.pairedTargetBrightness, p.systemBrightness), 1)
	cb.Blend()
	cb.DisableScissor()
}

func setPaneMousePosition(ctx *panes.Context, panePosition redsmath.Vec2) {
	if ctx == nil || ctx.Platform == nil {
		return
	}
	ctx.Platform.SetMousePosition(redsmath.Vec2{
		X: ctx.PaneRect.Min.X + panePosition.X,
		Y: ctx.PaneRect.Min.Y + panePosition.Y,
	})
}
