package asdex

import (
	"encoding/json"
	"fmt"
	"strings"

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

func (p *PreviewArea) BuildTextBlock() TextBlock {
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
		p.state.SafetyLine1,
		p.state.SafetyLine2,
		p.state.FeedbackLine1,
		p.state.FeedbackLine2,
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
) {
	if p == nil || td == nil || font == nil {
		return
	}

	p.list.SetLocation(p.location.Location(displaySize, p.list.style.RepositionSize))
	p.list.Render(td, font, p.BuildTextBlock())
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
