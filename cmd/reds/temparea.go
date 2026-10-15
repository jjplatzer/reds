// cmd/reds/temparea.go
//
// ASDE-X temporary map area editor: dialog controls, the native auxiliary
// window, and its IPC with the main REDS process.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AllenDang/cimgui-go/imgui"
	implogl3 "github.com/AllenDang/cimgui-go/impl/opengl3"
	"github.com/juliusplatzer/reds/asdex"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/util"
)

const (
	tempAreaDialogWidth  = float32(470)
	tempAreaDialogHeight = float32(388)
	tempAreaColumnGap    = float32(12)
)

var tempAreaTypes = []string{"Closed Area", "Restriction Area"}

type tempAreaDialog struct {
	open    bool
	airport string
	loadErr error

	taxiways        []string
	taxiwayLabels   []string
	runways         []string
	boundaryOptions []string
	areaTypeIndex   int
	taxiwayIndex    int
	fromIndex       int
	toIndex         int
	sendMessage     func(string)
	status          string
	statusError     bool
}

// loadTempAreaNames reads only the fields needed to populate the dialog. The
// graph may be inferred and imperfect, so names here are NOT a claim that a
// proposed closure is physically valid or that two endpoints are connected.
func loadTempAreaNames(airport string) ([]string, []string, error) {
	path := filepath.ToSlash(filepath.Join("asdex", "surface", airport+".json.zst"))
	if !util.ResourceExists(path) {
		return nil, nil, fmt.Errorf("surface file not found: %s", path)
	}

	var source struct {
		Runways []struct {
			ID string `json:"id"`
		} `json:"rwys"`
		Graph struct {
			Edges []struct {
				Names []string `json:"names"`
			} `json:"edges"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(util.LoadResourceBytes(path), &source); err != nil {
		return nil, nil, fmt.Errorf("decode %s: %w", path, err)
	}

	seen := make(map[string]bool)
	taxiways := make([]string, 0)
	for _, edge := range source.Graph.Edges {
		for _, name := range edge.Names {
			name = strings.TrimSpace(name)
			if name != "" && !seen[name] {
				seen[name] = true
				taxiways = append(taxiways, name)
			}
		}
	}
	sort.Strings(taxiways)
	if len(taxiways) == 0 {
		return nil, nil, fmt.Errorf("no named taxiway graph edges in %s", path)
	}

	runways := make([]string, 0, len(source.Runways))
	seen = make(map[string]bool)
	for _, runway := range source.Runways {
		name := strings.TrimSpace(runway.ID)
		if name != "" && !seen[name] {
			seen[name] = true
			runways = append(runways, name)
		}
	}
	sort.Strings(runways)
	return taxiways, runways, nil
}

func (d *tempAreaDialog) show(airport string) {
	// Reset selections when explicitly opened or when the airport changes.
	d.airport = airport
	d.areaTypeIndex, d.taxiwayIndex = 0, 0
	d.fromIndex, d.toIndex = 0, 0
	d.taxiways = nil
	d.taxiwayLabels = nil
	d.runways = nil
	d.boundaryOptions = nil
	d.loadErr = nil
	d.status = ""

	d.taxiways, d.runways, d.loadErr = loadTempAreaNames(airport)
	if d.loadErr == nil {
		d.taxiwayLabels = make([]string, len(d.taxiways))
		for i, name := range d.taxiways {
			d.taxiwayLabels[i] = "TWY " + name
		}
		d.rebuildEndpoints()
	}
	d.open = true
}

// rebuildEndpoints intentionally exposes available labels for a design review.
// Endpoint connectivity/ordering will be checked by a future closure resolver.
func (d *tempAreaDialog) rebuildEndpoints() {
	d.boundaryOptions = []string{"No Endpoint"}
	for i, taxiway := range d.taxiways {
		if i != d.taxiwayIndex {
			d.boundaryOptions = append(d.boundaryOptions, "TWY "+taxiway)
		}
	}
	for _, runway := range d.runways {
		d.boundaryOptions = append(d.boundaryOptions, "RWY "+runway)
	}
	d.fromIndex, d.toIndex = 0, 0
}

func (d *tempAreaDialog) drawBody() {
	if d.loadErr != nil {
		imgui.Text("Unable to load airport surface graph:")
		imgui.TextWrapped(d.loadErr.Error())
		imgui.Dummy(imgui.Vec2{X: 0, Y: 14})
		if button("Close", false) {
			d.open = false
		}
		return
	}

	label("Temporary Map Area Type")
	if dropdown("##area-type", stateCurrent, tempAreaTypes, &d.areaTypeIndex, true) {
		d.selectionChanged()
	}
	imgui.Dummy(imgui.Vec2{X: 0, Y: 12})

	label("Map Area")
	if dropdown("##taxiway", stateCurrent, d.taxiwayLabels, &d.taxiwayIndex, true) {
		d.rebuildEndpoints()
		d.selectionChanged()
	}
	imgui.Dummy(imgui.Vec2{X: 0, Y: 12})

	start := imgui.CursorPos()
	colWidth := (imgui.ContentRegionAvail().X - tempAreaColumnGap) / 2
	label("From")
	imgui.SetCursorPos(imgui.Vec2{X: start.X + colWidth + tempAreaColumnGap, Y: start.Y})
	label("To")
	fieldsY := start.Y + imgui.FontSize() + 7
	imgui.SetCursorPos(imgui.Vec2{X: start.X, Y: fieldsY})
	if dropdownSized("##from", stateCurrent, d.boundaryOptions, &d.fromIndex, true, colWidth) {
		d.selectionChanged()
	}
	imgui.SetCursorPos(imgui.Vec2{X: start.X + colWidth + tempAreaColumnGap, Y: fieldsY})
	if dropdownSized("##to", stateCurrent, d.boundaryOptions, &d.toIndex, true, colWidth) {
		d.selectionChanged()
	}
	imgui.SetCursorPos(imgui.Vec2{X: start.X, Y: fieldsY + controlHeight + 18})

	// Keep the buttons in the same footer position as the UI-only version.
	const footerHeight = float32(buttonHeight + 14)
	if space := imgui.ContentRegionAvail().Y - footerHeight; space > 0 {
		imgui.Dummy(imgui.Vec2{X: 0, Y: space})
	}
	if d.status != "" {
		// Show up to two status lines in the reserved space above the footer.
		// In particular, an invalid graph selection must be readable rather
		// than just silently failing to paint on the scope.
		lines := tempAreaStatusLines(d.status, 53)
		pos := imgui.CursorScreenPos()
		pos.Y -= float32(len(lines))*19 + 6
		col := colLabel
		if d.statusError {
			col = rgb(255, 171, 157)
		}
		for _, line := range lines {
			imgui.WindowDrawList().AddTextVec2(pos, imgui.ColorU32Vec4(col), line)
			pos.Y += 19
		}
	}
	if x := imgui.CursorPosX(); x >= 0 {
		width := imgui.ContentRegionAvail().X
		space := imgui.CurrentStyle().ItemSpacing().X
		buttonsWidth := buttonWidth("Cancel") + buttonWidth("Preview") +
			buttonWidth("Create Closure") + 2*space
		if width > buttonsWidth {
			imgui.SetCursorPosX(x + width - buttonsWidth)
		}
	}
	if button("Cancel", false) {
		d.open = false
	}
	imgui.SameLine()
	if button("Preview", false) && d.sendMessage != nil {
		d.sendMessage("preview")
	}
	imgui.SameLine()
	if button("Create Closure", true) && d.sendMessage != nil {
		d.sendMessage("create")
	}

	if imgui.IsKeyPressedBool(imgui.KeyEscape) {
		d.open = false
	}
}

// selectionChanged clears the previous preview immediately; nothing gets
// committed until the user explicitly chooses Create Closure.
func (d *tempAreaDialog) selectionChanged() {
	d.status = ""
	if d.sendMessage != nil {
		d.sendMessage("clear")
	}
}

func tempAreaStatusLines(message string, maxRunes int) []string {
	words := strings.Fields(message)
	var lines []string
	current := ""
	for _, word := range words {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if len([]rune(candidate)) > maxRunes && current != "" {
			lines = append(lines, current)
			current = word
		} else {
			current = candidate
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	if len(lines) > 2 {
		lines = lines[:2]
		suffix := "..."
		r := []rune(lines[1])
		if len(r) > maxRunes-len(suffix) {
			r = r[:maxRunes-len(suffix)]
		}
		lines[1] = string(r) + suffix
	}
	return lines
}

// Native temporary map area window and communication with the scope.
// A message emitted by the native editor or returned by its parent.
// All UI changes are validated and applied by the ASDE-X process itself.
type tempAreaWindowMessage struct {
	Action  string                 `json:"action"`
	Request asdex.GraphAreaRequest `json:"request,omitempty"`
	OK      bool                   `json:"ok,omitempty"`
	Message string                 `json:"message,omitempty"`
}

type tempAreaWindowProcess struct {
	cmd     *exec.Cmd
	done    chan struct{}
	airport string
	input   io.WriteCloser
	events  chan tempAreaWindowMessage
}

// show keeps at most one editor open per REDS scope. Invoking the menu again
// replaces the existing editor so it reliably reappears in front of the scope.
func (w *tempAreaWindowProcess) show(airport string) error {
	w.close()

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate REDS executable: %w", err)
	}
	cmd := exec.Command(executable, "-temp-area-dialog="+airport)
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr
	input, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("editor stdin: %w", err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("editor stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		return fmt.Errorf("launch temporary map area window: %w", err)
	}
	w.cmd = cmd
	w.done = make(chan struct{})
	w.input = input
	w.events = make(chan tempAreaWindowMessage, 16)
	w.airport = airport
	events := w.events
	done := w.done
	go func() {
		defer close(done)
		defer close(events)
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			var msg tempAreaWindowMessage
			if json.Unmarshal(scanner.Bytes(), &msg) == nil {
				events <- msg
			}
		}
		// StdoutPipe must be fully read before Wait to avoid losing the final
		// "cancel" message as the child exits.
		_ = cmd.Wait()
	}()
	return nil
}

func (w *tempAreaWindowProcess) close() {
	if w.cmd != nil && w.cmd.Process != nil {
		select {
		case <-w.done:
		default:
			_ = w.cmd.Process.Kill()
		}
	}
	if w.input != nil {
		_ = w.input.Close()
	}
	w.cmd, w.done, w.airport, w.input, w.events = nil, nil, "", nil, nil
}

// Drain only on the parent render thread; never mutate the pane from the
// background stdout reader. This keeps TempData and OpenGL resources single-threaded.
func (w *tempAreaWindowProcess) drain(pane *asdex.ASDEXPane) {
	if pane == nil || w.events == nil {
		return
	}
	for {
		select {
		case msg, ok := <-w.events:
			if !ok {
				pane.ClearGraphAreaPreview()
				w.events = nil
				return
			}
			switch msg.Action {
			case "clear", "cancel":
				pane.ClearGraphAreaPreview()
			case "preview", "create":
				count, err := pane.ApplyGraphArea(msg.Request, msg.Action == "preview")
				result := tempAreaWindowMessage{Action: "result", OK: err == nil}
				if err != nil {
					result.Message = err.Error()
				} else if msg.Action == "preview" {
					result.Message = fmt.Sprintf("Previewing %d graph segments", count)
				} else {
					result.Message = fmt.Sprintf("Created area covering %d graph segments", count)
				}
				if w.input != nil {
					_ = json.NewEncoder(w.input).Encode(result)
				}
			}
		default:
			return
		}
	}
}

func runTempAreaWindow(airport string) error {
	imgui.CreateContext()
	defer imgui.DestroyContext()
	imgui.CurrentIO().SetIniFilename("")

	plat, err := platform.New(&platform.Config{
		Title:             "Define Temporary Map Area",
		InitialWindowSize: [2]int{int(tempAreaDialogWidth), int(tempAreaDialogHeight)},
		MinWindowSize:     [2]int{int(tempAreaDialogWidth), int(tempAreaDialogHeight)},
		Resizable:         false,
	})
	if err != nil {
		return fmt.Errorf("initialize temporary area window: %w", err)
	}
	defer plat.Dispose()
	plat.SetWindowDecorated(false)
	loadFont()

	var dialog tempAreaDialog
	dialog.sendMessage = func(action string) {
		req := asdex.GraphAreaRequest{}
		if len(dialog.taxiways) > 0 && dialog.taxiwayIndex < len(dialog.taxiways) && dialog.fromIndex < len(dialog.boundaryOptions) && dialog.toIndex < len(dialog.boundaryOptions) {
			req = asdex.GraphAreaRequest{AreaType: tempAreaTypes[dialog.areaTypeIndex], Taxiway: dialog.taxiways[dialog.taxiwayIndex], From: dialog.boundaryOptions[dialog.fromIndex], To: dialog.boundaryOptions[dialog.toIndex]}
		}
		_ = json.NewEncoder(os.Stdout).Encode(tempAreaWindowMessage{Action: action, Request: req})
	}
	dialog.show(airport)
	replies := make(chan tempAreaWindowMessage, 8)
	go func() {
		defer close(replies)
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			var response tempAreaWindowMessage
			if json.Unmarshal(scanner.Bytes(), &response) == nil {
				replies <- response
			}
		}
	}()
	for dialog.open && !plat.ShouldStop() {
		select {
		case reply, ok := <-replies:
			if ok && reply.Action == "result" {
				dialog.status = reply.Message
				dialog.statusError = !reply.OK
			}
		default:
		}
		plat.ProcessEvents()
		plat.NewFrame()
		imgui.NewFrame()
		dialog.drawNative(plat)
		imgui.Render()
		bg := colDialogBg
		plat.Clear(bg.X, bg.Y, bg.Z)
		implogl3.RenderDrawData(imgui.CurrentDrawData())
		plat.PostRender()
	}
	dialog.sendMessage("cancel")
	return nil
}

// drawNative owns the whole auxiliary GLFW framebuffer, so unlike an embedded
// ImGui dialog it can be moved outside the main REDS window. The titlebar and
// close button intentionally reuse the main window's colors, glyph and size.
func (d *tempAreaDialog) drawNative(plat platform.Platform) {
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
	imgui.BeginV("##temporary-map-area-root", nil, flags)

	d.drawNativeTitleBar(plat, size[0])
	imgui.SetCursorScreenPos(imgui.Vec2{X: 18, Y: scopeTitleBarHeight + 16})
	d.drawBody()

	imgui.End()
	imgui.PopStyleVarV(4)
	imgui.PopStyleColor()
}

func (d *tempAreaDialog) drawNativeTitleBar(plat platform.Platform, width float32) {
	barMax := imgui.Vec2{X: width, Y: scopeTitleBarHeight}
	imgui.WindowDrawList().AddRectFilledV(imgui.Vec2{}, barMax,
		imgui.ColorU32Vec4(titleBarBg), 0, imgui.DrawFlagsNone)

	// Align the title's left edge with the dropdowns below it.
	imgui.WindowDrawList().AddTextVec2(
		imgui.Vec2{X: 18, Y: (scopeTitleBarHeight - imgui.FontSize()) / 2},
		imgui.ColorU32Vec4(titleBarFg), "Define Temporary Map Area")

	closeMin := imgui.Vec2{X: width - titleBarButtonWidth, Y: 0}
	imgui.SetCursorScreenPos(closeMin)
	if imgui.InvisibleButtonV("##temp-area-close",
		imgui.Vec2{X: titleBarButtonWidth, Y: scopeTitleBarHeight},
		imgui.ButtonFlagsMouseButtonLeft) {
		d.open = false
	}
	if imgui.IsItemHovered() {
		imgui.WindowDrawList().AddRectFilledV(closeMin, barMax,
			imgui.ColorU32Vec4(titleBarCloseHover), 0, imgui.DrawFlagsNone)
	}
	drawTitleBarSymbolGlyph(titleBarGlyphClose, symbolsFont8, titleBarButtonSymbolSize,
		closeMin, barMax, titleBarFg)

	imgui.SetCursorScreenPos(imgui.Vec2{})
	imgui.InvisibleButtonV("##temp-area-drag",
		imgui.Vec2{X: width - titleBarButtonWidth, Y: scopeTitleBarHeight},
		imgui.ButtonFlagsMouseButtonLeft)
	if imgui.IsItemActive() && imgui.IsMouseDragging(imgui.MouseButtonLeft) {
		delta := imgui.CurrentIO().MouseDelta()
		plat.MoveWindowBy(delta.X, delta.Y)
	}
}
