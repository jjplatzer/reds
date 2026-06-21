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
	uiFontSize = 16 // logical px

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
		Title:             "REDS",
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
	scopeTitle := ""
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
					plat.SetWindowTitle("REDS")
					continue
				}
				active = pane
				scopeTitle = m.selection.Airport + " ASDE-X"
				mode = appModeScope
			case menuCancelled:
				return
			}

		case appModeScope:
			titlebarCaptured, titlebarAction := drawScopeTitleBar(
				plat,
				scopeTitle,
				plat.DisplaySize(),
			)

			io := imgui.CurrentIO()
			panes.DrawPane(active, plat, r, panes.DrawOptions{
				MenuBarHeight:    scopeTitleBarHeight,
				MouseCaptured:    io.WantCaptureMouse() || titlebarCaptured,
				KeyboardCaptured: io.WantCaptureKeyboard(),
			})

			imgui.Render()
			implogl3.RenderDrawData(imgui.CurrentDrawData())
			plat.PostRender()

			if titlebarAction == titleBarActionSwitchFacility {
				switchToMenu(&mode, &active, &scopeTitle, plat, consumer, m)
			}
		}
	}
}

func launchScope(sel Selection, plat platform.Platform, consumer *smesConsumer) (panes.Pane, error) {
	switch sel.Mode {
	case DisplayASDEX:
		useRemoteTargets := remoteTargetURLConfigured()
		if !useRemoteTargets {
			if err := consumer.Start(sel.Airport); err != nil {
				return nil, err
			}
		}
		pane, err := asdex.NewPane(sel.Airport)
		if err != nil {
			if !useRemoteTargets {
				consumer.Stop()
			}
			return nil, err
		}
		plat.SetWindowTitle(sel.Airport + " ASDE-X")
		plat.SetWindowDecorated(false)
		plat.SetWindowSizeCentered(asdexWindowWidth, asdexWindowHeight)
		return pane, nil
	default:
		return nil, fmt.Errorf("%s scope is not implemented yet", sel.Mode)
	}
}

func remoteTargetURLConfigured() bool {
	return os.Getenv("REDS_TARGET_WS_URL") != ""
}

func switchToMenu(
	mode *appMode,
	active *panes.Pane,
	scopeTitle *string,
	plat platform.Platform,
	consumer *smesConsumer,
	m *menu,
) {
	if consumer != nil {
		consumer.Stop()
	}
	if active != nil {
		if pane := *active; pane != nil {
			if disposable, ok := pane.(interface{ Dispose() }); ok {
				disposable.Dispose()
			}
		}
		*active = nil
	}
	if scopeTitle != nil {
		*scopeTitle = ""
	}
	if plat != nil {
		plat.ClearCursorOverride()
		plat.SetWindowDecorated(true)
		plat.SetWindowTitle("REDS")
		plat.SetWindowSizeCentered(200, 350)
		plat.ShowSystemCursor()
	}
	if m != nil {
		m.firstFrame = true
	}
	if mode != nil {
		*mode = appModeMenu
	}
}
