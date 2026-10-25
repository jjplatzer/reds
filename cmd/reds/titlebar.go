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
	titleBarMenuPopupWidth         = 300
	titleBarStarsMenuPopupWidth    = 330
	titleBarMenuItemHeight         = 30
	titleBarMenuTextPadX           = 13
	titleBarMenuCheckedTextPadX    = 40
	titleBarMenuCheckGlyphOffsetX  = 8
	titleBarMenuCheckGlyphBoxWidth = 20
	titleBarMenuShortcutPadRight   = 14
	titleBarMenuPopupRounding      = 6
	titleBarMenuPopupBorderSize    = 1
	titleBarMenuSeparatorHeight    = 9 // 4 px padding + 1 px line + 4 px padding
	titleBarMenuSeparatorInsetX    = 12
	titleBarMenuSeparatorWidth     = 1
)

const (
	titleBarGlyphMenu     = "\ue700"
	titleBarGlyphCheck    = "\ue73e"
	titleBarGlyphMinimize = "\ue949"
	titleBarGlyphMaximize = "\ue15b"
	titleBarGlyphRestore  = "\ue158"
	titleBarGlyphClose    = "\ue106"
)

const (
	shortcutCommandSymbol = "\u2318"
	shortcutOptionSymbol  = "\u2325"
	shortcutControlSymbol = "\u2303"
	shortcutShiftSymbol   = "\u21e7"
)

var (
	titleBarBg         = imgui.Vec4{X: 20.0 / 255.0, Y: 20.0 / 255.0, Z: 20.0 / 255.0, W: 1}
	titleBarFg         = imgui.Vec4{X: 1, Y: 1, Z: 1, W: 1}
	titleBarHover      = imgui.Vec4{X: 37.0 / 255.0, Y: 37.0 / 255.0, Z: 37.0 / 255.0, W: 1}
	titleBarCloseHover = imgui.Vec4{X: 232.0 / 255.0, Y: 17.0 / 255.0, Z: 35.0 / 255.0, W: 1}

	titleBarMenuBg     = imgui.Vec4{X: 61.0 / 255.0, Y: 61.0 / 255.0, Z: 76.0 / 255.0, W: 1}
	titleBarMenuBorder = imgui.Vec4{X: 74.0 / 255.0, Y: 74.0 / 255.0, Z: 94.0 / 255.0, W: 1}
	titleBarMenuHover  = imgui.Vec4{X: 76.0 / 255.0, Y: 79.0 / 255.0, Z: 98.0 / 255.0, W: 1}
	// CRC uses Avalonia Fluent's SystemBaseMediumLowColor (#66FFFFFF) for separators.
	titleBarMenuSeparator  = imgui.Vec4{X: 1, Y: 1, Z: 1, W: 102.0 / 255.0}
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
	titleBarActionAddNewWindow
	titleBarActionSwitchFacility
	titleBarActionDefineTemporaryArea
	titleBarActionViewNOTAMs
	titleBarActionViewIAPChart
	titleBarActionToggleStarsAssignedAltitudes
	titleBarActionToggleStarsTAISLeaderDirection
	titleBarActionToggleStarsFontSetB
	titleBarActionToggleStarsFAAHFSTD010APalette
)

type titleBarMenuOptions struct {
	ShowASDEXSeparator           bool
	ShowStarsIAPChart            bool
	ShowStarsAssignedAltitudes   bool
	HideStarsAssignedAltitudes   bool
	ShowStarsTAISLeaderDirection bool
	UseStarsTAISLeaderDirection  bool
	ShowStarsFontSetB            bool
	UseStarsFontSetB             bool
	ShowStarsFAAHFSTD010APalette bool
	UseStarsFAAHFSTD010APalette  bool
}

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
	menuOptions titleBarMenuOptions,
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
	menuCaptured, menuAction := drawTitleBarMenuButton(menuOptions)
	if shortcutAction := titleBarShortcutAction(plat, menuOptions); shortcutAction != titleBarActionNone {
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

func drawTitleBarMenuButton(menuOptions titleBarMenuOptions) (bool, titleBarAction) {
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

	action := drawTitleBarMenuPopup(min, max, menuOptions)
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

func drawTitleBarMenuPopup(
	buttonMin, buttonMax imgui.Vec2,
	menuOptions titleBarMenuOptions,
) titleBarAction {
	imgui.SetNextWindowPosV(
		imgui.Vec2{X: buttonMin.X, Y: buttonMax.Y},
		imgui.CondAlways,
		imgui.Vec2{},
	)
	menuHeight := float32(2 * titleBarMenuItemHeight)
	if menuOptions.ShowASDEXSeparator {
		// The separator and two ASDE-X-only menu rows.
		menuHeight += titleBarMenuSeparatorHeight + 2*titleBarMenuItemHeight
	}
	popupWidth := float32(titleBarMenuPopupWidth)
	if menuOptions.ShowStarsIAPChart {
		menuHeight += float32(titleBarMenuSeparatorHeight + titleBarMenuItemHeight)
	}
	if menuOptions.ShowStarsAssignedAltitudes {
		menuHeight += float32(titleBarMenuItemHeight)
	}
	if menuOptions.ShowStarsTAISLeaderDirection {
		menuHeight += float32(titleBarMenuItemHeight)
	}
	if menuOptions.ShowStarsFontSetB {
		menuHeight += float32(titleBarMenuItemHeight)
	}
	if menuOptions.ShowStarsFAAHFSTD010APalette {
		menuHeight += float32(titleBarMenuItemHeight)
	}
	if menuOptions.ShowStarsIAPChart || menuOptions.ShowStarsAssignedAltitudes || menuOptions.ShowStarsTAISLeaderDirection || menuOptions.ShowStarsFontSetB || menuOptions.ShowStarsFAAHFSTD010APalette {
		popupWidth = titleBarStarsMenuPopupWidth
	}
	imgui.SetNextWindowSizeV(
		imgui.Vec2{X: popupWidth, Y: menuHeight},
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
		// Right-align each complete shortcut; modifier glyphs need not share a left edge.
		shortcutRightX := popupWidth - titleBarMenuShortcutPadRight
		textPadX := float32(titleBarMenuTextPadX)
		if menuOptions.ShowStarsIAPChart || menuOptions.ShowStarsAssignedAltitudes || menuOptions.ShowStarsTAISLeaderDirection || menuOptions.ShowStarsFontSetB || menuOptions.ShowStarsFAAHFSTD010APalette {
			// Reserve the same check gutter as the STARS checked rows so all
			// labels start on the same vertical axis.
			textPadX = titleBarMenuCheckedTextPadX
		}

		imgui.SetCursorPos(imgui.Vec2{X: 0, Y: 0})
		newWindowClicked := imgui.InvisibleButtonV(
			"##add-new-window-menu-item",
			imgui.Vec2{X: popupWidth, Y: titleBarMenuItemHeight},
			imgui.ButtonFlagsMouseButtonLeft,
		)

		newWindowRowMin := imgui.ItemRectMin()
		newWindowRowMax := imgui.ItemRectMax()
		if imgui.IsItemHovered() {
			imgui.WindowDrawList().AddRectFilledV(
				newWindowRowMin,
				newWindowRowMax,
				imgui.ColorU32Vec4(titleBarMenuHover),
				titleBarMenuPopupRounding,
				imgui.DrawFlagsRoundCornersAll,
			)
		}

		drawTitleBarMenuItemTextWithPad(
			newWindowRowMin,
			titleBarMenuItemHeight,
			shortcutRightX,
			textPadX,
			"New Window...",
			titleBarAddNewWindowShortcutParts(),
		)

		if newWindowClicked {
			action = titleBarActionAddNewWindow
			imgui.CloseCurrentPopup()
		}

		rowY := float32(titleBarMenuItemHeight)
		imgui.SetCursorPos(imgui.Vec2{X: 0, Y: rowY})
		switchFacilityClicked := imgui.InvisibleButtonV(
			"##switch-facility-menu-item",
			imgui.Vec2{X: popupWidth, Y: titleBarMenuItemHeight},
			imgui.ButtonFlagsMouseButtonLeft,
		)

		switchFacilityRowMin := imgui.ItemRectMin()
		switchFacilityRowMax := imgui.ItemRectMax()
		if imgui.IsItemHovered() {
			imgui.WindowDrawList().AddRectFilledV(
				switchFacilityRowMin,
				switchFacilityRowMax,
				imgui.ColorU32Vec4(titleBarMenuHover),
				titleBarMenuPopupRounding,
				imgui.DrawFlagsRoundCornersAll,
			)
		}

		drawTitleBarMenuItemTextWithPad(
			switchFacilityRowMin,
			titleBarMenuItemHeight,
			shortcutRightX,
			textPadX,
			"Switch Facility...",
			titleBarSwitchFacilityShortcutParts(),
		)

		if switchFacilityClicked {
			action = titleBarActionSwitchFacility
			imgui.CloseCurrentPopup()
		}

		rowY += float32(titleBarMenuItemHeight)
		if menuOptions.ShowASDEXSeparator || menuOptions.ShowStarsIAPChart {
			// Match the existing ASDE-X separator before display-specific actions.
			lineY := switchFacilityRowMax.Y + float32(titleBarMenuSeparatorHeight)*0.5
			imgui.WindowDrawList().AddLineV(
				imgui.Vec2{X: switchFacilityRowMin.X + titleBarMenuSeparatorInsetX, Y: lineY},
				imgui.Vec2{X: switchFacilityRowMax.X - titleBarMenuSeparatorInsetX, Y: lineY},
				imgui.ColorU32Vec4(titleBarMenuSeparator),
				titleBarMenuSeparatorWidth,
			)
			rowY += titleBarMenuSeparatorHeight
		}
		if menuOptions.ShowASDEXSeparator {
			imgui.SetCursorPos(imgui.Vec2{X: 0, Y: rowY})
			defineTemporaryAreaClicked := imgui.InvisibleButtonV(
				"##asdex-define-temporary-area-menu-item",
				imgui.Vec2{X: popupWidth, Y: titleBarMenuItemHeight},
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

			drawTitleBarMenuItemTextWithPad(
				rowMin,
				titleBarMenuItemHeight,
				shortcutRightX,
				textPadX,
				"Define Temporary Area...",
				titleBarDefineTemporaryAreaShortcutParts(),
			)

			if defineTemporaryAreaClicked {
				action = titleBarActionDefineTemporaryArea
				imgui.CloseCurrentPopup()
			}
			rowY += float32(titleBarMenuItemHeight)

			imgui.SetCursorPos(imgui.Vec2{X: 0, Y: rowY})
			viewNOTAMsClicked := imgui.InvisibleButtonV(
				"##asdex-view-notams-menu-item",
				imgui.Vec2{X: popupWidth, Y: titleBarMenuItemHeight},
				imgui.ButtonFlagsMouseButtonLeft,
			)

			rowMin = imgui.ItemRectMin()
			rowMax = imgui.ItemRectMax()
			if imgui.IsItemHovered() {
				imgui.WindowDrawList().AddRectFilledV(
					rowMin,
					rowMax,
					imgui.ColorU32Vec4(titleBarMenuHover),
					titleBarMenuPopupRounding,
					imgui.DrawFlagsRoundCornersAll,
				)
			}

			drawTitleBarMenuItemTextWithPad(
				rowMin,
				titleBarMenuItemHeight,
				shortcutRightX,
				textPadX,
				"View NOTAMs...",
				titleBarViewNOTAMsShortcutParts(),
			)

			if viewNOTAMsClicked {
				action = titleBarActionViewNOTAMs
				imgui.CloseCurrentPopup()
			}
			rowY += float32(titleBarMenuItemHeight)
		}

		if menuOptions.ShowStarsIAPChart {
			imgui.SetCursorPos(imgui.Vec2{X: 0, Y: rowY})
			clicked := imgui.InvisibleButtonV(
				"##stars-view-iap-chart-menu-item",
				imgui.Vec2{X: popupWidth, Y: titleBarMenuItemHeight},
				imgui.ButtonFlagsMouseButtonLeft,
			)
			rowMin := imgui.ItemRectMin()
			rowMax := imgui.ItemRectMax()
			if imgui.IsItemHovered() {
				imgui.WindowDrawList().AddRectFilledV(
					rowMin, rowMax, imgui.ColorU32Vec4(titleBarMenuHover),
					titleBarMenuPopupRounding, imgui.DrawFlagsRoundCornersAll,
				)
			}
			drawTitleBarMenuItemTextWithPad(
				rowMin, titleBarMenuItemHeight, shortcutRightX,
				titleBarMenuCheckedTextPadX,
				"View IAP Chart", titleBarViewIAPChartShortcutParts(),
			)
			if clicked {
				action = titleBarActionViewIAPChart
				imgui.CloseCurrentPopup()
			}
			rowY += float32(titleBarMenuItemHeight)
		}

		if menuOptions.ShowStarsAssignedAltitudes {
			imgui.SetCursorPos(imgui.Vec2{X: 0, Y: rowY})
			hideAssignedAltitudeClicked := imgui.InvisibleButtonV(
				"##stars-hide-assigned-altitudes-menu-item",
				imgui.Vec2{X: popupWidth, Y: titleBarMenuItemHeight},
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

			if menuOptions.HideStarsAssignedAltitudes {
				drawTitleBarMenuCheck(rowMin, titleBarMenuItemHeight)
			}
			drawTitleBarMenuItemTextWithPad(
				rowMin,
				titleBarMenuItemHeight,
				shortcutRightX,
				titleBarMenuCheckedTextPadX,
				"Hide Assigned Altitudes",
				titleBarHideAssignedAltitudesShortcutParts(),
			)

			if hideAssignedAltitudeClicked {
				action = titleBarActionToggleStarsAssignedAltitudes
			}
			rowY += float32(titleBarMenuItemHeight)
		}

		if menuOptions.ShowStarsTAISLeaderDirection {
			imgui.SetCursorPos(imgui.Vec2{X: 0, Y: rowY})
			clicked := imgui.InvisibleButtonV(
				"##stars-use-tais-leader-direction-menu-item",
				imgui.Vec2{X: popupWidth, Y: titleBarMenuItemHeight},
				imgui.ButtonFlagsMouseButtonLeft,
			)
			rowMin := imgui.ItemRectMin()
			rowMax := imgui.ItemRectMax()
			if imgui.IsItemHovered() {
				imgui.WindowDrawList().AddRectFilledV(
					rowMin, rowMax, imgui.ColorU32Vec4(titleBarMenuHover),
					titleBarMenuPopupRounding, imgui.DrawFlagsRoundCornersAll,
				)
			}
			if menuOptions.UseStarsTAISLeaderDirection {
				drawTitleBarMenuCheck(rowMin, titleBarMenuItemHeight)
			}
			drawTitleBarMenuItemTextWithPad(
				rowMin, titleBarMenuItemHeight, shortcutRightX,
				titleBarMenuCheckedTextPadX,
				"Use TAIS-derived LDR DIR", titleBarTAISLeaderDirectionShortcutParts(),
			)
			if clicked {
				action = titleBarActionToggleStarsTAISLeaderDirection
			}
			rowY += float32(titleBarMenuItemHeight)
		}

		if menuOptions.ShowStarsFontSetB {
			imgui.SetCursorPos(imgui.Vec2{X: 0, Y: rowY})
			fontClicked := imgui.InvisibleButtonV(
				"##stars-font-set-b-menu-item",
				imgui.Vec2{X: popupWidth, Y: titleBarMenuItemHeight},
				imgui.ButtonFlagsMouseButtonLeft,
			)

			fontRowMin := imgui.ItemRectMin()
			fontRowMax := imgui.ItemRectMax()
			if imgui.IsItemHovered() {
				imgui.WindowDrawList().AddRectFilledV(
					fontRowMin,
					fontRowMax,
					imgui.ColorU32Vec4(titleBarMenuHover),
					titleBarMenuPopupRounding,
					imgui.DrawFlagsRoundCornersAll,
				)
			}

			if menuOptions.UseStarsFontSetB {
				drawTitleBarMenuCheck(fontRowMin, titleBarMenuItemHeight)
			}
			drawTitleBarMenuItemTextWithPad(
				fontRowMin,
				titleBarMenuItemHeight,
				shortcutRightX,
				titleBarMenuCheckedTextPadX,
				"Use Font Set B",
				titleBarFontSetBShortcutParts(),
			)

			if fontClicked {
				action = titleBarActionToggleStarsFontSetB
			}
			rowY += float32(titleBarMenuItemHeight)
		}

		if menuOptions.ShowStarsFAAHFSTD010APalette {
			imgui.SetCursorPos(imgui.Vec2{X: 0, Y: rowY})
			wxClicked := imgui.InvisibleButtonV(
				"##stars-legacy-wx-colors-menu-item",
				imgui.Vec2{X: popupWidth, Y: titleBarMenuItemHeight},
				imgui.ButtonFlagsMouseButtonLeft,
			)

			wxRowMin := imgui.ItemRectMin()
			wxRowMax := imgui.ItemRectMax()
			if imgui.IsItemHovered() {
				imgui.WindowDrawList().AddRectFilledV(
					wxRowMin,
					wxRowMax,
					imgui.ColorU32Vec4(titleBarMenuHover),
					titleBarMenuPopupRounding,
					imgui.DrawFlagsRoundCornersAll,
				)
			}

			if menuOptions.UseStarsFAAHFSTD010APalette {
				drawTitleBarMenuCheck(wxRowMin, titleBarMenuItemHeight)
			}
			drawTitleBarMenuItemTextWithPad(
				wxRowMin,
				titleBarMenuItemHeight,
				shortcutRightX,
				titleBarMenuCheckedTextPadX,
				"Use FAA-HF-STD010A Palette",
				titleBarFAAHFSTD010APaletteShortcutParts(),
			)

			if wxClicked {
				action = titleBarActionToggleStarsFAAHFSTD010APalette
			}
		}

		imgui.EndPopup()
	}

	imgui.PopStyleColorV(2)
	imgui.PopStyleVarV(4)
	return action
}

func drawTitleBarMenuCheck(rowMin imgui.Vec2, itemHeight float32) {
	min := imgui.Vec2{
		X: rowMin.X + titleBarMenuCheckGlyphOffsetX,
		Y: rowMin.Y,
	}
	max := imgui.Vec2{
		X: min.X + titleBarMenuCheckGlyphBoxWidth,
		Y: rowMin.Y + itemHeight,
	}
	drawTitleBarSymbolGlyph(
		titleBarGlyphCheck,
		symbolsFont12,
		12,
		min,
		max,
		titleBarFg,
	)
}

func drawTitleBarMenuItemTextWithPad(
	rowMin imgui.Vec2,
	itemHeight float32, shortcutRightX float32, textPadX float32,
	label string,
	shortcut []titleBarShortcutPart,
) {
	textY := rowMin.Y + (itemHeight-imgui.FontSize())*0.5
	if textY < rowMin.Y {
		textY = rowMin.Y
	}

	imgui.WindowDrawList().AddTextVec2(
		imgui.Vec2{X: rowMin.X + textPadX, Y: textY},
		imgui.ColorU32Vec4(titleBarMenuFg),
		label,
	)

	drawTitleBarShortcutParts(rowMin, itemHeight, shortcutRightX, shortcut)
}

func drawTitleBarMenuItemText(
	rowMin imgui.Vec2,
	itemHeight float32, shortcutRightX float32,
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

	drawTitleBarShortcutParts(rowMin, itemHeight, shortcutRightX, shortcut)
}

func titleBarAddNewWindowShortcutParts() []titleBarShortcutPart {
	if runtime.GOOS == "darwin" {
		return []titleBarShortcutPart{
			{Text: shortcutCommandSymbol},
			{Text: "+"},
			{Text: shortcutShiftSymbol},
			{Text: "+W"},
		}
	}

	return []titleBarShortcutPart{
		{Text: shortcutControlSymbol},
		{Text: "+"},
		{Text: shortcutShiftSymbol},
		{Text: "+W"},
	}
}

func titleBarSwitchFacilityShortcutParts() []titleBarShortcutPart {
	if runtime.GOOS == "darwin" {
		return []titleBarShortcutPart{
			{Text: shortcutCommandSymbol},
			{Text: "+"},
			{Text: shortcutShiftSymbol},
			{Text: "+F"},
		}
	}

	return []titleBarShortcutPart{
		{Text: shortcutControlSymbol},
		{Text: "+"},
		{Text: shortcutShiftSymbol},
		{Text: "+F"},
	}
}

func titleBarDefineTemporaryAreaShortcutParts() []titleBarShortcutPart {
	if runtime.GOOS == "darwin" {
		return []titleBarShortcutPart{
			{Text: shortcutCommandSymbol},
			{Text: "+"},
			{Text: shortcutShiftSymbol},
			{Text: "+M"},
		}
	}

	return []titleBarShortcutPart{
		{Text: shortcutControlSymbol},
		{Text: "+"},
		{Text: shortcutShiftSymbol},
		{Text: "+M"},
	}
}

func titleBarViewNOTAMsShortcutParts() []titleBarShortcutPart {
	if runtime.GOOS == "darwin" {
		return []titleBarShortcutPart{
			{Text: shortcutCommandSymbol},
			{Text: "+"},
			{Text: shortcutShiftSymbol},
			{Text: "+N"},
		}
	}

	return []titleBarShortcutPart{
		{Text: shortcutControlSymbol},
		{Text: "+"},
		{Text: shortcutShiftSymbol},
		{Text: "+N"},
	}
}

func titleBarViewIAPChartShortcutParts() []titleBarShortcutPart {
	if runtime.GOOS == "darwin" {
		return []titleBarShortcutPart{
			{Text: shortcutCommandSymbol},
			{Text: "+"},
			{Text: shortcutShiftSymbol},
			{Text: "+V"},
		}
	}
	return []titleBarShortcutPart{
		{Text: shortcutControlSymbol},
		{Text: "+"},
		{Text: shortcutShiftSymbol},
		{Text: "+V"},
	}
}

func titleBarHideAssignedAltitudesShortcutParts() []titleBarShortcutPart {
	if runtime.GOOS == "darwin" {
		return []titleBarShortcutPart{
			{Text: shortcutCommandSymbol},
			{Text: "+"},
			{Text: shortcutShiftSymbol},
			{Text: "+A"},
		}
	}

	return []titleBarShortcutPart{
		{Text: shortcutControlSymbol},
		{Text: "+"},
		{Text: shortcutShiftSymbol},
		{Text: "+A"},
	}
}

// REDS-only leader-direction override: macOS Command+Option+L, otherwise
// Control+Alt+L. Render it in the same right-aligned shortcut column.
func titleBarTAISLeaderDirectionShortcutParts() []titleBarShortcutPart {
	if runtime.GOOS == "darwin" {
		return []titleBarShortcutPart{
			{Text: shortcutCommandSymbol}, {Text: "+"},
			{Text: shortcutOptionSymbol}, {Text: "+L"},
		}
	}
	return []titleBarShortcutPart{{Text: "Ctrl+Alt+L"}}
}

func titleBarFontSetBShortcutParts() []titleBarShortcutPart {
	if runtime.GOOS == "darwin" {
		return []titleBarShortcutPart{
			{Text: shortcutCommandSymbol},
			{Text: "+"},
			{Text: shortcutShiftSymbol},
			{Text: "+S"},
		}
	}

	return []titleBarShortcutPart{
		{Text: shortcutControlSymbol},
		{Text: "+"},
		{Text: shortcutShiftSymbol},
		{Text: "+S"},
	}
}

func titleBarFAAHFSTD010APaletteShortcutParts() []titleBarShortcutPart {
	if runtime.GOOS == "darwin" {
		return []titleBarShortcutPart{
			{Text: shortcutCommandSymbol},
			{Text: "+"},
			{Text: shortcutShiftSymbol},
			{Text: "+P"},
		}
	}

	return []titleBarShortcutPart{
		{Text: shortcutControlSymbol},
		{Text: "+"},
		{Text: shortcutShiftSymbol},
		{Text: "+P"},
	}
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
	itemHeight float32, shortcutRightX float32,
	parts []titleBarShortcutPart,
) {
	if len(parts) == 0 {
		return
	}

	// Measure the complete shortcut in its actual fonts so its right edge is fixed.
	x := rowMin.X + shortcutRightX - titleBarShortcutPartsWidth(parts)

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

		textSize := imgui.CalcTextSize(part.Text)
		textY := rowMin.Y + (itemHeight-imgui.FontSize())*0.5
		if textY < rowMin.Y {
			textY = rowMin.Y
		}
		imgui.WindowDrawList().AddTextVec2(
			imgui.Vec2{X: x, Y: textY},
			imgui.ColorU32Vec4(titleBarMenuShortcutFg),
			part.Text,
		)
		x += textSize.X
	}
}

func titleBarShortcutAction(
	plat platform.Platform,
	menuOptions titleBarMenuOptions,
) titleBarAction {
	if plat == nil {
		return titleBarActionNone
	}

	keyboard := plat.GetKeyboard()
	// This shortcut has Option/Alt instead of Shift, unlike the other
	// title-bar shortcuts. Check it before the existing Shift guard.
	if menuOptions.ShowStarsTAISLeaderDirection &&
		titleBarTAISLeaderDirectionShortcutPressed(keyboard, runtime.GOOS) {
		return titleBarActionToggleStarsTAISLeaderDirection
	}
	if !keyboard.IsDown(platform.KeyShift) {
		return titleBarActionNone
	}

	modifierDown := keyboard.IsDown(platform.KeyControl)
	if runtime.GOOS == "darwin" {
		modifierDown = keyboard.IsDown(platform.KeyCommand)
	}
	if !modifierDown {
		return titleBarActionNone
	}

	if keyboard.WasPressed(platform.KeyW) {
		return titleBarActionAddNewWindow
	}
	if keyboard.WasPressed(platform.KeyF) {
		return titleBarActionSwitchFacility
	}
	if menuOptions.ShowASDEXSeparator && keyboard.WasPressed(platform.KeyM) {
		return titleBarActionDefineTemporaryArea
	}
	if menuOptions.ShowASDEXSeparator && keyboard.WasPressed(platform.KeyN) {
		return titleBarActionViewNOTAMs
	}
	if menuOptions.ShowStarsIAPChart && keyboard.WasPressed(platform.KeyV) {
		return titleBarActionViewIAPChart
	}
	if menuOptions.ShowStarsAssignedAltitudes && keyboard.WasPressed(platform.KeyA) {
		return titleBarActionToggleStarsAssignedAltitudes
	}
	if menuOptions.ShowStarsFontSetB && keyboard.WasPressed(platform.KeyS) {
		return titleBarActionToggleStarsFontSetB
	}
	if menuOptions.ShowStarsFAAHFSTD010APalette && keyboard.WasPressed(platform.KeyP) {
		return titleBarActionToggleStarsFAAHFSTD010APalette
	}

	return titleBarActionNone
}

// The key event is edge-triggered, so holding the chord never repeatedly
// toggles the setting. Distinguish Command on macOS from Control elsewhere.
func titleBarTAISLeaderDirectionShortcutPressed(keyboard platform.KeyboardState, goos string) bool {
	if !keyboard.WasPressed(platform.KeyL) || !keyboard.IsDown(platform.KeyAlt) || keyboard.IsDown(platform.KeyShift) {
		return false
	}
	if goos == "darwin" {
		return keyboard.IsDown(platform.KeyCommand) && !keyboard.IsDown(platform.KeyControl)
	}
	return keyboard.IsDown(platform.KeyControl) && !keyboard.IsDown(platform.KeyCommand)
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
