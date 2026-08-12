package eram

import (
	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"
)

// CRC uses one generic ViewPopup for small confirmation popups attached to
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
