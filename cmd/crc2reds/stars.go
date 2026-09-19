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
	VideoMaps         []crcSTARSVideoMap `json:"videoMaps"`
}

type crcSTARSFacility struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name"`

	Positions []crcSTARSPosition `json:"positions"`

	STARSConfiguration *crcSTARSConfiguration `json:"starsConfiguration"`

	ChildFacilities []crcSTARSFacility `json:"childFacilities"`
}

type crcSTARSConfiguration struct {
	Areas       []crcSTARSArea     `json:"areas"`
	VideoMapIDs []string           `json:"videoMapIds"`
	MapGroups   []crcSTARSMapGroup `json:"mapGroups"`
	TCPs        []crcSTARSTCP      `json:"tcps"`
}

type crcSTARSMapGroup struct {
	MapIDs []*int   `json:"mapIds"`
	TCPs   []string `json:"tcps"`
}

type crcSTARSArea struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	VisibilityCenter   json.RawMessage `json:"visibilityCenter"`
	SurveillanceRange  int             `json:"surveillanceRange"`
	UnderlyingAirports []string        `json:"underlyingAirports"`
	SSAAirports        []string        `json:"ssaAirports"`
}

type crcSTARSTCP struct {
	Subset         int    `json:"subset"`
	SectorID       string `json:"sectorId"`
	ID             string `json:"id"`
	ParentTCPID    string `json:"parentTcpId"`
	TerminalSector string `json:"terminalSector"`
}

type crcSTARSPositionConfiguration struct {
	AreaID string `json:"areaId"`
	TCPID  string `json:"tcpId"`
}

type crcSTARSPosition struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	RadioName string `json:"radioName"`
	Callsign  string `json:"callsign"`

	Frequency starsFloat64 `json:"frequency"`

	STARSConfiguration *crcSTARSPositionConfiguration `json:"starsConfiguration"`
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
	ID               string           `json:"id"`
	PhysicalFacility string           `json:"physicalFacility,omitempty"`
	Name             string           `json:"name,omitempty"`
	RadioName        string           `json:"radioName,omitempty"`
	Callsign         string           `json:"callsign,omitempty"`
	AreaID           string           `json:"areaId"`
	TCPID            string           `json:"tcpId,omitempty"`
	TCP              string           `json:"tcp,omitempty"`
	VisualCenter     *redsSTARSLatLon `json:"visualCenter,omitempty"`
	Range            *float64         `json:"range,omitempty"`
	Frequency        float64          `json:"freq,omitempty"`
}

type redsSTARSArea struct {
	ID                 string           `json:"id"`
	Name               string           `json:"name,omitempty"`
	VisibilityCenter   *redsSTARSLatLon `json:"visibilityCenter,omitempty"`
	SurveillanceRange  int              `json:"surveillanceRange"`
	UnderlyingAirports []string         `json:"underlyingAirports,omitempty"`
	SSAAirports        []string         `json:"ssaAirports,omitempty"`
}

type redsSTARSTCP struct {
	ID             string `json:"id"`
	Code           string `json:"code"`
	Subset         int    `json:"subset"`
	SectorID       string `json:"sectorId"`
	ParentTCPID    string `json:"parentTcpId,omitempty"`
	ParentTCP      string `json:"parentTcp,omitempty"`
	TerminalSector string `json:"terminalSector,omitempty"`
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

	Areas            []redsSTARSArea            `json:"areas,omitempty"`
	TCPs             []redsSTARSTCP             `json:"tcps,omitempty"`
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
		return fmt.Errorf("usage: crc2reds stars <config|maps|audio> ...")
	}

	switch args[0] {
	case "config":
		return runStarsConfig(args[1:])
	case "maps":
		return runStarsMaps(args[1:])
	case "audio":
		return runStarsAudio(args[1:])
	default:
		return fmt.Errorf(
			"unknown STARS conversion %q; expected config, maps, or audio",
			args[0],
		)
	}
}

// runStarsAudio extracts CRC's shared STARS/ATC sounds.
//
// CRC's SoundService loads shared sounds from:
//
//	<CRC install>/Sounds/<name>.wav
//
// Only root-level WAV files are copied. We intentionally do not recurse into
// Sounds subdirectories because those may contain display-specific sound sets
// such as ASDE-X.
func runStarsAudio(args []string) error {
	fs := flag.NewFlagSet("stars audio", flag.ContinueOnError)

	inPath := fs.String(
		"in",
		"",
		"CRC data root containing Sounds/",
	)
	outDir := fs.String(
		"out",
		"",
		"output directory for STARS WAV resources",
	)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *inPath == "" || *outDir == "" {
		return fmt.Errorf(
			"usage: crc2reds stars audio -in /path/to/CRC -out resources/audio/stars",
		)
	}

	return convertSTARSAudio(*inPath, *outDir)
}

func convertSTARSAudio(root, outDir string) error {
	soundsDir := filepath.Join(root, "Sounds")

	entries, err := os.ReadDir(soundsDir)
	if err != nil {
		return fmt.Errorf(
			"read CRC sounds directory %s: %w",
			soundsDir,
			err,
		)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if strings.HasPrefix(name, "._") {
			continue
		}
		if !strings.EqualFold(filepath.Ext(name), ".wav") {
			continue
		}

		names = append(names, name)
	}

	sort.Strings(names)

	if len(names) == 0 {
		return fmt.Errorf(
			"no root-level .wav files found in %s",
			soundsDir,
		)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	for _, name := range names {
		src := filepath.Join(soundsDir, name)
		dst := filepath.Join(outDir, name)

		raw, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read %s: %w", src, err)
		}

		// Preserve CRC's WAV payload exactly; only the filesystem location
		// changes.
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dst, err)
		}
	}

	fmt.Printf(
		"wrote %d STARS/shared CRC WAV files to %s\n",
		len(names),
		outDir,
	)

	return nil
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

		cfg, err := buildSTARSFacilityConfig(
			artcc,
			facility,
			rootCenter,
			videoByID,
		)
		if err != nil {
			return written, fmt.Errorf("build STARS config %s/%s: %w", artcc, facility.ID, err)
		}

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
	facility *crcSTARSFacility,
	rootCenter *redsSTARSLatLon,
	videoByID map[string]crcSTARSVideoMap,
) (redsSTARSFacilityConfig, error) {
	stars := facility.STARSConfiguration
	if stars == nil {
		return redsSTARSFacilityConfig{}, fmt.Errorf("facility has no STARS configuration")
	}

	center := starsMeanAreaCenters(stars.Areas)
	if center == nil {
		center = rootCenter
	}

	cfg := redsSTARSFacilityConfig{
		ARTCC:         artcc,
		Facility:      strings.ToUpper(facility.ID),
		Name:          facility.Name,
		Type:          facility.Type,
		DefaultCenter: center,
	}

	areaByID := make(map[string]crcSTARSArea, len(stars.Areas))
	for _, area := range stars.Areas {
		id := strings.TrimSpace(area.ID)
		if id == "" {
			return redsSTARSFacilityConfig{}, fmt.Errorf("STARS area with empty id")
		}
		if _, exists := areaByID[id]; exists {
			return redsSTARSFacilityConfig{}, fmt.Errorf("duplicate STARS area id %q", id)
		}
		areaByID[id] = area

		cfg.Areas = append(cfg.Areas, redsSTARSArea{
			ID:                 id,
			Name:               area.Name,
			VisibilityCenter:   starsDecodeLatLon(area.VisibilityCenter),
			SurveillanceRange:  starsAreaRange(area),
			UnderlyingAirports: append([]string(nil), area.UnderlyingAirports...),
			SSAAirports:        append([]string(nil), area.SSAAirports...),
		})
	}
	sort.Slice(cfg.Areas, func(i, j int) bool { return cfg.Areas[i].ID < cfg.Areas[j].ID })

	tcpByID := make(map[string]crcSTARSTCP, len(stars.TCPs))
	tcpByCode := make(map[string]crcSTARSTCP, len(stars.TCPs))
	for _, tcp := range stars.TCPs {
		id := strings.TrimSpace(tcp.ID)
		if id == "" {
			return redsSTARSFacilityConfig{}, fmt.Errorf("STARS TCP with empty unique id")
		}
		code := starsTCPCode(tcp)
		if code == "" {
			return redsSTARSFacilityConfig{}, fmt.Errorf("STARS TCP %q has empty operational code", id)
		}
		if _, exists := tcpByID[id]; exists {
			return redsSTARSFacilityConfig{}, fmt.Errorf("duplicate STARS TCP unique id %q", id)
		}
		if other, exists := tcpByCode[code]; exists {
			return redsSTARSFacilityConfig{}, fmt.Errorf(
				"duplicate STARS TCP code %q for ids %q and %q",
				code,
				other.ID,
				id,
			)
		}
		tcpByID[id] = tcp
		tcpByCode[code] = tcp
	}

	for _, tcp := range stars.TCPs {
		parentCode := ""
		parentID := strings.TrimSpace(tcp.ParentTCPID)
		if parentID != "" {
			if parent, ok := tcpByID[parentID]; ok {
				parentCode = starsTCPCode(parent)
			}
		}

		cfg.TCPs = append(cfg.TCPs, redsSTARSTCP{
			ID:             strings.TrimSpace(tcp.ID),
			Code:           starsTCPCode(tcp),
			Subset:         tcp.Subset,
			SectorID:       strings.TrimSpace(tcp.SectorID),
			ParentTCPID:    parentID,
			ParentTCP:      parentCode,
			TerminalSector: strings.TrimSpace(tcp.TerminalSector),
		})
	}
	sort.Slice(cfg.TCPs, func(i, j int) bool {
		if cfg.TCPs[i].Code != cfg.TCPs[j].Code {
			return cfg.TCPs[i].Code < cfg.TCPs[j].Code
		}
		return cfg.TCPs[i].ID < cfg.TCPs[j].ID
	})

	for _, fp := range starsPositionsForRadarFacility(facility) {
		p := fp.Position
		if p.STARSConfiguration == nil {
			continue
		}

		positionID := strings.TrimSpace(p.ID)
		if positionID == "" {
			return redsSTARSFacilityConfig{}, fmt.Errorf(
				"STARS position %q has empty position id",
				p.Callsign,
			)
		}

		areaID := strings.TrimSpace(p.STARSConfiguration.AreaID)
		area, ok := areaByID[areaID]
		if !ok {
			return redsSTARSFacilityConfig{}, fmt.Errorf(
				"position %q (%s) references unknown STARS area %q",
				p.Callsign,
				positionID,
				areaID,
			)
		}

		tcpID := strings.TrimSpace(p.STARSConfiguration.TCPID)
		tcpCode := ""
		if tcpID != "" {
			tcp, ok := tcpByID[tcpID]
			if !ok {
				return redsSTARSFacilityConfig{}, fmt.Errorf(
					"position %q (%s) references unknown STARS TCP id %q",
					p.Callsign,
					positionID,
					tcpID,
				)
			}
			tcpCode = starsTCPCode(tcp)
		}

		rangeNM := float64(starsAreaRange(area))
		cfg.ControlPositions = append(cfg.ControlPositions, redsSTARSControlPosition{
			ID:               positionID,
			PhysicalFacility: fp.FacilityID,
			Name:             p.Name,
			RadioName:        p.RadioName,
			Callsign:         p.Callsign,
			AreaID:           areaID,
			TCPID:            tcpID,
			TCP:              tcpCode,
			VisualCenter:     starsDecodeLatLon(area.VisibilityCenter),
			Range:            &rangeNM,
			Frequency:        starsFrequencyMHz(float64(p.Frequency)),
		})
	}

	sort.Slice(cfg.ControlPositions, func(i, j int) bool {
		a, b := cfg.ControlPositions[i], cfg.ControlPositions[j]
		if (a.TCP == "") != (b.TCP == "") {
			return a.TCP != ""
		}
		if a.TCP != b.TCP {
			return a.TCP < b.TCP
		}
		if a.Callsign != b.Callsign {
			return a.Callsign < b.Callsign
		}
		return a.ID < b.ID
	})

	for _, mg := range stars.MapGroups {
		cfg.MapGroups = append(cfg.MapGroups, redsSTARSMapGroup{
			TCPs:          append([]string(nil), mg.TCPs...),
			MapIDs:        starsCloneMapIDs(mg.MapIDs),
			MainMapIDs:    starsTransposeMapIDs(mg.MapIDs, 0, 3),
			SubmenuMapIDs: starsTransposeMapIDs(mg.MapIDs, 6, 15),
		})
	}

	seen := make(map[string]bool)
	for _, id := range stars.VideoMapIDs {
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

	return cfg, nil
}

type starsFacilityPosition struct {
	FacilityID string
	Position   crcSTARSPosition
}

func starsPositionsForRadarFacility(f *crcSTARSFacility) []starsFacilityPosition {
	out := make([]starsFacilityPosition, 0, len(f.Positions))
	for _, p := range f.Positions {
		out = append(out, starsFacilityPosition{FacilityID: f.ID, Position: p})
	}

	// CRC's Artcc.GetStarsFacility() uses a STARS-configured parent facility
	// as the radar facility for a direct child that has no STARS configuration
	// of its own. Include those physical-facility positions here as well so the
	// generated radar-facility config has the same position -> TCP mapping.
	for i := range f.ChildFacilities {
		child := &f.ChildFacilities[i]
		if child.STARSConfiguration != nil {
			continue
		}
		for _, p := range child.Positions {
			out = append(out, starsFacilityPosition{FacilityID: child.ID, Position: p})
		}
	}

	return out
}

func starsTCPCode(tcp crcSTARSTCP) string {
	sectorID := strings.TrimSpace(tcp.SectorID)
	if sectorID == "" {
		return ""
	}
	return fmt.Sprintf("%d%s", tcp.Subset, sectorID)
}

func starsAreaRange(area crcSTARSArea) int {
	if area.SurveillanceRange > 0 {
		return area.SurveillanceRange
	}
	// This is the default initializer in CRC's StarsArea class.
	return 50
}

func starsMeanAreaCenters(areas []crcSTARSArea) *redsSTARSLatLon {
	var lat float64
	var lon float64
	n := 0
	for _, area := range areas {
		center := starsDecodeLatLon(area.VisibilityCenter)
		if center == nil {
			continue
		}
		lat += center.Lat
		lon += center.Lon
		n++
	}
	if n == 0 {
		return nil
	}
	return &redsSTARSLatLon{Lat: lat / float64(n), Lon: lon / float64(n)}
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
