package stars

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/juliusplatzer/reds/cmd/wx"
	redslog "github.com/juliusplatzer/reds/log"
	redsmath "github.com/juliusplatzer/reds/math"
	redsnet "github.com/juliusplatzer/reds/net"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	zBackground        renderer.Z = -1000
	zWeather           renderer.Z = -950
	zRangeRings        renderer.Z = -925
	zCompass           renderer.Z = -850
	zRangeBearingLine  renderer.Z = -92
	zMinimumSeparation renderer.Z = -93
	zTargetTPA         renderer.Z = -91
)

// STARSPane is the STARS TCW/TDW display surface. Facility adaptation feeds
// its preference/view state; maps, targets, data blocks, and DCB controls all
// share the same geographic scope transformation as they are added.
type STARSPane struct {
	logger                       *redslog.Logger
	config                       selectedConfig
	prefs                        Preferences
	prefSets                     *starsPreferenceSets
	prefSelected                 *int
	prefRestore                  *starsPreferenceSnapshot
	prefRestoreSelected          *int
	longitudeScaleFactor         float64
	colors                       MonitorColors
	cursorTexture                renderer.TextureID
	cursorTextureAsset           string
	dcbScroll                    float32
	dcbShowAux                   bool
	dcbSuppressPressUntilRelease bool
	useFontSetB                  bool
	useFAAHFSTD010APalette       bool
	hideAssignedAltitudes        bool
	useTAISLeaderDirection       bool // REDS display override, off by default; not a STARS preference set.
	useYellowRestrictionColor1   bool // REDS display-only override; retains the official palette by default.
	systemFont                   *renderer.BitmapFont
	systemFontTextures           map[int]renderer.TextureID
	systemOutlineFont            *renderer.BitmapFont
	systemOutlineFontTextures    map[int]renderer.TextureID
	systemAltimeter              systemAltimeterState
	ssaAirportWeather            ssaAirportWeatherState
	tais                         *redsnet.TaisClient
	tfr                          *redsnet.TFRClient

	wxDomain              wx.Domain
	wxLogger              *redslog.Logger
	wxCenter              configPoint
	wxRadiusNM            float64
	wxStream              *wx.Stream
	wxGrid                *wx.Grid
	nexradGeneration      uint64
	nexradBuiltGeneration uint64
	nexrad                [6]starsNexradLevelCmdBuffers

	videoMaps map[int]*starsVideoMap
	iapChart  iapChartOverlay

	commandMode               CommandMode
	commandInput              string
	commandResponse           string
	multiFuncPrefix           string
	activeBrightnessControl   string
	brightnessDragAccumY      float32
	activeCharSizeControl     string
	charSizeDragAccumY        float32
	rangeRingDragAccumY       float32
	leaderDirectionDragAccumY float32
	leaderLengthDragAccumY    float32
	ptlLengthDragAccumY       float32
	dwellDragAccumY           float32

	// DWELL is a temporary single-track brightness override, not the manual
	// cyan highlight or an aircraft selection. LOCK retains the target while
	// the pointer is stationary; the identity guards against recycled tracks.
	dwellTrackKey      string
	dwellTrackIdentity starsTrackHighlightIdentity
	dwellCursor        redsmath.Vec2
	dwellCursorValid   bool

	// singleTrackQuickLook records TI 6191.409 6.13.4 implied-command
	// quick looks. An unowned associated track normally presents a PDB; a
	// left-trackball slew toggles that track to an unowned FDB until it is
	// reselected. This is transient display state, not a saved preference.
	singleTrackQuickLook map[string]struct{}

	// TI 6191.409 Rev. 30, 6.3.1: MULTI FUNC R toggles an individual
	// track's predicted track line. This state is local to the TCW/TDW and
	// not stored in a preference set. Track identity guards against reuse.
	singleTrackPTLs map[string]starsTrackHighlightIdentity

	// highlightedTracks implements TI 6191.409 Rev. 30, 6.20.  The toggle
	// is local to this TCW/TDW (not a global track or saved preference). A
	// signature prevents a recycled TAIS track key from inheriting cyan.
	highlightedTracks map[string]starsTrackHighlightIdentity

	// singleTrackLeaderDirections records TI 6191.409 6.13.1 / 6.13.17
	// manual data-block orientations. These overrides are local to this
	// TCW/TDW and apply only to the selected associated track. Entering 5
	// removes the override and returns the track to its applicable default.
	singleTrackLeaderDirections map[string]leaderLineDirection

	// beaconCodeReadoutHeld is the frame-local, momentary 6.13.12
	// Beacon Code Readout (physical ABC top-left unlabeled key, mapped to
	// Ctrl+F1 as in VICE). It is sampled from keyboard state each Draw, not
	// toggled or persisted in a preference set.
	beaconCodeReadoutHeld bool

	// ldbBeaconReadoutUntil records TI 6191.409 6.13.2 implied-command
	// beacon readouts. Slew + left trackball on an unassociated track forces
	// its reported beacon code into LDB field 1 for five seconds.
	ldbBeaconReadoutUntil map[string]time.Time

	// SPC acknowledgement is TCW/TDW-local unless the manual calls for
	// system-wide propagation. The value records the active beacon code and any
	// acknowledgement-specific color override.
	spcAcknowledged       map[string]spcAcknowledgement
	spcSystemAcknowledged map[string]spcSystemAcknowledgement

	// Requested-altitude display state is local to this TCW/TDW. The global
	// pointer is nil until the controller overrides the site default with RA;
	// per-track entries implement RA/RAE/RAI followed by a slew.
	requestedAltitudeDisplayOverride *bool
	requestedAltitudeTrackOverrides  map[string]requestedAltitudeDisplayOverride

	// rangeBearingLines are the operator-created *T Range Bearing Lines from
	// TI 6191.409 6.7. wipRBL holds the first endpoint while STARS waits for
	// the second endpoint. RBL state is intentionally transient display state,
	// matching VICE rather than a saved preference.
	rangeBearingLines []starsRangeBearingLine
	wipRBL            *starsRangeBearingLine

	// §6.6 restriction areas: current construction is window-local, completed
	// geometry is stored at the STARS site level (shared by panes at that site).
	wipRestrictionArea *starsRestrictionArea
	wipRestrictionMove *restrictionAreaPendingMove
	// Last TFR client revision merged into the shared GEO RESTRICTIONS store.
	tfrListRevision uint64
	// §6.6.12: the two-ENTER DoD color-selection sequence.
	restrictionColorPendingID int
	restrictionColorChanged   bool
	restrictionAreas          *starsRestrictionAreaStore

	// MIN graphics are transient TCW/TDW state, not a preference. Pair keys
	// identify TAIS tracks rather than callsigns (unassociated 1200 targets
	// have no flight-plan ACID). A new selection does not erase the old pair
	// until both new targets are selected.
	minSepTracks     [2]string
	minSepPendingKey string

	// taisOwnership retains the last known controlling TCP while TAIS reports
	// OCR=PENDING. In AIG200, CPS changes meaning in that state: it names the
	// handoff receiver rather than the current owner. Keeping the last owner
	// lets REDS reproduce the STARS handoff presentation without treating the
	// receiving TCP as though it already owned the track.
	taisOwnership map[string]taisOwnershipState

	// Coast/Suspend tab line numbers are system-provided in real STARS/CRC.
	// TAIS does not carry them yet, so REDS keeps a display-local 0..99
	// allocator for suspended-list presentation until authoritative line-number
	// data is added to the feed.
	coastSuspendLineNumbers   map[string]int
	coastSuspendFirstSeen     map[string]uint64
	coastSuspendFrozenAtEntry map[string]bool
	coastSuspendSequence      uint64
	coastSuspendNextLine      int

	// tpaTracks stores manual TPA J-Ring/Cone state local to this TCW/TDW.
	tpaTracks map[string]tpaTrackState

	// atpaTracks is rebuilt from the current TAIS snapshot and adapted ATPA
	// approach volumes. It holds pairing, minimum separation, and current
	// Monitor/Warning/Alert state.
	atpaTracks map[string]atpaTrackState

	// atpaSite is shared by every pane for the same STARS facility. ATPA
	// system/volume/2.5 enable state is supervisor-controlled site state, not
	// a saved per-position preference (TI 6191.409 Rev. 30, 8.37-8.39).
	atpaSite               *atpaSiteRuntimeState
	atpaTrackSettingsReset uint64
	atpaWarnAlertReset     uint64

	// CRDA modes/ghosts belong to this TCW; no TAIS state is mutated.
	crda crdaRuntime

	// trackRepositions stores TI 6191.409 5.7.3 TRK RPOS presentation state.
	// TAIS itself is read-only, so the local TCW/TDW snapshot is rewritten for
	// display without mutating live feed state.
	trackRepositions     map[string]trackRepositionState
	rposPendingSourceKey string
}

// AssignedAltitudesHidden reports the display-only burger-menu override used
// to suppress FDB assigned-altitude field 7. It deliberately does not alter
// TAIS flight-plan state or STARS preference sets.
func (p *STARSPane) AssignedAltitudesHidden() bool {
	return p != nil && p.hideAssignedAltitudes
}

// ToggleAssignedAltitudesHidden changes only this TCW/TDW presentation.
func (p *STARSPane) ToggleAssignedAltitudesHidden() {
	if p != nil {
		p.hideAssignedAltitudes = !p.hideAssignedAltitudes
	}
}

// UseTAISLeaderDirection reports the REDS-only burger-menu display override.
// It is local to this TCW/TDW and is deliberately not persisted in a STARS
// preference set. The zero value is off (normal <LDR DIR> behavior).
func (p *STARSPane) UseTAISLeaderDirection() bool {
	return p != nil && p.useTAISLeaderDirection
}

// ToggleTAISLeaderDirection uses the TAIS flightPlan.lld even on locally owned
// tracks when it is enabled. Explicit single-track leader positioning still
// takes precedence. Turning it on cancels an active DCB LDR DIR adjustment.
func (p *STARSPane) ToggleTAISLeaderDirection() {
	if p == nil {
		return
	}
	p.useTAISLeaderDirection = !p.useTAISLeaderDirection
	if p.useTAISLeaderDirection && p.commandMode == CommandModeLDRDir {
		p.resetCommand()
	}
}

// UseYellowRestrictionColor1 reports the local REDS display override for
// restriction geometry color 1. The zero value leaves the official dim-gray
// palette unchanged, including all stored restriction-area color numbers.
func (p *STARSPane) UseYellowRestrictionColor1() bool {
	return p != nil && p.useYellowRestrictionColor1
}

func (p *STARSPane) ToggleYellowRestrictionColor1() {
	if p != nil {
		p.useYellowRestrictionColor1 = !p.useYellowRestrictionColor1
	}
}

// resolvedQuickLookTCP expands the one-character controller-symbol shorthand
// documented by TI 6191.409 section 6.13 using the entering position's
// controller subset, then validates the resulting TCP against facility
// adaptation. REDS' generated STARS configs currently carry explicit TCPs but
// not Quicklook Group IDs, so group IDs remain invalid until that adaptation is
// exported by crc2reds.
func (p *STARSPane) resolvedQuickLookTCP(input string) (string, bool) {
	if p == nil {
		return "", false
	}
	input = strings.ToUpper(strings.TrimSpace(input))
	own := strings.ToUpper(strings.TrimSpace(p.config.ControlPosition.TCP))
	if len(input) == 1 && len(own) == 2 {
		input = own[:1] + input
	}
	if len(input) != 2 {
		return "", false
	}
	for _, position := range p.config.Facility.ControlPositions {
		if strings.EqualFold(strings.TrimSpace(position.TCP), input) {
			return strings.ToUpper(strings.TrimSpace(position.TCP)), true
		}
	}
	return "", false
}

func (p *STARSPane) ownTCP() string {
	if p == nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(p.config.ControlPosition.TCP))
}

// toggleQuickLookTCP implements the per-owner toggle in TI 6191.409 6.13.5
// and 6.13.13. tcp is already resolved/validated by the command handler. The
// bool stored in QuickLookTCPs is the manual's quick-look-plus state: false =
// Full DB in Unowned color, true = Full DB in Owned color.
func (p *STARSPane) toggleQuickLookTCP(tcp string, plus bool) {
	ps := p.currentPrefs()
	if ps.QuickLookTCPs == nil {
		ps.QuickLookTCPs = make(map[string]bool)
	}
	// Selecting an individual TCP leaves the ALL modality, matching VICE.
	ps.QuickLookAll = false
	ps.QuickLookAllIsPlus = false
	if currentPlus, enabled := ps.QuickLookTCPs[tcp]; enabled && currentPlus == plus {
		delete(ps.QuickLookTCPs, tcp)
	} else {
		ps.QuickLookTCPs[tcp] = plus
	}
}

// disableQuickLooks implements 6.13.15's distinction between Q and Q+:
// without +, ordinary quick looks are removed while QL+ entries remain;
// with +, quick-look-plus entries are removed while ordinary entries remain.
func (p *STARSPane) disableQuickLooks(plus bool) {
	if p == nil {
		return
	}
	ps := p.currentPrefs()
	for tcp, isPlus := range ps.QuickLookTCPs {
		if isPlus == plus {
			delete(ps.QuickLookTCPs, tcp)
		}
	}
	if ps.QuickLookAll && ps.QuickLookAllIsPlus == plus {
		ps.QuickLookAll = false
		ps.QuickLookAllIsPlus = false
	}
}

func (p *STARSPane) hasQuickLookStatus() bool {
	if p == nil {
		return false
	}
	ps := p.currentPrefs()
	return ps.QuickLookAll || len(ps.QuickLookTCPs) != 0
}

// qlPositionsString follows VICE's ordering and two-line width: quick-look-plus
// TCPs first, then normal TCPs, each group lexicographically sorted. Table 2-15
// permits up to two SSA lines and uses a trailing spaced '+' when additional
// TCPs cannot be shown.
func (p *STARSPane) qlPositionsString() string {
	if p == nil {
		return ""
	}
	ps := p.currentPrefs()
	var entries []string
	if ps.QuickLookAll {
		if ps.QuickLookAllIsPlus {
			entries = append(entries, "ALL+")
		} else {
			entries = append(entries, "ALL")
		}
	}

	tcps := make([]string, 0, len(ps.QuickLookTCPs))
	for tcp := range ps.QuickLookTCPs {
		tcps = append(tcps, tcp)
	}
	sort.Slice(tcps, func(i, j int) bool {
		iPlus, jPlus := ps.QuickLookTCPs[tcps[i]], ps.QuickLookTCPs[tcps[j]]
		if iPlus != jPlus {
			return iPlus
		}
		return tcps[i] < tcps[j]
	})
	for _, tcp := range tcps {
		if ps.QuickLookTCPs[tcp] {
			tcp += "+"
		}
		entries = append(entries, tcp)
	}

	if len(entries) == 0 {
		return ""
	}

	// Wrap at 32 characters, at most two lines. If more TCPs remain, Table
	// 2-15 specifies a spaced '+' at the end of line two.
	lines := make([]string, 0, 2)
	idx := 0
	for len(lines) < 2 && idx < len(entries) {
		line := entries[idx]
		idx++
		for idx < len(entries) && len(line)+1+len(entries[idx]) <= 32 {
			line += " " + entries[idx]
			idx++
		}
		lines = append(lines, line)
	}
	if idx < len(entries) && len(lines) == 2 {
		line := lines[1]
		for len(line)+2 > 32 {
			cut := strings.LastIndexByte(line, ' ')
			if cut < 0 {
				line = line[:min(len(line), 30)]
				break
			}
			line = line[:cut]
		}
		lines[1] = strings.TrimSpace(line) + " +"
	}
	return strings.Join(lines, "\n")
}

// targetQuickLookState combines the existing single-track implied quick look
// with the owner-wide quick-look state. The second result is QL+ and therefore
// controls Owned-vs-Unowned color; brightness remains OTH because ownership
// itself has not changed.
func (p *STARSPane) targetQuickLookState(target *redsnet.TaisTarget) (bool, bool) {
	if p == nil || target == nil || target.FlightPlan == nil ||
		p.targetOwnedByCurrentTCP(target) || p.targetInboundHandoff(target) {
		return false, false
	}

	enabled := p.targetSingleTrackQuickLooked(target)
	plus := false
	ps := p.currentPrefs()
	if ps.QuickLookAll {
		enabled = true
		plus = ps.QuickLookAllIsPlus
	}
	owner, ownerOK := p.targetOwnerTCP(target)
	if ownerOK {
		if ownerPlus, ok := ps.QuickLookTCPs[owner]; ok {
			// 6.13.14 keeps pre-existing individual QL states as exceptions
			// when QL ALL/ALL+ is selected: ordinary QL stays Unowned color
			// under ALL+, and QL+ stays Owned color under ordinary ALL.
			enabled = true
			plus = ownerPlus
		}
	}
	return enabled, plus
}

// quickLookDisplayStatus is the subset of 6.13.6/6.13.16 REDS can currently
// display. Quicklook-region adaptation is not yet exported by crc2reds, so the
// Preview Area contains the enabled TCPs/ALL state only.
func (p *STARSPane) quickLookDisplayStatus() string {
	return p.qlPositionsString()
}

// ssaConsolidationCouplingLines returns fields S-U from TI 6191.409 Rev. 30,
// Table 2-15. A normal keyboard with its own airspace is represented as
//
//	1R CON: 1R
//
// while a keyboard coupled to another TCP would use CPL instead. REDS does
// not currently receive STARS keyboard/SISO consolidation or coupling state
// from TAIS, and each live pane is created for one selected control position.
// Therefore the only state that can be stated faithfully today is the selected
// keyboard's primary TCP assignment. Do not infer additional consolidation
// from flight-plan CPS values: CPS describes track ownership, not the keyboard
// consolidation topology.
//
// Keeping this behind a dedicated helper mirrors VICE's separation between
// SSA rendering and simulator consolidation state, and gives the future SISO
// integration a single place to replace the default line with real CON/CPL
// membership (including *, /, and the ten-TCP '+' truncation rules).
func (p *STARSPane) ssaConsolidationCouplingLines() []string {
	if p == nil {
		return nil
	}
	tcp := p.ownTCP()
	if tcp == "" {
		return nil
	}
	return []string{tcp + " CON: " + tcp}
}

// monitorColorsForPosition selects the monitor palette adapted for the
// controller position. CRC emits lower-case "tcw", "tdw", and "dod" color
// sets. DoD STARS uses a separate palette that REDS does not model yet, so keep
// its existing TCW fallback until that palette is implemented.
func monitorColorsForPosition(colorSet string) MonitorColors {
	switch strings.ToLower(strings.TrimSpace(colorSet)) {
	case "tdw":
		return defaultTDWColors
	case "", "tcw", "dod":
		return defaultTCWColors
	default:
		return defaultTCWColors
	}
}

// NewPane creates a STARS TCW pane for the selected controller position.
func NewPane(artcc, tracon, positionID string, logger *redslog.Logger) (*STARSPane, error) {
	if logger == nil {
		logger = &redslog.Logger{Logger: slog.Default(), Start: time.Now()}
	}

	cfg, err := loadSelectedConfig(artcc, tracon, positionID)
	if err != nil {
		return nil, err
	}

	const (
		useFontSetB            = false
		useFAAHFSTD010APalette = false
	)

	colors := monitorColorsForPosition(cfg.ControlPosition.ColorSet)
	prefSets, prefSetsErr := sharedStarsPreferenceSets(cfg.Facility.Facility)
	if prefSets == nil {
		prefSets = &starsPreferenceSets{}
	}
	pane := &STARSPane{
		logger:                 logger,
		config:                 cfg,
		prefs:                  newPreferences(cfg),
		prefSets:               prefSets,
		colors:                 colors,
		useFontSetB:            useFontSetB,
		useFAAHFSTD010APalette: useFAAHFSTD010APalette,
		systemFont:             newSystemFont(useFontSetB),
		systemOutlineFont:      newSystemOutlineFont(useFontSetB),
	}
	if prefSetsErr != nil {
		logger.Warn("Unable to load STARS preference sets; starting with empty slots",
			slog.String("facility", cfg.Facility.Facility),
			slog.Any("error", prefSetsErr))
	}
	pane.atpaSite = sharedATPASiteRuntimeState(cfg)
	pane.crda = newCRDARuntime(cfg.Facility.RPCs)
	pane.longitudeScaleFactor = pane.initialLongitudeScaleFactor()
	// Validate the magnetic-adaptation resource once at startup. The actual
	// value is selected from the tile containing the current display center in
	// scopeTransformations(), so OFF CNTR can cross a tile boundary exactly as
	// the FAA maintenance procedure's "display center Magnetic Variation Tile"
	// wording implies.
	center := pane.currentCenter()
	if _, lookupErr := radar.MagneticVariationAt(center.Lat, center.Lon); lookupErr != nil {
		logger.Warn("Unable to resolve STARS magnetic variation; true-north orientation will be used where lookup fails",
			slog.Float64("lat", center.Lat),
			slog.Float64("lon", center.Lon),
			slog.Any("error", lookupErr))
	}
	if err := pane.refreshCRDAEngine(); err != nil {
		logger.Warn("Unable to initialize STARS CRDA adaptation", slog.Any("error", err))
	}
	pane.initializeSystemAltimeter(cfg.systemAltimeterAirport())
	pane.initializeSSAAirportWeather(cfg.Area.SSAAirports)
	pane.wxDomain = wx.DomainForARTCC(cfg.Facility.ARTCC)
	pane.wxLogger = logger.With(slog.String("component", "wx"))
	pane.restartNexradStream(starsInitialNexradRadiusNM)
	if err := pane.loadMainVideoMaps(); err != nil {
		logger.Warn("Unable to load STARS Main DCB video maps", slog.Any("error", err))
	}

	// Keep transport/state ownership in net.TaisClient. The STARS pane only
	// owns the client's lifetime; the later fusion layer can consume detached
	// snapshots without coupling display code to WebSocket/revision handling.
	pane.tais = redsnet.NewTaisClient(
		redsnet.TaisWebSocketURL(),
		pane.config.Facility.Facility,
		logger.With(slog.String("component", "tais")),
	)
	pane.tais.Start()

	// The TFR client caches published FAA geometry in the background. Rendering
	// and user-facing visibility controls are introduced separately.
	if tfr, err := redsnet.NewTFRClient(redsnet.TFRGeoJSONURL(), redsnet.TFRPollInterval); err != nil {
		logger.Warn("Unable to configure FAA TFR client", slog.Any("error", err))
	} else {
		pane.tfr = tfr
		pane.tfr.Start()
	}

	return pane, nil
}

func (p *STARSPane) Draw(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil {
		return
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	backgroundCB := zcb.At(zBackground)
	backgroundCB.Viewport(x, y, width, height)
	backgroundCB.Scissor(x, y, width, height)
	backgroundCB.ClearRGB(p.colors.Background)
	backgroundCB.DisableScissor()

	p.consumeSystemAltimeterUpdates()
	p.refreshSystemAltimeter()
	p.consumeSSAAirportWeatherUpdates()
	p.refreshSSAAirportWeather()
	p.consumeNexradUpdates()
	p.ensureNexradCoverage(ctx)
	p.rebuildNexradIfNeeded()

	// Sample before any target filtering/drawing so the FDB/PDB/LDB
	// presentation and altitude-filter bypass agree for the whole frame.
	// A missing/captured keyboard or key release immediately restores STARS.
	p.beaconCodeReadoutHeld = starsBeaconCodeReadoutHeld(ctx.Keyboard)
	p.processKeyboardInput(ctx)
	transforms := p.scopeTransformations(ctx)
	allTargets := p.targetSnapshot()
	now := time.Now()
	p.updateTaisOwnership(allTargets, now)

	// Coast Phase 2 removes an ordinary track from the radar display and its
	// TPA/ATPA processing, but the retained server record is still needed for
	// the Coast/Suspend List. Keep separate snapshots rather than deleting the
	// source target at Total Coast Time.
	activeTargets := targetWithoutCoastPhase2(allTargets)
	// ATPA is system processing derived from the authoritative surveillance
	// positions, not from any local TRK RPOS display relocation.
	p.updateATPAInTrail(activeTargets)
	// TRK RPOS is local display state layered on top of the authoritative TAIS
	// snapshot. Entering Coast Phase 2 also removes local display-only target
	// state such as TPA, quick-look, and manual leader direction.
	p.pruneTrackRepositions(activeTargets)
	p.pruneSingleTrackQuickLook(activeTargets)
	p.pruneSingleTrackLeaderDirections(activeTargets)
	p.pruneLDBBeaconReadouts(activeTargets, now)
	// Preserve manual TPA state while a track is in the Coast/Suspend List.
	// Figure 2-30 removes the J-ring/cone from presentation while listed, but
	// requires it to redisplay automatically if the flight plan associates to
	// the track again. Only a true source removal should discard that state.
	p.pruneTPAState(allTargets)
	p.pruneRequestedAltitudeDisplayOverrides(activeTargets)

	// SPC acknowledgement must survive while a Phase 2 special-condition track
	// remains visible at its last valid position. Other Phase 2 tracks are
	// excluded from the radar-facing snapshot below.
	p.pruneSPCAcknowledgements(allTargets)
	targets := p.radarTargetSnapshot(allTargets)
	// Preserve a highlighted coast-phase-2 SPC target for as long as it
	// remains on-screen; discard local cyan only after the displayed track
	// disappears, is suspended, or is replaced by another aircraft.
	p.pruneTrackHighlights(targets)
	targets = p.applyTrackRepositions(targets)
	p.pruneSingleTrackPTLs(targets)
	if err := p.refreshCRDAEngine(); err != nil {
		// Keep the last valid regions; a malformed facility update must not
		// take down the radar pane.
		p.logger.Warn("Unable to update STARS CRDA regions", slog.Any("error", err))
	}
	p.updateCRDA(allTargets) // live, pre-RPOS source locations
	// Ghost picking must use the same frame's projected positions as drawing.
	p.consumeMouseEvents(ctx, transforms, targets, allTargets)
	p.updateDwell(ctx, targets, transforms)

	p.drawNexrad(ctx, zcb, transforms)
	p.drawRangeRings(ctx, zcb, transforms)
	p.drawVideoMaps(ctx, zcb, transforms)
	p.drawIAPChartOverlay(ctx, zcb, transforms)
	p.drawCRDADiagrams(ctx, zcb, transforms)
	p.drawCompass(ctx, zcb, transforms)

	// Apply new FAA snapshots before both geometry and GEO RESTRICTIONS list
	// are rendered. Fetching itself remains on TFRClient's background goroutine.
	p.syncTFRRestrictionList()
	p.drawTargetHistory(ctx, zcb, transforms, targets)
	p.drawPredictedTrackLines(ctx, zcb, transforms, targets)
	p.drawRangeBearingLines(ctx, zcb, transforms, targets)
	p.drawRestrictionAreas(ctx, zcb, transforms)
	p.drawMinSep(ctx, zcb, transforms, targets)
	// TPA graphics are suppressed while a track is in the Coast/Suspend List,
	// while their local state is retained above for automatic redisplay.
	p.drawTPA(ctx, zcb, transforms, targetOutsideCoastSuspendList(targets))
	p.drawTargets(ctx, zcb, transforms, targets)
	p.drawTargetLeaderLines(ctx, zcb, transforms, targets, now)
	p.drawTargetPositionSymbols(ctx, zcb, transforms, targets, now)
	p.drawDatablocks(ctx, zcb, transforms, targets, now)
	p.drawCRDAGhosts(ctx, zcb, transforms, allTargets)

	if p.currentPrefs().DisplayDCB {
		p.drawDCB(ctx, zcb)
	}
	p.drawPreviewArea(ctx, zcb)
	p.drawSSA(ctx, zcb)
	p.drawCoastSuspendList(ctx, zcb, allTargets)
	p.drawTowerLists(ctx, zcb, activeTargets)
	p.drawVideoMapsList(ctx, zcb)
	p.drawRestrictionAreasList(ctx, zcb)
	p.drawCRDAStatusList(ctx, zcb)
	p.applyCursor(ctx)
	p.renderCursor(ctx, zcb)
}

func (p *STARSPane) Dispose() {
	if p == nil {
		return
	}
	if p.tais != nil {
		p.tais.Close()
		p.tais = nil
	}
	if p.tfr != nil {
		p.tfr.Close()
		p.tfr = nil
	}
	if p.wxStream != nil {
		p.wxStream.Close()
		p.wxStream = nil
	}
	p.releaseNexradCmdBuffers()
	p.releaseVideoMaps()
	p.iapChart.release()
	p.wxGrid = nil
}

func (p *STARSPane) scopeTransformations(ctx *panes.Context) radar.LatLonTransformations {
	if p == nil || ctx == nil {
		return radar.LatLonTransformations{}
	}
	center := p.currentCenter()
	paneExtent := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())

	// TI 6191.409 Rev. 30, 4.4.1 defines display range as the distance from
	// the display center to the nearest screen edge. FAA JO 6191.3 §§522-523
	// and §556 describe STARS map alignment to site magnetic variation, with
	// maps themselves built to true north. Select the piecewise-constant value
	// of the REDS magnetic tile containing the current display center. The tile
	// geometry is WMM-derived approximation data, not an FAA/OSF tile listing.
	magneticVariation := 0.0
	if variation, err := radar.MagneticVariationAt(center.Lat, center.Lon); err == nil {
		magneticVariation = variation
	}
	return radar.GetLatLonTransformations(
		paneExtent,
		center.Lat,
		center.Lon,
		p.longitudeScaleFactor,
		float64(p.currentPrefs().Range),
		magneticVariation,
	)
}

func (p *STARSPane) consumeMouseEvents(
	ctx *panes.Context,
	transforms radar.LatLonTransformations,
	targets redsnet.TaisSnapshot,
	crdaParents redsnet.TaisSnapshot,
) {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return
	}

	// Active STARS adjustment buttons capture trackball motion. RR changes
	// range-ring spacing; LDR DIR changes the owned-data-block orientation.
	// Neither adjustment may also pan or zoom the radar scope.
	if p.commandMode == CommandModeCharSizeSpinner {
		// TI 6191.409 Rev. 30, 4.9.1 completes a trackball CHAR SIZE
		// adjustment with the left trackball button. Clicking back in the DCB
		// is handled by the submenu itself; a scope click simply returns to the
		// CHAR SIZE submenu without changing the selected value.
		if ctx.Mouse.WasPressed(platform.MouseButtonLeft) && !p.mouseOverDCB(ctx) {
			p.commandMode = CommandModeCharSize
			p.activeCharSizeControl = ""
			p.charSizeDragAccumY = 0
			p.commandInput = ""
			p.commandResponse = ""
		}
		return
	}
	if p.commandMode == CommandModeRangeRings {
		p.adjustRangeRingSpacing(ctx)
		return
	}
	if p.commandMode == CommandModeLDRDir {
		p.adjustLeaderLineDirection(ctx)
		return
	}
	if p.commandMode == CommandModeLDRLen {
		p.adjustLeaderLineLength(ctx)
		return
	}
	if p.commandMode == CommandModePTLLength {
		p.adjustPTLLength(ctx)
		return
	}
	if p.commandMode == CommandModeDwell {
		p.adjustDwell(ctx)
		return
	}
	if p.mouseOverDCB(ctx) {
		return
	}

	// Restriction-area construction consumes trackball clicks before ordinary
	// target slews, CRDA ghost picks and scope-center gestures.
	if p.commandMode == CommandModeRestrictionArea &&
		ctx.Mouse.WasReleased(platform.MouseButtonLeft) {
		lat, lon := transforms.LatLonFromWindow(ctx.Mouse.Pos)
		if err := p.restrictionAreaClick(configPoint{Lat: lat, Lon: normalizeLongitude(lon)}); err != nil {
			p.commandResponse = err.Error()
		} else {
			p.commandResponse = ""
		}
		return
	}

	mouse := ctx.Mouse
	ps := p.currentPrefs()

	// TI 6191.409 Rev. 30, 6.9: D* is a cursor-position readout, not
	// a target slew. Left trackball designates the precise scope location;
	// clear command-entry state before publishing the Preview Area result.
	// No ENTER is required (and D* + ENTER is FORMAT).
	if p.commandMode == CommandModeMultiFunc &&
		strings.EqualFold(p.multiFuncPrefix+p.commandInput, "D*") &&
		mouse.WasReleased(platform.MouseButtonLeft) {
		lat, lon := transforms.LatLonFromWindow(mouse.Pos)
		p.resetCommand()
		p.commandResponse = starsCursorLatLonReadout(lat, normalizeLongitude(lon))
		return
	}

	// TI 6191.409 Rev. 30, 6.4: MIN uses two consecutive trackball
	// designations, and must consume the click before normal implied
	// commands (including CRDA ghost/data-block selection).
	if p.commandMode == CommandModeMin {
		if mouse.WasReleased(platform.MouseButtonLeft) {
			p.selectMinSepTarget(closestSlewTarget(targets, mouse.Pos, transforms), targets)
		}
		return
	}

	// TI 6191.409 Rev. 30, 6.3.1: <MULTI FUNC> <R> then slew and
	// left-trackball toggles one PTL. Repetitive mode is allowed; leave
	// FR in the Preview Area after each designation (including ILL TRK).
	// Consume this selection before bare-slew quick look or CRDA handling.
	if p.commandMode == CommandModeMultiFunc &&
		strings.EqualFold(p.multiFuncPrefix+p.commandInput, "R") &&
		mouse.WasReleased(platform.MouseButtonLeft) {
		p.commandResponse = ""
		if err := p.toggleSingleTrackPTL(closestSlewTarget(targets, mouse.Pos, transforms)); err != nil {
			p.commandResponse = err.Error()
		}
		return
	}

	// TI 6191.409 Rev. 30, 6.20: slew to a track and release the
	// middle trackball button to toggle its cyan highlight. CRC/VICE use
	// the target position (not the detached data-block text) for picking.
	// An ordinary highlight must not consume another active command.
	if p.commandMode == CommandModeNone && p.commandInput == "" &&
		mouse.WasReleased(platform.MouseButtonMiddle) {
		if target := closestSlewTarget(targets, mouse.Pos, transforms); target != nil {
			if err := p.toggleTrackHighlight(target, time.Now()); err != nil {
				p.commandResponse = err.Error()
			} else {
				p.commandResponse = ""
			}
			return
		}
	}

	// TI 6191.409 6.5: N+slew on a ghost suppresses it, N+slew on
	// its parent forces/restores ghosting, and N*+slew toggles the parent's
	// qualification override. A plain slew to the ghost DATA BLOCK toggles
	// FDB/PDB (§6.5.7); the symbol alone is selectable only with N/N*.
	// Prefer the nearest ghost to a real track when both occupy a hit radius.
	if mouse.WasReleased(platform.MouseButtonLeft) {
		entry := strings.ToUpper(p.multiFuncPrefix + p.commandInput)
		isMultiN := p.commandMode == CommandModeMultiFunc && (entry == "N" || entry == "N*")
		if isMultiN || (p.commandMode == CommandModeNone && p.commandInput == "") {
			if key, ok := p.closestCRDAGhost(mouse.Pos, transforms, crdaParents, isMultiN); ok {
				if isMultiN && entry == "N*" {
					// CRC N*+ghost queries the parent track's live readout.
					p.commandResponse = p.crdaParentReadout(targets, key)
					return
				}
				if err := p.crdaSlewGhost(key, isMultiN); err != nil {
					p.commandResponse = err.Error()
				} else if isMultiN {
					p.resetCommand()
				}
				return
			}
			if isMultiN {
				target := closestSlewTarget(targets, mouse.Pos, transforms)
				if err := p.crdaSlewParent(target, entry == "N*"); err != nil {
					p.commandResponse = err.Error()
				} else {
					p.resetCommand()
				}
				return
			}
		}
	}

	// VICE 6.5.12: TN + scope click moves and shows the CRDA status list.
	if p.commandMode == CommandModeMultiFunc &&
		strings.EqualFold(p.multiFuncPrefix+p.commandInput, "TN") &&
		mouse.WasPressed(platform.MouseButtonLeft) {
		w, h := ctx.PaneRect.Width(), ctx.PaneRect.Height()
		if w > 0 && h > 0 && p.crdaAvailable() {
			ps.CRDAStatusList.Position = [2]float32{mouse.Pos.X / w, mouse.Pos.Y / h}
			ps.CRDAStatusList.Visible = true
		}
		p.resetCommand()
		return
	}

	// TI 6191.409 Rev. 30, 4.4.2 PLACE CNTR. After the Main DCB
	// selection, ordinary trackball/cursor motion pans the radar picture; the
	// left trackball button fixes the new user-defined center and exits. Unlike
	// right-button desktop panning, no mouse button is held while moving.
	if p.commandMode == CommandModePlaceCenter {
		if mouse.Delta.X != 0 || mouse.Delta.Y != 0 {
			deltaLat, deltaLon := transforms.LatLonFromWindowV(mouse.Delta)
			ps.UserCenter.Lat -= deltaLat
			ps.UserCenter.Lon = normalizeLongitude(ps.UserCenter.Lon - deltaLon)
			ps.UseUserCenter = true
		}
		if mouse.WasReleased(platform.MouseButtonLeft) {
			p.setCommandMode(CommandModeNone)
		}
		return
	}

	// TI 6191.409 Rev. 30, 5.7.3 TRK RPOS. F2 enters a repetitive
	// two-designation command. The source may be selected by slew or identified
	// by ACID/discrete beacon code; the destination is either an unassociated
	// track or an empty scope position, which creates an Unsupported FDB.
	if p.commandMode == CommandModeTrackReposition && mouse.WasReleased(platform.MouseButtonLeft) {
		var source *redsnet.TaisTarget
		if p.rposPendingSourceKey != "" {
			for i := range targets.Targets {
				if targetDisplayStateKey(&targets.Targets[i]) == p.rposPendingSourceKey {
					source = &targets.Targets[i]
					break
				}
			}
			if source == nil {
				p.rposPendingSourceKey = ""
				p.commandResponse = ErrSTARSNoTrack.Error()
				return
			}
		} else if strings.TrimSpace(p.commandInput) != "" {
			var err error
			source, err = p.rposTargetByReference(targets, p.commandInput)
			if err != nil {
				p.commandResponse = err.Error()
				return
			}
		} else {
			source = closestSlewTarget(targets, mouse.Pos, transforms)
			if source == nil {
				p.commandResponse = ErrSTARSNoTrack.Error()
				return
			}
			if !p.rposSourceEligible(source) {
				p.commandResponse = ErrSTARSIllegalTrack.Error()
				return
			}
			p.rposPendingSourceKey = targetDisplayStateKey(source)
			p.commandInput = ""
			p.commandResponse = ""
			return
		}

		if !p.rposSourceEligible(source) {
			p.commandResponse = ErrSTARSIllegalTrack.Error()
			return
		}

		destination := closestSlewTarget(targets, mouse.Pos, transforms)
		if destination != nil {
			if destination.FlightPlan != nil {
				p.commandResponse = ErrSTARSIllegalTrack.Error()
				return
			}
			if err := p.setTrackReposition(source, destination, configPoint{}); err != nil {
				p.commandResponse = err.Error()
				return
			}
		} else {
			lat, lon := transforms.LatLonFromWindow(mouse.Pos)
			if err := p.setTrackReposition(source, nil, configPoint{Lat: lat, Lon: lon}); err != nil {
				p.commandResponse = err.Error()
				return
			}
		}

		// TRK RPOS is repetitive: clear the two designations but stay in RP mode.
		p.rposPendingSourceKey = ""
		p.commandInput = ""
		p.commandResponse = ""
		return
	}

	// TI 6191.409 Rev. 30, 7.16: <MULTI FUNC>, <G>, slew + left
	// trackball acknowledges an SPC on an unassociated track system-wide. This
	// is distinct from the bare-slew 7.3 acknowledgement, which is local for an
	// unassociated SPC.
	if p.commandMode == CommandModeMultiFunc &&
		strings.EqualFold(p.multiFuncPrefix+p.commandInput, "G") &&
		mouse.WasReleased(platform.MouseButtonLeft) {
		target := closestSlewTarget(targets, mouse.Pos, transforms)
		if err := p.acknowledgeUnassociatedSPCSystemWide(target); err != nil {
			p.commandResponse = err.Error()
			return
		}
		p.resetCommand()
		return
	}

	// TI 6191.409 Rev. 30, 6.13.24: <MULTI FUNC>, <R>, <A>,
	// optionally <E>/<I>, then slew + left trackball controls requested-altitude
	// display for one Full data block. The keyboard-only all-FDB forms are
	// registered in cmdtools.go.
	if p.commandMode == CommandModeMultiFunc && mouse.WasReleased(platform.MouseButtonLeft) {
		command := strings.ToUpper(p.multiFuncPrefix + p.commandInput)
		if command == "RA" || command == "RAE" || command == "RAI" {
			target := closestSlewTarget(targets, mouse.Pos, transforms)
			if err := p.applyRequestedAltitudeDisplayCommand(target, command); err != nil {
				p.commandResponse = err.Error()
				return
			}
			p.resetCommand()
			return
		}
	}

	// TI 6191.409 Rev. 30, 6.13.1 / 6.13.17 Specify data block
	// position for a single associated track. The implied form is simply
	// <direction>, slew, left trackball. The explicit keyboard form is
	// <MULTI FUNC>, L, <direction>, slew, left trackball. Both echo the
	// direction in the Preview Area while waiting for the slew. Direction 5
	// removes the manual per-track setting. The explicit form is repetitive.
	if mouse.WasReleased(platform.MouseButtonLeft) {
		directionInput := ""
		repetitive := false
		if p.commandMode == CommandModeNone {
			directionInput = p.commandInput
		} else if p.commandMode == CommandModeMultiFunc && strings.EqualFold(p.multiFuncPrefix, "L") {
			directionInput = p.commandInput
			repetitive = true
		}

		if direction, recognized, err := singleTrackLeaderDirectionCommand(directionInput); recognized {
			if err != nil {
				p.commandResponse = err.Error()
				return
			}
			target := closestSlewTarget(targets, mouse.Pos, transforms)
			if target == nil {
				if repetitive {
					p.commandResponse = ErrSTARSNoTrack.Error()
				}
				return
			}
			if !p.targetSupportsSingleTrackLeaderDirection(target) {
				p.commandResponse = ErrSTARSIllegalTrack.Error()
				return
			}
			p.setSingleTrackLeaderDirection(target, direction)
			p.commandResponse = ""
			if !repetitive {
				p.resetCommand()
			}
			return
		}
	}

	// TI 6191.409 Rev. 30, 6.21.2-6.21.10 manual TPA commands plus
	// single-track ATPA Warning/Alert, Monitor, and INTRAIL DIST overrides
	// (6.21.12, 6.21.14, and 6.21.16).
	// The keyboard portion remains in the Preview Area while the operator slews
	// to a target and selects the left trackball button. These commands are
	// repetitive, so a successful selection does not clear the entry.
	if p.commandMode == CommandModeNone && mouse.WasReleased(platform.MouseButtonLeft) {
		op, distance, sizeMode, recognized, err := parseTPAImpliedCommand(p.commandInput)
		if recognized {
			if err != nil {
				p.commandResponse = err.Error()
				return
			}
			target := closestSlewTarget(targets, mouse.Pos, transforms)
			if target == nil {
				p.commandResponse = ErrSTARSNoTrack.Error()
				return
			}
			if err := p.applyTPAImpliedCommand(target, op, distance, sizeMode); err != nil {
				p.commandResponse = err.Error()
				return
			}
			p.commandResponse = ""
			return
		}
	}

	// TI 6191.409 Rev. 30, 6.7 Create Range Bearing Line. Once *T has
	// been entered, the left trackball may designate either a track or an
	// arbitrary geographic point. The first designation starts the RBL and
	// the second completes it. As in VICE, a track inside the normal 20-pixel
	// slew radius takes precedence over the raw cursor location.
	if p.commandMode == CommandModeNone && strings.EqualFold(p.commandInput, "*T") &&
		mouse.WasReleased(platform.MouseButtonLeft) {
		if p.wipRBL == nil && len(p.rangeBearingLines) >= starsMaxRangeBearingLines {
			p.commandResponse = ErrSTARSCapacity.Error()
			return
		}

		endpoint := starsRangeBearingEndpoint{}
		if target := closestSlewTarget(targets, mouse.Pos, transforms); target != nil {
			endpoint.TargetKey = targetDisplayStateKey(target)
		} else {
			lat, lon := transforms.LatLonFromWindow(mouse.Pos)
			endpoint.Location = configPoint{Lat: lat, Lon: normalizeLongitude(lon)}
		}

		if p.wipRBL == nil {
			p.beginRangeBearingLine(endpoint)
		} else {
			p.completeRangeBearingLine(endpoint)
			p.resetCommand()
		}
		return
	}

	// TI 6191.409 Rev. 30, 4.9.4 Move System status area. The command is
	// keyboard-only until its final slew: <MULTI FUNC>, S, then position the
	// screen cursor at the desired top-left corner and click the left trackball
	// button. Pressing ENTER instead is FORMAT. VICE implements this as the
	// Multi Func command S[POS_NORM].
	if p.commandMode == CommandModeMultiFunc && p.multiFuncPrefix+p.commandInput == "S" {
		if mouse.WasPressed(platform.MouseButtonLeft) {
			w, h := ctx.PaneRect.Width(), ctx.PaneRect.Height()
			if w > 0 && h > 0 {
				ps.SSAListPosition = [2]float32{
					mouse.Pos.X / w,
					mouse.Pos.Y / h,
				}
			}
			p.resetCommand()
		}
		return
	}

	// TI 6191.409 Rev. 30, §6.6.11: MULTI FUNC T R A, then
	// designate the new upper-left list corner. Unlike TRA + ENTER, this
	// makes the list visible even if it was hidden.
	if p.commandMode == CommandModeMultiFunc &&
		strings.EqualFold(p.multiFuncPrefix+p.commandInput, "TRA") {
		if mouse.WasPressed(platform.MouseButtonLeft) {
			w, h := ctx.PaneRect.Width(), ctx.PaneRect.Height()
			if w > 0 && h > 0 {
				ps.RestrictionAreaList.Position = [2]float32{mouse.Pos.X / w, mouse.Pos.Y / h}
				ps.RestrictionAreaList.Visible = true
			}
			p.resetCommand()
		}
		return
	}

	// TI 6191.409 Rev. 30, 4.5.3 Move Map category list. After
	// <MULTI FUNC>, <T>, <X>, a left trackball click relocates the top-left
	// corner of the currently selected Map category list. If the list was
	// hidden, the same command makes it visible at the selected location.
	// VICE implements this as the Multi Func command TX[POS_NORM].
	if p.commandMode == CommandModeMultiFunc && p.multiFuncPrefix+p.commandInput == "TX" {
		if mouse.WasPressed(platform.MouseButtonLeft) {
			w, h := ctx.PaneRect.Width(), ctx.PaneRect.Height()
			if w > 0 && h > 0 {
				ps.VideoMapsList.Position = [2]float32{
					mouse.Pos.X / w,
					mouse.Pos.Y / h,
				}
				ps.VideoMapsList.Visible = true
			}
			p.resetCommand()
		}
		return
	}

	// TI 6191.409 Rev. 30, 4.9.6 Move and show aircraft list. After
	// <MULTI FUNC>, T, C, a left-trackball click relocates the top-left
	// corner of the Coast/Suspend list and makes it visible. TC<ENTER> is the
	// separate 4.9.11 hide/show operation registered in cmdsetup.go.
	if p.commandMode == CommandModeMultiFunc && p.multiFuncPrefix+p.commandInput == "TC" {
		if mouse.WasPressed(platform.MouseButtonLeft) {
			w, h := ctx.PaneRect.Width(), ctx.PaneRect.Height()
			if w > 0 && h > 0 {
				ps.CoastSuspendList.Position = [2]float32{
					mouse.Pos.X / w,
					mouse.Pos.Y / h,
				}
				ps.CoastSuspendList.Visible = true
			}
			p.resetCommand()
		}
		return
	}

	// TI 6191.409 Rev. 30, 4.9.7 Move Tower list. P<id> remains in
	// the Preview Area while the operator slews to the desired top-left corner;
	// a left-trackball click moves the list and also shows it if it was hidden.
	// Pressing ENTER instead is handled by cmdsetup.go as the 4.9.12
	// hide/show command.
	if p.commandMode == CommandModeMultiFunc {
		command := strings.ToUpper(strings.TrimSpace(p.multiFuncPrefix + p.commandInput))
		if strings.HasPrefix(command, "P") && len(command) >= 2 && len(command) <= 4 &&
			!strings.ContainsAny(command, " \t\r\n") {
			identifier := command[1:]
			validID := true
			for _, r := range identifier {
				if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
					validID = false
					break
				}
			}
			if validID && mouse.WasPressed(platform.MouseButtonLeft) {
				idx, ok := p.towerListIndex(identifier)
				if !ok {
					p.commandResponse = ErrSTARSIllegalFunction.Error()
					return
				}
				w, h := ctx.PaneRect.Width(), ctx.PaneRect.Height()
				if w > 0 && h > 0 {
					ps.TowerLists[idx].Position = [2]float32{
						mouse.Pos.X / w,
						mouse.Pos.Y / h,
					}
					ps.TowerLists[idx].Visible = true
				}
				p.resetCommand()
				return
			}
		}
	}

	// TI 6191.409 Rev. 30, 6.1.2 Define user-specified range ring
	// center. PLACE RR captures scope input until the operator clicks the
	// desired point. The result is immediately displayed about that point and
	// RR CNTR becomes off (not highlighted), i.e. the user center is active.
	// The manual specifies no response or error message for this command.
	if p.commandMode == CommandModePlaceRangeRings {
		if mouse.WasPressed(platform.MouseButtonLeft) {
			lat, lon := transforms.LatLonFromWindow(mouse.Pos)
			ps.RangeRingsUserCenter = configPoint{
				Lat: lat,
				Lon: normalizeLongitude(lon),
			}
			ps.UseUserRangeRingsCenter = true
			p.setCommandMode(CommandModeNone)
		}
		return
	}

	// TI 6191.409 Rev. 30, 6.13.2 and 6.13.4 implied commands share the
	// same slew + left-trackball modality. On an unassociated/LDB track the
	// click forces its reported beacon code into LDB field 1 for five seconds.
	// On an associated track owned by another controller it toggles PDB/FDB
	// single-track quick look until the track is selected again. VICE uses a
	// 20-pixel target slew radius for these implied commands; retain that here.
	if p.commandMode == CommandModeNone && mouse.WasReleased(platform.MouseButtonLeft) {
		if target := closestSlewTarget(targets, mouse.Pos, transforms); target != nil {
			// After a handoff is accepted, the former owner's white FDB is
			// retained until that controller slews/selects the track. Consume
			// the click here so it cannot also become a single-track quick look.
			if p.acknowledgeOutboundHandoff(target) {
				return
			}
			// TI 6191.409 Rev. 30, 7.3: bare slew + left trackball
			// acknowledges an unacknowledged SPC (among the alert types handled
			// by the real system) and leaves its abbreviation steady.
			if p.acknowledgeSPCBySlew(target) {
				return
			}
			if p.targetSupportsSingleTrackQuickLook(target) {
				p.toggleSingleTrackQuickLook(target)
				return
			}
			if p.targetSupportsLDBBeaconReadout(target) {
				p.startLDBBeaconReadout(target, time.Now())
				return
			}
		}
	}

	// TI 6191.409 Rev. 30, 4.4.2 Re-center display (pan) and define
	// user-specified center: VICE maps the STARS trackball pan to a secondary
	// mouse-button drag. Move the user center by the exact geographic vector
	// represented by this frame's drag delta.
	if mouse.IsDown(platform.MouseButtonRight) && (mouse.Delta.X != 0 || mouse.Delta.Y != 0) {
		deltaLat, deltaLon := transforms.LatLonFromWindowV(mouse.Delta)
		ps.UserCenter.Lat -= deltaLat
		ps.UserCenter.Lon = normalizeLongitude(ps.UserCenter.Lon - deltaLon)
		ps.UseUserCenter = true
	}

	if mouse.Wheel.Y == 0 {
		return
	}

	// Match VICE's STARS wheel units: one wheel unit changes RANGE by one
	// nautical mile and Control triples the change. VICE negates the platform
	// wheel Y value when it builds pane-local mouse state; REDS does not, so
	// invert it here to preserve the same user-facing scroll direction.
	deltaRange := -mouse.Wheel.Y
	if ctx.Keyboard != nil && ctx.Keyboard.IsDown(platform.KeyControl) {
		deltaRange *= 3
	}

	oldRange := ps.Range
	newRange := clampSTARSRange(oldRange + deltaRange)
	if newRange == oldRange {
		return
	}

	// VICE keeps the lat/lon beneath the mouse fixed while wheel-zooming. The
	// affine update below is the same operation expressed directly in lat/lon.
	mouseLat, mouseLon := transforms.LatLonFromWindow(mouse.Pos)
	scale := float64(newRange / oldRange)
	ps.UserCenter.Lat = mouseLat + scale*(ps.UserCenter.Lat-mouseLat)
	ps.UserCenter.Lon = normalizeLongitude(
		mouseLon + scale*longitudeDelta(ps.UserCenter.Lon, mouseLon),
	)
	ps.Range = newRange
	ps.UseUserCenter = true
}

// MonitorColors contains the STARS TCW/TDW display colors.
//
// Unless noted otherwise, the values below come from TI 6191.409 Rev. 30,
// Appendix B, Table B-1 ("TCW/TDW Default Color Definition"). The manual
// explicitly removes DCB and Dwell colors from that table, so DCB colors are
// taken from VICE's legacy STARS palette. Dwell colors are intentionally not
// invented here.
type MonitorColors struct {
	// Tracks / data blocks.
	OwnedDatablock         renderer.RGB
	UnownedDatablock       renderer.RGB
	AlertDatablock         renderer.RGB
	CautionDatablock       renderer.RGB
	GhostDatablock         renderer.RGB
	SelectedDatablock      renderer.RGB
	TrackGeometry          renderer.RGB
	BeaconTargetExtent     renderer.RGB
	TrackHistory           [5]renderer.RGB
	PositionSymbolOwned    renderer.RGB
	PositionSymbolOutline  renderer.RGB
	TestTarget             renderer.RGB
	TerminalProximityAlert renderer.RGB

	// Lists / text.
	List           renderer.RGB
	TextAlert      renderer.RGB
	TextWarning    renderer.RGB
	SystemStatusWX renderer.RGB
	CoordMessage   renderer.RGB
	PreviewList    renderer.RGB
	SSAASR9Status  renderer.RGB

	// UI.
	Cursor     renderer.RGB
	Background renderer.RGB

	// Tools.
	RangeBearingLine renderer.RGB
	PTL              renderer.RGB
	Compass          renderer.RGB
	RangeRing        renderer.RGB
	ATPAWarning      renderer.RGB
	ATPAAlert        renderer.RGB

	// Maps / restriction areas. Indices 0..7 correspond to brightness
	// categories 1..8 in Table B-1.
	MapADefault         renderer.RGB
	MapBDefault         renderer.RGB
	MapA                [8]renderer.RGB
	MapB                [8]renderer.RGB
	RestrictionAreaText renderer.RGB
	RestrictionAreaGeom [8]renderer.RGB

	// Weather. TI 6191.409 Table B-1 defines the two base colors and white
	// weather pattern; the level-to-pattern mapping follows the STARS stipple
	// masks used by VICE (0=solid, 1=light, 2=dense).
	WX             [6]renderer.RGB
	WXPattern      renderer.RGB
	WXLevelStipple [6]int

	// FMA display colors. FMA is not available at TDWs; the TDW palette leaves
	// these at their zero value.
	FMARunway         renderer.RGB
	FMANTZNormal      renderer.RGB
	FMANTZCaution     renderer.RGB
	FMANTZWarning     renderer.RGB
	FMAReferenceLines renderer.RGB
	FMAAMZOutline     renderer.RGB
	FMACourseLine     renderer.RGB
	FMAFixBarLine     renderer.RGB

	// TSAS timeline / slot colors.
	TSASTimelineETAOwned      renderer.RGB
	TSASTimelineETAUnowned    renderer.RGB
	TSASTimelineNonFlightData renderer.RGB
	TSASTimelineSTA           renderer.RGB
	TSASTimelineSpacing       renderer.RGB
	TSASTrajectory            renderer.RGB
	TSASSlotOwned             renderer.RGB
	TSASSlotUnowned           renderer.RGB
	TSASSlotHighlight         renderer.RGB
	TSASSlotHandoffAttention  renderer.RGB
	TSASSlotPointoutAttention renderer.RGB
	TSASSlotPointoutAccepted  renderer.RGB
	TSASSlotRNPOverride       renderer.RGB
	TSASSlotNonDeconflicted   renderer.RGB

	// DCB. TI 6191.409 explicitly omits these colors; these values are from
	// VICE's legacy STARS palette.
	DCBButton            renderer.RGB
	DCBActiveButton      renderer.RGB
	DCBText              renderer.RGB
	DCBTextSelected      renderer.RGB
	DCBUnsupportedButton renderer.RGB
	DCBUnsupportedText   renderer.RGB
	DCBDisabledButton    renderer.RGB
	DCBDisabledText      renderer.RGB
	DCBBackground        renderer.RGB
	DCBTopBevel          renderer.RGB
	DCBBottomBevel       renderer.RGB
	DCBWXButton          renderer.RGB
	DCBActiveWXButton    renderer.RGB
}

var (
	starsBlack          = renderer.RGB8(0, 0, 0)
	starsWhite          = renderer.RGB8(255, 255, 255)
	starsGreen          = renderer.RGB8(0, 255, 0)
	starsRed            = renderer.RGB8(255, 0, 0)
	starsYellow         = renderer.RGB8(255, 255, 0)
	starsCyan           = renderer.RGB8(0, 255, 255)
	starsBlue           = renderer.RGB8(30, 120, 255)
	starsOrange         = renderer.RGB8(255, 55, 0)
	starsDimGray        = renderer.RGB8(140, 140, 140)
	starsGray           = renderer.RGB8(128, 128, 128)
	starsLightBlue      = renderer.RGB8(173, 216, 230)
	starsMagenta        = renderer.RGB8(255, 0, 255)
	starsGold1          = renderer.RGB8(238, 201, 0)
	starsCoral2         = renderer.RGB8(238, 106, 80)
	starsDarkOliveGreen = renderer.RGB8(162, 205, 90)
	starsGoldenRod      = renderer.RGB8(218, 165, 32)
	starsRoyalBlue      = renderer.RGB8(72, 118, 255)
	starsLightSlateBlue = renderer.RGB8(132, 112, 255)
	starsAquamarine2    = renderer.RGB8(118, 238, 198)
	starsCarrot         = renderer.RGB8(237, 145, 33)
	starsOrchid         = renderer.RGB8(218, 112, 214)
	starsRosyBrown2     = renderer.RGB8(238, 180, 180)
	starsLimeGreen      = renderer.RGB8(50, 205, 50)
	starsIndianRed      = renderer.RGB8(255, 106, 106)
	starsDarkGrayBlue   = renderer.RGB8(38, 77, 77)
	starsDarkMustard    = renderer.RGB8(100, 100, 51)
)

// defaultTCWColors is the canonical TCW palette from TI 6191.409 Table B-1.
// Blinking is a rendering state, not a separate RGB value, so blinking and
// steady variants share the same color here.
var defaultTCWColors = MonitorColors{
	OwnedDatablock:         starsWhite,
	UnownedDatablock:       starsGreen,
	AlertDatablock:         starsRed,
	CautionDatablock:       starsYellow,
	GhostDatablock:         starsYellow,
	SelectedDatablock:      starsCyan,
	TrackGeometry:          starsBlue,
	BeaconTargetExtent:     starsGreen,
	PositionSymbolOwned:    starsWhite,
	PositionSymbolOutline:  starsBlack,
	TestTarget:             starsWhite,
	TerminalProximityAlert: renderer.RGB8(90, 180, 255),
	TrackHistory: [5]renderer.RGB{
		renderer.RGB8(30, 80, 200),
		renderer.RGB8(70, 70, 170),
		renderer.RGB8(50, 50, 130),
		renderer.RGB8(40, 40, 110),
		renderer.RGB8(30, 30, 90), // Also histories 6 through 10.
	},

	List:           starsGreen,
	TextAlert:      starsRed,
	TextWarning:    starsYellow,
	SystemStatusWX: starsCyan,
	CoordMessage:   starsGreen,
	PreviewList:    starsGreen,
	SSAASR9Status:  starsYellow,

	Cursor:     starsWhite,
	Background: starsBlack,

	RangeBearingLine: starsWhite,
	PTL:              starsWhite,
	Compass:          starsDimGray,
	RangeRing:        starsDimGray,
	ATPAWarning:      starsYellow,
	ATPAAlert:        starsOrange,

	MapADefault: starsDimGray,
	MapBDefault: starsDimGray,
	MapA: [8]renderer.RGB{
		starsDimGray,
		starsCyan,
		starsMagenta,
		starsGold1,
		starsCoral2,
		starsDarkOliveGreen,
		starsGoldenRod,
		starsRoyalBlue,
	},
	MapB: [8]renderer.RGB{
		starsDimGray,
		starsLightSlateBlue,
		starsAquamarine2,
		starsCarrot,
		starsOrchid,
		starsRosyBrown2,
		starsLimeGreen,
		starsIndianRed,
	},
	RestrictionAreaText: starsYellow,
	RestrictionAreaGeom: [8]renderer.RGB{
		starsDimGray,
		starsCyan,
		starsMagenta,
		starsGold1,
		starsCoral2,
		starsLightSlateBlue,
		starsAquamarine2,
		starsLimeGreen,
	},

	WX: [6]renderer.RGB{
		starsDarkGrayBlue,
		starsDarkGrayBlue,
		starsDarkGrayBlue,
		starsDarkMustard,
		starsDarkMustard,
		starsDarkMustard,
	},
	WXPattern:      starsWhite,
	WXLevelStipple: [6]int{0, 1, 2, 0, 1, 2},

	FMARunway:         starsGray,
	FMANTZNormal:      starsWhite,
	FMANTZCaution:     starsYellow,
	FMANTZWarning:     starsRed,
	FMAReferenceLines: starsWhite,
	FMAAMZOutline:     starsLightBlue,
	FMACourseLine:     starsWhite,
	FMAFixBarLine:     starsWhite,

	TSASTimelineETAOwned:      starsWhite,
	TSASTimelineETAUnowned:    starsGreen,
	TSASTimelineNonFlightData: starsWhite,
	TSASTimelineSTA:           starsCyan,
	TSASTimelineSpacing:       starsYellow,
	TSASTrajectory:            starsWhite,
	TSASSlotOwned:             starsWhite,
	TSASSlotUnowned:           starsGreen,
	TSASSlotHighlight:         starsCyan,
	TSASSlotHandoffAttention:  starsWhite,
	TSASSlotPointoutAttention: starsYellow,
	TSASSlotPointoutAccepted:  starsYellow,
	TSASSlotRNPOverride:       starsOrange,
	TSASSlotNonDeconflicted:   starsYellow,

	DCBButton:            renderer.RGB8(0, 44, 0),
	DCBActiveButton:      renderer.RGB8(0, 78, 0),
	DCBText:              renderer.RGB8(255, 255, 255),
	DCBTextSelected:      renderer.RGB8(255, 255, 0),
	DCBUnsupportedButton: renderer.RGB8(100, 100, 100),
	DCBUnsupportedText:   renderer.RGB8(200, 200, 200),
	DCBDisabledButton:    renderer.RGB8(0, 22, 0),
	DCBDisabledText:      renderer.RGB8(128, 128, 128),
	DCBBackground:        renderer.RGB8(0, 12, 0),
	DCBTopBevel:          renderer.RGB8(50, 50, 50),
	DCBBottomBevel:       renderer.RGB8(0, 0, 0),
	DCBWXButton:          renderer.RGB8(83, 83, 162),
	DCBActiveWXButton:    renderer.RGB8(116, 116, 162),
}

// defaultTDWColors starts from the TCW defaults and applies the differences
// listed in TI 6191.409 Table B-1. Per note 4, facilities may adapt the TDW
// unowned-data-block colors to green/blinking green instead of the default
// white/blinking white; this palette intentionally keeps the documented
// default.
var defaultTDWColors = func() MonitorColors {
	c := defaultTCWColors

	c.UnownedDatablock = starsWhite
	c.TerminalProximityAlert = starsWhite

	// CRC uses yellow as the TDW videomap base hue for both MPA and MPB; the
	// A/B distinction changes brightness only. Keep the indexed defaults
	// consistent as well even though videomap.go now reads ColorSet directly.
	c.MapADefault = starsYellow
	c.MapBDefault = starsYellow
	c.MapA[0] = starsYellow
	c.MapB[0] = starsYellow

	// FMA mode is not available at TDWs (Table B-1 note 1).
	c.FMARunway = renderer.RGB{}
	c.FMANTZNormal = renderer.RGB{}
	c.FMANTZCaution = renderer.RGB{}
	c.FMANTZWarning = renderer.RGB{}
	c.FMAReferenceLines = renderer.RGB{}
	c.FMAAMZOutline = renderer.RGB{}
	c.FMACourseLine = renderer.RGB{}
	c.FMAFixBarLine = renderer.RGB{}

	// TSAS timeline ETA entries stay green for unowned flights at TDWs, but
	// unowned TSAS slot markers/decor use the TDW white default.
	c.TSASSlotUnowned = starsWhite

	return c
}()

// FAA TFR projection and rendering (alongside VICE-style STARS draw methods).

// tfrProjectedPolygon converts one FAA polygon (exterior ring and optional
// holes) into the existing scope projection. A GeoJSON ring repeats its first
// vertex; remove that closure before calling AddLineLoop/earcut. Longitude is
// wrapped relative to the scope center for TFRs around the antimeridian.
func tfrProjectedPolygon(t radar.LatLonTransformations, polygon starsTFRPolygon) ([][]renderer.PointVertex, [][]redsmath.Vec2) {
	lines := make([][]renderer.PointVertex, 0, len(polygon.Rings))
	rings := make([][]redsmath.Vec2, 0, len(polygon.Rings))
	for _, ring := range polygon.Rings {
		if len(ring) < 4 {
			continue
		}
		points := ring
		if ring[0] == ring[len(ring)-1] {
			points = ring[:len(ring)-1]
		}
		if len(points) < 3 {
			continue
		}
		line := make([]renderer.PointVertex, 0, len(points))
		xy := make([]redsmath.Vec2, 0, len(points))
		for _, pt := range points {
			pos := t.WindowFromLatLonWrapped(pt.Lat, pt.Lon)
			line = append(line, renderer.PointVertex{X: pos.X, Y: pos.Y})
			xy = append(xy, pos)
		}
		lines = append(lines, line)
		rings = append(rings, xy)
	}
	return lines, rings
}

// Cheap bounding-box rejection avoids tessellating distant national TFRs on
// every scope redraw. The first ring is the exterior, per RFC 7946.
func tfrPolygonInViewport(outer []renderer.PointVertex, w, h float32) bool {
	if len(outer) == 0 || w <= 0 || h <= 0 {
		return false
	}
	minX, maxX, minY, maxY := outer[0].X, outer[0].X, outer[0].Y, outer[0].Y
	for _, pt := range outer[1:] {
		minX = min(minX, pt.X)
		maxX = max(maxX, pt.X)
		minY = min(minY, pt.Y)
		maxY = max(maxY, pt.Y)
	}
	return maxX >= 0 && maxY >= 0 && minX <= w && minY <= h
}

// VICE converts each TFR to a closed, shaded restriction area, with its title
// positioned at the average of the geographic vertices and HideId=true.
// REDS follows those choices, but uses the Rev.30 restriction-area palette
// (including the optional yellow-color-1 override) instead of VICE's palette.
// This is only called for a locally visible FAA entry; it does not mutate any
// controller-defined areas or the cached FAA GeoJSON.
// FAA scope text uses the same two display lines as GEO RESTRICTIONS.
// A hidden description must still leave the restriction's numeric ID visible.
func tfrScopeLabelText(a starsRestrictionArea, hideText bool) string {
	text := fmt.Sprintf("[%d]", a.ID)
	if !hideText {
		if title := strings.TrimSpace(a.Text[0]); title != "" {
			text += " " + title
			if notam := strings.TrimSpace(a.Text[1]); notam != "" {
				text += "\n      " + notam
			}
		}
	}
	return text
}

func (p *STARSPane) drawFAARestrictionArea(
	ctx *panes.Context, cb *renderer.CmdBuffer, t radar.LatLonTransformations,
	ra starsRestrictionArea, settings restrictionAreaDisplaySettings,
	shadeMode renderer.DrawMode, labels *renderer.TextDrawBuilder,
	fontSize int, textColor renderer.RGB, blinkDim bool,
) {
	color := p.currentPrefs().Brightness.VideoGroupB.ScaleRGB(p.restrictionAreaGeometryColor(ra.Color - 1))
	activePolygons := make([]starsTFRPolygon, 0, len(ra.TFRPolygons))
	now := time.Now().UTC()
	for _, polygon := range ra.TFRPolygons {
		if polygon.Schedule.active(now) {
			activePolygons = append(activePolygons, polygon)
		}
	}
	if len(activePolygons) == 0 {
		return
	}
	for _, polygon := range activePolygons {
		lines, rings := tfrProjectedPolygon(t, polygon)
		if len(lines) == 0 || !tfrPolygonInViewport(lines[0], ctx.PaneRect.Width(), ctx.PaneRect.Height()) {
			continue
		}
		outline := renderer.GetLinesBuilder()
		for _, line := range lines {
			outline.AddLineLoop(line) // holes also have visible boundaries
		}
		if ra.Shaded {
			// Shared renderer earcut supports holes and arbitrary vertex counts;
			// triangulating each polygon separately preserves multipart NOTAMs.
			vertices, indices := renderer.TessellateRings(rings)
			if len(indices) > 0 {
				shaded := renderer.GetTrianglesBuilder()
				for i := 0; i+2 < len(indices); i += 3 {
					shaded.AddTriangle(vertices[indices[i]], vertices[indices[i+1]], vertices[indices[i+2]])
				}
				cb.SetRGB(color)
				shaded.GenerateCommands(cb, shadeMode, 0)
				renderer.ReturnTrianglesBuilder(shaded)
			}
		}
		cb.SetRGB(color)
		outline.GenerateCommands(cb)
		renderer.ReturnLinesBuilder(outline)
	}
	if labels == nil {
		return
	}
	// Match controller-defined restrictions: the numeric ID remains visible
	// even when the optional restriction text is hidden.
	text := tfrScopeLabelText(ra, settings.HideText)
	labelPosition := tfrLabelPosition(activePolygons)
	point := t.WindowFromLatLonWrapped(labelPosition.Lat, labelPosition.Lon)
	fs := p.systemFont.Size(fontSize)
	if fs == nil {
		return
	}
	// A multi-line label must be measured by its widest line, not by the
	// sum of glyph advances across the newline.
	maxWidth := float32(0)
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		maxWidth = max(maxWidth, starsRBLTextWidth(fs, line))
	}
	point.X -= maxWidth / 2
	point.Y -= float32(len(lines)*fs.LineHeight) / 2
	labelColor := restrictionAreaBlinkColor(textColor,
		(ra.Blink || settings.ForceBlinkingText) && !settings.StopBlinkingText && blinkDim)
	labels.AddText(text, point, renderer.TextStyle{Size: fontSize, Color: labelColor.ToRGBA()})
}
