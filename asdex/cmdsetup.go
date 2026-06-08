package asdex

import (
	"strings"
	"unicode"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
)

type MultiFunctionCommand struct {
	value  string
	cursor int
}

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
	if command.value != "" {
		return
	}

	command.value = string(r)
	command.cursor = 1
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
	originalCenter redsmath.Vec2
	initialized    bool
}

func NewMapRepositionCommand(center redsmath.Vec2) *MapRepositionCommand {
	return &MapRepositionCommand{
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
	value            string
	cursor           int
	originalRotation float32
}

func NewMapRotateCommand(rotation float32) *MapRotateCommand {
	return &MapRotateCommand{
		originalRotation: rotation,
	}
}

func (command *MapRotateCommand) DisplayLines() []string {
	if command == nil {
		return nil
	}
	return []string{"ROTATE", command.value}
}

func (command *MapRotateCommand) CursorLine() int {
	return 2
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

func registerSetupCommands() {
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

func (ap *ASDEXPane) cmdDataBlocksOnOff(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	ap.datablockSettings.ShowDataBlocks = !ap.datablockSettings.ShowDataBlocks

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}

func (ap *ASDEXPane) cmdMultiFunction(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	ap.commandMode = CommandModeMultiFunction
	ap.multiFunction = NewMultiFunctionCommand()
	ap.previewReposition = nil
	ap.coastListReposition = nil
	ap.mapReposition = nil
	ap.mapRotate = nil
	ap.dcbSpinner = nil
	ap.dcbMenuCommand = nil
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

	ap.commandMode = CommandModeMapReposition
	ap.mapReposition = NewMapRepositionCommand(ap.center)
	ap.multiFunction = nil
	ap.previewReposition = nil
	ap.coastListReposition = nil
	ap.mapRotate = nil
	ap.dcbSpinner = nil
	ap.dcbMenuCommand = nil
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

	ap.commandMode = CommandModeMapRotate
	ap.mapRotate = NewMapRotateCommand(ap.rotation)
	ap.mapReposition = nil
	ap.multiFunction = nil
	ap.previewReposition = nil
	ap.coastListReposition = nil
	ap.dcbSpinner = nil
	ap.dcbMenuCommand = nil
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
	ap.previewArea.SetLocation(pos, ctx.PaneSize())

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
	ap.coastList.SetLocation(pos, ctx.PaneSize())

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

	ap.datablockSettings.LeaderDirection = input.Direction
	clear(ap.leaderDirectionByTarget)

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

	if ap.leaderDirectionByTarget == nil {
		ap.leaderDirectionByTarget = make(map[string]LeaderDirection)
	}
	ap.leaderDirectionByTarget[target.ID] = input.Direction

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

	ap.datablockSettings.LeaderLength = input.Value
	clear(ap.leaderLengthByTarget)

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

	if ap.leaderLengthByTarget == nil {
		ap.leaderLengthByTarget = make(map[string]int)
	}
	ap.leaderLengthByTarget[target.ID] = input.Value

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}
