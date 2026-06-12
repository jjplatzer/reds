package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type legacyGlyph struct {
	codepoint     int32
	width         int32
	height        int32
	textureOffset int32
	bearingX      int32
	bearingY      int32
	advance       int32
}

type legacyFontSize struct {
	family      string
	size        int32
	lineHeight  int32
	atlasWidth  int32
	atlasHeight int32
	atlasR8     []byte
	glyphs      []legacyGlyph
}

func readMaybeZstd(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(path, ".zst") {
		return raw, nil
	}

	zstdPath, err := exec.LookPath("zstd")
	if err != nil {
		return nil, fmt.Errorf("%s is zstd-compressed; install zstd first, e.g. `brew install zstd`", path)
	}

	cmd := exec.Command(zstdPath, "-q", "-d", "-c", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("zstd failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func parseLegacyFont(family string, data []byte) ([]legacyFontSize, error) {
	reader := bytes.NewReader(data)
	var sizes []legacyFontSize

	for reader.Len() > 0 {
		var fs legacyFontSize
		fs.family = family

		if err := binary.Read(reader, binary.LittleEndian, &fs.size); err != nil {
			return nil, fmt.Errorf("read font size: %w", err)
		}
		if err := binary.Read(reader, binary.LittleEndian, &fs.lineHeight); err != nil {
			return nil, fmt.Errorf("read line height for size %d: %w", fs.size, err)
		}
		if err := binary.Read(reader, binary.LittleEndian, &fs.atlasWidth); err != nil {
			return nil, fmt.Errorf("read atlas width for size %d: %w", fs.size, err)
		}
		if err := binary.Read(reader, binary.LittleEndian, &fs.atlasHeight); err != nil {
			return nil, fmt.Errorf("read atlas height for size %d: %w", fs.size, err)
		}

		if fs.size < 0 || fs.lineHeight <= 0 || fs.atlasWidth <= 0 || fs.atlasHeight <= 0 {
			return nil, fmt.Errorf("invalid size record: size=%d lineHeight=%d atlas=%dx%d",
				fs.size, fs.lineHeight, fs.atlasWidth, fs.atlasHeight)
		}

		atlasLen64 := int64(fs.atlasWidth) * int64(fs.atlasHeight)
		if atlasLen64 <= 0 || atlasLen64 > int64(reader.Len()) {
			return nil, fmt.Errorf("truncated atlas for size %d", fs.size)
		}

		fs.atlasR8 = make([]byte, int(atlasLen64))
		if _, err := reader.Read(fs.atlasR8); err != nil {
			return nil, fmt.Errorf("read atlas for size %d: %w", fs.size, err)
		}

		var glyphCount int32
		if err := binary.Read(reader, binary.LittleEndian, &glyphCount); err != nil {
			return nil, fmt.Errorf("read glyph count for size %d: %w", fs.size, err)
		}
		if glyphCount < 0 {
			return nil, fmt.Errorf("negative glyph count for size %d: %d", fs.size, glyphCount)
		}

		fs.glyphs = make([]legacyGlyph, 0, glyphCount)
		for i := int32(0); i < glyphCount; i++ {
			var glyph legacyGlyph
			fields := []*int32{
				&glyph.codepoint,
				&glyph.width,
				&glyph.height,
				&glyph.textureOffset,
				&glyph.bearingX,
				&glyph.bearingY,
				&glyph.advance,
			}
			for _, field := range fields {
				if err := binary.Read(reader, binary.LittleEndian, field); err != nil {
					return nil, fmt.Errorf("read glyph %d for size %d: %w", i, fs.size, err)
				}
			}

			if glyph.codepoint < 0 || glyph.codepoint > 0x10ffff {
				return nil, fmt.Errorf("invalid codepoint %d in size %d", glyph.codepoint, fs.size)
			}
			if glyph.width < 0 || glyph.height < 0 || glyph.textureOffset < 0 || glyph.advance < 0 {
				return nil, fmt.Errorf("invalid glyph metric in size %d, codepoint %d", fs.size, glyph.codepoint)
			}
			if glyph.width > 0 && glyph.height > 0 {
				if glyph.textureOffset+glyph.width > fs.atlasWidth || glyph.height > fs.atlasHeight {
					return nil, fmt.Errorf("glyph outside atlas in size %d, codepoint %d", fs.size, glyph.codepoint)
				}
			}

			fs.glyphs = append(fs.glyphs, glyph)
		}

		sort.Slice(fs.glyphs, func(i, j int) bool {
			return fs.glyphs[i].codepoint < fs.glyphs[j].codepoint
		})
		sizes = append(sizes, fs)
	}

	if len(sizes) == 0 {
		return nil, fmt.Errorf("font file contained no sizes")
	}

	sort.Slice(sizes, func(i, j int) bool {
		return sizes[i].size < sizes[j].size
	})
	return sizes, nil
}

func nominalWidth(fs legacyFontSize) int32 {
	byCodepoint := make(map[int32]legacyGlyph, len(fs.glyphs))
	for _, glyph := range fs.glyphs {
		byCodepoint[glyph.codepoint] = glyph
	}

	for _, codepoint := range []int32{' ', '0', 'A', 0} {
		if glyph, ok := byCodepoint[codepoint]; ok && glyph.advance > 0 {
			return glyph.advance
		}
	}

	var maxAdvance int32
	for _, glyph := range fs.glyphs {
		if glyph.advance > maxAdvance {
			maxAdvance = glyph.advance
		}
	}
	return maxAdvance
}

func sanitizeIdent(value string) string {
	var b strings.Builder
	lastUnderscore := false

	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}

	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "Font"
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "Font" + out
	}
	return out
}

func formatUint32Array(values []uint32, indent string) string {
	if len(values) == 0 {
		return "nil"
	}

	const perLine = 8

	var b strings.Builder
	b.WriteString("[]uint32{")
	for i := 0; i < len(values); i += perLine {
		end := i + perLine
		if end > len(values) {
			end = len(values)
		}

		b.WriteString("\n")
		b.WriteString(indent)
		for j := i; j < end; j++ {
			if j > i {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "0x%08X", values[j])
		}
		b.WriteString(",")
	}
	b.WriteString("\n")
	b.WriteString(indent[:len(indent)-1])
	b.WriteString("}")

	return b.String()
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
