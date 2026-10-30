package stars

import (
	"encoding/json"
	"fmt"
	stdmath "math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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

// Minimum separation prediction and rendering (VICE: stars/tools.go).
// minSepApproach is a two-dimensional constant-ground-velocity prediction.
// Altitudes are deliberately not considered: TI 6191.409 Rev. 30, Fig. 6-1
// defines MIN as horizontal separation irrespective of vertical separation.
type minSepApproach struct {
	PointA, PointB configPoint
	DistanceNM     float64
	Converging     bool
	Valid          bool
}

// minSepClosestApproach computes CPA in the local east/north tangent plane.
// TAIS VX/VY are ground-velocity components in knots (east/north). Using one
// shared longitude scale for both aircraft preserves relative motion. Unlike
// CRC's five-second iterative sampling, the analytic minimum is not quantized
// to a polling interval; positions are nevertheless updated every render frame.
func minSepClosestApproach(a, b redsnet.TaisTrack) minSepApproach {
	latMean := (a.Lat + b.Lat) / 2
	lonScale := stdmath.Cos(latMean * stdmath.Pi / 180)
	if lonScale <= 1e-6 {
		return minSepApproach{}
	}

	deltaEast := longitudeDelta(b.Lon, a.Lon) * 60 * lonScale
	deltaNorth := (b.Lat - a.Lat) * 60
	relEast := float64(b.VX - a.VX)
	relNorth := float64(b.VY - a.VY)
	relativeSpeed2 := relEast*relEast + relNorth*relNorth

	// A positive time means the current distance is decreasing. If time is
	// nonpositive (or both ground velocities are equal), STARS draws the
	// current separation with NO XING rather than extrapolating backwards.
	hours := 0.0
	if relativeSpeed2 > 0 {
		hours = -(deltaEast*relEast + deltaNorth*relNorth) / relativeSpeed2
	}
	if hours <= 0 || stdmath.IsNaN(hours) || stdmath.IsInf(hours, 0) {
		return minSepApproach{
			PointA:     configPoint{Lat: a.Lat, Lon: a.Lon},
			PointB:     configPoint{Lat: b.Lat, Lon: b.Lon},
			DistanceNM: stdmath.Hypot(deltaEast, deltaNorth),
			Valid:      true,
		}
	}

	pA := configPoint{
		Lat: a.Lat + float64(a.VY)*hours/60,
		Lon: normalizeLongitude(a.Lon + float64(a.VX)*hours/(60*lonScale)),
	}
	pB := configPoint{
		Lat: b.Lat + float64(b.VY)*hours/60,
		Lon: normalizeLongitude(b.Lon + float64(b.VX)*hours/(60*lonScale)),
	}
	return minSepApproach{
		PointA:     pA,
		PointB:     pB,
		DistanceNM: stdmath.Hypot(deltaEast+relEast*hours, deltaNorth+relNorth*hours),
		Converging: true,
		Valid:      true,
	}
}

// drawMinSep is the graphic defined in TI 6191.409 Rev. 30, 6.4 and Fig. 6-1.
// Its base color, tools font, and LIN brightness follow CRC and VICE. The
// operator manual places "NO XING" BELOW the distance; both simulators place
// it above, so here the manual takes precedence.
func (p *STARSPane) drawMinSep(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.LatLonTransformations,
	snapshot redsnet.TaisSnapshot,
) {
	if p == nil || !snapshot.Ready {
		return
	}
	// Track deletion can happen between frames. Prune here, using the exact
	// snapshot used for the graphic, without coupling MIN to Draw ordering.
	p.pruneMinSepTracks(snapshot)
	if ctx == nil || zcb == nil || p.systemFont == nil ||
		p.minSepTracks[0] == "" || p.minSepTracks[1] == "" || !snapshot.Ready {
		return
	}
	ps := p.currentPrefs()
	if ps.Brightness.Lines == 0 {
		return
	}
	a := rangeBearingTargetByKey(snapshot, p.minSepTracks[0])
	b := rangeBearingTargetByKey(snapshot, p.minSepTracks[1])
	if a == nil || b == nil {
		return
	}

	approach := minSepClosestApproach(a.Track, b.Track)
	// A degenerate polar geometry is not representable in the pane's local
	// tangent-plane projection. The US terminal facilities never reach it.
	if !approach.Valid {
		return
	}

	fontSize := p.toolsFontSize()
	texture := p.systemFontTexture(ctx.Renderer, fontSize)
	fs := p.systemFont.Size(fontSize)
	if texture == 0 || fs == nil {
		return
	}
	color := ps.Brightness.Lines.ScaleRGB(p.colors.PTL)
	startA := transforms.WindowFromLatLon(a.Track.Lat, a.Track.Lon)
	startB := transforms.WindowFromLatLon(b.Track.Lat, b.Track.Lon)
	endA := transforms.WindowFromLatLon(approach.PointA.Lat, approach.PointA.Lon)
	endB := transforms.WindowFromLatLon(approach.PointB.Lat, approach.PointB.Lon)
	point := func(v redsmath.Vec2) renderer.PointVertex { return renderer.PointVertex{X: v.X, Y: v.Y} }

	segments := renderer.GetColoredLinesBuilder()
	defer renderer.ReturnColoredLinesBuilder(segments)
	markers := renderer.GetColoredTrianglesBuilder()
	defer renderer.ReturnColoredTrianglesBuilder(markers)
	backing := renderer.GetColoredTrianglesBuilder()
	defer renderer.ReturnColoredTrianglesBuilder(backing)
	border := renderer.GetColoredLinesBuilder()
	defer renderer.ReturnColoredLinesBuilder(border)
	label := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(label)
	label.SetFont(p.systemFont)

	if approach.Converging {
		segments.AddLineRGB(point(startA), point(endA), color)
		segments.AddLineRGB(point(endA), point(endB), color)
		segments.AddLineRGB(point(endB), point(startB), color)
		// STARS filled triangle glyph, represented geometrically because the
		// CRC bitmap font glyph is not guaranteed in every REDS font atlas.
		for _, at := range []redsmath.Vec2{endA, endB} {
			markers.AddTriangleRGB(
				renderer.PointVertex{X: at.X, Y: at.Y - 6},
				renderer.PointVertex{X: at.X - 5, Y: at.Y + 4},
				renderer.PointVertex{X: at.X + 5, Y: at.Y + 4}, color)
		}
	} else {
		segments.AddLineRGB(point(startA), point(startB), color)
	}

	separationLabel := fmt.Sprintf("%.2f NM", approach.DistanceNM)
	lines := []string{separationLabel}
	if !approach.Converging {
		lines = append(lines, "NO XING")
	}
	widest := float32(0)
	for _, line := range lines {
		widest = max(widest, starsRBLTextWidth(fs, line))
	}
	lineHeight := float32(fs.LineHeight)
	width := widest + 8
	height := lineHeight*float32(len(lines)) + 8
	midpoint := endA.Add(endB).Mul(0.5)
	left, top := midpoint.X-width/2, midpoint.Y-height/2
	right, bottom := left+width, top+height

	backing.AddQuad(renderer.PointVertex{X: left, Y: top},
		renderer.PointVertex{X: right, Y: top},
		renderer.PointVertex{X: right, Y: bottom},
		renderer.PointVertex{X: left, Y: bottom}, p.colors.Background)
	corners := []renderer.PointVertex{
		{X: left, Y: top}, {X: right, Y: top},
		{X: right, Y: bottom}, {X: left, Y: bottom},
	}
	for i := 0; i < len(corners); i++ {
		border.AddLineRGB(corners[i], corners[(i+1)%len(corners)], color)
	}
	for i, line := range lines {
		label.AddText(line, redsmath.Vec2{
			X: midpoint.X - starsRBLTextWidth(fs, line)/2,
			Y: top + 4 + float32(i)*lineHeight,
		}, renderer.TextStyle{Size: fontSize, Color: color.ToRGBA()})
	}

	x, y, w, h := ctx.PaneFramebufferRect()
	cb := zcb.At(zMinimumSeparation)
	cb.Viewport(x, y, w, h)
	cb.Scissor(x, y, w, h)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.LineWidth(max(float32(1), ctx.DPIScale))
	segments.GenerateCommands(cb)
	markers.GenerateCommands(cb)
	backing.GenerateCommands(cb)
	border.GenerateCommands(cb)
	label.GenerateCommands(cb, texture)
	cb.DisableScissor()
}

func formatTPADistance(v float32) string {
	if v == float32(int(v)) {
		return strconv.Itoa(int(v))
	}
	return fmt.Sprintf("%.1f", v)
}

func (p *STARSPane) tpaSizeVisible(state tpaTrackState) bool {
	if state.DisplaySize != nil {
		return *state.DisplaySize
	}
	return p.currentPrefs().DisplayTPASize
}

func (p *STARSPane) atpaInTrailVisible(state tpaTrackState) bool {
	if state.DisplayATPAInTrail != nil {
		return *state.DisplayATPAInTrail
	}
	return p.currentPrefs().DisplayATPAInTrailDist
}

func (p *STARSPane) atpaMonitorVisible(state tpaTrackState) bool {
	if state.DisplayATPAMonitor != nil {
		return *state.DisplayATPAMonitor
	}
	return p.currentPrefs().DisplayATPAMonitorCones
}

func (p *STARSPane) atpaWarnAlertVisible(state tpaTrackState) bool {
	if !p.currentPrefs().DisplayATPAWarningAlertCones {
		return false
	}
	return state.DisplayATPAWarnAlert == nil || *state.DisplayATPAWarnAlert
}

func (p *STARSPane) drawTPA(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.LatLonTransformations,
	snapshot redsnet.TaisSnapshot,
) {
	if p == nil || ctx == nil || zcb == nil || !snapshot.Ready ||
		(len(p.tpaTracks) == 0 && len(p.atpaTracks) == 0) {
		return
	}
	ps := p.currentPrefs()
	if ps.Brightness.Lines == 0 {
		return
	}

	shortSide := ctx.PaneRect.Width()
	if h := ctx.PaneRect.Height(); h < shortSide {
		shortSide = h
	}
	if shortSide <= 0 || ps.Range <= 0 {
		return
	}
	pixelsPerNM := shortSide / (2 * ps.Range)
	if pixelsPerNM <= 0 {
		return
	}

	baseLines := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(baseLines)
	warningLines := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(warningLines)
	alertLines := renderer.GetLinesBuilder()
	defer renderer.ReturnLinesBuilder(alertLines)
	text := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(text)
	text.SetFont(p.systemFont)

	fontSize := p.datablockFontSize()
	fontTexture := p.systemFontTexture(ctx.Renderer, fontSize)
	baseColor := ps.Brightness.Lines.ScaleRGB(p.colors.TerminalProximityAlert)
	warningColor := ps.Brightness.Lines.ScaleRGB(p.colors.ATPAWarning)
	alertColor := ps.Brightness.Lines.ScaleRGB(p.colors.ATPAAlert)

	// ATPA cone orientation is toward the leading track, so make the current
	// snapshot addressable by the same stable key used by atpaTrackState.
	targetsByKey := make(map[string]*redsnet.TaisTarget, len(snapshot.Targets))
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if key := targetDisplayStateKey(target); key != "" {
			targetsByKey[key] = target
		}
	}

	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		key := targetDisplayStateKey(target)
		state := p.tpaState(key)
		atpa, hasATPA := p.atpaTrackState(key)
		if (!state.hasGraphic() && !hasATPA) || !taisTargetHasPosition(target) {
			continue
		}

		maxGraphicNM := max(state.JRingRadius, state.ConeLength)
		if hasATPA {
			maxGraphicNM = max(maxGraphicNM, atpa.MinimumSeparation)
		}
		center := transforms.WindowFromLatLon(target.Track.Lat, target.Track.Lon)
		if !targetCenterNearPane(ctx, center, max(float32(20), maxGraphicNM*pixelsPerNM+20)) {
			continue
		}

		// A J-Ring is independent of ATPA cones; Warning/Alert modalities only
		// supersede TPA Cones, not J-Rings (Figure 6-25 / section 6.21).
		if state.JRingRadius > 0 {
			radius := state.JRingRadius * pixelsPerNM
			baseLines.AddCircle(renderer.PointVertex{X: center.X, Y: center.Y}, radius, 360)
			if p.tpaSizeVisible(state) && fontTexture != 0 {
				leader := leaderLineUnitVector(p.targetLeaderLineDirection(target))
				pos := redsmath.Vec2{
					X: center.X - leader.X*radius,
					Y: center.Y - leader.Y*radius,
				}
				label := formatTPADistance(state.JRingRadius)
				pos.X -= float32(len(label)*fontSize) * 0.25
				pos.Y -= float32(fontSize) * 0.5
				text.AddText(label, pos, renderer.TextStyle{Size: fontSize, Color: baseColor.ToRGBA()})
			}
		}

		var lead *redsnet.TaisTarget
		var volume *atpaVolumeConfig
		if hasATPA && atpa.LeadKey != "" && atpa.MinimumSeparation > 0 {
			lead = targetsByKey[atpa.LeadKey]
			volume = p.atpaVolumeForState(atpa)
		}

		warnAlertTrackEnabled := p.atpaWarnAlertVisible(state)
		monitorTrackEnabled := p.atpaMonitorVisible(state)
		inTrailTrackEnabled := p.atpaInTrailVisible(state)
		perTrackWarnAlertInhibited := ps.DisplayATPAWarningAlertCones &&
			state.DisplayATPAWarnAlert != nil && !*state.DisplayATPAWarnAlert

		drawATPAWarning := false
		drawATPAAlert := false
		drawATPAMonitor := false
		if lead != nil && taisTargetHasPosition(lead) && volume != nil {
			// 6.21.12 allows a controller to enable Warning/Alert Cones for an
			// individual qualifying FDB even when this TCP is not one of the
			// volume's normally adapted Warning/Alert display positions.
			warningAlertAllowed := p.atpaWarningAlertConeAllowed(target, volume) ||
				(state.DisplayATPAWarnAlert != nil && *state.DisplayATPAWarnAlert)
			monitorAllowed := p.atpaMonitorConeAllowed(target, volume)

			switch atpa.Status {
			case atpaStatusWarning:
				drawATPAWarning = warnAlertTrackEnabled && warningAlertAllowed
			case atpaStatusAlert:
				drawATPAAlert = warnAlertTrackEnabled && warningAlertAllowed
			case atpaStatusMonitor:
				drawATPAMonitor = monitorTrackEnabled && monitorAllowed
			}

			// Sections 6.21.12/13/16/17: if a Warning/Alert Cone is inhibited,
			// a Monitor Cone is shown instead only when Monitor Cones are enabled
			// and the actual in-trail distance is inhibited for the track.
			if (atpa.Status == atpaStatusWarning || atpa.Status == atpaStatusAlert) &&
				!drawATPAWarning && !drawATPAAlert && !inTrailTrackEnabled &&
				monitorTrackEnabled && monitorAllowed {
				drawATPAMonitor = true
			}
		}

		drawATPACone := drawATPAMonitor || drawATPAWarning || drawATPAAlert

		// TI 6191.409 6-160: a manual TPA cone in an ATPA pair is enlarged to
		// at least the allowable minimum. If an ATPA cone is actually displayed,
		// its own length is exactly the allowable minimum and it supersedes the
		// manual TPA cone. A Warning/Alert condition with colored in-trail data
		// also inhibits the manual cone even when Warning/Alert Cones are hidden.
		manualConeLength := state.ConeLength
		if hasATPA && atpa.MinimumSeparation > 0 && manualConeLength > 0 {
			manualConeLength = max(manualConeLength, atpa.MinimumSeparation)
		}
		inWarningAlert := atpa.Status == atpaStatusWarning || atpa.Status == atpaStatusAlert
		actualInTrailDisplayed := inTrailTrackEnabled && !perTrackWarnAlertInhibited
		if drawATPAWarning || drawATPAAlert {
			actualInTrailDisplayed = true // Warning/Alert Cones force the distance on.
		}
		drawManualCone := manualConeLength > 0 && !drawATPACone && !(inWarningAlert && actualInTrailDisplayed)
		if !drawATPACone && !drawManualCone {
			continue
		}

		coneLength := manualConeLength
		coneColor := baseColor
		coneLines := baseLines
		var end redsmath.Vec2
		if drawATPACone {
			coneLength = atpa.MinimumSeparation
			leadPos := transforms.WindowFromLatLon(lead.Track.Lat, lead.Track.Lon)
			dx, dy := leadPos.X-center.X, leadPos.Y-center.Y
			norm := float32(stdmath.Hypot(float64(dx), float64(dy)))
			if norm <= 0 {
				continue
			}
			scale := coneLength * pixelsPerNM / norm
			end = redsmath.Vec2{X: center.X + dx*scale, Y: center.Y + dy*scale}

			if drawATPAWarning {
				coneColor = warningColor
				coneLines = warningLines
			} else if drawATPAAlert {
				coneColor = alertColor
				coneLines = alertLines
			}
		} else {
			// A manual TPA Cone follows current velocity.
			vx, vy := float64(target.Track.VX), float64(target.Track.VY)
			speed := stdmath.Hypot(vx, vy)
			if speed == 0 {
				continue
			}
			east := vx / speed
			north := vy / speed
			lonScale := radar.LongitudeScaleFactorForLat(target.Track.Lat)
			if lonScale == 0 {
				continue
			}
			endLat := target.Track.Lat + north*float64(coneLength)/60
			endLon := normalizeLongitude(target.Track.Lon + east*float64(coneLength)/(60*lonScale))
			end = transforms.WindowFromLatLon(endLat, endLon)
		}

		dx, dy := end.X-center.X, end.Y-center.Y
		norm := float32(stdmath.Hypot(float64(dx), float64(dy)))
		if norm <= 0 {
			continue
		}
		px, py := -dy/norm*5, dx/norm*5
		apex := renderer.PointVertex{X: center.X, Y: center.Y}
		baseLeft := renderer.PointVertex{X: end.X + px, Y: end.Y + py}
		baseRight := renderer.PointVertex{X: end.X - px, Y: end.Y - py}

		clippedForMileage := false
		if p.tpaSizeVisible(state) && fontTexture != 0 {
			mid := redsmath.Vec2{X: center.X + dx*0.5, Y: center.Y + dy*0.5}
			label := formatTPADistance(coneLength)
			if minInk, maxInk, ok := tpaTextInkBounds(p.systemFont, fontSize, label); ok {
				inkCenter := redsmath.Vec2{
					X: (minInk.X + maxInk.X) * 0.5,
					Y: (minInk.Y + maxInk.Y) * 0.5,
				}
				textPos := redsmath.Vec2{X: mid.X - inkCenter.X, Y: mid.Y - inkCenter.Y}

				const pad = float32(2)
				exclusion := redsmath.NewRect(
					textPos.X+minInk.X-pad,
					textPos.Y+minInk.Y-pad,
					textPos.X+maxInk.X+pad,
					textPos.Y+maxInk.Y+pad,
				)
				addTPALineOutsideRect(coneLines, apex, baseLeft, exclusion)
				addTPALineOutsideRect(coneLines, apex, baseRight, exclusion)
				text.AddText(label, textPos, renderer.TextStyle{Size: fontSize, Color: coneColor.ToRGBA()})
				clippedForMileage = true
			}
		}

		if !clippedForMileage {
			coneLines.AddLine(apex, baseLeft)
			coneLines.AddLine(apex, baseRight)
		}
		coneLines.AddLine(baseLeft, baseRight)
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zTargetTPA)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.LineWidth(max(float32(1), ctx.DPIScale))
	cb.SetRGB(baseColor)
	baseLines.GenerateCommands(cb)
	cb.SetRGB(warningColor)
	warningLines.GenerateCommands(cb)
	cb.SetRGB(alertColor)
	alertLines.GenerateCommands(cb)
	if fontTexture != 0 {
		text.GenerateCommands(cb, fontTexture)
	}
	cb.DisableScissor()
}

// addTPALineOutsideRect adds the portions of segment a-b that lie outside
// rect. The rectangle is only a drawing exclusion zone for a TPA cone side;
// nothing is filled, so lower-z scope content remains visible through the gap.
func addTPALineOutsideRect(lines *renderer.LinesBuilder, a, b renderer.PointVertex, rect redsmath.Rect) {
	if lines == nil {
		return
	}
	tEnter, tExit, intersects := tpaSegmentRectInterval(a, b, rect)
	if !intersects {
		lines.AddLine(a, b)
		return
	}

	if tEnter > 0 {
		lines.AddLine(a, tpaLerpPoint(a, b, tEnter))
	}
	if tExit < 1 {
		lines.AddLine(tpaLerpPoint(a, b, tExit), b)
	}
}

// tpaSegmentRectInterval returns the parametric interval of a-b lying inside
// rect. Liang-Barsky clipping keeps this allocation-free since drawTPA may run
// for many tracks every frame.
func tpaSegmentRectInterval(a, b renderer.PointVertex, rect redsmath.Rect) (float32, float32, bool) {
	dx, dy := b.X-a.X, b.Y-a.Y
	tEnter, tExit := float32(0), float32(1)

	clip := func(p, q float32) bool {
		if p == 0 {
			return q >= 0
		}
		r := q / p
		if p < 0 {
			if r > tExit {
				return false
			}
			if r > tEnter {
				tEnter = r
			}
		} else {
			if r < tEnter {
				return false
			}
			if r < tExit {
				tExit = r
			}
		}
		return true
	}

	if !clip(-dx, a.X-rect.Min.X) ||
		!clip(dx, rect.Max.X-a.X) ||
		!clip(-dy, a.Y-rect.Min.Y) ||
		!clip(dy, rect.Max.Y-a.Y) {
		return 0, 0, false
	}
	return tEnter, tExit, true
}

func tpaLerpPoint(a, b renderer.PointVertex, t float32) renderer.PointVertex {
	return renderer.PointVertex{
		X: a.X + (b.X-a.X)*t,
		Y: a.Y + (b.Y-a.Y)*t,
	}
}

// tpaTextInkBounds returns the rasterized glyph bounds relative to the origin
// accepted by TextDrawBuilder.AddText. Keeping this local to TPA avoids using
// layout-cell dimensions for the cone cutout; the exclusion should follow the
// actual glyph ink rather than character advances.
func tpaTextInkBounds(font *renderer.BitmapFont, size int, label string) (redsmath.Vec2, redsmath.Vec2, bool) {
	if font == nil || label == "" {
		return redsmath.Vec2{}, redsmath.Vec2{}, false
	}
	fs := font.Size(size)
	if fs == nil {
		return redsmath.Vec2{}, redsmath.Vec2{}, false
	}

	minInk := redsmath.Vec2{X: float32(stdmath.MaxFloat32), Y: float32(stdmath.MaxFloat32)}
	maxInk := redsmath.Vec2{X: -float32(stdmath.MaxFloat32), Y: -float32(stdmath.MaxFloat32)}
	penX := float32(0)
	found := false
	runes := []rune(label)
	for i, r := range runes {
		glyph, ok := fs.Glyph(r)
		if !ok {
			continue
		}

		x0 := penX + float32(glyph.BearingX)
		y0 := float32(fs.LineHeight - glyph.BearingY)
		x1 := x0 + float32(glyph.Width)
		y1 := y0 + float32(glyph.Height)
		minInk.X = min(minInk.X, x0)
		minInk.Y = min(minInk.Y, y0)
		maxInk.X = max(maxInk.X, x1)
		maxInk.Y = max(maxInk.Y, y1)
		found = true

		if i == len(runes)-1 {
			penX += float32(glyph.Width)
		} else {
			penX += float32(glyph.Advance)
		}
	}
	return minInk, maxInk, found
}

// Restriction-area geometry (§6.6) stays in tools.go, like VICE's STARS tools.
// It is kept independent of command parsing so a future read-only TFR importer
// can pass arbitrary-length geographic rings to the same drawing primitives.
type starsRestrictionKind uint8

const (
	starsRestrictionText starsRestrictionKind = iota
	starsRestrictionCircle
	starsRestrictionClosed
	starsRestrictionOpen
)

type starsRestrictionArea struct {
	ID            int
	Kind          starsRestrictionKind
	Text          [2]string
	Label, Center configPoint
	RadiusNM      float64
	Vertices      []configPoint
	Color         int
	Blink, Shaded bool
	LabelPending  bool              // in-progress polygon, awaiting label location
	IsFAA         bool              // read-only imported FAA TFR; not a controller-defined area
	TFRARTCC      string            // FAA-assigned ARTCC for list filtering
	NOTAM         string            // source identity; features may share GeoJSON IDs
	TFRParts      int               // number of polygon features sharing this NOTAM
	TFRPolygons   []starsTFRPolygon // closed exterior rings plus holes, read-only
	Serial        uint64            // distinguishes a deleted/reused site ID for local settings
}

type starsRestrictionAreaStore struct {
	mu         sync.RWMutex
	areas      map[int]starsRestrictionArea
	tfrSlots   map[string]int // stable NOTAM -> system restriction ID
	tfrAsOf    time.Time      // prevent out-of-order pane snapshots
	nextSerial uint64
}

var starsRestrictionStores sync.Map // same-site panes in this client process

func starsRestrictionStoreForSite(site string) *starsRestrictionAreaStore {
	if site == "" {
		site = "unknown"
	}
	v, _ := starsRestrictionStores.LoadOrStore(site, &starsRestrictionAreaStore{areas: make(map[int]starsRestrictionArea)})
	return v.(*starsRestrictionAreaStore)
}
func (s *starsRestrictionAreaStore) create(area starsRestrictionArea) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// §6.6 site-created IDs 1..100; reserved 101..200 belong to adaptation.
	for id := 1; id <= 100; id++ {
		if _, used := s.areas[id]; !used {
			s.nextSerial++
			area.Serial = s.nextSerial
			area.ID = id
			area.Vertices = append([]configPoint(nil), area.Vertices...)
			area.LabelPending = false
			s.areas[id] = area
			return nil
		}
	}
	return ErrSTARSCapacity
}
func (s *starsRestrictionAreaStore) snapshot() []starsRestrictionArea {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]starsRestrictionArea, 0, len(s.areas))
	for id := 1; id <= 200; id++ {
		if area, ok := s.areas[id]; ok {
			result = append(result, area)
		}
	}
	return result
}

// All modifications to controller-defined geometry are shared by the same
// facility's displays, while per-display visibility lives in Preferences.
// Copy slices before mutation, so snapshots and failed edits remain immutable.
func (s *starsRestrictionAreaStore) get(id int) (starsRestrictionArea, error) {
	if id < 1 || id > 200 {
		return starsRestrictionArea{}, ErrSTARSIllegalGeoID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	area, ok := s.areas[id]
	if !ok {
		return starsRestrictionArea{}, ErrSTARSIllegalGeoID
	}
	area.Vertices = append([]configPoint(nil), area.Vertices...)
	return area, nil
}
func (s *starsRestrictionAreaStore) update(id int, fn func(*starsRestrictionArea) error) error {
	if id < 1 || id > 100 {
		return ErrSTARSIllegalGeoID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	area, ok := s.areas[id]
	if !ok {
		return ErrSTARSIllegalGeoID
	}
	area.Vertices = append([]configPoint(nil), area.Vertices...)
	if err := fn(&area); err != nil {
		return err
	}
	s.areas[id] = area
	return nil
}
func (s *starsRestrictionAreaStore) delete(id int) error {
	if id < 1 || id > 100 {
		return ErrSTARSIllegalGeoID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.areas[id]; !ok {
		return ErrSTARSIllegalGeoID
	}
	delete(s.areas, id)
	return nil
}

// §6.6.5: move the text/ID to the designated point and translate *all*
// graphics by the same offset. Circle radius, polygon shape and label-to-
// geometry offsets remain unchanged. Longitude uses the shortest displacement
// through the antimeridian; invalid resulting geographic points are rejected.
func moveRestrictionArea(area *starsRestrictionArea, destination configPoint) error {
	if !validRestrictionPoint(destination) {
		return fmt.Errorf("ILL GEO LOC")
	}
	dlat := destination.Lat - area.Label.Lat
	dlon := normalizeLongitude(destination.Lon - area.Label.Lon)
	relocate := func(pt configPoint) (configPoint, error) {
		result := configPoint{Lat: pt.Lat + dlat, Lon: normalizeLongitude(pt.Lon + dlon)}
		if !validRestrictionPoint(result) {
			return configPoint{}, fmt.Errorf("ILL GEO LOC")
		}
		return result, nil
	}
	shifted := append([]configPoint(nil), area.Vertices...)
	for i, pt := range shifted {
		var err error
		if shifted[i], err = relocate(pt); err != nil {
			return err
		}
	}
	center, err := relocate(area.Center)
	if err != nil && area.Kind == starsRestrictionCircle {
		return err
	}
	area.Vertices = shifted
	if area.Kind == starsRestrictionCircle {
		area.Center = center
	}
	area.Label = destination
	return nil
}
func validRestrictionPoint(p configPoint) bool {
	return !stdmath.IsNaN(p.Lat) && !stdmath.IsNaN(p.Lon) &&
		p.Lat >= -90 && p.Lat <= 90 && p.Lon >= -180 && p.Lon <= 180
}

// restrictionAreaGeometryColor resolves the operator's 1-based restriction
// color number (received here as a zero-based index). The REDS-only override
// changes both boundary and stippled fill for color 1, but never the yellow
// text color or any other restriction color.
func (p *STARSPane) restrictionAreaGeometryColor(index int) renderer.RGB {
	if index < 0 || index >= len(p.colors.RestrictionAreaGeom) {
		index = 0
	}
	if index == 0 && p.useYellowRestrictionColor1 {
		return starsYellow
	}
	return p.colors.RestrictionAreaGeom[index]
}

func (p *STARSPane) drawRestrictionAreas(ctx *panes.Context, zcb *renderer.ZCmdBuffer, t radar.LatLonTransformations) {
	if p == nil || p.systemFont == nil {
		return
	}
	if p.restrictionAreas == nil {
		p.restrictionAreas = starsRestrictionStoreForSite(p.config.Facility.Facility)
	}
	areas := p.restrictionAreas.snapshot()
	if p.wipRestrictionArea != nil {
		areas = append(areas, *p.wipRestrictionArea)
	}
	if len(areas) == 0 {
		return
	}
	ps := p.currentPrefs()
	if ps.Brightness.VideoGroupB == 0 {
		return
	}
	fontSize := p.toolsFontSize()
	texture := p.systemFontTexture(ctx.Renderer, fontSize)
	fs := p.systemFont.Size(fontSize)
	x, y, w, h := ctx.PaneFramebufferRect()
	cb := zcb.At(zRangeBearingLine - 1)
	cb.Viewport(x, y, w, h)
	cb.Scissor(x, y, w, h)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.LineWidth(max(ctx.DPIScale, 1))
	labels := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(labels)
	labels.SetFont(p.systemFont)
	// VICE STARS uses a 32x32 OpenGL polygon-stipple pattern for shaded
	// restriction areas, with a separate doubled-pixel mask on high-DPI displays.
	shadeMode := renderer.DrawStippleRestrictionArea
	if ctx.DPIScale > 1.5 {
		shadeMode = renderer.DrawStippleRestrictionAreaHighDPI
	}
	// Match VICE: 500 ms normal/50%-brightness text phases; no off phase.
	blinkDim := restrictionAreaBlinkDim(time.Now().UnixMilli())
	textColor := ps.Brightness.VideoGroupB.ScaleRGB(p.colors.RestrictionAreaText)
	for _, ra := range areas {
		local := restrictionAreaSettingsFor(ps, ra)
		if ra.ID != 0 && local.Hidden {
			continue
		}
		if ra.IsFAA {
			p.drawFAARestrictionArea(ctx, cb, t, ra, local, shadeMode, labels, fontSize, textColor, blinkDim)
			continue
		}
		color := ps.Brightness.VideoGroupB.ScaleRGB(p.restrictionAreaGeometryColor(ra.Color - 1))
		outline := renderer.GetLinesBuilder()
		vertices := make([]renderer.PointVertex, 0, len(ra.Vertices))
		for _, pt := range ra.Vertices {
			wp := t.WindowFromLatLon(pt.Lat, pt.Lon)
			vertices = append(vertices, renderer.PointVertex{X: wp.X, Y: wp.Y})
		}
		if ra.Kind == starsRestrictionCircle {
			center := t.WindowFromLatLon(ra.Center.Lat, ra.Center.Lon)
			scale := ctx.PaneRect.Width()
			if ctx.PaneRect.Height() < scale {
				scale = ctx.PaneRect.Height()
			}
			radius := float32(ra.RadiusNM) * scale / (2 * ps.Range)
			outline.AddCircle(renderer.PointVertex{X: center.X, Y: center.Y}, radius, 96)
			if ra.Shaded {
				shaded := renderer.GetTrianglesBuilder()
				shaded.AddCircle(renderer.PointVertex{X: center.X, Y: center.Y}, radius, 96)
				cb.SetRGB(color)
				shaded.GenerateCommands(cb, shadeMode, 0)
				renderer.ReturnTrianglesBuilder(shaded)
			}
		} else if ra.Kind == starsRestrictionClosed {
			if len(vertices) >= 3 {
				if ra.Shaded {
					shaded := renderer.GetTrianglesBuilder()
					for _, tri := range restrictionAreaTriangulate(vertices) {
						shaded.AddTriangle(tri[0], tri[1], tri[2])
					}
					cb.SetRGB(color)
					shaded.GenerateCommands(cb, shadeMode, 0)
					renderer.ReturnTrianglesBuilder(shaded)
				}
				outline.AddLineLoop(vertices)
			} else {
				outline.AddLineStrip(vertices)
			}
		} else if ra.Kind == starsRestrictionOpen {
			outline.AddLineStrip(vertices)
		}
		// Preview the next segment, matching VICE's in-progress polygon.
		if ra.ID == 0 && (ra.Kind == starsRestrictionClosed || ra.Kind == starsRestrictionOpen) && len(vertices) > 0 &&
			!ra.LabelPending && ctx.Mouse != nil && p.commandInput == "" {
			mouse := ctx.Mouse.Pos
			last := vertices[len(vertices)-1]
			outline.AddLine(last, renderer.PointVertex{X: mouse.X, Y: mouse.Y})
		}
		cb.SetRGB(color)
		outline.GenerateCommands(cb)
		renderer.ReturnLinesBuilder(outline)
		if ra.ID == 0 || texture == 0 || fs == nil {
			continue
		}
		label := fmt.Sprintf("[%d]", ra.ID)
		for i, line := range ra.Text {
			if local.HideText {
				break
			}
			if line != "" {
				if i == 0 {
					label += " " + line
				} else {
					label += "\n" + line
				}
			}
		}
		pt := t.WindowFromLatLon(ra.Label.Lat, ra.Label.Lon)
		// The bracketed ID remains visible independently of blinking text.
		labelWidth := float32(0)
		for _, line := range strings.Split(label, "\n") {
			width := starsRBLTextWidth(fs, line)
			if width > labelWidth {
				labelWidth = width
			}
		}
		pt.X -= labelWidth / 2
		pt.Y -= float32(fs.LineHeight) / 2
		labels.AddText(label, pt, renderer.TextStyle{Size: fontSize, Color: restrictionAreaBlinkColor(textColor, (ra.Blink || local.ForceBlinkingText) && !local.StopBlinkingText && blinkDim).ToRGBA()})
	}
	if texture != 0 {
		labels.GenerateCommands(cb, texture)
	}
	cb.DisableScissor()
}

// VICE's restriction text alternates half- and full-brightness every 500 ms.
func restrictionAreaBlinkDim(unixMilli int64) bool {
	return unixMilli/500&1 == 0
}

// VICE dims the *entire* label, including the bracketed ID, instead of
// removing just the text during the blink's dim phase. Keep BRITE scaling
// separate so this adjustment also works at reduced Video Group B brightness.
func restrictionAreaBlinkColor(color renderer.RGB, dim bool) renderer.RGB {
	if !dim {
		return color
	}
	return renderer.RGB{R: color.R * 0.5, G: color.G * 0.5, B: color.B * 0.5}
}

// Ear clipping supports concave polygons without filling across their voids.
// Manual input permits up to ten vertices; the algorithm has no ten-vertex cap.
func restrictionAreaTriangulate(points []renderer.PointVertex) [][3]renderer.PointVertex {
	if len(points) < 3 {
		return nil
	}
	idx := make([]int, len(points))
	for i := range idx {
		idx[i] = i
	}
	cross := func(a, b, c renderer.PointVertex) float64 {
		return float64(b.X-a.X)*float64(c.Y-a.Y) - float64(b.Y-a.Y)*float64(c.X-a.X)
	}
	area := 0.0
	for i := range points {
		j := (i + 1) % len(points)
		area += float64(points[i].X)*float64(points[j].Y) - float64(points[j].X)*float64(points[i].Y)
	}
	sign := 1.0
	if area < 0 {
		sign = -1
	}
	var tris [][3]renderer.PointVertex
	for len(idx) > 3 {
		clipped := false
		for j := range idx {
			a, b, c := points[idx[(j+len(idx)-1)%len(idx)]], points[idx[j]], points[idx[(j+1)%len(idx)]]
			if sign*cross(a, b, c) <= 0 {
				continue
			}
			contains := false
			for _, k := range idx {
				q := points[k]
				if q == a || q == b || q == c {
					continue
				}
				if sign*cross(a, b, q) >= 0 && sign*cross(b, c, q) >= 0 && sign*cross(c, a, q) >= 0 {
					contains = true
					break
				}
			}
			if contains {
				continue
			}
			tris = append(tris, [3]renderer.PointVertex{a, b, c})
			idx = append(idx[:j], idx[j+1:]...)
			clipped = true
			break
		}
		if !clipped {
			return tris
		} // degeneracy/self-intersection: do not invent fill
	}
	if len(idx) == 3 {
		tris = append(tris, [3]renderer.PointVertex{points[idx[0]], points[idx[1]], points[idx[2]]})
	}
	return tris
}

// FAA TFR restriction areas (site store, geometry and snapshot synchronization).

// The STARS GEO RESTRICTIONS list reserves 1..100 for controller-created
// areas and 101..200 for system/adaptation areas (as in VICE). Each FAA NOTAM
// occupies ONE system ID, even when WFS contains multiple features for it.
// Rendering is delegated to tfr_render.go, but IDs and visibility remain
// managed by the existing restriction-area store and commands.
const (
	firstTFRRestrictionID = 101
	lastTFRRestrictionID  = 200
)

type tfrRestrictionSummary struct {
	key          string
	title        string
	category     string
	city         string
	cityCode     string
	state        string
	featureCount int
	artcc        string
	polygons     []starsTFRPolygon
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// Keep the full NOTAM key for identity and display only its FAA number.
func tfrDisplayID(key string) string {
	key = strings.TrimSpace(key)
	if i := strings.IndexByte(key, '-'); i >= 0 {
		key = key[:i]
	}
	return key
}

// Name the restriction with FAA classification and city; the NOTAM number
// remains its own second line. Never use the long date-heavy GeoJSON TITLE
// or the often numeric txtLocalName as an operator-facing descriptor.
func tfrDisplayDescription(category, city, state string) string {
	category = strings.ToUpper(strings.Join(strings.Fields(category), " "))
	switch category {
	case "AIR SHOWS/SPORTS":
		category = "AIRSHOW"
	case "SPACE OPERATIONS":
		category = "SPACE OPS"
	case "UAS PUBLIC GATHERING":
		category = "UAS"
	}
	if category == "" {
		category = "TFR"
	}
	location := strings.ToUpper(strings.Join(strings.Fields(city), " "))
	if location == "" {
		location = strings.ToUpper(strings.Join(strings.Fields(state), " "))
	}
	if location == "" {
		return tfrListLabel(category)
	}
	return tfrListLabel(category + " " + location)
}

func decodeTFRRestrictionSummaries(raw []byte) ([]tfrRestrictionSummary, error) {
	var fc struct {
		Type     string `json:"type"`
		Features []struct {
			Type       string `json:"type"`
			Properties struct {
				NOTAM         string                  `json:"NOTAM_KEY"`
				Title         string                  `json:"TITLE"`
				LocalName     string                  `json:"TFR_LOCAL_NAME"`
				Category      string                  `json:"TFR_CATEGORY"`
				City          string                  `json:"TFR_CITY"`
				CityCode      string                  `json:"TFR_CITY_CODE"`
				State         string                  `json:"TFR_STATE"`
				ARTCC         string                  `json:"ARTCC"`
				ARTCCID       string                  `json:"ARTCC_ID"`
				Center        string                  `json:"CENTER"`
				CenterID      string                  `json:"CENTER_ID"`
				Schedule      tfrActivationSchedule   `json:"TFR_SCHEDULE"`
				PartSchedules []tfrActivationSchedule `json:"TFR_PART_SCHEDULES"`
			} `json:"properties"`
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &fc); err != nil {
		return nil, fmt.Errorf("decode FAA TFR list: %w", err)
	}
	if fc.Type != "FeatureCollection" || len(fc.Features) == 0 {
		return nil, fmt.Errorf("FAA TFR list requires a nonempty FeatureCollection")
	}

	byKey := make(map[string]*tfrRestrictionSummary)
	for _, feature := range fc.Features {
		key := strings.TrimSpace(feature.Properties.NOTAM)
		if feature.Type != "Feature" || key == "" {
			return nil, fmt.Errorf("FAA TFR feature missing type or NOTAM_KEY")
		}
		entry := byKey[key]
		if entry == nil {
			entry = &tfrRestrictionSummary{key: key}
			byKey[key] = entry
		}
		polygons, err := decodeTFRPolygons(feature.Geometry.Type, feature.Geometry.Coordinates)
		if err != nil {
			return nil, fmt.Errorf("FAA TFR %s: %w", key, err)
		}
		// Keep the source center assignment with the TFR; do not infer it
		// from map overlap (a TFR may cross ARTCC boundaries).
		center := firstNonempty(feature.Properties.ARTCC, feature.Properties.ARTCCID,
			feature.Properties.Center, feature.Properties.CenterID)
		if center != "" {
			entry.artcc = strings.ToUpper(strings.TrimSpace(center))
		}
		for i := range polygons {
			// Ignore old mirror snapshots lacking per-part metadata.
			// Never apply a combined schedule to all MultiPolygon parts.
			if i < len(feature.Properties.PartSchedules) {
				polygons[i].Schedule = feature.Properties.PartSchedules[i]
			} else if len(polygons) == 1 {
				polygons[i].Schedule = feature.Properties.Schedule
			}
		}
		entry.polygons = append(entry.polygons, polygons...)
		entry.featureCount++
		// GeoJSON feature IDs are not unique across polygon parts.
		// Keep display metadata separate from the NOTAM identity. FAA's
		// txtLocalName can be a date code, so it is not used for this label.
		if entry.category == "" {
			entry.category = feature.Properties.Category
		}
		if entry.city == "" {
			entry.city = feature.Properties.City
		}
		if entry.cityCode == "" {
			entry.cityCode = feature.Properties.CityCode
		}
		if entry.state == "" {
			entry.state = feature.Properties.State
		}
	}
	result := make([]tfrRestrictionSummary, 0, len(byKey))
	for _, entry := range byKey {
		entry.title = tfrDisplayDescription(entry.category, firstNonempty(entry.cityCode, entry.city), entry.state)
		result = append(result, *entry)
	}
	// Deterministic startup order; subsequently an existing NOTAM keeps its
	// system ID when the FAA changes order, geometry, or titles.
	sort.Slice(result, func(i, j int) bool { return result[i].key < result[j].key })
	return result, nil
}

// A polygon consists of one exterior ring followed by any interior holes.
// Never flatten rings: the shading must not cover excluded areas.
type starsTFRPolygon struct {
	Rings    [][]configPoint
	Schedule tfrActivationSchedule // each WFS feature may have its own schedule
}

type tfrActivationInterval struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}
type tfrActivationSchedule struct {
	Known      bool                    `json:"known"`
	Indefinite bool                    `json:"indefinite"`
	Intervals  []tfrActivationInterval `json:"intervals"`
}

// Only a positively established interval or standing/indefinite restriction
// is drawable. No schedule metadata (older mirror snapshots) means unknown.
func (s tfrActivationSchedule) active(at time.Time) bool {
	if !s.Known {
		return false
	}
	if s.Indefinite {
		return true
	}
	for _, interval := range s.Intervals {
		if !at.Before(interval.Start) && at.Before(interval.End) {
			return true
		}
	}
	return false
}
func tfrHasActivePolygon(area starsRestrictionArea, at time.Time) bool {
	for _, polygon := range area.TFRPolygons {
		if polygon.Schedule.active(at) {
			return true
		}
	}
	return false
}

func decodeTFRPolygons(kind string, raw json.RawMessage) ([]starsTFRPolygon, error) {
	var groups [][][][]float64
	switch kind {
	case "Polygon":
		var polygon [][][]float64
		if err := json.Unmarshal(raw, &polygon); err != nil {
			return nil, err
		}
		groups = [][][][]float64{polygon}
	case "MultiPolygon":
		if err := json.Unmarshal(raw, &groups); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported TFR geometry %q", kind)
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("empty TFR geometry")
	}
	out := make([]starsTFRPolygon, 0, len(groups))
	for _, group := range groups {
		if len(group) == 0 {
			return nil, fmt.Errorf("TFR polygon has no exterior ring")
		}
		poly := starsTFRPolygon{Rings: make([][]configPoint, 0, len(group))}
		for _, ring := range group {
			if len(ring) < 4 {
				return nil, fmt.Errorf("TFR ring has fewer than four positions")
			}
			coords := make([]configPoint, 0, len(ring))
			for _, lonLat := range ring {
				if len(lonLat) != 2 || stdmath.IsNaN(lonLat[0]) || stdmath.IsNaN(lonLat[1]) ||
					stdmath.IsInf(lonLat[0], 0) || stdmath.IsInf(lonLat[1], 0) ||
					lonLat[0] < -180 || lonLat[0] > 180 || lonLat[1] < -90 || lonLat[1] > 90 {
					return nil, fmt.Errorf("invalid TFR lon/lat")
				}
				coords = append(coords, configPoint{Lat: lonLat[1], Lon: lonLat[0]})
			}
			if coords[0] != coords[len(coords)-1] {
				return nil, fmt.Errorf("unclosed TFR ring")
			}
			poly.Rings = append(poly.Rings, coords)
		}
		out = append(out, poly)
	}
	return out, nil
}

// Like VICE's AverageVertexPosition, calculate one text position from all
// exterior vertices, ignoring repeated closing points and hole boundaries.
func tfrLabelPosition(polygons []starsTFRPolygon) configPoint {
	var lat, sinLon, cosLon float64
	var n int
	for _, polygon := range polygons {
		if len(polygon.Rings) == 0 {
			continue
		}
		ring := polygon.Rings[0]
		if len(ring) > 1 && ring[0] == ring[len(ring)-1] {
			ring = ring[:len(ring)-1]
		}
		for _, pt := range ring {
			lat += pt.Lat
			sinLon += stdmath.Sin(pt.Lon * stdmath.Pi / 180)
			cosLon += stdmath.Cos(pt.Lon * stdmath.Pi / 180)
			n++
		}
	}
	if n == 0 {
		return configPoint{}
	}
	return configPoint{Lat: lat / float64(n), Lon: stdmath.Atan2(sinLon, cosLon) * 180 / stdmath.Pi}
}

func tfrListLabel(title string) string {
	// One list entry occupies one row; discard control characters and cap
	// the title to the STARS restriction-area text line length (32 chars).
	title = strings.ToUpper(strings.Join(strings.Fields(title), " "))
	runes := []rune(title)
	if len(runes) > 32 {
		runes = runes[:32]
	}
	return string(runes)
}

// updateTFRList atomically adds/removes read-only FAA placeholders in the
// shared site restriction store. Adaptation-defined 101..200 slots are never
// overwritten. An updated NOTAM keeps both its ID and Serial; if an ID is
// recycled for another NOTAM, Serial changes so local visibility cannot leak.
// A stale pane may not replace a newer snapshot obtained by another pane.
func (s *starsRestrictionAreaStore) updateTFRList(entries []tfrRestrictionSummary, fetchedAt time.Time) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tfrAsOf.IsZero() && fetchedAt.Before(s.tfrAsOf) {
		return 0, false
	}
	if s.areas == nil {
		s.areas = make(map[int]starsRestrictionArea)
	}
	if s.tfrSlots == nil {
		s.tfrSlots = make(map[string]int)
	}
	wanted := make(map[string]tfrRestrictionSummary, len(entries))
	for _, entry := range entries {
		wanted[entry.key] = entry
	}
	// Remove absent NOTAMs, leaving unrelated site adaptation untouched.
	for key, id := range s.tfrSlots {
		if _, present := wanted[key]; !present {
			if a, ok := s.areas[id]; ok && a.IsFAA && a.NOTAM == key {
				delete(s.areas, id)
			}
			delete(s.tfrSlots, key)
		}
	}
	used := make(map[int]bool)
	for id := firstTFRRestrictionID; id <= lastTFRRestrictionID; id++ {
		if _, ok := s.areas[id]; ok {
			used[id] = true
		}
	}
	dropped := 0
	for _, entry := range entries {
		id := s.tfrSlots[entry.key]
		if id != 0 {
			if a, ok := s.areas[id]; !ok || !a.IsFAA || a.NOTAM != entry.key {
				delete(s.tfrSlots, entry.key)
				id = 0
			}
		}
		if id == 0 {
			for candidate := firstTFRRestrictionID; candidate <= lastTFRRestrictionID; candidate++ {
				if !used[candidate] {
					id = candidate
					used[id] = true
					break
				}
			}
		}
		if id == 0 {
			dropped++ // cannot use a manual ID or collide with site adaptation
			continue
		}
		a := s.areas[id]
		if a.Serial == 0 || !a.IsFAA || a.NOTAM != entry.key {
			s.nextSerial++
			a.Serial = s.nextSerial
		}
		a.ID = id
		a.IsFAA = true
		a.NOTAM = entry.key
		a.TFRARTCC = entry.artcc
		a.TFRParts = entry.featureCount
		a.Kind = starsRestrictionClosed
		a.Shaded = true
		a.TFRPolygons = entry.polygons
		a.Label = tfrLabelPosition(entry.polygons)
		a.Text = [2]string{tfrListLabel(firstNonempty(entry.title, "TFR")), tfrDisplayID(entry.key)}
		a.Color = 1
		s.areas[id] = a
		s.tfrSlots[entry.key] = id
	}
	s.tfrAsOf = fetchedAt
	return dropped, true
}

// Called on the STARS draw goroutine. The TFR client fetches in the background;
// the integration never performs network I/O on the rendering thread.
func (p *STARSPane) syncTFRRestrictionList() {
	if p == nil || p.tfr == nil {
		return
	}
	snapshot, ok := p.tfr.Snapshot()
	if !ok {
		return // retain the previous site list during network failure
	}
	if snapshot.Revision != p.tfrListRevision {
		entries, err := decodeTFRRestrictionSummaries(snapshot.GeoJSON)
		if err != nil {
			p.logger.Warn("Unable to decode FAA TFR list", "error", err)
			return // do not replace the valid list with a partial decode
		}
		dropped, _ := p.restrictionAreaStore().updateTFRList(entries, snapshot.FetchedAt)
		if dropped > 0 {
			p.logger.Warn("FAA TFR GEO RESTRICTIONS list at capacity", "dropped", dropped)
		}
		// Even when a newer pane has already updated the site-wide store,
		// this pane has processed its snapshot and need not re-decode it.
		p.tfrListRevision = snapshot.Revision
	}
	p.ensureTFRRestrictionVisibility()
}

// Each new FAA entry is initialized explicitly hidden on every pane. Serial
// guards against an expired/replaced NOTAM inheriting another TFR's visibility.
func (p *STARSPane) ensureTFRRestrictionVisibility() {
	if p == nil {
		return
	}
	// Initialize newly imported slots as locally hidden. Existing preferences
	// are never overwritten: an operator's later visibility choice persists.
	ps := p.currentPrefs()
	if ps.RestrictionAreaSettings == nil {
		ps.RestrictionAreaSettings = make(map[int]restrictionAreaDisplaySettings)
	}
	for _, a := range p.restrictionAreaStore().snapshot() {
		if !a.IsFAA {
			continue
		}
		if existing, ok := ps.RestrictionAreaSettings[a.ID]; !ok || existing.Serial != a.Serial {
			ps.RestrictionAreaSettings[a.ID] = restrictionAreaDisplaySettings{Serial: a.Serial, Hidden: true, HideText: true}
		}
	}
}
