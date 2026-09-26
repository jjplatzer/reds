package stars

import (
	"fmt"
	"sort"
	"strings"
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	// VICE's default SSA position is (0.05, 0.90) in bottom-left-origin
	// normalized pane coordinates. REDS screen-space drawing uses a top-left
	// origin, so the equivalent y coordinate is 0.10 from the top.
	ssaDefaultX = float32(0.05)
	ssaDefaultY = float32(0.10)

	// TI 6191.409 Rev. 30, Table 2-15 describes the Red Check symbol as a
	// solid inverted delta centered in a green outlined box. The manual does
	// not prescribe pixel dimensions; these dimensions match VICE's STARS
	// implementation: a 10x10 box around a 7-unit-high equilateral triangle.
	ssaCheckBoxHalfSize = float32(5)
	ssaCheckTriangleH   = float32(7)

	zLists renderer.Z = 0
)

func (p *STARSPane) toggleVideoMapsList(selection videoMapsListSelection) {
	if p == nil {
		return
	}
	list := &p.currentPrefs().VideoMapsList
	if list.Visible && list.Selection == selection {
		list.Visible = false
		return
	}
	list.Selection = selection
	list.Visible = true
}

func mapsListLabel(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 8 {
		s = s[:8]
	}
	return strings.ReplaceAll(strings.ToUpper(s), " ", "_")
}

func (p *STARSPane) videoMapsListText() string {
	if p == nil {
		return ""
	}

	ps := p.currentPrefs()
	if !ps.VideoMapsList.Visible {
		return ""
	}

	// TI 6191.409 Rev. 30, 4.5.2 / Figure 4-5: a category list is
	// reference-only, begins with the category title, is ordered by map
	// number, and prefixes an on-screen map with ">". VICE caps the rendered
	// list at 50 entries, matching STARS' 50-map display capacity.
	maps := make([]videoMapConfig, 0, len(p.config.Facility.VideoMaps))
	for _, vm := range p.config.Facility.VideoMaps {
		if vm.STARSID <= 0 || strings.TrimSpace(vm.ShortName) == "" {
			continue
		}
		if ps.VideoMapsList.Selection == videoMapsListCurrent && !ps.VideoMapVisible[vm.STARSID] {
			continue
		}
		maps = append(maps, vm)
	}

	sort.SliceStable(maps, func(i, j int) bool {
		return maps[i].STARSID < maps[j].STARSID
	})
	if len(maps) > 50 {
		maps = maps[:50]
	}

	var text strings.Builder
	switch ps.VideoMapsList.Selection {
	case videoMapsListCurrent:
		// VICE uses "MAPS" for the CURRENT list heading; Figure 4-5 shows
		// category names as list titles (for example AERODROMES).
		text.WriteString("MAPS\n")
	default:
		text.WriteString("GEOGRAPHIC MAPS\n")
	}

	for _, vm := range maps {
		indicator := ' '
		if ps.VideoMapVisible[vm.STARSID] {
			indicator = '>'
		}
		fmt.Fprintf(
			&text,
			"%c%3d %-8s %s\n",
			indicator,
			vm.STARSID,
			mapsListLabel(vm.ShortName),
			strings.ToUpper(strings.TrimSpace(vm.Name)),
		)
	}

	return strings.TrimRight(text.String(), "\n")
}

// drawVideoMapsList renders the reference-only map category list selected by
// GEO MAPS or CURRENT. TI 6191.409 Rev. 30 Appendix B, Table B-1 specifies
// both Normal List Text and List Title as green; therefore the complete list
// uses the standard List color and LISTS brightness control. The operator
// manual leaves the initial list position site-adaptable; Preferences carries
// VICE's established default until REDS imports that adaptation.
func (p *STARSPane) drawVideoMapsList(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || p.systemFont == nil {
		return
	}

	text := p.videoMapsListText()
	if text == "" {
		return
	}

	w, h := ctx.PaneRect.Width(), ctx.PaneRect.Height()
	if w <= 0 || h <= 0 {
		return
	}

	fontSize := p.listFontSize()
	texture := p.systemFontTexture(ctx.Renderer, fontSize)
	if texture == 0 {
		return
	}

	ps := p.currentPrefs()
	position := redsmath.Vec2{
		X: ps.VideoMapsList.Position[0] * w,
		Y: ps.VideoMapsList.Position[1] * h,
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zLists)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())

	td := renderer.GetTextDrawBuilder()
	td.SetFont(p.systemFont)
	td.AddText(text, position, renderer.TextStyle{
		Size:  fontSize,
		Color: ps.Brightness.Lists.ScaleRGB(p.colors.List).ToRGBA(),
	})
	td.GenerateCommands(cb, texture)
	renderer.ReturnTextDrawBuilder(td)

	cb.DisableScissor()
}

func formatSSAWeatherLevelStatus(available, displayed [6]bool) string {
	// TI 6191.409 Rev. 30, Figure 2-24 and Table 2-15, field D:
	// weather levels occupy fixed three-character positions. A received level
	// is parenthesized when enabled for on-screen display, shown as a bare
	// digit when inhibited, and left blank when that level is not being
	// received. If no levels are being received, the field is absent.
	var b strings.Builder
	b.Grow(3 * len(available))

	anyAvailable := false
	for i, have := range available {
		if !have {
			b.WriteString("   ")
			continue
		}

		anyAvailable = true
		digit := byte('1' + i)
		if displayed[i] {
			b.WriteByte('(')
			b.WriteByte(digit)
			b.WriteByte(')')
		} else {
			b.WriteByte(' ')
			b.WriteByte(digit)
			b.WriteByte(' ')
		}
	}

	if !anyAvailable {
		return ""
	}
	return strings.TrimRight(b.String(), " ")
}

func (p *STARSPane) ssaWeatherLevelStatusText() string {
	if p == nil {
		return ""
	}

	var available [6]bool
	for i := range available {
		// The same per-level availability state drives the DCB's AVL indication.
		// A non-nil fill buffer means the current weather product contains at
		// least one cell in this STARS reflectivity band.
		available[i] = p.nexrad[i].fill != nil
	}

	return formatSSAWeatherLevelStatus(available, p.currentPrefs().DisplayWeatherLevel)
}

// drawSSA draws the System Status Area in the field order defined by
// TI 6191.409 Rev. 30, Table 2-15. Empty fields do not consume a line, so the
// area automatically shortens or lengthens as status information is added.
func (p *STARSPane) drawSSA(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil {
		return
	}

	w, h := ctx.PaneRect.Width(), ctx.PaneRect.Height()
	if w <= 0 || h <= 0 {
		return
	}

	// VICE stores the SSA's default anchor at normalized (0.05, 0.90) with
	// a bottom-left origin. Convert that to REDS' top-left screen coordinates.
	centerX := ssaDefaultX*w + ssaCheckBoxHalfSize
	centerY := ssaDefaultY * h

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zLists)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())

	listBrightness := p.currentPrefs().Brightness.Lists

	// Field A - TCW/TDW Failure Alert, EFSL / DSF indicator.
	// A healthy TCW/TDW leaves this field empty (Table 2-15).

	// Field B - System Overload Alert.
	// A TCW/TDW that is not overloaded leaves this field empty (Table 2-15).

	// Field C - Sensor Failure Alert.
	// The Red Check symbol is always displayed. Table 2-15 defines it as a
	// solid inverted delta centered in a green outlined box; failed sensor
	// names, when available, will be rendered on this field after the symbol.
	cb.SetRGB(listBrightness.ScaleRGB(p.colors.List))
	cb.LineWidth(1)
	box := renderer.GetLinesBuilder()
	box.AddLineLoop([]renderer.PointVertex{
		{X: centerX - ssaCheckBoxHalfSize, Y: centerY - ssaCheckBoxHalfSize},
		{X: centerX + ssaCheckBoxHalfSize, Y: centerY - ssaCheckBoxHalfSize},
		{X: centerX + ssaCheckBoxHalfSize, Y: centerY + ssaCheckBoxHalfSize},
		{X: centerX - ssaCheckBoxHalfSize, Y: centerY + ssaCheckBoxHalfSize},
	})
	box.GenerateCommands(cb)
	renderer.ReturnLinesBuilder(box)

	// For an equilateral triangle of height h, the centroid lies h/3 from
	// the base. REDS' y axis increases downward, so the inverted delta's tip
	// has the larger y value.
	const invSqrt3 = float32(0.5773502691896258)
	halfBase := ssaCheckTriangleH * invSqrt3
	baseY := centerY - ssaCheckTriangleH/3
	tipY := centerY + 2*ssaCheckTriangleH/3

	triangle := renderer.GetColoredTrianglesBuilder()
	triangle.AddTriangleRGB(
		renderer.PointVertex{X: centerX - halfBase, Y: baseY},
		renderer.PointVertex{X: centerX + halfBase, Y: baseY},
		renderer.PointVertex{X: centerX, Y: tipY},
		listBrightness.ScaleRGB(p.colors.TextAlert),
	)
	triangle.GenerateCommands(cb)
	renderer.ReturnColoredTrianglesBuilder(triangle)

	// Field C1 - ADS Ground Station Alert.
	// With no failing/offline ADS-B Ground Stations this field is empty.

	fontSize := p.listFontSize()
	texture := p.systemFontTexture(ctx.Renderer, fontSize)
	if texture != 0 && p.systemFont != nil {
		td := renderer.GetTextDrawBuilder()
		td.SetFont(p.systemFont)

		textX := ssaDefaultX * w
		textY := centerY + 10
		addLine := func(text string, color renderer.RGB) {
			if text == "" {
				return
			}
			td.AddText(
				text,
				redsmath.Vec2{X: textX, Y: textY},
				renderer.TextStyle{Size: fontSize, Color: listBrightness.ScaleRGB(color).ToRGBA()},
			)
			textY += float32(fontSize)
		}

		// Field D - Weather Level Status. TI 6191.409 Rev. 30, Figure 2-24
		// and Table 2-15 place this immediately before field E and specify cyan;
		// Appendix B, Table B-1 defines System Status Wx Text as RGB 0,255,255.
		// VICE uses the same parenthesized-enabled / bare-inhibited modality, but
		// renders it with the generic list style; the operator manual takes
		// precedence here, so REDS uses the dedicated SystemStatusWX color.
		addLine(p.ssaWeatherLevelStatusText(), p.colors.SystemStatusWX)

		// Field E - UTC Time, System Altimeter Setting.
		// Hours and minutes / seconds are followed by the system altimeter
		// setting used for altitude correction in this Terminal control area.
		addLine(p.ssaFieldEText(time.Now()), p.colors.List)

		// TI 6191.409 Rev. 30, 6.3.4 says a non-zero PTL value is reflected
		// in the System Status Area and is removed when the value is zero. The
		// manual does not prescribe the exact text formatting; VICE renders the
		// value as "PTL: x.x", so use that established fallback.
		if p.currentPrefs().PTLLength > 0 {
			addLine(fmt.Sprintf("PTL: %.1f", p.currentPrefs().PTLLength), p.colors.List)
		}

		// Fields E1 through N are omitted until their corresponding facility,
		// surveillance, flow-management, or controller preference state exists.

		// Field O - Mode of Operation.
		// The initial REDS STARS TCW/TDW runs in operational / normal mode.
		addLine("MODE: NORMAL", p.colors.List)

		td.GenerateCommands(cb, texture)
		renderer.ReturnTextDrawBuilder(td)
	}

	cb.DisableScissor()
}

// drawPreviewArea draws the STARS Preview Area. TI 6191.409 4.9.2 identifies
// command entry prompts, echoed input, and response/error messages as Preview
// Area contents. VICE reserves the first line for response/output, followed by
// the command prompt and then echoed input; spaces in echoed input start a new
// display line.
func (p *STARSPane) drawPreviewArea(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || p.systemFont == nil {
		return
	}
	if p.commandResponse == "" && p.commandMode == CommandModeNone && p.commandInput == "" {
		return
	}

	var text strings.Builder
	text.WriteString(p.commandResponse)
	text.WriteByte('\n')

	if prompt := p.commandMode.PreviewString(); prompt != "" {
		text.WriteString(prompt)
		if p.commandMode == CommandModeMultiFunc {
			text.WriteString(p.multiFuncPrefix)
		}
		text.WriteByte('\n')
	}
	text.WriteString(strings.Join(strings.Fields(p.commandInput), "\n"))

	fontSize := p.listFontSize()
	texture := p.systemFontTexture(ctx.Renderer, fontSize)
	if texture == 0 {
		return
	}

	ps := p.currentPrefs()
	position := redsmath.Vec2{
		X: ps.PreviewAreaPosition[0] * ctx.PaneRect.Width(),
		Y: ps.PreviewAreaPosition[1] * ctx.PaneRect.Height(),
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zLists)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())

	td := renderer.GetTextDrawBuilder()
	td.SetFont(p.systemFont)
	td.AddText(text.String(), position, renderer.TextStyle{
		Size:  fontSize,
		Color: ps.Brightness.FullDatablocks.ScaleRGB(p.colors.PreviewList).ToRGBA(),
	})
	td.GenerateCommands(cb, texture)
	renderer.ReturnTextDrawBuilder(td)

	cb.DisableScissor()
}
