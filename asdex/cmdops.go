package asdex

import (
	"time"

	"github.com/juliusplatzer/reds/panes"
)

const (
	maxSuspendedTargets     = 26
	suspendedTrackLifetime  = time.Hour
	numSuspendedTracksAtMax = "NUM SUSP TRKS AT MAX"
)

func registerOpsCommands() {
	registerCommand(
		CommandModeNone,
		"[TRK SUSP]",
		func(ap *ASDEXPane, ctx *panes.Context) CommandStatus {
			return ap.cmdTrackSuspend(ctx)
		},
	)

	registerCommand(
		CommandModeTrackSuspend,
		"[SLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, target *Target) CommandStatus {
			return ap.cmdTrackSuspendSlew(ctx, target)
		},
	)
}

func (ap *ASDEXPane) cmdTrackSuspend(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{}
	}

	ap.commandMode = CommandModeTrackSuspend
	ap.datablockEdit = nil
	ap.editingTargetID = ""
	ap.clearHighlightedTarget()
	ap.previewArea.SetSystemResponse("")

	return CommandStatus{Clear: ClearNone}
}

func (ap *ASDEXPane) cmdTrackSuspendSlew(
	_ *panes.Context,
	target *Target,
) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}
	if target == nil {
		return CommandStatus{
			Clear:     ClearAll,
			Output:    "NO SLEW",
			HasOutput: true,
		}
	}
	if target.Suspended || target.Coasting || target.Dropped {
		return CommandStatus{Clear: ClearAll}
	}
	if !targetHasDatablock(classifyTarget(target)) {
		return CommandStatus{Clear: ClearAll}
	}
	if ap.targets.SuspendedCount() >= maxSuspendedTargets {
		return commandOutputClearAll(numSuspendedTracksAtMax)
	}

	letter := ap.targets.NextAvailableSuspendedTrackID()
	if letter == "" {
		return commandOutputClearAll(numSuspendedTracksAtMax)
	}

	ap.targets.SuspendTarget(
		target.ID,
		letter,
		time.Now().UTC().Add(suspendedTrackLifetime),
	)

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}

func commandOutputClearAll(text string) CommandStatus {
	return CommandStatus{
		Clear:     ClearAll,
		Output:    text,
		HasOutput: true,
	}
}
