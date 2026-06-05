package asdex

import "github.com/juliusplatzer/reds/panes"

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
