package renderer

import (
	"fmt"
	"image"
)

// TextureID is an application-level texture handle. The OpenGL backend maps
// these IDs to actual GL texture names so callers never depend on GL types.
type TextureID uint32

// RendererStats tracks the work submitted to the backend for one render pass.
type RendererStats struct {
	Buffers     int
	BufferBytes int
	DrawCalls   int
	Points      int
	Lines       int
	Triangles   int
}

// Add merges another stats value into s.
func (s *RendererStats) Add(o RendererStats) {
	s.Buffers += o.Buffers
	s.BufferBytes += o.BufferBytes
	s.DrawCalls += o.DrawCalls
	s.Points += o.Points
	s.Lines += o.Lines
	s.Triangles += o.Triangles
}

func (s RendererStats) String() string {
	return fmt.Sprintf("%d buffers (%.2f MB), %d draw calls: %d points, %d lines, %d tris",
		s.Buffers, float32(s.BufferBytes)/(1024*1024), s.DrawCalls, s.Points, s.Lines, s.Triangles)
}

// Renderer consumes CmdBuffers and translates them into backend draw calls.
type Renderer interface {
	Init() error
	Dispose()

	CreateTextureFromImage(img image.Image, magNearest bool) TextureID
	CreateTextureR8(width, height int, pixels []byte, magNearest bool) TextureID
	UpdateTextureFromImage(id TextureID, img image.Image, magNearest bool)
	DestroyTexture(id TextureID)

	RenderCmdBuffer(cb *CmdBuffer) RendererStats
	RenderZCmdBuffer(zcb *ZCmdBuffer) RendererStats
	ReadPixels(x, y, w, h int, outRGBA []byte)
}
