package asdex

import (
	"encoding/json"
	"fmt"
	stdmath "math"
	"path/filepath"
	"sort"
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util"
)

const feetPerNM = 6076.12

type PolygonType int

const (
	PolygonApron PolygonType = iota
	PolygonStructure
	PolygonTaxiway
	PolygonRunway
)

type VideoMapMesh struct {
	Type     PolygonType
	Z        float32
	Vertices []renderer.PointVertex
	Indices  []uint32
}

type VideoMap struct {
	meshes []VideoMapMesh
	bounds redsmath.Rect
	anchor redsmath.Vec2
	valid  bool

	dayCmd   *renderer.CmdBuffer
	nightCmd *renderer.CmdBuffer
}

func LoadVideoMap(icao string) (*VideoMap, error) {
	icao = strings.ToUpper(strings.TrimSpace(icao))
	if icao == "" {
		return nil, fmt.Errorf("Videomap is empty.")
	}

	raw, err := loadVideoMapBytes(icao)
	if err != nil {
		return nil, err
	}

	var fc featureCollection
	if err := json.Unmarshal(raw, &fc); err != nil {
		return nil, fmt.Errorf("parse ASDE-X videomap %s: %w", icao, err)
	}

	polygons, lonLatBounds, ok := parseVideoMapPolygons(fc)
	vm := &VideoMap{}
	if !ok {
		return vm, nil
	}

	vm.anchor = redsmath.Vec2{
		X: (lonLatBounds.Min.X + lonLatBounds.Max.X) * 0.5,
		Y: (lonLatBounds.Min.Y + lonLatBounds.Max.Y) * 0.5,
	}

	toFeet := lonLatToFeet(vm.anchor)
	firstFeet := true
	for i := range polygons {
		for r := range polygons[i].Rings {
			for p := range polygons[i].Rings[r] {
				polygons[i].Rings[r][p] = toFeet(polygons[i].Rings[r][p])
			}
			updateRingBounds(polygons[i].Rings[r], &vm.bounds, &firstFeet)
		}

		vertices, indices := renderer.TessellateRings(polygons[i].Rings)
		if len(vertices) == 0 || len(indices) == 0 {
			continue
		}
		vm.meshes = append(vm.meshes, VideoMapMesh{
			Type:     polygons[i].Type,
			Z:        videoMapZ(polygons[i].Type),
			Vertices: vertices,
			Indices:  indices,
		})
	}

	sort.Slice(vm.meshes, func(i, j int) bool { return vm.meshes[i].Z < vm.meshes[j].Z })
	vm.valid = len(vm.meshes) > 0
	if vm.valid {
		vm.dayCmd = vm.buildCmdBuffer(ModeDay)
		vm.nightCmd = vm.buildCmdBuffer(ModeNight)
	}
	return vm, nil
}

func (vm *VideoMap) IsValid() bool { return vm != nil && vm.valid }
func (vm *VideoMap) BoundsFeet() redsmath.Rect {
	if vm == nil {
		return redsmath.Rect{}
	}
	return vm.bounds
}
func (vm *VideoMap) AnchorLonLat() redsmath.Vec2 {
	if vm == nil {
		return redsmath.Vec2{}
	}
	return vm.anchor
}
func (vm *VideoMap) Meshes() []VideoMapMesh {
	if vm == nil {
		return nil
	}
	return vm.meshes
}

func DrawVideoMap(vm *VideoMap, cb *renderer.CmdBuffer, mode Mode) {
	if vm == nil || cb == nil || !vm.IsValid() {
		return
	}
	if mode == ModeNight {
		cb.Call(vm.nightCmd)
		return
	}
	cb.Call(vm.dayCmd)
}

func (vm *VideoMap) buildCmdBuffer(mode Mode) *renderer.CmdBuffer {
	cb := &renderer.CmdBuffer{}
	for _, mesh := range vm.meshes {
		cb.SetRGB(videoMapColor(mesh.Type, mode))
		builder := renderer.GetTrianglesBuilder()
		builder.AddIndexed(mesh.Vertices, mesh.Indices)
		builder.GenerateCommands(cb, renderer.DrawSolid, 0)
		renderer.ReturnTrianglesBuilder(builder)
	}
	return cb
}

func loadVideoMapBytes(icao string) ([]byte, error) {
	zstPath := filepath.ToSlash(filepath.Join("resources", "videomaps", "asdex", icao+".geojson.zst"))
	if !util.ResourceExists(zstPath) {
		return nil, fmt.Errorf("ASDE-X videomap %s not found as .geojson.zst", icao)
	}
	return util.LoadResourceBytes(zstPath), nil
}

type featureCollection struct {
	Type     string           `json:"type"`
	Features []geoJSONFeature `json:"features"`
}

type geoJSONFeature struct {
	Properties map[string]any   `json:"properties"`
	Geometry   *geoJSONGeometry `json:"geometry"`
}

type geoJSONGeometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

type polygonRings struct {
	Type  PolygonType
	Rings [][]redsmath.Vec2
}

func parseVideoMapPolygons(fc featureCollection) ([]polygonRings, redsmath.Rect, bool) {
	var polygons []polygonRings
	var bounds redsmath.Rect
	firstRing := true

	for _, feature := range fc.Features {
		if feature.Geometry == nil {
			continue
		}
		polygonType, ok := classifyPolygonType(stringProperty(feature.Properties, "asdex"))
		if !ok {
			continue
		}

		switch feature.Geometry.Type {
		case "Polygon":
			var coords [][][]float64
			if err := json.Unmarshal(feature.Geometry.Coordinates, &coords); err != nil {
				continue
			}
			if poly := parsePolygon(polygonType, coords, &bounds, &firstRing); len(poly.Rings) > 0 {
				polygons = append(polygons, poly)
			}
		case "MultiPolygon":
			var coords [][][][]float64
			if err := json.Unmarshal(feature.Geometry.Coordinates, &coords); err != nil {
				continue
			}
			for _, polygon := range coords {
				if poly := parsePolygon(polygonType, polygon, &bounds, &firstRing); len(poly.Rings) > 0 {
					polygons = append(polygons, poly)
				}
			}
		}
	}

	return polygons, bounds, !firstRing
}

func parsePolygon(polygonType PolygonType, rings [][][]float64, bounds *redsmath.Rect, firstRing *bool) polygonRings {
	poly := polygonRings{Type: polygonType}
	for _, coords := range rings {
		ring := parseRing(coords)
		if len(ring) == 0 {
			continue
		}
		poly.Rings = append(poly.Rings, ring)
		updateRingBounds(ring, bounds, firstRing)
	}
	return poly
}

func parseRing(coords [][]float64) []redsmath.Vec2 {
	ring := make([]redsmath.Vec2, 0, len(coords))
	for _, coord := range coords {
		if len(coord) < 2 {
			continue
		}
		ring = append(ring, redsmath.Vec2{X: float32(coord[0]), Y: float32(coord[1])})
	}
	if len(ring) >= 2 {
		first := ring[0]
		last := ring[len(ring)-1]
		if stdmath.Abs(float64(first.X-last.X)) < 1e-12 && stdmath.Abs(float64(first.Y-last.Y)) < 1e-12 {
			ring = ring[:len(ring)-1]
		}
	}
	if len(ring) < 3 {
		return nil
	}
	return ring
}

func updateRingBounds(ring []redsmath.Vec2, bounds *redsmath.Rect, first *bool) {
	if len(ring) == 0 {
		return
	}
	minX, maxX := ring[0].X, ring[0].X
	minY, maxY := ring[0].Y, ring[0].Y
	for _, p := range ring[1:] {
		if p.X < minX {
			minX = p.X
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}
	ringBounds := redsmath.NewRect(minX, minY, maxX, maxY)
	if *first {
		*bounds = ringBounds
		*first = false
		return
	}
	if ringBounds.Min.X < bounds.Min.X {
		bounds.Min.X = ringBounds.Min.X
	}
	if ringBounds.Min.Y < bounds.Min.Y {
		bounds.Min.Y = ringBounds.Min.Y
	}
	if ringBounds.Max.X > bounds.Max.X {
		bounds.Max.X = ringBounds.Max.X
	}
	if ringBounds.Max.Y > bounds.Max.Y {
		bounds.Max.Y = ringBounds.Max.Y
	}
}

func stringProperty(properties map[string]any, key string) string {
	if properties == nil {
		return ""
	}
	value, ok := properties[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func classifyPolygonType(asdexKind string) (PolygonType, bool) {
	switch asdexKind {
	case "runway":
		return PolygonRunway, true
	case "taxiway":
		return PolygonTaxiway, true
	case "apron":
		return PolygonApron, true
	case "structure", "building":
		return PolygonStructure, true
	default:
		return PolygonApron, false
	}
}

func videoMapZ(polygonType PolygonType) float32 {
	const base = -9.0
	switch polygonType {
	case PolygonStructure:
		return base + 0.4
	case PolygonRunway:
		return base + 0.3
	case PolygonTaxiway:
		return base + 0.2
	case PolygonApron:
		return base + 0.1
	default:
		return base
	}
}

func videoMapColor(polygonType PolygonType, mode Mode) renderer.RGB {
	day := mode == ModeDay
	var base renderer.RGB
	switch polygonType {
	case PolygonRunway:
		base = renderer.RGB8(0, 0, 0)
	case PolygonTaxiway:
		if day {
			base = renderer.RGB8(47, 47, 47)
		} else {
			base = renderer.RGB8(17, 39, 80)
		}
	case PolygonApron:
		if day {
			base = renderer.RGB8(73, 73, 73)
		} else {
			base = renderer.RGB8(18, 55, 97)
		}
	case PolygonStructure:
		if day {
			base = renderer.RGB8(100, 100, 100)
		} else {
			base = renderer.RGB8(34, 63, 103)
		}
	}
	return applyBrightness(base, brightnessDefault, brightnessFloorDefault)
}

func lonLatToFeet(anchor redsmath.Vec2) func(redsmath.Vec2) redsmath.Vec2 {
	cosLat := float32(stdmath.Cos(float64(anchor.Y) * stdmath.Pi / 180.0))
	return func(p redsmath.Vec2) redsmath.Vec2 {
		return redsmath.Vec2{
			X: (p.X - anchor.X) * 60.0 * cosLat * feetPerNM,
			Y: (p.Y - anchor.Y) * 60.0 * feetPerNM,
		}
	}
}
