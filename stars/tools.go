package stars

import (
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

const starsRangeRingCount = 39

// drawRangeRings draws the STARS range rings in pane/window coordinates.
//
// TI 6191.409 Rev. 30, Appendix B, Table B-1 defines the normal range-ring
// color as dim gray (140,140,140). The BRITE/RR control independently scales
// that color and an illumination factor of OFF suppresses the rings.
//
// Keep this in tools.go to mirror VICE's STARS organization: VICE groups
// scope drawing helpers such as range rings in stars/tools.go rather than in a
// range-ring-specific source file.
func (p *STARSPane) drawRangeRings(ctx *panes.Context, zcb *renderer.ZCmdBuffer, transforms radar.LatLonTransformations) {
	if p == nil || ctx == nil || zcb == nil {
		return
	}

	ps := p.currentPrefs()
	if ps.Brightness.RangeRings == 0 || ps.RangeRingRadius <= 0 || ps.Range <= 0 {
		return
	}

	center := ps.DefaultCenter
	if ps.UseUserRangeRingsCenter {
		center = ps.RangeRingsUserCenter
	}
	centerWindow := transforms.WindowFromLatLon(center.Lat, center.Lon)

	// STARS RANGE is the distance from the display center to the nearest pane
	// edge. Consequently one NM occupies shortSide/(2*RANGE) logical pixels.
	shortSide := ctx.PaneRect.Width()
	if h := ctx.PaneRect.Height(); h < shortSide {
		shortSide = h
	}
	if shortSide <= 0 {
		return
	}
	pixelsPerNM := shortSide / (2 * ps.Range)
	if pixelsPerNM <= 0 {
		return
	}

	builder := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(builder)
	for i := 1; i <= starsRangeRingCount; i++ {
		radius := float32(i) * ps.RangeRingRadius * pixelsPerNM
		builder.AddCircle(
			renderer.PointVertex{X: centerWindow.X, Y: centerWindow.Y},
			radius,
			360,
		)
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zRangeRings)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.SetRGB(ps.Brightness.RangeRings.ScaleRGB(p.colors.RangeRing))

	// One STARS display pixel. REDS line widths are framebuffer pixels, while
	// the geometry above is in logical pane coordinates.
	lineWidth := ctx.DPIScale
	if lineWidth < 1 {
		lineWidth = 1
	}
	cb.LineWidth(lineWidth)
	builder.GenerateCommands(cb)
	cb.DisableScissor()
}
