package eram

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/juliusplatzer/reds/cmd/wx"
	redslog "github.com/juliusplatzer/reds/log"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util"
)

const (
	// CRC's BCG defaults. Background brightness is additionally scaled by
	// system brightness, whose ERAM backlight floor is 66 percent.
	defaultBackgroundBrightness = 26
	defaultSystemBrightness     = 90
	systemBrightnessFloor       = 66
	defaultToolbarBrightness    = 40
	defaultToolbarFontSize      = 1
	defaultToolbarVisible       = true

	zBackground              renderer.Z = -1000
	zNexrad                  renderer.Z = -900
	zMapData                 renderer.Z = -800
	zLoweredMasterToolbar    renderer.Z = -700
	zTimeViewSemiTransparent renderer.Z = -500
	// CRC keeps the default transparent checklist below TIME and the floating
	// tear-off buttons. Tear-offs start at -600 and their child menus are drawn
	// above that, so -610 preserves that ordering while keeping the checklist
	// above the lowered master toolbar at -700.
	zChecklistViewSemiTransparent renderer.Z = -610
	zTimeViewOpaque               renderer.Z = -400
	// Within CRC's opaque-view group TIME precedes CHECKLIST as well.
	zChecklistViewOpaque renderer.Z = -410
	zResponseAreaView    renderer.Z = 590
	zMCAView             renderer.Z = 600
	zViewSettingsMenu    renderer.Z = 800
	zViewMoveFrame       renderer.Z = 899

	minRangeNM              = 0.25
	maxRangeNM              = 1300
	defaultRangeNM          = 300
	initialWxRadiusNM       = 700
	wxPrefetchMarginNM      = 100
	wxRefreshMarginNM       = 50
	defaultNexradLevels     = 3
	defaultNexradBrightness = 50
)

// CRC StyleManager.SituationDisplayBackground uses EramColor.DarkBlue.
var defaultBackgroundColor = renderer.RGB8(0, 0, 212)

// CRC toolbar backgrounds use EramColor.Gray before BCG scaling.
var toolbarGray = renderer.RGB8(199, 199, 199)

var mrmsHTTPClient = &http.Client{Timeout: 20 * time.Second}

type LatLon struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type Sector struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	VisualCenter *LatLon `json:"visualCenter,omitempty"`
	Frequency    float64 `json:"freq"`
}

func (s Sector) Label() string {
	name := sectorDisplayName(s.ID, s.Name)
	if name == "" {
		return s.ID
	}
	return s.ID + " - " + name
}

type Facility struct {
	DefaultCenter           LatLon   `json:"defaultCenter"`
	Sectors                 []Sector `json:"sectors"`
	EmergencyChecklist      []string `json:"emergencyChecklist"`
	PositionReliefChecklist []string `json:"positionReliefChecklist"`
}

// ERAMPane is the controller-selected ERAM situation display. The first
// implementation intentionally owns only the persistent view center and the
// display defaults needed to draw the empty scope; maps and tracks build on
// this state later.
type ERAMPane struct {
	logger *redslog.Logger

	artcc  string
	sector Sector
	center LatLon

	longitudeScaleFactor float64

	backgroundBrightness      int
	systemBrightness          int
	buttonBrightness          int
	borderBrightness          int
	cursorBrightness          int
	cursorSize                int
	textBrightness            int
	toolbarBorderBrightness   int
	pairedTargetBrightness    int
	unpairedTargetBrightness  int
	fdbBrightness             int
	pairedHistoryBrightness   int
	unpairedHistoryBrightness int
	satCommBrightness         int
	ldbBrightness             int
	onFrequencyBrightness     int
	weatherBrightness         int
	fenceBrightness           int
	dbfelBrightness           int
	outageBrightness          int
	nonADSBrightness          int
	activeBorderBrightness    int
	toolbarVisible            bool
	toolbarBrightness         int
	toolbarFontSize           int
	toolbar                   toolbarState
	clock                     eramClockState
	mca                       eramMCAState
	responseArea              eramResponseAreaState
	checklist                 eramChecklistState
	viewUI                    eramViewUIState
	maps                      eramMapState

	rangeNM float64

	nexradLevels     int
	nexradBrightness int

	wxDomain   wx.Domain
	wxLogger   *redslog.Logger
	wxCenter   LatLon
	wxRadiusNM float64
	wxStream   *wx.Stream
	wxGrid     *wx.Grid

	nexradGeneration      uint64
	nexradBuiltGeneration uint64
	nexrad                nexradCmdBuffers

	cursors         CursorSet
	transientCursor eramTransientCursor
	audio           *eramAudioManager
	panDrag         *eramPanDrag
}

func LoadFacility(artcc string) (Facility, error) {
	artcc = strings.ToUpper(strings.TrimSpace(artcc))
	if artcc == "" {
		return Facility{}, fmt.Errorf("ERAM: empty ARTCC")
	}

	path := "resources/configs/eram/" + artcc + ".json"
	if !util.ResourceExists(path) {
		return Facility{}, fmt.Errorf("ERAM: facility config %s not found", path)
	}

	var facility Facility
	if err := json.Unmarshal(util.LoadResourceBytes(path), &facility); err != nil {
		return Facility{}, fmt.Errorf("ERAM: decode %s: %w", path, err)
	}

	return facility, nil
}

func NewPane(artcc string, sector Sector, logger *redslog.Logger) (*ERAMPane, error) {
	if logger == nil {
		logger = &redslog.Logger{
			Logger: slog.Default(),
			Start:  time.Now(),
		}
	}

	artcc = strings.ToUpper(strings.TrimSpace(artcc))
	if artcc == "" {
		return nil, fmt.Errorf("empty ERAM ARTCC")
	}

	facility, err := LoadFacility(artcc)
	if err != nil {
		return nil, err
	}

	center, source := initialCenter(facility, sector)
	pane := &ERAMPane{
		logger:                    logger,
		artcc:                     artcc,
		sector:                    sector,
		center:                    center,
		longitudeScaleFactor:      radar.LongitudeScaleFactorForLat(center.Lat),
		backgroundBrightness:      defaultBackgroundBrightness,
		systemBrightness:          defaultSystemBrightness,
		buttonBrightness:          defaultButtonBrightness,
		borderBrightness:          defaultBorderBrightness,
		cursorBrightness:          defaultCursorBrightness,
		cursorSize:                defaultCursorSize,
		textBrightness:            defaultTextBrightness,
		toolbarBorderBrightness:   defaultToolbarBorderBrightness,
		pairedTargetBrightness:    defaultPairedTargetBrightness,
		unpairedTargetBrightness:  defaultUnpairedTargetBrightness,
		fdbBrightness:             defaultFDBBrightness,
		pairedHistoryBrightness:   defaultPairedHistoryBrightness,
		unpairedHistoryBrightness: defaultUnpairedHistoryBrightness,
		satCommBrightness:         defaultSatCommBrightness,
		ldbBrightness:             defaultLDBBrightness,
		onFrequencyBrightness:     defaultOnFrequencyBrightness,
		weatherBrightness:         defaultWeatherBrightness,
		fenceBrightness:           defaultFenceBrightness,
		dbfelBrightness:           defaultDBFELBrightness,
		outageBrightness:          defaultOutageBrightness,
		nonADSBrightness:          defaultNonADSBrightness,
		activeBorderBrightness:    defaultActiveBorderBrightness,
		toolbarVisible:            defaultToolbarVisible,
		toolbarBrightness:         defaultToolbarBrightness,
		toolbarFontSize:           defaultToolbarFontSize,
		rangeNM:                   defaultRangeNM,
		nexradLevels:              defaultNexradLevels,
		nexradBrightness:          defaultNexradBrightness,
	}

	// CRC always ensures one special TOOLBAR control tear-off exists at
	// TopLeft (90, 71). It remains present even when MASTER TOOLBAR is hidden.
	pane.audio = newERAMAudioManager(logger.With(slog.String("component", "audio")))
	pane.initializeToolbarState()
	pane.initializeClockState()
	pane.initializeAreaStates()
	pane.initializeChecklistState(facility)
	pane.initializeMapState()
	if err := pane.loadGeoMapMetadata(); err != nil {
		return nil, err
	}
	if err := pane.loadActiveGeoMapGeometry(); err != nil {
		return nil, err
	}

	pane.wxDomain = wx.DomainForARTCC(artcc)
	pane.wxLogger = logger.With(slog.String("component", "wx"))
	pane.restartWxStream(initialWxRadiusNM)

	logger.Info(
		"ERAM pane initialized",
		slog.Float64("center_lat", center.Lat),
		slog.Float64("center_lon", center.Lon),
		slog.String("center_source", source),
		slog.String("wx_domain", string(pane.wxDomain)),
	)
	return pane, nil
}

func (p *ERAMPane) Draw(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil {
		return
	}

	p.consumeWxUpdates()
	p.consumeInput(ctx)
	p.ensureWxCoverage(ctx)
	p.rebuildNexradIfNeeded()

	p.ensureCursorLoaded()
	p.applyCursor(ctx)

	x, y, width, height := ctx.PaneFramebufferRect()
	backgroundCB := zcb.At(zBackground)
	backgroundCB.Viewport(x, y, width, height)
	backgroundCB.Scissor(x, y, width, height)
	backgroundCB.ClearRGB(p.scopeBackgroundColor())
	backgroundCB.DisableScissor()

	p.drawNexrad(ctx, zcb)
	p.drawGeoMaps(ctx, zcb)
	p.drawToolbar(ctx, zcb)
	p.drawClock(ctx, zcb)
	p.drawChecklist(ctx, zcb)
	p.drawResponseArea(ctx, zcb)
	p.drawMCA(ctx, zcb)
	p.drawViewSettingsMenu(ctx, zcb)
	p.drawViewMoveFrame(ctx, zcb)
	p.renderCursor(ctx, zcb)
}

func (p *ERAMPane) Dispose() {
	if p == nil {
		return
	}
	if p.wxStream != nil {
		p.wxStream.Close()
		p.wxStream = nil
	}
	p.releaseNexradCmdBuffers()
	p.releaseGeoMapCmdBuffers()
	p.wxGrid = nil
}

func initialCenter(facility Facility, sector Sector) (LatLon, string) {
	if sector.VisualCenter != nil {
		return *sector.VisualCenter, "visualCenter"
	}
	return facility.DefaultCenter, "defaultCenter"
}

func (p *ERAMPane) scopeBackgroundColor() renderer.RGB {
	if p == nil {
		return renderer.RGB{}
	}
	return applyERAMBrightness(
		defaultBackgroundColor,
		p.backgroundBrightness,
		p.systemBrightness,
	)
}

// applyERAMBrightness mirrors CRC Style.Calculate: a BCG is first reduced by
// the system-brightness backlight factor, truncated to an integer percentage,
// and then applied to the raw ERAM color.
func applyERAMBrightness(color renderer.RGB, brightness, systemBrightness int) renderer.RGB {
	brightness = clampBrightness(brightness)
	systemBrightness = clampBrightness(systemBrightness)

	systemScale := (float32(systemBrightnessFloor) +
		float32(systemBrightness)*float32(100-systemBrightnessFloor)/100) / 100
	effectiveBrightness := int(float32(brightness) * systemScale)
	scale := float32(effectiveBrightness) / 100

	return renderer.RGB{
		R: truncateBrightnessComponent(color.R, scale),
		G: truncateBrightnessComponent(color.G, scale),
		B: truncateBrightnessComponent(color.B, scale),
	}
}

func truncateBrightnessComponent(component, scale float32) float32 {
	value := int(component * 255 * scale)
	if value < 0 {
		value = 0
	}
	if value > 255 {
		value = 255
	}
	return float32(value) / 255
}

func clampBrightness(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func sectorDisplayName(id, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	ids := sectorIDDisplayForms(id)
	for _, value := range ids {
		quoted := regexp.QuoteMeta(value)

		// Common ERAM names include redundant parenthesized IDs such as
		// "(02) WICHITA HI" or "Sector 2 (R2)".
		name = regexp.MustCompile(`\(\s*[[:alpha:]]*`+quoted+`\s*\)`).ReplaceAllString(name, "")

		// Remove the ID when it appears as its own numeric token at the
		// beginning, middle, or end of the name.
		token := regexp.MustCompile(`(^|[^[:alnum:]])` + quoted + `([^[:alnum:]]|$)`)
		for {
			next := token.ReplaceAllString(name, "${1}${2}")
			if next == name {
				break
			}
			name = next
		}
	}

	name = strings.ReplaceAll(name, "_", " ")
	name = strings.Join(strings.Fields(name), " ")
	return strings.Trim(name, " -_")
}

func sectorIDDisplayForms(id string) []string {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}

	out := []string{id}
	trimmed := strings.TrimLeft(id, "0")
	if trimmed == "" {
		trimmed = "0"
	}
	if trimmed != id {
		out = append(out, trimmed)
	}
	return out
}
