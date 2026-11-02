package stars

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	redsnet "github.com/juliusplatzer/reds/net"
)

// VICE stars/track.go updateCAAircraft(): associated/associated CA uses a
// 3-NM lateral minimum and 1000 ft vertical separation with 5-ft tolerance.
// These are VICE's *current separation* settings, not the FAA system's
// adapted/predictive Conflict Alert algorithm.
const (
	caLateralMinimumNM  = 3.0
	caVerticalMinimumFT = 995
	caMaximumTrackAge   = 30 * time.Second // TAIS-only validity guard
	caHeadingDifference = 15.0
	caMinLatitudeNM     = 59.9 // conservative screening bound, no false negatives
)

// caPair identifies two associated live TAIS tracks. Identity is by live
// track key (not ACID, which can repeat), and keys are always ordered.
// CA/MCI acknowledgments and alert lifetimes are intentionally NOT modeled yet.
type caPair struct{ A, B string }

type caTrack struct {
	target *redsnet.TaisTarget
	key    string
	point  msawPoint
}

// caDecodeInhibitZones reuses the existing VICE-format geometry decoder.
// A missing adaptation is NOT equivalent to an explicitly empty filter: the
// detector stays disabled rather than ignoring an unknown set of inhibit zones.
func caDecodeInhibitZones(raw []json.RawMessage, airportCenters map[string]configPoint) ([]msawRegion, error) {
	if raw == nil {
		return nil, errors.New("missing inhibitZones.ca adaptation")
	}
	zones := make([]msawInhibitZone, 0, len(raw))
	for i, encoded := range raw {
		var zone msawInhibitZone
		if err := json.Unmarshal(encoded, &zone); err != nil {
			return nil, fmt.Errorf("CA inhibit %d: %w", i, err)
		}
		zones = append(zones, zone)
	}
	return msawParseInhibitZones(zones, airportCenters)
}

// caUsableTrack uses the closest TAIS equivalents to VICE IsAssociated() and
// TransponderModeAltitude. An associated, unexpired flight plan and an
// explicitly usable reported altitude are required. TAIS currently lacks a
// distinct Mode-C transponder-mode bit, hence this proxy is not exact.
func caUsableTrack(t *redsnet.TaisTarget, now time.Time) bool {
	if t == nil || t.FlightPlan == nil || t.FlightPlan.Deleted || t.FlightPlan.Suspended ||
		t.CoastPhase >= redsnet.TaisCoastPhase2 || !taisTargetHasPosition(t) ||
		!t.Track.UsableReportedAltitude() || targetDisplayStateKey(t) == "" {
		return false
	}
	return !t.Track.MRTTime.IsZero() && !t.Track.MRTTime.After(now.Add(2*time.Second)) &&
		now.Sub(t.Track.MRTTime) <= caMaximumTrackAge
}

// caSameATPAVolume reproduces VICE's special approach exclusion: when both
// aircraft have the same adapted ATPA volume, CA is left to ATPA. Mere
// establishment on a CIFP final does NOT cause a general CA inhibition.
func caSameATPAVolume(p *STARSPane, a, b *redsnet.TaisTarget) bool {
	if p == nil || a == nil || b == nil {
		return false
	}
	av, okA := p.atpaTrackState(targetDisplayStateKey(a))
	bv, okB := p.atpaTrackState(targetDisplayStateKey(b))
	return okA && okB && av.VolumeID != "" && av.VolumeID == bv.VolumeID
}

// caDiverging mirrors the precise test in VICE's STARSPane.diverging():
// extend the two heading-vector LINES to their intersection; if both
// intersections are ahead, do not exclude. Otherwise exclude if headings
// differ by >=15 degrees. This is deliberately not a CPA approximation.
// TAIS velocity direction (VX east, VY north) substitutes for VICE's
// TrackState smoothed heading vector. Missing/parallel vectors do not exclude.
func caDiverging(a, b *redsnet.TaisTarget) bool {
	if a == nil || b == nil {
		return false
	}
	ax, ay := float64(a.Track.VX), float64(a.Track.VY)
	bx, by := float64(b.Track.VX), float64(b.Track.VY)
	if ax*ax+ay*ay < 1 || bx*bx+by*by < 1 {
		return false
	}
	ah, bh := math.Atan2(ax, ay)*180/math.Pi, math.Atan2(bx, by)*180/math.Pi
	difference := math.Abs(math.Remainder(ah-bh, 360))
	if difference < caHeadingDifference {
		return false
	}

	// Project both locations into the same local tangent plane. Only the
	// signs of the intersection parameters matter, not the vectors' units.
	latitude := (a.Track.Lat + b.Track.Lat) * math.Pi / 360
	cosLat := math.Cos(latitude)
	if math.Abs(cosLat) < 1e-6 {
		return false
	}
	dx := (b.Track.Lon - a.Track.Lon) * cosLat * 60
	dy := (b.Track.Lat - a.Track.Lat) * 60
	cross := ax*by - ay*bx
	if math.Abs(cross) <= 1e-10*math.Sqrt((ax*ax+ay*ay)*(bx*bx+by*by)) {
		return false // parallel/antiparallel: VICE LineLineIntersect fails
	}
	ta := (dx*by - dy*bx) / cross
	tb := (dx*ay - dy*ax) / cross
	return !(ta > 0 && tb > 0)
}

// caFindPairs computes CURRENT associated-associated CA only. It does not
// create alert events, datablock indicators, acknowledge state, list entries,
// audio or MCI. The latitude sweep is equivalent to checking all unordered
// pairs but avoids quadratic work for 1000+ simultaneous TAIS targets.
func caFindPairs(snapshot redsnet.TaisSnapshot, now time.Time, inhibit []msawRegion,
	inSameATPA func(a, b *redsnet.TaisTarget) bool) []caPair {
	if !snapshot.Ready {
		return nil
	}
	eligible := make([]caTrack, 0, len(snapshot.Targets))
	for i := range snapshot.Targets {
		t := &snapshot.Targets[i]
		if !caUsableTrack(t, now) {
			continue
		}
		pt := msawPoint{lat: t.Track.Lat, lon: t.Track.Lon}
		if !msawValidPoint(pt) {
			continue
		}
		inhibited := false
		for _, zone := range inhibit {
			if zone.contains(pt, t.Track.ReportedAltitude) {
				inhibited = true
				break
			}
		}
		if !inhibited {
			eligible = append(eligible, caTrack{target: t, key: targetDisplayStateKey(t), point: pt})
		}
	}
	// Sorting an index-like, small struct avoids copying flight plans or
	// histories; no extra N-by-N matrix is allocated.
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].point.lat < eligible[j].point.lat })
	pairs := make([]caPair, 0)
	for i := range eligible {
		a := &eligible[i]
		for j := i + 1; j < len(eligible); j++ {
			b := &eligible[j]
			if (b.point.lat-a.point.lat)*caMinLatitudeNM > caLateralMinimumNM {
				break
			}
			if a.key == b.key || math.Abs(float64(a.target.Track.ReportedAltitude-b.target.Track.ReportedAltitude)) > caVerticalMinimumFT {
				continue
			}
			if inSameATPA != nil && inSameATPA(a.target, b.target) {
				continue
			}
			if msawDistanceNM(a.point, b.point) > caLateralMinimumNM || caDiverging(a.target, b.target) {
				continue
			}
			key0, key1 := a.key, b.key
			if strings.Compare(key0, key1) > 0 {
				key0, key1 = key1, key0
			}
			pairs = append(pairs, caPair{key0, key1})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].A == pairs[j].A {
			return pairs[i].B < pairs[j].B
		}
		return pairs[i].A < pairs[j].A
	})
	return pairs
}

// updateCADetection runs after ATPA has computed the current volume members.
// Its output is private diagnostic state; no operator-facing CA presentation
// or commands are activated by this patch.
func (p *STARSPane) updateCADetection(snapshot redsnet.TaisSnapshot, now time.Time) {
	if p == nil {
		return
	}
	p.caPairs = nil
	if !p.caZonesLoaded {
		p.caZonesLoaded = true
		raw := p.config.Facility.InhibitZones.CA
		var centers map[string]configPoint
		// Resolve named airport centers only if an adapted circle needs one.
		for _, item := range raw {
			var shape struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(item, &shape) == nil && strings.EqualFold(shape.Type, "circle") {
				airports, err := loadSTARSAirportDatabase()
				if err != nil {
					p.logger.Warn("STARS CA detection disabled: airport centers unavailable", "error", err)
					return
				}
				centers = airports.positionByID
				break
			}
		}
		zones, err := caDecodeInhibitZones(raw, centers)
		if err != nil {
			p.logger.Warn("STARS CA detection disabled: invalid CA inhibit adaptation", "error", err)
			return
		}
		p.caZones = zones
		p.caZonesValid = true
	}
	if !p.caZonesValid {
		return
	}
	p.caPairs = caFindPairs(snapshot, now, p.caZones, func(a, b *redsnet.TaisTarget) bool {
		return caSameATPAVolume(p, a, b)
	})
}
