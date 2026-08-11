package eram

import (
	"time"

	redsmath "github.com/juliusplatzer/reds/math"
)

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
	topLine  int
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
		topLine:        0,
	}
}

// CRC WeatherStationReportViewSettings defaults.
const (
	defaultWXReportLines      = 5
	defaultWXReportFontSize   = 2
	defaultWXReportBrightness = 80
)

type eramWXReportPreferences struct {
	location     eramAnchoredLocation
	lines        int
	fontSize     int
	brightness   int
	showBorder   bool
	showTearoffs bool
	isOpaque     bool
	visible      bool
}

type wxReportStation struct {
	ICAO      string
	DisplayID string
}

type eramWXReportState struct {
	prefs eramWXReportPreferences

	stations    []wxReportStation
	metars      map[string]wxMETAR
	fetching    map[string]bool
	lastAttempt map[string]time.Time
	updates     chan wxMETARUpdate
	topLine     int
}

func defaultWXReportPreferences() eramWXReportPreferences {
	return eramWXReportPreferences{
		// ViewListMenuSettingsBase.Location = TopLeft (20, 110).
		location: eramAnchoredLocation{
			Offset: redsmath.Vec2{X: 20, Y: 110},
			Anchor: eramViewAnchorTopLeft,
		},
		lines:        defaultWXReportLines,
		fontSize:     defaultWXReportFontSize,
		brightness:   defaultWXReportBrightness,
		showBorder:   true,
		showTearoffs: true,
	}
}

func (p *ERAMPane) initializeWXReportState() {
	if p == nil {
		return
	}
	p.wxReport = eramWXReportState{
		prefs:       defaultWXReportPreferences(),
		metars:      make(map[string]wxMETAR),
		fetching:    make(map[string]bool),
		lastAttempt: make(map[string]time.Time),
		updates:     make(chan wxMETARUpdate, 64),
	}
}
