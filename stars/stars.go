package stars

import (
	"log/slog"
	"time"

	"github.com/juliusplatzer/reds/cmd/wx"
	redslog "github.com/juliusplatzer/reds/log"
	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/radar"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	zBackground renderer.Z = -1000
	zWeather    renderer.Z = -950
)

// STARSPane is the STARS TCW/TDW display surface. Facility adaptation feeds
// its preference/view state; maps, targets, data blocks, and DCB controls all
// share the same geographic scope transformation as they are added.
type STARSPane struct {
	logger               *redslog.Logger
	config               selectedConfig
	prefs                Preferences
	longitudeScaleFactor float64
	colors               MonitorColors
	cursorTexture        renderer.TextureID
	useFontSetB          bool
	systemFont           *renderer.BitmapFont
	systemFontTextures   map[int]renderer.TextureID
	systemAltimeter      systemAltimeterState

	wxDomain              wx.Domain
	wxLogger              *redslog.Logger
	wxCenter              configPoint
	wxRadiusNM            float64
	wxStream              *wx.Stream
	wxGrid                *wx.Grid
	nexradGeneration      uint64
	nexradBuiltGeneration uint64
	nexrad                [6]starsNexradLevelCmdBuffers

	commandMode     CommandMode
	commandInput    string
	commandResponse string
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

	const useFontSetB = true
	pane := &STARSPane{
		logger:      logger,
		config:      cfg,
		prefs:       newPreferences(cfg),
		colors:      defaultTCWColors,
		useFontSetB: useFontSetB,
		systemFont:  newSystemFont(useFontSetB),
	}
	pane.longitudeScaleFactor = pane.initialLongitudeScaleFactor()
	pane.initializeSystemAltimeter(cfg.systemAltimeterAirport())
	pane.wxDomain = wx.DomainForARTCC(cfg.Facility.ARTCC)
	pane.wxLogger = logger.With(slog.String("component", "wx"))
	pane.restartNexradStream(starsInitialNexradRadiusNM)
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
	p.consumeNexradUpdates()
	p.ensureNexradCoverage(ctx)
	p.rebuildNexradIfNeeded()

	p.processKeyboardInput(ctx)
	transforms := p.scopeTransformations(ctx)
	p.consumeMouseEvents(ctx, transforms)

	p.drawNexrad(ctx, zcb, transforms)
	p.drawDCBBackground(ctx, zcb)
	p.drawPreviewArea(ctx, zcb)
	p.drawSSA(ctx, zcb)
	p.applyCursor(ctx)
	p.renderCursor(ctx, zcb)
}

func (p *STARSPane) Dispose() {
	if p == nil {
		return
	}
	if p.wxStream != nil {
		p.wxStream.Close()
		p.wxStream = nil
	}
	p.releaseNexradCmdBuffers()
	p.wxGrid = nil
}

func (p *STARSPane) scopeTransformations(ctx *panes.Context) radar.LatLonTransformations {
	if p == nil || ctx == nil {
		return radar.LatLonTransformations{}
	}
	center := p.currentCenter()
	paneExtent := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())

	// TI 6191.409 Rev. 30, 4.4.1 defines display range as the distance from
	// the display center to the nearest screen edge. GetLatLonTransformations
	// uses the pane's shorter dimension as the range reference, matching that
	// definition and VICE's STARS scope transformation convention.
	return radar.GetLatLonTransformations(
		paneExtent,
		center.Lat,
		center.Lon,
		p.longitudeScaleFactor,
		float64(p.currentPrefs().Range),
	)
}

func (p *STARSPane) consumeMouseEvents(ctx *panes.Context, transforms radar.LatLonTransformations) {
	if p == nil || ctx == nil || ctx.Mouse == nil || p.mouseOverDCB(ctx) {
		return
	}

	mouse := ctx.Mouse
	ps := p.currentPrefs()

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

	// The default map colors are yellow at TDWs. Brightness Category A Maps
	// - 1 is also yellow; Brightness Category B Maps - 1 remains dim gray.
	c.MapADefault = starsYellow
	c.MapBDefault = starsYellow
	c.MapA[0] = starsYellow

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
