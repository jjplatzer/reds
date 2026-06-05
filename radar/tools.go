package radar

import (
	stdmath "math"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/renderer"
)

// ScopeTransformations converts between pane-local window coordinates and
// scope-local world coordinates. World coordinates are feet with y increasing
// upward; window coordinates have a top-left origin with y increasing downward.
type ScopeTransformations struct {
	paneExtent redsmath.Rect

	center    redsmath.Vec2
	rangeFeet float32
	rotation  float32

	worldProjection  renderer.Mat4
	windowProjection renderer.Mat4
}

// GetScopeTransformations returns transformations for a scope view. rangeFeet
// is half the visible world height, matching VICE's range semantics.
func GetScopeTransformations(
	paneExtent redsmath.Rect,
	center redsmath.Vec2,
	rangeFeet float32,
	rotation float32,
) ScopeTransformations {
	if paneExtent.Empty() || rangeFeet <= 0 {
		return ScopeTransformations{
			paneExtent:       paneExtent,
			center:           center,
			rangeFeet:        rangeFeet,
			rotation:         rotation,
			worldProjection:  renderer.Identity(),
			windowProjection: renderer.Identity(),
		}
	}

	width := paneExtent.Width()
	height := paneExtent.Height()

	return ScopeTransformations{
		paneExtent:       paneExtent,
		center:           center,
		rangeFeet:        rangeFeet,
		rotation:         rotation,
		worldProjection:  rotatedWorldProjection(width, height, center, rangeFeet, rotation),
		windowProjection: renderer.ScreenOrtho(width, height),
	}
}

func rotatedWorldProjection(
	width, height float32,
	center redsmath.Vec2,
	rangeFeet float32,
	rotation float32,
) renderer.Mat4 {
	if width <= 0 || height <= 0 || rangeFeet <= 0 {
		return renderer.Identity()
	}

	aspect := width / height
	worldHalfH := rangeFeet
	worldHalfW := rangeFeet * aspect

	sx := float32(1) / worldHalfW
	sy := float32(1) / worldHalfH

	theta := float64(normalizedDegreesFloat(rotation)) * stdmath.Pi / 180
	c := float32(stdmath.Cos(theta))
	s := float32(stdmath.Sin(theta))

	cx := center.X
	cy := center.Y

	return renderer.Mat4{
		sx * c, sy * s, 0, 0,
		-sx * s, sy * c, 0, 0,
		0, 0, -1, 0,
		sx * (-c*cx + s*cy), sy * (-s*cx - c*cy), 0, 1,
	}
}

func (st ScopeTransformations) WorldFromWindowP(p redsmath.Vec2) redsmath.Vec2 {
	if st.paneExtent.Empty() || st.rangeFeet <= 0 {
		return st.center
	}

	width := st.paneExtent.Width()
	height := st.paneExtent.Height()
	aspect := width / height

	worldHalfH := st.rangeFeet
	worldHalfW := st.rangeFeet * aspect

	rx := ((p.X / width) - 0.5) * 2 * worldHalfW
	ry := (0.5 - (p.Y / height)) * 2 * worldHalfH

	theta := float64(normalizedDegreesFloat(st.rotation)) * stdmath.Pi / 180
	c := float32(stdmath.Cos(theta))
	s := float32(stdmath.Sin(theta))

	dx := c*rx + s*ry
	dy := -s*rx + c*ry

	return redsmath.Vec2{
		X: st.center.X + dx,
		Y: st.center.Y + dy,
	}
}

func (st ScopeTransformations) WorldFromWindowV(v redsmath.Vec2) redsmath.Vec2 {
	if st.paneExtent.Empty() || st.rangeFeet <= 0 {
		return redsmath.Vec2{}
	}

	width := st.paneExtent.Width()
	height := st.paneExtent.Height()
	aspect := width / height

	worldHalfH := st.rangeFeet
	worldHalfW := st.rangeFeet * aspect

	rx := (v.X / width) * (2 * worldHalfW)
	ry := -(v.Y / height) * (2 * worldHalfH)

	theta := float64(normalizedDegreesFloat(st.rotation)) * stdmath.Pi / 180
	c := float32(stdmath.Cos(theta))
	s := float32(stdmath.Sin(theta))

	return redsmath.Vec2{
		X: c*rx + s*ry,
		Y: -s*rx + c*ry,
	}
}

func (st ScopeTransformations) WindowFromWorldP(p redsmath.Vec2) redsmath.Vec2 {
	if st.paneExtent.Empty() || st.rangeFeet <= 0 {
		return redsmath.Vec2{}
	}

	width := st.paneExtent.Width()
	height := st.paneExtent.Height()
	aspect := width / height

	worldHalfH := st.rangeFeet
	worldHalfW := st.rangeFeet * aspect

	dx := p.X - st.center.X
	dy := p.Y - st.center.Y

	theta := float64(normalizedDegreesFloat(st.rotation)) * stdmath.Pi / 180
	c := float32(stdmath.Cos(theta))
	s := float32(stdmath.Sin(theta))

	rx := c*dx - s*dy
	ry := s*dx + c*dy

	return redsmath.Vec2{
		X: width*0.5 + (rx/worldHalfW)*width*0.5,
		Y: height*0.5 - (ry/worldHalfH)*height*0.5,
	}
}

func (st ScopeTransformations) WindowFromWorldV(v redsmath.Vec2) redsmath.Vec2 {
	if st.paneExtent.Empty() || st.rangeFeet <= 0 {
		return redsmath.Vec2{}
	}

	width := st.paneExtent.Width()
	height := st.paneExtent.Height()
	aspect := width / height

	worldHalfH := st.rangeFeet
	worldHalfW := st.rangeFeet * aspect

	theta := float64(normalizedDegreesFloat(st.rotation)) * stdmath.Pi / 180
	c := float32(stdmath.Cos(theta))
	s := float32(stdmath.Sin(theta))

	rx := c*v.X - s*v.Y
	ry := s*v.X + c*v.Y

	return redsmath.Vec2{
		X: (rx / worldHalfW) * width * 0.5,
		Y: -(ry / worldHalfH) * height * 0.5,
	}
}

func normalizedDegreesFloat(degrees float32) float32 {
	for degrees >= 360 {
		degrees -= 360
	}
	for degrees < 0 {
		degrees += 360
	}
	return degrees
}

func (st ScopeTransformations) LoadWorldViewingMatrices(cb *renderer.CmdBuffer) {
	if cb == nil {
		return
	}
	cb.LoadProjectionMatrix(st.worldProjection)
}

func (st ScopeTransformations) LoadWindowViewingMatrices(cb *renderer.CmdBuffer) {
	if cb == nil {
		return
	}
	cb.LoadProjectionMatrix(st.windowProjection)
}
