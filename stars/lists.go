package stars

import (
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	// VICE's default SSA position is (0.05, 0.90) in bottom-left-origin
	// normalized pane coordinates. REDS screen-space drawing uses a top-left
	// origin, so the equivalent y coordinate is 0.10 from the top.
	ssaDefaultX = float32(0.05)
	ssaDefaultY = float32(0.10)

	// TI 6191.409 Rev. 30, Table 2-15 describes the Red Check symbol as a
	// solid inverted delta centered in a green outlined box. The manual does
	// not prescribe pixel dimensions; these dimensions match VICE's STARS
	// implementation: a 10x10 box around a 7-unit-high equilateral triangle.
	ssaCheckBoxHalfSize = float32(5)
	ssaCheckTriangleH   = float32(7)

	zLists renderer.Z = 0
)

// drawSSA draws the System Status Area in the field order defined by
// TI 6191.409 Rev. 30, Table 2-15. Empty fields do not consume a line, so the
// area automatically shortens or lengthens as status information is added.
func (p *STARSPane) drawSSA(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil {
		return
	}

	w, h := ctx.PaneRect.Width(), ctx.PaneRect.Height()
	if w <= 0 || h <= 0 {
		return
	}

	// VICE stores the SSA's default anchor at normalized (0.05, 0.90) with
	// a bottom-left origin. Convert that to REDS' top-left screen coordinates.
	centerX := ssaDefaultX*w + ssaCheckBoxHalfSize
	centerY := ssaDefaultY * h

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zLists)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())

	// Field A - TCW/TDW Failure Alert, EFSL / DSF indicator.
	// A healthy TCW/TDW leaves this field empty (Table 2-15).

	// Field B - System Overload Alert.
	// A TCW/TDW that is not overloaded leaves this field empty (Table 2-15).

	// Field C - Sensor Failure Alert.
	// The Red Check symbol is always displayed. Table 2-15 defines it as a
	// solid inverted delta centered in a green outlined box; failed sensor
	// names, when available, will be rendered on this field after the symbol.
	cb.SetRGB(p.colors.List)
	cb.LineWidth(1)
	box := renderer.GetLinesBuilder()
	box.AddLineLoop([]renderer.PointVertex{
		{X: centerX - ssaCheckBoxHalfSize, Y: centerY - ssaCheckBoxHalfSize},
		{X: centerX + ssaCheckBoxHalfSize, Y: centerY - ssaCheckBoxHalfSize},
		{X: centerX + ssaCheckBoxHalfSize, Y: centerY + ssaCheckBoxHalfSize},
		{X: centerX - ssaCheckBoxHalfSize, Y: centerY + ssaCheckBoxHalfSize},
	})
	box.GenerateCommands(cb)
	renderer.ReturnLinesBuilder(box)

	// For an equilateral triangle of height h, the centroid lies h/3 from
	// the base. REDS' y axis increases downward, so the inverted delta's tip
	// has the larger y value.
	const invSqrt3 = float32(0.5773502691896258)
	halfBase := ssaCheckTriangleH * invSqrt3
	baseY := centerY - ssaCheckTriangleH/3
	tipY := centerY + 2*ssaCheckTriangleH/3

	triangle := renderer.GetColoredTrianglesBuilder()
	triangle.AddTriangleRGB(
		renderer.PointVertex{X: centerX - halfBase, Y: baseY},
		renderer.PointVertex{X: centerX + halfBase, Y: baseY},
		renderer.PointVertex{X: centerX, Y: tipY},
		p.colors.TextAlert,
	)
	triangle.GenerateCommands(cb)
	renderer.ReturnColoredTrianglesBuilder(triangle)

	// Field C1 - ADS Ground Station Alert.
	// With no failing/offline ADS-B Ground Stations this field is empty.

	fontSize := p.listFontSize()
	texture := p.systemFontTexture(ctx.Renderer, fontSize)
	if texture != 0 && p.systemFont != nil {
		td := renderer.GetTextDrawBuilder()
		td.SetFont(p.systemFont)

		textX := ssaDefaultX * w
		textY := centerY + 10
		addLine := func(text string, color renderer.RGB) {
			if text == "" {
				return
			}
			td.AddText(
				text,
				redsmath.Vec2{X: textX, Y: textY},
				renderer.TextStyle{Size: fontSize, Color: color.ToRGBA()},
			)
			textY += float32(fontSize)
		}

		// Field D - Weather Level Status.
		// This field is omitted until STARS weather-receipt/display state exists.

		// Field E - UTC Time, System Altimeter Setting.
		// Hours and minutes / seconds are followed by the system altimeter
		// setting used for altitude correction in this Terminal control area.
		addLine(p.ssaFieldEText(time.Now()), p.colors.List)

		// Fields E1 through N are omitted until their corresponding facility,
		// surveillance, flow-management, or controller preference state exists.

		// Field O - Mode of Operation.
		// The initial REDS STARS TCW/TDW runs in operational / normal mode.
		addLine("MODE: NORMAL", p.colors.List)

		td.GenerateCommands(cb, texture)
		renderer.ReturnTextDrawBuilder(td)
	}

	cb.DisableScissor()
}
