package stars

import (
	"bytes"
	"encoding/json"
	stdmath "math"
	"sort"
	"strconv"
	"strings"

	redsnet "github.com/juliusplatzer/reds/net"
	"github.com/juliusplatzer/reds/radar"
)

const feetPerNauticalMile = 6076.12

// atpaTrackState is derived from the current TAIS snapshot and the adapted
// CRC/vNAS ATPA approach volumes. It is intentionally not a saved preference:
// STARS continuously recomputes in-trail pairing as tracks move through an
// approach volume.
type atpaTrackState struct {
	VolumeID        string
	LeadKey         string
	InTrailDistance float32
	Ineligible      bool
}

type atpaVolumeMember struct {
	TargetIndex       int
	ThresholdDistance float64
	Ineligible        bool
}

// atpaEnabled is the runtime stand-in for STARS' system-wide ATPA state. REDS
// does not yet implement the supervisor 2ATPA command, so an adapted volume is
// treated as an enabled ATPA installation. When the supervisor state is added,
// this is the single place that needs to become state-aware.
func (p *STARSPane) atpaEnabled() bool {
	return p != nil && len(p.config.Facility.ATPAVolumes) != 0
}

func (p *STARSPane) atpaTrackState(key string) (atpaTrackState, bool) {
	if p == nil || p.atpaTracks == nil || key == "" {
		return atpaTrackState{}, false
	}
	state, ok := p.atpaTracks[key]
	return state, ok
}

// updateATPAInTrail implements the pairing portion of ATPA described around TI
// 6191.409 Rev. 30 Figure 6-25 and mirrors VICE's updateInTrailDistance:
// qualifying tracks are grouped by ATPA approach volume, ordered by distance
// from the runway threshold, and every track after the first is paired with the
// track directly ahead. The current horizontal distance between the pair is
// the Full Data Block INTRAIL DIST value.
//
// This first REDS ATPA stage intentionally stops at in-trail distance. Minimum
// CWT separation, monitor/warning/alert status, and ATPA cones are added in a
// later stage; keeping the derived pairing state independent makes that
// extension straightforward.
func (p *STARSPane) updateATPAInTrail(snapshot redsnet.TaisSnapshot) {
	if p == nil {
		return
	}

	// Rebuild rather than incrementally mutate. Typical terminal target counts
	// and ATPA-volume counts are small, and this avoids stale pairings when a
	// track crosses a volume boundary or its scratchpad changes.
	p.atpaTracks = nil
	if !snapshot.Ready || !p.atpaEnabled() || len(snapshot.Targets) == 0 {
		return
	}

	members := make([][]atpaVolumeMember, len(p.config.Facility.ATPAVolumes))

	for ti := range snapshot.Targets {
		target := &snapshot.Targets[ti]
		if !atpaBaseTrackEligible(target) {
			continue
		}

		bestVolume := -1
		bestLateral := stdmath.Inf(1)
		bestThresholdDistance := stdmath.Inf(1)
		bestIneligible := false

		for vi := range p.config.Facility.ATPAVolumes {
			volume := &p.config.Facility.ATPAVolumes[vi]
			if !validConfigPoint(volume.RunwayThreshold) || volume.Length <= 0 {
				continue
			}
			if p.atpaTrackExcludedByTCP(target, volume) {
				continue
			}

			excluded, ineligible := atpaScratchpadDisposition(volume, target.FlightPlan)
			if excluded {
				continue
			}

			inside, lateral := atpaVolumeContains(volume, target)
			if !inside {
				continue
			}

			thresholdDistance := starsRBLNMDistance(
				configPoint{Lat: target.Track.Lat, Lon: target.Track.Lon},
				volume.RunwayThreshold,
			)

			// Scratchpad adaptation should normally make overlapping runway
			// volumes unambiguous. If more than one still qualifies, select the
			// volume whose centerline is closest to the target, with distance
			// to threshold as a deterministic tie-breaker.
			if bestVolume < 0 || lateral < bestLateral-1e-6 ||
				(stdmath.Abs(lateral-bestLateral) <= 1e-6 && thresholdDistance < bestThresholdDistance) {
				bestVolume = vi
				bestLateral = lateral
				bestThresholdDistance = thresholdDistance
				bestIneligible = ineligible
			}
		}

		if bestVolume >= 0 {
			members[bestVolume] = append(members[bestVolume], atpaVolumeMember{
				TargetIndex:       ti,
				ThresholdDistance: bestThresholdDistance,
				Ineligible:        bestIneligible,
			})
		}
	}

	states := make(map[string]atpaTrackState)
	for vi := range members {
		group := members[vi]
		if len(group) == 0 {
			continue
		}
		sort.SliceStable(group, func(i, j int) bool {
			return group[i].ThresholdDistance < group[j].ThresholdDistance
		})

		volume := &p.config.Facility.ATPAVolumes[vi]
		volumeID := strings.TrimSpace(volume.VolumeID)
		if volumeID == "" {
			volumeID = strings.TrimSpace(volume.ID)
		}

		for i := range group {
			target := &snapshot.Targets[group[i].TargetIndex]
			key := targetDisplayStateKey(target)
			if key == "" {
				continue
			}
			state := atpaTrackState{
				VolumeID:   volumeID,
				Ineligible: group[i].Ineligible,
			}

			// vNAS "Ineligible" scratchpads keep the aircraft in the sequence
			// so it may be the lead for the aircraft behind it, but the
			// ineligible aircraft itself does not receive an ATPA cone/distance.
			if i > 0 && !group[i].Ineligible {
				lead := &snapshot.Targets[group[i-1].TargetIndex]
				state.LeadKey = targetDisplayStateKey(lead)
				state.InTrailDistance = float32(starsRBLNMDistance(
					configPoint{Lat: target.Track.Lat, Lon: target.Track.Lon},
					configPoint{Lat: lead.Track.Lat, Lon: lead.Track.Lon},
				))
			}
			states[key] = state
		}
	}

	if len(states) != 0 {
		p.atpaTracks = states
	}
}

func atpaBaseTrackEligible(target *redsnet.TaisTarget) bool {
	if target == nil || target.FlightPlan == nil || !taisTargetHasPosition(target) {
		return false
	}
	if target.FlightPlan.Deleted || target.FlightPlan.Suspended {
		return false
	}

	// TI 6191.409 6.21.19 lists non-IFR status as an ATPA exclusion
	// criterion. A blank flight-rules field is the normal STARS IFR encoding,
	// so only reject values that positively identify VFR/VFR-on-top.
	rules := strings.ToUpper(strings.TrimSpace(target.FlightPlan.FlightRules))
	rules = strings.ReplaceAll(rules, "_", " ")
	rules = strings.ReplaceAll(rules, "-", " ")
	if rules == "V" || rules == "VFR" || strings.Contains(rules, "VFR ON TOP") {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(target.FlightPlan.RawFlightRules), "V") {
		return false
	}

	return true
}

// atpaVolumeContains mirrors VICE's ATPAVolume.Inside/GetRect logic using the
// CRC/vNAS units: floor/ceiling in feet, width in feet, length in NM, and a
// magnetic runway heading. It returns |lateral distance| as a tie-breaker for
// the uncommon case where a target qualifies for overlapping volumes.
func atpaVolumeContains(volume *atpaVolumeConfig, target *redsnet.TaisTarget) (bool, float64) {
	if volume == nil || target == nil {
		return false, 0
	}
	alt := target.Track.ReportedAltitude
	if alt < volume.Floor || alt > volume.Ceiling {
		return false, 0
	}

	vx, vy := float64(target.Track.VX), float64(target.Track.VY)
	if vx == 0 && vy == 0 {
		return false, 0
	}
	trueHeading := atpaNormalizeHeading(stdmath.Atan2(vx, vy) * 180 / stdmath.Pi)
	trackVariation := 0.0
	if variation, err := radar.MagneticVariationAt(target.Track.Lat, target.Track.Lon); err == nil {
		trackVariation = variation
	}
	magneticHeading := atpaNormalizeHeading(trueHeading + trackVariation)
	maxDeviation := float64(volume.MaximumHeadingDeviation)
	if atpaHeadingDifference(magneticHeading, float64(volume.MagneticHeading)) > maxDeviation {
		return false, 0
	}

	threshold := volume.RunwayThreshold
	nmPerLongitude := 60 * radar.LongitudeScaleFactorForLat(threshold.Lat)
	if nmPerLongitude <= 0 {
		return false, 0
	}
	east := longitudeDelta(target.Track.Lon, threshold.Lon) * nmPerLongitude
	north := (target.Track.Lat - threshold.Lat) * 60

	thresholdVariation := 0.0
	if variation, err := radar.MagneticVariationAt(threshold.Lat, threshold.Lon); err == nil {
		thresholdVariation = variation
	}
	// The approach volume extends outward from the threshold, opposite the
	// runway's inbound magnetic heading. REDS magnetic variation is positive
	// west, so true = magnetic - variation.
	outboundTrue := atpaNormalizeHeading(float64(volume.MagneticHeading) + 180 - thresholdVariation)
	rad := outboundTrue * stdmath.Pi / 180
	forwardEast, forwardNorth := stdmath.Sin(rad), stdmath.Cos(rad)
	perpEast, perpNorth := -forwardNorth, forwardEast

	along := east*forwardEast + north*forwardNorth
	lateral := east*perpEast + north*perpNorth
	left := float64(volume.WidthLeft) / feetPerNauticalMile
	right := float64(volume.WidthRight) / feetPerNauticalMile
	if left < 0 {
		left = 0
	}
	if right < 0 {
		right = 0
	}

	return along >= 0 && along <= volume.Length && lateral >= -left && lateral <= right,
		stdmath.Abs(lateral)
}

func atpaNormalizeHeading(h float64) float64 {
	h = stdmath.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	return h
}

func atpaHeadingDifference(a, b float64) float64 {
	d := stdmath.Abs(atpaNormalizeHeading(a) - atpaNormalizeHeading(b))
	if d > 180 {
		d = 360 - d
	}
	return d
}

func atpaScratchpadDisposition(volume *atpaVolumeConfig, fp *redsnet.TaisFlightPlan) (excluded, ineligible bool) {
	if volume == nil || fp == nil {
		return false, false
	}
	for _, entry := range volume.Scratchpads {
		wanted := strings.ToUpper(strings.TrimSpace(entry.Entry))
		if wanted == "" {
			continue
		}
		actual := fp.ScratchPad1
		if atpaRawEnumIs(entry.ScratchPadNumber, "Two", 1) {
			actual = fp.ScratchPad2
		}
		if !strings.EqualFold(strings.TrimSpace(actual), wanted) {
			continue
		}
		if atpaRawEnumIs(entry.Type, "Ineligible", 1) {
			ineligible = true
		} else {
			// Exclude is both ordinal zero and the CRC default.
			excluded = true
		}
	}
	return excluded, ineligible
}

func atpaRawEnumIs(raw json.RawMessage, name string, ordinal int) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ordinal == 0
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		text = strings.TrimSpace(text)
		if strings.EqualFold(text, name) {
			return true
		}
		if n, err := strconv.Atoi(text); err == nil {
			return n == ordinal
		}
		return false
	}
	if n, err := strconv.Atoi(string(raw)); err == nil {
		return n == ordinal
	}
	return false
}

func (p *STARSPane) atpaTrackExcludedByTCP(target *redsnet.TaisTarget, volume *atpaVolumeConfig) bool {
	if p == nil || target == nil || volume == nil || len(volume.ExcludedTCPIDs) == 0 {
		return false
	}
	owner, ok := p.targetOwnerTCP(target)
	if !ok {
		return false
	}
	for _, position := range p.config.Facility.ControlPositions {
		if !strings.EqualFold(strings.TrimSpace(position.TCP), strings.TrimSpace(owner)) {
			continue
		}
		for _, excludedID := range volume.ExcludedTCPIDs {
			if strings.EqualFold(strings.TrimSpace(position.TCPID), strings.TrimSpace(excludedID)) {
				return true
			}
		}
	}
	return false
}
