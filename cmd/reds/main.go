// cmd/reds/main.go
//
// reds entrypoint. Opens a GLFW + Dear ImGui window, shows the startup menu
// and dispatches to the selected scope once Confirm is pressed.

package main

import (
	"fmt"
	"os"

	"github.com/juliusplatzer/reds/asdex"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"

	"github.com/AllenDang/cimgui-go/imgui"
	implogl3 "github.com/AllenDang/cimgui-go/impl/opengl3"
)

const (
	uiFontSize = 18 // logical px

	asdexWindowWidth  = 1280
	asdexWindowHeight = 800
)

type appMode int

const (
	appModeMenu appMode = iota
	appModeScope
)

func main() {
	// ImGui context must exist before the platform touches CurrentIO().
	imgui.CreateContext()
	defer imgui.DestroyContext()
	imgui.CurrentIO().SetIniFilename("") // no imgui.ini side file

	plat, err := platform.New(&platform.Config{
		Title:             "reds",
		InitialWindowSize: [2]int{200, 350},
		MinWindowSize:     [2]int{200, 200},
		Resizable:         true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "reds: %v\n", err)
		os.Exit(1)
	}
	defer plat.Dispose()

	r := renderer.NewOpenGLRenderer()
	if err := r.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "reds: renderer init failed: %v\n", err)
		os.Exit(1)
	}
	defer r.Dispose()

	loadFont()
	if err := initSVGIcons(); err != nil {
		fmt.Fprintf(os.Stderr, "reds: failed to initialize SVG icons: %v\n", err)
	}
	defer disposeSVGIcons()

	m := newMenu()
	if len(m.airports) == 0 {
		fmt.Fprintln(os.Stderr, "reds: no ASDE-X facilities found under resources/videomaps/asdex")
	}

	mode := appModeMenu
	var active panes.Pane
	consumer := &smesConsumer{}
	defer consumer.Stop()

	bg := colDialogBg
	for !plat.ShouldStop() {
		plat.ProcessEvents()
		plat.NewFrame()
		imgui.NewFrame()

		switch mode {
		case appModeMenu:
			res := m.draw(plat.DisplaySize())
			imgui.Render()
			plat.Clear(bg.X, bg.Y, bg.Z)
			implogl3.RenderDrawData(imgui.CurrentDrawData())
			plat.PostRender()

			switch res {
			case menuConfirmed:
				pane, err := launchScope(m.selection, plat, consumer)
				if err != nil {
					fmt.Fprintf(os.Stderr, "reds: %v\n", err)
					plat.SetWindowTitle("reds")
					continue
				}
				active = pane
				mode = appModeScope
			case menuCancelled:
				return
			}

		case appModeScope:
			io := imgui.CurrentIO()
			panes.DrawPane(active, plat, r, panes.DrawOptions{
				MouseCaptured:    io.WantCaptureMouse(),
				KeyboardCaptured: io.WantCaptureKeyboard(),
			})

			imgui.Render()
			implogl3.RenderDrawData(imgui.CurrentDrawData())
			plat.PostRender()
		}
	}
}

func launchScope(sel Selection, plat platform.Platform, consumer *smesConsumer) (panes.Pane, error) {
	switch sel.Mode {
	case DisplayASDEX:
		if err := consumer.Start(sel.Airport); err != nil {
			return nil, err
		}
		pane, err := asdex.NewPane(sel.Airport)
		if err != nil {
			consumer.Stop()
			return nil, err
		}
		plat.SetWindowTitle("REDS ASDE-X " + sel.Airport)
		plat.SetWindowSizeCentered(asdexWindowWidth, asdexWindowHeight)
		return pane, nil
	default:
		return nil, fmt.Errorf("%s scope is not implemented yet", sel.Mode)
	}
}
