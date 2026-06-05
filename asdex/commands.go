package asdex

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unicode"

	redsmath "github.com/juliusplatzer/reds/math"
	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
	"github.com/juliusplatzer/reds/radar"
)

type CommandMode int

const (
	CommandModeNone CommandMode = iota
	CommandModeEditDatablockFields
	CommandModeTrackSuspend
	CommandModeInitiateControl
	CommandModeTerminateControl
)

type CommandClear int

const (
	ClearAll CommandClear = iota
	ClearInput
	ClearNone
)

type CommandStatus struct {
	Clear CommandClear

	Output    string
	HasOutput bool
}

const maxManualTagAircraftIDLength = 7

type AircraftID string

type LeaderDirectionInput struct {
	Digit     rune
	Direction LeaderDirection
}

type CommandTextEntryKind int

const (
	CommandTextEntryNone CommandTextEntryKind = iota
	CommandTextEntryACID
	CommandTextEntryLeaderDirection
)

type CommandTextEntry struct {
	kind   CommandTextEntryKind
	value  string
	cursor int
}

func (entry *CommandTextEntry) Empty() bool {
	return entry == nil ||
		entry.kind == CommandTextEntryNone ||
		strings.TrimSpace(entry.value) == ""
}

func (entry *CommandTextEntry) Kind() CommandTextEntryKind {
	if entry == nil {
		return CommandTextEntryNone
	}
	return entry.kind
}

func (entry *CommandTextEntry) Value() string {
	if entry == nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(entry.value))
}

func (entry *CommandTextEntry) DisplayLines() []string {
	if entry == nil || entry.kind == CommandTextEntryNone {
		return nil
	}

	switch entry.kind {
	case CommandTextEntryACID:
		return []string{"ACID", entry.value}
	case CommandTextEntryLeaderDirection:
		return []string{"LDR DIR", entry.value}
	default:
		return nil
	}
}

func (entry *CommandTextEntry) CursorLine() int {
	if entry == nil || entry.kind == CommandTextEntryNone {
		return 0
	}
	return 2
}

func (entry *CommandTextEntry) CursorColumn() int {
	if entry == nil {
		return 0
	}
	return entry.cursor
}

func (entry *CommandTextEntry) Insert(r rune) {
	if entry == nil {
		return
	}

	r = unicode.ToUpper(r)

	if entry.kind == CommandTextEntryNone {
		switch {
		case unicode.IsLetter(r):
			entry.StartACID(r)
		case unicode.IsDigit(r):
			entry.StartLeaderDirection(r)
		}
		return
	}

	switch entry.kind {
	case CommandTextEntryACID:
		entry.insertACIDRune(r)
	case CommandTextEntryLeaderDirection:
		entry.insertLeaderDirectionRune(r)
	}
}

func (entry *CommandTextEntry) StartACID(r rune) {
	if entry == nil {
		return
	}

	entry.Clear()
	r = unicode.ToUpper(r)
	if !unicode.IsLetter(r) {
		return
	}

	entry.kind = CommandTextEntryACID
	entry.value = string(r)
	entry.cursor = 1
}

func (entry *CommandTextEntry) insertACIDRune(r rune) {
	if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
		return
	}

	value := []rune(entry.value)
	if len(value) >= maxManualTagAircraftIDLength {
		return
	}
	entry.cursor = clampInt(entry.cursor, 0, len(value))

	value = append(value[:entry.cursor], append([]rune{r}, value[entry.cursor:]...)...)
	entry.value = string(value)
	entry.cursor++
}

func (entry *CommandTextEntry) StartLeaderDirection(r rune) {
	if entry == nil {
		return
	}

	entry.Clear()
	r = unicode.ToUpper(r)
	if !unicode.IsDigit(r) {
		return
	}

	entry.kind = CommandTextEntryLeaderDirection
	entry.value = string(r)
	entry.cursor = 1
}

func (entry *CommandTextEntry) insertLeaderDirectionRune(r rune) {
	if !unicode.IsDigit(r) {
		return
	}

	value := []rune(entry.value)
	if len(value) >= 1 {
		return
	}
	entry.cursor = clampInt(entry.cursor, 0, len(value))

	value = append(value[:entry.cursor], append([]rune{r}, value[entry.cursor:]...)...)
	entry.value = string(value)
	entry.cursor++
}

func (entry *CommandTextEntry) Backspace() {
	if entry == nil || entry.cursor <= 0 {
		return
	}

	value := []rune(entry.value)
	if entry.cursor > len(value) {
		entry.cursor = len(value)
	}
	if entry.cursor <= 0 {
		return
	}

	entry.cursor--
	value = append(value[:entry.cursor], value[entry.cursor+1:]...)
	if len(value) == 0 {
		entry.Clear()
		return
	}
	entry.value = string(value)
}

func (entry *CommandTextEntry) DeleteForward() {
	if entry == nil {
		return
	}

	value := []rune(entry.value)
	entry.cursor = clampInt(entry.cursor, 0, len(value))
	if entry.cursor >= len(value) {
		return
	}

	value = append(value[:entry.cursor], value[entry.cursor+1:]...)
	if len(value) == 0 {
		entry.Clear()
		return
	}
	entry.value = string(value)
}

func (entry *CommandTextEntry) MoveLeft() {
	if entry == nil {
		return
	}
	if entry.cursor > 0 {
		entry.cursor--
	}
}

func (entry *CommandTextEntry) MoveRight() {
	if entry == nil {
		return
	}
	value := []rune(entry.value)
	if entry.cursor < len(value) {
		entry.cursor++
	}
}

func (entry *CommandTextEntry) Clear() {
	if entry == nil {
		return
	}
	entry.kind = CommandTextEntryNone
	entry.value = ""
	entry.cursor = 0
}

func commandOutput(text string) CommandStatus {
	return CommandStatus{
		Output:    text,
		HasOutput: true,
	}
}

type CommandClickKind int

const (
	CommandClickNone CommandClickKind = iota
	CommandClickLeft
	CommandClickRight
)

type CommandInput struct {
	text string

	clickedTarget *Target
	clickKind     CommandClickKind

	mousePosition redsmath.Vec2
	transforms    radar.ScopeTransformations
}

type matchResult struct {
	values    []any
	remaining string
	matched   bool
	priority  int
}

type matcher interface {
	match(ap *ASDEXPane, ctx *panes.Context, input *CommandInput, text string) (*matchResult, error)
	validate() error
	goType() reflect.Type
	consumesClick() bool
}

type literalMatcher struct {
	text string
}

func (m literalMatcher) match(
	_ *ASDEXPane,
	_ *panes.Context,
	_ *CommandInput,
	text string,
) (*matchResult, error) {
	if !strings.HasPrefix(text, m.text) {
		return nil, nil
	}
	return &matchResult{
		remaining: text[len(m.text):],
		matched:   true,
		priority:  -len(m.text),
	}, nil
}

func (m literalMatcher) validate() error {
	if m.text == "" {
		return fmt.Errorf("empty literal matcher")
	}
	return nil
}

func (literalMatcher) goType() reflect.Type { return nil }
func (literalMatcher) consumesClick() bool  { return false }

type slewMatcher struct{}

func (slewMatcher) match(
	_ *ASDEXPane,
	_ *panes.Context,
	input *CommandInput,
	text string,
) (*matchResult, error) {
	if input == nil || input.clickKind != CommandClickLeft || input.clickedTarget == nil {
		return nil, nil
	}
	return &matchResult{
		values:    []any{input.clickedTarget},
		remaining: text,
		matched:   true,
	}, nil
}

func (slewMatcher) validate() error      { return nil }
func (slewMatcher) goType() reflect.Type { return reflect.TypeFor[*Target]() }
func (slewMatcher) consumesClick() bool  { return true }

type rightSlewMatcher struct{}

func (rightSlewMatcher) match(
	_ *ASDEXPane,
	_ *panes.Context,
	input *CommandInput,
	text string,
) (*matchResult, error) {
	if input == nil || input.clickKind != CommandClickRight || input.clickedTarget == nil {
		return nil, nil
	}
	return &matchResult{
		values:    []any{input.clickedTarget},
		remaining: text,
		matched:   true,
	}, nil
}

func (rightSlewMatcher) validate() error      { return nil }
func (rightSlewMatcher) goType() reflect.Type { return reflect.TypeFor[*Target]() }
func (rightSlewMatcher) consumesClick() bool  { return true }

type ldrDirMatcher struct{}

func (ldrDirMatcher) match(
	_ *ASDEXPane,
	_ *panes.Context,
	_ *CommandInput,
	text string,
) (*matchResult, error) {
	input, ok := parseLeaderDirectionInput(text)
	if !ok {
		return nil, nil
	}

	return &matchResult{
		values:    []any{input},
		remaining: "",
		matched:   true,
	}, nil
}

func (ldrDirMatcher) validate() error      { return nil }
func (ldrDirMatcher) goType() reflect.Type { return reflect.TypeFor[LeaderDirectionInput]() }
func (ldrDirMatcher) consumesClick() bool  { return false }

func parseLeaderDirectionInput(text string) (LeaderDirectionInput, bool) {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) != 1 {
		return LeaderDirectionInput{}, false
	}

	switch runes[0] {
	case '1':
		return LeaderDirectionInput{Digit: '1', Direction: LeaderSW}, true
	case '2':
		return LeaderDirectionInput{Digit: '2', Direction: LeaderS}, true
	case '3':
		return LeaderDirectionInput{Digit: '3', Direction: LeaderSE}, true
	case '4':
		return LeaderDirectionInput{Digit: '4', Direction: LeaderW}, true
	case '6':
		return LeaderDirectionInput{Digit: '6', Direction: LeaderE}, true
	case '7':
		return LeaderDirectionInput{Digit: '7', Direction: LeaderNW}, true
	case '8':
		return LeaderDirectionInput{Digit: '8', Direction: LeaderN}, true
	case '9':
		return LeaderDirectionInput{Digit: '9', Direction: LeaderNE}, true
	default:
		return LeaderDirectionInput{}, false
	}
}

type acidMatcher struct{}

func (acidMatcher) match(
	_ *ASDEXPane,
	_ *panes.Context,
	_ *CommandInput,
	text string,
) (*matchResult, error) {
	text = strings.ToUpper(strings.TrimSpace(text))
	if text == "" {
		return nil, nil
	}

	runes := []rune(text)
	if len(runes) > maxManualTagAircraftIDLength {
		return nil, nil
	}
	if !unicode.IsLetter(runes[0]) {
		return nil, nil
	}
	for _, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return nil, nil
		}
	}

	return &matchResult{
		values:    []any{AircraftID(text)},
		remaining: "",
		matched:   true,
	}, nil
}

func (acidMatcher) validate() error      { return nil }
func (acidMatcher) goType() reflect.Type { return reflect.TypeFor[AircraftID]() }
func (acidMatcher) consumesClick() bool  { return false }

type handlerArgumentKind int

const (
	handlerArgumentPane handlerArgumentKind = iota
	handlerArgumentContext
	handlerArgumentMatcher
)

type handlerArgument struct {
	kind         handlerArgumentKind
	matcherIndex int
}

type userCommand struct {
	cmd           string
	handlerFunc   reflect.Value
	matchers      []matcher
	arguments     []handlerArgument
	consumesClick bool
}

var (
	initCommandsOnce sync.Once

	userCommands       = make(map[CommandMode][]userCommand)
	registeredCommands = make(map[CommandMode]map[string]bool)
)

func InitCommands() {
	initCommandsOnce.Do(func() {
		registerOpsCommands()
		registerSetupCommands()
		registerSlewCommands()
	})
}

func registerCommand(mode CommandMode, spec string, handler any) {
	if registeredCommands[mode] == nil {
		registeredCommands[mode] = make(map[string]bool)
	}

	for _, alternative := range splitCommands(spec) {
		if registeredCommands[mode][alternative] {
			panic(fmt.Sprintf("duplicate command registration in mode %v: %s", mode, alternative))
		}

		command, err := makeUserCommand(alternative, handler)
		if err != nil {
			panic(fmt.Sprintf("register command %q: %v", alternative, err))
		}
		userCommands[mode] = append(userCommands[mode], command)
		registeredCommands[mode][alternative] = true
	}
}

func splitCommands(spec string) []string {
	parts := strings.Split(spec, "|")
	commands := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToUpper(strings.TrimSpace(part))
		if part == "" {
			panic(fmt.Sprintf("empty command alternative in %q", spec))
		}
		commands = append(commands, part)
	}
	return commands
}

func makeUserCommand(spec string, handler any) (userCommand, error) {
	matchers, err := makeMatchers(spec)
	if err != nil {
		return userCommand{}, err
	}

	arguments, err := bindHandlerArguments(handler, matchers)
	if err != nil {
		return userCommand{}, err
	}

	command := userCommand{
		cmd:         spec,
		handlerFunc: reflect.ValueOf(handler),
		matchers:    matchers,
		arguments:   arguments,
	}
	for _, matcher := range matchers {
		if matcher.consumesClick() {
			command.consumesClick = true
		}
	}
	return command, nil
}

func makeMatchers(spec string) ([]matcher, error) {
	var matchers []matcher
	remaining := spec

	for len(remaining) > 0 {
		if remaining[0] == '[' {
			end := strings.IndexByte(remaining, ']')
			if end == -1 {
				return nil, fmt.Errorf("unclosed [ in spec %q", spec)
			}

			switch name := remaining[1:end]; name {
			case "ACID":
				matchers = append(matchers, acidMatcher{})
			case "LDR DIR":
				matchers = append(matchers, ldrDirMatcher{})
			case "SLEW":
				matchers = append(matchers, slewMatcher{})
			case "R SLEW":
				matchers = append(matchers, rightSlewMatcher{})
			default:
				matchers = append(matchers, literalMatcher{text: "[" + name + "]"})
			}
			remaining = remaining[end+1:]
			continue
		}

		end := strings.IndexByte(remaining, '[')
		if end == -1 {
			end = len(remaining)
		}
		matchers = append(matchers, literalMatcher{text: remaining[:end]})
		remaining = remaining[end:]
	}

	for index, matcher := range matchers {
		if err := matcher.validate(); err != nil {
			return nil, fmt.Errorf("invalid matcher in %q: %w", spec, err)
		}
		if index < len(matchers)-1 && matcher.consumesClick() {
			return nil, fmt.Errorf("click-consuming matcher must be last in %q", spec)
		}
	}
	return matchers, nil
}

var (
	asdexPaneType      = reflect.TypeFor[*ASDEXPane]()
	panesContextType   = reflect.TypeFor[*panes.Context]()
	commandStatusType  = reflect.TypeFor[CommandStatus]()
	errorInterfaceType = reflect.TypeFor[error]()
)

func bindHandlerArguments(handler any, matchers []matcher) ([]handlerArgument, error) {
	handlerType := reflect.TypeOf(handler)
	if handlerType == nil || handlerType.Kind() != reflect.Func {
		return nil, fmt.Errorf("handler must be a function")
	}
	if err := validateHandlerReturns(handlerType); err != nil {
		return nil, err
	}

	matcherBound := make([]bool, len(matchers))
	var paneBound, contextBound bool
	arguments := make([]handlerArgument, 0, handlerType.NumIn())

	for index := 0; index < handlerType.NumIn(); index++ {
		argumentType := handlerType.In(index)
		switch argumentType {
		case asdexPaneType:
			if paneBound {
				return nil, fmt.Errorf("handler has duplicate *ASDEXPane argument")
			}
			paneBound = true
			arguments = append(arguments, handlerArgument{kind: handlerArgumentPane})
		case panesContextType:
			if contextBound {
				return nil, fmt.Errorf("handler has duplicate *panes.Context argument")
			}
			contextBound = true
			arguments = append(arguments, handlerArgument{kind: handlerArgumentContext})
		default:
			matcherIndex := firstUnboundMatcherOfType(matchers, matcherBound, argumentType)
			if matcherIndex == -1 {
				return nil, fmt.Errorf("unsupported handler argument %s", argumentType)
			}
			matcherBound[matcherIndex] = true
			arguments = append(arguments, handlerArgument{
				kind:         handlerArgumentMatcher,
				matcherIndex: matcherIndex,
			})
		}
	}

	for index, matcher := range matchers {
		if matcher.goType() != nil && !matcherBound[index] {
			return nil, fmt.Errorf("handler does not accept matcher argument %s", matcher.goType())
		}
	}
	return arguments, nil
}

func firstUnboundMatcherOfType(matchers []matcher, bound []bool, target reflect.Type) int {
	for index, matcher := range matchers {
		if !bound[index] && matcher.goType() == target {
			return index
		}
	}
	return -1
}

func validateHandlerReturns(handlerType reflect.Type) error {
	switch handlerType.NumOut() {
	case 0:
		return nil
	case 1:
		if result := handlerType.Out(0); result == commandStatusType || result == errorInterfaceType {
			return nil
		}
	case 2:
		if handlerType.Out(0) == commandStatusType && handlerType.Out(1) == errorInterfaceType {
			return nil
		}
	}
	return fmt.Errorf("unsupported handler return signature")
}

func (command userCommand) match(
	ap *ASDEXPane,
	ctx *panes.Context,
	input *CommandInput,
) ([]any, bool, error) {
	if input == nil {
		return nil, false, nil
	}
	if (input.clickKind != CommandClickNone) != command.consumesClick {
		return nil, false, nil
	}

	text := input.text
	var values []any
	for _, matcher := range command.matchers {
		result, err := matcher.match(ap, ctx, input, text)
		if err != nil {
			return nil, false, err
		}
		if result == nil || !result.matched {
			return nil, false, nil
		}
		values = append(values, result.values...)
		text = result.remaining
	}
	return values, text == "", nil
}

func (command userCommand) call(
	ap *ASDEXPane,
	ctx *panes.Context,
	values []any,
) (CommandStatus, error) {
	matcherValues := make(map[int]any)
	valueIndex := 0
	for matcherIndex, matcher := range command.matchers {
		if matcher.goType() == nil {
			continue
		}
		if valueIndex >= len(values) {
			return CommandStatus{}, fmt.Errorf("command %q did not produce matcher arguments", command.cmd)
		}
		matcherValues[matcherIndex] = values[valueIndex]
		valueIndex++
	}

	args := make([]reflect.Value, 0, len(command.arguments))
	for _, argument := range command.arguments {
		switch argument.kind {
		case handlerArgumentPane:
			args = append(args, reflect.ValueOf(ap))
		case handlerArgumentContext:
			args = append(args, reflect.ValueOf(ctx))
		case handlerArgumentMatcher:
			args = append(args, reflect.ValueOf(matcherValues[argument.matcherIndex]))
		}
	}

	results := command.handlerFunc.Call(args)
	switch len(results) {
	case 0:
		return CommandStatus{}, nil
	case 1:
		if results[0].Type() == commandStatusType {
			return results[0].Interface().(CommandStatus), nil
		}
		return CommandStatus{}, reflectedError(results[0])
	case 2:
		return results[0].Interface().(CommandStatus), reflectedError(results[1])
	default:
		panic("validated command handler returned an unexpected result count")
	}
}

func reflectedError(value reflect.Value) error {
	if value.IsNil() {
		return nil
	}
	return value.Interface().(error)
}

func (ap *ASDEXPane) tryExecuteUserCommand(
	ctx *panes.Context,
	cmd string,
	clickedTarget *Target,
	clickKind CommandClickKind,
	mousePosition redsmath.Vec2,
	transforms radar.ScopeTransformations,
) (CommandStatus, error, bool) {
	if ap == nil {
		return CommandStatus{}, nil, false
	}

	input := &CommandInput{
		text:          strings.ToUpper(cmd),
		clickedTarget: clickedTarget,
		clickKind:     clickKind,
		mousePosition: mousePosition,
		transforms:    transforms,
	}
	return ap.dispatchCommand(ctx, userCommands[ap.commandMode], input)
}

func (ap *ASDEXPane) dispatchCommand(
	ctx *panes.Context,
	commands []userCommand,
	input *CommandInput,
) (CommandStatus, error, bool) {
	for _, command := range commands {
		values, matched, err := command.match(ap, ctx, input)
		if err != nil {
			return CommandStatus{}, err, true
		}
		if !matched {
			continue
		}

		status, err := command.call(ap, ctx, values)
		return status, err, true
	}
	return CommandStatus{}, nil, false
}

func (ap *ASDEXPane) applyCommandStatus(status CommandStatus) {
	if ap == nil {
		return
	}
	if status.HasOutput {
		ap.previewArea.SetSystemResponse(status.Output)
	}

	switch status.Clear {
	case ClearAll:
		ap.commandMode = CommandModeNone
		ap.initControlEntry = nil
		ap.termControlEntry = nil
		ap.commandEntry.Clear()
	case ClearInput:
		ap.commandEntry.Clear()
	case ClearNone:
	}
}

func (ap *ASDEXPane) consumeOpsHotkeys(
	ctx *panes.Context,
	transforms radar.ScopeTransformations,
) bool {
	if ap == nil || ctx == nil || ctx.Keyboard == nil || ap.datablockEdit != nil {
		return false
	}
	if ap.commandMode != CommandModeNone {
		return false
	}

	command := ""
	switch {
	case ctx.Keyboard.WasPressed(platform.KeyF3):
		command = "[INIT CNTL]"
	case ctx.Keyboard.WasPressed(platform.KeyF4):
		command = "[TRK SUSP]"
	case ctx.Keyboard.WasPressed(platform.KeyF5):
		command = "[TERM CNTL]"
	default:
		return false
	}

	status, err, handled := ap.tryExecuteUserCommand(
		ctx,
		command,
		nil,
		CommandClickNone,
		redsmath.Vec2{},
		transforms,
	)
	if err != nil {
		ap.previewArea.SetSystemResponse(err.Error())
		return true
	}
	if handled {
		ap.applyCommandStatus(status)
		return true
	}
	return false
}

func (ap *ASDEXPane) consumeCommandClicks(
	ctx *panes.Context,
	transforms radar.ScopeTransformations,
) bool {
	if ap == nil || ctx == nil || ctx.Mouse == nil {
		return false
	}
	if ap.datablockEdit != nil {
		return false
	}

	mouse := ctx.Mouse
	rightReleased := mouse.WasReleased(platform.MouseButtonRight)
	if rightReleased {
		defer ap.clearRightClickGesture()
	}

	clickKind := CommandClickNone
	if mouse.WasReleased(platform.MouseButtonLeft) {
		clickKind = CommandClickLeft
	} else if rightReleased && ap.rightClickCandidate && !ap.rightClickDragged {
		clickKind = CommandClickRight
	}
	if clickKind == CommandClickNone {
		return false
	}

	paneLocal := redsmath.RectFromSize(ctx.PaneRect.Width(), ctx.PaneRect.Height())
	if !paneLocal.Contains(mouse.Pos) {
		return false
	}

	target := ap.highlightedTarget()
	if target == nil {
		if clickKind == CommandClickLeft {
			if ap.commandMode == CommandModeNone && !ap.commandEntry.Empty() {
				ap.commandEntry.Clear()
				ap.applyCommandStatus(commandOutputClearAll("NO SLEW"))
				return true
			}

			switch ap.commandMode {
			case CommandModeTrackSuspend:
				ap.applyCommandStatus(commandOutputClearAll("NO SLEW"))
				return true
			case CommandModeInitiateControl:
				ap.initControlEntry = nil
				ap.applyCommandStatus(commandOutputClearAll("NO SLEW"))
				return true
			case CommandModeTerminateControl:
				ap.termControlEntry = nil
				ap.applyCommandStatus(commandOutputClearAll("NO SLEW"))
				return true
			}
		}
		return false
	}

	cmdText := ""
	if ap.commandMode == CommandModeNone && clickKind == CommandClickLeft {
		switch ap.commandEntry.Kind() {
		case CommandTextEntryACID, CommandTextEntryLeaderDirection:
			cmdText = ap.commandEntry.Value()
		}
	}

	status, err, handled := ap.tryExecuteUserCommand(
		ctx,
		cmdText,
		target,
		clickKind,
		mouse.Pos,
		transforms,
	)
	if err != nil {
		ap.previewArea.SetSystemResponse(err.Error())
		return true
	}
	if handled {
		ap.applyCommandStatus(status)
		return true
	}
	if ap.commandMode == CommandModeNone && clickKind == CommandClickLeft && !ap.commandEntry.Empty() {
		ap.applyCommandStatus(commandOutputClearAll("INVALID ENTRY"))
		return true
	}
	return false
}
