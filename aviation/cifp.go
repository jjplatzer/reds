package aviation

import (
	"archive/tar"
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/juliusplatzer/reds/util"
)

const (
	arinc424RecordLength = 132
	cifpRawFilename      = "FAACIFP18"
	cifpZstdResource     = "resources/nav/FAACIFP18.zst"
	cifpRawResource      = "resources/nav/FAACIFP18"
)

// ARINC424Result is the subset of the FAA CIFP parsed by VICE's
// StaticDatabase.LookupWaypoint: radio navaids/localizers plus en-route,
// airport-terminal, and heliport-terminal waypoints.
type ARINC424Result struct {
	Navaids map[string]Navaid
	Fixes   map[string]Fix
}

func parseCIFPInt(field []byte) (int, error) {
	value, err := strconv.Atoi(string(field))
	if err != nil {
		return 0, fmt.Errorf("parse CIFP integer %q: %w", string(field), err)
	}
	return value, nil
}

func cifpFieldEmpty(field []byte) bool {
	for _, b := range field {
		if b != ' ' {
			return false
		}
	}
	return true
}

func parseCIFPLatLong(lat, lon []byte) (Point, error) {
	parseDMS := func(deg, min, sec []byte) (float64, error) {
		d, err := parseCIFPInt(deg)
		if err != nil {
			return 0, err
		}
		m, err := parseCIFPInt(min)
		if err != nil {
			return 0, err
		}
		s, err := parseCIFPInt(sec)
		if err != nil {
			return 0, err
		}
		return float64(d) + float64(m)/60 + float64(s)/100/3600, nil
	}

	if len(lat) != 9 || len(lon) != 10 {
		return Point{}, fmt.Errorf("invalid CIFP latitude/longitude field lengths %d/%d", len(lat), len(lon))
	}

	latitude, err := parseDMS(lat[1:3], lat[3:5], lat[5:])
	if err != nil {
		return Point{}, err
	}
	longitude, err := parseDMS(lon[1:4], lon[4:6], lon[6:])
	if err != nil {
		return Point{}, err
	}

	switch lat[0] {
	case 'N':
	case 'S':
		latitude = -latitude
	default:
		return Point{}, fmt.Errorf("invalid CIFP latitude hemisphere %q", lat[0])
	}
	switch lon[0] {
	case 'E':
	case 'W':
		longitude = -longitude
	default:
		return Point{}, fmt.Errorf("invalid CIFP longitude hemisphere %q", lon[0])
	}

	return Point{Lat: latitude, Lon: longitude}, nil
}

// parseCIFPStationDeclination mirrors VICE's ARINC 424 5.66 handling. REDS
// uses the same sign convention here: positive west, negative east.
func parseCIFPStationDeclination(field []byte) (float64, bool, error) {
	if len(field) != 5 {
		return 0, false, fmt.Errorf("invalid CIFP station-declination length %d", len(field))
	}
	switch field[0] {
	case 'E', 'W':
		value, err := parseCIFPInt(field[1:])
		if err != nil {
			return 0, false, err
		}
		if field[0] == 'E' {
			return -float64(value) / 10, true, nil
		}
		return float64(value) / 10, true, nil
	case 'T':
		return 0, true, nil
	case 'G', ' ':
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("invalid CIFP station-declination direction %q", field[0])
	}
}

// ParseARINC424 parses the waypoint/navaid records from an FAA FAACIFP18 file.
// The byte offsets intentionally mirror VICE's ARINC424 parser. Parsing is
// streaming: the 50+ MB source file is never retained in memory.
func ParseARINC424(r io.Reader) (ARINC424Result, error) {
	result := ARINC424Result{
		Navaids: make(map[string]Navaid),
		Fixes:   make(map[string]Fix),
	}

	br := bufio.NewReaderSize(r, 64*1024)
	lineNumber := 0
	for {
		line, err := br.ReadBytes('\n')
		if len(line) == 0 && errors.Is(err, io.EOF) {
			break
		}
		lineNumber++

		line = bytesWithoutLineEnding(line)
		if len(line) != arinc424RecordLength {
			return ARINC424Result{}, fmt.Errorf("CIFP line %d: unexpected record length %d", lineNumber, len(line))
		}

		if line[0] != 'S' { // header/tail/non-standard record
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}

		section := line[4]
		switch section {
		case 'D':
			// VHF navaids and NDBs. This is the same section/subsection set
			// used by VICE's LookupWaypoint database.
			subsection := line[5]
			if subsection != ' ' && subsection != 'B' {
				break
			}
			continuation := line[21]
			if continuation != '0' && continuation != '1' {
				break
			}

			id := strings.TrimSpace(string(line[13:17]))
			// VICE excludes two-letter NDB identifiers because they are not
			// nationally unique (for example, "AA").
			if len(id) < 3 {
				break
			}

			name := strings.TrimSpace(string(line[93:123]))
			dmeID := strings.TrimSpace(string(line[51:55]))
			hasDME := !cifpFieldEmpty(line[55:74])
			var dmeLocation Point
			if hasDME {
				var parseErr error
				dmeLocation, parseErr = parseCIFPLatLong(line[55:64], line[64:74])
				if parseErr != nil {
					return ARINC424Result{}, fmt.Errorf("CIFP line %d DME %s: %w", lineNumber, id, parseErr)
				}
			}

			hasDMEElevation := hasDME && !cifpFieldEmpty(line[79:84])
			dmeElevation := 0
			if hasDMEElevation {
				var parseErr error
				dmeElevation, parseErr = parseCIFPInt(line[79:84])
				if parseErr != nil {
					return ARINC424Result{}, fmt.Errorf("CIFP line %d DME %s elevation: %w", lineNumber, id, parseErr)
				}
			}

			if !cifpFieldEmpty(line[32:51]) {
				location, parseErr := parseCIFPLatLong(line[32:41], line[41:51])
				if parseErr != nil {
					return ARINC424Result{}, fmt.Errorf("CIFP line %d navaid %s: %w", lineNumber, id, parseErr)
				}
				n := Navaid{
					ID:              id,
					Type:            map[bool]string{true: "VOR", false: "NDB"}[subsection == ' '],
					Name:            name,
					Location:        location,
					HasDME:          hasDME,
					DMELocation:     dmeLocation,
					DMEElevation:    dmeElevation,
					HasDMEElevation: hasDMEElevation,
				}
				if subsection == ' ' {
					declination, ok, parseErr := parseCIFPStationDeclination(line[74:79])
					if parseErr != nil {
						return ARINC424Result{}, fmt.Errorf("CIFP line %d navaid %s declination: %w", lineNumber, id, parseErr)
					}
					n.Declination = declination
					n.HasDeclination = ok
				}
				result.Navaids[id] = n
			} else if hasDME {
				result.Navaids[id] = Navaid{
					ID:              id,
					Type:            "DME",
					Name:            name,
					Location:        dmeLocation,
					HasDME:          true,
					DMELocation:     dmeLocation,
					DMEElevation:    dmeElevation,
					HasDMEElevation: hasDMEElevation,
				}
			}
			if hasDME && dmeID != "" && dmeID != id {
				result.Navaids[dmeID] = Navaid{
					ID:              dmeID,
					Type:            "DME",
					Name:            name,
					Location:        dmeLocation,
					HasDME:          true,
					DMELocation:     dmeLocation,
					DMEElevation:    dmeElevation,
					HasDMEElevation: hasDMEElevation,
				}
			}

		case 'E':
			if line[5] != 'A' { // en-route waypoint
				break
			}
			id := strings.TrimSpace(string(line[13:18]))
			location, parseErr := parseCIFPLatLong(line[32:41], line[41:51])
			if parseErr != nil {
				return ARINC424Result{}, fmt.Errorf("CIFP line %d waypoint %s: %w", lineNumber, id, parseErr)
			}
			result.Fixes[id] = Fix{ID: id, Location: location}

		case 'H':
			if line[12] != 'C' { // heliport terminal waypoint
				break
			}
			id := strings.TrimSpace(string(line[13:18]))
			location, parseErr := parseCIFPLatLong(line[32:41], line[41:51])
			if parseErr != nil {
				return ARINC424Result{}, fmt.Errorf("CIFP line %d heliport waypoint %s: %w", lineNumber, id, parseErr)
			}
			result.Fixes[id] = Fix{ID: id, Location: location}

		case 'P':
			switch line[12] {
			case 'C': // airport terminal waypoint
				id := strings.TrimSpace(string(line[13:18]))
				location, parseErr := parseCIFPLatLong(line[32:41], line[41:51])
				if parseErr != nil {
					return ARINC424Result{}, fmt.Errorf("CIFP line %d airport waypoint %s: %w", lineNumber, id, parseErr)
				}
				result.Fixes[id] = Fix{ID: id, Location: location}

			case 'I': // localizer/glide-slope record
				continuation := line[21]
				if continuation != '0' && continuation != '1' {
					break
				}
				id := strings.TrimSpace(string(line[13:17]))
				if id == "" || cifpFieldEmpty(line[32:51]) {
					break
				}
				// Match VICE: a localizer must not overwrite a same-ID VOR/DME.
				if _, exists := result.Navaids[id]; exists {
					break
				}
				location, parseErr := parseCIFPLatLong(line[32:41], line[41:51])
				if parseErr != nil {
					return ARINC424Result{}, fmt.Errorf("CIFP line %d localizer %s: %w", lineNumber, id, parseErr)
				}
				result.Navaids[id] = Navaid{ID: id, Type: "LOC", Location: location}
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ARINC424Result{}, fmt.Errorf("read CIFP line %d: %w", lineNumber, err)
		}
	}

	return result, nil
}

func bytesWithoutLineEnding(line []byte) []byte {
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	return line
}

var (
	cifpDatabaseOnce sync.Once
	cifpDatabase     StaticDatabase
	cifpDatabaseErr  error
)

// LoadCIFPDatabase lazily loads the current FAA CIFP navigation database.
// REDS supports VICE's directly-compressed FAACIFP18.zst resource as well as
// the official FAA cifp_*.tar.zst download. When several cycle archives are
// present, the lexicographically newest cycle filename is selected.
func LoadCIFPDatabase() (StaticDatabase, error) {
	cifpDatabaseOnce.Do(func() {
		result, err := loadCIFPResult()
		if err != nil {
			cifpDatabaseErr = err
			return
		}
		cifpDatabase = StaticDatabase{Navaids: result.Navaids, Fixes: result.Fixes}
	})
	return cifpDatabase, cifpDatabaseErr
}

func loadCIFPResult() (ARINC424Result, error) {
	for _, resource := range []string{cifpZstdResource, cifpRawResource} {
		if !util.ResourceExists(resource) {
			continue
		}
		r := util.LoadResource(resource)
		defer r.Close()
		return ParseARINC424(r)
	}

	archiveResource, ok, err := newestCIFPArchiveResource()
	if err != nil {
		return ARINC424Result{}, err
	}
	if !ok {
		return ARINC424Result{}, fmt.Errorf("FAA CIFP resource not found under resources/nav")
	}

	r := util.LoadResource(archiveResource) // transparently decompress .zst
	defer r.Close()
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ARINC424Result{}, fmt.Errorf("read %s: %w", archiveResource, err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		if filepath.Base(header.Name) != cifpRawFilename {
			continue
		}
		return ParseARINC424(tr)
	}

	return ARINC424Result{}, fmt.Errorf("%s does not contain %s", archiveResource, cifpRawFilename)
}

func newestCIFPArchiveResource() (string, bool, error) {
	dir := util.FindProjectRelativeDir("resources/nav")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, fmt.Errorf("read CIFP resource directory %s: %w", dir, err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "cifp_") && strings.HasSuffix(lower, ".tar.zst") {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", false, nil
	}
	sort.Strings(names)
	return filepath.ToSlash(filepath.Join("resources", "nav", names[len(names)-1])), true, nil
}

// This resource is intentionally the cycle supplied with REDS. Unlike the
// navigation-fix loader, approach matching must never silently select a
// different cycle whose geometry might not match the reviewed procedures.
const cifpApproachResource = "resources/nav/cifp_261001.tar.zst"

// CIFPAltitudeConstraint retains the altitude semantics of an ARINC 424 PF
// waypoint record. A missing bound is distinct from an altitude of zero.
// GlidepathAltitude is the second altitude on G/H/I/J/V/X descriptors; it is
// a reference for vertical interpolation, NOT an additional hard restriction.
type CIFPAltitudeConstraint struct {
	MinFeet, MaxFeet  int
	HasMin, HasMax    bool
	GlidepathAltitude int
}

// CIFPFinalLeg describes the path from Points[i] to Points[i+1]. RF is a
// constant-radius arc about Center; TF/CF are straight legs. The turn
// direction is the actual ARINC RF turn, not the shortest chord direction.
type CIFPFinalLeg struct {
	Type      string
	Center    Point
	RadiusNM  float64
	Clockwise bool
}

// CIFPFinalApproach contains only supported legs between FAF and runway.
// Unknown geometry or altitude semantics causes the *whole* approach to be
// excluded from MSAW suppression. Missed-approach legs are not included.
type CIFPFinalApproach struct {
	Airport        string
	ID             string
	Runway         string
	Points         []Point                  // FAF, intermediate fixes, runway threshold
	Legs           []CIFPFinalLeg           // one per pair of Points
	Constraints    []CIFPAltitudeConstraint // one per Point
	FAFAltitude    int                      // feet MSL; no positive value => no eligibility
	RunwayAltitude int                      // feet MSL
}

type cifpApproachRecord struct {
	fix, path string
	faf       bool
	altitude  int
	altitude2 int
	altDesc   byte
	center    string
	radius    float64
	turn      byte
}

// The altitude mapping follows ARINC 424 field 5.29 as interpreted in
// VICE's ssaRecord.GetWaypoint: blank/G/I/X = at, '+' / H/J/V = at-or-above,
// '-' = at-or-below, and B = between (alt1 low, alt0 high).
// Glideslope/vertical-angle alt1 is separately retained for G/H/I/J/V/X.
// Unknown descriptors cannot justify inhibiting a safety warning.
func cifpApproachAltitude(r cifpApproachRecord) (CIFPAltitudeConstraint, bool) {
	var c CIFPAltitudeConstraint
	if r.altitude == 0 && r.altitude2 == 0 {
		return c, true
	}
	if r.altitude <= 0 {
		return c, false
	}
	switch r.altDesc {
	case ' ', 'G', 'I', 'X':
		c.HasMin, c.HasMax = true, true
		c.MinFeet, c.MaxFeet = r.altitude, r.altitude
	case '+', 'H', 'J', 'V':
		c.HasMin, c.MinFeet = true, r.altitude
	case '-':
		c.HasMax, c.MaxFeet = true, r.altitude
	case 'B':
		if r.altitude2 <= 0 || r.altitude2 > r.altitude {
			return c, false
		}
		c.HasMin, c.HasMax = true, true
		c.MinFeet, c.MaxFeet = r.altitude2, r.altitude
	default:
		return c, false
	}
	switch r.altDesc {
	case 'G', 'H', 'I', 'J', 'V', 'X':
		if r.altitude2 > 0 {
			c.GlidepathAltitude = r.altitude2
		}
	}
	return c, true
}

func parseCIFPApproachFeet(raw []byte) (int, bool) {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return 0, true
	}
	if strings.HasPrefix(s, "FL") {
		fl, err := strconv.Atoi(s[2:])
		return fl * 100, err == nil && fl > 0
	}
	feet, err := strconv.Atoi(s)
	return feet, err == nil && feet >= 0
}

// Reject malformed arcs instead of drawing a chord or trusting the radius
// blindly. Coordinates and FAA radius differ by <0.02 NM for the tested CIFP
// RF finals; 0.10 NM allows normal chart/coordinate rounding.
func cifpRFGeometryValid(a, b Point, leg CIFPFinalLeg) bool {
	if leg.RadiusNM < 0.1 || leg.RadiusNM > 100 {
		return false
	}
	latCos := math.Cos(leg.Center.Lat * math.Pi / 180)
	if math.Abs(latCos) < 0.01 {
		return false
	}
	for _, p := range [...]Point{a, b} {
		d := math.Hypot((p.Lon-leg.Center.Lon)*60*latCos, (p.Lat-leg.Center.Lat)*60)
		if math.Abs(d-leg.RadiusNM) > 0.10 {
			return false
		}
	}
	from := math.Atan2((a.Lat-leg.Center.Lat)*60, (a.Lon-leg.Center.Lon)*60*latCos)
	to := math.Atan2((b.Lat-leg.Center.Lat)*60, (b.Lon-leg.Center.Lon)*60*latCos)
	span := math.Mod(to-from+2*math.Pi, 2*math.Pi)
	if leg.Clockwise {
		span = math.Mod(from-to+2*math.Pi, 2*math.Pi)
	}
	// Ambiguous full turns and >180-degree legs are not treated as finals.
	return span > 0.01 && span <= math.Pi
}

// ParseCIFPFinalApproaches reads ARINC 424 directly with bounded scratch
// space per record. It retains only the final-approach legs and the terminal
// fix/runway positions needed to resolve them. Only the main (empty-transition)
// coded route is used; approach transitions and missed approaches are excluded.
func ParseCIFPFinalApproaches(r io.Reader) (map[string][]CIFPFinalApproach, error) {
	type key struct{ airport, id string }
	fixes := make(map[string]Point)
	runways := make(map[string]struct {
		position  Point
		elevation int
	})
	routes := make(map[key][]cifpApproachRecord)
	br := bufio.NewReaderSize(r, 64*1024)
	lineNo := 0
	for {
		line, readErr := br.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		lineNo++
		line = bytesWithoutLineEnding(line)
		if len(line) != arinc424RecordLength {
			return nil, fmt.Errorf("CIFP line %d: length %d, expected %d", lineNo, len(line), arinc424RecordLength)
		}
		if line[0] != 'S' || line[4] != 'P' || line[5] != ' ' {
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return nil, readErr
			}
			continue
		}
		airport := strings.TrimSpace(string(line[6:10]))
		switch line[12] {
		case 'C': // Airport-terminal waypoint.
			if line[21] != '0' && line[21] != '1' {
				break
			}
			id := strings.TrimSpace(string(line[13:18]))
			if id == "" || cifpFieldEmpty(line[32:51]) {
				break
			}
			at, err := parseCIFPLatLong(line[32:41], line[41:51])
			if err != nil {
				return nil, fmt.Errorf("CIFP line %d terminal fix: %w", lineNo, err)
			}
			fixes[airport+"/"+id] = at
		case 'G': // Runway threshold and elevation.
			if line[21] != '0' && line[21] != '1' {
				break
			}
			id := strings.TrimSpace(string(line[13:18]))
			if !strings.HasPrefix(id, "RW") || cifpFieldEmpty(line[32:51]) {
				break
			}
			at, err := parseCIFPLatLong(line[32:41], line[41:51])
			if err != nil {
				return nil, fmt.Errorf("CIFP line %d runway: %w", lineNo, err)
			}
			elevation, err := strconv.Atoi(strings.TrimSpace(string(line[66:71])))
			if err != nil {
				break
			}
			runways[airport+"/"+id] = struct {
				position  Point
				elevation int
			}{at, elevation}
		case 'F': // Instrument approach legs, section 4.1.9.
			if (line[38] != '0' && line[38] != '1') || strings.TrimSpace(string(line[20:25])) != "" {
				break
			}
			id := strings.TrimSpace(string(line[13:19]))
			if id == "" {
				break
			}
			alt, ok0 := parseCIFPApproachFeet(line[84:89])
			alt2, ok1 := parseCIFPApproachFeet(line[89:94])
			if !ok0 || !ok1 { // Malformed altitude cannot inhibit LA.
				alt, alt2 = -1, -1
			}
			arcRadius := 0.0
			if line[47] == 'R' && line[48] == 'F' {
				v, err := strconv.Atoi(strings.TrimSpace(string(line[56:62])))
				if err == nil {
					arcRadius = float64(v) / 1000
				}
			}
			k := key{airport, id}
			routes[k] = append(routes[k], cifpApproachRecord{
				fix:       strings.TrimSpace(string(line[29:34])),
				path:      string(line[47:49]),
				faf:       line[42] == 'F',
				altitude:  alt,
				altitude2: alt2,
				altDesc:   line[82],
				center:    strings.TrimSpace(string(line[106:111])),
				radius:    arcRadius,
				turn:      line[43],
			})
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, readErr
		}
	}

	result := make(map[string][]CIFPFinalApproach)
	for k, records := range routes {
		fafIndex := -1
		for i, record := range records {
			if record.faf && record.altitude > 0 {
				fafIndex = i
				break
			}
		}
		if fafIndex < 0 {
			continue
		}
		faf := records[fafIndex]
		at, ok := fixes[k.airport+"/"+faf.fix]
		if !ok {
			continue
		}
		fafConstraint, valid := cifpApproachAltitude(faf)
		if !valid {
			continue
		}
		approach := CIFPFinalApproach{
			Airport: k.airport, ID: k.id, FAFAltitude: faf.altitude,
			Points: []Point{at}, Constraints: []CIFPAltitudeConstraint{fafConstraint},
		}
		for i := fafIndex + 1; i < len(records); i++ {
			rec := records[i]
			if rec.path != "CF" && rec.path != "TF" && rec.path != "RF" {
				break // Never skip an unsupported leg to reach the runway.
			}
			c, ok := cifpApproachAltitude(rec)
			if !ok {
				break
			}
			var next Point
			isRunway := strings.HasPrefix(rec.fix, "RW")
			if isRunway {
				rwy, exists := runways[k.airport+"/"+rec.fix]
				if !exists {
					break
				}
				next = rwy.position
				approach.Runway = strings.TrimPrefix(rec.fix, "RW")
				approach.RunwayAltitude = rwy.elevation
			} else {
				point, exists := fixes[k.airport+"/"+rec.fix]
				if !exists {
					break
				}
				next = point
			}
			leg := CIFPFinalLeg{Type: rec.path}
			if rec.path == "RF" {
				center, exists := fixes[k.airport+"/"+rec.center]
				if !exists || (rec.turn != 'L' && rec.turn != 'R') {
					break
				}
				leg.Center, leg.RadiusNM, leg.Clockwise = center, rec.radius, rec.turn == 'R'
				if !cifpRFGeometryValid(approach.Points[len(approach.Points)-1], next, leg) {
					break
				}
			} else if approachDistanceNM(approach.Points[len(approach.Points)-1], next) < 0.05 {
				break
			}
			approach.Points = append(approach.Points, next)
			approach.Legs = append(approach.Legs, leg)
			approach.Constraints = append(approach.Constraints, c)
			if isRunway {
				break
			}
		}
		if approach.Runway == "" || len(approach.Points) < 2 ||
			approach.FAFAltitude <= approach.RunwayAltitude+200 ||
			len(approach.Legs) != len(approach.Points)-1 ||
			len(approach.Constraints) != len(approach.Points) {
			continue
		}
		result[k.airport] = append(result[k.airport], approach)
	}
	// A fixed order makes selection among equivalent ILS/LOC/RNAV finals
	// deterministic across process launches (Go map iteration is randomized).
	for airport := range result {
		sort.Slice(result[airport], func(i, j int) bool {
			a, b := result[airport][i], result[airport][j]
			if a.Runway != b.Runway {
				return a.Runway < b.Runway
			}
			return a.ID < b.ID
		})
	}
	return result, nil
}

func approachDistanceNM(a, b Point) float64 {
	const rad, earthRadiusNM = math.Pi / 180, 3440.065
	dLat := (b.Lat - a.Lat) * rad
	dLon := (b.Lon - a.Lon) * rad
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Pow(math.Sin(dLon/2), 2)
	return 2 * earthRadiusNM * math.Asin(math.Sqrt(math.Min(1, h)))
}

// LoadCIFPFinalApproaches reads the exact archived cycle in resources/nav.
// Missing resources return an error; callers must leave MSAW enabled.
func LoadCIFPFinalApproaches() (map[string][]CIFPFinalApproach, error) {
	if !util.ResourceExists(cifpApproachResource) {
		return nil, fmt.Errorf("missing CIFP approach archive %s", cifpApproachResource)
	}
	r := util.LoadResource(cifpApproachResource)
	defer r.Close()
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read CIFP archive: %w", err)
		}
		if (header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA) && strings.HasSuffix(header.Name, "/FAACIFP18") {
			return ParseCIFPFinalApproaches(tr)
		}
	}
	return nil, errors.New("CIFP archive does not contain FAACIFP18")
}
