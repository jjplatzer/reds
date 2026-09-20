package radar

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
)

const (
	magneticVariationTileSetVersion = 1
	magneticTileLookupBucketDegrees = 2.0
	magneticTileMaxGenerationDepth  = 32
)

// MagneticVariationBounds is an axis-aligned geographic rectangle. Generated
// tile sets do not cross the antimeridian; West must therefore be < East.
type MagneticVariationBounds struct {
	South float64 `json:"south"`
	North float64 `json:"north"`
	West  float64 `json:"west"`
	East  float64 `json:"east"`
}

type MagneticVariationTile struct {
	MagneticVariationBounds
	// Variation is degrees, positive west.
	Variation float64 `json:"variation"`
}

// MagneticVariationTileSet is REDS' approximation of the STARS Magnetic
// Variance Tile Set described by FAA JO 6191.3. It is deliberately source-
// agnostic: today cmd/wmm2reds generates it from WMM; a future converter can
// replace the resource with genuine site-adaptation tiles without changing
// the STARS renderer.
type MagneticVariationTileSet struct {
	Version          int                     `json:"version"`
	Source           string                  `json:"source"`
	Epoch            int                     `json:"epoch"`
	ToleranceDegrees float64                 `json:"toleranceDegrees"`
	MaxErrorDegrees  float64                 `json:"maxErrorDegrees"`
	Bounds           MagneticVariationBounds `json:"bounds"`
	Tiles            []MagneticVariationTile `json:"tiles"`

	bucketCols int
	bucketRows int
	buckets    [][]int
}

// GenerateMagneticVariationTileSet recursively bisects the WMM domain until
// every accepted tile satisfies
//
//	max_{p in tile} |WMM(p) - WMM(tile center)| <= epsilonDegrees.
//
// The maximum is exact with respect to WMMGrid's piecewise-bilinear field:
// on each source-grid cell a bilinear function reaches its extrema at the
// cell's corners, so it is enough to check all source-grid vertices inside
// the candidate plus the intersections of its boundary with source-grid
// lines. A leaf is maximal within this deterministic bisection hierarchy:
// except for the root, its parent failed the tolerance test.
//
// epsilonDegrees is an approximation control, not an FAA-published STARS
// tolerance. The real tile geometry/value list should replace this generated
// resource if it becomes available.
func GenerateMagneticVariationTileSet(grid *WMMGrid, epsilonDegrees float64) (*MagneticVariationTileSet, error) {
	if grid == nil {
		return nil, fmt.Errorf("nil WMM grid")
	}
	if !isFinite(epsilonDegrees) || epsilonDegrees <= 0 {
		return nil, fmt.Errorf("epsilon must be finite and > 0")
	}

	set := &MagneticVariationTileSet{
		Version:          magneticVariationTileSetVersion,
		Source:           grid.spec.Source,
		Epoch:            grid.spec.Epoch,
		ToleranceDegrees: epsilonDegrees,
		Bounds:           grid.Bounds(),
	}

	var generate func(MagneticVariationBounds, int) error
	generate = func(bounds MagneticVariationBounds, depth int) error {
		variation, err := grid.VariationAt(
			(bounds.South+bounds.North)*0.5,
			(bounds.West+bounds.East)*0.5,
		)
		if err != nil {
			return err
		}

		maxError, err := maxWMMVariationError(grid, bounds, variation)
		if err != nil {
			return err
		}
		if maxError <= epsilonDegrees {
			set.Tiles = append(set.Tiles, MagneticVariationTile{
				MagneticVariationBounds: bounds,
				Variation:               variation,
			})
			set.MaxErrorDegrees = max(set.MaxErrorDegrees, maxError)
			return nil
		}
		if depth >= magneticTileMaxGenerationDepth {
			return fmt.Errorf("unable to meet %.6g-degree tolerance by generation depth %d (remaining error %.6g)",
				epsilonDegrees, magneticTileMaxGenerationDepth, maxError)
		}

		first, second, ok := splitMagneticBounds(bounds)
		if !ok {
			return fmt.Errorf("unable to split magnetic tile %+v with error %.6g", bounds, maxError)
		}
		if err := generate(first, depth+1); err != nil {
			return err
		}
		return generate(second, depth+1)
	}

	if err := generate(set.Bounds, 0); err != nil {
		return nil, err
	}

	// Stable geographic order makes generated resources reproducible and also
	// makes a raw JSON tile listing easy to inspect by eye.
	sort.Slice(set.Tiles, func(i, j int) bool {
		a, b := set.Tiles[i], set.Tiles[j]
		if a.South != b.South {
			return a.South < b.South
		}
		if a.West != b.West {
			return a.West < b.West
		}
		if a.North != b.North {
			return a.North < b.North
		}
		return a.East < b.East
	})
	if err := set.buildLookupIndex(); err != nil {
		return nil, err
	}
	return set, nil
}

func DecodeMagneticVariationTileSet(r io.Reader) (*MagneticVariationTileSet, error) {
	if r == nil {
		return nil, fmt.Errorf("nil magnetic tile reader")
	}
	var set MagneticVariationTileSet
	if err := json.NewDecoder(r).Decode(&set); err != nil {
		return nil, fmt.Errorf("decode magnetic variation tiles: %w", err)
	}
	if err := set.validate(); err != nil {
		return nil, err
	}
	if err := set.buildLookupIndex(); err != nil {
		return nil, err
	}
	return &set, nil
}

// Lookup returns the piecewise-constant adapted variation for the tile that
// contains lat/lon. Internal tile boundaries are half-open to make the result
// deterministic; the outer north/east edges remain inclusive.
func (s *MagneticVariationTileSet) Lookup(lat, lon float64) (float64, bool) {
	if s == nil || len(s.Tiles) == 0 || !s.Bounds.containsOuter(lat, lon) {
		return 0, false
	}
	if len(s.buckets) == 0 {
		if err := s.buildLookupIndex(); err != nil {
			return 0, false
		}
	}

	row := int(math.Floor((lat - s.Bounds.South) / magneticTileLookupBucketDegrees))
	col := int(math.Floor((lon - s.Bounds.West) / magneticTileLookupBucketDegrees))
	row = clampInt(row, 0, s.bucketRows-1)
	col = clampInt(col, 0, s.bucketCols-1)
	for _, tileIndex := range s.buckets[row*s.bucketCols+col] {
		tile := s.Tiles[tileIndex]
		if tile.contains(lat, lon, s.Bounds) {
			return tile.Variation, true
		}
	}
	return 0, false
}

func (s *MagneticVariationTileSet) validate() error {
	if s.Version != magneticVariationTileSetVersion {
		return fmt.Errorf("unsupported magnetic variation tile-set version %d", s.Version)
	}
	if !validMagneticBounds(s.Bounds) {
		return fmt.Errorf("invalid magnetic variation tile-set bounds %+v", s.Bounds)
	}
	if !isFinite(s.ToleranceDegrees) || s.ToleranceDegrees <= 0 {
		return fmt.Errorf("invalid magnetic variation tolerance %.6g", s.ToleranceDegrees)
	}
	if len(s.Tiles) == 0 {
		return fmt.Errorf("magnetic variation tile set contains no tiles")
	}
	for i, tile := range s.Tiles {
		if !validMagneticBounds(tile.MagneticVariationBounds) || !isFinite(tile.Variation) {
			return fmt.Errorf("invalid magnetic variation tile %d", i)
		}
		if tile.South < s.Bounds.South || tile.North > s.Bounds.North || tile.West < s.Bounds.West || tile.East > s.Bounds.East {
			return fmt.Errorf("magnetic variation tile %d is outside set bounds", i)
		}
	}
	return nil
}

func (s *MagneticVariationTileSet) buildLookupIndex() error {
	if err := s.validate(); err != nil {
		return err
	}

	s.bucketRows = max(1, int(math.Ceil((s.Bounds.North-s.Bounds.South)/magneticTileLookupBucketDegrees)))
	s.bucketCols = max(1, int(math.Ceil((s.Bounds.East-s.Bounds.West)/magneticTileLookupBucketDegrees)))
	s.buckets = make([][]int, s.bucketRows*s.bucketCols)

	for tileIndex, tile := range s.Tiles {
		row0 := clampInt(int(math.Floor((tile.South-s.Bounds.South)/magneticTileLookupBucketDegrees)), 0, s.bucketRows-1)
		row1 := clampInt(int(math.Floor((tile.North-s.Bounds.South)/magneticTileLookupBucketDegrees)), 0, s.bucketRows-1)
		col0 := clampInt(int(math.Floor((tile.West-s.Bounds.West)/magneticTileLookupBucketDegrees)), 0, s.bucketCols-1)
		col1 := clampInt(int(math.Floor((tile.East-s.Bounds.West)/magneticTileLookupBucketDegrees)), 0, s.bucketCols-1)
		for row := row0; row <= row1; row++ {
			for col := col0; col <= col1; col++ {
				s.buckets[row*s.bucketCols+col] = append(s.buckets[row*s.bucketCols+col], tileIndex)
			}
		}
	}
	return nil
}

func splitMagneticBounds(bounds MagneticVariationBounds) (MagneticVariationBounds, MagneticVariationBounds, bool) {
	centerLat := (bounds.South + bounds.North) * 0.5
	heightNM := (bounds.North - bounds.South) * 60
	widthNM := (bounds.East - bounds.West) * 60 * math.Max(math.Abs(math.Cos(centerLat*math.Pi/180)), 0.01)

	if widthNM >= heightNM {
		mid := (bounds.West + bounds.East) * 0.5
		if mid > bounds.West && mid < bounds.East {
			first, second := bounds, bounds
			first.East = mid
			second.West = mid
			return first, second, true
		}
	}

	mid := (bounds.South + bounds.North) * 0.5
	if mid > bounds.South && mid < bounds.North {
		first, second := bounds, bounds
		first.North = mid
		second.South = mid
		return first, second, true
	}

	mid = (bounds.West + bounds.East) * 0.5
	if mid > bounds.West && mid < bounds.East {
		first, second := bounds, bounds
		first.East = mid
		second.West = mid
		return first, second, true
	}
	return MagneticVariationBounds{}, MagneticVariationBounds{}, false
}

// maxWMMVariationError computes the exact maximum error against the
// piecewise-bilinear WMMGrid interpolant over bounds.
func maxWMMVariationError(grid *WMMGrid, bounds MagneticVariationBounds, variation float64) (float64, error) {
	if !validMagneticBounds(bounds) || !grid.Bounds().containsBounds(bounds) {
		return 0, fmt.Errorf("magnetic tile %+v outside WMM grid", bounds)
	}

	maxError := 0.0
	check := func(lat, lon float64) error {
		v, err := grid.VariationAt(lat, lon)
		if err != nil {
			return err
		}
		maxError = max(maxError, math.Abs(v-variation))
		return nil
	}

	// Four candidate corners.
	for _, lat := range []float64{bounds.South, bounds.North} {
		for _, lon := range []float64{bounds.West, bounds.East} {
			if err := check(lat, lon); err != nil {
				return 0, err
			}
		}
	}

	lat0, lat1 := gridIndexRange(bounds.South, bounds.North, grid.spec.MinLatitude, grid.spec.Step, grid.nLat)
	lon0, lon1 := gridIndexRange(bounds.West, bounds.East, grid.spec.MinLongitude, grid.spec.Step, grid.nLon)

	// All source-grid vertices inside the candidate.
	for i := lat0; i <= lat1; i++ {
		for j := lon0; j <= lon1; j++ {
			maxError = max(maxError, math.Abs(grid.variationAtIndex(i, j)-variation))
		}
	}

	// Candidate-boundary/source-grid intersections. Together with the source
	// vertices above and the four corners, these are exactly the vertices of
	// every bilinear patch clipped by the candidate rectangle.
	for j := lon0; j <= lon1; j++ {
		lon := grid.spec.MinLongitude + float64(j)*grid.spec.Step
		if err := check(bounds.South, lon); err != nil {
			return 0, err
		}
		if err := check(bounds.North, lon); err != nil {
			return 0, err
		}
	}
	for i := lat0; i <= lat1; i++ {
		lat := grid.spec.MinLatitude + float64(i)*grid.spec.Step
		if err := check(lat, bounds.West); err != nil {
			return 0, err
		}
		if err := check(lat, bounds.East); err != nil {
			return 0, err
		}
	}

	return maxError, nil
}

func gridIndexRange(low, high, gridMin, step float64, count int) (int, int) {
	const numericSlack = 1e-10
	first := int(math.Ceil((low-gridMin)/step - numericSlack))
	last := int(math.Floor((high-gridMin)/step + numericSlack))
	first = clampInt(first, 0, count-1)
	last = clampInt(last, 0, count-1)
	if first > last {
		return 1, 0
	}
	return first, last
}

func validMagneticBounds(bounds MagneticVariationBounds) bool {
	return isFinite(bounds.South) && isFinite(bounds.North) && isFinite(bounds.West) && isFinite(bounds.East) &&
		bounds.South < bounds.North && bounds.West < bounds.East
}

func (b MagneticVariationBounds) containsOuter(lat, lon float64) bool {
	return lat >= b.South && lat <= b.North && lon >= b.West && lon <= b.East
}

func (b MagneticVariationBounds) containsBounds(other MagneticVariationBounds) bool {
	return other.South >= b.South && other.North <= b.North && other.West >= b.West && other.East <= b.East
}

func (t MagneticVariationTile) contains(lat, lon float64, outer MagneticVariationBounds) bool {
	latInside := lat >= t.South && (lat < t.North || (t.North == outer.North && lat <= t.North))
	lonInside := lon >= t.West && (lon < t.East || (t.East == outer.East && lon <= t.East))
	return latInside && lonInside
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
