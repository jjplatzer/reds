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

	centerLat float64
	centerLon float64
	rangeNM   float32

	worldProjection renderer.Mat4
}

func GetLatLonTransformations(
	paneExtent redsmath.Rect,
	centerLat float64,
	centerLon float64,
	rangeNM float32,
) LatLonTransformations {
	if paneExtent.Empty() || rangeNM <= 0 {
		return LatLonTransformations{
			paneExtent:      paneExtent,
			centerLat:       centerLat,
			centerLon:       centerLon,
			rangeNM:         rangeNM,
			worldProjection: renderer.Identity(),
		}
	}

	width := paneExtent.Width()
	height := paneExtent.Height()
	aspect := width / height
	halfH := rangeNM * 0.5
	halfW := halfH * aspect

	cosLat := stdmath.Cos(centerLat * stdmath.Pi / 180)
	if stdmath.Abs(cosLat) < 0.01 {
		cosLat = stdmath.Copysign(0.01, cosLat)
	}

	sx := float32(60*cosLat) / halfW
	sy := float32(60) / halfH

	return LatLonTransformations{
		paneExtent: paneExtent,
		centerLat:  centerLat,
		centerLon:  centerLon,
		rangeNM:    rangeNM,
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
	aspect := width / height
	halfH := st.rangeNM * 0.5
	halfW := halfH * aspect

	cosLat := stdmath.Cos(st.centerLat * stdmath.Pi / 180)
	if stdmath.Abs(cosLat) < 0.01 {
		cosLat = stdmath.Copysign(0.01, cosLat)
	}

	x := (lon - st.centerLon) * 60 * cosLat
	y := (lat - st.centerLat) * 60

	return redsmath.Vec2{
		X: width*0.5 + float32(x)/halfW*width*0.5,
		Y: height*0.5 - float32(y)/halfH*height*0.5,
	}
}
