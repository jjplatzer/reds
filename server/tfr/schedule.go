package main

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
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
	raw = []byte(strings.TrimPrefix(string(raw), "\ufeff"))
	var doc xmlDocument
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	if doc.XMLName.Local != "XNOTAM-Update" {
		return "", errors.New("not XNOTAM XML")
	}
	for _, notice := range doc.Notices {
		if name := strings.TrimSpace(notice.LocalName); name != "" {
			return name, nil
		}
	}
	return "", nil
}
