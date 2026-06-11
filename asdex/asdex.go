package asdex

import (
	"encoding/json"
	"fmt"
	stdmath "math"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

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

	// CRC ASDE-X RANGE is not nautical miles. DisplayType.Asdex uses
	// RangeUnits.Feet, and ViewManager converts the displayed range setting
	// to feet as: rangeFeet = Range * 100. RANGE 100 means 10,000 ft from
	// center to the limiting screen edge.
	asdexMinRangeSetting     = 6
	asdexMaxRangeSetting     = 300
	asdexDefaultRangeSetting = 100
	asdexFeetPerRangeUnit    = 100
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
)

func windowZ(stackIndex int, localZ renderer.Z) renderer.Z {
	return renderer.Z(-10000 + stackIndex*1000 + int(localZ))
}

func scopeWindowZ(stackIndex int, localZ renderer.Z) renderer.Z {
	return renderer.Z(-20000 + stackIndex*1000 + int(localZ))
}

type ASDEXPane struct {
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

	displayStateByWindow      map[ScopeWindowID]*WindowDisplayState
	dbFieldSettings           DataBlockFieldSettings
	datablockTimeshareStart   time.Time
	showBeaconUntilByTargetID map[string]time.Time
	listsBrightness           int
	dcbBrightness             int
	previewArea               PreviewArea
	coastList                 CoastList
	alertRepository           AlertRepository
	auralAlerts               *AuralAlertManager
	alertMessageBox           AlertMessageBox
	dcb                       Dcb
	dcbSpinner                *DcbSpinner
	dcbMenuCommand            *DcbMenuCommand
	dbAreaDraft               *DataBlockAreaDraft
	dbAreaSelection           *DataBlockAreaSelection
	tempAreaDraft             *TempAreaDraft
	tempTextCommand           *TempTextCommand
	tempTextPlacement         *TempTextPlacementCommand
	tempDataSelectMode        TempDataSelectMode
	hoveredTempData           TempDataHit
	newWindow                 *NewWindowCommand
	showCoastList             bool
	hoveredCoastListTarget    string

	commandMode         CommandMode
	commandEntry        CommandTextEntry
	datablockEdit       *DatablockEditCommand
	editingTargetID     string
	initControlEntry    *CoastListIDEntryCommand
	termControlEntry    *CoastListIDEntryCommand
	multiFunction       *MultiFunctionCommand
	previewReposition   *PreviewRepositionCommand
	coastListReposition *CoastListRepositionCommand
	mapReposition       *MapRepositionCommand
	mapRotate           *MapRotateCommand

	rightClickStart     redsmath.Vec2
	rightClickCandidate bool
	rightClickDragged   bool

	hover ScopeHoverState

	center          redsmath.Vec2
	rangeSetting    int
	rangeFeet       float32
	rotation        float32
	viewInitialized bool
}

func NewPane(airport string) (*ASDEXPane, error) {
	airport = strings.ToUpper(strings.TrimSpace(airport))
	if airport == "" {
		return nil, fmt.Errorf("empty ASDE-X airport")
	}
	InitCommands()

	vm, err := LoadVideoMap(airport)
	if err != nil {
		return nil, err
	}
	safetyLogic, err := LoadSafetyLogic(airport, vm)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reds: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "reds: %v\n", err)
	}
	preview.SetSystemResponse("CRITICAL FAULT START")
	coastList := NewCoastList()
	auralAlerts := NewAuralAlertManager()
	configAirport := loadConfigAirportCode(airport)

	client := redsnet.NewSmesClient(targetWebSocketURL())
	client.SetAirport(airport)
	client.Start()

	return &ASDEXPane{
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
		previewArea:               preview,
		coastList:                 coastList,
		alertRepository:           NewAlertRepository(auralAlerts),
		auralAlerts:               auralAlerts,
		alertMessageBox:           NewAlertMessageBox(),
		dcb:                       NewDcb(),
		showCoastList:             true,
		rangeSetting:              asdexDefaultRangeSetting,
		rangeFeet:                 rangeFeetFromSetting(asdexDefaultRangeSetting),
	}, nil
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

func (p *ASDEXPane) Draw(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if ctx == nil || zcb == nil || p == nil {
		return
	}

	p.ensureCursorsLoaded(ctx)
	p.consumeNetworkEvents()
	p.consumeCommandKeyboard(ctx)
	p.initView(ctx.PaneRect)
	if !p.viewInitialized {
		return
	}

	referenceExtent := mainReferenceExtent(ctx.PaneSize())
	transforms := scopeTransformForWindow(referenceExtent, referenceExtent, p.mainScopeView())

	now := time.Now().UTC()
	p.expireTemporaryBeaconDisplays(now)
	p.targets.ExpireSuspendedTracks(now)
	p.targets.UpdateCoastDropTracks(
		now,
		aircraftCoastDelay,
		coastDropLifetime,
		p.isDestinationCurrentAirport,
	)
	p.consumeOpsHotkeys(ctx, transforms)
	p.coastList.SetVisible(p.showCoastList)
	p.coastList.SetEntries(p.buildCoastSuspendEntries(now))
	if p.mapReposition == nil && !p.listRepositionActive() && p.dbAreaDraft == nil && p.dbAreaSelection == nil &&
		p.tempAreaDraft == nil && p.tempTextCommand == nil && p.tempTextPlacement == nil &&
		p.tempDataSelectMode == TempDataSelectNone && p.newWindow == nil {
		p.updateCoastListHover(ctx)
	} else {
		p.hoveredCoastListTarget = ""
	}
	if p.mapReposition == nil && p.dbAreaDraft == nil && p.dbAreaSelection == nil && p.tempAreaDraft == nil &&
		p.tempTextCommand == nil && p.tempTextPlacement == nil &&
		p.tempDataSelectMode == TempDataSelectNone && p.newWindow == nil {
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
			)
		}
	} else if p.listRepositionActive() {
		p.clearHighlightedTarget()
		p.clampListRepositionCursor(ctx)
		p.consumeListRepositionClick(ctx)
	} else if p.datablockEdit != nil {
		p.clearHighlightedTarget()
		p.consumeDatablockEditWheel(ctx)
	} else if p.newWindow != nil {
		p.clearHighlightedTarget()
		p.consumeNewWindowInput(ctx, transforms)
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
			)
		}
	} else if p.dbAreaSelection != nil {
		p.clearHighlightedTarget()
		if !p.consumeDcbInput(ctx) {
			p.consumeDataBlockAreaSelectionInput(ctx, referenceExtent)
		}
	} else if p.dcbMenuCommand != nil {
		p.clearHighlightedTarget()
		p.consumeDcbInput(ctx)
	} else {
		if p.consumeDcbInput(ctx) {
			p.clearHighlightedTarget()
		} else {
			p.maybeActivateScopeWindowOnLeftPress(ctx)
			if ctx.Mouse == nil {
				p.clearHighlightedTarget()
			} else {
				windowID, windowRect, view, ok := p.scopeWindowAtPoint(ctx.Mouse.Pos, ctx.PaneSize())
				if ok {
					scopeTransforms := scopeTransformForWindow(windowRect, referenceExtent, view)
					updatedView, changed := p.consumeScopeMouseEvents(ctx, windowRect, view, scopeTransforms)
					if changed {
						p.setScopeView(windowID, updatedView)
						view = updatedView
						scopeTransforms = scopeTransformForWindow(windowRect, referenceExtent, view)
						if windowID == mainScopeWindowID {
							transforms = scopeTransforms
						}
					}
					p.updateHighlightedTargetInWindow(ctx, windowID, windowRect, scopeTransforms)
					if !p.consumeCoastListClicks(ctx) {
						p.consumeCommandClicksInWindow(ctx, windowRect, scopeTransforms)
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
			scopeTransforms := scopeTransformForWindow(windowRect, referenceExtent, view)
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
	p.applyCurrentCursor(ctx)
	p.coastList.SetEntries(p.buildCoastSuspendEntries(now))
	targets := p.targets.All()
	alertChanges := p.safetyLogic.Update(targets, SafetyLogicUpdateOptions{
		RunwayConfiguration: p.currentSafetyRunwayConfiguration(),
		RunwayClosed:        p.tempData.RunwayClosed,
	})
	p.alertRepository.ApplyChanges(alertChanges)
	alertTargetIDs := p.alertRepository.AircraftIDsInAlertSet()
	alertInProgress := p.alertRepository.AlertInProgress()
	alertOn := alertFlashOn(now)

	mainRect := redsmath.RectFromSize(ctx.PaneSize().X, ctx.PaneSize().Y)
	transforms = p.renderScopeWindow(
		ctx,
		zcb,
		0,
		mainScopeWindowID,
		mainRect,
		referenceExtent,
		p.mainScopeView(),
		targets,
		now,
		true,
		alertTargetIDs,
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
			targets,
			now,
			false,
			alertTargetIDs,
			alertInProgress,
			alertOn,
		)
	}
	p.renderWindowBorders(ctx, zcb, transforms)
	p.renderNewWindowPreview(ctx, zcb, transforms)

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
		p.coastList.Render(td, p.fonts.font, ctx.PaneSize())
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
}

func (p *ASDEXPane) renderScopeWindow(
	ctx *panes.Context,
	zcb *renderer.ZCmdBuffer,
	stackIndex int,
	windowID ScopeWindowID,
	rect redsmath.Rect,
	referenceExtent redsmath.Rect,
	view ScopeView,
	targets []*Target,
	now time.Time,
	drawDraft bool,
	alertTargetIDs map[string]bool,
	alertInProgress bool,
	alertOn bool,
) radar.ScopeTransformations {
	if p == nil || ctx == nil || zcb == nil || rect.Empty() {
		return radar.ScopeTransformations{}
	}

	transforms := scopeTransformForWindow(rect, referenceExtent, view)
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
	transforms.LoadWindowViewingMatrices(holdBarCB)
	p.safetyLogic.DrawHoldBars(holdBarCB, transforms, brightness.HoldBars)
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
			VectorSeconds:       3,
			Brightness:          brightness.Track,
			ScopeRotationDeg:    int(view.Rotation),
			HighlightedTargetID: highlightedTargetID,
			AlertTargetIDs:      alertTargetIDs,
			AlertFlashOn:        alertOn,
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
		p.tempDataSelectMode != TempDataSelectNone || p.newWindow != nil {
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
		p.previewReposition == nil &&
		p.coastListReposition == nil &&
		p.mapReposition == nil &&
		p.mapRotate == nil
}

func (p *ASDEXPane) dcbMouseCaptured() bool {
	if p == nil {
		return false
	}
	return false
}

func (p *ASDEXPane) dcbState() DcbState {
	if p == nil {
		return DcbState{
			Mode:         ModeDay,
			VectorOn:     true,
			VectorLength: 3,
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
	}
	fields := p.dbFieldSettings

	state := DcbState{
		Range:                  rangeSetting,
		Mode:                   p.mode,
		VectorOn:               true,
		VectorLength:           3,
		LeaderLength:           active.DB.LeaderLength,
		DataBlocksOn:           active.DB.ShowDataBlocks,
		DcbOn:                  p.dcb.On(),
		RotationDeg:            int(normalizeRotation(active.View.Rotation)),
		ShowHistory:            true,
		HistoryLength:          7,
		ShowCoastList:          p.showCoastList,
		CursorSpeed:            1,
		CursorHome:             false,
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
		ActiveSpinnerFunction:  activeSpinnerFunction,
	}

	windowState := p.displayStateForWindow(active.WindowID)
	if area, ok := windowState.selectedDataBlockArea(); ok {
		state.HasSelectedDbArea = true
		state.SelectedDbAreaTraits = area.Traits
	}

	return state
}

func (p *ASDEXPane) currentSafetyRunwayConfiguration() SafetyRunwayConfiguration {
	if p == nil {
		return LimitedSafetyRunwayConfiguration()
	}

	name := p.previewArea.RunwayConfigName()
	if strings.EqualFold(strings.TrimSpace(name), "LIMITED") {
		return LimitedSafetyRunwayConfiguration()
	}

	// Later: return the selected runway configuration with arrival/departure
	// runway maps once REDS stores the full preview config selection.
	return SafetyRunwayConfiguration{Name: name}
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

func (p *ASDEXPane) activateDcbHit(_ *panes.Context, hit DcbHit) bool {
	if p == nil {
		return false
	}
	if !hit.HasFunction {
		return false
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
	case DcbFunctionTools:
		p.openToolsMenu()
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
		p.previewArea.SetSystemResponse("")
		p.clearHighlightedTarget()
		return true
	default:
		return true
	}
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
	p.previewReposition = nil
	p.coastListReposition = nil
	p.mapReposition = nil
	p.mapRotate = nil
	p.dcbSpinner = nil
	p.dbAreaDraft = nil
	p.dbAreaSelection = nil
	p.tempAreaDraft = nil
	p.tempTextCommand = nil
	p.tempTextPlacement = nil
	p.tempDataSelectMode = TempDataSelectNone
	p.hoveredTempData = TempDataHit{Type: TempDataHitNone, Index: -1}
	p.tempData.ClearHighlights()
	p.newWindow = nil
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

	p.updateActiveDataBlockSettings(func(settings *DataBlockSettings) {
		settings.FullDataBlocks = !settings.FullDataBlocks
	})
	p.previewArea.SetSystemResponse("")
}

func (p *ASDEXPane) toggleDbField(update func(*DataBlockFieldSettings)) {
	if p == nil || update == nil {
		return
	}

	update(&p.dbFieldSettings)
	p.previewArea.SetSystemResponse("")
}

func (p *ASDEXPane) toggleDataBlocksOnOff() {
	if p == nil {
		return
	}

	windowID := p.activeWindowID()
	p.updateActiveDataBlockSettings(func(settings *DataBlockSettings) {
		settings.ShowDataBlocks = !settings.ShowDataBlocks
	})
	p.clearTargetShowDBOverrides(windowID)
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

func isToolsPlaceholderFunction(function DcbFunction) bool {
	switch function {
	case DcbFunctionRange,
		DcbFunctionMapReposition,
		DcbFunctionRotate,
		DcbFunctionNewWindow,
		DcbFunctionResizeWindow,
		DcbFunctionDeleteWindow,
		DcbFunctionWindowReposition,
		DcbFunctionHistoryOnOff,
		DcbFunctionHistory,
		DcbFunctionCoastOnOff,
		DcbFunctionCoastReposition,
		DcbFunctionPreviewReposition,
		DcbFunctionCursorSpeed,
		DcbFunctionCursorHomeOnOff,
		DcbFunctionDcbTop,
		DcbFunctionDcbLeft,
		DcbFunctionDcbRight,
		DcbFunctionDcbBottom,
		DcbFunctionChangePassword,
		DcbFunctionPlayBack:
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

	state := p.displayStateForWindow(p.activeWindowID())
	area, ok := state.selectedDataBlockArea()
	if !ok || area.Traits.DataBlocksOff {
		p.previewArea.SetSystemResponse("")
		return false
	}

	update(&area.Traits)
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
	p.previewReposition = nil
	p.coastListReposition = nil
	p.mapReposition = nil
	p.mapRotate = nil
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
	p.commandEntry.Clear()
	p.dcbSpinner = NewRangeDcbSpinner(windowID, currentRange)
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
		p.commitDcbSpinner()
		return true
	default:
		return false
	}
}

func (p *ASDEXPane) cancelDcbSpinner() {
	if p == nil {
		return
	}
	if p.dcbSpinner != nil && p.dcbSpinner.Type == DcbSpinnerBrightness {
		p.dcbSpinner = nil
		p.dcb.SetMenu(DcbMenuBrightness)
		p.dcbMenuCommand = NewDcbMenuCommand("BRITE")
		p.previewArea.SetSystemResponse("")
		return
	}
	if p.dcbSpinner != nil && p.dcbSpinner.Type != DcbSpinnerRange {
		p.restoreDbAreaEditCommand(p.dcbSpinner)
	}
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
			p.dcbSpinner = nil
			p.previewArea.SetSystemResponse("")
			return
		}

		value, ok := spinner.ParsedValue()
		if !ok {
			p.dcbSpinner = nil
			p.previewArea.SetSystemResponse("INVALID RANGE")
			return
		}

		p.setRangeSettingForWindow(spinner.WindowID, value)
		p.dcbSpinner = nil
		p.previewArea.SetSystemResponse("")
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

	if p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
		t.FontSize = value
	}) {
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

	if p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
		t.Brightness = value
	}) {
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

	if p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
		t.LeaderLength = value
	}) {
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
	if p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
		t.LeaderDirection = direction
	}) {
		p.finishDbAreaSpinner(spinner, "")
		return
	}
	p.finishDbAreaSpinner(spinner, "INVALID ENTRY")
}

func (p *ASDEXPane) finishBrightnessSpinner(systemResponse string) {
	if p == nil {
		return
	}
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

	p.setBrightnessValue(spinner.Function, value)
	p.finishBrightnessSpinner("")
}

func (p *ASDEXPane) incrementActiveDcbSpinner(delta int) {
	if p == nil || p.dcbSpinner == nil || delta == 0 {
		return
	}

	spinner := p.dcbSpinner
	switch spinner.Type {
	case DcbSpinnerRange:
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
	case DcbSpinnerDbAreaCharSize:
		next := clampInt(spinner.Value+delta, 1, 6)
		if next != spinner.Value {
			spinner.Value = next
			p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
				t.FontSize = next
			})
		}
	case DcbSpinnerDbAreaBrightness:
		next := clampInt(spinner.Value+delta, brightnessFloorDefault, brightnessMax)
		if next != spinner.Value {
			spinner.Value = next
			p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
				t.Brightness = next
			})
		}
	case DcbSpinnerDbAreaLeaderLength:
		next := clampInt(spinner.Value+delta, leaderLengthMin, leaderLengthMax)
		if next != spinner.Value {
			spinner.Value = next
			p.updateDataBlockAreaTraitsByID(spinner.WindowID, spinner.AreaID, func(t *DataBlockAreaTraits) {
				t.LeaderLength = next
			})
		}
	case DcbSpinnerDbAreaLeaderDirection:
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
		view.RangeFeet = rangeFeetFromSetting(rangeSetting)
	})
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
	WindowID   ScopeWindowID
	View       ScopeView
	DB         DataBlockSettings
	Brightness WindowBrightnessSettings
}

func (p *ASDEXPane) activeDcbWindowState() ActiveDcbWindowState {
	windowID := p.activeWindowID()

	view, ok := p.scopeViewForWindow(windowID)
	if !ok {
		windowID = mainScopeWindowID
		view = p.mainScopeView()
	}

	return ActiveDcbWindowState{
		WindowID:   windowID,
		View:       view,
		DB:         p.dataBlockSettingsForWindow(windowID),
		Brightness: p.displayStateForWindow(windowID).Brightness,
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
		// global DB field toggles, manual per-target leader overrides, then
		// DB area traits while the target is inside the area.
		settings = p.applyManualLeaderOverrides(settings, windowID, target.ID)

		if area, ok := p.dataBlockAreaForPoint(windowID, target.PosFeet); ok {
			settings = applyDataBlockAreaTraits(settings, area.Traits)
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
	if p == nil || ctx == nil || ctx.Platform == nil || p.cursors.loaded {
		return
	}
	if err := p.cursors.Load(ctx.Platform); err != nil {
		fmt.Fprintf(os.Stderr, "reds: %v\n", err)
	}
}

func (p *ASDEXPane) applyCurrentCursor(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Platform == nil {
		return
	}
	if p.datablockEdit != nil {
		p.applyCursorMode(ctx, CursorModeHidden)
		return
	}
	if ctx.Mouse == nil {
		return
	}

	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	if !paneLocal.Contains(ctx.Mouse.Pos) {
		return
	}
	p.applyCursorMode(ctx, p.resolveCursorMode(ctx))
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

func (p *ASDEXPane) applyCursorMode(ctx *panes.Context, mode CursorMode) {
	if p == nil || ctx == nil || ctx.Platform == nil {
		return
	}

	p.cursorMode = mode
	cursor, hidden := p.cursors.CursorForMode(mode)
	if hidden {
		ctx.Platform.SetCursorHiddenOverride()
		return
	}
	if cursor != nil {
		ctx.Platform.SetCursorOverride(cursor)
	}
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
	if p.dcbSpinner != nil {
		return p.dcbSpinner.DisplayLines()
	}
	if p.newWindow != nil {
		return p.newWindow.DisplayLines()
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
	if p.multiFunction != nil {
		return p.multiFunction.CursorLine(), p.multiFunction.CursorColumn(), true
	}
	if p.mapRotate != nil {
		return p.mapRotate.CursorLine(), p.mapRotate.CursorColumn(), true
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
	if p.mapReposition != nil && p.mapReposition.initialized {
		windowID := p.mapReposition.WindowID
		originalCenter := p.mapReposition.originalCenter
		p.updateScopeViewForWindow(windowID, func(view *ScopeView) {
			view.Center = originalCenter
		})
	}
	if p.mapRotate != nil {
		windowID := p.mapRotate.WindowID
		originalRotation := p.mapRotate.originalRotation
		p.updateScopeViewForWindow(windowID, func(view *ScopeView) {
			view.Rotation = originalRotation
		})
	}
	p.commandMode = CommandModeNone
	p.datablockEdit = nil
	p.editingTargetID = ""
	p.initControlEntry = nil
	p.termControlEntry = nil
	p.multiFunction = nil
	p.previewReposition = nil
	p.coastListReposition = nil
	p.mapReposition = nil
	p.mapRotate = nil
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
	if p.multiFunction != nil {
		return p.handleMultiFunctionKeyboard(ctx)
	}
	if p.mapRotate != nil {
		return p.handleMapRotateKeyboard(ctx)
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
	if p.commandMode == CommandModeNone {
		return p.handleNormalCommandKeyboard(ctx)
	}
	return false
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
	p.previewReposition = NewMultiPreviewRepositionCommand()
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
	p.previewReposition = nil
	p.coastListReposition = NewMultiCoastListRepositionCommand()
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
		p.mapRotate = nil
		p.applyCommandStatus(commandOutputClearAll("INVALID ENTRY"))
		return
	}

	windowID := p.mapRotate.WindowID
	rotation := normalizeRotation(float32(value))
	p.updateScopeViewForWindow(windowID, func(view *ScopeView) {
		view.Rotation = rotation
	})
	p.applyCommandStatus(CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	})
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

func mapRepositionCursorCenter(rect redsmath.Rect) redsmath.Vec2 {
	size := rect.Size()
	return rect.Min.Add(redsmath.Vec2{
		X: size.X * 0.5,
		Y: size.Y * 0.5,
	})
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
	ctx.Platform.SetMousePosition(ctx.PaneRect.Min.Add(center))
	if ctx.Mouse != nil {
		ctx.Mouse.Pos = center
		ctx.Mouse.Delta = redsmath.Vec2{}
	}
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
	transforms = scopeTransformForWindow(rect, mainReferenceExtent(ctx.PaneSize()), view)

	center := mapRepositionCursorCenter(rect)
	delta := mouse.Pos.Sub(center)
	if delta.X == 0 && delta.Y == 0 {
		return true
	}

	deltaWorld := transforms.WorldFromWindowV(delta)
	p.updateScopeViewForWindow(windowID, func(view *ScopeView) {
		view.Center = view.Center.Sub(deltaWorld)
	})

	ctx.Platform.SetMousePosition(ctx.PaneRect.Min.Add(center))
	mouse.Pos = center
	mouse.Delta = redsmath.Vec2{}

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
	for _, target := range p.targets.All() {
		if target == nil {
			continue
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

func targetWebSocketURL() string {
	if value := os.Getenv("REDS_TARGET_WS_URL"); value != "" {
		return value
	}
	if value := os.Getenv("NASCOPE_TARGET_WS_URL"); value != "" {
		return value
	}
	port := os.Getenv("WS_PORT")
	if port == "" {
		port = "8080"
	}
	return "ws://localhost:" + port + "/ws"
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

func (p *ASDEXPane) initView(rect redsmath.Rect) {
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

	const margin = float32(1.08)

	aspect := rect.Width() / rect.Height()
	rangeFromHeight := height * margin * 0.5
	rangeFromWidth := (width * margin) / (2 * aspect)

	fitRangeFeet := rangeFromHeight
	if rangeFromWidth > fitRangeFeet {
		fitRangeFeet = rangeFromWidth
	}

	p.center = redsmath.Vec2{
		X: (bounds.Min.X + bounds.Max.X) * 0.5,
		Y: (bounds.Min.Y + bounds.Max.Y) * 0.5,
	}
	fitRangeSetting := int(stdmath.Ceil(float64(fitRangeFeet / asdexFeetPerRangeUnit)))
	fitRangeSetting = clampInt(fitRangeSetting, asdexMinRangeSetting, asdexMaxRangeSetting)
	if p.rangeSetting == 0 {
		p.rangeSetting = asdexDefaultRangeSetting
	}
	if fitRangeSetting > p.rangeSetting {
		p.rangeSetting = fitRangeSetting
	}
	p.rangeFeet = rangeFeetFromSetting(p.rangeSetting)
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
		oldRangeFeet := p.rangeFeet
		oldCenter := p.center
		p.setMainRangeSetting(p.rangeSetting + wheelRangeDelta(mouse.Wheel.Y))
		newRangeFeet := p.rangeFeet

		if oldRangeFeet > 0 && newRangeFeet > 0 && newRangeFeet != oldRangeFeet {
			if ctx.Keyboard != nil && ctx.Keyboard.IsDown(platform.KeyAlt) {
				mouseWorld := transforms.WorldFromWindowP(mouse.Pos)
				scale := newRangeFeet / oldRangeFeet
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

func wheelRangeDelta(wheelY float32) int {
	switch {
	case wheelY > 0:
		return -1
	case wheelY < 0:
		return 1
	default:
		return 0
	}
}

func rangeFeetFromSetting(rangeSetting int) float32 {
	rangeSetting = clampInt(rangeSetting, asdexMinRangeSetting, asdexMaxRangeSetting)
	return float32(rangeSetting * asdexFeetPerRangeUnit)
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
