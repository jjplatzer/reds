package asdex

import (
	"fmt"
	"strings"

	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/util"
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

type cursorFile struct {
	Type CursorType
	Path string
	Name string
}

var cursorFiles = []cursorFile{
	{CursorScope, "asdex/assets/Asdex.cur.zst", "Asdex.cur"},
	{CursorDcb, "asdex/assets/AsdexDcb.cur.zst", "AsdexDcb.cur"},
	{CursorCaptured, "asdex/assets/AsdexCaptured.cur.zst", "AsdexCaptured.cur"},
	{CursorSelect, "asdex/assets/AsdexSelect.cur.zst", "AsdexSelect.cur"},
	{CursorMove, "asdex/assets/AsdexMove.cur.zst", "AsdexMove.cur"},
	{CursorUpDown, "asdex/assets/AsdexUpDown.cur.zst", "AsdexUpDown.cur"},
	{CursorLeftRight, "asdex/assets/AsdexLeftRight.cur.zst", "AsdexLeftRight.cur"},
}

type CursorSet struct {
	cursors map[CursorType]*platform.Cursor

	loaded bool
	err    error
}

func (cs *CursorSet) Load(plat platform.Platform) error {
	if cs == nil || plat == nil {
		return fmt.Errorf("ASDE-X cursors require a platform")
	}
	if cs.loaded {
		return cs.err
	}

	cs.loaded = true
	cs.cursors = make(map[CursorType]*platform.Cursor)

	var loadErrors []string
	for _, file := range cursorFiles {
		if !util.ResourceExists(file.Path) {
			loadErrors = append(loadErrors, file.Path+": missing")
			continue
		}

		cursor, err := plat.LoadCursorFromBytes(file.Name, util.LoadResourceBytes(file.Path))
		if err != nil {
			loadErrors = append(loadErrors, err.Error())
			continue
		}
		cs.cursors[file.Type] = cursor
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

func (cs *CursorSet) Cursor(cursorType CursorType) *platform.Cursor {
	if cs == nil {
		return nil
	}
	return cs.cursors[cursorType]
}

func (cs *CursorSet) CursorForMode(mode CursorMode) (*platform.Cursor, bool) {
	switch mode {
	case CursorModeDcb:
		return cs.first(CursorDcb, CursorScope), false
	case CursorModeCaptured:
		return cs.first(CursorCaptured, CursorDcb, CursorScope), false
	case CursorModeSelect:
		return cs.first(CursorSelect, CursorScope), false
	case CursorModeMove:
		return cs.first(CursorMove, CursorScope), false
	case CursorModeUpDown:
		return cs.first(CursorUpDown, CursorScope), false
	case CursorModeLeftRight:
		return cs.first(CursorLeftRight, CursorScope), false
	case CursorModeHidden:
		return nil, true
	default:
		return cs.first(CursorScope), false
	}
}

func (cs *CursorSet) first(types ...CursorType) *platform.Cursor {
	for _, cursorType := range types {
		if cursor := cs.Cursor(cursorType); cursor != nil {
			return cursor
		}
	}
	return nil
}
