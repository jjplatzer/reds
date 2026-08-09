package eram

import (
	"strings"
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
	eramViewChecklist
	eramViewMCA
	eramViewResponseArea
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

const (
	// CRC McaViewSettings / ResponseAreaViewSettings defaults. Preview and
	// Feedback are one MCA view; Response Area is a separate movable view.
	defaultMCAWidth              = 30
	defaultResponseAreaWidth     = 25
	defaultAreaFontSize          = 2
	defaultAreaBrightness        = 80
	eramAreaBorderWidth          = 1
	eramAreaPaddingX             = 7
	eramAreaPaddingY             = 3
	eramAreaLineSpacing          = 6
	eramAreaScrollReserve        = 19
	eramMCAPreviewMinLines       = 2
	eramMCAPreviewMaxLines       = 6
	eramMCAFeedbackMinLines      = 4
	eramResponseAreaMinimumLines = 4
)

var eramAreaBlack = renderer.RGB8(0, 0, 0) // EramColor.Black

type eramMCAState struct {
	location   eramAnchoredLocation
	input      []rune
	cursor     int
	width      int
	fontSize   int
	brightness int
}

type eramResponseAreaState struct {
	location   eramAnchoredLocation
	width      int
	fontSize   int
	brightness int
}

func (p *ERAMPane) initializeAreaStates() {
	if p == nil {
		return
	}
	p.mca = eramMCAState{
		// CRC McaViewSettings.Location = BottomLeft (0, 1).
		location: eramAnchoredLocation{
			Offset: redsmath.Vec2{X: 0, Y: 1},
			Anchor: eramViewAnchorBottomLeft,
		},
		width:      defaultMCAWidth,
		fontSize:   defaultAreaFontSize,
		brightness: defaultAreaBrightness,
	}
	p.responseArea = eramResponseAreaState{
		// CRC ResponseAreaViewSettings.Location = BottomLeft (395, 1). The
		// default MCA is 395 px wide at ERAM font size 2, so the two views
		// touch exactly at startup.
		location: eramAnchoredLocation{
			Offset: redsmath.Vec2{X: 395, Y: 1},
			Anchor: eramViewAnchorBottomLeft,
		},
		width:      defaultResponseAreaWidth,
		fontSize:   defaultAreaFontSize,
		brightness: defaultAreaBrightness,
	}
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
	case eramViewChecklist:
		return p.checklistBounds(paneSize)
	case eramViewMCA:
		return p.mcaBounds(paneSize)
	case eramViewResponseArea:
		return p.responseAreaBounds(paneSize)
	default:
		return redsmath.Rect{}
	}
}

func (p *ERAMPane) setViewTopLeft(kind eramViewKind, topLeft, size, paneSize redsmath.Vec2) {
	switch kind {
	case eramViewTime:
		p.clock.location = anchoredLocationForTopLeft(topLeft, size, paneSize)
	case eramViewChecklist:
		p.checklist.prefs.location = anchoredLocationForTopLeft(topLeft, size, paneSize)
	case eramViewMCA:
		p.mca.location = anchoredLocationForTopLeft(topLeft, size, paneSize)
	case eramViewResponseArea:
		p.responseArea.location = anchoredLocationForTopLeft(topLeft, size, paneSize)
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

func eramAreaTextHeight(lines, lineHeight int) int {
	if lines <= 0 || lineHeight <= 0 {
		return 0
	}
	return lines*lineHeight + maxInt(0, lines-1)*eramAreaLineSpacing
}

func (p *ERAMPane) eramAreaSize(widthChars, textLines, fontSize int) redsmath.Vec2 {
	if p == nil || widthChars <= 0 || textLines <= 0 {
		return redsmath.Vec2{}
	}
	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return redsmath.Vec2{}
	}
	charAdvance, lineHeight := font.CharSize(fontSize)
	if charAdvance <= 0 || lineHeight <= 0 {
		return redsmath.Vec2{}
	}
	return redsmath.Vec2{
		X: float32(2*eramAreaBorderWidth + 2*eramAreaPaddingX + widthChars*charAdvance + eramAreaScrollReserve),
		Y: float32(2*eramAreaBorderWidth + 2*eramAreaPaddingY + eramAreaTextHeight(textLines, lineHeight)),
	}
}

func (p *ERAMPane) mcaPreviewTotalLines() int {
	if p == nil || p.mca.width <= 0 {
		return 1
	}
	// CRC forces a fresh line when the input ends exactly on the configured
	// width so the overstrike cursor always has a visible cell.
	return len(p.mca.input)/p.mca.width + 1
}

func (p *ERAMPane) mcaSectionLines() (previewLines, feedbackLines int, separatorVisible bool) {
	total := p.mcaPreviewTotalLines()
	previewLines = maxInt(eramMCAPreviewMinLines, minInt(total, eramMCAPreviewMaxLines))
	if total >= eramMCAPreviewMaxLines {
		return previewLines, 0, false
	}
	feedbackLines = maxInt(eramMCAFeedbackMinLines-(previewLines-eramMCAPreviewMinLines), 0)
	return previewLines, feedbackLines, true
}

func (p *ERAMPane) mcaSize() redsmath.Vec2 {
	if p == nil {
		return redsmath.Vec2{}
	}
	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return redsmath.Vec2{}
	}
	charAdvance, lineHeight := font.CharSize(p.mca.fontSize)
	if charAdvance <= 0 || lineHeight <= 0 {
		return redsmath.Vec2{}
	}
	previewLines, feedbackLines, separatorVisible := p.mcaSectionLines()
	previewHeight := 2*eramAreaPaddingY + eramAreaTextHeight(previewLines, lineHeight)
	feedbackHeight := 0
	if feedbackLines > 0 {
		feedbackHeight = 2*eramAreaPaddingY + eramAreaTextHeight(feedbackLines, lineHeight)
	}
	separatorHeight := 0
	if separatorVisible {
		separatorHeight = 1
	}
	return redsmath.Vec2{
		X: float32(2*eramAreaBorderWidth + 2*eramAreaPaddingX + p.mca.width*charAdvance + eramAreaScrollReserve),
		Y: float32(2*eramAreaBorderWidth + previewHeight + separatorHeight + feedbackHeight),
	}
}

func (p *ERAMPane) mcaBounds(paneSize redsmath.Vec2) redsmath.Rect {
	if p == nil {
		return redsmath.Rect{}
	}
	size := p.mcaSize()
	if size.X <= 0 || size.Y <= 0 {
		return redsmath.Rect{}
	}
	topLeft := resolveERAMAnchoredLocation(p.mca.location, size, paneSize)
	return redsmath.NewRect(topLeft.X, topLeft.Y, topLeft.X+size.X, topLeft.Y+size.Y)
}

func (p *ERAMPane) responseAreaSize() redsmath.Vec2 {
	if p == nil {
		return redsmath.Vec2{}
	}
	return p.eramAreaSize(p.responseArea.width, eramResponseAreaMinimumLines, p.responseArea.fontSize)
}

func (p *ERAMPane) responseAreaBounds(paneSize redsmath.Vec2) redsmath.Rect {
	if p == nil {
		return redsmath.Rect{}
	}
	size := p.responseAreaSize()
	if size.X <= 0 || size.Y <= 0 {
		return redsmath.Rect{}
	}
	topLeft := resolveERAMAnchoredLocation(p.responseArea.location, size, paneSize)
	return redsmath.NewRect(topLeft.X, topLeft.Y, topLeft.X+size.X, topLeft.Y+size.Y)
}

func (p *ERAMPane) consumeMCAInput(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}
	bounds := p.mcaBounds(ctx.PaneSize())
	if bounds.Empty() || !bounds.Contains(ctx.Mouse.Pos) {
		return false
	}
	if ctx.Mouse.WasPressed(platform.MouseButtonLeft) {
		p.startViewMove(ctx, eramViewMCA)
		return true
	}
	// CRC middle-click requests the MCA settings menu. That menu is not part of
	// this first area pass yet, but consume the pick so it cannot leak through
	// the view while preserving the correct future interaction point.
	if ctx.Mouse.WasPressed(platform.MouseButtonMiddle) {
		return true
	}
	return false
}

func (p *ERAMPane) consumeResponseAreaInput(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}
	bounds := p.responseAreaBounds(ctx.PaneSize())
	if bounds.Empty() || !bounds.Contains(ctx.Mouse.Pos) {
		return false
	}
	if ctx.Mouse.WasPressed(platform.MouseButtonLeft) {
		p.startViewMove(ctx, eramViewResponseArea)
		return true
	}
	if ctx.Mouse.WasPressed(platform.MouseButtonMiddle) {
		return true
	}
	return false
}

func (p *ERAMPane) consumeMCAKeyboard(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Keyboard == nil {
		return
	}
	keyboard := ctx.Keyboard

	if keyboard.WasPressed(platform.KeyEscape) {
		p.mca.input = p.mca.input[:0]
		p.mca.cursor = 0
		return
	}
	if keyboard.WasPressed(platform.KeyBackspace) {
		if p.mca.cursor > 0 {
			copy(p.mca.input[p.mca.cursor-1:], p.mca.input[p.mca.cursor:])
			p.mca.input = p.mca.input[:len(p.mca.input)-1]
			p.mca.cursor--
		}
	}
	if keyboard.WasPressed(platform.KeyDelete) && p.mca.cursor < len(p.mca.input) {
		copy(p.mca.input[p.mca.cursor:], p.mca.input[p.mca.cursor+1:])
		p.mca.input = p.mca.input[:len(p.mca.input)-1]
	}
	if keyboard.WasPressed(platform.KeyLeft) && p.mca.cursor > 0 {
		p.mca.cursor--
	}
	if keyboard.WasPressed(platform.KeyRight) && p.mca.cursor < len(p.mca.input) {
		p.mca.cursor++
	}
	if keyboard.WasPressed(platform.KeyUp) && p.mca.cursor > 0 {
		p.mca.cursor = maxInt(0, p.mca.cursor-p.mca.width)
	}
	if keyboard.WasPressed(platform.KeyDown) && p.mca.cursor < len(p.mca.input) {
		p.mca.cursor = minInt(len(p.mca.input), p.mca.cursor+p.mca.width)
	}

	// CRC uppercases ordinary printable keyboard input before putting it into
	// the Preview Area. Control/Command chords should not leak their letters.
	if keyboard.IsDown(platform.KeyControl) || keyboard.IsDown(platform.KeyCommand) {
		return
	}
	for _, r := range keyboard.Text {
		if r < ' ' || r > '~' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		p.insertMCACharacter(r)
	}
}

func (p *ERAMPane) insertMCACharacter(r rune) {
	if p == nil || p.mca.width <= 0 {
		return
	}
	maxCharacters := 1500
	if p.mca.width == 30 {
		maxCharacters = 1020
	}
	if p.mca.cursor >= maxCharacters {
		return
	}
	// CRC starts in overstrike mode: replace an existing character at the
	// cursor, otherwise append. Insert-mode support can be layered on later.
	if p.mca.cursor < len(p.mca.input) {
		p.mca.input[p.mca.cursor] = r
	} else if len(p.mca.input) < maxCharacters {
		p.mca.input = append(p.mca.input, r)
	}
	if p.mca.cursor+1 < maxCharacters {
		p.mca.cursor++
	}
}

func (p *ERAMPane) drawMCA(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Renderer == nil {
		return
	}
	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return
	}
	texture := p.toolbarTexture(ctx.Renderer, p.mca.fontSize)
	if texture == 0 {
		return
	}
	charAdvance, lineHeight := font.CharSize(p.mca.fontSize)
	if charAdvance <= 0 || lineHeight <= 0 {
		return
	}
	bounds := p.mcaBounds(ctx.PaneSize())
	if bounds.Empty() {
		return
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zMCAView)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.DisableBlend()
	drawSolidRect(cb, bounds, eramAreaBlack)
	borderColor := applyERAMBrightness(clockWhite, p.borderBrightness, p.systemBrightness)
	drawBorderOnly(cb, bounds, borderColor, eramAreaBorderWidth)

	previewLines, _, separatorVisible := p.mcaSectionLines()
	previewHeight := 2*eramAreaPaddingY + eramAreaTextHeight(previewLines, lineHeight)
	if separatorVisible {
		separatorY := bounds.Min.Y + eramAreaBorderWidth + float32(previewHeight)
		drawSolidRect(cb, redsmath.NewRect(
			bounds.Min.X+eramAreaBorderWidth,
			separatorY,
			bounds.Max.X-eramAreaBorderWidth,
			separatorY+1,
		), borderColor)
	}

	textColor := applyERAMBrightness(clockWhite, p.mca.brightness, p.systemBrightness).ToRGBA()
	cursorColor := applyERAMBrightness(clockWhite, p.pairedTargetBrightness, p.systemBrightness).ToRGBA()
	black := eramAreaBlack.ToRGBA()
	contentOrigin := redsmath.Vec2{
		X: bounds.Min.X + eramAreaBorderWidth + eramAreaPaddingX,
		Y: bounds.Min.Y + eramAreaBorderWidth + eramAreaPaddingY,
	}

	cursorLine := 0
	cursorColumn := 0
	if p.mca.width > 0 {
		cursorLine = p.mca.cursor / p.mca.width
		cursorColumn = p.mca.cursor % p.mca.width
	}
	topLine := 0
	if cursorLine >= previewLines {
		topLine = cursorLine - previewLines + 1
	}
	maxTop := maxInt(0, p.mcaPreviewTotalLines()-previewLines)
	topLine = minInt(topLine, maxTop)

	td := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(td)
	td.SetFont(font)
	for row := 0; row < previewLines; row++ {
		lineIndex := topLine + row
		start := lineIndex * p.mca.width
		if start >= len(p.mca.input) {
			continue
		}
		end := minInt(start+p.mca.width, len(p.mca.input))
		line := string(p.mca.input[start:end])
		if line == "" {
			continue
		}
		td.AddText(line, redsmath.Vec2{
			X: contentOrigin.X,
			Y: contentOrigin.Y + float32(row*(lineHeight+eramAreaLineSpacing)),
		}, renderer.TextStyle{Size: p.mca.fontSize, Color: textColor, Background: black})
	}
	if cursorLine >= topLine && cursorLine < topLine+previewLines {
		row := cursorLine - topLine
		td.AddText("_", redsmath.Vec2{
			X: contentOrigin.X + float32(cursorColumn*charAdvance),
			Y: contentOrigin.Y + float32(row*(lineHeight+eramAreaLineSpacing)),
		}, renderer.TextStyle{Size: p.mca.fontSize, Color: cursorColor})
	}
	cb.Blend()
	td.GenerateCommands(cb, texture)
	cb.DisableScissor()
}

func (p *ERAMPane) drawResponseArea(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil {
		return
	}
	bounds := p.responseAreaBounds(ctx.PaneSize())
	if bounds.Empty() {
		return
	}
	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zResponseAreaView)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.DisableBlend()
	drawSolidRect(cb, bounds, eramAreaBlack)
	drawBorderOnly(cb, bounds, applyERAMBrightness(clockWhite, p.borderBrightness, p.systemBrightness), eramAreaBorderWidth)
	cb.Blend()
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

// CRC ViewChecklist layout constants. Preference defaults live in prefs.go;
// the geometry and interaction live with the other ERAM views here.
const (
	checklistBorderWidth  = 1
	checklistEntryGap     = 1
	checklistTextYPadding = 3
)

var checklistGray = renderer.RGB8(199, 199, 199) // EramColor.Gray

func (p *ERAMPane) checklistEntries() []string {
	if p == nil {
		return nil
	}
	switch p.checklist.active {
	case eramChecklistPositionRelief:
		return p.checklist.positionRelief
	case eramChecklistEmergency:
		return p.checklist.emergency
	default:
		return nil
	}
}

func (p *ERAMPane) checklistTitle() string {
	switch p.checklist.active {
	case eramChecklistPositionRelief:
		return "POS CHECK"
	case eramChecklistEmergency:
		return "EMERG CHECK"
	default:
		return ""
	}
}

type eramChecklistLayout struct {
	Bounds        redsmath.Rect
	Header        redsmath.Rect
	Menu          redsmath.Rect
	Title         redsmath.Rect
	Suppress      redsmath.Rect
	Body          redsmath.Rect
	EntryText     []redsmath.Rect
	Entries       []string
	LongestChars  int
	EntryHeight   float32
	WrapperPadX   float32
	WrapperPadY   float32
	HeaderHeight  float32
	ContentOrigin redsmath.Vec2
}

func (p *ERAMPane) checklistLayout(paneSize redsmath.Vec2) eramChecklistLayout {
	var layout eramChecklistLayout
	if p == nil || p.checklist.active == eramChecklistNone {
		return layout
	}
	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return layout
	}
	charAdvance, lineHeight := font.CharSize(p.checklist.prefs.fontSize)
	if charAdvance <= 0 || lineHeight <= 0 {
		return layout
	}

	entries := p.checklistEntries()
	if len(entries) == 0 {
		return layout
	}
	visibleCount := minInt(len(entries), p.checklist.prefs.lines)
	entries = entries[:visibleCount]
	longest := 0
	for _, entry := range entries {
		if n := len([]rune(entry)); n > longest {
			longest = n
		}
	}
	if longest == 0 {
		longest = 1
	}

	// ViewChecklist puts mEntriesWrapper and ScrollPickAreas next to each other
	// in one Row. ScrollPickAreas is Visibility.Hidden (not Collapsed) when the
	// checklist does not need scrolling, so CRC still reserves its full width
	// beneath the header's '-' cell. At ERAM font size 2 that width is 19 px:
	// 1 px margin on each side + a 17 px scroll pick-area cell.
	//
	// mEntriesWrapper itself has 0.5-character padding. Measure the padded
	// entry text using the actual bitmap glyph width instead of N*advance; this
	// is important because the final glyph is 10 px wide while its advance is
	// 12 px. With the 19 px scroll reserve, CRC's +2 px text circumscription
	// ends exactly 24 px before the view's right edge, aligned with the '-'
	// header cell.
	wrapperPadX := float32(int(float64(charAdvance) * 0.5))
	wrapperPadY := float32(int(float64(lineHeight) * 0.5))
	entryHeight := float32(lineHeight + 2*checklistTextYPadding)
	paddedTextWidth, _ := font.MeasureText(strings.Repeat(" ", longest), p.checklist.prefs.fontSize)
	scrollReserveWidth := checklistScrollReserveWidth(font)
	bodyWidth := float32(2*checklistBorderWidth) + 2*wrapperPadX + float32(paddedTextWidth) + scrollReserveWidth
	bodyHeight := float32(2*checklistBorderWidth) + 2*wrapperPadY +
		float32(visibleCount)*entryHeight + float32(maxInt(0, visibleCount-1)*checklistEntryGap)

	// Header, MenuPickArea and SuppressPickArea use font size 2 regardless of
	// the list font. Header Text is 11 px high at size 2, has 3 px Y padding,
	// and a 1 px border. HeaderRow overlaps the body by one pixel.
	headerCharAdvance, headerLineHeight := font.CharSize(2)
	if headerCharAdvance <= 0 || headerLineHeight <= 0 {
		return layout
	}
	headerHeight := float32(headerLineHeight + 2*checklistTextYPadding + 2*checklistBorderWidth)
	headerRowHeight := headerHeight - 1
	// MinWidth = 2 chars - 1 px, then 1 px border on both sides and a -1 px
	// inner margin. The resulting M and suppression cells are each 24 px with
	// the extracted CRC font (12 px advance), matching CRC's Node calculation.
	headerSideWidth := float32(2 * headerCharAdvance)

	viewWidth := bodyWidth
	minimumHeaderWidth := 2*headerSideWidth + float32(len(p.checklistTitle())*headerCharAdvance+2*checklistTextYPadding+2*checklistBorderWidth)
	if viewWidth < minimumHeaderWidth {
		viewWidth = minimumHeaderWidth
	}
	viewHeight := headerRowHeight + bodyHeight
	size := redsmath.Vec2{X: viewWidth, Y: viewHeight}
	topLeft := resolveERAMAnchoredLocation(p.checklist.prefs.location, size, paneSize)
	topLeft = clampERAMViewPosition(topLeft, size, paneSize)

	layout.Bounds = redsmath.NewRect(topLeft.X, topLeft.Y, topLeft.X+viewWidth, topLeft.Y+viewHeight)
	layout.Header = redsmath.NewRect(topLeft.X, topLeft.Y, topLeft.X+viewWidth, topLeft.Y+headerHeight)
	layout.Menu = redsmath.NewRect(topLeft.X, topLeft.Y, topLeft.X+headerSideWidth, topLeft.Y+headerHeight)
	layout.Suppress = redsmath.NewRect(topLeft.X+viewWidth-headerSideWidth, topLeft.Y, topLeft.X+viewWidth, topLeft.Y+headerHeight)
	layout.Title = redsmath.NewRect(layout.Menu.Max.X-1, topLeft.Y, layout.Suppress.Min.X+1, topLeft.Y+headerHeight)
	layout.Body = redsmath.NewRect(topLeft.X, topLeft.Y+headerRowHeight, topLeft.X+viewWidth, topLeft.Y+viewHeight)
	layout.Entries = entries
	layout.LongestChars = longest
	layout.EntryHeight = entryHeight
	layout.WrapperPadX = wrapperPadX
	layout.WrapperPadY = wrapperPadY
	layout.HeaderHeight = headerHeight
	layout.ContentOrigin = redsmath.Vec2{
		X: layout.Body.Min.X + checklistBorderWidth + wrapperPadX,
		Y: layout.Body.Min.Y + checklistBorderWidth + wrapperPadY,
	}
	layout.EntryText = make([]redsmath.Rect, len(entries))
	textWidth := float32(paddedTextWidth)
	for i := range entries {
		y := layout.ContentOrigin.Y + float32(i)*(entryHeight+checklistEntryGap)
		// Text.HandleMouseMove uses the text circumscription box: two pixels
		// around the measured text, not the whole body row.
		layout.EntryText[i] = redsmath.NewRect(
			layout.ContentOrigin.X-2,
			y+checklistTextYPadding-2,
			layout.ContentOrigin.X+textWidth+2,
			y+checklistTextYPadding+float32(lineHeight)+2,
		)
	}
	return layout
}

// checklistScrollReserveWidth reproduces CRC ScrollPickAreas' natural width
// while hidden. ScrollPickAreas has a 1 px margin on both sides; each arrow
// pick area has a 1 px border, 2 px left padding, 3 px right padding, and a
// size-2 ERAM triangle glyph. Hidden nodes still participate in CRC layout.
func checklistScrollReserveWidth(font *renderer.BitmapFont) float32 {
	if font == nil {
		return 0
	}
	triangleWidth, _ := font.MeasureText(string(rune(138)), 2) // EramChar.UpTriangle
	if triangleWidth <= 0 {
		return 0
	}
	return float32(2 + 2 + 2 + 3 + triangleWidth)
}

func (p *ERAMPane) checklistBounds(paneSize redsmath.Vec2) redsmath.Rect {
	return p.checklistLayout(paneSize).Bounds
}

func (p *ERAMPane) consumeChecklistInput(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil || p.checklist.active == eramChecklistNone {
		return false
	}
	layout := p.checklistLayout(ctx.PaneSize())
	if layout.Bounds.Empty() || !layout.Bounds.Contains(ctx.Mouse.Pos) {
		return false
	}
	left := ctx.Mouse.WasPressed(platform.MouseButtonLeft)
	middle := ctx.Mouse.WasPressed(platform.MouseButtonMiddle)
	if !left && !middle {
		return false
	}

	// ViewListBase's M menu button requests ChecklistView settings. REDS does
	// not yet expose the full list-settings popup; consume the exact pick area
	// now so it does not leak through to the scope.
	if layout.Menu.Contains(ctx.Mouse.Pos) {
		return true
	}
	// The centered header is the MovableViewBase move handle. CRC permits TBP
	// or TBE here and then captures placement until the next TBP/TBE or Escape.
	if layout.Title.Contains(ctx.Mouse.Pos) {
		p.startViewMove(ctx, eramViewChecklist)
		return true
	}
	if layout.Suppress.Contains(ctx.Mouse.Pos) {
		p.checklist.active = eramChecklistNone
		p.checklist.selected = make(map[int]bool)
		return true
	}
	for i, bounds := range layout.EntryText {
		if bounds.Contains(ctx.Mouse.Pos) {
			p.checklist.selected[i] = !p.checklist.selected[i]
			return true
		}
	}
	// CRC consumes picks in the body even when they do not hit an entry.
	return true
}

func (p *ERAMPane) drawChecklist(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Renderer == nil || p.checklist.active == eramChecklistNone {
		return
	}
	layout := p.checklistLayout(ctx.PaneSize())
	if layout.Bounds.Empty() {
		return
	}
	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return
	}
	texture := p.toolbarTexture(ctx.Renderer, p.checklist.prefs.fontSize)
	headerTexture := p.toolbarTexture(ctx.Renderer, 2)
	if texture == 0 || headerTexture == 0 {
		return
	}
	_, lineHeight := font.CharSize(p.checklist.prefs.fontSize)
	if lineHeight <= 0 {
		return
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	z := zChecklistViewSemiTransparent
	if p.checklist.prefs.isOpaque {
		z = zChecklistViewOpaque
	}
	cb := zcb.At(z)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.DisableBlend()

	border := applyERAMBrightness(toolbarWhite, p.borderBrightness, p.systemBrightness)
	text := applyERAMBrightness(toolbarWhite, p.checklist.prefs.brightness, p.systemBrightness)
	black := toolbarBlack
	headerBackground := black
	if p.checklist.prefs.isOpaque {
		headerBackground = applyERAMBrightness(toolbarGray, p.buttonBrightness, p.systemBrightness)
	}

	// ViewListBase always gives its contents a black body. ShowBorder only
	// controls the body's 1 px border; it does not remove the black list body.
	// Draw it before the header: HeaderRow has ZIndex 0.0001 in CRC specifically
	// so its one-pixel overlap remains above the list body.
	drawSolidRect(cb, layout.Body, black)
	if p.checklist.prefs.showBorder {
		drawBorderOnly(cb, layout.Body, border, checklistBorderWidth)
	}

	// HeaderRow: M | centered checklist title | suppression '-'. Adjacent header
	// cells overlap by one pixel in CRC; drawing the shared borders in this
	// order produces the same one-pixel dividers.
	drawSolidRect(cb, layout.Menu, headerBackground)
	drawBorderOnly(cb, layout.Menu, border, checklistBorderWidth)
	drawSolidRect(cb, layout.Title, headerBackground)
	drawBorderOnly(cb, layout.Title, border, checklistBorderWidth)
	drawSolidRect(cb, layout.Suppress, headerBackground)
	drawBorderOnly(cb, layout.Suppress, border, checklistBorderWidth)

	// Selection emphasis is White on Gray with derived text brightness 76 and
	// derived background brightness 40. CRC pads each entry to the longest
	// adapted checklist string, making every selection rectangle the same width.
	for i := range layout.Entries {
		if !p.checklist.selected[i] {
			continue
		}
		entry := layout.EntryText[i]
		fill := applyERAMBrightness(checklistGray, p.checklist.prefs.highlightBrightness, p.systemBrightness)
		// Selection circumscription extends 2 px around measured text.
		drawSolidRect(cb, entry, fill)
	}

	// Text uses the actual extracted ERAM bitmap font. Header controls are
	// always size 2; list entries use ChecklistViewSettings.FontSize (default 2).
	headerTD := renderer.GetTextDrawBuilder()
	headerTD.SetFont(font)
	addChecklistCenteredText(headerTD, font, "M", layout.Menu, 2, text.ToRGBA(), headerBackground.ToRGBA())
	addChecklistCenteredText(headerTD, font, p.checklistTitle(), layout.Title, 2, text.ToRGBA(), headerBackground.ToRGBA())
	addChecklistCenteredText(headerTD, font, "-", layout.Suppress, 2, text.ToRGBA(), headerBackground.ToRGBA())
	cb.Blend()
	headerTD.GenerateCommands(cb, headerTexture)
	renderer.ReturnTextDrawBuilder(headerTD)

	td := renderer.GetTextDrawBuilder()
	td.SetFont(font)
	for i, raw := range layout.Entries {
		entry := raw + strings.Repeat(" ", maxInt(0, layout.LongestChars-len([]rune(raw))))
		background := black.ToRGBA()
		if p.checklist.selected[i] {
			background = applyERAMBrightness(checklistGray, p.checklist.prefs.highlightBrightness, p.systemBrightness).ToRGBA()
		}
		td.AddText(entry, redsmath.Vec2{
			X: layout.ContentOrigin.X,
			Y: layout.ContentOrigin.Y + float32(i)*(layout.EntryHeight+checklistEntryGap) + checklistTextYPadding,
		}, renderer.TextStyle{
			Size:       p.checklist.prefs.fontSize,
			Color:      text.ToRGBA(),
			Background: background,
		})
	}
	td.GenerateCommands(cb, texture)
	renderer.ReturnTextDrawBuilder(td)

	// TextDrawBuilder emits opaque glyph-cell backgrounds. If the hover
	// circumscription is drawn before text, those cells can overwrite portions
	// of its bottom edge and make it look dotted. CRC renders the Text node's
	// circumscription above its text, so draw all hover chrome after both header
	// and entry text have been submitted.
	if ctx.Mouse != nil {
		hover := ctx.Mouse.Pos
		emphasis := applyERAMBrightness(toolbarWhite, p.pairedTargetBrightness, p.systemBrightness)
		switch {
		case layout.Menu.Contains(hover):
			drawBorderOnly(cb, layout.Menu, emphasis, 1)
		case layout.Title.Contains(hover):
			drawBorderOnly(cb, layout.Title, emphasis, 1)
		case layout.Suppress.Contains(hover):
			drawBorderOnly(cb, layout.Suppress, emphasis, 1)
		default:
			for _, entry := range layout.EntryText {
				if entry.Contains(hover) {
					drawBorderOnly(cb, entry, emphasis, 1)
					break
				}
			}
		}
	}

	cb.DisableScissor()
}

func addChecklistCenteredText(td *renderer.TextDrawBuilder, font *renderer.BitmapFont, text string, bounds redsmath.Rect, size int, color, background renderer.RGBA) {
	if td == nil || font == nil || text == "" {
		return
	}
	w, h := font.MeasureText(text, size)
	td.AddText(text, redsmath.Vec2{
		X: bounds.Min.X + (bounds.Width()-float32(w))/2,
		Y: bounds.Min.Y + (bounds.Height()-float32(h))/2,
	}, renderer.TextStyle{Size: size, Color: color, Background: background})
}
