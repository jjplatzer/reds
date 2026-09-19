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

// drawSSA draws the System Status Area. For now this intentionally contains
// only the mandatory Red Check symbol and UTC time from TI 6191.409 Rev. 30,
// Table 2-15; the remaining SSA fields are added separately.
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

	// Green outlined 10x10 box. Table B-1 defines normal list text/graphics
	// as green; Table 2-15 explicitly requires a green outline here.
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

	// Solid inverted equilateral delta, centered in the box. For an
	// equilateral triangle of height h, the centroid lies h/3 from the base.
	// REDS' y axis increases downward, so the tip has the larger y value.
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

	// TI 6191.409 Rev. 30, Table 2-15 field E displays UTC as HHMM/SS.
	// VICE places the first SSA text line 10 display units below the Red Check
	// anchor, with its left edge aligned to the check box's left edge.
	texture := p.systemFontTexture(ctx.Renderer, defaultListFontSize)
	if texture != 0 && p.systemFont != nil {
		td := renderer.GetTextDrawBuilder()
		td.SetFont(p.systemFont)
		td.AddText(
			time.Now().UTC().Format("1504/05"),
			redsmath.Vec2{X: ssaDefaultX * w, Y: centerY + 10},
			renderer.TextStyle{
				Size:  defaultListFontSize,
				Color: p.colors.List.ToRGBA(),
			},
		)
		td.GenerateCommands(cb, texture)
		renderer.ReturnTextDrawBuilder(td)
	}

	cb.DisableScissor()
}
