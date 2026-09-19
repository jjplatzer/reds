package stars

import (
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/renderer"
)

// STARS uses a one-button-thick Display Control Bar. VICE models the
// controller display with 72-unit DCB buttons, so the bar itself is 72 units
// thick. The official manual shows the DCB at the top by default and allows it
// to be moved to the other three edges later.
const dcbButtonSize = 72

const zDCBBackground renderer.Z = -900

func (p *STARSPane) mouseOverDCB(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}
	pos := ctx.Mouse.Pos
	return pos.X >= 0 && pos.X < ctx.PaneRect.Width() && pos.Y >= 0 && pos.Y < dcbButtonSize
}

// drawDCBBackground draws only the DCB backing strip. Buttons, bevels, text,
// input handling, and the other DCB presentation details are intentionally not
// part of this first step.
func (p *STARSPane) drawDCBBackground(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil {
		return
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	if width <= 0 || height <= 0 {
		return
	}

	// Match VICE's DCB sizing: 72 display units scaled to framebuffer pixels.
	// DrawPixelScale defaults to the platform DPI scale in panes.NewContext.
	scale := ctx.DrawPixelScale
	if scale <= 0 {
		scale = 1
	}
	dcbHeight := int(float32(dcbButtonSize)*scale + 0.5)
	if dcbHeight > height {
		dcbHeight = height
	}

	cb := zcb.At(zDCBBackground)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y+height-dcbHeight, width, dcbHeight)
	cb.ClearRGB(p.colors.DCBBackground)
	cb.DisableScissor()
}
