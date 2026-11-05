package stars

import (
	"strings"

	redsnet "github.com/juliusplatzer/reds/net"
)

type alertArgumentsParser struct{}

func (alertArgumentsParser) Identifier() string { return "ALERT_ARGS" }
func (alertArgumentsParser) Parse(text string) (any, string, bool, error) {
	if strings.TrimSpace(text) == "" {
		return nil, text, false, nil
	}
	return strings.Fields(text), "", true, nil
}

func init() {
	commandTypeParsers["ALERT_ARGS"] = alertArgumentsParser{}
	// §§7.9,7.11,7.17: data identity counterparts to the slew commands.
	registerCommand(CommandModeCA, "K [ALERT_ARGS]", func(p *STARSPane, args []any) (CommandStatus, error) {
		fields := args[0].([]string)
		if len(fields) != 1 {
			return CommandStatus{}, ErrSTARSCommandFormat
		}
		target, err := p.alertCommandTarget(fields[0])
		if err == nil {
			err = p.toggleCATrackInhibit(target)
		}
		return CommandStatus{}, err
	})
	registerCommand(CommandModeCA, "M [ALERT_ARGS]", func(p *STARSPane, args []any) (CommandStatus, error) {
		fields := args[0].([]string)
		if len(fields) < 1 || len(fields) > 2 {
			return CommandStatus{}, ErrSTARSCommandFormat
		}
		target, err := p.alertCommandTarget(fields[0])
		code := ""
		if len(fields) == 2 {
			code = mciBeaconCode(fields[1])
			if code == "" {
				return CommandStatus{}, ErrSTARSCommandFormat
			}
		}
		if err == nil {
			err = p.toggleMCIBeaconInhibit(target, code)
		}
		return CommandStatus{}, err
	})
	registerCommand(CommandModeCA, "P [ALERT_ARGS]", func(p *STARSPane, args []any) (CommandStatus, error) {
		fields := args[0].([]string)
		if len(fields) != 1 {
			return CommandStatus{}, ErrSTARSCommandFormat
		}
		target, err := p.alertCommandTarget(fields[0])
		if err == nil {
			err = p.inhibitCAPair(target)
		}
		return CommandStatus{}, err
	})
	for _, text := range []string{"C", "CE", "CI"} {
		registerCommand(CommandModeCA, text, func(p *STARSPane, _ []any) (CommandStatus, error) {
			return p.configureTCPCAPairs(text)
		})
	}
	// §§7.18-7.19. List position/visibility are display preferences, while
	// the per-track suppression state remains shared and independent.
	registerCommand(CommandModeMultiFunc, "TO", func(p *STARSPane, _ []any) (CommandStatus, error) {
		p.prefs.MCISuppressionList.Visible = !p.prefs.MCISuppressionList.Visible
		return CommandStatus{}, nil
	})
	// FAA runway Type 1/2 zones require separate airport/runway adaptation.
	// VICE inhibit polygons are fixed filters, not these switchable zones.
	registerCommand(CommandModeCA, "QQ", func(p *STARSPane, _ []any) (CommandStatus, error) { return CommandStatus{}, ErrSTARSIllegalAirport })
	registerCommand(CommandModeCA, "QZ", func(p *STARSPane, _ []any) (CommandStatus, error) { return CommandStatus{Output: "NO CHANGE"}, nil })
	registerCommand(CommandModeCA, "Q[ALERT_ARGS]", func(p *STARSPane, _ []any) (CommandStatus, error) { return CommandStatus{}, ErrSTARSIllegalAirport })
}

// Resolve current associated tracks by the same ACID/beacon identities used
// by RPOS, without allocating another snapshot or accepting ambiguous IDs.
func (p *STARSPane) alertCommandTarget(ref string) (*redsnet.TaisTarget, error) {
	ref = strings.ToUpper(strings.TrimSpace(ref))
	var found *redsnet.TaisTarget
	beacon := mciBeaconCode(ref) != ""
	for _, target := range p.caTargets {
		if target.FlightPlan == nil {
			continue
		}
		match := strings.EqualFold(target.FlightPlan.ACID, ref)
		if beacon {
			match = target.Track.ReportedBeaconCode == ref || target.FlightPlan.AssignedBeaconCode == ref
		}
		if !match {
			continue
		}
		if found != nil {
			if beacon {
				return nil, ErrSTARSDuplicateBeacon
			}
			return nil, ErrSTARSDuplicateACID
		}
		found = target
	}
	if found == nil {
		return nil, ErrSTARSNoFlight
	}
	return found, nil
}

func (p *STARSPane) configureTCPCAPairs(command string) (CommandStatus, error) {
	if p.ownTCP() == "" {
		return CommandStatus{}, ErrSTARSIllegalPosition
	}
	site := p.caSiteState()
	site.Lock()
	defer site.Unlock()
	if site.caDisabled {
		return CommandStatus{}, ErrSTARSCAOff
	}
	if site.tcpInhibits == nil {
		site.tcpInhibits = make(map[string]bool)
	}
	inhibit := !site.tcpInhibits[p.ownTCP()]
	if command == "CE" {
		inhibit = false
	} else if command == "CI" {
		inhibit = true
	}
	if inhibit {
		site.tcpInhibits[p.ownTCP()] = true
	} else {
		delete(site.tcpInhibits, p.ownTCP())
	}
	return CommandStatus{}, nil
}

func (p *STARSPane) inhibitCAPair(target *redsnet.TaisTarget) error {
	if !p.caCommandTargetValid(target) {
		return ErrSTARSIllegalTrack
	}
	key := targetDisplayStateKey(target)
	site := p.caSiteState()
	site.RLock()
	for pair := range site.pairs {
		if caPairHas(pair, key) {
			site.RUnlock()
			return ErrSTARSIllegalTrack
		}
	}
	site.RUnlock()
	return p.toggleCAPairInhibit(target)
}

// §7.3 acknowledges all alert classes on the selected track in one slew.
// A co-conflicting track's unrelated LA/SPC remains untouched.
func (p *STARSPane) acknowledgeTrackAlerts(target *redsnet.TaisTarget) bool {
	if p == nil || target == nil || taisOwnershipPending(target) {
		return false
	}
	ca := p.acknowledgeCABySlew(target)
	mci := p.acknowledgeMCIBySlew(target)
	la := p.acknowledgeMSAWBySlew(target)
	spc := p.acknowledgeSPCBySlew(target)
	return ca || mci || la || spc
}

// acknowledgeUnassociatedSPCSystemWide implements TI 6191.409 Rev. 30 section
// 7.16: <MULTI FUNC>, <G>, slew an unassociated SPC track, left click. Unlike
// the ordinary 7.3 acknowledgement this explicitly distributes the SPC
// acknowledgement to every REDS STARS pane in this process for the same target.
func (p *STARSPane) acknowledgeUnassociatedSPCSystemWide(target *redsnet.TaisTarget) error {
	if target == nil {
		return ErrSTARSNoTrack
	}
	if target.FlightPlan != nil {
		return ErrSTARSIllegalTrack
	}
	if _, ok := targetSpecialConditionIndicator(target); !ok || p.targetSPCAcknowledged(target) {
		return ErrSTARSIllegalTrack
	}
	p.setSPCAcknowledged(target, true, true)
	return nil
}
