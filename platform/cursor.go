package platform

import (
	"fmt"
	"image"
	"image/color"
	"sort"

	"github.com/go-gl/glfw/v3.3/glfw"
)

// Cursor is a platform cursor handle. Callers choose cursors by domain-level
// meaning while the platform package owns the native GLFW object.
type Cursor struct {
	cursor *glfw.Cursor
}

/*
.cur uses Little Endian encoding.

File header:
0-1: reserved. (0 is valid)
2-3: file type. (2 is cursor)
4-5: number of images.

Each image directory entry is 16 bytes at 6 + 16 * i:
0: width.
1: height.
2-3: reserved.
4-5: hotspot x.
6-7: hotspot y.
8-11: size.
12-15: offset.

The image offset points to an embedded DIB with an XOR bitmap and an AND
transparency mask. The ASDE-X cursor assets use 1, 8, and 32 bit DIBs.
*/
type curEntry struct {
	width, height int
	hotspot       [2]int
	imageSize     int
	imageOffset   int
}

func (g *glfwPlatform) LoadCursorFromBytes(name string, data []byte) (*Cursor, error) {
	targetSize := int(32*g.DPIScale() + 0.5)
	if targetSize <= 0 {
		targetSize = 32
	}

	rgba, hotspot, err := loadCurBytes(name, data, targetSize)
	if err != nil {
		return nil, err
	}

	hotspot[0] = clampCursorInt(hotspot[0], 0, rgba.Rect.Dx()-1)
	hotspot[1] = clampCursorInt(hotspot[1], 0, rgba.Rect.Dy()-1)

	native := glfw.CreateCursor(rgba, hotspot[0], hotspot[1])
	if native == nil {
		return nil, fmt.Errorf("%s: failed to create GLFW cursor", name)
	}
	g.loadedCursors = append(g.loadedCursors, native)
	return &Cursor{cursor: native}, nil
}

func (g *glfwPlatform) SetCursorOverride(cursor *Cursor) {
	if g == nil || g.window == nil {
		return
	}

	g.cursorHiddenOverride = false
	if cursor == nil || cursor.cursor == nil {
		g.cursorOverride = nil
		return
	}

	g.cursorOverride = cursor.cursor
}

func (g *glfwPlatform) SetCursorHiddenOverride() {
	if g == nil || g.window == nil {
		return
	}

	g.cursorOverride = nil
	g.cursorHiddenOverride = true
}

func (g *glfwPlatform) ClearCursorOverride() {
	if g == nil || g.window == nil {
		return
	}

	g.cursorOverride = nil
	g.cursorHiddenOverride = false
}

func (g *glfwPlatform) ShowSystemCursor() {
	if g == nil || g.window == nil {
		return
	}

	g.cursorOverride = nil
	g.cursorHiddenOverride = false
	g.currentCursor = nil
	g.currentCursorHidden = false

	g.window.SetInputMode(glfw.CursorMode, glfw.CursorNormal)
	g.window.SetCursor(nil)
}

func (g *glfwPlatform) applyCursorState() {
	if g == nil || g.window == nil {
		return
	}

	if g.cursorHiddenOverride {
		if !g.currentCursorHidden {
			g.window.SetCursor(nil)
			g.window.SetInputMode(glfw.CursorMode, glfw.CursorHidden)
			g.currentCursor = nil
			g.currentCursorHidden = true
		}
		return
	}

	if g.currentCursorHidden {
		g.window.SetInputMode(glfw.CursorMode, glfw.CursorNormal)
		g.currentCursorHidden = false
	}
	if g.currentCursor != g.cursorOverride {
		g.currentCursor = g.cursorOverride
		g.window.SetCursor(g.cursorOverride)
	}
}

func loadCurBytes(name string, data []byte, targetSize int) (*image.RGBA, [2]int, error) {
	if len(data) < 6 {
		return nil, [2]int{}, fmt.Errorf("%s: truncated .cur", name)
	}
	if u16le(data, 0) != 0 || u16le(data, 2) != 2 {
		return nil, [2]int{}, fmt.Errorf("%s: not a valid .cur file", name)
	}

	count := int(u16le(data, 4))
	if count < 1 || count > (len(data)-6)/16 {
		return nil, [2]int{}, fmt.Errorf("%s: invalid .cur directory", name)
	}

	entries := make([]curEntry, 0, count)
	for i := 0; i < count; i++ {
		offset := 6 + i*16
		width := int(data[offset])
		if width == 0 {
			width = 256
		}
		height := int(data[offset+1])
		if height == 0 {
			height = 256
		}

		entries = append(entries, curEntry{
			width:       width,
			height:      height,
			hotspot:     [2]int{int(u16le(data, offset+4)), int(u16le(data, offset+6))},
			imageSize:   int(u32le(data, offset+8)),
			imageOffset: int(u32le(data, offset+12)),
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return cursorSizeDistance(entries[i], targetSize) < cursorSizeDistance(entries[j], targetSize)
	})

	var lastErr error
	for _, entry := range entries {
		rgba, err := decodeCursorDIB(data, entry)
		if err == nil {
			return rgba, entry.hotspot, nil
		}
		lastErr = err
	}
	return nil, [2]int{}, fmt.Errorf("%s: %w", name, lastErr)
}

func decodeCursorDIB(data []byte, entry curEntry) (*image.RGBA, error) {
	if entry.imageSize < 40 ||
		entry.imageOffset < 0 ||
		entry.imageOffset > len(data)-entry.imageSize {
		return nil, fmt.Errorf("invalid .cur image bounds")
	}

	dib := entry.imageOffset
	imageEnd := entry.imageOffset + entry.imageSize
	if dib > imageEnd-40 {
		return nil, fmt.Errorf("truncated cursor DIB")
	}

	headerSize := int(u32le(data, dib))
	if headerSize < 40 || headerSize > imageEnd-dib {
		return nil, fmt.Errorf("unsupported DIB header")
	}

	dibWidth := int(s32le(data, dib+4))
	dibHeight := int(s32le(data, dib+8))
	planes := u16le(data, dib+12)
	bpp := int(u16le(data, dib+14))
	compression := u32le(data, dib+16)
	colorsUsed := int(u32le(data, dib+32))
	if planes != 1 || compression != 0 {
		return nil, fmt.Errorf("unsupported compressed cursor DIB")
	}

	height := dibHeight
	if height < 0 {
		height = -height
	}
	height /= 2
	if dibWidth <= 0 || height <= 0 || dibWidth != entry.width || height != entry.height {
		return nil, fmt.Errorf("cursor size mismatch")
	}
	if bpp != 1 && bpp != 8 && bpp != 32 {
		return nil, fmt.Errorf("unsupported cursor depth: %d bpp", bpp)
	}

	paletteCount := 0
	if bpp <= 8 {
		paletteCount = colorsUsed
		if paletteCount == 0 {
			paletteCount = 1 << bpp
		}
	}

	paletteOffset := dib + headerSize
	xorStride := ((dibWidth*bpp + 31) / 32) * 4
	andStride := ((dibWidth + 31) / 32) * 4
	xorOffset := paletteOffset + paletteCount*4
	andOffset := xorOffset + xorStride*height
	if paletteOffset < dib ||
		xorOffset < paletteOffset ||
		andOffset < xorOffset ||
		andOffset > imageEnd-andStride*height {
		return nil, fmt.Errorf("truncated cursor bitmap data")
	}

	palette := make([]color.RGBA, paletteCount)
	for i := range palette {
		offset := paletteOffset + i*4
		if offset > imageEnd-4 {
			return nil, fmt.Errorf("truncated cursor palette")
		}
		palette[i] = color.RGBA{R: data[offset+2], G: data[offset+1], B: data[offset], A: 255}
	}

	rgba := image.NewRGBA(image.Rect(0, 0, dibWidth, height))
	for y := 0; y < height; y++ {
		srcY := y
		if dibHeight > 0 {
			srcY = height - 1 - y
		}
		xorRow := data[xorOffset+xorStride*srcY : xorOffset+xorStride*(srcY+1)]
		andRow := data[andOffset+andStride*srcY : andOffset+andStride*(srcY+1)]

		for x := 0; x < dibWidth; x++ {
			transparent := maskBitIsSet(andRow, x)
			var pixel color.RGBA

			switch bpp {
			case 32:
				offset := x * 4
				pixel = color.RGBA{
					R: data[xorOffset+xorStride*srcY+offset+2],
					G: data[xorOffset+xorStride*srcY+offset+1],
					B: data[xorOffset+xorStride*srcY+offset],
					A: data[xorOffset+xorStride*srcY+offset+3],
				}
			case 8:
				pixel = paletteColor(palette, int(xorRow[x]))
			case 1:
				index := 0
				if maskBitIsSet(xorRow, x) {
					index = 1
				}
				pixel = paletteColor(palette, index)
			}

			if transparent {
				pixel.A = 0
			} else if bpp != 32 {
				pixel.A = 255
			}
			rgba.SetRGBA(x, y, pixel)
		}
	}
	return rgba, nil
}

func cursorSizeDistance(entry curEntry, targetSize int) int {
	return absCursorInt(entry.width-targetSize) + absCursorInt(entry.height-targetSize)
}

func paletteColor(palette []color.RGBA, index int) color.RGBA {
	if index < 0 || index >= len(palette) {
		return color.RGBA{A: 255}
	}
	return palette[index]
}

func maskBitIsSet(row []byte, x int) bool {
	return row[x>>3]&(0x80>>uint(x&7)) != 0
}

func u16le(data []byte, offset int) uint16 {
	return uint16(data[offset]) | uint16(data[offset+1])<<8
}

func u32le(data []byte, offset int) uint32 {
	return uint32(data[offset]) |
		uint32(data[offset+1])<<8 |
		uint32(data[offset+2])<<16 |
		uint32(data[offset+3])<<24
}

func s32le(data []byte, offset int) int32 {
	return int32(u32le(data, offset))
}

func clampCursorInt(value, lo, hi int) int {
	if value < lo {
		return lo
	}
	if value > hi {
		return hi
	}
	return value
}

func absCursorInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
