package stars

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/juliusplatzer/reds/aviation"
	redsnet "github.com/juliusplatzer/reds/net"
)

// REDS associated/associated CA detection limits, in NM and feet.
// These simulation settings do not specify the FAA's adapted CA algorithm.
const (
	caLateralMinimumNM  = 1.0
	caVerticalMinimumFT = 300
	caMaximumTrackAge   = 30 * time.Second // TAIS-only validity guard
	caHeadingDifference = 15.0
	caMinLatitudeNM     = 59.9 // conservative screening bound, no false negatives

	// REDS simulation choices, not certified FAA STARS parameters. Only
	// existing events get clearance hysteresis beyond the detection limits.
	caLookahead           = 60 * time.Second
	caLateralClearanceNM  = caLateralMinimumNM + 0.1
	caVerticalClearanceFT = caVerticalMinimumFT + 100
)

// caPair identifies two associated live TAIS tracks. Identity is by live
// track key (not ACID, which can repeat), and keys are always ordered.
// Acknowledgments and lifetimes are managed by the event state below.
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
// retained for MCI; predictive CA uses the relative-motion solver below.
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

// caFindPairs evaluates associated-associated CA without mutating event state.
// continuing contains identities of existing events/inhibits, so new-alert
// closure rules cannot erase an ongoing event or disable pair re-enablement.
// The latitude sweep accounts for travel during report age AND lookahead.
func caFindPairs(snapshot redsnet.TaisSnapshot, now time.Time, inhibit []msawRegion,
	inSameATPA func(a, b *redsnet.TaisTarget) bool, continuing map[caPair]caIdentityPair,
	parallel caParallelPolicy) []caPair {
	if !snapshot.Ready {
		return nil
	}
	eligible := make([]caMotionTrack, 0, len(snapshot.Targets))
	maxSpeed, maxOffset := 0.0, 0.0
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
			track := caMotionTrack{caTrack: caTrack{target: t, key: targetDisplayStateKey(t), point: pt},
				cosLatitude: math.Cos(pt.lat * math.Pi / 180),
				hasVelocity: t.Track.HasHorizontalVelocity(), hasVerticalRate: t.Track.HasVerticalRate(),
				final: parallel.track(t)}
			// There is no existing surveillance extrapolator. Align known
			// motion to this update's now, bounded by caMaximumTrackAge.
			// Never infer velocity from display headings or a single altitude.
			age := now.Sub(t.Track.MRTTime).Seconds()
			if track.hasVelocity {
				track.eastVelocity, track.northVelocity = float64(t.Track.VX)/3600, float64(t.Track.VY)/3600
				track.eastOffset, track.northOffset = age*track.eastVelocity, age*track.northVelocity
				track.speed = math.Hypot(track.eastVelocity, track.northVelocity)
				maxSpeed = math.Max(maxSpeed, track.speed)
				maxOffset = math.Max(maxOffset, math.Abs(age)*track.speed)
			}
			if track.hasVerticalRate {
				track.verticalRate = float64(t.Track.VerticalRate) / 60
				track.verticalOffset = age * track.verticalRate
			}
			eligible = append(eligible, track)
		}
	}
	// Sorting an index-like, small struct avoids copying flight plans or
	// histories; no extra N-by-N matrix is allocated.
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].point.lat < eligible[j].point.lat })
	pairs := make([]caPair, 0)
	for i := range eligible {
		a := &eligible[i]
		bound := caLateralClearanceNM + caLookahead.Seconds()*(a.speed+maxSpeed) +
			math.Hypot(a.eastOffset, a.northOffset) + maxOffset
		for j := i + 1; j < len(eligible); j++ {
			b := &eligible[j]
			if (b.point.lat-a.point.lat)*caMinLatitudeNM > bound {
				break
			}
			if a.key == b.key {
				continue
			}
			if inSameATPA != nil && inSameATPA(a.target, b.target) {
				continue
			}
			key0, key1 := a.key, b.key
			if strings.Compare(key0, key1) > 0 {
				key0, key1 = key1, key0
			}
			pair := caPair{key0, key1}
			ids, active := continuing[pair]
			if active {
				first, second := a.target, b.target
				if a.key != pair.A {
					first, second = second, first
				}
				active = caIdentitiesMatch(ids, caIdentityPair{trackHighlightIdentity(first), trackHighlightIdentity(second)})
			}
			if caEvaluatePair(a, b, active, parallel.radius(a.final, b.final)) {
				pairs = append(pairs, pair)
			}
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
// Detection and event lifetime/acknowledgment management remain separate.
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
				Type   string `json:"type"`
				Center string `json:"center"`
			}
			if json.Unmarshal(item, &shape) == nil && strings.EqualFold(shape.Type, "circle") && !strings.Contains(shape.Center, ",") {
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
	p.updateCAFinals(snapshot, now)
	p.caPairs = caFindPairs(snapshot, now, p.caZones, func(a, b *redsnet.TaisTarget) bool {
		return caSameATPAVolume(p, a, b)
	}, p.caContinuingPairs(), caParallelPolicy{p.caParallel, p.caFinals, now})
}

// Parallel final geometry and classification. This is the Step 1 approximation
// in Tang (NASA, 2015), section II.C: min(ordinary radius, factor*spacing), with
// REDS's existing vertical/predictive criterion. It is not the paper's complete
// normalized conformance algorithm, ATC separation approval, or an FMA/NTZ
// detector. Step 2 must handle blunders and corridor-exit transitions explicitly.

type caFinalAirport struct {
	origin  aviation.Point
	runways []int
}

type caFinalRunway struct {
	airport            int
	id                 string
	threshold, outward [2]float64 // airport-local east/north NM, unit outward
	elevation          int
	hasElevation       bool
	maxCross           float64 // widest qualifying neighbor; pair checks narrow it
}

type caParallelPair struct {
	spacing, radius, maxCross float64 // NM
}

type caParallelGeometry struct {
	settings    caParallelConfig
	airports    []caFinalAirport
	airportByID map[string]int // canonical ICAO and verified database aliases
	runways     []caFinalRunway
	pairs       map[[2]int]caParallelPair
}

func caRunwayPair(a, b int) [2]int {
	if a > b {
		a, b = b, a
	}
	return [2]int{a, b}
}

// Reuse the double-precision local projection already used for CIFP approach
// geometry. Normalize longitude about the origin for airports near +/-180.
func caFinalXY(origin aviation.Point, at aviation.Point) [2]float64 {
	x, y := msawApproachXY(origin, msawPoint{at.Lat, origin.Lon + math.Remainder(at.Lon-origin.Lon, 360)})
	return [2]float64{x, y}
}

func newCAParallelGeometry(runways map[string][]aviation.Runway, cfg caParallelConfig) *caParallelGeometry {
	g := &caParallelGeometry{settings: cfg, airportByID: make(map[string]int), pairs: make(map[[2]int]caParallelPair)}
	airports := make([]string, 0, len(runways))
	for airport := range runways {
		airports = append(airports, airport)
	}
	sort.Strings(airports)
	for _, airport := range airports {
		source := runways[airport]
		if len(source) < 2 {
			continue
		}
		origin := source[0].Threshold
		if !msawValidPoint(msawPoint{origin.Lat, origin.Lon}) || math.Abs(origin.Lat) > 80 {
			continue // This local projection is not a polar approximation.
		}
		ai := len(g.airports)
		g.airportByID[airport] = ai
		g.airports = append(g.airports, caFinalAirport{origin: origin})
		byID := make(map[string]aviation.Runway, len(source))
		conflicting := make(map[string]bool)
		for _, r := range source {
			id := normalizeATPARunway(r.ID)
			if id != "" {
				if old, exists := byID[id]; exists && old.Threshold != r.Threshold {
					conflicting[id] = true
				}
				byID[id] = r
			}
		}
		seen := make(map[string]bool, len(source))
		for _, r := range source {
			id := normalizeATPARunway(r.ID)
			if id == "" || conflicting[id] || seen[id] || !msawValidPoint(msawPoint{r.Threshold.Lat, r.Threshold.Lon}) {
				continue
			}
			var number int
			if _, err := fmt.Sscanf(id, "%d", &number); err != nil {
				continue
			}
			suffix := strings.TrimLeft(id, "0123456789")
			if suffix == "L" {
				suffix = "R"
			} else if suffix == "R" {
				suffix = "L"
			}
			reciprocalID := fmt.Sprintf("%d%s", (number+17)%36+1, suffix)
			reciprocal, ok := byID[reciprocalID]
			if !ok || conflicting[reciprocalID] || !msawValidPoint(msawPoint{reciprocal.Threshold.Lat, reciprocal.Threshold.Lon}) {
				continue
			}
			q, end := caFinalXY(origin, r.Threshold), caFinalXY(origin, reciprocal.Threshold)
			dx, dy := q[0]-end[0], q[1]-end[1]
			length := math.Hypot(dx, dy)
			if !caFinite(length) || length < .02 || length > 10 {
				continue
			}
			ri := len(g.runways)
			seen[id] = true
			g.runways = append(g.runways, caFinalRunway{airport: ai, id: id, threshold: q,
				outward: [2]float64{dx / length, dy / length}, elevation: r.Elevation, hasElevation: r.HasElevation})
			g.airports[ai].runways = append(g.airports[ai].runways, ri)
		}
		indices := g.airports[ai].runways
		for i, a := range indices {
			for _, b := range indices[i+1:] {
				if pair, ok := caParallelSpacing(g.runways[a], g.runways[b], cfg); ok {
					g.pairs[caRunwayPair(a, b)] = pair
					g.runways[a].maxCross = math.Max(g.runways[a].maxCross, pair.maxCross)
					g.runways[b].maxCross = math.Max(g.runways[b].maxCross, pair.maxCross)
				}
			}
		}
	}
	return g
}

func caParallelSpacing(a, b caFinalRunway, cfg caParallelConfig) (caParallelPair, bool) {
	dot := a.outward[0]*b.outward[0] + a.outward[1]*b.outward[1]
	if a.airport != b.airport || a.id == b.id || dot < math.Cos(cfg.MaxCourseDifferenceDeg*math.Pi/180) {
		return caParallelPair{}, false
	}
	ux, uy := a.outward[0]+b.outward[0], a.outward[1]+b.outward[1]
	length := math.Hypot(ux, uy)
	ux, uy = ux/length, uy/length
	nx, ny := -uy, ux
	// Compare both lines at common along-track stations, not at their
	// independently staggered thresholds. Reject crossing/unstable finals.
	type line struct{ along, cross, slope, end float64 }
	project := func(r caFinalRunway) line {
		along := r.threshold[0]*ux + r.threshold[1]*uy
		forward := r.outward[0]*ux + r.outward[1]*uy
		return line{along: along, cross: r.threshold[0]*nx + r.threshold[1]*ny,
			slope: (r.outward[0]*nx + r.outward[1]*ny) / forward,
			end:   along + cfg.MaxFinalDistanceNM*forward}
	}
	la, lb := project(a), project(b)
	start, end := math.Max(la.along, lb.along), math.Min(la.end, lb.end)
	if start >= end {
		return caParallelPair{}, false
	}
	spacing := func(s float64) float64 { return lb.cross + (s-lb.along)*lb.slope - la.cross - (s-la.along)*la.slope }
	d0, d1 := spacing(start), spacing(end)
	low, high := math.Min(math.Abs(d0), math.Abs(d1)), math.Max(math.Abs(d0), math.Abs(d1))
	if !caFinite(low) || !caFinite(high) || low <= 1e-6 || d0*d1 <= 0 || high-low > cfg.MaxSpacingVariation*low {
		return caParallelPair{}, false
	}
	d := (low + high) / 2
	return caParallelPair{spacing: d, radius: math.Min(caLateralMinimumNM, cfg.SeparationFactor*d),
		maxCross: math.Min(cfg.MaxCrossTrackNM, cfg.CrossTrackSpacingFactor*d)}, true
}

func (p *STARSPane) initCAParallelApproaches() {
	p.caParallel = nil
	clear(p.caFinals)
	cfg, err := p.config.Facility.CA.ParallelApproaches.settings()
	if err == nil && cfg.Enabled != nil && !*cfg.Enabled {
		return
	}
	var nav aviation.StaticDatabase
	if err == nil {
		nav, err = aviation.LoadCIFPDatabase()
	}
	if err != nil {
		if p.logger != nil {
			p.logger.Warn("CA parallel adaptation unavailable; using ordinary CA limits", "error", err)
		}
		return
	}
	db, err := loadSTARSAirportDatabase()
	if err != nil {
		if p.logger != nil {
			p.logger.Warn("CA parallel airport aliases unavailable; using ordinary CA limits", "error", err)
		}
		return
	}
	selected := make(map[string][]aviation.Runway)
	add := func(id string) {
		id = strings.ToUpper(strings.TrimSpace(id))
		if alias := db.byIATA[id]; alias != "" {
			id = alias
		}
		if runways := nav.Runways[id]; len(runways) != 0 {
			selected[id] = runways
		}
	}
	for _, area := range p.config.Facility.Areas {
		for _, id := range area.UnderlyingAirports {
			add(id)
		}
		for _, id := range area.SSAAirports {
			add(id)
		}
	}
	for _, volume := range p.config.Facility.ATPAVolumes {
		add(volume.AirportID)
	}
	p.caParallel = newCAParallelGeometry(selected, cfg)
	if len(p.caParallel.pairs) == 0 {
		p.caParallel = nil
		return
	}
	for alias, icao := range db.byIATA {
		if i, ok := p.caParallel.airportByID[icao]; ok {
			p.caParallel.airportByID[alias] = i
		}
	}
}

type caFinalObservation struct {
	position                aviation.Point
	vx, vy, altitude, climb int
	hasMotion, hasClimb     bool
	assignment, airport     string
}

type caFinalState struct {
	identity              starsTrackHighlightIdentity
	observation           caFinalObservation
	last                  time.Time
	sweep                 uint64
	runway, confirmations int
	established           bool
	along, cross          float64 // NM at report time
	alongRate, crossRate  float64 // NM/s
}

func (g *caParallelGeometry) classify(t *redsnet.TaisTarget, observation caFinalObservation) (caFinalState, bool) {
	best, second := math.Inf(1), math.Inf(1)
	state := caFinalState{}
	airportID := strings.ToUpper(strings.TrimSpace(observation.airport))
	assigned := normalizeATPARunway(observation.assignment)
	airport, knownAirport := g.airportByID[airportID]
	if (airportID != "" && !knownAirport) || (strings.TrimSpace(observation.assignment) != "" && assigned == "") {
		return state, false
	}
	speed := math.Hypot(float64(t.Track.VX), float64(t.Track.VY))
	if speed < 1 || !t.Track.HasHorizontalVelocity() {
		return state, false
	}
	for ai, a := range g.airports {
		if knownAirport && airport != ai {
			continue
		}
		at := caFinalXY(a.origin, observation.position)
		for _, ri := range a.runways {
			r := g.runways[ri]
			if r.maxCross == 0 || (assigned != "" && assigned != r.id) {
				continue
			}
			dx, dy := at[0]-r.threshold[0], at[1]-r.threshold[1]
			along := dx*r.outward[0] + dy*r.outward[1]
			cross := -dx*r.outward[1] + dy*r.outward[0]
			inbound := -(float64(t.Track.VX)*r.outward[0] + float64(t.Track.VY)*r.outward[1]) / speed
			if along < 0 || along > g.settings.MaxFinalDistanceNM || math.Abs(cross) > r.maxCross ||
				inbound < math.Cos(g.settings.MaxTrackErrorDeg*math.Pi/180) {
				continue
			}
			// Broad supporting bounds, not an invented glidepath. Reject flight
			// below the runway or clearly overhead, allowing step-downs/visuals.
			if r.hasElevation && (t.Track.ReportedAltitude < r.elevation-100 || t.Track.ReportedAltitude > r.elevation+10000) {
				continue
			}
			// Reuse the existing sustained-climb evidence near the threshold;
			// no extra go-around state machine or dependence on MSAW enablement.
			if along <= msawInitialClimbRadiusNM {
				if _, climbing := msawClimbingHistory(t); climbing {
					continue
				}
			}
			crossError := math.Abs(cross)
			if crossError < best {
				second, best = best, crossError
				state = caFinalState{runway: ri, along: along, cross: cross, established: assigned != "" && knownAirport,
					alongRate: (float64(t.Track.VX)*r.outward[0] + float64(t.Track.VY)*r.outward[1]) / 3600,
					crossRate: (-float64(t.Track.VX)*r.outward[1] + float64(t.Track.VY)*r.outward[0]) / 3600}
			} else if crossError < second {
				second = crossError
			}
		}
	}
	// Within 0.005 NM (30 ft), inferred candidates are indistinguishable at
	// this precision. Do not resolve ties by airport/runway enumeration order.
	return state, !math.IsInf(best, 1) && second-best > .005
}

func (p *STARSPane) updateCAFinals(snapshot redsnet.TaisSnapshot, now time.Time) {
	if p.caParallel == nil || !snapshot.Ready {
		clear(p.caFinals)
		return
	}
	if p.caFinals == nil {
		p.caFinals = make(map[string]caFinalState)
	}
	p.caFinalSweep++
	for i := range snapshot.Targets {
		t := &snapshot.Targets[i]
		if !caUsableTrack(t, now) || t.CoastPhase != 0 || t.Track.Frozen || taisFlightPlanIsDeparture(t) ||
			!msawValidPoint(msawPoint{t.Track.Lat, t.Track.Lon}) {
			continue
		}
		observation := caFinalObservation{position: aviation.Point{Lat: t.Track.Lat, Lon: t.Track.Lon},
			vx: t.Track.VX, vy: t.Track.VY, altitude: t.Track.ReportedAltitude, climb: t.Track.VerticalRate,
			hasMotion: t.Track.HasHorizontalVelocity(), hasClimb: t.Track.HasVerticalRate(), assignment: t.FlightPlan.Runway,
			airport: t.FlightPlan.Airport}
		if t.EnhancedData != nil && strings.TrimSpace(t.EnhancedData.DestinationAirport) != "" {
			observation.airport = t.EnhancedData.DestinationAirport
		}
		key, identity, when := targetDisplayStateKey(t), trackHighlightIdentity(t), t.Track.MRTTime
		previous, exists := p.caFinals[key]
		same := exists && trackHighlightIdentityMatches(previous.identity, identity)
		if same && when.Before(previous.last) {
			previous.established, previous.confirmations, previous.sweep = false, 0, p.caFinalSweep
			p.caFinals[key] = previous
			continue // Out-of-order reports must not become new confirmations.
		}
		if same && when.Equal(previous.last) && observation == previous.observation {
			previous.sweep = p.caFinalSweep
			p.caFinals[key] = previous
			continue // Rendering another frame is not another radar measurement.
		}
		state, valid := p.caParallel.classify(t, observation)
		state.identity, state.observation, state.last, state.sweep = identity, observation, when, p.caFinalSweep
		if valid {
			state.confirmations = 1
			if same && previous.confirmations != 0 && previous.runway == state.runway &&
				previous.observation.assignment == observation.assignment && previous.observation.airport == observation.airport &&
				when.Sub(previous.last) <= caMaximumTrackAge {
				state.confirmations = previous.confirmations
				if when.After(previous.last) && state.confirmations < p.caParallel.settings.InferredConfirmationCount {
					state.confirmations++
				}
			}
			state.established = state.established || state.confirmations >= p.caParallel.settings.InferredConfirmationCount
		}
		p.caFinals[key] = state
	}
	for key, state := range p.caFinals {
		if state.sweep != p.caFinalSweep {
			delete(p.caFinals, key)
		}
	}
}

// Copy only a runway index and deviation into each detector candidate. Full
// observation/identity/stability records are read once per target, never once
// per aircraft pair. Time alignment is arithmetic, without pair-loop geometry.
type caParallelTrack struct {
	runway      int
	cross       float64
	established bool
}

type caParallelPolicy struct {
	geometry *caParallelGeometry
	finals   map[string]caFinalState
	now      time.Time
}

func (p caParallelPolicy) track(t *redsnet.TaisTarget) caParallelTrack {
	if p.geometry == nil {
		return caParallelTrack{}
	}
	final := p.finals[targetDisplayStateKey(t)]
	if !final.established {
		return caParallelTrack{}
	}
	age := p.now.Sub(final.last)
	along := final.along + age.Seconds()*final.alongRate
	if age > caMaximumTrackAge || age < -2*time.Second || along < 0 || along > p.geometry.settings.MaxFinalDistanceNM {
		return caParallelTrack{}
	}
	return caParallelTrack{runway: final.runway, cross: final.cross + age.Seconds()*final.crossRate, established: true}
}

func (p caParallelPolicy) radius(a, b caParallelTrack) float64 {
	if p.geometry != nil && a.established && b.established {
		if pair, ok := p.geometry.pairs[caRunwayPair(a.runway, b.runway)]; ok && math.Abs(a.cross) <= pair.maxCross && math.Abs(b.cross) <= pair.maxCross {
			return pair.radius
		}
	}
	return caLateralMinimumNM
}

func (p *STARSPane) caParallelRadius(a, b *redsnet.TaisTarget, now time.Time) float64 {
	policy := caParallelPolicy{p.caParallel, p.caFinals, now}
	return policy.radius(policy.track(a), policy.track(b))
}

// Predictive CA geometry.

// Cached once per CA update. Keep this separate from caTrack so MCI retains
// its small candidate records. Velocities are ground NM/s and ft/s.
type caMotionTrack struct {
	caTrack
	final                                                  caParallelTrack
	cosLatitude, eastVelocity, northVelocity, verticalRate float64
	eastOffset, northOffset, verticalOffset, speed         float64
	hasVelocity, hasVerticalRate                           bool
}

// caInterval is an inclusive time interval in seconds. Empty intervals are
// returned with ok=false, including infringements wholly outside [0,horizon].
type caInterval struct{ start, end float64 }

func caFinite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// caHorizontalInterval solves |r+t*v| <= radius analytically. Position and
// radius are NM; velocity is NM/s. Tiny nonzero speeds are solved normally,
// rather than rounded to zero near an infringement boundary.
func caHorizontalInterval(rx, ry, vx, vy, radius, horizon float64) (caInterval, bool) {
	if !caFinite(rx) || !caFinite(ry) || !caFinite(vx) || !caFinite(vy) ||
		!caFinite(radius) || !caFinite(horizon) || radius <= 0 || horizon < 0 {
		return caInterval{}, false
	}
	a := vx*vx + vy*vy
	rv := rx*vx + ry*vy
	c := rx*rx + ry*ry - radius*radius
	if !caFinite(a) || !caFinite(rv) || !caFinite(c) {
		return caInterval{}, false
	}
	if a == 0 {
		return caInterval{0, horizon}, c <= 0
	}
	// Half-discriminant form avoids the common factors of two. Clamp only
	// roundoff at tangency (32 machine epsilons), never a horizon offset.
	disc := math.FMA(-a, c, rv*rv)
	if !caFinite(disc) {
		return caInterval{}, false
	}
	tolerance := 32 * 0x1p-52 * (rv*rv + math.Abs(a*c))
	if disc < -tolerance {
		return caInterval{}, false
	}
	disc = math.Max(0, disc)
	// Stable quadratic roots: avoid subtracting nearly equal numbers when
	// one root is close to the current time. Their product is c/a.
	q := -rv - math.Copysign(math.Sqrt(disc), rv)
	lo, hi := 0.0, 0.0
	if q != 0 {
		lo, hi = q/a, c/q
		if lo > hi {
			lo, hi = hi, lo
		}
	}
	i := caInterval{math.Max(0, lo), math.Min(horizon, hi)}
	return i, i.start <= i.end
}

// caVerticalInterval solves |height+t*rate| <= limit. Height/limit are feet
// and signed rate is ft/s. A zero rate may be a measured value or the explicit
// unknown-motion approximation selected by caEvaluatePair.
func caVerticalInterval(height, rate, limit, horizon float64) (caInterval, bool) {
	if !caFinite(height) || !caFinite(rate) || !caFinite(limit) || !caFinite(horizon) || limit <= 0 || horizon < 0 {
		return caInterval{}, false
	}
	if rate == 0 {
		return caInterval{0, horizon}, math.Abs(height) <= limit
	}
	lo, hi := (-limit-height)/rate, (limit-height)/rate
	if lo > hi {
		lo, hi = hi, lo
	}
	i := caInterval{math.Max(0, lo), math.Min(horizon, hi)}
	return i, i.start <= i.end
}

// caInfringement intersects horizontal and vertical intervals at a COMMON
// time. Separate minima (including horizontal CPA) cannot establish conflict.
func caInfringement(rx, ry, vx, vy, height, rate, horizon, radius float64) (caInterval, bool) {
	// Vertical screening is inexpensive and must include future closure.
	v, ok := caVerticalInterval(height, rate, caVerticalMinimumFT, horizon)
	if !ok {
		return caInterval{}, false
	}
	h, ok := caHorizontalInterval(rx, ry, vx, vy, radius, horizon)
	if !ok {
		return caInterval{}, false
	}
	i := caInterval{math.Max(h.start, v.start), math.Min(h.end, v.end)}
	return i, i.start <= i.end
}

// caNewConflict applies new-event policy AFTER the mathematical test. An
// outside pair may enter vertically even while diverging horizontally. Inside
// both thresholds, require non-resolving horizontal motion. The 0.1-knot
// radial tolerance ignores numerical noise; coincident positions within
// 0.001 NM retain legacy alerting despite the zero-distance derivative.
func caNewConflict(rx, ry, vx, vy, height, rate, horizon, radius float64) bool {
	interval, ok := caInfringement(rx, ry, vx, vy, height, rate, horizon, radius)
	if !ok {
		return false
	}
	distance2 := rx*rx + ry*ry
	if distance2 > radius*radius || math.Abs(height) > caVerticalMinimumFT {
		return true
	}
	const coincidentNM = 0.001
	if distance2 <= coincidentNM*coincidentNM {
		return true
	}
	distance := math.Sqrt(distance2)
	const closureToleranceNMPS = 0.1 / 3600
	if rx*vx+ry*vy > distance*closureToleranceNMPS {
		return false
	}
	// If increasing vertical separation already dominates normalized
	// severity, q(t)=max(d/R,|h|/H) can only worsen until vertical exit.
	// Suppress this clearly resolving infringement, including an immediate
	// vertical exit. Otherwise keep even brief developing encounters.
	if height*rate > 0 && (interval.end == 0 || math.Abs(height)/caVerticalMinimumFT >= distance/radius) {
		return false
	}
	return true // negligible relative motion preserves legacy proximity CA
}

// caEvaluatePair uses east/north NM in a local tangent plane, consistent
// with REDS's geographic projections. Averaged cached cosines avoid pair-loop
// trigonometry. Longitude wrapping also handles encounters across +/-180.
func caEvaluatePair(a, b *caMotionTrack, continuing bool, radius float64) bool {
	clearance := radius + (caLateralClearanceNM - caLateralMinimumNM)
	height := float64(b.target.Track.ReportedAltitude) - float64(a.target.Track.ReportedAltitude)
	if !a.hasVelocity || !b.hasVelocity {
		// No directional prediction without both velocity vectors. Retain
		// legacy current-separation alerting; an incomplete vector cannot
		// prove divergence. Only existing events get clearance hysteresis.
		limit := float64(caVerticalMinimumFT)
		if continuing {
			radius, limit = clearance, caVerticalClearanceFT
		}
		return msawDistanceNM(a.point, b.point) <= radius && math.Abs(height) <= limit
	}
	rx := math.Remainder(b.point.lon-a.point.lon, 360)*60*(a.cosLatitude+b.cosLatitude)/2 + b.eastOffset - a.eastOffset
	ry := (b.point.lat-a.point.lat)*60 + b.northOffset - a.northOffset
	vx, vy := b.eastVelocity-a.eastVelocity, b.northVelocity-a.northVelocity
	rate := 0.0
	if a.hasVerticalRate && b.hasVerticalRate {
		height += b.verticalOffset - a.verticalOffset
		rate = b.verticalRate - a.verticalRate
	}
	// Otherwise hold the observed altitude difference: unknown vertical
	// motion is an approximation, not a measured zero climb/descent.
	if continuing {
		if rx*rx+ry*ry <= clearance*clearance && math.Abs(height) <= caVerticalClearanceFT {
			return true
		}
		_, ok := caInfringement(rx, ry, vx, vy, height, rate, caLookahead.Seconds(), radius)
		return ok
	}
	return caNewConflict(rx, ry, vx, vy, height, rate, caLookahead.Seconds(), radius)
}

// CA event state, acknowledgment and inhibition.

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
	caDisabled, mciDisabled, msawDisabled, approachDisabled bool

	tcpInhibits map[string]bool // both members owned by this TCP; §7.13
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
func (p *STARSPane) caPairOverrideValid(pair caPair, o caPairOverride, a, b *redsnet.TaisTarget) bool {
	if a == nil || b == nil || !caIdentitiesMatch(caIdentityPair{trackHighlightIdentity(a), trackHighlightIdentity(b)}, o.Identities) {
		return false
	}
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

// caContinuingPairs snapshots existing event identities before geometry runs.
// Inhibited pairs remain discoverable by CA+slew for re-enablement. The shared
// ACK cache deliberately outlives resolved alerts by two seconds; it is NOT
// evidence that an event remains active. Staleness, identity, adaptation and
// ownership checks still run in their existing paths.
// No site lock is held across the aircraft-pair scan.
func (p *STARSPane) caContinuingPairs() map[caPair]caIdentityPair {
	pairs := make(map[caPair]caIdentityPair, len(p.caAlerts))
	for pair, event := range p.caAlerts {
		pairs[pair] = caIdentityPair{event.AIdentity, event.BIdentity}
	}
	site := p.caSiteState()
	site.RLock()
	defer site.RUnlock()
	for pair, inhibit := range site.pairs {
		pairs[pair] = inhibit.Identities
	}
	return pairs
}

func (p *STARSPane) updateCAEvents(snapshot redsnet.TaisSnapshot, now time.Time) {
	if p == nil {
		return
	}
	if !snapshot.Ready || !p.caZonesValid {
		clear(p.caAlerts)
		clear(p.caTargets)
		return
	}
	if p.caAlerts == nil {
		p.caAlerts = make(map[caPair]caAlertState)
	}
	p.caGeneration++
	generation := p.caGeneration
	// Reuse the event update's index for counterpart lookup during rendering.
	// Snapshot tracks are detached from the network client's mutable state.
	if p.caTargets == nil {
		p.caTargets = make(map[string]*redsnet.TaisTarget, len(snapshot.Targets))
	} else {
		clear(p.caTargets)
	}
	byKey := p.caTargets
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
		if !p.caPairOverrideValid(pair, o, byKey[pair.A], byKey[pair.B]) {
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

// caPairEligible is the common local presentation rule for CA indications,
// FDB promotion, list rows, audio and acknowledgment. The provisional REDS
// policy requires ownership of either member; Quick Look never grants alert
// eligibility. Global detection and event lifetime remain independent.
// Caller holds the site's read or write lock; this function never locks it.
func (p *STARSPane) caPairEligible(pair caPair, event caAlertState, a, b *redsnet.TaisTarget, site *caSiteRuntimeState) bool {
	if p == nil || !p.caZonesValid || site.caDisabled || a == nil || b == nil || a.FlightPlan == nil || b.FlightPlan == nil ||
		a.FlightPlan.Deleted || b.FlightPlan.Deleted || a.FlightPlan.Suspended || b.FlightPlan.Suspended ||
		a.CoastPhase >= redsnet.TaisCoastPhase2 || b.CoastPhase >= redsnet.TaisCoastPhase2 ||
		(!p.targetOwnedByCurrentTCP(a) && !p.targetOwnedByCurrentTCP(b)) {
		return false
	}
	ao, oka := p.targetOwnerTCP(a)
	bo, okb := p.targetOwnerTCP(b)
	if oka && okb && strings.EqualFold(ao, bo) && site.tcpInhibits[strings.ToUpper(ao)] {
		return false
	}
	if !caIdentitiesMatch(caIdentityPair{event.AIdentity, event.BIdentity},
		caIdentityPair{trackHighlightIdentity(a), trackHighlightIdentity(b)}) {
		return false
	}
	if inhibit, ok := site.tracks[pair.A]; ok && p.caTrackOverrideValid(a, inhibit) {
		return false
	}
	if inhibit, ok := site.tracks[pair.B]; ok && p.caTrackOverrideValid(b, inhibit) {
		return false
	}
	// Pairwise inhibition applies only to this pair, even when one member
	// participates in other active conflicts. Check late slew changes too.
	if inhibit, ok := site.pairs[pair]; ok && p.caPairOverrideValid(pair, inhibit, a, b) {
		return false
	}
	return true
}

// caEventAcknowledged preserves the existing TCW-local and system-wide owned
// acknowledgment semantics. Caller holds the site's read or write lock.
func caEventAcknowledged(pair caPair, event caAlertState, site *caSiteRuntimeState) bool {
	global, ok := site.events[pair]
	return event.Acknowledged || (ok && global.Acknowledged &&
		caIdentitiesMatch(global.Identities, caIdentityPair{event.AIdentity, event.BIdentity}))
}

func (p *STARSPane) caTrackAlertPresentation(t *redsnet.TaisTarget) (active, unacknowledged bool) {
	if p == nil || t == nil || len(p.caAlerts) == 0 {
		return false, false
	}
	key := targetDisplayStateKey(t)
	identity := trackHighlightIdentity(t)
	shared := p.caSiteState()
	shared.RLock()
	defer shared.RUnlock()
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
		a, b := p.caTargets[pair.A], p.caTargets[pair.B]
		if key == pair.A {
			a = t
		} else {
			b = t
		}
		if !p.caPairEligible(pair, event, a, b, shared) {
			continue
		}
		active = true
		if !caEventAcknowledged(pair, event, shared) {
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
		a, b := p.caTargets[pair.A], p.caTargets[pair.B]
		if key == pair.A {
			a = t
		} else {
			b = t
		}
		if !p.caPairEligible(pair, ev, a, b, site) {
			continue
		}
		global := site.events[pair]
		if caEventAcknowledged(pair, ev, site) {
			continue
		}
		changed = true
		// §7.3: ownership of the clicked aircraft alone is insufficient.
		// Every involved member must be owned here for a system-wide CA ACK.
		if p.targetOwnedByCurrentTCP(a) && p.targetOwnedByCurrentTCP(b) {
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
	if site.caDisabled {
		return ErrSTARSCAOff
	}
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
	if site.caDisabled {
		return ErrSTARSCAOff
	}
	if _, ok := site.tracks[key]; ok {
		return ErrSTARSIllegalTrack
	}
	// Re-enablement does not require the encounter still to be geometrically
	// conflicting. Retained pair inhibition is the authoritative selection.
	var inhibited caPair
	count := 0
	for pair := range site.pairs {
		if caPairHas(pair, key) {
			inhibited = pair
			count++
		}
	}
	if count != 0 {
		if count != 1 {
			return ErrSTARSIllegalTrack
		}
		delete(site.pairs, inhibited)
		return nil
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

// §§7.11-7.12: P + first ACID + second slew identifies a specific pair,
// including an inhibited pair that has since resolved geometrically.
func (p *STARSPane) toggleCAExplicitPair(a, b *redsnet.TaisTarget) error {
	if a == nil || b == nil || a.FlightPlan == nil || b.FlightPlan == nil {
		return ErrSTARSIllegalTrack
	}
	if !p.caCommandTargetValid(a) && !p.caCommandTargetValid(b) {
		return ErrSTARSIllegalTrack
	}
	if targetDisplayStateKey(a) == targetDisplayStateKey(b) {
		return ErrSTARSIllegalTrack
	}
	if targetDisplayStateKey(a) > targetDisplayStateKey(b) {
		a, b = b, a
	}
	pair := caPair{targetDisplayStateKey(a), targetDisplayStateKey(b)}
	ownerTrack := a
	if !p.targetOwnedByCurrentTCP(a) {
		ownerTrack = b
	}
	owner, _ := p.targetOwnerTCP(ownerTrack)
	o := caPairOverride{Identities: caIdentityPair{trackHighlightIdentity(a), trackHighlightIdentity(b)},
		OwnerTCP: owner, OwnerTrackKey: targetDisplayStateKey(ownerTrack)}
	if !p.caPairOverrideValid(pair, o, a, b) {
		return ErrSTARSIllegalTrack
	}
	site := p.caSiteState()
	site.Lock()
	defer site.Unlock()
	if site.caDisabled {
		return ErrSTARSCAOff
	}
	if _, ok := site.pairs[pair]; ok {
		delete(site.pairs, pair)
		return nil
	}
	if _, blocked := site.tracks[pair.A]; blocked {
		return ErrSTARSIllegalTrack
	}
	if _, blocked := site.tracks[pair.B]; blocked {
		return ErrSTARSIllegalTrack
	}
	if _, active := p.caAlerts[pair]; !active {
		return ErrSTARSIllegalTrack
	}
	delete(site.mciSuppress, pair.A)
	delete(site.mciSuppress, pair.B)
	site.pairs[pair] = o
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

// MCI detection, event state and beacon suppression.

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
		clear(p.mciTargets)
		return
	}
	if p.mciAlerts == nil {
		p.mciAlerts = make(map[mciPair]mciAlertState)
	}
	p.mciGeneration++
	generation := p.mciGeneration
	if p.mciTargets == nil {
		p.mciTargets = make(map[string]*redsnet.TaisTarget, len(snapshot.Targets))
	} else {
		clear(p.mciTargets)
	}
	byKey := p.mciTargets
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
	if site.caDisabled || site.mciDisabled {
		return false, false
	}
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
	if site.caDisabled || site.mciDisabled {
		return false
	}
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
		assoc := p.mciTargets[pair.Associated]
		if assoc != nil && p.targetOwnedByCurrentTCP(assoc) && mciEventMatches(global, event) {
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
	if site.caDisabled || site.mciDisabled {
		return true
	}
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
	if p == nil || !p.caCommandTargetValid(t) || (code != "" && mciBeaconCode(code) == "") {
		return ErrSTARSIllegalTrack
	}
	key := targetDisplayStateKey(t)
	owner, _ := p.targetOwnerTCP(t)
	site := p.caSiteState()
	site.Lock()
	defer site.Unlock()
	if site.caDisabled {
		return ErrSTARSCAOff
	}
	// Omitting a code removes ANY existing code, not just the default 0477.
	// This distinction must survive parsing the keyboard and slew variants.
	if code == "" {
		if old, ok := site.mciSuppress[key]; ok && mciOverrideValid(p, t, old) {
			delete(site.mciSuppress, key)
			return nil
		}
		code = mciDefaultBeaconCode
	}
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

// CA/MCI audio and shared alert priority.

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
		if now.Before(event.Since) || !now.Before(event.Since.Add(caAudioDuration)) || caEventAcknowledged(pair, event, site) {
			continue
		}
		a, b := byKey[pair.A], byKey[pair.B]
		if !p.caPairEligible(pair, event, a, b, site) {
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
	if site.caDisabled || site.mciDisabled {
		return false
	}
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
