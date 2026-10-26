package stars

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	redsnet "github.com/juliusplatzer/reds/net"
)

// CRDA <MULTI FUNC> N commands from CRC InputManager.ProcessMultiFuncN and
// STARS TI 6191.409 section 6.5. Rendering and ghost hit tests use the same
// runtime state rather than maintaining a second command-only model.

type crdaControl struct {
	kind      string
	airport   string
	runway    string
	pairIndex int
	mode      crdaMode
	action    byte
	direction leaderLineDirection
}

type crdaControlParser struct{}

func (crdaControlParser) Identifier() string { return "CRDA_CONTROL" }

var (
	crdaPairCommand    = regexp.MustCompile(`^([A-Z0-9]{3})([1-9])([TSD])$`)
	crdaRunwayCommand  = regexp.MustCompile(`^([A-Z0-9]{3})(0[1-9]|[12][0-9]|3[0-6])[LRC]?([EI])?$`)
	crdaOverlayCommand = regexp.MustCompile(`^([A-Z0-9]{3})((?:0[1-9]|[12][0-9]|3[0-6])[LRC]?) ([BL])$`)
	crdaLeaderCommand  = regexp.MustCompile(`^L([A-Z0-9]{3})((?:0[1-9]|[12][0-9]|3[0-6])[LRC]?)([1-9])$`)
)

func (crdaControlParser) Parse(text string) (any, string, bool, error) {
	text = strings.ToUpper(text)
	if text == "*ALL" {
		return crdaControl{kind: "force-all"}, "", true, nil
	}
	if m := crdaPairCommand.FindStringSubmatch(text); m != nil {
		i, _ := strconv.Atoi(m[2]) // regexp restricts to one digit 1..9
		mode := crdaDisabled
		switch m[3] {
		case "T":
			mode = crdaTie
		case "S":
			mode = crdaStagger
		}
		return crdaControl{kind: "pair", airport: m[1], pairIndex: i, mode: mode}, "", true, nil
	}
	if m := crdaOverlayCommand.FindStringSubmatch(text); m != nil {
		return crdaControl{kind: "overlay", airport: m[1], runway: m[2], action: m[3][0]}, "", true, nil
	}
	if m := crdaLeaderCommand.FindStringSubmatch(text); m != nil {
		direction, ok := leaderLineDirectionFromKeypad(int(m[3][0] - '0'))
		if !ok {
			return nil, text, true, ErrSTARSCommandFormat
		}
		return crdaControl{kind: "leader", airport: m[1], runway: m[2], direction: direction}, "", true, nil
	}
	if m := crdaRunwayCommand.FindStringSubmatch(text); m != nil {
		// Extract runway separately: optional L/R/C is part of its identifier.
		runway := strings.TrimPrefix(text, m[1])
		action := byte(0)
		if m[3] != "" {
			action = m[3][0]
			runway = strings.TrimSuffix(runway, m[3])
		}
		return crdaControl{kind: "runway", airport: m[1], runway: runway, action: action}, "", true, nil
	}
	return nil, text, false, nil
}

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

// Minimum separation selection and lifecycle (VICE: stars/cmdtools.go).
// pruneMinSepTracks removes a graphic as soon as either selected aircraft is
// no longer on the radar scope, matching section 6.4 and CRC's track manager.
func (p *STARSPane) pruneMinSepTracks(snapshot redsnet.TaisSnapshot) {
	if p == nil || (p.minSepTracks[0] == "" && p.minSepTracks[1] == "") {
		return
	}
	for _, key := range p.minSepTracks {
		if key == "" || rangeBearingTargetByKey(snapshot, key) == nil {
			p.minSepTracks = [2]string{}
			return
		}
	}
}

// selectMinSepTarget handles the two trackball designations after <MIN>.
// Input errors leave the operator in MIN mode so another track can be slewed.
func (p *STARSPane) selectMinSepTarget(target *redsnet.TaisTarget, snapshot redsnet.TaisSnapshot) {
	if p == nil || target == nil {
		return
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return
	}
	if p.minSepPendingKey == "" {
		p.minSepPendingKey = key
		p.commandResponse = ""
		return
	}
	if p.minSepPendingKey == key {
		p.commandResponse = "DUP TRK"
		return
	}
	if rangeBearingTargetByKey(snapshot, p.minSepPendingKey) == nil {
		p.minSepPendingKey = ""
		p.commandResponse = ErrSTARSIllegalTrack.Error()
		return
	}
	p.minSepTracks = [2]string{p.minSepPendingKey, key}
	p.resetCommand()
}

func init() {
	// TI 6191.409 Rev. 30, 6.4: <MIN><ENTER> clears the displayed pair.
	// Track designation is handled by selectMinSepTarget after each slew.
	registerCommand(CommandModeMin, "", func(p *STARSPane, _ []any) (CommandStatus, error) {
		p.minSepTracks = [2]string{}
		return CommandStatus{}, nil
	})

	// N with no parameter inhibits all CRDA processing at this TCW, or
	// restores processing (without re-enabling previously disabled RPCs).
	registerCommand(CommandModeMultiFunc, "N", func(p *STARSPane, _ []any) (CommandStatus, error) {
		return CommandStatus{}, p.crdaToggleInhibited()
	})
	registerCommand(CommandModeMultiFunc, "N[CRDA_CONTROL]", func(p *STARSPane, args []any) (CommandStatus, error) {
		if err := p.crdaReady(); err != nil {
			return CommandStatus{}, err
		}
		c := args[0].(crdaControl)
		switch c.kind {
		case "force-all":
			p.crda.forceAll = !p.crda.forceAll
		case "pair":
			output, err := p.crdaSetPairMode(c.airport, c.pairIndex, c.mode)
			if err != nil {
				return CommandStatus{}, err
			}
			if c.mode != crdaDisabled {
				p.currentPrefs().CRDAStatusList.Visible = true
			}
			return CommandStatus{Output: output}, nil
		case "runway":
			var enable *bool
			if c.action != 0 {
				b := c.action == 'E'
				enable = &b
			}
			active, err := p.crdaSetRunwayGhosting(c.airport, c.runway, enable)
			if err != nil {
				return CommandStatus{}, err
			}
			status := "DISABLED"
			if active {
				status = "ENABLED"
			}
			return CommandStatus{Output: fmt.Sprintf("%s %s GHOSTING %s", c.airport, c.runway, status)}, nil
		case "overlay":
			return CommandStatus{}, p.crdaToggleRunwayOverlay(c.airport, c.runway, c.action == 'L')
		case "leader":
			return CommandStatus{}, p.crdaSetRunwayLeader(c.airport, c.runway, c.direction)
		}
		return CommandStatus{}, ErrSTARSCommandFormat
	})
	// CRDA status-list visibility; the corresponding click/move is handled
	// in consumeMouseEvents. VICE/STARS 6.5.11-6.5.12.
	registerCommand(CommandModeMultiFunc, "TN", func(p *STARSPane, _ []any) (CommandStatus, error) {
		if !p.crdaAvailable() {
			return CommandStatus{}, ErrSTARSIllegalFunction
		}
		ps := p.currentPrefs()
		ps.CRDAStatusList.Visible = !ps.CRDAStatusList.Visible
		return CommandStatus{}, nil
	})

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

	// TI 6191.409 Rev. 30, 6.21.18. <MULTI FUNC>, <+>, <ENTER>
	// displays the ATPA Approach Volumes disabled since the last configuration
	// plan change. The display is local to this TCW/TDW; the volume enable
	// state itself remains the shared site state controlled by 2ATPA...E/I.
	registerCommand(CommandModeMultiFunc, "+", func(p *STARSPane, args []any) (CommandStatus, error) {
		if !p.atpaEnabled() {
			return CommandStatus{}, ErrSTARSIllegalFunction
		}
		disabled := make([]string, 0, len(p.config.Facility.ATPAVolumes))
		for i := range p.config.Facility.ATPAVolumes {
			volume := &p.config.Facility.ATPAVolumes[i]
			id := atpaVolumeID(volume)
			if id != "" && !p.atpaVolumeEnabled(volume) {
				disabled = append(disabled, id)
			}
		}
		return CommandStatus{Output: strings.Join(disabled, " ")}, nil
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

// toggleSingleTrackPTL implements the keyboard-only, repetitive MULTI FUNC R
// designation in TI 6191.409 Rev. 30, 6.3.1 (p. 6-13). An individually
// toggled PTL is forbidden if PTL ALL / PTL OWN already displays that track's
// PTL. Only active radar tracks are eligible (never ghosts, pseudo tracks,
// unsupported data blocks, or coasted/dropped tracks).
func (p *STARSPane) toggleSingleTrackPTL(target *redsnet.TaisTarget) error {
	if p == nil || !singleTrackPTLEligible(target) {
		return ErrSTARSIllegalTrack
	}
	if p.automaticPTLForTarget(target) {
		return ErrSTARSIllegalTrack
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return ErrSTARSIllegalTrack
	}
	if p.singleTrackPTLs == nil {
		p.singleTrackPTLs = make(map[string]starsTrackHighlightIdentity)
	}
	id := trackHighlightIdentity(target)
	if stored, ok := p.singleTrackPTLs[key]; ok && trackHighlightIdentityMatches(stored, id) {
		delete(p.singleTrackPTLs, key)
	} else {
		p.singleTrackPTLs[key] = id
	}
	return nil
}

func singleTrackPTLEligible(target *redsnet.TaisTarget) bool {
	return taisTargetHasPosition(target) && !isRPOSUnsupportedTarget(target) &&
		target.CoastPhase < redsnet.TaisCoastPhase1 &&
		strings.EqualFold(strings.TrimSpace(target.Track.Status), "active")
}

func (p *STARSPane) pruneSingleTrackPTLs(snapshot redsnet.TaisSnapshot) {
	if p == nil || !snapshot.Ready || len(p.singleTrackPTLs) == 0 {
		return
	}
	live := make(map[string]starsTrackHighlightIdentity, len(snapshot.Targets))
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if singleTrackPTLEligible(target) {
			if key := targetDisplayStateKey(target); key != "" {
				live[key] = trackHighlightIdentity(target)
			}
		}
	}
	for key, id := range p.singleTrackPTLs {
		if current, ok := live[key]; !ok || !trackHighlightIdentityMatches(id, current) {
			delete(p.singleTrackPTLs, key)
		}
	}
}

// VICE places §6.6 restriction-area commands under cmdtools.go. Keep the
// controller's F12 commands here; location and text decoding is in parsecmd.go,
// and geometry drawing is in tools.go.
func init() {
	registerCommand(CommandModeRestrictionArea, "G[RA_TEXT]", func(p *STARSPane, args []any) (CommandStatus, error) {
		c := args[0].(starsRestrictionClause)
		r := &starsRestrictionArea{Kind: starsRestrictionText, Text: c.Text, Blink: c.Blink, Color: 1}
		if c.Location != nil {
			r.Label = *c.Location
			return CommandStatus{}, p.finishRestrictionArea(r)
		}
		p.wipRestrictionArea = r
		return CommandStatus{Clear: ClearInput}, nil
	})
	registerCommand(CommandModeRestrictionArea, "C[RA_CIRCLE]", func(p *STARSPane, args []any) (CommandStatus, error) {
		c := args[0].(starsRestrictionClause)
		r := &starsRestrictionArea{Kind: starsRestrictionCircle, RadiusNM: c.Radius, Text: c.Text, Blink: c.Blink, Shaded: c.Shaded, Color: c.Color}
		if c.Location != nil {
			r.Center = *c.Location
			r.Label = r.Center
			return CommandStatus{}, p.finishRestrictionArea(r)
		}
		p.wipRestrictionArea = r
		return CommandStatus{Clear: ClearInput}, nil
	})
	// §§6.6.5–6.6.8: VICE's typed ID commands, with the manual's
	// keyboard-only procedure and per-TCW/TDW text visibility.
	registerCommand(CommandModeRestrictionArea, "[USER_RA_INDEX]*[RA_MOVE]", func(p *STARSPane, args []any) (CommandStatus, error) {
		id := args[0].(int)
		move := args[1].(restrictionAreaMoveSpec)
		if _, err := p.restrictionAreaStore().get(id); err != nil {
			return CommandStatus{}, err
		}
		if move.Position == nil {
			p.wipRestrictionMove = &restrictionAreaPendingMove{ID: id, Blink: move.Blink}
			return CommandStatus{Clear: ClearInput}, nil
		}
		return CommandStatus{}, p.completeRestrictionAreaMove(restrictionAreaPendingMove{ID: id, Blink: move.Blink}, *move.Position)
	})
	registerCommand(CommandModeRestrictionArea, "[USER_RA_INDEX]DEL", func(p *STARSPane, args []any) (CommandStatus, error) {
		id := args[0].(int)
		if err := p.restrictionAreaStore().delete(id); err != nil {
			return CommandStatus{}, err
		}
		delete(p.currentPrefs().RestrictionAreaSettings, id)
		return CommandStatus{}, nil
	})
	registerCommand(CommandModeRestrictionArea, "[USER_RA_INDEX]T[RA_EDIT_TEXT]", func(p *STARSPane, args []any) (CommandStatus, error) {
		id, c := args[0].(int), args[1].(starsRestrictionClause)
		err := p.restrictionAreaStore().update(id, func(a *starsRestrictionArea) error {
			a.Text, a.Blink = c.Text, c.Blink
			return nil
		})
		if err != nil {
			return CommandStatus{}, err
		}
		p.showRestrictionAreaLocally(id)
		return CommandStatus{}, nil
	})
	registerCommand(CommandModeRestrictionArea, "[RA_INDEX]T [RA_DELTA]", func(p *STARSPane, args []any) (CommandStatus, error) {
		return CommandStatus{}, p.toggleRestrictionAreaText(args[0].(int), true)
	})
	registerCommand(CommandModeRestrictionArea, "[RA_INDEX]T", func(p *STARSPane, args []any) (CommandStatus, error) {
		return CommandStatus{}, p.toggleRestrictionAreaText(args[0].(int), false)
	})
	for _, shape := range []struct {
		prefix string
		kind   starsRestrictionKind
	}{{"P", starsRestrictionClosed}, {"A", starsRestrictionOpen}} {
		kind := shape.kind
		registerCommand(CommandModeRestrictionArea, shape.prefix+"[RA_LOCATION]", func(p *STARSPane, args []any) (CommandStatus, error) {
			p.wipRestrictionArea = &starsRestrictionArea{Kind: kind, Color: 1, Vertices: []configPoint{args[0].(configPoint)}}
			return CommandStatus{Clear: ClearInput}, nil
		})
		registerCommand(CommandModeRestrictionArea, shape.prefix, func(p *STARSPane, _ []any) (CommandStatus, error) {
			p.wipRestrictionArea = &starsRestrictionArea{Kind: kind, Color: 1}
			return CommandStatus{Clear: ClearInput}, nil
		})
	}
}

// The pending cursor relocation belongs to the entering STARS pane;
// the completed geometry change is applied to the site-wide store.
type restrictionAreaPendingMove struct {
	ID    int
	Blink bool
}

func (p *STARSPane) restrictionAreaStore() *starsRestrictionAreaStore {
	if p.restrictionAreas == nil {
		p.restrictionAreas = starsRestrictionStoreForSite(p.config.Facility.Facility)
	}
	return p.restrictionAreas
}
func (p *STARSPane) showRestrictionAreaLocally(id int) {
	area, err := p.restrictionAreaStore().get(id)
	if err != nil {
		return
	}
	ps := p.currentPrefs()
	if ps.RestrictionAreaSettings == nil {
		ps.RestrictionAreaSettings = make(map[int]restrictionAreaDisplaySettings)
	}
	ps.RestrictionAreaSettings[id] = restrictionAreaSettingsFor(ps, area) // text visibility unchanged
}
func (p *STARSPane) completeRestrictionAreaMove(spec restrictionAreaPendingMove, pos configPoint) error {
	err := p.restrictionAreaStore().update(spec.ID, func(a *starsRestrictionArea) error {
		if err := moveRestrictionArea(a, pos); err != nil {
			return err
		}
		if spec.Blink {
			a.Blink = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	p.showRestrictionAreaLocally(spec.ID)
	p.resetCommand()
	return nil
}
func (p *STARSPane) toggleRestrictionAreaText(id int, delta bool) error {
	area, err := p.restrictionAreaStore().get(id)
	if err != nil {
		return err
	}
	ps := p.currentPrefs()
	old := restrictionAreaSettingsFor(ps, area)
	if delta && !old.HideText {
		// §6.6.8: delta applies *only* when causing hidden text to show.
		return ErrSTARSCommandFormat
	}
	old.HideText = !old.HideText
	old.ForceBlinkingText = delta && !old.HideText
	if ps.RestrictionAreaSettings == nil {
		ps.RestrictionAreaSettings = make(map[int]restrictionAreaDisplaySettings)
	}
	ps.RestrictionAreaSettings[id] = old
	return nil
}

func (p *STARSPane) finishRestrictionArea(r *starsRestrictionArea) error {
	if r.Kind == starsRestrictionClosed && len(r.Vertices) < 3 {
		return fmt.Errorf("ILL FNCT - MIN 3 VERTICES")
	}
	if r.Kind == starsRestrictionOpen && len(r.Vertices) < 2 {
		return fmt.Errorf("ILL FNCT - MIN 2 VERTICES")
	}
	if p.restrictionAreas == nil {
		p.restrictionAreas = starsRestrictionStoreForSite(p.config.Facility.Facility)
	}
	if err := p.restrictionAreas.create(*r); err != nil {
		return err
	}
	p.resetCommand()
	return nil
}

// A polygon accepts successive cursor or keyboard vertices, then a text entry
// and a *final* cursor designation for the label (6.6.3-6.6.4). A text-only
// or circular restriction instead uses the first cursor designation for its
// location. Empty polygon input always adds a vertex, not a label.
func (p *STARSPane) restrictionAreaClick(pos configPoint) error {
	if p.wipRestrictionMove != nil {
		return p.completeRestrictionAreaMove(*p.wipRestrictionMove, pos)
	}
	// F12 ID * [DELTA] followed by a left-trackball designation is
	// valid without an intervening ENTER, as in the manual.
	if p.wipRestrictionArea == nil && strings.Contains(p.commandInput, "*") &&
		len(p.commandInput) > 0 && p.commandInput[0] >= '0' && p.commandInput[0] <= '9' {
		_, err := p.executeCommand(CommandModeRestrictionArea, p.commandInput)
		if err != nil {
			return err
		}
		p.commandInput = ""
		if p.wipRestrictionMove == nil {
			return ErrSTARSCommandFormat
		}
		return p.completeRestrictionAreaMove(*p.wipRestrictionMove, pos)
	}
	r := p.wipRestrictionArea
	if r == nil {
		// The manual permits G/C command text followed directly by a left
		// trackball designation, without an intervening ENTER.
		entry := strings.ToUpper(strings.TrimSpace(p.commandInput))
		if strings.HasPrefix(entry, "G") || strings.HasPrefix(entry, "C") {
			_, err := p.executeCommand(CommandModeRestrictionArea, entry)
			if err != nil {
				return err
			}
			if p.wipRestrictionArea == nil {
				// An explicit keyboard location already completed the command.
				return nil
			}
			p.commandInput = ""
			return p.restrictionAreaClick(pos)
		}
		// VICE allows the first polygon vertex by trackball without ENTER.
		switch entry {
		case "P", "A":
			kind := starsRestrictionClosed
			if entry == "A" {
				kind = starsRestrictionOpen
			}
			p.wipRestrictionArea = &starsRestrictionArea{Kind: kind, Color: 1, Vertices: []configPoint{pos}}
			p.commandInput = ""
			return nil
		}
		return ErrSTARSCommandFormat
	}

	switch r.Kind {
	case starsRestrictionText, starsRestrictionCircle:
		if r.Kind == starsRestrictionCircle {
			r.Center = pos
		}
		r.Label = pos
		return p.finishRestrictionArea(r)
	case starsRestrictionOpen, starsRestrictionClosed:
		if r.LabelPending {
			r.Label = pos
			return p.finishRestrictionArea(r)
		}
		if strings.TrimSpace(p.commandInput) != "" {
			c, err := parseRestrictionAreaText(p.commandInput, r.Kind == starsRestrictionClosed)
			if err != nil {
				return err
			}
			r.Text, r.Blink, r.Shaded, r.Color = c.Text, c.Blink, c.Shaded, c.Color
			r.Label = pos
			return p.finishRestrictionArea(r)
		}
		return p.appendRestrictionVertex(pos)
	}
	return ErrSTARSCommandFormat
}

func (p *STARSPane) appendRestrictionVertex(pos configPoint) error {
	if len(p.wipRestrictionArea.Vertices) >= 10 {
		return fmt.Errorf("CAPACITY - MAX 10 VERTICES")
	}
	p.wipRestrictionArea.Vertices = append(p.wipRestrictionArea.Vertices, pos)
	return nil
}

func (p *STARSPane) commitRestrictionAreaStep(input string) error {
	r := p.wipRestrictionArea
	if r == nil {
		return ErrSTARSCommandFormat
	}
	if r.Kind == starsRestrictionCircle || r.Kind == starsRestrictionText {
		pt, err := parseRestrictionAreaLocation(input)
		if err != nil {
			return err
		}
		if r.Kind == starsRestrictionCircle {
			r.Center = pt
		}
		r.Label = pt
		return p.finishRestrictionArea(r)
	}
	if r.LabelPending {
		pt, err := parseRestrictionAreaLocation(input)
		if err != nil {
			return err
		}
		r.Label = pt
		return p.finishRestrictionArea(r)
	}
	if pt, err := parseRestrictionAreaLocation(input); err == nil {
		return p.appendRestrictionVertex(pt)
	}
	minVertices := 2
	if r.Kind == starsRestrictionClosed {
		minVertices = 3
	}
	if len(r.Vertices) < minVertices {
		return fmt.Errorf("ILL FNCT - MIN %d VERTICES", minVertices)
	}
	c, err := parseRestrictionAreaText(input, r.Kind == starsRestrictionClosed)
	if err != nil {
		return err
	}
	r.Text, r.Blink, r.Shaded, r.Color = c.Text, c.Blink, c.Shaded, c.Color
	r.LabelPending = true
	return nil
}
