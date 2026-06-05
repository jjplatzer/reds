package asdex

import "github.com/juliusplatzer/reds/panes"

func registerSetupCommands() {
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
