package asdex

import (
	asdexassets "github.com/juliusplatzer/reds/asdex/assets"
	eramassets "github.com/juliusplatzer/reds/eram/assets"
	"github.com/juliusplatzer/reds/renderer"
)

type fontCache struct {
	font     *renderer.BitmapFont
	textures map[int]renderer.TextureID
}

func loadFontCache() (fontCache, error) {
	return newFontCache(renderer.NewBitmapFontFromAlpha(asdexassets.AsdexFonts)), nil
}

func loadEramTextFontCache() (fontCache, error) {
	return newFontCache(renderer.NewBitmapFontFromMono(eramassets.EramTextFonts)), nil
}

func newFontCache(font *renderer.BitmapFont) fontCache {
	return fontCache{
		font:     font,
		textures: make(map[int]renderer.TextureID),
	}
}

func (c *fontCache) textureForSize(r renderer.Renderer, size int) renderer.TextureID {
	if c == nil || c.font == nil || r == nil {
		return 0
	}
	if texture := c.textures[size]; texture != 0 {
		return texture
	}

	fs := c.font.Size(size)
	if fs == nil {
		return 0
	}

	texture := r.CreateTextureR8(fs.AtlasWidth, fs.AtlasHeight, fs.AtlasR8, true)
	if texture != 0 {
		c.textures[size] = texture
	}
	return texture
}
