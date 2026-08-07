package eram

import (
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"
)

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
	location := resolveERAMAnchoredLocation(p.clock.location, size, paneSize)
	return redsmath.NewRect(location.X, location.Y, location.X+size.X, location.Y+size.Y)
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
	cb.DisableBlend()

	drawSolidRect(cb, bounds, applyERAMBrightness(
		clockDarkBlue,
		p.backgroundBrightness,
		p.systemBrightness,
	))
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
	td.AddText(text, redsmath.Vec2{
		X: bounds.Min.X + clockBorderWidth + float32(padX),
		Y: bounds.Min.Y + clockBorderWidth + float32(padY),
	}, renderer.TextStyle{
		Size:       fontSize,
		Color:      applyERAMBrightness(clockWhite, p.clock.brightness, p.systemBrightness).ToRGBA(),
		Background: applyERAMBrightness(clockDarkBlue, p.backgroundBrightness, p.systemBrightness).ToRGBA(),
	})
	td.GenerateCommands(cb, texture)

	cb.Blend()
	cb.DisableScissor()
}
