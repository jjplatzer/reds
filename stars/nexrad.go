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
	starsInitialNexradRadiusNM  = 150
	starsNexradPrefetchMarginNM = 100
	starsNexradRefreshMarginNM  = 50
)

var starsMRMSHTTPClient = &http.Client{Timeout: 20 * time.Second}

// STARS uses six weather levels. FAA/NWS reflectivity bands are 18-29,
// 30-40, 41-45, 46-49, 50-56, and 57+ dBZ. MRMS has its own much finer
// color-ramp bins; those are source-product legend bins, not STARS level
// numbers, so numeric MRMS dBZ is translated into these six STARS bands.
var starsNexradThresholds = [6]uint8{18, 30, 41, 46, 50, 57}

type starsNexradLevelCmdBuffers struct {
	fill    *renderer.CmdBuffer
	stipple *renderer.CmdBuffer
}

func (p *STARSPane) consumeNexradUpdates() {
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
				p.nexradGeneration++
			}
			return
		}
	}
}

func (p *STARSPane) ensureNexradCoverage(ctx *panes.Context) {
	if p == nil || ctx == nil {
		return
	}
	visibleRadius := p.visibleNexradRadiusNM(ctx)
	if visibleRadius <= 0 {
		return
	}

	desiredRadius := visibleRadius + starsNexradPrefetchMarginNM
	if desiredRadius < starsInitialNexradRadiusNM {
		desiredRadius = starsInitialNexradRadiusNM
	}

	distanceFromCropCenter := p.centerDistanceNM(p.wxCenter)
	if p.wxStream == nil || p.wxRadiusNM <= 0 ||
		desiredRadius > p.wxRadiusNM+starsNexradRefreshMarginNM ||
		distanceFromCropCenter+visibleRadius > p.wxRadiusNM-starsNexradRefreshMarginNM {
		p.restartNexradStream(desiredRadius)
	}
}

func (p *STARSPane) restartNexradStream(radiusNM float64) {
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

func (p *STARSPane) visibleNexradRadiusNM(ctx *panes.Context) float64 {
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

func (p *STARSPane) rebuildNexradIfNeeded() {
	if p == nil || p.nexradBuiltGeneration == p.nexradGeneration {
		return
	}
	p.releaseNexradCmdBuffers()
	if p.wxGrid != nil {
		p.nexrad = buildStarsNexradCmdBuffers(p.wxGrid, p.colors.WXLevelStipple)
	}
	p.nexradBuiltGeneration = p.nexradGeneration
}

func (p *STARSPane) releaseNexradCmdBuffers() {
	if p == nil {
		return
	}
	for i := range p.nexrad {
		renderer.ReturnCmdBuffer(p.nexrad[i].fill)
		renderer.ReturnCmdBuffer(p.nexrad[i].stipple)
		p.nexrad[i] = starsNexradLevelCmdBuffers{}
	}
	p.nexradBuiltGeneration = 0
}

func buildStarsNexradCmdBuffers(grid *wx.Grid, stipple [6]int) [6]starsNexradLevelCmdBuffers {
	var out [6]starsNexradLevelCmdBuffers
	if grid == nil {
		return out
	}

	for level := range out {
		lower := starsNexradThresholds[level]
		var upper *uint8
		if level+1 < len(starsNexradThresholds) {
			v := starsNexradThresholds[level+1]
			upper = &v
		}
		rects := wx.MergeDBZRange(grid, lower, upper)
		if len(rects) == 0 {
			continue
		}
		out[level].fill = buildStarsNexradRectBuffer(rects, renderer.DrawSolid)
		switch stipple[level] {
		case 1:
			out[level].stipple = buildStarsNexradRectBuffer(rects, renderer.DrawStippleLight)
		case 2:
			out[level].stipple = buildStarsNexradRectBuffer(rects, renderer.DrawStippleDense)
		}
	}
	return out
}

func buildStarsNexradRectBuffer(rects []wx.GridRect, mode renderer.DrawMode) *renderer.CmdBuffer {
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

func (p *STARSPane) drawNexrad(ctx *panes.Context, zcb *renderer.ZCmdBuffer, transforms radar.LatLonTransformations) {
	if p == nil || ctx == nil || zcb == nil || p.wxGrid == nil {
		return
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zWeather)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	transforms.LoadGeoViewingMatrices(cb)

	ps := p.currentPrefs()
	active := ps.DisplayWeatherLevel
	for i := range p.nexrad {
		if !active[i] || p.nexrad[i].fill == nil {
			continue
		}
		cb.SetRGB(ps.Brightness.Weather.ScaleRGB(p.colors.WX[i]))
		cb.Call(p.nexrad[i].fill)
		if p.nexrad[i].stipple != nil {
			cb.SetRGB(ps.Brightness.WxContrast.ScaleRGB(p.colors.WXPattern))
			cb.Call(p.nexrad[i].stipple)
		}
	}
	cb.DisableScissor()
}
