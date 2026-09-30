package stars

import (
	"strings"
	"time"

	redsnet "github.com/juliusplatzer/reds/net"
)

const rposUnsupportedKeyPrefix = "__RPOS_UNSUPPORTED__:"

// trackRepositionState is local TCW/TDW presentation state for TI 6191.409
// Rev. 30 section 5.7.3 TRK RPOS. TAIS is an observational feed, so REDS must
// not mutate its authoritative snapshot when a controller repositions a flight
// data block locally. Instead, the source flight plan is detached in the
// display-facing copy and either attached to an unassociated radar track or
// represented as an Unsupported Full Data Block at a geographic position.
type trackRepositionState struct {
	ACID           string
	OriginKey      string
	DestinationKey string
	Position       configPoint
	Facility       string
	MRTTime        time.Time
	FlightPlan     redsnet.TaisFlightPlan
	EnhancedData   redsnet.TaisEnhancedData
	HasEnhanced    bool
}

func rposACID(acid string) string {
	return strings.ToUpper(strings.TrimSpace(acid))
}

func rposUnsupportedTargetKey(acid string) string {
	return rposUnsupportedKeyPrefix + rposACID(acid)
}

func isRPOSUnsupportedTarget(target *redsnet.TaisTarget) bool {
	return target != nil && strings.HasPrefix(target.Key, rposUnsupportedKeyPrefix)
}

func allOctalDigits(s string) bool {
	if len(s) != 4 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '7' {
			return false
		}
	}
	return true
}

// rposTargetByReference resolves the source designators documented for TRK
// RPOS: ACID or a four-digit discrete beacon code. The snapshot supplied here
// is already display-facing, so a flight plan that has been repositioned to an
// unassociated track or Unsupported FDB can be selected again naturally.
func (p *STARSPane) rposTargetByReference(snapshot redsnet.TaisSnapshot, input string) (*redsnet.TaisTarget, error) {
	ref := strings.ToUpper(strings.TrimSpace(input))
	if ref == "" {
		return nil, ErrSTARSNoTrack
	}

	var matches []*redsnet.TaisTarget
	if allOctalDigits(ref) {
		for i := range snapshot.Targets {
			target := &snapshot.Targets[i]
			if target.FlightPlan == nil {
				continue
			}
			reported := strings.TrimSpace(target.Track.ReportedBeaconCode)
			assigned := strings.TrimSpace(target.FlightPlan.AssignedBeaconCode)
			if reported == ref || assigned == ref {
				matches = append(matches, target)
			}
		}
		if len(matches) > 1 {
			return nil, ErrSTARSDuplicateBeacon
		}
	} else {
		for i := range snapshot.Targets {
			target := &snapshot.Targets[i]
			if target.FlightPlan != nil && strings.EqualFold(strings.TrimSpace(target.FlightPlan.ACID), ref) {
				matches = append(matches, target)
			}
		}
		if len(matches) > 1 {
			return nil, ErrSTARSDuplicateACID
		}
	}

	if len(matches) == 0 {
		return nil, ErrSTARSNoTrack
	}
	if !p.rposSourceEligible(matches[0]) {
		return nil, ErrSTARSIllegalTrack
	}
	return matches[0], nil
}

// rposSourceEligible implements the source-side restrictions REDS can observe
// from TAIS today. STARS rejects an unassociated track and a track in handoff;
// suspended-list and command-override cases require state REDS does not yet
// carry and therefore are not manufactured here.
func (p *STARSPane) rposSourceEligible(target *redsnet.TaisTarget) bool {
	if target == nil || target.FlightPlan == nil || target.FlightPlan.Suspended {
		return false
	}
	return !taisOwnershipPending(target)
}

// setTrackReposition records one completed TRK RPOS operation. An existing
// state is addressed by ACID so an Unsupported FDB or a previously chosen
// unassociated destination can itself be repositioned again.
func (p *STARSPane) setTrackReposition(source, destination *redsnet.TaisTarget, position configPoint) error {
	if p == nil || !p.rposSourceEligible(source) {
		return ErrSTARSIllegalTrack
	}
	if destination != nil && destination.FlightPlan != nil {
		return ErrSTARSIllegalTrack
	}

	acid := rposACID(source.FlightPlan.ACID)
	if acid == "" {
		return ErrSTARSNoFlight
	}
	if p.trackRepositions == nil {
		p.trackRepositions = make(map[string]trackRepositionState)
	}

	state, exists := p.trackRepositions[acid]
	if !exists {
		state.OriginKey = targetDisplayStateKey(source)
		state.Facility = source.Facility
		state.MRTTime = source.Track.MRTTime
	}
	state.ACID = acid
	state.FlightPlan = *source.FlightPlan
	if source.EnhancedData != nil {
		state.EnhancedData = *source.EnhancedData
		state.HasEnhanced = true
	}
	if state.MRTTime.IsZero() {
		state.MRTTime = time.Unix(1, 0)
	}

	oldDisplayKey := targetDisplayStateKey(source)
	newDisplayKey := ""
	if destination != nil {
		state.DestinationKey = targetDisplayStateKey(destination)
		state.Position = configPoint{}
		newDisplayKey = state.DestinationKey
	} else {
		if !validTargetLatLon(position.Lat, position.Lon) {
			return ErrSTARSIllegalTrack
		}
		state.DestinationKey = ""
		state.Position = position
		newDisplayKey = rposUnsupportedTargetKey(acid)
	}
	p.trackRepositions[acid] = state

	// Section 5.7.3 requires an existing TPA graphic to follow the repositioned
	// Full Data Block. Move the local display-keyed state rather than duplicating
	// it. Preserve a manually selected leader direction for the same reason.
	if oldDisplayKey != "" && newDisplayKey != "" && oldDisplayKey != newDisplayKey {
		if tpa, ok := p.tpaTracks[oldDisplayKey]; ok {
			delete(p.tpaTracks, oldDisplayKey)
			if p.tpaTracks == nil {
				p.tpaTracks = make(map[string]tpaTrackState)
			}
			p.tpaTracks[newDisplayKey] = tpa
		}
		if dir, ok := p.singleTrackLeaderDirections[oldDisplayKey]; ok {
			delete(p.singleTrackLeaderDirections, oldDisplayKey)
			if p.singleTrackLeaderDirections == nil {
				p.singleTrackLeaderDirections = make(map[string]leaderLineDirection)
			}
			p.singleTrackLeaderDirections[newDisplayKey] = dir
		}
	}

	return nil
}

// pruneTrackRepositions drops local presentation state when the authoritative
// TAIS flight has disappeared, moved to a different real track, or when the
// chosen destination has since become associated with another flight.
func (p *STARSPane) pruneTrackRepositions(snapshot redsnet.TaisSnapshot) {
	if p == nil || len(p.trackRepositions) == 0 {
		return
	}
	if !snapshot.Ready {
		clear(p.trackRepositions)
		return
	}

	for acid, state := range p.trackRepositions {
		originOK := false
		convergedElsewhere := false
		for i := range snapshot.Targets {
			target := &snapshot.Targets[i]
			if target.FlightPlan == nil || !strings.EqualFold(strings.TrimSpace(target.FlightPlan.ACID), acid) {
				continue
			}
			if targetDisplayStateKey(target) == state.OriginKey {
				originOK = true
			} else {
				convergedElsewhere = true
			}
		}
		if !originOK || convergedElsewhere {
			delete(p.trackRepositions, acid)
			continue
		}

		if state.DestinationKey != "" {
			for i := range snapshot.Targets {
				target := &snapshot.Targets[i]
				if targetDisplayStateKey(target) == state.DestinationKey && target.FlightPlan != nil {
					delete(p.trackRepositions, acid)
					break
				}
			}
		}
	}
}

// applyTrackRepositions returns a display-only snapshot. The live TAIS
// snapshot remains untouched: the original radar target loses its flight plan
// locally, and the plan is either attached to the selected unassociated target
// or rendered as an Unsupported Full Data Block at the chosen position.
func (p *STARSPane) applyTrackRepositions(snapshot redsnet.TaisSnapshot) redsnet.TaisSnapshot {
	if p == nil || !snapshot.Ready || len(p.trackRepositions) == 0 {
		return snapshot
	}

	index := make(map[string]int, len(snapshot.Targets))
	for i := range snapshot.Targets {
		index[targetDisplayStateKey(&snapshot.Targets[i])] = i
	}

	for acid, state := range p.trackRepositions {
		if i, ok := index[state.OriginKey]; ok {
			target := &snapshot.Targets[i]
			if target.FlightPlan != nil && strings.EqualFold(strings.TrimSpace(target.FlightPlan.ACID), acid) {
				target.FlightPlan = nil
				target.EnhancedData = nil
			}
		}

		fp := state.FlightPlan
		var enhanced *redsnet.TaisEnhancedData
		if state.HasEnhanced {
			value := state.EnhancedData
			enhanced = &value
		}

		if state.DestinationKey != "" {
			if i, ok := index[state.DestinationKey]; ok && snapshot.Targets[i].FlightPlan == nil {
				snapshot.Targets[i].FlightPlan = &fp
				snapshot.Targets[i].EnhancedData = enhanced
			}
			continue
		}

		key := rposUnsupportedTargetKey(acid)
		snapshot.Targets = append(snapshot.Targets, redsnet.TaisTarget{
			Key:          key,
			Facility:     state.Facility,
			FlightPlan:   &fp,
			EnhancedData: enhanced,
			Track: redsnet.TaisTrack{
				MRTTime: state.MRTTime,
				Lat:     state.Position.Lat,
				Lon:     state.Position.Lon,
			},
		})
		index[key] = len(snapshot.Targets) - 1
	}

	return snapshot
}
