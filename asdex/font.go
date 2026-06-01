package asdex

import (
	"fmt"

	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util"
)

const asdexFontPath = "asdex/assets/font.bin"

type fontCache struct {
	font     *renderer.BitmapFont
	textures map[int]renderer.TextureID
}

func loadFontCache() (fontCache, error) {
	if !util.ResourceExists(asdexFontPath) {
		return fontCache{}, fmt.Errorf("ASDE-X font resource %s not found", asdexFontPath)
	}

	font, err := renderer.LoadBitmapFontBytes(util.LoadResourceBytes(asdexFontPath))
	if err != nil {
		return fontCache{}, fmt.Errorf("load ASDE-X font: %w", err)
	}
	return newFontCache(font), nil
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
