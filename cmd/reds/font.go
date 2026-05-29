package main

import (
	"runtime"
	"unsafe"

	"github.com/juliusplatzer/reds/util"

	"github.com/AllenDang/cimgui-go/imgui"
)

const (
	menuFontPath     = "fonts/ClearSans-Regular.ttf.zst"
	menuBoldFontPath = "fonts/ClearSans-Bold.ttf.zst"
)

// fontPinner keeps decompressed font bytes pinned so the GC can't move them
// while ImGui reads them when building the atlas.
var fontPinner runtime.Pinner

var (
	menuFontTTF     []byte
	menuBoldFontTTF []byte
	menuBoldFont    *imgui.Font
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
	menuBoldFont = fonts.AddFontFromMemoryTTFV(
		uintptr(unsafe.Pointer(&menuBoldFontTTF[0])),
		int32(len(menuBoldFontTTF)),
		uiFontSize,
		boldCfg,
		nil,
	)
}
