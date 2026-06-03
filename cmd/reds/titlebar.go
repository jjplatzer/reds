package main

import (
	"fmt"

	"github.com/juliusplatzer/reds/platform"

	"github.com/AllenDang/cimgui-go/imgui"
)

const (
	scopeTitleBarHeight = 24

	titleBarMenuButtonWidth = 28
	titleBarMenuIconSize    = 24

	titleBarButtonWidth = 36
	titleBarIconSize    = 16
	titleBarIconStroke  = 1.5
)

var (
	titleBarBg         = imgui.Vec4{X: 20.0 / 255.0, Y: 20.0 / 255.0, Z: 20.0 / 255.0, W: 1}
	titleBarFg         = imgui.Vec4{X: 1, Y: 1, Z: 1, W: 1}
	titleBarHover      = imgui.Vec4{X: 37.0 / 255.0, Y: 37.0 / 255.0, Z: 37.0 / 255.0, W: 1}
	titleBarCloseHover = imgui.Vec4{X: 232.0 / 255.0, Y: 17.0 / 255.0, Z: 35.0 / 255.0, W: 1}
)

type titleBarAction int

const (
	titleBarActionNone titleBarAction = iota
	titleBarActionSwitchFacility
)

type titleBarButtonKind int

const (
	titleBarButtonMinimize titleBarButtonKind = iota
	titleBarButtonMaximize
	titleBarButtonClose
)

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
	dl := imgui.WindowDrawList()

	x0 := (min.X + max.X - titleBarMenuIconSize) * 0.5
	y0 := (min.Y + max.Y - titleBarMenuIconSize) * 0.5

	color := imgui.ColorU32Vec4(titleBarFg)
	rect := func(x, y, w, h float32) {
		dl.AddRectFilledV(
			imgui.Vec2{X: x0 + x, Y: y0 + y},
			imgui.Vec2{X: x0 + x + w, Y: y0 + y + h},
			color,
			0,
			imgui.DrawFlagsNone,
		)
	}

	rect(3, 5, 18, 2)
	rect(3, 11, 18, 2)
	rect(3, 17, 18, 2)
}

func drawTitleBarMenuPopup(buttonMin, buttonMax imgui.Vec2) titleBarAction {
	const (
		popupWidth = 160
		itemHeight = 24
		textPadX   = 10
	)

	imgui.SetNextWindowPosV(
		imgui.Vec2{X: buttonMin.X, Y: buttonMax.Y},
		imgui.CondAlways,
		imgui.Vec2{},
	)
	imgui.SetNextWindowSizeV(
		imgui.Vec2{X: popupWidth, Y: itemHeight},
		imgui.CondAlways,
	)

	flags := imgui.WindowFlagsNoTitleBar |
		imgui.WindowFlagsNoResize |
		imgui.WindowFlagsNoMove |
		imgui.WindowFlagsNoSavedSettings |
		imgui.WindowFlagsNoScrollbar

	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{})
	imgui.PushStyleVarVec2(imgui.StyleVarItemSpacing, imgui.Vec2{})
	imgui.PushStyleVarFloat(imgui.StyleVarPopupRounding, 0)
	imgui.PushStyleVarFloat(imgui.StyleVarPopupBorderSize, 1)
	imgui.PushStyleColorVec4(imgui.ColPopupBg, titleBarBg)
	imgui.PushStyleColorVec4(imgui.ColBorder, titleBarBg)

	action := titleBarActionNone
	if imgui.BeginPopupV("##titlebar-menu-popup", flags) {
		imgui.SetCursorPos(imgui.Vec2{X: 0, Y: 0})
		clicked := imgui.InvisibleButtonV(
			"##switch-facility-menu-item",
			imgui.Vec2{X: popupWidth, Y: itemHeight},
			imgui.ButtonFlagsMouseButtonLeft,
		)

		rowMin := imgui.ItemRectMin()
		rowMax := imgui.ItemRectMax()
		if imgui.IsItemHovered() {
			imgui.WindowDrawList().AddRectFilledV(
				rowMin,
				rowMax,
				imgui.ColorU32Vec4(titleBarHover),
				0,
				imgui.DrawFlagsNone,
			)
		}

		textY := rowMin.Y + (itemHeight-imgui.FontSize())*0.5
		if textY < rowMin.Y {
			textY = rowMin.Y
		}
		imgui.WindowDrawList().AddTextVec2(
			imgui.Vec2{X: rowMin.X + textPadX, Y: textY},
			imgui.ColorU32Vec4(titleBarFg),
			"Switch Facility",
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

func drawTitleBarButtons(plat platform.Platform, windowWidth float32) bool {
	captured := false
	captured = drawTitleBarButton(plat, titleBarButtonMinimize, windowWidth) || captured
	captured = drawTitleBarButton(plat, titleBarButtonMaximize, windowWidth) || captured
	captured = drawTitleBarButton(plat, titleBarButtonClose, windowWidth) || captured
	return captured
}

func titleBarButtonRect(kind titleBarButtonKind, windowWidth float32) (imgui.Vec2, imgui.Vec2) {
	min := imgui.Vec2{Y: 0}
	max := imgui.Vec2{Y: scopeTitleBarHeight}

	switch kind {
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
	kind titleBarButtonKind,
	windowWidth float32,
) bool {
	min, max := titleBarButtonRect(kind, windowWidth)

	imgui.SetCursorScreenPos(min)
	clicked := imgui.InvisibleButtonV(
		fmt.Sprintf("##titlebar-button-%d", kind),
		imgui.Vec2{X: max.X - min.X, Y: max.Y - min.Y},
		imgui.ButtonFlagsMouseButtonLeft,
	)

	hovered := imgui.IsItemHovered()
	if hovered {
		hoverColor := titleBarHover
		if kind == titleBarButtonClose {
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

	drawTitleBarIcon(kind, min, max)

	if clicked {
		switch kind {
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

func drawTitleBarIcon(kind titleBarButtonKind, min, max imgui.Vec2) {
	dl := imgui.WindowDrawList()

	x0 := (min.X + max.X - titleBarIconSize) * 0.5
	y0 := (min.Y + max.Y - titleBarIconSize) * 0.5

	p := func(x, y float32) imgui.Vec2 {
		return imgui.Vec2{X: x0 + x, Y: y0 + y}
	}

	color := imgui.ColorU32Vec4(titleBarFg)

	switch kind {
	case titleBarButtonMinimize:
		dl.AddLineV(p(3, 8), p(13, 8), color, titleBarIconStroke)

	case titleBarButtonMaximize:
		dl.AddRectV(
			p(3, 3),
			p(13, 13),
			color,
			0,
			imgui.DrawFlagsNone,
			titleBarIconStroke,
		)

	case titleBarButtonClose:
		dl.AddLineV(p(4, 4), p(12, 12), color, titleBarIconStroke)
		dl.AddLineV(p(12, 4), p(4, 12), color, titleBarIconStroke)
	}
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
