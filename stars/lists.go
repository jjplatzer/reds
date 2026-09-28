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
	// solid inverted delta centered in a green outlined box. The source FAA
	// raster shows the delta as a 7x7 stepped bitmap: row widths 7,7,5,5,3,3,1.
	// That keeps VICE's 7-unit dimension while matching the actual STARS
	// pixel structure instead of relying on rasterization of a vector triangle.
	ssaCheckBoxHalfSize  = float32(5)
	ssaCheckTriangleSize = float32(7)

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

// ssaRadarModeText returns the sensor-mode portion of SSA field G.
//
// TI 6191.409 Rev. 30, Table 2-16 distinguishes SYS (multi-sensor mode)
// from FUSED (fused mode). REDS currently consumes the already-processed
// STARS/TAIS track stream and does not model individual adapted radar sites
// or per-display sensor selection, so advertise the presentation as FUSED.
// If true SITE/MULTI support is added later, this helper can return SYS or
// the selected sensor identifier without changing the SSA layout/filter logic.
func (p *STARSPane) ssaRadarModeText() string {
	if p == nil {
		return ""
	}
	return "FUSED"
}

// ssaSystemStatusText returns the STATUS portion of SSA field G.
//
// Table 2-15 permits FSL/EFSL/DSF status values such as OK, NA, TR, and NR.
// VICE uses the display client's connection state as the simulator proxy and
// presents OK/OK/NA while connected and NA/NA/NA in alert red otherwise.
// REDS has the equivalent live-state boundary at the TAIS client, so mirror
// that behavior rather than inventing facility-health information TAIS does
// not provide.
func (p *STARSPane) ssaSystemStatusText() (string, bool) {
	if p != nil && p.tais != nil && p.tais.Status().Connected {
		return "OK/OK/NA", false
	}
	return "NA/NA/NA", true
}

// ssaSystemOffText returns SSA field J (System OFF Indicators).
//
// TI 6191.409 Rev. 30, Table 2-18 defines this field as system-wide inhibited
// processing capabilities (CA, MCI, MSAW, CRDA, HOP, INTRAIL, etc.). VICE
// builds the line only from actual simulator inhibit state and omits it when
// nothing is disabled. REDS does not yet receive those site-wide inhibit
// states from TAIS, so an empty field is the only truthful current result.
// Keep the decision isolated here so those states can be wired in later
// without changing the SSA FILTER or renderer layout.
func (p *STARSPane) ssaSystemOffText() string {
	return ""
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

	// SSAListPosition is the operator-selected anchor. TI 6191.409 Rev. 30,
	// 4.9.4 defines this as the location selected for the System status area's
	// top-left corner. VICE stores the same state as ps.SSAList.Position.
	ssaPos := p.currentPrefs().SSAListPosition
	centerX := ssaPos[0]*w + ssaCheckBoxHalfSize
	centerY := ssaPos[1] * h

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zLists)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())

	listBrightness := p.currentPrefs().Brightness.Lists
	filter := p.currentPrefs().SSAFilter

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

	// Match the FAA raster exactly rather than drawing a mathematically smooth
	// equilateral triangle. At native size the red pixels are arranged as:
	//
	//   #######
	//   #######
	//    #####
	//    #####
	//     ###
	//     ###
	//      #
	//
	// Four quads reproduce those 7x7 pixel tiers with no extra geometry.
	triangle := renderer.GetColoredTrianglesBuilder()
	triangleColor := listBrightness.ScaleRGB(p.colors.TextAlert)
	half := ssaCheckTriangleSize / 2
	addTier := func(width, y0, y1 float32) {
		halfWidth := width / 2
		triangle.AddQuad(
			renderer.PointVertex{X: centerX - halfWidth, Y: centerY - half + y0},
			renderer.PointVertex{X: centerX + halfWidth, Y: centerY - half + y0},
			renderer.PointVertex{X: centerX + halfWidth, Y: centerY - half + y1},
			renderer.PointVertex{X: centerX - halfWidth, Y: centerY - half + y1},
			triangleColor,
		)
	}
	addTier(7, 0, 2)
	addTier(5, 2, 4)
	addTier(3, 4, 6)
	addTier(1, 6, 7)
	triangle.GenerateCommands(cb)
	renderer.ReturnColoredTrianglesBuilder(triangle)

	// Field C1 - ADS Ground Station Alert.
	// With no failing/offline ADS-B Ground Stations this field is empty.

	fontSize := p.listFontSize()
	texture := p.systemFontTexture(ctx.Renderer, fontSize)
	if texture != 0 && p.systemFont != nil {
		td := renderer.GetTextDrawBuilder()
		td.SetFont(p.systemFont)

		textX := ssaPos[0] * w
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
		type textSegment struct {
			text  string
			color renderer.RGB
		}
		addSegments := func(segments ...textSegment) {
			x := textX
			wrote := false
			for _, segment := range segments {
				if segment.text == "" {
					continue
				}
				td.AddText(
					segment.text,
					redsmath.Vec2{X: x, Y: textY},
					renderer.TextStyle{Size: fontSize, Color: listBrightness.ScaleRGB(segment.color).ToRGBA()},
				)
				// MeasureText returns the visible glyph bounds, so a trailing space
				// contributes no width. Append a sentinel space while measuring to
				// get the actual pen advance, including any intentional trailing
				// space already present in the segment (e.g. STATUS before RADAR).
				width, _ := p.systemFont.MeasureText(segment.text+" ", fontSize)
				x += float32(width)
				wrote = true
			}
			if wrote {
				textY += float32(fontSize)
			}
		}

		// Field D - Weather Level Status. TI 6191.409 Rev. 30, Figure 2-24
		// and Table 2-15 place this immediately before field E and specify cyan;
		// Appendix B, Table B-1 defines System Status Wx Text as RGB 0,255,255.
		// VICE uses the same parenthesized-enabled / bare-inhibited modality, but
		// renders it with the generic list style; the operator manual takes
		// precedence here, so REDS uses the dedicated SystemStatusWX color.
		if filter.All || filter.Wx {
			addLine(p.ssaWeatherLevelStatusText(), p.colors.SystemStatusWX)
		}

		// Field E - UTC Time, System Altimeter Setting.
		// Hours and minutes / seconds are followed by the system altimeter
		// setting used for altitude correction in this Terminal control area.
		// Figure 4-7 exposes TIME and ALTSTG as independent filter buttons even
		// though Table 2-15 places them on one SSA line.
		if filter.All || filter.Time || filter.Altimeter {
			addLine(
				p.ssaFieldEText(time.Now(), filter.All || filter.Time, filter.All || filter.Altimeter),
				p.colors.List,
			)
		}

		// Field G - FSL / EFSL / DSF status, Configuration Plan, Sensor Modes.
		// STATUS, PLAN, and RADAR independently filter portions of this one line.
		// Match VICE's composition: status first, then plan, then radar. REDS has
		// no live configuration-plan identifier yet, so PLAN contributes no text.
		// STATUS uses the TAIS connection as the same kind of simulator-health
		// proxy VICE uses for its client connection; RADAR is currently SYS.
		if filter.All || filter.Status || filter.ConfigPlan || filter.Radar {
			statusText := ""
			statusColor := p.colors.List
			if filter.All || filter.Status {
				var alert bool
				statusText, alert = p.ssaSystemStatusText()
				statusText += " "
				if alert {
					statusColor = p.colors.TextAlert
				}
			}

			radarText := ""
			if filter.All || filter.Radar {
				radarText = p.ssaRadarModeText()
			}

			addSegments(
				textSegment{statusText, statusColor},
				textSegment{radarText, p.colors.List},
			)
		}

		// Field H - Selected beacon codes / code blocks. REDS already carries
		// the selected-code preference used by unassociated-track presentation,
		// so expose the same state in the SSA when CODES is enabled. Table 2-15
		// permits up to ten entries; VICE lays them out five per line.
		if (filter.All || filter.Codes) && len(p.currentPrefs().SelectedBeacons) > 0 {
			codes := p.currentPrefs().SelectedBeacons
			for i := 0; i < len(codes) && i < 10; i += 5 {
				end := min(i+5, len(codes), 10)
				addLine(strings.Join(codes[i:end], " "), p.colors.List)
			}
		}

		// Field J - System OFF Indicators. Table 2-18 lists capabilities that
		// have been inhibited system-wide. As in VICE, the line is omitted when
		// there are no active indicators; the SYS OFF filter controls visibility
		// only and does not change the underlying system state.
		if filter.All || filter.SysOff {
			addLine(p.ssaSystemOffText(), p.colors.List)
		}

		// Field K - Display Range / PTL value. RANGE and PTL are independently
		// filterable but share one SSA line. Table 2-15 shows the range as nNM;
		// VICE supplies the exact PTL text formatting used here.
		if filter.All || filter.Range || filter.PredictedTrackLines {
			var parts []string
			if filter.All || filter.Range {
				parts = append(parts, fmt.Sprintf("%dNM", int(p.currentPrefs().Range+0.5)))
			}
			if (filter.All || filter.PredictedTrackLines) && p.currentPrefs().PTLLength > 0 {
				parts = append(parts, fmt.Sprintf("PTL: %.1f", p.currentPrefs().PTLLength))
			}
			addLine(strings.Join(parts, " "), p.colors.List)
		}

		// Field L - Altitude Filters. Table 2-15 displays the unassociated
		// range first, followed by U, then the associated range and A. The
		// SSA FILTER <ALT FIL> button controls only this status line; the
		// altitude filters themselves remain active regardless of whether the
		// line is selected for display.
		if filter.All || filter.AltitudeFilters {
			addLine(p.currentPrefs().AltitudeFilters.ssaText(), p.colors.List)
		}

		// Remaining fields between E1 and N are omitted until their corresponding
		// facility, surveillance, flow-management, or controller state exists.

		// Field N - Airport with Altimeter. Table 2-15 allows up to six airports
		// when only one pressure unit is displayed. REDS currently receives the
		// area's adapted SSA airport list from CRC and uses AviationWeather METARs,
		// so each available value is an automatically updated (A) inHg reading.
		// Match VICE's presentation of three airports per line.
		if filter.All || filter.AirportWeather {
			for _, line := range p.ssaAirportWeatherLines() {
				addLine(line, p.colors.List)
			}
		}

		// Field O - Mode of Operation.
		// The initial REDS STARS TCW/TDW runs in operational / normal mode.
		if filter.All || filter.OperationMode {
			addLine("MODE: NORMAL", p.colors.List)
		}

		// Field Q - Quick Look TCPs. Table 2-15 permits up to two lines,
		// with QL+ positions first and a spaced trailing '+' when more TCPs
		// are enabled than can be displayed. The SSA FILTER <QL> button only
		// controls visibility of this status field; it does not alter QL state.
		if (filter.All || filter.QuickLookPositions) && p.hasQuickLookStatus() {
			ql := strings.Split(p.qlPositionsString(), "\n")
			for i, line := range ql {
				if i == 0 {
					line = "QL: " + line
				}
				addLine(line, p.colors.List)
			}
		}

		// Fields S-U - Keyboard Consolidation/Coupling status. Table 2-15
		// assigns these fields to the three TCW/TDW keyboards and makes all
		// of them subject to the single SSA FILTER <CON/CPL> selection. REDS'
		// current live STARS pane represents one operational keyboard/control
		// position, so render that keyboard's current assignment here. The
		// helper is intentionally isolated so a future live SISO/consolidation
		// feed can supply secondary TCPs and CPL state without changing SSA
		// layout/filter behavior.
		if filter.All || filter.Consolidation {
			for _, line := range p.ssaConsolidationCouplingLines() {
				addLine(line, p.colors.List)
			}
		}

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

	// TI 6191.409 Rev. 30, 4.9.1 places the Preview Area in the DATA BLOCKS
	// character-size group rather than the LISTS group.
	fontSize := p.datablockFontSize()
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
