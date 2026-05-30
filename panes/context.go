package panes

import (
	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"
)

// Context is the frame-local drawing/input context passed to a pane. It is the
// REDS equivalent of vice's panes.Context: the pane can read geometry, DPI and
// input from here and write draw commands into the supplied ZCmdBuffer.
type Context struct {
	// PaneRect is the drawable pane area in logical window coordinates, with a
	// top-left origin and y increasing downward. Mouse coordinates passed to the
	// pane are pane-local.
	PaneRect redsmath.Rect

	// DisplayRect is the full GLFW client area in logical window coordinates.
	DisplayRect redsmath.Rect

	DisplaySize     [2]float32
	FramebufferSize [2]float32

	// DPIScale is framebuffer pixels per logical display unit. DrawPixelScale is
	// kept separate because later scopes may intentionally render at a fixed
	// virtual resolution while still using the platform DPI for ImGui.
	DPIScale       float32
	DrawPixelScale float32

	// Mouse and Keyboard are nil when a higher-level UI such as ImGui captured
	// that input for the current frame.
	Mouse    *platform.MouseState
	Keyboard *platform.KeyboardState

	Platform platform.Platform
	Renderer renderer.Renderer
}

// PaneSize returns the logical size of the drawable pane.
func (ctx *Context) PaneSize() redsmath.Vec2 { return ctx.PaneRect.Size() }

// ScreenProjection returns a top-left-origin orthographic projection in pane
// coordinates. Drawing helpers can load this before emitting screen-space UI.
func (ctx *Context) ScreenProjection() renderer.Mat4 {
	return renderer.ScreenOrtho(ctx.PaneRect.Width(), ctx.PaneRect.Height())
}

// FullScreenProjection returns a top-left-origin projection for the entire
// GLFW client area.
func (ctx *Context) FullScreenProjection() renderer.Mat4 {
	return renderer.ScreenOrtho(ctx.DisplayRect.Width(), ctx.DisplayRect.Height())
}

// LogicalToFramebufferRect converts a logical top-left-origin rectangle to the
// lower-left-origin pixel rectangle expected by glViewport/glScissor.
func (ctx *Context) LogicalToFramebufferRect(r redsmath.Rect) (x, y, w, h int) {
	if ctx.DisplaySize[0] <= 0 || ctx.DisplaySize[1] <= 0 {
		return 0, 0, 0, 0
	}
	sx := ctx.FramebufferSize[0] / ctx.DisplaySize[0]
	sy := ctx.FramebufferSize[1] / ctx.DisplaySize[1]

	x = int(r.Min.X * sx)
	w = int(r.Width() * sx)
	h = int(r.Height() * sy)
	// OpenGL viewport/scissor y is measured from the bottom edge.
	y = int((ctx.DisplaySize[1] - r.Max.Y) * sy)
	return x, y, w, h
}

// PaneFramebufferRect returns the OpenGL viewport/scissor rectangle for the
// current pane.
func (ctx *Context) PaneFramebufferRect() (x, y, w, h int) {
	return ctx.LogicalToFramebufferRect(ctx.PaneRect)
}
