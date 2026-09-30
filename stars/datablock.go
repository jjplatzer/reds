package stars

import (
	"fmt"
	stdmath "math"
	"strings"
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	redsnet "github.com/juliusplatzer/reds/net"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

type specialConditionClass uint8

const (
	specialConditionNone specialConditionClass = iota
	specialConditionPrimary
	specialConditionSecondary
)

type specialConditionIndicator struct {
	Abbreviation string
	Class        specialConditionClass
}

// targetSpecialConditionIndicator maps the standard Mode 3/A special-condition
// beacon codes to the STARS abbreviations in TI 6191.409 Rev. 30, Table 2-25.
//
//	7400 -> LL  UAS Lost Link              (primary / alert red)
//	7500 -> HJ  Hijack Situation           (primary / alert red)
//	7600 -> RF  Radio Communications Fail  (primary / alert red)
//	7700 -> EM  General Emergency          (primary / alert red)
//	7777 -> MI  Military Intercept         (secondary / caution yellow)
//
// LL=7400 is specified by JO 7110.65 for STARS/MEARTS. 7777 is reserved for
// military interceptor operations; Table 2-25 defines MI as the corresponding
// standard secondary Special Condition abbreviation. Facility-adapted SPC
// beacon codes are intentionally not guessed here because REDS does not yet
// carry that STARS adaptation data.
func targetSpecialConditionIndicator(target *redsnet.TaisTarget) (specialConditionIndicator, bool) {
	if target == nil {
		return specialConditionIndicator{}, false
	}

	switch strings.TrimSpace(target.Track.ReportedBeaconCode) {
	case "7400":
		return specialConditionIndicator{Abbreviation: "LL", Class: specialConditionPrimary}, true
	case "7500":
		return specialConditionIndicator{Abbreviation: "HJ", Class: specialConditionPrimary}, true
	case "7600":
		return specialConditionIndicator{Abbreviation: "RF", Class: specialConditionPrimary}, true
	case "7700":
		return specialConditionIndicator{Abbreviation: "EM", Class: specialConditionPrimary}, true
	case "7777":
		return specialConditionIndicator{Abbreviation: "MI", Class: specialConditionSecondary}, true
	default:
		return specialConditionIndicator{}, false
	}
}

// TI 6191.409 2.16.2 uses a half-second cadence while an SPC remains
// unacknowledged. Once acknowledged, drawDatablocks keeps the abbreviation
// continuously visible in its alert/caution color.
func specialConditionBlinkVisible(now time.Time) bool {
	return (now.UnixMilli()/500)&1 != 0
}

// targetDatablockType is the subset of the STARS data-block presentation that
// REDS can determine directly from TAIS today. TI 6191.409 Rev. 30 section
// 2.12 defines Full, Partial, and Limited data blocks. Single-track quick look
// (6.13.4) is modeled as transient local display state. OCR-backed handoff
// attention is also modeled; pointouts, alerts, and the remaining force-FDB
// cases remain future additions.
type targetDatablockType uint8

const (
	targetDatablockFull targetDatablockType = iota
	targetDatablockPartial
	targetDatablockLimited
)

// drawDatablocks renders the STARS data-block subset REDS can derive from
// TAIS today. The field layout follows TI 6191.409 Rev. 30 figures 2-20, 2-22,
// and 2-23. In addition to the surveillance fields, TAIS supplies both
// scratchpads and the assigned altitude, so the corresponding FDB/PDB fields
// participate in the normal STARS clock-phase timesharing:
//
//	Full (owned associated):      ACID
//	                              altitude/scratchpad  ground-speed/type
//	                                      Axxx
//
//	Partial (unowned associated): altitude/scratchpad  ground-speed
//
//	Limited (unassociated):       beacon code
//	                              altitude ground-speed
//
// Ground speed is displayed in tens of knots, as specified by the manual.
// VICE's placement modality is retained: the leader endpoint anchors the data
// block, text is offset four display pixels from it, and west-side leaders
// right-justify the block. Text remains fixed in screen space when the scope is
// zoomed.
func (p *STARSPane) drawDatablocks(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.LatLonTransformations,
	snapshot redsnet.TaisSnapshot,
	now time.Time,
) {
	if p == nil || ctx == nil || zcb == nil || !snapshot.Ready || p.systemFont == nil {
		return
	}

	fontSize := p.datablockFontSize()
	fontTexture := p.systemFontTexture(ctx.Renderer, fontSize)
	if fontTexture == 0 {
		return
	}
	lineHeight := p.systemFont.LineHeight(fontSize)
	if lineHeight <= 0 {
		return
	}

	td := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(td)
	td.SetFont(p.systemFont)

	// STARS timeshared fields use one facility-wide clock phase. TI 6191.409
	// defines the field contents but leaves the clock timing to adaptation.
	// Use VICE's STARS fallback adaptation until crc2reds carries the site's
	// clock-phase sequence and intervals. Use the same frame timestamp for
	// temporary 6.13.2 LDB beacon readouts and handoff-attention flashing.
	clockPhase := defaultSTARSDataBlockClockPhase(now)

	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if !taisTargetHasPosition(target) {
			continue
		}
		// Suspended tracks have a separate suspended data-block presentation in
		// STARS/VICE. Do not show a normal F/PDB until that presentation exists.
		if target.FlightPlan != nil && target.FlightPlan.Suspended {
			continue
		}
		if !p.targetAltitudeFilterAllowsDatablock(target, now) {
			continue
		}

		dbType, color, brightness := p.targetDatablockPresentation(target)
		brightness = p.targetHandoffBrightness(target, brightness, now)
		if brightness == 0 {
			continue
		}

		center := transforms.WindowFromLatLon(target.Track.Lat, target.Track.Lon)
		if !targetCenterNearPane(ctx, center, starsLeaderLineLengthPixels(p.currentPrefs().LeaderLineLength)+220) {
			continue
		}

		direction := p.targetLeaderLineDirection(target)
		anchor := p.targetDatablockAnchor(center, direction)
		style := renderer.TextStyle{
			Size:  fontSize,
			Color: brightness.ScaleRGB(color).ToRGBA(),
		}

		spc, hasSPC := targetSpecialConditionIndicator(target)
		spcAck, spcAcknowledged := p.targetSPCAcknowledgement(target)
		spcStyle := style
		if hasSPC {
			spcColor := p.colors.AlertDatablock
			if spc.Class == specialConditionSecondary && !(spcAcknowledged && spcAck.ForceAlertColor) {
				spcColor = p.colors.CautionDatablock
			}
			spcStyle.Color = brightness.ScaleRGB(spcColor).ToRGBA()
		}

		switch dbType {
		case targetDatablockFull:
			// Figure 2-20 / section 2.16.2: line zero is immediately above
			// the ACID line and carries Special Condition alert/caution text.
			// The leader remains aligned with the ACID line.
			if hasSPC && (spcAcknowledged || specialConditionBlinkVisible(now)) {
				p.addDatablockLine(td, spc.Abbreviation, anchor, direction, -1, 0, lineHeight, spcStyle)
			}
			line1 := targetDatablockACID(target)
			line2 := p.targetFullDatablockLine2(target, clockPhase)
			line3 := targetFullDatablockLine3(target, direction)
			p.addDatablockLine(td, line1, anchor, direction, 0, 0, lineHeight, style)
			p.addDatablockLine(td, line2, anchor, direction, 1, 0, lineHeight, style)
			p.addDatablockLine(td, line3, anchor, direction, 2, 0, lineHeight, style)

		case targetDatablockPartial:
			// Figure 2-22: an ordinary unowned associated track does not show
			// ACID in its PDB. Field 1 timeshares Mode-C altitude and primary
			// scratchpad; the secondary scratchpad is a site-adapted PDB option
			// and stays disabled until REDS carries that adaptation.
			line := targetPartialDatablockLine1(target, clockPhase)
			p.addDatablockLine(td, line, anchor, direction, 0, 0, lineHeight, style)

		case targetDatablockLimited:
			// Figure 2-23: the leader aligns with the altitude/ground-speed
			// line. An active Special Condition occupies fixed line zero and,
			// as in STARS/VICE, also expands the LDB so the current beacon code
			// is visible in field 1 even when normal LDB beacon display is off.
			if hasSPC && (spcAcknowledged || specialConditionBlinkVisible(now)) {
				p.addDatablockLine(td, spc.Abbreviation, anchor, direction, -1, 1, lineHeight, spcStyle)
			}
			if hasSPC || p.currentPrefs().DisplayLDBBeaconCodes || p.targetLDBBeaconReadoutActive(target, now) {
				line1 := normalizeBeaconCode(target.Track.ReportedBeaconCode)
				p.addDatablockLine(td, line1, anchor, direction, 0, 1, lineHeight, style)
			}
			line2 := targetDatablockAltitudeGroundSpeed(target)
			p.addDatablockLine(td, line2, anchor, direction, 1, 1, lineHeight, style)
		}
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zTargetDatablock)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	td.GenerateCommands(cb, fontTexture)
	cb.DisableScissor()
}

func altitudeWithinFilter(altitudeFeet int, filter [2]int) bool {
	return altitudeFeet >= filter[0] && altitudeFeet <= filter[1]
}

// targetHasAltitudeFilterSPC covers the universal emergency Mode 3/A codes
// that STARS treats as special-purpose-code conditions. Facility-adapted SPCs
// can be added when REDS carries that adaptation data.
func targetHasAltitudeFilterSPC(target *redsnet.TaisTarget) bool {
	_, ok := targetSpecialConditionIndicator(target)
	return ok
}

// targetAltitudeFilterAllowsDatablock mirrors VICE's placement of altitude
// filtering in datablock presentation rather than in a standalone subsystem.
// The target symbol remains visible outside the filter; the ordinary data
// block, leader and history presentation are suppressed. FDB modalities,
// emergency/SPC tracks, and the five-second LDB beacon readout bypass it.
func (p *STARSPane) targetAltitudeFilterAllowsDatablock(target *redsnet.TaisTarget, now time.Time) bool {
	if p == nil || target == nil {
		return false
	}

	if target.FlightPlan == nil && p.targetLDBBeaconReadoutActive(target, now) {
		return true
	}
	if targetHasAltitudeFilterSPC(target) {
		return true
	}

	ps := p.currentPrefs()
	if target.FlightPlan != nil {
		dbType, _, _ := p.targetDatablockPresentation(target)
		if dbType == targetDatablockFull {
			return true
		}
		return altitudeWithinFilter(target.Track.ReportedAltitude, ps.AltitudeFilters.Associated)
	}

	return altitudeWithinFilter(target.Track.ReportedAltitude, ps.AltitudeFilters.Unassociated)
}

// targetDatablockPresentation maps TAIS association/ownership plus the local
// quick-look state to the operator-manual data-block categories:
//
//	own associated track          -> Full data block, white, FDB brightness
//	inbound pending handoff       -> Full data block, blinking white, FDB brightness
//	accepted at former owner      -> Full data block, white, FDB brightness
//	other associated track        -> Partial data block, green, LDB brightness
//	quick-looked other track      -> Full data block, green, OTH brightness
//	quick-look-plus other track   -> Full data block, white, OTH brightness
//	unassociated track            -> Limited data block, green, LDB brightness
//
// TI 6191.409 Table 4-1 explicitly assigns Partial and Limited data blocks to
// LDB brightness and all unowned FDBs (including their position symbols) to
// OTH brightness. Appendix B defines both unowned FDB and non-FDB TCW text as
// green.
func (p *STARSPane) targetDatablockPresentation(target *redsnet.TaisTarget) (targetDatablockType, renderer.RGB, Brightness) {
	if p == nil || target == nil {
		return targetDatablockLimited, renderer.RGB{}, 0
	}

	ps := p.currentPrefs()
	if target.FlightPlan == nil {
		return targetDatablockLimited, p.colors.UnownedDatablock, ps.Brightness.LimitedDatablocks
	}

	if p.targetOwnedByCurrentTCP(target) || p.targetInboundHandoff(target) || p.targetOutboundHandoffAccepted(target) {
		return targetDatablockFull, p.colors.OwnedDatablock, ps.Brightness.FullDatablocks
	}
	if quickLooked, plus := p.targetQuickLookState(target); quickLooked {
		color := p.colors.UnownedDatablock
		if plus {
			color = p.colors.OwnedDatablock
		}
		return targetDatablockFull, color, ps.Brightness.OtherTracks
	}
	// TI 6191.409 2.16.2: an associated track with an active Special
	// Condition is presented as an FDB so its line-zero warning is visible.
	// If another controller owns it, retain the normal unowned FDB color and
	// OTH brightness; only the SPC abbreviation itself is alert/caution color.
	if _, ok := targetSpecialConditionIndicator(target); ok {
		return targetDatablockFull, p.colors.UnownedDatablock, ps.Brightness.OtherTracks
	}
	return targetDatablockPartial, p.colors.UnownedDatablock, ps.Brightness.LimitedDatablocks
}

// targetDatablockAnchor returns the leader endpoint used by the data block. If
// LDR LEN is zero there is no line, but VICE/STARS still keeps the text clear
// of the target by moving it to the appropriate side of the position symbol.
func (p *STARSPane) targetDatablockAnchor(center redsmath.Vec2, direction leaderLineDirection) redsmath.Vec2 {
	length := starsLeaderLineLengthPixels(p.currentPrefs().LeaderLineLength)
	if length > 0 {
		unit := leaderLineUnitVector(direction)
		return redsmath.Vec2{
			X: center.X + unit.X*length,
			Y: center.Y + unit.Y*length,
		}
	}

	offset := starsTargetDiameterPixels*0.5 + 4
	if datablockRightJustified(direction) {
		return redsmath.Vec2{X: center.X - offset, Y: center.Y}
	}
	return redsmath.Vec2{X: center.X + offset, Y: center.Y}
}

func (p *STARSPane) addDatablockLine(
	td *renderer.TextDrawBuilder,
	text string,
	anchor redsmath.Vec2,
	direction leaderLineDirection,
	lineIndex int,
	anchorLine int,
	lineHeight int,
	style renderer.TextStyle,
) {
	if p == nil || td == nil || text == "" {
		return
	}

	width, _ := p.systemFont.MeasureText(text, style.Size)
	x := anchor.X + 4
	if datablockRightJustified(direction) {
		x = anchor.X - 4 - float32(width)
	}

	// REDS uses top-origin window coordinates. Place the center of anchorLine
	// on the leader endpoint, then step later lines downward by one cell.
	y := anchor.Y - float32(lineHeight)/2 + float32(lineIndex-anchorLine)*float32(lineHeight)
	td.AddText(text, redsmath.Vec2{X: x, Y: y}, style)
}

// VICE right-justifies datablocks for S/SW/W/NW leader orientations. Its
// direction enum uses the same clockwise ordering as REDS.
func datablockRightJustified(direction leaderLineDirection) bool {
	return direction >= leaderLineDirectionSouth
}

func targetDatablockACID(target *redsnet.TaisTarget) string {
	if target == nil {
		return ""
	}
	if target.FlightPlan != nil {
		if acid := strings.ToUpper(strings.TrimSpace(target.FlightPlan.ACID)); acid != "" {
			return acid
		}
		if code := normalizeBeaconCode(target.FlightPlan.AssignedBeaconCode); code != "" {
			return code
		}
	}
	return normalizeBeaconCode(target.Track.ReportedBeaconCode)
}

func targetDatablockAltitudeGroundSpeed(target *redsnet.TaisTarget) string {
	if target == nil {
		return ""
	}
	return targetDatablockAltitude(target.Track.ReportedAltitude) + " " + targetDatablockGroundSpeed(target.Track.VX, target.Track.VY)
}

// STARS displays Mode-C altitude in hundreds of feet. Follow VICE's rounding
// convention; negative altitudes use the normal Nxx presentation.
func targetDatablockAltitude(altitudeFeet int) string {
	if altitudeFeet < 0 {
		return fmt.Sprintf("N%02d", (-altitudeFeet+50)/100)
	}
	return fmt.Sprintf("%03d", (altitudeFeet+50)/100)
}

// TAIS vx/vy are the horizontal velocity components used by STARS. Convert
// their magnitude to the two-character ground-speed field in tens of knots,
// matching VICE and TI 6191.409 figures 2-20/2-22/2-23.
func targetDatablockGroundSpeed(vx, vy int) string {
	groundSpeed := stdmath.Hypot(float64(vx), float64(vy))
	return fmt.Sprintf("%02d", int(groundSpeed+5)/10)
}

// VICE's STARS fallback clock-phase adaptation is 1,2,1,3 with durations
// 2s,1s,2s,1s. The operator manual identifies the affected fields as
// timeshared, while the exact sequence/durations are site adaptation.
func defaultSTARSDataBlockClockPhase(now time.Time) int {
	const cycle = 6 * time.Second
	pos := time.Duration(now.UnixNano() % int64(cycle))
	switch {
	case pos < 2*time.Second:
		return 1
	case pos < 3*time.Second:
		return 2
	case pos < 5*time.Second:
		return 1
	default:
		return 3
	}
}

// normalizeDatablockScratchpad returns the default three-character STARS
// scratchpad presentation. TI 6191.409 allows four characters only when that
// site adaptation is enabled; REDS does not carry that adaptation yet, so use
// VICE's normal/default three-character width.
func normalizeDatablockScratchpad(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) > 3 {
		runes = runes[:3]
	}
	return string(runes)
}

func padDatablockField(s string, width int) string {
	runes := []rune(s)
	if len(runes) > width {
		runes = runes[:width]
	}
	for len(runes) < width {
		runes = append(runes, ' ')
	}
	return string(runes)
}

// targetPrimaryScratchpad mirrors VICE's priority: a live STARS scratchpad
// wins; when none is present, REDS keeps its current exit-fix fallback for
// departures. Treating the fallback as scratchpad content makes it timeshare
// with Mode-C altitude rather than replacing altitude in every phase.
func targetPrimaryScratchpad(target *redsnet.TaisTarget) string {
	if target == nil || target.FlightPlan == nil {
		return ""
	}
	if sp := normalizeDatablockScratchpad(target.FlightPlan.ScratchPad1); sp != "" {
		return sp
	}
	if taisFlightPlanIsDeparture(target) {
		return normalizeDatablockScratchpad(targetDatablockExitFix(target.FlightPlan.ExitFix))
	}
	return ""
}

func targetSecondaryScratchpad(target *redsnet.TaisTarget) string {
	if target == nil || target.FlightPlan == nil {
		return ""
	}
	return normalizeDatablockScratchpad(target.FlightPlan.ScratchPad2)
}

// targetFullDatablockField34 implements Figure 2-20 fields 3 and 4 using the
// same default phase priority as VICE:
//
//	phase 1: Mode-C altitude
//	phase 2: primary scratchpad -> secondary scratchpad -> altitude
//	phase 3: secondary scratchpad -> primary scratchpad -> altitude
//	phase 4: blank
//
// A pending intrafacility handoff uses field 4 for the receiver position
// symbol. REDS still cannot populate VICE's phase-3 interfacility handoff TCP
// because TAIS does not provide the originating facility/sector in a form we
// can map without guessing. A displayed secondary scratchpad is followed by
// '+' in field 4, exactly as described by the operator manual.
func (p *STARSPane) targetFullDatablockField34(target *redsnet.TaisTarget, clockPhase int) string {
	if target == nil {
		return ""
	}

	altitude := targetDatablockAltitude(target.Track.ReportedAltitude)
	// An Unsupported FDB has no surveillance altitude. VICE/STARS therefore
	// leaves the altitude slot empty unless a timeshared scratchpad occupies it.
	if isRPOSUnsupportedTarget(target) {
		altitude = ""
	}
	sp1 := targetPrimaryScratchpad(target)
	sp2 := targetSecondaryScratchpad(target)
	handoff := p.targetIntrafacilityHandoffIndicator(target)
	alt := func() string { return padDatablockField(altitude, 3) + handoff }
	primary := func() string { return padDatablockField(sp1, 3) + handoff }
	secondary := func() string { return padDatablockField(sp2, 3) + "+" }

	switch clockPhase {
	case 2:
		if sp1 != "" {
			return primary()
		}
		if sp2 != "" {
			return secondary()
		}
		return alt()
	case 3:
		if sp2 != "" {
			return secondary()
		}
		if sp1 != "" {
			return primary()
		}
		return alt()
	case 4:
		return ""
	default:
		return alt()
	}
}

// targetIntrafacilityHandoffIndicator implements the portion of Figure 2-20
// field 4 that TAIS can identify without guessing. During OCR=PENDING, CPS is
// the receiver. If both the remembered owner and receiver are adapted TCPs in
// this STARS facility, this is an intrafacility handoff and field 4 shows the
// receiver's position symbol. After acceptance, STARS keeps that receiver TCP
// in field 4 at the former owner's display for the five-second acceptance
// interval. Interfacility handoffs require the adapted originating-facility
// symbol, which TAIS AIG200 does not provide directly, so REDS intentionally
// leaves that case blank for now.
func (p *STARSPane) targetIntrafacilityHandoffIndicator(target *redsnet.TaisTarget) string {
	if p == nil || target == nil {
		return " "
	}

	if taisOwnershipPending(target) {
		owner, ownerOK := p.targetOwnerTCP(target)
		receiver, receiverOK := p.targetHandoffTCP(target)
		if !ownerOK || !receiverOK || !p.localTCP(owner) || !p.localTCP(receiver) || strings.EqualFold(owner, receiver) {
			return " "
		}
		symbol, ok := starsPositionSymbolFromTCP(receiver)
		if !ok {
			return " "
		}
		return symbol
	}

	if p.targetOutboundHandoffAccepted(target) {
		state := p.taisOwnership[targetDisplayStateKey(target)]
		if time.Now().Before(state.outboundHandoffFlashEnd) && p.localTCP(state.acceptedReceiverTCP) {
			symbol, ok := starsPositionSymbolFromTCP(state.acceptedReceiverTCP)
			if ok {
				return symbol
			}
		}
	}

	return " "
}

// targetFullDatablockLine2 formats Figure 2-20 line 2. Fields 3/4 already
// include their separator/indicator cell, so field 5 follows immediately.
func (p *STARSPane) targetFullDatablockLine2(target *redsnet.TaisTarget, clockPhase int) string {
	if target == nil {
		return ""
	}
	return p.targetFullDatablockField34(target, clockPhase) + targetFullDatablockField5(target, clockPhase)
}

// targetPartialDatablockLine1 formats Figure 2-22's single visible data row.
// VICE's default PDB presentation uses Mode-C altitude in phase 1/4 and primary
// scratchpad in phases 2/3 when present. PDB display of scratchpad #2 is a site
// adaptation and is intentionally not guessed here.
func targetPartialDatablockLine1(target *redsnet.TaisTarget, clockPhase int) string {
	if target == nil {
		return ""
	}

	left := targetDatablockAltitude(target.Track.ReportedAltitude)
	if clockPhase == 2 || clockPhase == 3 {
		if sp1 := targetPrimaryScratchpad(target); sp1 != "" {
			left = sp1
		}
	}
	return padDatablockField(left, 3) + " " + targetDatablockGroundSpeed(target.Track.VX, target.Track.VY)
}

// targetFullDatablockLine3 implements the assigned-altitude portion of Figure
// 2-20 field 7. STARS prefixes the value with 'A' and displays hundreds of
// feet. The leading columns reproduce VICE's field-6/field-7 placement: for
// N-through-SE leaders field 7 is indented one extra character; for the
// right-justified S-through-NW leaders it is not.
func targetFullDatablockLine3(target *redsnet.TaisTarget, direction leaderLineDirection) string {
	if target == nil || target.FlightPlan == nil || target.FlightPlan.AssignedAltitude == 0 {
		return ""
	}

	text := fmt.Sprintf("A%03d", target.FlightPlan.AssignedAltitude/100)
	leading := 5 // field 6
	if !datablockRightJustified(direction) {
		leading++
	}
	return strings.Repeat(" ", leading) + text
}

// TAIS flightPlan.type maps to the STARS flight-status field. Current SimpleXML
// uses P for departures and A for arrivals; accept the expanded spellings too.
// The enhanced airport fields provide a conservative fallback for feeds that
// expose a different spelling.
func taisFlightPlanIsDeparture(target *redsnet.TaisTarget) bool {
	if target == nil || target.FlightPlan == nil {
		return false
	}

	switch strings.ToUpper(strings.TrimSpace(target.FlightPlan.Type)) {
	case "P", "D", "DEP", "DEPARTURE":
		return true
	case "A", "ARR", "ARRIVAL":
		return false
	}

	if target.EnhancedData == nil {
		return false
	}
	airport := normalizeTaisAirport(target.FlightPlan.Airport)
	departure := normalizeTaisAirport(target.EnhancedData.DepartureAirport)
	destination := normalizeTaisAirport(target.EnhancedData.DestinationAirport)
	return airport != "" && airport == departure && airport != destination
}

func targetDatablockExitFix(exitFix string) string {
	exitFix = strings.ToUpper(strings.TrimSpace(exitFix))
	if exitFix == "" || exitFix == "UNASSIGNED" {
		return ""
	}
	if i := strings.IndexByte(exitFix, '.'); i >= 0 {
		exitFix = exitFix[:i]
	}

	runes := []rune(exitFix)
	// Figure 2-20 defines the normal field-3 width as three characters; a
	// fourth character is site-adaptation dependent and is not enabled yet.
	if len(runes) > 3 {
		runes = runes[:3]
	}
	return string(runes)
}

func normalizeTaisAirport(airport string) string {
	airport = strings.ToUpper(strings.TrimSpace(airport))
	if len(airport) == 4 && airport[0] == 'K' {
		return airport[1:]
	}
	return airport
}

func targetFullDatablockField5(target *redsnet.TaisTarget, clockPhase int) string {
	if target == nil || target.FlightPlan == nil {
		return ""
	}

	// An Unsupported FDB has no radar-derived groundspeed. STARS/VICE use
	// 00 in the speed slot while retaining the flight-rules/category suffix.
	if isRPOSUnsupportedTarget(target) && clockPhase == 1 {
		return "00" + targetDatablockFlightRulesIndicator(target.FlightPlan) +
			targetDatablockCategory(target.FlightPlan)
	}

	switch clockPhase {
	case 1:
		return targetDatablockGroundSpeed(target.Track.VX, target.Track.VY) +
			targetDatablockFlightRulesIndicator(target.FlightPlan) +
			targetDatablockCategory(target.FlightPlan)
	case 2, 3, 4:
		actype := strings.ToUpper(strings.TrimSpace(target.FlightPlan.ACType))
		if target.FlightPlan.RNAV != 0 && actype != "" {
			actype += "^"
		}
		return actype
	default:
		return targetDatablockGroundSpeed(target.Track.VX, target.Track.VY) +
			targetDatablockFlightRulesIndicator(target.FlightPlan) +
			targetDatablockCategory(target.FlightPlan)
	}
}

// RawFlightRules is the closest TAIS representation of the STARS one-character
// field and may contain site-adapted IFR identifiers. Fall back to the expanded
// flightRules value used by newer STDDS releases.
func targetDatablockFlightRulesIndicator(fp *redsnet.TaisFlightPlan) string {
	if fp == nil {
		return " "
	}

	if raw := strings.ToUpper(strings.TrimSpace(fp.RawFlightRules)); len([]rune(raw)) == 1 {
		return raw
	}

	switch strings.ToUpper(strings.TrimSpace(fp.FlightRules)) {
	case "V", "VFR":
		return "V"
	case "P":
		return "P"
	case "E":
		return "E"
	default:
		// IFR is normally blank unless the site adapts another alpha character.
		return " "
	}
}

// TAIS category is the STARS aircraft category/RNAV or wake-turbulence
// category already intended for data-block presentation. Keep the raw adapted
// one-character value rather than trying to derive wake class from AC type.
func targetDatablockCategory(fp *redsnet.TaisFlightPlan) string {
	if fp == nil {
		return " "
	}
	category := []rune(strings.ToUpper(strings.TrimSpace(fp.Category)))
	if len(category) == 0 {
		return " "
	}
	return string(category[0])
}
