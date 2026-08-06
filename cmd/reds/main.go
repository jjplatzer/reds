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
	"github.com/juliusplatzer/reds/eram"
	redslog "github.com/juliusplatzer/reds/log"
	redsnet "github.com/juliusplatzer/reds/net"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util/buildinfo"

	"github.com/AllenDang/cimgui-go/imgui"
	implogl3 "github.com/AllenDang/cimgui-go/impl/opengl3"
)

const (
	uiFontSize = 16 // logical px

	asdexWindowWidth  = 1280
	asdexWindowHeight = 800
	eramWindowWidth   = 1280
	eramWindowHeight  = 800
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
	showVersion = flag.Bool(
		"version",
		false,
		"print REDS version and exit",
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

	if *showVersion {
		fmt.Println(buildinfo.String())
		return 0
	}

	resolvedLogDir := redslog.DefaultLogDir(*logDir)
	crashStderrPath := redslog.RedirectStderrToCrashFile(resolvedLogDir)
	if crashStderrPath != "" {
		defer redslog.RemoveCurrentCrashStderrFile()
	}

	logger, err := redslog.New(*logLevel, resolvedLogDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reds: initialize logging: %v\n", err)
		return 1
	}
	slog.SetDefault(logger.Logger)

	if crashStderrPath != "" {
		logger.Info(
			"Redirected stderr",
			slog.String("file", crashStderrPath),
		)
	}

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
		slog.String("version", buildinfo.Version),
		slog.String("commit", buildinfo.Commit),
		slog.String("build_time", buildinfo.BuildTime),
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
		InitialWindowSize: [2]int{275, 350},
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
	if len(m.asdexFacilities) == 0 {
		logger.Error(
			"No ASDE-X facilities found",
			slog.String("path", "resources/videomaps/asdex"),
		)
	}
	if len(m.eramFacilities) == 0 {
		logger.Error(
			"No ERAM facilities found",
			slog.String("path", "resources/configs/eram"),
			slog.Any("error", m.eramLoadErr),
		)
	}
	logger.Info(
		"Startup menu loaded",
		slog.Int("asdex_facilities", len(m.asdexFacilities)),
		slog.Int("eram_facilities", len(m.eramFacilities)),
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
					selectionLogAttrs(m.selection)...,
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
				scopeTitle = m.selection.ScopeTitle()
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

func selectionLogAttrs(selection Selection) []any {
	attrs := []any{
		slog.String("mode", selection.Mode.String()),
		slog.String("facility", selection.Facility),
	}
	if selection.Mode == DisplayERAM && selection.Sector != nil {
		attrs = append(
			attrs,
			slog.String("sector_id", selection.Sector.ID),
			slog.String("sector_name", selection.Sector.Name),
		)
	}
	return attrs
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
			slog.String("facility", sel.Facility),
		)
		scopeLogger.Info("Launching scope")

		usePublicServer := redsnet.UsePublicServer()
		if !usePublicServer {
			if err := consumer.Start(sel.Facility); err != nil {
				return nil, err
			}
		}
		pane, err := asdex.NewPane(sel.Facility, scopeLogger)
		if err != nil {
			if !usePublicServer {
				consumer.Stop()
			}
			return nil, err
		}
		plat.SetWindowTitle(sel.ScopeTitle())
		plat.SetWindowDecorated(false)
		plat.SetWindowSizeCentered(asdexWindowWidth, asdexWindowHeight)
		scopeLogger.Info("ASDE-X scope launched")
		return pane, nil
	case DisplayERAM:
		if sel.Sector == nil {
			return nil, fmt.Errorf("ERAM sector is required")
		}
		scopeLogger := logger.With(
			slog.String("display", "eram"),
			slog.String("facility", sel.Facility),
			slog.String("sector_id", sel.Sector.ID),
			slog.String("sector_name", sel.Sector.Name),
		)
		scopeLogger.Info("Launching scope")

		pane, err := eram.NewPane(sel.Facility, *sel.Sector, scopeLogger)
		if err != nil {
			return nil, err
		}
		plat.SetWindowTitle(sel.ScopeTitle())
		plat.SetWindowDecorated(false)
		plat.SetWindowSizeCentered(eramWindowWidth, eramWindowHeight)
		scopeLogger.Info("ERAM scope launched")
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
		plat.SetWindowSizeCentered(275, 350)
		plat.ShowSystemCursor()
	}
	if m != nil {
		m.firstFrame = true
	}
	if mode != nil {
		*mode = appModeMenu
	}
}
