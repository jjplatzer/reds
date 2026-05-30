package math

// Rect is an axis-aligned rectangle. Min is inclusive and Max is exclusive in
// the usual screen-space sense.
type Rect struct {
	Min, Max Vec2
}

func NewRect(x0, y0, x1, y1 float32) Rect {
	return Rect{Min: Vec2{X: x0, Y: y0}, Max: Vec2{X: x1, Y: y1}}
}

func RectFromSize(w, h float32) Rect {
	return NewRect(0, 0, w, h)
}

func (r Rect) Width() float32  { return r.Max.X - r.Min.X }
func (r Rect) Height() float32 { return r.Max.Y - r.Min.Y }
func (r Rect) Size() Vec2      { return Vec2{X: r.Width(), Y: r.Height()} }

func (r Rect) Empty() bool { return r.Width() <= 0 || r.Height() <= 0 }

func (r Rect) Contains(p Vec2) bool {
	return p.X >= r.Min.X && p.X < r.Max.X && p.Y >= r.Min.Y && p.Y < r.Max.Y
}

func (r Rect) Translate(v Vec2) Rect {
	return Rect{Min: r.Min.Add(v), Max: r.Max.Add(v)}
}
