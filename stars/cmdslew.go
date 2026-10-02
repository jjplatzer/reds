package stars

import redsnet "github.com/juliusplatzer/reds/net"

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
