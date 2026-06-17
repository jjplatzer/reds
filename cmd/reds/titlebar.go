package main

import (
	"fmt"
	"runtime"

	"github.com/juliusplatzer/reds/platform"

	"github.com/AllenDang/cimgui-go/imgui"
)

const (
	scopeTitleBarHeight = 24

	titleBarMenuButtonWidth  = 28
	titleBarButtonWidth      = 36
	titleBarMenuSymbolSize   = 12
	titleBarButtonSymbolSize = 8
)

const (
	titleBarMenuPopupWidth       = 300
	titleBarMenuItemHeight       = 30
	titleBarMenuTextPadX         = 13
	titleBarMenuShortcutPadRight = 14
	titleBarMenuPopupRounding    = 6
	titleBarMenuPopupBorderSize  = 1
)

const (
	titleBarGlyphMenu     = "\ue700"
	titleBarGlyphMinimize = "\ue949"
	titleBarGlyphMaximize = "\ue15b"
	titleBarGlyphRestore  = "\ue158"
	titleBarGlyphClose    = "\ue106"
)

const (
	shortcutCommandSymbol = "\u2318"
	shortcutControlSymbol = "\u2303"
	shortcutOptionSymbol  = "\u2325"
	shortcutShiftSymbol   = "\u21e7"
)

var (
	titleBarBg         = imgui.Vec4{X: 20.0 / 255.0, Y: 20.0 / 255.0, Z: 20.0 / 255.0, W: 1}
	titleBarFg         = imgui.Vec4{X: 1, Y: 1, Z: 1, W: 1}
	titleBarHover      = imgui.Vec4{X: 37.0 / 255.0, Y: 37.0 / 255.0, Z: 37.0 / 255.0, W: 1}
	titleBarCloseHover = imgui.Vec4{X: 232.0 / 255.0, Y: 17.0 / 255.0, Z: 35.0 / 255.0, W: 1}

	titleBarMenuBg         = imgui.Vec4{X: 61.0 / 255.0, Y: 61.0 / 255.0, Z: 76.0 / 255.0, W: 1}
	titleBarMenuBorder     = imgui.Vec4{X: 74.0 / 255.0, Y: 74.0 / 255.0, Z: 94.0 / 255.0, W: 1}
	titleBarMenuHover      = imgui.Vec4{X: 76.0 / 255.0, Y: 79.0 / 255.0, Z: 98.0 / 255.0, W: 1}
	titleBarMenuFg         = imgui.Vec4{X: 240.0 / 255.0, Y: 240.0 / 255.0, Z: 240.0 / 255.0, W: 1}
	titleBarMenuShortcutFg = imgui.Vec4{
		X: 190.0 / 255.0,
		Y: 190.0 / 255.0,
		Z: 200.0 / 255.0,
		W: 1,
	}
)

type titleBarAction int

const (
	titleBarActionNone titleBarAction = iota
	titleBarActionSwitchFacility
)

type titleBarButtonType int

const (
	titleBarButtonMinimize titleBarButtonType = iota
	titleBarButtonMaximize
	titleBarButtonClose
)

type titleBarShortcutPart struct {
	Text     string
	Font     *imgui.Font
	FontSize float32
}

func drawScopeTitleBar(
	plat platform.Platform,
	title string,
	displaySize [2]float32,
) (bool, titleBarAction) {
	if plat == nil || displaySize[0] <= 0 {
		return false, titleBarActionNone
	}

	imgui.SetNextWindowPosV(imgui.Vec2{X: 0, Y: 0}, imgui.CondAlways, imgui.Vec2{})
	imgui.SetNextWindowSizeV(
		imgui.Vec2{X: displaySize[0], Y: scopeTitleBarHeight},
		imgui.CondAlways,
	)

	flags := imgui.WindowFlagsNoTitleBar |
		imgui.WindowFlagsNoResize |
		imgui.WindowFlagsNoMove |
		imgui.WindowFlagsNoScrollbar |
		imgui.WindowFlagsNoSavedSettings |
		imgui.WindowFlagsNoBringToFrontOnFocus

	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{})
	imgui.PushStyleVarVec2(imgui.StyleVarItemSpacing, imgui.Vec2{})
	imgui.PushStyleVarVec2(imgui.StyleVarWindowMinSize, imgui.Vec2{})
	imgui.PushStyleVarFloat(imgui.StyleVarWindowRounding, 0)
	imgui.PushStyleVarFloat(imgui.StyleVarWindowBorderSize, 0)
	imgui.PushStyleColorVec4(imgui.ColWindowBg, titleBarBg)

	imgui.BeginV("##scope-titlebar", nil, flags)

	drawTitleBarBackground(displaySize[0])
	menuCaptured, menuAction := drawTitleBarMenuButton()
	if shortcutAction := titleBarShortcutAction(plat); shortcutAction != titleBarActionNone {
		menuAction = shortcutAction
	}
	drawTitleBarTitle(title)
	capturedButtons := drawTitleBarButtons(plat, displaySize[0])
	capturedDrag := handleTitleBarDrag(plat, displaySize[0])

	imgui.End()

	imgui.PopStyleColor()
	imgui.PopStyleVarV(5)

	return menuCaptured || capturedButtons || capturedDrag, menuAction
}

func drawTitleBarBackground(width float32) {
	imgui.WindowDrawList().AddRectFilledV(
		imgui.Vec2{X: 0, Y: 0},
		imgui.Vec2{X: width, Y: scopeTitleBarHeight},
		imgui.ColorU32Vec4(titleBarBg),
		0,
		imgui.DrawFlagsNone,
	)
}

func drawTitleBarTitle(title string) {
	const titleMarginLeft = 6

	textY := (float32(scopeTitleBarHeight) - imgui.FontSize()) * 0.5
	if textY < 0 {
		textY = 0
	}

	imgui.WindowDrawList().AddTextVec2(
		imgui.Vec2{X: titleBarMenuButtonWidth + titleMarginLeft, Y: textY},
		imgui.ColorU32Vec4(titleBarFg),
		title,
	)
}

func drawTitleBarMenuButton() (bool, titleBarAction) {
	min := imgui.Vec2{X: 0, Y: 0}
	max := imgui.Vec2{X: titleBarMenuButtonWidth, Y: scopeTitleBarHeight}

	imgui.SetCursorScreenPos(min)
	clicked := imgui.InvisibleButtonV(
		"##titlebar-menu-button",
		imgui.Vec2{X: max.X - min.X, Y: max.Y - min.Y},
		imgui.ButtonFlagsMouseButtonLeft,
	)

	hovered := imgui.IsItemHovered()
	if hovered {
		imgui.WindowDrawList().AddRectFilledV(
			min,
			max,
			imgui.ColorU32Vec4(titleBarHover),
			0,
			imgui.DrawFlagsNone,
		)
	}

	drawBurgerIcon(min, max)

	if clicked {
		imgui.OpenPopupStrV("##titlebar-menu-popup", imgui.PopupFlagsNone)
	}

	action := drawTitleBarMenuPopup(min, max)
	captured := hovered ||
		imgui.IsItemActive() ||
		imgui.IsPopupOpenStr("##titlebar-menu-popup")
	return captured, action
}

func drawBurgerIcon(min, max imgui.Vec2) {
	drawTitleBarSymbolGlyph(
		titleBarGlyphMenu,
		symbolsFont12,
		titleBarMenuSymbolSize,
		min,
		max,
		titleBarFg,
	)
}

func drawTitleBarMenuPopup(buttonMin, buttonMax imgui.Vec2) titleBarAction {
	imgui.SetNextWindowPosV(
		imgui.Vec2{X: buttonMin.X, Y: buttonMax.Y},
		imgui.CondAlways,
		imgui.Vec2{},
	)
	imgui.SetNextWindowSizeV(
		imgui.Vec2{X: titleBarMenuPopupWidth, Y: titleBarMenuItemHeight},
		imgui.CondAlways,
	)

	flags := imgui.WindowFlagsNoTitleBar |
		imgui.WindowFlagsNoResize |
		imgui.WindowFlagsNoMove |
		imgui.WindowFlagsNoSavedSettings |
		imgui.WindowFlagsNoScrollbar

	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{})
	imgui.PushStyleVarVec2(imgui.StyleVarItemSpacing, imgui.Vec2{})
	imgui.PushStyleVarFloat(imgui.StyleVarPopupRounding, titleBarMenuPopupRounding)
	imgui.PushStyleVarFloat(imgui.StyleVarPopupBorderSize, titleBarMenuPopupBorderSize)
	imgui.PushStyleColorVec4(imgui.ColPopupBg, titleBarMenuBg)
	imgui.PushStyleColorVec4(imgui.ColBorder, titleBarMenuBorder)

	action := titleBarActionNone
	if imgui.BeginPopupV("##titlebar-menu-popup", flags) {
		imgui.SetCursorPos(imgui.Vec2{X: 0, Y: 0})
		clicked := imgui.InvisibleButtonV(
			"##switch-facility-menu-item",
			imgui.Vec2{X: titleBarMenuPopupWidth, Y: titleBarMenuItemHeight},
			imgui.ButtonFlagsMouseButtonLeft,
		)

		rowMin := imgui.ItemRectMin()
		rowMax := imgui.ItemRectMax()
		if imgui.IsItemHovered() {
			imgui.WindowDrawList().AddRectFilledV(
				rowMin,
				rowMax,
				imgui.ColorU32Vec4(titleBarMenuHover),
				titleBarMenuPopupRounding,
				imgui.DrawFlagsRoundCornersAll,
			)
		}

		drawTitleBarMenuItemText(
			rowMin,
			titleBarMenuItemHeight,
			"Switch Facility...",
			titleBarSwitchFacilityShortcutParts(),
		)

		if clicked {
			action = titleBarActionSwitchFacility
			imgui.CloseCurrentPopup()
		}

		imgui.EndPopup()
	}

	imgui.PopStyleColorV(2)
	imgui.PopStyleVarV(4)
	return action
}

func drawTitleBarMenuItemText(
	rowMin imgui.Vec2,
	itemHeight float32,
	label string,
	shortcut []titleBarShortcutPart,
) {
	textY := rowMin.Y + (itemHeight-imgui.FontSize())*0.5
	if textY < rowMin.Y {
		textY = rowMin.Y
	}

	imgui.WindowDrawList().AddTextVec2(
		imgui.Vec2{X: rowMin.X + titleBarMenuTextPadX, Y: textY},
		imgui.ColorU32Vec4(titleBarMenuFg),
		label,
	)

	drawTitleBarShortcutParts(rowMin, itemHeight, shortcut)
}

func titleBarSwitchFacilityShortcutParts() []titleBarShortcutPart {
	if runtime.GOOS == "darwin" {
		if shortcutSymbolFont12 != nil {
			return []titleBarShortcutPart{
				{Text: shortcutCommandSymbol, Font: shortcutSymbolFont12, FontSize: 12},
				{Text: "+"},
				{Text: shortcutShiftSymbol, Font: shortcutSymbolFont12, FontSize: 12},
				{Text: "+F"},
			}
		}

		return []titleBarShortcutPart{{Text: "Cmd+Shift+F"}}
	}

	if shortcutSymbolFont12 != nil {
		return []titleBarShortcutPart{
			{Text: shortcutControlSymbol, Font: shortcutSymbolFont12, FontSize: 12},
			{Text: "+"},
			{Text: shortcutShiftSymbol, Font: shortcutSymbolFont12, FontSize: 12},
			{Text: "+F"},
		}
	}

	return []titleBarShortcutPart{{Text: "Ctrl+Shift+F"}}
}

func titleBarShortcutPartsWidth(parts []titleBarShortcutPart) float32 {
	var width float32

	for _, part := range parts {
		if part.Text == "" {
			continue
		}

		if part.Font != nil {
			size := part.FontSize
			if size <= 0 {
				size = imgui.FontSize()
			}

			imgui.PushFont(part.Font, size)
			partSize := imgui.CalcTextSize(part.Text)
			imgui.PopFont()
			width += partSize.X
			continue
		}

		width += imgui.CalcTextSize(part.Text).X
	}

	return width
}

func drawTitleBarShortcutParts(
	rowMin imgui.Vec2,
	itemHeight float32,
	parts []titleBarShortcutPart,
) {
	if len(parts) == 0 {
		return
	}

	x := rowMin.X + titleBarMenuPopupWidth -
		titleBarMenuShortcutPadRight -
		titleBarShortcutPartsWidth(parts)

	for _, part := range parts {
		if part.Text == "" {
			continue
		}

		if part.Font != nil {
			size := part.FontSize
			if size <= 0 {
				size = imgui.FontSize()
			}

			imgui.PushFont(part.Font, size)
			textSize := imgui.CalcTextSize(part.Text)
			y := rowMin.Y + (itemHeight-textSize.Y)*0.5
			if y < rowMin.Y {
				y = rowMin.Y
			}
			imgui.WindowDrawList().AddTextVec2(
				imgui.Vec2{X: x, Y: y},
				imgui.ColorU32Vec4(titleBarMenuShortcutFg),
				part.Text,
			)
			imgui.PopFont()
			x += textSize.X
			continue
		}

		textY := rowMin.Y + (itemHeight-imgui.FontSize())*0.5
		if textY < rowMin.Y {
			textY = rowMin.Y
		}
		imgui.WindowDrawList().AddTextVec2(
			imgui.Vec2{X: x, Y: textY},
			imgui.ColorU32Vec4(titleBarMenuShortcutFg),
			part.Text,
		)
		x += imgui.CalcTextSize(part.Text).X
	}
}

func titleBarShortcutAction(plat platform.Platform) titleBarAction {
	if plat == nil {
		return titleBarActionNone
	}

	keyboard := plat.GetKeyboard()
	if !keyboard.WasPressed(platform.KeyF) ||
		!keyboard.IsDown(platform.KeyShift) {
		return titleBarActionNone
	}

	if runtime.GOOS == "darwin" {
		if keyboard.IsDown(platform.KeyCommand) {
			return titleBarActionSwitchFacility
		}
		return titleBarActionNone
	}

	if keyboard.IsDown(platform.KeyControl) {
		return titleBarActionSwitchFacility
	}

	return titleBarActionNone
}

func drawTitleBarButtons(plat platform.Platform, windowWidth float32) bool {
	captured := false
	captured = drawTitleBarButton(plat, titleBarButtonMinimize, windowWidth) || captured
	captured = drawTitleBarButton(plat, titleBarButtonMaximize, windowWidth) || captured
	captured = drawTitleBarButton(plat, titleBarButtonClose, windowWidth) || captured
	return captured
}

func titleBarButtonRect(buttonType titleBarButtonType, windowWidth float32) (imgui.Vec2, imgui.Vec2) {
	min := imgui.Vec2{Y: 0}
	max := imgui.Vec2{Y: scopeTitleBarHeight}

	switch buttonType {
	case titleBarButtonClose:
		min.X = windowWidth - titleBarButtonWidth
		max.X = windowWidth
	case titleBarButtonMaximize:
		min.X = windowWidth - 2*titleBarButtonWidth
		max.X = windowWidth - titleBarButtonWidth
	default:
		min.X = windowWidth - 3*titleBarButtonWidth
		max.X = windowWidth - 2*titleBarButtonWidth
	}

	return min, max
}

func drawTitleBarButton(
	plat platform.Platform,
	buttonType titleBarButtonType,
	windowWidth float32,
) bool {
	min, max := titleBarButtonRect(buttonType, windowWidth)

	imgui.SetCursorScreenPos(min)
	clicked := imgui.InvisibleButtonV(
		fmt.Sprintf("##titlebar-button-%d", buttonType),
		imgui.Vec2{X: max.X - min.X, Y: max.Y - min.Y},
		imgui.ButtonFlagsMouseButtonLeft,
	)

	hovered := imgui.IsItemHovered()
	if hovered {
		hoverColor := titleBarHover
		if buttonType == titleBarButtonClose {
			hoverColor = titleBarCloseHover
		}
		imgui.WindowDrawList().AddRectFilledV(
			min,
			max,
			imgui.ColorU32Vec4(hoverColor),
			0,
			imgui.DrawFlagsNone,
		)
	}

	drawTitleBarIcon(plat, buttonType, min, max)

	if clicked {
		switch buttonType {
		case titleBarButtonMinimize:
			plat.MinimizeWindow()
		case titleBarButtonMaximize:
			plat.ToggleMaximizeWindow()
		case titleBarButtonClose:
			plat.CloseWindow()
		}
	}

	return hovered || imgui.IsItemActive()
}

func drawTitleBarIcon(
	plat platform.Platform,
	buttonType titleBarButtonType,
	min, max imgui.Vec2,
) {
	glyph := titleBarGlyphMinimize

	switch buttonType {
	case titleBarButtonMinimize:
		glyph = titleBarGlyphMinimize

	case titleBarButtonMaximize:
		if plat != nil && plat.IsWindowMaximized() {
			glyph = titleBarGlyphRestore
		} else {
			glyph = titleBarGlyphMaximize
		}

	case titleBarButtonClose:
		glyph = titleBarGlyphClose
	}

	drawTitleBarSymbolGlyph(
		glyph,
		symbolsFont8,
		titleBarButtonSymbolSize,
		min,
		max,
		titleBarFg,
	)
}

func drawTitleBarSymbolGlyph(
	glyph string,
	font *imgui.Font,
	fontSize float32,
	min, max imgui.Vec2,
	color imgui.Vec4,
) {
	if glyph == "" || font == nil {
		return
	}

	imgui.PushFont(font, fontSize)
	defer imgui.PopFont()

	textSize := imgui.CalcTextSize(glyph)
	pos := imgui.Vec2{
		X: min.X + (max.X-min.X-textSize.X)*0.5,
		Y: min.Y + (max.Y-min.Y-textSize.Y)*0.5,
	}

	imgui.WindowDrawList().AddTextVec2(
		pos,
		imgui.ColorU32Vec4(color),
		glyph,
	)
}

func handleTitleBarDrag(plat platform.Platform, windowWidth float32) bool {
	dragStartX := float32(titleBarMenuButtonWidth)
	dragWidth := windowWidth - dragStartX - 3*titleBarButtonWidth
	if plat == nil || dragWidth <= 0 {
		return false
	}

	imgui.SetCursorScreenPos(imgui.Vec2{X: dragStartX, Y: 0})
	imgui.InvisibleButtonV(
		"##titlebar-drag-region",
		imgui.Vec2{X: dragWidth, Y: scopeTitleBarHeight},
		imgui.ButtonFlagsMouseButtonLeft,
	)

	hovered := imgui.IsItemHovered()
	active := imgui.IsItemActive()

	if hovered && imgui.IsMouseDoubleClicked(imgui.MouseButtonLeft) {
		plat.ToggleMaximizeWindow()
		return true
	}

	if active && imgui.IsMouseDragging(imgui.MouseButtonLeft) {
		if !plat.IsWindowMaximized() {
			delta := imgui.CurrentIO().MouseDelta()
			plat.MoveWindowBy(delta.X, delta.Y)
		}
		return true
	}

	return hovered || active
}
