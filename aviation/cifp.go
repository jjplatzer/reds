package aviation

import (
	"archive/tar"
	"bufio"
	"errors"
	"fmt"
	"io"
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
