package eram

import redsmath "github.com/juliusplatzer/reds/math"

// CRC ChecklistViewSettings defaults. Keep user-adjustable checklist settings
// separate from checklist rendering/runtime state, following VICE's prefs.go
// split for ERAM views.
const (
	defaultChecklistLines               = 21
	defaultChecklistFontSize            = 2
	defaultChecklistHighlightBrightness = 40
	defaultChecklistBrightness          = 76
)

type eramChecklistType uint8

const (
	eramChecklistNone eramChecklistType = iota
	eramChecklistPositionRelief
	eramChecklistEmergency
)

type eramChecklistPreferences struct {
	location            eramAnchoredLocation
	lines               int
	fontSize            int
	highlightBrightness int
	brightness          int
	showBorder          bool
	isOpaque            bool
}

type eramChecklistState struct {
	active eramChecklistType
	prefs  eramChecklistPreferences

	// Checklist bodies are facility adaptation data, not user preferences.
	positionRelief []string
	emergency      []string

	// Entry emphasis is transient view state and resets when the active list
	// changes, matching CRC's BuildChecklist behavior.
	selected map[int]bool
}

func defaultChecklistPreferences() eramChecklistPreferences {
	return eramChecklistPreferences{
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
	}
}

func (p *ERAMPane) initializeChecklistState(facility Facility) {
	if p == nil {
		return
	}
	p.checklist = eramChecklistState{
		prefs:          defaultChecklistPreferences(),
		positionRelief: append([]string(nil), facility.PositionReliefChecklist...),
		emergency:      append([]string(nil), facility.EmergencyChecklist...),
		selected:       make(map[int]bool),
	}
}
