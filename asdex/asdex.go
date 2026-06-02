package asdex

import (
	"fmt"
	stdmath "math"
	"os"
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	redsnet "github.com/juliusplatzer/reds/net"
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
	targets  TargetStore
	smes     *redsnet.SmesClient
	fonts    fontCache

	cursors    CursorSet
	cursorMode CursorMode

	datablockSettings DataBlockSettings
	previewArea       PreviewArea

	highlightedTargetID    string
	highlightMouseWorld    redsmath.Vec2
	highlightStoreRevision uint64
	highlightQueryValid    bool

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

	fonts, err := loadFontCache()
	if err != nil {
		return nil, err
	}

	preview := NewPreviewArea()
	if err := preview.LoadDefaultStateFromAirportConfig(airport); err != nil {
		fmt.Fprintf(os.Stderr, "reds: %v\n", err)
	}
	preview.SetSystemResponse("CRITICAL FAULT START")

	client := redsnet.NewSmesClient(targetWebSocketURL())
	client.SetAirport(airport)
	client.Start()

	return &ASDEXPane{
		airport:  airport,
		mode:     ModeDay,
		videomap: vm,
		targets:  NewTargetStore(),
		smes:     client,
		fonts:    fonts,

		datablockSettings: DefaultDataBlockSettings(),
		previewArea:       preview,
	}, nil
}

func (p *ASDEXPane) Draw(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if ctx == nil || zcb == nil || p == nil {
		return
	}

	p.ensureCursorsLoaded(ctx)
	p.consumeNetworkEvents()
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
	p.updateHighlightedTarget(ctx, transforms)
	p.applyCurrentCursor(ctx)

	cb := zcb.At(windowZ(0, zVideoMap))
	x, y, w, h := ctx.PaneFramebufferRect()
	cb.Viewport(x, y, w, h)
	cb.Scissor(x, y, w, h)
	cb.Clear(applyBrightness(backgroundColor(p.mode), brightnessDefault, 20).ToRGBA())

	transforms.LoadWorldViewingMatrices(cb)
	DrawVideoMap(p.videomap, cb, p.mode)
	cb.DisableScissor()

	targetCB := zcb.At(windowZ(0, zTargets))
	targetCB.Viewport(x, y, w, h)
	targetCB.Scissor(x, y, w, h)
	transforms.LoadWorldViewingMatrices(targetCB)
	DrawTargets(
		p.targets.All(),
		p.targets.History(),
		targetCB,
		TargetDrawOptions{
			VectorSeconds: 3,
			Brightness:    brightnessDefault,
		},
	)
	targetCB.DisableScissor()

	dbCB := zcb.At(windowZ(0, zDatablocks))
	dbCB.Viewport(x, y, w, h)
	dbCB.Scissor(x, y, w, h)
	DrawDatablocks(
		p.targets.All(),
		dbCB,
		transforms,
		DataBlockDrawOptions{
			Font: p.fonts.font,
			FontTextureForSize: func(size int) renderer.TextureID {
				return p.fonts.textureForSize(ctx.Renderer, size)
			},
			SettingsForTarget: func(_ *Target) DataBlockSettings {
				return p.datablockSettings
			},
		},
	)
	dbCB.DisableScissor()

	previewCB := zcb.At(windowZ(0, zPreviewArea))
	previewCB.Viewport(x, y, w, h)
	previewCB.Scissor(x, y, w, h)
	transforms.LoadWindowViewingMatrices(previewCB)

	textureID := p.fonts.textureForSize(ctx.Renderer, p.previewArea.FontSize())
	if textureID != 0 {
		td := renderer.GetTextDrawBuilder()
		td.SetFont(p.fonts.font)
		p.previewArea.Render(td, p.fonts.font, ctx.PaneSize())
		td.GenerateCommands(previewCB, textureID)
		renderer.ReturnTextDrawBuilder(td)
	}
	previewCB.DisableScissor()
}

func (p *ASDEXPane) ensureCursorsLoaded(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Platform == nil || p.cursors.loaded {
		return
	}
	if err := p.cursors.Load(ctx.Platform); err != nil {
		fmt.Fprintf(os.Stderr, "reds: %v\n", err)
	}
}

func (p *ASDEXPane) applyCurrentCursor(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Platform == nil || ctx.Mouse == nil {
		return
	}

	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	if !paneLocal.Contains(ctx.Mouse.Pos) {
		return
	}
	p.applyCursorMode(ctx, p.resolveCursorMode(ctx))
}

func (p *ASDEXPane) resolveCursorMode(ctx *panes.Context) CursorMode {
	if ctx != nil && ctx.Mouse != nil && ctx.Mouse.IsDown(platform.MouseButtonRight) {
		return CursorModeHidden
	}
	return CursorModeScope
}

func (p *ASDEXPane) applyCursorMode(ctx *panes.Context, mode CursorMode) {
	if p == nil || ctx == nil || ctx.Platform == nil {
		return
	}

	p.cursorMode = mode
	cursor, hidden := p.cursors.CursorForMode(mode)
	if hidden {
		ctx.Platform.SetCursorHiddenOverride()
		return
	}
	if cursor != nil {
		ctx.Platform.SetCursorOverride(cursor)
	}
}

func (p *ASDEXPane) updateHighlightedTarget(
	ctx *panes.Context,
	transforms radar.ScopeTransformations,
) {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		p.clearHighlightedTarget()
		return
	}

	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	if !paneLocal.Contains(ctx.Mouse.Pos) {
		p.clearHighlightedTarget()
		return
	}

	mouseWorld := transforms.WorldFromWindowP(ctx.Mouse.Pos)
	storeRevision := p.targets.HoverRevision()
	if p.highlightQueryValid &&
		p.highlightMouseWorld == mouseWorld &&
		p.highlightStoreRevision == storeRevision {
		return
	}

	p.highlightedTargetID = p.targets.HighlightNearest(mouseWorld)
	p.highlightMouseWorld = mouseWorld
	p.highlightStoreRevision = storeRevision
	p.highlightQueryValid = true
}

func (p *ASDEXPane) clearHighlightedTarget() {
	if p == nil {
		return
	}

	if !p.highlightQueryValid && p.highlightedTargetID == "" {
		return
	}

	p.highlightedTargetID = ""
	p.highlightQueryValid = false
	p.targets.ClearHighlight()
}

func targetWebSocketURL() string {
	if value := os.Getenv("REDS_TARGET_WS_URL"); value != "" {
		return value
	}
	if value := os.Getenv("NASCOPE_TARGET_WS_URL"); value != "" {
		return value
	}
	port := os.Getenv("WS_PORT")
	if port == "" {
		port = "8080"
	}
	return "ws://localhost:" + port + "/ws"
}

func (p *ASDEXPane) consumeNetworkEvents() {
	if p == nil || p.smes == nil {
		return
	}

	for {
		select {
		case status := <-p.smes.Status():
			p.applySmesStatus(status)
		case frame := <-p.smes.Frames():
			if !frame.Removed && frame.Airport != "" && !strings.EqualFold(frame.Airport, p.airport) {
				continue
			}
			p.targets.ApplySmesFrame(frame, p.videomap)
		default:
			return
		}
	}
}

func (p *ASDEXPane) applySmesStatus(status redsnet.SmesStatusEvent) {
	if p == nil {
		return
	}

	switch status.Status {
	case redsnet.SmesStatusConnected:
		p.previewArea.SetSystemResponse("CRITICAL FAULT END")
	case redsnet.SmesStatusDisconnected:
		p.previewArea.SetSystemResponse("CRITICAL FAULT START")
	}
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
		oldRange := p.rangeFeet
		p.rangeFeet = p.zoomedRange(mouse.Wheel.Y)
		newRange := p.rangeFeet

		if oldRange > 0 && newRange > 0 && newRange != oldRange {
			if ctx.Keyboard != nil && ctx.Keyboard.IsDown(platform.KeyAlt) {
				mouseWorld := transforms.WorldFromWindowP(mouse.Pos)
				scale := newRange / oldRange
				p.center = mouseWorld.Add(p.center.Sub(mouseWorld).Mul(scale))
			}
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
