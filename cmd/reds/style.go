// cmd/reds/style.go
//
// Translation of ui/menu.css into Dear ImGui style state, plus the custom
// dropdown widget that reproduces the Qt QComboBox look (rounded frame, thin
// chevron, state-driven background, bounded 5-item popup) and the styled
// Confirm/Cancel buttons.
//
// ImGui is immediate-mode and has no stylesheet, so "the CSS stays the same"
// means: set the equivalent colors/metrics as scoped Push*/Pop* state each
// frame, and hand-draw what the built-in widgets can't reproduce.

package main

import (
	"strconv"

	"github.com/AllenDang/cimgui-go/imgui"
)

// rgb converts 0..255 sRGB components to an ImGui Vec4 (0..1, opaque).
func rgb(r, g, b uint8) imgui.Vec4 {
	return imgui.Vec4{X: float32(r) / 255, Y: float32(g) / 255, Z: float32(b) / 255, W: 1}
}

// Palette, lifted directly from menu.css.
var (
	colDialogBg     = rgb(55, 57, 68)    // QDialog background
	colLabel        = rgb(220, 220, 220) // QLabel text
	colComboBg      = rgb(33, 35, 41)    // QComboBox / past / current
	colComboBgFut   = rgb(104, 104, 112) // QComboBox[state="future"]
	colBorder       = rgb(159, 160, 162) // 1px borders + chevron stroke (#9fa0a2)
	colBorderFocus  = rgb(200, 200, 205) // QPushButton:default border
	colText         = rgb(230, 230, 230) // enabled combo / button text
	colTextDisabled = rgb(160, 160, 160) // QComboBox:disabled text
	colPopupBg      = rgb(33, 35, 41)    // QComboBox QAbstractItemView
	colSelection    = rgb(70, 72, 82)    // selection-background-color
	colButtonHover  = rgb(45, 48, 56)    // QPushButton:hover
)

// dropState mirrors the Qt "state" dynamic property. past/current render the
// black frame; future renders the gray frame. The extra states exist so more
// time-keyed dropdowns can be added the way the original design intended.
type dropState int

const (
	statePast dropState = iota
	stateCurrent
	stateFuture
)

func (s dropState) frameColor() imgui.Vec4 {
	if s == stateFuture {
		return colComboBgFut
	}
	return colComboBg
}

const (
	frameRounding   = 5
	controlHeight   = 30
	controlPaddingX = 10
	popupGap        = 2
	popupTextInset  = 10
	buttonHeight    = 30
	buttonPaddingX  = 14
	minButtonWidth  = 74
	popupItemHeight = 24 // QComboBox QAbstractItemView::item min-height
	maxVisibleItems = 5  // kDropdownMaxVisible
)

// dropdown draws a QComboBox-styled selector. id must be unique and is hidden
// from the label (the visible label is drawn separately, like the Qt QLabel).
// Returns true if the selection changed this frame.
func dropdown(id string, st dropState, items []string, selected *int, enabled bool) bool {
	frameBg := st.frameColor()
	framePaddingY := (controlHeight - imgui.FontSize()) / 2
	if framePaddingY < 0 {
		framePaddingY = 0
	}

	imgui.PushStyleVarFloat(imgui.StyleVarFrameRounding, frameRounding)
	imgui.PushStyleVarFloat(imgui.StyleVarFrameBorderSize, 1)
	imgui.PushStyleVarVec2(imgui.StyleVarFramePadding, imgui.Vec2{X: controlPaddingX, Y: framePaddingY})

	imgui.PushStyleColorVec4(imgui.ColFrameBg, frameBg)
	imgui.PushStyleColorVec4(imgui.ColFrameBgHovered, frameBg)
	imgui.PushStyleColorVec4(imgui.ColFrameBgActive, frameBg)
	imgui.PushStyleColorVec4(imgui.ColBorder, colBorder)
	imgui.PushStyleColorVec4(imgui.ColPopupBg, colPopupBg)
	imgui.PushStyleColorVec4(imgui.ColHeader, colSelection)
	imgui.PushStyleColorVec4(imgui.ColHeaderHovered, colSelection)
	imgui.PushStyleColorVec4(imgui.ColHeaderActive, colSelection)
	if enabled {
		imgui.PushStyleColorVec4(imgui.ColText, colText)
	} else {
		imgui.PushStyleColorVec4(imgui.ColText, colTextDisabled)
	}

	if !enabled {
		imgui.BeginDisabled()
	}

	preview := ""
	if *selected >= 0 && *selected < len(items) {
		preview = items[*selected]
	}

	changed := false
	width := imgui.ContentRegionAvail().X
	popupID := id + "Popup"

	if imgui.InvisibleButtonV(id, imgui.Vec2{X: width, Y: controlHeight}, imgui.ButtonFlagsMouseButtonLeft) && enabled {
		imgui.OpenPopupStrV(popupID, imgui.PopupFlagsNone)
	}
	rmin, rmax := imgui.ItemRectMin(), imgui.ItemRectMax()

	dl := imgui.WindowDrawList()
	dl.AddRectFilledV(rmin, rmax, imgui.ColorU32Vec4(frameBg), frameRounding, imgui.DrawFlagsRoundCornersAll)
	dl.AddRectV(rmin, rmax, imgui.ColorU32Vec4(colBorder), frameRounding, imgui.DrawFlagsRoundCornersAll, 1)
	textCol := colText
	if !enabled {
		textCol = colTextDisabled
	}
	textY := rmin.Y + (controlHeight-imgui.FontSize())/2
	dl.AddTextVec2(imgui.Vec2{X: rmin.X + controlPaddingX, Y: textY}, imgui.ColorU32Vec4(textCol), preview)
	drawChevron(rmin, rmax, enabled)

	itemH := float32(popupItemHeight)
	visibleItems := len(items)
	if visibleItems > maxVisibleItems {
		visibleItems = maxVisibleItems
	}
	if visibleItems < 1 {
		visibleItems = 1
	}
	popupH := itemH * float32(visibleItems)
	imgui.SetNextWindowPosV(imgui.Vec2{X: rmin.X, Y: rmax.Y + popupGap}, imgui.CondAlways, imgui.Vec2{})
	imgui.SetNextWindowSizeV(imgui.Vec2{X: width, Y: popupH}, imgui.CondAlways)
	popupFlags := imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize |
		imgui.WindowFlagsNoMove | imgui.WindowFlagsNoSavedSettings
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{})
	imgui.PushStyleVarVec2(imgui.StyleVarItemSpacing, imgui.Vec2{})
	imgui.PushStyleVarVec2(imgui.StyleVarSelectableTextAlign, imgui.Vec2{X: 0, Y: 0.5})
	imgui.PushStyleVarFloat(imgui.StyleVarPopupRounding, frameRounding)
	imgui.PushStyleVarFloat(imgui.StyleVarPopupBorderSize, 1)
	if imgui.BeginPopupV(popupID, popupFlags) {
		for i, it := range items {
			rowID := it + "##" + id + strconv.Itoa(i)
			imgui.SetCursorPosX(0)
			clicked := imgui.InvisibleButtonV(rowID, imgui.Vec2{X: width, Y: itemH}, imgui.ButtonFlagsMouseButtonLeft)
			rowMin, rowMax := imgui.ItemRectMin(), imgui.ItemRectMax()
			if imgui.IsItemHovered() {
				imgui.WindowDrawList().AddRectFilled(rowMin, rowMax, imgui.ColorU32Vec4(colSelection))
			}
			textY := rowMin.Y + (itemH-imgui.FontSize())/2
			imgui.WindowDrawList().AddTextVec2(
				imgui.Vec2{X: rowMin.X + popupTextInset, Y: textY},
				imgui.ColorU32Vec4(colText),
				it,
			)
			if clicked {
				if i != *selected {
					*selected = i
					changed = true
				}
				imgui.CloseCurrentPopup()
			}
		}
		imgui.EndPopup()
	}
	imgui.PopStyleVarV(5)

	if !enabled {
		imgui.EndDisabled()
	}

	imgui.PopStyleColorV(9)
	imgui.PopStyleVarV(3)

	return changed
}

// drawChevron renders the same path as chevron.svg:
// <path d="M1 1 L5 5 L9 1"/> in a 10x6 viewBox.
func drawChevron(rmin, rmax imgui.Vec2, enabled bool) {
	const (
		viewBoxW       = 10
		viewBoxH       = 6
		strokeWidth    = 1.4
		arrowZoneWidth = 26
	)

	cx := rmax.X - arrowZoneWidth/2
	cy := (rmin.Y + rmax.Y) / 2
	x0 := cx - viewBoxW/2
	y0 := cy - viewBoxH/2

	stroke := colBorder
	if !enabled {
		stroke = colTextDisabled
	}
	col := imgui.ColorU32Vec4(stroke)

	p1 := imgui.Vec2{X: x0 + 1, Y: y0 + 1}
	pm := imgui.Vec2{X: x0 + 5, Y: y0 + 5}
	p2 := imgui.Vec2{X: x0 + 9, Y: y0 + 1}

	dl := imgui.WindowDrawList()
	if chevronIcon != nil {
		dl.AddImageV(
			*chevronIcon.ref,
			imgui.Vec2{X: x0, Y: y0},
			imgui.Vec2{X: x0 + viewBoxW, Y: y0 + viewBoxH},
			imgui.Vec2{},
			imgui.Vec2{X: 1, Y: 1},
			col,
		)
		return
	}

	dl.AddLineV(p1, pm, col, strokeWidth)
	dl.AddLineV(pm, p2, col, strokeWidth)
}

// label draws a QLabel-styled caption above a control.
func label(text string) {
	imgui.PushStyleColorVec4(imgui.ColText, colLabel)
	if menuBoldFont != nil {
		imgui.PushFont(menuBoldFont, uiFontSize)
	}
	imgui.Text(text)
	if menuBoldFont != nil {
		imgui.PopFont()
	}
	imgui.PopStyleColor()
}

// button draws a QPushButton-styled button. When isDefault is true it takes
// the lighter focus border (QPushButton:default) and initial keyboard focus.
func button(text string, isDefault bool) bool {
	framePaddingY := (buttonHeight - imgui.FontSize()) / 2
	if framePaddingY < 0 {
		framePaddingY = 0
	}

	imgui.PushStyleVarFloat(imgui.StyleVarFrameRounding, frameRounding)
	imgui.PushStyleVarFloat(imgui.StyleVarFrameBorderSize, 1)
	imgui.PushStyleVarVec2(imgui.StyleVarFramePadding, imgui.Vec2{X: buttonPaddingX, Y: framePaddingY})

	imgui.PushStyleColorVec4(imgui.ColButton, colComboBg)
	imgui.PushStyleColorVec4(imgui.ColButtonHovered, colButtonHover)
	imgui.PushStyleColorVec4(imgui.ColButtonActive, colButtonHover)
	imgui.PushStyleColorVec4(imgui.ColText, colText)
	if isDefault {
		imgui.PushStyleColorVec4(imgui.ColBorder, colBorderFocus)
	} else {
		imgui.PushStyleColorVec4(imgui.ColBorder, colBorder)
	}

	clicked := imgui.ButtonV(text, imgui.Vec2{X: buttonWidth(text), Y: buttonHeight})
	if isDefault {
		imgui.SetItemDefaultFocus()
	}

	imgui.PopStyleColorV(5)
	imgui.PopStyleVarV(3)
	return clicked
}

// buttonWidth estimates a button's width for right-alignment.
func buttonWidth(text string) float32 {
	return maxf(minButtonWidth, imgui.CalcTextSize(text).X+2*buttonPaddingX)
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
