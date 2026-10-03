package stars

import (
	"fmt"
	"sort"
	"strings"
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	redsnet "github.com/juliusplatzer/reds/net"
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
// TI 6191.409 Rev. 30, Table 2-18 defines INTRAIL as the system-off indicator
// when ATPA processing is inhibited site-wide. Other site-wide inhibits can be
// added here as REDS gains their authoritative state.
func (p *STARSPane) ssaSystemOffText() string {
	if p != nil && p.atpaAdapted() && !p.atpaEnabled() {
		return "INTRAIL"
	}
	return ""
}

// ssaChopLong applies the Table 2-15 continuation modality used by the ATPA
// M2/M3 fields: if the line cannot fit, terminate the visible list with '+'.
// VICE uses the STARS 32-character SSA field width, so mirror it here.
func ssaChopLong(text string) string {
	if len(text) <= 32 {
		return text
	}
	text = text[:32]
	if i := strings.LastIndexByte(text, ' '); i >= 0 {
		return text[:i] + "+"
	}
	return text
}

// ssaATPAInTrailText implements Table 2-15 field M2. When ATPA is enabled,
// positions adapted for at least one volume show INTRAIL ON unless one or more
// volumes of interest have been manually disabled; in that case those IDs are
// listed instead.
func (p *STARSPane) ssaATPAInTrailText() string {
	if p == nil || !p.atpaEnabled() {
		return ""
	}
	var disabled []string
	hasEnabled := false
	for i := range p.config.Facility.ATPAVolumes {
		volume := &p.config.Facility.ATPAVolumes[i]
		if !p.atpaVolumeOfInterest(volume) {
			continue
		}
		if p.atpaVolumeEnabled(volume) {
			hasEnabled = true
		} else if id := atpaVolumeID(volume); id != "" {
			disabled = append(disabled, id)
		}
	}
	if len(disabled) != 0 {
		return ssaChopLong("INTRAIL OFF: " + strings.Join(disabled, " "))
	}
	if hasEnabled {
		return "INTRAIL ON"
	}
	return ""
}

// ssaATPA25Text implements Table 2-15 field M3. Only enabled, adapted volumes
// of interest whose 2.5-NM reduced-separation state is currently enabled are
// listed. An empty set removes the M3 line entirely.
func (p *STARSPane) ssaATPA25Text() string {
	if p == nil || !p.atpaEnabled() {
		return ""
	}
	var enabled []string
	for i := range p.config.Facility.ATPAVolumes {
		volume := &p.config.Facility.ATPAVolumes[i]
		if !p.atpaVolumeOfInterest(volume) || !p.atpaVolume25Enabled(volume) {
			continue
		}
		if id := atpaVolumeID(volume); id != "" {
			enabled = append(enabled, id)
		}
	}
	if len(enabled) == 0 {
		return ""
	}
	return ssaChopLong("INTRAIL 2.5 ON: " + strings.Join(enabled, " "))
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
				// MeasureText reports the visible width of the final glyph rather than
				// its full advance. Measure with a visible sentinel and subtract that
				// sentinel's width to recover the exact pen advance of this segment,
				// including one intentional trailing space between STATUS and RADAR.
				widthWithSentinel, _ := p.systemFont.MeasureText(segment.text+"X", fontSize)
				sentinelWidth, _ := p.systemFont.MeasureText("X", fontSize)
				x += float32(widthWithSentinel - sentinelWidth)
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

		// Fields M2/M3 - ATPA In-trail Approach Volume and 2.5-NM Reduced
		// Separation status (Table 2-15). These lines are suppressed whenever
		// ATPA is disabled system-wide. INTRAIL and 2.5 are independently
		// selectable in the SSA FILTER submenu.
		if filter.All || filter.Intrail {
			addLine(p.ssaATPAInTrailText(), p.colors.List)
		}
		if filter.All || filter.Intrail25 {
			addLine(p.ssaATPA25Text(), p.colors.List)
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

const coastSuspendTabLineCapacity = 100

type coastSuspendListEntry struct {
	target           *redsnet.TaisTarget
	lineNumber       int
	status           byte
	frozenBeforeList bool
}

// coastSuspendListEntries implements TI 6191.409 Rev. 30 section 2.15.2:
// suspended flights owned by the controller, plus Coast Phase 2 tracks owned
// by the controller. A center-owned coasting track also appears at a pending
// receiver and at the previous owner, which maps to REDS' inbound-pending and
// post-acceptance ownership states.
//
// Real STARS tab line numbers are system data (CRC receives them separately as
// StarsLineNumberDto). The current REDS TAIS parser does not carry an
// authoritative tab line number, so REDS allocates stable display-local 0..99
// numbers until that data is available.
func (p *STARSPane) coastSuspendListEntries(snapshot redsnet.TaisSnapshot) []coastSuspendListEntry {
	if p == nil || !snapshot.Ready {
		return nil
	}
	if p.coastSuspendLineNumbers == nil {
		p.coastSuspendLineNumbers = make(map[string]int)
	}
	if p.coastSuspendFirstSeen == nil {
		p.coastSuspendFirstSeen = make(map[string]uint64)
	}
	if p.coastSuspendFrozenAtEntry == nil {
		p.coastSuspendFrozenAtEntry = make(map[string]bool)
	}

	targets := make(map[string]*redsnet.TaisTarget)
	statuses := make(map[string]byte)
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if target.FlightPlan == nil || target.FlightPlan.Deleted {
			continue
		}

		status := byte(0)
		if target.FlightPlan.Suspended {
			// Section 2.15.2 lists suspended flights owned by the controller.
			if !p.targetOwnedByCurrentTCP(target) {
				continue
			}
			status = 'S'
		} else if target.CoastPhase >= redsnet.TaisCoastPhase2 {
			// Center-owned Coast Phase 2 tracks also appear in the pending and
			// previous owner's Coast/Suspend lists.
			if !p.targetOwnedByCurrentTCP(target) && !p.targetInboundHandoff(target) &&
				!p.targetOutboundHandoffAccepted(target) {
				continue
			}
			status = 'C'
		} else {
			continue
		}

		key := targetDisplayStateKey(target)
		if key == "" {
			// Flight-plan-only records normally retain a TAIS target key. Keep a
			// deterministic fallback for feeds where that key is absent.
			acid := strings.ToUpper(strings.TrimSpace(target.FlightPlan.ACID))
			if target.FlightPlan.SFPN > 0 {
				key = fmt.Sprintf("%s:FP:%d", strings.TrimSpace(target.Facility), target.FlightPlan.SFPN)
			} else if acid != "" {
				key = strings.TrimSpace(target.Facility) + ":FP:" + acid
			}
		}
		if key == "" {
			continue
		}
		targets[key] = target
		statuses[key] = status
	}

	// List-lifetime state is released as soon as a flight leaves the list. If
	// it later re-enters, it is a newly added entry and receives a new ordering
	// token (and potentially a new tab line number).
	for key := range p.coastSuspendLineNumbers {
		if _, ok := targets[key]; !ok {
			delete(p.coastSuspendLineNumbers, key)
		}
	}
	for key := range p.coastSuspendFirstSeen {
		if _, ok := targets[key]; !ok {
			delete(p.coastSuspendFirstSeen, key)
			delete(p.coastSuspendFrozenAtEntry, key)
		}
	}

	// On a fresh client snapshot several already-existing rows can appear at
	// once. Coast Phase 2 has an authoritative transition time from the server;
	// suspended records use the TAIS receipt time as the best available entry
	// time. The manual requires oldest at the top, newest at the bottom.
	entryTime := func(key string) time.Time {
		target := targets[key]
		if statuses[key] == 'C' && !target.CoastPhase2At.IsZero() {
			return target.CoastPhase2At
		}
		return target.ReceivedAt
	}
	var newKeys []string
	for key := range targets {
		if _, ok := p.coastSuspendFirstSeen[key]; !ok {
			newKeys = append(newKeys, key)
		}
	}
	sort.SliceStable(newKeys, func(i, j int) bool {
		aTime, bTime := entryTime(newKeys[i]), entryTime(newKeys[j])
		if !aTime.Equal(bTime) {
			if aTime.IsZero() {
				return false
			}
			if bTime.IsZero() {
				return true
			}
			return aTime.Before(bTime)
		}
		a, b := targets[newKeys[i]], targets[newKeys[j]]
		aacid := strings.ToUpper(strings.TrimSpace(a.FlightPlan.ACID))
		bacid := strings.ToUpper(strings.TrimSpace(b.FlightPlan.ACID))
		if aacid != bacid {
			return aacid < bacid
		}
		return newKeys[i] < newKeys[j]
	})
	for _, key := range newKeys {
		p.coastSuspendSequence++
		p.coastSuspendFirstSeen[key] = p.coastSuspendSequence
		// Figure 2-30 defines ZZ as a flight that was frozen before entering
		// the list. Snapshot the flag now instead of letting a later update
		// retroactively change the list condition.
		p.coastSuspendFrozenAtEntry[key] = targets[key].Track.Frozen
	}

	var used [coastSuspendTabLineCapacity]bool
	for key, line := range p.coastSuspendLineNumbers {
		if _, ok := targets[key]; !ok || line < 0 || line >= len(used) || used[line] {
			delete(p.coastSuspendLineNumbers, key)
			continue
		}
		used[line] = true
	}

	orderedKeys := make([]string, 0, len(targets))
	for key := range targets {
		orderedKeys = append(orderedKeys, key)
	}
	sort.SliceStable(orderedKeys, func(i, j int) bool {
		return p.coastSuspendFirstSeen[orderedKeys[i]] < p.coastSuspendFirstSeen[orderedKeys[j]]
	})

	// Allocate line numbers in list-addition order. STARS has 100 tab line
	// numbers. When all are occupied the list row's number field is blank; the
	// corresponding suspended target-symbol behavior can be added when REDS
	// implements the suspended position symbol itself.
	for _, key := range orderedKeys {
		if _, ok := p.coastSuspendLineNumbers[key]; ok {
			continue
		}
		for step := 0; step < coastSuspendTabLineCapacity; step++ {
			line := (p.coastSuspendNextLine + step) % coastSuspendTabLineCapacity
			if used[line] {
				continue
			}
			p.coastSuspendLineNumbers[key] = line
			used[line] = true
			p.coastSuspendNextLine = (line + 1) % coastSuspendTabLineCapacity
			break
		}
	}

	entries := make([]coastSuspendListEntry, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		line := -1
		if assigned, ok := p.coastSuspendLineNumbers[key]; ok {
			line = assigned
		}
		entries = append(entries, coastSuspendListEntry{
			target:           targets[key],
			lineNumber:       line,
			status:           statuses[key],
			frozenBeforeList: p.coastSuspendFrozenAtEntry[key],
		})
	}
	return entries
}

func coastSuspendBeaconCode(entry coastSuspendListEntry) string {
	target := entry.target
	if target == nil || target.FlightPlan == nil {
		return ""
	}
	// A coast row represents the last tracked target, so prefer its reported
	// Mode 3/A. A suspended flight-plan row follows VICE/CRC practice and uses
	// its assigned code, falling back to the other source only when necessary.
	if entry.status == 'C' {
		if code := normalizeBeaconCode(target.Track.ReportedBeaconCode); code != "" {
			return code
		}
		return normalizeBeaconCode(target.FlightPlan.AssignedBeaconCode)
	}
	if code := normalizeBeaconCode(target.FlightPlan.AssignedBeaconCode); code != "" {
		return code
	}
	return normalizeBeaconCode(target.Track.ReportedBeaconCode)
}

func coastSuspendSupplement(entry coastSuspendListEntry) string {
	target := entry.target
	if target == nil {
		return ""
	}

	// Figure 2-30 defines these conditions directly. Current TAIS exposes enough
	// state for NT, CST and ZZ, plus ordinary surveillance altitude. It does not
	// expose a pilot-reported-altitude value/provenance, out-of-range status,
	// primary-only status, or the specific beacon/IFDT disassociation cause
	// required for OR, RDR, the '*' altitude suffix, or SDBC. Those values must
	// remain absent rather than be inferred from unrelated surveillance fields.
	if entry.status == 'S' && target.CoastPhase >= redsnet.TaisCoastPhase1 {
		return "CST"
	}
	if !taisTargetHasPosition(target) {
		return "NT"
	}
	if entry.frozenBeforeList {
		return "ZZ"
	}
	if target.Track.ReportedAltitude != 0 {
		return targetDatablockAltitude(target.Track.ReportedAltitude)
	}
	return ""
}

func (p *STARSPane) coastSuspendListText(snapshot redsnet.TaisSnapshot) string {
	if p == nil {
		return ""
	}
	entries := p.coastSuspendListEntries(snapshot)
	list := p.currentPrefs().CoastSuspendList
	if !list.Visible {
		return ""
	}

	var text strings.Builder
	text.WriteString("COAST/SUSPEND\n")
	limit := min(list.Lines, len(entries))
	for i := 0; i < limit; i++ {
		entry := entries[i]
		target := entry.target
		acid := strings.ToUpper(strings.TrimSpace(target.FlightPlan.ACID))
		if len(acid) > 7 {
			acid = acid[:7]
		}
		line := "  "
		if entry.lineNumber >= 0 {
			line = fmt.Sprintf("%2d", entry.lineNumber)
		}
		fmt.Fprintf(&text, "%s %-7s %c %4s", line, acid, entry.status, coastSuspendBeaconCode(entry))
		if supplement := coastSuspendSupplement(entry); supplement != "" {
			text.WriteByte(' ')
			text.WriteString(supplement)
		}
		if i+1 < limit {
			text.WriteByte('\n')
		}
	}
	return strings.TrimRight(text.String(), "\n")
}

// drawCoastSuspendList uses the standard STARS list modalities: LISTS
// character size and brightness, and the green List/List Title color from
// Appendix B. The title remains visible even when there are no entries.
func (p *STARSPane) drawCoastSuspendList(ctx *panes.Context, zcb *renderer.ZCmdBuffer, snapshot redsnet.TaisSnapshot) {
	if p == nil || ctx == nil || zcb == nil || p.systemFont == nil {
		return
	}

	text := p.coastSuspendListText(snapshot)
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
	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zLists)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())

	td := renderer.GetTextDrawBuilder()
	td.SetFont(p.systemFont)
	td.AddText(text, redsmath.Vec2{
		X: ps.CoastSuspendList.Position[0] * w,
		Y: ps.CoastSuspendList.Position[1] * h,
	}, renderer.TextStyle{
		Size:  fontSize,
		Color: ps.Brightness.Lists.ScaleRGB(p.colors.List).ToRGBA(),
	})
	td.GenerateCommands(cb, texture)
	renderer.ReturnTextDrawBuilder(td)
	cb.DisableScissor()
}

// towerListAirports resolves the three tower-list slots REDS can derive from
// the current CRC-generated adaptation. Real STARS carries an explicit adapted
// tower-list identifier/airport assignment; crc2reds does not export that
// field yet. Until it does, use the area's adapted underlying-airport order,
// restricted to airports for which the facility exposes a Tower position.
// This preserves a stable per-position assignment and avoids manufacturing
// tower lists for small non-towered underlying airports. VICE likewise limits
// the display to three tower lists.
func (p *STARSPane) towerListAirports() []string {
	if p == nil {
		return nil
	}

	towered := make(map[string]bool)
	for _, position := range p.config.Facility.ControlPositions {
		callsign := strings.ToUpper(strings.TrimSpace(position.Callsign))
		name := strings.ToUpper(strings.TrimSpace(position.Name + " " + position.RadioName))
		if !strings.Contains(callsign, "_TWR") && !strings.Contains(name, "TOWER") {
			continue
		}
		airport := starsAirportDisplayID(strings.ToUpper(strings.TrimSpace(position.PhysicalFacility)))
		if airport != "" {
			towered[airport] = true
		}
	}

	db, dbErr := loadSTARSAirportDatabase()
	seen := make(map[string]bool)
	var out []string
	appendAirport := func(raw string, requireTower bool) {
		if len(out) >= 3 {
			return
		}
		airport := starsAirportDisplayID(strings.ToUpper(strings.TrimSpace(raw)))
		if airport == "" || seen[airport] || (requireTower && len(towered) != 0 && !towered[airport]) {
			return
		}
		if dbErr == nil {
			if _, ok := db.positionByID[airport]; !ok {
				return
			}
		}
		seen[airport] = true
		out = append(out, airport)
	}

	for _, airport := range p.config.Area.UnderlyingAirports {
		appendAirport(airport, true)
	}
	// A handful of adaptations do not expose Tower positions in the same child
	// facility even though the area still has adapted airports. Keep the feature
	// useful in that case without changing the stable underlying-airport order.
	if len(out) == 0 {
		for _, airport := range p.config.Area.UnderlyingAirports {
			appendAirport(airport, false)
		}
	}
	return out
}

// towerListIndex maps the operator-entered tower-list identifier to one of the
// three local list slots. The operator manual makes this a site-adapted 1-3
// alphanumeric identifier; CRC's explicit identifier is not exported by
// crc2reds yet. VICE uses 1/2/3 for the three slots, so REDS uses those same
// identifiers until the real adaptation is available.
func (p *STARSPane) towerListIndex(identifier string) (int, bool) {
	identifier = strings.ToUpper(strings.TrimSpace(identifier))
	airports := p.towerListAirports()
	if len(identifier) != 1 || identifier[0] < '1' || identifier[0] > '3' {
		return 0, false
	}
	idx := int(identifier[0] - '1')
	return idx, idx < len(airports)
}

func towerListArrivalAirport(target *redsnet.TaisTarget) string {
	if target == nil || target.FlightPlan == nil || target.FlightPlan.Deleted || target.FlightPlan.Suspended {
		return ""
	}
	if taisFlightPlanIsDeparture(target) {
		return ""
	}
	if target.EnhancedData != nil {
		if airport := starsAirportDisplayID(strings.ToUpper(strings.TrimSpace(target.EnhancedData.DestinationAirport))); airport != "" {
			return airport
		}
	}
	return starsAirportDisplayID(strings.ToUpper(strings.TrimSpace(target.FlightPlan.Airport)))
}

type towerListEntry struct {
	distance float64
	acid     string
	acType   string
}

func (p *STARSPane) towerListEntries(airport string, snapshot redsnet.TaisSnapshot) []towerListEntry {
	db, err := loadSTARSAirportDatabase()
	if err != nil {
		return nil
	}
	airport = starsAirportDisplayID(strings.ToUpper(strings.TrimSpace(airport)))
	airportPos, ok := db.positionByID[airport]
	if !ok {
		return nil
	}

	entries := make([]towerListEntry, 0)
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if towerListArrivalAirport(target) != airport || !validTargetLatLon(target.Track.Lat, target.Track.Lon) {
			continue
		}
		acid := strings.ToUpper(strings.TrimSpace(target.FlightPlan.ACID))
		if acid == "" {
			continue
		}
		entries = append(entries, towerListEntry{
			distance: starsRBLNMDistance(airportPos, configPoint{Lat: target.Track.Lat, Lon: target.Track.Lon}),
			acid:     acid,
			acType:   strings.ToUpper(strings.TrimSpace(target.FlightPlan.ACType)),
		})
	}

	// VICE's STARS implementation orders each Tower list by increasing
	// distance to the arrival airport. Use ACID as a deterministic tie-breaker;
	// the real operator manual does not specify a secondary ordering rule.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].distance != entries[j].distance {
			return entries[i].distance < entries[j].distance
		}
		return entries[i].acid < entries[j].acid
	})
	return entries
}

func (p *STARSPane) towerListText(airport string, lines int, snapshot redsnet.TaisSnapshot) string {
	if lines < 1 {
		return ""
	}
	entries := p.towerListEntries(airport, snapshot)
	var text strings.Builder
	fmt.Fprintf(&text, "%s TOWER\n", airport)
	for i := 0; i < len(entries) && i < lines; i++ {
		// VICE's default Tower-list format is [ACID] [ACTYPE]: ACID occupies
		// seven columns and aircraft type four, right aligned.
		fmt.Fprintf(&text, "%-7s %4s\n", entries[i].acid, entries[i].acType)
	}
	return strings.TrimRight(text.String(), "\n")
}

func (p *STARSPane) drawTowerLists(ctx *panes.Context, zcb *renderer.ZCmdBuffer, snapshot redsnet.TaisSnapshot) {
	if p == nil || ctx == nil || zcb == nil || p.systemFont == nil {
		return
	}
	airports := p.towerListAirports()
	if len(airports) == 0 {
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

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zLists)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())

	ps := p.currentPrefs()
	td := renderer.GetTextDrawBuilder()
	td.SetFont(p.systemFont)
	for i, airport := range airports {
		if i >= len(ps.TowerLists) || !ps.TowerLists[i].Visible {
			continue
		}
		text := p.towerListText(airport, ps.TowerLists[i].Lines, snapshot)
		if text == "" {
			continue
		}
		td.AddText(text, redsmath.Vec2{
			X: ps.TowerLists[i].Position[0] * w,
			Y: ps.TowerLists[i].Position[1] * h,
		}, renderer.TextStyle{
			Size:  fontSize,
			Color: ps.Brightness.Lists.ScaleRGB(p.colors.List).ToRGBA(),
		})
	}
	td.GenerateCommands(cb, texture)
	renderer.ReturnTextDrawBuilder(td)
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
