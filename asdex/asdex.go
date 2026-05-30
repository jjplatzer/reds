package asdex

import (
	"fmt"
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
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

	cb := zcb.At(windowZ(0, zVideoMap))
	x, y, w, h := ctx.PaneFramebufferRect()
	cb.Viewport(x, y, w, h)
	cb.Scissor(x, y, w, h)
	cb.LoadProjectionMatrix(p.videoMapProjection(ctx.PaneRect))
	cb.Clear(applyBrightness(backgroundColor(p.mode), brightnessDefault, 20).ToRGBA())

	DrawVideoMap(p.videomap, cb, p.mode)
	cb.DisableScissor()
}

func (p *ASDEXPane) videoMapProjection(rect redsmath.Rect) renderer.Mat4 {
	if p == nil || p.videomap == nil || !p.videomap.IsValid() || rect.Empty() {
		return renderer.Identity()
	}

	bounds := p.videomap.BoundsFeet()
	width := bounds.Width()
	height := bounds.Height()
	if width <= 0 || height <= 0 {
		return renderer.Identity()
	}

	// Static first view: fit the whole airport into the pane with a small
	// margin. Pan/zoom will later replace this with ScopeView state.
	const margin = float32(1.08)
	cx := (bounds.Min.X + bounds.Max.X) * 0.5
	cy := (bounds.Min.Y + bounds.Max.Y) * 0.5

	paneAspect := rect.Width() / rect.Height()
	mapAspect := width / height

	viewW := width * margin
	viewH := height * margin
	if paneAspect > mapAspect {
		viewW = viewH * paneAspect
	} else {
		viewH = viewW / paneAspect
	}

	left := cx - viewW*0.5
	right := cx + viewW*0.5
	bottom := cy - viewH*0.5
	top := cy + viewH*0.5
	return renderer.Ortho(left, right, bottom, top, -1, 1)
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
