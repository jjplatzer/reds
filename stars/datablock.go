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

// targetDatablockType is the subset of the STARS data-block presentation that
// REDS can determine directly from TAIS today. TI 6191.409 Rev. 30 section
// 2.12 defines Full, Partial, and Limited data blocks. Special cases that force
// an otherwise unowned associated track to a Full data block (quick look,
// pointout, alerts, handoff attention, etc.) are intentionally deferred until
// REDS carries the corresponding operator/display state.
type targetDatablockType uint8

const (
	targetDatablockFull targetDatablockType = iota
	targetDatablockPartial
	targetDatablockLimited
)

// drawDatablocks renders the first, deliberately small STARS data-block
// implementation. The field layout follows TI 6191.409 Rev. 30 figures 2-20,
// 2-22, and 2-23, while limiting the contents to the basic surveillance fields
// requested for the first pass:
//
//	Full (owned associated):     ACID
//	                             altitude ground-speed
//
//	Partial (unowned associated): altitude ground-speed
//
//	Limited (unassociated):      beacon code
//	                             altitude ground-speed
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
	// clock-phase sequence and intervals.
	clockPhase := defaultSTARSDataBlockClockPhase(time.Now())

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

		dbType, color, brightness := p.targetDatablockPresentation(target)
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

		switch dbType {
		case targetDatablockFull:
			// Figure 2-20: the leader is aligned with the ACID line. The
			// omitted alert line remains conceptually above it; it does not need
			// an empty raster row in this reduced first implementation. Field 5
			// on line 2 now follows the STARS clock phase: GS + flight-rules /
			// category timeshares with aircraft type.
			line1 := targetDatablockACID(target)
			line2 := targetFullDatablockLine2(target, clockPhase)
			p.addDatablockLine(td, line1, anchor, direction, 0, 0, lineHeight, style)
			p.addDatablockLine(td, line2, anchor, direction, 1, 0, lineHeight, style)

		case targetDatablockPartial:
			// Figure 2-22: an ordinary unowned associated track does not show
			// ACID in its PDB. For this first pass retain the core altitude + GS
			// fields and omit category/handoff/alert fields.
			line := targetDatablockAltitudeGroundSpeed(target)
			p.addDatablockLine(td, line, anchor, direction, 0, 0, lineHeight, style)

		case targetDatablockLimited:
			// Figure 2-23: the leader aligns with the altitude/ground-speed
			// line; the beacon-code line is immediately above it.
			line1 := normalizeBeaconCode(target.Track.ReportedBeaconCode)
			line2 := targetDatablockAltitudeGroundSpeed(target)
			p.addDatablockLine(td, line1, anchor, direction, 0, 1, lineHeight, style)
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

// targetDatablockPresentation maps the basic TAIS association/ownership state
// to the operator-manual data-block categories. With no force-FDB state yet,
// the normal presentation is:
//
//	own associated track      -> Full data block, white, FDB brightness
//	other associated track    -> Partial data block, green, LDB brightness
//	unassociated track        -> Limited data block, green, LDB brightness
//
// TI 6191.409 Table 4-1 explicitly assigns Partial and Limited data blocks to
// the LDB brightness control; OTH is for unowned Full data blocks.
func (p *STARSPane) targetDatablockPresentation(target *redsnet.TaisTarget) (targetDatablockType, renderer.RGB, Brightness) {
	if p == nil || target == nil {
		return targetDatablockLimited, renderer.RGB{}, 0
	}

	ps := p.currentPrefs()
	if target.FlightPlan == nil {
		return targetDatablockLimited, p.colors.UnownedDatablock, ps.Brightness.LimitedDatablocks
	}

	if _, cps, ok := taisCPSPositionSymbol(target); ok {
		if ownTCP := strings.TrimSpace(p.config.ControlPosition.TCP); ownTCP != "" && strings.EqualFold(cps, ownTCP) {
			return targetDatablockFull, p.colors.OwnedDatablock, ps.Brightness.FullDatablocks
		}
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

// targetFullDatablockLine2 formats Figure 2-20's line 2. The manual allows
// field 3 to show an exit fix for departure flights when enabled by adaptation.
// REDS enables that presentation by default: a departure with a usable TAIS
// exitFix shows it on the left; otherwise the normal Mode-C altitude is used.
// Field 4 is not populated yet, so keep a single visible blank between the
// field-3 value and field 5, matching VICE's chopped field34 presentation.
//
// Field 5 implements the basic VICE/STARS timeshare supported by current TAIS:
//
//	phase 1: ground speed + flight-rules indicator + STARS category/CWT
//	phase 2: aircraft type (plus RNAV caret when applicable)
//	phase 3: aircraft type (requested-altitude display is adaptation-dependent)
//
// Phase 4 is not in VICE's default clock sequence; use the same presentation
// as phase 2 if a future site adaptation selects it.
func targetFullDatablockLine2(target *redsnet.TaisTarget, clockPhase int) string {
	if target == nil {
		return ""
	}

	left := targetFullDatablockField3(target)
	right := targetFullDatablockField5(target, clockPhase)
	if right == "" {
		return left
	}
	return left + " " + right
}

// targetFullDatablockField3 implements the default departure exit-fix
// presentation described for Figure 2-20 field 3. VICE exposes this through
// the DisplayExitFix adaptation; REDS currently has no per-facility equivalent,
// so this first implementation treats it as enabled by default.
func targetFullDatablockField3(target *redsnet.TaisTarget) string {
	if target == nil {
		return ""
	}

	if target.FlightPlan != nil && taisFlightPlanIsDeparture(target) {
		if exitFix := targetDatablockExitFix(target.FlightPlan.ExitFix); exitFix != "" {
			return exitFix
		}
	}
	return targetDatablockAltitude(target.Track.ReportedAltitude)
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
