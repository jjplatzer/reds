package stars

import (
	"strings"
	"sync"
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	redsnet "github.com/juliusplatzer/reds/net"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	// Keep surveillance below lists/preview/DCB but above the video map. This
	// mirrors the TCW presentation order: maps are background information;
	// surveillance targets/history remain visible over them; operator UI is
	// always on top.
	zTargetHistory   renderer.Z = -100
	zTargetPTL       renderer.Z = -95
	zTargetGeometry  renderer.Z = -90
	zTargetLeader    renderer.Z = -85
	zTargetPosition  renderer.Z = -80
	zTargetDatablock renderer.Z = -75

	// The active AUX DCB currently presents HISTORY 5 / H_RATE 4.5. TI 6191.409
	// defines HISTORY as the number of target-history positions and H_RATE as
	// their update interval. Until those AUX controls are made interactive,
	// render their displayed values exactly rather than using every TAIS report
	// as a history mark.
	starsTargetHistoryCount = 5
	starsTargetHistoryRate  = 4500 * time.Millisecond

	// TI 6191.409 Rev. 30, 6.13.2 specifies a five-second beacon readout
	// after an unassociated track is slewed and the left trackball selected.
	starsLDBBeaconReadoutDuration = 5 * time.Second

	// TI 6191.409 defines a five-second previously-owned flash after a
	// handoff is accepted. VICE uses the same fallback duration when site
	// adaptation does not override it.
	starsHandoffAcceptFlashDuration = 5 * time.Second

	// The operator material defines current target geometry and target history
	// as distinct display elements but does not specify their raster dimensions.
	// Use the VICE STARS dimensions as the fallback: a nominal 13-pixel current
	// target and an 8-pixel history mark. These are fixed screen-space sizes;
	// unlike VICE, REDS intentionally does not enlarge them when zooming in.
	starsTargetDiameterPixels  float32 = 13
	starsHistoryDiameterPixels float32 = 8

	// TAIS reports can arrive more frequently than H_RATE. Retain enough recent
	// raw positions in the per-frame snapshot to select five 4.5-second history
	// marks without copying the full 64-position client trail every frame.
	starsTargetHistorySourcePoints = 32

	// VICE/STARS accepts a slew to the nearest surveillance track within 20
	// display pixels. Use the same screen-space radius for implied target-click
	// commands such as TI 6191.409 6.13.4 single-track quick look.
	starsTargetSlewRadiusPixels = float32(20)
)

// VICE's STARS implementation uses these fixed display lengths for the eight
// values presented by <LDR LEN n>. TI 6191.409 4.14.3 defines the operator
// values as 0 through 7 and describes the physical range as roughly 0 to 1.5
// inches, but does not provide pixel dimensions. Keep the VICE dimensions as
// the rendering fallback, just as REDS does for target-symbol raster sizes.
var starsLeaderLineLengths = [8]float32{0, 17, 32, 47, 62, 77, 114, 152}

// targetSnapshot returns display-facing surveillance state for the selected
// STARS facility. WebSocket/revision details remain owned by net.TaisClient;
// target rendering therefore does not depend on the transport implementation.
func (p *STARSPane) targetSnapshot() redsnet.TaisSnapshot {
	if p == nil || p.tais == nil {
		return redsnet.TaisSnapshot{}
	}
	return p.tais.SnapshotFacilityHistory(
		p.config.Facility.Facility,
		starsTargetHistorySourcePoints,
	)
}

// closestSlewTarget mirrors VICE's 20-pixel target slew tolerance. Only
// surveillance positions are considered; data-block text itself is not a separate
// hit target for this implied command.
func closestSlewTarget(
	snapshot redsnet.TaisSnapshot,
	mouse redsmath.Vec2,
	transforms radar.LatLonTransformations,
) *redsnet.TaisTarget {
	if !snapshot.Ready {
		return nil
	}

	limit2 := starsTargetSlewRadiusPixels * starsTargetSlewRadiusPixels
	best2 := limit2
	best := -1
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if !taisTargetHasPosition(target) {
			continue
		}
		center := transforms.WindowFromLatLon(target.Track.Lat, target.Track.Lon)
		dx := center.X - mouse.X
		dy := center.Y - mouse.Y
		d2 := dx*dx + dy*dy
		if d2 < best2 {
			best2 = d2
			best = i
		}
	}
	if best < 0 {
		return nil
	}
	return &snapshot.Targets[best]
}

func targetDisplayStateKey(target *redsnet.TaisTarget) string {
	if target == nil {
		return ""
	}
	if key := strings.TrimSpace(target.Key); key != "" {
		return key
	}
	track := strings.TrimSpace(target.Track.TrackNum)
	if track == "" {
		return ""
	}
	return strings.TrimSpace(target.Facility) + ":T" + track
}

type taisOwnershipState struct {
	ownerTCP     string
	seenRevision uint64
	pending      bool

	// After a pending handoff is accepted, STARS retains the Full data block
	// at the former owner in the Owned/Previously-Owned color until that
	// controller acknowledges it. The initial adapted interval flashes.
	formerOwnerTCP          string
	acceptedReceiverTCP     string
	outboundAccepted        bool
	outboundHandoffFlashEnd time.Time
}

// normalizeTaisOCR accepts both the SimpleXML spelling (for example
// "Normal handoff") and the FIXM-style enum spelling (NORMAL_HANDOFF).
// AIG200 defines PENDING specially: while it is set, CPS identifies the
// handoff receiver instead of the controller that still owns the track.
func normalizeTaisOCR(ocr string) string {
	ocr = strings.ToUpper(strings.TrimSpace(ocr))
	ocr = strings.ReplaceAll(ocr, "_", " ")
	return strings.Join(strings.Fields(ocr), " ")
}

func taisOwnershipPending(target *redsnet.TaisTarget) bool {
	return target != nil && target.FlightPlan != nil &&
		normalizeTaisOCR(target.FlightPlan.OCR) == "PENDING"
}

func taisCPS(target *redsnet.TaisTarget) (string, bool) {
	if target == nil || target.FlightPlan == nil {
		return "", false
	}
	cps := strings.ToUpper(strings.TrimSpace(target.FlightPlan.CPS))
	switch cps {
	case "", "UNKNOWN", "UNASSIGNED", "UNAVAILABLE", "NONE", "PENDING":
		return "", false
	default:
		return cps, true
	}
}

func starsPositionSymbolFromTCP(tcp string) (string, bool) {
	runes := []rune(strings.ToUpper(strings.TrimSpace(tcp)))
	if len(runes) == 0 {
		return "", false
	}
	last := runes[len(runes)-1]
	if !((last >= 'A' && last <= 'Z') || (last >= '0' && last <= '9')) {
		return "", false
	}
	return string(last), true
}

func (p *STARSPane) localTCP(tcp string) bool {
	if p == nil {
		return false
	}
	tcp = strings.ToUpper(strings.TrimSpace(tcp))
	if tcp == "" {
		return false
	}
	for _, position := range p.config.Facility.ControlPositions {
		if strings.EqualFold(strings.TrimSpace(position.TCP), tcp) {
			return true
		}
	}
	return false
}

// updateTaisOwnership remembers the controlling local TCP across OCR=PENDING
// reports. This is necessary because TAIS deliberately overloads CPS during a
// pending handoff: CPS becomes the receiving position. A newly associated or
// re-used track never inherits ownership from an older target with the same
// key, and stale state is pruned with the authoritative snapshot.
func (p *STARSPane) updateTaisOwnership(snapshot redsnet.TaisSnapshot, now time.Time) {
	if p == nil {
		return
	}
	if !snapshot.Ready {
		clear(p.taisOwnership)
		return
	}
	if p.taisOwnership == nil {
		p.taisOwnership = make(map[string]taisOwnershipState)
	}

	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		key := targetDisplayStateKey(target)
		if key == "" {
			continue
		}
		if target.Track.NewTrack {
			delete(p.taisOwnership, key)
		}

		state := p.taisOwnership[key]
		state.seenRevision = snapshot.Revision
		if taisOwnershipPending(target) {
			state.pending = true
			p.taisOwnership[key] = state
			continue
		}

		// AIG200 makes acceptance observable as OCR leaving PENDING while CPS
		// changes to the new owner. Remember the old local owner so that its
		// display can reproduce STARS' five-second accepted-handoff flash and
		// retained Previously-Owned Full data block.
		cps, cpsOK := taisCPS(target)
		if state.pending && state.ownerTCP != "" && cpsOK && !strings.EqualFold(cps, state.ownerTCP) {
			state.formerOwnerTCP = state.ownerTCP
			state.acceptedReceiverTCP = cps
			state.outboundAccepted = true
			state.outboundHandoffFlashEnd = now.Add(starsHandoffAcceptFlashDuration)
		}
		state.pending = false

		if cpsOK && p.localTCP(cps) {
			state.ownerTCP = cps
		} else {
			// For non-local CPS values TAIS may be identifying an ARTCC or
			// adjacent facility rather than a local owner. Do not manufacture
			// a local ownership relationship from that value.
			state.ownerTCP = ""
		}
		p.taisOwnership[key] = state
	}

	for key, state := range p.taisOwnership {
		if state.seenRevision != snapshot.Revision {
			delete(p.taisOwnership, key)
		}
	}
}

// targetOwnerTCP returns the controller that still owns the track. During a
// pending handoff it uses the last non-pending local CPS instead of the current
// TAIS CPS, because the latter is the handoff receiver by definition.
func (p *STARSPane) targetOwnerTCP(target *redsnet.TaisTarget) (string, bool) {
	if p == nil || target == nil {
		return "", false
	}
	if taisOwnershipPending(target) {
		if state, ok := p.taisOwnership[targetDisplayStateKey(target)]; ok && state.ownerTCP != "" {
			return state.ownerTCP, true
		}
		return "", false
	}
	cps, ok := taisCPS(target)
	return cps, ok
}

func (p *STARSPane) targetHandoffTCP(target *redsnet.TaisTarget) (string, bool) {
	if !taisOwnershipPending(target) {
		return "", false
	}
	return taisCPS(target)
}

func (p *STARSPane) targetInboundHandoff(target *redsnet.TaisTarget) bool {
	toTCP, ok := p.targetHandoffTCP(target)
	return ok && p.ownTCP() != "" && strings.EqualFold(toTCP, p.ownTCP())
}

// targetOutboundHandoffAccepted is the post-acceptance state at the former
// owner's display. STARS keeps this presentation until the former owner slews
// the track and selects the left trackball button.
func (p *STARSPane) targetOutboundHandoffAccepted(target *redsnet.TaisTarget) bool {
	if p == nil || target == nil || p.ownTCP() == "" {
		return false
	}
	state, ok := p.taisOwnership[targetDisplayStateKey(target)]
	return ok && state.outboundAccepted && state.formerOwnerTCP != "" &&
		strings.EqualFold(state.formerOwnerTCP, p.ownTCP())
}

func (p *STARSPane) acknowledgeOutboundHandoff(target *redsnet.TaisTarget) bool {
	if !p.targetOutboundHandoffAccepted(target) {
		return false
	}
	key := targetDisplayStateKey(target)
	state := p.taisOwnership[key]
	state.outboundAccepted = false
	state.formerOwnerTCP = ""
	state.acceptedReceiverTCP = ""
	state.outboundHandoffFlashEnd = time.Time{}
	p.taisOwnership[key] = state
	return true
}

// targetHandoffAttentionDim follows VICE's 500 ms handoff-attention cadence.
// An inbound offered handoff alternates normal and half brightness until it is
// accepted. At the former owner, the accepted handoff flashes for the adapted
// interval (five seconds here) and then remains steady white until acknowledged.
func (p *STARSPane) targetHandoffAttentionDim(target *redsnet.TaisTarget, now time.Time) bool {
	flashing := p.targetInboundHandoff(target)
	if p.targetOutboundHandoffAccepted(target) {
		state := p.taisOwnership[targetDisplayStateKey(target)]
		flashing = flashing || now.Before(state.outboundHandoffFlashEnd)
	}
	return flashing && (now.UnixMilli()/500)&1 == 0
}

func (p *STARSPane) targetHandoffBrightness(target *redsnet.TaisTarget, brightness Brightness, now time.Time) Brightness {
	if p.targetHandoffAttentionDim(target, now) {
		return brightness / 2
	}
	return brightness
}

func (p *STARSPane) targetOwnedByCurrentTCP(target *redsnet.TaisTarget) bool {
	owner, ok := p.targetOwnerTCP(target)
	return ok && p.ownTCP() != "" && strings.EqualFold(owner, p.ownTCP())
}

// targetSupportsSingleTrackQuickLook is the state predicate for TI 6191.409
// 6.13.4: the implied command applies to an associated track owned by another
// controller. LDB/unassociated tracks use a different implied command.
func (p *STARSPane) targetSupportsSingleTrackQuickLook(target *redsnet.TaisTarget) bool {
	if p == nil || target == nil || target.FlightPlan == nil || target.FlightPlan.Suspended ||
		p.targetInboundHandoff(target) || p.targetOutboundHandoffAccepted(target) {
		return false
	}
	if _, ok := p.targetOwnerTCP(target); !ok {
		return false
	}
	return !p.targetOwnedByCurrentTCP(target)
}

func (p *STARSPane) targetSingleTrackQuickLooked(target *redsnet.TaisTarget) bool {
	if p == nil || len(p.singleTrackQuickLook) == 0 {
		return false
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return false
	}
	_, ok := p.singleTrackQuickLook[key]
	return ok
}

// targetSupportsSingleTrackLeaderDirection implements the track eligibility
// from TI 6191.409 Rev. 30 6.13.1 / 6.13.17: the command is valid for any
// associated track at the entering TCW/TDW.
func (p *STARSPane) targetSupportsSingleTrackLeaderDirection(target *redsnet.TaisTarget) bool {
	return p != nil && target != nil && target.FlightPlan != nil
}

// singleTrackLeaderDirectionCommand parses the numpad orientation used by the
// single-track leader-direction commands. Direction 5 is special: it removes
// a previously selected manual orientation rather than naming a direction.
func singleTrackLeaderDirectionCommand(input string) (*leaderLineDirection, bool, error) {
	if len(input) != 1 || input[0] < '0' || input[0] > '9' {
		return nil, false, nil
	}
	if input[0] == '5' {
		return nil, true, nil
	}
	direction, ok := leaderLineDirectionFromKeypad(int(input[0] - '0'))
	if !ok {
		return nil, true, ErrSTARSCommandFormat
	}
	return &direction, true, nil
}

func (p *STARSPane) setSingleTrackLeaderDirection(target *redsnet.TaisTarget, direction *leaderLineDirection) {
	if p == nil || !p.targetSupportsSingleTrackLeaderDirection(target) {
		return
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return
	}
	if direction == nil {
		delete(p.singleTrackLeaderDirections, key)
		return
	}
	if p.singleTrackLeaderDirections == nil {
		p.singleTrackLeaderDirections = make(map[string]leaderLineDirection)
	}
	p.singleTrackLeaderDirections[key] = *direction
}

func (p *STARSPane) targetSingleTrackLeaderDirection(target *redsnet.TaisTarget) (leaderLineDirection, bool) {
	if p == nil || len(p.singleTrackLeaderDirections) == 0 || !p.targetSupportsSingleTrackLeaderDirection(target) {
		return leaderLineDirectionNorth, false
	}
	direction, ok := p.singleTrackLeaderDirections[targetDisplayStateKey(target)]
	return direction, ok
}

// pruneSingleTrackLeaderDirections prevents a later flight reusing a TAIS key
// from inheriting a local manual leader direction. Preserve overrides across a
// temporary transport disconnect, just like the other transient track state.
func (p *STARSPane) pruneSingleTrackLeaderDirections(snapshot redsnet.TaisSnapshot) {
	if p == nil || !snapshot.Ready || len(p.singleTrackLeaderDirections) == 0 {
		return
	}
	live := make(map[string]struct{}, len(snapshot.Targets))
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if !p.targetSupportsSingleTrackLeaderDirection(target) {
			continue
		}
		if key := targetDisplayStateKey(target); key != "" {
			live[key] = struct{}{}
		}
	}
	for key := range p.singleTrackLeaderDirections {
		if _, ok := live[key]; !ok {
			delete(p.singleTrackLeaderDirections, key)
		}
	}
}

func (p *STARSPane) toggleSingleTrackQuickLook(target *redsnet.TaisTarget) {
	if p == nil || !p.targetSupportsSingleTrackQuickLook(target) {
		return
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return
	}
	if p.singleTrackQuickLook == nil {
		p.singleTrackQuickLook = make(map[string]struct{})
	}
	if _, ok := p.singleTrackQuickLook[key]; ok {
		delete(p.singleTrackQuickLook, key)
	} else {
		p.singleTrackQuickLook[key] = struct{}{}
	}
}

// pruneSingleTrackQuickLook prevents a reused TAIS track number from
// inheriting stale local quick-look state. Preserve state across a temporary
// transport disconnect by pruning only from an authoritative ready snapshot.
func (p *STARSPane) pruneSingleTrackQuickLook(snapshot redsnet.TaisSnapshot) {
	if p == nil || !snapshot.Ready || len(p.singleTrackQuickLook) == 0 {
		return
	}
	live := make(map[string]struct{}, len(snapshot.Targets))
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if !p.targetSupportsSingleTrackQuickLook(target) {
			continue
		}
		if key := targetDisplayStateKey(target); key != "" {
			live[key] = struct{}{}
		}
	}
	for key := range p.singleTrackQuickLook {
		if _, ok := live[key]; !ok {
			delete(p.singleTrackQuickLook, key)
		}
	}
}

// targetSupportsLDBBeaconReadout is the state predicate for TI 6191.409
// 6.13.2: the implied beacon readout applies to an unassociated track. REDS
// currently represents that state as a surveillance target with no associated
// TAIS flight plan.
func (p *STARSPane) targetSupportsLDBBeaconReadout(target *redsnet.TaisTarget) bool {
	return p != nil && target != nil && target.FlightPlan == nil
}

func (p *STARSPane) startLDBBeaconReadout(target *redsnet.TaisTarget, now time.Time) {
	if p == nil || !p.targetSupportsLDBBeaconReadout(target) {
		return
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return
	}
	if p.ldbBeaconReadoutUntil == nil {
		p.ldbBeaconReadoutUntil = make(map[string]time.Time)
	}
	p.ldbBeaconReadoutUntil[key] = now.Add(starsLDBBeaconReadoutDuration)
}

func (p *STARSPane) targetLDBBeaconReadoutActive(target *redsnet.TaisTarget, now time.Time) bool {
	if p == nil || len(p.ldbBeaconReadoutUntil) == 0 || !p.targetSupportsLDBBeaconReadout(target) {
		return false
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return false
	}
	until, ok := p.ldbBeaconReadoutUntil[key]
	return ok && now.Before(until)
}

// pruneLDBBeaconReadouts drops expired entries and prevents a reused TAIS
// track number from inheriting an old five-second readout. As with quick-look
// state, preserve it across a temporary transport disconnect by pruning only
// from an authoritative ready snapshot.
func (p *STARSPane) pruneLDBBeaconReadouts(snapshot redsnet.TaisSnapshot, now time.Time) {
	if p == nil || !snapshot.Ready || len(p.ldbBeaconReadoutUntil) == 0 {
		return
	}
	live := make(map[string]struct{}, len(snapshot.Targets))
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if !p.targetSupportsLDBBeaconReadout(target) {
			continue
		}
		if key := targetDisplayStateKey(target); key != "" {
			live[key] = struct{}{}
		}
	}
	for key, until := range p.ldbBeaconReadoutUntil {
		if !now.Before(until) {
			delete(p.ldbBeaconReadoutUntil, key)
			continue
		}
		if _, ok := live[key]; !ok {
			delete(p.ldbBeaconReadoutUntil, key)
		}
	}
}

// drawTargetPositionSymbols draws the STARS position symbol centered at the
// target location, independently of the fused target geometry. TI 6191.409
// Rev. 30 §2.11 defines the position symbol as the controlling TCP identifier
// for associated tracks and requires a dark outline for readability. REDS
// uses the configured position-symbol character size and, like VICE, draws the
// outline mask first and the colored glyph second.
func (p *STARSPane) drawTargetPositionSymbols(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.LatLonTransformations,
	snapshot redsnet.TaisSnapshot,
	now time.Time,
) {
	if p == nil || ctx == nil || zcb == nil || !snapshot.Ready ||
		p.systemFont == nil || p.systemOutlineFont == nil {
		return
	}

	fontSize := p.positionSymbolFontSize()
	fontTexture := p.systemFontTexture(ctx.Renderer, fontSize)
	outlineTexture := p.systemOutlineFontTexture(ctx.Renderer, fontSize)
	if fontTexture == 0 || outlineTexture == 0 {
		return
	}

	fill := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(fill)
	fill.SetFont(p.systemFont)

	outline := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(outline)
	outline.SetFont(p.systemOutlineFont)

	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if !taisTargetHasPosition(target) {
			continue
		}

		symbol, color, brightness := p.targetPositionSymbol(target)
		brightness = p.targetHandoffBrightness(target, brightness, now)
		if symbol == "" || brightness == 0 {
			continue
		}

		center := transforms.WindowFromLatLon(target.Track.Lat, target.Track.Lon)
		if !targetCenterNearPane(ctx, center, float32(fontSize)) {
			continue
		}

		// Match VICE's half-pixel alignment so the bitmap mask lands cleanly on
		// the pixel grid. The outline is deliberately full black rather than
		// brightness-scaled; Appendix B defines the position-symbol outline as
		// black for both owned and unowned presentations.
		center.X += 0.5
		center.Y -= 0.5
		addCenteredTargetGlyph(
			outline,
			p.systemOutlineFont,
			symbol,
			fontSize,
			center,
			renderer.TextStyle{Size: fontSize, Color: p.colors.PositionSymbolOutline.ToRGBA()},
		)
		addCenteredTargetGlyph(
			fill,
			p.systemFont,
			symbol,
			fontSize,
			center,
			renderer.TextStyle{Size: fontSize, Color: brightness.ScaleRGB(color).ToRGBA()},
		)
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zTargetPosition)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	outline.GenerateCommands(cb, outlineTexture)
	fill.GenerateCommands(cb, fontTexture)
	cb.DisableScissor()
}

// targetPositionSymbol maps TAIS flight-plan ownership to the STARS position
// symbol presentation. TAIS carries CPS as the logical terminal controller
// position (for example "2K"); like VICE's normal STARS presentation, REDS
// displays the final position character ("K").
//
// The color/brightness split follows TI 6191.409 Appendix B plus VICE's STARS
// modality: own-position symbols use POS brightness and white; ordinary known
// symbols owned by another position are Partial data blocks and therefore use
// LDB brightness with the TCW unowned green; an unassociated/unknown-CPS
// symbol likewise uses LDB brightness and green. OTH applies when an unowned
// track is being shown as a Full data block (quick look/handoff/etc.), which is
// not yet modeled. The black outline is applied separately.
func (p *STARSPane) targetPositionSymbol(target *redsnet.TaisTarget) (string, renderer.RGB, Brightness) {
	if p == nil || target == nil {
		return "", renderer.RGB{}, 0
	}

	ps := p.currentPrefs()
	if symbol, _, ok := p.targetPositionSymbolForOwnership(target); ok {
		if p.targetOwnedByCurrentTCP(target) || p.targetInboundHandoff(target) || p.targetOutboundHandoffAccepted(target) {
			return symbol, p.colors.PositionSymbolOwned, ps.Brightness.Positions
		}
		if quickLooked, plus := p.targetQuickLookState(target); quickLooked {
			// Table 4-1 assigns any unowned FDB and its position symbol to
			// OTH brightness. QL+ changes the presentation to the Owned color
			// without changing actual ownership.
			if plus {
				return symbol, p.colors.PositionSymbolOwned, ps.Brightness.OtherTracks
			}
			return symbol, p.colors.UnownedDatablock, ps.Brightness.OtherTracks
		}
		// An ordinary associated track owned by another TCP is a Partial
		// data block. TI 6191.409 Table 4-1 assigns PDBs and their position
		// symbols to LDB brightness.
		return symbol, p.colors.UnownedDatablock, ps.Brightness.LimitedDatablocks
	}

	// TI 6191.409's unassociated Mode-C presentation is keyed to whether the
	// reported beacon code has been selected by the operator: selected codes
	// use the open-square position symbol; otherwise use an asterisk. This is
	// independent of IFR/VFR flight rules.
	if p.beaconCodeSelected(target.Track.ReportedBeaconCode) {
		return string(rune(129)), p.colors.UnownedDatablock, ps.Brightness.LimitedDatablocks
	}
	return "*", p.colors.UnownedDatablock, ps.Brightness.LimitedDatablocks
}

func (p *STARSPane) targetPositionSymbolForOwnership(target *redsnet.TaisTarget) (symbol, owner string, ok bool) {
	owner, ok = p.targetOwnerTCP(target)
	if !ok {
		// If REDS attached in the middle of a pending handoff there may be no
		// previous CPS to recover. Retain the TAIS position indication rather
		// than dropping the position symbol completely; subsequent non-pending
		// reports establish the controlling TCP normally.
		owner, ok = taisCPS(target)
	}
	if !ok {
		return "", "", false
	}
	symbol, ok = starsPositionSymbolFromTCP(owner)
	return symbol, owner, ok
}

func (p *STARSPane) beaconCodeSelected(code string) bool {
	if p == nil {
		return false
	}

	code = normalizeBeaconCode(code)
	if len(code) != 4 {
		return false
	}

	for _, selected := range p.currentPrefs().SelectedBeacons {
		selected = normalizeBeaconCode(selected)
		switch len(selected) {
		case 2:
			// A two-digit selection denotes the full beacon-code bank, matching
			// STARS/VICE B[BCN_BLOCK] semantics (e.g. 12 selects 1200-1277).
			if strings.HasPrefix(code, selected) {
				return true
			}
		case 4:
			if code == selected {
				return true
			}
		}
	}
	return false
}

func normalizeBeaconCode(code string) string {
	code = strings.TrimSpace(code)
	if len(code) != 2 && len(code) != 4 {
		return ""
	}
	for _, r := range code {
		if r < '0' || r > '7' {
			return ""
		}
	}
	return code
}

// addCenteredTargetGlyph centers a single STARS bitmap glyph by its actual
// raster extent rather than its character cell. Position symbols are one
// display character in the current CPS presentation, so this reproduces the
// visual centering of VICE's AddTextCentered without allocating temporary
// strings or measuring the entire font on each frame.
func addCenteredTargetGlyph(
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	symbol string,
	fontSize int,
	center redsmath.Vec2,
	style renderer.TextStyle,
) {
	if td == nil || font == nil || symbol == "" {
		return
	}

	fs := font.Size(fontSize)
	if fs == nil {
		return
	}
	runes := []rune(symbol)
	if len(runes) != 1 {
		return
	}
	glyph, ok := fs.Glyph(runes[0])
	if !ok || glyph.Width <= 0 || glyph.Height <= 0 {
		return
	}

	// TextDrawBuilder positions a glyph at:
	//   x = pos.X + BearingX
	//   y = pos.Y + LineHeight - BearingY
	// Solve those equations so the glyph's ink rectangle is centered at the
	// requested target location.
	pos := redsmath.Vec2{
		X: center.X - float32(glyph.Width)/2 - float32(glyph.BearingX),
		Y: center.Y - float32(glyph.Height)/2 - float32(fs.LineHeight-glyph.BearingY),
	}
	td.AddText(symbol, pos, style)
}

// drawTargetHistory draws stored past target positions. The STARS HST control
// independently controls history brightness, and Appendix B assigns successive
// history ages their own blue shades. The newest displayed history position is
// history color 1, then progressively older colors through history color 5.
func (p *STARSPane) drawTargetHistory(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.LatLonTransformations,
	snapshot redsnet.TaisSnapshot,
) {
	if p == nil || ctx == nil || zcb == nil || !snapshot.Ready {
		return
	}

	ps := p.currentPrefs()
	if ps.Brightness.History == 0 {
		return
	}

	var builders [starsTargetHistoryCount]*renderer.TrianglesBuilder
	for i := range builders {
		builders[i] = renderer.GetTrianglesBuilder()
		defer renderer.ReturnTrianglesBuilder(builders[i])
	}
	now := time.Now()

	// Sample each target once per frame, then batch geometry by age/color. This
	// avoids allocating/re-scanning the same history five times per target.
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if !taisTargetHasPosition(target) || isRPOSUnsupportedTarget(target) {
			continue
		}
		// VICE suppresses history whenever the ordinary data-block
		// presentation is removed by the altitude filter. The current target
		// and its position symbol remain visible.
		if !p.targetAltitudeFilterAllowsDatablock(target, now) {
			continue
		}

		history, historyCount := sampledTargetHistory(target)
		for age := 0; age < historyCount; age++ {
			point := history[age]
			center := transforms.WindowFromLatLon(point.Lat, point.Lon)
			if !targetCenterNearPane(ctx, center, starsHistoryDiameterPixels) {
				continue
			}
			addFilledTargetOctagon(
				builders[age],
				renderer.PointVertex{X: center.X, Y: center.Y},
				starsHistoryDiameterPixels,
			)
		}
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zTargetHistory)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	for age := range builders {
		colorIndex := age
		if colorIndex >= len(p.colors.TrackHistory) {
			colorIndex = len(p.colors.TrackHistory) - 1
		}
		cb.SetRGB(ps.Brightness.History.ScaleRGB(p.colors.TrackHistory[colorIndex]))
		builders[age].GenerateCommands(cb, renderer.DrawSolid, 0)
	}
	cb.DisableScissor()
}

// drawPredictedTrackLines renders the PTLs selected by the Auxiliary DCB.
// TI 6191.409 Rev. 30, 6.3.2-6.3.4 defines PTL ALL for all associated
// tracks, PTL OWN for tracks owned by the entering position as well as tracks
// for which the entering position is the pending controller or previous owner,
// and a prediction interval of 0.0-5.0 minutes in half-minute increments.
// OCR-aware ownership state lets REDS identify all three PTL OWN cases.
//
// TAIS VX/VY are the horizontal velocity components already used to compute
// STARS ground speed. Treat them as east/north knots, project them for the
// selected number of minutes, and let the geographic scope transform apply the
// display's magnetic rotation. PTLs use the LIN brightness category and the
// Appendix-B PTL color, matching VICE.
func (p *STARSPane) drawPredictedTrackLines(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.LatLonTransformations,
	snapshot redsnet.TaisSnapshot,
) {
	if p == nil || ctx == nil || zcb == nil || !snapshot.Ready {
		return
	}

	ps := p.currentPrefs()
	if ps.PTLLength <= 0 || (!ps.PTLAll && !ps.PTLOwn) || ps.Brightness.Lines == 0 {
		return
	}

	builder := renderer.GetColoredLinesBuilder()
	defer renderer.ReturnColoredLinesBuilder(builder)
	color := ps.Brightness.Lines.ScaleRGB(p.colors.PTL)

	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if !taisTargetHasPosition(target) || target.FlightPlan == nil || isRPOSUnsupportedTarget(target) {
			continue
		}
		if !ps.PTLAll && !(ps.PTLOwn && (p.targetOwnedByCurrentTCP(target) ||
			p.targetInboundHandoff(target) ||
			p.targetOutboundHandoffAccepted(target))) {
			continue
		}
		if target.Track.VX == 0 && target.Track.VY == 0 {
			continue
		}

		start := transforms.WindowFromLatLon(target.Track.Lat, target.Track.Lon)
		if !targetCenterNearPane(ctx, start, starsTargetDiameterPixels) {
			continue
		}

		minutes := float64(ps.PTLLength)
		eastNM := float64(target.Track.VX) * minutes / 60
		northNM := float64(target.Track.VY) * minutes / 60
		lonScale := radar.LongitudeScaleFactorForLat(target.Track.Lat)
		if lonScale == 0 {
			continue
		}
		endLat := target.Track.Lat + northNM/60
		endLon := normalizeLongitude(target.Track.Lon + eastNM/(60*lonScale))
		end := transforms.WindowFromLatLon(endLat, endLon)

		builder.AddLineRGB(
			renderer.PointVertex{X: start.X, Y: start.Y},
			renderer.PointVertex{X: end.X, Y: end.Y},
			color,
		)
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zTargetPTL)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.LineWidth(max(float32(1), ctx.DPIScale))
	builder.GenerateCommands(cb)
	cb.DisableScissor()
}

// drawTargets draws the current STARS target geometry only. Position symbols,
// leader lines and FDB/MDB/LDB presentation are separate operator-display
// layers. PRI controls target-geometry brightness; POS must not affect it.
func (p *STARSPane) drawTargets(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.LatLonTransformations,
	snapshot redsnet.TaisSnapshot,
) {
	if p == nil || ctx == nil || zcb == nil || !snapshot.Ready {
		return
	}

	ps := p.currentPrefs()
	if ps.Brightness.PrimarySymbols == 0 {
		return
	}

	builder := renderer.GetTrianglesBuilder()
	defer renderer.ReturnTrianglesBuilder(builder)

	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if !taisTargetHasPosition(target) || isRPOSUnsupportedTarget(target) {
			continue
		}

		center := transforms.WindowFromLatLon(target.Track.Lat, target.Track.Lon)
		if !targetCenterNearPane(ctx, center, starsTargetDiameterPixels) {
			continue
		}

		// TAIS protocol-v1 gives us the tracked position but not the raw sensor
		// plot/extent information needed to reproduce single- or multi-sensor
		// primary/beacon glyphs. Render only the fused-track target geometry here;
		// do not invent a sensor-specific target modality from TAIS fields.
		addFilledTargetOctagon(
			builder,
			renderer.PointVertex{X: center.X, Y: center.Y},
			starsTargetDiameterPixels,
		)
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zTargetGeometry)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.SetRGB(ps.Brightness.PrimarySymbols.ScaleRGB(p.colors.TrackGeometry))
	builder.GenerateCommands(cb, renderer.DrawSolid, 0)
	cb.DisableScissor()
}

// drawTargetLeaderLines draws the leader-line portion of the STARS data-block
// presentation without drawing the datablock text itself. TI 6191.409 2.11
// defines the leader as the line connecting the position symbol to its data
// block; 4.14.3 defines the eight length settings and 4.14.5 defines the owned
// track direction setting.
//
// Color/brightness follow the data-block category, matching VICE and Appendix
// B/Table 4-1: owned FDB presentation is white/FDB, while ordinary unowned
// associated Partial and unassociated Limited presentation is green/LDB. OTH
// is used only when an unowned track is promoted to an FDB, which REDS does
// not yet model.
// Suspended associated tracks intentionally have no leader, matching VICE.
func (p *STARSPane) drawTargetLeaderLines(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.LatLonTransformations,
	snapshot redsnet.TaisSnapshot,
	now time.Time,
) {
	if p == nil || ctx == nil || zcb == nil || !snapshot.Ready {
		return
	}

	ps := p.currentPrefs()
	length := starsLeaderLineLengthPixels(ps.LeaderLineLength)
	if length == 0 {
		return
	}

	builder := renderer.GetColoredLinesBuilder()
	defer renderer.ReturnColoredLinesBuilder(builder)

	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		if !taisTargetHasPosition(target) {
			continue
		}
		if target.FlightPlan != nil && target.FlightPlan.Suspended {
			continue
		}
		if !p.targetAltitudeFilterAllowsDatablock(target, now) {
			continue
		}

		color, brightness := p.targetLeaderPresentation(target)
		brightness = p.targetHandoffBrightness(target, brightness, now)
		if brightness == 0 {
			continue
		}

		center := transforms.WindowFromLatLon(target.Track.Lat, target.Track.Lon)
		if !targetCenterNearPane(ctx, center, length+starsTargetDiameterPixels) {
			continue
		}

		direction := p.targetLeaderLineDirection(target)
		unit := leaderLineUnitVector(direction)
		startOffset := starsTargetDiameterPixels * 0.5
		start := renderer.PointVertex{
			X: center.X + unit.X*startOffset,
			Y: center.Y + unit.Y*startOffset,
		}
		end := renderer.PointVertex{
			X: center.X + unit.X*length,
			Y: center.Y + unit.Y*length,
		}
		builder.AddLineRGB(start, end, brightness.ScaleRGB(color))
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	cb := zcb.At(zTargetLeader)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.LineWidth(max(float32(1), ctx.DPIScale))
	builder.GenerateCommands(cb)
	cb.DisableScissor()
}

func starsLeaderLineLengthPixels(length int) float32 {
	if length < 0 {
		length = 0
	}
	if length >= len(starsLeaderLineLengths) {
		length = len(starsLeaderLineLengths) - 1
	}
	return starsLeaderLineLengths[length]
}

// targetLeaderPresentation is the leader-line equivalent of VICE's
// trackDatablockColorBrightness for the subset of ownership state available
// from TAIS. A leader is part of the data-block presentation, so it does not
// use POS brightness even though it originates next to the position symbol.
func (p *STARSPane) targetLeaderPresentation(target *redsnet.TaisTarget) (renderer.RGB, Brightness) {
	if p == nil || target == nil {
		return renderer.RGB{}, 0
	}

	ps := p.currentPrefs()
	if _, _, ok := p.targetPositionSymbolForOwnership(target); ok {
		if p.targetOwnedByCurrentTCP(target) || p.targetInboundHandoff(target) || p.targetOutboundHandoffAccepted(target) {
			return p.colors.OwnedDatablock, ps.Brightness.FullDatablocks
		}
		if quickLooked, plus := p.targetQuickLookState(target); quickLooked {
			// VICE draws all quick-look FDB components with OTH brightness.
			// QL+ changes their color to Owned/white.
			if plus {
				return p.colors.OwnedDatablock, ps.Brightness.OtherTracks
			}
			return p.colors.UnownedDatablock, ps.Brightness.OtherTracks
		}
		// Other-owner associated tracks normally carry a Partial data block,
		// whose line/position presentation is controlled by LDB brightness.
		return p.colors.UnownedDatablock, ps.Brightness.LimitedDatablocks
	}
	return p.colors.UnownedDatablock, ps.Brightness.LimitedDatablocks
}

// targetLeaderLineDirection applies the local <LDR DIR> setting exactly where
// TI 6191.409 4.14.5 says it applies: current/future tracks owned by the
// entering TCW/TDW. For other associated tracks, TAIS's LLD field provides the
// source STARS leader direction; if it is unavailable, use the normal north
// fallback also used by VICE. Unassociated tracks likewise default north until
// REDS implements the separate unassociated/other-owner positioning commands.
func (p *STARSPane) targetLeaderLineDirection(target *redsnet.TaisTarget) leaderLineDirection {
	if p == nil || target == nil {
		return leaderLineDirectionNorth
	}

	// TI 6191.409 6.13.1 / 6.13.17 manual single-track positioning
	// overrides the owner/other-owner defaults. VICE applies TrackState's
	// per-aircraft LeaderLineDirection at the same precedence.
	if direction, ok := p.targetSingleTrackLeaderDirection(target); ok {
		return direction
	}

	if p.targetOwnedByCurrentTCP(target) {
		return p.currentPrefs().LeaderLineDirection
	}

	if target.FlightPlan != nil {
		if direction, ok := parseLeaderLineDirection(target.FlightPlan.LLD); ok {
			return direction
		}
	}
	return leaderLineDirectionNorth
}

func parseLeaderLineDirection(text string) (leaderLineDirection, bool) {
	switch strings.ToUpper(strings.TrimSpace(text)) {
	case "N":
		return leaderLineDirectionNorth, true
	case "NE":
		return leaderLineDirectionNorthEast, true
	case "E":
		return leaderLineDirectionEast, true
	case "SE":
		return leaderLineDirectionSouthEast, true
	case "S":
		return leaderLineDirectionSouth, true
	case "SW":
		return leaderLineDirectionSouthWest, true
	case "W":
		return leaderLineDirectionWest, true
	case "NW":
		return leaderLineDirectionNorthWest, true
	default:
		return leaderLineDirectionNorth, false
	}
}

func leaderLineUnitVector(direction leaderLineDirection) redsmath.Vec2 {
	const diagonal = float32(0.70710678)
	switch direction {
	case leaderLineDirectionNorth:
		return redsmath.Vec2{X: 0, Y: -1}
	case leaderLineDirectionNorthEast:
		return redsmath.Vec2{X: diagonal, Y: -diagonal}
	case leaderLineDirectionEast:
		return redsmath.Vec2{X: 1, Y: 0}
	case leaderLineDirectionSouthEast:
		return redsmath.Vec2{X: diagonal, Y: diagonal}
	case leaderLineDirectionSouth:
		return redsmath.Vec2{X: 0, Y: 1}
	case leaderLineDirectionSouthWest:
		return redsmath.Vec2{X: -diagonal, Y: diagonal}
	case leaderLineDirectionWest:
		return redsmath.Vec2{X: -1, Y: 0}
	case leaderLineDirectionNorthWest:
		return redsmath.Vec2{X: -diagonal, Y: -diagonal}
	default:
		return redsmath.Vec2{X: 0, Y: -1}
	}
}

// addFilledTargetOctagon renders the fixed-size fused/history target shape used
// by STARS. The vertices are rotated half an angular step so the octagon has
// horizontal and vertical sides like a stop sign, matching VICE's STARS target
// geometry. Diameter is nominal: as in VICE, the radius is rounded to a whole
// display pixel before the vertices are generated.
func addFilledTargetOctagon(builder *renderer.TrianglesBuilder, center renderer.PointVertex, diameter float32) {
	if builder == nil || diameter <= 0 {
		return
	}

	// cos(22.5°) and sin(22.5°). Keeping these constant avoids trigonometry for
	// every target on every frame.
	const long = float32(0.92387953)
	const short = float32(0.38268343)

	radius := float32(int(diameter/2 + 0.5))
	a := radius * long
	b := radius * short
	vertices := [8]renderer.PointVertex{
		{X: center.X + a, Y: center.Y + b},
		{X: center.X + b, Y: center.Y + a},
		{X: center.X - b, Y: center.Y + a},
		{X: center.X - a, Y: center.Y + b},
		{X: center.X - a, Y: center.Y - b},
		{X: center.X - b, Y: center.Y - a},
		{X: center.X + b, Y: center.Y - a},
		{X: center.X + a, Y: center.Y - b},
	}

	for i := range vertices {
		builder.AddTriangle(center, vertices[i], vertices[(i+1)%len(vertices)])
	}
}

func taisTargetHasPosition(target *redsnet.TaisTarget) bool {
	if target == nil || target.Track.Pseudo || target.Track.MRTTime.IsZero() {
		return false
	}
	return validTargetLatLon(target.Track.Lat, target.Track.Lon)
}

func validTargetLatLon(lat, lon float64) bool {
	return lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 &&
		!(lat == 0 && lon == 0)
}

// sampledTargetHistory converts TAIS's raw sensor/track reports into the five
// history marks represented by the current HISTORY/H_RATE settings. It selects
// the most recent real report at or before each 4.5-second history epoch; no
// synthetic/interpolated surveillance position is invented by the display.
// Results are newest-to-oldest so index 0 maps to TrackHistory[0].
func sampledTargetHistory(target *redsnet.TaisTarget) ([starsTargetHistoryCount]redsnet.TaisHistory, int) {
	var out [starsTargetHistoryCount]redsnet.TaisHistory
	if target == nil || target.Track.MRTTime.IsZero() || len(target.History) == 0 {
		return out, 0
	}

	count := 0
	searchBefore := len(target.History)
	for age := 1; age <= starsTargetHistoryCount; age++ {
		epoch := target.Track.MRTTime.Add(time.Duration(-age) * starsTargetHistoryRate)
		selected := -1

		for i := searchBefore - 1; i >= 0; i-- {
			point := target.History[i]
			if point.Time.IsZero() || !validTargetLatLon(point.Lat, point.Lon) {
				continue
			}
			if !point.Time.After(epoch) {
				selected = i
				break
			}
		}
		if selected < 0 {
			break
		}

		out[count] = target.History[selected]
		count++
		searchBefore = selected
	}
	return out, count
}

func targetCenterNearPane(ctx *panes.Context, center redsmath.Vec2, diameter float32) bool {
	if ctx == nil {
		return false
	}
	margin := diameter
	return center.X >= -margin && center.Y >= -margin &&
		center.X <= ctx.PaneRect.Width()+margin && center.Y <= ctx.PaneRect.Height()+margin
}

// Per-target local display state. These helpers live with the rest of the
// target state/rendering code rather than in a separate track.go.
const starsMaxTPAGraphics = 50

type tpaTrackState struct {
	JRingRadius            float32
	ConeLength             float32
	DisplaySize            *bool
	InhibitInTrailDistance bool
}

func (s tpaTrackState) hasGraphic() bool {
	return s.JRingRadius > 0 || s.ConeLength > 0
}

func (p *STARSPane) tpaState(key string) tpaTrackState {
	if p == nil || p.tpaTracks == nil || key == "" {
		return tpaTrackState{}
	}
	return p.tpaTracks[key]
}

func (p *STARSPane) setTPAState(key string, state tpaTrackState) {
	if p == nil || key == "" {
		return
	}
	if p.tpaTracks == nil {
		p.tpaTracks = make(map[string]tpaTrackState)
	}
	if !state.hasGraphic() && state.DisplaySize == nil && !state.InhibitInTrailDistance {
		delete(p.tpaTracks, key)
		return
	}
	p.tpaTracks[key] = state
}

func (p *STARSPane) tpaGraphicCount() int {
	count := 0
	for _, state := range p.tpaTracks {
		if state.hasGraphic() {
			count++
		}
	}
	return count
}

func (p *STARSPane) pruneTPAState(snapshot redsnet.TaisSnapshot) {
	if p == nil || !snapshot.Ready || len(p.tpaTracks) == 0 {
		return
	}
	live := make(map[string]struct{}, len(snapshot.Targets))
	for i := range snapshot.Targets {
		if key := targetDisplayStateKey(&snapshot.Targets[i]); key != "" {
			live[key] = struct{}{}
		}
	}
	for key := range p.tpaTracks {
		if _, ok := live[key]; !ok {
			delete(p.tpaTracks, key)
		}
	}
}

// spcSystemAcknowledgement records a system-wide acknowledgement initiated by
// this pane. Keeping the origin pane lets us retire the shared acknowledgement
// as soon as a later authoritative TAIS snapshot shows that the SPC has cleared
// or changed, so a subsequent occurrence starts flashing again.
type spcAcknowledgement struct {
	Code            string
	ForceAlertColor bool
}

type spcSystemAcknowledgement struct {
	SystemKey string
	Ack       spcAcknowledgement
}

var systemSPCAcknowledgements = struct {
	sync.RWMutex
	acks map[string]spcAcknowledgement
}{acks: make(map[string]spcAcknowledgement)}

func specialConditionCode(target *redsnet.TaisTarget) (string, bool) {
	if _, ok := targetSpecialConditionIndicator(target); !ok || target == nil {
		return "", false
	}
	code := strings.TrimSpace(target.Track.ReportedBeaconCode)
	return code, code != ""
}

func specialConditionSystemKey(target *redsnet.TaisTarget) string {
	if target == nil {
		return ""
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return ""
	}
	facility := strings.ToUpper(strings.TrimSpace(target.Facility))
	return facility + "\x00" + key
}

func (p *STARSPane) targetSPCAcknowledgement(target *redsnet.TaisTarget) (spcAcknowledgement, bool) {
	if p == nil || target == nil {
		return spcAcknowledgement{}, false
	}
	code, ok := specialConditionCode(target)
	if !ok {
		return spcAcknowledgement{}, false
	}

	// A system-wide acknowledgement takes precedence because section 7.16's
	// unassociated-track command specifically changes the indication to the
	// Alert/acknowledge color (red), including a secondary SPC.
	systemKey := specialConditionSystemKey(target)
	if systemKey != "" {
		systemSPCAcknowledgements.RLock()
		ack, acknowledged := systemSPCAcknowledgements.acks[systemKey]
		systemSPCAcknowledgements.RUnlock()
		if acknowledged && ack.Code == code {
			return ack, true
		}
	}

	key := targetDisplayStateKey(target)
	if key != "" && p.spcAcknowledged != nil {
		if ack, acknowledged := p.spcAcknowledged[key]; acknowledged && ack.Code == code {
			return ack, true
		}
	}
	return spcAcknowledgement{}, false
}

func (p *STARSPane) targetSPCAcknowledged(target *redsnet.TaisTarget) bool {
	_, ok := p.targetSPCAcknowledgement(target)
	return ok
}

func (p *STARSPane) setSPCAcknowledged(target *redsnet.TaisTarget, systemWide, forceAlertColor bool) {
	if p == nil || target == nil {
		return
	}
	code, ok := specialConditionCode(target)
	if !ok {
		return
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return
	}
	ack := spcAcknowledgement{Code: code, ForceAlertColor: forceAlertColor}
	if p.spcAcknowledged == nil {
		p.spcAcknowledged = make(map[string]spcAcknowledgement)
	}
	p.spcAcknowledged[key] = ack

	if !systemWide {
		return
	}
	systemKey := specialConditionSystemKey(target)
	if systemKey == "" {
		return
	}
	systemSPCAcknowledgements.Lock()
	systemSPCAcknowledgements.acks[systemKey] = ack
	systemSPCAcknowledgements.Unlock()

	if p.spcSystemAcknowledged == nil {
		p.spcSystemAcknowledged = make(map[string]spcSystemAcknowledgement)
	}
	p.spcSystemAcknowledged[key] = spcSystemAcknowledgement{SystemKey: systemKey, Ack: ack}
}

func (p *STARSPane) pruneSPCAcknowledgements(snapshot redsnet.TaisSnapshot) {
	if p == nil || !snapshot.Ready {
		return
	}
	if len(p.spcAcknowledged) == 0 && len(p.spcSystemAcknowledged) == 0 {
		return
	}

	active := make(map[string]string)
	for i := range snapshot.Targets {
		target := &snapshot.Targets[i]
		code, ok := specialConditionCode(target)
		if !ok {
			continue
		}
		if key := targetDisplayStateKey(target); key != "" {
			active[key] = code
		}
	}

	for key, ack := range p.spcAcknowledged {
		if active[key] != ack.Code {
			delete(p.spcAcknowledged, key)
		}
	}
	for key, systemAck := range p.spcSystemAcknowledged {
		if active[key] == systemAck.Ack.Code {
			continue
		}
		systemSPCAcknowledgements.Lock()
		if ack, ok := systemSPCAcknowledgements.acks[systemAck.SystemKey]; ok && ack == systemAck.Ack {
			delete(systemSPCAcknowledgements.acks, systemAck.SystemKey)
		}
		systemSPCAcknowledgements.Unlock()
		delete(p.spcSystemAcknowledged, key)
	}
}
