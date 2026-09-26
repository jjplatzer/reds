package stars

import (
	stdmath "math"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

const starsRangeRingCount = 39

type compassEdge uint8

const (
	compassEdgeLeft compassEdge = iota
	compassEdgeRight
	compassEdgeTop
	compassEdgeBottom
)

// drawCompass draws the STARS compass rose against the usable scope boundary.
//
// TI 6191.409 Rev. 30, Appendix B, Table B-1 defines the TCW/TDW compass
// rose base color as dim gray (140,140,140); Table 4-1 assigns it to the CMP
// brightness category, including OFF. The operator manual does not specify
// raster dimensions for the individual ticks/labels. For those presentation
// details mirror VICE's STARS implementation: 5-degree ticks, labels every 10
// degrees, 10 display-pixel tick length, and label origins 14 display pixels
// inboard from the scope edge. VICE also uses STARS Tools character size 1.
//
// The high-resolution DCB occupies the top 72 display units in REDS. The
// compass therefore treats the DCB's lower edge as the top of the radar scope,
// rather than drawing underneath the application/window edge.
func (p *STARSPane) drawCompass(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.LatLonTransformations,
) {
	if p == nil || ctx == nil || zcb == nil || p.systemFont == nil {
		return
	}

	ps := p.currentPrefs()
	if ps.Brightness.Compass == 0 {
		return
	}

	w, h := ctx.PaneRect.Width(), ctx.PaneRect.Height()
	if w <= 0 || h <= dcbButtonSize {
		return
	}

	// REDS currently presents the STARS DCB at the top of the pane. This is
	// intentionally only the *compass* boundary: scope transformations remain
	// based on the complete pane just as in VICE, whose drawDCB returns a
	// reduced scope extent specifically for edge-oriented graphics.
	scope := redsmath.NewRect(0, dcbButtonSize, w, h)
	center := p.currentCenter()
	centerWindow := transforms.WindowFromLatLon(center.Lat, center.Lon)

	fontSize := p.listFontSize() // VICE: CharSize.Tools defaults to STARS size 1.
	texture := p.systemFontTexture(ctx.Renderer, fontSize)
	fs := p.systemFont.Size(fontSize)
	if texture == 0 || fs == nil {
		return
	}

	color := ps.Brightness.Compass.ScaleRGB(p.colors.Compass)
	lines := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(lines)
	text := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(text)
	text.SetFont(p.systemFont)

	for heading := 5; heading <= 360; heading += 5 {
		radians := float64(heading) * stdmath.Pi / 180
		// Heading zero/360 is screen-up; headings increase clockwise. STARS
		// has already rotated geographic content into magnetic display space.
		dir := redsmath.Vec2{
			X: float32(stdmath.Sin(radians)),
			Y: float32(-stdmath.Cos(radians)),
		}

		pEdge, edge, ok := compassRayToRect(centerWindow, dir, scope)
		if !ok {
			continue
		}

		// VICE uses a 10-pixel tick and starts the label 14 pixels inboard.
		pInset := pEdge.Sub(dir.Mul(10))
		lines.AddLine(
			renderer.PointVertex{X: pEdge.X, Y: pEdge.Y},
			renderer.PointVertex{X: pInset.X, Y: pInset.Y},
		)

		if heading%10 != 0 {
			continue
		}

		label := compassHeadingLabel(heading)
		labelWidth := compassTextWidth(fs, label)
		lineHeight := float32(fs.LineHeight)
		pText := pEdge.Sub(dir.Mul(14))

		// Text positions in REDS are upper-left origins. Keep the label centered
		// on the radial at left/right edges and centered horizontally at the
		// top/bottom edges, matching VICE's edge-specific adjustments.
		switch edge {
		case compassEdgeLeft:
			pText.Y -= lineHeight / 2
		case compassEdgeRight:
			pText.X -= labelWidth
			pText.Y -= lineHeight / 2
		case compassEdgeTop:
			pText.X -= labelWidth / 2
		case compassEdgeBottom:
			pText.X -= labelWidth / 2
			pText.Y -= lineHeight
		}

		text.AddText(label, pText, renderer.TextStyle{
			Size:  fontSize,
			Color: color.ToRGBA(),
		})
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zCompass)
	cb.Viewport(x, y, width, height)
	absoluteScope := scope.Translate(ctx.PaneRect.Min)
	sx, sy, sw, sh := ctx.LogicalToFramebufferRect(absoluteScope)
	cb.Scissor(sx, sy, sw, sh)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.SetRGB(color)

	// One STARS display pixel. Geometry is expressed in logical display units;
	// renderer line widths are framebuffer pixels.
	lineWidth := ctx.DPIScale
	if lineWidth < 1 {
		lineWidth = 1
	}
	cb.LineWidth(lineWidth)
	lines.GenerateCommands(cb)
	text.GenerateCommands(cb, texture)
	cb.DisableScissor()
}

func compassRayToRect(origin, dir redsmath.Vec2, bounds redsmath.Rect) (redsmath.Vec2, compassEdge, bool) {
	const epsilon = float32(1e-6)
	bestT := float32(stdmath.MaxFloat32)
	bestEdge := compassEdgeLeft
	found := false

	consider := func(t float32, edge compassEdge) {
		if t < 0 || t >= bestT {
			return
		}
		p := origin.Add(dir.Mul(t))
		const tolerance = float32(0.125)
		if p.X < bounds.Min.X-tolerance || p.X > bounds.Max.X+tolerance ||
			p.Y < bounds.Min.Y-tolerance || p.Y > bounds.Max.Y+tolerance {
			return
		}
		bestT = t
		bestEdge = edge
		found = true
	}

	if dir.X > epsilon {
		consider((bounds.Max.X-origin.X)/dir.X, compassEdgeRight)
	} else if dir.X < -epsilon {
		consider((bounds.Min.X-origin.X)/dir.X, compassEdgeLeft)
	}
	if dir.Y > epsilon {
		consider((bounds.Max.Y-origin.Y)/dir.Y, compassEdgeBottom)
	} else if dir.Y < -epsilon {
		consider((bounds.Min.Y-origin.Y)/dir.Y, compassEdgeTop)
	}

	if !found {
		return redsmath.Vec2{}, 0, false
	}
	return origin.Add(dir.Mul(bestT)), bestEdge, true
}

func compassHeadingLabel(heading int) string {
	// VICE labels magnetic north as 360 rather than 000. The loop only calls
	// this for 10-degree headings, but keep the formatter general.
	return string([]byte{
		byte('0' + (heading/100)%10),
		byte('0' + (heading/10)%10),
		byte('0' + heading%10),
	})
}

func compassTextWidth(fs *renderer.BitmapFontSize, text string) float32 {
	if fs == nil {
		return 0
	}
	var width int
	for _, r := range text {
		if glyph, ok := fs.Glyph(r); ok {
			width += glyph.Advance
		}
	}
	return float32(width)
}

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
