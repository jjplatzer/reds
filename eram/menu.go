package eram

import (
	"strconv"
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"
)

// Generic ERAM function popups.
//
// VICE keeps reusable popup placement/render/input infrastructure in menu.go,
// while individual views decide when to open a popup. Keep REDS organized the
// same way: WX and future list views own their actions, but the popup chrome,
// placement, hit testing, and lifecycle are shared here.
//
// // CRC uses one generic ViewPopup for small confirmation popups attached to
// list entries (WX, altimeter, CRR, ...). Keep the REDS model generic too so
// future list views can reuse placement/render/input instead of duplicating it.
type eramPopupKind uint8

const (
	eramPopupNone eramPopupKind = iota
	eramPopupDeleteWXReport
)

type eramPopupState struct {
	Kind    eramPopupKind
	Text    string
	Payload string
	Origin  redsmath.Vec2
}

const (
	eramPopupFontSize   = 2
	eramPopupWidthChars = 13
	eramPopupBorder     = 1
	eramPopupRootPadY   = 2
	eramPopupTextPadY   = 3
)

func (p *ERAMPane) closePopup() {
	if p == nil {
		return
	}
	p.popup = eramPopupState{}
}

func (p *ERAMPane) popupBounds() redsmath.Rect {
	if p == nil || p.popup.Kind == eramPopupNone {
		return redsmath.Rect{}
	}
	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return redsmath.Rect{}
	}
	advance, _ := font.CharSize(eramPopupFontSize)
	_, textHeight := font.MeasureText("0", eramPopupFontSize)
	if advance <= 0 || textHeight <= 0 {
		return redsmath.Rect{}
	}
	width := float32(eramPopupWidthChars*advance + 2*eramPopupBorder)
	// ViewPopup root: Border(1), Padding(2, 0). Text contributes its standard
	// 3 px top/bottom padding, so total height is glyph + 12 px.
	height := float32(textHeight + 2*eramPopupTextPadY + 2*eramPopupRootPadY + 2*eramPopupBorder)
	return redsmath.NewRect(p.popup.Origin.X, p.popup.Origin.Y, p.popup.Origin.X+width, p.popup.Origin.Y+height)
}

func (p *ERAMPane) openPopup(ctx *panes.Context, kind eramPopupKind, text, payload string, host redsmath.Rect) {
	if p == nil || ctx == nil || kind == eramPopupNone || host.Empty() {
		return
	}
	p.closeViewMenu()
	p.popup = eramPopupState{Kind: kind, Text: text, Payload: payload}
	bounds := p.popupBounds()
	if bounds.Empty() {
		p.closePopup()
		return
	}
	size := bounds.Size()
	pane := ctx.PaneSize()

	// CRC supplies two anchored locations: immediately right of the picked Text
	// node, or immediately left if the right side does not fit. Vertically the
	// popup is centered on the selected text node.
	x := host.Max.X + 1
	if x+size.X > pane.X {
		x = host.Min.X - 1 - size.X
	}
	y := host.Min.Y + host.Size().Y/2 - size.Y/2
	x = maxFloat32(0, minFloat32(x, pane.X-size.X))
	y = maxFloat32(0, minFloat32(y, pane.Y-size.Y))
	p.popup.Origin = redsmath.Vec2{X: x, Y: y}

	// CRC moves the cursor to the popup center when it first appears.
	popup := p.popupBounds()
	setPaneMousePosition(ctx, redsmath.Vec2{
		X: popup.Min.X + popup.Size().X/2,
		Y: popup.Min.Y + popup.Size().Y/2,
	})
}

func (p *ERAMPane) consumePopupInput(ctx *panes.Context) bool {
	if p == nil || ctx == nil || p.popup.Kind == eramPopupNone {
		return false
	}
	if ctx.Keyboard != nil && ctx.Keyboard.WasPressed(platform.KeyEscape) {
		p.closePopup()
		return true
	}
	if ctx.Mouse == nil {
		return false
	}
	if !ctx.Mouse.WasPressed(platform.MouseButtonLeft) && !ctx.Mouse.WasPressed(platform.MouseButtonMiddle) {
		return false
	}

	picked := p.popupBounds().Contains(ctx.Mouse.Pos)
	kind := p.popup.Kind
	payload := p.popup.Payload
	p.closePopup()
	if picked {
		switch kind {
		case eramPopupDeleteWXReport:
			p.removeWXStation(payload)
		}
	}
	// CRC ViewPopup consumes TBP/TBE whether the click confirms or merely closes.
	return true
}

func (p *ERAMPane) drawPopup(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Renderer == nil || p.popup.Kind == eramPopupNone {
		return
	}
	bounds := p.popupBounds()
	if bounds.Empty() {
		return
	}
	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return
	}
	texture := p.toolbarTexture(ctx.Renderer, eramPopupFontSize)
	if texture == 0 {
		return
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zViewPopup)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.DisableBlend()

	body := applyERAMBrightness(toolbarGray, p.buttonBrightness, p.systemBrightness)
	border := applyERAMBrightness(toolbarWhite, p.borderBrightness, p.systemBrightness)
	text := applyERAMBrightness(toolbarWhite, p.textBrightness, p.systemBrightness)
	drawSolidRect(cb, bounds, body)
	drawBorderOnly(cb, bounds, border, eramPopupBorder)

	td := renderer.GetTextDrawBuilder()
	td.SetFont(font)
	addCenteredMenuText(td, font, p.popup.Text, bounds, eramPopupFontSize, text.ToRGBA(), body.ToRGBA())
	cb.Blend()
	td.GenerateCommands(cb, texture)
	renderer.ReturnTextDrawBuilder(td)

	if ctx.Mouse != nil && bounds.Contains(ctx.Mouse.Pos) {
		emphasis := applyERAMBrightness(toolbarWhite, p.pairedTargetBrightness, p.systemBrightness)
		drawBorderOnly(cb, bounds, emphasis, 1)
	}
	cb.DisableScissor()
}

// ERAM has many middle-click configuration menus attached to movable views.
// Keep the menu model/render/input path generic: each view contributes only a
// menu spec and action handling, while placement, hit testing, row chrome,
// auto-repeat and cursor warping are shared.
type eramViewMenuKind uint8

const (
	eramViewMenuNone eramViewMenuKind = iota
	eramViewMenuTime
	eramViewMenuChecklist
	eramViewMenuWX
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
	eramViewMenuChecklistOpaque
	eramViewMenuChecklistBorder
	eramViewMenuChecklistLines
	eramViewMenuChecklistFont
	eramViewMenuChecklistHighlight
	eramViewMenuChecklistText
	eramViewMenuWXOpaque
	eramViewMenuWXBorder
	eramViewMenuWXTearoffs
	eramViewMenuWXLines
	eramViewMenuWXFont
	eramViewMenuWXBrightness
)

type eramViewMenuRow struct {
	Action        eramViewMenuAction
	Kind          eramViewMenuRowKind
	Label         string
	ActiveLabel   string
	InactiveLabel string
	Active        bool
	Value         int
	ValueText     string
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

// eramViewMenuAnchor describes which host edge is pinned to a popup that was
// placed beside it. Once a menu opens its Origin never follows later host-size
// changes; instead the adjacent host edge stays pinned. This is what CRC does
// in practice and is also how VICE's viewPopupPlacement works.
type eramViewMenuAnchor uint8

const (
	eramViewMenuAnchorNone  eramViewMenuAnchor = iota
	eramViewMenuAnchorRight                    // host right edge == PinX (menu is on the right)
	eramViewMenuAnchorLeft                     // host left edge == PinX (menu is on the left)
)

type eramViewMenuState struct {
	Kind   eramViewMenuKind
	Origin redsmath.Vec2
	Anchor eramViewMenuAnchor
	PinX   float32
	Repeat *eramViewMenuRepeat
}

const (
	eramViewMenuFontSize       = 2
	eramViewMenuBorderWidth    = 1
	eramViewMenuCloseWidthChar = 2
	// CRC Text has a fixed 3 px top and bottom pad. Header and MenuPickArea
	// each add a 1 px border around that Text node.
	eramViewMenuTextPadY = 3
)

type eramViewMenuMetrics struct {
	Width       float32
	RowHeight   float32
	CloseWidth  float32
	CharAdvance int
	TextHeight  int
}

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
	case eramViewMenuChecklist:
		spec.Title = "POS CHK"
		if p.checklist.active == eramChecklistEmergency {
			spec.Title = "EMRG CHK"
		}
		spec.Rows[0] = eramViewMenuRow{
			Action:        eramViewMenuChecklistOpaque,
			Kind:          eramViewMenuToggle,
			ActiveLabel:   "O",
			InactiveLabel: "T",
			Active:        p.checklist.prefs.isOpaque,
			Centered:      true,
		}
		spec.Rows[1] = eramViewMenuRow{
			Action: eramViewMenuChecklistBorder,
			Kind:   eramViewMenuToggle,
			Label:  "BORDER",
			Active: p.checklist.prefs.showBorder,
		}
		linesValue := strconv.Itoa(p.checklist.prefs.lines)
		if p.checklist.prefs.lines == defaultChecklistLines {
			linesValue = "21+"
		}
		spec.Rows[2] = eramViewMenuRow{
			Action:     eramViewMenuChecklistLines,
			Kind:       eramViewMenuIncDec,
			Label:      "LINES",
			Value:      p.checklist.prefs.lines,
			ValueText:  linesValue,
			AutoRepeat: true,
		}
		spec.Rows[3] = eramViewMenuRow{
			Action: eramViewMenuChecklistFont,
			Kind:   eramViewMenuIncDec,
			Label:  "FONT",
			Value:  p.checklist.prefs.fontSize,
		}
		spec.Rows[4] = eramViewMenuRow{
			Action:     eramViewMenuChecklistHighlight,
			Kind:       eramViewMenuIncDec,
			Label:      "HIGHLIGHT",
			Value:      p.checklist.prefs.highlightBrightness,
			AutoRepeat: true,
		}
		spec.Rows[5] = eramViewMenuRow{
			Action:     eramViewMenuChecklistText,
			Kind:       eramViewMenuIncDec,
			Label:      "TEXT",
			Value:      p.checklist.prefs.brightness,
			AutoRepeat: true,
		}
		spec.RowCount = 6
		return spec, true
	case eramViewMenuWX:
		spec.Title = "WX"
		spec.Rows[0] = eramViewMenuRow{
			Action:        eramViewMenuWXOpaque,
			Kind:          eramViewMenuToggle,
			ActiveLabel:   "O",
			InactiveLabel: "T",
			Active:        p.wxReport.prefs.isOpaque,
			Centered:      true,
		}
		spec.Rows[1] = eramViewMenuRow{
			Action: eramViewMenuWXBorder,
			Kind:   eramViewMenuToggle,
			Label:  "BORDER",
			Active: p.wxReport.prefs.showBorder,
		}
		spec.Rows[2] = eramViewMenuRow{
			Action: eramViewMenuWXTearoffs,
			Kind:   eramViewMenuToggle,
			Label:  "TEAROFF",
			Active: p.wxReport.prefs.showTearoffs,
		}
		linesValue := strconv.Itoa(p.wxReport.prefs.lines)
		if p.wxReport.prefs.lines == 21 {
			linesValue = "21+"
		}
		spec.Rows[3] = eramViewMenuRow{
			Action:     eramViewMenuWXLines,
			Kind:       eramViewMenuIncDec,
			Label:      "LINES",
			Value:      p.wxReport.prefs.lines,
			ValueText:  linesValue,
			AutoRepeat: true,
		}
		spec.Rows[4] = eramViewMenuRow{
			Action: eramViewMenuWXFont,
			Kind:   eramViewMenuIncDec,
			Label:  "FONT",
			Value:  p.wxReport.prefs.fontSize,
		}
		spec.Rows[5] = eramViewMenuRow{
			Action:     eramViewMenuWXBrightness,
			Kind:       eramViewMenuIncDec,
			Label:      "BRIGHT",
			Value:      p.wxReport.prefs.brightness,
			AutoRepeat: true,
		}
		spec.RowCount = 6
		return spec, true
	default:
		return spec, false
	}
}

func (p *ERAMPane) viewMenuTargetBounds(kind eramViewMenuKind, paneSize redsmath.Vec2) redsmath.Rect {
	switch kind {
	case eramViewMenuTime:
		return p.clockBounds(paneSize)
	case eramViewMenuChecklist:
		return p.checklistBounds(paneSize)
	case eramViewMenuWX:
		return p.wxReportBounds(paneSize)
	default:
		return redsmath.Rect{}
	}
}

func (p *ERAMPane) viewMenuMetrics(kind eramViewMenuKind) (eramViewMenuMetrics, bool) {
	var metrics eramViewMenuMetrics
	if p == nil {
		return metrics, false
	}
	if _, ok := p.activeViewMenuSpec(kind); !ok {
		return metrics, false
	}
	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return metrics, false
	}
	charAdvance, _ := font.CharSize(eramViewMenuFontSize)
	_, textHeight := font.MeasureText("0", eramViewMenuFontSize)
	if charAdvance <= 0 || textHeight <= 0 {
		return metrics, false
	}

	// MenuPickAreaBase MinWidth is 11 characters of content. Non-centered
	// rows add a 0.5-character left Padding, which makes them the widest child
	// and therefore determines the root Column width in CRC.
	leftPad := int(float64(charAdvance) * 0.5)
	widthChars := 11
	if kind == eramViewMenuChecklist {
		widthChars = 14
	}
	metrics.Width = float32(widthChars*charAdvance + leftPad + 2*eramViewMenuBorderWidth)
	metrics.RowHeight = float32(textHeight + 2*eramViewMenuTextPadY + 2*eramViewMenuBorderWidth)
	// ClosePickArea uses MinWidth=2 chars with addPixels=-1 plus a 1 px border;
	// its -1 left margin makes the header/close borders overlap by one pixel.
	metrics.CloseWidth = float32(eramViewMenuCloseWidthChar*charAdvance + 1)
	metrics.CharAdvance = charAdvance
	metrics.TextHeight = textHeight
	return metrics, true
}

func viewMenuHeight(rowHeight float32, rowCount int) float32 {
	if rowCount <= 0 {
		return rowHeight
	}
	// The header does not overlap the first MenuPickArea. Each subsequent
	// MenuPickArea has Margin.Bottom=-1, so content-row borders overlap by 1 px.
	return rowHeight + float32(rowCount)*rowHeight - float32(maxInt(0, rowCount-1))
}

func (p *ERAMPane) openViewMenu(ctx *panes.Context, kind eramViewMenuKind) {
	if p == nil || ctx == nil || kind == eramViewMenuNone {
		return
	}
	spec, ok := p.activeViewMenuSpec(kind)
	if !ok {
		return
	}
	metrics, ok := p.viewMenuMetrics(kind)
	if !ok {
		return
	}

	// Resolve the target before installing menu state so clockBounds doesn't
	// attempt to pin against a half-initialized popup.
	target := p.viewMenuTargetBounds(kind, ctx.PaneSize())
	if target.Empty() {
		return
	}
	menuHeight := viewMenuHeight(metrics.RowHeight, spec.RowCount)
	paneSize := ctx.PaneSize()

	x := target.Max.X
	anchor := eramViewMenuAnchorRight
	pinX := target.Max.X
	if x+metrics.Width > paneSize.X {
		if target.Min.X-metrics.Width >= 0 {
			x = target.Min.X - metrics.Width
			anchor = eramViewMenuAnchorLeft
			pinX = target.Min.X
		} else {
			x = maxFloat32(0, paneSize.X-metrics.Width)
			anchor = eramViewMenuAnchorRight
			pinX = x
		}
	}
	if x < 0 {
		x = 0
		anchor = eramViewMenuAnchorLeft
		pinX = x + metrics.Width
	}
	y := target.Min.Y
	if y < 0 {
		y = 0
	}
	if y+menuHeight > paneSize.Y {
		y = maxFloat32(0, paneSize.Y-menuHeight)
	}

	p.viewUI.Menu = eramViewMenuState{
		Kind:   kind,
		Origin: redsmath.Vec2{X: x, Y: y},
		Anchor: anchor,
		PinX:   pinX,
	}
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

func (p *ERAMPane) pinnedViewTopLeft(kind eramViewMenuKind, current, size, paneSize redsmath.Vec2) (redsmath.Vec2, bool) {
	if p == nil || p.viewUI.Menu.Kind != kind {
		return current, false
	}
	position := current
	switch p.viewUI.Menu.Anchor {
	case eramViewMenuAnchorRight:
		position.X = p.viewUI.Menu.PinX - size.X
	case eramViewMenuAnchorLeft:
		position.X = p.viewUI.Menu.PinX
	default:
		return current, false
	}
	return clampERAMViewPosition(position, size, paneSize), true
}

func (p *ERAMPane) buildViewMenuLayout(ctx *panes.Context, kind eramViewMenuKind) (eramViewMenuLayout, bool) {
	var layout eramViewMenuLayout
	if p == nil || ctx == nil || p.viewUI.Menu.Kind != kind {
		return layout, false
	}
	spec, ok := p.activeViewMenuSpec(kind)
	if !ok {
		return layout, false
	}
	metrics, ok := p.viewMenuMetrics(kind)
	if !ok {
		return layout, false
	}

	x := p.viewUI.Menu.Origin.X
	y := p.viewUI.Menu.Origin.Y
	menuHeight := viewMenuHeight(metrics.RowHeight, spec.RowCount)
	layout.Bounds = redsmath.NewRect(x, y, x+metrics.Width, y+menuHeight)
	layout.Close = redsmath.NewRect(layout.Bounds.Max.X-metrics.CloseWidth, y, layout.Bounds.Max.X, y+metrics.RowHeight)
	layout.Title = redsmath.NewRect(x, y, layout.Close.Min.X, y+metrics.RowHeight)

	rowY := y + metrics.RowHeight
	for i := 0; i < spec.RowCount; i++ {
		layout.Rows[i] = eramViewMenuRowLayout{
			Row:    spec.Rows[i],
			Bounds: redsmath.NewRect(x, rowY, x+metrics.Width, rowY+metrics.RowHeight),
		}
		layout.Count++
		rowY += metrics.RowHeight - 1
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
	case eramViewMenuChecklistOpaque:
		p.checklist.prefs.isOpaque = !p.checklist.prefs.isOpaque
		return true
	case eramViewMenuChecklistBorder:
		p.checklist.prefs.showBorder = !p.checklist.prefs.showBorder
		return true
	case eramViewMenuChecklistLines:
		old := p.checklist.prefs.lines
		if increment {
			p.checklist.prefs.lines = minInt(old+1, defaultChecklistLines)
		} else {
			p.checklist.prefs.lines = maxInt(old-1, 3)
		}
		if p.checklist.prefs.lines != old {
			p.clampChecklistTopLine()
		}
		return p.checklist.prefs.lines != old
	case eramViewMenuChecklistFont:
		old := p.checklist.prefs.fontSize
		if increment {
			p.checklist.prefs.fontSize = minInt(old+1, 3)
		} else {
			p.checklist.prefs.fontSize = maxInt(old-1, 1)
		}
		return p.checklist.prefs.fontSize != old
	case eramViewMenuChecklistHighlight:
		old := p.checklist.prefs.highlightBrightness
		if increment {
			p.checklist.prefs.highlightBrightness = minInt(old+2, 100)
		} else {
			p.checklist.prefs.highlightBrightness = maxInt(old-2, 0)
		}
		return p.checklist.prefs.highlightBrightness != old
	case eramViewMenuChecklistText:
		old := p.checklist.prefs.brightness
		if increment {
			p.checklist.prefs.brightness = minInt(old+2, 100)
		} else {
			p.checklist.prefs.brightness = maxInt(old-2, 0)
		}
		return p.checklist.prefs.brightness != old
	case eramViewMenuWXOpaque:
		p.wxReport.prefs.isOpaque = !p.wxReport.prefs.isOpaque
		return true
	case eramViewMenuWXBorder:
		p.wxReport.prefs.showBorder = !p.wxReport.prefs.showBorder
		return true
	case eramViewMenuWXTearoffs:
		p.wxReport.prefs.showTearoffs = !p.wxReport.prefs.showTearoffs
		return true
	case eramViewMenuWXLines:
		old := p.wxReport.prefs.lines
		if increment {
			p.wxReport.prefs.lines = minInt(old+1, 21)
		} else {
			p.wxReport.prefs.lines = maxInt(old-1, 3)
		}
		if p.wxReport.prefs.lines != old {
			p.clampWXTopLine()
		}
		return p.wxReport.prefs.lines != old
	case eramViewMenuWXFont:
		old := p.wxReport.prefs.fontSize
		if increment {
			p.wxReport.prefs.fontSize = minInt(old+1, 3)
		} else {
			p.wxReport.prefs.fontSize = maxInt(old-1, 1)
		}
		return p.wxReport.prefs.fontSize != old
	case eramViewMenuWXBrightness:
		old := p.wxReport.prefs.brightness
		if increment {
			p.wxReport.prefs.brightness = minInt(old+2, 100)
		} else {
			p.wxReport.prefs.brightness = maxInt(old-2, 0)
		}
		return p.wxReport.prefs.brightness != old
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

func (p *ERAMPane) drawViewSettingsMenu(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Renderer == nil || p.viewUI.Menu.Kind == eramViewMenuNone {
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
	metrics, ok := p.viewMenuMetrics(p.viewUI.Menu.Kind)
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

	// CRC overlaps adjacent MenuPickArea borders by one pixel. Draw all normal
	// faces first, then draw the hovered outline last. Otherwise the next row's
	// background overwrites the bottom edge of a hovered middle row (T, BORDER,
	// FONT), while the final row happens to look correct because nothing follows
	// it.
	drawViewMenuFace(cb, layout.Title, gray, border)
	drawViewMenuFace(cb, layout.Close, gray, border)
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
		drawViewMenuFace(cb, row.Bounds, background, border)
	}

	// Hover chrome sits above every overlapping row face, matching CRC's
	// highlighted pick-area outline on all four sides.
	if layout.Title.Contains(mousePos) {
		drawBorderOnly(cb, layout.Title, hoverBorder, eramViewMenuBorderWidth)
	}
	if layout.Close.Contains(mousePos) {
		drawBorderOnly(cb, layout.Close, hoverBorder, eramViewMenuBorderWidth)
	}
	for i := 0; i < layout.Count; i++ {
		if layout.Rows[i].Bounds.Contains(mousePos) {
			drawBorderOnly(cb, layout.Rows[i].Bounds, hoverBorder, eramViewMenuBorderWidth)
		}
	}

	td := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(td)
	td.SetFont(font)
	addCenteredMenuText(td, font, spec.Title, layout.Title, eramViewMenuFontSize, textColor, gray.ToRGBA())
	addCenteredMenuText(td, font, "X", layout.Close, eramViewMenuFontSize, textColor, gray.ToRGBA())
	leftPad := float32(int(float64(metrics.CharAdvance) * 0.5))
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
			value := row.ValueText
			if value == "" {
				value = strconv.Itoa(row.Value)
			}
			label += " " + value
		}
		if row.Centered {
			addCenteredMenuText(td, font, label, rowLayout.Bounds, eramViewMenuFontSize, textColor, background.ToRGBA())
		} else {
			// CRC's Text node contributes 3 px top/bottom padding inside the
			// MenuPickArea. Centering by measured glyph height reproduces the
			// same baseline and avoids the vertically cramped appearance.
			td.AddText(label, redsmath.Vec2{
				X: rowLayout.Bounds.Min.X + eramViewMenuBorderWidth + leftPad,
				Y: rowLayout.Bounds.Min.Y + (rowLayout.Bounds.Height()-float32(metrics.TextHeight))*0.5,
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

func drawViewMenuFace(cb *renderer.CmdBuffer, bounds redsmath.Rect, background, border renderer.RGB) {
	drawSolidRect(cb, bounds, background)
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
