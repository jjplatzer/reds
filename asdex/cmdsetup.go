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
		"[MULT FUNC]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdMultiFunction(ctx)
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

func (ap *ASDEXPane) cmdMultiFunction(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	ap.commandMode = CommandModeMultiFunction
	ap.multiFunction = NewMultiFunctionCommand()
	ap.previewReposition = nil
	ap.coastListReposition = nil
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
