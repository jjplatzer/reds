package stars

import (
	"github.com/juliusplatzer/reds/renderer"
	starsassets "github.com/juliusplatzer/reds/stars/assets"
)

const (
	starsCharacterSizeCount = 6
)

var (
	// TI 6191.409 Rev. 30 section 4.9.1 exposes character sizes 0-5 for all
	// display groups except the DCB, which is limited to 0-2. These are the
	// exact bitmap-font sizes used by VICE for the two STARS font sets.
	fontSetASizes = [starsCharacterSizeCount]int{9, 14, 17, 20, 21, 23}
	fontSetBSizes = [starsCharacterSizeCount]int{11, 12, 15, 16, 18, 19}

	fontSetAFontNames = [starsCharacterSizeCount]string{
		"sddCharFontSetASize0",
		"sddCharFontSetASize1",
		"sddCharFontSetASize2",
		"sddCharFontSetASize3",
		"sddCharFontSetASize4",
		"sddCharFontSetASize5",
	}
	fontSetBFontNames = [starsCharacterSizeCount]string{
		"sddCharFontSetBSize0",
		"sddCharFontSetBSize1",
		"sddCharFontSetBSize2",
		"sddCharFontSetBSize3",
		"sddCharFontSetBSize4",
		"sddCharFontSetBSize5",
	}
	fontSetAOutlineFontNames = [starsCharacterSizeCount]string{
		"sddCharOutlineFontSetASize0",
		"sddCharOutlineFontSetASize1",
		"sddCharOutlineFontSetASize2",
		"sddCharOutlineFontSetASize3",
		"sddCharOutlineFontSetASize4",
		"sddCharOutlineFontSetASize5",
	}
	fontSetBOutlineFontNames = [starsCharacterSizeCount]string{
		"sddCharOutlineFontSetBSize0",
		"sddCharOutlineFontSetBSize1",
		"sddCharOutlineFontSetBSize2",
		"sddCharOutlineFontSetBSize3",
		"sddCharOutlineFontSetBSize4",
		"sddCharOutlineFontSetBSize5",
	}
)

// newSystemFont builds all six selectable STARS character sizes into one
// bitmap-font atlas wrapper. Rendering code still addresses a concrete pixel
// size, while CHAR SIZE preferences remain the operator-visible 0-5 values.
func newSystemFont(useFontSetB bool) *renderer.BitmapFont {
	names := fontSetAFontNames
	sizes := fontSetASizes
	if useFontSetB {
		names = fontSetBFontNames
		sizes = fontSetBSizes
	}

	fonts := make(map[int]*renderer.MonoBitmapFont, starsCharacterSizeCount)
	for i := range names {
		font := starsassets.StarsFonts[names[i]]
		if font == nil {
			return nil
		}
		fonts[sizes[i]] = starsFontForRenderer(font)
	}
	return renderer.NewBitmapFontFromMono(fonts)
}

// newSystemOutlineFont is the dark mask used behind STARS position symbols.
// TI 6191.409 Rev. 30 §2.11 explicitly requires position-symbol characters to
// have a dark outline; Appendix B defines that outline as black. The outline
// glyphs are the same PCF-derived masks used by VICE.
func newSystemOutlineFont(useFontSetB bool) *renderer.BitmapFont {
	names := fontSetAOutlineFontNames
	sizes := fontSetASizes
	if useFontSetB {
		names = fontSetBOutlineFontNames
		sizes = fontSetBSizes
	}

	fonts := make(map[int]*renderer.MonoBitmapFont, starsCharacterSizeCount)
	for i := range names {
		font := starsassets.StarsFonts[names[i]]
		if font == nil {
			return nil
		}
		fonts[sizes[i]] = starsFontForRenderer(font)
	}
	return renderer.NewBitmapFontFromMono(fonts)
}

// starsFontForRenderer converts the STARS/VICE bitmap-font Y offsets into the
// top-origin convention expected by REDS' BitmapFont renderer. In the source
// fonts Offset[1] is measured upward from the bottom of the character cell;
// REDS expects the glyph's top offset. Font Set B mostly masks this difference
// because its glyphs occupy complete cells, while Font Set A punctuation does
// not (for example the period would otherwise be drawn near the top).
func starsFontForRenderer(src *renderer.MonoBitmapFont) *renderer.MonoBitmapFont {
	if src == nil {
		return nil
	}

	font := *src
	font.Glyphs = append([]renderer.MonoBitmapGlyph(nil), src.Glyphs...)
	for i := range font.Glyphs {
		glyph := &font.Glyphs[i]
		glyph.Offset[1] = font.Height - glyph.Offset[1] - glyph.Bounds[1]
	}
	return &font
}

func (p *STARSPane) characterFontSize(index int) int {
	index = max(0, min(index, starsCharacterSizeCount-1))
	if p != nil && p.useFontSetB {
		return fontSetBSizes[index]
	}
	return fontSetASizes[index]
}

func (p *STARSPane) dcbFontSize() int {
	if p == nil {
		return fontSetASizes[1]
	}
	return p.characterFontSize(max(0, min(p.currentPrefs().CharSize.DCB, 2)))
}

func (p *STARSPane) listFontSize() int {
	if p == nil {
		return fontSetASizes[1]
	}
	return p.characterFontSize(p.currentPrefs().CharSize.Lists)
}

func (p *STARSPane) datablockFontSize() int {
	if p == nil {
		return fontSetASizes[1]
	}
	return p.characterFontSize(p.currentPrefs().CharSize.Datablocks)
}

func (p *STARSPane) toolsFontSize() int {
	if p == nil {
		return fontSetASizes[1]
	}
	return p.characterFontSize(p.currentPrefs().CharSize.Tools)
}

func (p *STARSPane) positionSymbolFontSize() int {
	if p == nil {
		return fontSetASizes[0]
	}
	return p.characterFontSize(p.currentPrefs().CharSize.PositionSymbols)
}

// UseFontSetB reports the state shown by the STARS-only title-bar menu item.
func (p *STARSPane) UseFontSetB() bool {
	return p != nil && p.useFontSetB
}

// ToggleFontSetB switches between the non-legacy Font Set B and legacy Font
// Set A. Destroy the old atlas before replacing it so repeated toggles do not
// accumulate unused GPU textures.
func (p *STARSPane) ToggleFontSetB(r renderer.Renderer) {
	if p == nil {
		return
	}

	if r != nil {
		for _, texture := range p.systemFontTextures {
			if texture != 0 {
				r.DestroyTexture(texture)
			}
		}
		for _, texture := range p.systemOutlineFontTextures {
			if texture != 0 {
				r.DestroyTexture(texture)
			}
		}
	}
	p.systemFontTextures = nil
	p.systemOutlineFontTextures = nil
	p.useFontSetB = !p.useFontSetB
	p.systemFont = newSystemFont(p.useFontSetB)
	p.systemOutlineFont = newSystemOutlineFont(p.useFontSetB)
}

func (p *STARSPane) systemFontTexture(r renderer.Renderer, size int) renderer.TextureID {
	if p == nil || p.systemFont == nil || r == nil {
		return 0
	}
	if p.systemFontTextures == nil {
		p.systemFontTextures = make(map[int]renderer.TextureID)
	}
	if texture := p.systemFontTextures[size]; texture != 0 {
		return texture
	}

	fs := p.systemFont.Size(size)
	if fs == nil {
		return 0
	}

	texture := r.CreateTextureR8(fs.AtlasWidth, fs.AtlasHeight, fs.AtlasR8, true)
	if texture != 0 {
		p.systemFontTextures[size] = texture
	}
	return texture
}

func (p *STARSPane) systemOutlineFontTexture(r renderer.Renderer, size int) renderer.TextureID {
	if p == nil || p.systemOutlineFont == nil || r == nil {
		return 0
	}
	if p.systemOutlineFontTextures == nil {
		p.systemOutlineFontTextures = make(map[int]renderer.TextureID)
	}
	if texture := p.systemOutlineFontTextures[size]; texture != 0 {
		return texture
	}

	fs := p.systemOutlineFont.Size(size)
	if fs == nil {
		return 0
	}

	texture := r.CreateTextureR8(fs.AtlasWidth, fs.AtlasHeight, fs.AtlasR8, true)
	if texture != 0 {
		p.systemOutlineFontTextures[size] = texture
	}
	return texture
}
