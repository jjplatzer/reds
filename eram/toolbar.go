package eram

import (
	"fmt"
	"strings"

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

	defaultButtonBrightness        = 80
	defaultBorderBrightness        = 56
	defaultTextBrightness          = 90
	defaultToolbarBorderBrightness = 50
	defaultPairedTargetBrightness  = 92

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
	Owner  toolbarOwner
	Bounds redsmath.Rect
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

var (
	geomapToolbarEntries        = makeNumberedToolbarEntries("map-filter", 20)
	mapBrightnessToolbarEntries = makeNumberedToolbarEntries("map-bcg", 20)
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

func toolbarSpec(id toolbarButtonID) toolbarButtonSpec {
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
		spec.Lines, spec.Kind, spec.NoControl = [2]string{"SIZE", "1"}, toolbarIncDecButton, true
	case "audio-volume":
		spec.Lines, spec.Kind, spec.NoControl = [2]string{"VOLUME", "5"}, toolbarIncDecButton, true

	case "backlight-brightness":
		spec = brightnessSpec(id, "BCKLGHT", "90")
	case "cpdlc-brightness":
		spec.Lines, spec.Kind = [2]string{"CPDLC", ""}, toolbarMenuButton
	case "button-brightness":
		spec = brightnessSpec(id, "BUTTON", "80")
	case "background-brightness":
		spec = brightnessSpec(id, "BCKGRD", "26")
	case "border-brightness":
		spec = brightnessSpec(id, "BORDER", "56")
	case "cursor-brightness":
		spec = brightnessSpec(id, "CURSOR", "100")
	case "toolbar-brightness":
		spec = brightnessSpec(id, "TOOLBAR", "40")
	case "text-brightness":
		spec = brightnessSpec(id, "TEXT", "90")
	case "toolbar-border-brightness":
		spec = brightnessSpec(id, "TB BRDR", "50")
	case "paired-target-brightness":
		spec = brightnessSpec(id, "PR TGT", "92")
	case "sd-border-brightness":
		spec = brightnessSpec(id, "AB BRDR", "56")
	case "unpaired-target-brightness":
		spec = brightnessSpec(id, "UNP TGT", "92")
	case "fdb-brightness":
		spec = brightnessSpec(id, "FDB", "90")
	case "paired-history-brightness":
		spec = brightnessSpec(id, "PR HST", "16")
	case "portal-brightness":
		spec = brightnessSpec(id, "PORTAL", "=")
	case "unpaired-history-brightness":
		spec = brightnessSpec(id, "UNP HST", "16")
	case "satcomm-brightness":
		spec = brightnessSpec(id, "SATCOMM", "90")
	case "ldb-brightness":
		spec = brightnessSpec(id, "LDB", "60")
	case "on-frequency-brightness":
		spec = brightnessSpec(id, "ON-FREQ", "90")
	case "select-ldb-brightness":
		spec = brightnessSpec(id, "SLDB", "+5")
	case "line4-brightness":
		spec = brightnessSpec(id, "LINE 4", "=")
	case "weather-brightness":
		spec = brightnessSpec(id, "WX", "50")
	case "dwell-brightness":
		spec = brightnessSpec(id, "DWELL", "+20")
	case "nexrad-brightness":
		spec = brightnessSpec(id, "NEXRAD", "50")
	case "fence-brightness":
		spec = brightnessSpec(id, "FENCE", "90")
	case "dbfel-brightness":
		spec = brightnessSpec(id, "DBFEL", "80")
	case "outage-brightness":
		spec = brightnessSpec(id, "OUTAGE", "80")

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
		spec = brightnessSpec(id, "NONADSB", "90")
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

func brightnessSpec(id toolbarButtonID, label, value string) toolbarButtonSpec {
	return toolbarButtonSpec{
		ID:        id,
		Lines:     [2]string{label, value},
		Kind:      toolbarIncDecButton,
		NoControl: true,
	}
}

func toolbarSubmenu(id toolbarButtonID) []toolbarMenuEntry {
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
		return geomapToolbarEntries
	case toolbarCursor:
		return cursorToolbarEntries
	case toolbarBrightness:
		return brightnessToolbarEntries
	case toolbarMapBrightness:
		return mapBrightnessToolbarEntries
	case toolbarRadar:
		return radarToolbarEntries
	case toolbarFont:
		return fontToolbarEntries
	case toolbarDBFields:
		return dbFieldsToolbarEntries
	default:
		return nil
	}
}

func (p *ERAMPane) toolbarButtonLines(spec toolbarButtonSpec) [2]string {
	lines := spec.Lines
	switch spec.ID {
	case toolbarRange:
		lines[1] = fmt.Sprintf("%g", p.rangeNM)
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
	if ctx == nil || !p.toolbarVisible {
		return scratch.buttons, scratch.expansions
	}

	metrics := p.toolbarMetrics()
	masterOwner := toolbarOwner{Kind: toolbarOwnerMaster}
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
	p.appendExpandedMenus(masterLayouts, masterOwner, p.toolbar.masterExpansion, metrics)

	paneSize := ctx.PaneSize()
	for i := range p.toolbar.tearoffs {
		tearoff := &p.toolbar.tearoffs[i]
		spec := toolbarSpec(tearoff.Type)
		size := redsmath.Vec2{X: p.toolbarButtonWidth(spec, metrics), Y: metrics.buttonHeight}
		topLeft := toolbarTearoffTopLeft(*tearoff, paneSize, size)
		owner := toolbarOwner{Kind: toolbarOwnerTearoff, TearoffID: tearoff.ID}
		layout := p.appendToolbarButton(spec, topLeft.X, topLeft.Y, owner, 0, 0, metrics)
		p.appendExpandedMenus([]toolbarButtonLayout{layout}, owner, tearoff.Expansion, metrics)
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
		width := p.toolbarButtonWidth(toolbarSpec(entry.ID), metrics)
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
		x := left + offsets[entry.Column]
		y := top + float32(entry.Row)*(metrics.buttonHeight+toolbarButtonRowGap)
		p.appendToolbarButton(toolbarSpec(entry.ID), x, y, owner, depth, entry.Row, metrics)
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
	entries := toolbarSubmenu(parent.Spec.ID)
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
		Bounds: redsmath.NewRect(
			parent.Root.Max.X,
			contentTop-toolbarExpansionBorder,
			contentLeft+width+toolbarExpansionBorder,
			contentTop+height+toolbarExpansionBorder,
		),
	})

	if expansion.Child == "" {
		return
	}
	for _, child := range children {
		if child.Spec.ID != expansion.Child {
			continue
		}
		grandchildren := toolbarSubmenu(child.Spec.ID)
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
	if p == nil || ctx == nil || zcb == nil || !p.toolbarVisible {
		return
	}
	paneWidth := ctx.PaneRect.Width()
	if paneWidth <= 0 || ctx.PaneRect.Height() <= 0 {
		return
	}
	metrics := p.toolbarMetrics()
	buttons, expansions := p.buildToolbarLayout(ctx)

	x, y, width, fbHeight := ctx.PaneFramebufferRect()
	masterCB := zcb.At(zLoweredMasterToolbar)
	prepareToolbarCB(masterCB, ctx, x, y, width, fbHeight)
	drawSolidRect(masterCB, redsmath.NewRect(0, 0, paneWidth, metrics.toolbarHeight), applyERAMBrightness(toolbarGray, p.toolbarBrightness, p.systemBrightness))

	// CRC's visible lower half of the master-toolbar move-down control.
	moveRect := redsmath.NewRect(0, (metrics.toolbarHeight-toolbarInteriorLineWidth)/2, metrics.moveWidth, metrics.toolbarHeight-toolbarInteriorLineWidth)
	p.drawToolbarBorderedRect(masterCB, moveRect, toolbarGray, defaultButtonBrightness, false, toolbarWhite, defaultBorderBrightness, 1)

	for _, expansion := range expansions {
		if expansion.Owner.Kind == toolbarOwnerMaster {
			p.drawToolbarExpansion(masterCB, expansion.Bounds)
		}
	}
	p.drawToolbarButtons(ctx, masterCB, buttons, toolbarOwner{Kind: toolbarOwnerMaster}, metrics)
	p.drawToolbarText(ctx, masterCB, buttons, toolbarOwner{Kind: toolbarOwnerMaster}, metrics)

	// The one-pixel interior line is part of CRC's 73-pixel footprint.
	drawSolidRect(masterCB, redsmath.NewRect(0, metrics.toolbarHeight-1, paneWidth, metrics.toolbarHeight), applyERAMBrightness(toolbarWhite, defaultToolbarBorderBrightness, p.systemBrightness))
	p.drawToolbarArrow(ctx, masterCB, moveRect, metrics)
	masterCB.Blend()
	masterCB.DisableScissor()

	for i := range p.toolbar.tearoffs {
		owner := toolbarOwner{Kind: toolbarOwnerTearoff, TearoffID: p.toolbar.tearoffs[i].ID}
		cb := zcb.At(zTearoffButtons + renderer.Z(i*2))
		prepareToolbarCB(cb, ctx, x, y, width, fbHeight)
		for _, expansion := range expansions {
			if expansion.Owner == owner {
				p.drawToolbarExpansion(cb, expansion.Bounds)
			}
		}
		p.drawToolbarButtons(ctx, cb, buttons, owner, metrics)
		p.drawToolbarText(ctx, cb, buttons, owner, metrics)
		cb.Blend()
		cb.DisableScissor()
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
		drawBorderOnly(cb, bounds, applyERAMBrightness(toolbarWhite, defaultPairedTargetBrightness, p.systemBrightness), 1)
		cb.Blend()
		cb.DisableScissor()
	}
}

func prepareToolbarCB(cb *renderer.CmdBuffer, ctx *panes.Context, x, y, width, height int) {
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.DisableBlend()
}

func (p *ERAMPane) drawToolbarExpansion(cb *renderer.CmdBuffer, bounds redsmath.Rect) {
	p.drawToolbarBorderedRect(
		cb,
		bounds,
		toolbarGray,
		defaultButtonBrightness,
		false,
		toolbarBrightCoral,
		defaultBorderBrightness,
		toolbarExpansionBorder,
	)
}

func (p *ERAMPane) drawToolbarButtons(
	ctx *panes.Context,
	cb *renderer.CmdBuffer,
	buttons []toolbarButtonLayout,
	owner toolbarOwner,
	metrics toolbarMetrics,
) {
	for _, layout := range buttons {
		if layout.Owner != owner {
			continue
		}
		hoverControl := p.toolbar.moving == nil && ctx.Mouse != nil && !layout.Control.Empty() &&
			p.toolbarControlEligible(layout) && layout.Control.Contains(ctx.Mouse.Pos)
		hoverPick := p.toolbar.moving == nil && ctx.Mouse != nil && !layout.Spec.Disabled && layout.Pick.Contains(ctx.Mouse.Pos)

		if !layout.Spec.NoControl {
			controlColor := toolbarBrightGold
			if owner.Kind != toolbarOwnerTearoff && p.hasToolbarTearoff(layout.Spec.ID) {
				controlColor = toolbarGray
			}
			p.drawToolbarBorderedRect(cb, layout.Control, controlColor, defaultButtonBrightness, hoverControl, toolbarWhite, defaultBorderBrightness, 1)
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
		p.drawToolbarBorderedRect(cb, layout.Pick, background, defaultButtonBrightness, hoverPick, toolbarWhite, defaultBorderBrightness, 1)

		if layout.Spec.Kind == toolbarPressHoldButton {
			// CRC's inactive press/hold buttons have a 10x10 gray cut corner.
			x1, y0 := layout.Pick.Max.X-1, layout.Pick.Min.Y+1
			cb.SetRGB(applyERAMBrightness(toolbarGray, defaultButtonBrightness, p.systemBrightness))
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
		if layout.Owner != owner {
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
				Color:      applyERAMBrightness(textColor, defaultTextBrightness, p.systemBrightness).ToRGBA(),
				Background: applyERAMBrightness(background, defaultButtonBrightness, p.systemBrightness).ToRGBA(),
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
		Color:      applyERAMBrightness(toolbarWhite, defaultTextBrightness, p.systemBrightness).ToRGBA(),
		Background: applyERAMBrightness(toolbarGray, defaultButtonBrightness, p.systemBrightness).ToRGBA(),
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
		borderBrightness = defaultPairedTargetBrightness
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
	if p == nil || ctx == nil || !p.toolbarVisible {
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
		return false
	}

	buttons, expansions := p.buildToolbarLayout(ctx)
	pressed := mouse.WasPressed(platform.MouseButtonLeft) || mouse.WasPressed(platform.MouseButtonMiddle)
	if pressed {
		// Later tearoffs and nested menus are rendered above earlier entries.
		for i := len(buttons) - 1; i >= 0; i-- {
			layout := buttons[i]
			if !layout.Control.Empty() && layout.Control.Contains(mouse.Pos) && p.toolbarControlEligible(layout) {
				p.startToolbarMove(ctx, layout, metrics)
				return true
			}
			if layout.Pick.Contains(mouse.Pos) {
				p.toggleToolbarExpansion(layout)
				return true
			}
		}
	}

	// Toolbar UI owns all pointer input over its visible background, expansion
	// panels, and floating copies, even when a particular face has no action.
	if mouse.Pos.Y >= 0 && mouse.Pos.Y < metrics.toolbarHeight && mouse.Pos.X >= 0 && mouse.Pos.X < ctx.PaneRect.Width() {
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

func (p *ERAMPane) toolbarControlEligible(layout toolbarButtonLayout) bool {
	if layout.Spec.NoControl {
		return false
	}
	if layout.Owner.Kind == toolbarOwnerTearoff {
		return true
	}
	return !p.hasToolbarTearoff(layout.Spec.ID)
}

func (p *ERAMPane) startToolbarMove(ctx *panes.Context, layout toolbarButtonLayout, metrics toolbarMetrics) {
	existingID := 0
	if layout.Owner.Kind == toolbarOwnerTearoff {
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
	if len(toolbarSubmenu(layout.Spec.ID)) == 0 {
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
