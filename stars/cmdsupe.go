package stars

// Supervisor commands from TI 6191.409 Rev. 30, 8.37-8.39.
//
// REDS does not currently carry STARS supervisor-login/authorization state, so
// these commands cannot yet emit the manual's PRIV VIOLATION response. The
// command syntax, ATPA state transitions, ILL FNCT / ILL VOL responses, and
// SSA effects otherwise follow the operator manual; VICE is used for the same
// runtime-state split and default-reset behavior.
func init() {
	// 8.37 Enable / disable ATPA system-wide.
	registerCommand(CommandModeMultiFunc, "2ATPAE", func(p *STARSPane, args []any) (CommandStatus, error) {
		return configureATPASystem(p, true)
	})
	registerCommand(CommandModeMultiFunc, "2ATPAI", func(p *STARSPane, args []any) (CommandStatus, error) {
		return configureATPASystem(p, false)
	})

	// 8.38 Enable / disable an ATPA Approach Volume.
	registerCommand(CommandModeMultiFunc, "2ATPA[ATPA_VOLUME_ACTION]", func(p *STARSPane, args []any) (CommandStatus, error) {
		if !p.atpaEnabled() {
			return CommandStatus{}, ErrSTARSIllegalFunction
		}
		action := args[0].(atpaVolumeAction)
		volume := p.atpaVolumeByID(action.VolumeID)
		if volume == nil {
			return CommandStatus{}, ErrSTARSIllegalVolume
		}
		p.setATPAVolumeEnabled(volume, action.Enable)
		return CommandStatus{}, nil
	})

	// 8.39 Enable / disable ATPA 2.5-NM Reduced Separation for a volume.
	registerCommand(CommandModeMultiFunc, "2.5[ATPA_VOLUME_ACTION]", func(p *STARSPane, args []any) (CommandStatus, error) {
		if !p.atpaEnabled() {
			return CommandStatus{}, ErrSTARSIllegalFunction
		}
		action := args[0].(atpaVolumeAction)
		volume := p.atpaVolumeByID(action.VolumeID)
		if volume == nil {
			return CommandStatus{}, ErrSTARSIllegalVolume
		}
		// The manual specifies ILL FNCT when 2.5-NM separation is not adapted
		// for the entered volume, and ILL VOL when the volume itself is disabled.
		if !volume.TwoPointFiveApproachEnabled || volume.TwoPointFiveApproachDistance == nil ||
			*volume.TwoPointFiveApproachDistance <= 0 {
			return CommandStatus{}, ErrSTARSIllegalFunction
		}
		if !p.atpaVolumeEnabled(volume) {
			return CommandStatus{}, ErrSTARSIllegalVolume
		}
		p.setATPAVolume25Enabled(volume, action.Enable)
		return CommandStatus{}, nil
	})
}

func configureATPASystem(p *STARSPane, enabled bool) (CommandStatus, error) {
	if p == nil || !p.atpaAdapted() {
		return CommandStatus{}, ErrSTARSIllegalFunction
	}
	if !p.setATPAEnabled(enabled) {
		// 8.37 explicitly defines this informational response when ATPA is
		// already in the requested site-wide state.
		return CommandStatus{Output: "NO CHANGE"}, nil
	}
	return CommandStatus{}, nil
}
