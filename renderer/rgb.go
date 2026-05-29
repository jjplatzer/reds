// Package renderer provides an API-agnostic command-buffer renderer and an
// OpenGL backend used by the REDS scopes.
package renderer

// RGB stores a non-premultiplied RGB color with components in [0, 1].
type RGB struct {
	R, G, B float32
}

// RGBA stores a non-premultiplied RGBA color with components in [0, 1].
type RGBA struct {
	R, G, B, A float32
}

// ToRGBA converts an RGB color to opaque RGBA.
func (c RGB) ToRGBA() RGBA {
	return RGBA{R: c.R, G: c.G, B: c.B, A: 1}
}

// RGB8 returns an RGB color from 8-bit components.
func RGB8(r, g, b uint8) RGB {
	const inv = 1.0 / 255.0
	return RGB{R: float32(r) * inv, G: float32(g) * inv, B: float32(b) * inv}
}

// RGBA8 returns an RGBA color from 8-bit components.
func RGBA8(r, g, b, a uint8) RGBA {
	const inv = 1.0 / 255.0
	return RGBA{R: float32(r) * inv, G: float32(g) * inv, B: float32(b) * inv, A: float32(a) * inv}
}

// RGBHex returns an RGB color from a 0xRRGGBB integer.
func RGBHex(x uint32) RGB {
	return RGB8(uint8(x>>16), uint8(x>>8), uint8(x))
}

// RGBAHex returns an opaque RGBA color from a 0xRRGGBB integer.
func RGBAHex(x uint32) RGBA {
	return RGBHex(x).ToRGBA()
}
