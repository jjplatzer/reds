package stars

import redsnet "github.com/juliusplatzer/reds/net"

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
