package renderer

import (
	"sort"
	"sync"
)

// DrawMode controls special fragment behavior for filled triangles.
type DrawMode int

const (
	DrawSolid DrawMode = iota
	DrawHatched
)

// Mat4 is a column-major 4x4 matrix, matching OpenGL's uniform matrix layout.
type Mat4 [16]float32

// Identity returns the identity matrix.
func Identity() Mat4 {
	return Mat4{
		1, 0, 0, 0,
		0, 1, 0, 0,
		0, 0, 1, 0,
		0, 0, 0, 1,
	}
}

// Ortho returns an orthographic projection matrix.
func Ortho(left, right, bottom, top, near, far float32) Mat4 {
	return Mat4{
		2 / (right - left), 0, 0, 0,
		0, 2 / (top - bottom), 0, 0,
		0, 0, -2 / (far - near), 0,
		-(right + left) / (right - left), -(top + bottom) / (top - bottom), -(far + near) / (far - near), 1,
	}
}

// ScreenOrtho returns a projection for logical screen coordinates with origin
// in the top-left and y increasing downward.
func ScreenOrtho(width, height float32) Mat4 {
	if width <= 0 || height <= 0 {
		return Identity()
	}
	return Ortho(0, width, height, 0, -1, 1)
}

// PointVertex is a 2D position-only vertex.
type PointVertex struct {
	X, Y float32
}

// ColoredVertex is a 2D vertex with per-vertex RGB color.
type ColoredVertex struct {
	X, Y  float32
	Color RGB
}

// TexturedVertex is a 2D vertex with texture coordinates.
type TexturedVertex struct {
	X, Y float32
	U, V float32
}

// FontVertex is a 2D text vertex. Color is the glyph color and Background is
// the rectangle/background color to blend against for bitmap font rendering.
type FontVertex struct {
	X, Y       float32
	U, V       float32
	Color      RGBA
	Background RGBA
}

type commandType int

const (
	cmdResetState commandType = iota
	cmdLoadProjectionMatrix
	cmdClear
	cmdViewport
	cmdScissor
	cmdDisableScissor
	cmdBlend
	cmdDisableBlend
	cmdSetColor
	cmdLineWidth
	cmdDrawLines
	cmdDrawColoredLines
	cmdDrawTriangles
	cmdDrawColoredTriangles
	cmdDrawTexturedTriangles
	cmdDrawFontTriangles
	cmdCall
)

type command struct {
	type_ commandType

	matrix Mat4
	color  RGBA

	lineWidth   float32
	hatchOffset float32
	textureID   TextureID

	x, y, w, h int

	vertexOffset int
	vertexCount  int
	indexOffset  int
	indexCount   int

	drawMode DrawMode
	called   *CmdBuffer
}

// CmdBuffer records draw commands and their transient geometry for one frame
// or for pre-baked static geometry such as video maps.
type CmdBuffer struct {
	commands []command

	points         []PointVertex
	coloredPoints  []ColoredVertex
	texturedPoints []TexturedVertex
	fontPoints     []FontVertex
	indices        []uint32
}

var cmdBufferPool = sync.Pool{New: func() any { return &CmdBuffer{} }}

func GetCmdBuffer() *CmdBuffer {
	return cmdBufferPool.Get().(*CmdBuffer)
}

func ReturnCmdBuffer(cb *CmdBuffer) {
	if cb == nil {
		return
	}
	cb.Reset()
	cmdBufferPool.Put(cb)
}

// Reset clears the buffer while retaining backing allocations for reuse.
func (cb *CmdBuffer) Reset() {
	cb.commands = cb.commands[:0]
	cb.points = cb.points[:0]
	cb.coloredPoints = cb.coloredPoints[:0]
	cb.texturedPoints = cb.texturedPoints[:0]
	cb.fontPoints = cb.fontPoints[:0]
	cb.indices = cb.indices[:0]
}

func (cb *CmdBuffer) Empty() bool { return cb == nil || len(cb.commands) == 0 }

func (cb *CmdBuffer) ResetState() {
	cb.commands = append(cb.commands, command{type_: cmdResetState})
}

func (cb *CmdBuffer) LoadProjectionMatrix(m Mat4) {
	cb.commands = append(cb.commands, command{type_: cmdLoadProjectionMatrix, matrix: m})
}

func (cb *CmdBuffer) Clear(color RGBA) {
	cb.commands = append(cb.commands, command{type_: cmdClear, color: color})
}

func (cb *CmdBuffer) ClearRGB(color RGB) {
	cb.Clear(color.ToRGBA())
}

func (cb *CmdBuffer) Viewport(x, y, w, h int) {
	cb.commands = append(cb.commands, command{type_: cmdViewport, x: x, y: y, w: w, h: h})
}

func (cb *CmdBuffer) Scissor(x, y, w, h int) {
	cb.commands = append(cb.commands, command{type_: cmdScissor, x: x, y: y, w: w, h: h})
}

func (cb *CmdBuffer) DisableScissor() {
	cb.commands = append(cb.commands, command{type_: cmdDisableScissor})
}

func (cb *CmdBuffer) Blend() {
	cb.commands = append(cb.commands, command{type_: cmdBlend})
}

func (cb *CmdBuffer) DisableBlend() {
	cb.commands = append(cb.commands, command{type_: cmdDisableBlend})
}

func (cb *CmdBuffer) SetRGBA(color RGBA) {
	cb.commands = append(cb.commands, command{type_: cmdSetColor, color: color})
}

func (cb *CmdBuffer) SetRGB(color RGB) {
	cb.SetRGBA(color.ToRGBA())
}

func (cb *CmdBuffer) LineWidth(width float32) {
	cb.commands = append(cb.commands, command{type_: cmdLineWidth, lineWidth: width})
}

func (cb *CmdBuffer) DrawLines(points []PointVertex, indices []uint32) {
	if len(points) == 0 || len(indices) == 0 {
		return
	}
	vo, io := len(cb.points), len(cb.indices)
	cb.points = append(cb.points, points...)
	cb.indices = append(cb.indices, indices...)
	cb.commands = append(cb.commands, command{type_: cmdDrawLines, vertexOffset: vo, vertexCount: len(points), indexOffset: io, indexCount: len(indices)})
}

func (cb *CmdBuffer) DrawColoredLines(points []ColoredVertex, indices []uint32) {
	if len(points) == 0 || len(indices) == 0 {
		return
	}
	vo, io := len(cb.coloredPoints), len(cb.indices)
	cb.coloredPoints = append(cb.coloredPoints, points...)
	cb.indices = append(cb.indices, indices...)
	cb.commands = append(cb.commands, command{type_: cmdDrawColoredLines, vertexOffset: vo, vertexCount: len(points), indexOffset: io, indexCount: len(indices)})
}

func (cb *CmdBuffer) DrawTriangles(points []PointVertex, indices []uint32, mode DrawMode, hatchOffset float32) {
	if len(points) == 0 || len(indices) == 0 {
		return
	}
	vo, io := len(cb.points), len(cb.indices)
	cb.points = append(cb.points, points...)
	cb.indices = append(cb.indices, indices...)
	cb.commands = append(cb.commands, command{type_: cmdDrawTriangles, vertexOffset: vo, vertexCount: len(points), indexOffset: io, indexCount: len(indices), drawMode: mode, hatchOffset: hatchOffset})
}

func (cb *CmdBuffer) DrawColoredTriangles(points []ColoredVertex, indices []uint32) {
	if len(points) == 0 || len(indices) == 0 {
		return
	}
	vo, io := len(cb.coloredPoints), len(cb.indices)
	cb.coloredPoints = append(cb.coloredPoints, points...)
	cb.indices = append(cb.indices, indices...)
	cb.commands = append(cb.commands, command{type_: cmdDrawColoredTriangles, vertexOffset: vo, vertexCount: len(points), indexOffset: io, indexCount: len(indices)})
}

func (cb *CmdBuffer) DrawTexturedTriangles(textureID TextureID, points []TexturedVertex, indices []uint32) {
	if len(points) == 0 || len(indices) == 0 {
		return
	}
	vo, io := len(cb.texturedPoints), len(cb.indices)
	cb.texturedPoints = append(cb.texturedPoints, points...)
	cb.indices = append(cb.indices, indices...)
	cb.commands = append(cb.commands, command{type_: cmdDrawTexturedTriangles, textureID: textureID, vertexOffset: vo, vertexCount: len(points), indexOffset: io, indexCount: len(indices)})
}

func (cb *CmdBuffer) DrawFontTriangles(textureID TextureID, points []FontVertex, indices []uint32) {
	if len(points) == 0 || len(indices) == 0 {
		return
	}
	vo, io := len(cb.fontPoints), len(cb.indices)
	cb.fontPoints = append(cb.fontPoints, points...)
	cb.indices = append(cb.indices, indices...)
	cb.commands = append(cb.commands, command{type_: cmdDrawFontTriangles, textureID: textureID, vertexOffset: vo, vertexCount: len(points), indexOffset: io, indexCount: len(indices)})
}

// Call appends a command that replays another CmdBuffer. It is intended for
// pre-baked static content such as video maps.
func (cb *CmdBuffer) Call(other *CmdBuffer) {
	if other == nil || other.Empty() {
		return
	}
	cb.commands = append(cb.commands, command{type_: cmdCall, called: other})
}

// Z is a CRC-style z-index. ASDE-X/ERAM/STARS packages should define their own
// domain constants using this type.
type Z int

// ZCmdBuffer is one frame-level command buffer split into CRC-style z-indexed
// CmdBuffers. It flushes deterministically from low z to high z.
type ZCmdBuffer struct {
	buffers map[Z]*CmdBuffer
	keys    []Z
}

var zCmdBufferPool = sync.Pool{New: func() any { return &ZCmdBuffer{} }}

func GetZCmdBuffer() *ZCmdBuffer {
	zcb := zCmdBufferPool.Get().(*ZCmdBuffer)
	if zcb.buffers == nil {
		zcb.buffers = make(map[Z]*CmdBuffer)
	}
	return zcb
}

func ReturnZCmdBuffer(zcb *ZCmdBuffer) {
	if zcb == nil {
		return
	}
	zcb.Reset()
	zCmdBufferPool.Put(zcb)
}

// Reset clears all z-indexed buffers and returns their CmdBuffers to the pool.
func (zcb *ZCmdBuffer) Reset() {
	for _, z := range zcb.keys {
		ReturnCmdBuffer(zcb.buffers[z])
		delete(zcb.buffers, z)
	}
	zcb.keys = zcb.keys[:0]
}

func (zcb *ZCmdBuffer) Empty() bool {
	return zcb == nil || len(zcb.keys) == 0
}

// At returns the CmdBuffer for the requested z-index, creating it on demand.
func (zcb *ZCmdBuffer) At(z Z) *CmdBuffer {
	if zcb.buffers == nil {
		zcb.buffers = make(map[Z]*CmdBuffer)
	}
	if cb := zcb.buffers[z]; cb != nil {
		return cb
	}
	cb := GetCmdBuffer()
	zcb.buffers[z] = cb
	zcb.keys = append(zcb.keys, z)
	sort.Slice(zcb.keys, func(i, j int) bool { return zcb.keys[i] < zcb.keys[j] })
	return cb
}

// Render submits all non-empty CmdBuffers in increasing z order.
func (zcb *ZCmdBuffer) Render(r Renderer) RendererStats {
	var stats RendererStats
	if zcb == nil || r == nil {
		return stats
	}
	for _, z := range zcb.keys {
		cb := zcb.buffers[z]
		if cb == nil || cb.Empty() {
			continue
		}
		stats.Add(r.RenderCmdBuffer(cb))
	}
	return stats
}
