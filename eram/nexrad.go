package eram

import (
	stdmath "math"

	"github.com/juliusplatzer/reds/cmd/wx"
	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

var (
	nexradBlue = renderer.RGB8(0, 0, 188)
	nexradCyan = renderer.RGB8(0, 188, 174)
)

type nexradCmdBuffers struct {
	moderate *renderer.CmdBuffer
	heavy    *renderer.CmdBuffer
	extreme  *renderer.CmdBuffer
}

func (p *ERAMPane) consumeWxUpdates() {
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

func (p *ERAMPane) rebuildNexradIfNeeded() {
	if p == nil || p.nexradBuiltGeneration == p.nexradGeneration {
		return
	}

	p.releaseNexradCmdBuffers()
	if p.wxGrid != nil {
		p.nexrad = buildNexradCmdBuffers(p.wxGrid)
	}
	p.nexradBuiltGeneration = p.nexradGeneration
}

func (p *ERAMPane) releaseNexradCmdBuffers() {
	if p == nil {
		return
	}

	renderer.ReturnCmdBuffer(p.nexrad.moderate)
	renderer.ReturnCmdBuffer(p.nexrad.heavy)
	renderer.ReturnCmdBuffer(p.nexrad.extreme)
	p.nexrad = nexradCmdBuffers{}
	p.nexradBuiltGeneration = 0
}

func buildNexradCmdBuffers(grid *wx.Grid) nexradCmdBuffers {
	if grid == nil {
		return nexradCmdBuffers{}
	}

	return nexradCmdBuffers{
		moderate: buildNexradLevelCmdBuffer(grid, wx.LevelModerate, renderer.DrawSolid),
		heavy:    buildNexradLevelCmdBuffer(grid, wx.LevelHeavy, renderer.DrawCheckered),
		extreme:  buildNexradLevelCmdBuffer(grid, wx.LevelExtreme, renderer.DrawSolid),
	}
}

func buildNexradLevelCmdBuffer(grid *wx.Grid, level wx.Level, mode renderer.DrawMode) *renderer.CmdBuffer {
	rects := wx.MergeLevelRectangles(grid, level)
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

func (p *ERAMPane) drawNexrad(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || p.wxGrid == nil || p.nexradLevels <= 0 {
		return
	}

	paneExtent := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	transforms := radar.GetLatLonTransformations(
		paneExtent,
		p.center.Lat,
		p.center.Lon,
		p.rangeNM,
	)

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zNexrad)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	transforms.LoadGeoViewingMatrices(cb)
	offsetX, offsetY := checkerOffset(ctx, transforms, x, y, width, height)
	cb.SetCheckerOffset(offsetX, offsetY)

	if p.nexradLevels == 3 {
		cb.SetRGB(applyERAMBrightness(nexradBlue, p.nexradBrightness, p.systemBrightness))
		cb.Call(p.nexrad.moderate)
	}
	if p.nexradLevels >= 2 {
		cb.SetRGB(applyERAMBrightness(nexradCyan, p.nexradBrightness, p.systemBrightness))
		cb.Call(p.nexrad.heavy)
	}
	if p.nexradLevels >= 1 {
		cb.SetRGB(applyERAMBrightness(nexradCyan, p.nexradBrightness, p.systemBrightness))
		cb.Call(p.nexrad.extreme)
	}

	cb.DisableScissor()
}

func checkerOffset(
	ctx *panes.Context,
	transforms radar.LatLonTransformations,
	viewportX int,
	viewportY int,
	viewportWidth int,
	viewportHeight int,
) (float32, float32) {
	if ctx == nil || ctx.PaneRect.Width() <= 0 || ctx.PaneRect.Height() <= 0 {
		return 0, 0
	}

	zero := transforms.WindowFromLatLon(0, 0)
	scaleX := float32(viewportWidth) / ctx.PaneRect.Width()
	scaleY := float32(viewportHeight) / ctx.PaneRect.Height()

	fbX := float32(viewportX) + zero.X*scaleX
	fbY := float32(viewportY) + (ctx.PaneRect.Height()-zero.Y)*scaleY

	return positiveMod8(-fbX), positiveMod8(fbY)
}

func positiveMod8(value float32) float32 {
	out := float32(stdmath.Mod(float64(value), 8))
	if out < 0 {
		out += 8
	}
	return out
}
