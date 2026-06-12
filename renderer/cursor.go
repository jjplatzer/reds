package renderer

type CursorBitmap struct {
	Name    string
	Width   int
	Height  int
	Hotspot [2]int
	Pixels  []uint32 // row-major 0xRRGGBBAA
}

func (c *CursorBitmap) RGBABytes() []byte {
	if c == nil || c.Width <= 0 || c.Height <= 0 {
		return nil
	}

	out := make([]byte, 0, c.Width*c.Height*4)
	for _, px := range c.Pixels {
		out = append(out,
			byte(px>>24),
			byte(px>>16),
			byte(px>>8),
			byte(px),
		)
	}
	return out
}
