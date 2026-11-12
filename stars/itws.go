package stars

import (
	"log/slog"
	"math"
	"time"

	redsnet "github.com/juliusplatzer/reds/net"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

type starsITWSWeather struct {
	client     *redsnet.ITWSClient
	generation uint64
	buffers    [6]starsNexradLevelCmdBuffers
}

// UseNexradWeather is a per-window REDS source choice, outside STARS preference
// sets. ITWS is the default. Sources are exclusive, including clear areas.
func (p *STARSPane) UseNexradWeather() bool {
	return p != nil && p.useNexradWeather
}

func (p *STARSPane) ToggleNexradWeather() {
	if p == nil {
		return
	}
	p.closeWeather()
	p.useNexradWeather = !p.useNexradWeather
	p.startWeather()
}

func (p *STARSPane) startWeather() {
	if p.useNexradWeather {
		p.restartNexradStream(starsInitialNexradRadiusNM)
	} else {
		p.itwsWeather.client = redsnet.NewITWSClient("", p.config.Facility.Facility,
			p.logger.With(slog.String("component", "itws")))
		p.itwsWeather.client.Start()
	}
}

func (p *STARSPane) closeWeather() {
	p.itwsWeather.client.Close()
	p.itwsWeather.client = nil
	p.releaseITWSCmdBuffers()
	if p.wxStream != nil {
		p.wxStream.Close()
		p.wxStream = nil
	}
	p.releaseNexradCmdBuffers()
	p.wxGrid = nil
}

func (p *STARSPane) updateWeather(ctx *panes.Context) {
	if p.useNexradWeather {
		p.consumeNexradUpdates()
		p.ensureNexradCoverage(ctx)
		p.rebuildNexradIfNeeded()
		return
	}
	state := &p.itwsWeather
	products, generation := state.client.Snapshot(state.generation, time.Now())
	if generation == state.generation {
		return
	}
	p.releaseITWSCmdBuffers()
	state.buffers = buildStarsITWSCmdBuffers(products, p.wxPresentation().stipple)
	state.generation = generation
}

func (p *STARSPane) drawWeather(ctx *panes.Context, zcb *renderer.ZCmdBuffer, transforms radar.LatLonTransformations) {
	if p.useNexradWeather {
		p.drawNexrad(ctx, zcb, transforms)
	} else {
		p.drawWeatherBuffers(ctx, zcb, transforms, p.itwsWeather.buffers)
	}
}

func (p *STARSPane) releaseITWSCmdBuffers() {
	for i, buffers := range p.itwsWeather.buffers {
		renderer.ReturnCmdBuffer(buffers.fill)
		renderer.ReturnCmdBuffer(buffers.stipple)
		p.itwsWeather.buffers[i] = starsNexradLevelCmdBuffers{}
	}
	p.itwsWeather.generation = 0
}

// itwsGridProjection implements the supplied PNG convention in metres: row 0
// is the top row, columns run left to right, and positive rotation is CCW about
// the ORIGINAL source buffer's centre. No raster resampling or flipping is
// needed: project the cell edges directly, retaining the server's crop stride.
//
// Georeferencing assumption: x/y offsets locate the unrotated buffer's lower
// left edge in the TRP tangent plane. Use spherical stereographic with unit
// scale at the TRP (mean Earth radius). These choices reproduce the supplied
// PNG, which was not checked against a map. Production ITWS row direction,
// rotation sign/pivot, datum and offset conventions still need an ICD or a
// georeferenced reference. Keep these assumptions here, separate from STARS'
// independently applied scope/magnetic rotation.
type itwsGridProjection struct {
	grid                                *redsnet.ITWSGrid
	cx, cy, halfWidth, halfHeight       float64
	sinRotation, cosRotation            float64
	sinLatitude, cosLatitude, longitude float64
}

func newITWSGridProjection(grid *redsnet.ITWSGrid) itwsGridProjection {
	halfWidth := float64(grid.SourceBufferColumns) * grid.DXMeters / 2
	halfHeight := float64(grid.SourceBufferRows) * grid.DYMeters / 2
	sinRotation, cosRotation := math.Sincos(grid.RotationDegrees * math.Pi / 180)
	sinLatitude, cosLatitude := math.Sincos(grid.TRPLatitude * math.Pi / 180)
	return itwsGridProjection{grid: grid, cx: grid.XOffsetMeters + halfWidth,
		cy: grid.YOffsetMeters + halfHeight, halfWidth: halfWidth, halfHeight: halfHeight,
		sinRotation: sinRotation, cosRotation: cosRotation,
		sinLatitude: sinLatitude, cosLatitude: cosLatitude, longitude: grid.TRPLongitude * math.Pi / 180}
}

func (p itwsGridProjection) meters(column, row float64) (east, north float64) {
	x := column*p.grid.DXMeters - p.halfWidth
	y := p.halfHeight - row*p.grid.DYMeters
	return p.cx + p.cosRotation*x - p.sinRotation*y, p.cy + p.sinRotation*x + p.cosRotation*y
}

func (p itwsGridProjection) latLon(column, row float64) (lat, lon float64) {
	x, y := p.meters(column, row)
	return p.inverse(x, y)
}

func (p itwsGridProjection) inverse(x, y float64) (lat, lon float64) {
	const earthRadiusMeters = 6371008.8
	rho := math.Hypot(x, y)
	if rho == 0 {
		return p.grid.TRPLatitude, p.grid.TRPLongitude
	}
	sinC, cosC := math.Sincos(2 * math.Atan2(rho, 2*earthRadiusMeters))
	lat = math.Asin(math.Max(-1, math.Min(1, cosC*p.sinLatitude+y*sinC*p.cosLatitude/rho)))
	lon = p.longitude + math.Atan2(x*sinC, rho*p.cosLatitude*cosC-y*p.sinLatitude*sinC)
	// Keep longitude continuous around this TRP, avoiding quads that span the
	// whole map at the antimeridian. The scope uses the same geographic frame.
	return lat * 180 / math.Pi, lon * 180 / math.Pi
}

func (p itwsGridProjection) vertex(column, row int) renderer.PointVertex {
	lat, lon := p.latLon(float64(column), float64(row))
	return renderer.PointVertex{X: float32(lon), Y: float32(lat)}
}

func buildStarsITWSCmdBuffers(products []*redsnet.ITWSProduct, stipple [6]int) [6]starsNexradLevelCmdBuffers {
	var out [6]starsNexradLevelCmdBuffers
	var builders [6]*renderer.TrianglesBuilder
	for i := range builders {
		builders[i] = renderer.GetTrianglesBuilder()
		defer renderer.ReturnTrianglesBuilder(builders[i])
	}
	var populated [6]bool
	for _, product := range products {
		g := &product.Grid
		projection := newITWSGridProjection(g)
		// Reuse identical projected vertices along shared cell edges. Merging
		// different-length row runs would introduce T-junctions on curved grid
		// lines and leave hairline gaps. Two corner rows bound temporary memory;
		// all geometry remains cached across frames, pan, zoom and rotation.
		top := make([]renderer.PointVertex, g.Columns+1)
		bottom := make([]renderer.PointVertex, g.Columns+1)
		for column := range top {
			top[column] = projection.vertex(column, 0)
		}
		for row := 0; row < g.Rows; row++ {
			for column := range bottom {
				bottom[column] = projection.vertex(column, row+1)
			}
			for column := 0; column < g.Columns; column++ {
				value := g.Cells[row*g.Columns+column]
				if value < 1 || value > 6 {
					continue
				}
				builders[value-1].AddQuad(top[column], top[column+1], bottom[column+1], bottom[column])
				populated[value-1] = true
			}
			top, bottom = bottom, top
		}
	}
	// All sources share each level buffer; the existing low-to-high drawing
	// order presents the highest enabled precipitation level in overlaps.
	// Clear, no-coverage and diagnostic values never become precipitation.
	for i, builder := range builders {
		if !populated[i] {
			continue
		}
		out[i].fill = renderer.GetCmdBuffer()
		builder.GenerateCommands(out[i].fill, renderer.DrawSolid, 0)
		var mode renderer.DrawMode
		switch stipple[i] {
		case 1:
			mode = renderer.DrawStippleLight
		case 2:
			mode = renderer.DrawStippleDense
		case 3:
			mode = renderer.DrawStippleFAAHFSTD010A
		default:
			continue
		}
		out[i].stipple = renderer.GetCmdBuffer()
		builder.GenerateCommands(out[i].stipple, mode, 0)
	}
	return out
}
