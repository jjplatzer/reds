package asdex

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/juliusplatzer/reds/panes"
)

// commands that are executed on stand-alone slews (click on target) and right slews (right click on target)

func registerSlewCommands() {
	registerCommand(
		CommandModeNone,
		"[ACID][SLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, acid AircraftID, target *Target) CommandStatus {
			return ap.cmdManualTagUnknownTarget(ctx, acid, target)
		},
	)
	registerCommand(
		CommandModeNone,
		"[SLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, target *Target) CommandStatus {
			return ap.cmdBareAircraftSlew(ctx, target)
		},
	)
	registerCommand(
		CommandModeNone,
		"[R SLEW]",
		func(ap *ASDEXPane, ctx *panes.Context, target *Target) CommandStatus {
			return ap.cmdRSlew(ctx, target)
		},
	)
}

func (ap *ASDEXPane) cmdBareAircraftSlew(
	_ *panes.Context,
	target *Target,
) CommandStatus {
	if ap == nil || target == nil {
		return CommandStatus{}
	}

	if target.Suspended {
		ap.targets.UnsuspendTarget(target.ID)
		return CommandStatus{
			Output:    "",
			HasOutput: true,
		}
	}

	if target.Coasting || target.Dropped {
		return CommandStatus{}
	}

	if !targetCanHaveDataBlock(target) {
		return CommandStatus{}
	}

	target.ShowDB = !target.ShowDB
	return CommandStatus{}
}

func (ap *ASDEXPane) cmdRSlew(
	_ *panes.Context,
	target *Target,
) CommandStatus {
	if ap == nil || target == nil {
		return CommandStatus{}
	}
	if !targetCanEditDBFields(target) {
		return CommandStatus{}
	}

	edit := NewDatablockEditCommandFromTarget(target)
	ap.commandMode = CommandModeEditDatablockFields
	ap.commandEntry.Clear()
	ap.editingTargetID = target.ID
	ap.datablockEdit = &edit
	ap.initControlEntry = nil
	ap.termControlEntry = nil
	ap.multiFunction = nil
	ap.previewReposition = nil
	ap.coastListReposition = nil
	ap.previewArea.SetSystemResponse("")
	ap.clearHighlightedTarget()

	return CommandStatus{Clear: ClearNone}
}

func (ap *ASDEXPane) cmdManualTagUnknownTarget(
	_ *panes.Context,
	acid AircraftID,
	target *Target,
) CommandStatus {
	if ap == nil {
		return CommandStatus{Clear: ClearAll}
	}

	aircraftID := strings.ToUpper(strings.TrimSpace(string(acid)))
	if aircraftID == "" {
		return commandOutputClearAll("INVALID ENTRY")
	}
	if target == nil {
		return commandOutputClearAll("NO SLEW")
	}
	if !targetIsManualTagCandidate(target) {
		return commandOutputClearAll("INVALID ENTRY")
	}
	if !ap.targets.ManualTagUnknownTarget(target.ID, aircraftID) {
		return commandOutputClearAll("INVALID ENTRY")
	}

	return CommandStatus{
		Clear:     ClearAll,
		Output:    "",
		HasOutput: true,
	}
}

func targetCanEditDBFields(target *Target) bool {
	if target == nil {
		return false
	}
	if target.Suspended || target.Coasting || target.Dropped {
		return false
	}
	return targetCanHaveDataBlock(target)
}

type EditedDBFields struct {
	Callsign    string
	Beacon      string
	CWT         string
	TargetType  string
	Fix         string
	Scratchpad1 string
	Scratchpad2 string
}

type DatablockEditCommand struct {
	fields []dbEditField
	active int
}

type dbEditField struct {
	label            string
	value            string
	cursor           int
	columnOffset     int
	resetOnFirstType bool
}

func NewDatablockEditCommandFromTarget(target *Target) DatablockEditCommand {
	if target == nil {
		return DatablockEditCommand{}
	}

	targetType := ""
	if target.TargetType != nil {
		targetType = *target.TargetType
	}

	command := DatablockEditCommand{
		fields: []dbEditField{
			{label: "A/C:", value: normalizedDBEdit(target.Callsign), columnOffset: 5},
			{label: "BCN:", value: normalizedDBEdit(target.Beacon), columnOffset: 5},
			{label: "CAT:", value: normalizedDBEdit(target.CWT), columnOffset: 5},
			{label: "TYP:", value: normalizedDBEdit(targetType), columnOffset: 5},
			{label: "FIX:", value: normalizedDBEdit(target.Fix), columnOffset: 5},
			{label: "SP1:", value: normalizedDBEdit(target.Scratchpad1), columnOffset: 5},
			{label: "SP2:", value: normalizedDBEdit(target.Scratchpad2), columnOffset: 5},
		},
	}
	command.activateField(0)
	return command
}

func normalizedDBEdit(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func (field dbEditField) displayLine() string {
	return field.label + " " + field.value
}

func (command *DatablockEditCommand) DisplayLines() []string {
	if command == nil {
		return nil
	}

	lines := make([]string, 0, len(command.fields))
	for _, field := range command.fields {
		lines = append(lines, field.displayLine())
	}
	return lines
}

func (command *DatablockEditCommand) CursorLine() int {
	if command == nil {
		return 0
	}
	return command.active + 1
}

func (command *DatablockEditCommand) CursorColumn() int {
	if command == nil || len(command.fields) == 0 {
		return 0
	}

	field := command.fields[command.active]
	return field.columnOffset + field.cursor
}

func (command *DatablockEditCommand) activateField(index int) {
	if command == nil || len(command.fields) == 0 {
		return
	}
	if index < 0 {
		index = 0
	}
	if index >= len(command.fields) {
		index = len(command.fields) - 1
	}

	command.active = index
	field := &command.fields[command.active]
	field.cursor = len([]rune(field.value))
	field.resetOnFirstType = true
}

func (command *DatablockEditCommand) Insert(r rune) {
	if command == nil || len(command.fields) == 0 || !isAllowedDBEditInputRune(r) {
		return
	}

	field := &command.fields[command.active]
	if field.resetOnFirstType {
		field.value = ""
		field.cursor = 0
		field.resetOnFirstType = false
	}

	r = unicode.ToUpper(r)
	runes := []rune(field.value)
	field.cursor = clampInt(field.cursor, 0, len(runes))
	runes = append(runes[:field.cursor], append([]rune{r}, runes[field.cursor:]...)...)
	field.value = string(runes)
	field.cursor++
}

func (command *DatablockEditCommand) Backspace() {
	field := command.activeField()
	if field == nil {
		return
	}
	field.resetOnFirstType = false

	runes := []rune(field.value)
	if field.cursor <= 0 || field.cursor > len(runes) {
		return
	}

	runes = append(runes[:field.cursor-1], runes[field.cursor:]...)
	field.value = string(runes)
	field.cursor--
}

func (command *DatablockEditCommand) DeleteForward() {
	field := command.activeField()
	if field == nil {
		return
	}
	field.resetOnFirstType = false

	runes := []rune(field.value)
	if field.cursor < 0 || field.cursor >= len(runes) {
		return
	}

	runes = append(runes[:field.cursor], runes[field.cursor+1:]...)
	field.value = string(runes)
}

func (command *DatablockEditCommand) MoveLeft() {
	field := command.activeField()
	if field == nil {
		return
	}
	field.resetOnFirstType = false
	if field.cursor > 0 {
		field.cursor--
	}
}

func (command *DatablockEditCommand) MoveRight() {
	field := command.activeField()
	if field == nil {
		return
	}
	field.resetOnFirstType = false
	if field.cursor < len([]rune(field.value)) {
		field.cursor++
	}
}

func (command *DatablockEditCommand) MoveUp() {
	if command == nil {
		return
	}
	command.activateField(command.active - 1)
}

func (command *DatablockEditCommand) MoveDown() {
	if command == nil {
		return
	}
	command.activateField(command.active + 1)
}

func (command *DatablockEditCommand) Enter() bool {
	if command == nil || len(command.fields) == 0 {
		return false
	}
	if command.active >= len(command.fields)-1 {
		return true
	}
	command.activateField(command.active + 1)
	return false
}

func (command *DatablockEditCommand) Values() EditedDBFields {
	if command == nil || len(command.fields) < 7 {
		return EditedDBFields{}
	}
	return EditedDBFields{
		Callsign:    normalizedDBEdit(command.fields[0].value),
		Beacon:      normalizedDBEdit(command.fields[1].value),
		CWT:         normalizedDBEdit(command.fields[2].value),
		TargetType:  normalizedDBEdit(command.fields[3].value),
		Fix:         normalizedDBEdit(command.fields[4].value),
		Scratchpad1: normalizedDBEdit(command.fields[5].value),
		Scratchpad2: normalizedDBEdit(command.fields[6].value),
	}
}

func (command *DatablockEditCommand) ValidateForTarget(target *Target) (string, bool) {
	values := command.Values()

	if !dbEditCallsignRe.MatchString(values.Callsign) ||
		!dbEditBeaconRe.MatchString(values.Beacon) ||
		!dbEditCWTRe.MatchString(values.CWT) ||
		!dbEditTargetTypeRe.MatchString(values.TargetType) ||
		!dbEditFixRe.MatchString(values.Fix) ||
		!dbEditScratchpadRe.MatchString(values.Scratchpad1) ||
		!dbEditScratchpadRe.MatchString(values.Scratchpad2) {
		return "INVALID ENTRY", false
	}

	return "", true
}

func (command *DatablockEditCommand) activeField() *dbEditField {
	if command == nil || len(command.fields) == 0 {
		return nil
	}
	if command.active < 0 {
		command.active = 0
	}
	if command.active >= len(command.fields) {
		command.active = len(command.fields) - 1
	}
	return &command.fields[command.active]
}

func isAllowedDBEditInputRune(r rune) bool {
	return unicode.IsLetter(r) ||
		unicode.IsDigit(r) ||
		r == ' ' ||
		r == '.' ||
		r == '/'
}

var (
	dbEditCallsignRe   = regexp.MustCompile(`^[A-Z\d]{0,8}$`)
	dbEditBeaconRe     = regexp.MustCompile(`^[0-7]{4}$|^$`)
	dbEditCWTRe        = regexp.MustCompile(`^[A-Z]$|^$`)
	dbEditTargetTypeRe = regexp.MustCompile(`^[A-Z\d]{0,4}$`)
	dbEditFixRe        = regexp.MustCompile(`^[A-Z\d]{3}$|^$`)
	dbEditScratchpadRe = regexp.MustCompile(`^[A-Z\d]{0,7}$`)
)

func (ap *ASDEXPane) submitDatablockEdit() {
	if ap == nil || ap.datablockEdit == nil {
		return
	}

	target := ap.targets.TargetByID(ap.editingTargetID)
	if target == nil {
		ap.cancelDatablockEdit()
		return
	}

	if message, ok := ap.datablockEdit.ValidateForTarget(target); !ok {
		ap.previewArea.SetSystemResponse(message)
		return
	}

	values := ap.datablockEdit.Values()
	ap.targets.SetDatablockOverride(ap.editingTargetID, DatablockFieldOverride{
		Callsign:    values.Callsign,
		Beacon:      values.Beacon,
		CWT:         values.CWT,
		TargetType:  values.TargetType,
		Fix:         values.Fix,
		Scratchpad1: values.Scratchpad1,
		Scratchpad2: values.Scratchpad2,
	})

	ap.cancelDatablockEdit()
}
