package stars

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juliusplatzer/reds/aviation"
	redsnet "github.com/juliusplatzer/reds/net"
	"github.com/juliusplatzer/reds/util"
	"github.com/klauspost/compress/zstd"
)

// REDS-computed *current* MSAW, not an FAA-provided alert or the predicted
// MSAW function described by TI 6191.409 §2.16.3. CA/MCI are deferred.
const msawMaximumTrackAge = 30 * time.Second

type msawInhibitConfig struct {
	CA   []json.RawMessage `json:"ca"` // Reserved for the later CA implementation.
	MSAW []msawInhibitZone `json:"msaw"`
}

type msawInhibitZone struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Floor    int             `json:"floor"`
	Ceiling  int             `json:"ceiling"`
	Center   string          `json:"center"`
	Radius   float64         `json:"radius"` // nautical miles
	Vertices json.RawMessage `json:"vertices"`
	Holes    json.RawMessage `json:"holes"`
}

type msawPoint struct{ lat, lon float64 }
type msawRing []msawPoint

type msawPolygon struct {
	altitude                       int
	rings                          []msawRing // [0] is exterior, later rings are holes
	minLat, maxLat, minLon, maxLon float64
}

func (p msawPolygon) contains(at msawPoint) bool {
	if at.lat < p.minLat || at.lat > p.maxLat || at.lon < p.minLon || at.lon > p.maxLon || len(p.rings) == 0 || !msawRingContains(p.rings[0], at) {
		return false
	}
	for _, hole := range p.rings[1:] {
		if msawRingContains(hole, at) {
			return false
		}
	}
	return true
}

// Ray casting with an explicit boundary case. Longitude is the x coordinate;
// latitude is y. No allocations per target/sector are needed.
func msawRingContains(ring msawRing, at msawPoint) bool {
	if len(ring) < 3 {
		return false
	}
	inside := false
	j := len(ring) - 1
	for i := range ring {
		a, b := ring[j], ring[i]
		cross := (at.lon-a.lon)*(b.lat-a.lat) - (at.lat-a.lat)*(b.lon-a.lon)
		if math.Abs(cross) < 1e-10 && at.lat >= math.Min(a.lat, b.lat)-1e-10 && at.lat <= math.Max(a.lat, b.lat)+1e-10 && at.lon >= math.Min(a.lon, b.lon)-1e-10 && at.lon <= math.Max(a.lon, b.lon)+1e-10 {
			return true
		}
		if (a.lat > at.lat) != (b.lat > at.lat) && at.lon < (b.lon-a.lon)*(at.lat-a.lat)/(b.lat-a.lat)+a.lon {
			inside = !inside
		}
		j = i
	}
	return inside
}

func msawPolygonFromRings(altitude int, rings []msawRing) (msawPolygon, error) {
	if altitude <= 0 || len(rings) == 0 || len(rings[0]) < 3 {
		return msawPolygon{}, errors.New("invalid MVA polygon or minimum")
	}
	p := msawPolygon{altitude: altitude, rings: rings, minLat: math.Inf(1), maxLat: math.Inf(-1), minLon: math.Inf(1), maxLon: math.Inf(-1)}
	for ri, ring := range rings {
		if len(ring) < 3 {
			return msawPolygon{}, fmt.Errorf("ring %d has fewer than three points", ri)
		}
		for _, pt := range ring {
			if !msawValidPoint(pt) {
				return msawPolygon{}, errors.New("invalid MVA coordinate")
			}
			if ri == 0 {
				p.minLat = math.Min(p.minLat, pt.lat)
				p.maxLat = math.Max(p.maxLat, pt.lat)
				p.minLon = math.Min(p.minLon, pt.lon)
				p.maxLon = math.Max(p.maxLon, pt.lon)
			}
		}
	}
	return p, nil
}

func msawValidPoint(p msawPoint) bool {
	return !math.IsNaN(p.lat) && !math.IsNaN(p.lon) && !math.IsInf(p.lat, 0) && !math.IsInf(p.lon, 0) && p.lat >= -90 && p.lat <= 90 && p.lon >= -180 && p.lon <= 180
}

type msawRegion struct {
	floor, ceiling int
	polygon        *msawPolygon
	center         msawPoint
	radiusNM       float64
}

func (r msawRegion) contains(at msawPoint, altitude int) bool {
	if altitude < r.floor || altitude > r.ceiling {
		return false
	}
	if r.polygon != nil {
		return r.polygon.contains(at)
	}
	return msawDistanceNM(at, r.center) < r.radiusNM
}

func msawDistanceNM(a, b msawPoint) float64 {
	const earthRadiusNM = 3440.065
	dlat := (b.lat - a.lat) * math.Pi / 180
	dlon := (b.lon - a.lon) * math.Pi / 180
	la1, la2 := a.lat*math.Pi/180, b.lat*math.Pi/180
	h := math.Pow(math.Sin(dlat/2), 2) + math.Cos(la1)*math.Cos(la2)*math.Pow(math.Sin(dlon/2), 2)
	return 2 * earthRadiusNM * math.Asin(math.Sqrt(math.Min(1, h)))
}

type msawAirspace struct {
	polygons []msawPolygon
	inhibit  []msawRegion
}

// Match VICE's *actual* GetMVA behavior: first matching MVA in source order,
// not the (incorrectly documented) maximum of overlaps. No coverage -> false.
func (a *msawAirspace) mvaAt(at msawPoint) (int, bool) {
	if a == nil {
		return 0, false
	}
	for _, p := range a.polygons {
		if p.contains(at) {
			return p.altitude, true
		}
	}
	return 0, false
}
func (a *msawAirspace) inhibited(at msawPoint, altitude int) bool {
	if a == nil {
		return false
	}
	for _, r := range a.inhibit {
		if r.contains(at, altitude) {
			return true
		}
	}
	return false
}

// Decode one coordinate from VICE's DMS notation, e.g.
// N038.49.38.530,W077.05.15.197. Altitudes are MSL feet.
func msawParseDMSCoordinate(s string) (msawPoint, error) {
	parts := strings.Split(strings.TrimSpace(s), ",")
	if len(parts) != 2 {
		return msawPoint{}, fmt.Errorf("invalid DMS point %q", s)
	}
	parse := func(x string, positive, negative byte) (float64, error) {
		if len(x) < 2 || (x[0] != positive && x[0] != negative) {
			return 0, fmt.Errorf("invalid DMS coordinate %q", x)
		}
		v := strings.Split(x[1:], ".")
		if len(v) != 4 {
			return 0, fmt.Errorf("invalid DMS precision %q", x)
		}
		d, err := strconv.ParseFloat(v[0], 64)
		if err != nil {
			return 0, err
		}
		m, err := strconv.ParseFloat(v[1], 64)
		if err != nil {
			return 0, err
		}
		sec, err := strconv.ParseFloat(v[2]+"."+v[3], 64)
		if err != nil {
			return 0, err
		}
		if m >= 60 || sec >= 60 {
			return 0, fmt.Errorf("invalid DMS minutes/seconds in %q", x)
		}
		z := d + m/60 + sec/3600
		if x[0] == negative {
			z = -z
		}
		return z, nil
	}
	lat, err := parse(parts[0], 'N', 'S')
	if err != nil {
		return msawPoint{}, err
	}
	lon, err := parse(parts[1], 'E', 'W')
	if err != nil {
		return msawPoint{}, err
	}
	p := msawPoint{lat, lon}
	if !msawValidPoint(p) {
		return msawPoint{}, fmt.Errorf("invalid DMS point %q", s)
	}
	return p, nil
}

func msawParseVertexList(raw json.RawMessage) (msawRing, error) {
	var stringsList []string
	if err := json.Unmarshal(raw, &stringsList); err != nil {
		var text string
		if e := json.Unmarshal(raw, &text); e != nil {
			return nil, fmt.Errorf("decode vertex list: %w", err)
		}
		stringsList = strings.Fields(text)
	}
	points := make(msawRing, 0, len(stringsList))
	for _, s := range stringsList {
		p, err := msawParseDMSCoordinate(s)
		if err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	if len(points) < 3 {
		return nil, errors.New("inhibit polygon has too few vertices")
	}
	return points, nil
}

func msawParseInhibitZones(zones []msawInhibitZone, airports map[string]configPoint) ([]msawRegion, error) {
	result := make([]msawRegion, 0, len(zones))
	for _, zone := range zones {
		if zone.Floor > zone.Ceiling {
			return nil, fmt.Errorf("MSAW inhibit %s: floor above ceiling", zone.ID)
		}
		r := msawRegion{floor: zone.Floor, ceiling: zone.Ceiling}
		switch zone.Type {
		case "circle":
			if zone.Radius <= 0 || zone.Radius > 500 {
				return nil, fmt.Errorf("MSAW inhibit %s: invalid radius", zone.ID)
			}
			center, ok := airports[strings.ToUpper(strings.TrimSpace(zone.Center))]
			if !ok {
				return nil, fmt.Errorf("MSAW inhibit %s: unknown airport center %q", zone.ID, zone.Center)
			}
			r.center = msawPoint{center.Lat, center.Lon}
			r.radiusNM = zone.Radius
		case "polygon":
			ring, err := msawParseVertexList(zone.Vertices)
			if err != nil {
				return nil, fmt.Errorf("MSAW inhibit %s: %w", zone.ID, err)
			}
			rings := []msawRing{ring}
			if len(zone.Holes) > 0 && string(zone.Holes) != "null" {
				var holes []json.RawMessage
				if err := json.Unmarshal(zone.Holes, &holes); err != nil {
					return nil, fmt.Errorf("MSAW inhibit %s: invalid holes: %w", zone.ID, err)
				}
				for _, hole := range holes {
					r, err := msawParseVertexList(hole)
					if err != nil {
						return nil, err
					}
					rings = append(rings, r)
				}
			}
			poly, err := msawPolygonFromRings(1, rings)
			if err != nil {
				return nil, err
			}
			r.polygon = &poly
		default:
			return nil, fmt.Errorf("MSAW inhibit %s: unsupported shape %q", zone.ID, zone.Type)
		}
		result = append(result, r)
	}
	return result, nil
}

// Streaming decoder: zstd feeds the JSON decoder, avoiding a second copy of
// the complete decompressed GeoJSON buffer in RAM.
func msawReadGeoJSON(r io.Reader, facility string) ([]msawPolygon, error) {
	var fc struct {
		Type     string `json:"type"`
		Features []struct {
			Type       string `json:"type"`
			Properties struct {
				Facility  string `json:"facility"`
				Minimum   int    `json:"minimum_ft"`
				Reference string `json:"reference"`
			} `json:"properties"`
			Geometry struct {
				Type        string        `json:"type"`
				Coordinates [][][]float64 `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.NewDecoder(r).Decode(&fc); err != nil {
		return nil, err
	}
	if fc.Type != "FeatureCollection" || len(fc.Features) == 0 {
		return nil, errors.New("missing MVA GeoJSON features")
	}
	polygons := make([]msawPolygon, 0, len(fc.Features))
	for i, f := range fc.Features {
		if f.Type != "Feature" || f.Geometry.Type != "Polygon" || !strings.EqualFold(f.Properties.Facility, facility) || f.Properties.Minimum <= 0 || !strings.EqualFold(f.Properties.Reference, "MSL") {
			return nil, fmt.Errorf("invalid MVA feature %d (facility, type or reference)", i)
		}
		rings := make([]msawRing, 0, len(f.Geometry.Coordinates))
		for _, ring := range f.Geometry.Coordinates {
			points := make(msawRing, 0, len(ring))
			for _, c := range ring {
				if len(c) < 2 {
					return nil, fmt.Errorf("MVA feature %d: coordinate lacks lon/lat", i)
				}
				points = append(points, msawPoint{lat: c[1], lon: c[0]})
			}
			rings = append(rings, points)
		}
		poly, err := msawPolygonFromRings(f.Properties.Minimum, rings)
		if err != nil {
			return nil, fmt.Errorf("MVA feature %d: %w", i, err)
		}
		polygons = append(polygons, poly)
	}
	return polygons, nil
}

func loadMSAWAirspace(cfg selectedConfig) (*msawAirspace, error) {
	facility := cfg.Facility.Facility
	path := filepath.ToSlash(filepath.Join("resources", "mva", facility+".geojson.zst"))
	if !util.ResourceExists(path) {
		return nil, fmt.Errorf("MVA file %s not installed", path)
	}
	f, err := os.Open(util.FindProjectRelativeFile(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	polygons, err := msawReadGeoJSON(zr, facility)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	// If the original source was not merged, the absence of inhibit zones
	// cannot be interpreted as "no inhibits". Disable the detector instead.
	if cfg.Facility.InhibitZones.MSAW == nil {
		return nil, errors.New("missing inhibitZones.msaw adaptation")
	}
	var airports map[string]configPoint
	if len(cfg.Facility.InhibitZones.MSAW) > 0 {
		db, e := loadSTARSAirportDatabase()
		if e != nil {
			return nil, e
		}
		airports = db.positionByID
	}
	inhibits, err := msawParseInhibitZones(cfg.Facility.InhibitZones.MSAW, airports)
	if err != nil {
		return nil, err
	}
	return &msawAirspace{polygons: polygons, inhibit: inhibits}, nil
}

// Q (§7.14) suppresses only the current MSAW event; V (§7.15) toggles
// processing for a track. These are operational overrides, never PREF data.
// A facility-local shared state makes the commands visible to all open TCWs
// in this REDS process. A future server-side alert engine can distribute
// this state to clients on other machines.
type msawTrackOverride struct {
	Identity        starsTrackHighlightIdentity
	OwnerTCP        string
	EventSuppressed bool // MULTI FUNC Q: reset at end of this MSAW event
	Disabled        bool // MULTI FUNC V: persists until re-enabled/track change
	Generation      uint64
}

type msawSiteRuntimeState struct {
	sync.RWMutex
	tracks     map[string]msawTrackOverride
	generation uint64
}

var sharedMSAWSites = struct {
	sync.Mutex
	sites map[string]*msawSiteRuntimeState
}{sites: make(map[string]*msawSiteRuntimeState)}

func sharedMSAWSite(cfg selectedConfig) *msawSiteRuntimeState {
	key := atpaSiteKey(cfg)
	sharedMSAWSites.Lock()
	defer sharedMSAWSites.Unlock()
	site := sharedMSAWSites.sites[key]
	if site == nil {
		site = &msawSiteRuntimeState{tracks: make(map[string]msawTrackOverride)}
		sharedMSAWSites.sites[key] = site
	}
	return site
}

func (p *STARSPane) msawSiteState() *msawSiteRuntimeState {
	if p == nil {
		return nil
	}
	if p.msawSite == nil {
		p.msawSite = sharedMSAWSite(p.config)
	}
	return p.msawSite
}

// msawAutomaticVFRInhibited classifies VFR tracks independently of the manual
// Q/V overrides. The explicit flight-plan rules win over a 1200 squawk, which
// may be stale or inconsistent with an IFR flight plan. An unassociated 1200
// track is also inhibited, but has no ACID in which to display an asterisk.
// Unknown flight rules are not interpreted as VFR unless the *reported* Mode
// 3/A code is 1200; an assigned code alone does not establish VFR status.
func msawAutomaticVFRInhibited(t *redsnet.TaisTarget) bool {
	if t == nil {
		return false
	}
	if fp := t.FlightPlan; fp != nil {
		rules := strings.ToUpper(strings.TrimSpace(fp.FlightRules))
		raw := strings.ToUpper(strings.TrimSpace(fp.RawFlightRules))
		// VFR-on-top remains an IFR clearance; do not treat the substring
		// "VFR" as a general indicator of VFR flight rules.
		if rules == "I" || rules == "IFR" || rules == "VFR ON TOP" || rules == "VFR-ON-TOP" || raw == "I" {
			return false
		}
		if rules == "V" || rules == "VFR" || raw == "V" {
			return true
		}
	}
	return normalizeBeaconCode(t.Track.ReportedBeaconCode) == "1200"
}

// Keep automatic inhibition separate from the explicit manual override so a
// VFR->IFR transition removes only the automatic asterisk. This predicate is
// shared by the LA state machine, list filtering, and datablock rendering.
func (p *STARSPane) msawInhibited(t *redsnet.TaisTarget) bool {
	return msawAutomaticVFRInhibited(t) || p.msawManuallyInhibited(t)
}

// No per-frame heap allocation: overrides are marked with the sweep stamp
// while the site's lock is held, then stale entries are removed in-place.
func (s *msawSiteRuntimeState) observe(key string, id starsTrackHighlightIdentity, owner string, natural bool, eligible bool, vfr bool, generation uint64) msawTrackOverride {
	o, ok := s.tracks[key]
	if ok && (!trackHighlightIdentityMatches(o.Identity, id) || (o.OwnerTCP != "" && owner != o.OwnerTCP && !vfr)) {
		delete(s.tracks, key)
		ok = false
	}
	if !ok {
		return msawTrackOverride{}
	}
	o.Generation = generation
	if !natural && (eligible || !vfr) {
		o.EventSuppressed = false
	}
	o.OwnerTCP = owner
	if !o.EventSuppressed && !o.Disabled {
		delete(s.tracks, key) // no outstanding override
	} else {
		s.tracks[key] = o
	}
	return o
}

// An asterisk belongs to the ACID, even when Q has removed the LA indicator.
func (p *STARSPane) msawManuallyInhibited(t *redsnet.TaisTarget) bool {
	if p == nil || t == nil {
		return false
	}
	site := p.msawSiteState()
	key := targetDisplayStateKey(t)
	site.RLock()
	o, ok := site.tracks[key]
	site.RUnlock()
	return ok && trackHighlightIdentityMatches(o.Identity, trackHighlightIdentity(t)) && (o.EventSuppressed || o.Disabled)
}

// §7.14: only an active, owned, non-frozen MSAW may be suppressed.
// The event remains detected, but its LA/list entry disappears immediately.
func (p *STARSPane) inhibitCurrentMSAW(t *redsnet.TaisTarget) error {
	if t == nil {
		return ErrSTARSNoTrack
	}
	if t.Track.Frozen || t.FlightPlan == nil || !p.targetOwnedByCurrentTCP(t) {
		return ErrSTARSIllegalTrack
	}
	if _, ok := p.msawAlert(t); !ok {
		return ErrSTARSIllegalTrack
	}
	site := p.msawSiteState()
	key := targetDisplayStateKey(t)
	site.Lock()
	o := site.tracks[key]
	o.Identity = trackHighlightIdentity(t)
	o.OwnerTCP, _ = p.targetOwnerTCP(t)
	o.EventSuppressed = true
	site.tracks[key] = o
	site.Unlock()
	return nil
}

// §7.15: unlike Q, V works without an active warning. It remains in effect
// for the associated track until toggled back or the track loses eligibility.
func (p *STARSPane) toggleTrackMSAW(t *redsnet.TaisTarget) error {
	if t == nil {
		return ErrSTARSNoTrack
	}
	if t.Track.Frozen || t.FlightPlan == nil || !p.targetOwnedByCurrentTCP(t) {
		return ErrSTARSIllegalTrack
	}
	if p.msawAirspace == nil {
		return ErrSTARSMSAWOff
	}
	site := p.msawSiteState()
	key := targetDisplayStateKey(t)
	site.Lock()
	o := site.tracks[key]
	if !trackHighlightIdentityMatches(o.Identity, trackHighlightIdentity(t)) {
		o = msawTrackOverride{}
	}
	o.Identity = trackHighlightIdentity(t)
	o.OwnerTCP, _ = p.targetOwnerTCP(t)
	o.Disabled = !o.Disabled
	o.EventSuppressed = false
	if o.Disabled {
		site.tracks[key] = o
	} else {
		delete(site.tracks, key)
	}
	site.Unlock()
	// Already displayed LA is suppressed immediately when MSAW is disabled.
	return nil
}

type msawAlertState struct {
	Identity     starsTrackHighlightIdentity
	Since        time.Time
	Acknowledged bool
	Generation   uint64 // sweep stamp; avoids a second per-frame map
}

// updateMSAW maintains edge-triggered, TCW-local alert/acknowledgment state.
// Only locally owned tracks can generate new LA indications in this first
// conservative implementation; TAIS lacks VICE's MVAsApply eligibility flag.
// Q and V inhibit flags are process-wide per facility, guarded by site lock.
// A recycled track key cannot inherit an override or acknowledgement.
func (p *STARSPane) updateMSAW(snapshot redsnet.TaisSnapshot, now time.Time) {
	if p == nil {
		return
	}
	if p.msawAirspace == nil || !snapshot.Ready {
		clear(p.msawAlerts)
		clear(p.msawApproachTracks)
		return
	}
	if p.msawAlerts == nil {
		p.msawAlerts = make(map[string]msawAlertState)
	}
	p.msawGeneration++
	generation := p.msawGeneration
	site := p.msawSiteState()
	site.Lock()
	site.generation++
	siteGeneration := site.generation
	for i := range snapshot.Targets {
		t := &snapshot.Targets[i]
		key := targetDisplayStateKey(t)
		if key == "" {
			continue
		}
		identity := trackHighlightIdentity(t)
		owner, ownerKnown := p.targetOwnerTCP(t)
		ownedHere := ownerKnown && p.ownTCP() != "" && strings.EqualFold(owner, p.ownTCP())
		eligible := msawTrackEligible(t, now)
		// TAIS has no equivalent of VICE's MVAsApply flag (which marks
		// aircraft for which an MVA check is meaningful). Until REDS has
		// that adaptation, only issue a locally computed LA for tracks
		// actually owned by the current TCP. A facility-wide sweep of all
		// associated TAIS tracks produces a flood of false local alerts.
		//
		// Still evaluate a track with a pending Q override in other TCWs:
		// that system-wide, event-scoped override must expire when the
		// underlying warning condition clears, regardless of which TCW
		// currently observes the flight.
		override, hasOverride := site.tracks[key]
		checkMVA := ownedHere || (hasOverride && override.EventSuppressed)
		autoVFR := msawAutomaticVFRInhibited(t)
		natural := false
		// Automatic VFR inhibition takes precedence over the MVA and avoids
		// unnecessary spatial searches for 1200/VFR targets.
		if eligible && checkMVA && !autoVFR {
			at := msawPoint{t.Track.Lat, t.Track.Lon}
			mva, covered := p.msawAirspace.mvaAt(at)
			alt := t.Track.ReportedAltitude
			natural = covered && alt < mva && !p.msawAirspace.inhibited(at, alt)
			// Match the published FAF-to-runway trajectory only after proving
			// that this report would otherwise create a low-altitude warning.
			// Do not infer approach clearance from the flight plan alone.
			if natural && (p.msawInitialClimb(t) || p.msawEstablishedApproach(t, generation)) {
				natural = false
			}
		}
		o := site.observe(key, identity, owner, natural, eligible, autoVFR, siteGeneration)
		if !ownedHere || !eligible || !natural || autoVFR || o.Disabled {
			continue
		}
		if old, exists := p.msawAlerts[key]; !exists || !trackHighlightIdentityMatches(old.Identity, identity) {
			p.msawAlerts[key] = msawAlertState{Identity: identity, Since: now, Generation: generation}
		} else {
			old.Generation = generation
			p.msawAlerts[key] = old
		}
	}
	for key, state := range site.tracks {
		if state.Generation != siteGeneration {
			delete(site.tracks, key)
		}
	}
	site.Unlock()
	p.pruneMSAWApproachStates(generation)
	for key, state := range p.msawAlerts {
		if state.Generation != generation {
			delete(p.msawAlerts, key)
		}
	}
}

func msawTrackEligible(t *redsnet.TaisTarget, now time.Time) bool {
	if t == nil || t.FlightPlan == nil || t.FlightPlan.Deleted || t.FlightPlan.Suspended || t.CoastPhase >= redsnet.TaisCoastPhase2 || !taisTargetHasPosition(t) || !t.Track.UsableReportedAltitude() {
		return false
	}
	if t.Track.MRTTime.After(now.Add(2*time.Second)) || now.Sub(t.Track.MRTTime) > msawMaximumTrackAge {
		return false
	}
	// TAIS provides no pilot-reported altitude; do not substitute assigned
	// or requested altitude. Detection ignores operator altitude filters.
	return true
}

func (p *STARSPane) msawAlert(t *redsnet.TaisTarget) (msawAlertState, bool) {
	if p == nil || t == nil {
		return msawAlertState{}, false
	}
	state, ok := p.msawAlerts[targetDisplayStateKey(t)]
	if !ok || !trackHighlightIdentityMatches(state.Identity, trackHighlightIdentity(t)) || p.msawInhibited(t) {
		return msawAlertState{}, false
	}
	return state, true
}

// §7.3: MSAW acknowledgment is local to this TCW and does not suppress
// computation or remove the alert from the list.
func (p *STARSPane) acknowledgeMSAWBySlew(t *redsnet.TaisTarget) bool {
	state, ok := p.msawAlert(t)
	if !ok || state.Acknowledged {
		return false
	}
	state.Acknowledged = true
	p.msawAlerts[targetDisplayStateKey(t)] = state
	return true
}

// REDS-only inference from published approach geometry. This is NOT an FAA
// MSAW algorithm and does not establish an ATC approach clearance. Only
// coded FAF-to-runway TF/CF and validated RF arcs are eligible. Missed
// approaches, unresolved arcs and unsupported legs are excluded at parse time.
const (
	msawApproachMaxCrossTrackNM = 0.35
	msawApproachMinProgressNM   = 0.12
	msawApproachMinDuration     = 7 * time.Second
	msawApproachMaxGap          = 30 * time.Second
	msawApproachMaxAngleDeg     = 20
	msawApproachBelowPathFeet   = 250
	msawApproachAbovePathFeet   = 900
)

type msawApproachObservation struct {
	identity                      starsTrackHighlightIdentity
	key                           string // airport + procedure + runway
	start, last                   time.Time
	startPos                      msawPoint
	startRemaining, lastRemaining float64
	generation                    uint64
}

// Shared, immutable navigation index, decoded once in the background rather
// than parsing a 51 MB CIFP file on the STARS draw thread or for every TCW.
var msawApproachIndex = struct {
	once  sync.Once
	ready chan struct{}
	data  map[string][]aviation.CIFPFinalApproach
	err   error
}{ready: make(chan struct{})}

func startMSAWApproachIndex() {
	msawApproachIndex.once.Do(func() {
		go func() {
			msawApproachIndex.data, msawApproachIndex.err = aviation.LoadCIFPFinalApproaches()
			close(msawApproachIndex.ready) // Publishes the immutable index and error.
		}()
	})
}

func (p *STARSPane) updateMSAWApproachIndex() {
	if p == nil || p.msawApproachIndexLogged {
		return
	}
	select {
	case <-msawApproachIndex.ready:
		p.msawApproachIndexLogged = true
		if msawApproachIndex.err != nil {
			p.logger.Warn("CIFP MSAW approach inference disabled; normal LA processing remains active", "error", msawApproachIndex.err)
		} else {
			p.msawApproaches = msawApproachIndex.data
			n := 0
			for _, approaches := range p.msawApproaches {
				n += len(approaches)
			}
			p.logger.Info("CIFP MSAW approaches loaded", "airports", len(p.msawApproaches), "final_approaches", n)
		}
	default:
	}
}

// msawApproachAirport deliberately requires destination evidence. A reported
// runway alone or a geographically adjacent airport is not enough. FlightPlan
// Airport is used only in the absence of enhanced destination data and not
// when the flight is marked as a departure.
func msawApproachAirport(t *redsnet.TaisTarget) string {
	if t == nil || t.FlightPlan == nil || taisFlightPlanIsDeparture(t) {
		return ""
	}
	// Suppression needs positive IFR evidence, not merely the absence of a
	// VFR indication. Unknown flight rules continue receiving normal LA.
	rules := strings.ToUpper(strings.TrimSpace(t.FlightPlan.FlightRules))
	raw := strings.ToUpper(strings.TrimSpace(t.FlightPlan.RawFlightRules))
	if rules != "I" && rules != "IFR" && rules != "VFR ON TOP" && rules != "VFR-ON-TOP" && raw != "I" {
		return ""
	}
	airport := ""
	if t.EnhancedData != nil {
		airport = strings.ToUpper(strings.TrimSpace(t.EnhancedData.DestinationAirport))
	}
	if airport == "" {
		airport = strings.ToUpper(strings.TrimSpace(t.FlightPlan.Airport))
	}
	if len(airport) == 4 {
		return airport
	}
	if len(airport) != 3 {
		return ""
	}
	if db, err := loadSTARSAirportDatabase(); err == nil {
		if icao := db.byIATA[airport]; icao != "" {
			return icao
		}
	}
	return "K" + airport
}

// Work in a local tangent plane (east/north, nautical miles). RF geometry is
// projected about the *coded RF center*, not approximated with a chord.
func msawApproachXY(origin aviation.Point, at msawPoint) (x, y float64) {
	cosLat := math.Cos(origin.Lat * math.Pi / 180)
	return (at.lon - origin.Lon) * 60 * cosLat, (at.lat - origin.Lat) * 60
}

func msawApproachAngle(from, to float64, clockwise bool) float64 {
	if clockwise {
		return math.Mod(from-to+2*math.Pi, 2*math.Pi)
	}
	return math.Mod(to-from+2*math.Pi, 2*math.Pi)
}

func msawApproachLeg(a aviation.CIFPFinalApproach, i int) aviation.CIFPFinalLeg {
	if len(a.Legs) == len(a.Points)-1 {
		return a.Legs[i]
	}
	// Existing synthetic/test finals created before coded legs were available.
	return aviation.CIFPFinalLeg{Type: "TF"}
}

func msawApproachLegLength(a aviation.CIFPFinalApproach, i int) float64 {
	from, to := a.Points[i], a.Points[i+1]
	leg := msawApproachLeg(a, i)
	if leg.Type == "RF" {
		x0, y0 := msawApproachXY(leg.Center, msawPoint{from.Lat, from.Lon})
		x1, y1 := msawApproachXY(leg.Center, msawPoint{to.Lat, to.Lon})
		return msawApproachAngle(math.Atan2(y0, x0), math.Atan2(y1, x1), leg.Clockwise) * leg.RadiusNM
	}
	return math.Hypot((to.Lon-from.Lon)*60*math.Cos((to.Lat+from.Lat)*math.Pi/360), (to.Lat-from.Lat)*60)
}

// Projection returns distance along the leg, lateral error and unit tangent
// at the observed position. A point outside the coded arc sweep is rejected,
// even if it lies on the same circle (an essential RF-vs-radius distinction).
func msawApproachProjectLeg(a aviation.CIFPFinalApproach, i int, p msawPoint) (along, cross, tx, ty float64, ok bool) {
	from, to := a.Points[i], a.Points[i+1]
	leg := msawApproachLeg(a, i)
	if leg.Type == "RF" {
		if leg.RadiusNM <= 0 {
			return
		}
		x0, y0 := msawApproachXY(leg.Center, msawPoint{from.Lat, from.Lon})
		x1, y1 := msawApproachXY(leg.Center, msawPoint{to.Lat, to.Lon})
		x, y := msawApproachXY(leg.Center, p)
		start, finish := math.Atan2(y0, x0), math.Atan2(y1, x1)
		span := msawApproachAngle(start, finish, leg.Clockwise)
		progress := msawApproachAngle(start, math.Atan2(y, x), leg.Clockwise)
		if span < 0.01 || span > math.Pi || progress > span+0.01 {
			return
		}
		along = math.Min(progress, span) * leg.RadiusNM
		cross = math.Abs(math.Hypot(x, y) - leg.RadiusNM)
		angle := math.Atan2(y, x)
		if leg.Clockwise {
			tx, ty = math.Sin(angle), -math.Cos(angle)
		} else {
			tx, ty = -math.Sin(angle), math.Cos(angle)
		}
		return along, cross, tx, ty, true
	}
	cosLat := math.Cos((from.Lat + to.Lat) * math.Pi / 360)
	dx, dy := (to.Lon-from.Lon)*60*cosLat, (to.Lat-from.Lat)*60
	x, y := (p.lon-from.Lon)*60*cosLat, (p.lat-from.Lat)*60
	length := math.Hypot(dx, dy)
	if length < 0.05 {
		return
	}
	fraction := (x*dx + y*dy) / (length * length)
	if fraction < -0.02 || fraction > 1.02 {
		return
	}
	fraction = math.Max(0, math.Min(1, fraction))
	return fraction * length, math.Hypot(x-fraction*dx, y-fraction*dy), dx / length, dy / length, true
}

type msawApproachSample struct {
	remaining, cross, courseX, courseY float64
	legIndex                           int
	alongNM                            float64 // distance from FAF to observed position
}

func msawApproachProjection(a aviation.CIFPFinalApproach, p msawPoint) (msawApproachSample, bool) {
	if len(a.Points) < 2 || a.FAFAltitude <= a.RunwayAltitude {
		return msawApproachSample{}, false
	}
	total := 0.0
	for i := 0; i < len(a.Points)-1; i++ {
		total += msawApproachLegLength(a, i)
	}
	if total < 0.5 {
		return msawApproachSample{}, false
	}
	best := msawApproachSample{cross: math.Inf(1)}
	before := 0.0
	for i := 0; i < len(a.Points)-1; i++ {
		length := msawApproachLegLength(a, i)
		along, cross, tx, ty, ok := msawApproachProjectLeg(a, i, p)
		if ok && cross < best.cross {
			best = msawApproachSample{
				remaining: total - before - along, cross: cross, courseX: tx, courseY: ty,
				legIndex: i, alongNM: before + along,
			}
		}
		before += length
	}
	if best.cross > msawApproachMaxCrossTrackNM {
		return msawApproachSample{}, false
	}
	return best, true
}

func msawApproachConstraintReference(a aviation.CIFPFinalApproach, i int) (int, bool) {
	if i == 0 {
		return a.FAFAltitude, true
	}
	if i == len(a.Points)-1 {
		return a.RunwayAltitude, true
	}
	if len(a.Constraints) != len(a.Points) {
		return 0, false
	}
	c := a.Constraints[i]
	if c.GlidepathAltitude > 0 {
		return c.GlidepathAltitude, true
	}
	if c.HasMin {
		return c.MinFeet, true
	}
	if c.HasMax {
		return c.MaxFeet, true
	}
	return 0, false
}

// Check every *coded* restriction, not only the FAF altitude. On a descending
// final, an aircraft must remain above an upcoming fix's minimum, and below a
// previously passed fix's maximum. Both bounds apply near the fix. Altitude
// quantization allows 100 ft on a bound; this is a REDS tolerance, not FAA
// authorization to descend below a published restriction.
//
// For the plausibility envelope interpolate between the nearest coded altitude
// references (including glidepath altitudes when present), rather than one
// arbitrary straight line between FAF and runway.
func msawApproachAltitudeOK(a aviation.CIFPFinalApproach, sample msawApproachSample, altitude int) bool {
	total := sample.alongNM + sample.remaining
	if total < 0.5 {
		return false
	}
	// Existing hand-built finals have no coded restrictions; preserve their
	// former behavior instead of manufacturing restrictions from empty data.
	if len(a.Constraints) != len(a.Points) {
		expected := float64(a.RunwayAltitude) + float64(a.FAFAltitude-a.RunwayAltitude)*sample.remaining/total
		return float64(altitude) >= expected-msawApproachBelowPathFeet &&
			float64(altitude) <= expected+msawApproachAbovePathFeet
	}
	const boundToleranceFeet = 100
	const fixToleranceNM = 0.12
	before := 0.0
	for i, c := range a.Constraints {
		if c.HasMin && sample.alongNM <= before+fixToleranceNM && altitude < c.MinFeet-boundToleranceFeet {
			return false
		}
		if c.HasMax && sample.alongNM >= before-fixToleranceNM && altitude > c.MaxFeet+boundToleranceFeet {
			return false
		}
		if i < len(a.Legs) {
			before += msawApproachLegLength(a, i)
		}
	}
	// Find the nearest vertical references enclosing the aircraft's point.
	// Missing intermediate constraints inherit the enclosing envelope.
	leftPos, rightPos := 0.0, total
	leftAlt, rightAlt := float64(a.FAFAltitude), float64(a.RunwayAltitude)
	before = 0
	for i := range a.Points {
		if h, ok := msawApproachConstraintReference(a, i); ok {
			if before <= sample.alongNM {
				leftPos, leftAlt = before, float64(h)
			} else if before < rightPos {
				rightPos, rightAlt = before, float64(h)
			}
		}
		if i < len(a.Legs) {
			before += msawApproachLegLength(a, i)
		}
	}
	if rightPos-leftPos < 1e-6 {
		return false
	}
	fraction := math.Max(0, math.Min(1, (sample.alongNM-leftPos)/(rightPos-leftPos)))
	expected := leftAlt + (rightAlt-leftAlt)*fraction
	return float64(altitude) >= expected-msawApproachBelowPathFeet &&
		float64(altitude) <= expected+msawApproachAbovePathFeet
}

func msawApproachPosition(a aviation.CIFPFinalApproach, p msawPoint, altitude int) (remaining, crossNM float64, ok bool) {
	sample, found := msawApproachProjection(a, p)
	if !found || !msawApproachAltitudeOK(a, sample, altitude) {
		return 0, 0, false
	}
	return sample.remaining, sample.cross, true
}

func msawApproachCourseAligned(start, end msawPoint, a aviation.CIFPFinalApproach) bool {
	// Use the actual *local tangent*, not the FAF-to-runway chord, which
	// points in the wrong direction on a curved RF approach.
	sample, ok := msawApproachProjection(a, end)
	if !ok {
		return false
	}
	cosLat := math.Cos((start.lat + end.lat) * math.Pi / 360)
	ax, ay := (end.lon-start.lon)*cosLat, end.lat-start.lat
	an := math.Hypot(ax, ay)
	return an > 1e-9 && (ax*sample.courseX+ay*sample.courseY)/an >=
		math.Cos(msawApproachMaxAngleDeg*math.Pi/180)
}

// Called only for tracks that are otherwise eligible for a *local* MSAW.
// Matching is stateful across distinct surveillance reports, not draw frames:
// repeated Draw calls with the same MRT timestamp cannot establish a track.
func (p *STARSPane) msawEstablishedApproach(t *redsnet.TaisTarget, generation uint64) bool {
	if p == nil || t == nil || p.msawApproaches == nil {
		return false
	}
	key := targetDisplayStateKey(t)
	if key == "" {
		return false
	}
	if p.msawApproachTracks == nil {
		p.msawApproachTracks = make(map[string]msawApproachObservation)
	}
	airport := msawApproachAirport(t)
	if airport == "" {
		delete(p.msawApproachTracks, key)
		return false
	}
	assigned := normalizeATPARunway(t.FlightPlan.Runway)
	at := msawPoint{t.Track.Lat, t.Track.Lon}
	alt := t.Track.ReportedAltitude
	var candidate aviation.CIFPFinalApproach
	bestCross := math.Inf(1)
	bestRemaining := 0.0
	for _, approach := range p.msawApproaches[airport] {
		if assigned != "" && assigned != normalizeATPARunway(approach.Runway) {
			continue
		}
		remaining, cross, ok := msawApproachPosition(approach, at, alt)
		if ok && cross < bestCross {
			candidate, bestCross, bestRemaining = approach, cross, remaining
		}
	}
	if math.IsInf(bestCross, 1) {
		delete(p.msawApproachTracks, key)
		return false
	}
	candidateKey := candidate.Airport + "/" + candidate.ID + "/" + candidate.Runway
	identity := trackHighlightIdentity(t)
	when := t.Track.MRTTime
	previous, exists := p.msawApproachTracks[key]
	if !exists || !trackHighlightIdentityMatches(previous.identity, identity) || previous.key != candidateKey || when.Before(previous.last) || when.Sub(previous.last) > msawApproachMaxGap {
		state := msawApproachObservation{
			identity: identity, key: candidateKey, start: when, last: when, startPos: at,
			startRemaining: bestRemaining, lastRemaining: bestRemaining, generation: generation,
		}
		// When the pane first opens, prefer genuine historical positions to
		// temporarily displaying LA on already-established approaches. The
		// reports must carry distinct timestamps, move toward this runway,
		// and individually satisfy the same final-path altitude envelope.
		for i := len(t.History) - 1; i >= 0; i-- {
			h := t.History[i]
			age := when.Sub(h.Time)
			if age < msawApproachMinDuration || age > msawApproachMaxGap {
				continue
			}
			old := msawPoint{lat: h.Lat, lon: h.Lon}
			oldRemaining, _, matched := msawApproachPosition(candidate, old, h.ReportedAltitude)
			if matched && oldRemaining-bestRemaining >= msawApproachMinProgressNM && msawApproachCourseAligned(old, at, candidate) {
				state.start, state.startPos, state.startRemaining = h.Time, old, oldRemaining
				break
			}
		}
		p.msawApproachTracks[key] = state
		return state.startRemaining-bestRemaining >= msawApproachMinProgressNM &&
			when.Sub(state.start) >= msawApproachMinDuration
	}
	previous.generation = generation
	if when.After(previous.last) {
		if bestRemaining > previous.lastRemaining+0.08 { // Moving away from the runway.
			previous.start, previous.startPos, previous.startRemaining = when, at, bestRemaining
		}
		previous.last, previous.lastRemaining = when, bestRemaining
	}
	p.msawApproachTracks[key] = previous
	return when.Sub(previous.start) >= msawApproachMinDuration &&
		previous.startRemaining-bestRemaining >= msawApproachMinProgressNM &&
		msawApproachCourseAligned(previous.startPos, at, candidate)
}

func (p *STARSPane) pruneMSAWApproachStates(generation uint64) {
	for key, state := range p.msawApproachTracks {
		if state.generation != generation {
			delete(p.msawApproachTracks, key)
		}
	}
}

// This is a REDS-only initial-departure/go-around heuristic, NOT a certified
// STARS MSAW algorithm. We suppress current-MVA alerts only when a short,
// timestamped trajectory shows a sustained climb near a runway. Neither a
// flight-plan departure flag nor an instantaneous positive vertical rate is
// sufficient on its own.
const (
	msawInitialClimbRadiusNM    = 3.0
	msawInitialClimbHistoryMax  = 30 * time.Second
	msawInitialClimbHistoryMin  = 8 * time.Second
	msawInitialClimbMinGainFeet = 120
	msawInitialClimbMinRateFPM  = 300
	msawInitialClimbMinMoveNM   = 0.08
)

type msawClimbEvidence struct {
	first    msawPoint
	gainFeet int
	rateFPM  float64
	movedNM  float64
}

// Fit an ordinary least-squares slope over the available recent Mode-C
// history, using the track's *measurement* time rather than wall-clock time.
// O(1) extra RAM and O(len(history)) work. A positive instantaneous vertical
// rate alone is not adequate evidence; missing/short history fails closed.
func msawClimbingHistory(t *redsnet.TaisTarget) (msawClimbEvidence, bool) {
	if t == nil || t.Track.MRTTime.IsZero() || !t.Track.UsableReportedAltitude() ||
		!msawValidPoint(msawPoint{t.Track.Lat, t.Track.Lon}) || len(t.History) == 0 {
		return msawClimbEvidence{}, false
	}
	current := msawPoint{t.Track.Lat, t.Track.Lon}
	when := t.Track.MRTTime
	altitude := t.Track.ReportedAltitude
	// Translate timestamps relative to the latest report, avoiding large
	// epoch timestamps and loss of floating-point precision.
	var n, sumX, sumY, sumXX, sumXY float64
	add := func(x, y float64) {
		n++
		sumX += x
		sumY += y
		sumXX += x * x
		sumXY += x * y
	}
	add(0, float64(altitude))
	oldestAge := 0.0
	oldestAlt := altitude
	oldestPos := current
	recentAge := 0.0
	recentAlt := altitude
	for _, h := range t.History {
		age := when.Sub(h.Time).Seconds()
		if age <= 0 || age > msawInitialClimbHistoryMax.Seconds() ||
			!msawValidPoint(msawPoint{h.Lat, h.Lon}) ||
			h.ReportedAltitude < -1500 || h.ReportedAltitude > 60000 {
			continue
		}
		add(-age, float64(h.ReportedAltitude))
		if age >= 2 && age <= 8 && age > recentAge {
			recentAge, recentAlt = age, h.ReportedAltitude
		}
		if age > oldestAge {
			oldestAge = age
			oldestAlt = h.ReportedAltitude
			oldestPos = msawPoint{h.Lat, h.Lon}
		}
		// A recently observed descent of more than one 100-ft Mode-C
		// increment disqualifies the track, even if the older trend climbs.
		if age <= 6 && h.ReportedAltitude > altitude+100 {
			return msawClimbEvidence{}, false
		}
	}
	if n < 3 || oldestAge < msawInitialClimbHistoryMin.Seconds() ||
		altitude-oldestAlt < msawInitialClimbMinGainFeet || recentAge == 0 || altitude <= recentAlt {
		return msawClimbEvidence{}, false
	}
	denom := n*sumXX - sumX*sumX
	if denom <= 1e-8 {
		return msawClimbEvidence{}, false
	}
	rate := 60 * (n*sumXY - sumX*sumY) / denom
	movement := msawDistanceNM(current, oldestPos)
	if math.IsNaN(rate) || rate < msawInitialClimbMinRateFPM || movement < msawInitialClimbMinMoveNM {
		return msawClimbEvidence{}, false
	}
	return msawClimbEvidence{
		first:    oldestPos,
		gainFeet: altitude - oldestAlt, rateFPM: rate, movedNM: movement,
	}, true
}

// Resolve both IATA/FAA 3-letter codes and 4-letter ICAO identifiers through
// the actual resource database, rather than blindly prefixing 'K' (which is
// incorrect for non-CONUS airports).
func msawClimbAirportICAO(code string, db starsAirportDatabase) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) == 4 {
		if db.byICAO[code] != "" {
			return code
		}
	} else if len(code) == 3 {
		if icao := db.byIATA[code]; icao != "" {
			return icao
		}
		if db.byICAO["K"+code] != "" {
			return "K" + code
		}
	}
	return ""
}

// FlightPlan.Airport refers to departure or destination according to the
// explicit STARS flightPlan.type. Enhanced departure/destination information,
// when provided, is authoritative for the corresponding role. Both airports
// are eligible: a climbing arrival at its destination can be a go-around.
func msawClimbAirportCandidates(t *redsnet.TaisTarget, db starsAirportDatabase) (departure, destination string) {
	if t == nil || t.FlightPlan == nil {
		return "", ""
	}
	if t.EnhancedData != nil {
		departure = msawClimbAirportICAO(t.EnhancedData.DepartureAirport, db)
		destination = msawClimbAirportICAO(t.EnhancedData.DestinationAirport, db)
	}
	status := strings.ToUpper(strings.TrimSpace(t.FlightPlan.Type))
	if departure == "" && (status == "P" || status == "D" || status == "DEP" || status == "DEPARTURE") {
		departure = msawClimbAirportICAO(t.FlightPlan.Airport, db)
	}
	if destination == "" && (status == "A" || status == "ARR" || status == "ARRIVAL") {
		destination = msawClimbAirportICAO(t.FlightPlan.Airport, db)
	}
	return departure, destination
}

// Suppress locally simulated LA only for a sustained initial departure climb
// or a plausible climbing go-around within 3 NM of a matching runway/airport.
// No asterisk is added: this is phase-of-flight applicability, not manual or
// automatic per-track MSAW inhibition. If the trend ceases or the aircraft
// leaves the vicinity, normal MVA evaluation resumes immediately.
func (p *STARSPane) msawInitialClimb(t *redsnet.TaisTarget) bool {
	if p == nil || t == nil || t.FlightPlan == nil || msawAutomaticVFRInhibited(t) {
		return false
	}
	evidence, ok := msawClimbingHistory(t)
	if !ok {
		return false
	}
	db, err := loadSTARSAirportDatabase()
	if err != nil || p.msawRunways == nil {
		return false
	}
	current := msawPoint{t.Track.Lat, t.Track.Lon}
	departure, destination := msawClimbAirportCandidates(t, db)
	if departure != "" && p.msawRunways.nearAirport(departure, current) {
		return true
	}
	if destination != "" && p.msawRunways.nearAirport(destination, current) {
		return true // Arrival climbing near destination: possible go-around.
	}
	if departure != "" || destination != "" {
		return false // Known airports do not match this location.
	}
	// Unknown-airport tracks need a near-runway origin and outward progress.
	// The airport center and ATPA thresholds are no longer used as fallbacks.
	return p.msawRunways.nearUnknownDeparture(evidence.first, current)
}

const msawRunwayResource = "resources/nav/runways.geojson.zst"

// Runways are immutable after loading. Using endpoint coordinates instead of
// retaining the GeoJSON polygons keeps the resident index small.
type msawRunway struct {
	airport, id   string
	first, second msawPoint
}

type msawRunwayCell struct{ lat, lon int }

type msawRunwayIndex struct {
	runways   []msawRunway
	byAirport map[string][]int // FAA LID and verified ICAO aliases
	byCell    map[msawRunwayCell][]int
}

var msawRunwaysOnce sync.Once
var msawRunwaysValue *msawRunwayIndex
var msawRunwaysErr error

func loadMSAWRunways() (*msawRunwayIndex, error) {
	msawRunwaysOnce.Do(func() {
		if !util.ResourceExists(msawRunwayResource) {
			msawRunwaysErr = fmt.Errorf("runway resource %s not installed", msawRunwayResource)
			return
		}
		f, err := os.Open(util.FindProjectRelativeFile(msawRunwayResource))
		if err != nil {
			msawRunwaysErr = err
			return
		}
		defer f.Close()
		zr, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
		if err != nil {
			msawRunwaysErr = err
			return
		}
		defer zr.Close()
		db, err := loadSTARSAirportDatabase()
		if err != nil {
			msawRunwaysErr = err
			return
		}
		msawRunwaysValue, msawRunwaysErr = msawReadRunways(zr, db)
	})
	return msawRunwaysValue, msawRunwaysErr
}

// Stream the nationwide GeoJSON FeatureCollection one feature at a time. A
// full json.Unmarshal would simultaneously retain thousands of runway polygon
// coordinates and properties that the MSAW proximity query never needs.
func msawReadRunways(r io.Reader, db starsAirportDatabase) (*msawRunwayIndex, error) {
	dec := json.NewDecoder(r)
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("runways: expected GeoJSON object")
	}
	idx := &msawRunwayIndex{
		byAirport: make(map[string][]int),
		byCell:    make(map[msawRunwayCell][]int),
	}
	var kind string
	seenFeatures := false
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch key {
		case "type":
			if err := dec.Decode(&kind); err != nil {
				return nil, err
			}
		case "features":
			seenFeatures = true
			if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
				return nil, errors.New("runways: expected features array")
			}
			for dec.More() {
				var feature struct {
					Type       string `json:"type"`
					Properties struct {
						Airport string  `json:"ARPT_ID"`
						ID      string  `json:"RWY_ID"`
						Lat1    float64 `json:"LAT1_DECIMAL"`
						Lon1    float64 `json:"LONG1_DECIMAL"`
						Lat2    float64 `json:"LAT2_DECIMAL"`
						Lon2    float64 `json:"LONG2_DECIMAL"`
					} `json:"properties"`
				}
				if err := dec.Decode(&feature); err != nil {
					return nil, fmt.Errorf("runways feature %d: %w", len(idx.runways), err)
				}
				p := feature.Properties
				airport := strings.ToUpper(strings.TrimSpace(p.Airport))
				a := msawPoint{p.Lat1, p.Lon1}
				b := msawPoint{p.Lat2, p.Lon2}
				if feature.Type != "Feature" || airport == "" || p.ID == "" ||
					!msawValidPoint(a) || !msawValidPoint(b) ||
					(a.lat == 0 && a.lon == 0) || (b.lat == 0 && b.lon == 0) ||
					msawDistanceNM(a, b) < 0.02 || msawDistanceNM(a, b) > 10 {
					continue // Missing or corrupt runway: do not use it for LA suppression.
				}
				i := len(idx.runways)
				idx.runways = append(idx.runways, msawRunway{airport: airport, id: p.ID, first: a, second: b})
				idx.byAirport[airport] = append(idx.byAirport[airport], i)
				// Do not assume all three-letter FAA identifiers have a K prefix.
				if icao := db.byIATA[airport]; icao != "" && icao != airport {
					idx.byAirport[icao] = append(idx.byAirport[icao], i)
				} else if len(airport) == 3 && db.byICAO["K"+airport] != "" {
					idx.byAirport["K"+airport] = append(idx.byAirport["K"+airport], i)
				}
				idx.indexCells(i)
			}
			if _, err := dec.Token(); err != nil { // closing ']'
				return nil, err
			}
		default:
			var ignored json.RawMessage
			if err := dec.Decode(&ignored); err != nil {
				return nil, err
			}
		}
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return nil, err
	}
	if kind != "FeatureCollection" || !seenFeatures || len(idx.runways) == 0 {
		return nil, errors.New("runways: no valid GeoJSON runway features")
	}
	return idx, nil
}

// One-degree cells index runway segments, expanded by the 3-NM search radius.
// This ensures that the metadata-free fallback never scans the whole US.
func (idx *msawRunwayIndex) indexCells(i int) {
	r := idx.runways[i]
	latMax := math.Max(math.Abs(r.first.lat), math.Abs(r.second.lat))
	cosLat := math.Max(0.02, math.Cos(latMax*math.Pi/180))
	latMargin := msawInitialClimbRadiusNM/60 + 0.01
	lonMargin := msawInitialClimbRadiusNM/(60*cosLat) + 0.01
	loLat := int(math.Floor(math.Min(r.first.lat, r.second.lat) - latMargin))
	hiLat := int(math.Floor(math.Max(r.first.lat, r.second.lat) + latMargin))
	loLon := int(math.Floor(math.Min(r.first.lon, r.second.lon) - lonMargin))
	hiLon := int(math.Floor(math.Max(r.first.lon, r.second.lon) + lonMargin))
	for la := loLat; la <= hiLat; la++ {
		for lo := loLon; lo <= hiLon; lo++ {
			idx.byCell[msawRunwayCell{la, lo}] = append(idx.byCell[msawRunwayCell{la, lo}], i)
		}
	}
}

// Shortest horizontal distance to the actual runway *segment*, not to its
// endpoints or the airport reference point. Project into a local EN plane
// centered on p, clamp onto the runway, then use a great-circle distance.
// Local projection is accurate at these (<=3 NM) decision distances.
func msawRunwayDistanceNM(p msawPoint, r msawRunway) float64 {
	if !msawValidPoint(p) {
		return math.Inf(1)
	}
	cosLat := math.Cos(p.lat * math.Pi / 180)
	project := func(a msawPoint) (float64, float64) {
		dlon := math.Remainder(a.lon-p.lon, 360)
		return dlon * cosLat, a.lat - p.lat
	}
	ax, ay := project(r.first)
	bx, by := project(r.second)
	dx, dy := bx-ax, by-ay
	denom := dx*dx + dy*dy
	if denom < 1e-16 {
		return math.Inf(1)
	}
	u := math.Max(0, math.Min(1, -(ax*dx+ay*dy)/denom))
	nearest := msawPoint{lat: r.first.lat + u*(r.second.lat-r.first.lat), lon: r.first.lon + u*math.Remainder(r.second.lon-r.first.lon, 360)}
	nearest.lon = math.Remainder(nearest.lon, 360)
	return msawDistanceNM(p, nearest)
}

func (idx *msawRunwayIndex) nearAirport(airport string, current msawPoint) bool {
	if idx == nil {
		return false
	}
	for _, i := range idx.byAirport[airport] {
		if msawRunwayDistanceNM(current, idx.runways[i]) <= msawInitialClimbRadiusNM {
			return true
		}
	}
	return false
}

// With missing flight-plan airport metadata, require a recent origin within
// 0.75 NM of the runway itself, outward progress, and the same sustained climb
// checks as the metadata-qualified path.
func (idx *msawRunwayIndex) nearUnknownDeparture(first, current msawPoint) bool {
	if idx == nil || !msawValidPoint(current) || !msawValidPoint(first) {
		return false
	}
	cell := msawRunwayCell{int(math.Floor(current.lat)), int(math.Floor(current.lon))}
	for _, i := range idx.byCell[cell] {
		r := idx.runways[i]
		start := msawRunwayDistanceNM(first, r)
		end := msawRunwayDistanceNM(current, r)
		if start <= 0.75 && end <= msawInitialClimbRadiusNM && end-start >= 0.12 {
			return true
		}
	}
	return false
}
