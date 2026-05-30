// platform/glfw.go
//
// glfwPlatform implements Platform using GLFW for windowing and the
// cimgui-go GLFW + OpenGL3 backends for Dear ImGui. It is adapted from
// vice's platform/glfw.go, stripped down to the single-window menu/scope case.
// ImGui still owns the primary GLFW callbacks; REDS polls mouse/buttons/keys
// and chains the scroll callback so panes can receive frame-local input.

package platform

import (
	"fmt"
	"runtime"
	"unsafe"

	redsmath "github.com/juliusplatzer/reds/math"

	"github.com/AllenDang/cimgui-go/imgui"
	implglfw "github.com/AllenDang/cimgui-go/impl/glfw"
	implogl3 "github.com/AllenDang/cimgui-go/impl/opengl3"
	"github.com/go-gl/gl/v3.3-core/gl"
	"github.com/go-gl/glfw/v3.3/glfw"
)

func init() {
	// GLFW and OpenGL must be driven from a single OS thread.
	runtime.LockOSThread()
}

type glfwPlatform struct {
	window  *glfw.Window
	imguiIO *imgui.IO
	config  *Config

	mouse             MouseState
	prevMouseButtons  [MouseButtonCount]bool
	pendingMouseWheel redsmath.Vec2
	inputInitialized  bool

	keyboard    KeyboardState
	prevKeyDown map[Key]bool
}

// New creates the window and wires up the ImGui backends. An ImGui context
// must already exist (imgui.CreateContext) before calling New, because we
// touch imgui.CurrentIO() here — same ordering as vice.
func New(config *Config) (Platform, error) {
	if err := glfw.Init(); err != nil {
		return nil, fmt.Errorf("failed to initialize glfw: %w", err)
	}

	io := imgui.CurrentIO()
	io.SetBackendFlags(io.BackendFlags() | imgui.BackendFlagsHasMouseCursors)

	// Request an OpenGL 3.3 core context. The scope renderer is shader-based and
	// follows the existing NAScope OpenGL 3.3 renderer rather than vice's older
	// OpenGL 2.1 backend.
	glfw.WindowHint(glfw.ContextVersionMajor, 3)
	glfw.WindowHint(glfw.ContextVersionMinor, 3)
	glfw.WindowHint(glfw.OpenGLProfile, glfw.OpenGLCoreProfile)
	glfw.WindowHint(glfw.OpenGLForwardCompatible, glfw.True)

	vm := glfw.GetPrimaryMonitor().GetVideoMode()

	size := config.InitialWindowSize
	if size[0] == 0 || size[1] == 0 {
		size = [2]int{380, 320}
	}

	pos := config.InitialWindowPosition
	if pos[0] <= 0 || pos[1] <= 0 || pos[0] > vm.Width || pos[1] > vm.Height {
		// Center on the primary monitor.
		pos = [2]int{(vm.Width - size[0]) / 2, (vm.Height - size[1]) / 2}
	}

	// Create the window invisible so we can position it before showing.
	glfw.WindowHint(glfw.Visible, glfw.False)
	glfw.WindowHint(glfw.AutoIconify, glfw.False)
	if config.Resizable {
		glfw.WindowHint(glfw.Resizable, glfw.True)
	} else {
		glfw.WindowHint(glfw.Resizable, glfw.False)
	}

	title := config.Title
	if title == "" {
		title = "nascope"
	}

	window, err := glfw.CreateWindow(size[0], size[1], title, nil, nil)
	if err != nil {
		glfw.Terminate()
		return nil, fmt.Errorf("failed to create window: %w", err)
	}
	if config.Resizable {
		minSize := config.MinWindowSize
		if minSize[0] > 0 && minSize[1] > 0 {
			window.SetSizeLimits(minSize[0], minSize[1], glfw.DontCare, glfw.DontCare)
		}
	}
	window.SetPos(pos[0], pos[1])
	window.Show()
	window.MakeContextCurrent()

	if err := gl.Init(); err != nil {
		window.Destroy()
		glfw.Terminate()
		return nil, fmt.Errorf("failed to initialize OpenGL: %w", err)
	}

	// Wire the ImGui GLFW backend to our go-gl/glfw window. The raw
	// *C.GLFWwindow pointer is the first field of go-gl/glfw's Window struct.
	rawPtr := *(*unsafe.Pointer)(unsafe.Pointer(window))
	implWindow := implglfw.NewGLFWwindowFromC(rawPtr)

	// install_callbacks=true lets the backend handle mouse, keyboard, and
	// character input. We chain our scroll callback below so panes can also see
	// wheel deltas without breaking ImGui.
	implglfw.InitForOpenGL(implWindow, true)
	implogl3.InitV("#version 330 core")

	p := &glfwPlatform{
		window:      window,
		imguiIO:     io,
		config:      config,
		prevKeyDown: make(map[Key]bool),
	}

	var previousScroll glfw.ScrollCallback
	previousScroll = window.SetScrollCallback(func(w *glfw.Window, xoff, yoff float64) {
		if previousScroll != nil {
			previousScroll(w, xoff, yoff)
		}
		p.pendingMouseWheel = p.pendingMouseWheel.Add(redsmath.Vec2{X: float32(xoff), Y: float32(yoff)})
	})

	// Prime input so the first frame does not report a huge cursor delta.
	p.updateInput()

	// v-sync on.
	glfw.SwapInterval(1)

	return p, nil
}

func (g *glfwPlatform) ShouldStop() bool {
	return g.window.ShouldClose()
}

func (g *glfwPlatform) ProcessEvents() {
	glfw.PollEvents()
	g.updateInput()
}

func (g *glfwPlatform) NewFrame() {
	implogl3.NewFrame()
	implglfw.NewFrame()
}

func (g *glfwPlatform) Clear(r, gn, b float32) {
	fb := g.FramebufferSize()
	gl.Viewport(0, 0, int32(fb[0]), int32(fb[1]))
	gl.ClearColor(r, gn, b, 1)
	gl.Clear(gl.COLOR_BUFFER_BIT)
}

func (g *glfwPlatform) PostRender() {
	g.window.SwapBuffers()
}

func (g *glfwPlatform) SetWindowTitle(title string) {
	g.window.SetTitle(title)
}

func (g *glfwPlatform) DisplaySize() [2]float32 {
	w, h := g.window.GetSize()
	return [2]float32{float32(w), float32(h)}
}

func (g *glfwPlatform) FramebufferSize() [2]float32 {
	w, h := g.window.GetFramebufferSize()
	return [2]float32{float32(w), float32(h)}
}

func (g *glfwPlatform) WindowSize() [2]int {
	w, h := g.window.GetSize()
	return [2]int{w, h}
}

func (g *glfwPlatform) GetMouse() MouseState {
	return g.mouse
}

func (g *glfwPlatform) GetKeyboard() KeyboardState {
	return g.keyboard
}

func (g *glfwPlatform) updateInput() {
	g.updateMouse()
	g.updateKeyboard()
}

func (g *glfwPlatform) updateMouse() {
	x, y := g.window.GetCursorPos()
	pos := redsmath.Vec2{X: float32(x), Y: float32(y)}
	if !g.inputInitialized {
		g.mouse.Pos = pos
		g.inputInitialized = true
	}

	next := MouseState{
		Pos:   pos,
		Delta: pos.Sub(g.mouse.Pos),
		Wheel: g.pendingMouseWheel,
	}
	g.pendingMouseWheel = redsmath.Vec2{}

	buttons := [...]glfw.MouseButton{
		glfw.MouseButtonLeft,
		glfw.MouseButtonRight,
		glfw.MouseButtonMiddle,
	}
	for i, button := range buttons {
		down := g.window.GetMouseButton(button) == glfw.Press
		next.Down[i] = down
		next.Pressed[i] = down && !g.prevMouseButtons[i]
		next.Released[i] = !down && g.prevMouseButtons[i]
		g.prevMouseButtons[i] = down
	}

	g.mouse = next
}

type trackedKey struct {
	key  Key
	glfw glfw.Key
}

var trackedKeys = []trackedKey{
	{KeyEscape, glfw.KeyEscape},
	{KeyEnter, glfw.KeyEnter},
	{KeyKeypadEnter, glfw.KeyKPEnter},
	{KeyLeft, glfw.KeyLeft},
	{KeyRight, glfw.KeyRight},
	{KeyUp, glfw.KeyUp},
	{KeyDown, glfw.KeyDown},
	{KeyF1, glfw.KeyF1},
	{KeyF2, glfw.KeyF2},
	{KeyF3, glfw.KeyF3},
	{KeyF4, glfw.KeyF4},
	{KeyF5, glfw.KeyF5},
	{KeyF6, glfw.KeyF6},
	{KeyF7, glfw.KeyF7},
	{KeyF8, glfw.KeyF8},
	{KeyF9, glfw.KeyF9},
	{KeyF10, glfw.KeyF10},
	{KeyF11, glfw.KeyF11},
	{KeyF12, glfw.KeyF12},
}

func (g *glfwPlatform) updateKeyboard() {
	down := make(map[Key]bool, len(trackedKeys))
	pressed := make(map[Key]bool)
	released := make(map[Key]bool)

	for _, tk := range trackedKeys {
		isDown := g.window.GetKey(tk.glfw) == glfw.Press
		wasDown := g.prevKeyDown[tk.key]
		if isDown {
			down[tk.key] = true
		}
		if isDown && !wasDown {
			pressed[tk.key] = true
		}
		if !isDown && wasDown {
			released[tk.key] = true
		}
	}

	g.prevKeyDown = down
	g.keyboard = KeyboardState{
		Down:     down,
		Pressed:  pressed,
		Released: released,
	}
}

func (g *glfwPlatform) DPIScale() float32 {
	fb := g.FramebufferSize()
	win := g.DisplaySize()
	if win[0] == 0 {
		return 1
	}
	return fb[0] / win[0]
}

func (g *glfwPlatform) Dispose() {
	implogl3.Shutdown()
	implglfw.Shutdown()
	g.window.Destroy()
	glfw.Terminate()
}
