package math

import stdmath "math"

// DistanceToPolygonEdges returns the minimum Euclidean distance between p and
// the boundary segments of polygon, in the same units as the coordinates.
// Unlike a distance-to-filled-polygon function, this can be positive inside
// the polygon. An empty or degenerate polygon returns +Inf.
func DistanceToPolygonEdges(p Vec2, polygon []Vec2) float64 {
	best := stdmath.Inf(1)
	for i, a := range polygon {
		b := polygon[(i+1)%len(polygon)]
		dx, dy := float64(b.X-a.X), float64(b.Y-a.Y)
		lengthSquared := dx*dx + dy*dy
		if lengthSquared == 0 {
			continue
		}
		px, py := float64(p.X-a.X), float64(p.Y-a.Y)
		u := stdmath.Max(0, stdmath.Min(1, (px*dx+py*dy)/lengthSquared))
		distance := stdmath.Hypot(px-u*dx, py-u*dy)
		if distance < best {
			best = distance
		}
	}
	return best
}
