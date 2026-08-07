package radar

import (
	stdmath "math"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/renderer"
)

// LatLonTransformations renders geographic vertices whose X is longitude and
// Y is latitude. The projection is a local equirectangular transform centered
// on the selected ERAM sector, with world units in nautical miles.
type LatLonTransformations struct {
	paneExtent redsmath.Rect

	centerLat            float64
	centerLon            float64
	longitudeScaleFactor float64
	rangeNM              float64

	worldProjection renderer.Mat4
}

func LongitudeScaleFactorForLat(lat float64) float64 {
	scale := (59.998660261511354 / 60) * stdmath.Cos(lat*stdmath.Pi/180)
	if stdmath.Abs(scale) < 0.01 {
		scale = stdmath.Copysign(0.01, scale)
	}
	return scale
}

func GetLatLonTransformations(
	paneExtent redsmath.Rect,
	centerLat float64,
	centerLon float64,
	longitudeScaleFactor float64,
	rangeNM float64,
) LatLonTransformations {
	if paneExtent.Empty() || rangeNM <= 0 {
		return LatLonTransformations{
			paneExtent:           paneExtent,
			centerLat:            centerLat,
			centerLon:            centerLon,
			longitudeScaleFactor: longitudeScaleFactor,
			rangeNM:              rangeNM,
			worldProjection:      renderer.Identity(),
		}
	}

	width := paneExtent.Width()
	height := paneExtent.Height()
	halfW, halfH := LatLonHalfExtentsNM(width, height, rangeNM)
	if longitudeScaleFactor == 0 {
		longitudeScaleFactor = LongitudeScaleFactorForLat(centerLat)
	}

	sx := float32(60*longitudeScaleFactor) / float32(halfW)
	sy := float32(60) / float32(halfH)

	return LatLonTransformations{
		paneExtent:           paneExtent,
		centerLat:            centerLat,
		centerLon:            centerLon,
		longitudeScaleFactor: longitudeScaleFactor,
		rangeNM:              rangeNM,
		worldProjection: renderer.Mat4{
			sx, 0, 0, 0,
			0, sy, 0, 0,
			0, 0, -1, 0,
			-float32(centerLon) * sx, -float32(centerLat) * sy, 0, 1,
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

	x := (lon - st.centerLon) * 60 * scale
	y := (lat - st.centerLat) * 60

	return redsmath.Vec2{
		X: width*0.5 + float32(x/halfW)*width*0.5,
		Y: height*0.5 - float32(y/halfH)*height*0.5,
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

	xNM := (float64(pos.X) - float64(width)*0.5) / (float64(width) * 0.5) * halfW
	yNM := (float64(height)*0.5 - float64(pos.Y)) / (float64(height) * 0.5) * halfH

	lat = st.centerLat + yNM/60
	lon = normalizeLon(st.centerLon + xNM/(60*scale))
	return lat, lon
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
