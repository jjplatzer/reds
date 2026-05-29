package renderer

import (
	"math"
	"sync"
)

// LinesBuilder accumulates position-only line geometry that shares the current
// CmdBuffer color and line width.
type LinesBuilder struct {
	points  []PointVertex
	indices []uint32
}

func (b *LinesBuilder) Reset() {
	b.points = b.points[:0]
	b.indices = b.indices[:0]
}

func (b *LinesBuilder) AddLine(a, c PointVertex) {
	i := uint32(len(b.points))
	b.points = append(b.points, a, c)
	b.indices = append(b.indices, i, i+1)
}

func (b *LinesBuilder) AddLineStrip(points []PointVertex) {
	if len(points) < 2 {
		return
	}
	i := uint32(len(b.points))
	b.points = append(b.points, points...)
	for n := 0; n < len(points)-1; n++ {
		b.indices = append(b.indices, i+uint32(n), i+uint32(n+1))
	}
}

func (b *LinesBuilder) AddLineLoop(points []PointVertex) {
	if len(points) < 2 {
		return
	}
	i := uint32(len(b.points))
	b.points = append(b.points, points...)
	for n := range points {
		b.indices = append(b.indices, i+uint32(n), i+uint32((n+1)%len(points)))
	}
}

func (b *LinesBuilder) AddCircle(center PointVertex, radius float32, segments int) {
	if radius <= 0 || segments < 3 {
		return
	}
	i := uint32(len(b.points))
	for n := 0; n < segments; n++ {
		a := float32(n) / float32(segments) * 2 * math.Pi
		b.points = append(b.points, PointVertex{X: center.X + radius*float32(math.Cos(float64(a))), Y: center.Y + radius*float32(math.Sin(float64(a)))})
	}
	for n := 0; n < segments; n++ {
		b.indices = append(b.indices, i+uint32(n), i+uint32((n+1)%segments))
	}
}

func (b *LinesBuilder) GenerateCommands(cb *CmdBuffer) {
	cb.DrawLines(b.points, b.indices)
}

// ColoredLinesBuilder accumulates line geometry with per-vertex color.
type ColoredLinesBuilder struct {
	points  []ColoredVertex
	indices []uint32
}

func (b *ColoredLinesBuilder) Reset() {
	b.points = b.points[:0]
	b.indices = b.indices[:0]
}

func (b *ColoredLinesBuilder) AddLine(a PointVertex, colorA RGB, c PointVertex, colorC RGB) {
	i := uint32(len(b.points))
	b.points = append(b.points,
		ColoredVertex{X: a.X, Y: a.Y, Color: colorA},
		ColoredVertex{X: c.X, Y: c.Y, Color: colorC},
	)
	b.indices = append(b.indices, i, i+1)
}

func (b *ColoredLinesBuilder) AddLineRGB(a, c PointVertex, color RGB) {
	b.AddLine(a, color, c, color)
}

func (b *ColoredLinesBuilder) GenerateCommands(cb *CmdBuffer) {
	cb.DrawColoredLines(b.points, b.indices)
}

// TrianglesBuilder accumulates position-only filled triangle geometry.
type TrianglesBuilder struct {
	points  []PointVertex
	indices []uint32
}

func (b *TrianglesBuilder) Reset() {
	b.points = b.points[:0]
	b.indices = b.indices[:0]
}

func (b *TrianglesBuilder) AddTriangle(a, c, d PointVertex) {
	i := uint32(len(b.points))
	b.points = append(b.points, a, c, d)
	b.indices = append(b.indices, i, i+1, i+2)
}

func (b *TrianglesBuilder) AddQuad(a, c, d, e PointVertex) {
	i := uint32(len(b.points))
	b.points = append(b.points, a, c, d, e)
	b.indices = append(b.indices, i, i+1, i+2, i, i+2, i+3)
}

func (b *TrianglesBuilder) AddCircle(center PointVertex, radius float32, segments int) {
	if radius <= 0 || segments < 3 {
		return
	}
	centerIndex := uint32(len(b.points))
	b.points = append(b.points, center)
	first := uint32(len(b.points))
	for n := 0; n < segments; n++ {
		a := float32(n) / float32(segments) * 2 * math.Pi
		b.points = append(b.points, PointVertex{X: center.X + radius*float32(math.Cos(float64(a))), Y: center.Y + radius*float32(math.Sin(float64(a)))})
	}
	for n := 0; n < segments; n++ {
		b.indices = append(b.indices, centerIndex, first+uint32(n), first+uint32((n+1)%segments))
	}
}

func (b *TrianglesBuilder) AddIndexed(points []PointVertex, indices []uint32) {
	if len(points) == 0 || len(indices) == 0 {
		return
	}
	base := uint32(len(b.points))
	b.points = append(b.points, points...)
	for _, idx := range indices {
		b.indices = append(b.indices, base+idx)
	}
}

func (b *TrianglesBuilder) GenerateCommands(cb *CmdBuffer, mode DrawMode, hatchOffset float32) {
	cb.DrawTriangles(b.points, b.indices, mode, hatchOffset)
}

// ColoredTrianglesBuilder accumulates filled triangle geometry with per-vertex color.
type ColoredTrianglesBuilder struct {
	points  []ColoredVertex
	indices []uint32
}

func (b *ColoredTrianglesBuilder) Reset() {
	b.points = b.points[:0]
	b.indices = b.indices[:0]
}

func (b *ColoredTrianglesBuilder) AddTriangle(a PointVertex, colorA RGB, c PointVertex, colorC RGB, d PointVertex, colorD RGB) {
	i := uint32(len(b.points))
	b.points = append(b.points,
		ColoredVertex{X: a.X, Y: a.Y, Color: colorA},
		ColoredVertex{X: c.X, Y: c.Y, Color: colorC},
		ColoredVertex{X: d.X, Y: d.Y, Color: colorD},
	)
	b.indices = append(b.indices, i, i+1, i+2)
}

func (b *ColoredTrianglesBuilder) AddTriangleRGB(a, c, d PointVertex, color RGB) {
	b.AddTriangle(a, color, c, color, d, color)
}

func (b *ColoredTrianglesBuilder) AddQuad(a, c, d, e PointVertex, color RGB) {
	i := uint32(len(b.points))
	b.points = append(b.points,
		ColoredVertex{X: a.X, Y: a.Y, Color: color},
		ColoredVertex{X: c.X, Y: c.Y, Color: color},
		ColoredVertex{X: d.X, Y: d.Y, Color: color},
		ColoredVertex{X: e.X, Y: e.Y, Color: color},
	)
	b.indices = append(b.indices, i, i+1, i+2, i, i+2, i+3)
}

func (b *ColoredTrianglesBuilder) GenerateCommands(cb *CmdBuffer) {
	cb.DrawColoredTriangles(b.points, b.indices)
}

// TexturedTrianglesBuilder accumulates textured triangle geometry.
type TexturedTrianglesBuilder struct {
	points  []TexturedVertex
	indices []uint32
}

func (b *TexturedTrianglesBuilder) Reset() {
	b.points = b.points[:0]
	b.indices = b.indices[:0]
}

func (b *TexturedTrianglesBuilder) AddTriangle(a, uvA, c, uvC, d, uvD PointVertex) {
	i := uint32(len(b.points))
	b.points = append(b.points,
		TexturedVertex{X: a.X, Y: a.Y, U: uvA.X, V: uvA.Y},
		TexturedVertex{X: c.X, Y: c.Y, U: uvC.X, V: uvC.Y},
		TexturedVertex{X: d.X, Y: d.Y, U: uvD.X, V: uvD.Y},
	)
	b.indices = append(b.indices, i, i+1, i+2)
}

func (b *TexturedTrianglesBuilder) AddQuad(a, uvA, c, uvC, d, uvD, e, uvE PointVertex) {
	i := uint32(len(b.points))
	b.points = append(b.points,
		TexturedVertex{X: a.X, Y: a.Y, U: uvA.X, V: uvA.Y},
		TexturedVertex{X: c.X, Y: c.Y, U: uvC.X, V: uvC.Y},
		TexturedVertex{X: d.X, Y: d.Y, U: uvD.X, V: uvD.Y},
		TexturedVertex{X: e.X, Y: e.Y, U: uvE.X, V: uvE.Y},
	)
	b.indices = append(b.indices, i, i+1, i+2, i, i+2, i+3)
}

func (b *TexturedTrianglesBuilder) GenerateCommands(cb *CmdBuffer, textureID TextureID) {
	cb.DrawTexturedTriangles(textureID, b.points, b.indices)
}

var (
	linesBuilderPool             = sync.Pool{New: func() any { return &LinesBuilder{} }}
	coloredLinesBuilderPool      = sync.Pool{New: func() any { return &ColoredLinesBuilder{} }}
	trianglesBuilderPool         = sync.Pool{New: func() any { return &TrianglesBuilder{} }}
	coloredTrianglesBuilderPool  = sync.Pool{New: func() any { return &ColoredTrianglesBuilder{} }}
	texturedTrianglesBuilderPool = sync.Pool{New: func() any { return &TexturedTrianglesBuilder{} }}
)

func GetLinesBuilder() *LinesBuilder { return linesBuilderPool.Get().(*LinesBuilder) }
func ReturnLinesBuilder(b *LinesBuilder) {
	b.Reset()
	linesBuilderPool.Put(b)
}

func GetColoredLinesBuilder() *ColoredLinesBuilder {
	return coloredLinesBuilderPool.Get().(*ColoredLinesBuilder)
}
func ReturnColoredLinesBuilder(b *ColoredLinesBuilder) {
	b.Reset()
	coloredLinesBuilderPool.Put(b)
}

func GetTrianglesBuilder() *TrianglesBuilder { return trianglesBuilderPool.Get().(*TrianglesBuilder) }
func ReturnTrianglesBuilder(b *TrianglesBuilder) {
	b.Reset()
	trianglesBuilderPool.Put(b)
}

func GetColoredTrianglesBuilder() *ColoredTrianglesBuilder {
	return coloredTrianglesBuilderPool.Get().(*ColoredTrianglesBuilder)
}
func ReturnColoredTrianglesBuilder(b *ColoredTrianglesBuilder) {
	b.Reset()
	coloredTrianglesBuilderPool.Put(b)
}

func GetTexturedTrianglesBuilder() *TexturedTrianglesBuilder {
	return texturedTrianglesBuilderPool.Get().(*TexturedTrianglesBuilder)
}
func ReturnTexturedTrianglesBuilder(b *TexturedTrianglesBuilder) {
	b.Reset()
	texturedTrianglesBuilderPool.Put(b)
}
