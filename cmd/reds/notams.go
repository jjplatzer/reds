// cmd/reds/notams.go
// Native ASDE-X NOTAM viewer. It is a second GLFW process, like the temporary
// area editor, because REDS' platform backend owns one native window/process.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"github.com/AllenDang/cimgui-go/imgui"
	implogl3 "github.com/AllenDang/cimgui-go/impl/opengl3"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/util"
)

const (
	notamWindowWidth  = 820
	notamWindowHeight = 620
	notamFontPath     = "fonts/CourierPrime-Regular.ttf.zst"
)

// Internal command-line entry point for the child process.
var notamWindowAirport = flag.String("notams-window", "", "internal ASDE-X NOTAM viewer")

// A single viewer is kept per REDS scope. A repeated invocation replaces it.
type notamWindowProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (w *notamWindowProcess) show(airport string) error {
	w.close()
	airport = strings.ToUpper(strings.TrimSpace(airport))
	if !validNOTAMAirport(airport) {
		return fmt.Errorf("invalid NOTAM airport %q", airport)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate REDS executable: %w", err)
	}
	cmd := exec.Command(executable, "-notams-window="+airport)
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch NOTAM viewer: %w", err)
	}
	w.cmd = cmd
	w.done = make(chan struct{})
	done := w.done
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	return nil
}

func (w *notamWindowProcess) close() {
	if w.cmd != nil && w.cmd.Process != nil {
		select {
		case <-w.done:
		default:
			_ = w.cmd.Process.Kill()
		}
	}
	w.cmd, w.done = nil, nil
}

func validNOTAMAirport(airport string) bool {
	if len(airport) != 4 {
		return false
	}
	for _, c := range airport {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// The NOTAC fetcher is bound to server loopback. Read its public HTTPS proxy
// by default; REDS_NOTAMS_BASE_URL overrides it for testing/deployments.
// The NOTAC API key is never sent to the client.
func fetchNOTAMTexts(airport string) ([]string, error) {
	if !validNOTAMAirport(airport) {
		return nil, fmt.Errorf("invalid airport %q", airport)
	}
	base := strings.TrimSpace(os.Getenv("REDS_NOTAMS_BASE_URL"))
	if base == "" {
		base = "https://reds-stdds-live.jjplatzer.com/notams"
	}
	u, err := url.Parse(base)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid REDS_NOTAMS_BASE_URL")
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("HTTP NOTAM server must be on loopback; use HTTPS remotely")
		}
	} else if u.Scheme != "https" {
		return nil, fmt.Errorf("NOTAM server must use HTTPS (or HTTP on loopback)")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/notams"
	q := url.Values{}
	q.Set("airport", airport)
	u.RawQuery = q.Encode()

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("fetch NOTAMs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("NOTAM server returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Airport string `json:"airport"`
		Results []struct {
			Text string `json:"text"`
		} `json:"results"`
	}
	// Limit the local response so a misconfigured reverse proxy cannot exhaust
	// the native viewer's memory.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode NOTAM response: %w", err)
	}
	if payload.Airport != "" && !strings.EqualFold(payload.Airport, airport) {
		return nil, fmt.Errorf("NOTAM response airport mismatch: %q", payload.Airport)
	}
	texts := make([]string, 0, len(payload.Results))
	for _, n := range payload.Results {
		if n.Text != "" {
			texts = append(texts, n.Text)
		}
	}
	return texts, nil
}

type notamLoadResult struct {
	texts []string
	err   error
}

// Courier Prime is kept separately from the menu's Inter font. The TTF data
// must remain pinned while ImGui builds and retains its font atlas.
var (
	notamFontData []byte
	notamFont     *imgui.Font
)

func loadNOTAMFont() error {
	notamFontData = util.LoadResourceBytes(notamFontPath)
	if len(notamFontData) == 0 {
		return fmt.Errorf("Courier Prime font missing: %s", notamFontPath)
	}
	fontPinner.Pin(&notamFontData[0])
	cfg := imgui.NewFontConfig()
	cfg.SetFontDataOwnedByAtlas(false)
	notamFont = imgui.CurrentIO().Fonts().AddFontFromMemoryTTFV(
		uintptr(unsafe.Pointer(&notamFontData[0])), int32(len(notamFontData)),
		16, cfg, &menuFontRange[0],
	)
	return nil
}

func runNOTAMWindow(airport string) error {
	airport = strings.ToUpper(strings.TrimSpace(airport))
	if !validNOTAMAirport(airport) {
		return fmt.Errorf("invalid NOTAM airport %q", airport)
	}
	imgui.CreateContext()
	defer imgui.DestroyContext()
	imgui.CurrentIO().SetIniFilename("")

	plat, err := platform.New(&platform.Config{
		Title:             "NOTAMs - " + airport,
		InitialWindowSize: [2]int{notamWindowWidth, notamWindowHeight},
		MinWindowSize:     [2]int{550, 360},
		Resizable:         true,
	})
	if err != nil {
		return fmt.Errorf("initialize NOTAM window: %w", err)
	}
	defer plat.Dispose()
	plat.SetWindowDecorated(false)
	loadFont()
	if err := loadNOTAMFont(); err != nil {
		return err
	}

	results := make(chan notamLoadResult, 1)
	go func() {
		texts, err := fetchNOTAMTexts(airport)
		results <- notamLoadResult{texts: texts, err: err}
	}()
	loading := true
	var texts []string
	var loadErr error
	open := true
	for open && !plat.ShouldStop() {
		select {
		case res := <-results:
			texts, loadErr, loading = res.texts, res.err, false
		default:
		}
		plat.ProcessEvents()
		plat.NewFrame()
		imgui.NewFrame()
		drawNOTAMWindow(plat, airport, loading, texts, loadErr, &open)
		imgui.Render()
		bg := colDialogBg
		plat.Clear(bg.X, bg.Y, bg.Z)
		implogl3.RenderDrawData(imgui.CurrentDrawData())
		plat.PostRender()
	}
	return nil
}

func drawNOTAMWindow(plat platform.Platform, airport string, loading bool, texts []string, loadErr error, open *bool) {
	size := plat.DisplaySize()
	imgui.SetNextWindowPosV(imgui.Vec2{}, imgui.CondAlways, imgui.Vec2{})
	imgui.SetNextWindowSizeV(imgui.Vec2{X: size[0], Y: size[1]}, imgui.CondAlways)
	flags := imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize |
		imgui.WindowFlagsNoMove | imgui.WindowFlagsNoCollapse |
		imgui.WindowFlagsNoScrollbar | imgui.WindowFlagsNoSavedSettings
	imgui.PushStyleColorVec4(imgui.ColWindowBg, colDialogBg)
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{X: 18, Y: 16})
	imgui.PushStyleVarVec2(imgui.StyleVarWindowMinSize, imgui.Vec2{})
	imgui.PushStyleVarFloat(imgui.StyleVarWindowRounding, 0)
	imgui.PushStyleVarFloat(imgui.StyleVarWindowBorderSize, 0)
	imgui.BeginV("##notams-root", nil, flags)

	barMax := imgui.Vec2{X: size[0], Y: scopeTitleBarHeight}
	imgui.WindowDrawList().AddRectFilledV(imgui.Vec2{}, barMax,
		imgui.ColorU32Vec4(titleBarBg), 0, imgui.DrawFlagsNone)
	imgui.WindowDrawList().AddTextVec2(
		imgui.Vec2{X: 18, Y: (scopeTitleBarHeight - imgui.FontSize()) / 2},
		imgui.ColorU32Vec4(titleBarFg), "NOTAMs - "+airport)
	closeMin := imgui.Vec2{X: size[0] - titleBarButtonWidth, Y: 0}
	imgui.SetCursorScreenPos(closeMin)
	if imgui.InvisibleButtonV("##notams-close", imgui.Vec2{X: titleBarButtonWidth, Y: scopeTitleBarHeight},
		imgui.ButtonFlagsMouseButtonLeft) {
		*open = false
	}
	if imgui.IsItemHovered() {
		imgui.WindowDrawList().AddRectFilledV(closeMin, barMax,
			imgui.ColorU32Vec4(titleBarCloseHover), 0, imgui.DrawFlagsNone)
	}
	drawTitleBarSymbolGlyph(titleBarGlyphClose, symbolsFont8, titleBarButtonSymbolSize, closeMin, barMax, titleBarFg)
	imgui.SetCursorScreenPos(imgui.Vec2{})
	imgui.InvisibleButtonV("##notams-drag", imgui.Vec2{X: size[0] - titleBarButtonWidth, Y: scopeTitleBarHeight},
		imgui.ButtonFlagsMouseButtonLeft)
	if imgui.IsItemActive() && imgui.IsMouseDragging(imgui.MouseButtonLeft) {
		delta := imgui.CurrentIO().MouseDelta()
		plat.MoveWindowBy(delta.X, delta.Y)
	}

	imgui.SetCursorScreenPos(imgui.Vec2{X: 18, Y: scopeTitleBarHeight + 16})
	if loading {
		imgui.TextUnformatted("Loading NOTAMs...")
	} else if loadErr != nil {
		imgui.TextUnformatted(loadErr.Error())
	} else if len(texts) == 0 {
		imgui.TextUnformatted("No current NOTAMs found for " + airport + ".")
	} else {
		// Use the same background as the window and wrap the displayed raw
		// text to the available child width (recomputed when resized).
		imgui.PushStyleColorVec4(imgui.ColChildBg, colDialogBg)
		// Leave room for the source attribution below the scrolling notices.
		imgui.BeginChildStrV("##notams-scroll", imgui.Vec2{X: 0, Y: -28}, 0, 0)
		imgui.PushFont(notamFont, 16)
		for i, text := range texts {
			if i > 0 {
				imgui.Separator()
				imgui.Spacing()
			}
			imgui.TextWrapped(text)
		}
		imgui.PopFont()
		imgui.EndChild()
		imgui.PopStyleColor()
	}
	// NOTAC requires attribution and a clear statement that this is not an
	// official aeronautical information source. The notices above stay raw.
	imgui.SetCursorScreenPos(imgui.Vec2{X: 18, Y: size[1] - 28})
	imgui.TextUnformatted("Source: NOTAC - unofficial information; not for flight planning")
	imgui.End()
	imgui.PopStyleVarV(4)
	imgui.PopStyleColor()
}
