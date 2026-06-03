package asdex

import "github.com/juliusplatzer/reds/panes"

// commands that are executed on slews (click on target) and right slews (right click on target)

func registerSlewCommands() {
	registerCommand(
		CommandModeNone,
		"[SLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, target *Target) CommandStatus {
			return ap.cmdBareAircraftSlew(ctx, target)
		},
	)
}

func (ap *ASDEXPane) cmdBareAircraftSlew(
	_ *panes.Context,
	target *Target,
) CommandStatus {
	if ap == nil || target == nil {
		return CommandStatus{}
	}

	// Later passes add suspended and dropped target precedence here.
	if !targetHasDatablock(classifyTarget(target)) {
		return CommandStatus{}
	}

	target.ShowDB = !target.ShowDB
	return CommandStatus{}
}
