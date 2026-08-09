package eram

import (
	"strings"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/renderer"
)

// CRC ViewChecklist / ChecklistViewSettings defaults.
const (
	defaultChecklistLines               = 21
	defaultChecklistFontSize            = 2
	defaultChecklistHighlightBrightness = 40
	defaultChecklistBrightness          = 76
	checklistBorderWidth                = 1
	checklistEntryGap                   = 1
	checklistTextYPadding               = 3
)

var checklistGray = renderer.RGB8(199, 199, 199) // EramColor.Gray

type eramChecklistType uint8

const (
	eramChecklistNone eramChecklistType = iota
	eramChecklistPositionRelief
	eramChecklistEmergency
)

type eramChecklistState struct {
	active              eramChecklistType
	location            eramAnchoredLocation
	lines               int
	fontSize            int
	highlightBrightness int
	brightness          int
	showBorder          bool
	isOpaque            bool

	positionRelief []string
	emergency      []string
	selected       map[int]bool
}

type eramChecklistLayout struct {
	Bounds        redsmath.Rect
	Header        redsmath.Rect
	Menu          redsmath.Rect
	Title         redsmath.Rect
	Suppress      redsmath.Rect
	Body          redsmath.Rect
	EntryText     []redsmath.Rect
	Entries       []string
	LongestChars  int
	EntryHeight   float32
	WrapperPadX   float32
	WrapperPadY   float32
	HeaderHeight  float32
	ContentOrigin redsmath.Vec2
}

func (p *ERAMPane) initializeChecklistState(facility Facility) {
	if p == nil {
		return
	}
	p.checklist = eramChecklistState{
		// ViewListMenuSettingsBase.Location = TopLeft (20, 110).
		location: eramAnchoredLocation{
			Offset: redsmath.Vec2{X: 20, Y: 110},
			Anchor: eramViewAnchorTopLeft,
		},
		lines:               defaultChecklistLines,
		fontSize:            defaultChecklistFontSize,
		highlightBrightness: defaultChecklistHighlightBrightness,
		brightness:          defaultChecklistBrightness,
		showBorder:          true,
		positionRelief:      append([]string(nil), facility.PositionReliefChecklist...),
		emergency:           append([]string(nil), facility.EmergencyChecklist...),
		selected:            make(map[int]bool),
	}
}

func (p *ERAMPane) toggleChecklist(kind eramChecklistType) {
	if p == nil || kind == eramChecklistNone {
		return
	}
	if p.checklist.active == kind {
		p.checklist.active = eramChecklistNone
		p.checklist.selected = make(map[int]bool)
		return
	}
	p.checklist.active = kind
	// CRC BuildChecklist constructs fresh Text nodes whenever the active type
	// changes, so selection emphasis is reset on checklist switches.
	p.checklist.selected = make(map[int]bool)
}

func (p *ERAMPane) checklistEntries() []string {
	if p == nil {
		return nil
	}
	switch p.checklist.active {
	case eramChecklistPositionRelief:
		return p.checklist.positionRelief
	case eramChecklistEmergency:
		return p.checklist.emergency
	default:
		return nil
	}
}

func (p *ERAMPane) checklistTitle() string {
	switch p.checklist.active {
	case eramChecklistPositionRelief:
		return "POS CHECK"
	case eramChecklistEmergency:
		return "EMERG CHECK"
	default:
		return ""
	}
}

func (p *ERAMPane) checklistLayout(paneSize redsmath.Vec2) eramChecklistLayout {
	var layout eramChecklistLayout
	if p == nil || p.checklist.active == eramChecklistNone {
		return layout
	}
	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return layout
	}
	charAdvance, lineHeight := font.CharSize(p.checklist.fontSize)
	if charAdvance <= 0 || lineHeight <= 0 {
		return layout
	}

	entries := p.checklistEntries()
	if len(entries) == 0 {
		return layout
	}
	visibleCount := minInt(len(entries), p.checklist.lines)
	entries = entries[:visibleCount]
	longest := 0
	for _, entry := range entries {
		if n := len([]rune(entry)); n > longest {
			longest = n
		}
	}
	if longest == 0 {
		longest = 1
	}

	// ViewChecklist creates mEntriesWrapper with 0.5-character padding and a
	// 1 px gap. Each Text adds CRC's standard 3 px top/bottom padding.
	wrapperPadX := float32(int(float64(charAdvance) * 0.5))
	wrapperPadY := float32(int(float64(lineHeight) * 0.5))
	entryHeight := float32(lineHeight + 2*checklistTextYPadding)
	bodyWidth := float32(2*checklistBorderWidth) + 2*wrapperPadX + float32(longest*charAdvance)
	bodyHeight := float32(2*checklistBorderWidth) + 2*wrapperPadY +
		float32(visibleCount)*entryHeight + float32(maxInt(0, visibleCount-1)*checklistEntryGap)

	// Header, MenuPickArea and SuppressPickArea use font size 2 regardless of
	// the list font. Header Text is 11 px high at size 2, has 3 px Y padding,
	// and a 1 px border. HeaderRow overlaps the body by one pixel.
	headerCharAdvance, headerLineHeight := font.CharSize(2)
	if headerCharAdvance <= 0 || headerLineHeight <= 0 {
		return layout
	}
	headerHeight := float32(headerLineHeight + 2*checklistTextYPadding + 2*checklistBorderWidth)
	headerRowHeight := headerHeight - 1
	// MinWidth = 2 chars - 1 px, then 1 px border on both sides and a -1 px
	// inner margin. The resulting M and suppression cells are each 24 px with
	// the extracted CRC font (12 px advance), matching CRC's Node calculation.
	headerSideWidth := float32(2 * headerCharAdvance)

	viewWidth := bodyWidth
	minimumHeaderWidth := 2*headerSideWidth + float32(len(p.checklistTitle())*headerCharAdvance+2*checklistTextYPadding+2*checklistBorderWidth)
	if viewWidth < minimumHeaderWidth {
		viewWidth = minimumHeaderWidth
	}
	viewHeight := headerRowHeight + bodyHeight
	size := redsmath.Vec2{X: viewWidth, Y: viewHeight}
	topLeft := resolveERAMAnchoredLocation(p.checklist.location, size, paneSize)
	topLeft = clampERAMViewPosition(topLeft, size, paneSize)

	layout.Bounds = redsmath.NewRect(topLeft.X, topLeft.Y, topLeft.X+viewWidth, topLeft.Y+viewHeight)
	layout.Header = redsmath.NewRect(topLeft.X, topLeft.Y, topLeft.X+viewWidth, topLeft.Y+headerHeight)
	layout.Menu = redsmath.NewRect(topLeft.X, topLeft.Y, topLeft.X+headerSideWidth, topLeft.Y+headerHeight)
	layout.Suppress = redsmath.NewRect(topLeft.X+viewWidth-headerSideWidth, topLeft.Y, topLeft.X+viewWidth, topLeft.Y+headerHeight)
	layout.Title = redsmath.NewRect(layout.Menu.Max.X-1, topLeft.Y, layout.Suppress.Min.X+1, topLeft.Y+headerHeight)
	layout.Body = redsmath.NewRect(topLeft.X, topLeft.Y+headerRowHeight, topLeft.X+viewWidth, topLeft.Y+viewHeight)
	layout.Entries = entries
	layout.LongestChars = longest
	layout.EntryHeight = entryHeight
	layout.WrapperPadX = wrapperPadX
	layout.WrapperPadY = wrapperPadY
	layout.HeaderHeight = headerHeight
	layout.ContentOrigin = redsmath.Vec2{
		X: layout.Body.Min.X + checklistBorderWidth + wrapperPadX,
		Y: layout.Body.Min.Y + checklistBorderWidth + wrapperPadY,
	}
	layout.EntryText = make([]redsmath.Rect, len(entries))
	textWidth := float32(longest * charAdvance)
	for i := range entries {
		y := layout.ContentOrigin.Y + float32(i)*(entryHeight+checklistEntryGap)
		// Text.HandleMouseMove uses the text circumscription box: two pixels
		// around the measured text, not the whole body row.
		layout.EntryText[i] = redsmath.NewRect(
			layout.ContentOrigin.X-2,
			y+checklistTextYPadding-2,
			layout.ContentOrigin.X+textWidth+2,
			y+checklistTextYPadding+float32(lineHeight)+2,
		)
	}
	return layout
}

func (p *ERAMPane) checklistBounds(paneSize redsmath.Vec2) redsmath.Rect {
	return p.checklistLayout(paneSize).Bounds
}

func (p *ERAMPane) consumeChecklistInput(ctx *panes.Context) bool {
	if p == nil || ctx == nil || ctx.Mouse == nil || p.checklist.active == eramChecklistNone {
		return false
	}
	layout := p.checklistLayout(ctx.PaneSize())
	if layout.Bounds.Empty() || !layout.Bounds.Contains(ctx.Mouse.Pos) {
		return false
	}
	left := ctx.Mouse.WasPressed(platform.MouseButtonLeft)
	middle := ctx.Mouse.WasPressed(platform.MouseButtonMiddle)
	if !left && !middle {
		return false
	}

	// ViewListBase's M menu button requests ChecklistView settings. REDS does
	// not yet expose the full list-settings popup; consume the exact pick area
	// now so it does not leak through to the scope.
	if layout.Menu.Contains(ctx.Mouse.Pos) {
		return true
	}
	// The centered header is the MovableViewBase move handle. CRC permits TBP
	// or TBE here and then captures placement until the next TBP/TBE or Escape.
	if layout.Title.Contains(ctx.Mouse.Pos) {
		p.startViewMove(ctx, eramViewChecklist)
		return true
	}
	if layout.Suppress.Contains(ctx.Mouse.Pos) {
		p.checklist.active = eramChecklistNone
		p.checklist.selected = make(map[int]bool)
		return true
	}
	for i, bounds := range layout.EntryText {
		if bounds.Contains(ctx.Mouse.Pos) {
			p.checklist.selected[i] = !p.checklist.selected[i]
			return true
		}
	}
	// CRC consumes picks in the body even when they do not hit an entry.
	return true
}

func (p *ERAMPane) drawChecklist(ctx *panes.Context, zcb *renderer.ZCmdBuffer) {
	if p == nil || ctx == nil || zcb == nil || ctx.Renderer == nil || p.checklist.active == eramChecklistNone {
		return
	}
	layout := p.checklistLayout(ctx.PaneSize())
	if layout.Bounds.Empty() {
		return
	}
	p.ensureToolbarFont()
	font := p.toolbar.font
	if font == nil {
		return
	}
	texture := p.toolbarTexture(ctx.Renderer, p.checklist.fontSize)
	headerTexture := p.toolbarTexture(ctx.Renderer, 2)
	if texture == 0 || headerTexture == 0 {
		return
	}
	_, lineHeight := font.CharSize(p.checklist.fontSize)
	if lineHeight <= 0 {
		return
	}

	x, y, width, height := ctx.PaneFramebufferRect()
	z := zChecklistViewSemiTransparent
	if p.checklist.isOpaque {
		z = zChecklistViewOpaque
	}
	cb := zcb.At(z)
	cb.Viewport(x, y, width, height)
	cb.Scissor(x, y, width, height)
	cb.LoadProjectionMatrix(ctx.ScreenProjection())
	cb.DisableBlend()

	border := applyERAMBrightness(toolbarWhite, p.borderBrightness, p.systemBrightness)
	text := applyERAMBrightness(toolbarWhite, p.checklist.brightness, p.systemBrightness)
	black := toolbarBlack
	headerBackground := black
	if p.checklist.isOpaque {
		headerBackground = applyERAMBrightness(toolbarGray, p.buttonBrightness, p.systemBrightness)
	}

	// ViewListBase always gives its contents a black body. ShowBorder only
	// controls the body's 1 px border; it does not remove the black list body.
	// Draw it before the header: HeaderRow has ZIndex 0.0001 in CRC specifically
	// so its one-pixel overlap remains above the list body.
	drawSolidRect(cb, layout.Body, black)
	if p.checklist.showBorder {
		drawBorderOnly(cb, layout.Body, border, checklistBorderWidth)
	}

	// HeaderRow: M | centered checklist title | suppression '-'. Adjacent header
	// cells overlap by one pixel in CRC; drawing the shared borders in this
	// order produces the same one-pixel dividers.
	drawSolidRect(cb, layout.Menu, headerBackground)
	drawBorderOnly(cb, layout.Menu, border, checklistBorderWidth)
	drawSolidRect(cb, layout.Title, headerBackground)
	drawBorderOnly(cb, layout.Title, border, checklistBorderWidth)
	drawSolidRect(cb, layout.Suppress, headerBackground)
	drawBorderOnly(cb, layout.Suppress, border, checklistBorderWidth)

	// Selection emphasis is White on Gray with derived text brightness 76 and
	// derived background brightness 40. CRC pads each entry to the longest
	// adapted checklist string, making every selection rectangle the same width.
	for i := range layout.Entries {
		if !p.checklist.selected[i] {
			continue
		}
		entry := layout.EntryText[i]
		fill := applyERAMBrightness(checklistGray, p.checklist.highlightBrightness, p.systemBrightness)
		// Selection circumscription extends 2 px around measured text.
		drawSolidRect(cb, entry, fill)
	}

	// CRC gives borders / selected entries a Paired Target white outline while
	// hovering. Draw it last so adjacent one-pixel borders cannot overwrite it.
	if ctx.Mouse != nil {
		hover := ctx.Mouse.Pos
		emphasis := applyERAMBrightness(toolbarWhite, p.pairedTargetBrightness, p.systemBrightness)
		switch {
		case layout.Menu.Contains(hover):
			drawBorderOnly(cb, layout.Menu, emphasis, 1)
		case layout.Title.Contains(hover):
			drawBorderOnly(cb, layout.Title, emphasis, 1)
		case layout.Suppress.Contains(hover):
			drawBorderOnly(cb, layout.Suppress, emphasis, 1)
		default:
			for i, entry := range layout.EntryText {
				if entry.Contains(hover) {
					drawBorderOnly(cb, entry, emphasis, 1)
					_ = i
					break
				}
			}
		}
	}

	// Text uses the actual extracted ERAM bitmap font. Header controls are
	// always size 2; list entries use ChecklistViewSettings.FontSize (default 2).
	headerTD := renderer.GetTextDrawBuilder()
	headerTD.SetFont(font)
	addChecklistCenteredText(headerTD, font, "M", layout.Menu, 2, text.ToRGBA(), headerBackground.ToRGBA())
	addChecklistCenteredText(headerTD, font, p.checklistTitle(), layout.Title, 2, text.ToRGBA(), headerBackground.ToRGBA())
	addChecklistCenteredText(headerTD, font, "-", layout.Suppress, 2, text.ToRGBA(), headerBackground.ToRGBA())
	cb.Blend()
	headerTD.GenerateCommands(cb, headerTexture)
	renderer.ReturnTextDrawBuilder(headerTD)

	td := renderer.GetTextDrawBuilder()
	td.SetFont(font)
	for i, raw := range layout.Entries {
		entry := raw + strings.Repeat(" ", maxInt(0, layout.LongestChars-len([]rune(raw))))
		background := black.ToRGBA()
		if p.checklist.selected[i] {
			background = applyERAMBrightness(checklistGray, p.checklist.highlightBrightness, p.systemBrightness).ToRGBA()
		}
		td.AddText(entry, redsmath.Vec2{
			X: layout.ContentOrigin.X,
			Y: layout.ContentOrigin.Y + float32(i)*(layout.EntryHeight+checklistEntryGap) + checklistTextYPadding,
		}, renderer.TextStyle{
			Size:       p.checklist.fontSize,
			Color:      text.ToRGBA(),
			Background: background,
		})
	}
	td.GenerateCommands(cb, texture)
	renderer.ReturnTextDrawBuilder(td)

	cb.DisableScissor()
}

func addChecklistCenteredText(td *renderer.TextDrawBuilder, font *renderer.BitmapFont, text string, bounds redsmath.Rect, size int, color, background renderer.RGBA) {
	if td == nil || font == nil || text == "" {
		return
	}
	w, h := font.MeasureText(text, size)
	td.AddText(text, redsmath.Vec2{
		X: bounds.Min.X + (bounds.Width()-float32(w))/2,
		Y: bounds.Min.Y + (bounds.Height()-float32(h))/2,
	}, renderer.TextStyle{Size: size, Color: color, Background: background})
}
