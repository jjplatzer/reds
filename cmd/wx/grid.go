package wx

import (
	"math"
	"time"
)

type Level uint8

const (
	LevelNone     Level = iota
	LevelModerate       // ERAM level 1
	LevelHeavy          // ERAM level 2
	LevelExtreme        // ERAM level 3

	LevelMissing Level = 255
)

type Bounds struct {
	North float64
	South float64
	West  float64
	East  float64
}

type Grid struct {
	ObservedAt time.Time
	Bounds     Bounds

	NX int
	NY int

	// Positive values. Rows are always normalized north-to-south.
	DLat float64
	DLon float64

	// One byte per retained MRMS cell.
	Levels []Level
}

type GridMetadata struct {
	NX int
	NY int

	// Bounds are outer cell-edge bounds, not cell-center bounds.
	Bounds Bounds

	// Positive values.
	DLat float64
	DLon float64
}

type Crop struct {
	X0 int
	X1 int
	Y0 int
	Y1 int
}

func (c Crop) Empty() bool {
	return c.X0 >= c.X1 || c.Y0 >= c.Y1
}

func (c Crop) NX() int {
	if c.Empty() {
		return 0
	}
	return c.X1 - c.X0
}

func (c Crop) NY() int {
	if c.Empty() {
		return 0
	}
	return c.Y1 - c.Y0
}

type GridRect struct {
	X0 int
	X1 int
	Y0 int
	Y1 int

	North float64
	South float64
	West  float64
	East  float64
}

func LevelForDBZ(dbz float32, missing bool) Level {
	switch {
	case missing:
		return LevelMissing
	case dbz > 45:
		return LevelExtreme
	case dbz > 35:
		return LevelHeavy
	case dbz > 25:
		return LevelModerate
	default:
		return LevelNone
	}
}

func BoundsAround(lat, lon, radiusNM float64) Bounds {
	if radiusNM < 0 {
		radiusNM = 0
	}

	latDelta := radiusNM / 60
	cosLat := math.Cos(lat * math.Pi / 180)
	if math.Abs(cosLat) < 0.01 {
		cosLat = math.Copysign(0.01, cosLat)
	}
	lonDelta := radiusNM / (60 * math.Abs(cosLat))

	return Bounds{
		North: clampFloat64(lat+latDelta, -90, 90),
		South: clampFloat64(lat-latDelta, -90, 90),
		West:  normalizeLon(lon - lonDelta),
		East:  normalizeLon(lon + lonDelta),
	}
}

func CropIndices(metadata GridMetadata, bounds Bounds) Crop {
	if metadata.NX <= 0 || metadata.NY <= 0 || metadata.DLat <= 0 || metadata.DLon <= 0 {
		return Crop{}
	}

	west := bounds.West
	east := bounds.East
	if east < west {
		east += 360
	}

	metaWest := metadata.Bounds.West
	metaEast := metadata.Bounds.East
	if metaEast < metaWest {
		metaEast += 360
	}
	for west < metaWest {
		west += 360
		east += 360
	}
	for west >= metaEast {
		west -= 360
		east -= 360
	}

	x0 := int(math.Floor((west - metaWest) / metadata.DLon))
	x1 := int(math.Ceil((east - metaWest) / metadata.DLon))
	y0 := int(math.Floor((metadata.Bounds.North - bounds.North) / metadata.DLat))
	y1 := int(math.Ceil((metadata.Bounds.North - bounds.South) / metadata.DLat))

	return Crop{
		X0: clampInt(x0, 0, metadata.NX),
		X1: clampInt(x1, 0, metadata.NX),
		Y0: clampInt(y0, 0, metadata.NY),
		Y1: clampInt(y1, 0, metadata.NY),
	}
}

func MergeLevelRectangles(grid *Grid, level Level) []GridRect {
	if grid == nil || grid.NX <= 0 || grid.NY <= 0 || len(grid.Levels) < grid.NX*grid.NY {
		return nil
	}
	if level == LevelNone || level == LevelMissing {
		return nil
	}

	type runKey struct {
		x0 int
		x1 int
	}

	active := make(map[runKey]GridRect)
	var out []GridRect

	emitMissing := func(next map[runKey]GridRect) {
		for key, rect := range active {
			if _, ok := next[key]; !ok {
				out = append(out, gridRectWithBounds(grid, rect))
			}
		}
	}

	for y := 0; y < grid.NY; y++ {
		next := make(map[runKey]GridRect, len(active))
		row := grid.Levels[y*grid.NX : (y+1)*grid.NX]

		for x := 0; x < grid.NX; {
			if row[x] != level {
				x++
				continue
			}

			x0 := x
			for x < grid.NX && row[x] == level {
				x++
			}
			key := runKey{x0: x0, x1: x}

			if rect, ok := active[key]; ok {
				rect.Y1 = y + 1
				next[key] = rect
			} else {
				next[key] = GridRect{
					X0: x0,
					X1: x,
					Y0: y,
					Y1: y + 1,
				}
			}
		}

		emitMissing(next)
		active = next
	}

	for _, rect := range active {
		out = append(out, gridRectWithBounds(grid, rect))
	}

	return out
}

func gridRectWithBounds(grid *Grid, rect GridRect) GridRect {
	rect.West = grid.Bounds.West + float64(rect.X0)*grid.DLon
	rect.East = grid.Bounds.West + float64(rect.X1)*grid.DLon
	rect.North = grid.Bounds.North - float64(rect.Y0)*grid.DLat
	rect.South = grid.Bounds.North - float64(rect.Y1)*grid.DLat
	rect.West = normalizeLon(rect.West)
	rect.East = normalizeLon(rect.East)
	return rect
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

func clampFloat64(value, minValue, maxValue float64) float64 {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func normalizeLon(lon float64) float64 {
	for lon > 180 {
		lon -= 360
	}
	for lon <= -180 {
		lon += 360
	}
	return lon
}
