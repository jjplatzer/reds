package asdex

import (
	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/renderer"
)

type DcbPosition int

const (
	DcbTop DcbPosition = iota
	DcbBottom
	DcbLeft
	DcbRight
	DcbOff
)

const (
	dcbButtonSpacing = 3
	dcbColumnCount   = 14
	dcbMinBrightness = 20
)

var (
	dcbBackgroundRGB = renderer.RGB8(56, 56, 56)
	dcbMenuSlabRGB   = renderer.RGB8(100, 100, 100)
)

type Dcb struct {
	visible    bool
	position   DcbPosition
	brightness int
	charSize   int
}

type DcbLayout struct {
	Bounds     redsmath.Rect
	MenuBounds redsmath.Rect

	ButtonSize redsmath.Vec2
	MenuSize   redsmath.Vec2

	AutoSize       int
	RenderFontSize int
}

func NewDcb() Dcb {
	return Dcb{
		visible:    true,
		position:   DcbTop,
		brightness: brightnessDefault,
		charSize:   2,
	}
}

// TODO(DCB): Keep all layout code position-aware. CRC supports TOP, BOTTOM,
// LEFT, and RIGHT DCB positions. Buttons are not implemented yet, but Layout
// already returns correct bar/slab bounds for all positions.
func (p DcbPosition) IsHorizontal() bool {
	return p == DcbTop || p == DcbBottom
}

func (d *Dcb) Visible() bool {
	return d != nil && d.visible && d.position != DcbOff
}

func (d *Dcb) SetPosition(position DcbPosition) {
	if d == nil {
		return
	}
	d.position = position
}

func (d *Dcb) Position() DcbPosition {
	if d == nil {
		return DcbOff
	}
	return d.position
}

func (d *Dcb) buttonSizeForFont(font *renderer.BitmapFont, autoSize int) redsmath.Vec2 {
	if font == nil {
		return redsmath.Vec2{}
	}

	_, charHeight := font.CharSize(autoSize)
	if charHeight <= 0 {
		return redsmath.Vec2{}
	}

	buttonHeight := float32(charHeight*2 + 9)
	return redsmath.Vec2{
		X: buttonHeight * 3,
		Y: buttonHeight,
	}
}

func horizontalDcbMenuSize(button redsmath.Vec2) redsmath.Vec2 {
	return redsmath.Vec2{
		X: (button.X+float32(dcbButtonSpacing))*float32(dcbColumnCount) + float32(dcbButtonSpacing),
		Y: button.Y*2 + 9,
	}
}

func verticalDcbMenuSize(button redsmath.Vec2) redsmath.Vec2 {
	return redsmath.Vec2{
		X: button.X + 6,
		Y: button.Y*float32(dcbColumnCount)*2 + 87,
	}
}

func (d *Dcb) Layout(displaySize redsmath.Vec2, font *renderer.BitmapFont) DcbLayout {
	var out DcbLayout
	if d == nil || !d.Visible() || font == nil || displaySize.X <= 0 || displaySize.Y <= 0 {
		return out
	}

	autoSize := 3
	var buttonSize redsmath.Vec2
	var menuSize redsmath.Vec2
	for autoSize >= 1 {
		buttonSize = d.buttonSizeForFont(font, autoSize)
		if buttonSize.X <= 0 || buttonSize.Y <= 0 {
			return DcbLayout{}
		}

		if d.position.IsHorizontal() {
			menuSize = horizontalDcbMenuSize(buttonSize)
			if autoSize == 1 || displaySize.X >= menuSize.X {
				break
			}
		} else {
			menuSize = verticalDcbMenuSize(buttonSize)
			if autoSize == 1 || displaySize.Y >= menuSize.Y {
				break
			}
		}
		autoSize--
	}

	out.AutoSize = autoSize
	out.RenderFontSize = autoSize
	if d.charSize < out.RenderFontSize {
		out.RenderFontSize = d.charSize
	}
	out.ButtonSize = buttonSize
	out.MenuSize = menuSize

	switch d.position {
	case DcbTop:
		out.Bounds = redsmath.NewRect(0, 0, displaySize.X, menuSize.Y)
		menuX := float32(0)
		if displaySize.X > menuSize.X {
			menuX = (displaySize.X - menuSize.X) * 0.5
		}
		out.MenuBounds = redsmath.NewRect(menuX, 0, menuX+menuSize.X, menuSize.Y)

	case DcbBottom:
		y := displaySize.Y - menuSize.Y
		if y < 0 {
			y = 0
		}
		out.Bounds = redsmath.NewRect(0, y, displaySize.X, y+menuSize.Y)
		menuX := float32(0)
		if displaySize.X > menuSize.X {
			menuX = (displaySize.X - menuSize.X) * 0.5
		}
		out.MenuBounds = redsmath.NewRect(menuX, y, menuX+menuSize.X, y+menuSize.Y)

	case DcbLeft:
		out.Bounds = redsmath.NewRect(0, 0, menuSize.X, displaySize.Y)
		menuY := float32(0)
		if displaySize.Y > menuSize.Y {
			menuY = (displaySize.Y - menuSize.Y) * 0.5
		}
		out.MenuBounds = redsmath.NewRect(0, menuY, menuSize.X, menuY+menuSize.Y)

	case DcbRight:
		x := displaySize.X - menuSize.X
		if x < 0 {
			x = 0
		}
		out.Bounds = redsmath.NewRect(x, 0, x+menuSize.X, displaySize.Y)
		menuY := float32(0)
		if displaySize.Y > menuSize.Y {
			menuY = (displaySize.Y - menuSize.Y) * 0.5
		}
		out.MenuBounds = redsmath.NewRect(x, menuY, x+menuSize.X, menuY+menuSize.Y)
	}

	return out
}

func (d *Dcb) DrawBackground(cb *renderer.CmdBuffer, layout DcbLayout) {
	if d == nil || cb == nil || layout.Bounds.Empty() {
		return
	}

	builder := renderer.GetColoredTrianglesBuilder()
	defer renderer.ReturnColoredTrianglesBuilder(builder)

	background := applyBrightness(dcbBackgroundRGB, d.brightness, dcbMinBrightness)
	menuSlab := applyBrightness(dcbMenuSlabRGB, d.brightness, dcbMinBrightness)

	addDcbRect(builder, layout.Bounds, background)
	if !layout.MenuBounds.Empty() {
		addDcbRect(builder, layout.MenuBounds, menuSlab)
	}

	builder.GenerateCommands(cb)
}

func addDcbRect(builder *renderer.ColoredTrianglesBuilder, rect redsmath.Rect, color renderer.RGB) {
	if builder == nil || rect.Empty() {
		return
	}

	builder.AddQuad(
		renderer.PointVertex{X: rect.Min.X, Y: rect.Min.Y},
		renderer.PointVertex{X: rect.Max.X, Y: rect.Min.Y},
		renderer.PointVertex{X: rect.Max.X, Y: rect.Max.Y},
		renderer.PointVertex{X: rect.Min.X, Y: rect.Max.Y},
		color,
	)
}

func (d *Dcb) Contains(point redsmath.Vec2, displaySize redsmath.Vec2, font *renderer.BitmapFont) bool {
	layout := d.Layout(displaySize, font)
	return !layout.Bounds.Empty() && layout.Bounds.Contains(point)
}
