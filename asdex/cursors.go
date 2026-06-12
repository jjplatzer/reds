package asdex

import (
	"fmt"
	"strings"

	"github.com/juliusplatzer/reds/asdex/assets"
	"github.com/juliusplatzer/reds/renderer"
)

type CursorType int

const (
	CursorScope CursorType = iota
	CursorDcb
	CursorCaptured
	CursorSelect
	CursorMove
	CursorUpDown
	CursorLeftRight
)

type CursorMode int

const (
	CursorModeScope CursorMode = iota
	CursorModeDcb
	CursorModeCaptured
	CursorModeSelect
	CursorModeMove
	CursorModeUpDown
	CursorModeLeftRight
	CursorModeHidden
)

var cursorAssetNames = map[CursorType]string{
	CursorScope:     "Asdex",
	CursorDcb:       "AsdexDcb",
	CursorCaptured:  "AsdexCaptured",
	CursorSelect:    "AsdexSelect",
	CursorMove:      "AsdexMove",
	CursorUpDown:    "AsdexUpDown",
	CursorLeftRight: "AsdexLeftRight",
}

type CursorSet struct {
	cursors  map[CursorType]*renderer.CursorBitmap
	textures map[CursorType]renderer.TextureID

	loaded bool
	err    error
}

func (cs *CursorSet) Load() error {
	if cs == nil {
		return fmt.Errorf("ASDE-X cursors require a cursor set")
	}
	if cs.loaded {
		return cs.err
	}

	cs.loaded = true
	cs.cursors = make(map[CursorType]*renderer.CursorBitmap, len(cursorAssetNames))
	cs.textures = make(map[CursorType]renderer.TextureID)

	var loadErrors []string
	for cursorType, name := range cursorAssetNames {
		cursor := assets.AsdexCursors[name]
		if cursor == nil {
			loadErrors = append(loadErrors, name+": missing")
			continue
		}
		cs.cursors[cursorType] = cursor
	}

	if len(cs.cursors) == 0 {
		cs.err = fmt.Errorf("no ASDE-X cursors loaded: %s", strings.Join(loadErrors, "; "))
		return cs.err
	}
	if len(loadErrors) > 0 {
		cs.err = fmt.Errorf("load ASDE-X cursors: %s", strings.Join(loadErrors, "; "))
	}
	return cs.err
}

func (cs *CursorSet) Has(cursorType CursorType) bool {
	return cs != nil && cs.cursors[cursorType] != nil
}

func (cs *CursorSet) Cursor(cursorType CursorType) *renderer.CursorBitmap {
	if cs == nil {
		return nil
	}
	return cs.cursors[cursorType]
}

func (cs *CursorSet) CursorTypeForMode(mode CursorMode) (CursorType, bool) {
	switch mode {
	case CursorModeDcb:
		return cs.firstType(CursorDcb, CursorScope)
	case CursorModeCaptured:
		return cs.firstType(CursorCaptured, CursorDcb, CursorScope)
	case CursorModeSelect:
		return cs.firstType(CursorSelect, CursorScope)
	case CursorModeMove:
		return cs.firstType(CursorMove, CursorScope)
	case CursorModeUpDown:
		return cs.firstType(CursorUpDown, CursorScope)
	case CursorModeLeftRight:
		return cs.firstType(CursorLeftRight, CursorScope)
	case CursorModeHidden:
		return CursorScope, false
	default:
		return cs.firstType(CursorScope)
	}
}

func (cs *CursorSet) textureForCursor(r renderer.Renderer, cursorType CursorType) renderer.TextureID {
	if cs == nil || r == nil {
		return 0
	}
	if texture := cs.textures[cursorType]; texture != 0 {
		return texture
	}

	cursor := cs.Cursor(cursorType)
	if cursor == nil {
		return 0
	}

	texture := r.CreateTextureRGBA(cursor.Width, cursor.Height, cursor.RGBABytes(), true)
	if texture != 0 {
		cs.textures[cursorType] = texture
	}
	return texture
}

func (cs *CursorSet) firstType(types ...CursorType) (CursorType, bool) {
	for _, cursorType := range types {
		if cs.Has(cursorType) {
			return cursorType, true
		}
	}
	return CursorScope, false
}
