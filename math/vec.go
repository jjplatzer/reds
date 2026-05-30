package math

// Vec2 is a 2D vector in logical display coordinates or world coordinates,
// depending on the subsystem using it.
type Vec2 struct {
	X, Y float32
}

func (v Vec2) Add(o Vec2) Vec2    { return Vec2{X: v.X + o.X, Y: v.Y + o.Y} }
func (v Vec2) Sub(o Vec2) Vec2    { return Vec2{X: v.X - o.X, Y: v.Y - o.Y} }
func (v Vec2) Mul(s float32) Vec2 { return Vec2{X: v.X * s, Y: v.Y * s} }
func (v Vec2) Div(s float32) Vec2 {
	if s == 0 {
		return Vec2{}
	}
	return Vec2{X: v.X / s, Y: v.Y / s}
}
