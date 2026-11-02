package stars

import (
	"strings"
	"sync"
	"time"

	redsnet "github.com/juliusplatzer/reds/net"
)

// A pair is the unit of acknowledgment, never the flight-plan callsign or
// a single aircraft. Identity checks prevent recycled TAIS track keys from
// inheriting an earlier conflict's acknowledgement/inhibit state.
type caAlertState struct {
	AIdentity, BIdentity starsTrackHighlightIdentity
	Since                time.Time
	Acknowledged         bool // local TCW/TDW (unowned) acknowledgement
	Generation           uint64
}

type caIdentityPair struct{ A, B starsTrackHighlightIdentity }

func caIdentitiesMatch(a, b caIdentityPair) bool {
	return trackHighlightIdentityMatches(a.A, b.A) && trackHighlightIdentityMatches(a.B, b.B)
}

type caOverride struct {
	Identity starsTrackHighlightIdentity
	OwnerTCP string
}
type caPairOverride struct {
	Identities    caIdentityPair
	OwnerTCP      string // owner of the track selected for the original CA command
	OwnerTrackKey string
}
type caSharedEvent struct {
	Identities   caIdentityPair
	LastSeen     time.Time
	Acknowledged bool
}
type caSiteRuntimeState struct {
	sync.RWMutex
	tracks      map[string]caOverride
	pairs       map[caPair]caPairOverride
	events      map[caPair]caSharedEvent
	mciSuppress map[string]mciOverride
	mciEvents   map[mciPair]mciSharedEvent
}

var caSites = struct {
	sync.Mutex
	sites map[string]*caSiteRuntimeState
}{sites: make(map[string]*caSiteRuntimeState)}

func (p *STARSPane) caSiteState() *caSiteRuntimeState {
	if p.caSite != nil {
		return p.caSite
	}
	// Facility is intentionally part of the key: CA state must never leak
	// between N90, NCT, etc. Separate windows of one facility share inhibits.
	key := strings.ToUpper(strings.TrimSpace(p.config.Facility.Facility))
	caSites.Lock()
	site := caSites.sites[key]
	if site == nil {
		site = &caSiteRuntimeState{
			tracks:      make(map[string]caOverride),
			pairs:       make(map[caPair]caPairOverride),
			events:      make(map[caPair]caSharedEvent),
			mciSuppress: make(map[string]mciOverride),
			mciEvents:   make(map[mciPair]mciSharedEvent),
		}
		caSites.sites[key] = site
	}
	caSites.Unlock()
	p.caSite = site
	return site
}

func caPairHas(pair caPair, key string) bool { return key != "" && (pair.A == key || pair.B == key) }
func caPairIdentity(pair caPair, byKey map[string]*redsnet.TaisTarget) (caIdentityPair, bool) {
	a, okA := byKey[pair.A]
	b, okB := byKey[pair.B]
	if !okA || !okB {
		return caIdentityPair{}, false
	}
	return caIdentityPair{trackHighlightIdentity(a), trackHighlightIdentity(b)}, true
}
func (p *STARSPane) caTrackOverrideValid(target *redsnet.TaisTarget, o caOverride) bool {
	if target == nil || target.FlightPlan == nil || target.FlightPlan.Deleted ||
		target.FlightPlan.Suspended || target.CoastPhase >= redsnet.TaisCoastPhase2 ||
		taisOwnershipPending(target) || target.Track.Frozen {
		return false
	}
	owner, ok := p.targetOwnerTCP(target)
	return ok && strings.EqualFold(owner, o.OwnerTCP) && trackHighlightIdentityMatches(o.Identity, trackHighlightIdentity(target))
}

// Locked caller: site event/override state is shared between STARS panes.
func (p *STARSPane) caPairOverrideValid(pair caPair, o caPairOverride, byKey map[string]*redsnet.TaisTarget) bool {
	ids, ok := caPairIdentity(pair, byKey)
	if !ok || !caIdentitiesMatch(ids, o.Identities) {
		return false
	}
	a, b := byKey[pair.A], byKey[pair.B]
	if a.FlightPlan == nil || b.FlightPlan == nil || a.FlightPlan.Deleted || b.FlightPlan.Deleted ||
		a.FlightPlan.Suspended || b.FlightPlan.Suspended ||
		a.CoastPhase >= redsnet.TaisCoastPhase2 || b.CoastPhase >= redsnet.TaisCoastPhase2 ||
		taisOwnershipPending(a) || taisOwnershipPending(b) || a.Track.Frozen || b.Track.Frozen {
		return false
	}
	// Flight-plan/track re-association and owner changes invalidate a pair inhibit.
	ao, oka := p.targetOwnerTCP(a)
	bo, okb := p.targetOwnerTCP(b)
	if !oka || !okb {
		return false
	}
	if o.OwnerTrackKey == pair.A {
		return strings.EqualFold(ao, o.OwnerTCP)
	}
	return o.OwnerTrackKey == pair.B && strings.EqualFold(bo, o.OwnerTCP)
}

func (p *STARSPane) updateCAEvents(snapshot redsnet.TaisSnapshot, now time.Time) {
	if p == nil {
		return
	}
	if !snapshot.Ready || !p.caZonesValid {
		clear(p.caAlerts)
		return
	}
	if p.caAlerts == nil {
		p.caAlerts = make(map[caPair]caAlertState)
	}
	p.caGeneration++
	generation := p.caGeneration
	// Index only once for this frame: pair IDs are live track keys.
	byKey := make(map[string]*redsnet.TaisTarget, len(snapshot.Targets))
	for i := range snapshot.Targets {
		t := &snapshot.Targets[i]
		if key := targetDisplayStateKey(t); key != "" {
			byKey[key] = t
		}
	}
	site := p.caSiteState()
	site.Lock()
	for key, o := range site.tracks {
		if !p.caTrackOverrideValid(byKey[key], o) {
			delete(site.tracks, key)
		}
	}
	for pair, o := range site.pairs {
		if !p.caPairOverrideValid(pair, o, byKey) {
			delete(site.pairs, pair)
		}
	}
	for _, pair := range p.caPairs {
		ids, ok := caPairIdentity(pair, byKey)
		if !ok {
			continue
		}
		// A track-wide inhibit or pair inhibit suppresses both pair members.
		if _, inhibited := site.tracks[pair.A]; inhibited {
			delete(site.events, pair)
			continue
		}
		if _, inhibited := site.tracks[pair.B]; inhibited {
			delete(site.events, pair)
			continue
		}
		if _, inhibited := site.pairs[pair]; inhibited {
			delete(site.events, pair)
			continue
		}
		old, exists := p.caAlerts[pair]
		if !exists || !caIdentitiesMatch(caIdentityPair{old.AIdentity, old.BIdentity}, ids) {
			old = caAlertState{AIdentity: ids.A, BIdentity: ids.B, Since: now}
		}
		old.Generation = generation
		p.caAlerts[pair] = old
		global, exists := site.events[pair]
		// A gap longer than 2s is a genuinely new event for shared ACKs.
		if !exists || !caIdentitiesMatch(global.Identities, ids) || now.Sub(global.LastSeen) > 2*time.Second || now.Before(global.LastSeen) {
			global = caSharedEvent{Identities: ids}
		}
		global.LastSeen = now
		site.events[pair] = global
	}
	// Keep shared ACKs only briefly after a pair stops reporting, so a
	// subsequent conflict with the same tracks starts unacknowledged.
	for pair, ev := range site.events {
		if now.Sub(ev.LastSeen) > 2*time.Second || now.Before(ev.LastSeen) {
			delete(site.events, pair)
		}
	}
	site.Unlock()
	for pair, old := range p.caAlerts {
		if old.Generation != generation {
			delete(p.caAlerts, pair)
		}
	}
}

func (p *STARSPane) caTrackAlertPresentation(t *redsnet.TaisTarget) (active, unacknowledged bool) {
	if p == nil || t == nil {
		return false, false
	}
	key := targetDisplayStateKey(t)
	identity := trackHighlightIdentity(t)
	var shared *caSiteRuntimeState
	if len(p.caAlerts) > 0 {
		shared = p.caSiteState()
		shared.RLock()
		defer shared.RUnlock()
	}
	for pair, event := range p.caAlerts {
		if !caPairHas(pair, key) {
			continue
		}
		stored := event.AIdentity
		if key == pair.B {
			stored = event.BIdentity
		}
		if !trackHighlightIdentityMatches(stored, identity) {
			continue
		}
		active = true
		acknowledged := event.Acknowledged
		if !acknowledged && shared != nil {
			if global, ok := shared.events[pair]; ok && caIdentitiesMatch(global.Identities, caIdentityPair{event.AIdentity, event.BIdentity}) {
				acknowledged = global.Acknowledged
			}
		}
		if !acknowledged {
			unacknowledged = true
		}
	}
	return active, unacknowledged
}
func (p *STARSPane) caTrackAlertActive(t *redsnet.TaisTarget) bool {
	active, _ := p.caTrackAlertPresentation(t)
	return active
}
func (p *STARSPane) caTrackInhibited(t *redsnet.TaisTarget) bool {
	if p == nil || t == nil || t.FlightPlan == nil {
		return false
	}
	key := targetDisplayStateKey(t)
	site := p.caSiteState()
	site.RLock()
	defer site.RUnlock()
	if o, ok := site.tracks[key]; ok && p.caTrackOverrideValid(t, o) {
		return true
	}
	for pair, o := range site.pairs {
		if !caPairHas(pair, key) {
			continue
		}
		stored := o.Identities.A
		if pair.B == key {
			stored = o.Identities.B
		}
		if trackHighlightIdentityMatches(stored, trackHighlightIdentity(t)) {
			return true
		}
	}
	return false
}

// §7.3: acknowledge all active CA pairs involving the clicked aircraft.
// Other pairs involving the counterpart remain unacknowledged and flash.
func (p *STARSPane) acknowledgeCABySlew(t *redsnet.TaisTarget) bool {
	if p == nil || t == nil || taisOwnershipPending(t) {
		return false
	}
	key := targetDisplayStateKey(t)
	identity := trackHighlightIdentity(t)
	site := p.caSiteState()
	site.Lock()
	defer site.Unlock()
	changed := false
	for pair, ev := range p.caAlerts {
		if !caPairHas(pair, key) {
			continue
		}
		stored := ev.AIdentity
		if key == pair.B {
			stored = ev.BIdentity
		}
		if !trackHighlightIdentityMatches(stored, identity) {
			continue
		}
		global := site.events[pair]
		if ev.Acknowledged || (caIdentitiesMatch(global.Identities, caIdentityPair{ev.AIdentity, ev.BIdentity}) && global.Acknowledged) {
			continue
		}
		changed = true
		if p.targetOwnedByCurrentTCP(t) {
			if caIdentitiesMatch(global.Identities, caIdentityPair{ev.AIdentity, ev.BIdentity}) {
				global.Acknowledged = true
				site.events[pair] = global
			}
		} else {
			ev.Acknowledged = true
			p.caAlerts[pair] = ev
		}
	}
	return changed
}

func (p *STARSPane) caCommandTargetValid(t *redsnet.TaisTarget) bool {
	return t != nil && t.FlightPlan != nil && !t.FlightPlan.Deleted && !t.FlightPlan.Suspended &&
		t.CoastPhase < redsnet.TaisCoastPhase2 && !t.Track.Frozen &&
		!taisOwnershipPending(t) && p.targetOwnedByCurrentTCP(t)
}

// §7.9: CA+K+slew, system-wide track inhibition. Track-wide operation
// discards overlapping pairwise inhibits. There is no effect on LA/MSAW.
func (p *STARSPane) toggleCATrackInhibit(t *redsnet.TaisTarget) error {
	if p == nil || !p.caCommandTargetValid(t) {
		return ErrSTARSIllegalTrack
	}
	site := p.caSiteState()
	key := targetDisplayStateKey(t)
	owner, _ := p.targetOwnerTCP(t)
	site.Lock()
	defer site.Unlock()
	if old, ok := site.tracks[key]; ok && p.caTrackOverrideValid(t, old) {
		delete(site.tracks, key)
	} else {
		site.tracks[key] = caOverride{Identity: trackHighlightIdentity(t), OwnerTCP: owner}
		delete(site.mciSuppress, key)
	}
	for pair := range site.pairs {
		if caPairHas(pair, key) {
			delete(site.pairs, pair)
		}
	}
	return nil
}

// §7.10: CA+slew toggles the *unique* current/inhibited conflict. A track
// with two partners is ambiguous; require the operator to select a track
// with exactly one partner. Reenable uses the retained pair inhibit identity.
func (p *STARSPane) toggleCAPairInhibit(t *redsnet.TaisTarget) error {
	if p == nil || !p.caCommandTargetValid(t) {
		return ErrSTARSIllegalTrack
	}
	key := targetDisplayStateKey(t)
	site := p.caSiteState()
	site.Lock()
	defer site.Unlock()
	if _, ok := site.tracks[key]; ok {
		return ErrSTARSIllegalTrack
	}
	var matches []caPair
	for _, pair := range p.caPairs {
		if caPairHas(pair, key) {
			matches = append(matches, pair)
		}
	}
	if len(matches) != 1 {
		return ErrSTARSIllegalTrack
	}
	pair := matches[0]
	if _, ok := site.tracks[pair.A]; ok {
		return ErrSTARSIllegalTrack
	}
	if _, ok := site.tracks[pair.B]; ok {
		return ErrSTARSIllegalTrack
	}
	// Even when inhibited, the raw CA detector still lists this pair.
	if prev, ok := site.pairs[pair]; ok {
		stored := prev.Identities.A
		if key == pair.B {
			stored = prev.Identities.B
		}
		if trackHighlightIdentityMatches(stored, trackHighlightIdentity(t)) {
			delete(site.pairs, pair)
			return nil
		}
	}
	var ids caIdentityPair
	found := false
	for k, ev := range p.caAlerts {
		if k == pair {
			ids = caIdentityPair{ev.AIdentity, ev.BIdentity}
			found = true
			break
		}
	}
	if !found {
		return ErrSTARSIllegalTrack
	}
	owner, _ := p.targetOwnerTCP(t)
	// §7.17: CA and MCI suppression are mutually exclusive for an
	// associated track. Pairwise CA inhibition clears any earlier MCI code.
	delete(site.mciSuppress, pair.A)
	delete(site.mciSuppress, pair.B)
	site.pairs[pair] = caPairOverride{Identities: ids, OwnerTCP: owner, OwnerTrackKey: key}
	return nil
}

// caLineZeroText retains each alert's character cells while an unacknowledged
// abbreviation is in its off phase. This lets LA and CA flash independently
// without another alert shifting sideways or being overwritten.
func caLineZeroText(hasLA, laAcknowledged, hasCA, caUnacknowledged bool,
	spc string, spcVisible, flashOn bool) (red, secondary string) {
	if hasLA {
		if laAcknowledged || flashOn {
			red = "LA"
		} else {
			red = "  "
		}
	}
	if hasCA {
		if red != "" {
			red += "/"
		}
		if !caUnacknowledged || flashOn {
			red += "CA"
		} else {
			red += "  "
		}
	}
	if spc != "" {
		if red != "" {
			secondary = "/"
		}
		if spcVisible {
			secondary += spc
		} else {
			secondary += strings.Repeat(" ", len(spc))
		}
	}
	return red, secondary
}
