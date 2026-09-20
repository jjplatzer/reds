package stars

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util"
)

const zVideoMaps renderer.Z = -900

// starsVideoMap is the client-side rendering state for one adapted STARS
// video map. The geometry is pre-baked in geographic lon/lat coordinates so
// pan/range changes only update the viewing transform; the potentially large
// source GeoJSON is discarded immediately after this command buffer is built.
type starsVideoMap struct {
	config videoMapConfig
	cmd    *renderer.CmdBuffer
}

type starsMapFeatureCollection struct {
	Features []starsMapFeature `json:"features"`
}

type starsMapFeature struct {
	Properties *starsMapProperties `json:"properties"`
	Geometry   starsMapGeometry    `json:"geometry"`
}

type starsMapGeometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

type starsMapProperties struct {
	IsLineDefaults bool   `json:"isLineDefaults"`
	Style          string `json:"style"`
}

func starsMapResourcePath(artcc string) string {
	return filepath.ToSlash(filepath.Join(
		"resources",
		"videomaps",
		"stars",
		strings.ToUpper(strings.TrimSpace(artcc))+".json.zst",
	))
}

// loadMainVideoMaps preloads the six position-adapted Main DCB maps in one
// streaming pass over the ARTCC bundle. The ARTCC packages can contain maps
// for many TRACONs; decoding only these requested maps avoids retaining tens
// of megabytes of unrelated GeoJSON in RAM.
func (p *STARSPane) loadMainVideoMaps() error {
	if p == nil {
		return nil
	}
	maps := p.mainDCBMaps()
	return p.loadVideoMaps(maps[:])
}

// loadVideoMaps loads and pre-bakes any requested maps that are not already
// cached. It is intentionally generic so the future MAPS submenu can use the
// exact same path as the six Main DCB buttons.
func (p *STARSPane) loadVideoMaps(maps []videoMapConfig) error {
	if p == nil || len(maps) == 0 {
		return nil
	}
	if p.videoMaps == nil {
		p.videoMaps = make(map[int]*starsVideoMap)
	}

	wanted := make(map[string]videoMapConfig)
	for _, vm := range maps {
		if vm.ID == "" || vm.STARSID == 0 || p.videoMaps[vm.STARSID] != nil {
			continue
		}
		wanted[vm.ID] = vm
	}
	if len(wanted) == 0 {
		return nil
	}

	path := starsMapResourcePath(p.config.Facility.ARTCC)
	r := util.LoadResource(path)
	defer r.Close()

	dec := json.NewDecoder(r)
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("decode STARS map package %s: %w", path, err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("decode STARS map package %s: expected top-level object", path)
	}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("decode STARS map package %s: %w", path, err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("decode STARS map package %s: expected object key", path)
		}

		if key != "videoMaps" {
			if err := skipSTARSJSONValue(dec); err != nil {
				return fmt.Errorf("decode STARS map package %s: %w", path, err)
			}
			continue
		}

		arrayTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("decode STARS map package %s: %w", path, err)
		}
		if delim, ok := arrayTok.(json.Delim); !ok || delim != '[' {
			return fmt.Errorf("decode STARS map package %s: videoMaps is not an array", path)
		}

		for dec.More() && len(wanted) != 0 {
			id, raw, err := decodeWantedSTARSMap(dec, wanted)
			if err != nil {
				return fmt.Errorf("decode STARS map package %s: %w", path, err)
			}
			cfg, ok := wanted[id]
			if !ok {
				continue
			}

			cmd, err := buildSTARSVideoMapCommands(raw)
			if err != nil {
				return fmt.Errorf("build STARS map %d %q: %w", cfg.STARSID, cfg.Name, err)
			}
			p.videoMaps[cfg.STARSID] = &starsVideoMap{config: cfg, cmd: cmd}
			delete(wanted, id)
		}

		// If every requested map was found we deliberately stop before
		// decompressing/parsing the rest of the ARTCC's map package.
		if len(wanted) == 0 {
			return nil
		}

		for dec.More() {
			if err := skipSTARSJSONValue(dec); err != nil {
				return fmt.Errorf("decode STARS map package %s: %w", path, err)
			}
		}
		if _, err := dec.Token(); err != nil { // ']'
			return fmt.Errorf("decode STARS map package %s: %w", path, err)
		}
	}

	if len(wanted) == 0 {
		return nil
	}

	missing := make([]string, 0, len(wanted))
	for _, cfg := range wanted {
		missing = append(missing, fmt.Sprintf("%d %s", cfg.STARSID, cfg.ShortName))
	}
	sort.Strings(missing)
	return fmt.Errorf("STARS map package %s is missing adapted maps: %s", path, strings.Join(missing, ", "))
}

// decodeWantedSTARSMap reads one map-package entry. REDS' crc2reds generator
// writes id before geojson, allowing unrequested GeoJSON objects to be skipped
// directly from the stream rather than materialized as json.RawMessage.
func decodeWantedSTARSMap(dec *json.Decoder, wanted map[string]videoMapConfig) (string, json.RawMessage, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return "", nil, fmt.Errorf("expected video-map object")
	}

	var id string
	var raw json.RawMessage
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return "", nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return "", nil, fmt.Errorf("expected video-map object key")
		}

		switch key {
		case "id":
			if err := dec.Decode(&id); err != nil {
				return "", nil, err
			}
		case "geojson":
			if _, keep := wanted[id]; keep {
				if err := dec.Decode(&raw); err != nil {
					return "", nil, err
				}
			} else if err := skipSTARSJSONValue(dec); err != nil {
				return "", nil, err
			}
		default:
			if err := skipSTARSJSONValue(dec); err != nil {
				return "", nil, err
			}
		}
	}
	if _, err := dec.Token(); err != nil { // '}'
		return "", nil, err
	}
	return id, raw, nil
}

func skipSTARSJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		for dec.More() {
			if _, err := dec.Token(); err != nil { // key
				return err
			}
			if err := skipSTARSJSONValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := skipSTARSJSONValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	default:
		return nil
	}
}

func buildSTARSVideoMapCommands(raw json.RawMessage) (*renderer.CmdBuffer, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var fc starsMapFeatureCollection
	if err := json.Unmarshal(raw, &fc); err != nil {
		return nil, err
	}

	// Keep this builder local rather than returning a potentially very large
	// backing allocation to the global renderer pool; only the finished
	// CmdBuffer geometry needs to remain resident after map load.
	var builder renderer.LinesBuilder

	var defaultStyle string
	hasGeometry := false
	for _, feature := range fc.Features {
		if feature.Properties != nil && feature.Properties.IsLineDefaults {
			defaultStyle = feature.Properties.Style
			continue
		}
		if feature.Geometry.Type != "LineString" && feature.Geometry.Type != "MultiLineString" {
			// VICE's current STARS path likewise pre-bakes only map line
			// geometry. Symbols/text remain available in the resource for a
			// later rendering pass without changing map selection state.
			continue
		}

		style := defaultStyle
		if feature.Properties != nil && feature.Properties.Style != "" {
			style = feature.Properties.Style
		}
		if style != "" && !strings.EqualFold(style, "solid") {
			// VICE's current STARSPane draws the solid-line command buffer at
			// line width 1. Keep that exact foundation here; dashed styles can
			// later be layered from the same source geometry.
			continue
		}

		added, err := appendSTARSMapLineGeometry(&builder, feature.Geometry)
		if err != nil {
			return nil, err
		}
		hasGeometry = hasGeometry || added
	}

	if !hasGeometry {
		return nil, nil
	}
	cb := renderer.GetCmdBuffer()
	builder.GenerateCommands(cb)
	return cb, nil
}

func appendSTARSMapLineGeometry(builder *renderer.LinesBuilder, geometry starsMapGeometry) (bool, error) {
	appendPolyline := func(coords [][]float64) bool {
		if len(coords) < 2 {
			return false
		}
		points := make([]renderer.PointVertex, 0, len(coords))
		for _, c := range coords {
			if len(c) < 2 {
				continue
			}
			points = append(points, renderer.PointVertex{X: float32(c[0]), Y: float32(c[1])})
		}
		if len(points) < 2 {
			return false
		}
		builder.AddLineStrip(points)
		return true
	}

	switch geometry.Type {
	case "LineString":
		var coords [][]float64
		if err := json.Unmarshal(geometry.Coordinates, &coords); err != nil {
			return false, err
		}
		return appendPolyline(coords), nil

	case "MultiLineString":
		var lines [][][]float64
		if err := json.Unmarshal(geometry.Coordinates, &lines); err != nil {
			return false, err
		}
		added := false
		for _, coords := range lines {
			added = appendPolyline(coords) || added
		}
		return added, nil
	}

	return false, nil
}

func (p *STARSPane) videoMapColor(vm videoMapConfig) renderer.RGB {
	ps := p.currentPrefs()
	if strings.EqualFold(strings.TrimSpace(vm.BrightnessCategory), "B") {
		// TI 6191.409 Rev. 30 Table B-1 defines Category B Maps separately
		// from its numbered multi-color categories. CRC currently gives REDS
		// only the A/B brightness category, so use the documented B default
		// color and MPB brightness rather than guessing a numbered color.
		return ps.Brightness.VideoGroupB.ScaleRGB(p.colors.MapBDefault)
	}

	// Category A is the CRC/default fallback for maps without an explicit B.
	return ps.Brightness.VideoGroupA.ScaleRGB(p.colors.MapADefault)
}

func (p *STARSPane) drawVideoMaps(ctx *panes.Context, zcb *renderer.ZCmdBuffer, transforms radar.LatLonTransformations) {
	if p == nil || ctx == nil || zcb == nil || len(p.videoMaps) == 0 {
		return
	}

	ps := p.currentPrefs()
	ids := make([]int, 0, len(ps.VideoMapVisible))
	for id, visible := range ps.VideoMapVisible {
		if visible && p.videoMaps[id] != nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	sort.Ints(ids) // VICE draws selected video maps in ascending STARS id order.

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zVideoMaps)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	transforms.LoadGeoViewingMatrices(cb)

	// VICE's STARS renderer uses a one-display-pixel map line. REDS line
	// widths are framebuffer pixels, so scale that logical pixel by DPI.
	lineWidth := ctx.DPIScale
	if lineWidth < 1 {
		lineWidth = 1
	}
	cb.LineWidth(lineWidth)

	for _, id := range ids {
		vm := p.videoMaps[id]
		if vm == nil || vm.cmd == nil {
			continue
		}
		cb.SetRGB(p.videoMapColor(vm.config))
		cb.Call(vm.cmd)
	}
	cb.DisableScissor()
}

func (p *STARSPane) releaseVideoMaps() {
	if p == nil {
		return
	}
	for id, vm := range p.videoMaps {
		if vm != nil && vm.cmd != nil {
			renderer.ReturnCmdBuffer(vm.cmd)
			vm.cmd = nil
		}
		delete(p.videoMaps, id)
	}
}
