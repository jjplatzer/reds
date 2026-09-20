package radar

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/juliusplatzer/reds/util"
)

// magneticGrid is a sampled NOAA World Magnetic Model declination grid used
// only as a fallback when STARS site adaptation does not provide an explicit
// magnetic variation. It intentionally matches VICE's current orientation
// source: WMM declination at 0 m, sampled every 0.25 degrees for year 2024.
//
// FAA JO 6191.3 describes site magnetic-variation adaptation (including a
// Magnetic Variance Tile Set). Exact adapted data should therefore take
// precedence whenever crc2reds can supply it; this grid is not a replacement
// for that adaptation.
//
// Stored samples use the geomagnetic convention (east positive). REDS exposes
// the FAA/VICE scope convention (west positive), so lookup negates each sample.
type magneticGrid struct {
	minLat, maxLat float64
	minLon, maxLon float64
	step           float64
	samples        []float64
}

var loadMagneticGrid = sync.OnceValues(func() (*magneticGrid, error) {
	const (
		minLat = 17.0
		maxLat = 75.0
		minLon = -180.0
		maxLon = 150.0
		step   = 0.25
	)

	grid := &magneticGrid{
		minLat: minLat,
		maxLat: maxLat,
		minLon: minLon,
		maxLon: maxLon,
		step:   step,
	}

	r := util.LoadResource("resources/nav/magnetic_grid.txt.zst")
	defer r.Close()
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("read magnetic variation grid: %w", err)
		}
		line = strings.TrimSpace(line)
		if line != "" {
			v, parseErr := strconv.ParseFloat(line, 64)
			if parseErr != nil {
				return nil, fmt.Errorf("parse magnetic variation grid sample %q: %w", line, parseErr)
			}
			grid.samples = append(grid.samples, v)
		}
		if err == io.EOF {
			break
		}
	}

	nLat := int(1 + (maxLat-minLat)/step)
	nLon := int(1 + (maxLon-minLon)/step)
	if got, want := len(grid.samples), nLat*nLon; got != want {
		return nil, fmt.Errorf("magnetic variation grid has %d samples; expected %d", got, want)
	}
	return grid, nil
})

// MagneticVariationAt returns magnetic variation in degrees, west positive,
// matching the sign convention used by the STARS geographic scope transform.
// Nearest-sample lookup is deliberate: it is the same behavior VICE uses for
// its 0.25-degree 2024 WMM grid.
func MagneticVariationAt(lat, lon float64) (float64, error) {
	grid, err := loadMagneticGrid()
	if err != nil {
		return 0, err
	}
	if lat < grid.minLat || lat > grid.maxLat || lon < grid.minLon || lon > grid.maxLon {
		return 0, fmt.Errorf("magnetic variation lookup point %.6f, %.6f outside sampled grid", lat, lon)
	}

	nLat := int(1 + (grid.maxLat-grid.minLat)/grid.step)
	nLon := int(1 + (grid.maxLon-grid.minLon)/grid.step)
	latIndex := min(int((lat-grid.minLat)/grid.step+0.5), nLat-1)
	lonIndex := min(int((lon-grid.minLon)/grid.step+0.5), nLon-1)

	// NOAA/WMM declination is east-positive; REDS/VICE use west-positive.
	return -grid.samples[lonIndex+nLon*latIndex], nil
}
