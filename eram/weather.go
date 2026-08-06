package eram

import (
	"context"
	stdmath "math"

	"github.com/juliusplatzer/reds/cmd/wx"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
)

func (p *ERAMPane) ensureWxCoverage(ctx *panes.Context) {
	if p == nil || ctx == nil {
		return
	}

	visibleRadius := p.visibleWeatherRadiusNM(ctx)
	if visibleRadius <= 0 {
		return
	}

	desiredRadius := visibleRadius + wxPrefetchMarginNM
	if desiredRadius < initialWxRadiusNM {
		desiredRadius = initialWxRadiusNM
	}

	distanceFromCropCenter := p.centerDistanceNM(p.wxCenter)
	if p.wxStream == nil ||
		p.wxRadiusNM <= 0 ||
		desiredRadius > p.wxRadiusNM+wxRefreshMarginNM ||
		distanceFromCropCenter+visibleRadius > p.wxRadiusNM-wxRefreshMarginNM {
		p.restartWxStream(desiredRadius)
	}
}

func (p *ERAMPane) restartWxStream(radiusNM float64) {
	if p == nil || radiusNM <= 0 {
		return
	}

	if p.wxStream != nil {
		p.wxStream.Close()
		p.wxStream = nil
	}

	p.wxCenter = p.center
	p.wxRadiusNM = radiusNM
	p.wxStream = wx.Start(
		context.Background(),
		mrmsHTTPClient,
		p.wxDomain,
		wx.BoundsAround(p.wxCenter.Lat, p.wxCenter.Lon, radiusNM),
		p.wxLogger,
	)
}

func (p *ERAMPane) visibleWeatherRadiusNM(ctx *panes.Context) float64 {
	if p == nil || ctx == nil {
		return 0
	}
	halfW, halfH := radar.LatLonHalfExtentsNM(
		ctx.PaneRect.Width(),
		ctx.PaneRect.Height(),
		p.rangeNM,
	)
	if halfW > halfH {
		return halfW
	}
	return halfH
}

func (p *ERAMPane) centerDistanceNM(other LatLon) float64 {
	if p == nil {
		return 0
	}

	dLon := lonDelta(p.center.Lon, other.Lon) * 60 * p.longitudeScaleFactor
	dLat := (p.center.Lat - other.Lat) * 60
	return stdmath.Hypot(dLon, dLat)
}
