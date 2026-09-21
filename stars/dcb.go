package stars

import (
	"fmt"
	"strconv"
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"
)

// TI 6191.409 Rev. 30, Figure 2-10 shows the High Resolution (2K) Main
// Display Control Bar. VICE models each full DCB slot as 72 display units;
// half-height and half-width buttons split that slot exactly in two.
const dcbButtonSize = float32(72)

const (
	mainDCBColumns    = 19
	mainDCBMapColumns = 3
	briteDCBColumns   = 9 // TI 6191.409 Rev. 30, Figure 4-13.
)

const zDCB renderer.Z = 100

type dcbFlags uint8

const (
	buttonFull dcbFlags = 1 << iota
	buttonHalfVertical
	buttonHalfHorizontal
	buttonSelected
	buttonWXAVL
	buttonDisabled
	buttonUnsupported
)

type dcbDrawer struct {
	pane       *STARSPane
	ctx        *panes.Context
	cb         *renderer.CmdBuffer
	fontSize   int
	texture    renderer.TextureID
	buttonSize float32
	cursor     redsmath.Vec2
	bar        redsmath.Rect
}

func (p *STARSPane) mouseOverDCB(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}
	return redsmath.NewRect(0, 0, ctx.PaneRect.Width(), dcbButtonSize).Contains(ctx.Mouse.Pos)
}

// drawDCB draws the High Resolution (2K) Display Control Bar. The Main DCB
// follows TI 6191.409 Rev. 30, Figure 2-10; the Auxiliary DCB uses the
// TSAS-adapted layout in Figure 2-12; and the BRITE submenu follows Figure
// 4-13 / Table 4-1. Geometry and button rendering mirror VICE so additional
// STARS submenus can reuse the same helpers.
func (p *STARSPane) drawDCB(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || p.systemFont == nil {
		return
	}

	// A DCB layout change must not let the still-held click visually depress
	// the button that happens to occupy the same coordinates on the new page.
	// Keep suppressing held-button feedback until the SHIFT click is released.
	if p.dcbSuppressPressUntilRelease &&
		(ctx.Mouse == nil || !ctx.Mouse.IsDown(platform.MouseButtonLeft)) {
		p.dcbSuppressPressUntilRelease = false
	}

	fontSize := p.listFontSize() // DCB character size 1 uses the same STARS font as list size 1.
	texture := p.systemFontTexture(ctx.Renderer, fontSize)
	if texture == 0 {
		return
	}

	w, h := ctx.PaneRect.Width(), ctx.PaneRect.Height()
	if w <= 0 || h <= 0 {
		return
	}

	// VICE keeps 72-unit buttons pixel-exact and scrolls overflowing DCB
	// content instead of shrinking it. One wheel notch advances one full slot
	// unless a BRITE spinner is active, in which case the wheel adjusts it.
	maxScroll := max(float32(0), mainDCBColumns*dcbButtonSize-w)
	if ctx.Mouse != nil && p.mouseOverDCB(ctx) && ctx.Mouse.Wheel.Y != 0 &&
		p.commandMode != CommandModeBriteSpinner &&
		p.commandMode != CommandModeRangeRings && maxScroll > 0 {
		if ctx.Mouse.Wheel.Y > 0 {
			p.dcbScroll += dcbButtonSize
		} else {
			p.dcbScroll -= dcbButtonSize
		}
		p.dcbScroll = min(max(p.dcbScroll, 0), maxScroll)
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zDCB)
	cb.Viewport(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())

	bar := redsmath.NewRect(0, 0, w, min(dcbButtonSize, h))
	setDCBScissor(ctx, cb, bar)

	// VICE does not scale the DCB backing strip with the DCB brightness control.
	bg := renderer.GetColoredTrianglesBuilder()
	bg.AddQuad(
		renderer.PointVertex{X: bar.Min.X, Y: bar.Min.Y},
		renderer.PointVertex{X: bar.Max.X, Y: bar.Min.Y},
		renderer.PointVertex{X: bar.Max.X, Y: bar.Max.Y},
		renderer.PointVertex{X: bar.Min.X, Y: bar.Max.Y},
		p.colors.DCBBackground,
	)
	bg.GenerateCommands(cb)
	renderer.ReturnColoredTrianglesBuilder(bg)

	d := dcbDrawer{
		pane:       p,
		ctx:        ctx,
		cb:         cb,
		fontSize:   fontSize,
		texture:    texture,
		buttonSize: dcbButtonSize,
		cursor:     redsmath.Vec2{X: -p.dcbScroll, Y: 0},
		bar:        bar,
	}

	briteActive := p.commandMode == CommandModeBrite || p.commandMode == CommandModeBriteSpinner
	if p.dcbShowAux && !briteActive {
		d.drawAuxPage()
	} else {
		d.drawMainPage(briteActive)
	}
	if briteActive {
		// As in VICE, the submenu is drawn over the right-hand portion of the
		// disabled Main DCB. Revision 30 Figure 4-13 has nine full columns.
		d.cursor = redsmath.Vec2{
			X: -p.dcbScroll + float32(mainDCBColumns-briteDCBColumns)*d.buttonSize,
			Y: 0,
		}
		p.adjustActiveBrightness(ctx)
		d.drawBritePage()
	}
	cb.DisableScissor()
}

func (d *dcbDrawer) drawMainPage(disabled bool) {
	p := d.pane
	ps := p.currentPrefs()
	mainFlags := func(flags dcbFlags) dcbFlags {
		if disabled {
			return flags | buttonDisabled
		}
		return flags
	}

	// <RANGE n> — Table 2-6: current display range is displayed in the label.
	d.button("RANGE\n"+strconv.Itoa(int(ps.Range+0.5)), mainFlags(buttonFull),
		p.commandMode == CommandModeRange, func() {
			p.setCommandMode(CommandModeRange)
		})

	// <PLACE CNTR> / <OFF CNTR>.
	d.button("PLACE\nCNTR", mainFlags(buttonHalfVertical), false, nil)
	d.button("OFF\nCNTR", mainFlags(buttonHalfVertical), ps.UseUserCenter, func() {
		ps.UseUserCenter = !ps.UseUserCenter
	})

	// <RR n> / <PLACE RR> / <RR CNTR>. Table 2-6 identifies <RR nn> as an
	// adjustment button whose label always shows the current spacing.
	rrSelected := p.commandMode == CommandModeRangeRings
	d.button("RR\n"+strconv.Itoa(int(ps.RangeRingRadius+0.5)), mainFlags(buttonFull), rrSelected, func() {
		if rrSelected {
			p.setCommandMode(CommandModeNone)
		} else {
			p.setCommandMode(CommandModeRangeRings)
		}
	})
	d.button("PLACE\nRR", mainFlags(buttonHalfVertical), false, nil)
	// TI 6191.409 Rev. 30, 6.1.3: RR CNTR is highlighted when the system
	// default center is in use and is off when the user-specified center is in
	// use. This is intentionally the inverse of UseUserRangeRingsCenter.
	d.button("RR\nCNTR", mainFlags(buttonHalfVertical), !ps.UseUserRangeRingsCenter, func() {
		ps.UseUserRangeRingsCenter = !ps.UseUserRangeRingsCenter
	})

	// <MAPS> and the six position-adapted Main DCB map buttons. The facility
	// map group is already transposed row-major by crc2reds; VICE's index helper
	// restores top/bottom drawing order while filling three columns.
	d.button("MAPS", mainFlags(buttonFull), false, nil)
	maps := p.mainDCBMaps()
	for i := range 6 {
		idx := videoMapButtonIndex(0, mainDCBMapColumns, i)
		m := maps[idx]
		if m.STARSID == 0 {
			d.button("", buttonHalfVertical|buttonDisabled, false, nil)
			continue
		}
		label := strings.TrimSpace(m.ShortName)
		if label == "" {
			label = strings.TrimSpace(m.Name)
		}
		text := fmt.Sprintf("%d\n%s", m.STARSID, label)
		selected := ps.VideoMapVisible[m.STARSID]
		mapID := m.STARSID
		d.button(text, mainFlags(buttonHalfVertical), selected, func() {
			if ps.VideoMapVisible[mapID] {
				delete(ps.VideoMapVisible, mapID)
			} else {
				ps.VideoMapVisible[mapID] = true
			}
		})
	}

	// <WX 1> ... <WX 6>. AVL is displayed when that level is detected.
	for i := range ps.DisplayWeatherLevel {
		label := "WX" + strconv.Itoa(i+1)
		flags := buttonHalfHorizontal
		if p.nexrad[i].fill != nil {
			label += "\nAVL"
			flags |= buttonWXAVL
		}
		level := i
		d.button(label, mainFlags(flags), ps.DisplayWeatherLevel[i], func() {
			ps.DisplayWeatherLevel[level] = !ps.DisplayWeatherLevel[level]
		})
	}

	// <BRITE> — Table 2-6 / 4.10: displays the brightness submenu.
	d.button("BRITE", mainFlags(buttonFull), false, func() {
		p.setCommandMode(CommandModeBrite)
	})
	d.button("LDR DIR\n"+ps.LeaderLineDirection, mainFlags(buttonHalfVertical), false, nil)
	d.button("LDR LEN\n"+strconv.Itoa(ps.LeaderLineLength), mainFlags(buttonHalfVertical), false, nil)
	d.button("CHAR\nSIZE", mainFlags(buttonFull), false, nil)
	d.button("MODE\nFSL", mainFlags(buttonFull)|buttonUnsupported, false, nil)
	d.button("SITE\nMULTI", mainFlags(buttonFull), false, nil)
	d.button("PREF", mainFlags(buttonFull), false, nil)
	d.button("SSA\nFILTER", mainFlags(buttonHalfVertical), false, nil)
	d.button("GI TEXT\nFILTER", mainFlags(buttonHalfVertical), false, nil)
	d.button("SHIFT", mainFlags(buttonFull), false, func() {
		p.dcbShowAux = true
		p.dcbSuppressPressUntilRelease = true
	})
}

// drawAuxPage draws the High Resolution (2K) Auxiliary Display Control Bar
// using the TSAS-adapted layout in TI 6191.409 Rev. 30, Figure 2-12 / Table
// 2-6. For this first implementation only <SHIFT> is operational. TSAS and
// TIME LINE are deliberately shown as unsupported until REDS has TSAS state;
// the remaining controls retain their normal appearance so functionality can
// be added later without changing the page geometry.
func (d *dcbDrawer) drawAuxPage() {
	p := d.pane

	// <VOL n>.
	d.button("VOL\n2", buttonFull, false, nil)

	// <HISTORY n> / <H_RATE n.n>.
	d.button("HISTORY\n5", buttonHalfVertical, false, nil)
	d.button("H_RATE\n4.5", buttonHalfVertical, false, nil)

	// Cursor controls and uncorrelated-target presentation.
	d.button("CURSOR\nHOME", buttonFull, false, nil)
	d.button("CSR SPD\n5", buttonFull, false, nil)
	d.button("MAP\nUNCOR", buttonFull, false, nil)
	d.button("UNCOR", buttonFull, false, nil)
	d.button("BEACON\nMODE-2", buttonFull, false, nil)
	d.button("RTQC", buttonFull, false, nil)
	d.button("MCP", buttonFull, false, nil)

	// DCB position radio-button group. These are intentionally inert for now.
	d.button("DCB\nTOP", buttonHalfVertical, true, nil)
	d.button("DCB\nLEFT", buttonHalfVertical, false, nil)
	d.button("DCB\nRIGHT", buttonHalfVertical, false, nil)
	d.button("DCB\nBOTTOM", buttonHalfVertical, false, nil)

	// Predicted track line and dwell controls.
	d.button("PTL\nLNTH\n1.5", buttonFull, false, nil)
	d.button("PTL OWN", buttonHalfVertical, false, nil)
	d.button("PTL ALL", buttonHalfVertical, false, nil)
	d.button("DWELL\nON", buttonFull, false, nil)
	d.button("TPA /\nATPA", buttonFull, false, nil)

	// Figure 2-12 TSAS-adapted controls. Table 2-6 says these appear grayed
	// when TSAS is adapted but unavailable to the current TCW/TDW; REDS does
	// not have TSAS integration yet, so keep both controls in that state.
	d.button("TSAS", buttonHalfVertical|buttonUnsupported, false, nil)
	d.button("TIME\nLINE", buttonHalfVertical|buttonUnsupported, false, nil)

	// <SHIFT> toggles the Main and Auxiliary DCBs (Table 2-6).
	d.button("SHIFT", buttonFull, false, func() {
		p.dcbShowAux = false
		p.dcbSuppressPressUntilRelease = true
	})
}

const starsRangeRingMouseDelta = float32(10)

// stepRangeRingSpacing follows the STARS RR spinner ordering used by VICE:
// 2 <-> 5 <-> 10 <-> 20 NM. Positive delta moves toward the smaller value;
// negative delta moves toward the larger value.
func stepRangeRingSpacing(radius float32, delta int) float32 {
	if delta > 0 {
		switch radius {
		case 5:
			return 2
		case 10:
			return 5
		case 20:
			return 10
		}
		return radius
	}
	if delta < 0 {
		switch radius {
		case 2:
			return 5
		case 5:
			return 10
		case 10:
			return 20
		}
	}
	return radius
}

// adjustRangeRingSpacing implements the selected <RR nn> DCB spinner. Keep
// this input handling in dcb.go, matching VICE, where the RR spinner belongs
// to the DCB implementation while the actual ring drawing lives in tools.go.
func (p *STARSPane) adjustRangeRingSpacing(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Mouse == nil || p.commandMode != CommandModeRangeRings {
		return
	}

	mouse := ctx.Mouse
	ps := p.currentPrefs()

	if mouse.WasPressed(platform.MouseButtonLeft) {
		p.setCommandMode(CommandModeNone)
		return
	}

	if mouse.Wheel.Y != 0 {
		if mouse.Wheel.Y > 0 {
			ps.RangeRingRadius = stepRangeRingSpacing(ps.RangeRingRadius, 1)
		} else {
			ps.RangeRingRadius = stepRangeRingSpacing(ps.RangeRingRadius, -1)
		}
		p.rangeRingDragAccumY = 0
		return
	}

	p.rangeRingDragAccumY += mouse.Delta.Y
	if p.rangeRingDragAccumY > starsRangeRingMouseDelta {
		ps.RangeRingRadius = stepRangeRingSpacing(ps.RangeRingRadius, -1)
		p.rangeRingDragAccumY = 0
	} else if p.rangeRingDragAccumY < -starsRangeRingMouseDelta {
		ps.RangeRingRadius = stepRangeRingSpacing(ps.RangeRingRadius, 1)
		p.rangeRingDragAccumY = 0
	}
}

// brightnessControl is one adjustment button in the standard BRITE submenu.
// min/allowOff come from TI 6191.409 Rev. 30, Table 4-1.
type brightnessControl struct {
	id       string
	label    string
	value    *Brightness
	min      Brightness
	allowOff bool
}

func (p *STARSPane) briteControls() []brightnessControl {
	b := &p.currentPrefs().Brightness
	return []brightnessControl{
		{id: "DCB", label: "DCB", value: &b.DCB, min: 25},
		{id: "BKC", label: "BKC", value: &b.BackgroundContrast, min: 0},
		{id: "MPA", label: "MPA", value: &b.VideoGroupA, min: 5},
		{id: "MPB", label: "MPB", value: &b.VideoGroupB, min: 5},
		{id: "FDB", label: "FDB", value: &b.FullDatablocks, min: 5, allowOff: true},
		{id: "LST", label: "LST", value: &b.Lists, min: 25},
		{id: "POS", label: "POS", value: &b.Positions, min: 5, allowOff: true},
		{id: "LDB", label: "LDB", value: &b.LimitedDatablocks, min: 5, allowOff: true},
		{id: "OTH", label: "OTH", value: &b.OtherTracks, min: 5, allowOff: true},
		{id: "TLS", label: "TLS", value: &b.Lines, min: 5, allowOff: true},
		{id: "RR", label: "RR", value: &b.RangeRings, min: 5, allowOff: true},
		{id: "CMP", label: "CMP", value: &b.Compass, min: 5, allowOff: true},
		{id: "BCN", label: "BCN", value: &b.BeaconSymbols, min: 5, allowOff: true},
		{id: "PRI", label: "PRI", value: &b.PrimarySymbols, min: 5, allowOff: true},
		{id: "HST", label: "HST", value: &b.History, min: 5, allowOff: true},
		{id: "WX", label: "WX", value: &b.Weather, min: 5},
		{id: "WXC", label: "WXC", value: &b.WxContrast, min: 5},
	}
}

func (p *STARSPane) activeBriteControl() *brightnessControl {
	if p == nil || p.activeBrightnessControl == "" {
		return nil
	}
	controls := p.briteControls()
	for i := range controls {
		if controls[i].id == p.activeBrightnessControl {
			return &controls[i]
		}
	}
	return nil
}

func (p *STARSPane) setActiveBrightness(value Brightness) error {
	control := p.activeBriteControl()
	if control == nil {
		return ErrSTARSCommandFormat
	}
	if value > 100 || value < 0 ||
		(value < control.min && !(value == 0 && control.allowOff)) {
		return ErrSTARSIllegalValue
	}
	*control.value = value
	return nil
}

func (p *STARSPane) stepBrightness(control *brightnessControl, delta int) {
	if control == nil || delta == 0 {
		return
	}
	value := int(*control.value) + 5*delta
	if value > 100 {
		value = 100
	}
	if value < int(control.min) {
		if control.allowOff {
			value = 0
		} else {
			value = int(control.min)
		}
	}
	*control.value = Brightness(value)
}

func (p *STARSPane) adjustActiveBrightness(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Mouse == nil || p.commandMode != CommandModeBriteSpinner {
		return
	}
	control := p.activeBriteControl()
	if control == nil {
		return
	}

	// TI 6191.409 4.10 changes brightness in 5% increments with the trackball:
	// upward increases, downward decreases. On REDS' top-left-origin mouse,
	// upward motion has negative Y. The wheel follows the same user-facing
	// convention: scroll up increases, scroll down decreases.
	if ctx.Mouse.Wheel.Y != 0 {
		if ctx.Mouse.Wheel.Y > 0 {
			p.stepBrightness(control, 1)
		} else {
			p.stepBrightness(control, -1)
		}
		return
	}

	p.brightnessDragAccumY += ctx.Mouse.Delta.Y
	for p.brightnessDragAccumY <= -5 {
		p.stepBrightness(control, 1)
		p.brightnessDragAccumY += 5
	}
	for p.brightnessDragAccumY >= 5 {
		p.stepBrightness(control, -1)
		p.brightnessDragAccumY -= 5
	}
}

func (d *dcbDrawer) drawBritePage() {
	p := d.pane
	for _, control := range p.briteControls() {
		control := control
		valueText := strconv.Itoa(int(*control.value))
		if *control.value == 0 {
			valueText = "OFF"
		}
		selected := p.commandMode == CommandModeBriteSpinner &&
			p.activeBrightnessControl == control.id
		d.button(control.label+" "+valueText, buttonHalfVertical, selected, func() {
			if selected {
				p.commandMode = CommandModeBrite
				p.activeBrightnessControl = ""
				p.brightnessDragAccumY = 0
				p.commandInput = ""
				p.commandResponse = ""
				return
			}
			p.setCommandMode(CommandModeBriteSpinner)
			p.activeBrightnessControl = control.id
		})
	}

	d.button("DONE", buttonHalfVertical, false, func() {
		p.setCommandMode(CommandModeNone)
	})
}

func videoMapButtonIndex(base, columns, i int) int {
	if i&1 == 0 {
		return base + i/2
	}
	return base + columns + i/2
}

func (d *dcbDrawer) button(text string, flags dcbFlags, selected bool, onClick func()) {
	sz := dcbButtonDimensions(flags, d.buttonSize)
	rect := redsmath.NewRect(d.cursor.X, d.cursor.Y, d.cursor.X+sz.X, d.cursor.Y+sz.Y)
	visible := intersectDCBRect(rect, d.bar)
	mouseInside := d.ctx.Mouse != nil && !visible.Empty() && visible.Contains(d.ctx.Mouse.Pos)
	pressed := mouseInside && d.ctx.Mouse != nil &&
		d.ctx.Mouse.IsDown(platform.MouseButtonLeft) &&
		!d.pane.dcbSuppressPressUntilRelease
	clicked := mouseInside && d.ctx.Mouse != nil && d.ctx.Mouse.WasPressed(platform.MouseButtonLeft)

	disabled := flags&buttonDisabled != 0
	unsupported := flags&buttonUnsupported != 0
	pushedIn := selected
	if !disabled && !unsupported && pressed {
		pushedIn = !pushedIn
	}

	buttonColor := d.pane.colors.DCBButton
	textColor := d.pane.colors.DCBText
	switch {
	case disabled:
		buttonColor = d.pane.colors.DCBDisabledButton
		textColor = d.pane.colors.DCBDisabledText
	case unsupported:
		buttonColor = d.pane.colors.DCBUnsupportedButton
		textColor = d.pane.colors.DCBUnsupportedText
	default:
		if flags&buttonWXAVL != 0 {
			if pushedIn {
				buttonColor = d.pane.colors.DCBActiveWXButton
			} else {
				buttonColor = d.pane.colors.DCBWXButton
			}
		} else if pushedIn {
			buttonColor = d.pane.colors.DCBActiveButton
		}
		if mouseInside {
			textColor = d.pane.colors.DCBTextSelected
		}
	}

	// VICE scales the button fill by DCB brightness but leaves DCB text at
	// its palette color. This also matches the CRC appearance much better than
	// dimming the labels together with the button slab.
	buttonColor = d.pane.currentPrefs().Brightness.DCB.ScaleRGB(buttonColor)

	if !visible.Empty() {
		setDCBScissor(d.ctx, d.cb, visible)

		fill := renderer.GetColoredTrianglesBuilder()
		fill.AddQuad(
			renderer.PointVertex{X: rect.Min.X, Y: rect.Min.Y},
			renderer.PointVertex{X: rect.Max.X, Y: rect.Min.Y},
			renderer.PointVertex{X: rect.Max.X, Y: rect.Max.Y},
			renderer.PointVertex{X: rect.Min.X, Y: rect.Max.Y},
			buttonColor,
		)
		fill.GenerateCommands(d.cb)
		renderer.ReturnColoredTrianglesBuilder(fill)

		d.drawButtonBevel(rect, pushedIn && !disabled && !unsupported)
		d.drawButtonText(rect, text, textColor)
		setDCBScissor(d.ctx, d.cb, d.bar)
	}

	if clicked && !disabled && !unsupported && onClick != nil {
		onClick()
	}
	d.advance(flags, sz)
}

func (d *dcbDrawer) drawButtonBevel(rect redsmath.Rect, depressed bool) {
	topLeft := d.pane.colors.DCBTopBevel
	bottomRight := d.pane.colors.DCBBottomBevel
	if depressed {
		topLeft, bottomRight = bottomRight, topLeft
	}

	lines := renderer.GetColoredLinesBuilder()
	for i := 0; i < 3; i++ {
		fi := float32(i)
		lines.AddLineRGB(
			renderer.PointVertex{X: rect.Min.X, Y: rect.Min.Y + fi},
			renderer.PointVertex{X: rect.Max.X - fi, Y: rect.Min.Y + fi},
			topLeft,
		)
		lines.AddLineRGB(
			renderer.PointVertex{X: rect.Min.X + fi, Y: rect.Min.Y},
			renderer.PointVertex{X: rect.Min.X + fi, Y: rect.Max.Y - fi},
			topLeft,
		)
		lines.AddLineRGB(
			renderer.PointVertex{X: rect.Max.X - fi, Y: rect.Min.Y + fi},
			renderer.PointVertex{X: rect.Max.X - fi, Y: rect.Max.Y},
			bottomRight,
		)
		lines.AddLineRGB(
			renderer.PointVertex{X: rect.Min.X + fi, Y: rect.Max.Y - fi},
			renderer.PointVertex{X: rect.Max.X, Y: rect.Max.Y - fi},
			bottomRight,
		)
	}
	d.cb.LineWidth(max(float32(1), d.ctx.DPIScale))
	lines.GenerateCommands(d.cb)
	renderer.ReturnColoredLinesBuilder(lines)
}

func (d *dcbDrawer) drawButtonText(rect redsmath.Rect, text string, color renderer.RGB) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}

	fs := d.pane.systemFont.Size(d.fontSize)
	if fs == nil {
		return
	}
	lineHeight := float32(fs.LineHeight)
	totalHeight := lineHeight * float32(len(lines))
	y := rect.Min.Y + max(float32(0), (rect.Height()-totalHeight)/2)

	td := renderer.GetTextDrawBuilder()
	td.SetFont(d.pane.systemFont)
	for _, line := range lines {
		width := dcbTextWidth(fs, line)
		x := rect.Min.X + max(float32(1), (rect.Width()-width)/2)
		td.AddText(line, redsmath.Vec2{X: x, Y: y}, renderer.TextStyle{
			Size:  d.fontSize,
			Color: color.ToRGBA(),
		})
		y += lineHeight
	}
	td.GenerateCommands(d.cb, d.texture)
	renderer.ReturnTextDrawBuilder(td)
}

func (d *dcbDrawer) advance(flags dcbFlags, sz redsmath.Vec2) {
	switch {
	case flags&buttonFull != 0 || flags&buttonHalfHorizontal != 0:
		d.cursor.X += sz.X
		d.cursor.Y = 0
	case flags&buttonHalfVertical != 0:
		if d.cursor.Y == 0 {
			d.cursor.Y = sz.Y
		} else {
			d.cursor.X += sz.X
			d.cursor.Y = 0
		}
	}
}

func dcbButtonDimensions(flags dcbFlags, size float32) redsmath.Vec2 {
	switch {
	case flags&buttonFull != 0:
		return redsmath.Vec2{X: size, Y: size}
	case flags&buttonHalfVertical != 0:
		return redsmath.Vec2{X: size, Y: size / 2}
	case flags&buttonHalfHorizontal != 0:
		return redsmath.Vec2{X: size / 2, Y: size}
	default:
		return redsmath.Vec2{X: size, Y: size}
	}
}

func dcbTextWidth(fs *renderer.BitmapFontSize, text string) float32 {
	if fs == nil {
		return 0
	}
	width := 0
	for _, r := range text {
		if glyph, ok := fs.Glyph(r); ok {
			width += glyph.Advance
		}
	}
	return float32(width)
}

func intersectDCBRect(a, b redsmath.Rect) redsmath.Rect {
	r := redsmath.NewRect(
		max(a.Min.X, b.Min.X),
		max(a.Min.Y, b.Min.Y),
		min(a.Max.X, b.Max.X),
		min(a.Max.Y, b.Max.Y),
	)
	if r.Empty() {
		return redsmath.Rect{}
	}
	return r
}

func setDCBScissor(ctx *panes.Context, cb *renderer.CmdBuffer, local redsmath.Rect) {
	absolute := local.Translate(ctx.PaneRect.Min)
	x, y, w, h := ctx.LogicalToFramebufferRect(absolute)
	cb.Scissor(x, y, w, h)
}
