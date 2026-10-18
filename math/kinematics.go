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
