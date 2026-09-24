package net

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func decodeTaisFrame(t *testing.T, raw string) TaisFrame {
	t.Helper()
	var frame TaisFrame
	if err := json.Unmarshal([]byte(raw), &frame); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	return frame
}

func TestTaisStateSnapshotUpdateRemove(t *testing.T) {
	state := newTaisState()

	snapshot := decodeTaisFrame(t, `{
		"type":"snapshot","protocol":1,"feed":"tais","revision":10,
		"generatedAt":"2026-09-23T20:30:03.729623244Z",
		"targets":[{
			"key":"N90:T42","facility":"N90","track":{
				"trackNum":"42","mrtTime":"2026-09-23T20:30:00Z",
				"lat":40.0,"lon":-74.0,"reportedAltitude":5000,"vx":100,"vy":50,"adsb":true
			},
			"history":[{"time":"2026-09-23T20:30:00Z","lat":40.0,"lon":-74.0,"reportedAltitude":5000,"vx":100,"vy":50,"adsb":true}]
		}]
	}`)
	if err := state.apply(snapshot); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	update := decodeTaisFrame(t, `{
		"type":"update","protocol":1,"feed":"tais","revision":11,
		"target":{"key":"N90:T42","facility":"N90","track":{
			"trackNum":"42","mrtTime":"2026-09-23T20:30:05Z",
			"lat":40.01,"lon":-73.99,"reportedAltitude":5100,"vx":101,"vy":51,"adsb":true
		}}
	}`)
	if err := state.apply(update); err != nil {
		t.Fatalf("apply update: %v", err)
	}

	view := state.snapshot("N90")
	if !view.Ready || view.Revision != 11 {
		t.Fatalf("unexpected snapshot state: ready=%v revision=%d", view.Ready, view.Revision)
	}
	if len(view.Targets) != 1 || len(view.Targets[0].History) != 2 {
		t.Fatalf("expected one target with two history points, got %+v", view.Targets)
	}

	// The public snapshot must be detached from internal state.
	view.Targets[0].History[0].Lat = 0
	again := state.snapshot("N90")
	if again.Targets[0].History[0].Lat != 40.0 {
		t.Fatal("snapshot history aliases internal state")
	}

	remove := decodeTaisFrame(t, `{
		"type":"remove","protocol":1,"feed":"tais","revision":12,"key":"N90:T42"
	}`)
	if err := state.apply(remove); err != nil {
		t.Fatalf("apply remove: %v", err)
	}
	view = state.snapshot("")
	if view.Revision != 12 || len(view.Targets) != 0 {
		t.Fatalf("remove did not update state: revision=%d targets=%d", view.Revision, len(view.Targets))
	}
}

func TestTaisStateRejectsRevisionGap(t *testing.T) {
	state := newTaisState()
	if err := state.apply(decodeTaisFrame(t, `{
		"type":"snapshot","protocol":1,"feed":"tais","revision":100,"targets":[]
	}`)); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	err := state.apply(decodeTaisFrame(t, `{
		"type":"remove","protocol":1,"feed":"tais","revision":102,"key":"N90:T1"
	}`))
	if err == nil || !strings.Contains(err.Error(), "revision gap") {
		t.Fatalf("expected revision gap, got %v", err)
	}
}

func TestTaisNewTrackResetsHistory(t *testing.T) {
	state := newTaisState()
	if err := state.apply(decodeTaisFrame(t, `{
		"type":"snapshot","protocol":1,"feed":"tais","revision":1,
		"targets":[{"key":"N90:T5","facility":"N90","track":{"trackNum":"5"},
		"history":[{"time":"2026-09-23T20:00:00Z","lat":40,"lon":-74}]}]
	}`)); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}
	if err := state.apply(decodeTaisFrame(t, `{
		"type":"update","protocol":1,"feed":"tais","revision":2,
		"target":{"key":"N90:T5","facility":"N90","track":{
			"trackNum":"5","newTrack":true,"mrtTime":"2026-09-23T20:31:00Z","lat":41,"lon":-73
		}}
	}`)); err != nil {
		t.Fatalf("apply new-track update: %v", err)
	}

	view := state.snapshot("")
	if len(view.Targets) != 1 || len(view.Targets[0].History) != 1 {
		t.Fatalf("expected reset history with one point, got %+v", view.Targets)
	}
	want, _ := time.Parse(time.RFC3339, "2026-09-23T20:31:00Z")
	if !view.Targets[0].History[0].Time.Equal(want) {
		t.Fatalf("unexpected history time %s", view.Targets[0].History[0].Time)
	}
}
