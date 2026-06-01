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
		if size <= 0 || lineHeight <= 0 || atlasWidth <= 0 || atlasHeight <= 0 {
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
