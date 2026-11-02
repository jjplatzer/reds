package stars

import (
	"time"

	redsnet "github.com/juliusplatzer/reds/net"
)

// §2.16.3: nominal CA/MCI alarm duration is ten seconds, ending on
// acknowledgment or when the active conflict clears. VICE uses five seconds;
// REDS follows the operator manual's nominal duration as with LA.
const (
	caAudioDuration  = 10 * time.Second
	mciAudioDuration = 10 * time.Second
	caAudioResource  = "resources/audio/stars/ConflictAlert.wav"
	mciAudioResource = "resources/audio/stars/Mci.wav"
)

type starsAlarmKind uint8

const (
	starsAlarmNone starsAlarmKind = iota
	starsAlarmMCI
	starsAlarmCA
	starsAlarmLA
)

// caAudioWanted considers the effective pair acknowledgment, not just the
// detector's proximity pairs. Only conflicts involving a locally controlled
// track sound on this pane; a separate TCP's conflicts do not sound here.
func (p *STARSPane) caAudioWanted(snapshot redsnet.TaisSnapshot, now time.Time) bool {
	if p == nil || !snapshot.Ready || !p.caZonesValid || len(p.caAlerts) == 0 {
		return false
	}
	byKey := make(map[string]*redsnet.TaisTarget, len(snapshot.Targets))
	for i := range snapshot.Targets {
		t := &snapshot.Targets[i]
		if key := targetDisplayStateKey(t); key != "" {
			byKey[key] = t
		}
	}
	site := p.caSiteState()
	site.RLock()
	defer site.RUnlock()
	for pair, event := range p.caAlerts {
		if now.Before(event.Since) || !now.Before(event.Since.Add(caAudioDuration)) || event.Acknowledged {
			continue
		}
		a, b := byKey[pair.A], byKey[pair.B]
		if a == nil || b == nil || (!p.targetOwnedByCurrentTCP(a) && !p.targetOwnedByCurrentTCP(b)) {
			continue
		}
		if _, blocked := site.tracks[pair.A]; blocked {
			continue
		}
		if _, blocked := site.tracks[pair.B]; blocked {
			continue
		}
		if _, blocked := site.pairs[pair]; blocked {
			continue
		}
		ids, ok := caPairIdentity(pair, byKey)
		if !ok || !caIdentitiesMatch(ids, caIdentityPair{event.AIdentity, event.BIdentity}) {
			continue
		}
		if global, ok := site.events[pair]; ok && caIdentitiesMatch(global.Identities, ids) && global.Acknowledged {
			continue
		}
		return true
	}
	return false
}

// MCI audio belongs to the associated aircraft's controlling TCP; the
// unassociated intruder by itself never starts audio on unrelated displays.
func (p *STARSPane) mciAudioWanted(snapshot redsnet.TaisSnapshot, now time.Time) bool {
	if p == nil || !snapshot.Ready || !p.caZonesValid || len(p.mciAlerts) == 0 {
		return false
	}
	byKey := make(map[string]*redsnet.TaisTarget, len(snapshot.Targets))
	for i := range snapshot.Targets {
		t := &snapshot.Targets[i]
		if key := targetDisplayStateKey(t); key != "" {
			byKey[key] = t
		}
	}
	site := p.caSiteState()
	site.RLock()
	defer site.RUnlock()
	for pair, event := range p.mciAlerts {
		if now.Before(event.Since) || !now.Before(event.Since.Add(mciAudioDuration)) || event.Acknowledged {
			continue
		}
		assoc, intruder := byKey[pair.Associated], byKey[pair.Intruder]
		if assoc == nil || intruder == nil || !p.targetOwnedByCurrentTCP(assoc) || intruder.FlightPlan != nil {
			continue
		}
		if !trackHighlightIdentityMatches(event.AssociatedIdentity, trackHighlightIdentity(assoc)) ||
			!trackHighlightIdentityMatches(event.IntruderIdentity, trackHighlightIdentity(intruder)) ||
			event.IntruderCode != mciBeaconCode(intruder.Track.ReportedBeaconCode) {
			continue
		}
		if override, blocked := site.mciSuppress[pair.Associated]; blocked &&
			override.Code == event.IntruderCode && mciOverrideValid(p, assoc, override) {
			continue
		}
		if override, blocked := site.tracks[pair.Associated]; blocked &&
			p.caTrackOverrideValid(assoc, override) {
			continue
		}
		if global, ok := site.mciEvents[pair]; ok && mciEventMatches(global, event) && global.Acknowledged {
			continue
		}
		return true
	}
	return false
}

// Avoid three overlapping alert samples on the same TCW/TDW. When alerts
// coexist, LA has priority over CA, which has priority over MCI. Neither
// rendering nor acknowledgment is affected by this audio-only arbitration.
func (p *STARSPane) desiredSTARSAlarm(snapshot redsnet.TaisSnapshot, now time.Time) starsAlarmKind {
	if p == nil {
		return starsAlarmNone
	}
	if p.msawAudioWanted(snapshot, now) {
		return starsAlarmLA
	}
	if p.caAudioWanted(snapshot, now) {
		return starsAlarmCA
	}
	if p.mciAudioWanted(snapshot, now) {
		return starsAlarmMCI
	}
	return starsAlarmNone
}

func (p *STARSPane) updateSTARSAlertAudio(snapshot redsnet.TaisSnapshot, now time.Time) {
	if p == nil {
		return
	}
	selected := p.desiredSTARSAlarm(snapshot, now)
	// Cancel non-selected channels before starting the selected tone.
	if selected != starsAlarmLA {
		p.msawAudio.Stop()
	}
	if selected != starsAlarmCA {
		p.caAudio.Stop()
	}
	if selected != starsAlarmMCI {
		p.mciAudio.Stop()
	}
	switch selected {
	case starsAlarmLA:
		p.msawAudio.SetPlaying(true)
	case starsAlarmCA:
		p.caAudio.SetPlaying(true)
	case starsAlarmMCI:
		p.mciAudio.SetPlaying(true)
	}
}
