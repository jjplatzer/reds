package stars

import (
	"strconv"
	"strings"

	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
)

type CommandMode int

const (
	CommandModeNone CommandMode = iota
	CommandModeRange
)

const (
	starsCommandFormatError = "FORMAT"
	starsRangeLimitError    = "RANGE LIMIT"
)

func (p *STARSPane) processKeyboardInput(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Keyboard == nil {
		return
	}
	keyboard := ctx.Keyboard

	// VICE maps the physical STARS <RANGE> key to Ctrl+F11 while the DCB is
	// displayed. REDS currently always displays the STARS DCB strip.
	if keyboard.IsDown(platform.KeyControl) && keyboard.WasPressed(platform.KeyF11) {
		p.setCommandMode(CommandModeRange)
		return
	}

	if p.commandMode != CommandModeRange {
		return
	}

	if keyboard.WasPressed(platform.KeyEscape) {
		p.resetCommand()
		return
	}
	if keyboard.WasPressed(platform.KeyBackspace) {
		if n := len(p.commandInput); n != 0 {
			p.commandInput = p.commandInput[:n-1]
		}
	}

	for _, r := range keyboard.Text {
		if r >= '0' && r <= '9' {
			p.commandInput += string(r)
		} else if !strings.ContainsRune(" \t\r\n", r) {
			p.commandResponse = starsCommandFormatError
			p.commandMode = CommandModeNone
			p.commandInput = ""
			return
		}
	}

	if keyboard.WasPressed(platform.KeyEnter) || keyboard.WasPressed(platform.KeyKeypadEnter) {
		p.commitRangeCommand()
	}
}

func (p *STARSPane) setCommandMode(mode CommandMode) {
	if p == nil {
		return
	}
	p.commandMode = mode
	p.commandInput = ""
	p.commandResponse = ""
}

func (p *STARSPane) resetCommand() {
	if p == nil {
		return
	}
	p.commandMode = CommandModeNone
	p.commandInput = ""
	p.commandResponse = ""
}

// commitRangeCommand implements TI 6191.409 Rev. 30, 4.4.1 Change display
// range: <RANGE>, the desired range using the numeric keypad, then <ENTER>.
func (p *STARSPane) commitRangeCommand() {
	if p == nil {
		return
	}

	rangeNM, err := strconv.Atoi(p.commandInput)
	if err != nil || p.commandInput == "" {
		p.commandResponse = starsCommandFormatError
		p.commandMode = CommandModeNone
		p.commandInput = ""
		return
	}
	if rangeNM < int(minimumSTARSRange) || rangeNM > int(maximumTCWRange) {
		p.commandResponse = starsRangeLimitError
		p.commandMode = CommandModeNone
		p.commandInput = ""
		return
	}

	p.currentPrefs().Range = float32(rangeNM)
	p.commandResponse = ""
	p.commandMode = CommandModeNone
	p.commandInput = ""
}
