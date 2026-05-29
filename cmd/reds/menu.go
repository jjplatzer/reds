// cmd/reds/menu.go
//
// The startup menu, a faithful port of ui/menu.cpp: a "Display Type" dropdown
// locked to ASDE-X, a "Facility" dropdown populated from the ASDE-X videomaps,
// and Cancel / Confirm buttons. Confirm will eventually hand the Selection off
// to the ASDE-X / STARS / ERAM scope; for now it just returns the choice.

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/juliusplatzer/reds/util"

	"github.com/AllenDang/cimgui-go/imgui"
)

// DisplayMode is the scope the user is launching into. The dropdown is locked
// to ASDEX for now, but the enum carries all three so Confirm can dispatch to
// the right scope once they exist.
type DisplayMode int

const (
	DisplayASDEX DisplayMode = iota
	DisplaySTARS
	DisplayERAM
)

func (d DisplayMode) String() string {
	switch d {
	case DisplaySTARS:
		return "STARS"
	case DisplayERAM:
		return "ERAM"
	default:
		return "ASDE-X"
	}
}

// Selection is what the menu produces on Confirm.
type Selection struct {
	Mode    DisplayMode
	Airport string // ICAO, e.g. "KATL"
}

// menuResult signals how the menu frame ended.
type menuResult int

const (
	menuPending menuResult = iota
	menuConfirmed
	menuCancelled
)

// menu holds the menu's transient UI state across frames.
type menu struct {
	airports      []string
	displayIndex  int // locked to 0 (ASDE-X)
	facilityIndex int
	firstFrame    bool
	selection     Selection
}

// newMenu loads the facility list, mirroring loadAsdexAirports(): every
// *.geojson.gz under resources/videomaps/asdex, reduced to its ICAO prefix.
func newMenu() *menu {
	return &menu{
		airports:   loadAsdexAirports(),
		firstFrame: true,
	}
}

func loadAsdexAirports() []string {
	dir := util.FindProjectRelativeDir(filepath.Join("resources", "videomaps", "asdex"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var icaos []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".geojson.gz") {
			continue
		}
		// Take the text before the first '.', matching the C++ logic.
		if i := strings.IndexByte(name, '.'); i >= 0 {
			icaos = append(icaos, name[:i])
		}
	}
	sort.Strings(icaos)
	return icaos
}

// draw renders one frame of the menu and returns whether it is still pending,
// confirmed, or cancelled. The window fills the GLFW client area; the OS title
// bar provides the "nascope" title, as the QDialog did.
func (m *menu) draw(displaySize [2]float32) menuResult {
	imgui.SetNextWindowPosV(imgui.Vec2{X: 0, Y: 0}, imgui.CondAlways, imgui.Vec2{})
	imgui.SetNextWindowSize(imgui.Vec2{X: displaySize[0], Y: displaySize[1]})

	flags := imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize |
		imgui.WindowFlagsNoMove | imgui.WindowFlagsNoCollapse |
		imgui.WindowFlagsNoScrollbar | imgui.WindowFlagsNoSavedSettings |
		imgui.WindowFlagsNoBringToFrontOnFocus

	// Window background + content margins (QVBoxLayout 20/20/20/16).
	imgui.PushStyleColorVec4(imgui.ColWindowBg, colDialogBg)
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{X: 20, Y: 20})

	result := menuPending
	imgui.BeginV("nascope##menu", nil, flags)

	// Display Type — locked to ASDE-X, grayed, not selectable.
	label("Display Type")
	dropdown("##displayType", statePast, []string{DisplayASDEX.String()}, &m.displayIndex, false)

	imgui.Dummy(imgui.Vec2{X: 0, Y: 12})

	// Facility — populated from the ASDE-X videomaps.
	label("Facility")
	if m.firstFrame {
		imgui.SetKeyboardFocusHere()
	}
	dropdown("##facility", stateCurrent, m.airports, &m.facilityIndex, len(m.airports) > 0)

	// Push the buttons to the bottom of the window.
	avail := imgui.ContentRegionAvail()
	if spacer := avail.Y - buttonHeight - 4; spacer > 0 {
		imgui.Dummy(imgui.Vec2{X: 0, Y: spacer})
	}

	// Right-aligned Cancel + Confirm.
	spacing := imgui.CurrentStyle().ItemSpacing().X
	total := buttonWidth("Cancel") + buttonWidth("Confirm") + spacing
	if rowAvail := imgui.ContentRegionAvail().X; rowAvail > total {
		imgui.SetCursorPosX(imgui.CursorPosX() + (rowAvail - total))
	}

	if button("Cancel", false) {
		result = menuCancelled
	}
	imgui.SameLine()
	confirm := button("Confirm", true)

	imgui.End()
	imgui.PopStyleVar()
	imgui.PopStyleColor()

	// Modal keys: Enter confirms, Escape cancels (QDialog default/reject).
	enter := imgui.IsKeyPressedBool(imgui.KeyEnter) || imgui.IsKeyPressedBool(imgui.KeyKeypadEnter)
	if confirm || (enter && result == menuPending) {
		if len(m.airports) > 0 {
			result = menuConfirmed
		}
	}
	if imgui.IsKeyPressedBool(imgui.KeyEscape) {
		result = menuCancelled
	}

	if result == menuConfirmed {
		m.selection = Selection{
			Mode:    DisplayMode(m.displayIndex), // ASDE-X today
			Airport: m.airports[m.facilityIndex],
		}
	}

	m.firstFrame = false
	return result
}
