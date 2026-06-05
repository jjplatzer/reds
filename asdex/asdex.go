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
)

const (
	zVideoMap            renderer.Z = -900
	zRunwayClosures      renderer.Z = -800
	zSafetyLogicHoldBars renderer.Z = -790

	zRestrictedArea renderer.Z = -700
	zClosedArea     renderer.Z = -690
	zTempMapText    renderer.Z = -680
	zDBAreas        renderer.Z = -600

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

type ASDEXPane struct {
	airport           string
	configAirportCode string
	mode              Mode
	videomap          *VideoMap
	targets           TargetStore
	smes              *redsnet.SmesClient
	fonts             fontCache
	eramTextFonts     fontCache

	cursors    CursorSet
	cursorMode CursorMode

	datablockSettings         DataBlockSettings
	datablockTimeshareStart   time.Time
	leaderDirectionByTarget   map[string]LeaderDirection
	leaderLengthByTarget      map[string]int
	showBeaconUntilByTargetID map[string]time.Time
	previewArea               PreviewArea
	coastList                 CoastList
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

	highlightedTargetID    string
	highlightMouseWorld    redsmath.Vec2
	highlightStoreRevision uint64
	highlightQueryValid    bool

	center          redsmath.Vec2
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
	configAirport := loadConfigAirportCode(airport)

	client := redsnet.NewSmesClient(targetWebSocketURL())
	client.SetAirport(airport)
	client.Start()

	return &ASDEXPane{
		airport:           airport,
		configAirportCode: configAirport,
		mode:              ModeDay,
		videomap:          vm,
		targets:           NewTargetStore(),
		smes:              client,
		fonts:             fonts,
		eramTextFonts:     eramTextFonts,

		datablockSettings:         DefaultDataBlockSettings(),
		datablockTimeshareStart:   time.Now(),
		leaderDirectionByTarget:   make(map[string]LeaderDirection),
		leaderLengthByTarget:      make(map[string]int),
		showBeaconUntilByTargetID: make(map[string]time.Time),
		previewArea:               preview,
		coastList:                 coastList,
		showCoastList:             true,
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

	paneExtent := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	transforms := radar.GetScopeTransformations(
		paneExtent,
		p.center,
		p.rangeFeet,
		p.rotation,
	)

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
	if p.mapReposition == nil && !p.listRepositionActive() {
		p.updateCoastListHover(ctx)
	} else {
		p.hoveredCoastListTarget = ""
	}
	if p.mapReposition == nil {
		p.updateRightClickGesture(ctx)
	} else {
		p.clearRightClickGesture()
	}

	if p.mapReposition != nil {
		p.clearHighlightedTarget()
		if p.consumeMapRepositionMouse(ctx, transforms) {
			transforms = radar.GetScopeTransformations(
				paneExtent,
				p.center,
				p.rangeFeet,
				p.rotation,
			)
		}
	} else if p.listRepositionActive() {
		p.clearHighlightedTarget()
		p.clampListRepositionCursor(ctx)
		p.consumeListRepositionClick(ctx)
	} else if p.datablockEdit != nil {
		p.clearHighlightedTarget()
		p.consumeDatablockEditWheel(ctx)
	} else {
		if p.consumeMouseEvents(ctx, transforms) {
			transforms = radar.GetScopeTransformations(
				paneExtent,
				p.center,
				p.rangeFeet,
				p.rotation,
			)
		}
		p.updateHighlightedTarget(ctx, transforms)
		if !p.consumeCoastListClicks(ctx) {
			p.consumeCommandClicks(ctx, transforms)
		}
	}
	p.applyCurrentCursor(ctx)
	p.coastList.SetEntries(p.buildCoastSuspendEntries(now))
	targets := p.targets.All()

	cb := zcb.At(windowZ(0, zVideoMap))
	x, y, w, h := ctx.PaneFramebufferRect()
	cb.Viewport(x, y, w, h)
	cb.Scissor(x, y, w, h)
	cb.Clear(applyBrightness(backgroundColor(p.mode), brightnessDefault, 20).ToRGBA())

	transforms.LoadWorldViewingMatrices(cb)
	DrawVideoMap(p.videomap, cb, p.mode)
	cb.DisableScissor()

	targetCB := zcb.At(windowZ(0, zTargets))
	targetCB.Viewport(x, y, w, h)
	targetCB.Scissor(x, y, w, h)
	transforms.LoadWorldViewingMatrices(targetCB)
	DrawTargets(
		targets,
		p.targets.History(),
		targetCB,
		TargetDrawOptions{
			VectorSeconds:    3,
			Brightness:       brightnessDefault,
			ScopeRotationDeg: int(p.rotation),
		},
	)
	targetCB.DisableScissor()

	suspendedLabelCB := zcb.At(windowZ(0, zSuspendedLabels))
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

	datablockSettings := p.dataBlockSettings()
	dbCB := zcb.At(windowZ(0, zDatablocks))
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
				settings := datablockSettings
				if target != nil {
					if direction, ok := p.leaderDirectionByTarget[target.ID]; ok {
						settings.LeaderDirection = direction
					}
					if length, ok := p.leaderLengthByTarget[target.ID]; ok {
						settings.LeaderLength = length
					}
				}
				return settings
			},
			ShowBeaconCodeForTarget: func(target *Target) bool {
				return p.showBeaconCodeForTarget(target, now)
			},
		},
	)
	dbCB.DisableScissor()

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
}

func (p *ASDEXPane) dataBlockSettings() DataBlockSettings {
	settings := DefaultDataBlockSettings()
	if p == nil {
		return settings
	}

	settings = p.datablockSettings
	settings.TimesharePrimary = p.timesharePrimary(time.Now())
	return settings
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
	if ctx != nil && ctx.Mouse != nil && ctx.Mouse.IsDown(platform.MouseButtonRight) {
		return CursorModeHidden
	}
	if p != nil && p.showCoastList && ctx != nil && ctx.Mouse != nil {
		hit := p.coastList.HitTest(ctx.Mouse.Pos, p.fonts.font, p.eramTextFonts.font, ctx.PaneSize())
		if hit.Kind == CoastListHitEntry &&
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
	if p == nil || ctx == nil || ctx.Mouse == nil {
		p.clearHighlightedTarget()
		return
	}

	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	if !paneLocal.Contains(ctx.Mouse.Pos) {
		p.clearHighlightedTarget()
		return
	}

	mouseWorld := transforms.WorldFromWindowP(ctx.Mouse.Pos)
	storeRevision := p.targets.HoverRevision()
	if p.highlightQueryValid &&
		p.highlightMouseWorld == mouseWorld &&
		p.highlightStoreRevision == storeRevision {
		return
	}

	p.highlightedTargetID = p.targets.HighlightNearest(mouseWorld)
	p.highlightMouseWorld = mouseWorld
	p.highlightStoreRevision = storeRevision
	p.highlightQueryValid = true
}

func (p *ASDEXPane) clearHighlightedTarget() {
	if p == nil {
		return
	}

	if !p.highlightQueryValid && p.highlightedTargetID == "" {
		return
	}

	p.highlightedTargetID = ""
	p.highlightQueryValid = false
	p.targets.ClearHighlight()
}

func (p *ASDEXPane) highlightedTarget() *Target {
	if p == nil {
		return nil
	}
	if target := p.targets.HighlightedTarget(); target != nil {
		return target
	}
	return p.targets.TargetByID(p.highlightedTargetID)
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
		p.center = p.mapReposition.originalCenter
	}
	if p.mapRotate != nil {
		p.rotation = p.mapRotate.originalRotation
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

	p.rotation = normalizeRotation(float32(value))
	p.applyCommandStatus(CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	})
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
		kind := p.commandEntry.Kind()
		switch kind {
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
		if kind == CommandTextEntryLeaderLength {
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

func mapRepositionCursorCenter(ctx *panes.Context) redsmath.Vec2 {
	if ctx == nil {
		return redsmath.Vec2{}
	}
	size := ctx.PaneSize()
	return redsmath.Vec2{
		X: size.X * 0.5,
		Y: size.Y * 0.5,
	}
}

func (p *ASDEXPane) centerMapRepositionCursor(ctx *panes.Context) {
	if p == nil || p.mapReposition == nil || ctx == nil || ctx.Platform == nil {
		return
	}

	center := mapRepositionCursorCenter(ctx)
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

	center := mapRepositionCursorCenter(ctx)
	delta := mouse.Pos.Sub(center)
	if delta.X == 0 && delta.Y == 0 {
		return true
	}

	deltaWorld := transforms.WorldFromWindowV(delta)
	p.center = p.center.Sub(deltaWorld)

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
			entry.Selected = target.Highlighted
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
	if hit.Kind == CoastListHitEntry &&
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

	switch hit.Kind {
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

	rangeFeet := rangeFromHeight
	if rangeFromWidth > rangeFeet {
		rangeFeet = rangeFromWidth
	}

	p.center = redsmath.Vec2{
		X: (bounds.Min.X + bounds.Max.X) * 0.5,
		Y: (bounds.Min.Y + bounds.Max.Y) * 0.5,
	}
	p.rangeFeet = rangeFeet
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
		oldRange := p.rangeFeet
		p.rangeFeet = p.zoomedRange(mouse.Wheel.Y)
		newRange := p.rangeFeet

		if oldRange > 0 && newRange > 0 && newRange != oldRange {
			if ctx.Keyboard != nil && ctx.Keyboard.IsDown(platform.KeyAlt) {
				mouseWorld := transforms.WorldFromWindowP(mouse.Pos)
				scale := newRange / oldRange
				p.center = mouseWorld.Add(p.center.Sub(mouseWorld).Mul(scale))
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

func normalizeRotation(value float32) float32 {
	for value >= 360 {
		value -= 360
	}
	for value < 0 {
		value += 360
	}
	return value
}

func (p *ASDEXPane) zoomedRange(wheel float32) float32 {
	if p == nil {
		return 1
	}

	factor := float32(stdmath.Pow(1.12, float64(wheel)))
	if factor <= 0 {
		return p.rangeFeet
	}

	next := p.rangeFeet / factor
	return clamp(next, p.minRangeFeet(), p.maxRangeFeet())
}

func (p *ASDEXPane) minRangeFeet() float32 {
	return 500
}

func (p *ASDEXPane) maxRangeFeet() float32 {
	if p == nil || p.videomap == nil {
		return 100000
	}

	bounds := p.videomap.BoundsFeet()
	if bounds.Empty() {
		return 100000
	}

	maxDim := bounds.Width()
	if bounds.Height() > maxDim {
		maxDim = bounds.Height()
	}

	maxRange := maxDim * 10
	if maxRange < 2000 {
		maxRange = 2000
	}
	return maxRange
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
