package eram

import (
	"fmt"
	"log/slog"
	stdmath "math"

	"github.com/juliusplatzer/reds/eram/assets"
	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	// CRC EramDisplaySettings.CursorSize defaults to 1, which selects the
	// first cursor loaded by EramDisplay: Eram1.cur.
	defaultCursorAssetName = "Eram1"
	zMouseCursor           = renderer.Z(1000)
)

type CursorSet struct {
	cursor  *renderer.CursorBitmap
	texture renderer.TextureID

	loaded bool
	err    error
}

func (cs *CursorSet) Load() error {
	if cs == nil {
		return fmt.Errorf("ERAM cursors require a cursor set")
	}
	if cs.loaded {
		return cs.err
	}

	cs.loaded = true
	cs.cursor = assets.EramCursors[defaultCursorAssetName]
	if cs.cursor == nil {
		cs.err = fmt.Errorf("ERAM cursor %s is missing", defaultCursorAssetName)
	}
	return cs.err
}

func (cs *CursorSet) textureForCursor(r renderer.Renderer) renderer.TextureID {
	if cs == nil || cs.cursor == nil || r == nil {
		return 0
	}
	if cs.texture != 0 {
		return cs.texture
	}

	cs.texture = r.CreateTextureRGBA(
		cs.cursor.Width,
		cs.cursor.Height,
		cs.cursor.RGBABytes(),
		true,
	)
	return cs.texture
}

func (p *ERAMPane) ensureCursorLoaded() {
	if p == nil || p.cursors.loaded {
		return
	}
	if err := p.cursors.Load(); err != nil {
		if p.logger != nil {
			p.logger.Warn("Unable to load ERAM cursor", slog.Any("error", err))
		} else {
			slog.Warn("Unable to load ERAM cursor", slog.Any("error", err))
		}
	}
}

func (p *ERAMPane) applyCursor(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Platform == nil {
		return
	}
	if ctx.Mouse == nil {
		ctx.Platform.ClearCursorOverride()
		return
	}

	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	if !paneLocal.Contains(ctx.Mouse.Pos) || p.cursors.cursor == nil {
		ctx.Platform.ClearCursorOverride()
		return
	}

	// The cursor assets are already exact RGBA conversions of CRC's .cur
	// files, so use the same software-cursor path as ASDE-X.
	ctx.Platform.SetCursorHiddenOverride()
}

func (p *ERAMPane) renderCursor(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Mouse == nil || p.cursors.cursor == nil {
		return
	}

	cursor := p.cursors.cursor
	textureID := p.cursors.textureForCursor(ctx.Renderer)
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
