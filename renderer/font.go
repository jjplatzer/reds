package renderer

import (
	"encoding/binary"
	"fmt"
)

type BitmapGlyph struct {
	Codepoint     rune
	Width         int
	Height        int
	TextureOffset int
	BearingX      int
	BearingY      int
	Advance       int
}

type AlphaBitmapFont struct {
	PointSize int
	Width     int
	Height    int
	Glyphs    []AlphaBitmapGlyph
}

type AlphaBitmapGlyph struct {
	Name   string
	StepX  int
	Bounds [2]int
	Offset [2]int
	Alpha  []uint8
}

type MonoBitmapFont struct {
	PointSize int
	Width     int
	Height    int
	Glyphs    []MonoBitmapGlyph
}

type MonoBitmapGlyph struct {
	Name   string
	StepX  int
	Bounds [2]int
	Offset [2]int
	Bitmap []uint32
}

type BitmapFontSize struct {
	Size        int
	LineHeight  int
	AtlasWidth  int
	AtlasHeight int
	AtlasR8     []byte

	Glyphs     []BitmapGlyph
	GlyphIndex map[rune]int
}

func (fs *BitmapFontSize) Glyph(codepoint rune) (*BitmapGlyph, bool) {
	if fs == nil {
		return nil, false
	}
	index, ok := fs.GlyphIndex[codepoint]
	if !ok || index < 0 || index >= len(fs.Glyphs) {
		return nil, false
	}
	return &fs.Glyphs[index], true
}

type BitmapFont struct {
	sizes map[int]*BitmapFontSize
}

func LoadBitmapFontBytes(data []byte) (*BitmapFont, error) {
	offset := 0
	sizes := make(map[int]*BitmapFontSize)

	readI32 := func(field string) (int, error) {
		if offset+4 > len(data) {
			return 0, fmt.Errorf("truncated %s", field)
		}
		value := int(int32(binary.LittleEndian.Uint32(data[offset : offset+4])))
		offset += 4
		return value, nil
	}

	for offset < len(data) {
		size, err := readI32("font header")
		if err != nil {
			return nil, err
		}
		lineHeight, err := readI32("font header")
		if err != nil {
			return nil, err
		}
		atlasWidth, err := readI32("font header")
		if err != nil {
			return nil, err
		}
		atlasHeight, err := readI32("font header")
		if err != nil {
			return nil, err
		}
		if size < 0 || lineHeight <= 0 || atlasWidth <= 0 || atlasHeight <= 0 {
			return nil, fmt.Errorf("invalid font size entry")
		}

		atlasBytes := int64(atlasWidth) * int64(atlasHeight)
		if atlasBytes <= 0 || atlasBytes > int64(len(data)-offset) {
			return nil, fmt.Errorf("truncated font atlas")
		}

		fs := &BitmapFontSize{
			Size:        size,
			LineHeight:  lineHeight,
			AtlasWidth:  atlasWidth,
			AtlasHeight: atlasHeight,
			AtlasR8:     append([]byte(nil), data[offset:offset+int(atlasBytes)]...),
			GlyphIndex:  make(map[rune]int),
		}
		offset += int(atlasBytes)

		glyphCount, err := readI32("font glyph count")
		if err != nil || glyphCount < 0 {
			return nil, fmt.Errorf("invalid font glyph count")
		}
		const glyphBytes = 7 * 4
		if glyphCount > (len(data)-offset)/glyphBytes {
			return nil, fmt.Errorf("truncated glyph data")
		}
		fs.Glyphs = make([]BitmapGlyph, 0, glyphCount)

		for range glyphCount {
			fields := make([]int, 7)
			for i := range fields {
				value, err := readI32("glyph data")
				if err != nil {
					return nil, err
				}
				fields[i] = value
			}

			glyph := BitmapGlyph{
				Codepoint:     rune(fields[0]),
				Width:         fields[1],
				Height:        fields[2],
				TextureOffset: fields[3],
				BearingX:      fields[4],
				BearingY:      fields[5],
				Advance:       fields[6],
			}
			fs.GlyphIndex[glyph.Codepoint] = len(fs.Glyphs)
			fs.Glyphs = append(fs.Glyphs, glyph)
		}

		sizes[size] = fs
	}

	if len(sizes) == 0 {
		return nil, fmt.Errorf("font file contained no sizes")
	}
	return &BitmapFont{sizes: sizes}, nil
}

func NewBitmapFontFromAlpha(src map[int]*AlphaBitmapFont) *BitmapFont {
	sizes := make(map[int]*BitmapFontSize, len(src))

	for size, font := range src {
		if font == nil {
			continue
		}

		atlasWidth := 0
		atlasHeight := 0
		glyphCount := 0

		for _, glyph := range font.Glyphs {
			if !alphaGlyphPresent(glyph) {
				continue
			}

			w := glyph.Bounds[0]
			h := glyph.Bounds[1]
			if w < 0 || h < 0 {
				continue
			}

			glyphCount++
			atlasWidth += w
			if h > atlasHeight {
				atlasHeight = h
			}
		}

		if atlasWidth <= 0 {
			atlasWidth = 1
		}
		if atlasHeight <= 0 {
			atlasHeight = font.Height
			if atlasHeight <= 0 {
				atlasHeight = 1
			}
		}

		atlas := make([]byte, atlasWidth*atlasHeight)
		glyphs := make([]BitmapGlyph, 0, glyphCount)
		glyphIndex := make(map[rune]int, glyphCount)
		textureOffset := 0

		for codepoint, glyph := range font.Glyphs {
			if !alphaGlyphPresent(glyph) {
				continue
			}

			w := glyph.Bounds[0]
			h := glyph.Bounds[1]
			if w < 0 || h < 0 {
				continue
			}

			if w > 0 && h > 0 && len(glyph.Alpha) >= w*h {
				for y := 0; y < h; y++ {
					srcOffset := y * w
					dstOffset := y*atlasWidth + textureOffset
					copy(atlas[dstOffset:dstOffset+w], glyph.Alpha[srcOffset:srcOffset+w])
				}
			}

			runtimeGlyph := BitmapGlyph{
				Codepoint:     rune(codepoint),
				Width:         w,
				Height:        h,
				TextureOffset: textureOffset,
				BearingX:      glyph.Offset[0],
				BearingY:      font.Height - glyph.Offset[1],
				Advance:       glyph.StepX,
			}

			glyphIndex[runtimeGlyph.Codepoint] = len(glyphs)
			glyphs = append(glyphs, runtimeGlyph)
			textureOffset += w
		}

		lineHeight := font.Height
		if lineHeight <= 0 {
			lineHeight = atlasHeight
		}

		sizes[size] = &BitmapFontSize{
			Size:        size,
			LineHeight:  lineHeight,
			AtlasWidth:  atlasWidth,
			AtlasHeight: atlasHeight,
			AtlasR8:     atlas,
			Glyphs:      glyphs,
			GlyphIndex:  glyphIndex,
		}
	}

	return &BitmapFont{sizes: sizes}
}

func NewBitmapFontFromMono(src map[int]*MonoBitmapFont) *BitmapFont {
	sizes := make(map[int]*BitmapFontSize, len(src))

	for size, font := range src {
		if font == nil {
			continue
		}

		atlasWidth := 0
		atlasHeight := 0
		glyphCount := 0

		for _, glyph := range font.Glyphs {
			if !monoGlyphPresent(glyph) {
				continue
			}

			w := glyph.Bounds[0]
			h := glyph.Bounds[1]
			if w < 0 || h < 0 {
				continue
			}

			glyphCount++
			atlasWidth += w
			if h > atlasHeight {
				atlasHeight = h
			}
		}

		if atlasWidth <= 0 {
			atlasWidth = 1
		}
		if atlasHeight <= 0 {
			atlasHeight = font.Height
			if atlasHeight <= 0 {
				atlasHeight = 1
			}
		}

		atlas := make([]byte, atlasWidth*atlasHeight)
		glyphs := make([]BitmapGlyph, 0, glyphCount)
		glyphIndex := make(map[rune]int, glyphCount)
		textureOffset := 0

		for codepoint, glyph := range font.Glyphs {
			if !monoGlyphPresent(glyph) {
				continue
			}

			w := glyph.Bounds[0]
			h := glyph.Bounds[1]
			if w < 0 || h < 0 {
				continue
			}

			if w > 0 && h > 0 {
				for y := 0; y < h && y < len(glyph.Bitmap); y++ {
					row := glyph.Bitmap[y]
					dstOffset := y*atlasWidth + textureOffset

					for x := 0; x < w; x++ {
						if row&(uint32(1)<<uint(31-x)) != 0 {
							atlas[dstOffset+x] = 255
						}
					}
				}
			}

			runtimeGlyph := BitmapGlyph{
				Codepoint:     rune(codepoint),
				Width:         w,
				Height:        h,
				TextureOffset: textureOffset,
				BearingX:      glyph.Offset[0],
				BearingY:      font.Height - glyph.Offset[1],
				Advance:       glyph.StepX,
			}

			glyphIndex[runtimeGlyph.Codepoint] = len(glyphs)
			glyphs = append(glyphs, runtimeGlyph)
			textureOffset += w
		}

		lineHeight := font.Height
		if lineHeight <= 0 {
			lineHeight = atlasHeight
		}

		sizes[size] = &BitmapFontSize{
			Size:        size,
			LineHeight:  lineHeight,
			AtlasWidth:  atlasWidth,
			AtlasHeight: atlasHeight,
			AtlasR8:     atlas,
			Glyphs:      glyphs,
			GlyphIndex:  glyphIndex,
		}
	}

	return &BitmapFont{sizes: sizes}
}

func alphaGlyphPresent(g AlphaBitmapGlyph) bool {
	return g.Name != "" ||
		g.StepX != 0 ||
		g.Bounds[0] != 0 ||
		g.Bounds[1] != 0 ||
		g.Offset[0] != 0 ||
		g.Offset[1] != 0 ||
		len(g.Alpha) != 0
}

func monoGlyphPresent(g MonoBitmapGlyph) bool {
	return g.Name != "" ||
		g.StepX != 0 ||
		g.Bounds[0] != 0 ||
		g.Bounds[1] != 0 ||
		g.Offset[0] != 0 ||
		g.Offset[1] != 0 ||
		len(g.Bitmap) != 0
}

func (f *BitmapFont) Size(size int) *BitmapFontSize {
	if f == nil {
		return nil
	}
	return f.sizes[size]
}

func (f *BitmapFont) MeasureText(text string, size int) (width, height int) {
	fs := f.Size(size)
	if fs == nil {
		return 0, 0
	}

	maxWidth := 0
	penX := 0
	lines := 1
	var pendingGlyph *BitmapGlyph

	flushLine := func() {
		width := penX
		if pendingGlyph != nil {
			width += pendingGlyph.Width
		}
		if width > maxWidth {
			maxWidth = width
		}
		penX = 0
		pendingGlyph = nil
	}

	for _, codepoint := range text {
		switch codepoint {
		case '\r':
			continue
		case '\n':
			flushLine()
			lines++
			continue
		}

		glyph, ok := fs.Glyph(codepoint)
		if !ok {
			continue
		}
		if pendingGlyph != nil {
			penX += pendingGlyph.Advance
		}
		pendingGlyph = glyph
	}

	flushLine()
	return maxWidth, lines * fs.LineHeight
}

func (f *BitmapFont) LineHeight(size int) int {
	fs := f.Size(size)
	if fs == nil {
		return 0
	}
	return fs.LineHeight
}

func (f *BitmapFont) CharSize(size int) (width int, height int) {
	fs := f.Size(size)
	if fs == nil {
		return 0, 0
	}

	if glyph, ok := fs.Glyph(' '); ok {
		return glyph.Advance, fs.LineHeight
	}
	if glyph, ok := fs.Glyph('0'); ok {
		return glyph.Advance, fs.LineHeight
	}
	return 0, fs.LineHeight
}

func (f *BitmapFont) FontSpacing(size int) int {
	fs := f.Size(size)
	if fs == nil {
		return 0
	}

	if glyph, ok := fs.Glyph('0'); ok {
		if spacing := glyph.Advance - glyph.Width; spacing > 0 {
			return spacing
		}
	}
	return 0
}
