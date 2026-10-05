package stars

import (
	"errors"
	"strconv"
	"strings"
)

func validateStarsPreferenceSetName(name string) error {
	name = strings.ToUpper(strings.TrimSpace(name))
	if len(name) < 1 || len(name) > 7 {
		return ErrSTARSCommandFormat
	}
	for _, r := range name {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ErrSTARSCommandFormat
		}
	}
	if value, err := strconv.Atoi(name); err == nil && value >= 1 && value <= starsPreferenceSetCount {
		return ErrSTARSIllegalPrefset
	}
	return nil
}

func (p *STARSPane) selectPreferenceSetByReference(reference string) error {
	reference = strings.ToUpper(strings.TrimSpace(reference))
	if reference == "" {
		return ErrSTARSCommandFormat
	}
	if number, err := strconv.Atoi(reference); err == nil {
		if number < 1 || number > starsPreferenceSetCount {
			return ErrSTARSCommandFormat
		}
		return p.selectPreferenceSet(number - 1)
	}
	for i, saved := range p.prefSets.Saved {
		if saved != nil && strings.EqualFold(saved.Name, reference) {
			return p.selectPreferenceSet(i)
		}
	}
	return ErrSTARSIllegalPrefset
}

func (p *STARSPane) savePreferenceSetAs(name string) error {
	if p == nil {
		return ErrSTARSCommandFormat
	}
	name = strings.ToUpper(strings.TrimSpace(name))
	if err := validateStarsPreferenceSetName(name); err != nil {
		return err
	}
	for _, saved := range p.prefSets.Saved {
		if saved != nil && strings.EqualFold(saved.Name, name) {
			return ErrSTARSIllegalPrefset
		}
	}

	idx := -1
	for i, saved := range p.prefSets.Saved {
		if saved == nil {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ErrSTARSCapacity
	}

	p.prefSets.Saved[idx] = &starsSavedPreferenceSet{
		Name:        name,
		Preferences: captureStarsPreferenceSnapshot(p.currentPrefs()),
	}
	p.prefSelected = &idx
	p.persistPreferenceSets()
	return nil
}

// towerListIDCommandParser implements the adapted 1-3 alphanumeric identifier
// in TI 6191.409 Rev. 30 sections 4.9.7, 4.9.12, and 4.9.16. REDS does not yet
// receive CRC's explicit tower-list identifier adaptation, so REDS currently
// uses VICE's three list identifiers (1, 2, and 3). Other syntactically valid
// identifiers correctly resolve as a nonexistent adapted list (ILL FNCT).
type towerListIDCommandParser struct{}

func (towerListIDCommandParser) Identifier() string { return "TOWER_LIST_ID" }

func (towerListIDCommandParser) Parse(text string) (any, string, bool, error) {
	if text == "" {
		return nil, text, false, nil
	}
	end := 0
	for end < len(text) && text[end] != ' ' && text[end] != '\t' {
		end++
	}
	if end < 1 || end > 3 {
		return nil, text, true, ErrSTARSCommandFormat
	}
	id := strings.ToUpper(text[:end])
	for _, r := range id {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return nil, text, true, ErrSTARSCommandFormat
		}
	}
	return id, text[end:], true, nil
}

var errListIllegalParameter = errors.New("ILL PARAM")

type towerListLinesCommandParser struct{}

func (towerListLinesCommandParser) Identifier() string { return "TOWER_LIST_LINES" }

func (towerListLinesCommandParser) Parse(text string) (any, string, bool, error) {
	if text == "" {
		return nil, text, true, ErrSTARSCommandFormat
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return nil, text, true, ErrSTARSCommandFormat
		}
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return nil, text, true, ErrSTARSCommandFormat
	}
	if n < 1 || n > 100 {
		return nil, "", true, errListIllegalParameter
	}
	return n, "", true, nil
}

type coastSuspendListLinesCommandParser struct{}

func (coastSuspendListLinesCommandParser) Identifier() string { return "COAST_SUSPEND_LIST_LINES" }

func (coastSuspendListLinesCommandParser) Parse(text string) (any, string, bool, error) {
	return towerListLinesCommandParser{}.Parse(text)
}

func init() {
	// TI 6191.409 Rev. 30, 4.1.3: after selecting PREF SET the operator may
	// identify one of the 32 stored sets by number or by its saved name.
	registerCommand(CommandModePref, "[PREF_REFERENCE]", func(p *STARSPane, args []any) (CommandStatus, error) {
		if err := p.selectPreferenceSetByReference(args[0].(string)); err != nil {
			return CommandStatus{}, err
		}
		return CommandStatus{}, nil
	})

	// TI 6191.409 Rev. 30, 4.1.4: SAVE AS accepts a 1-7 character
	// alphanumeric name. The set is placed in the first available slot.
	registerCommand(CommandModeSavePrefAs, "[PREF_NAME]", func(p *STARSPane, args []any) (CommandStatus, error) {
		if err := p.savePreferenceSetAs(args[0].(string)); err != nil {
			return CommandStatus{}, err
		}
		p.commandMode = CommandModePref
		return CommandStatus{Clear: ClearInput}, nil
	})

	// TI 6191.409 Rev. 30, 4.1.10: MULTI FUNC K restores the current
	// display characteristics to their system defaults.
	registerCommand(CommandModeMultiFunc, "K", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.resetDefaultPreferenceCharacteristics()
		return CommandStatus{}, nil
	})

	// The parser registry is initialized before package init functions run, so
	// these local list parsers can be installed without growing parsecmd.go.
	commandTypeParsers["TOWER_LIST_ID"] = towerListIDCommandParser{}
	commandTypeParsers["TOWER_LIST_LINES"] = towerListLinesCommandParser{}
	commandTypeParsers["COAST_SUSPEND_LIST_LINES"] = coastSuspendListLinesCommandParser{}

	// TI 6191.409 Rev. 30, 4.9.15: <MULTI FUNC>, T, C, <SPACE>,
	// <1..100>, <ENTER> changes the Coast/Suspend-list display capacity and
	// shows the list if it was hidden. Keep the literal space in the command
	// specification; TC5 is not the documented keyboard sequence.
	registerCommand(CommandModeMultiFunc, "TC [COAST_SUSPEND_LIST_LINES]", func(p *STARSPane, args []any) (CommandStatus, error) {
		list := &p.currentPrefs().CoastSuspendList
		list.Lines = args[0].(int)
		list.Visible = true
		return CommandStatus{}, nil
	})

	// TI 6191.409 Rev. 30, 4.9.11: C is the Coast/Suspend aircraft-list ID.
	// TC<ENTER> toggles list visibility. TC followed by a slew/click is
	// intercepted in consumeMouseEvents and implements 4.9.6 instead.
	registerCommand(CommandModeMultiFunc, "TC", func(p *STARSPane, args []any) (CommandStatus, error) {
		list := &p.currentPrefs().CoastSuspendList
		list.Visible = !list.Visible
		return CommandStatus{}, nil
	})

	// 4.9.16 Change size of Tower list. The space is significant in the STARS
	// keyboard modality: P<id><SPACE><1..100><ENTER>. Resizing also shows a
	// hidden list.
	registerCommand(CommandModeMultiFunc, "P[TOWER_LIST_ID] [TOWER_LIST_LINES]", func(p *STARSPane, args []any) (CommandStatus, error) {
		idx, ok := p.towerListIndex(args[0].(string))
		if !ok {
			return CommandStatus{}, ErrSTARSIllegalFunction
		}
		list := &p.currentPrefs().TowerLists[idx]
		list.Lines = args[1].(int)
		list.Visible = true
		return CommandStatus{}, nil
	})

	// 4.9.12 Hide/show Tower list: P<id><ENTER> toggles only the selected
	// list. P<id> followed by a slew/click is intercepted by consumeMouseEvents
	// and performs 4.9.7 Move Tower list instead.
	registerCommand(CommandModeMultiFunc, "P[TOWER_LIST_ID]", func(p *STARSPane, args []any) (CommandStatus, error) {
		idx, ok := p.towerListIndex(args[0].(string))
		if !ok {
			return CommandStatus{}, ErrSTARSIllegalFunction
		}
		list := &p.currentPrefs().TowerLists[idx]
		list.Visible = !list.Visible
		return CommandStatus{}, nil
	})
}
