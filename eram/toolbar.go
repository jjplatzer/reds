package eram

import (
	"github.com/juliusplatzer/reds/eram/assets"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	toolbarTextYPadding      = 3
	toolbarButtonBorderWidth = 1
	toolbarButtonRowGap      = 2
	toolbarWrapperYPadding   = 3
	toolbarInteriorLineWidth = 1
)

func toolbarHeight(lineHeight int) int {
	buttonHeight :=
		2*(lineHeight+2*toolbarTextYPadding) +
			2*toolbarButtonBorderWidth

	return 2*buttonHeight +
		toolbarButtonRowGap +
		2*toolbarWrapperYPadding +
		toolbarInteriorLineWidth
}

func (p *ERAMPane) toolbarLineHeight() int {
	if p != nil {
		if font, ok := assets.EramTextFont(p.toolbarFontSize); ok && font != nil && font.Height > 0 {
			return font.Height
		}
	}

	if font, ok := assets.EramTextFont(defaultToolbarFontSize); ok && font != nil && font.Height > 0 {
		return font.Height
	}

	return 9
}

func (p *ERAMPane) drawToolbarBackground(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || !p.toolbarVisible {
		return
	}

	paneWidth := ctx.PaneRect.Width()
	if paneWidth <= 0 || ctx.PaneRect.Height() <= 0 {
		return
	}

	height := float32(toolbarHeight(p.toolbarLineHeight()))

	x, y, width, fbHeight := ctx.PaneFramebufferRect()
	cb := zcb.At(zLoweredMasterToolbar)
	cb.Viewport(x, y, width, fbHeight)
	cb.Scissor(x, y, width, fbHeight)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.DisableBlend()
	cb.SetRGB(applyERAMBrightness(
		toolbarGray,
		p.toolbarBrightness,
		p.systemBrightness,
	))
	cb.DrawTriangles(
		[]renderer.PointVertex{
			{X: 0, Y: 0},
			{X: paneWidth, Y: 0},
			{X: paneWidth, Y: height},
			{X: 0, Y: height},
		},
		[]uint32{0, 1, 2, 0, 2, 3},
		renderer.DrawSolid,
		0,
	)
	cb.Blend()
	cb.DisableScissor()
}
