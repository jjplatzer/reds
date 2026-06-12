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
	generatedPackage        = "assets"
	generatedRendererImport = "github.com/juliusplatzer/reds/renderer"
	generatedAllFontsVar    = "EramFonts"
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

type familyFonts struct {
	family string
	sizes  []legacyFontSize
}

func main() {
	inDir := flag.String("in", "", "input directory containing ERAM .bin or .bin.zst font files")
	outPath := flag.String("out", "", "output generated Go file")
	flag.Parse()

	if *inDir == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/eram2reds -in resources/bitmaps/eram/fonts -out eram/assets/font.go")
		os.Exit(2)
	}

	if err := run(*inDir, *outPath); err != nil {
		fmt.Fprintln(os.Stderr, "eram2reds:", err)
		os.Exit(1)
	}
}

func run(inDir, outPath string) error {
	families, err := parseInputDir(inDir)
	if err != nil {
		return err
	}
	if len(families) == 0 {
		return fmt.Errorf("no .bin or .bin.zst font files found in %s", inDir)
	}

	if err := writeGo(outPath, families); err != nil {
		return err
	}

	fmt.Printf("wrote %s\n", outPath)
	fmt.Print("converted font families:")
	for _, family := range families {
		fmt.Printf(" %s(", family.family)
		for i, size := range family.sizes {
			if i > 0 {
				fmt.Print(",")
			}
			fmt.Print(size.size)
		}
		fmt.Print(")")
	}
	fmt.Println()

	return nil
}

func parseInputDir(inDir string) ([]familyFonts, error) {
	info, err := os.Stat(inDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("-in must be a directory: %s", inDir)
	}

	entries, err := os.ReadDir(inDir)
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "._") {
			continue
		}
		if strings.HasSuffix(name, ".bin") || strings.HasSuffix(name, ".bin.zst") {
			paths = append(paths, filepath.Join(inDir, name))
		}
	}
	sort.Strings(paths)

	families := make([]familyFonts, 0, len(paths))
	for _, path := range paths {
		raw, err := readMaybeZstd(path)
		if err != nil {
			return nil, err
		}

		family := fontFamilyFromPath(path)
		sizes, err := parseLegacyFont(family, raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		families = append(families, familyFonts{
			family: family,
			sizes:  sizes,
		})
	}

	sort.Slice(families, func(i, j int) bool {
		return families[i].family < families[j].family
	})
	return families, nil
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

		if !atlasIsBinary(fs.atlasR8) {
			return nil, fmt.Errorf("font %s size %d is not binary 0/255; use alpha conversion instead", family, fs.size)
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
			if glyph.width > 32 {
				return nil, fmt.Errorf("glyph too wide for VICE-style uint32 rows in size %d, codepoint %d: width=%d",
					fs.size, glyph.codepoint, glyph.width)
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

func atlasIsBinary(atlas []byte) bool {
	for _, value := range atlas {
		if value != 0 && value != 255 {
			return false
		}
	}
	return true
}

func writeGo(outPath string, families []familyFonts) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("// Code generated by cmd/eram2reds; DO NOT EDIT.\n")
	b.WriteString("// Source: ERAM binary .bin(.zst) fonts converted to VICE-style 1-bit glyph rows.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", generatedPackage)
	fmt.Fprintf(&b, "import renderer %q\n\n", generatedRendererImport)

	fmt.Fprintf(&b, "var %s = map[string]*renderer.MonoBitmapFont{\n", generatedAllFontsVar)

	for _, family := range families {
		for _, fs := range family.sizes {
			writeFontLiteral(&b, fs)
		}
	}

	b.WriteString("}\n\n")

	for _, family := range families {
		varName := exportedFamilyVarName(family.family)
		fmt.Fprintf(&b, "var %s = map[int]*renderer.MonoBitmapFont{\n", varName)
		for _, fs := range family.sizes {
			fmt.Fprintf(&b, "\t%d: %s[%q],\n", fs.size, generatedAllFontsVar, fontKey(fs))
		}
		b.WriteString("}\n\n")
	}

	for _, family := range families {
		funcName := exportedFamilyFuncName(family.family)
		varName := exportedFamilyVarName(family.family)
		fmt.Fprintf(&b, "func %s(size int) (*renderer.MonoBitmapFont, bool) {\n", funcName)
		fmt.Fprintf(&b, "\tf, ok := %s[size]\n", varName)
		b.WriteString("\treturn f, ok\n")
		b.WriteString("}\n\n")
	}

	return os.WriteFile(outPath, []byte(b.String()), 0o644)
}

func writeFontLiteral(b *strings.Builder, fs legacyFontSize) {
	fmt.Fprintf(b, "\t%q: {\n", fontKey(fs))
	fmt.Fprintf(b, "\t\tPointSize: %d,\n", fs.size)
	fmt.Fprintf(b, "\t\tWidth:     %d,\n", nominalWidth(fs))
	fmt.Fprintf(b, "\t\tHeight:    %d,\n", fs.lineHeight)
	b.WriteString("\t\tGlyphs: []renderer.MonoBitmapGlyph{\n")

	for _, glyph := range fs.glyphs {
		bitmapRows := glyphBitmapRows(fs, glyph)

		// Convert existing FreeType-style bearing metrics into the same top-left
		// offset convention used by VICE's generated BitmapGlyph.
		offsetX := glyph.bearingX
		offsetY := fs.lineHeight - glyph.bearingY

		fmt.Fprintf(b, "\t\t\t%d: {\n", glyph.codepoint)
		fmt.Fprintf(b, "\t\t\t\tName:   %q,\n", glyphName(glyph.codepoint))
		fmt.Fprintf(b, "\t\t\t\tStepX:  %d,\n", glyph.advance)
		fmt.Fprintf(b, "\t\t\t\tBounds: [2]int{%d, %d},\n", glyph.width, glyph.height)
		fmt.Fprintf(b, "\t\t\t\tOffset: [2]int{%d, %d},\n", offsetX, offsetY)
		fmt.Fprintf(b, "\t\t\t\tBitmap: %s,\n", formatUint32Array(bitmapRows, "\t\t\t\t"))
		b.WriteString("\t\t\t},\n")
	}

	b.WriteString("\t\t},\n")
	b.WriteString("\t},\n\n")
}

func fontKey(fs legacyFontSize) string {
	return fmt.Sprintf("%s-%d", fs.family, fs.size)
}

func fontFamilyFromPath(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, ".zst")
	base = strings.TrimSuffix(base, ".bin")
	return sanitizeIdent(base)
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
	return out
}

func exportedFamilyVarName(family string) string {
	return sanitizeIdent(family) + "Fonts"
}

func exportedFamilyFuncName(family string) string {
	return sanitizeIdent(family) + "Font"
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

func glyphBitmapRows(fs legacyFontSize, glyph legacyGlyph) []uint32 {
	if glyph.width <= 0 || glyph.height <= 0 {
		return nil
	}

	width := int(glyph.width)
	height := int(glyph.height)
	atlasWidth := int(fs.atlasWidth)
	x0 := int(glyph.textureOffset)

	rows := make([]uint32, height)
	for y := 0; y < height; y++ {
		rowStart := y*atlasWidth + x0
		var row uint32
		for x := 0; x < width; x++ {
			if fs.atlasR8[rowStart+x] != 0 {
				row |= uint32(1) << uint(31-x)
			}
		}
		rows[y] = row
	}

	return rows
}

func glyphName(codepoint int32) string {
	if codepoint >= 0 && codepoint <= 0xffff {
		return fmt.Sprintf("uni%04X", codepoint)
	}
	return fmt.Sprintf("uni%06X", codepoint)
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
