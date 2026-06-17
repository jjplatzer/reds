package main

import (
	"runtime"
	"unsafe"

	"github.com/juliusplatzer/reds/util"

	"github.com/AllenDang/cimgui-go/imgui"
)

const (
	menuFontPath           = "fonts/B612-Regular.ttf.zst"
	menuBoldFontPath       = "fonts/B612-Bold.ttf.zst"
	symbolsFontPath        = "fonts/Symbols.ttf.zst"
	shortcutSymbolFontPath = "fonts/SF-Pro.ttf.zst"
)

// fontPinner keeps decompressed font bytes pinned so the GC can't move them
// while ImGui reads them when building the atlas.
var fontPinner runtime.Pinner

var (
	menuFontTTF      []byte
	menuBoldFontTTF  []byte
	menuFontRange    []imgui.Wchar
	symbolsFontTTF   []byte
	symbolsFontRange []imgui.Wchar

	shortcutSymbolFontTTF   []byte
	shortcutSymbolFontRange []imgui.Wchar

	menuBoldFont         *imgui.Font
	symbolsFont12        *imgui.Font
	symbolsFont8         *imgui.Font
	shortcutSymbolFont12 *imgui.Font
)

func loadFont() {
	menuFontTTF = util.LoadResourceBytes(menuFontPath)
	if len(menuFontTTF) == 0 {
		return
	}
	menuBoldFontTTF = util.LoadResourceBytes(menuBoldFontPath)

	fonts := imgui.CurrentIO().Fonts()
	fontPinner.Pin(&menuFontTTF[0])
	menuFontRange = []imgui.Wchar{
		0x0020, 0x00FF,
		0,
	}
	fontPinner.Pin(&menuFontRange[0])

	cfg := imgui.NewFontConfig()
	cfg.SetFontDataOwnedByAtlas(false)
	fonts.AddFontFromMemoryTTFV(
		uintptr(unsafe.Pointer(&menuFontTTF[0])),
		int32(len(menuFontTTF)),
		uiFontSize,
		cfg,
		&menuFontRange[0],
	)
	if len(menuBoldFontTTF) == 0 {
		return
	}

	fontPinner.Pin(&menuBoldFontTTF[0])
	boldCfg := imgui.NewFontConfig()
	boldCfg.SetFontDataOwnedByAtlas(false)
	menuBoldFont = fonts.AddFontFromMemoryTTFV(
		uintptr(unsafe.Pointer(&menuBoldFontTTF[0])),
		int32(len(menuBoldFontTTF)),
		uiFontSize,
		boldCfg,
		&menuFontRange[0],
	)

	symbolsFontTTF = util.LoadResourceBytes(symbolsFontPath)
	if len(symbolsFontTTF) == 0 {
		return
	}

	fontPinner.Pin(&symbolsFontTTF[0])
	symbolsFontRange = []imgui.Wchar{
		0xE000, 0xF8FF,
		0,
	}
	fontPinner.Pin(&symbolsFontRange[0])

	symbolCfg12 := imgui.NewFontConfig()
	symbolCfg12.SetFontDataOwnedByAtlas(false)
	symbolsFont12 = fonts.AddFontFromMemoryTTFV(
		uintptr(unsafe.Pointer(&symbolsFontTTF[0])),
		int32(len(symbolsFontTTF)),
		12,
		symbolCfg12,
		&symbolsFontRange[0],
	)

	symbolCfg8 := imgui.NewFontConfig()
	symbolCfg8.SetFontDataOwnedByAtlas(false)
	symbolsFont8 = fonts.AddFontFromMemoryTTFV(
		uintptr(unsafe.Pointer(&symbolsFontTTF[0])),
		int32(len(symbolsFontTTF)),
		8,
		symbolCfg8,
		&symbolsFontRange[0],
	)

	shortcutSymbolFontTTF = util.LoadResourceBytes(shortcutSymbolFontPath)
	if len(shortcutSymbolFontTTF) == 0 {
		return
	}

	fontPinner.Pin(&shortcutSymbolFontTTF[0])
	shortcutSymbolFontRange = []imgui.Wchar{
		0x21E7, 0x21E7, // ⇧
		0x2303, 0x2303, // ⌃
		0x2318, 0x2318, // ⌘
		0x2325, 0x2325, // ⌥
		0,
	}
	fontPinner.Pin(&shortcutSymbolFontRange[0])

	shortcutSymbolCfg12 := imgui.NewFontConfig()
	shortcutSymbolCfg12.SetFontDataOwnedByAtlas(false)
	shortcutSymbolFont12 = fonts.AddFontFromMemoryTTFV(
		uintptr(unsafe.Pointer(&shortcutSymbolFontTTF[0])),
		int32(len(shortcutSymbolFontTTF)),
		12,
		shortcutSymbolCfg12,
		&shortcutSymbolFontRange[0],
	)
}
