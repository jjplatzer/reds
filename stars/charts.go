package stars

import (
	"context"
	"strings"

	"github.com/juliusplatzer/reds/aviation"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

const zIAPChart renderer.Z = -895 // Above videomaps, below radar data blocks.

type iapResult struct {
	id    string
	chart *aviation.IAPChart
	err   error
}

type iapChartOverlay struct {
	requestedID    string
	selected       bool
	result         <-chan iapResult
	tex            renderer.TextureID
	points         []renderer.TexturedVertex
	indices        []uint32
	owner          renderer.Renderer
	opacityPercent int32
}

// SetIAPChartSelection does not download/rasterize on the UI/GL thread.
func (p *STARSPane) SetIAPChartSelection(id, cycle string, enabled bool, opacityPercent int32) {
	if p == nil {
		return
	}
	// This is a draw-time property: changing it must not restart the PDF load.
	if opacityPercent < 0 {
		opacityPercent = 0
	} else if opacityPercent > 100 {
		opacityPercent = 100
	}
	p.iapChart.opacityPercent = opacityPercent
	id = strings.ToUpper(strings.TrimSpace(id))
	if !enabled {
		id = ""
	}
	if p.iapChart.requestedID == id && p.iapChart.selected == enabled {
		return
	}
	p.iapChart.release()
	p.iapChart.requestedID = id
	p.iapChart.selected = enabled
	if id == "" {
		return
	}
	ch := make(chan iapResult, 1)
	p.iapChart.result = ch
	go func() {
		chart, err := aviation.FetchIAPChart(context.Background(), id, cycle)
		ch <- iapResult{id: id, chart: chart, err: err}
	}()
}

func (o *iapChartOverlay) release() {
	if o.owner != nil && o.tex != 0 {
		o.owner.DestroyTexture(o.tex)
	}
	o.tex = 0
	o.owner = nil
	o.points = nil
	o.indices = nil
	o.result = nil
}

func (p *STARSPane) drawIAPChartOverlay(ctx *panes.Context, zcb *renderer.ZCmdBuffer, transforms radar.LatLonTransformations) {
	if p == nil || ctx == nil || ctx.Renderer == nil || zcb == nil {
		return
	}
	o := &p.iapChart
	if o.result != nil {
		select {
		case result := <-o.result:
			o.result = nil
			if result.err != nil {
				if p.logger != nil {
					p.logger.Warn("IAP chart render failed", "chart_id", result.id, "error", result.err)
				}
			} else if result.id == o.requestedID && o.selected {
				o.tex = ctx.Renderer.CreateTextureFromImage(result.chart.Image, false)
				o.owner = ctx.Renderer
				o.points = make([]renderer.TexturedVertex, len(result.chart.Vertices))
				for i, v := range result.chart.Vertices {
					o.points[i] = renderer.TexturedVertex{X: v.Longitude, Y: v.Latitude, U: v.U, V: v.V}
				}
				o.indices = result.chart.Indices
			}
		default:
		}
	}
	if !o.selected || o.tex == 0 {
		return
	}
	x, y, w, h := ctx.PaneFramebufferRect()
	cb := zcb.At(zIAPChart)
	cb.Viewport(x, y, w, h)
	cb.Scissor(x, y, w, h)
	transforms.LoadGeoViewingMatrices(cb)
	cb.SetRGBA(renderer.RGBA{R: 1, G: 1, B: 1, A: float32(o.opacityPercent) / 100})
	cb.DrawTexturedTriangles(o.tex, o.points, o.indices)
	cb.DisableScissor()
}
