package radar

import (
	stdmath "math"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/renderer"
)

// LatLonTransformations renders geographic vertices whose X is longitude and
// Y is latitude. The projection is a local equirectangular transform centered
// on the selected scope center. Longitude is first scaled into local nautical
// miles and scope rotation is then applied in that isotropic east/north plane.
type LatLonTransformations struct {
	paneExtent redsmath.Rect

	centerLat            float64
	centerLon            float64
	longitudeScaleFactor float64
	rangeNM              float64
	rotationDegrees      float64

	worldProjection renderer.Mat4
}

func LongitudeScaleFactorForLat(lat float64) float64 {
	scale := (59.998660261511354 / 60) * stdmath.Cos(lat*stdmath.Pi/180)
	if stdmath.Abs(scale) < 0.01 {
		scale = stdmath.Copysign(0.01, scale)
	}
	return scale
}

// GetLatLonTransformations constructs the geographic scope transform.
// rotationDegrees is the clockwise rotation of true north relative to screen
// up. REDS follows VICE's FAA display convention here: magnetic variation is
// positive west, so STARS passes its site magnetic variation and ERAM passes 0.
func GetLatLonTransformations(
	paneExtent redsmath.Rect,
	centerLat float64,
	centerLon float64,
	longitudeScaleFactor float64,
	rangeNM float64,
	rotationDegrees float64,
) LatLonTransformations {
	if paneExtent.Empty() || rangeNM <= 0 {
		return LatLonTransformations{
			paneExtent:           paneExtent,
			centerLat:            centerLat,
			centerLon:            centerLon,
			longitudeScaleFactor: longitudeScaleFactor,
			rangeNM:              rangeNM,
			rotationDegrees:      rotationDegrees,
			worldProjection:      renderer.Identity(),
		}
	}

	width := paneExtent.Width()
	height := paneExtent.Height()
	halfW, halfH := LatLonHalfExtentsNM(width, height, rangeNM)
	if longitudeScaleFactor == 0 {
		longitudeScaleFactor = LongitudeScaleFactorForLat(centerLat)
	}

	// Geographic vertices arrive as longitude/latitude degrees. Convert their
	// deltas from the display center to local east/north NM, then rotate the
	// NM plane clockwise by rotationDegrees. Rotating after longitude scaling
	// is essential; rotating raw degrees would distort angles away from the
	// equator. This is algebraically equivalent to VICE's
	// Scale(...).Rotate(-magVar) scope transform.
	theta := rotationDegrees * stdmath.Pi / 180
	c, sin := stdmath.Cos(theta), stdmath.Sin(theta)

	// Column-major affine projection from [lon, lat, 0, 1] to NDC. The x and
	// y denominators differ for a non-square pane, so expand the rotated-NM
	// equations directly instead of composing a rotation in degree space.
	mLonX := c * 60 * longitudeScaleFactor / halfW
	mLatX := sin * 60 / halfW
	mLonY := -sin * 60 * longitudeScaleFactor / halfH
	mLatY := c * 60 / halfH
	tx := -(mLonX*centerLon + mLatX*centerLat)
	ty := -(mLonY*centerLon + mLatY*centerLat)

	return LatLonTransformations{
		paneExtent:           paneExtent,
		centerLat:            centerLat,
		centerLon:            centerLon,
		longitudeScaleFactor: longitudeScaleFactor,
		rangeNM:              rangeNM,
		rotationDegrees:      rotationDegrees,
		worldProjection: renderer.Mat4{
			float32(mLonX), float32(mLonY), 0, 0,
			float32(mLatX), float32(mLatY), 0, 0,
			0, 0, -1, 0,
			float32(tx), float32(ty), 0, 1,
		},
	}
}

func (st LatLonTransformations) LoadGeoViewingMatrices(cb *renderer.CmdBuffer) {
	if cb == nil {
		return
	}
	cb.LoadProjectionMatrix(st.worldProjection)
}

func (st LatLonTransformations) WindowFromLatLon(lat, lon float64) redsmath.Vec2 {
	if st.paneExtent.Empty() || st.rangeNM <= 0 {
		return redsmath.Vec2{}
	}

	width := st.paneExtent.Width()
	height := st.paneExtent.Height()
	halfW, halfH := LatLonHalfExtentsNM(width, height, st.rangeNM)
	scale := st.longitudeScaleFactor
	if scale == 0 {
		scale = LongitudeScaleFactorForLat(st.centerLat)
	}

	eastNM := (lon - st.centerLon) * 60 * scale
	northNM := (lat - st.centerLat) * 60
	theta := st.rotationDegrees * stdmath.Pi / 180
	c, sin := stdmath.Cos(theta), stdmath.Sin(theta)
	rotatedEastNM := c*eastNM + sin*northNM
	rotatedNorthNM := -sin*eastNM + c*northNM

	return redsmath.Vec2{
		X: width*0.5 + float32(rotatedEastNM/halfW)*width*0.5,
		Y: height*0.5 - float32(rotatedNorthNM/halfH)*height*0.5,
	}
}

func (st LatLonTransformations) LatLonFromWindow(pos redsmath.Vec2) (lat, lon float64) {
	if st.paneExtent.Empty() || st.rangeNM <= 0 {
		return st.centerLat, st.centerLon
	}

	width := st.paneExtent.Width()
	height := st.paneExtent.Height()
	halfW, halfH := LatLonHalfExtentsNM(width, height, st.rangeNM)
	scale := st.longitudeScaleFactor
	if scale == 0 {
		scale = LongitudeScaleFactorForLat(st.centerLat)
	}

	rotatedEastNM := (float64(pos.X) - float64(width)*0.5) / (float64(width) * 0.5) * halfW
	rotatedNorthNM := (float64(height)*0.5 - float64(pos.Y)) / (float64(height) * 0.5) * halfH

	// Invert the clockwise world-to-screen rotation.
	theta := st.rotationDegrees * stdmath.Pi / 180
	c, sin := stdmath.Cos(theta), stdmath.Sin(theta)
	eastNM := c*rotatedEastNM - sin*rotatedNorthNM
	northNM := sin*rotatedEastNM + c*rotatedNorthNM

	lat = st.centerLat + northNM/60
	lon = normalizeLon(st.centerLon + eastNM/(60*scale))
	return lat, lon
}

// LatLonFromWindowV converts a pane-local window vector to a geographic
// latitude/longitude delta. Window y increases downward, so a positive y
// vector produces a negative latitude delta.
func (st LatLonTransformations) LatLonFromWindowV(v redsmath.Vec2) (deltaLat, deltaLon float64) {
	if st.paneExtent.Empty() || st.rangeNM <= 0 {
		return 0, 0
	}

	width := st.paneExtent.Width()
	height := st.paneExtent.Height()
	halfW, halfH := LatLonHalfExtentsNM(width, height, st.rangeNM)
	scale := st.longitudeScaleFactor
	if scale == 0 {
		scale = LongitudeScaleFactorForLat(st.centerLat)
	}

	rotatedEastNM := float64(v.X) / (float64(width) * 0.5) * halfW
	rotatedNorthNM := -float64(v.Y) / (float64(height) * 0.5) * halfH
	theta := st.rotationDegrees * stdmath.Pi / 180
	c, sin := stdmath.Cos(theta), stdmath.Sin(theta)
	eastNM := c*rotatedEastNM - sin*rotatedNorthNM
	northNM := sin*rotatedEastNM + c*rotatedNorthNM
	return northNM / 60, eastNM / (60 * scale)
}

func LatLonHalfExtentsNM(width, height float32, rangeNM float64) (halfW, halfH float64) {
	if width <= 0 || height <= 0 || rangeNM <= 0 {
		return 0, 0
	}

	shortSide := float64(width)
	if height < width {
		shortSide = float64(height)
	}

	return rangeNM * float64(width) / shortSide,
		rangeNM * float64(height) / shortSide
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
