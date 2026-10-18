// cmd/reds/notams.go
// Native ASDE-X NOTAM viewer. It is a second GLFW process, like the temporary
// area editor, because REDS' platform backend owns one native window/process.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
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
	notamWindowWidth   = 820
	notamWindowHeight  = 620
	notamMinWidth      = 550
	notamMinListHeight = 80
	notamFontPath      = "fonts/CourierPrime-Regular.ttf.zst"

	notamFooterTextOffset   = 30 // top of the disclaimer body, relative to the divider
	notamFooterBottomMargin = 18

	notamDisclaimerTitle = "UNOFFICIAL NOTAM DATA - NOT FOR OPERATIONAL USE"
	notamDisclaimerText  = "Source: NOTAC (https://notac.aero). The underlying NOTAMs originate from the FAA NOTAM Management System and other issuing authorities. REDS is an independent, unofficial simulation tool, not an authorized aeronautical information service or briefing provider, and is not affiliated with or endorsed by NOTAC, the FAA, or any issuing authority.\n\n" +
		"Information is cached and may be delayed, incomplete, inaccurate, expired, superseded, cancelled, or unavailable. Keyword filtering may omit relevant notices. No guarantee of accuracy, completeness, timeliness, availability, or fitness for operational use is made.\n\n" +
		"For simulation and informational use only. Do not rely on this display for flight planning, dispatch, air traffic control, airport surface operations, or other safety-critical decisions. Verify applicable NOTAMs and obtain a current official briefing from the responsible authority. Nothing here limits statutory rights or liabilities that cannot lawfully be excluded."
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
			Number    string `json:"number"`
			NOTAMType string `json:"notam_type"`
			Text      string `json:"text"`
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
			texts = append(texts, formatNOTAMText(n.Number, n.NOTAMType, n.Text))
		}
	}
	return texts, nil
}

// NOTAC's number already includes the series (e.g. A3111/26), while
// notam_type is N (new), R (replacement), or C (cancellation). Some raw
// texts already begin with this header; only supply it when absent.
func formatNOTAMText(number, notamType, raw string) string {
	number = strings.ToUpper(strings.TrimSpace(number))
	notamType = strings.ToUpper(strings.TrimSpace(notamType))
	if number == "" || (notamType != "N" && notamType != "R" && notamType != "C") {
		return raw
	}
	header := number + " NOTAM" + notamType
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(raw)), header) {
		return raw
	}
	return header + "\n" + raw
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
		MinWindowSize:     [2]int{notamMinWidth, notamWindowHeight},
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
	var searchKeyword string
	open := true
	minHeightSet := false
	for open && !plat.ShouldStop() {
		select {
		case res := <-results:
			texts, loadErr, loading = res.texts, res.err, false
		default:
		}
		plat.ProcessEvents()
		plat.NewFrame()
		imgui.NewFrame()
		if !minHeightSet {
			// Measure at the narrowest allowed width: wrapping can only
			// take more vertical space as the viewer becomes narrower.
			minHeight := notamMinimumWindowHeight()
			plat.SetWindowMinSize(notamMinWidth, minHeight)
			if size := plat.WindowSize(); size[1] < minHeight {
				plat.SetWindowSizeCentered(size[0], minHeight)
			}
			minHeightSet = true
		}
		drawNOTAMWindow(plat, airport, loading, texts, loadErr, &searchKeyword, &open)
		imgui.Render()
		bg := colDialogBg
		plat.Clear(bg.X, bg.Y, bg.Z)
		implogl3.RenderDrawData(imgui.CurrentDrawData())
		plat.PostRender()
	}
	return nil
}

// drawNOTAMKeywordSearch is one full-width, focusable ImGui text input. Use
// the same rounded border as other controls, but no frame fill: the parent
// window background shows through. The bundled Symbols font provides U+E721.
func drawNOTAMKeywordSearch(windowWidth float32, keyword *string) {
	const (
		inset     float32 = 20
		iconWidth float32 = 34
	)
	x, y := inset, float32(scopeTitleBarHeight+16)
	width := windowWidth - 2*inset
	min := imgui.Vec2{X: x, Y: y}
	max := imgui.Vec2{X: x + width, Y: y + controlHeight}

	// Make the actual input occupy the entire outlined rectangle rather than
	// drawing a separate rounded background behind a smaller hit target.
	imgui.SetCursorScreenPos(min)
	imgui.SetNextItemWidth(width)
	imgui.PushStyleVarFloat(imgui.StyleVarFrameRounding, frameRounding)
	imgui.PushStyleVarFloat(imgui.StyleVarFrameBorderSize, 1)
	imgui.PushStyleVarVec2(imgui.StyleVarFramePadding,
		imgui.Vec2{X: controlPaddingX, Y: (controlHeight - imgui.FontSize()) / 2})
	// Fully transparent fill in all interaction states; just the outline.
	imgui.PushStyleColorVec4(imgui.ColFrameBg, imgui.Vec4{})
	imgui.PushStyleColorVec4(imgui.ColFrameBgHovered, imgui.Vec4{})
	imgui.PushStyleColorVec4(imgui.ColFrameBgActive, imgui.Vec4{})
	imgui.PushStyleColorVec4(imgui.ColBorder, colBorder)
	imgui.PushStyleColorVec4(imgui.ColText, colText)
	imgui.PushStyleColorVec4(imgui.ColTextDisabled, colBorder)
	imgui.InputTextWithHint("##notams-search", "Search Keyword...", keyword, 0, nil)
	imgui.PopStyleColorV(6)
	imgui.PopStyleVarV(3)

	// Draw the glyph without introducing another item that could take focus.
	drawTitleBarSymbolGlyph("\ue721", symbolsFont12, 12,
		imgui.Vec2{X: max.X - iconWidth, Y: y}, max, colBorder)
}

// notamDisclaimerFooterHeight measures exactly the font and wrapping used by
// TextWrapped below, plus room for the heading and the bottom margin.
func notamDisclaimerFooterHeight(windowWidth float32) float32 {
	textWidth := windowWidth - 36 // 18 px left and right insets
	return notamFooterTextOffset + imgui.CalcTextSizeV(notamDisclaimerText, false, textWidth).Y + notamFooterBottomMargin
}

// A native minimum height protects both the entire disclaimer and a usable
// scrollable NOTAM area, including when the window is narrowed to 550 px.
func notamMinimumWindowHeight() int {
	notamsTop := float32(scopeTitleBarHeight + 16 + controlHeight + 16)
	return int(math.Ceil(float64(notamsTop + notamMinListHeight + 10 + notamDisclaimerFooterHeight(notamMinWidth))))
}

func drawNOTAMWindow(plat platform.Platform, airport string, loading bool, texts []string, loadErr error, searchKeyword *string, open *bool) {
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

	// Size the fixed footer from the *entire* wrapped disclaimer. The
	// remainder of the window belongs to the independently scrollable list.
	footerHeight := notamDisclaimerFooterHeight(size[0])
	footerTop := size[1] - footerHeight
	notamsTop := float32(scopeTitleBarHeight + 16 + controlHeight + 16)

	// Keep the search bar visible even while the airport data is loading.
	drawNOTAMKeywordSearch(size[0], searchKeyword)
	imgui.SetCursorScreenPos(imgui.Vec2{X: 18, Y: notamsTop})
	if loading {
		imgui.TextUnformatted("Loading NOTAMs...")
	} else if loadErr != nil {
		imgui.TextUnformatted(loadErr.Error())
	} else if len(texts) == 0 {
		imgui.TextUnformatted("No current NOTAMs found for " + airport + ".")
	} else {
		keyword := strings.ToUpper(strings.TrimSpace(*searchKeyword))
		// Use the same background as the window and wrap the displayed raw
		// text to the available child width (recomputed when resized).
		imgui.PushStyleColorVec4(imgui.ColChildBg, colDialogBg)
		// Keep the scrollable NOTAM area above the persistent disclaimer.
		imgui.BeginChildStrV("##notams-scroll", imgui.Vec2{
			X: size[0] - 36, Y: footerTop - notamsTop - 10,
		}, 0, 0)
		imgui.PushFont(notamFont, 16)
		shown := 0
		for _, text := range texts {
			if keyword != "" && !strings.Contains(strings.ToUpper(text), keyword) {
				continue
			}
			if shown > 0 {
				imgui.Separator()
				imgui.Spacing()
			}
			imgui.TextWrapped(text)
			shown++
		}
		if shown == 0 {
			imgui.TextUnformatted("No NOTAMs match your keyword.")
		}
		imgui.PopFont()
		imgui.EndChild()
		imgui.PopStyleColor()
	}
	// NOTAC requires the full disclaimer to stay on screen at all times.
	// Render it directly in the root window, with no scrolling child.
	imgui.SetCursorScreenPos(imgui.Vec2{X: 18, Y: footerTop})
	imgui.Separator()
	imgui.SetCursorScreenPos(imgui.Vec2{X: 18, Y: footerTop + 8})
	imgui.PushStyleColorVec4(imgui.ColText, colText)
	imgui.TextUnformatted(notamDisclaimerTitle)
	imgui.PopStyleColor()
	imgui.SetCursorScreenPos(imgui.Vec2{X: 18, Y: footerTop + notamFooterTextOffset})
	imgui.PushStyleColorVec4(imgui.ColText, colTextDisabled)
	imgui.TextWrapped(notamDisclaimerText)
	imgui.PopStyleColor()
	imgui.End()
	imgui.PopStyleVarV(4)
	imgui.PopStyleColor()
}
