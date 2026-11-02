package stars

import (
	"math"
	"sort"
	"strings"
	"time"

	redsnet "github.com/juliusplatzer/reds/net"
)

// VICE stars/track.go mciConflict(): 1.5 NM / (500 - 5) ft. The
// 5-ft tolerance is intentional; it is not a predictive CA threshold.
const (
	mciLateralMinimumNM  = 1.5
	mciVerticalMinimumFT = 495
	mciDefaultBeaconCode = "0477" // TI 6191.409 §7.17
)

// MCI pairs are directional: the first track is associated and the second
// unassociated. CA pairs, in contrast, are unordered associated track pairs.
type mciPair struct{ Associated, Intruder string }

type mciAlertState struct {
	AssociatedIdentity, IntruderIdentity starsTrackHighlightIdentity
	IntruderCode                         string
	Since                                time.Time
	Acknowledged                         bool // TCW-local (non-owned) acknowledgement
	Generation                           uint64
}

type mciSharedEvent struct {
	AssociatedIdentity, IntruderIdentity starsTrackHighlightIdentity
	IntruderCode                         string
	LastSeen                             time.Time
	Acknowledged                         bool
}

type mciOverride struct {
	Identity starsTrackHighlightIdentity
	OwnerTCP string
	Code     string // one four-digit beacon code per associated track, as in VICE
}

// mciBeaconCode validates Mode-3/A as four octal digits. In particular,
// unassigned/missing codes cannot accidentally match a suppression.
func mciBeaconCode(raw string) string {
	code := strings.TrimSpace(raw)
	if len(code) != 4 {
		return ""
	}
	for _, ch := range code {
		if ch < '0' || ch > '7' {
			return ""
		}
	}
	return code
}

// parseMCISlewCode accepts CA M [dddd] followed by a slew. A missing code
// selects the §7.17 default 0477; other CA commands are handled separately.
func parseMCISlewCode(input string) (string, bool) {
	fields := strings.Fields(strings.ToUpper(strings.TrimSpace(input)))
	if len(fields) == 1 && fields[0] == "M" {
		return mciDefaultBeaconCode, true
	}
	if len(fields) == 2 && fields[0] == "M" {
		code := mciBeaconCode(fields[1])
		return code, code != ""
	}
	return "", false
}

func mciUsableTrack(t *redsnet.TaisTarget, now time.Time) bool {
	if t == nil || t.CoastPhase >= redsnet.TaisCoastPhase2 ||
		!taisTargetHasPosition(t) || !t.Track.UsableReportedAltitude() ||
		targetDisplayStateKey(t) == "" || t.Track.MRTTime.IsZero() ||
		t.Track.MRTTime.After(now.Add(2*time.Second)) || now.Sub(t.Track.MRTTime) > caMaximumTrackAge {
		return false
	}
	// Do not use a suspended/deleted plan as an associated aircraft; VICE
	// excludes tracks that are not currently participating in its radar feed.
	return t.FlightPlan == nil || (!t.FlightPlan.Deleted && !t.FlightPlan.Suspended)
}

// mciFindPairs follows VICE's mciConflict() except for upstream fields TAIS
// does not supply (explicit transponder mode and Mode-C display inhibition).
// Screening latitude-sorted tracks avoids a whole-facility N^2 allocation.
// Neither the shared ATPA-volume exclusion nor the virtual-controller rule
// belongs to VICE's MCI predicate (unlike associated/associated CA).
func mciFindPairs(snapshot redsnet.TaisSnapshot, now time.Time, inhibit []msawRegion,
	suppressed func(associated *redsnet.TaisTarget, beaconCode string) bool) []mciPair {
	if !snapshot.Ready {
		return nil
	}
	tracks := make([]caTrack, 0, len(snapshot.Targets))
	for i := range snapshot.Targets {
		t := &snapshot.Targets[i]
		if !mciUsableTrack(t, now) {
			continue
		}
		pt := msawPoint{lat: t.Track.Lat, lon: t.Track.Lon}
		if !msawValidPoint(pt) {
			continue
		}
		if t.FlightPlan == nil && mciBeaconCode(t.Track.ReportedBeaconCode) == "" {
			continue // cannot reliably identify the intruder's beacon code
		}
		blocked := false
		for _, zone := range inhibit {
			if zone.contains(pt, t.Track.ReportedAltitude) {
				blocked = true
				break
			}
		}
		if !blocked {
			tracks = append(tracks, caTrack{target: t, key: targetDisplayStateKey(t), point: pt})
		}
	}
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].point.lat < tracks[j].point.lat })
	pairs := make([]mciPair, 0)
	for i := range tracks {
		for j := i + 1; j < len(tracks); j++ {
			if (tracks[j].point.lat-tracks[i].point.lat)*caMinLatitudeNM > mciLateralMinimumNM {
				break
			}
			a, b := tracks[i], tracks[j]
			if a.key == b.key || (a.target.FlightPlan == nil) == (b.target.FlightPlan == nil) {
				continue // strictly associated/unassociated
			}
			if a.target.FlightPlan == nil {
				a, b = b, a
			}
			if math.Abs(float64(a.target.Track.ReportedAltitude-b.target.Track.ReportedAltitude)) > mciVerticalMinimumFT ||
				msawDistanceNM(a.point, b.point) > mciLateralMinimumNM ||
				caDiverging(a.target, b.target) {
				continue
			}
			code := mciBeaconCode(b.target.Track.ReportedBeaconCode)
			if suppressed != nil && suppressed(a.target, code) {
				continue
			}
			pairs = append(pairs, mciPair{Associated: a.key, Intruder: b.key})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Associated != pairs[j].Associated {
			return pairs[i].Associated < pairs[j].Associated
		}
		return pairs[i].Intruder < pairs[j].Intruder
	})
	return pairs
}

func mciOverrideValid(p *STARSPane, t *redsnet.TaisTarget, o mciOverride) bool {
	return o.Code != "" && p.caTrackOverrideValid(t, caOverride{Identity: o.Identity, OwnerTCP: o.OwnerTCP})
}

func (p *STARSPane) updateMCIDetection(snapshot redsnet.TaisSnapshot, now time.Time) {
	p.mciPairs = nil
	if !p.caZonesValid {
		return // VICE uses the same CA inhibit adaptation for MCI
	}
	site := p.caSiteState()
	// Do not hold the site mutex for an N-track geometric scan. Snapshot
	// the small inhibition set, then compare each candidate by identity.
	site.RLock()
	mciInhibits := make(map[string]mciOverride, len(site.mciSuppress))
	for key, o := range site.mciSuppress {
		mciInhibits[key] = o
	}
	caInhibits := make(map[string]caOverride, len(site.tracks))
	for key, o := range site.tracks {
		caInhibits[key] = o
	}
	site.RUnlock()
	p.mciPairs = mciFindPairs(snapshot, now, p.caZones, func(associated *redsnet.TaisTarget, code string) bool {
		key := targetDisplayStateKey(associated)
		if ca, found := caInhibits[key]; found && p.caTrackOverrideValid(associated, ca) {
			return true // VICE: DisableCA also disables MCI
		}
		if mci, found := mciInhibits[key]; found && mciOverrideValid(p, associated, mci) && mci.Code == code {
			return true
		}
		return false
	})
}

func mciEventMatches(event mciSharedEvent, state mciAlertState) bool {
	return event.IntruderCode == state.IntruderCode &&
		trackHighlightIdentityMatches(event.AssociatedIdentity, state.AssociatedIdentity) &&
		trackHighlightIdentityMatches(event.IntruderIdentity, state.IntruderIdentity)
}

// updateMCIEvents is separate from CA event management: a reported intruder
// may change its squawk, association, or track identity without changing its
// TAIS key. Such changes must start a fresh, unacknowledged event.
// Retain MCI inhibition through ordinary track coasting, as §7.17 requires.
// Other invalidation cases (handoff/owner change, suspension, reassociation,
// repositioning) still clear the suppressing track's code.
func mciOverrideRetainable(p *STARSPane, t *redsnet.TaisTarget, o mciOverride) bool {
	if t == nil || t.FlightPlan == nil || t.FlightPlan.Deleted || t.FlightPlan.Suspended ||
		taisOwnershipPending(t) || (t.Track.Frozen && t.CoastPhase == redsnet.TaisCoastPhaseNone) {
		return false
	}
	owner, ok := p.targetOwnerTCP(t)
	return ok && strings.EqualFold(owner, o.OwnerTCP) &&
		trackHighlightIdentityMatches(o.Identity, trackHighlightIdentity(t))
}

func (p *STARSPane) updateMCIEvents(snapshot, allTargets redsnet.TaisSnapshot, now time.Time) {
	if !snapshot.Ready || !p.caZonesValid {
		clear(p.mciAlerts)
		return
	}
	if p.mciAlerts == nil {
		p.mciAlerts = make(map[mciPair]mciAlertState)
	}
	p.mciGeneration++
	generation := p.mciGeneration
	byKey := make(map[string]*redsnet.TaisTarget, len(snapshot.Targets))
	for i := range snapshot.Targets {
		t := &snapshot.Targets[i]
		if key := targetDisplayStateKey(t); key != "" {
			byKey[key] = t
		}
	}
	site := p.caSiteState()
	site.Lock()
	// The all-target snapshot retains Phase-2 coasted tracks, which are
	// intentionally absent from the radar-facing MCI detection snapshot.
	allByKey := make(map[string]*redsnet.TaisTarget, len(allTargets.Targets))
	for i := range allTargets.Targets {
		t := &allTargets.Targets[i]
		if key := targetDisplayStateKey(t); key != "" {
			allByKey[key] = t
		}
	}
	for key, inhibit := range site.mciSuppress {
		if !mciOverrideRetainable(p, allByKey[key], inhibit) {
			delete(site.mciSuppress, key)
		}
	}
	for _, pair := range p.mciPairs {
		assoc, intruder := byKey[pair.Associated], byKey[pair.Intruder]
		if assoc == nil || intruder == nil || assoc.FlightPlan == nil || intruder.FlightPlan != nil {
			continue
		}
		identityA, identityB := trackHighlightIdentity(assoc), trackHighlightIdentity(intruder)
		code := mciBeaconCode(intruder.Track.ReportedBeaconCode)
		previous, ok := p.mciAlerts[pair]
		if !ok || previous.IntruderCode != code ||
			!trackHighlightIdentityMatches(previous.AssociatedIdentity, identityA) ||
			!trackHighlightIdentityMatches(previous.IntruderIdentity, identityB) {
			previous = mciAlertState{AssociatedIdentity: identityA, IntruderIdentity: identityB, IntruderCode: code, Since: now}
		}
		previous.Generation = generation
		p.mciAlerts[pair] = previous
		global, found := site.mciEvents[pair]
		if !found || !mciEventMatches(global, previous) || now.Sub(global.LastSeen) > 2*time.Second || now.Before(global.LastSeen) {
			global = mciSharedEvent{AssociatedIdentity: identityA, IntruderIdentity: identityB, IntruderCode: code}
		}
		global.LastSeen = now
		site.mciEvents[pair] = global
	}
	for pair, event := range site.mciEvents {
		if now.Sub(event.LastSeen) > 2*time.Second || now.Before(event.LastSeen) {
			delete(site.mciEvents, pair)
		}
	}
	site.Unlock()
	for pair, event := range p.mciAlerts {
		if event.Generation != generation {
			delete(p.mciAlerts, pair)
		}
	}
}

func mciPairHas(pair mciPair, key string) bool {
	return key != "" && (pair.Associated == key || pair.Intruder == key)
}

func (p *STARSPane) mciTrackAlertPresentation(target *redsnet.TaisTarget) (active, unacknowledged bool) {
	if p == nil || target == nil {
		return false, false
	}
	key := targetDisplayStateKey(target)
	identity := trackHighlightIdentity(target)
	if len(p.mciAlerts) == 0 {
		return false, false
	}
	site := p.caSiteState()
	site.RLock()
	defer site.RUnlock()
	for pair, event := range p.mciAlerts {
		if !mciPairHas(pair, key) {
			continue
		}
		stored := event.AssociatedIdentity
		if pair.Intruder == key {
			stored = event.IntruderIdentity
			if mciBeaconCode(target.Track.ReportedBeaconCode) != event.IntruderCode {
				continue
			}
		}
		if !trackHighlightIdentityMatches(stored, identity) {
			continue
		}
		active = true
		ack := event.Acknowledged
		if global, ok := site.mciEvents[pair]; ok && mciEventMatches(global, event) && global.Acknowledged {
			ack = true
		}
		if !ack {
			unacknowledged = true
		}
	}
	return active, unacknowledged
}

func (p *STARSPane) acknowledgeMCIBySlew(target *redsnet.TaisTarget) bool {
	if p == nil || target == nil || taisOwnershipPending(target) {
		return false
	}
	key := targetDisplayStateKey(target)
	identity := trackHighlightIdentity(target)
	site := p.caSiteState()
	site.Lock()
	defer site.Unlock()
	changed := false
	for pair, event := range p.mciAlerts {
		if !mciPairHas(pair, key) {
			continue
		}
		stored := event.AssociatedIdentity
		if key == pair.Intruder {
			stored = event.IntruderIdentity
			if mciBeaconCode(target.Track.ReportedBeaconCode) != event.IntruderCode {
				continue
			}
		}
		if !trackHighlightIdentityMatches(stored, identity) {
			continue
		}
		global := site.mciEvents[pair]
		if event.Acknowledged || (mciEventMatches(global, event) && global.Acknowledged) {
			continue
		}
		changed = true
		if target.FlightPlan != nil && p.targetOwnedByCurrentTCP(target) && mciEventMatches(global, event) {
			global.Acknowledged = true
			site.mciEvents[pair] = global
		} else {
			event.Acknowledged = true
			p.mciAlerts[pair] = event
		}
	}
	return changed
}

// mciAlertInhibitedFor checks both an exact beacon-code inhibit and a
// track-wide CA inhibit. Pairwise CA inhibition does not suppress MCI.
func (p *STARSPane) mciAlertInhibitedFor(t *redsnet.TaisTarget, code string) bool {
	if p == nil || t == nil || t.FlightPlan == nil {
		return false
	}
	site := p.caSiteState()
	site.RLock()
	defer site.RUnlock()
	key := targetDisplayStateKey(t)
	if o, ok := site.tracks[key]; ok && p.caTrackOverrideValid(t, o) {
		return true
	}
	o, exists := site.mciSuppress[key]
	return exists && o.Code == code && mciOverrideValid(p, t, o)
}

func (p *STARSPane) mciTrackInhibited(t *redsnet.TaisTarget) bool {
	if p == nil || t == nil || t.FlightPlan == nil {
		return false
	}
	site := p.caSiteState()
	site.RLock()
	defer site.RUnlock()
	o, found := site.mciSuppress[targetDisplayStateKey(t)]
	return found && mciOverrideValid(p, t, o)
}

// TI 6191.409 §7.17: CA M [beacon code] + slew. Exactly one code is
// suppressed for an owned associated track. Repeating it removes the inhibit.
// Setting it replaces any earlier CA track/pair suppression for that track.
func (p *STARSPane) toggleMCIBeaconInhibit(t *redsnet.TaisTarget, code string) error {
	if p == nil || !p.caCommandTargetValid(t) || mciBeaconCode(code) == "" {
		return ErrSTARSIllegalTrack
	}
	key := targetDisplayStateKey(t)
	owner, _ := p.targetOwnerTCP(t)
	site := p.caSiteState()
	site.Lock()
	defer site.Unlock()
	if old, found := site.mciSuppress[key]; found && mciOverrideValid(p, t, old) && old.Code == code {
		delete(site.mciSuppress, key)
		return nil
	}
	site.mciSuppress[key] = mciOverride{Identity: trackHighlightIdentity(t), OwnerTCP: owner, Code: code}
	delete(site.tracks, key)
	for pair := range site.pairs {
		if caPairHas(pair, key) {
			delete(site.pairs, pair)
		}
	}
	return nil
}

func (p *STARSPane) mciTrackAlertActive(t *redsnet.TaisTarget) bool {
	active, _ := p.mciTrackAlertPresentation(t)
	return active
}
