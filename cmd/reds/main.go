// cmd/reds/main.go
//
// reds entrypoint. Opens a GLFW + Dear ImGui window, shows the startup menu
// and dispatches to the selected scope once Confirm is pressed.

package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"time"

	"github.com/juliusplatzer/reds/asdex"
	redslog "github.com/juliusplatzer/reds/log"
	redsnet "github.com/juliusplatzer/reds/net"
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

var (
	logLevel = flag.String(
		"loglevel",
		"info",
		"logging level: debug, info, warn, error",
	)
	logDir = flag.String(
		"logdir",
		"",
		"log file directory",
	)
)

type appMode int

const (
	appModeMenu appMode = iota
	appModeScope
)

func main() {
	os.Exit(realMain())
}

func realMain() (exitCode int) {
	flag.Parse()

	logger, err := redslog.New(*logLevel, *logDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reds: initialize logging: %v\n", err)
		return 1
	}
	slog.SetDefault(logger.Logger)

	defer func() {
		if recovered := recover(); recovered != nil {
			logger.Error(
				"REDS crashed",
				slog.Any("panic", recovered),
				slog.String("stack", string(debug.Stack())),
			)
			exitCode = 1
		}
	}()

	logger.Info(
		"Log initialized",
		slog.String("file", logger.LogFile),
	)

	if err := run(logger); err != nil {
		logger.Error(
			"REDS exited with an error",
			slog.Any("error", err),
		)
		return 1
	}

	logger.Info(
		"REDS stopped",
		slog.Duration("runtime", time.Since(logger.Start)),
	)
	return 0
}

func run(logger *redslog.Logger) error {
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
		return fmt.Errorf("initialize platform: %w", err)
	}
	defer plat.Dispose()
	logger.Info("Platform initialized")

	r := renderer.NewOpenGLRenderer()
	if err := r.Init(); err != nil {
		return fmt.Errorf("initialize renderer: %w", err)
	}
	defer r.Dispose()
	logger.Info("Renderer initialized")

	loadFont()
	if err := initSVGIcons(); err != nil {
		logger.Warn(
			"Unable to initialize SVG icons",
			slog.Any("error", err),
		)
	}
	defer disposeSVGIcons()

	m := newMenu()
	if len(m.airports) == 0 {
		logger.Error(
			"No ASDE-X facilities found",
			slog.String("path", "resources/videomaps/asdex"),
		)
	}
	logger.Info(
		"Startup menu loaded",
		slog.Int("asdex_facilities", len(m.airports)),
	)

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
				logger.Info(
					"Facility selected",
					slog.String("mode", m.selection.Mode.String()),
					slog.String("airport", m.selection.Airport),
				)
				pane, err := launchScope(m.selection, plat, consumer, logger)
				if err != nil {
					logger.Error(
						"Scope launch failed",
						slog.Any("error", err),
					)
					plat.SetWindowTitle("REDS")
					continue
				}
				active = pane
				scopeTitle = m.selection.Airport + " ASDE-X"
				mode = appModeScope
			case menuCancelled:
				return nil
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
	return nil
}

func launchScope(
	sel Selection,
	plat platform.Platform,
	consumer *smesConsumer,
	logger *redslog.Logger,
) (panes.Pane, error) {
	switch sel.Mode {
	case DisplayASDEX:
		scopeLogger := logger.With(
			slog.String("display", "asdex"),
			slog.String("airport", sel.Airport),
		)
		scopeLogger.Info("Launching scope")

		usePublicServer := redsnet.UsePublicServer()
		if !usePublicServer {
			if err := consumer.Start(sel.Airport); err != nil {
				return nil, err
			}
		}
		pane, err := asdex.NewPane(sel.Airport, scopeLogger)
		if err != nil {
			if !usePublicServer {
				consumer.Stop()
			}
			return nil, err
		}
		plat.SetWindowTitle(sel.Airport + " ASDE-X")
		plat.SetWindowDecorated(false)
		plat.SetWindowSizeCentered(asdexWindowWidth, asdexWindowHeight)
		scopeLogger.Info("ASDE-X scope launched")
		return pane, nil
	default:
		return nil, fmt.Errorf("%s scope is not implemented yet", sel.Mode)
	}
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
