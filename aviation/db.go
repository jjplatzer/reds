package aviation

import "strings"

// Point is a geographic position in decimal degrees.
type Point struct {
	Lat float64
	Lon float64
}

// Navaid is the subset of an FAA CIFP navaid record that REDS currently needs.
// The additional DME/declination fields mirror VICE's database representation so
// the parser can be reused by later STARS navigation functions without rereading
// the 50+ MB CIFP file.
type Navaid struct {
	ID       string
	Type     string
	Name     string
	Location Point

	Declination    float64
	HasDeclination bool

	HasDME          bool
	DMELocation     Point
	DMEElevation    int
	HasDMEElevation bool
}

// Fix is an en-route or terminal waypoint from the FAA CIFP.
type Fix struct {
	ID       string
	Location Point
}

// StaticDatabase is the portion of VICE's aviation database used by the STARS
// *T range-bearing-line command. LookupWaypoint deliberately checks navaids
// before fixes, matching VICE.
type StaticDatabase struct {
	Navaids map[string]Navaid
	Fixes   map[string]Fix
}

func (d StaticDatabase) LookupWaypoint(id string) (Point, bool) {
	id = strings.ToUpper(strings.TrimSpace(id))
	if n, ok := d.Navaids[id]; ok {
		return n.Location, true
	}
	if f, ok := d.Fixes[id]; ok {
		return f.Location, true
	}
	return Point{}, false
}
