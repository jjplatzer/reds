package asdex

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/renderer"
	"github.com/juliusplatzer/reds/util"
)

const (
	screenListFontSizeMin = 1
	screenListFontSizeMax = 6
)

type TextFragment struct {
	Text       string
	Foreground renderer.RGB
	Background renderer.RGBA
	Underlined bool
	NewLine    bool
}

type TextBlock struct {
	Fragments   []TextFragment
	LineSpacing int
}

type ScreenListStyle struct {
	Location       redsmath.Vec2
	RepositionSize redsmath.Vec2

	FontSize      int
	Brightness    int
	MinBrightness int
	LineSpacing   int

	BaseTextColor renderer.RGB
}

type ScreenList struct {
	style ScreenListStyle
}

func NewScreenList(style ScreenListStyle) ScreenList {
	return ScreenList{style: style}
}

func (l *ScreenList) SetLocation(pos redsmath.Vec2) {
	if l == nil {
		return
	}
	l.style.Location = pos
}

func (l *ScreenList) Location() redsmath.Vec2 {
	if l == nil {
		return redsmath.Vec2{}
	}
	return l.style.Location
}

func (l *ScreenList) SetBrightness(brightness int) {
	if l == nil {
		return
	}
	l.style.Brightness = clampListInt(brightness, brightnessMin, brightnessMax)
}

func (l *ScreenList) Brightness() int {
	if l == nil {
		return 0
	}
	return l.style.Brightness
}

func (l *ScreenList) SetFontSize(size int) {
	if l == nil {
		return
	}
	l.style.FontSize = clampListInt(size, screenListFontSizeMin, screenListFontSizeMax)
}

func (l *ScreenList) FontSize() int {
	if l == nil {
		return 0
	}
	return l.style.FontSize
}

func (l *ScreenList) Render(
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	block TextBlock,
) {
	if l == nil || td == nil || font == nil {
		return
	}

	pos := l.style.Location
	lineHeight := font.LineHeight(l.style.FontSize)
	lineSpacing := block.LineSpacing
	if lineSpacing <= 0 {
		lineSpacing = l.style.LineSpacing
	}

	for _, fragment := range block.Fragments {
		color := fragment.Foreground
		if color == (renderer.RGB{}) {
			color = l.style.BaseTextColor
		}

		style := renderer.TextStyle{
			Size:       l.style.FontSize,
			Color:      applyBrightness(color, l.style.Brightness, l.style.MinBrightness).ToRGBA(),
			Background: fragment.Background,
			Underlined: fragment.Underlined,
		}

		if fragment.Text != "" {
			td.AddText(fragment.Text, pos, style)
		}

		if fragment.NewLine {
			pos.X = l.style.Location.X
			pos.Y += float32(lineHeight + lineSpacing)
		} else if fragment.Text != "" {
			width, _ := font.MeasureText(fragment.Text, l.style.FontSize)
			pos.X += float32(width)
		}
	}
}

type RelativeScreenLocation struct {
	left   *float32
	right  *float32
	top    *float32
	bottom *float32
}

func RelativeScreenLocationFromTopLeft(
	topLeft redsmath.Vec2,
	itemSize redsmath.Vec2,
	displaySize redsmath.Vec2,
) RelativeScreenLocation {
	var out RelativeScreenLocation

	leftMargin := topLeft.X
	rightMargin := displaySize.X - topLeft.X - itemSize.X
	if leftMargin < rightMargin {
		out.left = float32Pointer(leftMargin)
	} else {
		out.right = float32Pointer(rightMargin)
	}

	topMargin := topLeft.Y
	bottomMargin := displaySize.Y - topLeft.Y - itemSize.Y
	if topMargin < bottomMargin {
		out.top = float32Pointer(topMargin)
	} else {
		out.bottom = float32Pointer(bottomMargin)
	}

	return out
}

func (r RelativeScreenLocation) Location(
	displaySize redsmath.Vec2,
	itemSize redsmath.Vec2,
) redsmath.Vec2 {
	var out redsmath.Vec2

	if r.left != nil {
		out.X = *r.left
	} else if r.right != nil {
		out.X = displaySize.X - *r.right - itemSize.X
	}

	if r.top != nil {
		out.Y = *r.top
	} else if r.bottom != nil {
		out.Y = displaySize.Y - *r.bottom - itemSize.Y
	}

	return out
}

type CoastListEntryStatus int

const (
	CoastListEntryCoasting CoastListEntryStatus = iota
	CoastListEntrySuspended
	CoastListEntryDropped
)

const (
	coastListArrowFontSize = 1

	eramCharUpArrow   = rune(128)
	eramCharDownArrow = rune(129)
)

type CoastListEntry struct {
	Status CoastListEntryStatus

	TargetID string
	TrackID  string

	Callsign string
	Beacon   string

	TimeoutSeconds float64
	Selected       bool
}

type CoastListEntryHitType int

const (
	CoastListHitNone CoastListEntryHitType = iota
	CoastListHitHeader
	CoastListHitEntry
	CoastListHitUpArrow
	CoastListHitDownArrow
)

type CoastListEntryHit struct {
	Hit  bool
	Type CoastListEntryHitType

	TargetID string
	TrackID  string
	Status   CoastListEntryStatus
}

type CoastList struct {
	visible  bool
	expanded bool
	offset   int

	location RelativeScreenLocation
	list     ScreenList

	entries []CoastListEntry
}

func NewCoastList() CoastList {
	size := redsmath.Vec2{X: 300, Y: 500}
	defaultDisplay := redsmath.Vec2{X: 1300, Y: 900}
	topLeft := redsmath.Vec2{X: 1000, Y: 150}

	return CoastList{
		visible:  true,
		location: RelativeScreenLocationFromTopLeft(topLeft, size, defaultDisplay),
		list: NewScreenList(ScreenListStyle{
			Location:       topLeft,
			RepositionSize: size,

			FontSize:      2,
			Brightness:    95,
			MinBrightness: 20,
			LineSpacing:   5,

			BaseTextColor: renderer.RGB8(0, 248, 0),
		}),
	}
}

func (l *CoastList) SetVisible(visible bool) {
	if l == nil {
		return
	}
	l.visible = visible
}

func (l *CoastList) Visible() bool {
	return l != nil && l.visible
}

func (l *CoastList) SetEntries(entries []CoastListEntry) {
	if l == nil {
		return
	}
	l.entries = append(l.entries[:0], entries...)
}

func (l *CoastList) SetBrightness(brightness int) {
	if l == nil {
		return
	}
	l.list.SetBrightness(brightness)
}

func (l *CoastList) SetFontSize(size int) {
	if l == nil {
		return
	}
	l.list.SetFontSize(size)
}

func (l *CoastList) FontSize() int {
	if l == nil {
		return 0
	}
	return l.list.FontSize()
}

func (l *CoastList) SetLocation(pos redsmath.Vec2, displaySize redsmath.Vec2) {
	if l == nil {
		return
	}
	l.location = RelativeScreenLocationFromTopLeft(pos, l.list.style.RepositionSize, displaySize)
	l.list.SetLocation(pos)
}

func (l *CoastList) LocationForDisplay(displaySize redsmath.Vec2) redsmath.Vec2 {
	if l == nil {
		return redsmath.Vec2{}
	}
	return l.location.Location(displaySize, l.list.style.RepositionSize)
}

func (l *CoastList) RepositionSize() redsmath.Vec2 {
	if l == nil {
		return redsmath.Vec2{}
	}
	return l.list.style.RepositionSize
}

func (l *CoastList) ToggleExpanded() {
	if l == nil {
		return
	}
	l.expanded = !l.expanded
	l.offset = 0
}

func (l *CoastList) PageUp() {
	if l != nil && l.offset > 0 {
		l.offset--
	}
}

func (l *CoastList) PageDown(font *renderer.BitmapFont, displaySize redsmath.Vec2) {
	if l == nil || font == nil {
		return
	}

	pageSize := l.visibleEntryCount(font, displaySize)
	if pageSize <= 0 || len(l.entries) == 0 {
		return
	}
	page := l.clampedOffset(len(l.entries), pageSize)
	if (page+1)*pageSize < len(l.entries) {
		l.offset = page + 1
	}
}

func (l *CoastList) buildHeaderBlock(now time.Time) TextBlock {
	now = now.UTC()

	return TextBlock{
		LineSpacing: l.list.style.LineSpacing,
		Fragments: []TextFragment{
			{Text: padLeft(now.Format("01/02/06"), 12), NewLine: true},
			{Text: padLeft(now.Format("1504/05"), 12), NewLine: true},
		},
	}
}

func (l *CoastList) buildFullBlock(
	now time.Time,
	font *renderer.BitmapFont,
	displaySize redsmath.Vec2,
) TextBlock {
	block := l.buildHeaderBlock(now)
	ordered := l.orderedEntries()
	pageSize := l.visibleEntryCount(font, displaySize)
	if pageSize <= 0 {
		return block
	}

	page := l.clampedOffset(len(ordered), pageSize)
	start := page * pageSize
	count := min(pageSize, len(ordered)-start)
	for index := 0; index < count; index++ {
		entry := ordered[start+index]
		color := renderer.RGB{}
		if entry.Selected {
			color = renderer.RGB8(255, 255, 255)
		}
		block.Fragments = append(block.Fragments, TextFragment{
			Text:       l.entryLine(entry),
			Foreground: color,
			NewLine:    true,
		})
	}
	return block
}

func (l *CoastList) orderedEntries() []CoastListEntry {
	if l == nil {
		return nil
	}

	ordered := append([]CoastListEntry(nil), l.entries...)
	sort.SliceStable(ordered, func(i, j int) bool {
		iRank := coastListEntryRank(ordered[i].Status)
		jRank := coastListEntryRank(ordered[j].Status)
		if iRank != jRank {
			return iRank < jRank
		}
		return ordered[i].TimeoutSeconds > ordered[j].TimeoutSeconds
	})
	return ordered
}

func (l *CoastList) entryChar(status CoastListEntryStatus) rune {
	switch status {
	case CoastListEntryDropped:
		return 'D'
	case CoastListEntrySuspended:
		return 'S'
	default:
		return 'C'
	}
}

func (l *CoastList) entryLine(entry CoastListEntry) string {
	id := padRight(truncateRunes(strings.TrimSpace(entry.TrackID), 3), 3)

	label := strings.TrimSpace(entry.Callsign)
	if label == "" {
		label = strings.TrimSpace(entry.Beacon)
		if label != "" {
			label = zeroPadLeft(label, 4)
		}
	}
	if label == "" {
		label = "NO DATA"
	}
	label = padRight(truncateRunes(label, 8), 8)

	return fmt.Sprintf("%c %s %s", l.entryChar(entry.Status), id, label)
}

func (l *CoastList) visibleEntryCount(font *renderer.BitmapFont, displaySize redsmath.Vec2) int {
	if l == nil || font == nil {
		return 0
	}

	rowStep := font.LineHeight(l.FontSize()) + l.list.style.LineSpacing
	if rowStep <= 0 {
		return 0
	}
	if !l.expanded {
		return 5
	}

	location := l.LocationForDisplay(displaySize)
	available := int(displaySize.Y - (location.Y + 2*float32(rowStep)))
	return max(1, available/rowStep)
}

func (l *CoastList) clampedOffset(entryCount int, pageSize int) int {
	if l == nil || entryCount <= 0 || pageSize <= 0 {
		return 0
	}

	offset := max(0, l.offset)
	for offset > 0 && offset*pageSize >= entryCount {
		offset--
	}
	return offset
}

func (l *CoastList) headerBounds(font *renderer.BitmapFont, displaySize redsmath.Vec2) redsmath.Rect {
	if l == nil || !l.visible || font == nil {
		return redsmath.Rect{}
	}

	location := l.LocationForDisplay(displaySize)
	width, _ := font.MeasureText(padLeft("01/02/06", 12), l.FontSize())
	if listWidth := l.listWidth(font); listWidth > float32(width) {
		width = int(listWidth)
	}
	height := float32(font.LineHeight(l.FontSize()))*2.8 + 5
	return redsmath.NewRect(location.X, location.Y, location.X+float32(width), location.Y+height)
}

func (l *CoastList) listWidth(font *renderer.BitmapFont) float32 {
	if l == nil || font == nil {
		return 0
	}

	width, _ := font.MeasureText(strings.Repeat(" ", 15), l.FontSize())
	return float32(width)
}

func (l *CoastList) listBottomY(font *renderer.BitmapFont, displaySize redsmath.Vec2) float32 {
	if l == nil || font == nil {
		return 0
	}

	header := l.headerBounds(font, displaySize)
	if header.Empty() {
		return 0
	}

	ordered := l.orderedEntries()
	pageSize := l.visibleEntryCount(font, displaySize)
	if pageSize <= 0 {
		return header.Max.Y
	}

	page := l.clampedOffset(len(ordered), pageSize)
	start := page * pageSize
	count := min(pageSize, len(ordered)-start)
	if count <= 0 {
		return header.Max.Y
	}

	rowStep := font.LineHeight(l.FontSize()) + l.list.style.LineSpacing
	return header.Max.Y + float32(count*rowStep) - float32(l.list.style.LineSpacing)
}

func (l *CoastList) entryRowBounds(
	visibleIndex int,
	font *renderer.BitmapFont,
	displaySize redsmath.Vec2,
) redsmath.Rect {
	if l == nil || font == nil || visibleIndex < 0 {
		return redsmath.Rect{}
	}

	lineHeight := font.LineHeight(l.FontSize())
	if lineHeight <= 0 {
		return redsmath.Rect{}
	}

	location := l.LocationForDisplay(displaySize)
	rowWidth := l.listWidth(font)
	rowStep := lineHeight + l.list.style.LineSpacing
	y := location.Y + 2*float32(rowStep) + float32(visibleIndex*rowStep)
	return redsmath.NewRect(location.X, y, location.X+rowWidth, y+float32(lineHeight))
}

func (l *CoastList) upArrowBounds(
	asdexFont *renderer.BitmapFont,
	eramTextFont *renderer.BitmapFont,
	displaySize redsmath.Vec2,
) redsmath.Rect {
	if l == nil || asdexFont == nil || eramTextFont == nil {
		return redsmath.Rect{}
	}

	ordered := l.orderedEntries()
	pageSize := l.visibleEntryCount(asdexFont, displaySize)
	page := l.clampedOffset(len(ordered), pageSize)
	if page <= 0 {
		return redsmath.Rect{}
	}

	header := l.headerBounds(asdexFont, displaySize)
	if header.Empty() {
		return redsmath.Rect{}
	}

	location := l.LocationForDisplay(displaySize)
	listWidth := l.listWidth(asdexFont)
	asdexCharWidth, _ := asdexFont.CharSize(l.FontSize())
	eramArrowWidth, eramArrowHeight := eramTextFont.CharSize(coastListArrowFontSize)
	if listWidth <= 0 || asdexCharWidth <= 0 || eramArrowWidth <= 0 || eramArrowHeight <= 0 {
		return redsmath.Rect{}
	}

	x := location.X + listWidth - float32(asdexCharWidth)
	y := header.Max.Y
	return redsmath.NewRect(x, y, x+float32(eramArrowWidth), y+float32(eramArrowHeight))
}

func (l *CoastList) downArrowBounds(
	asdexFont *renderer.BitmapFont,
	eramTextFont *renderer.BitmapFont,
	displaySize redsmath.Vec2,
) redsmath.Rect {
	if l == nil || asdexFont == nil || eramTextFont == nil {
		return redsmath.Rect{}
	}

	ordered := l.orderedEntries()
	pageSize := l.visibleEntryCount(asdexFont, displaySize)
	if pageSize <= 0 {
		return redsmath.Rect{}
	}

	page := l.clampedOffset(len(ordered), pageSize)
	start := page * pageSize
	count := min(pageSize, len(ordered)-start)
	if count <= 0 || start+count >= len(ordered) {
		return redsmath.Rect{}
	}

	location := l.LocationForDisplay(displaySize)
	listWidth := l.listWidth(asdexFont)
	asdexCharWidth, _ := asdexFont.CharSize(l.FontSize())
	eramArrowWidth, eramArrowHeight := eramTextFont.CharSize(coastListArrowFontSize)
	if listWidth <= 0 || asdexCharWidth <= 0 || eramArrowWidth <= 0 || eramArrowHeight <= 0 {
		return redsmath.Rect{}
	}

	x := location.X + listWidth - float32(asdexCharWidth)
	y := l.listBottomY(asdexFont, displaySize) - float32(eramArrowHeight)
	return redsmath.NewRect(x, y, x+float32(eramArrowWidth), y+float32(eramArrowHeight))
}

func (l *CoastList) HitTest(
	point redsmath.Vec2,
	asdexFont *renderer.BitmapFont,
	eramTextFont *renderer.BitmapFont,
	displaySize redsmath.Vec2,
) CoastListEntryHit {
	if l == nil || !l.visible || asdexFont == nil {
		return CoastListEntryHit{}
	}
	if l.headerBounds(asdexFont, displaySize).Contains(point) {
		return CoastListEntryHit{Hit: true, Type: CoastListHitHeader}
	}
	if bounds := l.upArrowBounds(asdexFont, eramTextFont, displaySize); !bounds.Empty() && bounds.Contains(point) {
		return CoastListEntryHit{Hit: true, Type: CoastListHitUpArrow}
	}
	if bounds := l.downArrowBounds(asdexFont, eramTextFont, displaySize); !bounds.Empty() && bounds.Contains(point) {
		return CoastListEntryHit{Hit: true, Type: CoastListHitDownArrow}
	}

	ordered := l.orderedEntries()
	pageSize := l.visibleEntryCount(asdexFont, displaySize)
	if pageSize <= 0 {
		return CoastListEntryHit{}
	}
	page := l.clampedOffset(len(ordered), pageSize)
	start := page * pageSize
	count := min(pageSize, len(ordered)-start)
	for index := 0; index < count; index++ {
		if !l.entryRowBounds(index, asdexFont, displaySize).Contains(point) {
			continue
		}

		entry := ordered[start+index]
		return CoastListEntryHit{
			Hit:      true,
			Type:     CoastListHitEntry,
			TargetID: entry.TargetID,
			TrackID:  entry.TrackID,
			Status:   entry.Status,
		}
	}
	return CoastListEntryHit{}
}

func (l *CoastList) Render(
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	displaySize redsmath.Vec2,
) {
	if l == nil || !l.visible || td == nil || font == nil {
		return
	}

	l.list.SetLocation(l.LocationForDisplay(displaySize))
	l.list.Render(td, font, l.buildFullBlock(time.Now().UTC(), font, displaySize))
}

func (l *CoastList) RenderOverflowArrows(
	cb *renderer.CmdBuffer,
	asdexFont *renderer.BitmapFont,
	eramTextFont *renderer.BitmapFont,
	displaySize redsmath.Vec2,
	textureForEramSize func(size int) renderer.TextureID,
) {
	if l == nil || !l.visible || cb == nil || asdexFont == nil || eramTextFont == nil || textureForEramSize == nil {
		return
	}

	textureID := textureForEramSize(coastListArrowFontSize)
	if textureID == 0 {
		return
	}

	style := renderer.TextStyle{
		Size:  coastListArrowFontSize,
		Color: applyBrightness(l.list.style.BaseTextColor, l.list.style.Brightness, l.list.style.MinBrightness).ToRGBA(),
	}

	td := renderer.GetTextDrawBuilder()
	defer renderer.ReturnTextDrawBuilder(td)

	td.SetFont(eramTextFont)
	if bounds := l.downArrowBounds(asdexFont, eramTextFont, displaySize); !bounds.Empty() {
		td.AddText(string(eramCharDownArrow), bounds.Min, style)
	}
	if bounds := l.upArrowBounds(asdexFont, eramTextFont, displaySize); !bounds.Empty() {
		td.AddText(string(eramCharUpArrow), bounds.Min, style)
	}
	td.GenerateCommands(cb, textureID)
}

func coastListEntryRank(status CoastListEntryStatus) int {
	switch status {
	case CoastListEntryCoasting:
		return 0
	case CoastListEntrySuspended:
		return 1
	case CoastListEntryDropped:
		return 2
	default:
		return 3
	}
}

type PreviewAreaState struct {
	RunwayConfigName string
	TowerPositions   []string

	SystemResponse string

	SafetyLine1 string
	SafetyLine2 string

	FeedbackLine1 string
	FeedbackLine2 string

	TowerRunwayAssignmentsActive bool
	TowerRunwayAssignmentLines   []string
}

type previewAirportConfig struct {
	Airport string `json:"airport"`

	RunwayConfigurations []previewRunwayConfiguration `json:"runwayConfigurations"`
	TowerPositions       []previewTowerPosition       `json:"towerPositions"`
}

type previewRunwayConfiguration struct {
	Number             int      `json:"number"`
	Name               string   `json:"name"`
	ArrivalRunwayIDs   []string `json:"arrivalRunwayIds"`
	DepartureRunwayIDs []string `json:"departureRunwayIds"`
	Default            bool     `json:"default"`
}

type previewTowerPosition struct {
	Name      string   `json:"name"`
	RunwayIDs []string `json:"runwayIds"`
	Default   bool     `json:"default"`
}

func DefaultPreviewAreaState() PreviewAreaState {
	return PreviewAreaState{
		RunwayConfigName: "LIMITED",
		TowerPositions:   []string{"GC"},
		SystemResponse:   "CRITICAL FAULT START",
	}
}

type PreviewArea struct {
	location RelativeScreenLocation
	list     ScreenList
	state    PreviewAreaState
}

func NewPreviewArea() PreviewArea {
	size := redsmath.Vec2{X: 300, Y: 500}
	defaultDisplay := redsmath.Vec2{X: 1300, Y: 900}
	topLeft := redsmath.Vec2{X: 50, Y: 150}

	return PreviewArea{
		location: RelativeScreenLocationFromTopLeft(topLeft, size, defaultDisplay),
		list: NewScreenList(ScreenListStyle{
			Location:       topLeft,
			RepositionSize: size,

			FontSize:      2,
			Brightness:    95,
			MinBrightness: 20,
			LineSpacing:   3,

			BaseTextColor: renderer.RGB8(0, 248, 0),
		}),
		state: DefaultPreviewAreaState(),
	}
}

func (p *PreviewArea) FontSize() int {
	if p == nil {
		return 0
	}
	return p.list.FontSize()
}

func (p *PreviewArea) Brightness() int {
	if p == nil {
		return 0
	}
	return p.list.Brightness()
}

func (p *PreviewArea) SetLocation(pos redsmath.Vec2, displaySize redsmath.Vec2) {
	if p == nil {
		return
	}
	p.location = RelativeScreenLocationFromTopLeft(pos, p.list.style.RepositionSize, displaySize)
	p.list.SetLocation(pos)
}

func (p *PreviewArea) RepositionSize() redsmath.Vec2 {
	if p == nil {
		return redsmath.Vec2{}
	}
	return p.list.style.RepositionSize
}

func (p *PreviewArea) SetBrightness(brightness int) {
	if p == nil {
		return
	}
	p.list.SetBrightness(brightness)
}

func (p *PreviewArea) SetFontSize(size int) {
	if p == nil {
		return
	}
	p.list.SetFontSize(size)
}

func (p *PreviewArea) LoadDefaultStateFromAirportConfig(airport string) error {
	if p == nil {
		return nil
	}

	airport = strings.ToUpper(strings.TrimSpace(airport))
	if airport == "" {
		return nil
	}

	path := "resources/configs/asdex/" + airport + ".json"
	if !util.ResourceExists(path) {
		return fmt.Errorf("preview area: airport config %s not found", path)
	}

	var config previewAirportConfig
	if err := json.Unmarshal(util.LoadResourceBytes(path), &config); err != nil {
		return fmt.Errorf("preview area: decode %s: %w", path, err)
	}

	if name := defaultRunwayConfigName(config.RunwayConfigurations); name != "" {
		p.SetRunwayConfigName(name)
	}
	if positions := defaultTowerPositionNames(config.TowerPositions); len(positions) > 0 {
		p.SetTowerPositions(positions)
	}

	return nil
}

func (p *PreviewArea) SetRunwayConfigName(name string) {
	if p == nil {
		return
	}
	p.state.RunwayConfigName = strings.TrimSpace(name)
}

func (p *PreviewArea) RunwayConfigName() string {
	if p == nil {
		return "LIMITED"
	}
	return strings.TrimSpace(p.state.RunwayConfigName)
}

func (p *PreviewArea) SetTowerPositions(positions []string) {
	if p == nil {
		return
	}
	p.state.TowerPositions = append([]string(nil), positions...)
}

func (p *PreviewArea) SetSystemResponse(text string) {
	if p == nil {
		return
	}
	p.state.SystemResponse = text
}

func (p *PreviewArea) SetFeedback(line1, line2 string) {
	if p == nil {
		return
	}
	p.state.FeedbackLine1 = line1
	p.state.FeedbackLine2 = line2
}

func (p *PreviewArea) ClearFeedback() {
	p.SetFeedback("", "")
}

func (p *PreviewArea) SetArrivalAlertsDisabled(positions []string) {
	if p == nil {
		return
	}
	if len(positions) == 0 {
		p.state.SafetyLine1 = ""
		return
	}
	p.state.SafetyLine1 = "ARR ALERTS OFF:" + strings.Join(positions, ",")
}

func (p *PreviewArea) SetTrackAlertsInhibited(inhibited bool) {
	if p == nil {
		return
	}
	if inhibited {
		p.state.SafetyLine2 = "TRK ALERT INHIB"
	} else {
		p.state.SafetyLine2 = ""
	}
}

func (p *PreviewArea) SetTowerRunwayAssignments(lines []string) {
	if p == nil {
		return
	}
	p.state.TowerRunwayAssignmentLines = append([]string(nil), lines...)
}

func (p *PreviewArea) SetTowerRunwayAssignmentsActive(active bool) {
	if p == nil {
		return
	}
	p.state.TowerRunwayAssignmentsActive = active
}

func (p *PreviewArea) BuildTextBlock(commandLines []string) TextBlock {
	if p == nil {
		return TextBlock{}
	}
	if p.state.TowerRunwayAssignmentsActive {
		return p.buildTowerRunwayAssignmentBlock()
	}

	lines := []string{
		"RWY CFG: " + strings.TrimSpace(p.state.RunwayConfigName),
		"TWR CFG:" + strings.Join(p.state.TowerPositions, ","),
		p.state.SystemResponse,
	}
	if strings.TrimSpace(p.state.SafetyLine1) != "" {
		lines = append(lines, p.state.SafetyLine1)
	}
	if strings.TrimSpace(p.state.SafetyLine2) != "" {
		lines = append(lines, p.state.SafetyLine2)
	}
	lines = append(lines, commandLines...)
	if strings.TrimSpace(p.state.FeedbackLine1) != "" {
		lines = append(lines, p.state.FeedbackLine1)
	}
	if strings.TrimSpace(p.state.FeedbackLine2) != "" {
		lines = append(lines, p.state.FeedbackLine2)
	}

	block := TextBlock{LineSpacing: p.list.style.LineSpacing}
	for _, line := range lines {
		block.Fragments = append(block.Fragments, TextFragment{
			Text:    line,
			NewLine: true,
		})
	}
	return block
}

func (p *PreviewArea) buildTowerRunwayAssignmentBlock() TextBlock {
	block := TextBlock{LineSpacing: p.list.style.LineSpacing}
	for _, line := range p.state.TowerRunwayAssignmentLines {
		block.Fragments = append(block.Fragments, TextFragment{
			Text:    line,
			NewLine: true,
		})
	}
	return block
}

func (p *PreviewArea) Render(
	td *renderer.TextDrawBuilder,
	font *renderer.BitmapFont,
	displaySize redsmath.Vec2,
	commandLines []string,
) {
	if p == nil || td == nil || font == nil {
		return
	}

	p.list.SetLocation(p.location.Location(displaySize, p.list.style.RepositionSize))
	p.list.Render(td, font, p.BuildTextBlock(commandLines))
}

func (p *PreviewArea) BaseLineCount() int {
	if p == nil {
		return 0
	}

	count := 3
	if strings.TrimSpace(p.state.SafetyLine1) != "" {
		count++
	}
	if strings.TrimSpace(p.state.SafetyLine2) != "" {
		count++
	}
	return count
}

func (p *PreviewArea) TextRGB() renderer.RGB {
	if p == nil {
		return renderer.RGB{}
	}
	return applyBrightness(p.list.style.BaseTextColor, p.list.style.Brightness, p.list.style.MinBrightness)
}

func (p *PreviewArea) RenderCommandCursor(
	builder *renderer.LinesBuilder,
	font *renderer.BitmapFont,
	displaySize redsmath.Vec2,
	cursorLine int,
	cursorColumn int,
	baseLineCount int,
) {
	if p == nil || builder == nil || font == nil || cursorLine <= 0 || cursorColumn < 0 {
		return
	}

	location := p.location.Location(displaySize, p.list.style.RepositionSize)
	p.list.SetLocation(location)

	charWidth, _ := font.CharSize(p.list.FontSize())
	lineHeight := font.LineHeight(p.list.FontSize())
	fontSpacing := font.FontSpacing(p.list.FontSize())
	if charWidth <= 0 || lineHeight <= 0 {
		return
	}

	x := location.X +
		float32(charWidth*cursorColumn) +
		float32(fontSpacing*max(0, cursorColumn-1))
	y := location.Y +
		float32(lineHeight+p.list.style.LineSpacing)*
			float32(cursorLine+baseLineCount)

	builder.AddLine(
		renderer.PointVertex{X: x, Y: y},
		renderer.PointVertex{X: x + float32(charWidth), Y: y},
	)
}

func defaultRunwayConfigName(configs []previewRunwayConfiguration) string {
	for _, config := range configs {
		if config.Default {
			return strings.TrimSpace(config.Name)
		}
	}
	if len(configs) > 0 {
		return strings.TrimSpace(configs[0].Name)
	}
	return ""
}

func defaultTowerPositionNames(positions []previewTowerPosition) []string {
	var out []string
	for _, position := range positions {
		if !position.Default {
			continue
		}
		if name := strings.TrimSpace(position.Name); name != "" {
			out = append(out, name)
		}
	}
	if len(out) > 0 {
		return out
	}

	if len(positions) > 0 {
		if name := strings.TrimSpace(positions[0].Name); name != "" {
			return []string{name}
		}
	}
	return nil
}

func float32Pointer(value float32) *float32 {
	return &value
}

func clampListInt(value, lo, hi int) int {
	if value < lo {
		return lo
	}
	if value > hi {
		return hi
	}
	return value
}

func padLeft(value string, width int) string {
	runes := []rune(value)
	for len(runes) < width {
		runes = append([]rune{' '}, runes...)
	}
	return string(runes)
}

func padRight(value string, width int) string {
	runes := []rune(value)
	for len(runes) < width {
		runes = append(runes, ' ')
	}
	return string(runes)
}

func zeroPadLeft(value string, width int) string {
	runes := []rune(value)
	for len(runes) < width {
		runes = append([]rune{'0'}, runes...)
	}
	return string(runes)
}

func truncateRunes(value string, width int) string {
	runes := []rune(value)
	if len(runes) > width {
		runes = runes[:width]
	}
	return string(runes)
}
