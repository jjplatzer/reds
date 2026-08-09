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
		p.checklist.location = anchoredLocationForTopLeft(topLeft, size, paneSize)
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
