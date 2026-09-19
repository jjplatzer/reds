package stars

import (
	"github.com/juliusplatzer/reds/renderer"
	starsassets "github.com/juliusplatzer/reds/stars/assets"
)

const (
	// VICE maps STARS' non-legacy Font Set B list character size 1 to
	// sddCharFontSetBSize1. The source bitmap has a 10x12 character cell, so
	// 12 is the renderer size used for the default SSA/list text.
	defaultListFontSize = 12
	defaultListFontName = "sddCharFontSetBSize1"
)

// newDefaultSystemFont builds only the Font Set B size currently needed by
// the initial SSA implementation. Additional STARS character sizes can be
// added lazily as their controls are implemented instead of allocating atlas
// storage for unused sizes now.
func newDefaultSystemFont() *renderer.BitmapFont {
	font := starsassets.StarsFonts[defaultListFontName]
	if font == nil {
		return nil
	}
	return renderer.NewBitmapFontFromMono(map[int]*renderer.MonoBitmapFont{
		defaultListFontSize: font,
	})
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
