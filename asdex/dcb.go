package asdex

import (
	"strconv"
	"strings"
	"unicode"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/renderer"
)

type DcbPosition int

const (
	DcbTop DcbPosition = iota
	DcbBottom
	DcbLeft
	DcbRight
	DcbOff
)

type DcbMenu int

const (
	DcbMenuMain DcbMenu = iota
	DcbMenuTempData
	DcbMenuClosedRunway
	DcbMenuOff
)

type DcbButtonKind int

const (
	DcbButtonNormal DcbButtonKind = iota
	DcbButtonMenu
	DcbButtonValue
	DcbButtonToggle
	DcbButtonError
	DcbButtonVacant
	DcbButtonConfig
)

type DcbFunction int

const (
	DcbFunctionVacant DcbFunction = iota
	DcbFunctionRange
	DcbFunctionMapReposition
	DcbFunctionRotate
	DcbFunctionUndo
	DcbFunctionDefault
	DcbFunctionPrefs
	DcbFunctionDayNite
	DcbFunctionBrightness
	DcbFunctionCharSize
	DcbFunctionSafetyLogic
	DcbFunctionTools
	DcbFunctionVectorOnOff
	DcbFunctionVectorLength
	DcbFunctionTempData
	DcbFunctionClosedRunway
	DcbFunctionCloseRunway
	DcbFunctionStoredGlobalTempData
	DcbFunctionDefineClosedArea
	DcbFunctionDefineRestrictedArea
	DcbFunctionDefineTempText
	DcbFunctionShowHiddenTempData
	DcbFunctionHideTempData
	DcbFunctionDeleteGlobalTempData
	DcbFunctionDone
	DcbFunctionLeaderLength
	DcbFunctionLocal1
	DcbFunctionLocal2
	DcbFunctionDataBlockArea
	DcbFunctionDataBlockEdit
	DcbFunctionDataBlocksOnOff
	DcbFunctionInitControl
	DcbFunctionTrackSuspend
	DcbFunctionTermControl
	DcbFunctionDcbOnOff
	DcbFunctionOperationalMode
	DcbFunctionMlatOff
	DcbFunctionAsrOff
)

const (
	dcbButtonSpacing   = 3
	dcbColumnCount     = 14
	dcbMinBrightness   = 20
	dcbTextLineSpacing = 4
)

var (
	dcbBackgroundRGB  = renderer.RGB8(56, 56, 56)
	dcbMenuSlabRGB    = renderer.RGB8(100, 100, 100)
	dcbButtonRGB      = renderer.RGB8(56, 56, 56)
	dcbMenuButtonRGB  = renderer.RGB8(80, 80, 80)
	dcbDepressedRGB   = renderer.RGB8(45, 45, 45)
	dcbErrorButtonRGB = renderer.RGB8(255, 0, 0)
	dcbTextRGB        = renderer.RGB8(255, 255, 255)
	dcbTextHoverRGB   = renderer.RGB8(0, 255, 0)
	dcbHighlightRGB   = renderer.RGB8(255, 220, 40)
)

type Dcb struct {
	visible    bool
	position   DcbPosition
	menu       DcbMenu
	brightness int
	charSize   int
}

type DcbButtonSpec struct {
	Function  DcbFunction
	Kind      DcbButtonKind
	Large     bool
	Visible   bool
	Depressed bool
	Active    bool

	Lines     []string
	ShowValue bool
	Value     string

	OnLabel  string
	OffLabel string
	On       bool

	ConfigID int
	Label    string
}

type DcbButtonLayout struct {
	Spec   DcbButtonSpec
	Bounds redsmath.Rect
	Index  int
}

type DcbLayout struct {
	Bounds     redsmath.Rect
	MenuBounds redsmath.Rect
	Collapsed  bool

	ButtonSize redsmath.Vec2
	MenuSize   redsmath.Vec2

	AutoSize       int
	RenderFontSize int

	Buttons []DcbButtonLayout
}

type DcbState struct {
	Range        int
	Mode         Mode
	VectorOn     bool
	VectorLength int
	LeaderLength int
	DataBlocksOn bool
	DcbOn        bool

	ClosedRunways []DcbRunwayClosureState

	ActiveSpinnerFunction DcbFunction
}

type DcbRunwayClosureState struct {
	ID       string
	IsClosed bool
}

type DcbSpinnerKind int

const (
	DcbSpinnerNone DcbSpinnerKind = iota
	DcbSpinnerRange
)

type DcbSpinner struct {
	Kind     DcbSpinnerKind
	Function DcbFunction

	WindowID ScopeWindowID

	Title string

	Min  int
	Max  int
	Step int

	Value    int
	Original int

	input  string
	cursor int
}

type DcbMenuCommand struct {
	lines []string
}

func NewDcbMenuCommand(lines ...string) *DcbMenuCommand {
	return &DcbMenuCommand{lines: append([]string(nil), lines...)}
}

func (c *DcbMenuCommand) DisplayLines() []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.lines...)
}

func NewRangeDcbSpinner(windowID ScopeWindowID, currentRange int) *DcbSpinner {
	currentRange = clampInt(currentRange, asdexMinRangeSetting, asdexMaxRangeSetting)

	return &DcbSpinner{
		Kind:     DcbSpinnerRange,
		Function: DcbFunctionRange,
		WindowID: windowID,
		Title:    "RANGE",
		Min:      asdexMinRangeSetting,
		Max:      asdexMaxRangeSetting,
		Step:     1,
		Value:    currentRange,
		Original: currentRange,
		input:    "",
		cursor:   0,
	}
}

func (s *DcbSpinner) DisplayLines() []string {
	if s == nil {
		return nil
	}
	return []string{s.Title, s.InputText()}
}

func (s *DcbSpinner) CursorLine() int {
	return 2
}

func (s *DcbSpinner) CursorColumn() int {
	if s == nil {
		return 0
	}
	return s.cursor
}

func (s *DcbSpinner) InputText() string {
	if s == nil {
		return ""
	}
	return s.input
}

func (s *DcbSpinner) SetValue(value int) {
	if s == nil {
		return
	}

	value = clampInt(value, s.Min, s.Max)
	s.Value = value
	s.input = strconv.Itoa(value)
	s.cursor = len(s.input)
}

func (s *DcbSpinner) Increment(delta int) {
	if s == nil || delta == 0 {
		return
	}

	step := s.Step
	if step <= 0 {
		step = 1
	}

	value := s.Value
	if parsed, ok := s.ParsedValue(); ok {
		value = parsed
	}
	s.SetValue(value + delta*step)
}

func (s *DcbSpinner) Insert(r rune) {
	if s == nil || !unicode.IsDigit(r) {
		return
	}

	value := []rune(s.input)
	if len(value) >= 3 {
		return
	}

	s.cursor = clampInt(s.cursor, 0, len(value))
	value = append(value[:s.cursor], append([]rune{r}, value[s.cursor:]...)...)
	s.input = string(value)
	s.cursor++
}

func (s *DcbSpinner) Backspace() {
	if s == nil || s.cursor <= 0 {
		return
	}

	value := []rune(s.input)
	s.cursor = clampInt(s.cursor, 0, len(value))
	if s.cursor <= 0 {
		return
	}

	s.cursor--
	value = append(value[:s.cursor], value[s.cursor+1:]...)
	s.input = string(value)
}

func (s *DcbSpinner) DeleteForward() {
	if s == nil {
		return
	}

	value := []rune(s.input)
	s.cursor = clampInt(s.cursor, 0, len(value))
	if s.cursor >= len(value) {
		return
	}

	value = append(value[:s.cursor], value[s.cursor+1:]...)
	s.input = string(value)
}

func (s *DcbSpinner) MoveLeft() {
	if s != nil && s.cursor > 0 {
		s.cursor--
	}
}

func (s *DcbSpinner) MoveRight() {
	if s == nil {
		return
	}

	value := []rune(s.input)
	if s.cursor < len(value) {
		s.cursor++
	}
}

func (s *DcbSpinner) ParsedValue() (int, bool) {
	if s == nil {
		return 0, false
	}

	text := strings.TrimSpace(s.input)
	if text == "" {
		return 0, false
	}

	value, err := strconv.Atoi(text)
	if err != nil || value < s.Min || value > s.Max {
		return 0, false
	}
	return value, true
}

func NewDcb() Dcb {
	return Dcb{
		visible:    true,
		position:   DcbTop,
		menu:       DcbMenuMain,
		brightness: brightnessDefault,
		charSize:   2,
	}
}

// TODO(DCB): Keep all layout code position-aware. CRC supports TOP, BOTTOM,
// LEFT, and RIGHT DCB positions. Button click behavior is still being added,
// but Layout already returns correct bar/slab/button bounds for all positions.
func (p DcbPosition) IsHorizontal() bool {
	return p == DcbTop || p == DcbBottom
}

func (d *Dcb) Visible() bool {
	return d != nil && d.visible && d.position != DcbOff
}

func (d *Dcb) On() bool {
	return d != nil && d.menu != DcbMenuOff
}

func (d *Dcb) Collapsed() bool {
	return d != nil && d.menu == DcbMenuOff
}

func (d *Dcb) ToggleOnOff() {
	if d == nil {
		return
	}

	if d.menu == DcbMenuOff {
		d.menu = DcbMenuMain
	} else {
		d.menu = DcbMenuOff
	}
}

func (d *Dcb) SetMenu(menu DcbMenu) {
	if d == nil {
		return
	}
	d.menu = menu
}

func (d *Dcb) Menu() DcbMenu {
	if d == nil {
		return DcbMenuOff
	}
	return d.menu
}

func (d *Dcb) ReturnToMainMenu() {
	if d == nil {
		return
	}
	if d.menu != DcbMenuOff {
		d.menu = DcbMenuMain
	}
}

func (d *Dcb) SetPosition(position DcbPosition) {
	if d == nil {
		return
	}
	d.position = position
}

func (d *Dcb) Position() DcbPosition {
	if d == nil {
		return DcbOff
	}
	return d.position
}

func (d *Dcb) SetCharSize(size int) {
	if d == nil {
		return
	}
	d.charSize = clampInt(size, 1, 3)
}

func (d *Dcb) buttonSizeForFont(font *renderer.BitmapFont, autoSize int) redsmath.Vec2 {
	if font == nil {
		return redsmath.Vec2{}
	}

	_, charHeight := font.CharSize(autoSize)
	if charHeight <= 0 {
		return redsmath.Vec2{}
	}

	buttonHeight := float32(charHeight*2 + 9)
	return redsmath.Vec2{
		X: buttonHeight * 3,
		Y: buttonHeight,
	}
}

func horizontalDcbMenuSize(button redsmath.Vec2) redsmath.Vec2 {
	return redsmath.Vec2{
		X: (button.X+float32(dcbButtonSpacing))*float32(dcbColumnCount) + float32(dcbButtonSpacing),
		Y: button.Y*2 + 9,
	}
}

func verticalDcbMenuSize(button redsmath.Vec2) redsmath.Vec2 {
	return redsmath.Vec2{
		X: button.X + 6,
		Y: button.Y*float32(dcbColumnCount)*2 + 87,
	}
}

func offDcbMenuSize(button redsmath.Vec2) redsmath.Vec2 {
	return redsmath.Vec2{
		X: button.X + 6,
		Y: button.Y*2 + 9,
	}
}

func (d *Dcb) buttonColor(spec DcbButtonSpec) renderer.RGB {
	if d == nil {
		return dcbButtonRGB
	}
	if spec.Depressed {
		return applyBrightness(dcbDepressedRGB, d.brightness, dcbMinBrightness)
	}

	switch spec.Kind {
	case DcbButtonMenu:
		return applyBrightness(dcbMenuButtonRGB, d.brightness, dcbMinBrightness)
	case DcbButtonError:
		return applyBrightness(dcbErrorButtonRGB, d.brightness, dcbMinBrightness)
	default:
		return applyBrightness(dcbButtonRGB, d.brightness, dcbMinBrightness)
	}
}

func isLargeDcbFunction(function DcbFunction) bool {
	switch function {
	case DcbFunctionRange,
		DcbFunctionSafetyLogic,
		DcbFunctionTools,
		DcbFunctionClosedRunway,
		DcbFunctionStoredGlobalTempData,
		DcbFunctionDefineClosedArea,
		DcbFunctionDefineRestrictedArea,
		DcbFunctionDefineTempText,
		DcbFunctionShowHiddenTempData,
		DcbFunctionHideTempData,
		DcbFunctionDeleteGlobalTempData,
		DcbFunctionDone,
		DcbFunctionVacant:
		return true
	default:
		return false
	}
}

func (d *Dcb) mainButtonSpecs(state DcbState) []DcbButtonSpec {
	applyState := func(spec DcbButtonSpec) DcbButtonSpec {
		if state.ActiveSpinnerFunction == spec.Function {
			spec.Active = true
		}
		return spec
	}
	normal := func(function DcbFunction, lines ...string) DcbButtonSpec {
		return applyState(DcbButtonSpec{
			Function: function,
			Kind:     DcbButtonNormal,
			Large:    isLargeDcbFunction(function),
			Visible:  true,
			Lines:    append([]string(nil), lines...),
		})
	}
	menu := func(function DcbFunction, lines ...string) DcbButtonSpec {
		return applyState(DcbButtonSpec{
			Function: function,
			Kind:     DcbButtonMenu,
			Large:    isLargeDcbFunction(function),
			Visible:  true,
			Lines:    append([]string(nil), lines...),
		})
	}
	value := func(function DcbFunction, showValue bool, value string, lines ...string) DcbButtonSpec {
		return applyState(DcbButtonSpec{
			Function:  function,
			Kind:      DcbButtonValue,
			Large:     isLargeDcbFunction(function),
			Visible:   true,
			Lines:     append([]string(nil), lines...),
			ShowValue: showValue,
			Value:     value,
		})
	}
	toggle := func(function DcbFunction, on bool, onLabel string, offLabel string, lines ...string) DcbButtonSpec {
		return applyState(DcbButtonSpec{
			Function: function,
			Kind:     DcbButtonToggle,
			Large:    isLargeDcbFunction(function),
			Visible:  true,
			Lines:    append([]string(nil), lines...),
			On:       on,
			OnLabel:  onLabel,
			OffLabel: offLabel,
		})
	}

	return []DcbButtonSpec{
		value(DcbFunctionRange, true, d.rangeLabel(state), "RANGE"),
		normal(DcbFunctionMapReposition, "MAP", "RPOS"),
		value(DcbFunctionRotate, false, "", "ROTATE"),
		normal(DcbFunctionUndo, "UNDO"),
		normal(DcbFunctionDefault, "DEFAULT"),
		menu(DcbFunctionPrefs, "PREF"),
		toggle(DcbFunctionDayNite, state.Mode == ModeDay, "DAY", "NITE"),
		menu(DcbFunctionBrightness, "BRITE"),
		menu(DcbFunctionCharSize, "CHAR", "SIZE"),
		menu(DcbFunctionSafetyLogic, "SAFETY", "LOGIC", "LIMITED"),
		menu(DcbFunctionTools, "TOOLS"),
		toggle(DcbFunctionVectorOnOff, state.VectorOn, "ON", "OFF", "VECTOR"),
		value(DcbFunctionVectorLength, true, d.vectorLengthLabel(state), "VECTOR"),
		menu(DcbFunctionTempData, "TEMP", "DATA"),
		value(DcbFunctionLeaderLength, true, d.leaderLengthLabel(state), "LDR LNG"),
		menu(DcbFunctionLocal1, "LOCAL", "101-188"),
		menu(DcbFunctionLocal2, "LOCAL", "189-276"),
		menu(DcbFunctionDataBlockArea, "DB", "AREA"),
		menu(DcbFunctionDataBlockEdit, "DB EDIT"),
		toggle(DcbFunctionDataBlocksOnOff, state.DataBlocksOn, "ON", "OFF", "DB"),
		normal(DcbFunctionInitControl, "INIT", "CNTL"),
		normal(DcbFunctionTrackSuspend, "TRK", "SUSP"),
		normal(DcbFunctionTermControl, "TERM", "CNTL"),
		toggle(DcbFunctionDcbOnOff, state.DcbOn, "ON", "OFF", "DCB"),
		menu(DcbFunctionOperationalMode, "OPER", "MODE"),
	}
}

func (d *Dcb) offButtonSpecs(state DcbState) []DcbButtonSpec {
	applyState := func(spec DcbButtonSpec) DcbButtonSpec {
		if state.ActiveSpinnerFunction == spec.Function {
			spec.Active = true
		}
		return spec
	}
	toggle := func(function DcbFunction, on bool, onLabel string, offLabel string, lines ...string) DcbButtonSpec {
		return applyState(DcbButtonSpec{
			Function: function,
			Kind:     DcbButtonToggle,
			Visible:  true,
			Lines:    append([]string(nil), lines...),
			On:       on,
			OnLabel:  onLabel,
			OffLabel: offLabel,
		})
	}
	menu := func(function DcbFunction, lines ...string) DcbButtonSpec {
		return applyState(DcbButtonSpec{
			Function: function,
			Kind:     DcbButtonMenu,
			Visible:  true,
			Lines:    append([]string(nil), lines...),
		})
	}

	return []DcbButtonSpec{
		toggle(DcbFunctionDcbOnOff, state.DcbOn, "ON", "OFF", "DCB"),
		menu(DcbFunctionOperationalMode, "OPER", "MODE"),
	}
}

func (d *Dcb) tempDataButtonSpecs(state DcbState) []DcbButtonSpec {
	applyState := func(spec DcbButtonSpec) DcbButtonSpec {
		if state.ActiveSpinnerFunction == spec.Function {
			spec.Active = true
		}
		return spec
	}
	normal := func(function DcbFunction, lines ...string) DcbButtonSpec {
		return applyState(DcbButtonSpec{
			Function: function,
			Kind:     DcbButtonNormal,
			Large:    isLargeDcbFunction(function),
			Visible:  true,
			Lines:    append([]string(nil), lines...),
		})
	}
	menu := func(function DcbFunction, lines ...string) DcbButtonSpec {
		return applyState(DcbButtonSpec{
			Function: function,
			Kind:     DcbButtonMenu,
			Large:    isLargeDcbFunction(function),
			Visible:  true,
			Lines:    append([]string(nil), lines...),
		})
	}
	vacant := func() DcbButtonSpec {
		return DcbButtonSpec{
			Function: DcbFunctionVacant,
			Kind:     DcbButtonVacant,
			Large:    true,
			Visible:  true,
		}
	}

	return []DcbButtonSpec{
		vacant(),
		vacant(),
		vacant(),
		menu(DcbFunctionClosedRunway, "CLOSED", "RWY"),
		menu(DcbFunctionStoredGlobalTempData, "STORED", "GLOBAL", "TEMP", "DATA"),
		normal(DcbFunctionDefineClosedArea, "DEFINE", "CLOSED", "AREA"),
		normal(DcbFunctionDefineRestrictedArea, "DEFINE", "RESTR", "AREA"),
		normal(DcbFunctionDefineTempText, "DEFINE", "TEXT"),
		normal(DcbFunctionShowHiddenTempData, "SHOW", "HIDDEN", "DATA"),
		normal(DcbFunctionHideTempData, "HIDE", "DATA"),
		normal(DcbFunctionDeleteGlobalTempData, "DELETE", "GLOBAL"),
		normal(DcbFunctionDone, "DONE"),
		vacant(),
		vacant(),
	}
}

func (d *Dcb) closedRunwayButtonSpecs(state DcbState) []DcbButtonSpec {
	applyState := func(spec DcbButtonSpec) DcbButtonSpec {
		if state.ActiveSpinnerFunction == spec.Function {
			spec.Active = true
		}
		return spec
	}
	config := func(id int, label string, isClosed bool) DcbButtonSpec {
		return applyState(DcbButtonSpec{
			Function: DcbFunctionCloseRunway,
			Kind:     DcbButtonConfig,
			Visible:  true,
			ConfigID: id,
			Label:    label,
			On:       !isClosed,
			OnLabel:  "OPN",
			OffLabel: "CLSD",
		})
	}

	buttons := make([]DcbButtonSpec, 0, 27)
	for i := 1; i <= 26; i++ {
		label := ""
		isClosed := false
		if i <= len(state.ClosedRunways) {
			label = state.ClosedRunways[i-1].ID
			isClosed = state.ClosedRunways[i-1].IsClosed
		}
		buttons = append(buttons, config(i, label, isClosed))
	}

	buttons = append(buttons, applyState(DcbButtonSpec{
		Function: DcbFunctionDone,
		Kind:     DcbButtonNormal,
		Large:    isLargeDcbFunction(DcbFunctionDone),
		Visible:  true,
		Lines:    []string{"DONE"},
	}))

	return buttons
}

func (d *Dcb) rangeLabel(state DcbState) string {
	return strconv.Itoa(clampInt(state.Range, asdexMinRangeSetting, asdexMaxRangeSetting))
}

func (d *Dcb) vectorLengthLabel(state DcbState) string {
	return strconv.Itoa(state.VectorLength)
}

func (d *Dcb) leaderLengthLabel(state DcbState) string {
	return strconv.Itoa(state.LeaderLength)
}

func (d *Dcb) buttonSpecs(state DcbState) []DcbButtonSpec {
	if d == nil {
		return nil
	}

	switch d.menu {
	case DcbMenuOff:
		return d.offButtonSpecs(state)
	case DcbMenuTempData:
		return d.tempDataButtonSpecs(state)
	case DcbMenuClosedRunway:
		return d.closedRunwayButtonSpecs(state)
	default:
		return d.mainButtonSpecs(state)
	}
}

func (d *Dcb) Layout(displaySize redsmath.Vec2, font *renderer.BitmapFont, state DcbState) DcbLayout {
	var out DcbLayout
	if d == nil || !d.Visible() || font == nil || displaySize.X <= 0 || displaySize.Y <= 0 {
		return out
	}

	if d.Collapsed() {
		return d.collapsedLayout(displaySize, font, state)
	}

	autoSize := 3
	var buttonSize redsmath.Vec2
	var menuSize redsmath.Vec2
	for autoSize >= 1 {
		buttonSize = d.buttonSizeForFont(font, autoSize)
		if buttonSize.X <= 0 || buttonSize.Y <= 0 {
			return DcbLayout{}
		}

		if d.position.IsHorizontal() {
			menuSize = horizontalDcbMenuSize(buttonSize)
			if autoSize == 1 || displaySize.X >= menuSize.X {
				break
			}
		} else {
			menuSize = verticalDcbMenuSize(buttonSize)
			if autoSize == 1 || displaySize.Y >= menuSize.Y {
				break
			}
		}
		autoSize--
	}

	out.AutoSize = autoSize
	out.RenderFontSize = autoSize
	charSize := clampInt(d.charSize, 1, 3)
	if charSize < out.RenderFontSize {
		out.RenderFontSize = charSize
	}
	out.ButtonSize = buttonSize
	out.MenuSize = menuSize

	switch d.position {
	case DcbTop:
		out.Bounds = redsmath.NewRect(0, 0, displaySize.X, menuSize.Y)
		menuX := float32(0)
		if displaySize.X > menuSize.X {
			menuX = (displaySize.X - menuSize.X) * 0.5
		}
		out.MenuBounds = redsmath.NewRect(menuX, 0, menuX+menuSize.X, menuSize.Y)

	case DcbBottom:
		y := displaySize.Y - menuSize.Y
		if y < 0 {
			y = 0
		}
		out.Bounds = redsmath.NewRect(0, y, displaySize.X, y+menuSize.Y)
		menuX := float32(0)
		if displaySize.X > menuSize.X {
			menuX = (displaySize.X - menuSize.X) * 0.5
		}
		out.MenuBounds = redsmath.NewRect(menuX, y, menuX+menuSize.X, y+menuSize.Y)

	case DcbLeft:
		out.Bounds = redsmath.NewRect(0, 0, menuSize.X, displaySize.Y)
		menuY := float32(0)
		if displaySize.Y > menuSize.Y {
			menuY = (displaySize.Y - menuSize.Y) * 0.5
		}
		out.MenuBounds = redsmath.NewRect(0, menuY, menuSize.X, menuY+menuSize.Y)

	case DcbRight:
		x := displaySize.X - menuSize.X
		if x < 0 {
			x = 0
		}
		out.Bounds = redsmath.NewRect(x, 0, x+menuSize.X, displaySize.Y)
		menuY := float32(0)
		if displaySize.Y > menuSize.Y {
			menuY = (displaySize.Y - menuSize.Y) * 0.5
		}
		out.MenuBounds = redsmath.NewRect(x, menuY, x+menuSize.X, menuY+menuSize.Y)
	}

	out.Buttons = d.layoutButtons(out.MenuBounds, out.ButtonSize, d.buttonSpecs(state))
	return out
}

func (d *Dcb) collapsedLayout(
	displaySize redsmath.Vec2,
	font *renderer.BitmapFont,
	state DcbState,
) DcbLayout {
	var out DcbLayout

	autoSize := 3
	var buttonSize redsmath.Vec2
	var menuSize redsmath.Vec2
	for autoSize >= 1 {
		buttonSize = d.buttonSizeForFont(font, autoSize)
		if buttonSize.X <= 0 || buttonSize.Y <= 0 {
			return DcbLayout{}
		}

		menuSize = offDcbMenuSize(buttonSize)
		if autoSize == 1 || (displaySize.X >= menuSize.X && displaySize.Y >= menuSize.Y) {
			break
		}
		autoSize--
	}

	out.Collapsed = true
	out.AutoSize = autoSize
	out.RenderFontSize = autoSize
	charSize := clampInt(d.charSize, 1, 3)
	if charSize < out.RenderFontSize {
		out.RenderFontSize = charSize
	}
	out.ButtonSize = buttonSize
	out.MenuSize = menuSize

	x := displaySize.X - menuSize.X
	if x < 0 {
		x = 0
	}

	out.Bounds = redsmath.NewRect(x, 0, x+menuSize.X, menuSize.Y)
	out.MenuBounds = out.Bounds
	out.Buttons = layoutHorizontalDcbButtons(out.MenuBounds, out.ButtonSize, d.offButtonSpecs(state))
	return out
}

func (d *Dcb) layoutButtons(
	menuBounds redsmath.Rect,
	buttonSize redsmath.Vec2,
	specs []DcbButtonSpec,
) []DcbButtonLayout {
	if d == nil || menuBounds.Empty() || buttonSize.X <= 0 || buttonSize.Y <= 0 {
		return nil
	}
	if d.position.IsHorizontal() {
		return layoutHorizontalDcbButtons(menuBounds, buttonSize, specs)
	}
	return layoutVerticalDcbButtons(menuBounds, buttonSize, specs)
}

func layoutHorizontalDcbButtons(
	menuBounds redsmath.Rect,
	buttonSize redsmath.Vec2,
	specs []DcbButtonSpec,
) []DcbButtonLayout {
	buttons := make([]DcbButtonLayout, 0, len(specs))
	row := 1
	column := 1

	for _, spec := range specs {
		if !spec.Visible {
			continue
		}
		if column > dcbColumnCount {
			break
		}

		x := menuBounds.Min.X +
			float32(column*dcbButtonSpacing) +
			float32(column-1)*buttonSize.X

		y := menuBounds.Min.Y + float32(dcbButtonSpacing)
		if row == 2 {
			y = menuBounds.Min.Y + float32(2*dcbButtonSpacing) + buttonSize.Y
		}

		height := buttonSize.Y
		if spec.Large {
			height = buttonSize.Y*2 + float32(dcbButtonSpacing)
		}

		buttons = append(buttons, DcbButtonLayout{
			Spec: spec,
			Bounds: redsmath.NewRect(
				x,
				y,
				x+buttonSize.X,
				y+height,
			),
			Index: len(buttons),
		})

		if row == 2 || (row == 1 && spec.Large) {
			column++
			row = 1
		} else {
			row = 2
		}
	}

	return buttons
}

func layoutVerticalDcbButtons(
	menuBounds redsmath.Rect,
	buttonSize redsmath.Vec2,
	specs []DcbButtonSpec,
) []DcbButtonLayout {
	buttons := make([]DcbButtonLayout, 0, len(specs))

	x := menuBounds.Min.X + float32(dcbButtonSpacing)
	y := menuBounds.Min.Y + float32(dcbButtonSpacing)

	for _, spec := range specs {
		if !spec.Visible {
			continue
		}

		height := buttonSize.Y
		if spec.Large {
			height = buttonSize.Y*2 + float32(dcbButtonSpacing)
		}
		if y+height > menuBounds.Max.Y {
			break
		}

		buttons = append(buttons, DcbButtonLayout{
			Spec: spec,
			Bounds: redsmath.NewRect(
				x,
				y,
				x+buttonSize.X,
				y+height,
			),
			Index: len(buttons),
		})

		y += height + float32(dcbButtonSpacing)
	}

	return buttons
}

func (d *Dcb) DrawBackground(cb *renderer.CmdBuffer, layout DcbLayout) {
	if d == nil || cb == nil || layout.Bounds.Empty() {
		return
	}

	builder := renderer.GetColoredTrianglesBuilder()
	defer renderer.ReturnColoredTrianglesBuilder(builder)

	menuSlab := applyBrightness(dcbMenuSlabRGB, d.brightness, dcbMinBrightness)
	if layout.Collapsed {
		addDcbRect(builder, layout.Bounds, menuSlab)
		builder.GenerateCommands(cb)
		return
	}

	background := applyBrightness(dcbBackgroundRGB, d.brightness, dcbMinBrightness)
	addDcbRect(builder, layout.Bounds, background)
	if !layout.MenuBounds.Empty() {
		addDcbRect(builder, layout.MenuBounds, menuSlab)
	}

	builder.GenerateCommands(cb)
}

func (d *Dcb) DrawButtons(cb *renderer.CmdBuffer, layout DcbLayout) {
	if d == nil || cb == nil || layout.Bounds.Empty() || len(layout.Buttons) == 0 {
		return
	}

	builder := renderer.GetColoredTrianglesBuilder()
	defer renderer.ReturnColoredTrianglesBuilder(builder)

	for _, button := range layout.Buttons {
		if button.Bounds.Empty() {
			continue
		}
		addDcbRect(builder, button.Bounds, d.buttonColor(button.Spec))
	}

	builder.GenerateCommands(cb)
}

func (d *Dcb) DrawText(
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	layout DcbLayout,
	hoveredIndex int,
) {
	if d == nil || td == nil || font == nil || layout.RenderFontSize <= 0 || len(layout.Buttons) == 0 {
		return
	}

	td.SetFont(font)
	for _, button := range layout.Buttons {
		if button.Bounds.Empty() {
			continue
		}

		d.drawButtonText(td, font, layout.RenderFontSize, button, button.Index == hoveredIndex)
	}
}

func (d *Dcb) drawButtonText(
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	fontSize int,
	button DcbButtonLayout,
	hovering bool,
) {
	spec := button.Spec
	switch spec.Kind {
	case DcbButtonToggle:
		d.drawToggleButtonText(td, font, fontSize, button, hovering)
		return
	case DcbButtonConfig:
		d.drawConfigButtonText(td, font, fontSize, button, hovering)
		return
	}

	d.drawCenteredTextLines(
		td,
		font,
		fontSize,
		button.Bounds,
		d.textLinesForButton(spec),
		d.primaryTextColor(spec, hovering),
	)
}

func (d *Dcb) textLinesForButton(spec DcbButtonSpec) []string {
	lines := append([]string(nil), spec.Lines...)
	if spec.Kind != DcbButtonValue || !spec.ShowValue {
		return lines
	}

	value := strings.TrimSpace(spec.Value)
	if value == "" {
		value = "0"
	}

	if len(lines) > 1 {
		if _, err := strconv.Atoi(strings.TrimSpace(lines[1])); err == nil {
			lines[1] = value
			return lines
		}
		if len(lines) < 3 {
			return append(lines, value)
		}
		lines[2] = value
		return lines
	}

	return append(lines, value)
}

func (d *Dcb) drawCenteredTextLines(
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	fontSize int,
	bounds redsmath.Rect,
	lines []string,
	color renderer.RGB,
) {
	if td == nil || font == nil || bounds.Empty() || len(lines) == 0 {
		return
	}

	type measuredLine struct {
		text   string
		width  int
		height int
	}

	measured := make([]measuredLine, 0, len(lines))
	totalHeight := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		width, height := font.MeasureText(line, fontSize)
		if height <= 0 {
			continue
		}

		measured = append(measured, measuredLine{text: line, width: width, height: height})
		totalHeight += height
	}
	if len(measured) == 0 {
		return
	}

	totalHeight += (len(measured) - 1) * dcbTextLineSpacing
	y := bounds.Min.Y + (bounds.Height()-float32(totalHeight))*0.5
	style := renderer.TextStyle{
		Size:  fontSize,
		Color: color.ToRGBA(),
	}

	for i, line := range measured {
		x := bounds.Min.X + (bounds.Width()-float32(line.width))*0.5
		td.AddText(line.text, redsmath.Vec2{X: x, Y: y}, style)

		y += float32(line.height)
		if i != len(measured)-1 {
			y += dcbTextLineSpacing
		}
	}
}

func (d *Dcb) drawToggleButtonText(
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	fontSize int,
	button DcbButtonLayout,
	hovering bool,
) {
	if td == nil || font == nil || button.Bounds.Empty() {
		return
	}

	spec := button.Spec
	labelRows := make([]string, 0, len(spec.Lines))
	totalHeight := 0
	for _, line := range spec.Lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		_, height := font.MeasureText(line, fontSize)
		if height <= 0 {
			continue
		}
		labelRows = append(labelRows, line)
		totalHeight += height + dcbTextLineSpacing
	}

	toggleWidth, toggleHeight := d.measureToggleFragments(font, fontSize, spec)
	if toggleHeight <= 0 {
		return
	}
	totalHeight += toggleHeight

	bounds := button.Bounds
	y := bounds.Min.Y + (bounds.Height()-float32(totalHeight))*0.5
	primary := d.primaryTextColor(spec, hovering)
	stylePrimary := renderer.TextStyle{
		Size:  fontSize,
		Color: primary.ToRGBA(),
	}

	for _, line := range labelRows {
		width, height := font.MeasureText(line, fontSize)
		x := bounds.Min.X + (bounds.Width()-float32(width))*0.5
		td.AddText(line, redsmath.Vec2{X: x, Y: y}, stylePrimary)
		y += float32(height + dcbTextLineSpacing)
	}

	x := bounds.Min.X + (bounds.Width()-float32(toggleWidth))*0.5
	d.drawToggleFragments(td, font, fontSize, x, y, spec, primary)
}

func (d *Dcb) drawConfigButtonText(
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	fontSize int,
	button DcbButtonLayout,
	hovering bool,
) {
	if td == nil || font == nil || button.Bounds.Empty() {
		return
	}

	spec := button.Spec
	if strings.TrimSpace(spec.Label) == "" {
		return
	}

	labelWidth, labelHeight := font.MeasureText(spec.Label, fontSize)
	stateWidth, stateHeight := d.measureToggleFragments(font, fontSize, spec)
	if labelHeight <= 0 || stateHeight <= 0 {
		return
	}

	bounds := button.Bounds
	totalHeight := labelHeight + dcbTextLineSpacing + stateHeight
	y := bounds.Min.Y + (bounds.Height()-float32(totalHeight))*0.5

	primary := d.primaryTextColor(spec, hovering)
	td.AddText(
		spec.Label,
		redsmath.Vec2{
			X: bounds.Min.X + (bounds.Width()-float32(labelWidth))*0.5,
			Y: y,
		},
		renderer.TextStyle{
			Size:  fontSize,
			Color: primary.ToRGBA(),
		},
	)

	x := bounds.Min.X + (bounds.Width()-float32(stateWidth))*0.5
	y += float32(labelHeight + dcbTextLineSpacing)
	d.drawToggleFragments(td, font, fontSize, x, y, spec, primary)
}

func (d *Dcb) measureToggleFragments(
	font *renderer.BitmapFont,
	fontSize int,
	spec DcbButtonSpec,
) (width int, height int) {
	if font == nil {
		return 0, 0
	}

	for _, fragment := range d.toggleFragments(spec) {
		fragmentWidth, fragmentHeight := font.MeasureText(fragment, fontSize)
		width += fragmentWidth
		if fragmentHeight > height {
			height = fragmentHeight
		}
	}
	return width, height
}

func (d *Dcb) drawToggleFragments(
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	fontSize int,
	x float32,
	y float32,
	spec DcbButtonSpec,
	primary renderer.RGB,
) {
	if td == nil || font == nil {
		return
	}

	fragments := d.toggleFragments(spec)
	highlight := d.highlightTextColor()
	colors := []renderer.RGB{primary, primary, primary}
	if spec.On {
		colors[0] = highlight
	} else {
		colors[2] = highlight
	}

	for i, fragment := range fragments {
		if fragment == "" {
			continue
		}
		td.AddText(fragment, redsmath.Vec2{X: x, Y: y}, renderer.TextStyle{
			Size:  fontSize,
			Color: colors[i].ToRGBA(),
		})

		width, _ := font.MeasureText(fragment, fontSize)
		x += float32(width)
	}
}

func (d *Dcb) toggleFragments(spec DcbButtonSpec) []string {
	onLabel := strings.TrimSpace(spec.OnLabel)
	if onLabel == "" {
		onLabel = "ON"
	}
	offLabel := strings.TrimSpace(spec.OffLabel)
	if offLabel == "" {
		offLabel = "OFF"
	}
	return []string{onLabel, "/", offLabel}
}

func (d *Dcb) primaryTextColor(spec DcbButtonSpec, hovering bool) renderer.RGB {
	if d == nil {
		return dcbTextRGB
	}
	if spec.Kind == DcbButtonError {
		return applyBrightness(dcbTextRGB, d.brightness, dcbMinBrightness)
	}
	if spec.Active {
		return d.highlightTextColor()
	}
	if hovering && !spec.Depressed {
		return applyBrightness(dcbTextHoverRGB, d.brightness, dcbMinBrightness)
	}
	return d.normalTextColor()
}

func (d *Dcb) highlightTextColor() renderer.RGB {
	if d == nil {
		return dcbHighlightRGB
	}
	return applyBrightness(dcbHighlightRGB, d.brightness, dcbMinBrightness)
}

func (d *Dcb) normalTextColor() renderer.RGB {
	if d == nil {
		return dcbTextRGB
	}
	return applyBrightness(dcbTextRGB, d.brightness, dcbMinBrightness)
}

func addDcbRect(builder *renderer.ColoredTrianglesBuilder, rect redsmath.Rect, color renderer.RGB) {
	if builder == nil || rect.Empty() {
		return
	}

	builder.AddQuad(
		renderer.PointVertex{X: rect.Min.X, Y: rect.Min.Y},
		renderer.PointVertex{X: rect.Max.X, Y: rect.Min.Y},
		renderer.PointVertex{X: rect.Max.X, Y: rect.Max.Y},
		renderer.PointVertex{X: rect.Min.X, Y: rect.Max.Y},
		color,
	)
}

type DcbHit struct {
	OverDcb     bool
	ButtonIndex int
	Function    DcbFunction
	HasFunction bool
	ConfigID    int
	Label       string
	Spec        DcbButtonSpec
}

func (d *Dcb) HitTest(
	point redsmath.Vec2,
	displaySize redsmath.Vec2,
	font *renderer.BitmapFont,
	state DcbState,
) DcbHit {
	hit := DcbHit{ButtonIndex: -1}
	layout := d.Layout(displaySize, font, state)
	if layout.Bounds.Empty() || !layout.Bounds.Contains(point) {
		return hit
	}

	hit.OverDcb = true
	for i, button := range layout.Buttons {
		if !button.Bounds.Contains(point) {
			continue
		}

		hit.ButtonIndex = i
		hit.Spec = button.Spec
		hit.ConfigID = button.Spec.ConfigID
		hit.Label = button.Spec.Label
		if button.Spec.Function != DcbFunctionVacant {
			if button.Spec.Kind != DcbButtonConfig || strings.TrimSpace(button.Spec.Label) != "" {
				hit.Function = button.Spec.Function
				hit.HasFunction = true
			}
		}
		break
	}
	return hit
}

func (d *Dcb) Contains(
	point redsmath.Vec2,
	displaySize redsmath.Vec2,
	font *renderer.BitmapFont,
	state DcbState,
) bool {
	return d.HitTest(point, displaySize, font, state).OverDcb
}
