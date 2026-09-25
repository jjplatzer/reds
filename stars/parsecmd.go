package stars

import (
	"fmt"
	"strconv"
	"strings"
)

// Command Processing System
//
// This intentionally follows VICE's declarative STARS command style: command
// specifications combine literal text with reusable typed matchers in square
// brackets. REDS only needs [RANGE] today, but future commands can add typed
// building blocks without duplicating keyboard parsing/validation logic.

type commandTypeParser interface {
	Identifier() string
	Parse(text string) (value any, remaining string, matched bool, err error)
}

type rangeCommandParser struct{}

func (rangeCommandParser) Identifier() string { return "RANGE" }

func (rangeCommandParser) Parse(text string) (any, string, bool, error) {
	if text == "" {
		return nil, text, true, ErrSTARSCommandFormat
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return nil, text, true, ErrSTARSCommandFormat
		}
	}

	value, err := strconv.Atoi(text)
	if err != nil {
		return nil, text, true, ErrSTARSCommandFormat
	}
	if value < int(minimumSTARSRange) || value > int(maximumTCWRange) {
		return nil, "", true, ErrSTARSRangeLimit
	}
	return float32(value), "", true, nil
}

type rangeRingSpacingCommandParser struct{}

func (rangeRingSpacingCommandParser) Identifier() string { return "RANGE_RING_SPACING" }

func (rangeRingSpacingCommandParser) Parse(text string) (any, string, bool, error) {
	if text == "" {
		return nil, text, true, ErrSTARSCommandFormat
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return nil, text, true, ErrSTARSCommandFormat
		}
	}

	value, err := strconv.Atoi(text)
	if err != nil {
		return nil, text, true, ErrSTARSCommandFormat
	}
	switch value {
	case 2, 5, 10, 20:
		return float32(value), "", true, nil
	default:
		return nil, "", true, ErrSTARSIllegalValue
	}
}

type leaderDirectionCommandParser struct{}

func (leaderDirectionCommandParser) Identifier() string { return "LEADER_DIRECTION" }

func (leaderDirectionCommandParser) Parse(text string) (any, string, bool, error) {
	// TI 6191.409 Rev. 30, 4.14.5 permits exactly one numeric-keypad
	// direction: 1/2/3/4/6/7/8/9. The manual specifies FORMAT for 5 and
	// all other invalid direction entries.
	if len(text) != 1 || text[0] < '0' || text[0] > '9' {
		return nil, text, true, ErrSTARSCommandFormat
	}
	direction, ok := leaderLineDirectionFromKeypad(int(text[0] - '0'))
	if !ok {
		return nil, "", true, ErrSTARSCommandFormat
	}
	return direction, "", true, nil
}

type leaderLengthCommandParser struct{}

func (leaderLengthCommandParser) Identifier() string { return "LEADER_LENGTH" }

func (leaderLengthCommandParser) Parse(text string) (any, string, bool, error) {
	// TI 6191.409 Rev. 30, 4.14.3 permits exactly the eight selectable
	// leader-line lengths 0 through 7. Non-numeric input is FORMAT; a numeric
	// value outside the range is RANGE LIMIT.
	if text == "" {
		return nil, text, true, ErrSTARSCommandFormat
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return nil, text, true, ErrSTARSCommandFormat
		}
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		return nil, text, true, ErrSTARSCommandFormat
	}
	if value < 0 || value > 7 {
		return nil, "", true, ErrSTARSRangeLimit
	}
	return value, "", true, nil
}

type brightnessCommandParser struct{}

func (brightnessCommandParser) Identifier() string { return "BRIGHTNESS" }

func (brightnessCommandParser) Parse(text string) (any, string, bool, error) {
	if text == "" {
		return nil, text, true, ErrSTARSCommandFormat
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return nil, text, true, ErrSTARSCommandFormat
		}
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		return nil, text, true, ErrSTARSCommandFormat
	}
	if value < 0 || value > 100 {
		return nil, "", true, ErrSTARSIllegalValue
	}
	return value, "", true, nil
}

var commandTypeParsers = map[string]commandTypeParser{
	"RANGE":              rangeCommandParser{},
	"RANGE_RING_SPACING": rangeRingSpacingCommandParser{},
	"LEADER_DIRECTION":   leaderDirectionCommandParser{},
	"LEADER_LENGTH":      leaderLengthCommandParser{},
	"BRIGHTNESS":         brightnessCommandParser{},
}

type commandMatcher interface {
	Match(text string) (value any, remaining string, matched bool, err error)
}

type literalCommandMatcher string

func (m literalCommandMatcher) Match(text string) (any, string, bool, error) {
	literal := string(m)
	if !strings.HasPrefix(text, literal) {
		return nil, text, false, nil
	}
	return nil, text[len(literal):], true, nil
}

type typedCommandMatcher struct {
	parser commandTypeParser
}

func (m typedCommandMatcher) Match(text string) (any, string, bool, error) {
	return m.parser.Parse(text)
}

type userCommand struct {
	spec     string
	matchers []commandMatcher
	handler  func(*STARSPane, []any) (CommandStatus, error)
}

var userCommands = make(map[CommandMode][]userCommand)

func registerCommand(mode CommandMode, spec string, handler func(*STARSPane, []any) (CommandStatus, error)) {
	matchers, err := makeCommandMatchers(spec)
	if err != nil {
		panic(fmt.Sprintf("invalid STARS command %q: %v", spec, err))
	}
	userCommands[mode] = append(userCommands[mode], userCommand{
		spec:     spec,
		matchers: matchers,
		handler:  handler,
	})
}

func makeCommandMatchers(spec string) ([]commandMatcher, error) {
	var matchers []commandMatcher
	for len(spec) != 0 {
		open := strings.IndexByte(spec, '[')
		if open == -1 {
			matchers = append(matchers, literalCommandMatcher(spec))
			break
		}
		if open != 0 {
			matchers = append(matchers, literalCommandMatcher(spec[:open]))
			spec = spec[open:]
		}

		close := strings.IndexByte(spec, ']')
		if close == -1 {
			return nil, fmt.Errorf("unclosed typed matcher")
		}
		id := spec[1:close]
		parser := commandTypeParsers[id]
		if parser == nil {
			return nil, fmt.Errorf("unknown typed matcher %q", id)
		}
		matchers = append(matchers, typedCommandMatcher{parser: parser})
		spec = spec[close+1:]
	}
	return matchers, nil
}

func (p *STARSPane) executeCommand(mode CommandMode, input string) (CommandStatus, error) {
	commands := userCommands[mode]
	var firstErr error

	for _, command := range commands {
		remaining := input
		args := make([]any, 0, len(command.matchers))
		matched := true

		for _, matcher := range command.matchers {
			value, rest, ok, err := matcher.Match(remaining)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				matched = false
				break
			}
			if !ok {
				matched = false
				break
			}
			if value != nil {
				args = append(args, value)
			}
			remaining = rest
		}

		if matched && remaining == "" {
			return command.handler(p, args)
		}
	}

	if firstErr != nil {
		return CommandStatus{}, firstErr
	}
	return CommandStatus{}, ErrSTARSCommandFormat
}
