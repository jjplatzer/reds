package stars

import (
	"github.com/juliusplatzer/reds/renderer"
	starsassets "github.com/juliusplatzer/reds/stars/assets"
)

const (
	// VICE maps STARS list character size 1 to these renderer sizes. Font Set
	// B is the non-legacy/ARTS face and is REDS' default; Font Set A is the
	// legacy face selected when "Use Font Set B" is disabled.
	fontSetAListFontSize = 14
	fontSetAListFontName = "sddCharFontSetASize1"
	fontSetBListFontSize = 12
	fontSetBListFontName = "sddCharFontSetBSize1"
)

// newSystemFont builds only the currently selected list character size. More
// sizes can be added lazily when the STARS character-size controls are added.
func newSystemFont(useFontSetB bool) *renderer.BitmapFont {
	name, size := fontSetAListFontName, fontSetAListFontSize
	if useFontSetB {
		name, size = fontSetBListFontName, fontSetBListFontSize
	}

	font := starsassets.StarsFonts[name]
	if font == nil {
		return nil
	}
	return renderer.NewBitmapFontFromMono(map[int]*renderer.MonoBitmapFont{
		size: font,
	})
}

func (p *STARSPane) listFontSize() int {
	if p != nil && p.useFontSetB {
		return fontSetBListFontSize
	}
	return fontSetAListFontSize
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
	}
	p.systemFontTextures = nil
	p.useFontSetB = !p.useFontSetB
	p.systemFont = newSystemFont(p.useFontSetB)
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
