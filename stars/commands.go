package stars

import (
	"strings"

	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
)

type CommandMode int

const (
	CommandModeNone CommandMode = iota
	CommandModeRange
	CommandModeRangeRings
	CommandModePlaceRangeRings
	CommandModeBrite
	CommandModeBriteSpinner
)

// PreviewString returns the command entry prompt shown in the Preview Area.
// TI 6191.409 4.9.2 explicitly identifies command entry prompts and echoed
// input as Preview Area contents; these prompt strings follow VICE/STARS.
func (m CommandMode) PreviewString() string {
	switch m {
	case CommandModeRange:
		return "RANGE"
	case CommandModeRangeRings:
		return "RR"
	case CommandModeBrite:
		return ""
	case CommandModeBriteSpinner:
		return "BRT"
	default:
		return ""
	}
}

// CommandClear specifies how command state is cleared after execution. The
// zero value deliberately matches VICE: a successful command normally clears
// all command state unless a handler requests otherwise.
type CommandClear int

const (
	ClearAll CommandClear = iota
	ClearInput
	ClearNone
)

// CommandStatus is the reusable result returned by STARS command handlers.
// Output is displayed in the Preview Area.
type CommandStatus struct {
	Clear  CommandClear
	Output string
}

func init() {
	// TI 6191.409 Rev. 30, 4.4.1 Change display range.
	// [RANGE] is a reusable typed command matcher implemented in parsecmd.go.
	registerCommand(CommandModeRange, "[RANGE]", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.currentPrefs().Range = args[0].(float32)
		return CommandStatus{}, nil
	})

	// TI 6191.409 Rev. 30, 6.1.1 Change range ring spacing. The operator
	// manual permits exactly 2, 5, 10, or 20 NM and specifies FORMAT for
	// non-numeric input and ILL VALUE for any other numeric value.
	registerCommand(CommandModeRangeRings, "[RANGE_RING_SPACING]", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.currentPrefs().RangeRingRadius = args[0].(float32)
		return CommandStatus{}, nil
	})

	// TI 6191.409 Rev. 30, 4.10 Change brightness of display objects.
	// Once a BRITE submenu control is selected, a numeric value may be entered
	// from the keyboard and committed with <ENTER>.
	registerCommand(CommandModeBriteSpinner, "[BRIGHTNESS]", func(p *STARSPane, args []any) (CommandStatus, error) {
		if err := p.setActiveBrightness(Brightness(args[0].(int))); err != nil {
			return CommandStatus{}, err
		}
		p.commandMode = CommandModeBrite
		p.activeBrightnessControl = ""
		p.brightnessDragAccumY = 0
		return CommandStatus{Clear: ClearInput}, nil
	})
}

func (p *STARSPane) processKeyboardInput(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Keyboard == nil {
		return
	}
	keyboard := ctx.Keyboard

	// VICE maps physical STARS function keys to desktop shortcuts while the
	// DCB is displayed. REDS currently always displays the STARS DCB strip.
	if keyboard.IsDown(platform.KeyControl) && keyboard.WasPressed(platform.KeyF11) {
		p.setCommandMode(CommandModeRange)
		return
	}
	if keyboard.IsDown(platform.KeyControl) && keyboard.WasPressed(platform.KeyF5) {
		p.setCommandMode(CommandModeBrite)
		return
	}

	if p.commandMode == CommandModeNone {
		return
	}

	// TI 6191.409 Rev. 30, 6.1.2 defines PLACE RR as a Main-DCB-only
	// command: after selecting the button, the operator positions the cursor
	// and clicks the left trackball button. It has no keyboard form and the
	// manual specifies no Preview Area response.
	if p.commandMode == CommandModePlaceRangeRings {
		if keyboard.WasPressed(platform.KeyEscape) {
			p.resetCommand()
		}
		return
	}

	if keyboard.WasPressed(platform.KeyEscape) {
		p.resetCommand()
		return
	}
	if keyboard.WasPressed(platform.KeyBackspace) && len(p.commandInput) != 0 {
		r := []rune(p.commandInput)
		p.commandInput = string(r[:len(r)-1])
	}

	// Echo printable operator input exactly into the Preview Area. Validation
	// belongs to typed command matchers (for example [RANGE]) and therefore
	// occurs on <ENTER>, just as VICE's generic command path does.
	for _, r := range keyboard.Text {
		if r >= ' ' && r != 0x7f {
			p.commandInput += strings.ToUpper(string(r))
		}
	}

	if keyboard.WasPressed(platform.KeyEnter) || keyboard.WasPressed(platform.KeyKeypadEnter) {
		p.commitCommand()
	}
}

func (p *STARSPane) setCommandMode(mode CommandMode) {
	if p == nil {
		return
	}
	p.resetCommand()
	p.commandMode = mode
}

func (p *STARSPane) resetCommand() {
	if p == nil {
		return
	}
	p.commandMode = CommandModeNone
	p.commandInput = ""
	p.commandResponse = ""
	p.activeBrightnessControl = ""
	p.brightnessDragAccumY = 0
	p.rangeRingDragAccumY = 0
}

func (p *STARSPane) commitCommand() {
	if p == nil || p.commandMode == CommandModeNone {
		return
	}

	status, err := p.executeCommand(p.commandMode, p.commandInput)
	if err != nil {
		// Match VICE/STARS command-entry behavior: keep the prompt and echoed
		// input visible so the operator can correct/re-enter the command, and
		// place the official response/error message above them.
		p.commandResponse = err.Error()
		return
	}

	switch status.Clear {
	case ClearAll:
		p.resetCommand()
	case ClearInput:
		p.commandInput = ""
	case ClearNone:
	}
	p.commandResponse = status.Output
}
