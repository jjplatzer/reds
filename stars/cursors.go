package stars

import (
	stdmath "math"
	"strconv"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/stars/assets"
)

const (
	zMouseCursor   = renderer.Z(1000)
	dcbCursorScale = float32(1)
)

// applyCursor selects whether the platform cursor or a STARS cursor should be
// visible. The scope uses the STARS keyboard cursor; the DCB uses the Solaris
// OPEN LOOK basic pointer from the original cursor font.
func (p *STARSPane) applyCursor(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Platform == nil {
		return
	}
	if ctx.Mouse == nil {
		ctx.Platform.ClearCursorOverride()
		return
	}

	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	if !paneLocal.Contains(ctx.Mouse.Pos) {
		ctx.Platform.ClearCursorOverride()
		return
	}

	if cursor, _ := p.activeCursor(ctx); cursor != nil {
		ctx.Platform.SetCursorHiddenOverride()
		return
	}
	ctx.Platform.ClearCursorOverride()
}

func (p *STARSPane) scopeCursor() *renderer.CursorBitmap {
	if p == nil {
		return nil
	}
	// TI 6191.409 Rev. 30, 4.9.1 includes the cursor in the DATA BLOCKS
	// character-size group. VICE caps its STARS crosshair at size 4 even when
	// DATA BLOCKS is set to 5; mirror that presentation exactly.
	size := max(0, min(p.currentPrefs().CharSize.Datablocks, 4))
	cursor, _ := assets.StarsCursor("keyboard_1_" + strconv.Itoa(size))
	return cursor
}

// activeCursor returns the cursor under the mouse and its presentation scale.
// Keep the authentic OPEN LOOK DCB cursor at its native 18x18 source+mask
// size so it can be compared directly with the 2x presentation.
func (p *STARSPane) activeCursor(ctx *panes.Context) (*renderer.CursorBitmap, float32) {
	if p == nil || ctx == nil {
		return nil, 1
	}
	if p.mouseOverDCB(ctx) {
		cursor, _ := assets.StarsCursor("dcb_cursor_0")
		return cursor, dcbCursorScale
	}
	return p.scopeCursor(), 1
}

func (p *STARSPane) cursorTextureFor(r renderer.Renderer, cursor *renderer.CursorBitmap) renderer.TextureID {
	if p == nil || r == nil || cursor == nil {
		return 0
	}
	assetName := cursor.Name
	if p.cursorTexture != 0 && p.cursorTextureAsset == assetName {
		return p.cursorTexture
	}
	if p.cursorTexture != 0 {
		r.DestroyTexture(p.cursorTexture)
		p.cursorTexture = 0
	}

	p.cursorTexture = r.CreateTextureRGBA(cursor.Width, cursor.Height, cursor.RGBABytes(), true)
	if p.cursorTexture != 0 {
		p.cursorTextureAsset = assetName
	} else {
		p.cursorTextureAsset = ""
	}
	return p.cursorTexture
}

func (p *STARSPane) renderCursor(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Mouse == nil {
		return
	}

	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	if !paneLocal.Contains(ctx.Mouse.Pos) {
		return
	}

	cursor, scale := p.activeCursor(ctx)
	if cursor == nil {
		return
	}
	textureID := p.cursorTextureFor(ctx.Renderer, cursor)
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
	hotspotX := float32(cursor.Hotspot[0]) * scale
	hotspotY := float32(cursor.Hotspot[1]) * scale
	left := float32(stdmath.Floor(float64(mouse.X - hotspotX)))
	top := float32(stdmath.Floor(float64(mouse.Y - hotspotY)))
	right := left + float32(cursor.Width)*scale
	bottom := top + float32(cursor.Height)*scale

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
