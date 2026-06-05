package main

import (
	"runtime"
	"unsafe"

	"github.com/juliusplatzer/reds/util"

	"github.com/AllenDang/cimgui-go/imgui"
)

const (
	menuFontPath     = "fonts/B612-Regular.ttf.zst"
	menuBoldFontPath = "fonts/B612-Bold.ttf.zst"
	symbolsFontPath  = "fonts/Symbols.ttf.zst"
)

// fontPinner keeps decompressed font bytes pinned so the GC can't move them
// while ImGui reads them when building the atlas.
var fontPinner runtime.Pinner

var (
	menuFontTTF      []byte
	menuBoldFontTTF  []byte
	symbolsFontTTF   []byte
	symbolsFontRange []imgui.Wchar

	menuBoldFont  *imgui.Font
	symbolsFont12 *imgui.Font
	symbolsFont8  *imgui.Font
)

func loadFont() {
	menuFontTTF = util.LoadResourceBytes(menuFontPath)
	if len(menuFontTTF) == 0 {
		return
	}
	menuBoldFontTTF = util.LoadResourceBytes(menuBoldFontPath)

	fonts := imgui.CurrentIO().Fonts()
	fontPinner.Pin(&menuFontTTF[0])
	cfg := imgui.NewFontConfig()
	cfg.SetFontDataOwnedByAtlas(false)
	fonts.AddFontFromMemoryTTFV(
		uintptr(unsafe.Pointer(&menuFontTTF[0])),
		int32(len(menuFontTTF)),
		uiFontSize,
		cfg,
		nil,
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
		nil,
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
}
