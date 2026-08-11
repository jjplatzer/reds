package eram

import (
	stdmath "math"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/radar"
)

const eramFloatTolerance = 1e-10

type eramPanDrag struct {
	startMouse  redsmath.Vec2
	startCenter LatLon
	panning     bool
}

func (p *ERAMPane) consumeInput(ctx *panes.Context) {
	if p == nil || ctx == nil {
		return
	}

	// Captured placement modes own all input until they are completed/canceled.
	// Keep toolbar tearoff movement first because it was the original captured
	// interaction in REDS; movable ERAM views use the same exclusivity.
	if p.toolbar.moving != nil {
		p.consumeToolbarInput(ctx)
		return
	}
	if p.viewUI.Moving != nil {
		p.consumeViewMoveInput(ctx)
		return
	}

	// A right-button scope pan is captured from its starting point and remains
	// active even if the pointer later crosses another view.
	if p.panDrag != nil {
		p.consumePanInput(ctx)
		return
	}

	// ViewTearoffs owns Escape while deletion mode is active. Handle it before
	// MCA keyboard entry so the same key does not also clear the Preview Area.
	if p.toolbar.deletingTearoffs && ctx.Keyboard != nil && ctx.Keyboard.WasPressed(platform.KeyEscape) {
		p.cancelDeleteTearoffs()
		return
	}

	// CRC view settings menus have higher pick precedence than ordinary views.
	// An outside click closes the menu and is intentionally allowed to continue
	// to the underlying object in the same frame.
	if p.consumeViewMenuInput(ctx) {
		return
	}

	// ERAM keyboard entry is global command input and is displayed in the MCA
	// Preview Area. Captured move/menu interactions above get first refusal.
	p.consumeMCAKeyboard(ctx)

	// CRC gives MCA higher pick precedence than the Response Area, and both are
	// above ordinary semi-transparent views such as TIME.
	if p.consumeMCAInput(ctx) {
		return
	}
	if p.consumeResponseAreaInput(ctx) {
		return
	}

	// The default clock is a semi-transparent view in CRC and therefore picks
	// ahead of lowered toolbar/tearoff buttons.
	if p.consumeClockInput(ctx) {
		return
	}
	if p.consumeWXReportInput(ctx) {
		return
	}
	if p.consumeChecklistInput(ctx) {
		return
	}

	// CRC's DELETE_TEAROFF_ACTIVE pick list still leaves the MCA/RA and view
	// settings layers above the lowered toolbar, but removes SituationDisplay
	// input. Preserve that ordering: views above have already had first refusal,
	// deletion-mode toolbar picks are handled here, and scope pan/zoom below is
	// suppressed until deletion is confirmed or canceled.
	if p.toolbar.deletingTearoffs {
		p.consumeDeleteTearoffInput(ctx)
		return
	}

	if p.consumeToolbarInput(ctx) {
		return
	}

	p.consumePanInput(ctx)
	p.consumeZoomInput(ctx)
}

func (p *ERAMPane) consumePanInput(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		p.panDrag = nil
		return
	}

	mouse := ctx.Mouse
	if mouse.WasPressed(platform.MouseButtonRight) {
		p.panDrag = &eramPanDrag{
			startMouse:  truncateMousePos(mouse.Pos),
			startCenter: p.center,
		}
	}

	if p.panDrag == nil {
		return
	}

	if !mouse.IsDown(platform.MouseButtonRight) {
		p.panDrag = nil
		return
	}

	current := truncateMousePos(mouse.Pos)
	delta := current.Sub(p.panDrag.startMouse)
	if delta.X == 0 && delta.Y == 0 {
		return
	}

	p.panDrag.panning = true

	paneSize := ctx.PaneSize()
	shortSide := stdmath.Min(float64(paneSize.X), float64(paneSize.Y))
	if shortSide <= 0 || p.rangeNM <= 0 {
		return
	}

	nmPerPixel := 2 * p.rangeNM / shortSide
	deltaLon := -float64(delta.X) * nmPerPixel / (60 * p.longitudeScaleFactor)
	deltaLat := float64(delta.Y) * nmPerPixel / 60

	p.center = LatLon{
		Lat: clampFloat64(p.panDrag.startCenter.Lat+deltaLat, -90, 90),
		Lon: normalizeLon(p.panDrag.startCenter.Lon + deltaLon),
	}
}

func (p *ERAMPane) consumeZoomInput(ctx *panes.Context) {
	if p == nil || ctx == nil {
		return
	}

	if ctx.Mouse != nil && ctx.Mouse.Wheel.Y != 0 {
		zoomIn := ctx.Mouse.Wheel.Y > 0
		fast := ctx.Keyboard != nil && ctx.Keyboard.IsDown(platform.KeyControl)
		aboutPointer := ctx.Keyboard != nil && ctx.Keyboard.IsDown(platform.KeyAlt)
		p.applyZoom(ctx, zoomIn, fast, aboutPointer)
	}

	if ctx.Keyboard == nil || !ctx.Keyboard.IsDown(platform.KeyShift) {
		return
	}
	if ctx.Keyboard.WasPressed(platform.KeyPageUp) {
		p.applyZoom(ctx, false, true, false)
	}
	if ctx.Keyboard.WasPressed(platform.KeyPageDown) {
		p.applyZoom(ctx, true, true, false)
	}
}

func (p *ERAMPane) applyZoom(ctx *panes.Context, zoomIn, fast, aboutPointer bool) {
	if p == nil || ctx == nil {
		return
	}

	oldRange := p.rangeNM
	newRange := clampFloat64(oldRange+eramZoomDelta(oldRange, zoomIn, fast), minRangeNM, maxRangeNM)
	if almostEqual(oldRange, newRange) {
		return
	}

	if aboutPointer && ctx.Mouse != nil {
		mouse := truncateMousePos(ctx.Mouse.Pos)
		paneExtent := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
		transforms := radar.GetLatLonTransformations(
			paneExtent,
			p.center.Lat,
			p.center.Lon,
			p.longitudeScaleFactor,
			oldRange,
		)
		pointerLat, pointerLon := transforms.LatLonFromWindow(mouse)
		factor := newRange / oldRange
		p.center = LatLon{
			Lat: clampFloat64(pointerLat+factor*(p.center.Lat-pointerLat), -90, 90),
			Lon: normalizeLon(pointerLon + factor*lonDelta(p.center.Lon, pointerLon)),
		}
	}

	p.rangeNM = newRange
}

func eramZoomDelta(r float64, zoomIn, fast bool) float64 {
	sign := 1.0
	if zoomIn {
		sign = -1
	}

	if fast && (r > 10 || (almostEqual(r, 10) && !zoomIn)) {
		return sign * 10
	}

	if zoomIn {
		switch {
		case r <= 2:
			return -0.25
		case r <= 10:
			return -1
		default:
			return -2
		}
	}

	switch {
	case r < 2:
		return 0.25
	case r < 10:
		return 1
	default:
		return 2
	}
}

func truncateMousePos(pos redsmath.Vec2) redsmath.Vec2 {
	return redsmath.Vec2{
		X: float32(stdmath.Trunc(float64(pos.X))),
		Y: float32(stdmath.Trunc(float64(pos.Y))),
	}
}

func almostEqual(a, b float64) bool {
	return stdmath.Abs(a-b) <= eramFloatTolerance
}

func clampFloat64(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func lonDelta(lon, reference float64) float64 {
	delta := lon - reference
	for delta > 180 {
		delta -= 360
	}
	for delta <= -180 {
		delta += 360
	}
	return delta
}

func normalizeLon(lon float64) float64 {
	for lon > 180 {
		lon -= 360
	}
	for lon <= -180 {
		lon += 360
	}
	return lon
}
