// Converts CRC's ASDE-X bitmap .bin asset

package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	generatedPackage        = "asdex"
	generatedRendererImport = "github.com/juliusplatzer/reds/renderer"
	generatedVarName        = "asdexFonts"
	generatedFuncName       = "asdexFont"
)

var wantedSizes = map[int32]bool{
	1: true,
	2: true,
	3: true,
	4: true,
	5: true,
	6: true,
}

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
	size        int32
	lineHeight  int32
	atlasWidth  int32
	atlasHeight int32
	atlasR8     []byte
	glyphs      []legacyGlyph
}

func main() {
	inPath := flag.String("in", "", "input legacy font.bin or font.bin.zst")
	outPath := flag.String("out", "", "output generated Go file")
	flag.Parse()

	if *inPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/bin2reds -in resources/bitmaps/asdex/fonts/font.bin.zst -out asdex/fontbitmaps.go")
		os.Exit(2)
	}

	if err := run(*inPath, *outPath); err != nil {
		fmt.Fprintln(os.Stderr, "bin2reds:", err)
		os.Exit(1)
	}
}

func run(inPath, outPath string) error {
	raw, err := readMaybeZstd(inPath)
	if err != nil {
		return err
	}

	sizes, err := parseLegacyFont(raw)
	if err != nil {
		return err
	}

	filtered := sizes[:0]
	for _, fs := range sizes {
		if wantedSizes[fs.size] {
			filtered = append(filtered, fs)
		}
	}
	if len(filtered) == 0 {
		return fmt.Errorf("no sizes 1..6 found in %s", inPath)
	}

	if err := writeGo(outPath, filtered); err != nil {
		return err
	}

	fmt.Printf("wrote %s\n", outPath)
	fmt.Printf("converted sizes:")
	for _, fs := range filtered {
		fmt.Printf(" %d", fs.size)
	}
	fmt.Println()

	return nil
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

func parseLegacyFont(data []byte) ([]legacyFontSize, error) {
	r := bytes.NewReader(data)
	var sizes []legacyFontSize

	for r.Len() > 0 {
		var fs legacyFontSize

		if err := binary.Read(r, binary.LittleEndian, &fs.size); err != nil {
			return nil, fmt.Errorf("read font size: %w", err)
		}
		if err := binary.Read(r, binary.LittleEndian, &fs.lineHeight); err != nil {
			return nil, fmt.Errorf("read line height for size %d: %w", fs.size, err)
		}
		if err := binary.Read(r, binary.LittleEndian, &fs.atlasWidth); err != nil {
			return nil, fmt.Errorf("read atlas width for size %d: %w", fs.size, err)
		}
		if err := binary.Read(r, binary.LittleEndian, &fs.atlasHeight); err != nil {
			return nil, fmt.Errorf("read atlas height for size %d: %w", fs.size, err)
		}

		if fs.size < 0 || fs.lineHeight <= 0 || fs.atlasWidth <= 0 || fs.atlasHeight <= 0 {
			return nil, fmt.Errorf("invalid size record: size=%d lineHeight=%d atlas=%dx%d",
				fs.size, fs.lineHeight, fs.atlasWidth, fs.atlasHeight)
		}

		atlasLen64 := int64(fs.atlasWidth) * int64(fs.atlasHeight)
		if atlasLen64 <= 0 || atlasLen64 > int64(r.Len()) {
			return nil, fmt.Errorf("truncated atlas for size %d", fs.size)
		}

		fs.atlasR8 = make([]byte, int(atlasLen64))
		if _, err := r.Read(fs.atlasR8); err != nil {
			return nil, fmt.Errorf("read atlas for size %d: %w", fs.size, err)
		}

		var glyphCount int32
		if err := binary.Read(r, binary.LittleEndian, &glyphCount); err != nil {
			return nil, fmt.Errorf("read glyph count for size %d: %w", fs.size, err)
		}
		if glyphCount < 0 {
			return nil, fmt.Errorf("negative glyph count for size %d: %d", fs.size, glyphCount)
		}

		fs.glyphs = make([]legacyGlyph, 0, glyphCount)
		for i := int32(0); i < glyphCount; i++ {
			var g legacyGlyph
			fields := []*int32{
				&g.codepoint,
				&g.width,
				&g.height,
				&g.textureOffset,
				&g.bearingX,
				&g.bearingY,
				&g.advance,
			}
			for _, field := range fields {
				if err := binary.Read(r, binary.LittleEndian, field); err != nil {
					return nil, fmt.Errorf("read glyph %d for size %d: %w", i, fs.size, err)
				}
			}

			if g.codepoint < 0 || g.codepoint > 0x10ffff {
				return nil, fmt.Errorf("invalid codepoint %d in size %d", g.codepoint, fs.size)
			}
			if g.width < 0 || g.height < 0 || g.textureOffset < 0 || g.advance < 0 {
				return nil, fmt.Errorf("invalid glyph metric in size %d, codepoint %d", fs.size, g.codepoint)
			}
			if g.width > 0 && g.height > 0 {
				if g.textureOffset+g.width > fs.atlasWidth || g.height > fs.atlasHeight {
					return nil, fmt.Errorf("glyph outside atlas in size %d, codepoint %d", fs.size, g.codepoint)
				}
			}

			fs.glyphs = append(fs.glyphs, g)
		}

		sizes = append(sizes, fs)
	}

	sort.Slice(sizes, func(i, j int) bool {
		return sizes[i].size < sizes[j].size
	})
	return sizes, nil
}

func writeGo(outPath string, sizes []legacyFontSize) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}

	var b strings.Builder

	b.WriteString("// Code generated by cmd/bin2reds; DO NOT EDIT.\n")
	b.WriteString("// Source: legacy ASDE-X font.bin.zst converted to grayscale glyph data.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", generatedPackage)
	fmt.Fprintf(&b, "import renderer %q\n\n", generatedRendererImport)

	fmt.Fprintf(&b, "var %s = map[int]*renderer.AlphaBitmapFont{\n", generatedVarName)

	for _, fs := range sizes {
		fmt.Fprintf(&b, "\t%d: {\n", fs.size)
		fmt.Fprintf(&b, "\t\tPointSize: %d,\n", fs.size)
		fmt.Fprintf(&b, "\t\tWidth:     %d,\n", nominalWidth(fs))
		fmt.Fprintf(&b, "\t\tHeight:    %d,\n", fs.lineHeight)
		b.WriteString("\t\tGlyphs: []renderer.AlphaBitmapGlyph{\n")

		sort.Slice(fs.glyphs, func(i, j int) bool {
			return fs.glyphs[i].codepoint < fs.glyphs[j].codepoint
		})

		for _, g := range fs.glyphs {
			alpha := extractAlpha(fs, g)

			// Convert legacy FreeType-style bearing metrics to a top-left offset
			// relative to the line origin. The renderer adapter reverses this.
			offsetX := g.bearingX
			offsetY := fs.lineHeight - g.bearingY

			fmt.Fprintf(&b, "\t\t\t%d: {\n", g.codepoint)
			fmt.Fprintf(&b, "\t\t\t\tName:   %q,\n", fmt.Sprintf("char%d", g.codepoint))
			fmt.Fprintf(&b, "\t\t\t\tStepX:  %d,\n", g.advance)
			fmt.Fprintf(&b, "\t\t\t\tBounds: [2]int{%d, %d},\n", g.width, g.height)
			fmt.Fprintf(&b, "\t\t\t\tOffset: [2]int{%d, %d},\n", offsetX, offsetY)
			fmt.Fprintf(&b, "\t\t\t\tAlpha:  %s,\n", formatUint8Array(alpha, "\t\t\t\t"))
			b.WriteString("\t\t\t},\n")
		}

		b.WriteString("\t\t},\n")
		b.WriteString("\t},\n\n")
	}

	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "func %s(size int) (*renderer.AlphaBitmapFont, bool) {\n", generatedFuncName)
	fmt.Fprintf(&b, "\tf, ok := %s[size]\n", generatedVarName)
	b.WriteString("\treturn f, ok\n")
	b.WriteString("}\n")

	return os.WriteFile(outPath, []byte(b.String()), 0o644)
}

func nominalWidth(fs legacyFontSize) int32 {
	byCodepoint := make(map[int32]legacyGlyph, len(fs.glyphs))
	for _, g := range fs.glyphs {
		byCodepoint[g.codepoint] = g
	}

	for _, cp := range []int32{' ', '0', 'A'} {
		if g, ok := byCodepoint[cp]; ok && g.advance > 0 {
			return g.advance
		}
	}

	var maxAdvance int32
	for _, g := range fs.glyphs {
		if g.advance > maxAdvance {
			maxAdvance = g.advance
		}
	}
	return maxAdvance
}

func extractAlpha(fs legacyFontSize, g legacyGlyph) []byte {
	if g.width <= 0 || g.height <= 0 {
		return nil
	}

	w := int(g.width)
	h := int(g.height)
	atlasWidth := int(fs.atlasWidth)
	x0 := int(g.textureOffset)

	out := make([]byte, 0, w*h)
	for y := 0; y < h; y++ {
		rowStart := y*atlasWidth + x0
		out = append(out, fs.atlasR8[rowStart:rowStart+w]...)
	}
	return out
}

func formatUint8Array(values []byte, indent string) string {
	if len(values) == 0 {
		return "nil"
	}

	const perLine = 24

	var b strings.Builder
	b.WriteString("[]uint8{")
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
			fmt.Fprintf(&b, "%d", values[j])
		}
		b.WriteString(",")
	}
	b.WriteString("\n")
	b.WriteString(indent[:len(indent)-1])
	b.WriteString("}")

	return b.String()
}
