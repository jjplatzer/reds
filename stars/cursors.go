package stars

import (
	stdmath "math"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/stars/assets"
)

const (
	// VICE defaults STARS datablock character size to 1 and uses the matching
	// keyboard_1_1 crosshair glyph for the normal scope cursor.
	defaultScopeCursorAsset = "keyboard_1_1"
	zMouseCursor            = renderer.Z(1000)
)

// applyCursor selects whether the platform cursor or the STARS scope cursor
// should be visible. VICE leaves the normal OS arrow over the DCB and uses the
// STARS crosshair everywhere else inside the scope.
func (p *STARSPane) applyCursor(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Platform == nil {
		return
	}
	if ctx.Mouse == nil {
		ctx.Platform.ClearCursorOverride()
		return
	}

	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	if !paneLocal.Contains(ctx.Mouse.Pos) || p.mouseOverDCB(ctx) {
		ctx.Platform.ClearCursorOverride()
		return
	}

	if cursor := p.scopeCursor(); cursor != nil {
		ctx.Platform.SetCursorHiddenOverride()
		return
	}
	ctx.Platform.ClearCursorOverride()
}

func (p *STARSPane) scopeCursor() *renderer.CursorBitmap {
	if p == nil {
		return nil
	}
	cursor, _ := assets.StarsCursor(defaultScopeCursorAsset)
	return cursor
}

func (p *STARSPane) scopeCursorTexture(r renderer.Renderer) renderer.TextureID {
	if p == nil || r == nil {
		return 0
	}
	if p.cursorTexture != 0 {
		return p.cursorTexture
	}

	cursor := p.scopeCursor()
	if cursor == nil {
		return 0
	}
	p.cursorTexture = r.CreateTextureRGBA(cursor.Width, cursor.Height, cursor.RGBABytes(), true)
	return p.cursorTexture
}

func (p *STARSPane) renderCursor(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Mouse == nil || p.mouseOverDCB(ctx) {
		return
	}

	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	if !paneLocal.Contains(ctx.Mouse.Pos) {
		return
	}

	cursor := p.scopeCursor()
	if cursor == nil {
		return
	}
	textureID := p.scopeCursorTexture(ctx.Renderer)
	if textureID == 0 {
		return
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zMouseCursor)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.SetRGBA(renderer.RGBA{R: 1, G: 1, B: 1, A: 1})

	mouse := ctx.Mouse.Pos
	left := float32(stdmath.Floor(float64(mouse.X - float32(cursor.Hotspot[0]))))
	top := float32(stdmath.Floor(float64(mouse.Y - float32(cursor.Hotspot[1]))))
	right := left + float32(cursor.Width)
	bottom := top + float32(cursor.Height)

	builder := renderer.GetTexturedTrianglesBuilder()
	builder.AddQuad(
		renderer.PointVertex{X: left, Y: top},
		renderer.PointVertex{X: 0, Y: 0},
		renderer.PointVertex{X: right, Y: top},
		renderer.PointVertex{X: 1, Y: 0},
		renderer.PointVertex{X: right, Y: bottom},
		renderer.PointVertex{X: 1, Y: 1},
		renderer.PointVertex{X: left, Y: bottom},
		renderer.PointVertex{X: 0, Y: 1},
	)
	builder.GenerateCommands(cb, textureID)
	renderer.ReturnTexturedTrianglesBuilder(builder)
	cb.DisableScissor()
}
