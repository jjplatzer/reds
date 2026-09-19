// cmd/reds/menu.go
//
// The startup menu, a faithful port of ui/menu.cpp: a "Display Type" dropdown
// populated with launchable display types, a mode-dependent "Facility"
// dropdown, and Cancel / Confirm buttons.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/juliusplatzer/reds/eram"
	"github.com/juliusplatzer/reds/util"

	"github.com/AllenDang/cimgui-go/imgui"
)

// starsControlPosition is the identity and startup display information needed to
// launch a STARS TCW. ID remains authoritative because visible labels are not
// necessarily unique within a TRACON.
type starsControlPosition struct {
	ID       string `json:"id"`
	Callsign string `json:"callsign"`
	TCP      string `json:"tcp"`
}

func (p starsControlPosition) Label() string {
	tcp := strings.TrimSpace(p.TCP)
	callsign := strings.TrimSpace(p.Callsign)
	switch {
	case tcp != "" && callsign != "":
		return tcp + " - " + callsign
	case callsign != "":
		// A small number of valid CRC positions do not have a TCP assignment.
		return callsign
	case tcp != "":
		return tcp
	default:
		return strings.TrimSpace(p.ID)
	}
}

// starsMenuFacility contains the portion of a generated STARS facility config needed
// by the startup menu. Unknown fields remain available in the JSON resource
// for the eventual TCW implementation and are intentionally ignored here.
type starsMenuFacility struct {
	ARTCC            string                 `json:"artcc"`
	Facility         string                 `json:"facility"`
	Name             string                 `json:"name"`
	ControlPositions []starsControlPosition `json:"controlPositions"`
}

func loadStarsARTCCs() ([]string, error) {
	root := util.FindProjectRelativeDir(filepath.Join("resources", "configs", "stars"))
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("STARS: read facility root %s: %w", root, err)
	}

	artccs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		artcc := strings.ToUpper(strings.TrimSpace(entry.Name()))
		if artcc != "" {
			artccs = append(artccs, artcc)
		}
	}
	sort.Strings(artccs)
	return artccs, nil
}

func loadStarsTRACONs(artcc string) ([]string, error) {
	artcc, err := normalizedStarsResourceCode("ARTCC", artcc)
	if err != nil {
		return nil, err
	}

	dir := util.FindProjectRelativeDir(filepath.Join("resources", "configs", "stars", artcc))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("STARS: read %s facilities: %w", artcc, err)
	}

	tracons := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		if name != "" {
			tracons = append(tracons, strings.ToUpper(name))
		}
	}
	sort.Strings(tracons)
	return tracons, nil
}

func loadStarsMenuFacility(artcc, tracon string) (starsMenuFacility, error) {
	artcc, err := normalizedStarsResourceCode("ARTCC", artcc)
	if err != nil {
		return starsMenuFacility{}, err
	}
	tracon, err = normalizedStarsResourceCode("TRACON", tracon)
	if err != nil {
		return starsMenuFacility{}, err
	}

	path := filepath.ToSlash(filepath.Join(
		"resources", "configs", "stars", artcc, tracon+".json",
	))
	if !util.ResourceExists(path) {
		return starsMenuFacility{}, fmt.Errorf("STARS: facility config %s not found", path)
	}

	var facility starsMenuFacility
	if err := json.Unmarshal(util.LoadResourceBytes(path), &facility); err != nil {
		return starsMenuFacility{}, fmt.Errorf("STARS: decode %s: %w", path, err)
	}
	return facility, nil
}

func normalizedStarsResourceCode(kind, code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return "", fmt.Errorf("STARS: empty %s", kind)
	}
	if strings.ContainsAny(code, `/\\`) || code == "." || code == ".." {
		return "", fmt.Errorf("STARS: invalid %s %q", kind, code)
	}
	return code, nil
}

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
	DisplaySTARS,
	DisplayERAM,
}

var startupDisplayNames = []string{
	DisplayASDEX.String(),
	DisplaySTARS.String(),
	DisplayERAM.String(),
}

// Selection is what the menu produces on Confirm.
type Selection struct {
	Mode     DisplayMode
	Facility string
	TRACON   string
	Position *starsControlPosition
	Sector   *eram.Sector
}

func (s Selection) ScopeTitle() string {
	switch s.Mode {
	case DisplaySTARS:
		parts := []string{DisplaySTARS.String(), s.Facility, s.TRACON}
		if s.Position != nil {
			if label := s.Position.Label(); label != "" {
				parts = append(parts, label)
			}
		}
		return strings.Join(parts, " ")
	case DisplayERAM:
		parts := []string{DisplayERAM.String(), s.Facility}
		if s.Sector != nil {
			if name := strings.TrimSpace(s.Sector.Name); name != "" {
				parts = append(parts, name)
			}
		}
		return strings.Join(parts, " ")
	default:
		return strings.Join([]string{DisplayASDEX.String(), s.Facility}, " ")
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
	starsFacilities []string
	eramFacilities  []string

	displayIndex  int
	facilityIndex int
	traconIndex   int
	positionIndex int
	sectorIndex   int

	loadedStarsFacility string
	starsTRACONs        []string
	loadedStarsTRACON   string
	starsFacility       starsMenuFacility
	starsPositionLabels []string
	starsLoadErr        error

	loadedEramFacility string
	eramFacility       eram.Facility
	eramSectorLabels   []string
	eramLoadErr        error

	firstFrame bool
	selection  Selection
}

// newMenu discovers every launchable display facility from its resource tree.
func newMenu() *menu {
	starsFacilities, starsLoadErr := loadStarsARTCCs()
	m := &menu{
		asdexFacilities: loadAsdexAirports(),
		starsFacilities: starsFacilities,
		eramFacilities:  loadEramFacilities(),
		starsLoadErr:    starsLoadErr,
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

	case DisplaySTARS:
		facilities := m.starsFacilities
		if len(facilities) == 0 || m.starsLoadErr != nil {
			return Selection{Mode: DisplaySTARS}, false
		}
		m.clampFacilityIndex(facilities)
		if m.loadedStarsFacility != facilities[m.facilityIndex] {
			m.loadCurrentStarsFacility()
		}
		if len(m.starsTRACONs) == 0 || m.starsLoadErr != nil {
			return Selection{Mode: DisplaySTARS, Facility: facilities[m.facilityIndex]}, false
		}
		m.clampTRACONIndex()
		if m.loadedStarsTRACON != m.starsTRACONs[m.traconIndex] {
			m.loadCurrentStarsTRACON()
		}
		if len(m.starsFacility.ControlPositions) == 0 || m.starsLoadErr != nil {
			return Selection{
				Mode:     DisplaySTARS,
				Facility: facilities[m.facilityIndex],
				TRACON:   m.starsTRACONs[m.traconIndex],
			}, false
		}
		m.clampPositionIndex()

		position := m.starsFacility.ControlPositions[m.positionIndex]
		return Selection{
			Mode:     DisplaySTARS,
			Facility: facilities[m.facilityIndex],
			TRACON:   m.starsTRACONs[m.traconIndex],
			Position: &position,
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
	case DisplaySTARS:
		return m.starsFacilities
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
	m.traconIndex = 0
	m.positionIndex = 0
	m.sectorIndex = 0
	switch m.currentDisplayMode() {
	case DisplaySTARS:
		m.loadCurrentStarsFacility()
	case DisplayERAM:
		m.loadCurrentEramFacility()
	}
}

func (m *menu) handleFacilityChanged() {
	if m == nil {
		return
	}

	m.traconIndex = 0
	m.positionIndex = 0
	m.sectorIndex = 0
	switch m.currentDisplayMode() {
	case DisplaySTARS:
		m.loadCurrentStarsFacility()
	case DisplayERAM:
		m.loadCurrentEramFacility()
	}
}

func (m *menu) handleTRACONChanged() {
	if m == nil {
		return
	}
	m.positionIndex = 0
	m.loadCurrentStarsTRACON()
}

func (m *menu) loadCurrentStarsFacility() {
	if m == nil {
		return
	}

	m.loadedStarsFacility = ""
	m.starsTRACONs = nil
	m.loadedStarsTRACON = ""
	m.starsFacility = starsMenuFacility{}
	m.starsPositionLabels = nil
	m.traconIndex = 0
	m.positionIndex = 0
	m.starsLoadErr = nil

	facilities := m.starsFacilities
	if len(facilities) == 0 {
		return
	}
	m.clampFacilityIndex(facilities)

	artcc := facilities[m.facilityIndex]
	tracons, err := loadStarsTRACONs(artcc)
	if err != nil {
		m.starsLoadErr = err
		return
	}

	m.loadedStarsFacility = artcc
	m.starsTRACONs = tracons
	if len(tracons) != 0 {
		m.loadCurrentStarsTRACON()
	}
}

func (m *menu) loadCurrentStarsTRACON() {
	if m == nil {
		return
	}

	m.loadedStarsTRACON = ""
	m.starsFacility = starsMenuFacility{}
	m.starsPositionLabels = nil
	m.positionIndex = 0
	m.starsLoadErr = nil

	if len(m.starsFacilities) == 0 || len(m.starsTRACONs) == 0 {
		return
	}
	m.clampFacilityIndex(m.starsFacilities)
	m.clampTRACONIndex()

	artcc := m.starsFacilities[m.facilityIndex]
	tracon := m.starsTRACONs[m.traconIndex]
	config, err := loadStarsMenuFacility(artcc, tracon)
	if err != nil {
		m.starsLoadErr = err
		return
	}

	m.loadedStarsFacility = artcc
	m.loadedStarsTRACON = tracon
	m.starsFacility = config
	m.starsPositionLabels = make([]string, 0, len(config.ControlPositions))
	for _, position := range config.ControlPositions {
		m.starsPositionLabels = append(m.starsPositionLabels, position.Label())
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

func (m *menu) clampTRACONIndex() {
	if m == nil || len(m.starsTRACONs) == 0 {
		return
	}
	if m.traconIndex < 0 {
		m.traconIndex = 0
	}
	if m.traconIndex >= len(m.starsTRACONs) {
		m.traconIndex = len(m.starsTRACONs) - 1
	}
}

func (m *menu) clampPositionIndex() {
	if m == nil || len(m.starsFacility.ControlPositions) == 0 {
		return
	}
	if m.positionIndex < 0 {
		m.positionIndex = 0
	}
	if m.positionIndex >= len(m.starsFacility.ControlPositions) {
		m.positionIndex = len(m.starsFacility.ControlPositions) - 1
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

	switch m.currentDisplayMode() {
	case DisplaySTARS:
		imgui.Dummy(imgui.Vec2{X: 0, Y: 12})

		label("TRACON")
		traconChanged := dropdown(
			"##tracon",
			stateCurrent,
			m.starsTRACONs,
			&m.traconIndex,
			len(m.starsTRACONs) > 0 && m.starsLoadErr == nil,
		)
		if traconChanged {
			m.handleTRACONChanged()
		}

		imgui.Dummy(imgui.Vec2{X: 0, Y: 12})

		label("Position")
		dropdown(
			"##position",
			stateCurrent,
			m.starsPositionLabels,
			&m.positionIndex,
			len(m.starsPositionLabels) > 0 && m.starsLoadErr == nil,
		)

	case DisplayERAM:
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
