package asdex

import (
	"strconv"
	"strings"
	"unicode"
)

type DcbSpinnerKind int

const (
	DcbSpinnerNone DcbSpinnerKind = iota
	DcbSpinnerRange
)

type DcbSpinner struct {
	Kind     DcbSpinnerKind
	Function DcbFunction

	Title string

	Min  int
	Max  int
	Step int

	Value    int
	Original int

	input  string
	cursor int
}

func NewRangeDcbSpinner(currentRange int) *DcbSpinner {
	currentRange = clampInt(currentRange, 6, 300)
	input := strconv.Itoa(currentRange)

	return &DcbSpinner{
		Kind:     DcbSpinnerRange,
		Function: DcbFunctionRange,
		Title:    "RANGE",
		Min:      6,
		Max:      300,
		Step:     1,
		Value:    currentRange,
		Original: currentRange,
		input:    input,
		cursor:   len(input),
	}
}

func (s *DcbSpinner) DisplayLines() []string {
	if s == nil {
		return nil
	}
	return []string{s.Title, s.InputText()}
}

func (s *DcbSpinner) CursorLine() int {
	return 2
}

func (s *DcbSpinner) CursorColumn() int {
	if s == nil {
		return 0
	}
	return s.cursor
}

func (s *DcbSpinner) InputText() string {
	if s == nil {
		return ""
	}
	return s.input
}

func (s *DcbSpinner) SetValue(value int) {
	if s == nil {
		return
	}

	value = clampInt(value, s.Min, s.Max)
	s.Value = value
	s.input = strconv.Itoa(value)
	s.cursor = len(s.input)
}

func (s *DcbSpinner) Increment(delta int) {
	if s == nil || delta == 0 {
		return
	}

	step := s.Step
	if step <= 0 {
		step = 1
	}

	value := s.Value
	if parsed, ok := s.ParsedValue(); ok {
		value = parsed
	}
	s.SetValue(value + delta*step)
}

func (s *DcbSpinner) Insert(r rune) {
	if s == nil || !unicode.IsDigit(r) {
		return
	}

	value := []rune(s.input)
	if len(value) >= 3 {
		return
	}

	s.cursor = clampInt(s.cursor, 0, len(value))
	value = append(value[:s.cursor], append([]rune{r}, value[s.cursor:]...)...)
	s.input = string(value)
	s.cursor++
}

func (s *DcbSpinner) Backspace() {
	if s == nil || s.cursor <= 0 {
		return
	}

	value := []rune(s.input)
	s.cursor = clampInt(s.cursor, 0, len(value))
	if s.cursor <= 0 {
		return
	}

	s.cursor--
	value = append(value[:s.cursor], value[s.cursor+1:]...)
	s.input = string(value)
}

func (s *DcbSpinner) DeleteForward() {
	if s == nil {
		return
	}

	value := []rune(s.input)
	s.cursor = clampInt(s.cursor, 0, len(value))
	if s.cursor >= len(value) {
		return
	}

	value = append(value[:s.cursor], value[s.cursor+1:]...)
	s.input = string(value)
}

func (s *DcbSpinner) MoveLeft() {
	if s != nil && s.cursor > 0 {
		s.cursor--
	}
}

func (s *DcbSpinner) MoveRight() {
	if s == nil {
		return
	}

	value := []rune(s.input)
	if s.cursor < len(value) {
		s.cursor++
	}
}

func (s *DcbSpinner) ParsedValue() (int, bool) {
	if s == nil {
		return 0, false
	}

	text := strings.TrimSpace(s.input)
	if text == "" {
		return 0, false
	}

	value, err := strconv.Atoi(text)
	if err != nil || value < s.Min || value > s.Max {
		return 0, false
	}
	return value, true
}
