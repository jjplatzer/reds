package stars

import (
	"context"
	stdmath "math"
	"net/http"
	"time"

	"github.com/juliusplatzer/reds/cmd/wx"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	starsInitialWxRadiusNM  = 150
	starsWxPrefetchMarginNM = 100
	starsWxRefreshMarginNM  = 50
)

var starsMRMSHTTPClient = &http.Client{Timeout: 20 * time.Second}

// STARS uses six weather levels. These dBZ thresholds match VICE's mapping of
// NEXRAD/MRMS reflectivity to STARS levels: (20,30], (30,40], (40,45],
// (45,50], (50,55], and >55 dBZ.
var starsWxThresholds = [6]uint8{20, 30, 40, 45, 50, 55}

type starsWXLevelCmdBuffers struct {
	fill    *renderer.CmdBuffer
	stipple *renderer.CmdBuffer
}

func (p *STARSPane) consumeWxUpdates() {
	if p == nil || p.wxStream == nil {
		return
	}
	updates := p.wxStream.Updates()
	var latest *wx.Grid
	for {
		select {
		case grid := <-updates:
			if grid != nil {
				latest = grid
			}
		default:
			if latest != nil {
				p.wxGrid = latest
				p.wxGeneration++
			}
			return
		}
	}
}

func (p *STARSPane) ensureWxCoverage(ctx *panes.Context) {
	if p == nil || ctx == nil {
		return
	}
	visibleRadius := p.visibleWeatherRadiusNM(ctx)
	if visibleRadius <= 0 {
		return
	}

	desiredRadius := visibleRadius + starsWxPrefetchMarginNM
	if desiredRadius < starsInitialWxRadiusNM {
		desiredRadius = starsInitialWxRadiusNM
	}

	distanceFromCropCenter := p.centerDistanceNM(p.wxCenter)
	if p.wxStream == nil || p.wxRadiusNM <= 0 ||
		desiredRadius > p.wxRadiusNM+starsWxRefreshMarginNM ||
		distanceFromCropCenter+visibleRadius > p.wxRadiusNM-starsWxRefreshMarginNM {
		p.restartWxStream(desiredRadius)
	}
}

func (p *STARSPane) restartWxStream(radiusNM float64) {
	if p == nil || radiusNM <= 0 {
		return
	}
	if p.wxStream != nil {
		p.wxStream.Close()
		p.wxStream = nil
	}

	p.wxCenter = p.currentCenter()
	p.wxRadiusNM = radiusNM
	p.wxStream = wx.Start(
		context.Background(),
		starsMRMSHTTPClient,
		p.wxDomain,
		wx.BoundsAround(p.wxCenter.Lat, p.wxCenter.Lon, radiusNM),
		p.wxLogger,
	)
}

func (p *STARSPane) visibleWeatherRadiusNM(ctx *panes.Context) float64 {
	if p == nil || ctx == nil {
		return 0
	}
	halfW, halfH := radar.LatLonHalfExtentsNM(
		ctx.PaneRect.Width(),
		ctx.PaneRect.Height(),
		float64(p.currentPrefs().Range),
	)
	if halfW > halfH {
		return halfW
	}
	return halfH
}

func (p *STARSPane) centerDistanceNM(other configPoint) float64 {
	if p == nil {
		return 0
	}
	center := p.currentCenter()
	dLon := longitudeDelta(center.Lon, other.Lon) * 60 * p.longitudeScaleFactor
	dLat := (center.Lat - other.Lat) * 60
	return stdmath.Hypot(dLon, dLat)
}

func (p *STARSPane) rebuildWxIfNeeded() {
	if p == nil || p.wxBuiltGeneration == p.wxGeneration {
		return
	}
	p.releaseWxCmdBuffers()
	if p.wxGrid != nil {
		p.wxLevels = buildStarsWxCmdBuffers(p.wxGrid, p.colors.WXLevelStipple)
	}
	p.wxBuiltGeneration = p.wxGeneration
}

func (p *STARSPane) releaseWxCmdBuffers() {
	if p == nil {
		return
	}
	for i := range p.wxLevels {
		renderer.ReturnCmdBuffer(p.wxLevels[i].fill)
		renderer.ReturnCmdBuffer(p.wxLevels[i].stipple)
		p.wxLevels[i] = starsWXLevelCmdBuffers{}
	}
	p.wxBuiltGeneration = 0
}

func buildStarsWxCmdBuffers(grid *wx.Grid, stipple [6]int) [6]starsWXLevelCmdBuffers {
	var out [6]starsWXLevelCmdBuffers
	if grid == nil {
		return out
	}

	for level := range out {
		lower := starsWxThresholds[level]
		var upper *uint8
		if level+1 < len(starsWxThresholds) {
			v := starsWxThresholds[level+1]
			upper = &v
		}
		rects := wx.MergeDBZRectangles(grid, lower, upper)
		if len(rects) == 0 {
			continue
		}
		out[level].fill = buildStarsWxRectBuffer(rects, renderer.DrawSolid)
		switch stipple[level] {
		case 1:
			out[level].stipple = buildStarsWxRectBuffer(rects, renderer.DrawStippleLight)
		case 2:
			out[level].stipple = buildStarsWxRectBuffer(rects, renderer.DrawStippleDense)
		}
	}
	return out
}

func buildStarsWxRectBuffer(rects []wx.GridRect, mode renderer.DrawMode) *renderer.CmdBuffer {
	if len(rects) == 0 {
		return nil
	}
	builder := renderer.GetTrianglesBuilder()
	for _, rect := range rects {
		builder.AddQuad(
			renderer.PointVertex{X: float32(rect.West), Y: float32(rect.North)},
			renderer.PointVertex{X: float32(rect.East), Y: float32(rect.North)},
			renderer.PointVertex{X: float32(rect.East), Y: float32(rect.South)},
			renderer.PointVertex{X: float32(rect.West), Y: float32(rect.South)},
		)
	}
	cb := renderer.GetCmdBuffer()
	builder.GenerateCommands(cb, mode, 0)
	renderer.ReturnTrianglesBuilder(builder)
	return cb
}

func (p *STARSPane) drawWX(ctx *panes.Context, zcb *renderer.ZCmdBuffer, transforms radar.LatLonTransformations) {
	if p == nil || ctx == nil || zcb == nil || p.wxGrid == nil {
		return
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zWeather)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	transforms.LoadGeoViewingMatrices(cb)

	active := p.currentPrefs().DisplayWeatherLevel
	for i := range p.wxLevels {
		if !active[i] || p.wxLevels[i].fill == nil {
			continue
		}
		cb.SetRGB(p.colors.WX[i])
		cb.Call(p.wxLevels[i].fill)
		if p.wxLevels[i].stipple != nil {
			cb.SetRGB(p.colors.WXPattern)
			cb.Call(p.wxLevels[i].stipple)
		}
	}
	cb.DisableScissor()
}
