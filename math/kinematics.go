package math

import stdmath "math"

// ProjectConstantAcceleration estimates a 2D position after dt seconds.
// Direction must be a unit vector. Speed and acceleration use distance units
// per second and distance units per second squared, respectively. Acceleration
// is applied for at most accelerationDuration seconds; velocity then remains
// constant. This function has no runway or aircraft-specific assumptions.
func ProjectConstantAcceleration(position, direction Vec2, speed, acceleration, accelerationDuration, dt float64) Vec2 {
	acceleratedFor := stdmath.Min(dt, accelerationDuration)
	distance := speed*dt + acceleration*(acceleratedFor*dt-0.5*acceleratedFor*acceleratedFor)
	return position.Add(direction.Mul(float32(distance)))
}

// ClosestApproachOnLinearSegments returns the minimum separation and the
// normalized time u in [0,1] for two points moving linearly from a0 to a1
// and b0 to b1 over the SAME time interval. The computation is performed in
// float64 to avoid needless cancellation with large map coordinates.
func ClosestApproachOnLinearSegments(a0, a1, b0, b1 Vec2) (distance, u float64) {
	rx, ry := float64(a0.X)-float64(b0.X), float64(a0.Y)-float64(b0.Y)
	vx := (float64(a1.X) - float64(a0.X)) - (float64(b1.X) - float64(b0.X))
	vy := (float64(a1.Y) - float64(a0.Y)) - (float64(b1.Y) - float64(b0.Y))
	denom := vx*vx + vy*vy
	if denom > 0 {
		u = stdmath.Max(0, stdmath.Min(1, -(rx*vx+ry*vy)/denom))
	}
	return stdmath.Hypot(rx+u*vx, ry+u*vy), u
}
