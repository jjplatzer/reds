// platform/glfw.go
//
// glfwPlatform implements Platform using GLFW for windowing and the
// cimgui-go GLFW + OpenGL3 backends for Dear ImGui. It is adapted from
// vice's platform/glfw.go, stripped down to the single-window menu case:
// we let the ImGui GLFW backend install its own input callbacks
// (InitForOpenGL with install_callbacks=true) instead of chaining our own,
// so there is no manual mouse/keyboard forwarding to maintain yet.

package platform

import (
	"fmt"
	"runtime"
	"unsafe"

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

	// install_callbacks=true lets the backend handle mouse, keyboard, scroll,
	// and character input on its own. The menu needs nothing custom here.
	implglfw.InitForOpenGL(implWindow, true)
	implogl3.InitV("#version 330 core")

	// v-sync on.
	glfw.SwapInterval(1)

	return &glfwPlatform{
		window:  window,
		imguiIO: io,
		config:  config,
	}, nil
}

func (g *glfwPlatform) ShouldStop() bool {
	return g.window.ShouldClose()
}

func (g *glfwPlatform) ProcessEvents() {
	glfw.PollEvents()
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
