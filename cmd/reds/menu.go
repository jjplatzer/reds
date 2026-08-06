// cmd/reds/menu.go
//
// The startup menu, a faithful port of ui/menu.cpp: a "Display Type" dropdown
// populated with launchable display types, a mode-dependent "Facility"
// dropdown, and Cancel / Confirm buttons.

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/juliusplatzer/reds/eram"
	"github.com/juliusplatzer/reds/util"

	"github.com/AllenDang/cimgui-go/imgui"
)

// DisplayMode is the scope the user is launching into.
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

var startupDisplayModes = []DisplayMode{
	DisplayASDEX,
	DisplayERAM,
}

var startupDisplayNames = []string{
	DisplayASDEX.String(),
	DisplayERAM.String(),
}

// Selection is what the menu produces on Confirm.
type Selection struct {
	Mode     DisplayMode
	Facility string
	Sector   *eram.Sector
}

func (s Selection) ScopeTitle() string {
	switch s.Mode {
	case DisplayERAM:
		return s.Facility + " ERAM"
	default:
		return s.Facility + " ASDE-X"
	}
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
	asdexFacilities []string
	eramFacilities  []string

	displayIndex  int
	facilityIndex int
	sectorIndex   int

	loadedEramFacility string
	eramFacility       eram.Facility
	eramSectorLabels   []string
	eramLoadErr        error

	firstFrame bool
	selection  Selection
}

// newMenu loads the facility list, mirroring loadAsdexAirports(): every
// *.geojson.zst under resources/videomaps/asdex, reduced to its ICAO prefix.
func newMenu() *menu {
	m := &menu{
		asdexFacilities: loadAsdexAirports(),
		eramFacilities:  loadEramFacilities(),
		firstFrame:      true,
	}
	if m.currentDisplayMode() == DisplayERAM {
		m.loadCurrentEramFacility()
	}
	return m
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
		if !strings.HasSuffix(name, ".geojson.zst") {
			continue
		}
		// Take the text before the first '.', matching the C++ logic.
		if i := strings.IndexByte(name, '.'); i >= 0 {
			icaos = append(icaos, name[:i])
		}
	}
	sort.Strings(icaos)
	if len(icaos) <= 1 {
		return icaos
	}
	unique := icaos[:0]
	for _, icao := range icaos {
		if len(unique) == 0 || unique[len(unique)-1] != icao {
			unique = append(unique, icao)
		}
	}
	return unique
}

func loadEramFacilities() []string {
	dir := util.FindProjectRelativeDir(filepath.Join("resources", "configs", "eram"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	facilities := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}

		facility := strings.TrimSuffix(name, ".json")
		if facility != "" {
			facilities = append(facilities, facility)
		}
	}

	sort.Strings(facilities)
	return facilities
}

func (m *menu) currentSelection() (Selection, bool) {
	if m == nil {
		return Selection{}, false
	}

	switch m.currentDisplayMode() {
	case DisplayASDEX:
		facilities := m.asdexFacilities
		if len(facilities) == 0 {
			return Selection{Mode: DisplayASDEX}, false
		}
		m.clampFacilityIndex(facilities)

		return Selection{
			Mode:     DisplayASDEX,
			Facility: facilities[m.facilityIndex],
		}, true

	case DisplayERAM:
		facilities := m.eramFacilities
		if len(facilities) == 0 || m.eramLoadErr != nil {
			return Selection{Mode: DisplayERAM}, false
		}
		m.clampFacilityIndex(facilities)
		if m.loadedEramFacility != facilities[m.facilityIndex] {
			m.loadCurrentEramFacility()
		}
		if len(m.eramFacility.Sectors) == 0 {
			return Selection{Mode: DisplayERAM}, false
		}
		if m.sectorIndex < 0 {
			m.sectorIndex = 0
		}
		if m.sectorIndex >= len(m.eramFacility.Sectors) {
			m.sectorIndex = len(m.eramFacility.Sectors) - 1
		}

		sector := m.eramFacility.Sectors[m.sectorIndex]
		return Selection{
			Mode:     DisplayERAM,
			Facility: facilities[m.facilityIndex],
			Sector:   &sector,
		}, true

	default:
		return Selection{}, false
	}
}

func (m *menu) currentDisplayMode() DisplayMode {
	if m == nil || len(startupDisplayModes) == 0 {
		return DisplayASDEX
	}
	if m.displayIndex < 0 {
		m.displayIndex = 0
	}
	if m.displayIndex >= len(startupDisplayModes) {
		m.displayIndex = len(startupDisplayModes) - 1
	}
	return startupDisplayModes[m.displayIndex]
}

func (m *menu) currentFacilities() []string {
	if m == nil {
		return nil
	}

	switch m.currentDisplayMode() {
	case DisplayERAM:
		return m.eramFacilities
	default:
		return m.asdexFacilities
	}
}

func (m *menu) handleDisplayChanged() {
	if m == nil {
		return
	}

	m.facilityIndex = 0
	m.sectorIndex = 0
	if m.currentDisplayMode() == DisplayERAM {
		m.loadCurrentEramFacility()
	}
}

func (m *menu) handleFacilityChanged() {
	if m == nil {
		return
	}

	m.sectorIndex = 0
	if m.currentDisplayMode() == DisplayERAM {
		m.loadCurrentEramFacility()
	}
}

func (m *menu) loadCurrentEramFacility() {
	if m == nil {
		return
	}

	m.loadedEramFacility = ""
	m.eramFacility = eram.Facility{}
	m.eramSectorLabels = nil
	m.sectorIndex = 0
	m.eramLoadErr = nil

	facilities := m.eramFacilities
	if len(facilities) == 0 {
		return
	}
	m.clampFacilityIndex(facilities)

	artcc := facilities[m.facilityIndex]
	config, err := eram.LoadFacility(artcc)
	if err != nil {
		m.eramLoadErr = err
		return
	}

	m.loadedEramFacility = artcc
	m.eramFacility = config
	m.eramSectorLabels = make([]string, 0, len(config.Sectors))
	for _, sector := range config.Sectors {
		m.eramSectorLabels = append(m.eramSectorLabels, sector.Label())
	}
}

func (m *menu) clampFacilityIndex(facilities []string) {
	if m == nil || len(facilities) == 0 {
		return
	}
	if m.facilityIndex < 0 {
		m.facilityIndex = 0
	}
	if m.facilityIndex >= len(facilities) {
		m.facilityIndex = len(facilities) - 1
	}
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

	label("Display Type")
	displayChanged := dropdown(
		"##displayType",
		stateCurrent,
		startupDisplayNames,
		&m.displayIndex,
		true,
	)
	if displayChanged {
		m.handleDisplayChanged()
	}

	imgui.Dummy(imgui.Vec2{X: 0, Y: 12})

	label("Facility")
	if m.firstFrame {
		imgui.SetKeyboardFocusHere()
	}
	facilities := m.currentFacilities()
	facilityChanged := dropdown(
		"##facility",
		stateCurrent,
		facilities,
		&m.facilityIndex,
		len(facilities) > 0,
	)
	if facilityChanged {
		m.handleFacilityChanged()
	}

	if m.currentDisplayMode() == DisplayERAM {
		imgui.Dummy(imgui.Vec2{X: 0, Y: 12})

		label("Sector")
		dropdown(
			"##sector",
			stateCurrent,
			m.eramSectorLabels,
			&m.sectorIndex,
			len(m.eramSectorLabels) > 0 && m.eramLoadErr == nil,
		)
	}

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
		if selection, valid := m.currentSelection(); valid {
			m.selection = selection
			result = menuConfirmed
		}
	}
	if imgui.IsKeyPressedBool(imgui.KeyEscape) {
		result = menuCancelled
	}

	if result == menuConfirmed {
		selection, ok := m.currentSelection()
		if !ok {
			result = menuPending
		} else {
			m.selection = selection
		}
	}

	m.firstFrame = false
	return result
}
