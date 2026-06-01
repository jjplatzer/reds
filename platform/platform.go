// platform/platform.go
//
// The platform package abstracts the windowing system, the OpenGL context,
// and the Dear ImGui backends behind a small interface, mirroring the shape
// of vice's platform package (github.com/mmp/vice/platform).
//
// This is deliberately a trimmed-down version: it has exactly what the
// startup menu and first ASDE-X scope need: a window, the ImGui GLFW + OpenGL3
// backends, input via the backend's installed callbacks, frame lifecycle, and
// native cursor overrides. Audio, speech, mouse capture, and multi-viewport
// support remain in vice's fuller version.

package platform

// Platform abstracts platform-specific features: creating a window, running
// the ImGui frame lifecycle, and presenting rendered frames.
type Platform interface {
	// ShouldStop reports whether the user has asked to close the window.
	ShouldStop() bool

	// ProcessEvents pumps the windowing system's event queue. It must be
	// called once at the top of each frame, before NewFrame.
	ProcessEvents()

	// NewFrame marks the beginning of a render pass and advances the ImGui
	// GLFW + OpenGL3 backends. Call imgui.NewFrame() immediately after.
	NewFrame()

	// Clear resets the framebuffer to the given color (0..1 components). The
	// menu's single ImGui window covers the whole client area, so this color
	// is only visible in the (normally zero-area) margins; we still clear to
	// the dialog background so nothing flashes during resize.
	Clear(r, g, b float32)

	// PostRender presents the frame (swaps the front and back buffers). The
	// caller is responsible for imgui.Render() and RenderDrawData before this,
	// matching vice's split between drawing and presenting.
	PostRender()

	// SetWindowTitle sets the OS window title.
	SetWindowTitle(title string)

	// SetWindowSizeCentered resizes the OS window while keeping its current
	// center point fixed.
	SetWindowSizeCentered(width, height int)

	// LoadCursorFromBytes decodes a Windows .cur resource and creates a native
	// cursor suitable for the current display scale.
	LoadCursorFromBytes(name string, data []byte) (*Cursor, error)

	// Cursor overrides are frame-local pane decisions. Panes clear an old
	// override before drawing and apply the current override during Draw.
	SetCursorOverride(cursor *Cursor)
	SetCursorHiddenOverride()
	ClearCursorOverride()

	// DisplaySize returns the window size in screen (logical) coordinates.
	DisplaySize() [2]float32

	// FramebufferSize returns the size of the framebuffer in pixels. On a
	// HiDPI/Retina display this is larger than DisplaySize.
	FramebufferSize() [2]float32

	// WindowSize returns the window size in screen coordinates as ints.
	WindowSize() [2]int

	// GetMouse returns the current frame's mouse state in logical window
	// coordinates.
	GetMouse() MouseState

	// GetKeyboard returns the current frame's tracked keyboard state.
	GetKeyboard() KeyboardState

	// DPIScale is the framebuffer-to-window scale factor (1.0 on a standard
	// display, 2.0 on a typical Retina display).
	DPIScale() float32

	// Dispose tears down the ImGui backends, the window, and GLFW.
	Dispose()
}

// Config controls how the window is created.
type Config struct {
	// Title is the OS window title.
	Title string

	// InitialWindowSize is the client-area size in screen coordinates. If
	// either component is zero a sensible default is used.
	InitialWindowSize [2]int

	// MinWindowSize is the smallest allowed client-area size in screen
	// coordinates. If either component is zero, no minimum is applied.
	MinWindowSize [2]int

	// InitialWindowPosition is the top-left position in screen coordinates.
	// Negative or out-of-bounds values are clamped to a safe default.
	InitialWindowPosition [2]int

	// Resizable controls whether the user can resize the native window.
	Resizable bool
}
