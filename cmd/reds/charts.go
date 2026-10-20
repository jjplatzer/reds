// cmd/reds/charts.go
// STARS IAP chart lookup window. PDF download, rendering, and scope overlays
// are intentionally deferred; this window only checks chart availability.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AllenDang/cimgui-go/imgui"
	implogl3 "github.com/AllenDang/cimgui-go/impl/opengl3"
	"github.com/juliusplatzer/reds/platform"
)

const (
	iapWindowWidth  = 820
	iapWindowHeight = 620
	iapMinWidth     = 550
	iapMinHeight    = 360

	// Window-content positions: cycle label, then search field, then lookup result.
	iapCycleLabelTop = scopeTitleBarHeight + 16
	iapSearchTop     = scopeTitleBarHeight + 46
	iapResultTop     = iapSearchTop + controlHeight + 18

	// First iteration targets the FAA cycle used for the chart examples.
	// Automatic cycle discovery and selection will be added with PDF support.
	iapDTPPCycle = "2611"
)

var iapChartWindow = flag.Bool("iap-chart-window", false, "internal STARS IAP chart lookup window")
var iapChartOwnerPID = flag.Int("iap-chart-owner", 0, "internal chart overlay parent PID")

type iapOverlayChoice struct {
	ChartID        string `json:"chart_id"`
	Enabled        bool   `json:"enabled"`
	OpacityPercent int32  `json:"opacity_percent"`
}

const defaultIAPOpacityPercent int32 = 40

func clampIAPOpacityPercent(n int32) int32 {
	if n < 1 {
		return 1
	}
	if n > 100 {
		return 100
	}
	return n
}

func iapOverlayPath(pid int) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("reds-iap-overlay-%d.json", pid))
}

func readIAPOverlayChoice(pid int) (iapOverlayChoice, error) {
	// Nonzero initialization also handles older selection files that have
	// no opacity_percent field. The slider's supported range is 1-100%.
	choice := iapOverlayChoice{OpacityPercent: defaultIAPOpacityPercent}
	data, err := os.ReadFile(iapOverlayPath(pid))
	if os.IsNotExist(err) {
		return choice, nil
	}
	if err != nil {
		return choice, err
	}
	if err := json.Unmarshal(data, &choice); err != nil {
		return choice, err
	}
	choice.OpacityPercent = clampIAPOpacityPercent(choice.OpacityPercent)
	return choice, nil
}

func writeIAPOverlayChoice(pid int, choice iapOverlayChoice) error {
	choice.OpacityPercent = clampIAPOpacityPercent(choice.OpacityPercent)
	data, err := json.Marshal(choice)
	if err != nil {
		return err
	}
	// Owner and child are same-user processes; prevent other users from writing.
	return os.WriteFile(iapOverlayPath(pid), data, 0600)
}

func clearIAPOverlayChoice(pid int) { _ = os.Remove(iapOverlayPath(pid)) }

// Each scope owns at most one chart viewer, mirroring the NOTAM viewer.
type iapChartWindowProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (w *iapChartWindowProcess) show(parentPID int) error {
	w.close()
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate REDS executable: %w", err)
	}
	cmd := exec.Command(executable, "-iap-chart-window", "-iap-chart-owner="+strconv.Itoa(parentPID))
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch IAP chart viewer: %w", err)
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

func (w *iapChartWindowProcess) close() {
	if w.cmd != nil && w.cmd.Process != nil {
		select {
		case <-w.done:
		default:
			_ = w.cmd.Process.Kill()
		}
	}
	w.cmd, w.done = nil, nil
}

// FAA PDF filenames are opaque chart identifiers, not search queries or paths.
func normalizeIAPChartID(raw string) (string, error) {
	id := strings.ToUpper(strings.TrimSpace(raw))
	if id == "" || len(id) > 32 {
		return "", fmt.Errorf("enter a valid FAA chart ID (e.g. 00610IL22R)")
	}
	for _, c := range id {
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return "", fmt.Errorf("chart IDs must contain only letters and numbers")
		}
	}
	return id, nil
}

type iapChartLookup struct {
	id  string
	url string
	err error
}

// Only check availability. The response body is never read or retained.
func lookupIAPChart(id string) iapChartLookup {
	url := "https://aeronav.faa.gov/d-tpp/" + iapDTPPCycle + "/" + id + ".PDF"
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Head(url)
	if err != nil {
		return iapChartLookup{id: id, err: fmt.Errorf("FAA lookup failed: %w", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return iapChartLookup{id: id, err: fmt.Errorf("chart %s not found in cycle %s", id, iapDTPPCycle)}
	}
	if resp.StatusCode != http.StatusOK {
		return iapChartLookup{id: id, err: fmt.Errorf("FAA returned HTTP %d", resp.StatusCode)}
	}
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return iapChartLookup{id: id, err: fmt.Errorf("FAA returned HTML instead of a PDF")}
	}
	return iapChartLookup{id: id, url: url}
}

func runIAPChartWindow() error {
	imgui.CreateContext()
	defer imgui.DestroyContext()
	imgui.CurrentIO().SetIniFilename("")
	plat, err := platform.New(&platform.Config{
		Title:             "IAP Charts",
		InitialWindowSize: [2]int{iapWindowWidth, iapWindowHeight},
		MinWindowSize:     [2]int{iapMinWidth, iapMinHeight},
		Resizable:         true,
	})
	if err != nil {
		return fmt.Errorf("initialize IAP chart viewer: %w", err)
	}
	defer plat.Dispose()
	plat.SetWindowDecorated(false)
	loadFont()

	choice, _ := readIAPOverlayChoice(*iapChartOwnerPID)
	var searchID string
	selectedID := choice.ChartID
	overlayEnabled := choice.Enabled
	opacityPercent := choice.OpacityPercent
	var result iapChartLookup
	var resultCh <-chan iapChartLookup
	open := true
	for open && !plat.ShouldStop() {
		// A buffered channel per request prevents old searches from blocking
		// when a newer search supersedes them.
		if resultCh != nil {
			select {
			case result = <-resultCh:
				resultCh = nil
			default:
			}
		}
		plat.ProcessEvents()
		plat.NewFrame()
		imgui.NewFrame()
		previousID, previousEnabled, previousOpacity := selectedID, overlayEnabled, opacityPercent
		if drawIAPChartWindow(plat, &searchID, &result, resultCh != nil, &selectedID, &overlayEnabled, &opacityPercent, &open) {
			id, err := normalizeIAPChartID(searchID)
			result = iapChartLookup{err: err}
			resultCh = nil
			if err == nil {
				result = iapChartLookup{id: id}
				ch := make(chan iapChartLookup, 1)
				resultCh = ch
				go func() { ch <- lookupIAPChart(id) }()
			}
		}
		if previousID != selectedID || previousEnabled != overlayEnabled || previousOpacity != opacityPercent {
			if err := writeIAPOverlayChoice(*iapChartOwnerPID, iapOverlayChoice{
				ChartID: selectedID, Enabled: overlayEnabled, OpacityPercent: opacityPercent,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "reds: save IAP chart selection: %v\n", err)
			}
		}
		imgui.Render()
		bg := colDialogBg
		plat.Clear(bg.X, bg.Y, bg.Z)
		implogl3.RenderDrawData(imgui.CurrentDrawData())
		plat.PostRender()
	}
	return nil
}

// The search field is styled exactly like the NOTAM keyword field: same
// spacing, transparent fill, outlined input, and right-aligned search glyph.
func drawIAPChartSearch(width float32, value *string) bool {
	const (
		inset     float32 = 20
		iconWidth float32 = 34
	)
	x, y := inset, float32(iapSearchTop)
	w := width - 2*inset
	min := imgui.Vec2{X: x, Y: y}
	max := imgui.Vec2{X: x + w, Y: y + controlHeight}
	imgui.SetCursorScreenPos(min)
	imgui.SetNextItemWidth(w)
	imgui.PushStyleVarFloat(imgui.StyleVarFrameRounding, frameRounding)
	imgui.PushStyleVarFloat(imgui.StyleVarFrameBorderSize, 1)
	imgui.PushStyleVarVec2(imgui.StyleVarFramePadding,
		imgui.Vec2{X: controlPaddingX, Y: (controlHeight - imgui.FontSize()) / 2})
	imgui.PushStyleColorVec4(imgui.ColFrameBg, imgui.Vec4{})
	imgui.PushStyleColorVec4(imgui.ColFrameBgHovered, imgui.Vec4{})
	imgui.PushStyleColorVec4(imgui.ColFrameBgActive, imgui.Vec4{})
	imgui.PushStyleColorVec4(imgui.ColBorder, colBorder)
	imgui.PushStyleColorVec4(imgui.ColText, colText)
	imgui.PushStyleColorVec4(imgui.ColTextDisabled, colBorder)
	entered := imgui.InputTextWithHint("##iap-chart-search", "Search Chart ID...", value,
		imgui.InputTextFlagsEnterReturnsTrue, nil)
	imgui.PopStyleColorV(6)
	imgui.PopStyleVarV(3)
	drawTitleBarSymbolGlyph("\ue721", symbolsFont12, 12,
		imgui.Vec2{X: max.X - iconWidth, Y: y}, max, colBorder)
	return entered
}

func drawIAPChartWindow(plat platform.Platform, searchID *string, result *iapChartLookup,
	loading bool, selectedID *string, overlayEnabled *bool, opacityPercent *int32, open *bool) bool {
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
	imgui.BeginV("##iap-charts-root", nil, flags)

	barMax := imgui.Vec2{X: size[0], Y: scopeTitleBarHeight}
	imgui.WindowDrawList().AddRectFilledV(imgui.Vec2{}, barMax,
		imgui.ColorU32Vec4(titleBarBg), 0, imgui.DrawFlagsNone)
	imgui.WindowDrawList().AddTextVec2(
		imgui.Vec2{X: 18, Y: (scopeTitleBarHeight - imgui.FontSize()) / 2},
		imgui.ColorU32Vec4(titleBarFg), "IAP Charts")
	closeMin := imgui.Vec2{X: size[0] - titleBarButtonWidth, Y: 0}
	imgui.SetCursorScreenPos(closeMin)
	if imgui.InvisibleButtonV("##iap-chart-close", imgui.Vec2{X: titleBarButtonWidth, Y: scopeTitleBarHeight},
		imgui.ButtonFlagsMouseButtonLeft) {
		*open = false
	}
	if imgui.IsItemHovered() {
		imgui.WindowDrawList().AddRectFilledV(closeMin, barMax,
			imgui.ColorU32Vec4(titleBarCloseHover), 0, imgui.DrawFlagsNone)
	}
	drawTitleBarSymbolGlyph(titleBarGlyphClose, symbolsFont8, titleBarButtonSymbolSize, closeMin, barMax, titleBarFg)
	imgui.SetCursorScreenPos(imgui.Vec2{})
	imgui.InvisibleButtonV("##iap-chart-drag", imgui.Vec2{X: size[0] - titleBarButtonWidth, Y: scopeTitleBarHeight},
		imgui.ButtonFlagsMouseButtonLeft)
	if imgui.IsItemActive() && imgui.IsMouseDragging(imgui.MouseButtonLeft) {
		delta := imgui.CurrentIO().MouseDelta()
		plat.MoveWindowBy(delta.X, delta.Y)
	}

	imgui.SetCursorScreenPos(imgui.Vec2{X: 20, Y: float32(iapCycleLabelTop)})
	imgui.PushStyleColorVec4(imgui.ColText, colText)
	if menuBoldFont != nil {
		imgui.PushFont(menuBoldFont, uiFontSize)
	}
	imgui.TextUnformatted("FAA d-TPP - Cycle " + iapDTPPCycle)
	if menuBoldFont != nil {
		imgui.PopFont()
	}
	imgui.PopStyleColor()

	// InputTextFlagsEnterReturnsTrue makes Enter the only search trigger.
	search := drawIAPChartSearch(size[0], searchID)
	imgui.SetCursorScreenPos(imgui.Vec2{X: 20, Y: float32(iapResultTop)})
	// Search feedback stays directly beneath the search bar. Selecting a result
	// removes the search-result row, leaving the chart controls immediately below.
	controlsY := float32(iapResultTop)
	switch {
	case loading:
		imgui.TextUnformatted("Searching FAA charts...")
		controlsY += 46
	case result.err != nil:
		imgui.TextWrapped(result.err.Error())
		controlsY += 62
	case result.url != "":
		imgui.TextUnformatted(result.id + ".PDF - Available")
		imgui.Spacing()
		if button("Select Chart", false) {
			*selectedID = result.id
			*overlayEnabled = true
			// The chart is now selected; the search result is no longer needed.
			*result = iapChartLookup{}
		}
		controlsY += 82
	case *selectedID == "":
		imgui.TextUnformatted("Enter a chart ID and press Enter.")
	}

	if *selectedID != "" {
		// Keep the selected-chart label and its controls together, regardless
		// of window height. At the minimum width the slider still has room.
		imgui.SetCursorScreenPos(imgui.Vec2{X: 20, Y: controlsY})
		if *overlayEnabled {
			imgui.TextUnformatted("Displaying IAP Chart: " + *selectedID)
		} else {
			imgui.TextUnformatted("Selected IAP Chart: " + *selectedID)
		}

		const (
			margin          float32 = 20
			labelSliderGap  float32 = 15
			sliderButtonGap float32 = 20
		)
		rowY := controlsY + imgui.FontSize() + 18
		label := "Adjust Opacity:"
		labelWidth := imgui.CalcTextSize(label).X
		buttonLabel := "Disable Chart Overlay"
		if !*overlayEnabled {
			buttonLabel = "Enable Chart Overlay"
		}
		buttonW := buttonWidth(buttonLabel)
		buttonX := size[0] - margin - buttonW
		sliderX := margin + labelWidth + labelSliderGap
		sliderW := buttonX - sliderButtonGap - sliderX

		imgui.SetCursorScreenPos(imgui.Vec2{
			X: margin, Y: rowY + (buttonHeight-imgui.FontSize())*0.5,
		})
		imgui.TextUnformatted(label)
		drawIAPOpacitySlider(sliderX, rowY, sliderW, opacityPercent)

		imgui.SetCursorScreenPos(imgui.Vec2{X: buttonX, Y: rowY})
		if button(buttonLabel, false) {
			*overlayEnabled = !*overlayEnabled
		}
	}
	imgui.End()
	imgui.PopStyleVarV(4)
	imgui.PopStyleColor()
	return search
}

// drawIAPOpacitySlider implements a minimal line-and-circle slider. The
// invisible hit region is taller than the track for comfortable dragging;
// the visible slider never displays a percentage or value label.
func drawIAPOpacitySlider(x, y, width float32, percent *int32) {
	const (
		knobRadius  float32 = 8
		trackRadius float32 = 2
	)
	if width <= 2*knobRadius {
		return
	}
	imgui.SetCursorScreenPos(imgui.Vec2{X: x, Y: y})
	imgui.InvisibleButtonV("##iap-chart-opacity-slider",
		imgui.Vec2{X: width, Y: buttonHeight}, imgui.ButtonFlagsMouseButtonLeft)

	startX := x + knobRadius
	endX := x + width - knobRadius
	if imgui.IsItemActive() {
		// ImGui retains the active item while dragging beyond its hit area.
		// Clamping also makes clicking either end select exactly 1% or 100%.
		mouseX := imgui.CurrentIO().MousePos().X
		t := (mouseX - startX) / (endX - startX)
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
		*percent = clampIAPOpacityPercent(1 + int32(t*99+0.5))
	}

	value := clampIAPOpacityPercent(*percent)
	t := float32(value-1) / 99
	midY := y + buttonHeight*0.5
	dl := imgui.WindowDrawList()
	track := imgui.ColorU32Vec4(rgb(170, 172, 179))
	fill := imgui.ColorU32Vec4(rgb(235, 237, 240))
	outline := imgui.ColorU32Vec4(rgb(65, 68, 76))
	dl.AddRectFilledV(
		imgui.Vec2{X: startX, Y: midY - trackRadius},
		imgui.Vec2{X: endX, Y: midY + trackRadius},
		track, trackRadius, imgui.DrawFlagsRoundCornersAll,
	)
	knob := imgui.Vec2{X: startX + t*(endX-startX), Y: midY}
	dl.AddCircleFilledV(knob, knobRadius, fill, 24)
	dl.AddCircleV(knob, knobRadius, outline, 24, 1.5)
}
