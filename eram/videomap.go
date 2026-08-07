package eram

import (
	"encoding/json"
	"fmt"
	"math"
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

	dashCacheKey   eramMapDashCacheKey
	dashCacheValid bool
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

	// Solid maps are pre-baked in geographic (lon/lat) coordinates and can be
	// replayed directly under the geographic projection. Dashed maps keep their
	// original polylines so dash lengths can be reconstructed in framebuffer
	// pixels for the current range/viewport, matching CRC's screen-space
	// stippling behavior.
	Cmd       *renderer.CmdBuffer
	Polylines [][]renderer.PointVertex
}

type eramMapLineAccumulator struct {
	Builder   *renderer.LinesBuilder
	Polylines [][]renderer.PointVertex
}

type eramMapDashCacheKey struct {
	PaneWidth            float32
	PaneHeight           float32
	RangeNM              float64
	LongitudeScaleFactor float64
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

	accumulators, err := p.buildActiveGeoMapLineAccumulators(json.NewDecoder(r), wanted)
	if err != nil {
		return fmt.Errorf("load ERAM map geometry %s: %w", path, err)
	}

	keys := make([]eramMapLineBatchKey, 0, len(accumulators))
	for key := range accumulators {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return compareMapLineBatchKeys(keys[i], keys[j]) < 0
	})

	for _, key := range keys {
		acc := accumulators[key]
		batch := eramMapLineBatch{Key: key}

		if acc.Builder != nil {
			cb := renderer.GetCmdBuffer()
			acc.Builder.GenerateCommands(cb)
			batch.Cmd = cb
			renderer.ReturnLinesBuilder(acc.Builder)
		}
		batch.Polylines = acc.Polylines

		p.maps.lines = append(p.maps.lines, batch)
		delete(accumulators, key)
	}

	return nil
}

func (p *ERAMPane) buildActiveGeoMapLineAccumulators(
	dec *json.Decoder,
	wanted map[string]struct{},
) (map[eramMapLineBatchKey]*eramMapLineAccumulator, error) {
	accumulators := make(map[eramMapLineBatchKey]*eramMapLineAccumulator)

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
			if err := parseERAMVideoMapLines(vm, accumulators); err != nil {
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

		return accumulators, nil
	}

	return accumulators, nil
}

func parseERAMVideoMapLines(vm eramVideoMap, accumulators map[eramMapLineBatchKey]*eramMapLineAccumulator) error {
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

		acc := accumulators[key]
		if acc == nil {
			acc = &eramMapLineAccumulator{}
			accumulators[key] = acc
		}

		if key.Style == eramMapLineSolid {
			if acc.Builder == nil {
				acc.Builder = renderer.GetLinesBuilder()
			}
			if err := appendERAMLineGeometry(acc.Builder, feature.Geometry); err != nil {
				return err
			}
			continue
		}

		polylines, err := eramLineGeometryPolylines(feature.Geometry)
		if err != nil {
			return err
		}
		acc.Polylines = append(acc.Polylines, polylines...)
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
	if builder == nil {
		return nil
	}

	polylines, err := eramLineGeometryPolylines(geometry)
	if err != nil {
		return err
	}
	for _, points := range polylines {
		builder.AddLineStrip(points)
	}
	return nil
}

func eramLineGeometryPolylines(geometry eramGeoJSONGeometry) ([][]renderer.PointVertex, error) {
	if len(geometry.Coordinates) == 0 {
		return nil, nil
	}

	switch geometry.Type {
	case "LineString":
		var coords [][]float64
		if err := json.Unmarshal(geometry.Coordinates, &coords); err != nil {
			return nil, err
		}
		points := eramLineStringPoints(coords)
		if len(points) < 2 {
			return nil, nil
		}
		return [][]renderer.PointVertex{points}, nil

	case "MultiLineString":
		var lines [][][]float64
		if err := json.Unmarshal(geometry.Coordinates, &lines); err != nil {
			return nil, err
		}
		out := make([][]renderer.PointVertex, 0, len(lines))
		for _, line := range lines {
			points := eramLineStringPoints(line)
			if len(points) >= 2 {
				out = append(out, points)
			}
		}
		return out, nil
	}

	return nil, nil
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
	if width <= 0 || height <= 0 || paneExtent.Empty() {
		return
	}

	// Dashed line boundaries depend on range/logical viewport scale, but not on
	// pan: the geographic projection is affine, so translating the center leaves
	// all segment lengths unchanged. Rebuild only when the scale itself changes.
	p.ensureERAMDashedLineCmdBuffers(paneExtent, transforms)

	cb := zcb.At(zMapData)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	transforms.LoadGeoViewingMatrices(cb)

	for _, bcg := range p.orderedMapBCGs() {
		for _, batch := range p.maps.lines {
			if int(batch.Key.BCG) != bcg ||
				batch.Key.TDMOnly ||
				batch.Cmd == nil ||
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

func (p *ERAMPane) ensureERAMDashedLineCmdBuffers(
	paneExtent redsmath.Rect,
	transforms radar.LatLonTransformations,
) {
	if p == nil || paneExtent.Empty() {
		return
	}

	key := eramMapDashCacheKey{
		PaneWidth:            paneExtent.Width(),
		PaneHeight:           paneExtent.Height(),
		RangeNM:              p.rangeNM,
		LongitudeScaleFactor: p.longitudeScaleFactor,
	}
	if p.maps.dashCacheValid && p.maps.dashCacheKey == key {
		return
	}

	for i := range p.maps.lines {
		batch := &p.maps.lines[i]
		if batch.Key.Style == eramMapLineSolid {
			continue
		}

		if batch.Cmd != nil {
			renderer.ReturnCmdBuffer(batch.Cmd)
			batch.Cmd = nil
		}
		if len(batch.Polylines) == 0 {
			continue
		}

		builder := renderer.GetLinesBuilder()
		appendERAMDashedPolylines(
			builder,
			batch.Polylines,
			transforms,
			batch.Key.Style,
		)

		cb := renderer.GetCmdBuffer()
		builder.GenerateCommands(cb)
		renderer.ReturnLinesBuilder(builder)
		if cb.Empty() {
			renderer.ReturnCmdBuffer(cb)
			continue
		}
		batch.Cmd = cb
	}

	p.maps.dashCacheKey = key
	p.maps.dashCacheValid = true
}

func appendERAMDashedPolylines(
	builder *renderer.LinesBuilder,
	polylines [][]renderer.PointVertex,
	transforms radar.LatLonTransformations,
	style eramMapLineStyle,
) {
	if builder == nil {
		return
	}

	pattern, factor, patternLength := eramMapStipple(style)
	if factor <= 0 || patternLength <= 0 {
		return
	}

	for _, geographic := range polylines {
		if len(geographic) < 2 {
			continue
		}

		// CRC stores cumulative screen distance on each LineStrip vertex and its
		// fragment shader evaluates:
		//
		//   round(distance / stippleFactor) % patternLength
		//
		// Keep one cumulative distance for the entire strip so the dash phase
		// continues through bends rather than restarting at every segment.
		distance := float32(0)
		prevGeo := geographic[0]
		prevScreen := eramMapScreenDistancePoint(prevGeo, transforms)

		for i := 1; i < len(geographic); i++ {
			nextGeo := geographic[i]
			nextScreen := eramMapScreenDistancePoint(nextGeo, transforms)
			dx := nextScreen.X - prevScreen.X
			dy := nextScreen.Y - prevScreen.Y
			segmentLength := float32(math.Hypot(float64(dx), float64(dy)))
			if segmentLength <= 1e-5 {
				prevGeo = nextGeo
				prevScreen = nextScreen
				continue
			}

			consumed := float32(0)
			for consumed < segmentLength {
				cell := int(math.Floor(float64(distance/factor) + 0.5))
				bit := cell % patternLength
				nextBoundary := (float32(cell) + 0.5) * factor
				if nextBoundary <= distance+1e-5 {
					nextBoundary += factor
				}

				step := nextBoundary - distance
				if left := segmentLength - consumed; step > left {
					step = left
				}
				if step <= 1e-5 {
					// Guard against floating-point equality at a stipple boundary.
					step = segmentLength - consumed
				}

				if pattern&(uint32(1)<<uint(bit)) != 0 {
					t0 := consumed / segmentLength
					t1 := (consumed + step) / segmentLength
					a := lerpERAMMapPoint(prevGeo, nextGeo, t0)
					b := lerpERAMMapPoint(prevGeo, nextGeo, t1)
					builder.AddLine(a, b)
				}

				consumed += step
				distance += step
			}

			prevGeo = nextGeo
			prevScreen = nextScreen
		}
	}
}

func lerpERAMMapPoint(a, b renderer.PointVertex, t float32) renderer.PointVertex {
	return renderer.PointVertex{
		X: a.X + (b.X-a.X)*t,
		Y: a.Y + (b.Y-a.Y)*t,
	}
}

func eramMapScreenDistancePoint(
	point renderer.PointVertex,
	transforms radar.LatLonTransformations,
) renderer.PointVertex {
	window := transforms.WindowFromLatLon(float64(point.Y), float64(point.X))

	// CRC measures stipple distance in logical display coordinates before the
	// framebuffer/DPI scale is applied. Keep the floating-point position here:
	// unlike CRC's System.Drawing.Point helper this avoids a <=1 px quantization
	// dependency on pan, so the expensive dashed geometry only needs rebuilding
	// when range or viewport scale changes.
	return renderer.PointVertex{X: window.X, Y: window.Y}
}

// eramMapStipple mirrors Vatsim.Nas.Render.Engine's LineStyleExtensions.
// The pattern bits are consumed least-significant bit first by CRC's line
// shader. Factor and pattern length are measured in logical screen pixels/cells.
func eramMapStipple(style eramMapLineStyle) (pattern uint32, factor float32, patternLength int) {
	switch style {
	case eramMapLineShortDashed:
		return 1, 12, 2
	case eramMapLineLongDashed:
		return 1, 24, 2
	case eramMapLineLongDashShortDash:
		return 11, 12, 5
	default:
		return 0, 0, 0
	}
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
		if batch.Cmd != nil {
			renderer.ReturnCmdBuffer(batch.Cmd)
		}
	}
	p.maps.lines = nil
	p.maps.dashCacheKey = eramMapDashCacheKey{}
	p.maps.dashCacheValid = false
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
