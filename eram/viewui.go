package eram

import (
	"strconv"
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"
)

// CRC implements a number of ERAM display objects as MovableViewBase views:
// left-click enters captured placement mode and middle-click requests a small
// settings menu associated with the view. Keep that interaction machinery
// independent of the clock so later RA/MCA/list views can share it.
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

type eramViewMenuKind uint8

const (
	eramViewMenuNone eramViewMenuKind = iota
	eramViewMenuTime
)

type eramViewMenuRowKind uint8

const (
	eramViewMenuToggle eramViewMenuRowKind = iota
	eramViewMenuIncDec
)

type eramViewMenuAction uint8

const (
	eramViewMenuActionNone eramViewMenuAction = iota
	eramViewMenuTimeOpaque
	eramViewMenuTimeBorder
	eramViewMenuTimeFont
	eramViewMenuTimeBrightness
)

type eramViewMenuRow struct {
	Action        eramViewMenuAction
	Kind          eramViewMenuRowKind
	Label         string
	ActiveLabel   string
	InactiveLabel string
	Active        bool
	Value         int
	Centered      bool
	AutoRepeat    bool
}

const maxERAMViewMenuRows = 16

type eramViewMenuSpec struct {
	Title    string
	Rows     [maxERAMViewMenuRows]eramViewMenuRow
	RowCount int
}

type eramViewMenuRepeat struct {
	Action      eramViewMenuAction
	Increment   bool
	MouseButton platform.MouseButton
	Next        time.Time
}

type eramViewMenuState struct {
	Kind   eramViewMenuKind
	Repeat *eramViewMenuRepeat
}

type eramViewUIState struct {
	Moving *eramViewMove
	Menu   eramViewMenuState
}

const (
	eramViewMenuFontSize       = 2
	eramViewMenuWidthChars     = 11
	eramViewMenuBorderWidth    = 1
	eramViewMenuCloseWidthChar = 2
)

type eramViewMenuRowLayout struct {
	Row    eramViewMenuRow
	Bounds redsmath.Rect
}

type eramViewMenuLayout struct {
	Bounds redsmath.Rect
	Title  redsmath.Rect
	Close  redsmath.Rect
	Rows   [maxERAMViewMenuRows]eramViewMenuRowLayout
	Count  int
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
	p.viewUI.Menu = eramViewMenuState{}
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

func (p *ERAMPane) openViewMenu(ctx *panes.Context, kind eramViewMenuKind) {
	if p == nil || ctx == nil || kind == eramViewMenuNone {
		return
	}
	p.viewUI.Menu = eramViewMenuState{Kind: kind}
	if layout, ok := p.buildViewMenuLayout(ctx, kind); ok {
		setPaneMousePosition(ctx, redsmath.Vec2{
			X: (layout.Close.Min.X + layout.Close.Max.X) * 0.5,
			Y: (layout.Close.Min.Y + layout.Close.Max.Y) * 0.5,
		})
	}
}

func (p *ERAMPane) closeViewMenu() {
	if p == nil {
		return
	}
	p.viewUI.Menu = eramViewMenuState{}
}

func (p *ERAMPane) activeViewMenuSpec(kind eramViewMenuKind) (eramViewMenuSpec, bool) {
	var spec eramViewMenuSpec
	switch kind {
	case eramViewMenuTime:
		spec.Title = "TIME"
		spec.Rows[0] = eramViewMenuRow{
			Action:        eramViewMenuTimeOpaque,
			Kind:          eramViewMenuToggle,
			ActiveLabel:   "O",
			InactiveLabel: "T",
			Active:        p.clock.isOpaque,
			Centered:      true,
		}
		spec.Rows[1] = eramViewMenuRow{
			Action: eramViewMenuTimeBorder,
			Kind:   eramViewMenuToggle,
			Label:  "BORDER",
			Active: p.clock.showBorder,
		}
		spec.Rows[2] = eramViewMenuRow{
			Action: eramViewMenuTimeFont,
			Kind:   eramViewMenuIncDec,
			Label:  "FONT",
			Value:  p.clock.fontSize,
		}
		spec.Rows[3] = eramViewMenuRow{
			Action:     eramViewMenuTimeBrightness,
			Kind:       eramViewMenuIncDec,
			Label:      "BRIGHT",
			Value:      p.clock.brightness,
			AutoRepeat: true,
		}
		spec.RowCount = 4
		return spec, true
	default:
		return spec, false
	}
}

func (p *ERAMPane) viewMenuTargetBounds(kind eramViewMenuKind, paneSize redsmath.Vec2) redsmath.Rect {
	switch kind {
	case eramViewMenuTime:
		return p.clockBounds(paneSize)
	default:
		return redsmath.Rect{}
	}
}

func (p *ERAMPane) buildViewMenuLayout(ctx *panes.Context, kind eramViewMenuKind) (eramViewMenuLayout, bool) {
	var layout eramViewMenuLayout
	if p == nil || ctx == nil {
		return layout, false
	}
	spec, ok := p.activeViewMenuSpec(kind)
	if !ok {
		return layout, false
	}

	p.ensureToolbarFont()
	if p.toolbar.font == nil {
		return layout, false
	}
	charAdvance, lineHeight := p.toolbar.font.CharSize(eramViewMenuFontSize)
	if charAdvance <= 0 || lineHeight <= 0 {
		return layout, false
	}

	menuWidth := float32(eramViewMenuWidthChars*charAdvance + 2*eramViewMenuBorderWidth)
	headerHeight := float32(lineHeight + 2*eramViewMenuBorderWidth)
	centeredToggleHeight := headerHeight
	paddedRowHeight := float32(lineHeight + int(float64(lineHeight)*0.5) + 2*eramViewMenuBorderWidth)

	// CRC nodes overlap successive 1 px borders (Margin.Bottom = -1).
	menuHeight := headerHeight
	for i := 0; i < spec.RowCount; i++ {
		menuHeight -= 1
		if spec.Rows[i].Centered {
			menuHeight += centeredToggleHeight
		} else {
			menuHeight += paddedRowHeight
		}
	}

	target := p.viewMenuTargetBounds(kind, ctx.PaneSize())
	if target.Empty() {
		return layout, false
	}
	xRight := target.Max.X
	xLeft := target.Min.X - menuWidth
	x := xRight
	// GetStandardLocationOptions tries the view's right edge first and then its
	// left edge with the menu right-aligned to the view's left edge.
	if !(xRight > 0 && xRight+menuWidth < ctx.PaneSize().X) && xLeft > 0 && xLeft+menuWidth < ctx.PaneSize().X {
		x = xLeft
	}
	y := target.Min.Y
	if y < 0 {
		y = 0
	}
	if y+menuHeight > ctx.PaneSize().Y {
		y = maxFloat32(0, ctx.PaneSize().Y-menuHeight)
	}

	layout.Bounds = redsmath.NewRect(x, y, x+menuWidth, y+menuHeight)
	closeContentWidth := eramViewMenuCloseWidthChar*charAdvance - 1
	closeWidth := float32(closeContentWidth + 2*eramViewMenuBorderWidth)
	layout.Close = redsmath.NewRect(layout.Bounds.Max.X-closeWidth, y, layout.Bounds.Max.X, y+headerHeight)
	layout.Title = redsmath.NewRect(x, y, layout.Close.Min.X, y+headerHeight)

	rowY := y + headerHeight - 1
	for i := 0; i < spec.RowCount; i++ {
		height := paddedRowHeight
		if spec.Rows[i].Centered {
			height = centeredToggleHeight
		}
		layout.Rows[i] = eramViewMenuRowLayout{
			Row:    spec.Rows[i],
			Bounds: redsmath.NewRect(x, rowY, x+menuWidth, rowY+height),
		}
		layout.Count++
		rowY += height - 1
	}
	return layout, true
}

func (p *ERAMPane) consumeViewMenuInput(ctx *panes.Context) bool {
	if p == nil || ctx == nil || p.viewUI.Menu.Kind == eramViewMenuNone {
		return false
	}
	if ctx.Keyboard != nil && (ctx.Keyboard.WasPressed(platform.KeyEscape) ||
		ctx.Keyboard.WasPressed(platform.KeyEnter) || ctx.Keyboard.WasPressed(platform.KeyKeypadEnter)) {
		p.closeViewMenu()
		return true
	}
	if ctx.Mouse == nil {
		return false
	}
	if p.consumeViewMenuRepeat(ctx.Mouse) {
		return true
	}

	layout, ok := p.buildViewMenuLayout(ctx, p.viewUI.Menu.Kind)
	if !ok {
		p.closeViewMenu()
		return false
	}
	mouse := ctx.Mouse
	pressedLeft := mouse.WasPressed(platform.MouseButtonLeft)
	pressedMiddle := mouse.WasPressed(platform.MouseButtonMiddle)
	pressedRight := mouse.WasPressed(platform.MouseButtonRight)
	if !pressedLeft && !pressedMiddle && !pressedRight {
		return layout.Bounds.Contains(mouse.Pos)
	}

	// CRC closes a view settings menu on any mouse-down outside it and returns
	// false, allowing that same click to continue to the underlying UI.
	if !layout.Bounds.Contains(mouse.Pos) {
		p.closeViewMenu()
		return false
	}
	// Only TBP/TBE (left/middle) operate menu pick areas. Other buttons inside
	// the menu are consumed as invalid selections.
	if pressedRight {
		return true
	}
	if layout.Close.Contains(mouse.Pos) {
		p.closeViewMenu()
		return true
	}

	increment := pressedMiddle
	button := platform.MouseButtonLeft
	if pressedMiddle {
		button = platform.MouseButtonMiddle
	}
	for i := 0; i < layout.Count; i++ {
		row := layout.Rows[i]
		if !row.Bounds.Contains(mouse.Pos) {
			continue
		}
		changed := p.activateViewMenuAction(row.Row.Action, increment)
		if row.Row.AutoRepeat && changed {
			p.viewUI.Menu.Repeat = &eramViewMenuRepeat{
				Action:      row.Row.Action,
				Increment:   increment,
				MouseButton: button,
				Next:        time.Now().Add(toolbarRepeatInitialDelay),
			}
		}
		return true
	}
	return true
}

func (p *ERAMPane) consumeViewMenuRepeat(mouse *platform.MouseState) bool {
	if p == nil || mouse == nil || p.viewUI.Menu.Repeat == nil {
		return false
	}
	repeat := p.viewUI.Menu.Repeat
	if !mouse.IsDown(repeat.MouseButton) {
		p.viewUI.Menu.Repeat = nil
		return false
	}
	now := time.Now()
	if now.Before(repeat.Next) {
		return true
	}
	if !p.activateViewMenuAction(repeat.Action, repeat.Increment) {
		p.viewUI.Menu.Repeat = nil
		return true
	}
	repeat.Next = now.Add(toolbarRepeatDelay)
	return true
}

func (p *ERAMPane) activateViewMenuAction(action eramViewMenuAction, increment bool) bool {
	if p == nil {
		return false
	}
	switch action {
	case eramViewMenuTimeOpaque:
		p.clock.isOpaque = !p.clock.isOpaque
		return true
	case eramViewMenuTimeBorder:
		p.clock.showBorder = !p.clock.showBorder
		return true
	case eramViewMenuTimeFont:
		old := p.clock.fontSize
		if increment {
			p.clock.fontSize = minInt(old+1, 8)
		} else {
			p.clock.fontSize = maxInt(old-1, 1)
		}
		return p.clock.fontSize != old
	case eramViewMenuTimeBrightness:
		old := p.clock.brightness
		if increment {
			p.clock.brightness = minInt(old+2, 100)
		} else {
			p.clock.brightness = maxInt(old-2, 0)
		}
		return p.clock.brightness != old
	default:
		return false
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (p *ERAMPane) drawViewUI(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Renderer == nil {
		return
	}
	p.drawViewSettingsMenu(ctx, zcb)
	p.drawViewMoveFrame(ctx, zcb)
}

func (p *ERAMPane) drawViewMoveFrame(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p.viewUI.Moving == nil {
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

func (p *ERAMPane) drawViewSettingsMenu(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p.viewUI.Menu.Kind == eramViewMenuNone {
		return
	}
	layout, ok := p.buildViewMenuLayout(ctx, p.viewUI.Menu.Kind)
	if !ok {
		return
	}
	spec, ok := p.activeViewMenuSpec(p.viewUI.Menu.Kind)
	if !ok {
		return
	}
	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return
	}
	texture := p.toolbarTexture(ctx.Renderer, eramViewMenuFontSize)
	if texture == 0 {
		return
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zViewSettingsMenu)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.DisableBlend()

	mousePos := redsmath.Vec2{X: -1, Y: -1}
	if ctx.Mouse != nil {
		mousePos = ctx.Mouse.Pos
	}

	gray := applyERAMBrightness(toolbarGray, p.buttonBrightness, p.systemBrightness)
	black := toolbarBlack
	green := applyERAMBrightness(toolbarIncDecGreen, p.buttonBrightness, p.systemBrightness)
	border := applyERAMBrightness(toolbarWhite, p.borderBrightness, p.systemBrightness)
	hoverBorder := applyERAMBrightness(toolbarWhite, p.pairedTargetBrightness, p.systemBrightness)
	textColor := applyERAMBrightness(toolbarWhite, p.textBrightness, p.systemBrightness).ToRGBA()

	drawViewMenuFace(cb, layout.Title, gray, border, hoverBorder, layout.Title.Contains(mousePos))
	drawViewMenuFace(cb, layout.Close, gray, border, hoverBorder, layout.Close.Contains(mousePos))
	for i := 0; i < layout.Count; i++ {
		row := layout.Rows[i]
		background := green
		if row.Row.Kind == eramViewMenuToggle {
			if row.Row.Active {
				background = gray
			} else {
				background = black
			}
		}
		drawViewMenuFace(cb, row.Bounds, background, border, hoverBorder, row.Bounds.Contains(mousePos))
	}

	td := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(td)
	td.SetFont(font)
	addCenteredMenuText(td, font, spec.Title, layout.Title, eramViewMenuFontSize, textColor, gray.ToRGBA())
	addCenteredMenuText(td, font, "X", layout.Close, eramViewMenuFontSize, textColor, gray.ToRGBA())
	charAdvance, _ := font.CharSize(eramViewMenuFontSize)
	leftPad := float32(int(float64(charAdvance) * 0.5))
	for i := 0; i < layout.Count; i++ {
		rowLayout := layout.Rows[i]
		row := rowLayout.Row
		label := row.Label
		background := green
		if row.Kind == eramViewMenuToggle {
			if row.Active {
				background = gray
				if row.ActiveLabel != "" {
					label = row.ActiveLabel
				}
			} else {
				background = black
				if row.InactiveLabel != "" {
					label = row.InactiveLabel
				}
			}
		} else {
			label += " " + strconv.Itoa(row.Value)
		}
		if row.Centered {
			addCenteredMenuText(td, font, label, rowLayout.Bounds, eramViewMenuFontSize, textColor, background.ToRGBA())
		} else {
			td.AddText(label, redsmath.Vec2{
				X: rowLayout.Bounds.Min.X + eramViewMenuBorderWidth + leftPad,
				Y: rowLayout.Bounds.Min.Y + eramViewMenuBorderWidth,
			}, renderer.TextStyle{
				Size:       eramViewMenuFontSize,
				Color:      textColor,
				Background: background.ToRGBA(),
			})
		}
	}
	td.GenerateCommands(cb, texture)

	cb.Blend()
	cb.DisableScissor()
}

func drawViewMenuFace(cb *renderer.CmdBuffer, bounds redsmath.Rect, background, border, hoverBorder renderer.RGB, hovering bool) {
	drawSolidRect(cb, bounds, background)
	if hovering {
		border = hoverBorder
	}
	drawBorderOnly(cb, bounds, border, eramViewMenuBorderWidth)
}

func addCenteredMenuText(
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	text string,
	bounds redsmath.Rect,
	fontSize int,
	color, background renderer.RGBA,
) {
	if td == nil || font == nil {
		return
	}
	w, h := font.MeasureText(text, fontSize)
	td.AddText(text, redsmath.Vec2{
		X: bounds.Min.X + (bounds.Width()-float32(w))*0.5,
		Y: bounds.Min.Y + (bounds.Height()-float32(h))*0.5,
	}, renderer.TextStyle{Size: fontSize, Color: color, Background: background})
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
