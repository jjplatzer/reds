package panes

import (
	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"
)

// DrawOptions controls frame-local pane routing. The caller decides whether
// ImGui captured mouse/keyboard for this frame and passes that in here.
type DrawOptions struct {
	MenuBarHeight float32

	MouseCaptured    bool
	KeyboardCaptured bool

	// DrawPixelScale defaults to the platform DPI scale when zero.
	DrawPixelScale float32
}

// NewContext builds a frame-local Context for pane drawing.
func NewContext(
	plat platform.Platform,
	r renderer.Renderer,
	opts DrawOptions,
) Context {
	display := plat.DisplaySize()
	framebuffer := plat.FramebufferSize()
	dpi := plat.DPIScale()
	drawScale := opts.DrawPixelScale
	if drawScale == 0 {
		drawScale = dpi
	}

	displayRect := redsmath.RectFromSize(display[0], display[1])
	paneRect := redsmath.NewRect(0, opts.MenuBarHeight, display[0], display[1])

	ctx := Context{
		PaneRect:        paneRect,
		DisplayRect:     displayRect,
		DisplaySize:     display,
		FramebufferSize: framebuffer,
		DPIScale:        dpi,
		DrawPixelScale:  drawScale,
		Platform:        plat,
		Renderer:        r,
	}

	if !opts.MouseCaptured {
		mouse := plat.GetMouse()
		mouse.Pos = mouse.Pos.Sub(paneRect.Min)
		ctx.Mouse = &mouse
	}
	if !opts.KeyboardCaptured {
		keyboard := plat.GetKeyboard()
		ctx.Keyboard = &keyboard
	}

	return ctx
}

// DrawPane creates a Context, obtains a frame-level ZCmdBuffer, invokes the
// pane, renders the z-ordered command buffers, and returns the backend stats.
func DrawPane(
	pane Pane,
	plat platform.Platform,
	r renderer.Renderer,
	opts DrawOptions,
) renderer.RendererStats {
	var stats renderer.RendererStats
	if pane == nil || plat == nil || r == nil {
		return stats
	}
	fb := plat.FramebufferSize()
	if fb[0] <= 0 || fb[1] <= 0 {
		return stats
	}

	ctx := NewContext(plat, r, opts)

	zcb := renderer.GetZCmdBuffer()
	zcb.Reset()
	pane.Draw(&ctx, zcb)
	stats = r.RenderZCmdBuffer(zcb)
	renderer.ReturnZCmdBuffer(zcb)
	return stats
}
