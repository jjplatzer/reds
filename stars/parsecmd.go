package stars

import (
	"fmt"
	"github.com/juliusplatzer/reds/radar"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
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

// beaconSelectionCommandParser validates an individual Mode 3/A squawk (four
// octal digits) or a selected beacon bank (two octal digits), per 6.13.11.
// Reject partial and malformed values instead of silently choosing a bank.
type beaconSelectionCommandParser struct{}

func (beaconSelectionCommandParser) Identifier() string { return "BEACON_SELECTION" }

func (beaconSelectionCommandParser) Parse(text string) (any, string, bool, error) {
	if (len(text) != 2 && len(text) != 4) || normalizeBeaconCode(text) != text {
		return nil, text, true, ErrSTARSCommandFormat
	}
	return text, "", true, nil
}

type prefReferenceCommandParser struct{}

func (prefReferenceCommandParser) Identifier() string { return "PREF_REFERENCE" }

func (prefReferenceCommandParser) Parse(text string) (any, string, bool, error) {
	text = strings.ToUpper(strings.TrimSpace(text))
	if text == "" || len(text) > 7 {
		return nil, text, true, ErrSTARSCommandFormat
	}
	for _, r := range text {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return nil, text, true, ErrSTARSCommandFormat
		}
	}
	return text, "", true, nil
}

type prefNameCommandParser struct{}

func (prefNameCommandParser) Identifier() string { return "PREF_NAME" }

func (prefNameCommandParser) Parse(text string) (any, string, bool, error) {
	text = strings.ToUpper(strings.TrimSpace(text))
	if err := validateStarsPreferenceSetName(text); err != nil {
		return nil, text, true, err
	}
	return text, "", true, nil
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

type ptlLengthCommandParser struct{}

func (ptlLengthCommandParser) Identifier() string { return "PTL_LENGTH" }

func (ptlLengthCommandParser) Parse(text string) (any, string, bool, error) {
	// TI 6191.409 Rev. 30, 6.3.4 accepts 0.0 through 5.0 minutes in
	// half-minute increments. Both an out-of-range value and incorrect
	// formatting produce FORMAT. VICE applies the same validation.
	if text == "" {
		return nil, text, true, ErrSTARSCommandFormat
	}
	for _, r := range text {
		if (r < '0' || r > '9') && r != '.' {
			return nil, text, true, ErrSTARSCommandFormat
		}
	}
	value, err := strconv.ParseFloat(text, 32)
	if err != nil || value < 0 || value > 5 {
		return nil, "", true, ErrSTARSCommandFormat
	}
	twice := value * 2
	if twice != float64(int(twice)) {
		return nil, "", true, ErrSTARSCommandFormat
	}
	return float32(value), "", true, nil
}

func decodeAltitudeFilterLimit(hundreds int) int {
	if hundreds == 0 {
		return starsNegativeAltitudeFilterLimitFeet
	}
	return hundreds * 100
}

func sortedAltitudeFilterRange(v [2]int) [2]int {
	if v[0] <= v[1] {
		return v
	}
	return [2]int{v[1], v[0]}
}

type altitudeFilter6CommandParser struct{}

func (altitudeFilter6CommandParser) Identifier() string { return "ALT_FILTER_6" }

func (altitudeFilter6CommandParser) Parse(text string) (any, string, bool, error) {
	// TI 6191.409 Rev. 30, 4.11.2-4.11.3: each altitude-filter
	// range is entered as exactly six digits, two three-digit values in
	// hundreds of feet. The two values may be entered in either order.
	if len(text) < 6 {
		return nil, text, false, nil
	}
	for _, r := range text[:6] {
		if r < '0' || r > '9' {
			return nil, text, true, ErrSTARSCommandFormat
		}
	}

	first, err := strconv.Atoi(text[:3])
	if err != nil {
		return nil, text, true, ErrSTARSCommandFormat
	}
	second, err := strconv.Atoi(text[3:6])
	if err != nil {
		return nil, text, true, ErrSTARSCommandFormat
	}

	filter := sortedAltitudeFilterRange([2]int{
		decodeAltitudeFilterLimit(first),
		decodeAltitudeFilterLimit(second),
	})
	return filter, text[6:], true, nil
}

type tpaDistanceCommandParser struct{}

func (tpaDistanceCommandParser) Identifier() string { return "TPA_DISTANCE" }

func (tpaDistanceCommandParser) Parse(text string) (any, string, bool, error) {
	// TI 6191.409 Rev. 30, 6.21.2-6.21.7: 1 through 30 NM, with
	// exactly one optional tenths digit permitted only for values 1 through 9.
	if text == "" {
		return nil, text, false, nil
	}
	i := 0
	for i < len(text) && text[i] >= '0' && text[i] <= '9' {
		i++
	}
	if i == 0 {
		return nil, text, false, nil
	}
	whole, err := strconv.Atoi(text[:i])
	if err != nil {
		return nil, text, true, ErrSTARSCommandFormat
	}
	value := float32(whole)
	if i < len(text) && text[i] == '.' {
		if whole < 1 || whole > 9 || i+1 >= len(text) || text[i+1] < '0' || text[i+1] > '9' {
			return nil, text, true, ErrSTARSCommandFormat
		}
		value += float32(text[i+1]-'0') / 10
		i += 2
		if i < len(text) && ((text[i] >= '0' && text[i] <= '9') || text[i] == '.') {
			return nil, text, true, ErrSTARSCommandFormat
		}
	}
	if value < 1 || value > 30 {
		return nil, text[i:], true, ErrSTARSCommandFormat
	}
	return value, text[i:], true, nil
}

type atpaVolumeAction struct {
	VolumeID string
	Enable   bool
}

type atpaVolumeActionCommandParser struct{}

func (atpaVolumeActionCommandParser) Identifier() string { return "ATPA_VOLUME_ACTION" }

func (atpaVolumeActionCommandParser) Parse(text string) (any, string, bool, error) {
	// TI 6191.409 Rev. 30, 8.38-8.39: volume ID is 1-5 alphanumeric
	// characters followed immediately by E (enable) or I (inhibit).
	text = strings.ToUpper(strings.TrimSpace(text))
	if len(text) < 2 || len(text) > 6 {
		return nil, text, true, ErrSTARSCommandFormat
	}
	id := text[:len(text)-1]
	for _, r := range id {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return nil, text, true, ErrSTARSCommandFormat
		}
	}
	var enable bool
	switch text[len(text)-1] {
	case 'E':
		enable = true
	case 'I':
		enable = false
	default:
		return nil, text, true, ErrSTARSCommandFormat
	}
	return atpaVolumeAction{VolumeID: id, Enable: enable}, "", true, nil
}

type quickLookPositionSpec struct {
	TCP  string
	Plus bool
}

func parseQuickLookPositionToken(token string) (quickLookPositionSpec, bool) {
	token = strings.ToUpper(strings.TrimSpace(token))
	if token == "" {
		return quickLookPositionSpec{}, false
	}
	plus := strings.HasSuffix(token, "+")
	if plus {
		token = strings.TrimSuffix(token, "+")
	}
	if len(token) < 1 || len(token) > 2 {
		return quickLookPositionSpec{}, false
	}
	for _, r := range token {
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z') {
			return quickLookPositionSpec{}, false
		}
	}
	return quickLookPositionSpec{TCP: token, Plus: plus}, true
}

type quickLookPositionCommandParser struct{}

func (quickLookPositionCommandParser) Identifier() string { return "QL_POSITION" }

func (quickLookPositionCommandParser) Parse(text string) (any, string, bool, error) {
	if text == "" || strings.ContainsAny(text, " \t\r\n") {
		return nil, text, false, nil
	}
	position, ok := parseQuickLookPositionToken(text)
	if !ok {
		return nil, text, true, ErrSTARSCommandFormat
	}
	return position, "", true, nil
}

type quickLookPositionsCommandParser struct{}

func (quickLookPositionsCommandParser) Identifier() string { return "QL_POSITIONS" }

func (quickLookPositionsCommandParser) Parse(text string) (any, string, bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, text, false, nil
	}

	// 6.13.13 places the optional '+' after the TCP list; it applies to every
	// TCP entered by that command. VICE accepts per-token '+' as well, but the
	// operator manual's modality takes precedence here.
	plus := strings.HasSuffix(text, "+")
	if plus {
		text = strings.TrimSpace(strings.TrimSuffix(text, "+"))
	}
	if text == "" || strings.Contains(text, "+") {
		return nil, text, true, ErrSTARSCommandFormat
	}

	var positions []quickLookPositionSpec
	for _, field := range strings.Fields(text) {
		field = strings.ToUpper(field)
		if field == "" {
			continue
		}

		// Explicit two-character TCPs may be entered consecutively. If the
		// subset is omitted, the one-character symbol must be space-delimited;
		// two alphabetic characters are kept together so a future adapted Group
		// TCP ID can be validated by the handler.
		if field[0] >= '0' && field[0] <= '9' {
			if len(field)%2 != 0 {
				return nil, text, true, ErrSTARSCommandFormat
			}
			for i := 0; i < len(field); i += 2 {
				token := field[i : i+2]
				position, ok := parseQuickLookPositionToken(token)
				if !ok || token[0] < '0' || token[0] > '9' {
					return nil, text, true, ErrSTARSCommandFormat
				}
				position.Plus = plus
				positions = append(positions, position)
			}
		} else {
			if len(field) > 2 {
				return nil, text, true, ErrSTARSCommandFormat
			}
			position, ok := parseQuickLookPositionToken(field)
			if !ok {
				return nil, text, true, ErrSTARSCommandFormat
			}
			position.Plus = plus
			positions = append(positions, position)
		}
	}
	if len(positions) == 0 || len(positions) > 10 {
		return nil, text, true, ErrSTARSCommandFormat
	}
	return positions, "", true, nil
}

type rblIDCommandParser struct{}

func (rblIDCommandParser) Identifier() string { return "RBL_ID" }

func (rblIDCommandParser) Parse(text string) (any, string, bool, error) {
	if text == "" {
		return nil, text, false, nil
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return nil, text, false, nil
		}
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		return nil, text, true, ErrSTARSCommandFormat
	}
	return value, "", true, nil
}

type rblFieldCommandParser struct{}

func (rblFieldCommandParser) Identifier() string { return "RBL_FIELD" }

func (rblFieldCommandParser) Parse(text string) (any, string, bool, error) {
	text = strings.ToUpper(strings.TrimSpace(text))
	if text == "" || len(text) > 8 || strings.ContainsAny(text, " \t\r\n") {
		return nil, text, false, nil
	}
	for _, r := range text {
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z') {
			return nil, text, true, ErrSTARSCommandFormat
		}
	}
	return text, "", true, nil
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

type charSizeCommandParser struct{}

func (charSizeCommandParser) Identifier() string { return "CHAR_SIZE" }

func (charSizeCommandParser) Parse(text string) (any, string, bool, error) {
	// TI 6191.409 Rev. 30, 4.9.1: non-numeric entry is FORMAT; numeric
	// values outside the allowable character-size range are ILL VALUE.
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
	if value < 0 || value > 5 {
		return nil, "", true, ErrSTARSIllegalValue
	}
	return value, "", true, nil
}

var commandTypeParsers = map[string]commandTypeParser{
	"BEACON_SELECTION":   beaconSelectionCommandParser{},
	"PREF_REFERENCE":     prefReferenceCommandParser{},
	"PREF_NAME":          prefNameCommandParser{},
	"RANGE":              rangeCommandParser{},
	"RANGE_RING_SPACING": rangeRingSpacingCommandParser{},
	"LEADER_DIRECTION":   leaderDirectionCommandParser{},
	"LEADER_LENGTH":      leaderLengthCommandParser{},
	"PTL_LENGTH":         ptlLengthCommandParser{},
	"BRIGHTNESS":         brightnessCommandParser{},
	"CHAR_SIZE":          charSizeCommandParser{},
	"ALT_FILTER_6":       altitudeFilter6CommandParser{},
	"QL_POSITION":        quickLookPositionCommandParser{},
	"QL_POSITIONS":       quickLookPositionsCommandParser{},
	"RBL_ID":             rblIDCommandParser{},
	"RBL_FIELD":          rblFieldCommandParser{},
	"TPA_DISTANCE":       tpaDistanceCommandParser{},
	"ATPA_VOLUME_ACTION": atpaVolumeActionCommandParser{},
	"CRDA_CONTROL":       crdaControlParser{},
	"RA_TEXT":            restrictionAreaClauseParser{},
	"RA_CIRCLE":          restrictionAreaClauseParser{circle: true},
	"RA_LOCATION":        restrictionAreaLocationParser{},
	"USER_RA_INDEX":      restrictionAreaIDParser{userOnly: true},
	"RA_INDEX":           restrictionAreaIDParser{},
	"RA_MOVE":            restrictionAreaMoveParser{},
	"RA_EDIT_TEXT":       restrictionAreaEditTextParser{},
	"RA_DELTA":           restrictionAreaDeltaParser{},
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

// Restriction commands use the same ID/typed matcher organization as VICE.
// Only user-defined IDs (1-100) can be changed or deleted. Adapted IDs
// (101-200) may be used for per-display text visibility (§6.6.8).
type restrictionAreaIDParser struct{ userOnly bool }

func (p restrictionAreaIDParser) Identifier() string {
	if p.userOnly {
		return "USER_RA_INDEX"
	}
	return "RA_INDEX"
}
func (p restrictionAreaIDParser) Parse(input string) (any, string, bool, error) {
	n := 0
	for n < len(input) && input[n] >= '0' && input[n] <= '9' {
		n++
	}
	if n == 0 {
		return nil, input, false, nil
	}
	id, err := strconv.Atoi(input[:n])
	maxID := 200
	if p.userOnly {
		maxID = 100
	}
	if err != nil || id < 1 || id > maxID {
		return nil, input[n:], true, ErrSTARSIllegalGeoID
	}
	return id, input[n:], true, nil
}

type restrictionAreaMoveSpec struct {
	Blink    bool
	Position *configPoint // nil means trackball designation to follow
}
type restrictionAreaMoveParser struct{}

func (restrictionAreaMoveParser) Identifier() string { return "RA_MOVE" }
func (restrictionAreaMoveParser) Parse(input string) (any, string, bool, error) {
	spec := restrictionAreaMoveSpec{}
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "△") || strings.HasPrefix(input, "∆") || strings.HasPrefix(input, "Δ") {
		_, n := utf8.DecodeRuneInString(input)
		input = strings.TrimSpace(input[n:])
		spec.Blink = true
	}
	if input != "" {
		pos, err := parseRestrictionAreaLocation(input)
		if err != nil {
			return nil, "", true, err // FORMAT and ILL GEO LOC remain distinct
		}
		spec.Position = &pos
	}
	return spec, "", true, nil
}

type restrictionAreaEditTextParser struct{}

func (restrictionAreaEditTextParser) Identifier() string { return "RA_EDIT_TEXT" }
func (restrictionAreaEditTextParser) Parse(input string) (any, string, bool, error) {
	if input == "" {
		return nil, input, false, nil
	}
	// F12 ID T <SPACE> DELTA belongs to §6.6.8, not an edit.
	if isRestrictionDelta(strings.TrimSpace(input)) {
		return nil, input, false, nil
	}
	maxFirst := 30
	if input[0] == ' ' {
		maxFirst = 32
	}
	parsed, err := parseRestrictionAreaText(strings.TrimSpace(input), false)
	if err != nil {
		return nil, "", true, err
	}
	if len(parsed.Text[0]) > maxFirst {
		parsed.Text[0] = parsed.Text[0][:maxFirst]
	}
	return parsed, "", true, nil
}

func isRestrictionDelta(s string) bool { return s == "△" || s == "∆" || s == "Δ" }

type restrictionAreaDeltaParser struct{}

func (restrictionAreaDeltaParser) Identifier() string { return "RA_DELTA" }
func (restrictionAreaDeltaParser) Parse(input string) (any, string, bool, error) {
	if !isRestrictionDelta(strings.TrimSpace(input)) {
		return nil, input, false, nil
	}
	return true, "", true, nil
}

// §6.6 geographic command types are parsed independently of the drawing code.
// The manual distinguishes fix/bearing/range, DMS coordinates and cursor slews.
type restrictionAreaClauseParser struct{ circle bool }

func (r restrictionAreaClauseParser) Identifier() string {
	if r.circle {
		return "RA_CIRCLE"
	}
	return "RA_TEXT"
}
func (r restrictionAreaClauseParser) Parse(input string) (any, string, bool, error) {
	c, err := parseRestrictionAreaClause(input, r.circle)
	return c, "", true, err
}

type restrictionAreaLocationParser struct{}

func (restrictionAreaLocationParser) Identifier() string { return "RA_LOCATION" }
func (restrictionAreaLocationParser) Parse(input string) (any, string, bool, error) {
	pt, err := parseRestrictionAreaLocation(input)
	return pt, "", true, err
}

// starsRestrictionClause carries §6.6 text, modifiers and optional input
// location. Any supplied geographic position is resolved before construction.
type starsRestrictionClause struct {
	Text          [2]string
	Blink, Shaded bool
	Color         int
	Radius        float64
	Location      *configPoint
}

func parseRestrictionAreaClause(input string, circle bool) (starsRestrictionClause, error) {
	c := starsRestrictionClause{Color: 1}
	input = strings.TrimSpace(input)
	if circle {
		// When SPACE is omitted, the first period after an integer radius
		// can already belong to the text (C10. = a 10-NM textless circle).
		idx := 0
		for idx < len(input) && input[idx] >= '0' && input[idx] <= '9' {
			idx++
		}
		if idx+1 < len(input) && input[idx] == '.' && input[idx+1] >= '0' && input[idx+1] <= '9' {
			idx++
			for idx < len(input) && input[idx] >= '0' && input[idx] <= '9' {
				idx++
			}
		}
		if idx == 0 {
			return c, ErrSTARSCommandFormat
		}
		rad, err := strconv.ParseFloat(input[:idx], 64)
		if err != nil {
			return c, ErrSTARSCommandFormat
		}
		if rad < 1 || rad > 125 {
			return c, fmt.Errorf("ILL RANGE")
		}
		c.Radius = rad
		input = strings.TrimSpace(input[idx:])
	}
	fields := strings.Fields(input)
	// The only legal optional geographic suffix is DMS/fix or fix + bearing + range.
	// Do not confuse the ordinary text token with a fix unless a text token
	// precedes it, as required by the keyboard creation procedure.
	if len(fields) > 1 {
		for _, count := range []int{3, 1} {
			if len(fields) <= count {
				continue
			}
			pt, err := parseRestrictionAreaLocation(strings.Join(fields[len(fields)-count:], " "))
			if err == nil {
				c.Location = &pt
				fields = fields[:len(fields)-count]
				break
			}
		}
	}
	parsed, err := parseRestrictionAreaText(strings.Join(fields, " "), circle)
	if err != nil {
		return c, err
	}
	c.Text, c.Blink, c.Shaded, c.Color = parsed.Text, parsed.Blink, parsed.Shaded, parsed.Color
	return c, nil
}

// parseRestrictionAreaText accepts one or two periods-as-spaces text fields,
// followed by the manual's optional delta (blinking), plus (shading), and
// *1 through *8 (graphic color, not text color). Open polygons/text-only
// cannot be shaded. VICE uses exactly the same modifier split.
func parseRestrictionAreaText(input string, closed bool) (starsRestrictionClause, error) {
	c := starsRestrictionClause{Color: 1}
	parts := strings.Fields(strings.ToUpper(strings.TrimSpace(input)))
	if len(parts) == 0 {
		return c, ErrSTARSCommandFormat
	}
	line := 0
	seenOptions := false
	for _, part := range parts {
		if part == "△" || part == "∆" || part == "Δ" || part == "+" || strings.HasPrefix(part, "*") || strings.HasPrefix(part, "△") || strings.HasPrefix(part, "∆") || strings.HasPrefix(part, "Δ") {
			seenOptions = true
			for len(part) > 0 {
				switch {
				case strings.HasPrefix(part, "△"), strings.HasPrefix(part, "∆"), strings.HasPrefix(part, "Δ"):
					c.Blink = true
					_, size := utf8.DecodeRuneInString(part)
					part = part[size:]
				case part[0] == '+':
					if !closed {
						return c, ErrSTARSCommandFormat
					}
					c.Shaded = true
					part = part[1:]
				case part[0] == '*':
					if !closed || len(part) < 2 || part[1] < '1' || part[1] > '8' {
						return c, fmt.Errorf("ILL COLOR")
					}
					c.Color = int(part[1] - '0')
					part = part[2:]
				default:
					return c, ErrSTARSCommandFormat
				}
			}
			continue
		}
		if seenOptions || line >= 2 {
			return c, ErrSTARSCommandFormat
		}
		if len(part) > 32 {
			part = part[:32]
		} // manual CAPACITY: excess final chars ignored
		c.Text[line] = strings.TrimSpace(strings.ReplaceAll(part, ".", " "))
		line++
	}
	if line == 0 {
		return c, ErrSTARSCommandFormat
	}
	return c, nil
}

// parseRestrictionAreaLocation follows TI 6191.409 §6.6:
// DDMMSSN/DDDMMSSW, named CIFP fix, or fix + magnetic bearing + NM range.
func parseRestrictionAreaLocation(input string) (configPoint, error) {
	parts := strings.Fields(strings.ToUpper(strings.TrimSpace(input)))
	if len(parts) == 0 {
		return configPoint{}, ErrSTARSCommandFormat
	}
	if len(parts) == 1 {
		if lat, lon, ok := parseRestrictionAreaDMS(parts[0]); ok {
			return configPoint{Lat: lat, Lon: lon}, nil
		}
		if pt, ok := starsRBLStationaryPoint(parts[0]); ok {
			return pt, nil
		}
		return configPoint{}, fmt.Errorf("ILL GEO LOC")
	}
	if len(parts) != 3 {
		return configPoint{}, ErrSTARSCommandFormat
	}
	pt, ok := starsRBLStationaryPoint(parts[0])
	if !ok {
		return configPoint{}, fmt.Errorf("ILL GEO LOC")
	}
	bearing, errB := strconv.ParseFloat(parts[1], 64)
	rangeNM, errR := strconv.ParseFloat(parts[2], 64)
	if errB != nil || errR != nil || bearing < 1 || bearing > 360 || rangeNM < .01 || rangeNM > 125 {
		return configPoint{}, ErrSTARSCommandFormat
	}
	variation, _ := radar.MagneticVariationAt(pt.Lat, pt.Lon)
	return restrictionAreaOffset(pt, bearing+variation, rangeNM), nil
}

func parseRestrictionAreaDMS(s string) (float64, float64, bool) {
	lat, lon, ok := strings.Cut(s, "/")
	if !ok || len(lat) != 7 || len(lon) != 8 {
		return 0, 0, false
	}
	decode := func(s string, degreeDigits int, maxDegree int, first, second byte) (float64, bool) {
		if s[len(s)-1] != first && s[len(s)-1] != second {
			return 0, false
		}
		for _, b := range []byte(s[:len(s)-1]) {
			if b < '0' || b > '9' {
				return 0, false
			}
		}
		deg, _ := strconv.Atoi(s[:degreeDigits])
		min, _ := strconv.Atoi(s[degreeDigits : degreeDigits+2])
		sec, _ := strconv.Atoi(s[degreeDigits+2 : degreeDigits+4])
		if deg > maxDegree || min > 59 || sec > 59 || (deg == maxDegree && (min != 0 || sec != 0)) {
			return 0, false
		}
		value := float64(deg) + float64(min)/60 + float64(sec)/3600
		if s[len(s)-1] == second {
			value = -value
		}
		return value, true
	}
	a, okA := decode(lat, 2, 90, 'N', 'S')
	b, okB := decode(lon, 3, 180, 'E', 'W')
	return a, b, okA && okB
}

func restrictionAreaOffset(start configPoint, heading, nm float64) configPoint {
	const earthNM = 3440.065
	lat1 := start.Lat * math.Pi / 180
	lon1 := start.Lon * math.Pi / 180
	brg := heading * math.Pi / 180
	theta := nm / earthNM
	lat2 := math.Asin(math.Sin(lat1)*math.Cos(theta) + math.Cos(lat1)*math.Sin(theta)*math.Cos(brg))
	lon2 := lon1 + math.Atan2(math.Sin(brg)*math.Sin(theta)*math.Cos(lat1), math.Cos(theta)-math.Sin(lat1)*math.Sin(lat2))
	return configPoint{Lat: lat2 * 180 / math.Pi, Lon: normalizeLongitude(lon2 * 180 / math.Pi)}
}
