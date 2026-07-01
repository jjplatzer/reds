package asdex

import (
	"encoding/json"
	"fmt"
	stdmath "math"
	"path/filepath"
	"strings"
	"unicode"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/util"
)

type MultiFunctionCommand struct {
	value  string
	cursor int
}

const multiFunctionMaxLength = 2
const maxScratchpadCommandLength = 7

func NewMultiFunctionCommand() *MultiFunctionCommand {
	return &MultiFunctionCommand{}
}

func (command *MultiFunctionCommand) DisplayLines() []string {
	if command == nil {
		return nil
	}
	return []string{"MULT " + command.value}
}

func (command *MultiFunctionCommand) CursorLine() int {
	return 1
}

func (command *MultiFunctionCommand) CursorColumn() int {
	if command == nil {
		return 0
	}
	return 5 + command.cursor
}

func (command *MultiFunctionCommand) Insert(r rune) {
	if command == nil {
		return
	}

	r = unicode.ToUpper(r)
	if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
		return
	}

	value := []rune(command.value)
	if len(value) >= multiFunctionMaxLength {
		return
	}

	command.cursor = clampInt(command.cursor, 0, len(value))
	value = append(value[:command.cursor], append([]rune{r}, value[command.cursor:]...)...)
	command.value = string(value)
	command.cursor++
}

func (command *MultiFunctionCommand) Clear() {
	if command == nil {
		return
	}
	command.value = ""
	command.cursor = 0
}

func (command *MultiFunctionCommand) Value() string {
	if command == nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(command.value))
}

type ScratchpadEntryCommand struct {
	ScratchpadNumber int
	TargetID         string
	value            string
	cursor           int
}

func NewScratchpadEntryCommand(targetID string, scratchpadNumber int) *ScratchpadEntryCommand {
	if scratchpadNumber != 1 {
		scratchpadNumber = 2
	}
	return &ScratchpadEntryCommand{
		ScratchpadNumber: scratchpadNumber,
		TargetID:         targetID,
	}
}

func (command *ScratchpadEntryCommand) DisplayLines() []string {
	if command == nil {
		return nil
	}
	if command.ScratchpadNumber == 1 {
		return []string{"MULT Y", command.value}
	}
	return []string{"MULT H", command.value}
}

func (command *ScratchpadEntryCommand) CursorLine() int {
	return 2
}

func (command *ScratchpadEntryCommand) CursorColumn() int {
	if command == nil {
		return 0
	}
	return command.cursor
}

func (command *ScratchpadEntryCommand) Insert(r rune) {
	if command == nil {
		return
	}

	r = unicode.ToUpper(r)
	if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
		return
	}

	value := []rune(command.value)
	if len(value) >= maxScratchpadCommandLength {
		return
	}

	command.cursor = clampInt(command.cursor, 0, len(value))
	value = append(value[:command.cursor], append([]rune{r}, value[command.cursor:]...)...)
	command.value = string(value)
	command.cursor++
}

func (command *ScratchpadEntryCommand) Backspace() {
	if command == nil || command.cursor <= 0 {
		return
	}

	value := []rune(command.value)
	command.cursor = clampInt(command.cursor, 0, len(value))
	value = append(value[:command.cursor-1], value[command.cursor:]...)
	command.value = string(value)
	command.cursor--
}

func (command *ScratchpadEntryCommand) DeleteForward() {
	if command == nil {
		return
	}

	value := []rune(command.value)
	command.cursor = clampInt(command.cursor, 0, len(value))
	if command.cursor >= len(value) {
		return
	}

	value = append(value[:command.cursor], value[command.cursor+1:]...)
	command.value = string(value)
}

func (command *ScratchpadEntryCommand) MoveLeft() {
	if command == nil {
		return
	}
	command.cursor = max(0, command.cursor-1)
}

func (command *ScratchpadEntryCommand) MoveRight() {
	if command == nil {
		return
	}
	command.cursor = min(command.cursor+1, len([]rune(command.value)))
}

func (command *ScratchpadEntryCommand) Value() string {
	if command == nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(command.value))
}

func validScratchpadCommandValue(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len([]rune(value)) > maxScratchpadCommandLength {
		return false
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

type PreviewRepositionCommand struct{}

func NewMultiPreviewRepositionCommand() *PreviewRepositionCommand {
	return &PreviewRepositionCommand{}
}

func (command *PreviewRepositionCommand) DisplayLines() []string {
	if command == nil {
		return nil
	}
	return []string{"MULT P"}
}

type CoastListRepositionCommand struct{}

func NewMultiCoastListRepositionCommand() *CoastListRepositionCommand {
	return &CoastListRepositionCommand{}
}

func (command *CoastListRepositionCommand) DisplayLines() []string {
	if command == nil {
		return nil
	}
	return []string{"MULT C"}
}

type MapRepositionCommand struct {
	WindowID       ScopeWindowID
	originalCenter redsmath.Vec2
	initialized    bool
	UndoCaptured   bool
	UndoBefore     UndoSnapshot
}

func NewMapRepositionCommand(windowID ScopeWindowID, center redsmath.Vec2) *MapRepositionCommand {
	return &MapRepositionCommand{
		WindowID:       windowID,
		originalCenter: center,
		initialized:    true,
	}
}

func (command *MapRepositionCommand) DisplayLines() []string {
	if command == nil {
		return nil
	}
	return []string{"MAP RPOS"}
}

type MapRotateCommand struct {
	WindowID         ScopeWindowID
	value            string
	cursor           int
	originalRotation float32
	displayLines     []string
	returnMenu       DcbMenu
	UndoCaptured     bool
	UndoBefore       UndoSnapshot
}

type RunwayConfigCommand struct {
	value  string
	cursor int
}

func NewRunwayConfigCommand() *RunwayConfigCommand {
	return &RunwayConfigCommand{}
}

func (command *RunwayConfigCommand) DisplayLines() []string {
	if command == nil {
		return nil
	}
	return []string{
		"SAFETY LOGIC",
		"RWY CONFIG",
		command.value,
	}
}

func (command *RunwayConfigCommand) CursorLine() int {
	return 3
}

func (command *RunwayConfigCommand) CursorColumn() int {
	if command == nil {
		return 0
	}
	return command.cursor
}

func (command *RunwayConfigCommand) Insert(r rune) {
	if command == nil {
		return
	}

	r = unicode.ToUpper(r)
	if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
		return
	}

	value := []rune(command.value)
	command.cursor = clampInt(command.cursor, 0, len(value))
	value = append(value[:command.cursor], append([]rune{r}, value[command.cursor:]...)...)
	command.value = string(value)
	command.cursor++
}

func (command *RunwayConfigCommand) Backspace() bool {
	if command == nil {
		return true
	}

	value := []rune(command.value)
	if len(value) == 0 {
		return true
	}

	command.cursor = clampInt(command.cursor, 0, len(value))
	if command.cursor == 0 {
		return false
	}

	command.cursor--
	value = append(value[:command.cursor], value[command.cursor+1:]...)
	command.value = string(value)
	return false
}

func (command *RunwayConfigCommand) DeleteForward() {
	if command == nil {
		return
	}

	value := []rune(command.value)
	command.cursor = clampInt(command.cursor, 0, len(value))
	if command.cursor >= len(value) {
		return
	}

	value = append(value[:command.cursor], value[command.cursor+1:]...)
	command.value = string(value)
}

func (command *RunwayConfigCommand) MoveLeft() {
	if command != nil && command.cursor > 0 {
		command.cursor--
	}
}

func (command *RunwayConfigCommand) MoveRight() {
	if command == nil {
		return
	}
	if command.cursor < len([]rune(command.value)) {
		command.cursor++
	}
}

func (command *RunwayConfigCommand) Value() string {
	if command == nil {
		return ""
	}
	return strings.TrimSpace(command.value)
}

func NewMapRotateCommand(
	windowID ScopeWindowID,
	rotation float32,
	displayLines []string,
	returnMenu DcbMenu,
) *MapRotateCommand {
	return &MapRotateCommand{
		WindowID:         windowID,
		originalRotation: rotation,
		displayLines:     append([]string(nil), displayLines...),
		returnMenu:       returnMenu,
	}
}

func NewMainMapRotateCommand(windowID ScopeWindowID, rotation float32) *MapRotateCommand {
	return NewMapRotateCommand(
		windowID,
		rotation,
		[]string{"ROTATE"},
		DcbMenuMain,
	)
}

func NewToolsMapRotateCommand(windowID ScopeWindowID, rotation float32) *MapRotateCommand {
	return NewMapRotateCommand(
		windowID,
		rotation,
		[]string{"TOOLS", "ROTATE"},
		DcbMenuTools,
	)
}

func (command *MapRotateCommand) DisplayLines() []string {
	if command == nil {
		return nil
	}
	lines := append([]string(nil), command.displayLines...)
	lines = append(lines, command.value)
	return lines
}

func (command *MapRotateCommand) CursorLine() int {
	if command == nil {
		return 0
	}
	return len(command.displayLines) + 1
}

func (command *MapRotateCommand) CursorColumn() int {
	if command == nil {
		return 0
	}
	return command.cursor
}

func (command *MapRotateCommand) Insert(r rune) {
	if command == nil || !unicode.IsDigit(r) {
		return
	}

	value := []rune(command.value)
	if len(value) >= 3 {
		return
	}
	command.cursor = clampInt(command.cursor, 0, len(value))

	value = append(value[:command.cursor], append([]rune{r}, value[command.cursor:]...)...)
	command.value = string(value)
	command.cursor++
}

func (command *MapRotateCommand) Backspace() {
	if command == nil || command.cursor <= 0 {
		return
	}

	value := []rune(command.value)
	if command.cursor > len(value) {
		command.cursor = len(value)
	}
	if command.cursor <= 0 {
		return
	}

	command.cursor--
	value = append(value[:command.cursor], value[command.cursor+1:]...)
	command.value = string(value)
}

func (command *MapRotateCommand) DeleteForward() {
	if command == nil {
		return
	}

	value := []rune(command.value)
	command.cursor = clampInt(command.cursor, 0, len(value))
	if command.cursor >= len(value) {
		return
	}

	value = append(value[:command.cursor], value[command.cursor+1:]...)
	command.value = string(value)
}

func (command *MapRotateCommand) MoveLeft() {
	if command == nil {
		return
	}
	if command.cursor > 0 {
		command.cursor--
	}
}

func (command *MapRotateCommand) MoveRight() {
	if command == nil {
		return
	}

	value := []rune(command.value)
	if command.cursor < len(value) {
		command.cursor++
	}
}

func (command *MapRotateCommand) Value() string {
	if command == nil {
		return ""
	}
	return strings.TrimSpace(command.value)
}

type TowerReference struct {
	ID   string
	Feet redsmath.Vec2
}

type surfaceTowerJSON struct {
	ID       string    `json:"id"`
	Position []float64 `json:"position"`
}

type towerSurfaceJSON struct {
	Towers []surfaceTowerJSON `json:"towers"`
}

func LoadTowerReference(airport string, vm *VideoMap) (TowerReference, bool, error) {
	airport = strings.ToUpper(strings.TrimSpace(airport))
	if airport == "" || vm == nil {
		return TowerReference{}, false, nil
	}

	path := filepath.ToSlash(filepath.Join("asdex", "surface", airport+".json"))
	if !util.ResourceExists(path) {
		return TowerReference{}, false, nil
	}

	var surface towerSurfaceJSON
	if err := json.Unmarshal(util.LoadResourceBytes(path), &surface); err != nil {
		return TowerReference{}, false, err
	}

	for _, tower := range surface.Towers {
		if len(tower.Position) < 2 {
			continue
		}

		return TowerReference{
			ID: tower.ID,
			Feet: vm.LonLatToFeet(
				tower.Position[0],
				tower.Position[1],
			),
		}, true, nil
	}

	return TowerReference{}, false, nil
}

type TowerReadoutCommand struct {
	Tower TowerReference
	x     int
	y     int
}

func NewTowerReadoutCommand(tower TowerReference) *TowerReadoutCommand {
	return &TowerReadoutCommand{Tower: tower}
}

func (cmd *TowerReadoutCommand) SetValues(x int, y int) {
	if cmd == nil {
		return
	}
	cmd.x = x
	cmd.y = y
}

func (cmd *TowerReadoutCommand) DisplayLines() []string {
	if cmd == nil {
		return nil
	}

	return []string{
		fmt.Sprintf("X: %d", cmd.x),
		fmt.Sprintf("Y: %d", cmd.y),
	}
}

func towerReadoutValues(
	cursorFeet redsmath.Vec2,
	towerFeet redsmath.Vec2,
	rotationDeg float32,
) (int, int) {
	delta := cursorFeet.Sub(towerFeet)

	rot := float32(stdmath.Pi) * rotationDeg / 180
	c := float32(stdmath.Cos(float64(rot)))
	s := float32(stdmath.Sin(float64(rot)))

	x := delta.X*c - delta.Y*s
	y := delta.Y*c + delta.X*s

	return int(stdmath.Round(float64(x))),
		int(stdmath.Round(float64(y)))
}

func registerSetupCommands() {
	registerCommand(
		CommandModeNone,
		"[UNDO]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdUndo(ctx)
		},
	)

	registerCommand(
		CommandModeNone,
		"[DEFAULT]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdDefault(ctx)
		},
	)

	registerCommand(
		CommandModeNone,
		"[VOL TEST]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdVolumeTest(ctx)
		},
	)

	registerCommand(
		CommandModeMultiFunction,
		"VT",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdVolumeTest(ctx)
		},
	)

	registerCommand(
		CommandModeNone,
		"[MAP THEME]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdMapTheme(ctx)
		},
	)

	registerCommand(
		CommandModeNone,
		"[DB ON/OFF]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdDataBlocksOnOff(ctx)
		},
	)

	registerCommand(
		CommandModeNone,
		"[MULT FUNC]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdMultiFunction(ctx)
		},
	)

	registerCommand(
		CommandModeNone,
		"[MAP RPOS]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdMapReposition(ctx)
		},
	)

	registerCommand(
		CommandModeNone,
		"[ROTATE]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdMapRotate(ctx)
		},
	)

	registerCommand(
		CommandModeNone,
		"[RWY CFG]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdRunwayConfig(ctx)
		},
	)

	registerCommand(
		CommandModeNone,
		"[NEW WINDOW]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdNewWindow(ctx)
		},
	)

	registerCommand(
		CommandModeNone,
		"[WINDOW REPOS][WINDOW MSLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, slew WindowMiddleSlew) CommandStatus {
			return ap.cmdWindowRepositionMiddleSlew(ctx, slew)
		},
	)

	registerCommand(
		CommandModeNone,
		"[WINDOW RESIZE][WINDOW SMSLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, slew WindowShiftMiddleSlew) CommandStatus {
			return ap.cmdWindowResizeShiftMiddleSlew(ctx, slew)
		},
	)

	registerCommand(
		CommandModeNone,
		"[WINDOW SWITCH][WINDOW SLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, slew WindowSlew) CommandStatus {
			return ap.cmdWindowSwitchWindowSlew(ctx, slew)
		},
	)

	registerCommand(
		CommandModeNone,
		"[WINDOW SWITCH][DCB MSLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, slew DcbMiddleSlew) CommandStatus {
			return ap.cmdWindowSwitchDcbMiddleSlew(ctx, slew)
		},
	)

	registerCommand(
		CommandModeNone,
		"[TWR RDOUT]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdTowerReadout(ctx)
		},
	)

	registerCommand(
		CommandModeMultiFunction,
		"B[SLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, target *Target) CommandStatus {
			return ap.cmdBeaconatorSlew(ctx, target)
		},
	)

	registerCommand(
		CommandModePreviewReposition,
		"[DISPLAY SLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, point DisplayPoint) CommandStatus {
			return ap.cmdPreviewRepositionSlew(ctx, point)
		},
	)

	registerCommand(
		CommandModeCoastListReposition,
		"[DISPLAY SLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, point DisplayPoint) CommandStatus {
			return ap.cmdCoastListRepositionSlew(ctx, point)
		},
	)

	registerCommand(
		CommandModeNone,
		"[LDR DIR][SLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, input LeaderDirectionInput, target *Target) CommandStatus {
			return ap.cmdLeaderDirectionSlew(ctx, input, target)
		},
	)

	registerCommand(
		CommandModeNone,
		"[LDR DIR]",
		func(ap *ASDEXPane, ctx *panes.Context, input LeaderDirectionInput) CommandStatus {
			return ap.cmdLeaderDirectionAll(ctx, input)
		},
	)

	registerCommand(
		CommandModeNone,
		"[LDR LNG][SLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, input LeaderLengthInput, target *Target) CommandStatus {
			return ap.cmdLeaderLengthSlew(ctx, input, target)
		},
	)

	registerCommand(
		CommandModeNone,
		"[LDR LNG]",
		func(ap *ASDEXPane, ctx *panes.Context, input LeaderLengthInput) CommandStatus {
			return ap.cmdLeaderLengthAll(ctx, input)
		},
	)
}

func (ap *ASDEXPane) cmdMapTheme(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	switch ap.mode {
	case ModeDay:
		ap.mode = ModeNight
	default:
		ap.mode = ModeDay
	}

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}

func (ap *ASDEXPane) cmdVolumeTest(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	if ap.auralAlerts != nil {
		ap.auralAlerts.PlayVolumeTest()
	}

	ap.previewArea.SetSystemResponse("")
	ap.clearHighlightedTarget()

	if ap.commandMode == CommandModeMultiFunction {
		return CommandStatus{
			Clear:     ClearAll,
			Output:    "",
			HasOutput: true,
		}
	}

	return CommandStatus{
		Clear:     ClearNone,
		Output:    "",
		HasOutput: true,
	}
}

func (ap *ASDEXPane) cmdDataBlocksOnOff(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	before := ap.pushUndoBeforeMutation()
	windowID := ap.activeWindowID()
	ap.updateActiveDataBlockSettings(func(settings *DataBlockSettings) {
		settings.ShowDataBlocks = !settings.ShowDataBlocks
	})
	ap.clearTargetShowDBOverrides(windowID)
	ap.commitUndoIfChanged(before)

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}

func (ap *ASDEXPane) cmdNewWindow(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}
	if !ap.windows.CanAddSecondary() {
		return CommandStatus{
			Clear:     ClearAll,
			Output:    "",
			HasOutput: true,
		}
	}

	ap.startNewWindowCommand(NewNewWindowCommand())

	return CommandStatus{Clear: ClearNone}
}

func (ap *ASDEXPane) cmdWindowRepositionMiddleSlew(
	ctx *panes.Context,
	slew WindowMiddleSlew,
) CommandStatus {
	if ap == nil || ctx == nil {
		return CommandStatus{Clear: ClearAll}
	}
	if slew.WindowID == mainScopeWindowID || slew.Rect.Empty() {
		return CommandStatus{
			Clear:     ClearAll,
			Output:    "",
			HasOutput: true,
		}
	}

	ap.startWindowRepositionForWindow(
		slew.WindowID,
		slew.Rect,
		NewShortcutWindowRepositionCommand(slew.WindowID, slew.Rect),
	)

	return CommandStatus{Clear: ClearNone}
}

func (ap *ASDEXPane) cmdWindowResizeShiftMiddleSlew(
	_ *panes.Context,
	slew WindowShiftMiddleSlew,
) CommandStatus {
	if ap == nil || slew.WindowID == mainScopeWindowID || slew.Rect.Empty() {
		return CommandStatus{Clear: ClearNone}
	}

	ap.startResizeWindowForWindow(
		slew.WindowID,
		NewShortcutResizeWindowCommand(slew.WindowID),
	)

	return CommandStatus{Clear: ClearNone}
}

func (ap *ASDEXPane) cmdWindowSwitchWindowSlew(
	_ *panes.Context,
	slew WindowSlew,
) CommandStatus {
	if ap == nil || slew.Rect.Empty() || ap.activeWindowID() == slew.WindowID {
		return CommandStatus{Clear: ClearNone}
	}

	ap.windows.SetActiveWindow(slew.WindowID)
	ap.previewArea.SetSystemResponse("")
	ap.clearHighlightedTarget()
	return CommandStatus{Clear: ClearNone}
}

func (ap *ASDEXPane) cmdWindowSwitchDcbMiddleSlew(
	_ *panes.Context,
	_ DcbMiddleSlew,
) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearNone}
	}

	next, ok := ap.nextWindowSwitchID()
	if !ok {
		return CommandStatus{Clear: ClearNone}
	}

	ap.windows.SetActiveWindow(next)
	ap.previewArea.SetSystemResponse("")
	ap.clearHighlightedTarget()
	return CommandStatus{Clear: ClearNone}
}

func (ap *ASDEXPane) cmdRunwayConfig(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	ap.clearDcbModalConflicts()
	ap.commandMode = CommandModeSetRunwayConfig
	ap.runwayConfigCommand = NewRunwayConfigCommand()
	ap.previewArea.SetSystemResponse("")
	ap.refreshRunwayConfigPreviewLine()
	ap.refreshTowerConfigPreviewLine()
	ap.clearHighlightedTarget()

	return CommandStatus{Clear: ClearNone}
}

func (ap *ASDEXPane) cmdTowerReadout(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	if !ap.hasTowerReference {
		return commandOutputClearAll("NO GLOBAL DATA")
	}

	ap.commandMode = CommandModeNone
	ap.commandEntry.Clear()
	ap.datablockEdit = nil
	ap.editingTargetID = ""
	ap.initControlEntry = nil
	ap.termControlEntry = nil
	ap.multiFunction = nil
	ap.scratchpadEntry = nil
	ap.previewReposition = nil
	ap.coastListReposition = nil
	ap.mapReposition = nil
	ap.mapRotate = nil
	ap.runwayConfigCommand = nil
	ap.dcbSpinner = nil
	ap.dcbMenuCommand = nil
	ap.dbAreaDraft = nil
	ap.dbAreaSelection = nil
	ap.tempAreaDraft = nil
	ap.tempTextCommand = nil
	ap.tempTextPlacement = nil
	ap.tempDataSelectMode = TempDataSelectNone
	ap.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	ap.tempData.ClearHighlights()
	ap.newWindow = nil
	ap.deleteWindow = nil
	ap.windowReposition = nil
	ap.resizeWindow = nil
	ap.towerReadout = NewTowerReadoutCommand(ap.towerReference)
	ap.previewArea.SetSystemResponse("")
	ap.clearHighlightedTarget()

	return CommandStatus{Clear: ClearNone}
}

func (ap *ASDEXPane) cmdMultiFunction(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	ap.commandMode = CommandModeMultiFunction
	ap.multiFunction = NewMultiFunctionCommand()
	ap.scratchpadEntry = nil
	ap.previewReposition = nil
	ap.coastListReposition = nil
	ap.mapReposition = nil
	ap.mapRotate = nil
	ap.runwayConfigCommand = nil
	ap.towerReadout = nil
	ap.dcbSpinner = nil
	ap.dcbMenuCommand = nil
	ap.dbAreaDraft = nil
	ap.dbAreaSelection = nil
	ap.tempAreaDraft = nil
	ap.tempTextCommand = nil
	ap.tempTextPlacement = nil
	ap.tempDataSelectMode = TempDataSelectNone
	ap.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	ap.tempData.ClearHighlights()
	ap.newWindow = nil
	ap.deleteWindow = nil
	ap.windowReposition = nil
	ap.resizeWindow = nil
	ap.dcb.ReturnToMainMenu()
	ap.datablockEdit = nil
	ap.editingTargetID = ""
	ap.initControlEntry = nil
	ap.termControlEntry = nil
	ap.commandEntry.Clear()
	ap.previewArea.SetSystemResponse("")
	ap.clearHighlightedTarget()

	return CommandStatus{Clear: ClearNone}
}

func (ap *ASDEXPane) cmdMapReposition(ctx *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	windowID := ap.activeWindowID()
	view := ap.activeScopeView()
	ap.commandMode = CommandModeMapReposition
	ap.mapReposition = NewMapRepositionCommand(windowID, view.Center)
	ap.multiFunction = nil
	ap.scratchpadEntry = nil
	ap.previewReposition = nil
	ap.coastListReposition = nil
	ap.mapRotate = nil
	ap.runwayConfigCommand = nil
	ap.towerReadout = nil
	ap.dcbSpinner = nil
	ap.dcbMenuCommand = nil
	ap.dbAreaDraft = nil
	ap.dbAreaSelection = nil
	ap.tempAreaDraft = nil
	ap.tempTextCommand = nil
	ap.tempTextPlacement = nil
	ap.tempDataSelectMode = TempDataSelectNone
	ap.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	ap.tempData.ClearHighlights()
	ap.newWindow = nil
	ap.deleteWindow = nil
	ap.windowReposition = nil
	ap.resizeWindow = nil
	ap.dcb.ReturnToMainMenu()
	ap.datablockEdit = nil
	ap.editingTargetID = ""
	ap.initControlEntry = nil
	ap.termControlEntry = nil
	ap.commandEntry.Clear()
	ap.previewArea.SetSystemResponse("")
	ap.clearHighlightedTarget()
	ap.centerMapRepositionCursor(ctx)

	return CommandStatus{Clear: ClearNone}
}

func (ap *ASDEXPane) cmdMapRotate(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	windowID := ap.activeWindowID()
	view := ap.activeScopeView()
	ap.startMapRotateCommand(NewMainMapRotateCommand(windowID, view.Rotation))

	return CommandStatus{Clear: ClearNone}
}

func (ap *ASDEXPane) cmdPreviewRepositionSlew(
	ctx *panes.Context,
	point DisplayPoint,
) CommandStatus {
	if ap == nil || ctx == nil {
		return CommandStatus{Clear: ClearAll}
	}

	pos := clampListRepositionPoint(
		redsmath.Vec2(point),
		ctx.PaneSize(),
		ap.previewArea.RepositionSize(),
	)
	before := ap.pushUndoBeforeMutation()
	ap.previewArea.SetLocation(pos, ctx.PaneSize())
	ap.commitUndoIfChanged(before)

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}

func (ap *ASDEXPane) cmdCoastListRepositionSlew(
	ctx *panes.Context,
	point DisplayPoint,
) CommandStatus {
	if ap == nil || ctx == nil {
		return CommandStatus{Clear: ClearAll}
	}

	pos := clampListRepositionPoint(
		redsmath.Vec2(point),
		ctx.PaneSize(),
		ap.coastList.RepositionSize(),
	)
	before := ap.pushUndoBeforeMutation()
	ap.coastList.SetLocation(pos, ctx.PaneSize())
	ap.commitUndoIfChanged(before)

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}

func (ap *ASDEXPane) cmdBeaconatorSlew(
	_ *panes.Context,
	target *Target,
) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}
	if target == nil {
		return commandOutputClearAll("NO SLEW")
	}
	if target.Suspended || target.Dropped || !targetCanHaveDataBlock(target) {
		return commandOutputClearAll("INVALID ENTRY")
	}

	ap.toggleTemporaryBeaconCodeForTarget(target)

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}

func clampListRepositionPoint(
	pos redsmath.Vec2,
	displaySize redsmath.Vec2,
	itemSize redsmath.Vec2,
) redsmath.Vec2 {
	maxX := displaySize.X - itemSize.X
	maxY := displaySize.Y - itemSize.Y
	if maxX < 0 {
		maxX = 0
	}
	if maxY < 0 {
		maxY = 0
	}

	return redsmath.Vec2{
		X: clamp(pos.X, 0, maxX),
		Y: clamp(pos.Y, 0, maxY),
	}
}

func (ap *ASDEXPane) cmdLeaderDirectionAll(
	_ *panes.Context,
	input LeaderDirectionInput,
) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	windowID := ap.activeWindowID()
	before := ap.pushUndoBeforeMutation()
	ap.updateActiveDataBlockSettings(func(settings *DataBlockSettings) {
		settings.LeaderDirection = input.Direction
	})
	ap.clearLeaderDirectionOverrides(windowID)
	ap.commitUndoIfChanged(before)

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}

func (ap *ASDEXPane) cmdLeaderDirectionSlew(
	_ *panes.Context,
	input LeaderDirectionInput,
	target *Target,
) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}
	if target == nil {
		return commandOutputClearAll("NO SLEW")
	}
	if target.Suspended || target.Dropped {
		return commandOutputClearAll("INVALID ENTRY")
	}
	if !targetCanHaveDataBlock(target) {
		return commandOutputClearAll("INVALID ENTRY")
	}

	before := ap.pushUndoBeforeMutation()
	ap.setTargetLeaderDirectionManualOverride(
		ap.activeWindowID(),
		target,
		input.Direction,
	)
	ap.commitUndoIfChanged(before)

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}

func (ap *ASDEXPane) cmdLeaderLengthAll(
	_ *panes.Context,
	input LeaderLengthInput,
) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	windowID := ap.activeWindowID()
	before := ap.pushUndoBeforeMutation()
	ap.updateActiveDataBlockSettings(func(settings *DataBlockSettings) {
		settings.LeaderLength = input.Value
	})
	ap.clearLeaderLengthOverrides(windowID)
	ap.commitUndoIfChanged(before)

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}

func (ap *ASDEXPane) cmdLeaderLengthSlew(
	_ *panes.Context,
	input LeaderLengthInput,
	target *Target,
) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}
	if target == nil {
		return commandOutputClearAll("NO SLEW")
	}
	if target.Suspended || target.Dropped {
		return commandOutputClearAll("INVALID LNG")
	}
	if !targetCanHaveDataBlock(target) {
		return commandOutputClearAll("INVALID LNG")
	}

	before := ap.pushUndoBeforeMutation()
	ap.setTargetLeaderLengthManualOverride(
		ap.activeWindowID(),
		target,
		input.Value,
	)
	ap.commitUndoIfChanged(before)

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}
