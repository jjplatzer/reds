package radar

import (
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
// is half the visible world height, matching VICE's range semantics. Rotation
// is retained for future ASDE-X ROTATE support but is not applied yet.
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
	aspect := width / height

	worldHalfH := rangeFeet
	worldHalfW := rangeFeet * aspect

	left := center.X - worldHalfW
	right := center.X + worldHalfW
	bottom := center.Y - worldHalfH
	top := center.Y + worldHalfH

	return ScopeTransformations{
		paneExtent:       paneExtent,
		center:           center,
		rangeFeet:        rangeFeet,
		rotation:         rotation,
		worldProjection:  renderer.Ortho(left, right, bottom, top, -1, 1),
		windowProjection: renderer.ScreenOrtho(width, height),
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

	left := st.center.X - worldHalfW
	top := st.center.Y + worldHalfH

	return redsmath.Vec2{
		X: left + (p.X/width)*(2*worldHalfW),
		Y: top - (p.Y/height)*(2*worldHalfH),
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

	return redsmath.Vec2{
		X: (v.X / width) * (2 * worldHalfW),
		Y: -(v.Y / height) * (2 * worldHalfH),
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

	left := st.center.X - worldHalfW
	top := st.center.Y + worldHalfH

	return redsmath.Vec2{
		X: ((p.X - left) / (2 * worldHalfW)) * width,
		Y: ((top - p.Y) / (2 * worldHalfH)) * height,
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

	return redsmath.Vec2{
		X: (v.X / (2 * worldHalfW)) * width,
		Y: -(v.Y / (2 * worldHalfH)) * height,
	}
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
