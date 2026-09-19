package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// STARS adaptation in CRC lives on child facilities. The most important
// pieces are:
//
//   starsConfiguration.videoMapIds
//   starsConfiguration.mapGroups
//
// mapGroups associates TCPs (control-position ids such as "1B") with their
// DCB map layout. CRC does not expose a clean TCP -> owned-airspace polygon
// here; those boundaries are generally represented by video maps instead.

type starsFloat64 float64

func (f *starsFloat64) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*f = 0
		return nil
	}

	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		*f = starsFloat64(n)
		return nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return err
	}
	*f = starsFloat64(n)
	return nil
}

type crcSTARSARTCC struct {
	Facility crcSTARSFacility `json:"facility"`

	VisibilityCenters []json.RawMessage  `json:"visibilityCenters"`
	Positions         []crcSTARSPosition `json:"positions"`
	VideoMaps         []crcSTARSVideoMap `json:"videoMaps"`
}

type crcSTARSFacility struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`

	Positions         []crcSTARSPosition `json:"positions"`
	VisibilityCenters []json.RawMessage  `json:"visibilityCenters"`

	STARSConfiguration *crcSTARSConfiguration `json:"starsConfiguration"`

	ChildFacilities []crcSTARSFacility `json:"childFacilities"`
}

type crcSTARSConfiguration struct {
	VideoMapIDs []string           `json:"videoMapIds"`
	MapGroups   []crcSTARSMapGroup `json:"mapGroups"`

	Center       json.RawMessage `json:"center"`
	VisualCenter json.RawMessage `json:"visualCenter"`
	Range        starsFloat64    `json:"range"`
}

type crcSTARSMapGroup struct {
	MapIDs []*int   `json:"mapIds"`
	TCPs   []string `json:"tcps"`
}

type crcSTARSPosition struct {
	ID   string `json:"id"`
	TCP  string `json:"tcp"`
	Code string `json:"code"`

	Name     string `json:"name"`
	Callsign string `json:"callsign"`

	FacilityID string `json:"facilityId"`
	Facility   string `json:"facility"`
	Area       string `json:"area"`

	Scope     string `json:"scope"`
	ScopeChar string `json:"scopeChar"`

	Frequency starsFloat64 `json:"frequency"`
	Range     starsFloat64 `json:"range"`

	Center           json.RawMessage `json:"center"`
	VisualCenter     json.RawMessage `json:"visualCenter"`
	VisibilityCenter json.RawMessage `json:"visibilityCenter"`

	STARSConfiguration json.RawMessage `json:"starsConfiguration"`
}

type crcSTARSVideoMap struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	ShortName               string `json:"shortName"`
	STARSBrightnessCategory string `json:"starsBrightnessCategory"`
	STARSID                 int    `json:"starsId"`
}

type redsSTARSLatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type redsSTARSControlPosition struct {
	ID           string           `json:"id"`
	Name         string           `json:"name,omitempty"`
	Callsign     string           `json:"callsign,omitempty"`
	Scope        string           `json:"scope,omitempty"`
	Area         string           `json:"area,omitempty"`
	VisualCenter *redsSTARSLatLon `json:"visualCenter,omitempty"`
	Range        *float64         `json:"range,omitempty"`
	Frequency    float64          `json:"freq,omitempty"`
}

type redsSTARSMapGroup struct {
	TCPs []string `json:"tcps"`

	MapIDs []*int `json:"mapIds"`

	MainMapIDs    []*int `json:"mainMapIds"`
	SubmenuMapIDs []*int `json:"submenuMapIds"`
}

type redsSTARSVideoMap struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	ShortName          string `json:"shortName,omitempty"`
	STARSID            int    `json:"starsId,omitempty"`
	BrightnessCategory string `json:"brightnessCategory,omitempty"`
}

type redsSTARSFacilityConfig struct {
	ARTCC    string `json:"artcc"`
	Facility string `json:"facility"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`

	DefaultCenter *redsSTARSLatLon `json:"defaultCenter,omitempty"`
	DefaultRange  *float64         `json:"defaultRange,omitempty"`

	ControlPositions []redsSTARSControlPosition `json:"controlPositions"`

	MapGroups []redsSTARSMapGroup `json:"mapGroups,omitempty"`
	VideoMaps []redsSTARSVideoMap `json:"videoMaps,omitempty"`

	BoundaryMapCandidates []redsSTARSVideoMap `json:"boundaryMapCandidates,omitempty"`
}

type redsSTARSMapPackage struct {
	ARTCC     string                   `json:"artcc"`
	VideoMaps []redsSTARSVideoMapAsset `json:"videoMaps"`
}

type redsSTARSVideoMapAsset struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	ShortName          string          `json:"shortName,omitempty"`
	STARSID            int             `json:"starsId,omitempty"`
	BrightnessCategory string          `json:"brightnessCategory,omitempty"`
	GeoJSON            json.RawMessage `json:"geojson"`
}

func runStars(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: crc2reds stars <config> ...")
	}

	switch args[0] {
	case "config":
		return runStarsConfig(args[1:])
	case "maps":
		return runStarsMaps(args[1:])
	default:
		return fmt.Errorf("unknown STARS conversion %q; expected config or maps", args[0])
	}
}

func runStarsConfig(args []string) error {
	fs := flag.NewFlagSet("stars config", flag.ContinueOnError)

	inPath := fs.String("in", "", "CRC data root containing ARTCCs/")
	outDir := fs.String("out", "", "output directory; writes <ARTCC>/<facility>.json")
	artcc := fs.String(
		"artcc",
		"",
		"optional ARTCC id(s), comma-separated; blank processes every installed ARTCC",
	)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inPath == "" || *outDir == "" {
		return fmt.Errorf(
			"usage: crc2reds stars config -in /path/to/CRC -out resources/configs/stars [-artcc ZBW]",
		)
	}

	artccs, err := starsARTCCs(*inPath, *artcc)
	if err != nil {
		return err
	}

	total := 0
	for _, id := range artccs {
		n, err := convertSTARSConfig(*inPath, id, *outDir)
		if err != nil {
			return err
		}
		total += n
	}

	fmt.Printf("wrote %d STARS facility configs under %s\n", total, *outDir)
	return nil
}

func runStarsMaps(args []string) error {
	fs := flag.NewFlagSet("stars maps", flag.ContinueOnError)

	inPath := fs.String("in", "", "CRC data root containing ARTCCs/ and VideoMaps/")
	outDir := fs.String("out", "", "output directory; writes <ARTCC>.json.zst")
	artcc := fs.String(
		"artcc",
		"",
		"optional ARTCC id(s), comma-separated; blank processes every installed ARTCC",
	)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inPath == "" || *outDir == "" {
		return fmt.Errorf(
			"usage: crc2reds stars maps -in /path/to/CRC -out resources/videomaps/stars [-artcc ZBW]",
		)
	}

	artccs, err := starsARTCCs(*inPath, *artcc)
	if err != nil {
		return err
	}
	if len(artccs) == 0 {
		return fmt.Errorf("no ARTCC JSON files found under %s", filepath.Join(*inPath, "ARTCCs"))
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}

	written := 0
	totalMaps := 0
	for _, id := range artccs {
		outPath := filepath.Join(*outDir, id+".json.zst")
		n, err := convertSTARSMaps(*inPath, id, outPath)
		if err != nil {
			return err
		}
		if n == 0 {
			fmt.Printf("skipped %s: no STARS videomaps referenced\n", id)
			continue
		}
		written++
		totalMaps += n
	}

	fmt.Printf(
		"wrote %d STARS ARTCC map bundles (%d videomaps) under %s\n",
		written,
		totalMaps,
		*outDir,
	)
	return nil
}

func convertSTARSMaps(root, artcc, outPath string) (int, error) {
	if artcc == "" {
		return 0, fmt.Errorf("empty ARTCC")
	}

	artccPath := filepath.Join(root, "ARTCCs", artcc+".json")
	raw, err := os.ReadFile(artccPath)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", artccPath, err)
	}

	var src crcSTARSARTCC
	if err := json.Unmarshal(raw, &src); err != nil {
		return 0, fmt.Errorf("decode %s: %w", artccPath, err)
	}

	dst, err := buildRedsSTARSMapPackage(root, artcc, src)
	if err != nil {
		return 0, err
	}
	if len(dst.VideoMaps) == 0 {
		return 0, nil
	}

	encoded, err := json.Marshal(dst)
	if err != nil {
		return 0, fmt.Errorf("encode STARS maps: %w", err)
	}

	if err := writeZstd(outPath, encoded); err != nil {
		return 0, err
	}

	fmt.Printf(
		"wrote %s: %d STARS videomaps\n",
		outPath,
		len(dst.VideoMaps),
	)

	return len(dst.VideoMaps), nil
}

func buildRedsSTARSMapPackage(
	root string,
	artcc string,
	src crcSTARSARTCC,
) (redsSTARSMapPackage, error) {
	dst := redsSTARSMapPackage{
		ARTCC: artcc,
	}

	videoByID := make(map[string]crcSTARSVideoMap, len(src.VideoMaps))
	for _, vm := range src.VideoMaps {
		if vm.ID != "" {
			videoByID[vm.ID] = vm
		}
	}

	// One ARTCC bundle contains the union of maps referenced by every
	// STARS-equipped child facility. Per-facility membership and mapGroups
	// remain in resources/configs/stars/<ARTCC>/<facility>.json.
	referenced := make(map[string]struct{})
	var facilities []*crcSTARSFacility
	starsCollectFacilities(&src.Facility, &facilities)

	for _, facility := range facilities {
		if facility.STARSConfiguration == nil {
			continue
		}
		for _, id := range facility.STARSConfiguration.VideoMapIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				return redsSTARSMapPackage{}, fmt.Errorf(
					"STARS facility %q references an empty video map id",
					facility.ID,
				)
			}
			referenced[id] = struct{}{}
		}
	}

	ids := make([]string, 0, len(referenced))
	for id := range referenced {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		meta, ok := videoByID[id]
		if !ok {
			return redsSTARSMapPackage{}, fmt.Errorf(
				"STARS configuration references unknown video map %q",
				id,
			)
		}

		geoJSONPath := filepath.Join(
			root,
			"VideoMaps",
			artcc,
			id+".geojson",
		)
		geoJSON, err := os.ReadFile(geoJSONPath)
		if err != nil {
			return redsSTARSMapPackage{}, fmt.Errorf(
				"read STARS videomap %s: %w",
				id,
				err,
			)
		}

		geoJSON = bytes.TrimPrefix(
			geoJSON,
			[]byte{0xEF, 0xBB, 0xBF},
		)
		if !json.Valid(geoJSON) {
			return redsSTARSMapPackage{}, fmt.Errorf(
				"invalid GeoJSON in %s",
				geoJSONPath,
			)
		}

		dst.VideoMaps = append(
			dst.VideoMaps,
			redsSTARSVideoMapAsset{
				ID:                 id,
				Name:               meta.Name,
				ShortName:          meta.ShortName,
				STARSID:            meta.STARSID,
				BrightnessCategory: meta.STARSBrightnessCategory,
				GeoJSON:            json.RawMessage(geoJSON),
			},
		)
	}

	return dst, nil
}

func starsARTCCs(root, selected string) ([]string, error) {
	if strings.TrimSpace(selected) != "" {
		seen := make(map[string]bool)
		var ids []string

		for _, part := range strings.Split(selected, ",") {
			id := strings.ToUpper(strings.TrimSpace(part))
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}

		sort.Strings(ids)
		return ids, nil
	}

	entries, err := os.ReadDir(filepath.Join(root, "ARTCCs"))
	if err != nil {
		return nil, err
	}

	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}

		ids = append(ids, strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name))))
	}

	sort.Strings(ids)
	return ids, nil
}

func convertSTARSConfig(root, artcc, outDir string) (int, error) {
	path := filepath.Join(root, "ARTCCs", artcc+".json")

	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}

	var src crcSTARSARTCC
	if err := json.Unmarshal(raw, &src); err != nil {
		return 0, fmt.Errorf("decode %s: %w", path, err)
	}

	rootCenter := starsMeanCenters(src.VisibilityCenters)
	if rootCenter == nil {
		rootCenter = starsMeanCenters(src.Facility.VisibilityCenters)
	}

	videoByID := make(map[string]crcSTARSVideoMap, len(src.VideoMaps))
	for _, vm := range src.VideoMaps {
		if vm.ID != "" {
			videoByID[vm.ID] = vm
		}
	}

	var facilities []*crcSTARSFacility
	starsCollectFacilities(&src.Facility, &facilities)

	sort.Slice(facilities, func(i, j int) bool {
		return facilities[i].ID < facilities[j].ID
	})

	written := 0
	for _, facility := range facilities {
		if facility.ID == "" || facility.STARSConfiguration == nil {
			continue
		}

		cfg := buildSTARSFacilityConfig(
			artcc,
			&src,
			facility,
			rootCenter,
			videoByID,
		)

		dstDir := filepath.Join(outDir, artcc)
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			return written, err
		}

		dst := filepath.Join(
			dstDir,
			strings.ToUpper(facility.ID)+".json",
		)

		encoded, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return written, err
		}
		encoded = append(encoded, '\n')

		if err := os.WriteFile(dst, encoded, 0o644); err != nil {
			return written, err
		}

		fmt.Printf(
			"wrote %s: %d positions, %d map groups, %d maps, %d boundary candidates\n",
			dst,
			len(cfg.ControlPositions),
			len(cfg.MapGroups),
			len(cfg.VideoMaps),
			len(cfg.BoundaryMapCandidates),
		)

		written++
	}

	return written, nil
}

func starsCollectFacilities(f *crcSTARSFacility, out *[]*crcSTARSFacility) {
	if f.STARSConfiguration != nil {
		*out = append(*out, f)
	}

	for i := range f.ChildFacilities {
		starsCollectFacilities(&f.ChildFacilities[i], out)
	}
}

func buildSTARSFacilityConfig(
	artcc string,
	src *crcSTARSARTCC,
	facility *crcSTARSFacility,
	rootCenter *redsSTARSLatLon,
	videoByID map[string]crcSTARSVideoMap,
) redsSTARSFacilityConfig {
	center := starsFacilityCenter(facility)
	if center == nil {
		center = starsCopyCenter(rootCenter)
	}

	cfg := redsSTARSFacilityConfig{
		ARTCC:         artcc,
		Facility:      strings.ToUpper(facility.ID),
		Name:          facility.Name,
		Type:          facility.Type,
		DefaultCenter: center,
	}

	if r := float64(facility.STARSConfiguration.Range); r > 0 {
		cfg.DefaultRange = &r
	}

	positions := starsPositionsForFacility(src, facility)
	byID := make(map[string]crcSTARSPosition, len(positions))

	for _, p := range positions {
		id := starsPositionID(p)
		if id == "" {
			continue
		}
		if _, exists := byID[id]; !exists {
			byID[id] = p
		}
	}

	tcpSet := make(map[string]bool)

	for _, mg := range facility.STARSConfiguration.MapGroups {
		for _, tcp := range mg.TCPs {
			tcp = strings.TrimSpace(tcp)
			if tcp != "" {
				tcpSet[tcp] = true
			}
		}
	}
	for id := range byID {
		tcpSet[id] = true
	}

	ids := make([]string, 0, len(tcpSet))
	for id := range tcpSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		p, ok := byID[id]

		pos := redsSTARSControlPosition{
			ID:           id,
			VisualCenter: starsCopyCenter(center),
		}

		if ok {
			pos.Name = p.Name
			pos.Callsign = p.Callsign
			pos.Area = p.Area
			pos.Scope = starsFirstNonEmpty(p.Scope, p.ScopeChar)
			pos.Frequency = starsFrequencyMHz(float64(p.Frequency))

			if c := starsPositionCenter(p); c != nil {
				pos.VisualCenter = c
			}
			if r, ok := starsPositionRange(p); ok {
				pos.Range = &r
			}
		}

		cfg.ControlPositions = append(cfg.ControlPositions, pos)
	}

	for _, mg := range facility.STARSConfiguration.MapGroups {
		cfg.MapGroups = append(cfg.MapGroups, redsSTARSMapGroup{
			TCPs:          append([]string(nil), mg.TCPs...),
			MapIDs:        starsCloneMapIDs(mg.MapIDs),
			MainMapIDs:    starsTransposeMapIDs(mg.MapIDs, 0, 3),
			SubmenuMapIDs: starsTransposeMapIDs(mg.MapIDs, 6, 15),
		})
	}

	seen := make(map[string]bool)
	for _, id := range facility.STARSConfiguration.VideoMapIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		meta, ok := videoByID[id]
		if !ok {
			cfg.VideoMaps = append(
				cfg.VideoMaps,
				redsSTARSVideoMap{ID: id},
			)
			continue
		}

		vm := starsVideoMap(meta)
		cfg.VideoMaps = append(cfg.VideoMaps, vm)

		if starsLooksLikeBoundaryMap(meta) {
			cfg.BoundaryMapCandidates = append(
				cfg.BoundaryMapCandidates,
				vm,
			)
		}
	}

	sort.SliceStable(cfg.VideoMaps, func(i, j int) bool {
		a, b := cfg.VideoMaps[i], cfg.VideoMaps[j]

		if a.STARSID != b.STARSID {
			if a.STARSID == 0 {
				return false
			}
			if b.STARSID == 0 {
				return true
			}
			return a.STARSID < b.STARSID
		}

		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})

	sort.SliceStable(cfg.BoundaryMapCandidates, func(i, j int) bool {
		a := cfg.BoundaryMapCandidates[i]
		b := cfg.BoundaryMapCandidates[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})

	return cfg
}

func starsPositionsForFacility(
	src *crcSTARSARTCC,
	f *crcSTARSFacility,
) []crcSTARSPosition {
	out := append([]crcSTARSPosition(nil), f.Positions...)

	for _, p := range src.Positions {
		if starsPositionFacility(p) == f.ID {
			out = append(out, p)
		}
	}
	for _, p := range src.Facility.Positions {
		if starsPositionFacility(p) == f.ID {
			out = append(out, p)
		}
	}

	return out
}

func starsPositionFacility(p crcSTARSPosition) string {
	return strings.TrimSpace(
		starsFirstNonEmpty(p.FacilityID, p.Facility),
	)
}

func starsPositionID(p crcSTARSPosition) string {
	return strings.TrimSpace(
		starsFirstNonEmpty(p.ID, p.TCP, p.Code),
	)
}

func starsFacilityCenter(f *crcSTARSFacility) *redsSTARSLatLon {
	if f.STARSConfiguration != nil {
		if c := starsDecodeLatLon(
			f.STARSConfiguration.VisualCenter,
		); c != nil {
			return c
		}

		if c := starsDecodeLatLon(
			f.STARSConfiguration.Center,
		); c != nil {
			return c
		}
	}

	return starsMeanCenters(f.VisibilityCenters)
}

func starsPositionCenter(p crcSTARSPosition) *redsSTARSLatLon {
	for _, raw := range []json.RawMessage{
		p.VisualCenter,
		p.VisibilityCenter,
		p.Center,
	} {
		if c := starsDecodeLatLon(raw); c != nil {
			return c
		}
	}

	return starsCenterFromObject(
		p.STARSConfiguration,
		"visualCenter",
		"visibilityCenter",
		"center",
	)
}

func starsPositionRange(p crcSTARSPosition) (float64, bool) {
	if r := float64(p.Range); r > 0 {
		return r, true
	}
	return starsNumberFromObject(p.STARSConfiguration, "range")
}

func starsDecodeLatLon(raw json.RawMessage) *redsSTARSLatLon {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}

	lat, haveLat := starsNumberFromMap(obj, "lat", "latitude")
	lon, haveLon := starsNumberFromMap(
		obj,
		"lon",
		"lng",
		"longitude",
	)

	if !haveLat || !haveLon {
		return nil
	}

	return &redsSTARSLatLon{
		Lat: lat,
		Lon: lon,
	}
}

func starsCenterFromObject(
	raw json.RawMessage,
	keys ...string,
) *redsSTARSLatLon {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}

	for _, key := range keys {
		if child, ok := obj[key]; ok {
			if c := starsDecodeLatLon(child); c != nil {
				return c
			}
		}
	}

	return nil
}

func starsNumberFromObject(
	raw json.RawMessage,
	key string,
) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return 0, false
	}

	return starsNumberFromMap(obj, key)
}

func starsNumberFromMap(
	obj map[string]json.RawMessage,
	keys ...string,
) (float64, bool) {
	for _, key := range keys {
		raw, ok := obj[key]
		if !ok {
			continue
		}

		var n float64
		if err := json.Unmarshal(raw, &n); err == nil {
			return n, true
		}

		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			if err == nil {
				return n, true
			}
		}
	}

	return 0, false
}

func starsMeanCenters(
	raw []json.RawMessage,
) *redsSTARSLatLon {
	var lat float64
	var lon float64
	n := 0

	for _, item := range raw {
		c := starsDecodeLatLon(item)
		if c == nil {
			continue
		}

		lat += c.Lat
		lon += c.Lon
		n++
	}

	if n == 0 {
		return nil
	}

	return &redsSTARSLatLon{
		Lat: lat / float64(n),
		Lon: lon / float64(n),
	}
}

func starsCopyCenter(
	c *redsSTARSLatLon,
) *redsSTARSLatLon {
	if c == nil {
		return nil
	}
	out := *c
	return &out
}

func starsFrequencyMHz(v float64) float64 {
	switch {
	case v >= 1_000_000:
		return v / 1_000_000
	case v >= 1_000:
		return v / 1_000
	default:
		return v
	}
}

func starsCloneMapIDs(in []*int) []*int {
	out := make([]*int, len(in))

	for i, p := range in {
		if p == nil {
			continue
		}
		v := *p
		out[i] = &v
	}

	return out
}

func starsTransposeMapIDs(
	in []*int,
	base int,
	cols int,
) []*int {
	out := make([]*int, 2*cols)

	for col := 0; col < cols; col++ {
		for row := 0; row < 2; row++ {
			src := base + col*2 + row
			if src >= len(in) || in[src] == nil {
				continue
			}

			v := *in[src]
			out[row*cols+col] = &v
		}
	}

	return out
}

func starsVideoMap(src crcSTARSVideoMap) redsSTARSVideoMap {
	return redsSTARSVideoMap{
		ID:                 src.ID,
		Name:               src.Name,
		ShortName:          src.ShortName,
		STARSID:            src.STARSID,
		BrightnessCategory: src.STARSBrightnessCategory,
	}
}

func starsLooksLikeBoundaryMap(vm crcSTARSVideoMap) bool {
	s := strings.ToUpper(vm.Name + " " + vm.ShortName)

	for _, token := range []string{
		"BOUNDAR",
		"SECTOR",
		"AIRSPACE",
	} {
		if strings.Contains(s, token) {
			return true
		}
	}

	return false
}

func starsFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
