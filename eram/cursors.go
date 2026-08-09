package eram

import (
	"fmt"
	"log/slog"
	stdmath "math"
	"time"

	"github.com/juliusplatzer/reds/eram/assets"
	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	// CRC EramDisplaySettings.CursorSize is 1..5 and directly selects
	// Eram1.cur through Eram5.cur. The bitmap itself is not scaled.
	defaultCursorSize = 1
	minCursorSize     = 1
	maxCursorSize     = 5

	invalidCursorDuration = 500 * time.Millisecond
	zMouseCursor          = renderer.Z(1000)
)

type eramCursorType uint8

const (
	eramCursorScope eramCursorType = iota
	eramCursorDeletion
	eramCursorInvalidSelection
)

type eramTransientCursor struct {
	Type  eramCursorType
	Until time.Time
}

type CursorSet struct {
	cursors  [maxCursorSize]*renderer.CursorBitmap
	textures [maxCursorSize]renderer.TextureID

	deletion                *renderer.CursorBitmap
	deletionTexture         renderer.TextureID
	invalidSelection        *renderer.CursorBitmap
	invalidSelectionTexture renderer.TextureID

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
	for size := minCursorSize; size <= maxCursorSize; size++ {
		name := fmt.Sprintf("Eram%d", size)
		cursor := assets.EramCursors[name]
		if cursor == nil {
			cs.err = fmt.Errorf("ERAM cursor %s is missing", name)
			return cs.err
		}
		cs.cursors[size-1] = cursor
	}

	cs.deletion = assets.EramCursors["EramDeletion"]
	if cs.deletion == nil {
		cs.err = fmt.Errorf("ERAM cursor EramDeletion is missing")
		return cs.err
	}
	cs.invalidSelection = assets.EramCursors["EramInvalidSelection"]
	if cs.invalidSelection == nil {
		cs.err = fmt.Errorf("ERAM cursor EramInvalidSelection is missing")
		return cs.err
	}
	return cs.err
}

func (cs *CursorSet) cursorForSize(size int) *renderer.CursorBitmap {
	if cs == nil {
		return nil
	}
	if size < minCursorSize || size > maxCursorSize {
		size = defaultCursorSize
	}
	return cs.cursors[size-1]
}

func (cs *CursorSet) cursorForType(cursorType eramCursorType, size int) *renderer.CursorBitmap {
	if cs == nil {
		return nil
	}
	switch cursorType {
	case eramCursorDeletion:
		return cs.deletion
	case eramCursorInvalidSelection:
		return cs.invalidSelection
	default:
		return cs.cursorForSize(size)
	}
}

func (cs *CursorSet) textureForSize(r renderer.Renderer, size int) renderer.TextureID {
	if cs == nil || r == nil {
		return 0
	}
	if size < minCursorSize || size > maxCursorSize {
		size = defaultCursorSize
	}
	index := size - 1
	cursor := cs.cursors[index]
	if cursor == nil {
		return 0
	}
	if cs.textures[index] != 0 {
		return cs.textures[index]
	}

	cs.textures[index] = r.CreateTextureRGBA(
		cursor.Width,
		cursor.Height,
		cursor.RGBABytes(),
		true,
	)
	return cs.textures[index]
}

func (cs *CursorSet) textureForType(r renderer.Renderer, cursorType eramCursorType, size int) renderer.TextureID {
	if cs == nil || r == nil {
		return 0
	}
	switch cursorType {
	case eramCursorDeletion:
		if cs.deletion == nil {
			return 0
		}
		if cs.deletionTexture == 0 {
			cs.deletionTexture = r.CreateTextureRGBA(cs.deletion.Width, cs.deletion.Height, cs.deletion.RGBABytes(), true)
		}
		return cs.deletionTexture
	case eramCursorInvalidSelection:
		if cs.invalidSelection == nil {
			return 0
		}
		if cs.invalidSelectionTexture == 0 {
			cs.invalidSelectionTexture = r.CreateTextureRGBA(cs.invalidSelection.Width, cs.invalidSelection.Height, cs.invalidSelection.RGBABytes(), true)
		}
		return cs.invalidSelectionTexture
	default:
		return cs.textureForSize(r, size)
	}
}

func (p *ERAMPane) activeCursorType() eramCursorType {
	if p == nil {
		return eramCursorScope
	}
	if p.transientCursor.Type != eramCursorScope && time.Now().Before(p.transientCursor.Until) {
		return p.transientCursor.Type
	}
	if p.toolbar.deletingTearoffs {
		return eramCursorDeletion
	}
	return eramCursorScope
}

func (p *ERAMPane) showInvalidSelectionCursor() {
	if p == nil {
		return
	}
	p.transientCursor = eramTransientCursor{
		Type:  eramCursorInvalidSelection,
		Until: time.Now().Add(invalidCursorDuration),
	}
	if p.audio != nil {
		p.audio.Play(eramAudioError)
	}
}

func (p *ERAMPane) clearTransientCursor() {
	if p != nil {
		p.transientCursor = eramTransientCursor{}
	}
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
	if p.isPanning() {
		ctx.Platform.SetCursorHiddenOverride()
		return
	}
	if ctx.Mouse == nil {
		ctx.Platform.ClearCursorOverride()
		return
	}

	cursorType := p.activeCursorType()
	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	if !paneLocal.Contains(ctx.Mouse.Pos) || p.cursors.cursorForType(cursorType, p.cursorSize) == nil {
		ctx.Platform.ClearCursorOverride()
		return
	}

	// The cursor assets are already exact RGBA conversions of CRC's .cur
	// files, so use the same software-cursor path as ASDE-X.
	ctx.Platform.SetCursorHiddenOverride()
}

func (p *ERAMPane) isPanning() bool {
	return p != nil && p.panDrag != nil && p.panDrag.panning
}

func (p *ERAMPane) renderCursor(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Mouse == nil {
		return
	}

	cursorType := p.activeCursorType()
	cursor := p.cursors.cursorForType(cursorType, p.cursorSize)
	if cursor == nil {
		return
	}

	textureID := p.cursors.textureForType(ctx.Renderer, cursorType, p.cursorSize)
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
