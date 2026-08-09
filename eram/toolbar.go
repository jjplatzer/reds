package eram

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/juliusplatzer/reds/eram/assets"
	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"
)

const (
	toolbarTextYPadding      = 3
	toolbarButtonBorderWidth = 1
	toolbarButtonRowGap      = 2
	toolbarButtonColumnGap   = 2
	toolbarWrapperYPadding   = 3
	toolbarWrapperXPadding   = 2
	toolbarInteriorLineWidth = 1
	toolbarLabelWidthChars   = 7
	toolbarLabelAddPixels    = 9
	toolbarControlFontSize   = 2
	toolbarControlAddPixels  = -1
	toolbarMoveWidthChars    = 1.5
	toolbarExpansionBorder   = 2

	defaultButtonBrightness          = 80
	defaultBorderBrightness          = 56
	defaultCursorBrightness          = 100
	defaultTextBrightness            = 90
	defaultToolbarBorderBrightness   = 50
	defaultPairedTargetBrightness    = 92
	defaultUnpairedTargetBrightness  = 92
	defaultFDBBrightness             = 90
	defaultPairedHistoryBrightness   = 16
	defaultUnpairedHistoryBrightness = 16
	defaultSatCommBrightness         = 90
	defaultLDBBrightness             = 60
	defaultOnFrequencyBrightness     = 90
	defaultWeatherBrightness         = 50
	defaultFenceBrightness           = 90
	defaultDBFELBrightness           = 80
	defaultOutageBrightness          = 80
	defaultNonADSBrightness          = 90
	defaultActiveBorderBrightness    = 56

	toolbarRepeatInitialDelay = 350 * time.Millisecond
	toolbarRepeatDelay        = 80 * time.Millisecond

	zTearoffButtons  renderer.Z = -600
	zButtonMoveFrame renderer.Z = 900
)

// CRC ERAM color names and raw values used by the toolbar.
var (
	toolbarWhite       = renderer.RGB8(243, 243, 243) // EramColor.White
	toolbarLightGray   = renderer.RGB8(210, 210, 210) // EramColor.LightGray
	toolbarBlue        = renderer.RGB8(0, 0, 212)     // EramColor.Blue
	toolbarBurntCoral  = renderer.RGB8(220, 160, 155) // EramColor.BurntCoral
	toolbarTeal        = renderer.RGB8(0, 201, 212)   // EramColor.Teal
	toolbarIncDecGreen = renderer.RGB8(0, 205, 0)     // EramColor.IncDecGreen
	toolbarBrightGold  = renderer.RGB8(255, 255, 161) // EramColor.BrightGold
	// BrightCoral's palette value (255,140,0) is one of CRC's gamma-corrected
	// colors; EramColor.GetColor therefore supplies (255,194,0) to the style.
	toolbarBrightCoral = renderer.RGB8(255, 194, 0) // EramColor.BrightCoral
	toolbarBlack       = renderer.RGB8(0, 0, 0)     // EramColor.Black
)

type toolbarButtonKind uint8

const (
	toolbarToggleButton toolbarButtonKind = iota
	toolbarMenuButton
	toolbarCommandButton
	toolbarIncDecButton
	toolbarPressHoldButton
)

type toolbarPickAction uint8

const (
	toolbarSelect toolbarPickAction = iota
	toolbarEnter
)

type toolbarButtonID string

const (
	toolbarDraw       toolbarButtonID = "draw"
	toolbarViews      toolbarButtonID = "views"
	toolbarATCTools   toolbarButtonID = "atc-tools"
	toolbarCheckLists toolbarButtonID = "check-lists"
	toolbarABSetting  toolbarButtonID = "ab-setting"
	toolbarCommands   toolbarButtonID = "command-menus"
	toolbarRange      toolbarButtonID = "range"
	toolbarGeomap     toolbarButtonID = "geomap"
	toolbarCursor     toolbarButtonID = "cursor"
	toolbarAltLimits  toolbarButtonID = "alt-limits"
	toolbarBrightness toolbarButtonID = "brightness"
	toolbarRadar      toolbarButtonID = "radar-filter"
	toolbarFont       toolbarButtonID = "font"
	toolbarPrefset    toolbarButtonID = "prefset"
	toolbarDBFields   toolbarButtonID = "db-fields"
	toolbarDelete     toolbarButtonID = "delete-tearoff"
	toolbarVector     toolbarButtonID = "vector"

	toolbarWeather       toolbarButtonID = "weather"
	toolbarMapBrightness toolbarButtonID = "map-brightness"

	// CRC keeps a special TOOLBAR menu torn off by default. Its submenu
	// controls visibility/precedence of the available toolbar families.
	toolbarControlMenu       toolbarButtonID = "toolbar-control-menu"
	toolbarMasterDisplay     toolbarButtonID = "master-toolbar-display"
	toolbarMasterRaiseLower  toolbarButtonID = "master-toolbar-raise-lower"
	toolbarMCADisplay        toolbarButtonID = "mca-toolbar-display"
	toolbarMCARaiseLower     toolbarButtonID = "mca-toolbar-raise-lower"
	toolbarHorizontalDisplay toolbarButtonID = "horizontal-toolbar-display"
	toolbarHorizontalRaise   toolbarButtonID = "horizontal-toolbar-raise-lower"
	toolbarLeftDisplay       toolbarButtonID = "left-toolbar-display"
	toolbarLeftRaiseLower    toolbarButtonID = "left-toolbar-raise-lower"
	toolbarRightDisplay      toolbarButtonID = "right-toolbar-display"
	toolbarRightRaiseLower   toolbarButtonID = "right-toolbar-raise-lower"
)

type toolbarButtonSpec struct {
	ID        toolbarButtonID
	Lines     [2]string
	Kind      toolbarButtonKind
	NoControl bool
	Active    bool
	Disabled  bool
}

type toolbarMenuEntry struct {
	ID     toolbarButtonID
	Row    int
	Column int
}

type toolbarExpansion struct {
	Root  toolbarButtonID
	Child toolbarButtonID
}

type toolbarAnchor uint8

const (
	toolbarAnchorTopLeft toolbarAnchor = iota
	toolbarAnchorTopRight
	toolbarAnchorBottomLeft
	toolbarAnchorBottomRight
)

type toolbarTearoff struct {
	ID        int
	Type      toolbarButtonID
	Anchor    toolbarAnchor
	Offset    redsmath.Vec2
	Expansion toolbarExpansion
}

type toolbarTearoffMove struct {
	Type       toolbarButtonID
	ExistingID int
	Position   redsmath.Vec2
	Size       redsmath.Vec2
}

type toolbarOwnerKind uint8

const (
	toolbarOwnerMaster toolbarOwnerKind = iota
	toolbarOwnerTearoff
)

type toolbarOwner struct {
	Kind      toolbarOwnerKind
	TearoffID int
}

type toolbarButtonLayout struct {
	Spec    toolbarButtonSpec
	Owner   toolbarOwner
	Depth   int
	Row     int
	Root    redsmath.Rect
	Control redsmath.Rect
	Pick    redsmath.Rect
}

type toolbarExpansionLayout struct {
	Owner          toolbarOwner
	Depth          int
	Bounds         redsmath.Rect
	SuppressBorder bool
}

type toolbarRepeat struct {
	ID          toolbarButtonID
	Action      toolbarPickAction
	MouseButton platform.MouseButton
	Next        time.Time
}

// The slices are retained and reused every frame. The toolbar can contain a
// large BRIGHT menu, so this avoids a steady stream of short-lived layouts.
type toolbarLayoutScratch struct {
	buttons    []toolbarButtonLayout
	expansions []toolbarExpansionLayout
}

type toolbarState struct {
	font     *renderer.BitmapFont
	textures map[int]renderer.TextureID

	masterExpansion toolbarExpansion
	tearoffs        []toolbarTearoff
	nextTearoffID   int
	moving          *toolbarTearoffMove
	repeat          *toolbarRepeat

	layout toolbarLayoutScratch
}

type toolbarMetrics struct {
	fontSize      int
	charAdvance   int
	lineHeight    int
	buttonHeight  float32
	labelWidth    float32
	controlWidth  float32
	moveWidth     float32
	toolbarHeight float32
}

var masterToolbarEntries = []toolbarMenuEntry{
	{toolbarDraw, 0, 0}, {toolbarViews, 1, 0},
	{toolbarATCTools, 0, 1}, {toolbarCheckLists, 1, 1},
	{toolbarABSetting, 0, 2}, {toolbarCommands, 1, 2},
	{toolbarRange, 0, 3}, {toolbarGeomap, 1, 3},
	{toolbarCursor, 0, 4}, {toolbarAltLimits, 1, 4},
	{toolbarBrightness, 0, 5}, {toolbarRadar, 1, 5},
	{toolbarFont, 0, 6}, {toolbarPrefset, 1, 6},
	{toolbarDBFields, 0, 7}, {toolbarDelete, 1, 7},
	{toolbarVector, 0, 8},
}

var viewsToolbarEntries = []toolbarMenuEntry{
	{"as-view", 0, 0}, {"departure-view", 1, 0},
	{"ahi-view", 0, 1}, {"flight-event-view", 1, 1},
	{"cfr-view", 0, 2}, {"group-sup-view", 1, 2},
	{"code-view", 0, 3}, {"hold-view", 1, 3},
	{"ca-view", 0, 4}, {"inbound-view", 1, 4},
	{"saved-advisories-view", 0, 5}, {"mrp-view", 1, 5},
	{"message-history-view", 0, 6}, {"saa-filter-view", 1, 6},
	{"message-out-view", 0, 7}, {"dcrd-view", 1, 7},
	{"toc-settings-view", 0, 8}, {"wx-view", 1, 8},
	{"crr-view", 0, 9},
}

var atcToolsToolbarEntries = []toolbarMenuEntry{
	{"crr-fix", 0, 0},
	{"speed-advisory", 1, 1},
	{toolbarWeather, 0, 2},
}

var weatherToolbarEntries = []toolbarMenuEntry{
	{"nexrad-strata", 0, 0}, {"wx-low", 1, 0},
	{"nexrad-intensity", 0, 1}, {"wx-medium", 1, 1},
	{"wx-high", 1, 2},
}

var checkListsToolbarEntries = []toolbarMenuEntry{
	{"position-check", 0, 0},
	{"emergency-check", 0, 1},
}

var cursorToolbarEntries = []toolbarMenuEntry{
	{"cursor-speed", 0, 0},
	{"cursor-size", 0, 1},
	{"audio-volume", 0, 2},
}

var brightnessToolbarEntries = []toolbarMenuEntry{
	{toolbarMapBrightness, 0, 0}, {"backlight-brightness", 1, 0},
	{"cpdlc-brightness", 0, 1}, {"button-brightness", 1, 1},
	{"background-brightness", 0, 2}, {"border-brightness", 1, 2},
	{"cursor-brightness", 0, 3}, {"toolbar-brightness", 1, 3},
	{"text-brightness", 0, 4}, {"toolbar-border-brightness", 1, 4},
	{"paired-target-brightness", 0, 5}, {"sd-border-brightness", 1, 5},
	{"unpaired-target-brightness", 0, 6}, {"fdb-brightness", 1, 6},
	{"paired-history-brightness", 0, 7}, {"portal-brightness", 1, 7},
	{"unpaired-history-brightness", 0, 8}, {"satcomm-brightness", 1, 8},
	{"ldb-brightness", 0, 9}, {"on-frequency-brightness", 1, 9},
	{"select-ldb-brightness", 0, 10}, {"line4-brightness", 1, 10},
	{"weather-brightness", 0, 11}, {"dwell-brightness", 1, 11},
	{"nexrad-brightness", 0, 12}, {"fence-brightness", 1, 12},
	{"dbfel-brightness", 1, 13},
	{"outage-brightness", 1, 14},
}

var radarToolbarEntries = []toolbarMenuEntry{
	{"all-ldbs", 0, 0}, {"select-beacon", 1, 0},
	{"paired-ldb", 0, 1}, {"permanent-echo", 1, 1},
	{"unpaired-ldb", 0, 2}, {"strobe-lines", 1, 2},
	{"all-primary", 0, 3}, {"history", 1, 3},
	{"non-mode-c", 0, 4},
}

var fontToolbarEntries = []toolbarMenuEntry{
	{"font-line4", 0, 0}, {"font-rdb", 1, 0},
	{"font-fdb", 0, 1}, {"font-ldb", 1, 1},
	{"font-portal", 0, 2}, {"font-outage", 1, 2},
	{"font-toolbar", 0, 3},
}

var dbFieldsToolbarEntries = []toolbarMenuEntry{
	{"db-non-rvsm", 0, 0}, {"db-non-adsb", 1, 0},
	{"db-vri", 0, 1}, {"non-adsb-brightness", 1, 1},
	{"db-code", 0, 2}, {"db-satcomm", 1, 2},
	{"db-speed", 0, 3}, {"db-tfm-reroute", 1, 3},
	{"db-destination", 0, 4}, {"db-crr-rdb", 1, 4},
	{"db-aircraft-type", 0, 5}, {"db-sta-rdb", 1, 5},
	{"db-leader", 0, 6}, {"db-delay-rdb", 1, 6},
	{"db-bcast-flid", 0, 7}, {"db-delay-format", 1, 7},
	{"db-portal-fence", 0, 8},
}

// CRC ToolbarControlMenu: five columns, display toggle on row 0 and the
// corresponding raise/lower control immediately below it on row 1.
var toolbarControlEntries = []toolbarMenuEntry{
	{toolbarMasterDisplay, 0, 0}, {toolbarMasterRaiseLower, 1, 0},
	{toolbarMCADisplay, 0, 1}, {toolbarMCARaiseLower, 1, 1},
	{toolbarHorizontalDisplay, 0, 2}, {toolbarHorizontalRaise, 1, 2},
	{toolbarLeftDisplay, 0, 3}, {toolbarLeftRaiseLower, 1, 3},
	{toolbarRightDisplay, 0, 4}, {toolbarRightRaiseLower, 1, 4},
}

var (
	geomapToolbarEntries        = makeNumberedToolbarEntries("map-filter", 40)
	mapBrightnessToolbarEntries = makeNumberedToolbarEntries("map-bcg", 40)
)

func makeNumberedToolbarEntries(prefix string, count int) []toolbarMenuEntry {
	entries := make([]toolbarMenuEntry, 0, count)
	for i := 0; i < count; i++ {
		entries = append(entries, toolbarMenuEntry{
			ID:     toolbarButtonID(fmt.Sprintf("%s-%02d", prefix, i+1)),
			Row:    (i / 10) % 2,
			Column: i % 10,
		})
	}
	return entries
}

func toolbarHeight(lineHeight int) int {
	buttonHeight := 2*(lineHeight+2*toolbarTextYPadding) + 2*toolbarButtonBorderWidth
	return 2*buttonHeight + toolbarButtonRowGap + 2*toolbarWrapperYPadding + toolbarInteriorLineWidth
}

func (p *ERAMPane) initializeToolbarState() {
	if p == nil {
		return
	}

	// EramDisplayContext in CRC guarantees this special tear-off exists. Its
	// initial location is AnchoredLocation(Point(90, 71), Anchor.TopLeft).
	p.toolbar.nextTearoffID = 1
	p.toolbar.tearoffs = append(p.toolbar.tearoffs, toolbarTearoff{
		ID:     1,
		Type:   toolbarControlMenu,
		Anchor: toolbarAnchorTopLeft,
		Offset: redsmath.Vec2{X: 90, Y: 71},
	})
}

func (p *ERAMPane) toolbarLineHeight() int {
	if p != nil {
		if font, ok := assets.EramTextFont(p.toolbarFontSize); ok && font != nil && font.Height > 0 {
			return font.Height
		}
	}
	if font, ok := assets.EramTextFont(defaultToolbarFontSize); ok && font != nil && font.Height > 0 {
		return font.Height
	}
	return 9
}

func (p *ERAMPane) toolbarMetrics() toolbarMetrics {
	p.ensureToolbarFont()
	fontSize := p.toolbarFontSize
	charAdvance, lineHeight := 10, p.toolbarLineHeight()
	controlAdvance := 12
	if p.toolbar.font != nil {
		if w, h := p.toolbar.font.CharSize(fontSize); w > 0 && h > 0 {
			charAdvance, lineHeight = w, h
		}
		if w, _ := p.toolbar.font.CharSize(toolbarControlFontSize); w > 0 {
			controlAdvance = w
		}
	}
	buttonHeight := 2*(lineHeight+2*toolbarTextYPadding) + 2*toolbarButtonBorderWidth
	return toolbarMetrics{
		fontSize:      fontSize,
		charAdvance:   charAdvance,
		lineHeight:    lineHeight,
		buttonHeight:  float32(buttonHeight),
		labelWidth:    float32(toolbarLabelWidthChars*charAdvance + toolbarLabelAddPixels + 2*toolbarButtonBorderWidth),
		controlWidth:  float32(controlAdvance + toolbarControlAddPixels + 2*toolbarButtonBorderWidth),
		moveWidth:     float32(int(toolbarMoveWidthChars*float64(charAdvance)) + 2*toolbarButtonBorderWidth),
		toolbarHeight: float32(toolbarHeight(lineHeight)),
	}
}

func (p *ERAMPane) ensureToolbarFont() {
	if p.toolbar.font == nil {
		p.toolbar.font = renderer.NewBitmapFontFromMono(assets.EramTextFonts)
	}
	if p.toolbar.textures == nil {
		p.toolbar.textures = make(map[int]renderer.TextureID)
	}
}

func (p *ERAMPane) toolbarTexture(r renderer.Renderer, size int) renderer.TextureID {
	p.ensureToolbarFont()
	if r == nil || p.toolbar.font == nil {
		return 0
	}
	if texture := p.toolbar.textures[size]; texture != 0 {
		return texture
	}
	fs := p.toolbar.font.Size(size)
	if fs == nil {
		return 0
	}
	texture := r.CreateTextureR8(fs.AtlasWidth, fs.AtlasHeight, fs.AtlasR8, true)
	if texture != 0 {
		p.toolbar.textures[size] = texture
	}
	return texture
}

func baseToolbarSpec(id toolbarButtonID) toolbarButtonSpec {
	spec := toolbarButtonSpec{ID: id, Kind: toolbarToggleButton}
	switch id {
	case toolbarDraw:
		spec.Lines, spec.Kind = [2]string{"DRAW", ""}, toolbarMenuButton
	case toolbarViews:
		spec.Lines, spec.Kind = [2]string{"VIEWS", ""}, toolbarMenuButton
	case toolbarATCTools:
		spec.Lines, spec.Kind = [2]string{"ATC", "TOOLS"}, toolbarMenuButton
	case toolbarCheckLists:
		spec.Lines, spec.Kind = [2]string{"CHECK", "LISTS"}, toolbarMenuButton
	case toolbarABSetting:
		spec.Lines, spec.Kind = [2]string{"AB", "SETTING"}, toolbarMenuButton
	case toolbarCommands:
		spec.Lines, spec.Kind = [2]string{"COMMAND", "MENUS"}, toolbarMenuButton
	case toolbarRange:
		spec.Lines = [2]string{"RANGE", "300"}
	case toolbarGeomap:
		// CRC obtains both lines from the active facility geomap. REDS does not
		// yet retain that menu metadata, so the correct fallback is blank.
		spec.Kind = toolbarMenuButton
	case toolbarCursor:
		spec.Lines, spec.Kind = [2]string{"CURSOR", ""}, toolbarMenuButton
	case toolbarAltLimits:
		spec.Lines = [2]string{"ALT LIM", "000B999"}
	case toolbarBrightness:
		spec.Lines, spec.Kind = [2]string{"BRIGHT", ""}, toolbarMenuButton
	case toolbarRadar:
		spec.Lines, spec.Kind = [2]string{"RADAR", "FILTER"}, toolbarMenuButton
	case toolbarFont:
		spec.Lines, spec.Kind = [2]string{"FONT", ""}, toolbarMenuButton
	case toolbarPrefset:
		spec.Lines, spec.Kind = [2]string{"PREFSET", ""}, toolbarMenuButton
	case toolbarDBFields:
		spec.Lines, spec.Kind = [2]string{"DB", "FIELDS"}, toolbarMenuButton
	case toolbarDelete:
		spec.Lines, spec.Kind = [2]string{"DELETE", "TEAROFF"}, toolbarCommandButton
	case toolbarVector:
		spec.Lines, spec.Kind = [2]string{"VECTOR", "0"}, toolbarIncDecButton
	case toolbarWeather:
		spec.Lines, spec.Kind = [2]string{"WX", ""}, toolbarMenuButton
	case toolbarMapBrightness:
		spec.Lines, spec.Kind = [2]string{"MAP", "BRIGHT"}, toolbarMenuButton
	case toolbarControlMenu:
		spec.Lines, spec.Kind = [2]string{"TOOLBAR", ""}, toolbarMenuButton
	case toolbarMasterDisplay:
		spec.Lines = [2]string{"MASTER", "TOOLBAR"}
	case toolbarMasterRaiseLower:
		spec.Lines = [2]string{"MASTER", "RAISE"}
	case toolbarMCADisplay:
		spec.Lines = [2]string{"MCA", "TOOLBAR"}
	case toolbarMCARaiseLower:
		spec.Lines = [2]string{"MCA", "RAISE"}
	case toolbarHorizontalDisplay:
		spec.Lines = [2]string{"HORIZ", "TOOLBAR"}
	case toolbarHorizontalRaise:
		spec.Lines = [2]string{"HORIZ", "RAISE"}
	case toolbarLeftDisplay:
		spec.Lines = [2]string{"LEFT", "TOOLBAR"}
	case toolbarLeftRaiseLower:
		spec.Lines = [2]string{"LEFT", "RAISE"}
	case toolbarRightDisplay:
		spec.Lines = [2]string{"RIGHT", "TOOLBAR"}
	case toolbarRightRaiseLower:
		spec.Lines = [2]string{"RIGHT", "RAISE"}

	case "as-view":
		spec.Lines = [2]string{"ALTIM", "SET"}
	case "departure-view":
		spec.Lines = [2]string{"DEPT", "LIST"}
	case "ahi-view":
		spec.Lines = [2]string{"AUTO HO", "INHIB"}
	case "flight-event-view":
		spec.Lines = [2]string{"FLIGHT", "EVENT"}
	case "cfr-view":
		spec.Lines = [2]string{"CFR", ""}
	case "group-sup-view":
		spec.Lines = [2]string{"GROUP", "SUP"}
	case "code-view":
		spec.Lines = [2]string{"CODE", ""}
	case "hold-view":
		spec.Lines = [2]string{"HOLD", "LIST"}
	case "ca-view":
		spec.Lines = [2]string{"CONFLCT", "ALERT"}
	case "inbound-view":
		spec.Lines = [2]string{"INBND", "LIST"}
	case "saved-advisories-view":
		spec.Lines = [2]string{"CPDLC", "ADV"}
	case "mrp-view":
		spec.Lines = [2]string{"MRP", "LIST"}
	case "message-history-view":
		spec.Lines = [2]string{"CPDLC", "HIST"}
	case "saa-filter-view":
		spec.Lines = [2]string{"SAA", "FILTER"}
	case "message-out-view":
		spec.Lines = [2]string{"CPDLC", "MSGOUT"}
	case "dcrd-view":
		spec.Lines = [2]string{"UA", ""}
	case "toc-settings-view":
		spec.Lines = [2]string{"CPDLC", "TOC SET"}
	case "wx-view":
		spec.Lines = [2]string{"WX", "REPORT"}
	case "crr-view":
		spec.Lines = [2]string{"CRR", ""}

	case "crr-fix":
		spec.Lines = [2]string{"CRR", "FIX"}
	case "speed-advisory":
		spec.Lines = [2]string{"SPEED", "ADVSRY"}
	case "nexrad-strata":
		spec.Lines, spec.Kind, spec.NoControl = [2]string{"NX 000", "600"}, toolbarIncDecButton, true
	case "nexrad-intensity":
		spec.Lines, spec.Kind, spec.NoControl = [2]string{"NX LVL", "123"}, toolbarIncDecButton, true
	case "wx-low":
		spec.Lines, spec.NoControl = [2]string{"WX1", ""}, true
	case "wx-medium":
		spec.Lines, spec.NoControl = [2]string{"WX2", ""}, true
	case "wx-high":
		spec.Lines, spec.NoControl = [2]string{"WX3", ""}, true
	case "position-check":
		spec.Lines = [2]string{"POS", "CHECK"}
	case "emergency-check":
		spec.Lines = [2]string{"EMERG", "CHECK"}
	case "cursor-speed":
		spec.Lines, spec.Kind, spec.NoControl = [2]string{"SPEED", "1"}, toolbarIncDecButton, true
	case "cursor-size":
		spec.Lines, spec.Kind, spec.NoControl = [2]string{"SIZE", ""}, toolbarIncDecButton, true
	case "audio-volume":
		spec.Lines, spec.Kind, spec.NoControl = [2]string{"VOLUME", "5"}, toolbarIncDecButton, true

	case "backlight-brightness":
		spec = brightnessSpec(id, "BCKLGHT")
	case "cpdlc-brightness":
		spec.Lines, spec.Kind = [2]string{"CPDLC", ""}, toolbarMenuButton
	case "button-brightness":
		spec = brightnessSpec(id, "BUTTON")
	case "background-brightness":
		spec = brightnessSpec(id, "BCKGRD")
	case "border-brightness":
		spec = brightnessSpec(id, "BORDER")
	case "cursor-brightness":
		spec = brightnessSpec(id, "CURSOR")
	case "toolbar-brightness":
		spec = brightnessSpec(id, "TOOLBAR")
	case "text-brightness":
		spec = brightnessSpec(id, "TEXT")
	case "toolbar-border-brightness":
		spec = brightnessSpec(id, "TB BRDR")
	case "paired-target-brightness":
		spec = brightnessSpec(id, "PR TGT")
	case "sd-border-brightness":
		spec = brightnessSpec(id, "AB BRDR")
	case "unpaired-target-brightness":
		spec = brightnessSpec(id, "UNP TGT")
	case "fdb-brightness":
		spec = brightnessSpec(id, "FDB")
	case "paired-history-brightness":
		spec = brightnessSpec(id, "PR HST")
	case "portal-brightness":
		spec = incDecSpec(id, "PORTAL", "=")
	case "unpaired-history-brightness":
		spec = brightnessSpec(id, "UNP HST")
	case "satcomm-brightness":
		spec = brightnessSpec(id, "SATCOMM")
	case "ldb-brightness":
		spec = brightnessSpec(id, "LDB")
	case "on-frequency-brightness":
		spec = brightnessSpec(id, "ON-FREQ")
	case "select-ldb-brightness":
		spec = incDecSpec(id, "SLDB", "+5")
	case "line4-brightness":
		spec = incDecSpec(id, "LINE 4", "=")
	case "weather-brightness":
		spec = brightnessSpec(id, "WX")
	case "dwell-brightness":
		spec = incDecSpec(id, "DWELL", "+20")
	case "nexrad-brightness":
		spec = brightnessSpec(id, "NEXRAD")
	case "fence-brightness":
		spec = brightnessSpec(id, "FENCE")
	case "dbfel-brightness":
		spec = brightnessSpec(id, "DBFEL")
	case "outage-brightness":
		spec = brightnessSpec(id, "OUTAGE")

	case "all-ldbs":
		spec.Lines = [2]string{"ALL", "LDBS"}
	case "select-beacon":
		spec.Lines = [2]string{"SELECT", "BEACON"}
	case "paired-ldb":
		spec.Lines = [2]string{"PR", "LDB"}
	case "permanent-echo":
		spec.Lines = [2]string{"PERM", "ECHO"}
	case "unpaired-ldb":
		spec.Lines = [2]string{"UNP", "LDB"}
	case "strobe-lines":
		spec.Lines = [2]string{"STROBE", "LINES"}
	case "all-primary":
		spec.Lines = [2]string{"ALL", "PRIM"}
	case "history":
		spec.Lines, spec.Kind = [2]string{"HISTORY", "5"}, toolbarIncDecButton
	case "non-mode-c":
		spec.Lines = [2]string{"NON", "MODE-C"}

	case "font-line4":
		spec.Lines, spec.Kind = [2]string{"LINE4", "="}, toolbarIncDecButton
	case "font-rdb":
		spec.Lines, spec.Kind = [2]string{"RDB", "1"}, toolbarIncDecButton
	case "font-fdb":
		spec.Lines, spec.Kind = [2]string{"FDB", "1"}, toolbarIncDecButton
	case "font-ldb":
		spec.Lines, spec.Kind = [2]string{"LDB", "1"}, toolbarIncDecButton
	case "font-portal":
		spec.Lines, spec.Kind = [2]string{"PORTAL", "="}, toolbarIncDecButton
	case "font-outage":
		spec.Lines, spec.Kind = [2]string{"OUTAGE", "1"}, toolbarIncDecButton
	case "font-toolbar":
		spec.Lines, spec.Kind = [2]string{"TOOLBAR", "1"}, toolbarIncDecButton

	case "db-non-rvsm":
		spec.Lines, spec.Active, spec.Disabled = [2]string{"NON-", "RVSM"}, true, true
	case "db-non-adsb":
		spec.Lines, spec.Active = [2]string{"NON-", "ADSB"}, true
	case "db-vri":
		spec.Lines, spec.Kind = [2]string{"VRI", ""}, toolbarPressHoldButton
	case "non-adsb-brightness":
		spec = brightnessSpec(id, "NONADSB")
	case "db-code":
		spec.Lines, spec.Kind = [2]string{"CODE", ""}, toolbarPressHoldButton
	case "db-satcomm":
		spec.Lines = [2]string{"SAT", "COMM"}
	case "db-speed":
		spec.Lines, spec.Kind = [2]string{"SPEED", ""}, toolbarPressHoldButton
	case "db-tfm-reroute":
		spec.Lines, spec.Active = [2]string{"TFM", "REROUTE"}, true
	case "db-destination":
		spec.Lines = [2]string{"DEST", ""}
	case "db-crr-rdb":
		spec.Lines, spec.Active = [2]string{"CRR", "RDB"}, true
	case "db-aircraft-type":
		spec.Lines = [2]string{"TYPE", ""}
	case "db-sta-rdb":
		spec.Lines = [2]string{"STA", "RDB"}
	case "db-leader":
		spec.Lines, spec.Kind = [2]string{"FDB LDR", "1"}, toolbarIncDecButton
	case "db-delay-rdb":
		spec.Lines = [2]string{"DELAY", "RDB"}
	case "db-bcast-flid":
		spec.Lines, spec.Active = [2]string{"BCAST", "FLID"}, true
	case "db-delay-format":
		spec.Lines, spec.Kind = [2]string{"DELAY", "FORMAT"}, toolbarPressHoldButton
	case "db-portal-fence":
		spec.Lines, spec.Active = [2]string{"PORTAL", "FENCE"}, true
	default:
		if strings.HasPrefix(string(id), "map-bcg-") {
			spec.Kind, spec.NoControl = toolbarIncDecButton, true
		}
	}
	return spec
}

func (p *ERAMPane) toolbarSpec(id toolbarButtonID) toolbarButtonSpec {
	spec := baseToolbarSpec(id)

	if id == toolbarMasterDisplay {
		// CRC highlights MASTER TOOLBAR while the master toolbar is visible.
		spec.Active = p.toolbarVisible
		return spec
	}

	if id == toolbarGeomap {
		spec.Kind = toolbarMenuButton
		if geoMap := p.activeGeoMap(); geoMap != nil {
			spec.Lines = [2]string{
				geoMap.LabelLine1,
				geoMap.LabelLine2,
			}
		}
		return spec
	}

	if index, ok := mapFilterButtonIndex(id); ok {
		spec.Kind = toolbarToggleButton
		if geoMap := p.activeGeoMap(); geoMap != nil && index < len(geoMap.FilterMenu) {
			filter := geoMap.FilterMenu[index]
			spec.Lines = [2]string{
				filter.LabelLine1,
				filter.LabelLine2,
			}
		}
		spec.Active = p.maps.filters&(uint64(1)<<uint(index)) != 0
		return spec
	}

	if index, ok := mapBCGButtonIndex(id); ok {
		spec.Kind = toolbarIncDecButton
		spec.NoControl = true
		if geoMap := p.activeGeoMap(); geoMap != nil && index < len(geoMap.BCGMenu) {
			spec.Lines[0] = geoMap.BCGMenu[index]
		}
		return spec
	}

	return spec
}

func brightnessSpec(id toolbarButtonID, label string) toolbarButtonSpec {
	return incDecSpec(id, label, "")
}

func incDecSpec(id toolbarButtonID, label, value string) toolbarButtonSpec {
	return toolbarButtonSpec{
		ID:        id,
		Lines:     [2]string{label, value},
		Kind:      toolbarIncDecButton,
		NoControl: true,
	}
}

func mapBCGButtonIndex(id toolbarButtonID) (int, bool) {
	const prefix = "map-bcg-"

	value := string(id)
	if !strings.HasPrefix(value, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(value, prefix))
	if err != nil || n < 1 || n > eramMapBCGCount {
		return 0, false
	}
	return n - 1, true
}

func toolbarSubmenu(id toolbarButtonID, alternate bool) []toolbarMenuEntry {
	switch id {
	case toolbarViews:
		return viewsToolbarEntries
	case toolbarATCTools:
		return atcToolsToolbarEntries
	case toolbarWeather:
		return weatherToolbarEntries
	case toolbarCheckLists:
		return checkListsToolbarEntries
	case toolbarGeomap:
		return toolbarBankedEntries(geomapToolbarEntries, alternate)
	case toolbarCursor:
		return cursorToolbarEntries
	case toolbarBrightness:
		return brightnessToolbarEntries
	case toolbarMapBrightness:
		return toolbarBankedEntries(mapBrightnessToolbarEntries, alternate)
	case toolbarRadar:
		return radarToolbarEntries
	case toolbarFont:
		return fontToolbarEntries
	case toolbarDBFields:
		return dbFieldsToolbarEntries
	case toolbarControlMenu:
		return toolbarControlEntries
	default:
		return nil
	}
}

func toolbarBankedEntries(entries []toolbarMenuEntry, alternate bool) []toolbarMenuEntry {
	if len(entries) <= 20 {
		return entries
	}
	if alternate {
		if len(entries) > 40 {
			return entries[20:40]
		}
		return entries[20:]
	}
	return entries[:20]
}

func toolbarHasSubmenu(id toolbarButtonID) bool {
	return len(toolbarSubmenu(id, false)) > 0 || len(toolbarSubmenu(id, true)) > 0
}

func toolbarAlternateBank(ctx *panes.Context) bool {
	return ctx != nil && ctx.Keyboard != nil && ctx.Keyboard.IsDown(platform.KeyAlt)
}

func (p *ERAMPane) toolbarButtonLines(spec toolbarButtonSpec) [2]string {
	lines := spec.Lines
	if index, ok := mapBCGButtonIndex(spec.ID); ok {
		lines[1] = strconv.Itoa(p.maps.brightness[index])
		return lines
	}
	switch spec.ID {
	case toolbarRange:
		lines[1] = fmt.Sprintf("%g", p.rangeNM)
	case "backlight-brightness":
		lines[1] = strconv.Itoa(p.systemBrightness)
	case "button-brightness":
		lines[1] = strconv.Itoa(p.buttonBrightness)
	case "background-brightness":
		lines[1] = strconv.Itoa(p.backgroundBrightness)
	case "border-brightness":
		lines[1] = strconv.Itoa(p.borderBrightness)
	case "cursor-brightness":
		lines[1] = strconv.Itoa(p.cursorBrightness)
	case "cursor-size":
		lines[1] = strconv.Itoa(p.cursorSize)
	case "toolbar-brightness":
		lines[1] = strconv.Itoa(p.toolbarBrightness)
	case "text-brightness":
		lines[1] = strconv.Itoa(p.textBrightness)
	case "toolbar-border-brightness":
		lines[1] = strconv.Itoa(p.toolbarBorderBrightness)
	case "paired-target-brightness":
		lines[1] = strconv.Itoa(p.pairedTargetBrightness)
	case "sd-border-brightness":
		lines[1] = strconv.Itoa(p.activeBorderBrightness)
	case "unpaired-target-brightness":
		lines[1] = strconv.Itoa(p.unpairedTargetBrightness)
	case "fdb-brightness":
		lines[1] = strconv.Itoa(p.fdbBrightness)
	case "paired-history-brightness":
		lines[1] = strconv.Itoa(p.pairedHistoryBrightness)
	case "unpaired-history-brightness":
		lines[1] = strconv.Itoa(p.unpairedHistoryBrightness)
	case "satcomm-brightness":
		lines[1] = strconv.Itoa(p.satCommBrightness)
	case "ldb-brightness":
		lines[1] = strconv.Itoa(p.ldbBrightness)
	case "on-frequency-brightness":
		lines[1] = strconv.Itoa(p.onFrequencyBrightness)
	case "weather-brightness":
		lines[1] = strconv.Itoa(p.weatherBrightness)
	case "nexrad-brightness":
		lines[1] = strconv.Itoa(p.nexradBrightness)
	case "fence-brightness":
		lines[1] = strconv.Itoa(p.fenceBrightness)
	case "dbfel-brightness":
		lines[1] = strconv.Itoa(p.dbfelBrightness)
	case "outage-brightness":
		lines[1] = strconv.Itoa(p.outageBrightness)
	case "non-adsb-brightness":
		lines[1] = strconv.Itoa(p.nonADSBrightness)
	case "nexrad-intensity":
		switch p.nexradLevels {
		case 3:
			lines[1] = "123"
		case 2:
			lines[1] = "23"
		case 1:
			lines[1] = "3"
		default:
			lines[1] = "OFF"
		}
	}
	return lines
}

func (p *ERAMPane) toolbarButtonWidth(spec toolbarButtonSpec, metrics toolbarMetrics) float32 {
	if spec.NoControl {
		return metrics.labelWidth
	}
	return metrics.controlWidth + metrics.labelWidth
}

func (p *ERAMPane) buildToolbarLayout(ctx *panes.Context) ([]toolbarButtonLayout, []toolbarExpansionLayout) {
	if p == nil {
		return nil, nil
	}
	scratch := &p.toolbar.layout
	scratch.buttons = scratch.buttons[:0]
	scratch.expansions = scratch.expansions[:0]
	if ctx == nil {
		return scratch.buttons, scratch.expansions
	}

	metrics := p.toolbarMetrics()
	alternate := toolbarAlternateBank(ctx)
	masterOwner := toolbarOwner{Kind: toolbarOwnerMaster}
	if p.toolbarVisible {
		masterTop := float32(toolbarWrapperYPadding)
		masterLeft := metrics.moveWidth + toolbarWrapperXPadding

		// Preserve the original column locations while CRC suppresses all master
		// buttons except the expanded one.
		masterLayouts, _, _ := p.appendToolbarMenu(masterToolbarEntries, masterLeft, masterTop, masterOwner, 0, metrics)
		if p.toolbar.masterExpansion.Root != "" {
			write := masterLayouts[:0]
			for _, layout := range masterLayouts {
				if layout.Spec.ID == p.toolbar.masterExpansion.Root {
					write = append(write, layout)
				}
			}
			// appendToolbarMenu appended into scratch; remove the suppressed entries.
			scratch.buttons = scratch.buttons[:len(scratch.buttons)-len(masterLayouts)]
			scratch.buttons = append(scratch.buttons, write...)
			masterLayouts = write
		}
		p.appendExpandedMenus(masterLayouts, masterOwner, p.toolbar.masterExpansion, alternate, metrics)
	}

	paneSize := ctx.PaneSize()
	for i := range p.toolbar.tearoffs {
		tearoff := &p.toolbar.tearoffs[i]
		spec := p.toolbarSpec(tearoff.Type)
		size := redsmath.Vec2{X: p.toolbarButtonWidth(spec, metrics), Y: metrics.buttonHeight}
		topLeft := toolbarTearoffTopLeft(*tearoff, paneSize, size)
		owner := toolbarOwner{Kind: toolbarOwnerTearoff, TearoffID: tearoff.ID}
		layout := p.appendToolbarButton(spec, topLeft.X, topLeft.Y, owner, 0, 0, metrics)
		p.appendExpandedMenus([]toolbarButtonLayout{layout}, owner, tearoff.Expansion, alternate, metrics)
	}

	return scratch.buttons, scratch.expansions
}

func (p *ERAMPane) appendToolbarMenu(
	entries []toolbarMenuEntry,
	left, top float32,
	owner toolbarOwner,
	depth int,
	metrics toolbarMetrics,
) ([]toolbarButtonLayout, float32, float32) {
	if len(entries) == 0 {
		return nil, 0, 0
	}
	const maxColumns = 40
	var widths [maxColumns]float32
	maxColumn, maxRow := 0, 0
	for _, entry := range entries {
		if entry.Column < 0 || entry.Column >= maxColumns || entry.Row < 0 {
			continue
		}
		width := p.toolbarButtonWidth(p.toolbarSpec(entry.ID), metrics)
		if width > widths[entry.Column] {
			widths[entry.Column] = width
		}
		if entry.Column > maxColumn {
			maxColumn = entry.Column
		}
		if entry.Row > maxRow {
			maxRow = entry.Row
		}
	}
	var offsets [maxColumns]float32
	contentWidth := float32(0)
	for column := 0; column <= maxColumn; column++ {
		offsets[column] = contentWidth
		contentWidth += widths[column]
		if column != maxColumn {
			contentWidth += toolbarButtonColumnGap
		}
	}
	contentHeight := float32(maxRow+1)*metrics.buttonHeight + float32(maxRow*toolbarButtonRowGap)

	start := len(p.toolbar.layout.buttons)
	for _, entry := range entries {
		if entry.Column < 0 || entry.Column >= maxColumns || entry.Row < 0 {
			continue
		}
		spec := p.toolbarSpec(entry.ID)
		buttonWidth := p.toolbarButtonWidth(spec, metrics)
		// CRC gives every ButtonBase root HorizontalAlignment.Right. A column's
		// width is therefore set by its widest button (often a menu button with
		// a tear-off/BCPA strip), while narrower buttons in that same column are
		// shifted right. This leaves the compensating gray strip on the left; in
		// BRIGHT this is visible on BCKLGHT below MAP BRIGHT and BUTTON below
		// CPDLC.
		x := left + offsets[entry.Column] + widths[entry.Column] - buttonWidth
		y := top + float32(entry.Row)*(metrics.buttonHeight+toolbarButtonRowGap)
		p.appendToolbarButton(spec, x, y, owner, depth, entry.Row, metrics)
	}
	return p.toolbar.layout.buttons[start:], contentWidth, contentHeight
}

func (p *ERAMPane) appendToolbarButton(
	spec toolbarButtonSpec,
	x, y float32,
	owner toolbarOwner,
	depth int,
	row int,
	metrics toolbarMetrics,
) toolbarButtonLayout {
	rootWidth := p.toolbarButtonWidth(spec, metrics)
	layout := toolbarButtonLayout{
		Spec:  spec,
		Owner: owner,
		Depth: depth,
		Row:   row,
		Root:  redsmath.NewRect(x, y, x+rootWidth, y+metrics.buttonHeight),
	}
	if spec.NoControl {
		layout.Pick = layout.Root
	} else {
		layout.Control = redsmath.NewRect(x, y, x+metrics.controlWidth, y+metrics.buttonHeight)
		layout.Pick = redsmath.NewRect(x+metrics.controlWidth, y, x+rootWidth, y+metrics.buttonHeight)
	}
	p.toolbar.layout.buttons = append(p.toolbar.layout.buttons, layout)
	return layout
}

func (p *ERAMPane) appendExpandedMenus(
	rootLayouts []toolbarButtonLayout,
	owner toolbarOwner,
	expansion toolbarExpansion,
	alternate bool,
	metrics toolbarMetrics,
) {
	if expansion.Root == "" {
		return
	}
	var parent *toolbarButtonLayout
	for i := range rootLayouts {
		if rootLayouts[i].Spec.ID == expansion.Root {
			parent = &rootLayouts[i]
			break
		}
	}
	if parent == nil {
		return
	}
	entries := toolbarSubmenu(parent.Spec.ID, alternate)
	if len(entries) == 0 {
		return
	}
	contentTop := parent.Root.Min.Y
	if parent.Row > 0 && toolbarMenuHasSecondRow(entries) {
		contentTop += 2 - float32(int(metrics.toolbarHeight)/2)
	}
	contentLeft := parent.Root.Max.X + toolbarExpansionBorder
	children, width, height := p.appendToolbarMenu(entries, contentLeft, contentTop, owner, parent.Depth+1, metrics)
	p.toolbar.layout.expansions = append(p.toolbar.layout.expansions, toolbarExpansionLayout{
		Owner: owner,
		Depth: parent.Depth + 1,
		Bounds: redsmath.NewRect(
			parent.Root.Max.X,
			contentTop-toolbarExpansionBorder,
			contentLeft+width+toolbarExpansionBorder,
			contentTop+height+toolbarExpansionBorder,
		),
		SuppressBorder: expansion.Child != "",
	})

	if expansion.Child == "" {
		return
	}
	for _, child := range children {
		if child.Spec.ID != expansion.Child {
			continue
		}
		grandchildren := toolbarSubmenu(child.Spec.ID, alternate)
		if len(grandchildren) == 0 {
			return
		}
		grandTop := child.Root.Min.Y
		if child.Row > 0 && toolbarMenuHasSecondRow(grandchildren) {
			grandTop += 2 - float32(int(metrics.toolbarHeight)/2)
		}
		grandLeft := child.Root.Max.X + toolbarExpansionBorder
		_, grandWidth, grandHeight := p.appendToolbarMenu(grandchildren, grandLeft, grandTop, owner, child.Depth+1, metrics)
		p.toolbar.layout.expansions = append(p.toolbar.layout.expansions, toolbarExpansionLayout{
			Owner: owner,
			Depth: child.Depth + 1,
			Bounds: redsmath.NewRect(
				child.Root.Max.X,
				grandTop-toolbarExpansionBorder,
				grandLeft+grandWidth+toolbarExpansionBorder,
				grandTop+grandHeight+toolbarExpansionBorder,
			),
		})
		return
	}
}

func toolbarMenuHasSecondRow(entries []toolbarMenuEntry) bool {
	for _, entry := range entries {
		if entry.Row > 0 {
			return true
		}
	}
	return false
}

func toolbarTearoffTopLeft(tearoff toolbarTearoff, paneSize, size redsmath.Vec2) redsmath.Vec2 {
	switch tearoff.Anchor {
	case toolbarAnchorTopRight:
		return redsmath.Vec2{X: paneSize.X - tearoff.Offset.X - size.X, Y: tearoff.Offset.Y}
	case toolbarAnchorBottomLeft:
		return redsmath.Vec2{X: tearoff.Offset.X, Y: paneSize.Y - tearoff.Offset.Y - size.Y}
	case toolbarAnchorBottomRight:
		return redsmath.Vec2{X: paneSize.X - tearoff.Offset.X - size.X, Y: paneSize.Y - tearoff.Offset.Y - size.Y}
	default:
		return tearoff.Offset
	}
}

func (p *ERAMPane) drawToolbar(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil {
		return
	}
	paneWidth := ctx.PaneRect.Width()
	if paneWidth <= 0 || ctx.PaneRect.Height() <= 0 {
		return
	}
	metrics := p.toolbarMetrics()
	buttons, expansions := p.buildToolbarLayout(ctx)

	x, y, width, fbHeight := ctx.PaneFramebufferRect()
	moveRect := redsmath.NewRect(0, (metrics.toolbarHeight-toolbarInteriorLineWidth)/2, metrics.moveWidth, metrics.toolbarHeight-toolbarInteriorLineWidth)
	if p.toolbarVisible {
		for depth := 0; depth <= maxToolbarDepth(buttons, expansions, toolbarOwner{Kind: toolbarOwnerMaster}); depth++ {
			masterCB := zcb.At(toolbarZ(depth))
			prepareToolbarCB(masterCB, ctx, x, y, width, fbHeight)
			if depth == 0 {
				drawSolidRect(masterCB, redsmath.NewRect(0, 0, paneWidth, metrics.toolbarHeight), applyERAMBrightness(toolbarGray, p.toolbarBrightness, p.systemBrightness))

				// CRC's visible lower half of the master-toolbar move-down control.
				p.drawToolbarBorderedRect(masterCB, moveRect, toolbarGray, p.buttonBrightness, false, toolbarWhite, p.borderBrightness, 1)
			}

			for _, expansion := range expansions {
				if expansion.Owner == (toolbarOwner{Kind: toolbarOwnerMaster}) && expansion.Depth == depth {
					p.drawToolbarExpansion(masterCB, expansion.Bounds, expansion.SuppressBorder)
				}
			}
			p.drawToolbarButtons(ctx, masterCB, buttons, toolbarOwner{Kind: toolbarOwnerMaster}, depth, metrics)
			p.drawToolbarText(ctx, masterCB, buttons, toolbarOwner{Kind: toolbarOwnerMaster}, depth, metrics)

			if depth == 0 {
				// The one-pixel interior line is part of CRC's 73-pixel footprint.
				drawSolidRect(masterCB, redsmath.NewRect(0, metrics.toolbarHeight-1, paneWidth, metrics.toolbarHeight), applyERAMBrightness(toolbarWhite, p.toolbarBorderBrightness, p.systemBrightness))
				p.drawToolbarArrow(ctx, masterCB, moveRect, metrics)
			}
			masterCB.Blend()
			masterCB.DisableScissor()
		}
	}

	for i := range p.toolbar.tearoffs {
		owner := toolbarOwner{Kind: toolbarOwnerTearoff, TearoffID: p.toolbar.tearoffs[i].ID}
		for depth := 0; depth <= maxToolbarDepth(buttons, expansions, owner); depth++ {
			cb := zcb.At(zTearoffButtons + renderer.Z(i*4+depth))
			prepareToolbarCB(cb, ctx, x, y, width, fbHeight)
			for _, expansion := range expansions {
				if expansion.Owner == owner && expansion.Depth == depth {
					p.drawToolbarExpansion(cb, expansion.Bounds, expansion.SuppressBorder)
				}
			}
			p.drawToolbarButtons(ctx, cb, buttons, owner, depth, metrics)
			p.drawToolbarText(ctx, cb, buttons, owner, depth, metrics)
			cb.Blend()
			cb.DisableScissor()
		}
	}

	if p.toolbar.moving != nil {
		cb := zcb.At(zButtonMoveFrame)
		prepareToolbarCB(cb, ctx, x, y, width, fbHeight)
		bounds := redsmath.NewRect(
			p.toolbar.moving.Position.X,
			p.toolbar.moving.Position.Y,
			p.toolbar.moving.Position.X+p.toolbar.moving.Size.X,
			p.toolbar.moving.Position.Y+p.toolbar.moving.Size.Y,
		)
		drawBorderOnly(cb, bounds, applyERAMBrightness(toolbarWhite, p.pairedTargetBrightness, p.systemBrightness), 1)
		cb.Blend()
		cb.DisableScissor()
	}
}

func toolbarZ(depth int) renderer.Z {
	return zLoweredMasterToolbar + renderer.Z(depth)
}

func maxToolbarDepth(buttons []toolbarButtonLayout, expansions []toolbarExpansionLayout, owner toolbarOwner) int {
	maxDepth := 0
	for _, layout := range buttons {
		if layout.Owner == owner && layout.Depth > maxDepth {
			maxDepth = layout.Depth
		}
	}
	for _, expansion := range expansions {
		if expansion.Owner == owner && expansion.Depth > maxDepth {
			maxDepth = expansion.Depth
		}
	}
	return maxDepth
}

func prepareToolbarCB(cb *renderer.CmdBuffer, ctx *panes.Context, x, y, width, height int) {
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.DisableBlend()
}

func (p *ERAMPane) drawToolbarExpansion(cb *renderer.CmdBuffer, bounds redsmath.Rect, suppressBorder bool) {
	if suppressBorder {
		drawSolidRect(cb, bounds, applyERAMBrightness(toolbarGray, p.buttonBrightness, p.systemBrightness))
		return
	}
	p.drawToolbarBorderedRect(
		cb,
		bounds,
		toolbarGray,
		p.buttonBrightness,
		false,
		toolbarBrightCoral,
		p.borderBrightness,
		toolbarExpansionBorder,
	)
}

func (p *ERAMPane) drawToolbarButtons(
	ctx *panes.Context,
	cb *renderer.CmdBuffer,
	buttons []toolbarButtonLayout,
	owner toolbarOwner,
	depth int,
	metrics toolbarMetrics,
) {
	for _, layout := range buttons {
		if layout.Owner != owner || layout.Depth != depth {
			continue
		}
		hoverControl := p.toolbar.moving == nil && ctx.Mouse != nil && !layout.Control.Empty() &&
			p.toolbarControlEligible(layout) && layout.Control.Contains(ctx.Mouse.Pos)
		hoverPick := p.toolbar.moving == nil && ctx.Mouse != nil && !layout.Spec.Disabled && layout.Pick.Contains(ctx.Mouse.Pos)

		if !layout.Spec.NoControl {
			controlColor := toolbarBrightGold
			// A button that already has a floating copy loses its tear-off action.
			// The exception is the depth-0 root of that floating copy: its control
			// remains active because it is the handle used to reposition it.
			if p.hasToolbarTearoff(layout.Spec.ID) &&
				!(owner.Kind == toolbarOwnerTearoff && layout.Depth == 0) {
				controlColor = toolbarGray
			}
			p.drawToolbarBorderedRect(cb, layout.Control, controlColor, p.buttonBrightness, hoverControl, toolbarWhite, p.borderBrightness, 1)
		}

		background := toolbarBlack
		switch layout.Spec.Kind {
		case toolbarMenuButton:
			background = toolbarBlue
			if p.toolbarLayoutExpanded(layout) {
				background = toolbarBurntCoral
			}
		case toolbarCommandButton:
			background = toolbarTeal
		case toolbarIncDecButton:
			background = toolbarIncDecGreen
		case toolbarPressHoldButton:
			background = toolbarBlack
		default:
			if layout.Spec.Active || p.toolbarLayoutExpanded(layout) {
				background = toolbarGray
			}
		}
		p.drawToolbarBorderedRect(cb, layout.Pick, background, p.buttonBrightness, hoverPick, toolbarWhite, p.borderBrightness, 1)

		if layout.Spec.Kind == toolbarPressHoldButton {
			// CRC's inactive press/hold buttons have a 10x10 gray cut corner.
			x1, y0 := layout.Pick.Max.X-1, layout.Pick.Min.Y+1
			cb.SetRGB(applyERAMBrightness(toolbarGray, p.buttonBrightness, p.systemBrightness))
			cb.DrawTriangles(
				[]renderer.PointVertex{{X: x1 - 10, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y0 + 10}},
				[]uint32{0, 1, 2}, renderer.DrawSolid, 0,
			)
		}
	}
}

func (p *ERAMPane) drawToolbarText(
	ctx *panes.Context,
	cb *renderer.CmdBuffer,
	buttons []toolbarButtonLayout,
	owner toolbarOwner,
	depth int,
	metrics toolbarMetrics,
) {
	texture := p.toolbarTexture(ctx.Renderer, metrics.fontSize)
	if texture == 0 || p.toolbar.font == nil {
		return
	}
	td := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(td)
	td.SetFont(p.toolbar.font)
	for _, layout := range buttons {
		if layout.Owner != owner || layout.Depth != depth {
			continue
		}
		lines := p.toolbarButtonLines(layout.Spec)
		textColor := toolbarWhite
		if layout.Spec.Disabled {
			textColor = toolbarLightGray
		}
		background := p.toolbarButtonBackground(layout)
		for lineIndex, line := range lines {
			if line == "" {
				continue
			}
			textWidth, _ := p.toolbar.font.MeasureText(line, metrics.fontSize)
			x := layout.Pick.Min.X + toolbarButtonBorderWidth +
				(layout.Pick.Width()-2*toolbarButtonBorderWidth-float32(textWidth))/2
			y := layout.Pick.Min.Y + toolbarButtonBorderWidth + toolbarTextYPadding +
				float32(lineIndex*(metrics.lineHeight+2*toolbarTextYPadding))
			td.AddText(line, redsmath.Vec2{X: float32(int(x)), Y: y}, renderer.TextStyle{
				Size:       metrics.fontSize,
				Color:      applyERAMBrightness(textColor, p.textBrightness, p.systemBrightness).ToRGBA(),
				Background: applyERAMBrightness(background, p.buttonBrightness, p.systemBrightness).ToRGBA(),
			})
		}
	}
	td.GenerateCommands(cb, texture)
}

func (p *ERAMPane) drawToolbarArrow(ctx *panes.Context, cb *renderer.CmdBuffer, bounds redsmath.Rect, metrics toolbarMetrics) {
	texture := p.toolbarTexture(ctx.Renderer, metrics.fontSize)
	if texture == 0 || p.toolbar.font == nil {
		return
	}
	arrow := string(rune(129)) // Vatsim.Nas.Common.EramChar.DownArrow
	w, _ := p.toolbar.font.MeasureText(arrow, metrics.fontSize)
	x := bounds.Min.X + (bounds.Width()-float32(w))/2
	y := bounds.Min.Y + (bounds.Height()-float32(metrics.lineHeight))/2
	td := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(td)
	td.SetFont(p.toolbar.font)
	td.AddText(arrow, redsmath.Vec2{X: float32(int(x)), Y: float32(int(y))}, renderer.TextStyle{
		Size:       metrics.fontSize,
		Color:      applyERAMBrightness(toolbarWhite, p.textBrightness, p.systemBrightness).ToRGBA(),
		Background: applyERAMBrightness(toolbarGray, p.buttonBrightness, p.systemBrightness).ToRGBA(),
	})
	td.GenerateCommands(cb, texture)
}

func (p *ERAMPane) toolbarButtonBackground(layout toolbarButtonLayout) renderer.RGB {
	switch layout.Spec.Kind {
	case toolbarMenuButton:
		if p.toolbarLayoutExpanded(layout) {
			return toolbarBurntCoral
		}
		return toolbarBlue
	case toolbarCommandButton:
		return toolbarTeal
	case toolbarIncDecButton:
		return toolbarIncDecGreen
	case toolbarPressHoldButton:
		return toolbarBlack
	default:
		if layout.Spec.Active || p.toolbarLayoutExpanded(layout) {
			return toolbarGray
		}
		return toolbarBlack
	}
}

func (p *ERAMPane) drawToolbarBorderedRect(
	cb *renderer.CmdBuffer,
	bounds redsmath.Rect,
	fill renderer.RGB,
	fillBrightness int,
	hover bool,
	border renderer.RGB,
	borderBrightness int,
	borderWidth float32,
) {
	if bounds.Empty() || borderWidth <= 0 {
		return
	}
	if hover {
		borderBrightness = p.pairedTargetBrightness
	}
	drawSolidRect(cb, bounds, applyERAMBrightness(border, borderBrightness, p.systemBrightness))
	inner := redsmath.NewRect(
		bounds.Min.X+borderWidth,
		bounds.Min.Y+borderWidth,
		bounds.Max.X-borderWidth,
		bounds.Max.Y-borderWidth,
	)
	if !inner.Empty() {
		drawSolidRect(cb, inner, applyERAMBrightness(fill, fillBrightness, p.systemBrightness))
	}
}

func drawSolidRect(cb *renderer.CmdBuffer, bounds redsmath.Rect, color renderer.RGB) {
	if cb == nil || bounds.Empty() {
		return
	}
	cb.SetRGB(color)
	cb.DrawTriangles(
		[]renderer.PointVertex{
			{X: bounds.Min.X, Y: bounds.Min.Y},
			{X: bounds.Max.X, Y: bounds.Min.Y},
			{X: bounds.Max.X, Y: bounds.Max.Y},
			{X: bounds.Min.X, Y: bounds.Max.Y},
		},
		[]uint32{0, 1, 2, 0, 2, 3}, renderer.DrawSolid, 0,
	)
}

func drawBorderOnly(cb *renderer.CmdBuffer, bounds redsmath.Rect, color renderer.RGB, width float32) {
	if cb == nil || bounds.Empty() || width <= 0 {
		return
	}
	drawSolidRect(cb, redsmath.NewRect(bounds.Min.X, bounds.Min.Y, bounds.Max.X, bounds.Min.Y+width), color)
	drawSolidRect(cb, redsmath.NewRect(bounds.Min.X, bounds.Max.Y-width, bounds.Max.X, bounds.Max.Y), color)
	drawSolidRect(cb, redsmath.NewRect(bounds.Min.X, bounds.Min.Y+width, bounds.Min.X+width, bounds.Max.Y-width), color)
	drawSolidRect(cb, redsmath.NewRect(bounds.Max.X-width, bounds.Min.Y+width, bounds.Max.X, bounds.Max.Y-width), color)
}

func (p *ERAMPane) toolbarLayoutExpanded(layout toolbarButtonLayout) bool {
	expansion := p.toolbarExpansionFor(layout.Owner)
	if expansion == nil {
		return false
	}
	if layout.Depth == 0 {
		return expansion.Root == layout.Spec.ID
	}
	if layout.Depth == 1 {
		return expansion.Child == layout.Spec.ID
	}
	return false
}

func (p *ERAMPane) toolbarExpansionFor(owner toolbarOwner) *toolbarExpansion {
	if owner.Kind == toolbarOwnerMaster {
		return &p.toolbar.masterExpansion
	}
	for i := range p.toolbar.tearoffs {
		if p.toolbar.tearoffs[i].ID == owner.TearoffID {
			return &p.toolbar.tearoffs[i].Expansion
		}
	}
	return nil
}

func (p *ERAMPane) hasToolbarTearoff(id toolbarButtonID) bool {
	for i := range p.toolbar.tearoffs {
		if p.toolbar.tearoffs[i].Type == id {
			return true
		}
	}
	return false
}

func (p *ERAMPane) consumeToolbarInput(ctx *panes.Context) bool {
	if p == nil || ctx == nil {
		return false
	}
	metrics := p.toolbarMetrics()
	mouse := ctx.Mouse
	if p.toolbar.moving != nil {
		if ctx.Keyboard != nil && ctx.Keyboard.WasPressed(platform.KeyEscape) {
			p.toolbar.moving = nil
			return true
		}
		if mouse == nil {
			return true
		}
		p.toolbar.moving.Position = clampToolbarPosition(mouse.Pos, p.toolbar.moving.Size, ctx.PaneSize())
		if mouse.WasPressed(platform.MouseButtonLeft) || mouse.WasPressed(platform.MouseButtonMiddle) {
			p.confirmToolbarMove(ctx.PaneSize())
		}
		return true
	}
	if mouse == nil {
		p.toolbar.repeat = nil
		return false
	}
	if p.consumeToolbarRepeat(mouse) {
		return true
	}

	buttons, expansions := p.buildToolbarLayout(ctx)
	if mouse.WasPressed(platform.MouseButtonLeft) {
		if p.activateToolbarAt(ctx, buttons, mouse.Pos, metrics, toolbarSelect, platform.MouseButtonLeft) {
			return true
		}
	}
	if mouse.WasPressed(platform.MouseButtonMiddle) {
		if p.activateToolbarAt(ctx, buttons, mouse.Pos, metrics, toolbarEnter, platform.MouseButtonMiddle) {
			return true
		}
	}

	// Toolbar UI owns all pointer input over its visible background, expansion
	// panels, and floating copies, even when a particular face has no action.
	if p.toolbarVisible && mouse.Pos.Y >= 0 && mouse.Pos.Y < metrics.toolbarHeight && mouse.Pos.X >= 0 && mouse.Pos.X < ctx.PaneRect.Width() {
		return true
	}
	for _, expansion := range expansions {
		if expansion.Bounds.Contains(mouse.Pos) {
			return true
		}
	}
	for _, layout := range buttons {
		if layout.Owner.Kind == toolbarOwnerTearoff && layout.Root.Contains(mouse.Pos) {
			return true
		}
	}
	return false
}

func (p *ERAMPane) activateToolbarAt(
	ctx *panes.Context,
	buttons []toolbarButtonLayout,
	position redsmath.Vec2,
	metrics toolbarMetrics,
	action toolbarPickAction,
	button platform.MouseButton,
) bool {
	maxDepth := 0
	for _, layout := range buttons {
		if layout.Depth > maxDepth {
			maxDepth = layout.Depth
		}
	}
	for depth := maxDepth; depth >= 0; depth-- {
		for i := len(buttons) - 1; i >= 0; i-- {
			layout := buttons[i]
			if layout.Depth != depth {
				continue
			}
			if !layout.Control.Empty() && layout.Control.Contains(position) && p.toolbarControlEligible(layout) {
				p.startToolbarMove(ctx, layout, metrics)
				return true
			}
			if layout.Pick.Contains(position) {
				p.activateToolbarButton(layout, action, button)
				return true
			}
		}
	}
	return false
}

func (p *ERAMPane) toolbarControlEligible(layout toolbarButtonLayout) bool {
	if layout.Spec.NoControl {
		return false
	}
	// Only the depth-0 button is the root of an existing floating tear-off.
	// Controls on menus opened from that tear-off (depth 1+) still belong to
	// their own button and may therefore create their own tear-off. Treating
	// every descendant as part of the parent's tear-off makes dragging, e.g.,
	// MAP BRIGHT from a floating BRIGHT menu move BRIGHT itself.
	if layout.Owner.Kind == toolbarOwnerTearoff && layout.Depth == 0 {
		return true
	}
	return !p.hasToolbarTearoff(layout.Spec.ID)
}

func (p *ERAMPane) activateToolbarButton(layout toolbarButtonLayout, action toolbarPickAction, button platform.MouseButton) {
	if layout.Spec.ID == toolbarMasterDisplay {
		// CRC's MASTER TOOLBAR control only changes the master toolbar. The
		// special floating TOOLBAR tear-off remains visible so this can always
		// be toggled back on.
		p.toolbarVisible = !p.toolbarVisible
		return
	}

	if layout.Spec.Kind == toolbarIncDecButton {
		if p.activateToolbarIncDec(layout.Spec.ID, action) {
			if toolbarIncDecAutoRepeats(layout.Spec.ID) {
				p.startToolbarRepeat(layout.Spec.ID, action, button)
			} else {
				p.toolbar.repeat = nil
			}
		}
		return
	}

	if index, ok := mapFilterButtonIndex(layout.Spec.ID); ok {
		p.toggleMapFilter(index)
		return
	}

	p.toggleToolbarExpansion(layout)
}

func (p *ERAMPane) activateToolbarIncDec(id toolbarButtonID, action toolbarPickAction) bool {
	increment := action == toolbarEnter
	if index, ok := mapBCGButtonIndex(id); ok {
		adjustBrightness(&p.maps.brightness[index], increment, 0, 100)
		return true
	}
	if id == "cursor-size" {
		p.adjustCursorSize(increment)
		return true
	}
	return p.adjustToolbarBrightness(id, increment)
}

func toolbarIncDecAutoRepeats(id toolbarButtonID) bool {
	if _, ok := mapBCGButtonIndex(id); ok {
		return true
	}

	switch id {
	case "backlight-brightness",
		"button-brightness",
		"background-brightness",
		"border-brightness",
		"cursor-brightness",
		"toolbar-brightness",
		"text-brightness",
		"toolbar-border-brightness",
		"paired-target-brightness",
		"sd-border-brightness",
		"unpaired-target-brightness",
		"fdb-brightness",
		"paired-history-brightness",
		"unpaired-history-brightness",
		"satcomm-brightness",
		"ldb-brightness",
		"on-frequency-brightness",
		"weather-brightness",
		"nexrad-brightness",
		"fence-brightness",
		"dbfel-brightness",
		"outage-brightness",
		"non-adsb-brightness":
		return true
	default:
		return false
	}
}

func (p *ERAMPane) adjustCursorSize(increment bool) {
	if increment {
		if p.cursorSize < maxCursorSize {
			p.cursorSize++
		}
		return
	}
	if p.cursorSize > minCursorSize {
		p.cursorSize--
	}
}

func (p *ERAMPane) startToolbarRepeat(id toolbarButtonID, action toolbarPickAction, button platform.MouseButton) {
	p.toolbar.repeat = &toolbarRepeat{
		ID:          id,
		Action:      action,
		MouseButton: button,
		Next:        time.Now().Add(toolbarRepeatInitialDelay),
	}
}

func (p *ERAMPane) consumeToolbarRepeat(mouse *platform.MouseState) bool {
	repeat := p.toolbar.repeat
	if repeat == nil {
		return false
	}
	if mouse == nil || !mouse.IsDown(repeat.MouseButton) {
		p.toolbar.repeat = nil
		return false
	}
	now := time.Now()
	if now.Before(repeat.Next) {
		return false
	}
	if !p.activateToolbarIncDec(repeat.ID, repeat.Action) {
		p.toolbar.repeat = nil
		return false
	}
	repeat.Next = now.Add(toolbarRepeatDelay)
	return true
}

func (p *ERAMPane) adjustToolbarBrightness(id toolbarButtonID, increment bool) bool {
	switch id {
	case "backlight-brightness":
		adjustBrightness(&p.systemBrightness, increment, 0, 100)
	case "button-brightness":
		adjustBrightness(&p.buttonBrightness, increment, 0, 100)
	case "background-brightness":
		adjustBrightness(&p.backgroundBrightness, increment, 0, 60)
	case "border-brightness":
		adjustBrightness(&p.borderBrightness, increment, 0, 100)
	case "cursor-brightness":
		adjustBrightness(&p.cursorBrightness, increment, 0, 100)
	case "toolbar-brightness":
		adjustBrightness(&p.toolbarBrightness, increment, 0, 100)
	case "text-brightness":
		adjustBrightness(&p.textBrightness, increment, 0, 100)
	case "toolbar-border-brightness":
		adjustBrightness(&p.toolbarBorderBrightness, increment, 0, 100)
	case "paired-target-brightness":
		adjustBrightness(&p.pairedTargetBrightness, increment, 0, 100)
	case "sd-border-brightness":
		adjustBrightness(&p.activeBorderBrightness, increment, 0, 100)
	case "unpaired-target-brightness":
		adjustBrightness(&p.unpairedTargetBrightness, increment, 0, 100)
	case "fdb-brightness":
		adjustBrightness(&p.fdbBrightness, increment, 0, 100)
	case "paired-history-brightness":
		adjustBrightness(&p.pairedHistoryBrightness, increment, 0, 100)
	case "unpaired-history-brightness":
		adjustBrightness(&p.unpairedHistoryBrightness, increment, 0, 100)
	case "satcomm-brightness":
		adjustBrightness(&p.satCommBrightness, increment, 0, 100)
	case "ldb-brightness":
		adjustBrightness(&p.ldbBrightness, increment, 0, 100)
	case "on-frequency-brightness":
		adjustBrightness(&p.onFrequencyBrightness, increment, 0, 100)
	case "weather-brightness":
		adjustBrightness(&p.weatherBrightness, increment, 0, 100)
	case "nexrad-brightness":
		adjustBrightness(&p.nexradBrightness, increment, 30, 100)
	case "fence-brightness":
		adjustBrightness(&p.fenceBrightness, increment, 0, 100)
	case "dbfel-brightness":
		adjustBrightness(&p.dbfelBrightness, increment, 0, 100)
	case "outage-brightness":
		adjustBrightness(&p.outageBrightness, increment, 0, 100)
	case "non-adsb-brightness":
		adjustBrightness(&p.nonADSBrightness, increment, 0, 100)
	default:
		return false
	}
	return true
}

func adjustBrightness(value *int, increment bool, minValue, maxValue int) {
	delta := -2
	if increment {
		delta = 2
	}
	*value = clampInt(*value+delta, minValue, maxValue)
}

func (p *ERAMPane) startToolbarMove(ctx *panes.Context, layout toolbarButtonLayout, metrics toolbarMetrics) {
	existingID := 0
	// Descendants of a floating menu share its layout owner so their expansion
	// can be positioned/rendered with the parent. They are not, however, the
	// existing tear-off itself. Only the depth-0 root should move that existing
	// tear-off; a depth-1+ control starts a new tear-off for the child button.
	if layout.Owner.Kind == toolbarOwnerTearoff && layout.Depth == 0 {
		existingID = layout.Owner.TearoffID
	}
	p.toolbar.moving = &toolbarTearoffMove{
		Type:       layout.Spec.ID,
		ExistingID: existingID,
		Position:   layout.Root.Min,
		Size:       redsmath.Vec2{X: p.toolbarButtonWidth(layout.Spec, metrics), Y: metrics.buttonHeight},
	}
	if ctx.Platform != nil {
		// Platform cursor coordinates are window-local; pane input is pane-local.
		ctx.Platform.SetMousePosition(ctx.PaneRect.Min.Add(layout.Root.Min))
	}
}

func (p *ERAMPane) confirmToolbarMove(paneSize redsmath.Vec2) {
	move := p.toolbar.moving
	if move == nil {
		return
	}
	position := clampToolbarPosition(move.Position, move.Size, paneSize)
	right := paneSize.X - position.X - move.Size.X
	bottom := paneSize.Y - position.Y - move.Size.Y
	anchor := toolbarAnchorTopLeft
	offset := position
	if bottom < position.Y {
		if right >= position.X {
			anchor = toolbarAnchorBottomLeft
			offset = redsmath.Vec2{X: position.X, Y: bottom}
		} else {
			anchor = toolbarAnchorBottomRight
			offset = redsmath.Vec2{X: right, Y: bottom}
		}
	} else if right < position.X {
		anchor = toolbarAnchorTopRight
		offset = redsmath.Vec2{X: right, Y: position.Y}
	}

	if move.ExistingID != 0 {
		for i := range p.toolbar.tearoffs {
			if p.toolbar.tearoffs[i].ID == move.ExistingID {
				p.toolbar.tearoffs[i].Anchor = anchor
				p.toolbar.tearoffs[i].Offset = offset
				p.bringToolbarTearoffToFront(i)
				break
			}
		}
	} else if !p.hasToolbarTearoff(move.Type) {
		p.toolbar.nextTearoffID++
		p.toolbar.tearoffs = append(p.toolbar.tearoffs, toolbarTearoff{
			ID:     p.toolbar.nextTearoffID,
			Type:   move.Type,
			Anchor: anchor,
			Offset: offset,
		})
	}
	p.toolbar.moving = nil
}

func (p *ERAMPane) bringToolbarTearoffToFront(index int) {
	if index < 0 || index >= len(p.toolbar.tearoffs)-1 {
		return
	}
	item := p.toolbar.tearoffs[index]
	copy(p.toolbar.tearoffs[index:], p.toolbar.tearoffs[index+1:])
	p.toolbar.tearoffs[len(p.toolbar.tearoffs)-1] = item
}

func clampToolbarPosition(position, size, paneSize redsmath.Vec2) redsmath.Vec2 {
	maxX := paneSize.X - size.X
	maxY := paneSize.Y - size.Y
	if maxX < 0 {
		maxX = 0
	}
	if maxY < 0 {
		maxY = 0
	}
	if position.X < 0 {
		position.X = 0
	} else if position.X > maxX {
		position.X = maxX
	}
	if position.Y < 0 {
		position.Y = 0
	} else if position.Y > maxY {
		position.Y = maxY
	}
	return position
}

func (p *ERAMPane) toggleToolbarExpansion(layout toolbarButtonLayout) {
	if !toolbarHasSubmenu(layout.Spec.ID) {
		return
	}
	expansion := p.toolbarExpansionFor(layout.Owner)
	if expansion == nil {
		return
	}
	if layout.Depth == 0 {
		if expansion.Root == layout.Spec.ID {
			*expansion = toolbarExpansion{}
		} else {
			expansion.Root = layout.Spec.ID
			expansion.Child = ""
		}
		return
	}
	if layout.Depth == 1 {
		if expansion.Child == layout.Spec.ID {
			expansion.Child = ""
		} else {
			expansion.Child = layout.Spec.ID
		}
	}
}
