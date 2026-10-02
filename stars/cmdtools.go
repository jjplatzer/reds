package stars

import (
	"strings"

	redsnet "github.com/juliusplatzer/reds/net"
)

type requestedAltitudeDisplayOverride struct {
	SFPN    int
	ACID    string
	Display bool
}

func requestedAltitudeDisplayIdentity(target *redsnet.TaisTarget) (int, string) {
	if target == nil || target.FlightPlan == nil {
		return 0, ""
	}
	return target.FlightPlan.SFPN, strings.ToUpper(strings.TrimSpace(target.FlightPlan.ACID))
}

// displayRequestedAltitude returns the controller-wide fallback state used by
// FDBs that do not have a single-track override. REDS does not yet export the
// site's adapted FDB "display requested altitude" value from CRC, so use the
// same fallback as VICE's facility adaptation: disabled until RA/RAE changes it.
func (p *STARSPane) displayRequestedAltitude() bool {
	return p != nil && p.requestedAltitudeDisplayOverride != nil && *p.requestedAltitudeDisplayOverride
}

func (p *STARSPane) displayRequestedAltitudeForTarget(target *redsnet.TaisTarget) bool {
	if p == nil || target == nil || target.FlightPlan == nil || target.FlightPlan.RequestedAltitude <= 0 {
		return false
	}
	key := targetDisplayStateKey(target)
	if key != "" && p.requestedAltitudeTrackOverrides != nil {
		if override, ok := p.requestedAltitudeTrackOverrides[key]; ok {
			sfpn, acid := requestedAltitudeDisplayIdentity(target)
			if override.SFPN == sfpn && override.ACID == acid {
				return override.Display
			}
		}
	}
	return p.displayRequestedAltitude()
}

func (p *STARSPane) setRequestedAltitudeDisplayForTarget(target *redsnet.TaisTarget, display bool) {
	if p == nil || target == nil || target.FlightPlan == nil {
		return
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return
	}
	if p.requestedAltitudeTrackOverrides == nil {
		p.requestedAltitudeTrackOverrides = make(map[string]requestedAltitudeDisplayOverride)
	}
	sfpn, acid := requestedAltitudeDisplayIdentity(target)
	p.requestedAltitudeTrackOverrides[key] = requestedAltitudeDisplayOverride{
		SFPN: sfpn, ACID: acid, Display: display,
	}
}

func (p *STARSPane) pruneRequestedAltitudeDisplayOverrides(snapshot redsnet.TaisSnapshot) {
	if p == nil || !snapshot.Ready || len(p.requestedAltitudeTrackOverrides) == 0 {
		return
	}
	type identity struct {
		sfpn int
		acid string
	}
	live := make(map[string]identity, len(snapshot.Targets))
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if target.FlightPlan == nil || target.FlightPlan.RequestedAltitude <= 0 {
			continue
		}
		key := targetDisplayStateKey(target)
		if key == "" {
			continue
		}
		sfpn, acid := requestedAltitudeDisplayIdentity(target)
		live[key] = identity{sfpn: sfpn, acid: acid}
	}
	for key, override := range p.requestedAltitudeTrackOverrides {
		id, ok := live[key]
		if !ok || override.SFPN != id.sfpn || override.ACID != id.acid {
			delete(p.requestedAltitudeTrackOverrides, key)
		}
	}
}

func (p *STARSPane) applyRequestedAltitudeDisplayCommand(target *redsnet.TaisTarget, command string) error {
	if p == nil || target == nil {
		return ErrSTARSNoTrack
	}
	if target.FlightPlan == nil || target.FlightPlan.Suspended {
		return ErrSTARSIllegalTrack
	}
	dbType, _, _ := p.targetDatablockPresentation(target)
	if dbType != targetDatablockFull {
		return ErrSTARSIllegalTrack
	}
	if target.FlightPlan.RequestedAltitude <= 0 {
		return ErrSTARSIllegalFunction
	}

	display := false
	switch command {
	case "RAE":
		display = true
	case "RAI":
		display = false
	case "RA":
		display = !p.displayRequestedAltitudeForTarget(target)
	default:
		return ErrSTARSCommandFormat
	}
	p.setRequestedAltitudeDisplayForTarget(target, display)
	return nil
}

func init() {
	// TI 6191.409 Rev. 30, 6.13.23. These keyboard-only commands change the
	// entering TCW/TDW's fallback requested-altitude display state. Explicit
	// single-track RAE/RAI overrides remain in force, matching VICE/STARS.
	registerCommand(CommandModeMultiFunc, "RA", func(p *STARSPane, args []any) (CommandStatus, error) {
		b := !p.displayRequestedAltitude()
		p.requestedAltitudeDisplayOverride = &b
		return CommandStatus{}, nil
	})
	registerCommand(CommandModeMultiFunc, "RAE", func(p *STARSPane, args []any) (CommandStatus, error) {
		b := true
		p.requestedAltitudeDisplayOverride = &b
		return CommandStatus{}, nil
	})
	registerCommand(CommandModeMultiFunc, "RAI", func(p *STARSPane, args []any) (CommandStatus, error) {
		b := false
		p.requestedAltitudeDisplayOverride = &b
		return CommandStatus{}, nil
	})

	// TI 6191.409 Rev. 30, 6.21.5 / 6.21.9 / 6.21.11. Single-track
	// TPA commands end with a slew and are handled in consumeMouseEvents; the
	// keyboard-only all-track forms live in the declarative command table.
	registerCommand(CommandModeNone, "**J", func(p *STARSPane, args []any) (CommandStatus, error) {
		for key, state := range p.tpaTracks {
			state.JRingRadius = 0
			p.setTPAState(key, state)
		}
		return CommandStatus{}, nil
	})
	registerCommand(CommandModeNone, "**P", func(p *STARSPane, args []any) (CommandStatus, error) {
		for key, state := range p.tpaTracks {
			state.ConeLength = 0
			p.setTPAState(key, state)
		}
		return CommandStatus{}, nil
	})
	setAllTPASize := func(p *STARSPane, enabled bool) CommandStatus {
		p.currentPrefs().DisplayTPASize = enabled
		for key, state := range p.tpaTracks {
			state.DisplaySize = nil
			p.setTPAState(key, state)
		}
		if enabled {
			return CommandStatus{Output: "TPA SIZE ON"}
		}
		return CommandStatus{Output: "TPA SIZE OFF"}
	}
	registerCommand(CommandModeNone, "*D+", func(p *STARSPane, args []any) (CommandStatus, error) {
		return setAllTPASize(p, !p.currentPrefs().DisplayTPASize), nil
	})
	registerCommand(CommandModeNone, "*D+E", func(p *STARSPane, args []any) (CommandStatus, error) {
		return setAllTPASize(p, true), nil
	})
	registerCommand(CommandModeNone, "*D+I", func(p *STARSPane, args []any) (CommandStatus, error) {
		return setAllTPASize(p, false), nil
	})

	// TI 6191.409 Rev. 30, 6.21.17. The all-track INTRAIL DIST setting
	// affects current and future qualifying Full Data Blocks at this TCW/TDW.
	setAllATPAInTrail := func(p *STARSPane, enabled bool) (CommandStatus, error) {
		if !p.atpaEnabled() {
			return CommandStatus{}, ErrSTARSIllegalFunction
		}
		p.currentPrefs().DisplayATPAInTrailDist = enabled
		return CommandStatus{}, nil
	}
	registerCommand(CommandModeNone, "*DE", func(p *STARSPane, args []any) (CommandStatus, error) {
		return setAllATPAInTrail(p, true)
	})
	registerCommand(CommandModeNone, "*DI", func(p *STARSPane, args []any) (CommandStatus, error) {
		return setAllATPAInTrail(p, false)
	})

	// TI 6191.409 Rev. 30, 6.21.13. Enable/inhibit ATPA Warning and
	// Alert Cones for this TCW/TDW. This is the master gate for the
	// corresponding single-track *AE/*AI overrides.
	setAllATPAWarnAlert := func(p *STARSPane, enabled bool) (CommandStatus, error) {
		if !p.atpaEnabled() {
			return CommandStatus{}, ErrSTARSIllegalFunction
		}
		p.currentPrefs().DisplayATPAWarningAlertCones = enabled
		// A position-wide setting applies to all current and future qualifying
		// tracks. CRC likewise clears its selected-track Warning/Alert
		// overrides when this setting changes.
		for key, state := range p.tpaTracks {
			state.DisplayATPAWarnAlert = nil
			p.setTPAState(key, state)
		}
		return CommandStatus{}, nil
	}
	registerCommand(CommandModeNone, "*AE", func(p *STARSPane, args []any) (CommandStatus, error) {
		return setAllATPAWarnAlert(p, true)
	})
	registerCommand(CommandModeNone, "*AI", func(p *STARSPane, args []any) (CommandStatus, error) {
		return setAllATPAWarnAlert(p, false)
	})

	// TI 6191.409 Rev. 30, 6.21.15. Enable/inhibit ATPA Monitor Cones
	// for this TCW/TDW.
	setAllATPAMonitor := func(p *STARSPane, enabled bool) (CommandStatus, error) {
		if !p.atpaEnabled() {
			return CommandStatus{}, ErrSTARSIllegalFunction
		}
		p.currentPrefs().DisplayATPAMonitorCones = enabled
		// As with Warning/Alert Cones, the position-wide command establishes
		// the current/future setting for all qualifying tracks.
		for key, state := range p.tpaTracks {
			state.DisplayATPAMonitor = nil
			p.setTPAState(key, state)
		}
		return CommandStatus{}, nil
	}
	registerCommand(CommandModeNone, "*BE", func(p *STARSPane, args []any) (CommandStatus, error) {
		return setAllATPAMonitor(p, true)
	})
	registerCommand(CommandModeNone, "*BI", func(p *STARSPane, args []any) (CommandStatus, error) {
		return setAllATPAMonitor(p, false)
	})

}

func parseTPAImpliedCommand(input string) (op byte, distance float32, sizeMode byte, recognized bool, err error) {
	input = strings.ToUpper(strings.TrimSpace(input))
	if input == "" {
		return 0, 0, 0, false, nil
	}

	if input == "*AE" {
		return 'A', 0, 'E', true, nil
	}
	if input == "*AI" {
		return 'A', 0, 'I', true, nil
	}
	if input == "*BE" {
		return 'B', 0, 'E', true, nil
	}
	if input == "*BI" {
		return 'B', 0, 'I', true, nil
	}
	if input == "*DE" {
		return 'T', 0, 'E', true, nil
	}
	if input == "*DI" {
		return 'T', 0, 'I', true, nil
	}
	if input == "*D+" {
		return 'D', 0, 0, true, nil
	}
	if input == "*D+E" {
		return 'D', 0, 'E', true, nil
	}
	if input == "*D+I" {
		return 'D', 0, 'I', true, nil
	}
	if input == "*J" || input == "*P" {
		return input[1], 0, 0, true, nil
	}
	if !strings.HasPrefix(input, "*J") && !strings.HasPrefix(input, "*P") {
		return 0, 0, 0, false, nil
	}

	distanceText := input[2:]
	value, rest, matched, parseErr := (tpaDistanceCommandParser{}).Parse(distanceText)
	if parseErr != nil {
		return 0, 0, 0, true, parseErr
	}
	if !matched || rest != "" {
		return 0, 0, 0, true, ErrSTARSCommandFormat
	}
	return input[1], value.(float32), 0, true, nil
}

func (p *STARSPane) applyTPAImpliedCommand(target *redsnet.TaisTarget, op byte, distance float32, sizeMode byte) error {
	if p == nil || target == nil || !taisTargetHasPosition(target) {
		return ErrSTARSNoTrack
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return ErrSTARSNoTrack
	}
	state := p.tpaState(key)

	switch op {
	case 'J':
		if distance == 0 {
			if state.JRingRadius == 0 {
				return ErrSTARSIllegalFunction
			}
			state.JRingRadius = 0
			p.setTPAState(key, state)
			return nil
		}
		if !state.hasGraphic() && p.tpaGraphicCount() >= starsMaxTPAGraphics {
			return ErrSTARSCapacity
		}
		state.JRingRadius = distance
		state.ConeLength = 0
		p.setTPAState(key, state)
		return nil

	case 'P':
		if distance == 0 {
			if state.ConeLength == 0 {
				return ErrSTARSIllegalFunction
			}
			state.ConeLength = 0
			p.setTPAState(key, state)
			return nil
		}
		if !state.hasGraphic() && p.tpaGraphicCount() >= starsMaxTPAGraphics {
			return ErrSTARSCapacity
		}
		state.ConeLength = distance
		state.JRingRadius = 0
		p.setTPAState(key, state)
		return nil

	case 'D':
		atpa, hasATPA := p.atpaTrackState(key)
		if !state.hasGraphic() && (!hasATPA || atpa.MinimumSeparation <= 0 || atpa.LeadKey == "") {
			return ErrSTARSIllegalFunction
		}
		switch sizeMode {
		case 'E':
			v := true
			state.DisplaySize = &v
		case 'I':
			v := false
			state.DisplaySize = &v
		default:
			current := p.currentPrefs().DisplayTPASize
			if state.DisplaySize != nil {
				current = *state.DisplaySize
			}
			v := !current
			state.DisplaySize = &v
		}
		p.setTPAState(key, state)
		return nil

	case 'A':
		// 6.21.12: the position-wide Warning/Alert display must be enabled,
		// and the selected track must be an associated IFR FDB in an enabled
		// ATPA approach volume.
		if !p.atpaEnabled() || !p.currentPrefs().DisplayATPAWarningAlertCones {
			return ErrSTARSIllegalFunction
		}
		atpa, ok := p.atpaTrackState(key)
		if !ok || atpa.Ineligible || target.FlightPlan == nil {
			return ErrSTARSIllegalTrack
		}
		dbType, _, _ := p.targetDatablockPresentation(target)
		if dbType != targetDatablockFull {
			return ErrSTARSIllegalTrack
		}
		v := sizeMode == 'E'
		state.DisplayATPAWarnAlert = &v
		p.setTPAState(key, state)
		return nil

	case 'B':
		// 6.21.14: single-track Monitor Cone control requires ATPA and the
		// TCW/TDW Warning/Alert function to be enabled. The track must be
		// owned here or adapted for Monitor Cones, qualify in a volume, and
		// have usable weight/CWT data (i.e. not display NOWGT).
		if !p.atpaEnabled() || !p.currentPrefs().DisplayATPAWarningAlertCones {
			return ErrSTARSIllegalFunction
		}
		atpa, ok := p.atpaTrackState(key)
		if !ok || atpa.Ineligible || atpa.MinimumSeparation <= 0 || target.FlightPlan == nil {
			return ErrSTARSIllegalTrack
		}
		volume := p.atpaVolumeForState(atpa)
		if volume == nil || !p.atpaMonitorConeAllowed(target, volume) {
			return ErrSTARSIllegalTrack
		}
		v := sizeMode == 'E'
		state.DisplayATPAMonitor = &v
		p.setTPAState(key, state)
		return nil

	case 'T':
		// 6.21.16: ATPA must be enabled, and the selected track must be
		// associated, currently qualifying inside an ATPA volume, and
		// displaying a Full Data Block. A single-track setting overrides the
		// TCW/TDW-wide INTRAIL DIST state.
		if !p.atpaEnabled() {
			return ErrSTARSIllegalFunction
		}
		if target.FlightPlan == nil {
			return ErrSTARSIllegalTrack
		}
		atpa, ok := p.atpaTrackState(key)
		if !ok || atpa.Ineligible {
			return ErrSTARSIllegalTrack
		}
		dbType, _, _ := p.targetDatablockPresentation(target)
		if dbType != targetDatablockFull {
			return ErrSTARSIllegalTrack
		}
		v := sizeMode == 'E'
		state.DisplayATPAInTrail = &v
		p.setTPAState(key, state)
		return nil
	}

	return ErrSTARSCommandFormat
}
