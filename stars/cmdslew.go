package stars

import (
	"strings"

	redsnet "github.com/juliusplatzer/reds/net"
)

// §§6.13.3 and 6.13.8 use received, then assigned, beacon codes. An
// unavailable received code occupies five blanks; an unavailable assigned
// code is omitted. Do not replace a missing received code with the assignment.
func trackBeaconReadout(target *redsnet.TaisTarget) string {
	if target == nil || target.FlightPlan == nil {
		return ""
	}
	acid := strings.ToUpper(strings.TrimSpace(target.FlightPlan.ACID))
	if acid == "" {
		return ""
	}
	rbc := beaconReadoutCode(target.Track.ReportedBeaconCode)
	if rbc == "" {
		rbc = "     "
	}
	text := acid + " " + rbc
	if abc := beaconReadoutCode(target.FlightPlan.AssignedBeaconCode); abc != "" {
		text += " " + abc
	}
	return text
}

func beaconReadoutCode(code string) string {
	code = normalizeBeaconCode(code)
	if len(code) != 4 {
		return ""
	}
	return code
}

// §6.13.3 is reached only after higher-priority implied actions have been
// handled. TAIS does not carry pointouts or the DM/IF/HO/FP/CX/UN blinking
// states, so their additional manual guards cannot yet be evaluated here.
func (p *STARSPane) readOwnedTrackBeaconBySlew(target *redsnet.TaisTarget) bool {
	if p == nil || target == nil || target.FlightPlan == nil ||
		target.FlightPlan.Deleted || target.FlightPlan.Suspended || target.Track.Frozen ||
		taisOwnershipPending(target) || !p.targetOwnedByCurrentTCP(target) {
		return false
	}
	rbc := beaconReadoutCode(target.Track.ReportedBeaconCode)
	abc := beaconReadoutCode(target.FlightPlan.AssignedBeaconCode)
	if rbc != "" && abc != "" && rbc != abc {
		return false // A beacon mismatch has precedence over an implied readout.
	}
	text := trackBeaconReadout(target)
	if text == "" {
		return false
	}
	p.commandResponse = text
	return true
}

type ldbBeaconDisplayOverride struct {
	Identity starsTrackHighlightIdentity
	Visible  bool
}

func (p *STARSPane) targetLDBBeaconCodeVisible(target *redsnet.TaisTarget) bool {
	if override, ok := p.ldbBeaconDisplayOverrides[targetDisplayStateKey(target)]; ok &&
		trackHighlightIdentityMatches(override.Identity, trackHighlightIdentity(target)) {
		return override.Visible
	}
	return p.currentPrefs().DisplayLDBBeaconCodes
}

// MULTI FUNC B + slew is repetitive. §6.13.8 reads any associated track,
// regardless of ownership; §6.13.7 toggles one unassociated track's code.
// B + ENTER continues to control all LDBs via the keyboard command handler.
func (p *STARSPane) applyBeaconSlew(target *redsnet.TaisTarget) error {
	if target == nil {
		return ErrSTARSNoTrack
	}
	if target.FlightPlan != nil {
		text := trackBeaconReadout(target)
		if text == "" {
			return ErrSTARSIllegalTrack
		}
		p.commandResponse = text
		return nil
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return ErrSTARSIllegalTrack
	}
	visible := !p.targetLDBBeaconCodeVisible(target)
	if p.ldbBeaconDisplayOverrides == nil {
		p.ldbBeaconDisplayOverrides = make(map[string]ldbBeaconDisplayOverride)
	}
	p.ldbBeaconDisplayOverrides[key] = ldbBeaconDisplayOverride{
		Identity: trackHighlightIdentity(target), Visible: visible,
	}
	p.commandResponse = ""
	return nil
}

func (p *STARSPane) pruneLDBBeaconDisplayOverrides(snapshot redsnet.TaisSnapshot) {
	if !snapshot.Ready || len(p.ldbBeaconDisplayOverrides) == 0 {
		return
	}
	live := make(map[string]*redsnet.TaisTarget, len(snapshot.Targets))
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if target.FlightPlan == nil {
			live[targetDisplayStateKey(target)] = target
		}
	}
	for key, override := range p.ldbBeaconDisplayOverrides {
		target := live[key]
		if target == nil || !trackHighlightIdentityMatches(override.Identity, trackHighlightIdentity(target)) {
			delete(p.ldbBeaconDisplayOverrides, key)
		}
	}
}

// acknowledgeSPCBySlew implements TI 6191.409 Rev. 30 section 7.3's bare
// implied acknowledgement: slew the cursor to the unacknowledged alert/caution
// track and click the left trackball button. A track in a current handoff is
// not eligible. REDS does not yet receive pointout state, so that manual guard
// cannot be evaluated here.
func (p *STARSPane) acknowledgeSPCBySlew(target *redsnet.TaisTarget) bool {
	if p == nil || target == nil || taisOwnershipPending(target) || p.targetSPCAcknowledged(target) {
		return false
	}
	if _, ok := targetSpecialConditionIndicator(target); !ok {
		return false
	}

	// Section 7.3 propagates acknowledgement system-wide when the track is
	// owned by the acknowledging position (or a coupled position). REDS models
	// local ownership, but not coupled-position membership yet.
	systemWide := target.FlightPlan != nil && p.targetOwnedByCurrentTCP(target)
	p.setSPCAcknowledged(target, systemWide, false)
	return true
}
