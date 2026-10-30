package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Intervals are half-open, UTC, and tied to the specific drawable WFS geometry.
// An empty interval list is valid only for explicitly indefinite restrictions.
type activationInterval struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}
type activation struct {
	Known      bool                 `json:"known"`
	Intervals  []activationInterval `json:"intervals,omitempty"`
	Indefinite bool                 `json:"indefinite,omitempty"`
}
type xmlVertex struct {
	Lat string `xml:"geoLat"`
	Lon string `xml:"geoLong"`
}
type xmlSchedule struct {
	Separate   string `xml:"isTimeSeparate"`
	Start      string `xml:"dateEffective"`
	End        string `xml:"dateExpire"`
	ClockStart string `xml:"startTime"`
	ClockEnd   string `xml:"endTime"`
	DayCode    string `xml:"dayCode"`
}
type xmlArea struct {
	ID        string        `xml:"aseTFRArea>AseUid>codeId"`
	Scheduled string        `xml:"aseTFRArea>isScheduledTfrArea"`
	DayCode   string        `xml:"aseTFRArea>dayCode"`
	Schedules []xmlSchedule `xml:"aseTFRArea>ScheduleGroup"`
	Vertices  []xmlVertex   `xml:"abdMergedArea>Avx"`
}
type xmlNotice struct {
	XMLName xml.Name `xml:"XNOTAM-Update"`
	Notices []struct {
		Start string    `xml:"dateEffective"`
		End   string    `xml:"dateExpire"`
		Areas []xmlArea `xml:"TfrNot>TFRAreaGroup"`
	} `xml:"Not"`
}

// FAA XML nesting varies; use the TfrNot subtree rather than assuming the
// notice's wrapper name. Encoding/xml can identify nested elements via path.
type xmlDocument struct {
	XMLName xml.Name         `xml:"XNOTAM-Update"`
	Notices []xmlNoticeEntry `xml:"Group>Add>Not"`
}
type xmlNoticeEntry struct {
	Start     string    `xml:"dateEffective"`
	End       string    `xml:"dateExpire"`
	LocalName string    `xml:"NotUid>txtLocalName"`
	City      string    `xml:"AffLocGroup>txtNameCity"`
	Areas     []xmlArea `xml:"TfrNot>TFRAreaGroup"`
}

func xmlUTC(s string) (time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", time.RFC3339, "2006-01-02T15:04:05.000"} {
		if t, e := time.Parse(layout, strings.TrimSpace(s)); e == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported FAA timestamp %q", s)
}
func parseDegree(v string) (float64, error) {
	v = strings.TrimSpace(strings.ToUpper(v))
	if len(v) < 2 {
		return 0, errors.New("empty coordinate")
	}
	dir := v[len(v)-1]
	n, e := strconv.ParseFloat(v[:len(v)-1], 64)
	if e != nil {
		return 0, e
	}
	if dir != 'N' && dir != 'S' && dir != 'E' && dir != 'W' {
		return 0, errors.New("unknown hemisphere")
	}
	if dir == 'W' || dir == 'S' {
		n = -n
	}
	return n, nil
}

// Full outer-ring vertex multiset (order/direction independent); rounding to
// 1e-5 degree compensates for FAA WFS coordinate serialization differences.
// Exact multiset equality prevents matching a shape on a single common vertex.
func geometryKey(vertices [][2]float64) string {
	if len(vertices) > 1 && vertices[0] == vertices[len(vertices)-1] {
		vertices = vertices[:len(vertices)-1]
	}
	pts := make([]string, 0, len(vertices))
	for _, p := range vertices {
		pts = append(pts, fmt.Sprintf("%d:%d", int64(math.Round(p[0]*1e5)), int64(math.Round(p[1]*1e5))))
	}
	sort.Strings(pts)
	return strings.Join(pts, "|")
}
func areaKey(a xmlArea) (string, error) {
	if len(a.Vertices) < 3 {
		return "", nil
	}
	pts := make([][2]float64, 0, len(a.Vertices))
	for _, v := range a.Vertices {
		lat, e := parseDegree(v.Lat)
		if e != nil {
			return "", e
		}
		lon, e := parseDegree(v.Lon)
		if e != nil {
			return "", e
		}
		pts = append(pts, [2]float64{lon, lat})
	}
	return geometryKey(pts), nil
}
func wfsGeometryKeys(raw json.RawMessage) ([]string, error) {
	var g struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if e := json.Unmarshal(raw, &g); e != nil {
		return nil, e
	}
	var polys [][][][]float64
	switch g.Type {
	case "Polygon":
		var rings [][][]float64
		if e := json.Unmarshal(g.Coordinates, &rings); e != nil {
			return nil, e
		}
		polys = append(polys, rings)
	case "MultiPolygon":
		if e := json.Unmarshal(g.Coordinates, &polys); e != nil {
			return nil, e
		}
	default:
		return nil, errors.New("unsupported geometry")
	}
	keys := make([]string, 0, len(polys))
	for _, rings := range polys {
		if len(rings) == 0 {
			return nil, errors.New("empty rings")
		}
		p := make([][2]float64, 0, len(rings[0]))
		for _, c := range rings[0] {
			if len(c) != 2 {
				return nil, errors.New("bad vertex")
			}
			p = append(p, [2]float64{c[0], c[1]})
		}
		keys = append(keys, geometryKey(p))
	}
	return keys, nil
}
func clipInterval(x activationInterval, lo, hi time.Time) (activationInterval, bool) {
	if !lo.IsZero() && x.Start.Before(lo) {
		x.Start = lo
	}
	if !hi.IsZero() && x.End.After(hi) {
		x.End = hi
	}
	return x, x.End.After(x.Start)
}
func parseSchedule(start, end time.Time, a xmlArea) (activation, error) {
	result := activation{Known: true}
	if strings.EqualFold(a.Scheduled, "TRUE") && len(a.Schedules) == 0 {
		return activation{}, errors.New("scheduled area without ScheduleGroup")
	}
	if len(a.Schedules) == 0 {
		if !start.IsZero() && !end.IsZero() {
			result.Intervals = []activationInterval{{start, end}}
			return result, nil
		}
		if end.IsZero() {
			result.Indefinite = start.IsZero()
			if !start.IsZero() {
				result.Intervals = []activationInterval{{start, time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)}}
			}
			return result, nil
		}
		return activation{}, errors.New("end without start")
	}
	for _, s := range a.Schedules {
		lo, e := xmlUTC(s.Start)
		if e != nil {
			return activation{}, e
		}
		hi, e := xmlUTC(s.End)
		if e != nil {
			return activation{}, e
		}
		if lo.IsZero() {
			lo = start
		}
		if hi.IsZero() {
			hi = end
		}
		if lo.IsZero() || hi.IsZero() || !hi.After(lo) {
			return activation{}, errors.New("invalid schedule validity")
		}
		if strings.EqualFold(s.Separate, "TRUE") || strings.EqualFold(a.Scheduled, "TRUE") {
			day := strings.TrimSpace(s.DayCode)
			if day == "" {
				day = a.DayCode
			}
			if day != "" && !strings.EqualFold(day, "daily") {
				return activation{}, fmt.Errorf("unsupported recurring dayCode %q", day)
			}
			cs, e := xmlUTC(s.ClockStart)
			if e != nil {
				return activation{}, e
			}
			ce, e := xmlUTC(s.ClockEnd)
			if e != nil {
				return activation{}, e
			}
			if cs.IsZero() || ce.IsZero() {
				return activation{}, errors.New("missing recurring clock")
			}
			// Start on previous day as overnight cycles can extend into the first day.
			for d := time.Date(lo.Year(), lo.Month(), lo.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1); d.Before(hi); d = d.AddDate(0, 0, 1) {
				x := activationInterval{d.Add(time.Duration(cs.Hour())*time.Hour + time.Duration(cs.Minute())*time.Minute + time.Duration(cs.Second())*time.Second), d.Add(time.Duration(ce.Hour())*time.Hour + time.Duration(ce.Minute())*time.Minute + time.Duration(ce.Second())*time.Second)}
				if !x.End.After(x.Start) {
					x.End = x.End.AddDate(0, 0, 1)
				}
				if clipped, ok := clipInterval(x, lo, hi); ok {
					result.Intervals = append(result.Intervals, clipped)
				}
				if len(result.Intervals) > 4000 {
					return activation{}, errors.New("schedule exceeds 4000 intervals")
				}
			}
		} else {
			result.Intervals = append(result.Intervals, activationInterval{lo, hi})
		}
	}
	return result, nil
}
func extractAreaSchedules(raw []byte) (map[string]activation, error) {
	raw = []byte(strings.TrimPrefix(string(raw), "\ufeff"))
	var root xmlDocument
	if e := xml.Unmarshal(raw, &root); e != nil {
		return nil, e
	}
	if root.XMLName.Local != "XNOTAM-Update" {
		return nil, errors.New("not XNOTAM XML")
	}
	out := map[string]activation{}
	totalAreas := 0
	for _, n := range root.Notices {
		totalAreas += len(n.Areas)
	}
	for _, n := range root.Notices {
		if len(n.Areas) == 0 {
			continue
		}
		start, e := xmlUTC(n.Start)
		if e != nil {
			return nil, e
		}
		end, e := xmlUTC(n.End)
		if e != nil {
			return nil, e
		}
		for _, a := range n.Areas {
			key, e := areaKey(a)
			if e != nil {
				return nil, e
			}
			if key == "" {
				if totalAreas != 1 {
					continue
				}
				key = "__single_without_geometry__"
			}
			value, e := parseSchedule(start, end, a)
			if e != nil {
				// Unsupported recurrence is not an active restriction.
				out[key] = activation{}
				continue
			}
			existing := out[key]
			existing.Known = true
			existing.Indefinite = existing.Indefinite || value.Indefinite
			existing.Intervals = append(existing.Intervals, value.Intervals...)
			out[key] = existing
		}
	}
	if len(out) == 0 {
		return out, nil
	}
	return out, nil
}

// extractTFRLocalName reads the FAA's operator-facing name independently
// of its activation areas. Missing names are permitted (use client fallback).
func extractTFRLocalName(raw []byte) (string, error) {
	name, _, err := extractTFRNames(raw)
	return name, err
}

// The FAA's txtLocalName can be a date code (e.g. 261020-261030), so use
// the actual AffLocGroup city for the operator-facing location instead.
func extractTFRNames(raw []byte) (localName, city string, err error) {
	raw = []byte(strings.TrimPrefix(string(raw), "\ufeff"))
	var doc xmlDocument
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return "", "", err
	}
	if doc.XMLName.Local != "XNOTAM-Update" {
		return "", "", errors.New("not XNOTAM XML")
	}
	for _, notice := range doc.Notices {
		localName = strings.TrimSpace(notice.LocalName)
		city = strings.TrimSpace(notice.City)
		if localName != "" || city != "" {
			return localName, city, nil
		}
	}
	return "", "", nil
}

const defaultDetailURL = "https://tfr.faa.gov/download/detail_%s.xml"

// Fetch XML only for drawable NOTAMs. An incomplete upstream refresh is not
// published: the last successful snapshot remains available, marked stale.
func (m *mirror) annotateSchedules(ctx context.Context, raw []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	var features []map[string]json.RawMessage
	if err := json.Unmarshal(root["features"], &features); err != nil {
		return nil, err
	}
	if len(features) == 0 {
		return raw, nil
	}
	byID := map[string][]int{}
	for i, f := range features {
		var props struct {
			Key string `json:"NOTAM_KEY"`
		}
		if err := json.Unmarshal(f["properties"], &props); err != nil {
			return nil, err
		}
		byID[shortNOTAM(props.Key)] = append(byID[shortNOTAM(props.Key)], i)
	}
	if len(byID) > 200 {
		return nil, errors.New("too many TFR details in one refresh")
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	type result struct {
		id    string
		areas map[string]activation
		name  string
		city  string
		err   error
	}
	results := make(chan result, len(ids))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results <- result{id: id, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()
			template := m.cfg.DetailURL
			if template == "" {
				template = defaultDetailURL
			}
			// Only the verified FAA short NOTAM ID is interpolated into the path.
			safe := strings.ReplaceAll(id, "/", "_")
			if !validDetailID(id) {
				results <- result{id: id, err: errors.New("invalid NOTAM ID")}
				return
			}
			endpoint := fmt.Sprintf(template, url.PathEscape(safe))
			req, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
			if e != nil {
				results <- result{id: id, err: e}
				return
			}
			req.Header.Set("Accept", "application/xml, text/xml")
			resp, e := m.httpClient.Do(req)
			if e != nil {
				results <- result{id: id, err: e}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				results <- result{id: id, err: fmt.Errorf("detail HTTP %s", resp.Status)}
				return
			}
			content, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
			if e != nil {
				results <- result{id: id, err: e}
				return
			}
			if len(content) > 2<<20 {
				results <- result{id: id, err: errors.New("detail XML too large")}
				return
			}
			areas, e := extractAreaSchedules(content)
			if e != nil {
				results <- result{id: id, err: e}
				return
			}
			name, city, e := extractTFRNames(content)
			results <- result{id: id, areas: areas, name: name, city: city, err: e}
		}(id)
	}
	wg.Wait()
	close(results)
	details := map[string]map[string]activation{}
	localNames := map[string]string{}
	cities := map[string]string{}
	for r := range results {
		if r.err != nil {
			// Do not invent activation for an unavailable detail; publish
			// valid geometry as unknown when other NOTAMs were verified.
			log.Printf("[TFR] XML %s unavailable: %v (schedule unknown)", r.id, r.err)
			continue
		}
		details[r.id] = r.areas
		localNames[r.id] = r.name
		cities[r.id] = r.city
	}
	matched := 0
	for id, indices := range byID {
		schedules := details[id]
		for _, i := range indices {
			var rawGeo json.RawMessage = features[i]["geometry"]
			keys, e := wfsGeometryKeys(rawGeo)
			if e != nil {
				return nil, e
			}
			var combined activation
			partSchedules := make([]activation, len(keys))
			for part, key := range keys {
				a, ok := schedules[key]
				if !ok && len(indices) == 1 && len(keys) == 1 {
					// Only a single feature/single area with no XML geometry
					// can be joined without risking an area-order assumption.
					a, ok = schedules["__single_without_geometry__"]
				}
				if !ok {
					continue
				}
				partSchedules[part] = a
				matched++
				combined.Known = true
				combined.Indefinite = combined.Indefinite || a.Indefinite
				combined.Intervals = append(combined.Intervals, a.Intervals...)
			}
			// An unmatched polygon remains explicitly unknown: never infer schedule
			// from a neighboring polygon, NOTAM's broad validity, or feature order.
			props := map[string]json.RawMessage{}
			if e := json.Unmarshal(features[i]["properties"], &props); e != nil {
				return nil, e
			}
			encoded, e := json.Marshal(combined)
			if e != nil {
				return nil, e
			}
			props["TFR_SCHEDULE"] = encoded
			partsRaw, e := json.Marshal(partSchedules)
			if e != nil {
				return nil, e
			}
			props["TFR_PART_SCHEDULES"] = partsRaw
			if name := localNames[id]; name != "" {
				// Keep the source NOTAM and geometry identifiers unchanged.
				nameJSON, e := json.Marshal(name)
				if e != nil {
					return nil, e
				}
				props["TFR_LOCAL_NAME"] = nameJSON
			}
			if city := cities[id]; city != "" {
				cityJSON, e := json.Marshal(city)
				if e != nil {
					return nil, e
				}
				props["TFR_CITY"] = cityJSON
			}
			encoded, e = json.Marshal(props)
			if e != nil {
				return nil, e
			}
			features[i]["properties"] = encoded
		}
	}
	if matched == 0 {
		return nil, errors.New("no FAA XML areas matched WFS geometry; retain previous snapshot")
	}
	b, e := json.Marshal(features)
	if e != nil {
		return nil, e
	}
	root["features"] = b
	return json.Marshal(root)
}
func validDetailID(id string) bool {
	parts := strings.Split(id, "/")
	if len(parts) != 2 || len(parts[0]) != 1 || len(parts[1]) != 4 {
		return false
	}
	for _, p := range parts {
		for _, c := range p {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}
