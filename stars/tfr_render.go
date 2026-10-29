package stars

import (
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

// tfrProjectedPolygon converts one FAA polygon (exterior ring and optional
// holes) into the existing scope projection. A GeoJSON ring repeats its first
// vertex; remove that closure before calling AddLineLoop/earcut. Longitude is
// wrapped relative to the scope center for TFRs around the antimeridian.
func tfrProjectedPolygon(t radar.LatLonTransformations, polygon starsTFRPolygon) ([][]renderer.PointVertex, [][]redsmath.Vec2) {
	lines := make([][]renderer.PointVertex, 0, len(polygon.Rings))
	rings := make([][]redsmath.Vec2, 0, len(polygon.Rings))
	for _, ring := range polygon.Rings {
		if len(ring) < 4 {
			continue
		}
		points := ring
		if ring[0] == ring[len(ring)-1] {
			points = ring[:len(ring)-1]
		}
		if len(points) < 3 {
			continue
		}
		line := make([]renderer.PointVertex, 0, len(points))
		xy := make([]redsmath.Vec2, 0, len(points))
		for _, pt := range points {
			pos := t.WindowFromLatLonWrapped(pt.Lat, pt.Lon)
			line = append(line, renderer.PointVertex{X: pos.X, Y: pos.Y})
			xy = append(xy, pos)
		}
		lines = append(lines, line)
		rings = append(rings, xy)
	}
	return lines, rings
}

// Cheap bounding-box rejection avoids tessellating distant national TFRs on
// every scope redraw. The first ring is the exterior, per RFC 7946.
func tfrPolygonInViewport(outer []renderer.PointVertex, w, h float32) bool {
	if len(outer) == 0 || w <= 0 || h <= 0 {
		return false
	}
	minX, maxX, minY, maxY := outer[0].X, outer[0].X, outer[0].Y, outer[0].Y
	for _, pt := range outer[1:] {
		minX = min(minX, pt.X)
		maxX = max(maxX, pt.X)
		minY = min(minY, pt.Y)
		maxY = max(maxY, pt.Y)
	}
	return maxX >= 0 && maxY >= 0 && minX <= w && minY <= h
}

// VICE converts each TFR to a closed, shaded restriction area, with its title
// positioned at the average of the geographic vertices and HideId=true.
// REDS follows those choices, but uses the Rev.30 restriction-area palette
// (including the optional yellow-color-1 override) instead of VICE's palette.
// This is only called for a locally visible FAA entry; it does not mutate any
// controller-defined areas or the cached FAA GeoJSON.
func (p *STARSPane) drawFAARestrictionArea(
	ctx *panes.Context, cb *renderer.CmdBuffer, t radar.LatLonTransformations,
	ra starsRestrictionArea, settings restrictionAreaDisplaySettings,
	shadeMode renderer.DrawMode, labels *renderer.TextDrawBuilder,
	fontSize int, textColor renderer.RGB, blinkDim bool,
) {
	color := p.currentPrefs().Brightness.VideoGroupB.ScaleRGB(p.restrictionAreaGeometryColor(ra.Color - 1))
	for _, polygon := range ra.TFRPolygons {
		lines, rings := tfrProjectedPolygon(t, polygon)
		if len(lines) == 0 || !tfrPolygonInViewport(lines[0], ctx.PaneRect.Width(), ctx.PaneRect.Height()) {
			continue
		}
		outline := renderer.GetLinesBuilder()
		for _, line := range lines {
			outline.AddLineLoop(line) // holes also have visible boundaries
		}
		if ra.Shaded {
			// Shared renderer earcut supports holes and arbitrary vertex counts;
			// triangulating each polygon separately preserves multipart NOTAMs.
			vertices, indices := renderer.TessellateRings(rings)
			if len(indices) > 0 {
				shaded := renderer.GetTrianglesBuilder()
				for i := 0; i+2 < len(indices); i += 3 {
					shaded.AddTriangle(vertices[indices[i]], vertices[indices[i+1]], vertices[indices[i+2]])
				}
				cb.SetRGB(color)
				shaded.GenerateCommands(cb, shadeMode, 0)
				renderer.ReturnTrianglesBuilder(shaded)
			}
		}
		cb.SetRGB(color)
		outline.GenerateCommands(cb)
		renderer.ReturnLinesBuilder(outline)
	}
	if settings.HideText || labels == nil {
		return
	}
	text := strings.TrimSpace(strings.Join(ra.Text[:], " "))
	if text == "" {
		text = ra.NOTAM
	}
	// VICE's imported TFRs have HideId=true: the numeric ID is available in
	// GEO RESTRICTIONS, but is not printed on the geographic overlay.
	point := t.WindowFromLatLonWrapped(ra.Label.Lat, ra.Label.Lon)
	fs := p.systemFont.Size(fontSize)
	if fs == nil {
		return
	}
	point.X -= starsRBLTextWidth(fs, text) / 2
	point.Y -= float32(fs.LineHeight) / 2
	labelColor := restrictionAreaBlinkColor(textColor,
		(ra.Blink || settings.ForceBlinkingText) && !settings.StopBlinkingText && blinkDim)
	labels.AddText(text, point, renderer.TextStyle{Size: fontSize, Color: labelColor.ToRGBA()})
}
