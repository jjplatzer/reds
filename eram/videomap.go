package eram

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util"
)

const (
	eramMapFilterCount        = 40
	eramMapBCGCount           = 40
	defaultMapGroupBrightness = 12
)

type eramMapPackage struct {
	ARTCC     string         `json:"artcc"`
	GeoMaps   []eramGeoMap   `json:"geoMaps"`
	VideoMaps []eramVideoMap `json:"videoMaps"`
}

type eramGeoMap struct {
	Name        string          `json:"name"`
	LabelLine1  string          `json:"labelLine1"`
	LabelLine2  string          `json:"labelLine2"`
	FilterMenu  []eramMapFilter `json:"filterMenu"`
	BCGMenu     []string        `json:"bcgMenu"`
	VideoMapIDs []string        `json:"videoMapIds"`
}

type eramMapFilter struct {
	LabelLine1 string `json:"labelLine1"`
	LabelLine2 string `json:"labelLine2"`
}

type eramVideoMap struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	TDMOnly bool            `json:"tdmOnly"`
	GeoJSON json.RawMessage `json:"geojson"`
}

type eramMapState struct {
	geoMaps      []eramGeoMap
	activeGeoMap int

	// Bit 0 = CRC filter 1, ..., bit 39 = CRC filter 40.
	filters uint64

	// CRC defaults MapGroup1 through MapGroup40 to 12.
	brightness [eramMapBCGCount]int

	lines []eramMapLineBatch
}

type eramMapLineStyle uint8

const (
	eramMapLineSolid eramMapLineStyle = iota
	eramMapLineShortDashed
	eramMapLineLongDashed
	eramMapLineLongDashShortDash
)

type eramMapFilterMask struct {
	Always bool
	Bits   uint64
}

type eramMapLineBatchKey struct {
	Filters   eramMapFilterMask
	BCG       uint8
	Thickness uint8
	Style     eramMapLineStyle
	TDMOnly   bool
}

type eramMapLineBatch struct {
	Key eramMapLineBatchKey
	Cmd *renderer.CmdBuffer
}

type eramGeoJSONFeatureCollection struct {
	Type     string               `json:"type"`
	Features []eramGeoJSONFeature `json:"features"`
}

type eramGeoJSONFeature struct {
	Properties eramGeoJSONProperties `json:"properties"`
	Geometry   eramGeoJSONGeometry   `json:"geometry"`
}

type eramGeoJSONGeometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

type eramGeoJSONProperties struct {
	Filters   *[]int  `json:"filters"`
	BCG       *int    `json:"bcg"`
	Style     *string `json:"style"`
	Thickness *int    `json:"thickness"`

	IsLineDefaults   bool `json:"isLineDefaults"`
	IsSymbolDefaults bool `json:"isSymbolDefaults"`
	IsTextDefaults   bool `json:"isTextDefaults"`

	Size      *int            `json:"size"`
	Text      json.RawMessage `json:"text"`
	Underline *bool           `json:"underline"`
	Opaque    *bool           `json:"opaque"`
	XOffset   *int            `json:"xOffset"`
	YOffset   *int            `json:"yOffset"`
}

type eramLineDefaults struct {
	Filters   []int
	BCG       int
	Style     eramMapLineStyle
	Thickness int
}

var eramMapWhite = renderer.RGB8(243, 243, 243)

func eramMapResourcePath(artcc string) string {
	return filepath.ToSlash(filepath.Join(
		"resources",
		"videomaps",
		"eram",
		strings.ToUpper(strings.TrimSpace(artcc))+".json.zst",
	))
}

func (p *ERAMPane) initializeMapState() {
	if p == nil {
		return
	}
	p.maps.activeGeoMap = -1
	for i := range p.maps.brightness {
		p.maps.brightness[i] = defaultMapGroupBrightness
	}
}

func (p *ERAMPane) loadGeoMapMetadata() error {
	if p == nil {
		return nil
	}

	path := eramMapResourcePath(p.artcc)
	r := util.LoadResource(path)
	defer r.Close()

	artcc, geoMaps, err := decodeERAMGeoMapMetadata(json.NewDecoder(r))
	if err != nil {
		return fmt.Errorf("decode ERAM map package %s: %w", path, err)
	}
	if artcc != "" && !strings.EqualFold(artcc, p.artcc) {
		return fmt.Errorf("ERAM map package %s is for %s, expected %s", path, artcc, p.artcc)
	}

	p.maps.geoMaps = append(p.maps.geoMaps[:0], geoMaps...)
	if len(p.maps.geoMaps) > 0 {
		p.maps.activeGeoMap = 0
	} else {
		p.maps.activeGeoMap = -1
	}

	return nil
}

func decodeERAMGeoMapMetadata(dec *json.Decoder) (string, []eramGeoMap, error) {
	token, err := dec.Token()
	if err != nil {
		return "", nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return "", nil, fmt.Errorf("expected top-level object")
	}

	var artcc string
	var geoMaps []eramGeoMap

	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return "", nil, err
		}
		key, ok := token.(string)
		if !ok {
			return "", nil, fmt.Errorf("expected object key")
		}

		switch key {
		case "artcc":
			if err := dec.Decode(&artcc); err != nil {
				return "", nil, err
			}
		case "geoMaps":
			if err := dec.Decode(&geoMaps); err != nil {
				return "", nil, err
			}
			// REDS-generated map packages write ARTCC before GeoMaps and the
			// large VideoMaps array after it. Toolbar metadata needs nothing
			// beyond GeoMaps, so stop before scanning embedded coordinates.
			return artcc, geoMaps, nil
		default:
			if err := skipJSONValue(dec); err != nil {
				return "", nil, err
			}
		}
	}

	token, err = dec.Token()
	if err != nil {
		return "", nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '}' {
		return "", nil, fmt.Errorf("expected end of top-level object")
	}

	return artcc, geoMaps, nil
}

func skipJSONValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}

	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		for dec.More() {
			if _, err := dec.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err

	case '[':
		for dec.More() {
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	}

	return nil
}

func (p *ERAMPane) activeGeoMap() *eramGeoMap {
	if p == nil ||
		p.maps.activeGeoMap < 0 ||
		p.maps.activeGeoMap >= len(p.maps.geoMaps) {
		return nil
	}

	return &p.maps.geoMaps[p.maps.activeGeoMap]
}

func mapFilterButtonIndex(id toolbarButtonID) (int, bool) {
	const prefix = "map-filter-"

	value := string(id)
	if !strings.HasPrefix(value, prefix) {
		return 0, false
	}

	n, err := strconv.Atoi(strings.TrimPrefix(value, prefix))
	if err != nil || n < 1 || n > eramMapFilterCount {
		return 0, false
	}

	return n - 1, true
}

func (p *ERAMPane) toggleMapFilter(index int) {
	if p == nil || index < 0 || index >= eramMapFilterCount {
		return
	}

	p.maps.filters ^= uint64(1) << uint(index)
}

func (p *ERAMPane) loadActiveGeoMapGeometry() error {
	if p == nil {
		return nil
	}

	p.releaseGeoMapCmdBuffers()

	geoMap := p.activeGeoMap()
	if geoMap == nil {
		return nil
	}

	wanted := make(map[string]struct{}, len(geoMap.VideoMapIDs))
	for _, id := range geoMap.VideoMapIDs {
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	path := eramMapResourcePath(p.artcc)
	r := util.LoadResource(path)
	defer r.Close()

	builders, err := p.buildActiveGeoMapLineBuilders(json.NewDecoder(r), wanted)
	if err != nil {
		return fmt.Errorf("load ERAM map geometry %s: %w", path, err)
	}

	keys := make([]eramMapLineBatchKey, 0, len(builders))
	for key := range builders {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return compareMapLineBatchKeys(keys[i], keys[j]) < 0
	})

	for _, key := range keys {
		builder := builders[key]
		cb := renderer.GetCmdBuffer()
		builder.GenerateCommands(cb)
		p.maps.lines = append(p.maps.lines, eramMapLineBatch{
			Key: key,
			Cmd: cb,
		})
		renderer.ReturnLinesBuilder(builder)
		delete(builders, key)
	}

	return nil
}

func (p *ERAMPane) buildActiveGeoMapLineBuilders(
	dec *json.Decoder,
	wanted map[string]struct{},
) (map[eramMapLineBatchKey]*renderer.LinesBuilder, error) {
	builders := make(map[eramMapLineBatchKey]*renderer.LinesBuilder)

	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("expected top-level object")
	}

	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("expected object key")
		}

		if key != "videoMaps" {
			if err := skipJSONValue(dec); err != nil {
				return nil, err
			}
			continue
		}

		token, err = dec.Token()
		if err != nil {
			return nil, err
		}
		if delim, ok := token.(json.Delim); !ok || delim != '[' {
			return nil, fmt.Errorf("expected videoMaps array")
		}

		for dec.More() {
			var vm eramVideoMap
			if err := dec.Decode(&vm); err != nil {
				return nil, err
			}
			if _, ok := wanted[vm.ID]; !ok {
				continue
			}
			if err := parseERAMVideoMapLines(vm, builders); err != nil {
				return nil, fmt.Errorf("parse video map %s: %w", vm.ID, err)
			}
		}

		token, err = dec.Token()
		if err != nil {
			return nil, err
		}
		if delim, ok := token.(json.Delim); !ok || delim != ']' {
			return nil, fmt.Errorf("expected end of videoMaps array")
		}

		return builders, nil
	}

	return builders, nil
}

func parseERAMVideoMapLines(vm eramVideoMap, builders map[eramMapLineBatchKey]*renderer.LinesBuilder) error {
	if len(vm.GeoJSON) == 0 {
		return nil
	}

	var fc eramGeoJSONFeatureCollection
	if err := json.Unmarshal(vm.GeoJSON, &fc); err != nil {
		return err
	}

	defaults := defaultERAMLineDefaults()
	for _, feature := range fc.Features {
		if feature.Properties.IsLineDefaults {
			applyERAMLineDefaults(&defaults, feature.Properties)
		}
	}

	for _, feature := range fc.Features {
		props := feature.Properties
		if props.IsLineDefaults || props.IsSymbolDefaults || props.IsTextDefaults {
			continue
		}

		resolved := resolveERAMLineProperties(defaults, props)
		key := eramMapLineBatchKey{
			Filters:   makeERAMMapFilterMask(resolved.Filters),
			BCG:       uint8(clampInt(resolved.BCG, 1, eramMapBCGCount)),
			Thickness: uint8(clampInt(resolved.Thickness, 1, 3)),
			Style:     resolved.Style,
			TDMOnly:   vm.TDMOnly,
		}

		// Dashed ERAM maps need screen-distance stipple support. Skip them for
		// this first visible-map milestone instead of drawing them incorrectly.
		if key.Style != eramMapLineSolid {
			continue
		}

		builder := builders[key]
		if builder == nil {
			builder = renderer.GetLinesBuilder()
			builders[key] = builder
		}
		if err := appendERAMLineGeometry(builder, feature.Geometry); err != nil {
			return err
		}
	}

	return nil
}

func defaultERAMLineDefaults() eramLineDefaults {
	return eramLineDefaults{
		BCG:       1,
		Style:     eramMapLineSolid,
		Thickness: 1,
	}
}

func applyERAMLineDefaults(defaults *eramLineDefaults, props eramGeoJSONProperties) {
	if defaults == nil {
		return
	}
	if props.Filters != nil {
		defaults.Filters = append([]int(nil), (*props.Filters)...)
	}
	if props.BCG != nil {
		defaults.BCG = *props.BCG
	}
	if props.Style != nil {
		defaults.Style = parseERAMMapLineStyle(*props.Style)
	}
	if props.Thickness != nil {
		defaults.Thickness = *props.Thickness
	}
}

func resolveERAMLineProperties(defaults eramLineDefaults, props eramGeoJSONProperties) eramLineDefaults {
	out := defaults
	out.Filters = append([]int(nil), defaults.Filters...)

	if props.Filters != nil {
		out.Filters = append([]int(nil), (*props.Filters)...)
	}
	if props.BCG != nil {
		out.BCG = *props.BCG
	}
	if props.Style != nil {
		out.Style = parseERAMMapLineStyle(*props.Style)
	}
	if props.Thickness != nil {
		out.Thickness = *props.Thickness
	}

	return out
}

func parseERAMMapLineStyle(style string) eramMapLineStyle {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "shortdashed":
		return eramMapLineShortDashed
	case "longdashed":
		return eramMapLineLongDashed
	case "longdashshortdash":
		return eramMapLineLongDashShortDash
	default:
		return eramMapLineSolid
	}
}

func makeERAMMapFilterMask(filters []int) eramMapFilterMask {
	var mask eramMapFilterMask

	for _, filter := range filters {
		switch {
		case filter == 0:
			mask.Always = true
		case filter >= 1 && filter <= eramMapFilterCount:
			mask.Bits |= uint64(1) << uint(filter-1)
		}
	}

	return mask
}

func mapLineVisible(mask eramMapFilterMask, enabled uint64) bool {
	return mask.Always || mask.Bits&enabled != 0
}

func appendERAMLineGeometry(builder *renderer.LinesBuilder, geometry eramGeoJSONGeometry) error {
	if builder == nil || len(geometry.Coordinates) == 0 {
		return nil
	}

	switch geometry.Type {
	case "LineString":
		var coords [][]float64
		if err := json.Unmarshal(geometry.Coordinates, &coords); err != nil {
			return err
		}
		builder.AddLineStrip(eramLineStringPoints(coords))

	case "MultiLineString":
		var lines [][][]float64
		if err := json.Unmarshal(geometry.Coordinates, &lines); err != nil {
			return err
		}
		for _, line := range lines {
			builder.AddLineStrip(eramLineStringPoints(line))
		}
	}

	return nil
}

func eramLineStringPoints(coords [][]float64) []renderer.PointVertex {
	points := make([]renderer.PointVertex, 0, len(coords))
	for _, coord := range coords {
		if len(coord) < 2 {
			continue
		}
		points = append(points, renderer.PointVertex{
			X: float32(coord[0]),
			Y: float32(coord[1]),
		})
	}
	return points
}

func (p *ERAMPane) drawGeoMaps(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || len(p.maps.lines) == 0 {
		return
	}

	paneExtent := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	transforms := radar.GetLatLonTransformations(
		paneExtent,
		p.center.Lat,
		p.center.Lon,
		p.longitudeScaleFactor,
		p.rangeNM,
	)

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zMapData)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	transforms.LoadGeoViewingMatrices(cb)

	for _, bcg := range p.orderedMapBCGs() {
		for _, batch := range p.maps.lines {
			if int(batch.Key.BCG) != bcg ||
				batch.Key.TDMOnly ||
				batch.Key.Style != eramMapLineSolid ||
				!mapLineVisible(batch.Key.Filters, p.maps.filters) {
				continue
			}

			brightness := p.maps.brightness[bcg-1]
			cb.SetRGB(applyERAMBrightness(
				eramMapWhite,
				brightness,
				p.systemBrightness,
			))
			cb.LineWidth(float32(batch.Key.Thickness))
			cb.Call(batch.Cmd)
		}
	}

	cb.LineWidth(1)
	cb.DisableScissor()
}

func (p *ERAMPane) orderedMapBCGs() [eramMapBCGCount]int {
	var bcgs [eramMapBCGCount]int
	for i := range bcgs {
		bcgs[i] = i + 1
	}

	sort.Slice(bcgs[:], func(i, j int) bool {
		return p.maps.brightness[bcgs[i]-1] <
			p.maps.brightness[bcgs[j]-1]
	})

	return bcgs
}

func (p *ERAMPane) releaseGeoMapCmdBuffers() {
	if p == nil {
		return
	}

	for _, batch := range p.maps.lines {
		renderer.ReturnCmdBuffer(batch.Cmd)
	}
	p.maps.lines = nil
}

func compareMapLineBatchKeys(a, b eramMapLineBatchKey) int {
	if a.BCG != b.BCG {
		return int(a.BCG) - int(b.BCG)
	}
	if a.Thickness != b.Thickness {
		return int(a.Thickness) - int(b.Thickness)
	}
	if a.Style != b.Style {
		return int(a.Style) - int(b.Style)
	}
	if a.TDMOnly != b.TDMOnly {
		if a.TDMOnly {
			return 1
		}
		return -1
	}
	if a.Filters.Always != b.Filters.Always {
		if a.Filters.Always {
			return -1
		}
		return 1
	}
	if a.Filters.Bits < b.Filters.Bits {
		return -1
	}
	if a.Filters.Bits > b.Filters.Bits {
		return 1
	}
	return 0
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
