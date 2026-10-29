package stars

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// The STARS GEO RESTRICTIONS list reserves 1..100 for controller-created
// areas and 101..200 for system/adaptation areas (as in VICE). Each FAA NOTAM
// occupies ONE system ID, even when WFS contains multiple features for it.
// Rendering is delegated to tfr_render.go, but IDs and visibility remain
// managed by the existing restriction-area store and commands.
const (
	firstTFRRestrictionID = 101
	lastTFRRestrictionID  = 200
)

type tfrRestrictionSummary struct {
	key          string
	title        string
	featureCount int
	polygons     []starsTFRPolygon
}

func decodeTFRRestrictionSummaries(raw []byte) ([]tfrRestrictionSummary, error) {
	var fc struct {
		Type     string `json:"type"`
		Features []struct {
			Type       string `json:"type"`
			Properties struct {
				NOTAM string `json:"NOTAM_KEY"`
				Title string `json:"TITLE"`
			} `json:"properties"`
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &fc); err != nil {
		return nil, fmt.Errorf("decode FAA TFR list: %w", err)
	}
	if fc.Type != "FeatureCollection" || len(fc.Features) == 0 {
		return nil, fmt.Errorf("FAA TFR list requires a nonempty FeatureCollection")
	}

	byKey := make(map[string]*tfrRestrictionSummary)
	for _, feature := range fc.Features {
		key := strings.TrimSpace(feature.Properties.NOTAM)
		if feature.Type != "Feature" || key == "" {
			return nil, fmt.Errorf("FAA TFR feature missing type or NOTAM_KEY")
		}
		entry := byKey[key]
		if entry == nil {
			entry = &tfrRestrictionSummary{key: key}
			byKey[key] = entry
		}
		polygons, err := decodeTFRPolygons(feature.Geometry.Type, feature.Geometry.Coordinates)
		if err != nil {
			return nil, fmt.Errorf("FAA TFR %s: %w", key, err)
		}
		entry.polygons = append(entry.polygons, polygons...)
		entry.featureCount++
		// Use the FAA TITLE, falling back to NOTAM_KEY. Do not parse the
		// CNS_LOCATION_ID as a string: that property can be numeric.
		// GeoJSON feature IDs are not unique across polygon parts.
		if entry.title == "" {
			entry.title = strings.TrimSpace(feature.Properties.Title)
		}
	}
	result := make([]tfrRestrictionSummary, 0, len(byKey))
	for _, entry := range byKey {
		if entry.title == "" {
			entry.title = entry.key
		}
		result = append(result, *entry)
	}
	// Deterministic startup order; subsequently an existing NOTAM keeps its
	// system ID when the FAA changes order, geometry, or titles.
	sort.Slice(result, func(i, j int) bool { return result[i].key < result[j].key })
	return result, nil
}

// A polygon consists of one exterior ring followed by any interior holes.
// Never flatten rings: the shading must not cover excluded areas.
type starsTFRPolygon struct {
	Rings [][]configPoint
}

func decodeTFRPolygons(kind string, raw json.RawMessage) ([]starsTFRPolygon, error) {
	var groups [][][][]float64
	switch kind {
	case "Polygon":
		var polygon [][][]float64
		if err := json.Unmarshal(raw, &polygon); err != nil {
			return nil, err
		}
		groups = [][][][]float64{polygon}
	case "MultiPolygon":
		if err := json.Unmarshal(raw, &groups); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported TFR geometry %q", kind)
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("empty TFR geometry")
	}
	out := make([]starsTFRPolygon, 0, len(groups))
	for _, group := range groups {
		if len(group) == 0 {
			return nil, fmt.Errorf("TFR polygon has no exterior ring")
		}
		poly := starsTFRPolygon{Rings: make([][]configPoint, 0, len(group))}
		for _, ring := range group {
			if len(ring) < 4 {
				return nil, fmt.Errorf("TFR ring has fewer than four positions")
			}
			coords := make([]configPoint, 0, len(ring))
			for _, lonLat := range ring {
				if len(lonLat) != 2 || math.IsNaN(lonLat[0]) || math.IsNaN(lonLat[1]) ||
					math.IsInf(lonLat[0], 0) || math.IsInf(lonLat[1], 0) ||
					lonLat[0] < -180 || lonLat[0] > 180 || lonLat[1] < -90 || lonLat[1] > 90 {
					return nil, fmt.Errorf("invalid TFR lon/lat")
				}
				coords = append(coords, configPoint{Lat: lonLat[1], Lon: lonLat[0]})
			}
			if coords[0] != coords[len(coords)-1] {
				return nil, fmt.Errorf("unclosed TFR ring")
			}
			poly.Rings = append(poly.Rings, coords)
		}
		out = append(out, poly)
	}
	return out, nil
}

// Like VICE's AverageVertexPosition, calculate one text position from all
// exterior vertices, ignoring repeated closing points and hole boundaries.
func tfrLabelPosition(polygons []starsTFRPolygon) configPoint {
	var lat, sinLon, cosLon float64
	var n int
	for _, polygon := range polygons {
		if len(polygon.Rings) == 0 {
			continue
		}
		ring := polygon.Rings[0]
		if len(ring) > 1 && ring[0] == ring[len(ring)-1] {
			ring = ring[:len(ring)-1]
		}
		for _, pt := range ring {
			lat += pt.Lat
			sinLon += math.Sin(pt.Lon * math.Pi / 180)
			cosLon += math.Cos(pt.Lon * math.Pi / 180)
			n++
		}
	}
	if n == 0 {
		return configPoint{}
	}
	return configPoint{Lat: lat / float64(n), Lon: math.Atan2(sinLon, cosLon) * 180 / math.Pi}
}

func tfrListLabel(title string) string {
	// One list entry occupies one row; discard control characters and cap
	// the title to the STARS restriction-area text line length (32 chars).
	title = strings.ToUpper(strings.Join(strings.Fields(title), " "))
	runes := []rune(title)
	if len(runes) > 32 {
		runes = runes[:32]
	}
	return string(runes)
}

// updateTFRList atomically adds/removes read-only FAA placeholders in the
// shared site restriction store. Adaptation-defined 101..200 slots are never
// overwritten. An updated NOTAM keeps both its ID and Serial; if an ID is
// recycled for another NOTAM, Serial changes so local visibility cannot leak.
// A stale pane may not replace a newer snapshot obtained by another pane.
func (s *starsRestrictionAreaStore) updateTFRList(entries []tfrRestrictionSummary, fetchedAt time.Time) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.tfrAsOf.IsZero() && fetchedAt.Before(s.tfrAsOf) {
		return 0, false
	}
	if s.areas == nil {
		s.areas = make(map[int]starsRestrictionArea)
	}
	if s.tfrSlots == nil {
		s.tfrSlots = make(map[string]int)
	}
	wanted := make(map[string]tfrRestrictionSummary, len(entries))
	for _, entry := range entries {
		wanted[entry.key] = entry
	}
	// Remove absent NOTAMs, leaving unrelated site adaptation untouched.
	for key, id := range s.tfrSlots {
		if _, present := wanted[key]; !present {
			if a, ok := s.areas[id]; ok && a.IsFAA && a.NOTAM == key {
				delete(s.areas, id)
			}
			delete(s.tfrSlots, key)
		}
	}
	used := make(map[int]bool)
	for id := firstTFRRestrictionID; id <= lastTFRRestrictionID; id++ {
		if _, ok := s.areas[id]; ok {
			used[id] = true
		}
	}
	dropped := 0
	for _, entry := range entries {
		id := s.tfrSlots[entry.key]
		if id != 0 {
			if a, ok := s.areas[id]; !ok || !a.IsFAA || a.NOTAM != entry.key {
				delete(s.tfrSlots, entry.key)
				id = 0
			}
		}
		if id == 0 {
			for candidate := firstTFRRestrictionID; candidate <= lastTFRRestrictionID; candidate++ {
				if !used[candidate] {
					id = candidate
					used[id] = true
					break
				}
			}
		}
		if id == 0 {
			dropped++ // cannot use a manual ID or collide with site adaptation
			continue
		}
		a := s.areas[id]
		if a.Serial == 0 || !a.IsFAA || a.NOTAM != entry.key {
			s.nextSerial++
			a.Serial = s.nextSerial
		}
		a.ID = id
		a.IsFAA = true
		a.NOTAM = entry.key
		a.TFRParts = entry.featureCount
		a.Kind = starsRestrictionClosed
		a.Shaded = true
		a.TFRPolygons = entry.polygons
		a.Label = tfrLabelPosition(entry.polygons)
		a.Text = [2]string{tfrListLabel(entry.title)}
		a.Color = 1
		s.areas[id] = a
		s.tfrSlots[entry.key] = id
	}
	s.tfrAsOf = fetchedAt
	return dropped, true
}

// Called on the STARS draw goroutine. The TFR client fetches in the background;
// the integration never performs network I/O on the rendering thread.
func (p *STARSPane) syncTFRRestrictionList() {
	if p == nil || p.tfr == nil {
		return
	}
	snapshot, ok := p.tfr.Snapshot()
	if !ok {
		return // retain the previous site list during network failure
	}
	if snapshot.Revision != p.tfrListRevision {
		entries, err := decodeTFRRestrictionSummaries(snapshot.GeoJSON)
		if err != nil {
			p.logger.Warn("Unable to decode FAA TFR list", "error", err)
			return // do not replace the valid list with a partial decode
		}
		dropped, _ := p.restrictionAreaStore().updateTFRList(entries, snapshot.FetchedAt)
		if dropped > 0 {
			p.logger.Warn("FAA TFR GEO RESTRICTIONS list at capacity", "dropped", dropped)
		}
		// Even when a newer pane has already updated the site-wide store,
		// this pane has processed its snapshot and need not re-decode it.
		p.tfrListRevision = snapshot.Revision
	}
	p.ensureTFRRestrictionVisibility()
}

// Each new FAA entry is initialized explicitly hidden on every pane. Serial
// guards against an expired/replaced NOTAM inheriting another TFR's visibility.
func (p *STARSPane) ensureTFRRestrictionVisibility() {
	if p == nil {
		return
	}
	// Initialize newly imported slots as locally hidden. Existing preferences
	// are never overwritten: an operator's later visibility choice persists.
	ps := p.currentPrefs()
	if ps.RestrictionAreaSettings == nil {
		ps.RestrictionAreaSettings = make(map[int]restrictionAreaDisplaySettings)
	}
	for _, a := range p.restrictionAreaStore().snapshot() {
		if !a.IsFAA {
			continue
		}
		if existing, ok := ps.RestrictionAreaSettings[a.ID]; !ok || existing.Serial != a.Serial {
			ps.RestrictionAreaSettings[a.ID] = restrictionAreaDisplaySettings{Serial: a.Serial, Hidden: true}
		}
	}
}
