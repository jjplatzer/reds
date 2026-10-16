package asdex

// ASDE-X client-side preferences. The stored global TEMP DATA presets live
// here alongside the future ASDE-X PREF submenu persistence.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	tempDataPresetCount       = 88
	tempDataPresetTitleLength = 7
	tempDataPresetVersion     = 1
)

// CRC stores one TEMP DATA object per numbered slot. REDS keeps the entire
// geometry locally because there is no shared CRC TEMP DATA server to resolve IDs.
// The saved object can consist of multiple pavement components with one ID.
type tempDataPreset struct {
	Title  string             `json:"title"`
	Active bool               `json:"active"`
	Areas  []tempAreaSnapshot `json:"areas,omitempty"`
	Text   *tempTextSnapshot  `json:"text,omitempty"`
}

type tempAreaSnapshot struct {
	Type   TempDataType      `json:"type"`
	Points []redsmath.Vec2   `json:"points"`
	Holes  [][]redsmath.Vec2 `json:"holes,omitempty"`
}

type tempTextSnapshot struct {
	Location        redsmath.Vec2    `json:"location"`
	Line1           string           `json:"line1"`
	Line2           string           `json:"line2"`
	ShowDataBlock   *bool            `json:"showDataBlock,omitempty"`
	LeaderDirection *LeaderDirection `json:"leaderDirection,omitempty"`
	LeaderLength    *int             `json:"leaderLength,omitempty"`
	FontSize        *int             `json:"fontSize,omitempty"`
	Brightness      *int             `json:"brightness,omitempty"`
}

type tempDataPresetFile struct {
	Version int                                  `json:"version"`
	Airport string                               `json:"airport"`
	Slots   [tempDataPresetCount]*tempDataPreset `json:"slots"`
}

type tempDataPresetRepository struct {
	mu       sync.Mutex
	airport  string
	file     tempDataPresetFile
	revision uint64
}

var tempDataPresetRepositories = struct {
	sync.Mutex
	byAirport map[string]*tempDataPresetRepository
}{byAirport: make(map[string]*tempDataPresetRepository)}

func tempDataPresetsPath(airport string) (string, error) {
	airport = strings.ToUpper(strings.TrimSpace(airport))
	if len(airport) != 4 {
		return "", fmt.Errorf("invalid TEMP DATA airport: %q", airport)
	}
	for _, r := range airport {
		if r < 'A' || r > 'Z' {
			return "", fmt.Errorf("invalid TEMP DATA airport: %q", airport)
		}
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "REDS", "TempData", "ASDEX", airport+".json"), nil
}

func getTempDataPresetRepository(airport string) (*tempDataPresetRepository, error) {
	airport = strings.ToUpper(strings.TrimSpace(airport))
	tempDataPresetRepositories.Lock()
	defer tempDataPresetRepositories.Unlock()
	if repo := tempDataPresetRepositories.byAirport[airport]; repo != nil {
		return repo, nil
	}
	path, err := tempDataPresetsPath(airport)
	if err != nil {
		return nil, err
	}
	repo := &tempDataPresetRepository{airport: airport, file: tempDataPresetFile{Version: tempDataPresetVersion, Airport: airport}}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal(data, &repo.file); err != nil {
			return nil, fmt.Errorf("decode TEMP DATA presets: %w", err)
		}
		if repo.file.Version != tempDataPresetVersion || repo.file.Airport != airport {
			return nil, fmt.Errorf("invalid TEMP DATA preset file for %s", airport)
		}
		for i, slot := range repo.file.Slots {
			if slot == nil {
				continue
			}
			if !validTempDataPreset(slot) {
				return nil, fmt.Errorf("invalid TEMP DATA preset slot %02d", i+1)
			}
		}
	}
	tempDataPresetRepositories.byAirport[airport] = repo
	return repo, nil
}

func validTempDataPreset(slot *tempDataPreset) bool {
	if slot == nil || strings.TrimSpace(slot.Title) == "" || len(slot.Title) > tempDataPresetTitleLength || (slot.Text == nil && len(slot.Areas) == 0) || (slot.Text != nil && len(slot.Areas) != 0) {
		return false
	}
	if slot.Text != nil {
		return strings.TrimSpace(slot.Text.Line1) != ""
	}
	if len(slot.Areas) > 10000 {
		return false
	}
	for _, area := range slot.Areas {
		if (area.Type != TempDataClosedArea && area.Type != TempDataRestrictedArea) || len(area.Points) < 4 {
			return false
		}
	}
	return true
}

func (r *tempDataPresetRepository) snapshot() (tempDataPresetFile, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// All updates replace immutable slots. Consumers only read their snapshots.
	return r.file, r.revision
}

func (r *tempDataPresetRepository) change(slot int, fn func(*tempDataPresetFile) error) error {
	if slot < 1 || slot > tempDataPresetCount {
		return fmt.Errorf("invalid TEMP DATA slot %d", slot)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	updated := r.file
	if err := fn(&updated); err != nil {
		return err
	}
	data, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return err
	}
	path, err := tempDataPresetsPath(r.airport)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".temp-data-*.tmp")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	r.file = updated
	r.revision++
	return nil
}

func tempDataPresetID(slot int) string { return fmt.Sprintf("PRESET:%02d", slot) }

func cloneTempRings(in [][]redsmath.Vec2) [][]redsmath.Vec2 {
	out := make([][]redsmath.Vec2, len(in))
	for i := range in {
		out[i] = append([]redsmath.Vec2(nil), in[i]...)
	}
	return out
}

func copyTempTextSnapshot(t TempText) *tempTextSnapshot {
	return &tempTextSnapshot{
		Location: t.Location, Line1: t.Line1, Line2: t.Line2,
		ShowDataBlock: t.ShowDataBlock, LeaderDirection: t.LeaderDirection,
		LeaderLength: t.LeaderLength, FontSize: t.FontSize, Brightness: t.Brightness,
	}
}

func (td *TempData) captureTempDataPreset(id string, title string) (*tempDataPreset, bool) {
	if td == nil || id == "" || strings.HasPrefix(id, "PRESET:") {
		return nil, false
	}
	slot := &tempDataPreset{Title: title, Active: true}
	for _, a := range td.closedAreas {
		if a.ID == id {
			slot.Areas = append(slot.Areas, tempAreaSnapshot{Type: a.Type, Points: append([]redsmath.Vec2(nil), a.Points...), Holes: cloneTempRings(a.Holes)})
		}
	}
	for _, a := range td.restrictedAreas {
		if a.ID == id {
			slot.Areas = append(slot.Areas, tempAreaSnapshot{Type: a.Type, Points: append([]redsmath.Vec2(nil), a.Points...), Holes: cloneTempRings(a.Holes)})
		}
	}
	for _, t := range td.texts {
		if t.ID == id {
			slot.Text = copyTempTextSnapshot(t)
			break
		}
	}
	return slot, validTempDataPreset(slot)
}

func (td *TempData) removeTempDataByID(id string) {
	if td == nil {
		return
	}
	td.closedAreas = filterTempAreas(td.closedAreas, func(a TempArea) bool { return a.ID != id })
	td.restrictedAreas = filterTempAreas(td.restrictedAreas, func(a TempArea) bool { return a.ID != id })
	td.texts = filterTempTexts(td.texts, func(t TempText) bool { return t.ID != id })
}

func (td *TempData) refreshTempDataPresets(file tempDataPresetFile) {
	if td == nil {
		return
	}
	// Only replace preset-owned objects; never touch unsaved TEMP DATA.
	td.closedAreas = filterTempAreas(td.closedAreas, func(a TempArea) bool { return !strings.HasPrefix(a.ID, "PRESET:") })
	td.restrictedAreas = filterTempAreas(td.restrictedAreas, func(a TempArea) bool { return !strings.HasPrefix(a.ID, "PRESET:") })
	td.texts = filterTempTexts(td.texts, func(t TempText) bool { return !strings.HasPrefix(t.ID, "PRESET:") })
	for i, slot := range file.Slots {
		if slot == nil || !slot.Active {
			continue
		}
		id := tempDataPresetID(i + 1)
		if slot.Text != nil {
			t := slot.Text
			td.texts = append(td.texts, TempText{
				ID: id, Location: t.Location, Line1: t.Line1, Line2: t.Line2,
				ShowDataBlock: t.ShowDataBlock, LeaderDirection: t.LeaderDirection,
				LeaderLength: t.LeaderLength, FontSize: t.FontSize, Brightness: t.Brightness,
			})
		}
		for _, a := range slot.Areas {
			rings := append([][]redsmath.Vec2{a.Points}, a.Holes...)
			vertices, indices := renderer.TessellateRings(rings)
			if len(vertices) == 0 || len(indices) == 0 {
				continue
			}
			area := TempArea{
				ID: id, Type: a.Type, Points: append([]redsmath.Vec2(nil), a.Points...),
				Holes: cloneTempRings(a.Holes), Bounds: boundsForTempPolygon(a.Points),
				meshVertices: vertices, meshIndices: indices,
			}
			if a.Type == TempDataClosedArea {
				td.closedAreas = append(td.closedAreas, area)
			} else {
				td.restrictedAreas = append(td.restrictedAreas, area)
			}
		}
	}
}

func (p *ASDEXPane) syncTempDataPresets() {
	if p == nil || p.tempPresetRepository == nil {
		return
	}
	file, revision := p.tempPresetRepository.snapshot()
	if revision == p.tempPresetRevision {
		return
	}
	p.tempData.refreshTempDataPresets(file)
	p.tempPresetRevision = revision
}

// The persistent data is shared by all ASDE-X panes of an airport in this process.
// It is also saved under os.UserConfigDir, following STARS' local persistence.
func (p *ASDEXPane) loadTempDataPresets() error {
	repo, err := getTempDataPresetRepository(p.airport)
	if err != nil {
		return err
	}
	p.tempPresetRepository = repo
	file, revision := repo.snapshot()
	p.tempData.refreshTempDataPresets(file)
	p.tempPresetRevision = revision
	return nil
}

type tempPresetEditMode int

const (
	tempPresetEditSaveSelect tempPresetEditMode = iota + 1
	tempPresetEditSaveDetails
	tempPresetEditDeleteSelect
	tempPresetEditDeleteConfirm
)

type tempPresetEdit struct {
	Mode        tempPresetEditMode
	ObjectID    string
	Slot        string
	Title       string
	ActiveField int
}

func (p *ASDEXPane) updateTempPresetCommand() {
	e := p.tempPresetEdit
	if e == nil {
		return
	}
	lines := []string{"TEMP DATA", "STORED GLOBAL TEMP DATA"}
	switch e.Mode {
	case tempPresetEditSaveSelect:
		lines = append(lines, "SAVE", "SELECT TEMP DATA")
	case tempPresetEditSaveDetails:
		lines = append(lines, "SAVE", "SET: "+e.Slot, "TITLE: "+e.Title)
	case tempPresetEditDeleteSelect:
		lines = append(lines, "DELETE PRESET", "SET: "+e.Slot)
	case tempPresetEditDeleteConfirm:
		lines = append(lines, "DELETE PRESET", "DELETE?", "1 = NO", "2 = YES", "(1 OR 2): "+e.Title)
	}
	p.dcbMenuCommand = NewDcbMenuCommand(lines...)
}

func (p *ASDEXPane) stopTempPresetEdit() {
	p.tempPresetEdit = nil
	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.dcbMenuCommand = NewDcbMenuCommand("TEMP DATA", "STORED GLOBAL TEMP DATA")
}

func (p *ASDEXPane) startSaveTempPreset() {
	if p.tempData.VisibleObjectCount() == 0 {
		p.previewArea.SetSystemResponse("NO GLOBAL DATA")
		return
	}
	if p.tempPresetRepository == nil {
		p.previewArea.SetSystemResponse("PRESET STORE UNAVAILABLE")
		return
	}
	p.tempPresetEdit = &tempPresetEdit{Mode: tempPresetEditSaveSelect}
	p.tempDataSelectMode = TempDataSelectNone
	p.clearHighlightedTarget()
	p.previewArea.SetSystemResponse("")
	p.updateTempPresetCommand()
}

func (p *ASDEXPane) selectTempPresetObject(hit TempDataHit) {
	if p.tempPresetEdit == nil || p.tempPresetEdit.Mode != tempPresetEditSaveSelect {
		return
	}
	if hit.ID == "" || strings.HasPrefix(hit.ID, "PRESET:") {
		p.previewArea.SetSystemResponse("DATA ALREADY STORED")
		return
	}
	file, _ := p.tempPresetRepository.snapshot()
	slot := 0
	for i, existing := range file.Slots {
		if existing == nil {
			slot = i + 1
			break
		}
	}
	if slot == 0 {
		p.previewArea.SetSystemResponse("NO FREE SET")
		return
	}
	p.tempPresetEdit = &tempPresetEdit{Mode: tempPresetEditSaveDetails, ObjectID: hit.ID, Slot: fmt.Sprintf("%d", slot), ActiveField: 1}
	p.previewArea.SetSystemResponse("")
	p.updateTempPresetCommand()
}

func (p *ASDEXPane) startDeleteTempPreset() {
	if p.tempPresetRepository == nil {
		p.previewArea.SetSystemResponse("PRESET STORE UNAVAILABLE")
		return
	}
	file, _ := p.tempPresetRepository.snapshot()
	found := false
	for _, slot := range file.Slots {
		if slot != nil {
			found = true
			break
		}
	}
	if !found {
		p.previewArea.SetSystemResponse("NO STORED DATA")
		return
	}
	p.tempPresetEdit = &tempPresetEdit{Mode: tempPresetEditDeleteSelect}
	p.previewArea.SetSystemResponse("")
	p.updateTempPresetCommand()
}

func (p *ASDEXPane) tempPresetButton(slot int) {
	if p.tempPresetRepository == nil || slot < 1 || slot > tempDataPresetCount {
		return
	}
	file, _ := p.tempPresetRepository.snapshot()
	if p.tempPresetEdit != nil {
		if p.tempPresetEdit.Mode == tempPresetEditDeleteSelect {
			if file.Slots[slot-1] == nil {
				p.previewArea.SetSystemResponse("INVALID SET")
				return
			}
			p.tempPresetEdit.Mode = tempPresetEditDeleteConfirm
			p.tempPresetEdit.Slot = fmt.Sprint(slot)
			p.tempPresetEdit.Title = ""
			p.updateTempPresetCommand()
		}
		return
	}
	if file.Slots[slot-1] == nil {
		p.previewArea.SetSystemResponse("NO STORED DATA")
		return
	}
	err := p.tempPresetRepository.change(slot, func(file *tempDataPresetFile) error {
		saved := *file.Slots[slot-1]
		saved.Active = !saved.Active
		file.Slots[slot-1] = &saved
		return nil
	})
	if err != nil {
		p.previewArea.SetSystemResponse("SAVE FAILED")
		if p.logger != nil {
			p.logger.Warn("Unable to toggle TEMP DATA preset", "error", err)
		}
		return
	}
	p.syncTempDataPresets()
	p.previewArea.SetSystemResponse("")
}

func (p *ASDEXPane) saveTempPreset() {
	e := p.tempPresetEdit
	if e == nil {
		return
	}
	var slot int
	if parsed, err := strconv.Atoi(e.Slot); err == nil {
		slot = parsed
	}
	if slot < 1 || slot > tempDataPresetCount || strings.TrimSpace(e.Title) == "" || len(e.Title) > tempDataPresetTitleLength {
		p.previewArea.SetSystemResponse("INVALID SET")
		return
	}
	snapshot, ok := p.tempData.captureTempDataPreset(e.ObjectID, e.Title)
	if !ok {
		p.previewArea.SetSystemResponse("NO SLEW")
		return
	}
	err := p.tempPresetRepository.change(slot, func(file *tempDataPresetFile) error {
		if file.Slots[slot-1] != nil {
			return errors.New("set already assigned")
		}
		file.Slots[slot-1] = snapshot
		return nil
	})
	if err != nil {
		p.previewArea.SetSystemResponse("INVALID SET OR SAVE FAILED")
		return
	}
	p.tempData.removeTempDataByID(e.ObjectID)
	p.syncTempDataPresets()
	p.stopTempPresetEdit()
	p.previewArea.SetSystemResponse(fmt.Sprintf("SET %02d SAVED", slot))
}

func (p *ASDEXPane) deleteTempPreset() {
	e := p.tempPresetEdit
	if e == nil {
		return
	}
	var slot int
	if parsed, err := strconv.Atoi(e.Slot); err == nil {
		slot = parsed
	}
	if slot < 1 || slot > tempDataPresetCount {
		p.previewArea.SetSystemResponse("INVALID SET")
		return
	}
	err := p.tempPresetRepository.change(slot, func(file *tempDataPresetFile) error {
		if file.Slots[slot-1] == nil {
			return errors.New("empty set")
		}
		file.Slots[slot-1] = nil
		return nil
	})
	if err != nil {
		p.previewArea.SetSystemResponse("INVALID SET OR DELETE FAILED")
		return
	}
	p.syncTempDataPresets()
	p.stopTempPresetEdit()
	p.previewArea.SetSystemResponse(fmt.Sprintf("%02d DELETED", slot))
}

func (p *ASDEXPane) consumeTempPresetSaveSelection(ctx *panes.Context, transforms radar.ScopeTransformations) bool {
	if p.tempPresetEdit == nil || p.tempPresetEdit.Mode != tempPresetEditSaveSelect || ctx == nil || ctx.Mouse == nil {
		return false
	}
	if !ctx.Mouse.WasReleased(platform.MouseButtonLeft) {
		return false
	}
	mouse := ctx.Mouse
	world := transforms.WorldFromWindowP(mouse.Pos)
	if _, rect, view, ok := p.scopeWindowAtPoint(mouse.Pos, ctx.PaneSize()); ok {
		transform := scopeTransformForWindow(rect, mainReferenceExtent(ctx.PaneSize()), view, rangeVisibleScaleForContext(ctx))
		world = transform.WorldFromWindowP(mouse.Pos.Sub(rect.Min))
	}
	p.selectTempPresetObject(p.tempData.HitTest(world))
	return true
}

func (p *ASDEXPane) handleTempPresetKeyboard(ctx *panes.Context) bool {
	if p.tempPresetEdit == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}
	e := p.tempPresetEdit
	keyboard := ctx.Keyboard
	if keyboard.WasPressed(platform.KeyEscape) {
		p.stopTempPresetEdit()
		return true
	}
	if e.Mode == tempPresetEditSaveSelect {
		return true
	}
	if e.Mode == tempPresetEditSaveDetails {
		if keyboard.WasPressed(platform.KeyUp) {
			e.ActiveField = 0
			p.updateTempPresetCommand()
			return true
		}
		if keyboard.WasPressed(platform.KeyDown) {
			e.ActiveField = 1
			p.updateTempPresetCommand()
			return true
		}
	}
	if keyboard.WasPressed(platform.KeyBackspace) || keyboard.WasPressed(platform.KeyDelete) {
		if e.Mode == tempPresetEditSaveDetails && e.ActiveField == 1 {
			if e.Title != "" {
				e.Title = e.Title[:len(e.Title)-1]
			}
		} else if e.Mode == tempPresetEditDeleteConfirm {
			e.Title = ""
		} else if e.Slot != "" {
			e.Slot = e.Slot[:len(e.Slot)-1]
		}
		p.updateTempPresetCommand()
		return true
	}
	if keyboard.WasPressed(platform.KeyEnter) || keyboard.WasPressed(platform.KeyKeypadEnter) {
		switch e.Mode {
		case tempPresetEditSaveDetails:
			if e.ActiveField == 0 {
				e.ActiveField = 1
				p.updateTempPresetCommand()
			} else {
				p.saveTempPreset()
			}
		case tempPresetEditDeleteSelect:
			var slot int
			if parsed, err := strconv.Atoi(e.Slot); err == nil {
				slot = parsed
			}
			if slot < 1 || slot > tempDataPresetCount {
				p.previewArea.SetSystemResponse("INVALID SET")
			} else {
				file, _ := p.tempPresetRepository.snapshot()
				if file.Slots[slot-1] == nil {
					p.previewArea.SetSystemResponse("INVALID SET")
				} else {
					e.Mode = tempPresetEditDeleteConfirm
					e.Title = ""
					p.updateTempPresetCommand()
				}
			}
		case tempPresetEditDeleteConfirm:
			switch e.Title {
			case "1":
				p.stopTempPresetEdit()
			case "2":
				p.deleteTempPreset()
			default:
				p.previewArea.SetSystemResponse("INVALID ENTRY")
			}
		}
		return true
	}
	for _, r := range keyboard.Text {
		r = unicode.ToUpper(r)
		switch e.Mode {
		case tempPresetEditSaveDetails:
			if e.ActiveField == 0 {
				if r >= '0' && r <= '9' && len(e.Slot) < 2 {
					e.Slot += string(r)
				}
			} else if ((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == ' ' || r == '-') && len(e.Title) < tempDataPresetTitleLength {
				e.Title += string(r)
			}
		case tempPresetEditDeleteSelect:
			if r >= '0' && r <= '9' && len(e.Slot) < 2 {
				e.Slot += string(r)
			}
		case tempPresetEditDeleteConfirm:
			if r == '1' || r == '2' {
				e.Title = string(r)
			}
		}
	}
	p.updateTempPresetCommand()
	return true
}

// Populate titles and active flags just like CRC's DcbPrefButton.
func (p *ASDEXPane) tempDataPresetButtonStates() []DcbPrefSetState {
	if p == nil || p.tempPresetRepository == nil {
		return nil
	}
	file, _ := p.tempPresetRepository.snapshot()
	out := make([]DcbPrefSetState, 0)
	for i, slot := range file.Slots {
		if slot != nil {
			out = append(out, DcbPrefSetState{Number: i + 1, Name: slot.Title, Active: slot.Active})
		}
	}
	return out
}

// Cursor positions are 1-based; keep the entry cursor in the preview/command
// area like the existing CRC-style TEMP DATA text editor.
func (e *tempPresetEdit) cursor() (int, int, bool) {
	if e == nil {
		return 0, 0, false
	}
	switch e.Mode {
	case tempPresetEditSaveDetails:
		if e.ActiveField == 0 {
			return 4, len("SET: ") + len(e.Slot), true
		}
		return 5, len("TITLE: ") + len(e.Title), true
	case tempPresetEditDeleteSelect:
		return 4, len("SET: ") + len(e.Slot), true
	case tempPresetEditDeleteConfirm:
		return 7, len("(1 OR 2): ") + len(e.Title), true
	}
	return 0, 0, false
}

// ASDE-X PREF sets mirror CRC's 32 slots per operating initials. Unlike CRC's
// network-facing session, REDS saves window/display state only on this client.
// TEMP DATA is intentionally independent and is never included in a PREF set.
const asdexPrefFileVersion = 1

type asdexPrefRecord struct {
	Title     string        `json:"title"`
	Snapshot  UndoSnapshot  `json:"snapshot"`
	Secondary []ScopeWindow `json:"secondary,omitempty"`
}

type asdexPrefFile struct {
	Version  int                        `json:"version"`
	Airport  string                     `json:"airport"`
	Initials string                     `json:"initials"`
	PINs     map[string]string          `json:"pins,omitempty"`
	Sets     map[string]asdexPrefRecord `json:"sets,omitempty"`
}

type asdexPrefRepository struct {
	mu      sync.Mutex
	airport string
	file    asdexPrefFile
}

var asdexPrefRepositories = struct {
	sync.Mutex
	byAirport map[string]*asdexPrefRepository
}{byAirport: make(map[string]*asdexPrefRepository)}

func asdexPrefPath(airport string) (string, error) {
	if _, err := tempDataPresetsPath(airport); err != nil {
		return "", err
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "REDS", "Prefs", "ASDEX", airport+".json"), nil
}

// CRC accepts two capital letters or three digits for operating initials.
func validAsdexInitials(s string) bool {
	if len(s) == 2 {
		for _, r := range s {
			if r < 'A' || r > 'Z' {
				return false
			}
		}
		return true
	}
	if len(s) == 3 {
		for _, r := range s {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return false
}

func validAsdexPIN(s string) bool {
	if len(s) != 4 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseAsdexPrefID(input, currentOI string) (string, bool) {
	input = strings.ToUpper(strings.TrimSpace(input))
	// Prefer a fully-qualified ID; otherwise use the selected operator initials.
	for _, n := range []int{3, 2} {
		if len(input) > n && validAsdexInitials(input[:n]) {
			slot, err := strconv.Atoi(input[n:])
			if err == nil && slot >= 1 && slot <= 32 && len(input[n:]) <= 2 {
				return fmt.Sprintf("%s%02d", input[:n], slot), true
			}
		}
	}
	slot, err := strconv.Atoi(input)
	if err != nil || len(input) > 2 || slot < 1 || slot > 32 || !validAsdexInitials(currentOI) {
		return "", false
	}
	return fmt.Sprintf("%s%02d", currentOI, slot), true
}

func getAsdexPrefRepository(airport string) (*asdexPrefRepository, error) {
	asdexPrefRepositories.Lock()
	defer asdexPrefRepositories.Unlock()
	if r := asdexPrefRepositories.byAirport[airport]; r != nil {
		return r, nil
	}
	path, err := asdexPrefPath(airport)
	if err != nil {
		return nil, err
	}
	r := &asdexPrefRepository{airport: airport, file: asdexPrefFile{
		Version: asdexPrefFileVersion, Airport: airport, Initials: "000",
		PINs: map[string]string{}, Sets: map[string]asdexPrefRecord{},
	}}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &r.file); err != nil {
			return nil, fmt.Errorf("decode ASDE-X PREF file: %w", err)
		}
		if r.file.Version != asdexPrefFileVersion || r.file.Airport != airport || !validAsdexInitials(r.file.Initials) {
			return nil, fmt.Errorf("invalid ASDE-X PREF file for %s", airport)
		}
		for id, set := range r.file.Sets {
			if len(set.Title) > 7 || len(set.Secondary) > maxSecondaryWindows {
				return nil, fmt.Errorf("invalid PREF %s", id)
			}
			if _, ok := parseAsdexPrefID(id, "000"); !ok {
				return nil, fmt.Errorf("invalid PREF id %s", id)
			}
		}
		for oi, pin := range r.file.PINs {
			if !validAsdexInitials(oi) || !validAsdexPIN(pin) {
				return nil, fmt.Errorf("invalid ASDE-X PIN entry")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if r.file.PINs == nil {
		r.file.PINs = map[string]string{}
	}
	if r.file.Sets == nil {
		r.file.Sets = map[string]asdexPrefRecord{}
	}
	asdexPrefRepositories.byAirport[airport] = r
	return r, nil
}

func (r *asdexPrefRepository) snapshot() asdexPrefFile {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.file
}

// Persist with atomic replacement so a crash cannot truncate the previous set.
func (r *asdexPrefRepository) change(fn func(*asdexPrefFile) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	updated := r.file
	updated.Sets = make(map[string]asdexPrefRecord, len(r.file.Sets))
	for k, v := range r.file.Sets {
		updated.Sets[k] = v
	}
	updated.PINs = make(map[string]string, len(r.file.PINs))
	for k, v := range r.file.PINs {
		updated.PINs[k] = v
	}
	if err := fn(&updated); err != nil {
		return err
	}
	b, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return err
	}
	path, err := asdexPrefPath(r.airport)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".prefs-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	r.file = updated
	return nil
}

func (p *ASDEXPane) loadAsdexPrefs() error {
	repo, err := getAsdexPrefRepository(p.airport)
	if err != nil {
		p.prefInitials = "000"
		return err
	}
	p.prefStore = repo
	p.prefInitials = repo.snapshot().Initials
	return nil
}

func (p *ASDEXPane) asdexPrefButtonStates() []DcbPrefSetState {
	if p.prefStore == nil {
		return nil
	}
	f := p.prefStore.snapshot()
	out := make([]DcbPrefSetState, 0, 32)
	for i := 1; i <= 32; i++ {
		id := fmt.Sprintf("%s%02d", p.prefInitials, i)
		if r, ok := f.Sets[id]; ok {
			out = append(out, DcbPrefSetState{Number: i, Name: r.Title, Active: id == p.prefActiveID})
		}
	}
	return out
}

func (p *ASDEXPane) asdexCurrentPrefTitle() string {
	if p.prefStore == nil || p.prefActiveID == "" {
		return ""
	}
	return p.prefStore.snapshot().Sets[p.prefActiveID].Title
}

func (p *ASDEXPane) captureAsdexPref() asdexPrefRecord {
	r := asdexPrefRecord{Snapshot: p.captureUndoSnapshot()}
	// CRC PREF sets capture configured display behavior, not overrides tied to
	// individual live targets. Avoid persisting stale track identifiers.
	for id, state := range r.Snapshot.WindowStates {
		state.Display.TargetShowDBOverrides = nil
		state.Display.TargetDBOffAreaOverrides = nil
		state.Display.LeaderDirectionOverrides = nil
		state.Display.LeaderLengthOverrides = nil
		state.Display.TraitLeaderDirectionOverrides = nil
		state.Display.TraitLeaderLengthOverrides = nil
		state.Display.TargetTraitAreaByTarget = nil
		r.Snapshot.WindowStates[id] = state
	}
	for _, w := range p.windows.secondary {
		if !w.Hidden {
			r.Secondary = append(r.Secondary, w)
		}
	}
	return r
}

func (p *ASDEXPane) applyAsdexPref(record asdexPrefRecord) error {
	if len(record.Secondary) > maxSecondaryWindows {
		return errors.New("too many windows")
	}
	newWindows := NewScopeWindowManager()
	newDisplays := map[ScopeWindowID]*WindowDisplayState{mainScopeWindowID: NewWindowDisplayState()}
	for _, w := range record.Secondary {
		if w.ID <= mainScopeWindowID || w.Rect.Empty() {
			return errors.New("invalid secondary window")
		}
		if _, duplicate := newDisplays[w.ID]; duplicate {
			return errors.New("duplicate secondary window")
		}
		state, ok := record.Snapshot.WindowStates[w.ID]
		if !ok {
			return errors.New("missing secondary window settings")
		}
		copy := cloneWindowDisplayState(&state.Display)
		newDisplays[w.ID] = &copy
		newWindows.secondary = append(newWindows.secondary, w)
		if w.ID >= newWindows.nextID {
			newWindows.nextID = w.ID + 1
		}
	}
	if _, ok := record.Snapshot.WindowStates[mainScopeWindowID]; !ok {
		return errors.New("missing main window settings")
	}
	// Restoring a PREF can change the window topology. The ordinary UNDO
	// snapshots do not carry topology, so discard them rather than offering
	// a misleading partial undo after loading a preference set.
	p.undoStack = nil
	p.windows = newWindows
	p.displayStateByWindow = newDisplays
	p.restoreUndoSnapshot(record.Snapshot)
	return nil
}

type asdexPrefEditMode int

const (
	asdexPrefEditInitials asdexPrefEditMode = iota + 1
	asdexPrefEditSave
	asdexPrefEditDelete
	asdexPrefEditPin
)

type asdexPrefEdit struct {
	Mode   asdexPrefEditMode
	Fields []string
	Field  int
}

func (e *asdexPrefEdit) headings() []string {
	switch e.Mode {
	case asdexPrefEditInitials:
		return []string{"OP INITS"}
	case asdexPrefEditSave:
		return []string{"SET", "TITLE", "PIN"}
	case asdexPrefEditDelete:
		return []string{"DELETE", "PIN"}
	case asdexPrefEditPin:
		return []string{"OP INITS", "OLD PIN", "NEW PIN", "CONFIRM NEW PIN"}
	}
	return nil
}

func (e *asdexPrefEdit) subheading() string {
	switch e.Mode {
	case asdexPrefEditInitials:
		return "OP INITS"
	case asdexPrefEditSave:
		return "SAVE AS"
	case asdexPrefEditDelete:
		return "DELETE"
	case asdexPrefEditPin:
		return "CHG PIN"
	}
	return ""
}

func (e *asdexPrefEdit) isSecret(i int) bool {
	return (e.Mode == asdexPrefEditSave && i == 2) ||
		(e.Mode == asdexPrefEditDelete && i == 1) ||
		(e.Mode == asdexPrefEditPin && i > 0)
}

func (e *asdexPrefEdit) cursor() (int, int, bool) {
	if e == nil || e.Field >= len(e.Fields) {
		return 0, 0, false
	}
	name := e.headings()[e.Field]
	// Our command area lines are 1-based; the cursor follows the active entry.
	length := len(e.Fields[e.Field])
	if e.isSecret(e.Field) {
		length = len(e.Fields[e.Field])
	}
	return 3 + e.Field, len(name) + 2 + length, true
}

func (p *ASDEXPane) updateAsdexPrefCommand() {
	if p.prefEdit == nil {
		p.dcbMenuCommand = NewDcbMenuCommand("PREFS")
		return
	}
	e := p.prefEdit
	lines := []string{"PREFS", e.subheading()}
	for i, name := range e.headings() {
		value := e.Fields[i]
		if e.isSecret(i) {
			value = strings.Repeat("*", len(value))
		}
		lines = append(lines, name+": "+value)
	}
	p.dcbMenuCommand = NewDcbMenuCommand(lines...)
}

func (p *ASDEXPane) startAsdexPrefEdit(mode asdexPrefEditMode) {
	if p.prefStore == nil {
		p.previewArea.SetSystemResponse("PREF STORE UNAVAILABLE")
		return
	}
	e := &asdexPrefEdit{Mode: mode}
	switch mode {
	case asdexPrefEditInitials:
		e.Fields = []string{p.prefInitials}
	case asdexPrefEditSave:
		f := p.prefStore.snapshot()
		for i := 1; i <= 32; i++ {
			id := fmt.Sprintf("%s%02d", p.prefInitials, i)
			if _, ok := f.Sets[id]; !ok {
				e.Fields = []string{id, "", ""}
				break
			}
		}
		if e.Fields == nil {
			e.Fields = []string{"", "", ""}
		}
	case asdexPrefEditDelete:
		e.Fields = []string{"", ""}
	case asdexPrefEditPin:
		e.Fields = []string{p.prefInitials, "", "", ""}
	}
	p.prefEdit = e
	p.previewArea.SetSystemResponse("")
	p.updateAsdexPrefCommand()
}

func (p *ASDEXPane) activateAsdexPrefSlot(slot int) {
	if slot < 1 || slot > 32 || p.prefStore == nil {
		return
	}
	id := fmt.Sprintf("%s%02d", p.prefInitials, slot)
	if p.prefEdit != nil {
		if p.prefEdit.Mode == asdexPrefEditDelete {
			p.prefEdit.Fields[0] = id
			p.prefEdit.Field = 1
			p.updateAsdexPrefCommand()
		}
		return
	}
	record, ok := p.prefStore.snapshot().Sets[id]
	if !ok {
		p.previewArea.SetSystemResponse("NO STORED DATA")
		return
	}
	if err := p.applyAsdexPref(record); err != nil {
		p.previewArea.SetSystemResponse("INVALID PREF")
		if p.logger != nil {
			p.logger.Warn("Unable to apply ASDE-X PREF", "error", err)
		}
		return
	}
	p.prefActiveID = id
	p.previewArea.SetSystemResponse("")
	p.dcb.SetMenu(DcbMenuPrefs)
	p.dcbMenuCommand = NewDcbMenuCommand("PREFS")
}

func (p *ASDEXPane) finishAsdexPrefEdit() {
	p.prefEdit = nil
	p.dcbMenuCommand = NewDcbMenuCommand("PREFS")
}

func (p *ASDEXPane) submitAsdexPrefEdit() {
	e := p.prefEdit
	if e == nil || p.prefStore == nil {
		return
	}
	fields := e.Fields
	response := ""
	var err error
	switch e.Mode {
	case asdexPrefEditInitials:
		oi := strings.ToUpper(strings.TrimSpace(fields[0]))
		if !validAsdexInitials(oi) {
			response = "INVALID ENTRY"
			break
		}
		err = p.prefStore.change(func(f *asdexPrefFile) error { f.Initials = oi; return nil })
		if err == nil {
			p.prefInitials = oi
			p.prefActiveID = ""
		}
	case asdexPrefEditSave:
		id, ok := parseAsdexPrefID(fields[0], p.prefInitials)
		if !ok || len(fields[1]) > 7 {
			response = "INVALID SET"
			break
		}
		oi := id[:len(id)-2]
		if !validAsdexPIN(fields[2]) {
			response = "INVALID PIN"
			break
		}
		record := p.captureAsdexPref()
		record.Title = strings.ToUpper(fields[1])
		err = p.prefStore.change(func(f *asdexPrefFile) error {
			if pin := f.PINs[oi]; pin != "" && pin != fields[2] {
				return errors.New("INVALID PIN")
			}
			if pin := f.PINs[oi]; pin == "" && fields[2] != "1234" {
				return errors.New("INVALID PIN")
			}
			f.Sets[id] = record
			return nil
		})
		if err == nil {
			p.prefActiveID = id
			response = id + " SAVED"
		}
	case asdexPrefEditDelete:
		id, ok := parseAsdexPrefID(fields[0], p.prefInitials)
		if !ok {
			response = "INVALID SET"
			break
		}
		oi := id[:len(id)-2]
		if !validAsdexPIN(fields[1]) {
			response = "INVALID PIN"
			break
		}
		err = p.prefStore.change(func(f *asdexPrefFile) error {
			if _, found := f.Sets[id]; !found {
				return errors.New("INVALID SET")
			}
			if pin := f.PINs[oi]; pin != "" && pin != fields[1] {
				return errors.New("INVALID PIN")
			}
			if pin := f.PINs[oi]; pin == "" && fields[1] != "1234" {
				return errors.New("INVALID PIN")
			}
			delete(f.Sets, id)
			return nil
		})
		if err == nil {
			if p.prefActiveID == id {
				p.prefActiveID = ""
			}
			response = id + " DELETED"
		}
	case asdexPrefEditPin:
		oi := strings.ToUpper(strings.TrimSpace(fields[0]))
		if !validAsdexInitials(oi) || !validAsdexPIN(fields[1]) || !validAsdexPIN(fields[2]) || fields[2] != fields[3] {
			response = "INVALID ENTRY"
			break
		}
		err = p.prefStore.change(func(f *asdexPrefFile) error {
			old := f.PINs[oi]
			if old == "" {
				old = "1234"
			}
			if fields[1] != old {
				return errors.New("INVALID PIN")
			}
			f.PINs[oi] = fields[2]
			return nil
		})
		if err == nil {
			response = oi + " PIN CHANGED"
		}
	}
	if err != nil {
		response = err.Error()
		if response != "INVALID PIN" && response != "INVALID SET" {
			response = "PREF SAVE FAILED"
		}
		if p.logger != nil {
			p.logger.Warn("ASDE-X PREF operation failed", "error", err)
		}
	}
	if response == "" {
		response = ""
	}
	if err == nil && (response == "" || strings.HasSuffix(response, " SAVED") || strings.HasSuffix(response, " DELETED") || strings.HasSuffix(response, " PIN CHANGED")) {
		p.finishAsdexPrefEdit()
	}
	p.previewArea.SetSystemResponse(response)
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) handleAsdexPrefKeyboard(ctx *panes.Context) bool {
	if p == nil || p.prefEdit == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}
	e, keyboard := p.prefEdit, ctx.Keyboard
	if keyboard.WasPressed(platform.KeyEscape) {
		p.finishAsdexPrefEdit()
		p.previewArea.SetSystemResponse("")
		return true
	}
	if keyboard.WasPressed(platform.KeyUp) {
		if e.Field > 0 {
			e.Field--
		}
		p.updateAsdexPrefCommand()
		return true
	}
	if keyboard.WasPressed(platform.KeyDown) {
		if e.Field < len(e.Fields)-1 {
			e.Field++
		}
		p.updateAsdexPrefCommand()
		return true
	}
	if keyboard.WasPressed(platform.KeyEnter) || keyboard.WasPressed(platform.KeyKeypadEnter) {
		if e.Field < len(e.Fields)-1 {
			e.Field++
		} else {
			p.submitAsdexPrefEdit()
		}
		if p.prefEdit != nil {
			p.updateAsdexPrefCommand()
		}
		return true
	}
	if keyboard.WasPressed(platform.KeyBackspace) || keyboard.WasPressed(platform.KeyDelete) {
		if len(e.Fields[e.Field]) > 0 {
			e.Fields[e.Field] = e.Fields[e.Field][:len(e.Fields[e.Field])-1]
		}
		p.updateAsdexPrefCommand()
		return true
	}
	maxLen := 7
	switch e.Mode {
	case asdexPrefEditInitials:
		maxLen = 3
	case asdexPrefEditSave:
		if e.Field == 0 {
			maxLen = 5
		} else if e.Field == 2 {
			maxLen = 4
		}
	case asdexPrefEditDelete:
		if e.Field == 0 {
			maxLen = 5
		} else {
			maxLen = 4
		}
	case asdexPrefEditPin:
		if e.Field == 0 {
			maxLen = 3
		} else {
			maxLen = 4
		}
	}
	for _, r := range keyboard.Text {
		r = unicode.ToUpper(r)
		if len(e.Fields[e.Field]) >= maxLen {
			continue
		}
		if e.isSecret(e.Field) && (r < '0' || r > '9') {
			continue
		}
		if !e.isSecret(e.Field) && !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (r == ' ' && e.Mode == asdexPrefEditSave && e.Field == 1)) {
			continue
		}
		e.Fields[e.Field] += string(r)
	}
	p.previewArea.SetSystemResponse("")
	p.updateAsdexPrefCommand()
	return true
}
