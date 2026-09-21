package radar

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// WMMGridSpec describes the regular NOAA WMM grid used by cmd/wmm2reds.
// The generation-time grid was sampled at sea level for the 2024 epoch and
// stores east-positive geomagnetic declination samples. It is not loaded by
// the REDS runtime.
type WMMGridSpec struct {
	MinLatitude, MaxLatitude   float64
	MinLongitude, MaxLongitude float64
	Step                       float64
	Epoch                      int
	AltitudeMeters             float64
	Source                     string
}

var DefaultWMMGridSpec = WMMGridSpec{
	MinLatitude:  17,
	MaxLatitude:  75,
	MinLongitude: -180,
	MaxLongitude: 150,
	Step:         0.25,
	Epoch:        2024,
	Source:       "NOAA World Magnetic Model; 0 m; 2024 epoch; 0.25-degree sampled declination grid",
}

// WMMGrid is a regular declination grid. Samples are stored east-positive,
// matching NOAA/WMM output; VariationAt returns the FAA/REDS convention
// (positive west).
type WMMGrid struct {
	spec    WMMGridSpec
	nLat    int
	nLon    int
	samples []float64
}

// ParseWMMGrid parses one declination sample per line in row-major order:
// latitude first (south to north), then longitude (west to east).
func ParseWMMGrid(r io.Reader, spec WMMGridSpec) (*WMMGrid, error) {
	if r == nil {
		return nil, fmt.Errorf("nil WMM grid reader")
	}
	if spec.Step <= 0 || spec.MaxLatitude <= spec.MinLatitude || spec.MaxLongitude <= spec.MinLongitude {
		return nil, fmt.Errorf("invalid WMM grid bounds/step")
	}

	nLatF := (spec.MaxLatitude-spec.MinLatitude)/spec.Step + 1
	nLonF := (spec.MaxLongitude-spec.MinLongitude)/spec.Step + 1
	nLat := int(math.Round(nLatF))
	nLon := int(math.Round(nLonF))
	if nLat < 2 || nLon < 2 || math.Abs(float64(nLat)-nLatF) > 1e-9 || math.Abs(float64(nLon)-nLonF) > 1e-9 {
		return nil, fmt.Errorf("WMM grid bounds are not an integer number of %.9g-degree steps", spec.Step)
	}

	grid := &WMMGrid{
		spec:    spec,
		nLat:    nLat,
		nLon:    nLon,
		samples: make([]float64, 0, nLat*nLon),
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		v, err := strconv.ParseFloat(line, 64)
		if err != nil {
			return nil, fmt.Errorf("parse WMM sample %q: %w", line, err)
		}
		grid.samples = append(grid.samples, v)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read WMM grid: %w", err)
	}

	if got, want := len(grid.samples), nLat*nLon; got != want {
		return nil, fmt.Errorf("WMM grid has %d samples; expected %d", got, want)
	}
	return grid, nil
}

func (g *WMMGrid) Spec() WMMGridSpec {
	if g == nil {
		return WMMGridSpec{}
	}
	return g.spec
}

func (g *WMMGrid) Bounds() MagneticVariationBounds {
	if g == nil {
		return MagneticVariationBounds{}
	}
	return MagneticVariationBounds{
		South: g.spec.MinLatitude,
		North: g.spec.MaxLatitude,
		West:  g.spec.MinLongitude,
		East:  g.spec.MaxLongitude,
	}
}

func (g *WMMGrid) VariationAt(lat, lon float64) (float64, error) {
	if g == nil {
		return 0, fmt.Errorf("nil WMM grid")
	}
	if lat < g.spec.MinLatitude || lat > g.spec.MaxLatitude || lon < g.spec.MinLongitude || lon > g.spec.MaxLongitude {
		return 0, fmt.Errorf("WMM lookup point %.6f, %.6f outside sampled grid", lat, lon)
	}

	y := (lat - g.spec.MinLatitude) / g.spec.Step
	x := (lon - g.spec.MinLongitude) / g.spec.Step
	i0 := min(int(math.Floor(y)), g.nLat-1)
	j0 := min(int(math.Floor(x)), g.nLon-1)
	i1 := min(i0+1, g.nLat-1)
	j1 := min(j0+1, g.nLon-1)
	fy := y - float64(i0)
	fx := x - float64(j0)

	v00 := g.variationAtIndex(i0, j0)
	v01 := g.variationAtIndex(i0, j1)
	v10 := g.variationAtIndex(i1, j0)
	v11 := g.variationAtIndex(i1, j1)

	south := v00 + fx*(v01-v00)
	north := v10 + fx*(v11-v10)
	return south + fy*(north-south), nil
}

func (g *WMMGrid) variationAtIndex(latIndex, lonIndex int) float64 {
	// NOAA/WMM declination is east-positive; FAA/REDS magnetic variation is
	// west-positive.
	return -g.samples[lonIndex+g.nLon*latIndex]
}
