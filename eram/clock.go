package eram

import (
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
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
	location   redsmath.Vec2
	brightness int
	fontSize   int
	showBorder bool
}

func (p *ERAMPane) initializeClockState() {
	if p == nil {
		return
	}
	p.clock = eramClockState{
		// CRC TimeViewSettings.Location = TopLeft (20, 110).
		location:   redsmath.Vec2{X: 20, Y: 110},
		brightness: defaultClockBrightness,
		fontSize:   defaultClockFontSize,
		showBorder: true,
	}
}

// drawClock mirrors CRC's ERAM ViewTime default presentation. CRC updates the
// text every render from DateTime.UtcNow using the exact format "HHmm ss".
func (p *ERAMPane) drawClock(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Renderer == nil {
		return
	}

	// Reuse the ERAM text atlas already owned by the toolbar. The clock uses the
	// same FontType.EramText face in CRC, just at its own derived font size.
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

	// ViewTime gives its Text node Padding(.5, .5, .5, .5, TimeViewBody).
	// CRC converts those character-relative measurements by truncation.
	padX := int(float64(charAdvance) * 0.5)
	padY := int(float64(lineHeight) * 0.5)

	width := float32(2*clockBorderWidth + 2*padX + textWidth)
	height := float32(2*clockBorderWidth + 2*padY + textHeight)
	bounds := redsmath.NewRect(
		p.clock.location.X,
		p.clock.location.Y,
		p.clock.location.X+width,
		p.clock.location.Y+height,
	)

	x, y, fbWidth, fbHeight := ctx.PaneFramebufferRect()
	cb := zcb.At(zTimeView)
	cb.Viewport(x, y, fbWidth, fbHeight)
	cb.Scissor(x, y, fbWidth, fbHeight)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.DisableBlend()

	// TimeViewBody = White foreground, DarkBlue background, derived brightness
	// for the foreground and Background BCG for the body fill.
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
		X: p.clock.location.X + clockBorderWidth + float32(padX),
		Y: p.clock.location.Y + clockBorderWidth + float32(padY),
	}, renderer.TextStyle{
		Size:       fontSize,
		Color:      applyERAMBrightness(clockWhite, p.clock.brightness, p.systemBrightness).ToRGBA(),
		Background: applyERAMBrightness(clockDarkBlue, p.backgroundBrightness, p.systemBrightness).ToRGBA(),
	})
	td.GenerateCommands(cb, texture)

	cb.Blend()
	cb.DisableScissor()
}
