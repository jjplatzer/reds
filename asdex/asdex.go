package asdex

import (
	"encoding/json"
	"fmt"
	"log/slog"
	stdmath "math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	redslog "github.com/juliusplatzer/reds/log"
	redsmath "github.com/juliusplatzer/reds/math"
	redsnet "github.com/juliusplatzer/reds/net"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util"
)

type Mode int

const (
	ModeDay Mode = iota
	ModeNight
)

const (
	brightnessMin          = 1
	brightnessMax          = 99
	brightnessDefault      = 95
	brightnessFloorDefault = 20

	rightSlewDragThresholdPixels = float32(5)

	aircraftCoastDelay = 60 * time.Second
	coastDropLifetime  = 45 * time.Second

	unknownTargetStaleLifetime = 8 * time.Second

	defaultAuralVolume = 99
	minAuralVolume     = 1
	maxAuralVolume     = 99

	// New CRC ASDE-X RANGE uses RangeMeasurement.FullHorizontal and
	// RangeUnits._100sFeet. RANGE n means the full horizontal width of the
	// main display is n*100 feet. Secondary windows use the same feet-per-pixel
	// scale referenced to the main display width.
	asdexMinRangeSetting     = 3
	asdexMaxRangeSetting     = 600
	asdexDefaultRangeSetting = 100
	asdexFeetPerRangeUnit    = 100
	asdexWheelRangeStep      = 4
	asdexCtrlWheelRangeStep  = 16

	defaultVectorLengthSeconds = 5

	// Set to a positive value to override the platform-reported ASDE-X window
	// scale factor used for RANGE compatibility.
	asdexWindowScaleFactorOverride = float32(0)
)

const (
	zVideoMap                 renderer.Z = -900
	zSafetyLogicClosedRunways renderer.Z = -800
	zSafetyLogicHoldBars      renderer.Z = -790

	zRestrictedArea  renderer.Z = -700
	zClosedArea      renderer.Z = -690
	zTempMapText     renderer.Z = -680
	zTempAreaDrawing renderer.Z = -670
	zDBAreas         renderer.Z = -600

	zTargets         renderer.Z = -500
	zSuspendedLabels renderer.Z = -499
	zDatablocks      renderer.Z = -480

	zWindowBorders            renderer.Z = -300
	zAlertMessage             renderer.Z = -210
	zPreviewArea              renderer.Z = -200
	zPreviewCursor            renderer.Z = -190
	zPreviewRepositionOutline renderer.Z = -189

	zDCBBackground renderer.Z = -100
	zDCBButtons    renderer.Z = -99
	zDCBText       renderer.Z = -98

	zMouseCursor renderer.Z = 1000
)

func windowZ(stackIndex int, localZ renderer.Z) renderer.Z {
	return renderer.Z(-10000 + stackIndex*1000 + int(localZ))
}

func scopeWindowZ(stackIndex int, localZ renderer.Z) renderer.Z {
	return renderer.Z(-20000 + stackIndex*1000 + int(localZ))
}

type ASDEXPane struct {
	logger            *redslog.Logger
	airport           string
	configAirportCode string
	mode              Mode
	videomap          *VideoMap
	safetyLogic       SafetyLogic
	tempData          TempData
	windows           ScopeWindowManager
	targets           TargetStore
	smes              *redsnet.SmesClient
	fonts             fontCache
	eramTextFonts     fontCache

	cursors    CursorSet
	cursorMode CursorMode

	displayStateByWindow            map[ScopeWindowID]*WindowDisplayState
	undoStack                       []UndoSnapshot
	undoRestoring                   bool
	dbFieldSettings                 DataBlockFieldSettings
	datablockTimeshareStart         time.Time
	showBeaconUntilByTargetID       map[string]time.Time
	listsBrightness                 int
	dcbBrightness                   int
	vectorLength                    int
	auralVolume                     int
	prefPage                        int
	playbackHourOffset              int
	playbackClient                  *redsnet.PlaybackClient
	playbackSession                 *PlaybackSession
	playbackResults                 chan PlaybackLoadResult
	playbackLoadSeq                 uint64
	previewArea                     PreviewArea
	coastList                       CoastList
	alertRepository                 AlertRepository
	auralAlerts                     *AuralAlertManager
	alertMessageBox                 AlertMessageBox
	towerReference                  TowerReference
	hasTowerReference               bool
	runwayConfigurations            []RunwayConfiguration
	activeRunwayConfigID            string
	runwayConfigPage                int
	towerConfigurations             []TowerConfiguration
	defaultTowerConfigID            string
	activeTowerConfigIDs            map[string]bool
	dcb                             Dcb
	dcbSpinner                      *DcbSpinner
	dcbMenuCommand                  *DcbMenuCommand
	closedRunwayReturnMenu          DcbMenu
	closedRunwayReturnLines         []string
	trackAlertInhibitReturnMenu     DcbMenu
	trackAlertInhibitReturnLines    []string
	trackAlertInhibitHasReturnState bool
	dbAreaDraft                     *DataBlockAreaDraft
	dbAreaSelection                 *DataBlockAreaSelection
	tempAreaDraft                   *TempAreaDraft
	tempTextCommand                 *TempTextCommand
	tempTextPlacement               *TempTextPlacementCommand
	tempDataSelectMode              TempDataSelectMode
	hoveredTempData                 TempDataHit
	newWindow                       *NewWindowCommand
	deleteWindow                    *DeleteWindowCommand
	windowReposition                *WindowRepositionCommand
	resizeWindow                    *ResizeWindowCommand
	showCoastList                   bool
	hoveredCoastListTarget          string

	commandMode         CommandMode
	commandEntry        CommandTextEntry
	datablockEdit       *DatablockEditCommand
	editingTargetID     string
	initControlEntry    *CoastListIDEntryCommand
	termControlEntry    *CoastListIDEntryCommand
	multiFunction       *MultiFunctionCommand
	scratchpadEntry     *ScratchpadEntryCommand
	previewReposition   *PreviewRepositionCommand
	coastListReposition *CoastListRepositionCommand
	mapReposition       *MapRepositionCommand
	mapRotate           *MapRotateCommand
	runwayConfigCommand *RunwayConfigCommand
	towerReadout        *TowerReadoutCommand

	rightClickStart     redsmath.Vec2
	rightClickCandidate bool
	rightClickDragged   bool

	hover ScopeHoverState

	center                  redsmath.Vec2
	rangeSetting            int
	rangeFullHorizontalFeet float32
	rotation                float32
	viewInitialized         bool
}

func NewPane(airport string, logger *redslog.Logger) (*ASDEXPane, error) {
	if logger == nil {
		logger = &redslog.Logger{
			Logger: slog.Default(),
			Start:  time.Now(),
		}
	}

	airport = strings.ToUpper(strings.TrimSpace(airport))
	if airport == "" {
		return nil, fmt.Errorf("empty ASDE-X airport")
	}
	InitCommands()

	vm, err := LoadVideoMap(airport)
	if err != nil {
		return nil, err
	}
	towerReference, hasTowerReference, towerErr := LoadTowerReference(airport, vm)
	if towerErr != nil {
		logger.Warn(
			"Unable to load tower reference",
			slog.Any("error", towerErr),
		)
	}
	runwayConfigs, defaultRunwayConfigID, runwayConfigErr := loadRunwayConfigurations(airport)
	if runwayConfigErr != nil {
		logger.Warn(
			"Unable to load runway configurations",
			slog.Any("error", runwayConfigErr),
		)
	}
	towerConfigs, defaultTowerConfigID, towerConfigErr := loadTowerConfigurations(airport)
	if towerConfigErr != nil {
		logger.Warn(
			"Unable to load tower configurations",
			slog.Any("error", towerConfigErr),
		)
	}
	activeTowerConfigIDs := make(map[string]bool)
	if defaultTowerConfigID != "" {
		activeTowerConfigIDs[defaultTowerConfigID] = true
	}
	safetyLogic, err := LoadSafetyLogic(airport, vm)
	if err != nil {
		logger.Warn(
			"Unable to initialize safety logic",
			slog.Any("error", err),
		)
	}

	fonts, err := loadFontCache()
	if err != nil {
		return nil, err
	}
	eramTextFonts, err := loadEramTextFontCache()
	if err != nil {
		return nil, err
	}

	preview := NewPreviewArea()
	if err := preview.LoadDefaultStateFromAirportConfig(airport); err != nil {
		logger.Warn(
			"Unable to load preview-area default state",
			slog.Any("error", err),
		)
	}
	preview.SetSystemResponse("CRITICAL FAULT START")
	coastList := NewCoastList()
	auralAlerts := NewAuralAlertManager()
	auralAlerts.SetVolume(defaultAuralVolume)
	configAirport := loadConfigAirportCode(airport)

	client := redsnet.NewSmesClient(
		redsnet.TargetWebSocketURL(),
		logger.With(slog.String("component", "smes")),
	)
	client.SetAirport(airport)
	client.Start()

	pane := &ASDEXPane{
		logger:            logger,
		airport:           airport,
		configAirportCode: configAirport,
		mode:              ModeDay,
		videomap:          vm,
		safetyLogic:       safetyLogic,
		tempData:          NewTempData(),
		windows:           NewScopeWindowManager(),
		targets:           NewTargetStore(),
		smes:              client,
		fonts:             fonts,
		eramTextFonts:     eramTextFonts,

		displayStateByWindow: map[ScopeWindowID]*WindowDisplayState{
			mainScopeWindowID: NewWindowDisplayState(),
		},
		dbFieldSettings:           DefaultDataBlockFieldSettings(),
		datablockTimeshareStart:   time.Now(),
		showBeaconUntilByTargetID: make(map[string]time.Time),
		listsBrightness:           brightnessDefault,
		dcbBrightness:             brightnessDefault,
		vectorLength:              defaultVectorLengthSeconds,
		auralVolume:               defaultAuralVolume,
		playbackHourOffset:        0,
		playbackClient:            redsnet.NewPlaybackClient(redsnet.PlaybackBaseURL()),
		playbackResults:           make(chan PlaybackLoadResult, 1),
		previewArea:               preview,
		coastList:                 coastList,
		alertRepository:           NewAlertRepository(auralAlerts),
		auralAlerts:               auralAlerts,
		alertMessageBox:           NewAlertMessageBox(),
		towerReference:            towerReference,
		hasTowerReference:         hasTowerReference,
		runwayConfigurations:      runwayConfigs,
		activeRunwayConfigID:      defaultRunwayConfigID,
		runwayConfigPage:          1,
		towerConfigurations:       towerConfigs,
		defaultTowerConfigID:      defaultTowerConfigID,
		activeTowerConfigIDs:      activeTowerConfigIDs,
		dcb:                       NewDcb(),
		showCoastList:             true,
		rangeSetting:              asdexDefaultRangeSetting,
		rangeFullHorizontalFeet:   rangeFullHorizontalFeetFromSetting(asdexDefaultRangeSetting),
	}
	pane.refreshRunwayConfigPreviewLine()
	pane.refreshTowerConfigPreviewLine()

	return pane, nil
}

func (p *ASDEXPane) Dispose() {
	if p == nil {
		return
	}
	if p.smes != nil {
		p.smes.Close()
		p.smes = nil
	}
	if p.auralAlerts != nil {
		p.auralAlerts.Stop()
	}
	p.targets.Clear()
	clear(p.showBeaconUntilByTargetID)
}

const beaconatorDuration = 4 * time.Second

func (p *ASDEXPane) toggleTemporaryBeaconCodeForTarget(target *Target) {
	if p == nil || target == nil || target.ID == "" {
		return
	}

	if p.showBeaconUntilByTargetID == nil {
		p.showBeaconUntilByTargetID = make(map[string]time.Time)
	}

	now := time.Now().UTC()
	if until, ok := p.showBeaconUntilByTargetID[target.ID]; ok && until.After(now) {
		delete(p.showBeaconUntilByTargetID, target.ID)
		return
	}

	p.showBeaconUntilByTargetID[target.ID] = now.Add(beaconatorDuration)
}

func (p *ASDEXPane) expireTemporaryBeaconDisplays(now time.Time) {
	if p == nil || len(p.showBeaconUntilByTargetID) == 0 {
		return
	}

	for id, until := range p.showBeaconUntilByTargetID {
		if !until.After(now) || p.targets.TargetByID(id) == nil {
			delete(p.showBeaconUntilByTargetID, id)
		}
	}
}

func (p *ASDEXPane) showBeaconCodeForTarget(target *Target, now time.Time) bool {
	if p == nil || target == nil || target.ID == "" {
		return false
	}

	until, ok := p.showBeaconUntilByTargetID[target.ID]
	return ok && until.After(now)
}

func (p *ASDEXPane) reportClientActivity(ctx *panes.Context) {
	if p == nil || p.smes == nil || ctx == nil {
		return
	}

	if mouseActivity(ctx.Mouse) || keyboardActivity(ctx.Keyboard) {
		p.smes.ReportActivity()
	}
}

func mouseActivity(mouse *platform.MouseState) bool {
	if mouse == nil {
		return false
	}

	if abs32(mouse.Delta.X) > 0.01 || abs32(mouse.Delta.Y) > 0.01 {
		return true
	}
	if mouse.Wheel.X != 0 || mouse.Wheel.Y != 0 {
		return true
	}

	for i := platform.MouseButton(0); i < platform.MouseButtonCount; i++ {
		if mouse.WasPressed(i) || mouse.WasReleased(i) {
			return true
		}
	}

	return false
}

func keyboardActivity(keyboard *platform.KeyboardState) bool {
	if keyboard == nil {
		return false
	}

	if len(keyboard.Text) > 0 {
		return true
	}
	if len(keyboard.Pressed) > 0 || len(keyboard.Released) > 0 {
		return true
	}

	return false
}

func (p *ASDEXPane) Draw(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if ctx == nil || zcb == nil || p == nil {
		return
	}

	wallNow := time.Now().UTC()

	p.reportClientActivity(ctx)
	p.ensureCursorsLoaded(ctx)
	p.consumePlaybackResults()
	if p.playbackActiveOrLoading() {
		p.discardNetworkEvents()
		p.advancePlayback(wallNow)
	} else {
		p.consumeNetworkEvents()
	}
	p.consumeCommandKeyboard(ctx)
	rangeVisibleScale := rangeVisibleScaleForContext(ctx)
	p.initView(ctx.PaneRect, rangeVisibleScale)
	if !p.viewInitialized {
		return
	}

	p.clampDcbSubmenuCursor(ctx)

	referenceExtent := mainReferenceExtent(ctx.PaneSize())
	transforms := scopeTransformForWindow(
		referenceExtent,
		referenceExtent,
		p.mainScopeView(),
		rangeVisibleScale,
	)

	scopeNow := p.scopeNow(wallNow)
	p.expireTemporaryBeaconDisplays(wallNow)
	p.targets.ExpireRawUnknownTargets(scopeNow, unknownTargetStaleLifetime)
	p.targets.ExpireSuspendedTracks(scopeNow)
	p.targets.UpdateCoastDropTracks(
		scopeNow,
		aircraftCoastDelay,
		coastDropLifetime,
		p.isDestinationCurrentAirport,
	)
	p.consumeOpsHotkeys(ctx, transforms)
	p.coastList.SetVisible(p.showCoastList)
	p.coastList.SetEntries(p.buildCoastSuspendEntries(scopeNow))
	if p.mapReposition == nil && p.mapRotate == nil && p.towerReadout == nil && !p.listRepositionActive() && p.dbAreaDraft == nil && p.dbAreaSelection == nil &&
		p.tempAreaDraft == nil && p.tempTextCommand == nil && p.tempTextPlacement == nil &&
		p.tempDataSelectMode == TempDataSelectNone && p.newWindow == nil && p.deleteWindow == nil &&
		p.windowReposition == nil && p.resizeWindow == nil {
		p.updateCoastListHover(ctx)
	} else {
		p.hoveredCoastListTarget = ""
	}
	if p.mapReposition == nil && p.mapRotate == nil && p.towerReadout == nil && p.dbAreaDraft == nil && p.dbAreaSelection == nil && p.tempAreaDraft == nil &&
		p.tempTextCommand == nil && p.tempTextPlacement == nil &&
		p.tempDataSelectMode == TempDataSelectNone && p.newWindow == nil && p.deleteWindow == nil &&
		p.windowReposition == nil && p.resizeWindow == nil {
		p.updateRightClickGesture(ctx)
	} else {
		p.clearRightClickGesture()
	}
	if p.tempAreaDraft != nil {
		p.updateTempAreaDraftMouse(ctx, transforms)
	}

	if p.mapReposition != nil {
		p.clearHighlightedTarget()
		if p.consumeMapRepositionMouse(ctx, transforms) {
			transforms = scopeTransformForWindow(
				redsmath.RectFromSize(ctx.PaneSize().X, ctx.PaneSize().Y),
				referenceExtent,
				p.mainScopeView(),
				rangeVisibleScale,
			)
		}
	} else if p.listRepositionActive() {
		p.clearHighlightedTarget()
		p.clampListRepositionCursor(ctx)
		p.consumeListRepositionClick(ctx)
	} else if p.mapRotate != nil {
		p.clearHighlightedTarget()
		p.consumeMapRotateMouse(ctx)
	} else if p.towerReadout != nil {
		p.clearHighlightedTarget()
	} else if p.datablockEdit != nil {
		p.clearHighlightedTarget()
		p.consumeDatablockEditWheel(ctx)
	} else if p.newWindow != nil {
		p.clearHighlightedTarget()
		p.consumeNewWindowInput(ctx, transforms)
	} else if p.deleteWindow != nil {
		p.clearHighlightedTarget()
		p.consumeDeleteWindowInput(ctx)
	} else if p.windowReposition != nil {
		p.clearHighlightedTarget()
		p.consumeWindowRepositionInput(ctx)
	} else if p.resizeWindow != nil {
		p.clearHighlightedTarget()
		p.consumeResizeWindowInput(ctx, referenceExtent)
	} else if p.tempTextPlacement != nil {
		p.clearHighlightedTarget()
		p.consumeTempTextPlacementInput(ctx, transforms)
	} else if p.tempTextCommand != nil {
		p.clearHighlightedTarget()
	} else if p.tempAreaDraft != nil {
		p.clearHighlightedTarget()
		p.consumeTempAreaDraftInput(ctx, transforms)
	} else if p.dbAreaDraft != nil {
		p.clearHighlightedTarget()
		p.consumeDataBlockAreaDraftInput(ctx, referenceExtent)
	} else if p.tempDataSelectMode != TempDataSelectNone {
		p.clearHighlightedTarget()
		p.consumeTempDataSelectionInput(ctx, transforms)
	} else if p.dcbSpinner != nil {
		p.clearHighlightedTarget()
		if !p.consumeDcbOnOffClick(ctx) && p.consumeDcbSpinnerInput(ctx) {
			transforms = scopeTransformForWindow(
				redsmath.RectFromSize(ctx.PaneSize().X, ctx.PaneSize().Y),
				referenceExtent,
				p.mainScopeView(),
				rangeVisibleScale,
			)
		}
	} else if p.dbAreaSelection != nil {
		p.clearHighlightedTarget()
		if !p.consumeDcbInput(ctx) {
			p.consumeDataBlockAreaSelectionInput(ctx, referenceExtent)
		}
	} else if p.dcbMenuCommand != nil {
		p.clearHighlightedTarget()
		if !p.consumeDcbWindowSwitchShortcut(ctx) {
			p.consumeDcbInput(ctx)
		}
	} else {
		if p.consumeDcbInput(ctx) {
			p.clearHighlightedTarget()
		} else {
			if ctx.Mouse == nil {
				p.clearHighlightedTarget()
			} else {
				windowID, windowRect, view, ok := p.scopeWindowAtPoint(ctx.Mouse.Pos, ctx.PaneSize())
				if ok {
					scopeTransforms := scopeTransformForWindow(windowRect, referenceExtent, view, rangeVisibleScale)
					updatedView, changed := p.consumeScopeMouseEvents(ctx, windowRect, view, scopeTransforms)
					if changed {
						p.setScopeView(windowID, updatedView)
						view = updatedView
						scopeTransforms = scopeTransformForWindow(windowRect, referenceExtent, view, rangeVisibleScale)
						if windowID == mainScopeWindowID {
							transforms = scopeTransforms
						}
					}
					p.updateHighlightedTargetInWindow(ctx, windowID, windowRect, scopeTransforms)
					if !p.consumeCoastListClicks(ctx) {
						p.consumeCommandClicksInWindow(ctx, windowID, windowRect, scopeTransforms)
					}
				} else {
					p.clearHighlightedTarget()
					if !p.consumeCoastListClicks(ctx) {
						p.consumeCommandClicks(ctx, transforms)
					}
				}
			}
		}
	}
	if p.tempDataSelectMode != TempDataSelectNone && ctx.Mouse != nil {
		if _, windowRect, view, ok := p.scopeWindowAtPoint(ctx.Mouse.Pos, ctx.PaneSize()); ok {
			scopeTransforms := scopeTransformForWindow(windowRect, referenceExtent, view, rangeVisibleScale)
			p.hoveredTempData = p.tempData.HitTest(
				scopeTransforms.WorldFromWindowP(ctx.Mouse.Pos.Sub(windowRect.Min)),
			)
		} else {
			p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
		}
	} else {
		p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	}
	if p.dbAreaSelection != nil {
		p.updateDataBlockAreaSelectionHover(ctx, referenceExtent)
	}
	p.updateTowerReadout(ctx, referenceExtent, rangeVisibleScale)
	p.applyCurrentCursor(ctx)
	p.coastList.SetEntries(p.buildCoastSuspendEntries(scopeNow))
	targets := p.targets.All()
	p.previewArea.SetTrackAlertsInhibited(p.targets.AnyAlertsInhibited())
	alertChanges := p.safetyLogic.Update(targets, SafetyLogicUpdateOptions{
		RunwayConfiguration: p.currentSafetyRunwayConfiguration(),
		RunwayClosed:        p.tempData.RunwayClosed,
		TowerRunwayEnabled:  p.towerRunwayEnabledPredicate(),
		TargetAlertsInhibited: func(targetID string) bool {
			return p.targets.AlertsInhibited(targetID)
		},
	})
	targetAlertsInhibited := func(targetID string) bool {
		return p.targets.AlertsInhibited(targetID)
	}
	p.alertRepository.ApplyChanges(alertChanges, targetAlertsInhibited)
	alertTargetIDs := p.alertRepository.AircraftIDsInAlertSet()
	for targetID := range alertTargetIDs {
		if p.targets.AlertsInhibited(targetID) {
			delete(alertTargetIDs, targetID)
		}
	}
	alertInhibitedTargetIDs := p.targets.AlertInhibitedIDs()
	alertInProgress := p.alertRepository.AlertInProgress()
	alertOn := alertFlashOn(wallNow)

	mainRect := redsmath.RectFromSize(ctx.PaneSize().X, ctx.PaneSize().Y)
	transforms = p.renderScopeWindow(
		ctx,
		zcb,
		0,
		mainScopeWindowID,
		mainRect,
		referenceExtent,
		p.mainScopeView(),
		rangeVisibleScale,
		targets,
		wallNow,
		true,
		alertTargetIDs,
		alertInhibitedTargetIDs,
		alertInProgress,
		alertOn,
	)
	for i, win := range p.windows.secondary {
		if win.Hidden {
			continue
		}
		p.renderScopeWindow(
			ctx,
			zcb,
			i+1,
			win.ID,
			win.Rect,
			referenceExtent,
			win.View,
			rangeVisibleScale,
			targets,
			wallNow,
			false,
			alertTargetIDs,
			alertInhibitedTargetIDs,
			alertInProgress,
			alertOn,
		)
	}
	p.renderWindowBorders(ctx, zcb, transforms)
	p.renderNewWindowPreview(ctx, zcb, transforms)
	p.renderWindowRepositionPreview(ctx, zcb, transforms)
	p.renderResizeWindowPreview(ctx, zcb, transforms)

	x, y, w, h := ctx.PaneFramebufferRect()
	alertCB := zcb.At(windowZ(0, zAlertMessage))
	alertCB.Viewport(x, y, w, h)
	alertCB.Scissor(x, y, w, h)
	transforms.LoadWindowViewingMatrices(alertCB)
	alertTextureID := p.fonts.textureForSize(ctx.Renderer, alertMessageFontSize)
	if alertTextureID != 0 {
		td := renderer.GetTextDrawBuilder()
		td.SetFont(p.fonts.font)
		p.alertMessageBox.Render(
			alertCB,
			td,
			p.fonts.font,
			p.alertRepository.FirstN(alertMessageMaxAlerts),
			ctx.PaneSize(),
		)
		td.GenerateCommands(alertCB, alertTextureID)
		renderer.ReturnTextDrawBuilder(td)
	}
	alertCB.DisableScissor()

	listCB := zcb.At(windowZ(0, zPreviewArea))
	listCB.Viewport(x, y, w, h)
	listCB.Scissor(x, y, w, h)
	transforms.LoadWindowViewingMatrices(listCB)

	coastTextureID := p.fonts.textureForSize(ctx.Renderer, p.coastList.FontSize())
	if coastTextureID != 0 {
		td := renderer.GetTextDrawBuilder()
		td.SetFont(p.fonts.font)
		p.coastList.Render(td, p.fonts.font, ctx.PaneSize(), scopeNow)
		td.GenerateCommands(listCB, coastTextureID)
		renderer.ReturnTextDrawBuilder(td)
	}
	p.coastList.RenderOverflowArrows(
		listCB,
		p.fonts.font,
		p.eramTextFonts.font,
		ctx.PaneSize(),
		func(size int) renderer.TextureID {
			return p.eramTextFonts.textureForSize(ctx.Renderer, size)
		},
	)

	textureID := p.fonts.textureForSize(ctx.Renderer, p.previewArea.FontSize())
	if textureID != 0 {
		td := renderer.GetTextDrawBuilder()
		td.SetFont(p.fonts.font)
		p.previewArea.Render(td, p.fonts.font, ctx.PaneSize(), p.activeCommandLines())
		td.GenerateCommands(listCB, textureID)
		renderer.ReturnTextDrawBuilder(td)
	}
	listCB.DisableScissor()

	p.renderListRepositionOutline(ctx, zcb, transforms)

	if cursorLine, cursorColumn, ok := p.activeCommandCursor(); ok {
		cursorCB := zcb.At(windowZ(0, zPreviewCursor))
		cursorCB.Viewport(x, y, w, h)
		cursorCB.Scissor(x, y, w, h)
		transforms.LoadWindowViewingMatrices(cursorCB)
		cursorCB.SetRGB(p.previewArea.TextRGB())
		cursorCB.LineWidth(1)

		builder := renderer.GetLinesBuilder()
		p.previewArea.RenderCommandCursor(
			builder,
			p.fonts.font,
			ctx.PaneSize(),
			cursorLine,
			cursorColumn,
			p.previewArea.BaseLineCount(),
		)
		builder.GenerateCommands(cursorCB)
		renderer.ReturnLinesBuilder(builder)
		cursorCB.DisableScissor()
	}

	p.renderDcb(ctx, zcb, transforms)
	p.renderSoftwareCursor(ctx, zcb)
}

func (p *ASDEXPane) renderScopeWindow(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	stackIndex int,
	windowID ScopeWindowID,
	rect redsmath.Rect,
	referenceExtent redsmath.Rect,
	view ScopeView,
	rangeVisibleScale float32,
	targets []*Target,
	now time.Time,
	drawDraft bool,
	alertTargetIDs map[string]bool,
	alertInhibitedTargetIDs map[string]bool,
	alertInProgress bool,
	alertOn bool,
) radar.ScopeTransformations {
	if p == nil || ctx == nil || zcb == nil || rect.Empty() {
		return radar.ScopeTransformations{}
	}

	transforms := scopeTransformForWindow(rect, referenceExtent, view, rangeVisibleScale)
	x, y, w, h := scopeFramebufferRect(ctx, rect)
	displayState := p.displayStateForWindow(windowID)
	brightness := displayState.Brightness

	cb := zcb.At(scopeWindowZ(stackIndex, zVideoMap))
	cb.Viewport(x, y, w, h)
	cb.Scissor(x, y, w, h)
	cb.Clear(applyBrightness(backgroundColor(p.mode), brightness.Background, 20).ToRGBA())

	transforms.LoadWorldViewingMatrices(cb)
	DrawVideoMap(p.videomap, cb, p.mode, brightness.MovementArea)
	cb.DisableScissor()

	closedRunwayCB := zcb.At(scopeWindowZ(stackIndex, zSafetyLogicClosedRunways))
	closedRunwayCB.Viewport(x, y, w, h)
	closedRunwayCB.Scissor(x, y, w, h)
	transforms.LoadWorldViewingMatrices(closedRunwayCB)
	p.tempData.DrawClosedRunways(closedRunwayCB, &p.safetyLogic, brightness.TempMapAreas)
	closedRunwayCB.DisableScissor()

	restrictedAreaCB := zcb.At(scopeWindowZ(stackIndex, zRestrictedArea))
	restrictedAreaCB.Viewport(x, y, w, h)
	restrictedAreaCB.Scissor(x, y, w, h)
	transforms.LoadWorldViewingMatrices(restrictedAreaCB)
	p.tempData.DrawRestrictedAreas(restrictedAreaCB, transforms, brightness.TempMapAreas)
	restrictedAreaCB.DisableScissor()

	closedAreaCB := zcb.At(scopeWindowZ(stackIndex, zClosedArea))
	closedAreaCB.Viewport(x, y, w, h)
	closedAreaCB.Scissor(x, y, w, h)
	transforms.LoadWorldViewingMatrices(closedAreaCB)
	p.tempData.DrawClosedAreas(closedAreaCB, transforms, brightness.TempMapAreas)
	closedAreaCB.DisableScissor()

	tempTextCB := zcb.At(scopeWindowZ(stackIndex, zTempMapText))
	tempTextCB.Viewport(x, y, w, h)
	tempTextCB.Scissor(x, y, w, h)
	transforms.LoadWindowViewingMatrices(tempTextCB)
	p.tempData.DrawTempTextAnchors(tempTextCB, transforms, brightness.TempMapText)
	p.tempData.DrawTempTexts(
		tempTextCB,
		transforms,
		p.fonts.font,
		func(size int) renderer.TextureID {
			return p.fonts.textureForSize(ctx.Renderer, size)
		},
		p.dataBlockSettingsForWindow(windowID),
		displayState.TempDataCharSize,
		brightness.TempMapText,
	)
	tempTextCB.DisableScissor()

	if drawDraft {
		draftCB := zcb.At(scopeWindowZ(stackIndex, zTempAreaDrawing))
		draftCB.Viewport(x, y, w, h)
		draftCB.Scissor(x, y, w, h)
		transforms.LoadWorldViewingMatrices(draftCB)
		p.DrawTempAreaDraft(draftCB)
		draftCB.DisableScissor()
	}

	if p.showsDataBlockAreas() {
		dbAreaCB := zcb.At(scopeWindowZ(stackIndex, zDBAreas))
		dbAreaCB.Viewport(x, y, w, h)
		dbAreaCB.Scissor(x, y, w, h)
		transforms.LoadWorldViewingMatrices(dbAreaCB)
		p.drawDataBlockAreas(dbAreaCB, windowID)
		p.drawDataBlockAreaDraft(dbAreaCB, windowID)
		dbAreaCB.DisableScissor()
	}

	holdBarCB := zcb.At(scopeWindowZ(stackIndex, zSafetyLogicHoldBars))
	holdBarCB.Viewport(x, y, w, h)
	holdBarCB.Scissor(x, y, w, h)
	transforms.LoadWorldViewingMatrices(holdBarCB)
	p.safetyLogic.DrawHoldBars(holdBarCB, brightness.HoldBars)
	holdBarCB.DisableScissor()

	targetCB := zcb.At(scopeWindowZ(stackIndex, zTargets))
	targetCB.Viewport(x, y, w, h)
	targetCB.Scissor(x, y, w, h)
	transforms.LoadWorldViewingMatrices(targetCB)
	highlightedTargetID := ""
	if p.hover.WindowID == windowID {
		highlightedTargetID = p.hover.TargetID
	}
	DrawTargets(
		targets,
		p.targets.History(),
		targetCB,
		TargetDrawOptions{
			VectorSeconds: ClampedTargetVectorSeconds(p.vectorLength),
			VectorVisible: func(target *Target) bool {
				return p.targetVectorVisibleForWindow(windowID, target)
			},
			ShowHistory:             displayState.ShowHistory,
			HistoryLength:           displayState.HistoryLength,
			Brightness:              brightness.Track,
			ScopeRotationDeg:        int(view.Rotation),
			HighlightedTargetID:     highlightedTargetID,
			AlertTargetIDs:          alertTargetIDs,
			AlertInhibitedTargetIDs: alertInhibitedTargetIDs,
			AlertFlashOn:            alertOn,
		},
	)
	targetCB.DisableScissor()

	suspendedLabelCB := zcb.At(scopeWindowZ(stackIndex, zSuspendedLabels))
	suspendedLabelCB.Viewport(x, y, w, h)
	suspendedLabelCB.Scissor(x, y, w, h)
	transforms.LoadWindowViewingMatrices(suspendedLabelCB)
	DrawSuspendedTargetLabels(
		targets,
		suspendedLabelCB,
		transforms,
		p.fonts.font,
		p.fonts.textureForSize(ctx.Renderer, suspendedLabelFontSize),
	)
	suspendedLabelCB.DisableScissor()

	dbCB := zcb.At(scopeWindowZ(stackIndex, zDatablocks))
	dbCB.Viewport(x, y, w, h)
	dbCB.Scissor(x, y, w, h)
	DrawDatablocks(
		targets,
		dbCB,
		transforms,
		DataBlockDrawOptions{
			Font: p.fonts.font,
			FontTextureForSize: func(size int) renderer.TextureID {
				return p.fonts.textureForSize(ctx.Renderer, size)
			},
			SettingsForTarget: func(target *Target) DataBlockSettings {
				targetInAlert := false
				if target != nil {
					targetInAlert = alertTargetIDs[target.ID]
				}
				return p.resolveDataBlockSettings(
					target,
					windowID,
					alertInProgress,
					targetInAlert,
				)
			},
			ShowDataBlockForTarget: func(target *Target, settings DataBlockSettings) bool {
				return p.targetShowsDataBlockForRender(target, windowID, settings)
			},
			ShowBeaconCodeForTarget: func(target *Target) bool {
				return p.showBeaconCodeForTarget(target, now)
			},
		},
	)
	dbCB.DisableScissor()

	return transforms
}

func (p *ASDEXPane) renderDcb(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.ScopeTransformations,
) {
	if p == nil || ctx == nil || zcb == nil {
		return
	}

	layout := p.dcb.Layout(ctx.PaneSize(), p.fonts.font, p.dcbState())
	if layout.Bounds.Empty() {
		return
	}

	x, y, w, h := ctx.PaneFramebufferRect()

	bgCB := zcb.At(windowZ(0, zDCBBackground))
	bgCB.Viewport(x, y, w, h)
	bgCB.Scissor(x, y, w, h)
	transforms.LoadWindowViewingMatrices(bgCB)
	p.dcb.DrawBackground(bgCB, layout)
	bgCB.DisableScissor()

	buttonCB := zcb.At(windowZ(0, zDCBButtons))
	buttonCB.Viewport(x, y, w, h)
	buttonCB.Scissor(x, y, w, h)
	transforms.LoadWindowViewingMatrices(buttonCB)
	p.dcb.DrawButtons(buttonCB, layout)
	buttonCB.DisableScissor()

	textureID := p.fonts.textureForSize(ctx.Renderer, layout.RenderFontSize)
	if textureID != 0 {
		textCB := zcb.At(windowZ(0, zDCBText))
		textCB.Viewport(x, y, w, h)
		textCB.Scissor(x, y, w, h)
		transforms.LoadWindowViewingMatrices(textCB)

		td := renderer.GetTextDrawBuilder()
		p.dcb.DrawText(td, p.fonts.font, layout, p.hoveredDcbButtonIndex(ctx))
		td.GenerateCommands(textCB, textureID)
		renderer.ReturnTextDrawBuilder(td)

		textCB.DisableScissor()
	}
}

func (p *ASDEXPane) renderSoftwareCursor(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Mouse == nil {
		return
	}
	if p.cursorMode == CursorModeHidden {
		return
	}

	cursorType, ok := p.cursors.CursorTypeForMode(p.cursorMode)
	if !ok {
		return
	}

	cursor := p.cursors.Cursor(cursorType)
	if cursor == nil {
		return
	}

	textureID := p.cursors.textureForCursor(ctx.Renderer, cursorType)
	if textureID == 0 {
		return
	}

	x, y, w, h := ctx.PaneFramebufferRect()
	cb := zcb.At(zMouseCursor)
	cb.Viewport(x, y, w, h)
	cb.Scissor(x, y, w, h)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.SetRGBA(renderer.RGBA{R: 1, G: 1, B: 1, A: 1})

	mouse := ctx.Mouse.Pos
	left := float32(stdmath.Floor(float64(mouse.X - float32(cursor.Hotspot[0]))))
	top := float32(stdmath.Floor(float64(mouse.Y - float32(cursor.Hotspot[1]))))
	right := left + float32(cursor.Width)
	bottom := top + float32(cursor.Height)

	builder := renderer.GetTexturedTrianglesBuilder()
	builder.AddQuad(
		renderer.PointVertex{X: left, Y: top},
		renderer.PointVertex{X: 0, Y: 0},
		renderer.PointVertex{X: right, Y: top},
		renderer.PointVertex{X: 1, Y: 0},
		renderer.PointVertex{X: right, Y: bottom},
		renderer.PointVertex{X: 1, Y: 1},
		renderer.PointVertex{X: left, Y: bottom},
		renderer.PointVertex{X: 0, Y: 1},
	)
	builder.GenerateCommands(cb, textureID)
	renderer.ReturnTexturedTrianglesBuilder(builder)
	cb.DisableScissor()
}

func (p *ASDEXPane) hoveredDcbButtonIndex(ctx *panes.Context) int {
	hit := p.dcbHit(ctx)
	if !hit.OverDcb {
		return -1
	}
	return hit.ButtonIndex
}

func (p *ASDEXPane) mouseOverDcb(ctx *panes.Context) bool {
	return p.dcbHit(ctx).OverDcb
}

func (p *ASDEXPane) dcbHit(ctx *panes.Context) DcbHit {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return DcbHit{ButtonIndex: -1}
	}

	return p.dcb.HitTest(ctx.Mouse.Pos, ctx.PaneSize(), p.fonts.font, p.dcbState())
}

func (p *ASDEXPane) dcbCursorUnlocked() bool {
	if p == nil {
		return false
	}
	if p.dbAreaSelection != nil {
		return true
	}
	if p.dbAreaDraft != nil || p.tempAreaDraft != nil || p.tempTextCommand != nil || p.tempTextPlacement != nil ||
		p.tempDataSelectMode != TempDataSelectNone || p.newWindow != nil || p.deleteWindow != nil ||
		p.windowReposition != nil || p.resizeWindow != nil || p.towerReadout != nil {
		return false
	}
	if p.dcbSpinner != nil || p.dcbMenuCommand != nil {
		return true
	}

	return p.commandMode == CommandModeNone &&
		p.datablockEdit == nil &&
		p.initControlEntry == nil &&
		p.termControlEntry == nil &&
		p.multiFunction == nil &&
		p.scratchpadEntry == nil &&
		p.previewReposition == nil &&
		p.coastListReposition == nil &&
		p.mapReposition == nil &&
		p.mapRotate == nil &&
		p.towerReadout == nil
}

func (p *ASDEXPane) dcbSubmenuCursorCaptured() bool {
	if p == nil {
		return false
	}
	if !p.dcb.Visible() || p.dcb.Collapsed() {
		return false
	}

	switch p.dcb.Menu() {
	case DcbMenuMain, DcbMenuOff:
		return false
	}

	if p.dbAreaSelection != nil ||
		p.dbAreaDraft != nil ||
		p.tempAreaDraft != nil ||
		p.tempTextCommand != nil ||
		p.tempTextPlacement != nil ||
		p.tempDataSelectMode != TempDataSelectNone ||
		p.newWindow != nil ||
		p.deleteWindow != nil ||
		p.windowReposition != nil ||
		p.resizeWindow != nil ||
		p.previewReposition != nil ||
		p.coastListReposition != nil ||
		p.mapReposition != nil ||
		p.mapRotate != nil ||
		p.towerReadout != nil {
		return false
	}

	return p.dcbMenuCommand != nil
}

func (p *ASDEXPane) dcbMouseCaptured() bool {
	return p != nil && p.dcbSubmenuCursorCaptured()
}

func (p *ASDEXPane) dcbCursorCaptureRect(ctx *panes.Context) (redsmath.Rect, bool) {
	if p == nil || ctx == nil || p.fonts.font == nil {
		return redsmath.Rect{}, false
	}
	if !p.dcbSubmenuCursorCaptured() {
		return redsmath.Rect{}, false
	}

	layout := p.dcb.Layout(ctx.PaneSize(), p.fonts.font, p.dcbState())
	if layout.MenuBounds.Empty() {
		return redsmath.Rect{}, false
	}

	return layout.MenuBounds, true
}

func (p *ASDEXPane) clampDcbSubmenuCursor(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Mouse == nil || ctx.Platform == nil {
		return
	}

	bounds, ok := p.dcbCursorCaptureRect(ctx)
	if !ok {
		return
	}

	const edgeInset = float32(0.5)

	minX := bounds.Min.X + edgeInset
	minY := bounds.Min.Y + edgeInset
	maxX := bounds.Max.X - edgeInset
	maxY := bounds.Max.Y - edgeInset
	if maxX < minX {
		maxX = minX
	}
	if maxY < minY {
		maxY = minY
	}

	pos := ctx.Mouse.Pos
	clamped := redsmath.Vec2{
		X: clamp(pos.X, minX, maxX),
		Y: clamp(pos.Y, minY, maxY),
	}
	if clamped == pos {
		return
	}

	ctx.Platform.SetMousePosition(clamped.Add(ctx.PaneRect.Min))
	ctx.Mouse.Pos = clamped
	ctx.Mouse.Delta = redsmath.Vec2{}
}

func (p *ASDEXPane) dcbState() DcbState {
	if p == nil {
		return DcbState{
			Mode:         ModeDay,
			VectorOn:     false,
			VectorLength: defaultVectorLengthSeconds,
			Volume:       defaultAuralVolume,
			DcbOn:        true,
		}
	}

	active := p.activeDcbWindowState()
	rangeSetting := active.View.RangeSetting
	if rangeSetting == 0 {
		rangeSetting = asdexDefaultRangeSetting
	}
	rangeSetting = clampInt(rangeSetting, asdexMinRangeSetting, asdexMaxRangeSetting)

	activeSpinnerFunction := DcbFunctionVacant
	if p.dcbSpinner != nil {
		activeSpinnerFunction = p.dcbSpinner.Function
	} else if p.mapRotate != nil {
		activeSpinnerFunction = DcbFunctionRotate
	} else if p.newWindow != nil && p.dcb.Menu() == DcbMenuTools {
		activeSpinnerFunction = DcbFunctionNewWindow
	} else if p.deleteWindow != nil {
		activeSpinnerFunction = DcbFunctionDeleteWindow
	} else if p.windowReposition != nil {
		activeSpinnerFunction = DcbFunctionWindowReposition
	} else if p.resizeWindow != nil {
		activeSpinnerFunction = DcbFunctionResizeWindow
	} else if p.commandMode == CommandModeTrackAlertInhibit {
		activeSpinnerFunction = DcbFunctionTrackAlertInhibit
	}
	fields := p.dbFieldSettings
	playbackHourOffset := clampInt(p.playbackHourOffset, 0, playbackMaxHourOffset)
	playbackHour := p.playbackHourStart()

	state := DcbState{
		Range:                  rangeSetting,
		Mode:                   p.mode,
		VectorOn:               active.ShowVectorLine,
		VectorLength:           ClampedTargetVectorSeconds(p.vectorLength),
		LeaderLength:           active.DB.LeaderLength,
		DataBlocksOn:           active.DB.ShowDataBlocks,
		DcbOn:                  p.dcb.On(),
		RotationDeg:            int(normalizeRotation(active.View.Rotation)),
		ShowHistory:            active.ShowHistory,
		HistoryLength:          active.HistoryLength,
		ShowCoastList:          p.showCoastList,
		CursorSpeed:            1,
		CursorHome:             false,
		Volume:                 clampInt(p.auralVolume, minAuralVolume, maxAuralVolume),
		OperatingInitials:      "OP",
		PrefPage:               p.prefPage,
		CurrentPrefTitle:       "",
		DataBlockCharSize:      active.DB.FontSize,
		DcbCharSize:            p.dcb.CharSize(),
		CoastSuspendCharSize:   p.coastList.FontSize(),
		TempDataCharSize:       active.TempDataCharSize,
		PreviewAreaCharSize:    p.previewArea.FontSize(),
		PlaybackHourStart:      playbackHour,
		PlaybackHourOffset:     playbackHourOffset,
		FullDataBlocks:         active.DB.FullDataBlocks,
		ShowAltitude:           fields.ShowAltitude,
		ShowTargetType:         fields.ShowTargetType,
		ShowSensors:            fields.ShowSensors,
		ShowCWT:                fields.ShowCWT,
		ShowFix:                fields.ShowFix,
		ShowVelocity:           fields.ShowVelocity,
		ShowScratchpads:        fields.ShowScratchpads,
		HoldBarsBrightness:     active.Brightness.HoldBars,
		MovementAreaBrightness: active.Brightness.MovementArea,
		BackgroundBrightness:   active.Brightness.Background,
		TrackBrightness:        active.Brightness.Track,
		DataBlocksBrightness:   active.DB.Brightness,
		ListsBrightness:        p.listsBrightness,
		TempMapAreasBrightness: active.Brightness.TempMapAreas,
		TempMapTextBrightness:  active.Brightness.TempMapText,
		DcbBrightness:          p.dcbBrightness,
		ClosedRunways:          p.tempData.DcbRunwayClosureStates(&p.safetyLogic),
		RunwayConfigs:          p.dcbRunwayConfigStates(),
		RunwayConfigPage:       p.runwayConfigPage,
		ActiveRunwayConfigID:   p.activeRunwayConfigID,
		RunwayConfigName:       p.activeRunwayConfiguration().Name,
		TowerConfigs:           p.dcbTowerConfigStates(),
		ActiveSpinnerFunction:  activeSpinnerFunction,
	}

	windowState := p.displayStateForWindow(active.WindowID)
	if area, ok := windowState.selectedDataBlockArea(); ok {
		state.HasSelectedDbArea = true
		state.SelectedDbAreaTraits = area.Traits
	}

	return state
}

func (p *ASDEXPane) dcbRunwayConfigStates() []DcbRunwayConfigState {
	if p == nil {
		return nil
	}

	out := make([]DcbRunwayConfigState, 0, len(p.runwayConfigurations))
	for index, cfg := range p.runwayConfigurations {
		number := cfg.Number
		if number <= 0 {
			number = index + 1
		}
		out = append(out, DcbRunwayConfigState{
			ID:     cfg.ID,
			Number: number,
			Name:   cfg.Name,
			Active: cfg.ID == p.activeRunwayConfigID,
		})
	}
	return out
}

func (p *ASDEXPane) activeRunwayConfiguration() RunwayConfiguration {
	if p != nil {
		for _, cfg := range p.runwayConfigurations {
			if cfg.ID == p.activeRunwayConfigID {
				return cfg
			}
		}
	}

	return RunwayConfiguration{
		ID:   "limited",
		Name: "LIMITED",
	}
}

func (p *ASDEXPane) refreshRunwayConfigPreviewLine() {
	if p == nil {
		return
	}

	name := strings.TrimSpace(p.activeRunwayConfiguration().Name)
	if name == "" {
		name = "LIMITED"
	}
	p.previewArea.SetRunwayConfigName(name)
}

func (p *ASDEXPane) dcbTowerConfigStates() []DcbTowerConfigState {
	if p == nil {
		return nil
	}

	out := make([]DcbTowerConfigState, 0, len(p.towerConfigurations))
	for _, cfg := range p.towerConfigurations {
		defaultOn := cfg.ID == p.defaultTowerConfigID
		out = append(out, DcbTowerConfigState{
			ID:      cfg.ID,
			Name:    cfg.Name,
			On:      defaultOn || p.activeTowerConfigIDs[cfg.ID],
			Default: defaultOn,
		})
	}
	return out
}

func (p *ASDEXPane) activeTowerConfigNames() []string {
	if p == nil {
		return nil
	}

	names := make([]string, 0, len(p.towerConfigurations))
	for _, cfg := range p.towerConfigurations {
		if cfg.ID == p.defaultTowerConfigID || p.activeTowerConfigIDs[cfg.ID] {
			names = append(names, cfg.Name)
		}
	}
	return names
}

func (p *ASDEXPane) refreshTowerConfigPreviewLine() {
	if p == nil {
		return
	}
	if len(p.towerConfigurations) == 0 {
		return
	}
	p.previewArea.SetTowerPositions(p.activeTowerConfigNames())
}

func (p *ASDEXPane) currentSafetyRunwayConfiguration() SafetyRunwayConfiguration {
	if p == nil {
		return LimitedSafetyRunwayConfiguration()
	}

	cfg := p.activeRunwayConfiguration()
	if strings.EqualFold(strings.TrimSpace(cfg.Name), "LIMITED") ||
		strings.EqualFold(strings.TrimSpace(cfg.ID), "limited") {
		return LimitedSafetyRunwayConfiguration()
	}

	return SafetyRunwayConfiguration{
		Name:               cfg.Name,
		ArrivalRunwayIDs:   stringSet(cfg.ArrivalRunwayIDs),
		DepartureRunwayIDs: stringSet(cfg.DepartureRunwayIDs),
	}
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func (p *ASDEXPane) consumeDcbInput(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}

	hit := p.dcbHit(ctx)
	if !hit.OverDcb {
		return false
	}

	mouse := ctx.Mouse
	if mouse.WasReleased(platform.MouseButtonLeft) && hit.HasFunction {
		return p.activateDcbHit(ctx, hit)
	}

	return mouse.WasReleased(platform.MouseButtonLeft) ||
		mouse.WasReleased(platform.MouseButtonRight) ||
		mouse.Wheel.X != 0 ||
		mouse.Wheel.Y != 0 ||
		hit.OverDcb
}

func (p *ASDEXPane) consumeDcbWindowSwitchShortcut(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil || ctx.Keyboard == nil {
		return false
	}
	if !ctx.Keyboard.IsDown(platform.KeyShift) ||
		!ctx.Mouse.WasReleased(platform.MouseButtonMiddle) ||
		!p.mouseOverDcb(ctx) {
		return false
	}

	status, err, handled := p.tryExecuteUserCommandForDcb(
		ctx,
		"[WINDOW SWITCH]",
		CommandClickMiddle,
		ctx.Mouse.Pos,
	)
	if err != nil {
		p.previewArea.SetSystemResponse(err.Error())
		return true
	}
	if handled {
		p.applyCommandStatus(status)
		return true
	}
	return false
}

func (p *ASDEXPane) consumeDcbOnOffClick(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}
	if !ctx.Mouse.WasReleased(platform.MouseButtonLeft) {
		return false
	}

	hit := p.dcbHit(ctx)
	if !hit.OverDcb || !hit.HasFunction {
		return false
	}
	if hit.Function != DcbFunctionDcbOnOff {
		return false
	}
	return p.activateDcbHit(ctx, hit)
}

func (p *ASDEXPane) activateDcbFunction(ctx *panes.Context, function DcbFunction) bool {
	return p.activateDcbHit(ctx, DcbHit{
		Function:    function,
		HasFunction: function != DcbFunctionVacant,
	})
}

func (p *ASDEXPane) activateDcbHit(ctx *panes.Context, hit DcbHit) bool {
	if p == nil {
		return false
	}
	if !hit.HasFunction {
		return false
	}

	if p.dcb.Menu() == DcbMenuSafetyLogic && p.activateSafetyLogicDcbHit(ctx, hit) {
		return true
	}

	if p.dcb.Menu() == DcbMenuTowerConfig && p.activateTowerConfigDcbHit(ctx, hit) {
		return true
	}

	if p.dcb.Menu() == DcbMenuRunwayConfig && p.activateRunwayConfigDcbHit(ctx, hit) {
		return true
	}

	if p.dcb.Menu() == DcbMenuPrefs && p.activatePrefsDcbHit(ctx, hit) {
		return true
	}

	if p.dcb.Menu() == DcbMenuCharSize && p.activateCharSizeDcbHit(hit) {
		return true
	}

	if p.dcb.Menu() == DcbMenuPlayBack && p.activatePlayBackDcbHit(hit) {
		return true
	}

	if p.activateTempDataDcbHit(hit) {
		return true
	}

	if (p.dcb.Menu() == DcbMenuDefineTraitArea ||
		p.dcb.Menu() == DcbMenuModifyTraitArea) &&
		p.activateTraitAreaDcbHit(hit) {
		return true
	}
	if isBrightnessFunction(hit.Function) {
		p.startBrightnessSpinner(hit.Function)
		return true
	}
	if p.dcb.Menu() == DcbMenuTools && isToolsPlaceholderFunction(hit.Function) {
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	}

	switch hit.Function {
	case DcbFunctionRange:
		if p.dcb.On() {
			p.startRangeSpinner()
		}
		return true
	case DcbFunctionUndo:
		p.executeUndoCommand(ctx)
		return true
	case DcbFunctionDefault:
		p.applyCommandStatus(p.cmdDefault(ctx))
		p.clearHighlightedTarget()
		return true
	case DcbFunctionRotate:
		if p.dcb.On() {
			p.startDcbRotateCommand()
		}
		return true
	case DcbFunctionDone:
		if p.dcb.Menu() == DcbMenuDefineTraitArea ||
			p.dcb.Menu() == DcbMenuModifyTraitArea {
			p.dcb.SetMenu(DcbMenuDbArea)
			p.dcbMenuCommand = NewDcbMenuCommand("DB AREA")
			p.previewArea.SetSystemResponse("")
			p.clearHighlightedTarget()
			return true
		}
		p.closeDcbSubmenu()
		return true
	case DcbFunctionDataBlockArea:
		p.openDbAreaDcbMenu()
		return true
	case DcbFunctionDataBlockEdit:
		p.openDbEditDcbMenu()
		return true
	case DcbFunctionBrightness:
		p.openBrightnessMenu()
		return true
	case DcbFunctionCharSize:
		p.openCharSizeMenu()
		return true
	case DcbFunctionTools:
		p.openToolsMenu()
		return true
	case DcbFunctionPrefs:
		p.openPrefsMenu()
		return true
	case DcbFunctionPlayBack:
		if p.dcb.Menu() == DcbMenuTools {
			p.openPlayBackMenu()
		}
		return true
	case DcbFunctionSafetyLogic:
		p.openSafetyLogicMenu()
		return true
	case DcbFunctionDayNite:
		p.applyCommandStatus(p.cmdMapTheme(ctx))
		p.clearHighlightedTarget()
		return true
	case DcbFunctionHistoryOnOff:
		if p.dcb.Menu() == DcbMenuTools {
			p.toggleHistoryForActiveWindow()
		}
		return true
	case DcbFunctionHistory:
		if p.dcb.Menu() == DcbMenuTools {
			p.startHistorySpinner()
		}
		return true
	case DcbFunctionVectorOnOff:
		p.toggleVectorLineForActiveWindow()
		return true
	case DcbFunctionVectorLength:
		p.startVectorLengthSpinner()
		return true
	case DcbFunctionCoastOnOff:
		if p.dcb.Menu() == DcbMenuTools {
			p.toggleCoastList()
		}
		return true
	case DcbFunctionDcbTop:
		if p.dcb.Menu() == DcbMenuTools {
			p.setDcbPositionUndoable(DcbTop)
		}
		return true
	case DcbFunctionDcbLeft:
		if p.dcb.Menu() == DcbMenuTools {
			p.setDcbPositionUndoable(DcbLeft)
		}
		return true
	case DcbFunctionDcbRight:
		if p.dcb.Menu() == DcbMenuTools {
			p.setDcbPositionUndoable(DcbRight)
		}
		return true
	case DcbFunctionDcbBottom:
		if p.dcb.Menu() == DcbMenuTools {
			p.setDcbPositionUndoable(DcbBottom)
		}
		return true
	case DcbFunctionNewWindow:
		if p.dcb.Menu() == DcbMenuTools {
			p.startToolsNewWindowCommand()
		}
		return true
	case DcbFunctionDeleteWindow:
		if p.dcb.Menu() == DcbMenuTools {
			p.startDeleteWindowCommand()
		}
		return true
	case DcbFunctionWindowReposition:
		if p.dcb.Menu() == DcbMenuTools {
			p.startWindowRepositionCommand(ctx)
		}
		return true
	case DcbFunctionResizeWindow:
		if p.dcb.Menu() == DcbMenuTools {
			p.startResizeWindowCommand()
		}
		return true
	case DcbFunctionDefineDbTraitArea:
		p.startDefineDbTraitArea()
		return true
	case DcbFunctionDefineDbOffArea:
		p.startDefineDbOffArea()
		return true
	case DcbFunctionModifyDbTraitArea:
		p.startModifyDbTraitArea()
		return true
	case DcbFunctionDeleteAllDbAreas,
		DcbFunctionDeleteOneDbArea:
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	case DcbFunctionDbFullPart:
		p.toggleDbFullPart()
		return true
	case DcbFunctionDbAltitudeOnOff:
		p.toggleDbField(func(fields *DataBlockFieldSettings) {
			fields.ShowAltitude = !fields.ShowAltitude
		})
		return true
	case DcbFunctionDbTypeOnOff:
		p.toggleDbField(func(fields *DataBlockFieldSettings) {
			fields.ShowTargetType = !fields.ShowTargetType
		})
		return true
	case DcbFunctionDbSensorsOnOff:
		p.toggleDbField(func(fields *DataBlockFieldSettings) {
			fields.ShowSensors = !fields.ShowSensors
		})
		return true
	case DcbFunctionDbCategoryOnOff:
		p.toggleDbField(func(fields *DataBlockFieldSettings) {
			fields.ShowCWT = !fields.ShowCWT
		})
		return true
	case DcbFunctionDbFixOnOff:
		p.toggleDbField(func(fields *DataBlockFieldSettings) {
			fields.ShowFix = !fields.ShowFix
		})
		return true
	case DcbFunctionDbVelocityOnOff:
		p.toggleDbField(func(fields *DataBlockFieldSettings) {
			fields.ShowVelocity = !fields.ShowVelocity
		})
		return true
	case DcbFunctionDbScratchpadOnOff:
		p.toggleDbField(func(fields *DataBlockFieldSettings) {
			fields.ShowScratchpads = !fields.ShowScratchpads
		})
		return true
	case DcbFunctionDataBlocksOnOff:
		p.toggleDataBlocksOnOff()
		return true
	case DcbFunctionDcbOnOff:
		p.dcb.ToggleOnOff()
		p.commandMode = CommandModeNone
		p.runwayConfigCommand = nil
		p.dcbSpinner = nil
		p.dcbMenuCommand = nil
		p.clearTrackAlertInhibitReturnContext()
		p.towerReadout = nil
		p.dbAreaDraft = nil
		p.dbAreaSelection = nil
		p.tempAreaDraft = nil
		p.tempTextCommand = nil
		p.tempTextPlacement = nil
		p.tempDataSelectMode = TempDataSelectNone
		p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
		p.tempData.ClearHighlights()
		p.newWindow = nil
		p.deleteWindow = nil
		p.windowReposition = nil
		p.resizeWindow = nil
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	default:
		return true
	}
}

func (p *ASDEXPane) activateSafetyLogicDcbHit(ctx *panes.Context, hit DcbHit) bool {
	if p == nil {
		return false
	}

	switch hit.Function {
	case DcbFunctionRunwayConfig:
		p.openRunwayConfigMenu()
		return true
	case DcbFunctionTowerConfig:
		p.openTowerConfigMenu()
		return true
	case DcbFunctionTrackAlertInhibit:
		p.startTrackAlertInhibitFromDcb(ctx)
		return true
	case DcbFunctionAllTracksEnableAlerts:
		p.enableAllTrackAlerts()
		return true
	case DcbFunctionVolume:
		p.startSafetyVolumeSpinner()
		return true
	case DcbFunctionVolumeTest:
		p.executeVolumeTestCommand(ctx)
		return true
	case DcbFunctionClosedRunway:
		p.openSafetyLogicClosedRunwayDcbMenu()
		return true
	case DcbFunctionArrivalAlerts,
		DcbFunctionAlertReposition:
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	default:
		return false
	}
}

func (p *ASDEXPane) enableAllTrackAlerts() {
	if p == nil {
		return
	}

	if p.commandMode == CommandModeTrackAlertInhibit {
		p.commandMode = CommandModeNone
		p.commandEntry.Clear()
		p.clearTrackAlertInhibitReturnContext()
	}

	p.targets.ClearAlertInhibits()

	p.dcb.SetMenu(DcbMenuSafetyLogic)
	p.dcbMenuCommand = NewDcbMenuCommand("SAFETY LOGIC")

	p.previewArea.SetTrackAlertsInhibited(false)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) executeVolumeTestCommand(ctx *panes.Context) {
	if p == nil {
		return
	}

	status, err, handled := p.tryExecuteUserCommand(
		ctx,
		"[VOL TEST]",
		nil,
		CommandClickNone,
		redsmath.Vec2{},
		radar.ScopeTransformations{},
	)
	if err != nil {
		p.previewArea.SetSystemResponse(err.Error())
		return
	}
	if handled {
		p.applyCommandStatus(status)
	}

	p.dcb.SetMenu(DcbMenuSafetyLogic)
	p.dcbMenuCommand = NewDcbMenuCommand("SAFETY LOGIC")
}

func (p *ASDEXPane) executeUndoCommand(ctx *panes.Context) {
	if p == nil {
		return
	}

	status, err, handled := p.tryExecuteUserCommand(
		ctx,
		"[UNDO]",
		nil,
		CommandClickNone,
		redsmath.Vec2{},
		radar.ScopeTransformations{},
	)
	if err != nil {
		p.previewArea.SetSystemResponse(err.Error())
		return
	}
	if handled {
		p.applyCommandStatus(status)
	}
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) activateRunwayConfigDcbHit(_ *panes.Context, hit DcbHit) bool {
	if p == nil {
		return false
	}

	switch hit.Function {
	case DcbFunctionRunwayConfigPreset:
		p.selectRunwayConfigByNumber(hit.ConfigID)
		return true
	case DcbFunctionRunwayConfigPresetsPage1:
		p.setRunwayConfigPage(1)
		return true
	case DcbFunctionRunwayConfigPresetsPage2:
		p.setRunwayConfigPage(2)
		return true
	case DcbFunctionRunwayConfigPresetsPage3:
		p.setRunwayConfigPage(3)
		return true
	case DcbFunctionDone:
		p.dcb.SetMenu(DcbMenuSafetyLogic)
		p.dcbMenuCommand = NewDcbMenuCommand("SAFETY LOGIC")
		p.previewArea.SetSystemResponse("")
		p.refreshRunwayConfigPreviewLine()
		p.clearHighlightedTarget()
		return true
	default:
		return false
	}
}

func (p *ASDEXPane) setRunwayConfigPage(page int) {
	if p == nil {
		return
	}

	p.runwayConfigPage = clampInt(page, 1, 3)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) selectRunwayConfigByNumber(number int) {
	if p == nil {
		return
	}

	response := p.setRunwayConfigurationByOrdinal(number, false)
	if response == "" {
		return
	}
	p.previewArea.SetSystemResponse(response)
}

func (p *ASDEXPane) setRunwayConfigurationByOrdinal(
	number int,
	confirmEvenIfActive bool,
) string {
	if p == nil {
		return ""
	}
	if number < 1 || number > 60 {
		return "INVALID CONFIG"
	}

	index := number - 1
	if index < 0 || index >= len(p.runwayConfigurations) {
		return "NO STORED DATA"
	}

	cfg := p.runwayConfigurations[index]
	if cfg.ID == p.activeRunwayConfigID && !confirmEvenIfActive {
		return ""
	}

	p.activeRunwayConfigID = cfg.ID
	p.refreshRunwayConfigPreviewLine()
	p.clearHighlightedTarget()

	name := strings.ToUpper(strings.TrimSpace(cfg.Name))
	if name == "" {
		name = "LIMITED"
	}
	return name + " CONFIRMED"
}

func (p *ASDEXPane) activateTowerConfigDcbHit(_ *panes.Context, hit DcbHit) bool {
	if p == nil {
		return false
	}

	switch hit.Function {
	case DcbFunctionTowerConfigPreset:
		p.toggleTowerConfigByIndex(hit.ConfigID)
		return true
	case DcbFunctionDone:
		p.dcb.SetMenu(DcbMenuSafetyLogic)
		p.dcbMenuCommand = NewDcbMenuCommand("SAFETY LOGIC")
		p.previewArea.SetSystemResponse("")
		p.refreshTowerConfigPreviewLine()
		p.clearHighlightedTarget()
		return true
	default:
		return false
	}
}

func (p *ASDEXPane) toggleTowerConfigByIndex(id int) {
	if p == nil || id < 1 || id > len(p.towerConfigurations) {
		return
	}

	cfg := p.towerConfigurations[id-1]
	if cfg.ID == p.defaultTowerConfigID {
		return
	}

	if p.activeTowerConfigIDs == nil {
		p.activeTowerConfigIDs = make(map[string]bool)
	}

	if p.activeTowerConfigIDs[cfg.ID] {
		delete(p.activeTowerConfigIDs, cfg.ID)
	} else {
		p.activeTowerConfigIDs[cfg.ID] = true
	}

	if p.defaultTowerConfigID != "" {
		p.activeTowerConfigIDs[p.defaultTowerConfigID] = true
	}

	p.previewArea.SetSystemResponse("")
	p.refreshTowerConfigPreviewLine()
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) clearDcbModalConflicts() {
	if p == nil {
		return
	}

	p.commandMode = CommandModeNone
	p.commandEntry.Clear()
	p.datablockEdit = nil
	p.editingTargetID = ""
	p.initControlEntry = nil
	p.termControlEntry = nil
	p.multiFunction = nil
	p.scratchpadEntry = nil
	p.previewReposition = nil
	p.coastListReposition = nil
	p.mapReposition = nil
	p.mapRotate = nil
	p.runwayConfigCommand = nil
	p.towerReadout = nil
	p.dcbSpinner = nil
	p.clearTrackAlertInhibitReturnContext()
	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.tempData.ClearHighlights()
	p.newWindow = nil
	p.deleteWindow = nil
	p.windowReposition = nil
	p.resizeWindow = nil
}

func (p *ASDEXPane) openDbEditDcbMenu() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.dcb.SetMenu(DcbMenuDbEdit)
	p.dcbMenuCommand = NewDcbMenuCommand("DB EDIT")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) openDbAreaDcbMenu() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.dcb.SetMenu(DcbMenuDbArea)
	p.dcbMenuCommand = NewDcbMenuCommand("DB AREA")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) toggleDbFullPart() {
	if p == nil {
		return
	}

	before := p.pushUndoBeforeMutation()
	p.updateActiveDataBlockSettings(func(settings *DataBlockSettings) {
		settings.FullDataBlocks = !settings.FullDataBlocks
	})
	p.commitUndoIfChanged(before)
	p.previewArea.SetSystemResponse("")
}

func (p *ASDEXPane) toggleDbField(update func(*DataBlockFieldSettings)) {
	if p == nil || update == nil {
		return
	}

	before := p.pushUndoBeforeMutation()
	update(&p.dbFieldSettings)
	p.commitUndoIfChanged(before)
	p.previewArea.SetSystemResponse("")
}

func (p *ASDEXPane) toggleDataBlocksOnOff() {
	if p == nil {
		return
	}

	before := p.pushUndoBeforeMutation()
	windowID := p.activeWindowID()
	p.updateActiveDataBlockSettings(func(settings *DataBlockSettings) {
		settings.ShowDataBlocks = !settings.ShowDataBlocks
	})
	p.clearTargetShowDBOverrides(windowID)
	p.commitUndoIfChanged(before)
	p.previewArea.SetSystemResponse("")
}

func (p *ASDEXPane) setListsBrightness(value int) {
	if p == nil {
		return
	}

	value = clampBrightness(value)
	p.listsBrightness = value
	p.previewArea.SetBrightness(value)
	p.coastList.SetBrightness(value)
	p.alertMessageBox.SetBrightness(value)
}

func (p *ASDEXPane) setDcbBrightness(value int) {
	if p == nil {
		return
	}

	value = clampBrightness(value)
	p.dcbBrightness = value
	p.dcb.SetBrightness(value)
}

func isBrightnessFunction(function DcbFunction) bool {
	switch function {
	case DcbFunctionHoldBarsBrightness,
		DcbFunctionMovementAreaBrightness,
		DcbFunctionBackgroundBrightness,
		DcbFunctionTrackBrightness,
		DcbFunctionDataBlocksBrightness,
		DcbFunctionListsBrightness,
		DcbFunctionTempMapAreasBrightness,
		DcbFunctionTempMapTextBrightness,
		DcbFunctionDcbBrightness:
		return true
	default:
		return false
	}
}

func brightnessLabel(function DcbFunction) string {
	switch function {
	case DcbFunctionHoldBarsBrightness:
		return "HOLD BARS"
	case DcbFunctionMovementAreaBrightness:
		return "MVMENT AREA"
	case DcbFunctionBackgroundBrightness:
		return "BAKGND"
	case DcbFunctionTrackBrightness:
		return "TRACK"
	case DcbFunctionDataBlocksBrightness:
		return "DATA BLOCKS"
	case DcbFunctionListsBrightness:
		return "LISTS"
	case DcbFunctionTempMapAreasBrightness:
		return "TEMP MAP AREAS"
	case DcbFunctionTempMapTextBrightness:
		return "TEMP MAP TEXT"
	case DcbFunctionDcbBrightness:
		return "DCB"
	default:
		return ""
	}
}

func (p *ASDEXPane) currentBrightnessValue(function DcbFunction) int {
	if p == nil {
		return brightnessDefault
	}

	active := p.activeDcbWindowState()
	switch function {
	case DcbFunctionHoldBarsBrightness:
		return active.Brightness.HoldBars
	case DcbFunctionMovementAreaBrightness:
		return active.Brightness.MovementArea
	case DcbFunctionBackgroundBrightness:
		return active.Brightness.Background
	case DcbFunctionTrackBrightness:
		return active.Brightness.Track
	case DcbFunctionDataBlocksBrightness:
		return active.DB.Brightness
	case DcbFunctionListsBrightness:
		return p.listsBrightness
	case DcbFunctionTempMapAreasBrightness:
		return active.Brightness.TempMapAreas
	case DcbFunctionTempMapTextBrightness:
		return active.Brightness.TempMapText
	case DcbFunctionDcbBrightness:
		return p.dcbBrightness
	default:
		return brightnessDefault
	}
}

func (p *ASDEXPane) setBrightnessValue(function DcbFunction, value int) {
	if p == nil {
		return
	}

	value = clampBrightness(value)
	windowID := p.activeWindowID()
	state := p.displayStateForWindow(windowID)
	switch function {
	case DcbFunctionHoldBarsBrightness:
		state.Brightness.HoldBars = value
	case DcbFunctionMovementAreaBrightness:
		state.Brightness.MovementArea = value
	case DcbFunctionBackgroundBrightness:
		state.Brightness.Background = value
	case DcbFunctionTrackBrightness:
		state.Brightness.Track = value
	case DcbFunctionDataBlocksBrightness:
		state.DB.Brightness = value
	case DcbFunctionListsBrightness:
		p.setListsBrightness(value)
	case DcbFunctionTempMapAreasBrightness:
		state.Brightness.TempMapAreas = value
	case DcbFunctionTempMapTextBrightness:
		state.Brightness.TempMapText = value
	case DcbFunctionDcbBrightness:
		p.setDcbBrightness(value)
	}
}

func (p *ASDEXPane) openBrightnessMenu() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.dcb.SetMenu(DcbMenuBrightness)
	p.dcbMenuCommand = NewDcbMenuCommand("BRITE")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) openCharSizeMenu() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.dcb.SetMenu(DcbMenuCharSize)
	p.dcbMenuCommand = NewDcbMenuCommand("CHAR SIZE")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) activateCharSizeDcbHit(hit DcbHit) bool {
	if p == nil || !hit.HasFunction {
		return false
	}

	active := p.activeDcbWindowState()
	switch hit.Function {
	case DcbFunctionDataBlockCharSize:
		p.startCharSizeSpinner(
			DcbFunctionDataBlockCharSize,
			"DATA BLOCK",
			active.DB.FontSize,
			1,
			6,
		)
		return true
	case DcbFunctionDcbCharSize:
		p.startCharSizeSpinner(
			DcbFunctionDcbCharSize,
			"DCB",
			p.dcb.CharSize(),
			1,
			3,
		)
		return true
	case DcbFunctionCoastSuspendCharSize:
		p.startCharSizeSpinner(
			DcbFunctionCoastSuspendCharSize,
			"CS LIST",
			p.coastList.FontSize(),
			1,
			6,
		)
		return true
	case DcbFunctionTempDataCharSize:
		p.startCharSizeSpinner(
			DcbFunctionTempDataCharSize,
			"TEMP DATA",
			active.TempDataCharSize,
			1,
			6,
		)
		return true
	case DcbFunctionPreviewAreaCharSize:
		p.startCharSizeSpinner(
			DcbFunctionPreviewAreaCharSize,
			"PREVIEW",
			p.previewArea.FontSize(),
			1,
			6,
		)
		return true
	case DcbFunctionDone:
		p.closeDcbSubmenu()
		return true
	default:
		return false
	}
}

func (p *ASDEXPane) startCharSizeSpinner(
	function DcbFunction,
	label string,
	current int,
	minValue int,
	maxValue int,
) {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.dcb.SetMenu(DcbMenuCharSize)
	p.dcbMenuCommand = nil
	p.dcbSpinner = NewCharSizeDcbSpinner(function, label, current, minValue, maxValue)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) openToolsMenu() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.dcb.SetMenu(DcbMenuTools)
	p.dcbMenuCommand = NewDcbMenuCommand("TOOLS")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) openPrefsMenu() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.prefPage = 1
	p.dcb.SetMenu(DcbMenuPrefs)
	p.dcbMenuCommand = NewDcbMenuCommand("PREFS")
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) activatePrefsDcbHit(ctx *panes.Context, hit DcbHit) bool {
	if p == nil || !hit.HasFunction {
		return false
	}

	switch hit.Function {
	case DcbFunctionPrefPage1:
		p.prefPage = 1
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	case DcbFunctionPrefPage2:
		p.prefPage = 2
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	case DcbFunctionPrefPreset,
		DcbFunctionPrefOpInits,
		DcbFunctionPrefSaveAs,
		DcbFunctionPrefModify,
		DcbFunctionPrefChangePin,
		DcbFunctionPrefDelete:
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	case DcbFunctionDefault:
		p.applyCommandStatus(p.cmdDefault(ctx))
		p.dcb.SetMenu(DcbMenuPrefs)
		p.dcbMenuCommand = NewDcbMenuCommand("PREFS")
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	case DcbFunctionUndo:
		p.executeUndoCommand(ctx)
		p.dcb.SetMenu(DcbMenuPrefs)
		p.dcbMenuCommand = NewDcbMenuCommand("PREFS")
		return true
	case DcbFunctionDone:
		p.closeDcbSubmenu()
		return true
	default:
		return false
	}
}

func (p *ASDEXPane) openSafetyLogicMenu() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.dcb.SetMenu(DcbMenuSafetyLogic)
	p.dcbMenuCommand = NewDcbMenuCommand("SAFETY LOGIC")
	p.previewArea.SetSystemResponse("")
	p.refreshRunwayConfigPreviewLine()
	p.refreshTowerConfigPreviewLine()
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) openRunwayConfigMenu() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.runwayConfigPage = 1
	p.dcb.SetMenu(DcbMenuRunwayConfig)
	p.dcbMenuCommand = NewDcbMenuCommand("SAFETY LOGIC", "RWY CONFIG")
	p.previewArea.SetSystemResponse("")
	p.refreshRunwayConfigPreviewLine()
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) openTowerConfigMenu() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.dcb.SetMenu(DcbMenuTowerConfig)
	p.dcbMenuCommand = NewDcbMenuCommand("SAFETY LOGIC", "TOWER CONFIG")
	p.previewArea.SetSystemResponse("")
	p.refreshTowerConfigPreviewLine()
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startTrackAlertInhibitFromDcb(_ *panes.Context) {
	if p == nil {
		return
	}

	status := p.startTrackAlertInhibitCommand(
		DcbMenuSafetyLogic,
		[]string{"SAFETY LOGIC"},
		true,
	)
	p.applyCommandStatus(status)
}

func (p *ASDEXPane) startMapRotateCommand(command *MapRotateCommand) {
	if p == nil || command == nil {
		return
	}

	p.commandMode = CommandModeMapRotate
	p.mapRotate = command
	p.mapReposition = nil
	p.towerReadout = nil
	p.multiFunction = nil
	p.scratchpadEntry = nil
	p.previewReposition = nil
	p.coastListReposition = nil
	p.dcbSpinner = nil
	p.dcbMenuCommand = nil
	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.tempData.ClearHighlights()
	p.newWindow = nil
	p.deleteWindow = nil
	p.windowReposition = nil
	p.resizeWindow = nil
	p.datablockEdit = nil
	p.editingTargetID = ""
	p.initControlEntry = nil
	p.termControlEntry = nil
	p.commandEntry.Clear()

	if command.returnMenu == DcbMenuTools {
		p.dcb.SetMenu(DcbMenuTools)
	} else {
		p.dcb.ReturnToMainMenu()
	}

	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startDcbRotateCommand() {
	if p == nil {
		return
	}

	windowID := p.activeWindowID()
	view := p.activeScopeView()
	if p.dcb.Menu() == DcbMenuTools {
		p.startMapRotateCommand(NewToolsMapRotateCommand(windowID, view.Rotation))
		return
	}

	p.startMapRotateCommand(NewMainMapRotateCommand(windowID, view.Rotation))
}

func (p *ASDEXPane) startNewWindowCommand(command *NewWindowCommand) {
	if p == nil || command == nil {
		return
	}

	p.commandMode = CommandModeNone
	p.commandEntry.Clear()
	p.datablockEdit = nil
	p.editingTargetID = ""
	p.initControlEntry = nil
	p.termControlEntry = nil
	p.multiFunction = nil
	p.scratchpadEntry = nil
	p.previewReposition = nil
	p.coastListReposition = nil
	p.mapReposition = nil
	p.mapRotate = nil
	p.runwayConfigCommand = nil
	p.towerReadout = nil
	p.dcbSpinner = nil
	p.dcbMenuCommand = nil
	p.clearTrackAlertInhibitReturnContext()
	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.tempData.ClearHighlights()
	p.newWindow = command
	p.deleteWindow = nil
	p.windowReposition = nil
	p.resizeWindow = nil

	if command.returnMenu == DcbMenuTools {
		p.dcb.SetMenu(DcbMenuTools)
	} else {
		p.dcb.ReturnToMainMenu()
	}

	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startToolsNewWindowCommand() {
	if p == nil {
		return
	}
	if !p.windows.CanAddSecondary() {
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return
	}

	p.startNewWindowCommand(NewToolsNewWindowCommand())
}

func (p *ASDEXPane) startDeleteWindowCommand() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.dcb.SetMenu(DcbMenuTools)
	p.dcbMenuCommand = nil
	p.deleteWindow = NewDeleteWindowCommand()
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startWindowRepositionCommand(ctx *panes.Context) {
	if p == nil || ctx == nil {
		return
	}

	windowID := p.activeWindowID()
	if windowID == mainScopeWindowID {
		return
	}

	rect, ok := p.scopeWindowRectForWindow(windowID, ctx.PaneSize())
	if !ok {
		return
	}

	p.startWindowRepositionForWindow(
		windowID,
		rect,
		NewToolsWindowRepositionCommand(windowID, rect),
	)
}

func (p *ASDEXPane) startWindowRepositionForWindow(
	windowID ScopeWindowID,
	rect redsmath.Rect,
	command *WindowRepositionCommand,
) {
	if p == nil || command == nil || windowID == mainScopeWindowID || rect.Empty() {
		return
	}

	p.clearDcbModalConflicts()
	p.windows.SetActiveWindow(windowID)

	if command.restoreDcbMenu {
		p.dcb.SetMenu(command.returnMenu)
		if len(command.returnLines) > 0 {
			p.dcbMenuCommand = NewDcbMenuCommand(command.returnLines...)
		} else {
			p.dcbMenuCommand = nil
		}
	} else {
		p.dcb.ReturnToMainMenu()
		p.dcbMenuCommand = nil
	}

	p.windowReposition = command
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startResizeWindowCommand() {
	if p == nil {
		return
	}

	windowID := p.activeWindowID()
	if windowID == mainScopeWindowID {
		return
	}
	if _, ok := p.scopeViewForWindow(windowID); !ok {
		return
	}

	p.startResizeWindowForWindow(
		windowID,
		NewToolsResizeWindowCommand(windowID),
	)
}

func (p *ASDEXPane) startResizeWindowForWindow(
	windowID ScopeWindowID,
	command *ResizeWindowCommand,
) {
	if p == nil || command == nil || windowID == mainScopeWindowID {
		return
	}
	if _, ok := p.scopeViewForWindow(windowID); !ok {
		return
	}

	p.clearDcbModalConflicts()
	p.windows.SetActiveWindow(windowID)

	if command.restoreDcbMenu {
		p.dcb.SetMenu(command.returnMenu)
		if len(command.returnLines) > 0 {
			p.dcbMenuCommand = NewDcbMenuCommand(command.returnLines...)
		} else {
			p.dcbMenuCommand = nil
		}
	} else {
		p.dcb.ReturnToMainMenu()
		p.dcbMenuCommand = nil
	}

	p.resizeWindow = command
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func isToolsPlaceholderFunction(function DcbFunction) bool {
	switch function {
	case DcbFunctionRange,
		DcbFunctionMapReposition,
		DcbFunctionCoastReposition,
		DcbFunctionPreviewReposition,
		DcbFunctionCursorSpeed,
		DcbFunctionCursorHomeOnOff,
		DcbFunctionChangePassword:
		return true
	default:
		return false
	}
}

func (p *ASDEXPane) startBrightnessSpinner(function DcbFunction) {
	if p == nil {
		return
	}

	label := brightnessLabel(function)
	if label == "" {
		return
	}
	p.dcbSpinner = NewBrightnessSpinner(function, label, p.currentBrightnessValue(function))
	p.dcbMenuCommand = nil
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) activateTraitAreaDcbHit(hit DcbHit) bool {
	if p == nil {
		return false
	}

	switch hit.Function {
	case DcbFunctionDbFullPart:
		return p.updateSelectedDataBlockAreaTraits(func(t *DataBlockAreaTraits) {
			t.FullDataBlocks = !t.FullDataBlocks
		})
	case DcbFunctionDbAltitudeOnOff:
		return p.updateSelectedDataBlockAreaTraits(func(t *DataBlockAreaTraits) {
			t.ShowAltitude = !t.ShowAltitude
		})
	case DcbFunctionDbTypeOnOff:
		return p.updateSelectedDataBlockAreaTraits(func(t *DataBlockAreaTraits) {
			t.ShowTargetType = !t.ShowTargetType
		})
	case DcbFunctionDbSensorsOnOff:
		return p.updateSelectedDataBlockAreaTraits(func(t *DataBlockAreaTraits) {
			t.ShowSensors = !t.ShowSensors
		})
	case DcbFunctionDbCategoryOnOff:
		return p.updateSelectedDataBlockAreaTraits(func(t *DataBlockAreaTraits) {
			t.ShowCWT = !t.ShowCWT
		})
	case DcbFunctionDbFixOnOff:
		return p.updateSelectedDataBlockAreaTraits(func(t *DataBlockAreaTraits) {
			t.ShowFix = !t.ShowFix
		})
	case DcbFunctionDbVelocityOnOff:
		return p.updateSelectedDataBlockAreaTraits(func(t *DataBlockAreaTraits) {
			t.ShowVelocity = !t.ShowVelocity
		})
	case DcbFunctionDbScratchpadOnOff:
		return p.updateSelectedDataBlockAreaTraits(func(t *DataBlockAreaTraits) {
			t.ShowScratchpads = !t.ShowScratchpads
		})
	case DcbFunctionDbAreaVectorOnOff:
		return p.updateSelectedDataBlockAreaTraits(func(t *DataBlockAreaTraits) {
			t.ShowVector = !t.ShowVector
		})
	case DcbFunctionDbAreaDataBlockCharSize:
		p.startDbAreaCharSizeSpinner()
		return true
	case DcbFunctionDbAreaDataBlockBrightness:
		p.startDbAreaBrightnessSpinner()
		return true
	case DcbFunctionDbAreaLeaderLength:
		p.startDbAreaLeaderLengthSpinner()
		return true
	case DcbFunctionDbAreaLeaderDirection:
		p.startDbAreaLeaderDirectionSpinner()
		return true
	}
	return false
}

func (p *ASDEXPane) updateSelectedDataBlockAreaTraits(
	update func(*DataBlockAreaTraits),
) bool {
	if p == nil || update == nil {
		return false
	}

	windowID := p.activeWindowID()
	state := p.displayStateForWindow(windowID)
	area, ok := state.selectedDataBlockArea()
	if !ok || area.Traits.DataBlocksOff {
		p.previewArea.SetSystemResponse("")
		return false
	}

	before := p.pushUndoBeforeMutation()
	update(&area.Traits)
	p.clearTraitLeaderOverridesForArea(windowID, area.ID)
	p.commitUndoIfChanged(before)
	p.previewArea.SetSystemResponse("")
	return true
}

func (p *ASDEXPane) updateDataBlockAreaTraitsByID(
	windowID ScopeWindowID,
	areaID string,
	update func(*DataBlockAreaTraits),
) bool {
	if p == nil || areaID == "" || update == nil {
		return false
	}

	state := p.displayStateForWindow(windowID)
	for i := range state.DataBlockAreas {
		area := &state.DataBlockAreas[i]
		if area.ID != areaID || area.Traits.DataBlocksOff {
			continue
		}

		update(&area.Traits)
		p.clearTraitLeaderOverridesForArea(windowID, areaID)
		return true
	}
	return false
}

func (p *ASDEXPane) selectedDbAreaForEdit() (ScopeWindowID, *WindowDisplayState, *DataBlockArea, bool) {
	if p == nil {
		return 0, nil, nil, false
	}

	windowID := p.activeWindowID()
	state := p.displayStateForWindow(windowID)
	area, ok := state.selectedDataBlockArea()
	if !ok || area.Traits.DataBlocksOff {
		return 0, nil, nil, false
	}
	return windowID, state, area, true
}

func (p *ASDEXPane) startDbAreaCharSizeSpinner() {
	if p == nil {
		return
	}

	windowID, _, area, ok := p.selectedDbAreaForEdit()
	if !ok {
		p.previewArea.SetSystemResponse("")
		return
	}

	returnMenu := p.dcb.Menu()
	areaID := area.ID
	current := area.Traits.FontSize
	p.clearDcbModalConflicts()
	p.dcb.SetMenu(returnMenu)
	p.dcbMenuCommand = nil
	p.dcbSpinner = NewDbAreaCharSizeSpinner(windowID, areaID, returnMenu, current)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startDbAreaBrightnessSpinner() {
	if p == nil {
		return
	}

	windowID, _, area, ok := p.selectedDbAreaForEdit()
	if !ok {
		p.previewArea.SetSystemResponse("")
		return
	}

	returnMenu := p.dcb.Menu()
	areaID := area.ID
	current := area.Traits.Brightness
	p.clearDcbModalConflicts()
	p.dcb.SetMenu(returnMenu)
	p.dcbMenuCommand = nil
	p.dcbSpinner = NewDbAreaBrightnessSpinner(windowID, areaID, returnMenu, current)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startDbAreaLeaderLengthSpinner() {
	if p == nil {
		return
	}

	windowID, _, area, ok := p.selectedDbAreaForEdit()
	if !ok {
		p.previewArea.SetSystemResponse("")
		return
	}

	returnMenu := p.dcb.Menu()
	areaID := area.ID
	current := area.Traits.LeaderLength
	p.clearDcbModalConflicts()
	p.dcb.SetMenu(returnMenu)
	p.dcbMenuCommand = nil
	p.dcbSpinner = NewDbAreaLeaderLengthSpinner(windowID, areaID, returnMenu, current)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startDbAreaLeaderDirectionSpinner() {
	if p == nil {
		return
	}

	windowID, _, area, ok := p.selectedDbAreaForEdit()
	if !ok {
		p.previewArea.SetSystemResponse("")
		return
	}

	returnMenu := p.dcb.Menu()
	areaID := area.ID
	current := area.Traits.LeaderDirection
	p.clearDcbModalConflicts()
	p.dcb.SetMenu(returnMenu)
	p.dcbMenuCommand = nil
	p.dcbSpinner = NewDbAreaLeaderDirectionSpinner(windowID, areaID, returnMenu, current)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startRangeSpinner() {
	if p == nil {
		return
	}

	windowID := p.activeWindowID()
	currentRange := p.activeRangeSetting()

	p.commandMode = CommandModeNone
	p.datablockEdit = nil
	p.editingTargetID = ""
	p.initControlEntry = nil
	p.termControlEntry = nil
	p.multiFunction = nil
	p.scratchpadEntry = nil
	p.previewReposition = nil
	p.coastListReposition = nil
	p.mapReposition = nil
	p.mapRotate = nil
	p.runwayConfigCommand = nil
	p.towerReadout = nil
	p.dcbMenuCommand = nil
	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.tempData.ClearHighlights()
	p.newWindow = nil
	p.deleteWindow = nil
	p.windowReposition = nil
	p.resizeWindow = nil
	p.commandEntry.Clear()
	p.dcbSpinner = NewRangeDcbSpinner(windowID, currentRange)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) toggleHistoryForActiveWindow() {
	if p == nil {
		return
	}

	before := p.pushUndoBeforeMutation()
	state := p.displayStateForWindow(p.activeWindowID())
	state.ShowHistory = !state.ShowHistory
	p.commitUndoIfChanged(before)

	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) toggleVectorLineForActiveWindow() {
	if p == nil {
		return
	}

	before := p.pushUndoBeforeMutation()
	state := p.displayStateForWindow(p.activeWindowID())
	state.ShowVectorLine = !state.ShowVectorLine
	p.commitUndoIfChanged(before)

	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) toggleCoastList() {
	if p == nil {
		return
	}

	before := p.pushUndoBeforeMutation()
	p.showCoastList = !p.showCoastList
	if !p.showCoastList {
		p.hoveredCoastListTarget = ""
	}
	p.commitUndoIfChanged(before)

	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startHistorySpinner() {
	if p == nil {
		return
	}

	windowID := p.activeWindowID()
	state := p.displayStateForWindow(windowID)

	p.clearDcbModalConflicts()
	p.dcb.SetMenu(DcbMenuTools)
	p.dcbMenuCommand = nil
	p.dcbSpinner = NewHistoryDcbSpinner(windowID, state.HistoryLength)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startVectorLengthSpinner() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.dcb.ReturnToMainMenu()
	p.dcbMenuCommand = nil
	p.dcbSpinner = NewVectorLengthDcbSpinner(p.vectorLength)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startSafetyVolumeSpinner() {
	if p == nil {
		return
	}

	p.clearDcbModalConflicts()
	p.dcb.SetMenu(DcbMenuSafetyLogic)
	p.dcbMenuCommand = nil
	p.dcbSpinner = NewSafetyVolumeDcbSpinner(p.auralVolume)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) consumeDcbSpinnerInput(ctx *panes.Context) bool {
	if p == nil || p.dcbSpinner == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}

	mouse := ctx.Mouse
	switch {
	case mouse.Wheel.Y > 0:
		p.incrementActiveDcbSpinner(-1)
		return true
	case mouse.Wheel.Y < 0:
		p.incrementActiveDcbSpinner(1)
		return true
	case mouse.Wheel.X > 0:
		p.incrementActiveDcbSpinner(1)
		return true
	case mouse.Wheel.X < 0:
		p.incrementActiveDcbSpinner(-1)
		return true
	case mouse.WasReleased(platform.MouseButtonLeft):
		p.acceptActiveDcbSpinner()
		return true
	default:
		return false
	}
}

func (p *ASDEXPane) acceptActiveDcbSpinner() {
	if p == nil || p.dcbSpinner == nil {
		return
	}

	spinner := p.dcbSpinner
	switch spinner.Type {
	case DcbSpinnerRange:
		p.commitSpinnerUndoIfChanged(spinner)
		p.dcbSpinner = nil
		p.previewArea.SetSystemResponse("")
	case DcbSpinnerBrightness:
		p.finishBrightnessSpinner("")
	case DcbSpinnerCharSize:
		p.finishCharSizeSpinner("")
	case DcbSpinnerHistory:
		p.finishHistorySpinner("")
	case DcbSpinnerVectorLength:
		p.finishVectorLengthSpinner("")
	case DcbSpinnerSafetyVolume:
		p.finishSafetyVolumeSpinner("")
	case DcbSpinnerDbAreaCharSize,
		DcbSpinnerDbAreaBrightness,
		DcbSpinnerDbAreaLeaderLength,
		DcbSpinnerDbAreaLeaderDirection:
		p.finishDbAreaSpinner(spinner, "")
	default:
		p.dcbSpinner = nil
		p.previewArea.SetSystemResponse("")
	}
}

func (p *ASDEXPane) cancelDcbSpinner() {
	if p == nil {
		return
	}
	if p.dcbSpinner != nil && p.dcbSpinner.Type == DcbSpinnerVectorLength {
		p.finishVectorLengthSpinner("")
		return
	}
	if p.dcbSpinner != nil && p.dcbSpinner.Type == DcbSpinnerSafetyVolume {
		p.finishSafetyVolumeSpinner("")
		return
	}
	if p.dcbSpinner != nil && p.dcbSpinner.Type == DcbSpinnerCharSize {
		p.finishCharSizeSpinner("")
		return
	}
	if p.dcbSpinner != nil && p.dcbSpinner.Type == DcbSpinnerBrightness {
		p.commitSpinnerUndoIfChanged(p.dcbSpinner)
		p.dcbSpinner = nil
		p.dcb.SetMenu(DcbMenuBrightness)
		p.dcbMenuCommand = NewDcbMenuCommand("BRITE")
		p.previewArea.SetSystemResponse("")
		return
	}
	if p.dcbSpinner != nil && p.dcbSpinner.Type == DcbSpinnerHistory {
		p.finishHistorySpinner("")
		return
	}
	if p.dcbSpinner != nil && p.dcbSpinner.Type != DcbSpinnerRange {
		p.restoreDbAreaEditCommand(p.dcbSpinner)
	}
	p.commitSpinnerUndoIfChanged(p.dcbSpinner)
	p.dcbSpinner = nil
	p.previewArea.SetSystemResponse("")
}

func (p *ASDEXPane) restoreDbAreaEditCommand(spinner *DcbSpinner) {
	if p == nil || spinner == nil {
		return
	}

	if spinner.ReturnMenu != DcbMenuOff {
		p.dcb.SetMenu(spinner.ReturnMenu)
	}
	if len(spinner.ReturnLines) > 0 {
		p.dcbMenuCommand = NewDcbMenuCommand(spinner.ReturnLines...)
		return
	}
	p.dcb.SetMenu(dbAreaEditMenu(spinner.DbAreaEditMode))
	p.dcbMenuCommand = NewDcbMenuCommand(dbAreaEditCommandLines(spinner.DbAreaEditMode)...)
}

func (p *ASDEXPane) finishDbAreaSpinner(spinner *DcbSpinner, systemResponse string) {
	if p == nil || spinner == nil {
		return
	}

	p.restoreDbAreaEditCommand(spinner)
	p.commitSpinnerUndoIfChanged(spinner)
	p.dcbSpinner = nil
	p.previewArea.SetSystemResponse(systemResponse)
}

func (p *ASDEXPane) commitDcbSpinner() {
	if p == nil || p.dcbSpinner == nil {
		return
	}

	spinner := p.dcbSpinner
	switch spinner.Type {
	case DcbSpinnerRange:
		if strings.TrimSpace(spinner.InputText()) == "" {
			p.commitSpinnerUndoIfChanged(spinner)
			p.dcbSpinner = nil
			p.previewArea.SetSystemResponse("")
			return
		}

		value, ok := spinner.ParsedValue()
		if !ok {
			p.commitSpinnerUndoIfChanged(spinner)
			p.dcbSpinner = nil
			p.previewArea.SetSystemResponse("INVALID RANGE")
			return
		}

		before := p.undoBeforeForSpinnerMutation(spinner)
		p.setRangeSettingForWindow(spinner.WindowID, value)
		p.commitUndoIfChanged(before)
		p.dcbSpinner = nil
		p.previewArea.SetSystemResponse("")
		return
	case DcbSpinnerHistory:
		p.commitHistorySpinner(spinner)
		return
	case DcbSpinnerVectorLength:
		p.commitVectorLengthSpinner(spinner)
		return
	case DcbSpinnerSafetyVolume:
		p.commitSafetyVolumeSpinner(spinner)
		return
	case DcbSpinnerCharSize:
		p.commitCharSizeSpinner(spinner)
		return
	case DcbSpinnerDbAreaCharSize:
		p.commitDbAreaCharSizeSpinner(spinner)
		return
	case DcbSpinnerDbAreaBrightness:
		p.commitDbAreaBrightnessSpinner(spinner)
		return
	case DcbSpinnerDbAreaLeaderLength:
		p.commitDbAreaLeaderLengthSpinner(spinner)
		return
	case DcbSpinnerDbAreaLeaderDirection:
		p.commitDbAreaLeaderDirectionSpinner(spinner)
		return
	case DcbSpinnerBrightness:
		p.commitBrightnessSpinner(spinner)
		return
	default:
		p.dcbSpinner = nil
		p.previewArea.SetSystemResponse("INVALID ENTRY")
		return
	}
}

func (p *ASDEXPane) commitDbAreaCharSizeSpinner(spinner *DcbSpinner) {
	text := strings.TrimSpace(spinner.InputText())
	value, err := strconv.Atoi(text)
	if err != nil || value < 1 || value > 6 {
		p.finishDbAreaSpinner(spinner, "INVALID SIZE")
		return
	}

	before := p.undoBeforeForSpinnerMutation(spinner)
	if p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
		t.FontSize = value
	}) {
		p.commitUndoIfChanged(before)
		p.finishDbAreaSpinner(spinner, "")
		return
	}
	p.finishDbAreaSpinner(spinner, "INVALID SIZE")
}

func (p *ASDEXPane) commitDbAreaBrightnessSpinner(spinner *DcbSpinner) {
	text := strings.TrimSpace(spinner.InputText())
	value, err := strconv.Atoi(text)
	if err != nil || value < brightnessMin || value > brightnessMax {
		p.finishDbAreaSpinner(spinner, "INVALID ENTRY")
		return
	}

	before := p.undoBeforeForSpinnerMutation(spinner)
	if p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
		t.Brightness = value
	}) {
		p.commitUndoIfChanged(before)
		p.finishDbAreaSpinner(spinner, "")
		return
	}
	p.finishDbAreaSpinner(spinner, "INVALID ENTRY")
}

func (p *ASDEXPane) commitDbAreaLeaderLengthSpinner(spinner *DcbSpinner) {
	text := strings.TrimSpace(spinner.InputText())
	value, err := strconv.Atoi(text)
	if err != nil || value < leaderLengthMin || value > leaderLengthMax {
		p.finishDbAreaSpinner(spinner, "INVALID LNG")
		return
	}

	before := p.undoBeforeForSpinnerMutation(spinner)
	if p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
		t.LeaderLength = value
	}) {
		p.commitUndoIfChanged(before)
		p.finishDbAreaSpinner(spinner, "")
		return
	}
	p.finishDbAreaSpinner(spinner, "INVALID LNG")
}

func (p *ASDEXPane) commitDbAreaLeaderDirectionSpinner(spinner *DcbSpinner) {
	text := strings.TrimSpace(spinner.InputText())
	value, err := strconv.Atoi(text)
	if err != nil || value < 1 || value > 9 || value == 5 {
		p.finishDbAreaSpinner(spinner, "INVALID ENTRY")
		return
	}

	direction, ok := leaderDirectionFromDisplayValue(value)
	if !ok {
		p.finishDbAreaSpinner(spinner, "INVALID ENTRY")
		return
	}
	before := p.undoBeforeForSpinnerMutation(spinner)
	if p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
		t.LeaderDirection = direction
	}) {
		p.commitUndoIfChanged(before)
		p.finishDbAreaSpinner(spinner, "")
		return
	}
	p.finishDbAreaSpinner(spinner, "INVALID ENTRY")
}

func (p *ASDEXPane) finishBrightnessSpinner(systemResponse string) {
	if p == nil {
		return
	}
	p.commitSpinnerUndoIfChanged(p.dcbSpinner)
	p.dcbSpinner = nil
	p.dcb.SetMenu(DcbMenuBrightness)
	p.dcbMenuCommand = NewDcbMenuCommand("BRITE")
	p.previewArea.SetSystemResponse(systemResponse)
}

func (p *ASDEXPane) commitBrightnessSpinner(spinner *DcbSpinner) {
	if p == nil || spinner == nil {
		return
	}

	value, ok := spinner.ParsedValue()
	if !ok || value < brightnessMin || value > brightnessMax {
		p.finishBrightnessSpinner("INVALID ENTRY")
		return
	}

	before := p.undoBeforeForSpinnerMutation(spinner)
	p.setBrightnessValue(spinner.Function, value)
	p.commitUndoIfChanged(before)
	p.finishBrightnessSpinner("")
}

func (p *ASDEXPane) commitCharSizeSpinner(spinner *DcbSpinner) {
	if p == nil || spinner == nil {
		return
	}

	text := strings.TrimSpace(spinner.InputText())
	value, err := strconv.Atoi(text)
	if err != nil || value < spinner.Min || value > spinner.Max {
		p.finishCharSizeSpinner("INVALID SIZE")
		return
	}

	before := p.undoBeforeForSpinnerMutation(spinner)
	p.setCharSizeValue(spinner.Function, value)
	p.commitUndoIfChanged(before)
	p.finishCharSizeSpinner("")
}

func (p *ASDEXPane) finishCharSizeSpinner(systemResponse string) {
	if p == nil {
		return
	}

	p.commitSpinnerUndoIfChanged(p.dcbSpinner)
	p.dcbSpinner = nil
	p.dcb.SetMenu(DcbMenuCharSize)
	p.dcbMenuCommand = NewDcbMenuCommand("CHAR SIZE")
	p.previewArea.SetSystemResponse(systemResponse)
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) setCharSizeValue(function DcbFunction, value int) {
	if p == nil {
		return
	}

	switch function {
	case DcbFunctionDataBlockCharSize:
		value = clampInt(value, 1, 6)
		state := p.displayStateForWindow(p.activeWindowID())
		state.DB.FontSize = value
	case DcbFunctionDcbCharSize:
		value = clampInt(value, 1, 3)
		p.dcb.SetCharSize(value)
	case DcbFunctionCoastSuspendCharSize:
		value = clampInt(value, 1, 6)
		p.coastList.SetFontSize(value)
	case DcbFunctionTempDataCharSize:
		value = clampInt(value, 1, 6)
		p.displayStateForWindow(p.activeWindowID()).TempDataCharSize = value
	case DcbFunctionPreviewAreaCharSize:
		value = clampInt(value, 1, 6)
		p.previewArea.SetFontSize(value)
	}
}

func (p *ASDEXPane) commitHistorySpinner(spinner *DcbSpinner) {
	if p == nil || spinner == nil {
		return
	}

	value, ok := spinner.ParsedValue()
	if !ok || value < 1 || value > 7 {
		p.finishHistorySpinner("INVALID ENTRY")
		return
	}

	before := p.undoBeforeForSpinnerMutation(spinner)
	p.setHistoryLengthForWindow(spinner.WindowID, value)
	p.commitUndoIfChanged(before)
	p.finishHistorySpinner("")
}

func (p *ASDEXPane) finishHistorySpinner(systemResponse string) {
	if p == nil {
		return
	}

	p.commitSpinnerUndoIfChanged(p.dcbSpinner)
	p.dcbSpinner = nil
	p.dcb.SetMenu(DcbMenuTools)
	p.dcbMenuCommand = NewDcbMenuCommand("TOOLS")
	p.previewArea.SetSystemResponse(systemResponse)
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) commitVectorLengthSpinner(spinner *DcbSpinner) {
	if p == nil || spinner == nil {
		return
	}

	value, ok := spinner.ParsedValue()
	if !ok || value < minTargetVectorSeconds || value > maxTargetVectorSeconds {
		p.finishVectorLengthSpinner("INVALID ENTRY")
		return
	}

	before := p.undoBeforeForSpinnerMutation(spinner)
	p.setVectorLength(value)
	p.commitUndoIfChanged(before)
	p.finishVectorLengthSpinner("")
}

func (p *ASDEXPane) finishVectorLengthSpinner(systemResponse string) {
	if p == nil {
		return
	}

	p.commitSpinnerUndoIfChanged(p.dcbSpinner)
	p.dcbSpinner = nil
	p.dcb.ReturnToMainMenu()
	p.dcbMenuCommand = nil
	p.previewArea.SetSystemResponse(systemResponse)
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) commitSafetyVolumeSpinner(spinner *DcbSpinner) {
	if p == nil || spinner == nil {
		return
	}

	value, ok := spinner.ParsedValue()
	if !ok || value < minAuralVolume || value > maxAuralVolume {
		p.finishSafetyVolumeSpinner("INVALID ENTRY")
		return
	}

	p.setSafetyVolume(value)
	p.finishSafetyVolumeSpinner("")
}

func (p *ASDEXPane) finishSafetyVolumeSpinner(systemResponse string) {
	if p == nil {
		return
	}

	p.dcbSpinner = nil
	p.dcb.SetMenu(DcbMenuSafetyLogic)
	p.dcbMenuCommand = NewDcbMenuCommand("SAFETY LOGIC")
	p.previewArea.SetSystemResponse(systemResponse)
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) incrementActiveDcbSpinner(delta int) {
	if p == nil || p.dcbSpinner == nil || delta == 0 {
		return
	}

	spinner := p.dcbSpinner
	switch spinner.Type {
	case DcbSpinnerRange:
		p.captureSpinnerUndo(spinner)
		windowID := spinner.WindowID
		view, ok := p.scopeViewForWindow(windowID)
		if !ok {
			windowID = p.activeWindowID()
			view = p.activeScopeView()
			spinner.WindowID = windowID
		}

		next := view.RangeSetting
		if next == 0 {
			next = asdexDefaultRangeSetting
		}
		next = clampInt(
			next+delta,
			asdexMinRangeSetting,
			asdexMaxRangeSetting,
		)

		p.setRangeSettingForWindow(windowID, next)
		spinner.Value = next
	case DcbSpinnerHistory:
		p.captureSpinnerUndo(spinner)
		next := clampInt(spinner.Value+delta, 1, 7)
		if next != spinner.Value {
			spinner.Value = next
			p.setHistoryLengthForWindow(spinner.WindowID, next)
		}
	case DcbSpinnerVectorLength:
		p.captureSpinnerUndo(spinner)
		next := clampInt(
			p.vectorLength+delta,
			minTargetVectorSeconds,
			maxTargetVectorSeconds,
		)
		if next != p.vectorLength {
			p.setVectorLength(next)
			spinner.Value = next
		}
	case DcbSpinnerSafetyVolume:
		next := clampInt(
			p.auralVolume+delta,
			minAuralVolume,
			maxAuralVolume,
		)
		if next != p.auralVolume {
			p.setSafetyVolume(next)
			spinner.Value = next
		}
	case DcbSpinnerCharSize:
		p.captureSpinnerUndo(spinner)
		next := clampInt(spinner.Value+delta, spinner.Min, spinner.Max)
		if next != spinner.Value {
			spinner.Value = next
			p.setCharSizeValue(spinner.Function, next)
		}
	case DcbSpinnerDbAreaCharSize:
		p.captureSpinnerUndo(spinner)
		next := clampInt(spinner.Value+delta, 1, 6)
		if next != spinner.Value {
			spinner.Value = next
			p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
				t.FontSize = next
			})
		}
	case DcbSpinnerDbAreaBrightness:
		p.captureSpinnerUndo(spinner)
		next := clampInt(spinner.Value+delta, brightnessFloorDefault, brightnessMax)
		if next != spinner.Value {
			spinner.Value = next
			p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
				t.Brightness = next
			})
		}
	case DcbSpinnerDbAreaLeaderLength:
		p.captureSpinnerUndo(spinner)
		next := clampInt(spinner.Value+delta, leaderLengthMin, leaderLengthMax)
		if next != spinner.Value {
			spinner.Value = next
			p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
				t.LeaderLength = next
			})
		}
	case DcbSpinnerDbAreaLeaderDirection:
		p.captureSpinnerUndo(spinner)
		next := spinner.Value + delta
		if delta > 0 && next == 5 {
			next++
		} else if delta < 0 && next == 5 {
			next--
		}
		next = clampInt(next, 1, 9)
		if next != spinner.Value {
			direction, ok := leaderDirectionFromDisplayValue(next)
			if ok {
				spinner.Value = next
				p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
					t.LeaderDirection = direction
				})
			}
		}
	case DcbSpinnerBrightness:
		p.captureSpinnerUndo(spinner)
		next := clampBrightness(p.currentBrightnessValue(spinner.Function) + delta)
		p.setBrightnessValue(spinner.Function, next)
		spinner.Value = next
	default:
		spinner.Increment(delta)
	}
	p.previewArea.SetSystemResponse("")
}

func (p *ASDEXPane) activeRangeSetting() int {
	view := p.activeScopeView()
	if view.RangeSetting == 0 {
		return asdexDefaultRangeSetting
	}
	return clampInt(view.RangeSetting, asdexMinRangeSetting, asdexMaxRangeSetting)
}

func (p *ASDEXPane) setRangeSettingForWindow(id ScopeWindowID, rangeSetting int) {
	if p == nil {
		return
	}

	rangeSetting = clampInt(rangeSetting, asdexMinRangeSetting, asdexMaxRangeSetting)
	p.updateScopeViewForWindow(id, func(view *ScopeView) {
		view.RangeSetting = rangeSetting
		view.RangeFullHorizontalFeet = rangeFullHorizontalFeetFromSetting(rangeSetting)
	})
}

func (p *ASDEXPane) setHistoryLengthForWindow(windowID ScopeWindowID, value int) {
	if p == nil {
		return
	}

	state := p.displayStateForWindow(windowID)
	state.HistoryLength = clampInt(value, 1, 7)
}

func (p *ASDEXPane) setVectorLength(value int) {
	if p == nil {
		return
	}

	p.vectorLength = ClampedTargetVectorSeconds(value)
}

func (p *ASDEXPane) setSafetyVolume(value int) {
	if p == nil {
		return
	}

	value = clampInt(value, minAuralVolume, maxAuralVolume)
	p.auralVolume = value
	if p.auralAlerts != nil {
		p.auralAlerts.SetVolume(value)
	}
}

func (p *ASDEXPane) setMainRangeSetting(rangeSetting int) {
	p.setRangeSettingForWindow(mainScopeWindowID, rangeSetting)
}

func (p *ASDEXPane) setActiveRangeSetting(rangeSetting int) {
	if p == nil {
		return
	}
	p.setRangeSettingForWindow(p.activeWindowID(), rangeSetting)
}

func (p *ASDEXPane) dataBlockSettings() DataBlockSettings {
	return p.dataBlockSettingsForWindow(p.activeWindowID())
}

type ActiveDcbWindowState struct {
	WindowID         ScopeWindowID
	View             ScopeView
	DB               DataBlockSettings
	Brightness       WindowBrightnessSettings
	TempDataCharSize int
	ShowHistory      bool
	HistoryLength    int
	ShowVectorLine   bool
}

func (p *ASDEXPane) activeDcbWindowState() ActiveDcbWindowState {
	windowID := p.activeWindowID()

	view, ok := p.scopeViewForWindow(windowID)
	if !ok {
		windowID = mainScopeWindowID
		view = p.mainScopeView()
	}

	state := p.displayStateForWindow(windowID)
	return ActiveDcbWindowState{
		WindowID:         windowID,
		View:             view,
		DB:               p.dataBlockSettingsForWindow(windowID),
		Brightness:       state.Brightness,
		TempDataCharSize: state.TempDataCharSize,
		ShowHistory:      state.ShowHistory,
		HistoryLength:    state.HistoryLength,
		ShowVectorLine:   state.ShowVectorLine,
	}
}

func (p *ASDEXPane) updateActiveDataBlockSettings(
	update func(*DataBlockSettings),
) {
	if p == nil || update == nil {
		return
	}

	windowID := p.activeWindowID()
	settings := p.dataBlockSettingsForWindow(windowID)
	update(&settings)
	p.setDataBlockSettingsForWindow(windowID, settings)
}

func (p *ASDEXPane) activeWindowID() ScopeWindowID {
	if p == nil {
		return mainScopeWindowID
	}
	return p.windows.ActiveWindowID()
}

func (p *ASDEXPane) nextWindowSwitchID() (ScopeWindowID, bool) {
	if p == nil {
		return mainScopeWindowID, false
	}

	secondaryIDs := p.windows.SecondaryWindowIDsSorted()
	if len(secondaryIDs) == 0 {
		return mainScopeWindowID, true
	}

	active := p.activeWindowID()
	if active == mainScopeWindowID {
		return secondaryIDs[0], true
	}

	for i, id := range secondaryIDs {
		if id != active {
			continue
		}
		if i+1 < len(secondaryIDs) {
			return secondaryIDs[i+1], true
		}
		return mainScopeWindowID, true
	}

	return secondaryIDs[0], true
}

func (p *ASDEXPane) noScopeModalCommandActive() bool {
	if p == nil {
		return false
	}

	return p.commandEntry.Empty() &&
		p.datablockEdit == nil &&
		p.initControlEntry == nil &&
		p.termControlEntry == nil &&
		p.multiFunction == nil &&
		p.scratchpadEntry == nil &&
		p.previewReposition == nil &&
		p.coastListReposition == nil &&
		p.mapReposition == nil &&
		p.mapRotate == nil &&
		p.runwayConfigCommand == nil &&
		p.towerReadout == nil &&
		p.dcbSpinner == nil &&
		p.dcbMenuCommand == nil &&
		p.dbAreaDraft == nil &&
		p.dbAreaSelection == nil &&
		p.tempAreaDraft == nil &&
		p.tempTextCommand == nil &&
		p.tempTextPlacement == nil &&
		p.tempDataSelectMode == TempDataSelectNone &&
		p.newWindow == nil &&
		p.deleteWindow == nil &&
		p.windowReposition == nil &&
		p.resizeWindow == nil
}

func (p *ASDEXPane) displayStateForWindow(id ScopeWindowID) *WindowDisplayState {
	if p == nil {
		return NewWindowDisplayState()
	}
	if p.displayStateByWindow == nil {
		p.displayStateByWindow = make(map[ScopeWindowID]*WindowDisplayState)
	}
	state := p.displayStateByWindow[id]
	if state == nil {
		state = NewWindowDisplayState()
		p.displayStateByWindow[id] = state
	}
	return state
}

func (p *ASDEXPane) dataBlockSettingsForWindow(id ScopeWindowID) DataBlockSettings {
	if p == nil {
		settings := DefaultDataBlockSettings()
		settings.TimesharePrimary = true
		return settings
	}

	settings := p.displayStateForWindow(id).DB
	settings.TimesharePrimary = p.timesharePrimary(time.Now())
	return settings
}

func (p *ASDEXPane) setDataBlockSettingsForWindow(id ScopeWindowID, settings DataBlockSettings) {
	if p == nil {
		return
	}
	p.displayStateForWindow(id).DB = settings
}

func (p *ASDEXPane) targetVectorVisibleForWindow(
	windowID ScopeWindowID,
	target *Target,
) bool {
	if p == nil || target == nil {
		return false
	}

	if !targetCanHaveDataBlock(target) {
		return false
	}
	if target.Suspended || target.Coasting || target.Dropped {
		return false
	}

	state := p.displayStateForWindow(windowID)
	if area, ok := p.dataBlockTraitAreaForPoint(windowID, target.PosFeet); ok {
		return area.Traits.ShowVector
	}

	return state.ShowVectorLine
}

func (p *ASDEXPane) targetShowDBOverride(
	windowID ScopeWindowID,
	targetID string,
) (bool, bool) {
	if p == nil {
		return false, false
	}

	state := p.displayStateForWindow(windowID)
	value, ok := state.TargetShowDBOverrides[targetID]
	return value, ok
}

func (p *ASDEXPane) setTargetShowDBOverride(
	windowID ScopeWindowID,
	targetID string,
	value bool,
) {
	if p == nil || targetID == "" {
		return
	}

	state := p.displayStateForWindow(windowID)
	if state.TargetShowDBOverrides == nil {
		state.TargetShowDBOverrides = make(map[string]bool)
	}
	state.TargetShowDBOverrides[targetID] = value
}

func (p *ASDEXPane) clearTargetShowDBOverrides(windowID ScopeWindowID) {
	if p == nil || p.displayStateByWindow == nil {
		return
	}
	if state := p.displayStateByWindow[windowID]; state != nil {
		state.TargetShowDBOverrides = nil
	}
}

func (p *ASDEXPane) targetDBOffAreaOverride(
	windowID ScopeWindowID,
	targetID string,
) (bool, bool) {
	if p == nil || targetID == "" {
		return false, false
	}

	state := p.displayStateForWindow(windowID)
	if state.TargetDBOffAreaOverrides == nil {
		return false, false
	}

	value, ok := state.TargetDBOffAreaOverrides[targetID]
	return value, ok
}

func (p *ASDEXPane) setTargetDBOffAreaOverride(
	windowID ScopeWindowID,
	targetID string,
	value bool,
) {
	if p == nil || targetID == "" {
		return
	}

	state := p.displayStateForWindow(windowID)
	if state.TargetDBOffAreaOverrides == nil {
		state.TargetDBOffAreaOverrides = make(map[string]bool)
	}
	state.TargetDBOffAreaOverrides[targetID] = value
}

func (p *ASDEXPane) targetShowsDataBlockInWindow(
	target *Target,
	windowID ScopeWindowID,
	settings DataBlockSettings,
) bool {
	if target == nil || target.Suspended || target.Dropped || !targetCanHaveDataBlock(target) {
		return false
	}

	if settings.DataBlocksOff {
		if override, ok := p.targetDBOffAreaOverride(windowID, target.ID); ok {
			return override
		}
		return false
	}

	if override, ok := p.targetShowDBOverride(windowID, target.ID); ok {
		return override
	}

	if !target.ShowDB {
		return false
	}

	return settings.ShowDataBlocks
}

func (p *ASDEXPane) applyManualLeaderOverrides(
	settings DataBlockSettings,
	windowID ScopeWindowID,
	targetID string,
) DataBlockSettings {
	if p == nil || targetID == "" {
		return settings
	}

	if direction, ok := p.leaderDirectionOverride(windowID, targetID); ok {
		settings.LeaderDirection = direction
	}
	if length, ok := p.leaderLengthOverride(windowID, targetID); ok {
		settings.LeaderLength = length
	}

	return settings
}

func (p *ASDEXPane) applyTraitAreaManualLeaderOverrides(
	settings DataBlockSettings,
	windowID ScopeWindowID,
	targetID string,
) DataBlockSettings {
	if p == nil || targetID == "" {
		return settings
	}

	if direction, ok := p.traitLeaderDirectionOverride(windowID, targetID); ok {
		settings.LeaderDirection = direction
	}
	if length, ok := p.traitLeaderLengthOverride(windowID, targetID); ok {
		settings.LeaderLength = length
	}

	return settings
}

func (p *ASDEXPane) resolveDataBlockSettings(
	target *Target,
	windowID ScopeWindowID,
	alertInProgress bool,
	targetInAlert bool,
) DataBlockSettings {
	settings := p.dataBlockSettingsForWindow(windowID)
	fields := p.dbFieldSettings
	settings.ShowAltitude = fields.ShowAltitude
	settings.ShowTargetType = fields.ShowTargetType
	settings.ShowSensors = fields.ShowSensors
	settings.ShowCWT = fields.ShowCWT
	settings.ShowFix = fields.ShowFix
	settings.ShowVelocity = fields.ShowVelocity
	settings.ShowScratchpads = fields.ShowScratchpads

	if target != nil {
		// Datablock setting priority follows CRC: active window defaults,
		// global DB field toggles, regular per-target leader overrides, DB
		// area traits, then manual overrides made while already inside a DB
		// TRAIT AREA.
		settings = p.applyManualLeaderOverrides(settings, windowID, target.ID)

		area, hasArea := p.dataBlockAreaForPoint(windowID, target.PosFeet)
		traitArea, hasTraitArea := p.dataBlockTraitAreaForPoint(windowID, target.PosFeet)

		currentTraitAreaID := ""
		if hasTraitArea {
			currentTraitAreaID = traitArea.ID
		}
		p.syncTargetTraitAreaContext(windowID, target.ID, currentTraitAreaID)

		if hasArea {
			settings = applyDataBlockAreaTraits(settings, area.Traits)
		}
		if hasTraitArea {
			settings = p.applyTraitAreaManualLeaderOverrides(settings, windowID, target.ID)
		}
	}

	settings.AlertInProgress = alertInProgress
	settings.TargetInAlert = targetInAlert
	return settings
}

func (p *ASDEXPane) targetShowsDataBlockForRender(
	target *Target,
	windowID ScopeWindowID,
	settings DataBlockSettings,
) bool {
	if target == nil || target.Suspended || target.Dropped || !targetCanHaveDataBlock(target) {
		return false
	}

	// CRC bypasses normal datablock visibility suppression while any ASDE-X
	// alert is active.
	if settings.AlertInProgress {
		return true
	}

	return p.targetShowsDataBlockInWindow(target, windowID, settings)
}

func (p *ASDEXPane) syncTargetTraitAreaContext(
	windowID ScopeWindowID,
	targetID string,
	areaID string,
) {
	if p == nil || targetID == "" {
		return
	}

	state := p.displayStateForWindow(windowID)
	previous := ""
	if state.TargetTraitAreaByTarget != nil {
		previous = state.TargetTraitAreaByTarget[targetID]
	}
	if previous == areaID {
		return
	}

	if state.TraitLeaderDirectionOverrides != nil {
		delete(state.TraitLeaderDirectionOverrides, targetID)
	}
	if state.TraitLeaderLengthOverrides != nil {
		delete(state.TraitLeaderLengthOverrides, targetID)
	}

	if areaID == "" {
		if state.TargetTraitAreaByTarget != nil {
			delete(state.TargetTraitAreaByTarget, targetID)
		}
		return
	}

	if state.TargetTraitAreaByTarget == nil {
		state.TargetTraitAreaByTarget = make(map[string]string)
	}
	state.TargetTraitAreaByTarget[targetID] = areaID
}

func (p *ASDEXPane) clearTraitLeaderOverridesForArea(
	windowID ScopeWindowID,
	areaID string,
) {
	if p == nil || areaID == "" {
		return
	}

	state := p.displayStateForWindow(windowID)
	if state.TargetTraitAreaByTarget == nil {
		return
	}

	for targetID, currentAreaID := range state.TargetTraitAreaByTarget {
		if currentAreaID != areaID {
			continue
		}
		if state.TraitLeaderDirectionOverrides != nil {
			delete(state.TraitLeaderDirectionOverrides, targetID)
		}
		if state.TraitLeaderLengthOverrides != nil {
			delete(state.TraitLeaderLengthOverrides, targetID)
		}
	}
}

func (p *ASDEXPane) traitLeaderDirectionOverride(
	windowID ScopeWindowID,
	targetID string,
) (LeaderDirection, bool) {
	if p == nil || targetID == "" {
		return LeaderNE, false
	}

	state := p.displayStateForWindow(windowID)
	value, ok := state.TraitLeaderDirectionOverrides[targetID]
	return value, ok
}

func (p *ASDEXPane) setTraitLeaderDirectionOverride(
	windowID ScopeWindowID,
	targetID string,
	value LeaderDirection,
) {
	if p == nil || targetID == "" {
		return
	}

	state := p.displayStateForWindow(windowID)
	if state.TraitLeaderDirectionOverrides == nil {
		state.TraitLeaderDirectionOverrides = make(map[string]LeaderDirection)
	}
	state.TraitLeaderDirectionOverrides[targetID] = value
}

func (p *ASDEXPane) traitLeaderLengthOverride(
	windowID ScopeWindowID,
	targetID string,
) (int, bool) {
	if p == nil || targetID == "" {
		return 0, false
	}

	state := p.displayStateForWindow(windowID)
	value, ok := state.TraitLeaderLengthOverrides[targetID]
	return value, ok
}

func (p *ASDEXPane) setTraitLeaderLengthOverride(
	windowID ScopeWindowID,
	targetID string,
	value int,
) {
	if p == nil || targetID == "" {
		return
	}

	state := p.displayStateForWindow(windowID)
	if state.TraitLeaderLengthOverrides == nil {
		state.TraitLeaderLengthOverrides = make(map[string]int)
	}
	state.TraitLeaderLengthOverrides[targetID] = value
}

func (p *ASDEXPane) setTargetLeaderDirectionManualOverride(
	windowID ScopeWindowID,
	target *Target,
	value LeaderDirection,
) {
	if p == nil || target == nil || target.ID == "" {
		return
	}

	if area, ok := p.dataBlockTraitAreaForPoint(windowID, target.PosFeet); ok {
		p.syncTargetTraitAreaContext(windowID, target.ID, area.ID)
		p.setTraitLeaderDirectionOverride(windowID, target.ID, value)
		return
	}

	p.syncTargetTraitAreaContext(windowID, target.ID, "")
	p.setLeaderDirectionOverride(windowID, target.ID, value)
}

func (p *ASDEXPane) setTargetLeaderLengthManualOverride(
	windowID ScopeWindowID,
	target *Target,
	value int,
) {
	if p == nil || target == nil || target.ID == "" {
		return
	}

	if area, ok := p.dataBlockTraitAreaForPoint(windowID, target.PosFeet); ok {
		p.syncTargetTraitAreaContext(windowID, target.ID, area.ID)
		p.setTraitLeaderLengthOverride(windowID, target.ID, value)
		return
	}

	p.syncTargetTraitAreaContext(windowID, target.ID, "")
	p.setLeaderLengthOverride(windowID, target.ID, value)
}

func (p *ASDEXPane) leaderDirectionOverride(
	windowID ScopeWindowID,
	targetID string,
) (LeaderDirection, bool) {
	if p == nil {
		return LeaderNE, false
	}
	state := p.displayStateForWindow(windowID)
	value, ok := state.LeaderDirectionOverrides[targetID]
	return value, ok
}

func (p *ASDEXPane) setLeaderDirectionOverride(
	windowID ScopeWindowID,
	targetID string,
	value LeaderDirection,
) {
	if p == nil || targetID == "" {
		return
	}
	state := p.displayStateForWindow(windowID)
	if state.LeaderDirectionOverrides == nil {
		state.LeaderDirectionOverrides = make(map[string]LeaderDirection)
	}
	state.LeaderDirectionOverrides[targetID] = value
}

func (p *ASDEXPane) clearLeaderDirectionOverrides(windowID ScopeWindowID) {
	if p == nil || p.displayStateByWindow == nil {
		return
	}
	if state := p.displayStateByWindow[windowID]; state != nil {
		state.LeaderDirectionOverrides = nil
	}
}

func (p *ASDEXPane) leaderLengthOverride(
	windowID ScopeWindowID,
	targetID string,
) (int, bool) {
	if p == nil {
		return 0, false
	}
	state := p.displayStateForWindow(windowID)
	value, ok := state.LeaderLengthOverrides[targetID]
	return value, ok
}

func (p *ASDEXPane) setLeaderLengthOverride(
	windowID ScopeWindowID,
	targetID string,
	value int,
) {
	if p == nil || targetID == "" {
		return
	}
	state := p.displayStateForWindow(windowID)
	if state.LeaderLengthOverrides == nil {
		state.LeaderLengthOverrides = make(map[string]int)
	}
	state.LeaderLengthOverrides[targetID] = value
}

func (p *ASDEXPane) clearLeaderLengthOverrides(windowID ScopeWindowID) {
	if p == nil || p.displayStateByWindow == nil {
		return
	}
	if state := p.displayStateByWindow[windowID]; state != nil {
		state.LeaderLengthOverrides = nil
	}
}

func (p *ASDEXPane) timesharePrimary(now time.Time) bool {
	if p == nil {
		return true
	}
	if p.datablockTimeshareStart.IsZero() {
		p.datablockTimeshareStart = now
	}

	const interval = 2 * time.Second
	elapsed := now.Sub(p.datablockTimeshareStart)
	if elapsed < 0 {
		elapsed = 0
	}
	return int(elapsed/interval)%2 == 0
}

func loadConfigAirportCode(airport string) string {
	airport = strings.ToUpper(strings.TrimSpace(airport))
	if airport == "" {
		return ""
	}

	fallback := strings.TrimPrefix(airport, "K")
	path := "resources/configs/asdex/" + airport + ".json"
	if !util.ResourceExists(path) {
		return fallback
	}

	var cfg struct {
		Airport string `json:"airport"`
	}
	if err := json.Unmarshal(util.LoadResourceBytes(path), &cfg); err != nil {
		return fallback
	}

	code := strings.ToUpper(strings.TrimSpace(cfg.Airport))
	if code != "" {
		return code
	}
	return fallback
}

func (p *ASDEXPane) isDestinationCurrentAirport(target *Target) bool {
	if p == nil || target == nil {
		return false
	}

	fix := strings.ToUpper(strings.TrimSpace(target.Fix))
	if fix == "" {
		return false
	}

	configAirport := strings.ToUpper(strings.TrimSpace(p.configAirportCode))
	airport := strings.ToUpper(strings.TrimSpace(p.airport))
	airportNoK := airport
	if len(airportNoK) == 4 && strings.HasPrefix(airportNoK, "K") {
		airportNoK = airportNoK[1:]
	}

	return (configAirport != "" && fix == configAirport) ||
		fix == airportNoK ||
		fix == airport
}

func (p *ASDEXPane) ensureCursorsLoaded(ctx *panes.Context) {
	if p == nil || ctx == nil || p.cursors.loaded {
		return
	}
	if err := p.cursors.Load(); err != nil {
		if p.logger != nil {
			p.logger.Warn(
				"Unable to load cursors",
				slog.Any("error", err),
			)
		} else {
			slog.Warn(
				"Unable to load cursors",
				slog.Any("error", err),
			)
		}
	}
}

func (p *ASDEXPane) applyCurrentCursor(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Platform == nil {
		return
	}

	p.cursorMode = CursorModeHidden
	if ctx.Mouse == nil {
		ctx.Platform.ClearCursorOverride()
		return
	}

	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	if !paneLocal.Contains(ctx.Mouse.Pos) {
		ctx.Platform.ClearCursorOverride()
		return
	}

	p.cursorMode = p.resolveCursorMode(ctx)
	ctx.Platform.SetCursorHiddenOverride()
}

func (p *ASDEXPane) resolveCursorMode(ctx *panes.Context) CursorMode {
	if p != nil && p.datablockEdit != nil {
		return CursorModeHidden
	}
	if p != nil && p.mapReposition != nil {
		return CursorModeHidden
	}
	if p != nil && p.mapRotate != nil {
		return CursorModeHidden
	}
	if p != nil && p.listRepositionActive() {
		return CursorModeMove
	}
	if p != nil && p.dcbSpinner != nil {
		return CursorModeHidden
	}
	if p != nil && p.tempTextCommand != nil {
		return CursorModeHidden
	}
	if p != nil && p.tempTextPlacement != nil {
		return CursorModeScope
	}
	if p != nil && p.tempAreaDraft != nil {
		return CursorModeScope
	}
	if p != nil && p.dbAreaDraft != nil {
		return CursorModeScope
	}
	if p != nil && p.dbAreaSelection != nil {
		if p.dcbCursorUnlocked() && p.mouseOverDcb(ctx) {
			if p.dcbMouseCaptured() {
				return CursorModeCaptured
			}
			return CursorModeDcb
		}
		if p.dbAreaSelection.HoveredID != "" {
			return CursorModeSelect
		}
		return CursorModeScope
	}
	if p != nil && p.newWindow != nil {
		return CursorModeScope
	}
	if p != nil && p.deleteWindow != nil {
		return CursorModeScope
	}
	if p != nil && p.windowReposition != nil {
		return CursorModeMove
	}
	if p != nil && p.resizeWindow != nil {
		return p.resizeWindowCursorMode(ctx)
	}
	if p != nil && p.towerReadout != nil {
		return CursorModeScope
	}
	if p != nil && p.tempDataSelectMode != TempDataSelectNone {
		if p.hoveredTempData.Type != TempDataHitNone {
			return CursorModeSelect
		}
		return CursorModeScope
	}
	if ctx != nil && ctx.Mouse != nil && ctx.Mouse.IsDown(platform.MouseButtonRight) {
		return CursorModeHidden
	}
	if p != nil && p.dcbCursorUnlocked() && p.mouseOverDcb(ctx) {
		if p.dcbMouseCaptured() {
			return CursorModeCaptured
		}
		return CursorModeDcb
	}
	if p != nil && p.showCoastList && ctx != nil && ctx.Mouse != nil {
		hit := p.coastList.HitTest(ctx.Mouse.Pos, p.fonts.font, p.eramTextFonts.font, ctx.PaneSize())
		if hit.Type == CoastListHitEntry &&
			(hit.Status == CoastListEntrySuspended ||
				p.commandMode == CommandModeTerminateControl) {
			return CursorModeSelect
		}
	}
	return CursorModeScope
}

func (p *ASDEXPane) resizeWindowCursorMode(ctx *panes.Context) CursorMode {
	if p == nil || p.resizeWindow == nil || ctx == nil || ctx.Mouse == nil {
		return CursorModeScope
	}

	cmd := p.resizeWindow
	if cmd.HasOperation {
		return cursorModeForResizeOperation(cmd.Operation)
	}

	rect, ok := p.scopeWindowRectForWindow(cmd.WindowID, ctx.PaneSize())
	if !ok {
		return CursorModeScope
	}

	op, ok := resizeOperationAtPoint(ctx.Mouse.Pos, rect)
	if !ok {
		return CursorModeScope
	}
	return cursorModeForResizeOperation(op)
}

func (p *ASDEXPane) updateHighlightedTarget(
	ctx *panes.Context,
	transforms radar.ScopeTransformations,
) {
	if ctx == nil {
		p.clearHighlightedTarget()
		return
	}
	p.updateHighlightedTargetInWindow(
		ctx,
		mainScopeWindowID,
		redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height()),
		transforms,
	)
}

func (p *ASDEXPane) updateHighlightedTargetInWindow(
	ctx *panes.Context,
	windowID ScopeWindowID,
	windowRect redsmath.Rect,
	transforms radar.ScopeTransformations,
) {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		p.clearHighlightedTarget()
		return
	}

	if !windowRect.Contains(ctx.Mouse.Pos) {
		p.clearHighlightedTarget()
		return
	}

	mouseWorld := transforms.WorldFromWindowP(ctx.Mouse.Pos.Sub(windowRect.Min))
	storeRevision := p.targets.HoverRevision()
	if p.hover.Valid &&
		p.hover.WindowID == windowID &&
		p.hover.MouseWorld == mouseWorld &&
		p.hover.Revision == storeRevision {
		return
	}

	p.hover.TargetID = p.targets.NearestTargetID(mouseWorld)
	p.hover.WindowID = windowID
	p.hover.MouseWorld = mouseWorld
	p.hover.Revision = storeRevision
	p.hover.Valid = true
}

func (p *ASDEXPane) clearHighlightedTarget() {
	if p == nil {
		return
	}

	if !p.hover.Valid && p.hover.TargetID == "" {
		return
	}

	p.hover = ScopeHoverState{}
}

func (p *ASDEXPane) highlightedTarget() *Target {
	if p == nil {
		return nil
	}
	return p.targets.TargetByID(p.hover.TargetID)
}

func (p *ASDEXPane) updateTowerReadout(
	ctx *panes.Context,
	referenceExtent redsmath.Rect,
	rangeVisibleScale float32,
) {
	if p == nil || p.towerReadout == nil || ctx == nil || ctx.Mouse == nil {
		return
	}

	mouse := ctx.Mouse.Pos
	windowID, windowRect, view, ok := p.scopeWindowAtPoint(mouse, ctx.PaneSize())
	if !ok {
		windowID = mainScopeWindowID
		windowRect = redsmath.RectFromSize(ctx.PaneSize().X, ctx.PaneSize().Y)
		view = p.mainScopeView()
	}

	transforms := scopeTransformForWindow(
		windowRect,
		referenceExtent,
		view,
		rangeVisibleScale,
	)

	localMouse := mouse
	if windowID != mainScopeWindowID {
		localMouse = mouse.Sub(windowRect.Min)
	}

	cursorFeet := transforms.WorldFromWindowP(localMouse)
	x, y := towerReadoutValues(
		cursorFeet,
		p.towerReadout.Tower.Feet,
		view.Rotation,
	)
	p.towerReadout.SetValues(x, y)
}

func (p *ASDEXPane) activeCommandLines() []string {
	if p == nil {
		return nil
	}
	if p.datablockEdit != nil {
		return p.datablockEdit.DisplayLines()
	}
	if p.initControlEntry != nil {
		return p.initControlEntry.DisplayLines()
	}
	if p.termControlEntry != nil {
		return p.termControlEntry.DisplayLines()
	}
	if p.scratchpadEntry != nil {
		return p.scratchpadEntry.DisplayLines()
	}
	if p.multiFunction != nil {
		return p.multiFunction.DisplayLines()
	}
	if p.previewReposition != nil {
		return p.previewReposition.DisplayLines()
	}
	if p.coastListReposition != nil {
		return p.coastListReposition.DisplayLines()
	}
	if p.mapReposition != nil {
		return p.mapReposition.DisplayLines()
	}
	if p.mapRotate != nil {
		return p.mapRotate.DisplayLines()
	}
	if p.runwayConfigCommand != nil {
		return p.runwayConfigCommand.DisplayLines()
	}
	if p.towerReadout != nil {
		return p.towerReadout.DisplayLines()
	}
	if p.dcbSpinner != nil {
		return p.dcbSpinner.DisplayLines()
	}
	if p.newWindow != nil {
		return p.newWindow.DisplayLines()
	}
	if p.deleteWindow != nil {
		return p.deleteWindow.DisplayLines()
	}
	if p.windowReposition != nil {
		return p.windowReposition.DisplayLines()
	}
	if p.resizeWindow != nil {
		return p.resizeWindow.DisplayLines()
	}
	if p.tempTextCommand != nil {
		return p.tempTextCommand.DisplayLines()
	}
	if p.tempTextPlacement != nil {
		return p.tempTextPlacement.DisplayLines()
	}
	if p.dcbMenuCommand != nil {
		return p.dcbMenuCommand.DisplayLines()
	}
	if p.commandMode == CommandModeTrackSuspend {
		return []string{"TRK SUSP"}
	}
	if p.commandMode == CommandModeTrackAlertInhibit {
		return []string{"SAFETY LOGIC", "TRACK ALERT INHIB"}
	}
	if !p.commandEntry.Empty() {
		return p.commandEntry.DisplayLines()
	}
	return nil
}

func (p *ASDEXPane) activeCommandCursor() (line int, column int, ok bool) {
	if p == nil {
		return 0, 0, false
	}
	if p.datablockEdit != nil {
		return p.datablockEdit.CursorLine(), p.datablockEdit.CursorColumn(), true
	}
	if p.initControlEntry != nil {
		return p.initControlEntry.CursorLine(), p.initControlEntry.CursorColumn(), true
	}
	if p.termControlEntry != nil {
		return p.termControlEntry.CursorLine(), p.termControlEntry.CursorColumn(), true
	}
	if p.scratchpadEntry != nil {
		return p.scratchpadEntry.CursorLine(), p.scratchpadEntry.CursorColumn(), true
	}
	if p.multiFunction != nil {
		return p.multiFunction.CursorLine(), p.multiFunction.CursorColumn(), true
	}
	if p.mapRotate != nil {
		return p.mapRotate.CursorLine(), p.mapRotate.CursorColumn(), true
	}
	if p.runwayConfigCommand != nil {
		return p.runwayConfigCommand.CursorLine(), p.runwayConfigCommand.CursorColumn(), true
	}
	if p.dcbSpinner != nil {
		return p.dcbSpinner.CursorLine(), p.dcbSpinner.CursorColumn(), true
	}
	if p.tempTextCommand != nil {
		return p.tempTextCommand.CursorLine(), p.tempTextCommand.CursorColumn(), true
	}
	if !p.commandEntry.Empty() {
		return p.commandEntry.CursorLine(), p.commandEntry.CursorColumn(), true
	}
	return 0, 0, false
}

func (p *ASDEXPane) cancelDatablockEdit() {
	p.cancelActiveCommand()
}

func (p *ASDEXPane) cancelActiveCommand() {
	if p == nil {
		return
	}
	if p.mapRotate != nil {
		windowID := p.mapRotate.WindowID
		originalRotation := p.mapRotate.originalRotation
		p.updateScopeViewForWindow(windowID, func(view *ScopeView) {
			view.Rotation = originalRotation
		})
		p.finishMapRotateCommand("")
		return
	}
	if p.runwayConfigCommand != nil {
		p.finishRunwayConfigCommand("")
		return
	}
	if p.commandMode == CommandModeTrackAlertInhibit {
		p.finishTrackAlertInhibitCommand("")
		return
	}
	if p.mapReposition != nil && p.mapReposition.initialized {
		windowID := p.mapReposition.WindowID
		originalCenter := p.mapReposition.originalCenter
		p.updateScopeViewForWindow(windowID, func(view *ScopeView) {
			view.Center = originalCenter
		})
	}
	p.commandMode = CommandModeNone
	p.datablockEdit = nil
	p.editingTargetID = ""
	p.initControlEntry = nil
	p.termControlEntry = nil
	p.multiFunction = nil
	p.scratchpadEntry = nil
	p.previewReposition = nil
	p.coastListReposition = nil
	p.mapReposition = nil
	p.mapRotate = nil
	p.runwayConfigCommand = nil
	p.towerReadout = nil
	p.dcbSpinner = nil
	p.dcbMenuCommand = nil
	p.clearTrackAlertInhibitReturnContext()
	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.tempData.ClearHighlights()
	p.newWindow = nil
	p.deleteWindow = nil
	p.windowReposition = nil
	p.resizeWindow = nil
	p.dcb.ReturnToMainMenu()
	p.commandEntry.Clear()
	p.previewArea.SetSystemResponse("")
}

func (p *ASDEXPane) consumeCommandKeyboard(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}
	if p.datablockEdit != nil {
		return p.handleDatablockEditKeyboard(ctx)
	}
	if p.initControlEntry != nil {
		return p.handleInitControlKeyboard(ctx)
	}
	if p.termControlEntry != nil {
		return p.handleTerminateControlKeyboard(ctx)
	}
	if p.scratchpadEntry != nil {
		return p.handleScratchpadEntryKeyboard(ctx)
	}
	if p.multiFunction != nil {
		return p.handleMultiFunctionKeyboard(ctx)
	}
	if p.mapRotate != nil {
		return p.handleMapRotateKeyboard(ctx)
	}
	if p.runwayConfigCommand != nil {
		return p.handleRunwayConfigKeyboard(ctx)
	}
	if p.towerReadout != nil {
		return p.handleTowerReadoutKeyboard(ctx)
	}
	if p.dcbSpinner != nil {
		return p.handleDcbSpinnerKeyboard(ctx)
	}
	if p.tempTextCommand != nil {
		return p.handleTempTextKeyboard(ctx)
	}
	if p.tempTextPlacement != nil {
		return p.handleTempTextPlacementKeyboard(ctx)
	}
	if p.newWindow != nil {
		return p.handleNewWindowKeyboard(ctx)
	}
	if p.deleteWindow != nil {
		return p.handleDeleteWindowKeyboard(ctx)
	}
	if p.windowReposition != nil {
		return p.handleWindowRepositionKeyboard(ctx)
	}
	if p.resizeWindow != nil {
		return p.handleResizeWindowKeyboard(ctx)
	}
	if p.dbAreaDraft != nil {
		return p.handleDataBlockAreaDraftKeyboard(ctx)
	}
	if p.dbAreaSelection != nil {
		return p.handleDataBlockAreaSelectionKeyboard(ctx)
	}
	if p.consumeTempDataSelectionKeyboard(ctx) {
		return true
	}
	if p.dcbMenuCommand != nil {
		return p.handleDcbMenuKeyboard(ctx)
	}
	if p.commandMode != CommandModeNone {
		keyboard := ctx.Keyboard
		if keyboard.WasPressed(platform.KeyEscape) ||
			keyboard.WasPressed(platform.KeyBackspace) ||
			keyboard.WasPressed(platform.KeyDelete) {
			p.cancelActiveCommand()
			return true
		}
	}
	if towerReadoutShortcutPressed(ctx) {
		return false
	}
	if p.commandMode == CommandModeNone {
		return p.handleNormalCommandKeyboard(ctx)
	}
	return false
}

func (p *ASDEXPane) handleTowerReadoutKeyboard(ctx *panes.Context) bool {
	if p == nil || p.towerReadout == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	if keyboard.WasPressed(platform.KeyEscape) ||
		keyboard.WasPressed(platform.KeyBackspace) {
		p.towerReadout = nil
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	}
	return false
}

func (p *ASDEXPane) handleRunwayConfigKeyboard(ctx *panes.Context) bool {
	if p == nil || p.runwayConfigCommand == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	command := p.runwayConfigCommand
	switch {
	case keyboard.WasPressed(platform.KeyEscape):
		p.finishRunwayConfigCommand("")
		return true
	case keyboard.WasPressed(platform.KeyBackspace):
		if command.Backspace() {
			p.finishRunwayConfigCommand("")
		} else {
			p.previewArea.SetSystemResponse("")
		}
		return true
	case keyboard.WasPressed(platform.KeyDelete):
		command.DeleteForward()
		p.previewArea.SetSystemResponse("")
		return true
	case keyboard.WasPressed(platform.KeyLeft):
		command.MoveLeft()
		return true
	case keyboard.WasPressed(platform.KeyRight):
		command.MoveRight()
		return true
	case keyboard.WasPressed(platform.KeyEnter), keyboard.WasPressed(platform.KeyKeypadEnter):
		p.submitRunwayConfigCommand()
		return true
	}

	handled := false
	for _, r := range keyboard.Text {
		command.Insert(r)
		p.previewArea.SetSystemResponse("")
		handled = true
	}
	return handled
}

func (p *ASDEXPane) submitRunwayConfigCommand() {
	if p == nil || p.runwayConfigCommand == nil {
		return
	}

	number, err := strconv.Atoi(p.runwayConfigCommand.Value())
	if err != nil {
		p.finishRunwayConfigCommand("INVALID CONFIG")
		return
	}

	p.finishRunwayConfigCommand(
		p.setRunwayConfigurationByOrdinal(number, true),
	)
}

func (p *ASDEXPane) finishRunwayConfigCommand(response string) {
	if p == nil {
		return
	}

	p.runwayConfigCommand = nil
	p.commandMode = CommandModeNone
	p.commandEntry.Clear()
	p.dcb.ReturnToMainMenu()
	p.dcbMenuCommand = nil
	p.previewArea.SetSystemResponse(response)
	p.refreshRunwayConfigPreviewLine()
	p.refreshTowerConfigPreviewLine()
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) handleDatablockEditKeyboard(ctx *panes.Context) bool {
	if p == nil || p.datablockEdit == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	edit := p.datablockEdit
	switch {
	case keyboard.WasPressed(platform.KeyEscape):
		p.cancelDatablockEdit()
		return true
	case keyboard.WasPressed(platform.KeyEnter), keyboard.WasPressed(platform.KeyKeypadEnter):
		if edit.Enter() {
			p.submitDatablockEdit()
		}
		return true
	case keyboard.WasPressed(platform.KeyLeft):
		edit.MoveLeft()
		return true
	case keyboard.WasPressed(platform.KeyRight):
		edit.MoveRight()
		return true
	case keyboard.WasPressed(platform.KeyUp):
		edit.MoveUp()
		return true
	case keyboard.WasPressed(platform.KeyDown):
		edit.MoveDown()
		return true
	case keyboard.WasPressed(platform.KeyBackspace):
		edit.Backspace()
		return true
	case keyboard.WasPressed(platform.KeyDelete):
		edit.DeleteForward()
		return true
	}

	handled := false
	for _, r := range keyboard.Text {
		edit.Insert(r)
		handled = true
	}
	return handled
}

func (p *ASDEXPane) handleInitControlKeyboard(ctx *panes.Context) bool {
	if p == nil || p.initControlEntry == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	entry := p.initControlEntry
	switch {
	case keyboard.WasPressed(platform.KeyEscape):
		p.cancelActiveCommand()
		return true
	case keyboard.WasPressed(platform.KeyEnter), keyboard.WasPressed(platform.KeyKeypadEnter):
		p.submitInitControlEntry()
		return true
	case keyboard.WasPressed(platform.KeyLeft):
		entry.MoveLeft()
		return true
	case keyboard.WasPressed(platform.KeyRight):
		entry.MoveRight()
		return true
	case keyboard.WasPressed(platform.KeyBackspace):
		entry.Backspace()
		return true
	case keyboard.WasPressed(platform.KeyDelete):
		entry.DeleteForward()
		return true
	}

	handled := false
	for _, r := range keyboard.Text {
		entry.Insert(r)
		p.previewArea.SetSystemResponse("")
		handled = true
	}
	return handled
}

func (p *ASDEXPane) handleTerminateControlKeyboard(ctx *panes.Context) bool {
	if p == nil || p.termControlEntry == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	entry := p.termControlEntry
	switch {
	case keyboard.WasPressed(platform.KeyEscape):
		p.cancelActiveCommand()
		return true
	case keyboard.WasPressed(platform.KeyEnter), keyboard.WasPressed(platform.KeyKeypadEnter):
		p.submitTerminateControlEntry()
		return true
	case keyboard.WasPressed(platform.KeyLeft):
		entry.MoveLeft()
		return true
	case keyboard.WasPressed(platform.KeyRight):
		entry.MoveRight()
		return true
	case keyboard.WasPressed(platform.KeyBackspace):
		entry.Backspace()
		return true
	case keyboard.WasPressed(platform.KeyDelete):
		entry.DeleteForward()
		return true
	}

	handled := false
	for _, r := range keyboard.Text {
		entry.Insert(r)
		p.previewArea.SetSystemResponse("")
		handled = true
	}
	return handled
}

func (p *ASDEXPane) handleMultiFunctionKeyboard(ctx *panes.Context) bool {
	if p == nil || p.multiFunction == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	switch {
	case keyboard.WasPressed(platform.KeyEscape):
		p.cancelActiveCommand()
		return true
	case keyboard.WasPressed(platform.KeyBackspace), keyboard.WasPressed(platform.KeyDelete):
		if p.multiFunction.Value() == "" {
			p.cancelActiveCommand()
			return true
		}
		p.multiFunction.Clear()
		return true
	case keyboard.WasPressed(platform.KeyEnter), keyboard.WasPressed(platform.KeyKeypadEnter):
		if p.multiFunction.Value() == "B" {
			return true
		}
		if p.tryExecuteMultiFunctionValue(ctx) {
			return true
		}
		p.multiFunction = nil
		p.applyCommandStatus(commandOutputClearAll("INVALID ENTRY"))
		return true
	}

	for _, r := range keyboard.Text {
		r = unicode.ToUpper(r)
		if p.multiFunction.Value() == "" {
			switch r {
			case 'P':
				p.startMultiPreviewReposition()
				return true
			case 'C':
				p.startMultiCoastListReposition()
				return true
			}
		}

		p.multiFunction.Insert(r)
		p.previewArea.SetSystemResponse("")
	}

	return len(keyboard.Text) > 0
}

func (p *ASDEXPane) handleScratchpadEntryKeyboard(ctx *panes.Context) bool {
	if p == nil || p.scratchpadEntry == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	switch {
	case keyboard.WasPressed(platform.KeyEscape):
		p.cancelActiveCommand()
		return true
	case keyboard.WasPressed(platform.KeyEnter), keyboard.WasPressed(platform.KeyKeypadEnter):
		p.submitScratchpadEntryCommand()
		return true
	case keyboard.WasPressed(platform.KeyBackspace):
		p.scratchpadEntry.Backspace()
		return true
	case keyboard.WasPressed(platform.KeyDelete):
		p.scratchpadEntry.DeleteForward()
		return true
	case keyboard.WasPressed(platform.KeyLeft):
		p.scratchpadEntry.MoveLeft()
		return true
	case keyboard.WasPressed(platform.KeyRight):
		p.scratchpadEntry.MoveRight()
		return true
	}

	handled := false
	for _, r := range keyboard.Text {
		p.scratchpadEntry.Insert(r)
		p.previewArea.SetSystemResponse("")
		handled = true
	}
	return handled
}

func (p *ASDEXPane) submitScratchpadEntryCommand() {
	if p == nil || p.scratchpadEntry == nil {
		return
	}

	command := *p.scratchpadEntry
	value := command.Value()
	if !validScratchpadCommandValue(value) {
		p.finishScratchpadEntryCommand("INVALID ENTRY")
		return
	}

	target := p.targets.TargetByID(command.TargetID)
	if target == nil || !targetCanHaveDataBlock(target) {
		p.finishScratchpadEntryCommand("")
		return
	}
	if !p.targets.SetTargetScratchpad(command.TargetID, command.ScratchpadNumber, value) {
		p.finishScratchpadEntryCommand("")
		return
	}

	p.finishScratchpadEntryCommand("")
}

func (p *ASDEXPane) finishScratchpadEntryCommand(response string) {
	if p == nil {
		return
	}

	p.commandMode = CommandModeNone
	p.scratchpadEntry = nil
	p.multiFunction = nil
	p.dcb.ReturnToMainMenu()
	p.dcbMenuCommand = nil
	p.previewArea.SetSystemResponse(response)
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) tryExecuteMultiFunctionValue(ctx *panes.Context) bool {
	if p == nil || p.multiFunction == nil {
		return false
	}

	value := p.multiFunction.Value()
	if value == "" {
		return false
	}
	switch value {
	case "B", "V", "Y", "H":
		return false
	}

	input := &CommandInput{
		text:      value,
		entryType: CommandTextEntryNone,
		clickType: CommandClickNone,
	}
	status, err, handled := p.dispatchCommand(
		ctx,
		userCommands[CommandModeMultiFunction],
		input,
	)
	if err != nil {
		p.previewArea.SetSystemResponse(err.Error())
		return true
	}
	if handled {
		p.applyCommandStatus(status)
		return true
	}
	if len([]rune(value)) >= multiFunctionMaxLength {
		p.applyCommandStatus(commandOutputClearAll("INVALID ENTRY"))
		return true
	}
	return false
}

func (p *ASDEXPane) startMultiPreviewReposition() {
	if p == nil {
		return
	}

	p.commandMode = CommandModePreviewReposition
	p.multiFunction = nil
	p.scratchpadEntry = nil
	p.previewReposition = NewMultiPreviewRepositionCommand()
	p.coastListReposition = nil
	p.towerReadout = nil
	p.dcbSpinner = nil
	p.dcbMenuCommand = nil
	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.tempData.ClearHighlights()
	p.newWindow = nil
	p.deleteWindow = nil
	p.windowReposition = nil
	p.resizeWindow = nil
	p.commandEntry.Clear()
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) startMultiCoastListReposition() {
	if p == nil {
		return
	}

	p.commandMode = CommandModeCoastListReposition
	p.multiFunction = nil
	p.scratchpadEntry = nil
	p.previewReposition = nil
	p.coastListReposition = NewMultiCoastListRepositionCommand()
	p.towerReadout = nil
	p.dcbSpinner = nil
	p.dcbMenuCommand = nil
	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.tempData.ClearHighlights()
	p.newWindow = nil
	p.deleteWindow = nil
	p.windowReposition = nil
	p.resizeWindow = nil
	p.commandEntry.Clear()
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) handleMapRotateKeyboard(ctx *panes.Context) bool {
	if p == nil || p.mapRotate == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	command := p.mapRotate
	switch {
	case keyboard.WasPressed(platform.KeyEscape):
		p.cancelActiveCommand()
		return true
	case keyboard.WasPressed(platform.KeyEnter), keyboard.WasPressed(platform.KeyKeypadEnter):
		p.submitMapRotate()
		return true
	case keyboard.WasPressed(platform.KeyLeft):
		command.MoveLeft()
		return true
	case keyboard.WasPressed(platform.KeyRight):
		command.MoveRight()
		return true
	case keyboard.WasPressed(platform.KeyBackspace):
		command.Backspace()
		return true
	case keyboard.WasPressed(platform.KeyDelete):
		command.DeleteForward()
		return true
	}

	handled := false
	for _, r := range keyboard.Text {
		command.Insert(r)
		p.previewArea.SetSystemResponse("")
		handled = true
	}
	return handled
}

func (p *ASDEXPane) submitMapRotate() {
	if p == nil || p.mapRotate == nil {
		return
	}

	value, err := strconv.Atoi(p.mapRotate.Value())
	if err != nil || value < 0 || value > 359 {
		p.finishMapRotateCommand("INVALID ENTRY")
		return
	}

	windowID := p.mapRotate.WindowID
	rotation := normalizeRotation(float32(value))
	before := p.pushUndoBeforeMutation()
	if p.mapRotate.UndoCaptured {
		before = p.mapRotate.UndoBefore
		p.mapRotate.UndoCaptured = false
	}
	p.updateScopeViewForWindow(windowID, func(view *ScopeView) {
		view.Rotation = rotation
	})
	p.commitUndoIfChanged(before)
	p.finishMapRotateCommand("")
}

func (p *ASDEXPane) finishMapRotateCommand(response string) {
	if p == nil {
		return
	}

	command := p.mapRotate
	if command != nil && command.UndoCaptured {
		p.commitUndoIfChanged(command.UndoBefore)
		command.UndoCaptured = false
	}
	p.mapRotate = nil
	p.commandMode = CommandModeNone
	p.commandEntry.Clear()

	if command != nil && command.returnMenu == DcbMenuTools {
		p.dcb.SetMenu(DcbMenuTools)
		p.dcbMenuCommand = NewDcbMenuCommand("TOOLS")
	} else {
		p.dcb.ReturnToMainMenu()
		p.dcbMenuCommand = nil
	}

	p.previewArea.SetSystemResponse(response)
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) consumeMapRotateMouse(ctx *panes.Context) bool {
	if p == nil || p.mapRotate == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}

	hit := p.dcbHit(ctx)
	if !hit.OverDcb || !hit.HasFunction || hit.Function != DcbFunctionRotate {
		return false
	}

	mouse := ctx.Mouse
	switch {
	case mouse.WasReleased(platform.MouseButtonLeft):
		p.finishMapRotateCommand("")
		return true
	case mouse.Wheel.Y > 0 || mouse.Wheel.X > 0:
		return p.incrementActiveMapRotate(1)
	case mouse.Wheel.Y < 0 || mouse.Wheel.X < 0:
		return p.incrementActiveMapRotate(-1)
	default:
		return false
	}
}

func (p *ASDEXPane) incrementActiveMapRotate(delta float32) bool {
	if p == nil || p.mapRotate == nil || delta == 0 {
		return false
	}

	windowID := p.mapRotate.WindowID
	if !p.mapRotate.UndoCaptured {
		p.mapRotate.UndoBefore = p.captureUndoSnapshot()
		p.mapRotate.UndoCaptured = true
	}
	p.updateScopeViewForWindow(windowID, func(view *ScopeView) {
		view.Rotation = normalizeRotation(view.Rotation + delta)
	})
	p.previewArea.SetSystemResponse("")
	return true
}

func (p *ASDEXPane) handleDcbSpinnerKeyboard(ctx *panes.Context) bool {
	if p == nil || p.dcbSpinner == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	spinner := p.dcbSpinner
	switch {
	case keyboard.WasPressed(platform.KeyEscape):
		p.cancelDcbSpinner()
		return true
	case keyboard.WasPressed(platform.KeyBackspace), keyboard.WasPressed(platform.KeyDelete):
		p.cancelDcbSpinner()
		return true
	case keyboard.WasPressed(platform.KeyEnter), keyboard.WasPressed(platform.KeyKeypadEnter):
		p.commitDcbSpinner()
		return true
	case keyboard.WasPressed(platform.KeyLeft):
		spinner.MoveLeft()
		return true
	case keyboard.WasPressed(platform.KeyRight):
		spinner.MoveRight()
		return true
	}

	handled := false
	for _, r := range keyboard.Text {
		spinner.Insert(r)
		p.previewArea.SetSystemResponse("")
		handled = true
	}
	return handled
}

func (p *ASDEXPane) handleNormalCommandKeyboard(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Keyboard == nil {
		return false
	}

	keyboard := ctx.Keyboard
	switch {
	case keyboard.WasPressed(platform.KeyEscape):
		if !p.commandEntry.Empty() {
			p.commandEntry.Clear()
			p.previewArea.SetSystemResponse("")
			return true
		}
		return false
	case keyboard.WasPressed(platform.KeyEnter), keyboard.WasPressed(platform.KeyKeypadEnter):
		entryType := p.commandEntry.Type()
		switch entryType {
		case CommandTextEntryLeaderDirection, CommandTextEntryLeaderLength:
		default:
			return false
		}

		status, err, handled := p.tryExecuteUserCommand(
			ctx,
			p.commandEntry.Value(),
			nil,
			CommandClickNone,
			redsmath.Vec2{},
			radar.ScopeTransformations{},
		)
		if err != nil {
			p.commandEntry.Clear()
			p.previewArea.SetSystemResponse(err.Error())
			return true
		}
		if handled {
			p.applyCommandStatus(status)
			return true
		}

		p.commandEntry.Clear()
		if entryType == CommandTextEntryLeaderLength {
			p.previewArea.SetSystemResponse("INVALID LNG")
		} else {
			p.previewArea.SetSystemResponse("INVALID ENTRY")
		}
		return true
	case keyboard.WasPressed(platform.KeyLeft):
		p.commandEntry.MoveLeft()
		return !p.commandEntry.Empty()
	case keyboard.WasPressed(platform.KeyRight):
		p.commandEntry.MoveRight()
		return !p.commandEntry.Empty()
	case keyboard.WasPressed(platform.KeyBackspace):
		p.commandEntry.Backspace()
		return true
	case keyboard.WasPressed(platform.KeyDelete):
		p.commandEntry.DeleteForward()
		return true
	}

	handled := false
	for _, r := range keyboard.Text {
		p.commandEntry.Insert(r)
		p.previewArea.SetSystemResponse("")
		handled = true
	}
	return handled
}

func (p *ASDEXPane) consumeDatablockEditWheel(ctx *panes.Context) bool {
	if p == nil || p.datablockEdit == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}
	if ctx.Mouse.Wheel.Y > 0 {
		p.datablockEdit.MoveUp()
		return true
	}
	if ctx.Mouse.Wheel.Y < 0 {
		p.datablockEdit.MoveDown()
		return true
	}
	return false
}

func (p *ASDEXPane) listRepositionActive() bool {
	return p != nil && (p.previewReposition != nil || p.coastListReposition != nil)
}

const mapRepositionCenterDeadzonePx = float32(1)

func roundLogicalPixel(v float32) float32 {
	return float32(stdmath.Round(float64(v)))
}

func mapRepositionCursorCenter(rect redsmath.Rect) redsmath.Vec2 {
	return redsmath.Vec2{
		X: roundLogicalPixel(rect.Min.X + rect.Width()*0.5),
		Y: roundLogicalPixel(rect.Min.Y + rect.Height()*0.5),
	}
}

func mapRepositionDelta(mousePos, center redsmath.Vec2) (redsmath.Vec2, bool) {
	delta := mousePos.Sub(center)
	d2 := delta.X*delta.X + delta.Y*delta.Y
	if d2 <= mapRepositionCenterDeadzonePx*mapRepositionCenterDeadzonePx {
		return redsmath.Vec2{}, false
	}
	return delta, true
}

func (p *ASDEXPane) recenterMapRepositionCursor(ctx *panes.Context, center redsmath.Vec2) {
	if ctx == nil || ctx.Platform == nil || ctx.Mouse == nil {
		return
	}

	ctx.Platform.SetMousePosition(ctx.PaneRect.Min.Add(center))
	ctx.Mouse.Pos = center
	ctx.Mouse.Delta = redsmath.Vec2{}
}

func (p *ASDEXPane) centerMapRepositionCursor(ctx *panes.Context) {
	if p == nil || p.mapReposition == nil || ctx == nil || ctx.Platform == nil {
		return
	}

	rect, ok := p.scopeWindowRectForWindow(p.mapReposition.WindowID, ctx.PaneSize())
	if !ok {
		rect = redsmath.RectFromSize(ctx.PaneSize().X, ctx.PaneSize().Y)
	}
	center := mapRepositionCursorCenter(rect)
	p.recenterMapRepositionCursor(ctx, center)
}

func (p *ASDEXPane) consumeMapRepositionMouse(
	ctx *panes.Context,
	transforms radar.ScopeTransformations,
) bool {
	if p == nil || p.mapReposition == nil || ctx == nil || ctx.Mouse == nil || ctx.Platform == nil {
		return false
	}

	mouse := ctx.Mouse
	if mouse.WasPressed(platform.MouseButtonLeft) || mouse.WasReleased(platform.MouseButtonLeft) {
		if p.mapReposition.UndoCaptured {
			p.commitUndoIfChanged(p.mapReposition.UndoBefore)
			p.mapReposition.UndoCaptured = false
		}
		p.applyCommandStatus(CommandStatus{
			Clear:     ClearAll,
			Output:    "",
			HasOutput: true,
		})
		return true
	}

	windowID := p.mapReposition.WindowID
	rect, ok := p.scopeWindowRectForWindow(windowID, ctx.PaneSize())
	if !ok {
		rect = redsmath.RectFromSize(ctx.PaneSize().X, ctx.PaneSize().Y)
	}
	view, ok := p.scopeViewForWindow(windowID)
	if !ok {
		view = p.mainScopeView()
	}
	transforms = scopeTransformForWindow(
		rect,
		mainReferenceExtent(ctx.PaneSize()),
		view,
		rangeVisibleScaleForContext(ctx),
	)

	center := mapRepositionCursorCenter(rect)
	delta, moved := mapRepositionDelta(mouse.Pos, center)
	if !moved {
		p.recenterMapRepositionCursor(ctx, center)
		return true
	}

	deltaWorld := transforms.WorldFromWindowV(delta)
	if !p.mapReposition.UndoCaptured {
		p.mapReposition.UndoBefore = p.captureUndoSnapshot()
		p.mapReposition.UndoCaptured = true
	}
	p.updateScopeViewForWindow(windowID, func(view *ScopeView) {
		view.Center = view.Center.Sub(deltaWorld)
	})

	p.recenterMapRepositionCursor(ctx, center)

	return true
}

func (p *ASDEXPane) activeRepositionSize() redsmath.Vec2 {
	if p == nil {
		return redsmath.Vec2{}
	}
	if p.previewReposition != nil {
		return p.previewArea.RepositionSize()
	}
	if p.coastListReposition != nil {
		return p.coastList.RepositionSize()
	}
	return redsmath.Vec2{}
}

func (p *ASDEXPane) clampListRepositionCursor(ctx *panes.Context) {
	if p == nil || !p.listRepositionActive() || ctx == nil || ctx.Mouse == nil || ctx.Platform == nil {
		return
	}

	size := p.activeRepositionSize()
	if size.X <= 0 || size.Y <= 0 {
		return
	}

	local := ctx.Mouse.Pos
	clamped := clampListRepositionPoint(
		local,
		ctx.PaneSize(),
		size,
	)
	if clamped == local {
		return
	}

	ctx.Platform.SetMousePosition(ctx.PaneRect.Min.Add(clamped))
	ctx.Mouse.Pos = clamped
	ctx.Mouse.Delta = redsmath.Vec2{}
}

func (p *ASDEXPane) consumeListRepositionClick(ctx *panes.Context) bool {
	if p == nil || !p.listRepositionActive() || ctx == nil || ctx.Mouse == nil {
		return false
	}
	if !ctx.Mouse.WasReleased(platform.MouseButtonLeft) {
		return false
	}

	size := p.activeRepositionSize()
	if size.X <= 0 || size.Y <= 0 {
		return false
	}

	point := clampListRepositionPoint(
		ctx.Mouse.Pos,
		ctx.PaneSize(),
		size,
	)

	status, err, handled := p.tryExecuteUserCommand(
		ctx,
		"",
		nil,
		CommandClickLeft,
		point,
		radar.ScopeTransformations{},
	)
	if err != nil {
		p.previewArea.SetSystemResponse(err.Error())
		return true
	}
	if handled {
		p.applyCommandStatus(status)
		return true
	}

	return false
}

func (p *ASDEXPane) renderListRepositionOutline(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	transforms radar.ScopeTransformations,
) {
	if p == nil || !p.listRepositionActive() || ctx == nil || ctx.Mouse == nil || zcb == nil {
		return
	}

	size := p.activeRepositionSize()
	if size.X <= 0 || size.Y <= 0 {
		return
	}

	pos := clampListRepositionPoint(
		ctx.Mouse.Pos,
		ctx.PaneSize(),
		size,
	)

	x, y, w, h := ctx.PaneFramebufferRect()
	cb := zcb.At(windowZ(0, zPreviewRepositionOutline))
	cb.Viewport(x, y, w, h)
	cb.Scissor(x, y, w, h)
	transforms.LoadWindowViewingMatrices(cb)

	cb.SetRGB(previewRepositionOutlineColor(brightnessDefault))
	cb.LineWidth(1)

	builder := renderer.GetLinesBuilder()
	builder.AddLineLoop([]renderer.PointVertex{
		{X: pos.X, Y: pos.Y},
		{X: pos.X + size.X, Y: pos.Y},
		{X: pos.X + size.X, Y: pos.Y + size.Y},
		{X: pos.X, Y: pos.Y + size.Y},
	})
	builder.GenerateCommands(cb)
	renderer.ReturnLinesBuilder(builder)

	cb.DisableScissor()
}

func previewRepositionOutlineColor(brightness int) renderer.RGB {
	return applyBrightness(renderer.RGB8(0, 255, 255), brightness, brightnessFloorDefault)
}

func (p *ASDEXPane) updateRightClickGesture(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return
	}

	mouse := ctx.Mouse
	if mouse.WasPressed(platform.MouseButtonRight) {
		p.rightClickStart = mouse.Pos
		p.rightClickCandidate = true
		p.rightClickDragged = false
	}
	if mouse.IsDown(platform.MouseButtonRight) && p.rightClickCandidate {
		delta := mouse.Pos.Sub(p.rightClickStart)
		threshold2 := rightSlewDragThresholdPixels * rightSlewDragThresholdPixels
		if delta.X*delta.X+delta.Y*delta.Y > threshold2 {
			p.rightClickDragged = true
		}
	}
}

func (p *ASDEXPane) clearRightClickGesture() {
	if p == nil {
		return
	}
	p.rightClickCandidate = false
	p.rightClickDragged = false
}

func (p *ASDEXPane) buildCoastSuspendEntries(now time.Time) []CoastListEntry {
	if p == nil {
		return nil
	}

	var entries []CoastListEntry
	seenCoastDropKeys := make(map[string]bool)
	for _, target := range p.targets.All() {
		if target == nil {
			continue
		}

		if target.Coasting || target.Dropped {
			key := targetLogicalTrackKey(target)
			if key != "" {
				if seenCoastDropKeys[key] {
					continue
				}
				seenCoastDropKeys[key] = true
			}
		}

		entry := CoastListEntry{
			TargetID: target.ID,
			TrackID:  coastListTrackID(target),
			Callsign: target.Callsign,
			Beacon:   target.Beacon,
		}

		switch {
		case target.Dropped:
			entry.Status = CoastListEntryDropped
			entry.TimeoutSeconds = targetTimeoutSeconds(target.CoastUntil, now)
		case target.Coasting:
			entry.Status = CoastListEntryCoasting
			entry.TimeoutSeconds = targetTimeoutSeconds(target.CoastUntil, now)
		case target.Suspended:
			entry.Status = CoastListEntrySuspended
			entry.TimeoutSeconds = targetTimeoutSeconds(target.SuspendUntil, now)
			entry.Selected = p.hover.TargetID == target.ID
		default:
			continue
		}

		if target.ID == p.hoveredCoastListTarget {
			entry.Selected = true
		}
		entries = append(entries, entry)
	}
	return entries
}

func (p *ASDEXPane) updateCoastListHover(ctx *panes.Context) {
	if p == nil {
		return
	}
	p.hoveredCoastListTarget = ""
	if ctx == nil || ctx.Mouse == nil || !p.showCoastList {
		return
	}

	hit := p.coastList.HitTest(ctx.Mouse.Pos, p.fonts.font, p.eramTextFonts.font, ctx.PaneSize())
	if hit.Type == CoastListHitEntry &&
		(hit.Status == CoastListEntrySuspended ||
			p.commandMode == CommandModeTerminateControl) {
		p.hoveredCoastListTarget = hit.TargetID
	}
}

func (p *ASDEXPane) consumeCoastListClicks(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil || !p.showCoastList {
		return false
	}
	if !ctx.Mouse.WasReleased(platform.MouseButtonLeft) {
		return false
	}

	hit := p.coastList.HitTest(ctx.Mouse.Pos, p.fonts.font, p.eramTextFonts.font, ctx.PaneSize())
	if !hit.Hit {
		return false
	}

	switch hit.Type {
	case CoastListHitHeader:
		p.coastList.ToggleExpanded()
	case CoastListHitUpArrow:
		p.coastList.PageUp()
	case CoastListHitDownArrow:
		p.coastList.PageDown(p.fonts.font, ctx.PaneSize())
	case CoastListHitEntry:
		target := p.targets.TargetByID(hit.TargetID)
		if target == nil {
			return true
		}

		if p.commandMode == CommandModeTerminateControl {
			status, err, handled := p.tryExecuteUserCommand(
				ctx,
				"",
				target,
				CommandClickLeft,
				ctx.Mouse.Pos,
				radar.ScopeTransformations{},
			)
			if err != nil {
				p.previewArea.SetSystemResponse(err.Error())
				return true
			}
			if handled {
				p.applyCommandStatus(status)
			}
			return true
		}

		if hit.Status != CoastListEntrySuspended {
			return true
		}

		status, err, handled := p.tryExecuteUserCommand(
			ctx,
			"",
			target,
			CommandClickLeft,
			ctx.Mouse.Pos,
			radar.ScopeTransformations{},
		)
		if err != nil {
			p.previewArea.SetSystemResponse(err.Error())
			return true
		}
		if handled {
			p.applyCommandStatus(status)
		}
	}
	return true
}

func coastListTrackID(target *Target) string {
	if target == nil {
		return ""
	}
	if id := strings.TrimSpace(target.CoastListID); id != "" {
		return id
	}

	id := strings.TrimSpace(target.ID)
	if separator := strings.LastIndexByte(id, ':'); separator != -1 {
		id = id[separator+1:]
	}
	return id
}

func targetTimeoutSeconds(until, now time.Time) float64 {
	if until.IsZero() {
		return 0
	}
	return until.Sub(now).Seconds()
}

func (p *ASDEXPane) consumeNetworkEvents() {
	if p == nil || p.smes == nil {
		return
	}

	for {
		select {
		case status := <-p.smes.Status():
			p.applySmesStatus(status)
		case frame := <-p.smes.Frames():
			if !frame.Removed && frame.Airport != "" && !strings.EqualFold(frame.Airport, p.airport) {
				continue
			}
			p.targets.ApplySmesFrame(frame, p.videomap)
		default:
			return
		}
	}
}

func (p *ASDEXPane) applySmesStatus(status redsnet.SmesStatusEvent) {
	if p == nil {
		return
	}

	switch status.Status {
	case redsnet.SmesStatusConnected:
		p.previewArea.SetSystemResponse("CRITICAL FAULT END")
	case redsnet.SmesStatusDisconnected:
		p.previewArea.SetSystemResponse("CRITICAL FAULT START")
	}
}

func (p *ASDEXPane) initView(rect redsmath.Rect, rangeVisibleScale float32) {
	if p == nil || p.viewInitialized || p.videomap == nil || rect.Empty() {
		return
	}

	bounds := p.videomap.BoundsFeet()
	if bounds.Empty() {
		return
	}

	width := bounds.Width()
	height := bounds.Height()
	if width <= 0 || height <= 0 {
		return
	}

	paneW := rect.Width()
	paneH := rect.Height()
	if paneW <= 0 || paneH <= 0 {
		return
	}

	const margin = float32(1.08)

	referenceExtent := mainReferenceExtent(rect.Size())
	refWidth := referenceExtent.Width()
	if refWidth <= 0 || rangeVisibleScale <= 0 {
		return
	}

	rangeFromWidth := width * margin * refWidth / paneW
	rangeFromHeight := height * margin * refWidth / paneH

	fitFullHorizontalFeet := rangeFromWidth
	if rangeFromHeight > fitFullHorizontalFeet {
		fitFullHorizontalFeet = rangeFromHeight
	}

	p.center = redsmath.Vec2{
		X: (bounds.Min.X + bounds.Max.X) * 0.5,
		Y: (bounds.Min.Y + bounds.Max.Y) * 0.5,
	}
	fitRangeSetting := int(stdmath.Ceil(float64(
		fitFullHorizontalFeet / (asdexFeetPerRangeUnit * rangeVisibleScale),
	)))
	fitRangeSetting = clampInt(fitRangeSetting, asdexMinRangeSetting, asdexMaxRangeSetting)
	if p.rangeSetting == 0 {
		p.rangeSetting = asdexDefaultRangeSetting
	}
	if fitRangeSetting > p.rangeSetting {
		p.rangeSetting = fitRangeSetting
	}
	p.rangeFullHorizontalFeet = rangeFullHorizontalFeetFromSetting(p.rangeSetting)
	p.rotation = 0
	p.viewInitialized = true
}

func (p *ASDEXPane) consumeMouseEvents(
	ctx *panes.Context,
	transforms radar.ScopeTransformations,
) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}

	mouse := ctx.Mouse
	changed := false
	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())

	if !paneLocal.Contains(mouse.Pos) && !mouse.IsDown(platform.MouseButtonRight) {
		return false
	}

	if mouse.IsDown(platform.MouseButtonRight) &&
		(!p.rightClickCandidate || p.rightClickDragged) &&
		(mouse.Delta.X != 0 || mouse.Delta.Y != 0) {
		deltaWorld := transforms.WorldFromWindowV(mouse.Delta)
		p.center = p.center.Sub(deltaWorld)
		changed = true
	}

	if (mouse.Wheel.X != 0 || mouse.Wheel.Y != 0) &&
		ctx.Keyboard != nil &&
		ctx.Keyboard.IsDown(platform.KeyShift) &&
		paneLocal.Contains(mouse.Pos) {
		p.rotateFromWheel(mouse.Wheel)
		return true
	}

	if mouse.Wheel.Y != 0 && paneLocal.Contains(mouse.Pos) {
		oldRangeFullHorizontalFeet := p.rangeFullHorizontalFeet
		oldCenter := p.center
		p.setMainRangeSetting(
			p.rangeSetting + wheelRangeDeltaForContext(mouse.Wheel.Y, ctx),
		)
		newRangeFullHorizontalFeet := p.rangeFullHorizontalFeet

		if oldRangeFullHorizontalFeet > 0 && newRangeFullHorizontalFeet > 0 && newRangeFullHorizontalFeet != oldRangeFullHorizontalFeet {
			if ctx.Keyboard != nil && ctx.Keyboard.IsDown(platform.KeyAlt) {
				mouseWorld := transforms.WorldFromWindowP(mouse.Pos)
				scale := newRangeFullHorizontalFeet / oldRangeFullHorizontalFeet
				p.center = mouseWorld.Add(oldCenter.Sub(mouseWorld).Mul(scale))
			}
			changed = true
		}
	}

	return changed
}

func (p *ASDEXPane) rotateFromWheel(wheel redsmath.Vec2) {
	if p == nil {
		return
	}

	var delta float32
	switch {
	case wheel.Y > 0:
		delta = 1
	case wheel.Y < 0:
		delta = -1
	case wheel.X > 0:
		delta = 1
	case wheel.X < 0:
		delta = -1
	}
	if delta == 0 {
		return
	}

	p.rotateByDegrees(delta)
}

func (p *ASDEXPane) rotateByDegrees(delta float32) {
	if p == nil {
		return
	}
	p.rotation = normalizeRotation(p.rotation + delta)
}

func wheelRangeDeltaForContext(wheelY float32, ctx *panes.Context) int {
	if wheelY == 0 {
		return 0
	}

	step := asdexWheelRangeStep
	if ctx != nil && ctx.Keyboard != nil && ctx.Keyboard.IsDown(platform.KeyControl) {
		step = asdexCtrlWheelRangeStep
	}

	if wheelY > 0 {
		return -step
	}
	return step
}

const maxUndoSnapshots = 64

type UndoSnapshot struct {
	ActiveWindowID ScopeWindowID

	WindowStates map[ScopeWindowID]UndoWindowState

	DBFieldSettings DataBlockFieldSettings

	ShowCoastList bool

	PreviewLocation RelativeScreenLocation
	CoastLocation   RelativeScreenLocation

	DcbPosition          DcbPosition
	DcbCharSize          int
	CoastSuspendCharSize int
	PreviewAreaCharSize  int

	ListsBrightness int
	DcbBrightness   int

	VectorLength int
}

type UndoWindowState struct {
	View    ScopeView
	Display WindowDisplayState
}

func (ap *ASDEXPane) cmdUndo(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	ap.applyUndo()
	return CommandStatus{
		Clear:     ClearNone,
		Output:    "",
		HasOutput: true,
	}
}

func (ap *ASDEXPane) cmdDefault(_ *panes.Context) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	before := ap.pushUndoBeforeMutation()

	windowID := ap.activeWindowID()
	state := ap.displayStateForWindow(windowID)
	state.DB = DefaultDataBlockSettings()
	state.Brightness = NewWindowBrightnessSettings()
	state.ShowHistory = false
	state.HistoryLength = 7
	state.ShowVectorLine = false
	state.TargetShowDBOverrides = nil
	state.TargetDBOffAreaOverrides = nil
	state.LeaderDirectionOverrides = nil
	state.LeaderLengthOverrides = nil
	state.TraitLeaderDirectionOverrides = nil
	state.TraitLeaderLengthOverrides = nil
	state.TargetTraitAreaByTarget = nil

	ap.dbFieldSettings = DefaultDataBlockFieldSettings()

	ap.updateScopeViewForWindow(windowID, func(view *ScopeView) {
		view.RangeSetting = asdexDefaultRangeSetting
		view.RangeFullHorizontalFeet = rangeFullHorizontalFeetFromSetting(asdexDefaultRangeSetting)
		view.Rotation = 0
	})
	ap.setVectorLength(defaultVectorLengthSeconds)

	ap.commitUndoIfChanged(before)
	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}

func (p *ASDEXPane) captureUndoSnapshot() UndoSnapshot {
	if p == nil {
		return UndoSnapshot{}
	}

	out := UndoSnapshot{
		ActiveWindowID:       p.activeWindowID(),
		WindowStates:         make(map[ScopeWindowID]UndoWindowState),
		DBFieldSettings:      p.dbFieldSettings,
		ShowCoastList:        p.showCoastList,
		PreviewLocation:      p.previewArea.location,
		CoastLocation:        p.coastList.location,
		DcbPosition:          p.dcb.Position(),
		DcbCharSize:          p.dcb.CharSize(),
		CoastSuspendCharSize: p.coastList.FontSize(),
		PreviewAreaCharSize:  p.previewArea.FontSize(),
		ListsBrightness:      p.listsBrightness,
		DcbBrightness:        p.dcbBrightness,
		VectorLength:         p.vectorLength,
	}

	for id, state := range p.displayStateByWindow {
		if state == nil {
			continue
		}
		view, ok := p.scopeViewForWindow(id)
		if !ok {
			continue
		}
		out.WindowStates[id] = UndoWindowState{
			View:    view,
			Display: cloneWindowDisplayState(state),
		}
	}

	return out
}

func (p *ASDEXPane) pushUndoBeforeMutation() UndoSnapshot {
	if p == nil {
		return UndoSnapshot{}
	}
	return p.captureUndoSnapshot()
}

func (p *ASDEXPane) commitUndoIfChanged(before UndoSnapshot) {
	if p == nil || p.undoRestoring {
		return
	}

	after := p.captureUndoSnapshot()
	if reflect.DeepEqual(before, after) {
		return
	}

	p.undoStack = append(p.undoStack, before)
	if len(p.undoStack) > maxUndoSnapshots {
		p.undoStack = p.undoStack[len(p.undoStack)-maxUndoSnapshots:]
	}
}

func (p *ASDEXPane) applyUndo() {
	if p == nil {
		return
	}
	if len(p.undoStack) == 0 {
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return
	}

	last := p.undoStack[len(p.undoStack)-1]
	p.undoStack = p.undoStack[:len(p.undoStack)-1]

	p.undoRestoring = true
	defer func() {
		p.undoRestoring = false
	}()

	p.restoreUndoSnapshot(last)
	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) restoreUndoSnapshot(snapshot UndoSnapshot) {
	if p == nil {
		return
	}

	for id, state := range snapshot.WindowStates {
		if _, ok := p.scopeViewForWindow(id); !ok {
			continue
		}

		p.updateScopeViewForWindow(id, func(view *ScopeView) {
			*view = state.View
		})

		display := state.Display
		if p.displayStateByWindow == nil {
			p.displayStateByWindow = make(map[ScopeWindowID]*WindowDisplayState)
		}
		p.displayStateByWindow[id] = &display
	}

	if _, ok := p.scopeViewForWindow(snapshot.ActiveWindowID); ok {
		p.windows.SetActiveWindow(snapshot.ActiveWindowID)
	}

	p.dbFieldSettings = snapshot.DBFieldSettings

	p.showCoastList = snapshot.ShowCoastList
	if !p.showCoastList {
		p.hoveredCoastListTarget = ""
	}

	p.previewArea.location = snapshot.PreviewLocation
	p.coastList.location = snapshot.CoastLocation

	p.dcb.SetPosition(snapshot.DcbPosition)
	if snapshot.DcbCharSize > 0 {
		p.dcb.SetCharSize(snapshot.DcbCharSize)
	}
	if snapshot.CoastSuspendCharSize > 0 {
		p.coastList.SetFontSize(snapshot.CoastSuspendCharSize)
	}
	if snapshot.PreviewAreaCharSize > 0 {
		p.previewArea.SetFontSize(snapshot.PreviewAreaCharSize)
	}

	p.setListsBrightness(snapshot.ListsBrightness)
	p.setDcbBrightness(snapshot.DcbBrightness)
	p.setVectorLength(snapshot.VectorLength)
}

func (p *ASDEXPane) setDcbPositionUndoable(position DcbPosition) {
	if p == nil {
		return
	}

	before := p.pushUndoBeforeMutation()
	p.dcb.SetPosition(position)
	p.commitUndoIfChanged(before)

	p.previewArea.SetSystemResponse("")
	p.clearHighlightedTarget()
}

func (p *ASDEXPane) captureSpinnerUndo(spinner *DcbSpinner) {
	if p == nil || spinner == nil || spinner.UndoCaptured {
		return
	}
	spinner.UndoBefore = p.captureUndoSnapshot()
	spinner.UndoCaptured = true
}

func (p *ASDEXPane) commitSpinnerUndoIfChanged(spinner *DcbSpinner) {
	if p == nil || spinner == nil || !spinner.UndoCaptured {
		return
	}
	p.commitUndoIfChanged(spinner.UndoBefore)
	spinner.UndoCaptured = false
}

func (p *ASDEXPane) undoBeforeForSpinnerMutation(spinner *DcbSpinner) UndoSnapshot {
	if p == nil {
		return UndoSnapshot{}
	}
	if spinner != nil && spinner.UndoCaptured {
		spinner.UndoCaptured = false
		return spinner.UndoBefore
	}
	return p.pushUndoBeforeMutation()
}

func cloneWindowDisplayState(in *WindowDisplayState) WindowDisplayState {
	if in == nil {
		return *NewWindowDisplayState()
	}

	out := *in
	out.TargetShowDBOverrides = cloneBoolMap(in.TargetShowDBOverrides)
	out.TargetDBOffAreaOverrides = cloneBoolMap(in.TargetDBOffAreaOverrides)
	out.LeaderDirectionOverrides = cloneLeaderDirectionMap(in.LeaderDirectionOverrides)
	out.LeaderLengthOverrides = cloneIntMap(in.LeaderLengthOverrides)
	out.TraitLeaderDirectionOverrides = cloneLeaderDirectionMap(in.TraitLeaderDirectionOverrides)
	out.TraitLeaderLengthOverrides = cloneIntMap(in.TraitLeaderLengthOverrides)
	out.TargetTraitAreaByTarget = cloneStringMap(in.TargetTraitAreaByTarget)
	out.DataBlockAreas = cloneDataBlockAreas(in.DataBlockAreas)
	return out
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneLeaderDirectionMap(in map[string]LeaderDirection) map[string]LeaderDirection {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]LeaderDirection, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneDataBlockAreas(in []DataBlockArea) []DataBlockArea {
	if len(in) == 0 {
		return nil
	}
	out := make([]DataBlockArea, len(in))
	for i, area := range in {
		out[i] = area
		out[i].Points = append([]redsmath.Vec2(nil), area.Points...)
	}
	return out
}

func rangeFullHorizontalFeetFromSetting(rangeSetting int) float32 {
	rangeSetting = clampInt(rangeSetting, asdexMinRangeSetting, asdexMaxRangeSetting)
	return float32(rangeSetting * asdexFeetPerRangeUnit)
}

func windowScaleFactorForContext(ctx *panes.Context) float32 {
	if asdexWindowScaleFactorOverride > 0 {
		return asdexWindowScaleFactorOverride
	}
	if ctx == nil || ctx.Platform == nil {
		return 1
	}
	return ctx.Platform.WindowScaleFactor()
}

func rangeVisibleScale(windowScale float32) float32 {
	if windowScale <= 0 {
		return 1
	}

	intScale := float32(int(windowScale))
	if intScale < 1 {
		intScale = 1
	}
	return intScale / (windowScale * windowScale)
}

func rangeVisibleScaleForContext(ctx *panes.Context) float32 {
	return rangeVisibleScale(windowScaleFactorForContext(ctx))
}

func normalizeRotation(value float32) float32 {
	for value >= 360 {
		value -= 360
	}
	for value < 0 {
		value += 360
	}
	return value
}

func clamp(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampBrightness(value int) int {
	return clampInt(value, brightnessMin, brightnessMax)
}

func backgroundColor(mode Mode) renderer.RGB {
	if mode == ModeDay {
		return renderer.RGB8(0, 96, 120)
	}
	return renderer.RGB8(60, 60, 60)
}

func applyBrightness(color renderer.RGB, brightness int, minBrightness int) renderer.RGB {
	if brightness < brightnessMin {
		brightness = brightnessMin
	}
	if brightness > brightnessMax {
		brightness = brightnessMax
	}
	if minBrightness < 0 {
		minBrightness = 0
	}
	if minBrightness > 100 {
		minBrightness = 100
	}

	scale := (float32(brightness)*(100-float32(minBrightness))/100 + float32(minBrightness)) / 100
	return renderer.RGB{R: color.R * scale, G: color.G * scale, B: color.B * scale}
}
