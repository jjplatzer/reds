package main

import (
	"encoding/json"
	"testing"
	"time"
)

// Two FAA XML areas share one geometry, but have disjoint activation windows.
// The geometry join must retain both windows (no accidental continuous interval).
func TestSharedGeometryUnion(t *testing.T) {
	xmlData := []byte(`<XNOTAM-Update><Group><Add><Not><TfrNot>
 <TFRAreaGroup><aseTFRArea><ScheduleGroup><dateEffective>2026-10-29T15:00:00</dateEffective><dateExpire>2026-10-29T17:15:00</dateExpire></ScheduleGroup></aseTFRArea><abdMergedArea><Avx><geoLat>32N</geoLat><geoLong>97W</geoLong></Avx><Avx><geoLat>33N</geoLat><geoLong>97W</geoLong></Avx><Avx><geoLat>33N</geoLat><geoLong>96W</geoLong></Avx></abdMergedArea></TFRAreaGroup>
 <TFRAreaGroup><aseTFRArea><ScheduleGroup><dateEffective>2026-10-29T18:45:00</dateEffective><dateExpire>2026-10-29T21:45:00</dateExpire></ScheduleGroup></aseTFRArea><abdMergedArea><Avx><geoLat>32N</geoLat><geoLong>97W</geoLong></Avx><Avx><geoLat>33N</geoLat><geoLong>97W</geoLong></Avx><Avx><geoLat>33N</geoLat><geoLong>96W</geoLong></Avx></abdMergedArea></TFRAreaGroup>
 </TfrNot></Not></Add></Group></XNOTAM-Update>`)
	areas, err := extractAreaSchedules(xmlData)
	if err != nil {
		t.Fatal(err)
	}
	if len(areas) != 1 {
		t.Fatalf("expected 1 geometry, got %d", len(areas))
	}
	for _, a := range areas {
		if !a.Known || len(a.Intervals) != 2 {
			t.Fatalf("expected two activation windows: %+v", a)
		}
	}
	geometry := json.RawMessage(`{"type":"Polygon","coordinates":[[[-97,32],[-97,33],[-96,33],[-97,32]]]}`)
	keys, err := wfsGeometryKeys(geometry)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := areas[keys[0]]; !ok {
		t.Fatal("XML/WFS coordinate matching failed")
	}
}
func TestOvernightRecurrence(t *testing.T) {
	a := xmlArea{Scheduled: "TRUE", DayCode: "Daily", Schedules: []xmlSchedule{{Separate: "TRUE", Start: "2026-10-25T13:00:00", End: "2026-11-01T01:00:00", ClockStart: "2026-10-24T13:00:00", ClockEnd: "2026-10-24T01:00:00"}}}
	v, e := parseSchedule(time.Time{}, time.Time{}, a)
	if e != nil {
		t.Fatal(e)
	}
	if len(v.Intervals) != 7 {
		t.Fatalf("got %d windows, want 7", len(v.Intervals))
	}
	if !v.Intervals[0].End.Equal(time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("first window %+v", v.Intervals[0])
	}
}
func TestIndefiniteStanding(t *testing.T) {
	a, e := parseSchedule(time.Time{}, time.Time{}, xmlArea{})
	if e != nil || !a.Known || !a.Indefinite {
		t.Fatalf("missing dates must be indefinite: %+v %v", a, e)
	}
	start := time.Date(2020, 1, 15, 0, 0, 0, 0, time.UTC)
	b, e := parseSchedule(start, time.Time{}, xmlArea{})
	if e != nil || b.Indefinite || len(b.Intervals) != 1 || !b.Intervals[0].Start.Equal(start) {
		t.Fatalf("start-only must activate after start: %+v %v", b, e)
	}
}

func TestExtractTFRLocalName(t *testing.T) {
	xmlData := []byte("\xef\xbb\xbf" + `<XNOTAM-Update><Group><Add><Not><NotUid><txtLocalName>CIP NYC TFR MOD1</txtLocalName></NotUid></Not></Add></Group></XNOTAM-Update>`)
	got, err := extractTFRLocalName(xmlData)
	if err != nil || got != "CIP NYC TFR MOD1" {
		t.Fatalf("name=%q err=%v", got, err)
	}
}
