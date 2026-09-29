package stars

import (
	"fmt"
	stdmath "math"
	"strings"

	"github.com/juliusplatzer/reds/aviation"
	redsmath "github.com/juliusplatzer/reds/math"
	redsnet "github.com/juliusplatzer/reds/net"
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
// While displayed, the high-resolution DCB occupies the top 72 display
// units. When the operator toggles the DCB off with <DCB>, the radar scope
// immediately regains that area, matching VICE's DisplayDCB scope extent.
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
	if w <= 0 || h <= 0 {
		return
	}

	// Scope transformations remain based on the complete pane. Only the top
	// edge available to edge-oriented graphics changes with DCB visibility, as
	// in VICE's drawDCB/scopeExtent handling.
	top := float32(0)
	if ps.DisplayDCB {
		top = dcbButtonSize
	}
	if h <= top {
		return
	}
	scope := redsmath.NewRect(0, top, w, h)
	center := p.currentCenter()
	centerWindow := transforms.WindowFromLatLon(center.Lat, center.Lon)

	fontSize := p.toolsFontSize()
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

// Range bearing line tools ---------------------------------------------------
//
// VICE keeps STARS RBL geometry/state/rendering in stars/tools.go. Keep REDS
// organized the same way rather than maintaining a separate rbl.go.

const starsMaxRangeBearingLines = 9

type starsRangeBearingEndpoint struct {
	// TargetKey makes the endpoint follow the live surveillance track. When it
	// is empty, Location is a stationary geographic endpoint.
	TargetKey string
	Location  configPoint
}

type starsRangeBearingLine struct {
	P [2]starsRangeBearingEndpoint
}

func (p *STARSPane) beginRangeBearingLine(endpoint starsRangeBearingEndpoint) {
	if p == nil {
		return
	}
	p.wipRBL = &starsRangeBearingLine{}
	p.wipRBL.P[0] = endpoint
	// VICE leaves *T in the Preview Area while waiting for endpoint 2.
	p.commandMode = CommandModeNone
	p.commandInput = "*T"
	p.commandResponse = ""
}

func (p *STARSPane) completeRangeBearingLine(endpoint starsRangeBearingEndpoint) {
	if p == nil || p.wipRBL == nil {
		return
	}
	p.wipRBL.P[1] = endpoint
	p.rangeBearingLines = append(p.rangeBearingLines, *p.wipRBL)
	p.wipRBL = nil
}

// starsRBLStationaryPoint resolves *T stationary endpoints through the FAA
// CIFP navigation database, exactly like VICE's av.DB.LookupWaypoint path.
// Navaids take priority over fixes when an identifier exists in both maps.
func starsRBLStationaryPoint(id string) (configPoint, bool) {
	id = strings.ToUpper(strings.TrimSpace(id))
	if id == "" {
		return configPoint{}, false
	}
	db, err := aviation.LoadCIFPDatabase()
	if err != nil {
		return configPoint{}, false
	}
	point, ok := db.LookupWaypoint(id)
	if !ok {
		return configPoint{}, false
	}
	return configPoint{Lat: point.Lat, Lon: point.Lon}, true
}

// rangeBearingTargetByReference implements the *T track-reference rules used
// by VICE: a four-digit beacon code is matched against the assigned code;
// otherwise the field is treated as an ACID. Only associated active tracks are
// eligible for keyboard lookup. Slew-to-track does not need this lookup.
func (p *STARSPane) rangeBearingTargetByReference(ref string) (*redsnet.TaisTarget, error) {
	if p == nil {
		return nil, ErrSTARSNoFlight
	}
	ref = strings.ToUpper(strings.TrimSpace(ref))
	if ref == "" {
		return nil, ErrSTARSCommandFormat
	}

	snapshot := p.targetSnapshot()
	if !snapshot.Ready {
		return nil, ErrSTARSNoFlight
	}

	isBeacon := len(ref) == 4
	if isBeacon {
		for _, r := range ref {
			if r < '0' || r > '7' {
				isBeacon = false
				break
			}
		}
	}

	matches := make([]int, 0, 2)
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if target.FlightPlan == nil || !taisTargetHasPosition(target) {
			continue
		}
		if isBeacon {
			if normalizeBeaconCode(target.FlightPlan.AssignedBeaconCode) == ref {
				matches = append(matches, i)
			}
			continue
		}
		if strings.EqualFold(strings.TrimSpace(target.FlightPlan.ACID), ref) {
			matches = append(matches, i)
		}
	}

	if len(matches) == 0 {
		return nil, ErrSTARSNoFlight
	}
	if len(matches) > 1 {
		if isBeacon {
			return nil, ErrSTARSDuplicateBeacon
		}
		return nil, fmt.Errorf("DUP ID %s", ref)
	}
	return &snapshot.Targets[matches[0]], nil
}

func rangeBearingTargetByKey(snapshot redsnet.TaisSnapshot, key string) *redsnet.TaisTarget {
	if key == "" || !snapshot.Ready {
		return nil
	}
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if targetDisplayStateKey(target) == key && taisTargetHasPosition(target) {
			return target
		}
	}
	return nil
}

func rangeBearingEndpointPoint(snapshot redsnet.TaisSnapshot, endpoint starsRangeBearingEndpoint) (configPoint, *redsnet.TaisTarget, bool) {
	if endpoint.TargetKey != "" {
		target := rangeBearingTargetByKey(snapshot, endpoint.TargetKey)
		if target == nil {
			return configPoint{}, nil, false
		}
		return configPoint{Lat: target.Track.Lat, Lon: target.Track.Lon}, target, true
	}
	return endpoint.Location, nil, true
}

func starsRBLGroundSpeed(target *redsnet.TaisTarget) float64 {
	if target == nil {
		return 0
	}
	return stdmath.Hypot(float64(target.Track.VX), float64(target.Track.VY))
}

func starsRBLNMDistance(a, b configPoint) float64 {
	// Haversine distance, matching VICE's NMDistance2LL implementation.
	const earthRadiusM = 6371000.0
	rad := func(deg float64) float64 { return deg * stdmath.Pi / 180 }
	lat1, lon1 := rad(a.Lat), rad(a.Lon)
	lat2, lon2 := rad(b.Lat), rad(b.Lon)
	dLat, dLon := lat2-lat1, lon2-lon1
	x := stdmath.Sin(dLat/2)*stdmath.Sin(dLat/2) +
		stdmath.Cos(lat1)*stdmath.Cos(lat2)*stdmath.Sin(dLon/2)*stdmath.Sin(dLon/2)
	x = stdmath.Max(0, stdmath.Min(1, x))
	c := 2 * stdmath.Atan2(stdmath.Sqrt(x), stdmath.Sqrt(1-x))
	return earthRadiusM * c * 0.000539957
}

func starsRBLMagneticBearing(a, b configPoint, nmPerLongitude, magneticVariation float64) int {
	east := longitudeDelta(b.Lon, a.Lon) * nmPerLongitude
	north := (b.Lat - a.Lat) * 60
	degrees := stdmath.Atan2(east, north) * 180 / stdmath.Pi
	degrees += magneticVariation
	for degrees < 0 {
		degrees += 360
	}
	for degrees >= 360 {
		degrees -= 360
	}
	return int(degrees + 0.5)
}

func starsRBLLabel(a, b configPoint, groundSpeed, nmPerLongitude, magneticVariation float64, id int) string {
	bearing := starsRBLMagneticBearing(a, b, nmPerLongitude, magneticVariation)
	distance := starsRBLNMDistance(a, b)

	rangeText := "******"
	if distance <= 999.99 {
		rangeText = fmt.Sprintf("%.2f", distance)
	}
	text := fmt.Sprintf("%03d/%s", bearing, rangeText)
	if groundSpeed > 0 {
		minutes := 60 * distance / groundSpeed
		if minutes > 99 {
			text += "/**"
		} else {
			text += fmt.Sprintf("/%d", int(minutes+0.5))
		}
	}
	return fmt.Sprintf("%s-%d", text, id)
}

func starsRBLTextWidth(fs *renderer.BitmapFontSize, text string) float32 {
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

func starsRBLPointInsidePane(p redsmath.Vec2, width, height float32) bool {
	return p.X >= 0 && p.X <= width && p.Y >= 0 && p.Y <= height
}

// starsRBLRayPaneEntry returns the first point where the ray origin+t*dir,
// t >= 0, enters the pane. It is the REDS equivalent of VICE's pane-bounds
// IntersectRay call used to pin an off-screen endpoint-2 label to the edge.
func starsRBLRayPaneEntry(origin, dir redsmath.Vec2, width, height float32) (redsmath.Vec2, bool) {
	const eps = float32(1e-6)
	tEnter := float32(0)
	tExit := float32(1e30)

	clip := func(o, d, lo, hi float32) bool {
		if d > -eps && d < eps {
			return o >= lo && o <= hi
		}
		t0 := (lo - o) / d
		t1 := (hi - o) / d
		if t0 > t1 {
			t0, t1 = t1, t0
		}
		if t0 > tEnter {
			tEnter = t0
		}
		if t1 < tExit {
			tExit = t1
		}
		return tEnter <= tExit
	}

	if !clip(origin.X, dir.X, 0, width) || !clip(origin.Y, dir.Y, 0, height) || tExit < 0 {
		return redsmath.Vec2{}, false
	}
	if tEnter < 0 {
		tEnter = 0
	}
	return origin.Add(dir.Mul(tEnter)), true
}

// drawRangeBearingLines renders all complete RBLs and the in-progress line.
// TI 6191.409 6.7 defines the label as magnetic bearing / range / optional
// traversal time - RBL ID. The label is attached to the end point and clamped
// to the display edge. Track endpoints are resolved every frame, so the line,
// bearing, range and traversal time follow moving aircraft exactly as in VICE.
func (p *STARSPane) drawRangeBearingLines(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.LatLonTransformations,
	snapshot redsnet.TaisSnapshot,
) {
	if p == nil || ctx == nil || zcb == nil || p.systemFont == nil {
		return
	}
	if len(p.rangeBearingLines) == 0 && p.wipRBL == nil {
		return
	}

	ps := p.currentPrefs()
	if ps.Brightness.Lines == 0 {
		return
	}

	fontSize := p.toolsFontSize()
	texture := p.systemFontTexture(ctx.Renderer, fontSize)
	fs := p.systemFont.Size(fontSize)
	if texture == 0 || fs == nil {
		return
	}

	color := ps.Brightness.Lines.ScaleRGB(p.colors.RangeBearingLine)
	lines := renderer.GetColoredLinesBuilder()
	defer renderer.ReturnColoredLinesBuilder(lines)
	text := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(text)
	text.SetFont(p.systemFont)
	style := renderer.TextStyle{Size: fontSize, Color: color.ToRGBA()}

	magneticVariation := 0.0
	center := p.currentCenter()
	if variation, err := radar.MagneticVariationAt(center.Lat, center.Lon); err == nil {
		magneticVariation = variation
	}
	nmPerLongitude := 60 * p.longitudeScaleFactor
	paneW, paneH := ctx.PaneRect.Width(), ctx.PaneRect.Height()

	drawOne := func(a, b configPoint, id int, groundSpeed float64) {
		p0 := transforms.WindowFromLatLon(a.Lat, a.Lon)
		p1 := transforms.WindowFromLatLon(b.Lat, b.Lon)
		lines.AddLineRGB(
			renderer.PointVertex{X: p0.X, Y: p0.Y},
			renderer.PointVertex{X: p1.X, Y: p1.Y},
			color,
		)

		label := " " + starsRBLLabel(a, b, groundSpeed, nmPerLongitude, magneticVariation, id)
		labelWidth := starsRBLTextWidth(fs, label)
		lineHeight := float32(fs.LineHeight)

		// VICE places the RBL label at endpoint 2 and lets the subsequently
		// rendered target/leader/datablock presentation remain visually on top.
		// TI 6191.409 6.7 / Figure 6-6 specifies the endpoint placement (and
		// screen-edge retention) but no data-block collision avoidance, so overlap
		// with some leader/data-block geometries is possible and intentional here.
		pos := redsmath.Vec2{X: p1.X, Y: p1.Y}
		offsetRight := pos.X > paneW-labelWidth
		startAboveEnd := p0.Y < p1.Y

		// TI 6191.409 6.7: if endpoint 2 goes off-screen, retain the label at
		// the screen edge so the RBL data remains visible. Match VICE by
		// intersecting the ray from endpoint 2 back toward endpoint 1.
		if !starsRBLPointInsidePane(pos, paneW, paneH) {
			if edge, ok := starsRBLRayPaneEntry(pos, p0.Sub(pos), paneW, paneH); ok {
				pos = edge
			}
		}

		// VICE only introduces a vertical offset when the text has to be
		// shifted left from the right edge; this keeps the label from lying on
		// top of the RBL itself. REDS uses upper-left text origins, so express
		// the same visual offset in that coordinate convention.
		if offsetRight {
			if startAboveEnd {
				pos.Y += 4
			} else {
				pos.Y -= lineHeight + 4
			}
		}

		pos.X = min(max(float32(0), pos.X), max(float32(0), paneW-labelWidth))
		pos.Y = min(max(float32(0), pos.Y), max(float32(0), paneH-lineHeight))
		text.AddText(label, pos, style)
	}

	// Remove completed RBLs whose attached track no longer exists, matching
	// VICE's stale-RBL filtering. Track-track lines have no traversal time;
	// exactly one track endpoint contributes its current groundspeed.
	kept := p.rangeBearingLines[:0]
	for i := range p.rangeBearingLines {
		rbl := p.rangeBearingLines[i]
		a, trackA, okA := rangeBearingEndpointPoint(snapshot, rbl.P[0])
		b, trackB, okB := rangeBearingEndpointPoint(snapshot, rbl.P[1])
		if !okA || !okB {
			continue
		}
		kept = append(kept, rbl)
		groundSpeed := 0.0
		if trackA != nil && trackB == nil {
			groundSpeed = starsRBLGroundSpeed(trackA)
		} else if trackB != nil && trackA == nil {
			groundSpeed = starsRBLGroundSpeed(trackB)
		}
		drawOne(a, b, len(kept), groundSpeed)
	}
	p.rangeBearingLines = kept

	// While endpoint 2 is pending, VICE draws a live RBL to the scope cursor.
	if p.wipRBL != nil && ctx.Mouse != nil {
		a, trackA, ok := rangeBearingEndpointPoint(snapshot, p.wipRBL.P[0])
		if !ok {
			p.wipRBL = nil
			if strings.EqualFold(p.commandInput, "*T") {
				p.resetCommand()
			}
		} else {
			lat, lon := transforms.LatLonFromWindow(ctx.Mouse.Pos)
			b := configPoint{Lat: lat, Lon: normalizeLongitude(lon)}
			drawOne(a, b, len(p.rangeBearingLines)+1, starsRBLGroundSpeed(trackA))
		}
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zRangeBearingLine)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.LineWidth(max(float32(1), ctx.DPIScale))
	lines.GenerateCommands(cb)
	text.GenerateCommands(cb, texture)
	cb.DisableScissor()
}
