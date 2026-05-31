package asdex

import (
	"fmt"
	stdmath "math"
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

type Mode int

const (
	ModeDay Mode = iota
	ModeNight
)

const (
	brightnessMin          = 1
	brightnessMax          = 99
	brightnessDefault      = 95
	brightnessFloorDefault = 20
)

const (
	zVideoMap            renderer.Z = -900
	zRunwayClosures      renderer.Z = -800
	zSafetyLogicHoldBars renderer.Z = -790

	zRestrictedArea renderer.Z = -700
	zClosedArea     renderer.Z = -690
	zTempMapText    renderer.Z = -680
	zDBAreas        renderer.Z = -600

	zTargets    renderer.Z = -500
	zDatablocks renderer.Z = -480

	zWindowBorders renderer.Z = -300
	zAlertMessage  renderer.Z = -210
	zPreviewArea   renderer.Z = -200
	zPreviewCursor renderer.Z = -190

	zDCBBackground renderer.Z = -100
	zDCBButtons    renderer.Z = -99
	zDCBText       renderer.Z = -98
)

func windowZ(stackIndex int, localZ renderer.Z) renderer.Z {
	return renderer.Z(-10000 + stackIndex*1000 + int(localZ))
}

type ASDEXPane struct {
	airport  string
	mode     Mode
	videomap *VideoMap

	center          redsmath.Vec2
	rangeFeet       float32
	rotation        float32
	viewInitialized bool
}

func NewPane(airport string) (*ASDEXPane, error) {
	airport = strings.ToUpper(strings.TrimSpace(airport))
	if airport == "" {
		return nil, fmt.Errorf("empty ASDE-X airport")
	}

	vm, err := LoadVideoMap(airport)
	if err != nil {
		return nil, err
	}

	return &ASDEXPane{
		airport:  airport,
		mode:     ModeDay,
		videomap: vm,
	}, nil
}

func (p *ASDEXPane) Draw(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if ctx == nil || zcb == nil || p == nil {
		return
	}

	p.initView(ctx.PaneRect)
	if !p.viewInitialized {
		return
	}

	paneExtent := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	transforms := radar.GetScopeTransformations(
		paneExtent,
		p.center,
		p.rangeFeet,
		p.rotation,
	)

	if p.consumeMouseEvents(ctx, transforms) {
		transforms = radar.GetScopeTransformations(
			paneExtent,
			p.center,
			p.rangeFeet,
			p.rotation,
		)
	}

	cb := zcb.At(windowZ(0, zVideoMap))
	x, y, w, h := ctx.PaneFramebufferRect()
	cb.Viewport(x, y, w, h)
	cb.Scissor(x, y, w, h)
	cb.Clear(applyBrightness(backgroundColor(p.mode), brightnessDefault, 20).ToRGBA())

	transforms.LoadWorldViewingMatrices(cb)
	DrawVideoMap(p.videomap, cb, p.mode)
	cb.DisableScissor()
}

func (p *ASDEXPane) initView(rect redsmath.Rect) {
	if p == nil || p.viewInitialized || p.videomap == nil || rect.Empty() {
		return
	}

	bounds := p.videomap.BoundsFeet()
	if bounds.Empty() {
		return
	}

	width := bounds.Width()
	height := bounds.Height()
	if width <= 0 || height <= 0 {
		return
	}

	const margin = float32(1.08)

	aspect := rect.Width() / rect.Height()
	rangeFromHeight := height * margin * 0.5
	rangeFromWidth := (width * margin) / (2 * aspect)

	rangeFeet := rangeFromHeight
	if rangeFromWidth > rangeFeet {
		rangeFeet = rangeFromWidth
	}

	p.center = redsmath.Vec2{
		X: (bounds.Min.X + bounds.Max.X) * 0.5,
		Y: (bounds.Min.Y + bounds.Max.Y) * 0.5,
	}
	p.rangeFeet = rangeFeet
	p.rotation = 0
	p.viewInitialized = true
}

func (p *ASDEXPane) consumeMouseEvents(
	ctx *panes.Context,
	transforms radar.ScopeTransformations,
) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}

	mouse := ctx.Mouse
	changed := false
	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())

	if !paneLocal.Contains(mouse.Pos) && !mouse.IsDown(platform.MouseButtonRight) {
		return false
	}

	if mouse.IsDown(platform.MouseButtonRight) && (mouse.Delta.X != 0 || mouse.Delta.Y != 0) {
		deltaWorld := transforms.WorldFromWindowV(mouse.Delta)
		p.center = p.center.Sub(deltaWorld)
		changed = true
	}

	if mouse.Wheel.Y != 0 && paneLocal.Contains(mouse.Pos) {
		mouseWorld := transforms.WorldFromWindowP(mouse.Pos)

		oldRange := p.rangeFeet
		p.rangeFeet = p.zoomedRange(mouse.Wheel.Y)
		newRange := p.rangeFeet

		if oldRange > 0 && newRange > 0 && newRange != oldRange {
			scale := newRange / oldRange
			p.center = mouseWorld.Add(p.center.Sub(mouseWorld).Mul(scale))
			changed = true
		}
	}

	return changed
}

func (p *ASDEXPane) zoomedRange(wheel float32) float32 {
	if p == nil {
		return 1
	}

	factor := float32(stdmath.Pow(1.12, float64(wheel)))
	if factor <= 0 {
		return p.rangeFeet
	}

	next := p.rangeFeet / factor
	return clamp(next, p.minRangeFeet(), p.maxRangeFeet())
}

func (p *ASDEXPane) minRangeFeet() float32 {
	return 500
}

func (p *ASDEXPane) maxRangeFeet() float32 {
	if p == nil || p.videomap == nil {
		return 100000
	}

	bounds := p.videomap.BoundsFeet()
	if bounds.Empty() {
		return 100000
	}

	maxDim := bounds.Width()
	if bounds.Height() > maxDim {
		maxDim = bounds.Height()
	}

	maxRange := maxDim * 10
	if maxRange < 2000 {
		maxRange = 2000
	}
	return maxRange
}

func clamp(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func backgroundColor(mode Mode) renderer.RGB {
	if mode == ModeDay {
		return renderer.RGB8(0, 96, 120)
	}
	return renderer.RGB8(60, 60, 60)
}

func applyBrightness(color renderer.RGB, brightness int, minBrightness int) renderer.RGB {
	if brightness < brightnessMin {
		brightness = brightnessMin
	}
	if brightness > brightnessMax {
		brightness = brightnessMax
	}
	if minBrightness < 0 {
		minBrightness = 0
	}
	if minBrightness > 100 {
		minBrightness = 100
	}

	scale := (float32(brightness)*(100-float32(minBrightness))/100 + float32(minBrightness)) / 100
	return renderer.RGB{R: color.R * scale, G: color.G * scale, B: color.B * scale}
}
