package stars

import (
	"github.com/juliusplatzer/reds/renderer"
	starsassets "github.com/juliusplatzer/reds/stars/assets"
)

const (
	// TI 6191.409 describes position-symbol presentation and the POS character-
	// size control, but does not prescribe size 0 as the startup value. Use size
	// 1 as REDS' default position-symbol size. Keep the actual bitmap dimensions
	// here rather than teaching target rendering about font-set-specific pixels.
	fontSetAPositionSymbolFontSize  = 14
	fontSetAPositionSymbolFontName  = "sddCharFontSetASize1"
	fontSetAPositionOutlineFontName = "sddCharOutlineFontSetASize1"
	fontSetBPositionSymbolFontSize  = 12
	fontSetBPositionSymbolFontName  = "sddCharFontSetBSize1"
	fontSetBPositionOutlineFontName = "sddCharOutlineFontSetBSize1"

	// VICE maps STARS list character size 1 to these renderer sizes. Font Set
	// B is the non-legacy/ARTS face and is REDS' default; Font Set A is the
	// legacy face selected when "Use Font Set B" is disabled.
	fontSetAListFontSize = 14
	fontSetAListFontName = "sddCharFontSetASize1"
	fontSetBListFontSize = 12
	fontSetBListFontName = "sddCharFontSetBSize1"
)

// newSystemFont builds the character size currently used by REDS for both
// position symbols and list/data-entry text. Additional sizes can be added
// lazily when the full STARS character-size controls are implemented.
func newSystemFont(useFontSetB bool) *renderer.BitmapFont {
	positionName, positionSize := fontSetAPositionSymbolFontName, fontSetAPositionSymbolFontSize
	listName, listSize := fontSetAListFontName, fontSetAListFontSize
	if useFontSetB {
		positionName, positionSize = fontSetBPositionSymbolFontName, fontSetBPositionSymbolFontSize
		listName, listSize = fontSetBListFontName, fontSetBListFontSize
	}

	positionFont := starsassets.StarsFonts[positionName]
	listFont := starsassets.StarsFonts[listName]
	if positionFont == nil || listFont == nil {
		return nil
	}
	return renderer.NewBitmapFontFromMono(map[int]*renderer.MonoBitmapFont{
		positionSize: starsFontForRenderer(positionFont),
		listSize:     starsFontForRenderer(listFont),
	})
}

// newSystemOutlineFont is the dark mask used behind STARS position symbols.
// TI 6191.409 Rev. 30 §2.11 explicitly requires position-symbol characters to
// have a dark outline; Appendix B defines that outline as black. The outline
// glyphs are the same PCF-derived masks used by VICE.
func newSystemOutlineFont(useFontSetB bool) *renderer.BitmapFont {
	name, size := fontSetAPositionOutlineFontName, fontSetAPositionSymbolFontSize
	if useFontSetB {
		name, size = fontSetBPositionOutlineFontName, fontSetBPositionSymbolFontSize
	}

	font := starsassets.StarsFonts[name]
	if font == nil {
		return nil
	}
	return renderer.NewBitmapFontFromMono(map[int]*renderer.MonoBitmapFont{
		size: starsFontForRenderer(font),
	})
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

func (p *STARSPane) listFontSize() int {
	if p != nil && p.useFontSetB {
		return fontSetBListFontSize
	}
	return fontSetAListFontSize
}

func (p *STARSPane) positionSymbolFontSize() int {
	if p != nil && p.useFontSetB {
		return fontSetBPositionSymbolFontSize
	}
	return fontSetAPositionSymbolFontSize
}

// datablockFontSize returns STARS character size 1, matching VICE's default
// DATA BLOCKS character-size preference. REDS does not yet expose the full
// datablock character-size control, so keep this in one helper for the later
// CHAR SIZE wiring rather than baking pixel sizes into datablock rendering.
func (p *STARSPane) datablockFontSize() int {
	return p.listFontSize()
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
