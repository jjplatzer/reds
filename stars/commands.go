package stars

import (
	"strings"

	"github.com/juliusplatzer/reds/panes"
	"github.com/juliusplatzer/reds/platform"
)

type CommandMode int

const (
	CommandModeNone CommandMode = iota
	CommandModeMultiFunc
	CommandModeRange
	CommandModeRangeRings
	CommandModePlaceRangeRings
	CommandModeMaps
	CommandModeBrite
	CommandModeBriteSpinner
	CommandModeCharSize
	CommandModeCharSizeSpinner
	CommandModeLDRDir
	CommandModeLDRLen
	CommandModePTLLength
	CommandModeSSAFilter
)

// PreviewString returns the command entry prompt shown in the Preview Area.
// TI 6191.409 4.9.2 explicitly identifies command entry prompts and echoed
// input as Preview Area contents; these prompt strings follow VICE/STARS.
func (m CommandMode) PreviewString() string {
	switch m {
	case CommandModeMultiFunc:
		return "F"
	case CommandModeRange:
		return "RANGE"
	case CommandModeRangeRings:
		return "RR"
	case CommandModeBrite:
		return ""
	case CommandModeBriteSpinner:
		return "BRT"
	case CommandModeCharSize:
		return ""
	case CommandModeCharSizeSpinner:
		return "CHAR"
	case CommandModeLDRDir:
		return "LDR"
	case CommandModeLDRLen:
		return "LDR"
	case CommandModePTLLength:
		return "PTL"
	case CommandModeSSAFilter:
		return ""
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
	// TI 6191.409 Rev. 30, 4.5.4 Hide / show Map category list.
	// <MULTI FUNC>, <T>, <X>, <ENTER> toggles the currently selected list.
	// The manual specifies no response or error message. VICE implements the
	// same command as Multi Func "TX".
	registerCommand(CommandModeMultiFunc, "TX", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.currentPrefs().VideoMapsList.Visible = !p.currentPrefs().VideoMapsList.Visible
		return CommandStatus{}, nil
	})

	// TI 6191.409 Rev. 30, 6.13.9 Toggle beacon code display for
	// Limited data blocks. This command is keyboard-only:
	//   <MULTI FUNC>, <B>, <ENTER>     toggle the current state
	//   <MULTI FUNC>, <B>, <E>, <ENTER> enable/show beacon codes
	//   <MULTI FUNC>, <B>, <I>, <ENTER> inhibit/remove beacon codes
	// The selected state remains in effect until the command is reissued.
	registerCommand(CommandModeMultiFunc, "B", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.currentPrefs().DisplayLDBBeaconCodes = !p.currentPrefs().DisplayLDBBeaconCodes
		return CommandStatus{}, nil
	})
	registerCommand(CommandModeMultiFunc, "BE", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.currentPrefs().DisplayLDBBeaconCodes = true
		return CommandStatus{}, nil
	})
	registerCommand(CommandModeMultiFunc, "BI", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.currentPrefs().DisplayLDBBeaconCodes = false
		return CommandStatus{}, nil
	})

	// TI 6191.409 Rev. 30, 4.11.1-4.11.3 Altitude Filter Commands.
	//
	//   <MULTI FUNC> F <ENTER>
	//       displays the unassociated limits on line 1 and associated limits
	//       on line 2 of the Preview Area.
	//
	//   <MULTI FUNC> F uuuUUU [SPACE aaaAAA] <ENTER>
	//       changes the unassociated range and, when the optional second
	//       six-digit field is supplied, the associated range as well.
	//
	//   <MULTI FUNC> F C aaaAAA <ENTER>
	//       changes the associated range only.
	//
	// Within each six-digit range the two three-digit values may be entered
	// in either order; the lower/higher assignment is handled by the parser.
	registerCommand(CommandModeMultiFunc, "F", func(p *STARSPane, args []any) (CommandStatus, error) {
		return CommandStatus{Output: p.currentPrefs().AltitudeFilters.previewText()}, nil
	})
	registerCommand(CommandModeMultiFunc, "FC[ALT_FILTER_6]", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.currentPrefs().AltitudeFilters.Associated = args[0].([2]int)
		return CommandStatus{}, nil
	})
	registerCommand(CommandModeMultiFunc, "F[ALT_FILTER_6] [ALT_FILTER_6]", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.currentPrefs().AltitudeFilters.Unassociated = args[0].([2]int)
		p.currentPrefs().AltitudeFilters.Associated = args[1].([2]int)
		return CommandStatus{}, nil
	})
	registerCommand(CommandModeMultiFunc, "F[ALT_FILTER_6]", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.currentPrefs().AltitudeFilters.Unassociated = args[0].([2]int)
		return CommandStatus{}, nil
	})

	// TI 6191.409 Rev. 30, 6.13.5-6.13.6 and 6.13.13-6.13.16
	// Quick Look commands. REDS' facility config carries explicit TCPs, so the
	// operator may enter either a full two-character TCP (e.g. 1D) or omit the
	// subset and enter its one-character symbol (e.g. D). Quicklook Group IDs
	// require adaptation not yet exported by crc2reds and therefore return ILL POS.
	applyQuickLookPositions := func(p *STARSPane, positions []quickLookPositionSpec) (CommandStatus, error) {
		// Resolve and validate the complete list before changing any state. STARS
		// rejects the command with ILL POS when any entered TCP is invalid; an
		// earlier valid TCP in the same entry must not be toggled as a side effect.
		resolved := make([]quickLookPositionSpec, len(positions))
		for i, position := range positions {
			tcp, ok := p.resolvedQuickLookTCP(position.TCP)
			if !ok {
				return CommandStatus{}, ErrSTARSIllegalPosition
			}
			resolved[i] = quickLookPositionSpec{TCP: tcp, Plus: position.Plus}
		}

		if len(resolved) == 1 && !resolved[0].Plus && resolved[0].TCP == p.ownTCP() {
			// 6.13.6 / 6.13.16: entering a TCP assigned to this TCW/TDW
			// displays the currently enabled quick looks in the Preview Area.
			return CommandStatus{Output: p.quickLookDisplayStatus()}, nil
		}
		for _, position := range resolved {
			if position.TCP == p.ownTCP() {
				return CommandStatus{}, ErrSTARSIllegalPosition
			}
		}

		for _, position := range resolved {
			p.toggleQuickLookTCP(position.TCP, position.Plus)
		}
		return CommandStatus{Output: p.qlPositionsString()}, nil
	}

	// 6.13.5 implied form: enter another owner's TCP directly, optionally with +.
	registerCommand(CommandModeNone, "[QL_POSITION]", func(p *STARSPane, args []any) (CommandStatus, error) {
		return applyQuickLookPositions(p, []quickLookPositionSpec{args[0].(quickLookPositionSpec)})
	})

	// 6.13.14 Quick look all other owners' tracks. QL+ changes only the
	// presentation color; brightness remains the OTH category because ownership
	// itself has not changed.
	registerCommand(CommandModeMultiFunc, "QALL+", func(p *STARSPane, args []any) (CommandStatus, error) {
		ps := p.currentPrefs()
		ps.QuickLookAll = true
		ps.QuickLookAllIsPlus = true
		return CommandStatus{Output: "QL ALL+"}, nil
	})
	registerCommand(CommandModeMultiFunc, "QALL", func(p *STARSPane, args []any) (CommandStatus, error) {
		ps := p.currentPrefs()
		ps.QuickLookAll = true
		ps.QuickLookAllIsPlus = false
		return CommandStatus{Output: "QL ALL"}, nil
	})

	// 6.13.15 Disable quick look for all tracks. The optional + selects which
	// class is disabled: ordinary QL or QL+.
	registerCommand(CommandModeMultiFunc, "Q+", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.disableQuickLooks(true)
		return CommandStatus{}, nil
	})
	registerCommand(CommandModeMultiFunc, "Q", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.disableQuickLooks(false)
		return CommandStatus{}, nil
	})

	// 6.13.13 toggles up to ten explicit TCPs; 6.13.16 is the special case
	// where the sole entered TCP belongs to the entering TCW/TDW.
	registerCommand(CommandModeMultiFunc, "Q[QL_POSITIONS]", func(p *STARSPane, args []any) (CommandStatus, error) {
		return applyQuickLookPositions(p, args[0].([]quickLookPositionSpec))
	})

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

	// TI 6191.409 Rev. 30, 4.14.5. After selecting <LDR DIR xx>, the
	// operator may enter one of the numeric-keypad directions and press ENTER.
	registerCommand(CommandModeLDRDir, "[LEADER_DIRECTION]", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.currentPrefs().LeaderLineDirection = args[0].(leaderLineDirection)
		return CommandStatus{}, nil
	})

	// TI 6191.409 Rev. 30, 4.14.3. After selecting <LDR LEN n>, the
	// operator may enter any integer from 0 through 7 and press ENTER.
	registerCommand(CommandModeLDRLen, "[LEADER_LENGTH]", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.currentPrefs().LeaderLineLength = args[0].(int)
		return CommandStatus{}, nil
	})

	// TI 6191.409 Rev. 30, 6.3.4 Change Predicted Track Line value.
	// After selecting <PTL LNTH>, the operator may enter a value from 0.0
	// through 5.0 minutes in 0.5-minute increments and press ENTER.
	registerCommand(CommandModePTLLength, "[PTL_LENGTH]", func(p *STARSPane, args []any) (CommandStatus, error) {
		p.currentPrefs().PTLLength = args[0].(float32)
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

	// TI 6191.409 Rev. 30, 4.9.1 Change character font size. After the
	// operator selects one of the CHAR SIZE submenu groups, a numeric value
	// may be entered and committed with <ENTER>. All groups accept 0-5 except
	// DCB, which accepts 0-2.
	registerCommand(CommandModeCharSizeSpinner, "[CHAR_SIZE]", func(p *STARSPane, args []any) (CommandStatus, error) {
		if err := p.setActiveCharSize(args[0].(int)); err != nil {
			return CommandStatus{}, err
		}
		p.commandMode = CommandModeCharSize
		p.activeCharSizeControl = ""
		p.charSizeDragAccumY = 0
		return CommandStatus{Clear: ClearInput}, nil
	})
}

func (p *STARSPane) processKeyboardInput(ctx *panes.Context) {
	if p == nil || ctx == nil || ctx.Keyboard == nil {
		return
	}
	keyboard := ctx.Keyboard

	// VICE maps the physical STARS <MULTI FUNC> key to F7. TI 6191.409
	// 4.5.3-4.5.4 specify these commands as keyboard-only.
	if !keyboard.IsDown(platform.KeyControl) && keyboard.WasPressed(platform.KeyF7) {
		p.setCommandMode(CommandModeMultiFunc)
		return
	}

	// TI 6191.409 Rev. 30 section 2.5 defines the physical <DCB> key as an
	// on/off toggle for the Display Control Bar. VICE maps that key to Ctrl+F9
	// and clears any in-progress command before changing visibility.
	if keyboard.IsDown(platform.KeyControl) && keyboard.WasPressed(platform.KeyF9) {
		p.resetCommand()
		p.currentPrefs().DisplayDCB = !p.currentPrefs().DisplayDCB
		return
	}

	// VICE maps the remaining physical STARS function keys to desktop shortcuts.
	if keyboard.IsDown(platform.KeyControl) && keyboard.WasPressed(platform.KeyF11) &&
		p.currentPrefs().DisplayDCB {
		p.setCommandMode(CommandModeRange)
		return
	}
	if keyboard.IsDown(platform.KeyControl) && keyboard.WasPressed(platform.KeyF3) {
		p.setCommandMode(CommandModeMaps)
		return
	}
	if keyboard.IsDown(platform.KeyControl) && keyboard.WasPressed(platform.KeyF5) {
		p.setCommandMode(CommandModeBrite)
		return
	}
	// VICE maps the physical STARS <CHAR SIZE> key to Ctrl+F7 while the DCB
	// is displayed.
	if keyboard.IsDown(platform.KeyControl) && keyboard.WasPressed(platform.KeyF7) &&
		p.currentPrefs().DisplayDCB {
		p.setCommandMode(CommandModeCharSize)
		return
	}

	// CommandModeNone is also the real STARS implied-command entry state.
	// Ordinary typed text is therefore accumulated even without selecting a
	// DCB/function-key mode; <ENTER> dispatches it to CommandModeNone handlers.

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

	// The first MAPS implementation is the Main-DCB submenu from 4.5.1.
	// Selecting map buttons, CLR ALL, and DONE is mouse/trackball-driven; the
	// separate keyboard MAPS command can be added without changing submenu
	// state. Escape is the desktop equivalent of removing the submenu.
	if p.commandMode == CommandModeMaps {
		if keyboard.WasPressed(platform.KeyEscape) {
			p.resetCommand()
		}
		return
	}

	// TI 6191.409 Rev. 30, 4.7 explicitly makes SSA FILTER a Main-DCB-only
	// command; it has no keyboard entry form and no Preview Area response.
	// Escape is retained as REDS' desktop equivalent of leaving the submenu.
	if p.commandMode == CommandModeSSAFilter {
		if keyboard.WasPressed(platform.KeyEscape) {
			p.resetCommand()
		}
		return
	}

	// With no individual CHAR SIZE adjustment selected, the submenu itself is
	// mouse/DCB driven. Numeric keyboard entry is accepted only after selecting
	// DATA BLOCKS, LISTS, DCB, TOOLS, or POS, matching the manual and VICE.
	if p.commandMode == CommandModeCharSize {
		if keyboard.WasPressed(platform.KeyEscape) {
			p.resetCommand()
		}
		return
	}
	if p.commandMode == CommandModeCharSizeSpinner && keyboard.WasPressed(platform.KeyEscape) {
		// VICE's dcbCharSizeSpinner.ModeAfter() returns to the CHAR SIZE
		// submenu rather than closing the submenu entirely.
		p.commandMode = CommandModeCharSize
		p.activeCharSizeControl = ""
		p.charSizeDragAccumY = 0
		p.commandInput = ""
		p.commandResponse = ""
		return
	}

	if keyboard.WasPressed(platform.KeyEscape) {
		p.resetCommand()
		return
	}
	if keyboard.WasPressed(platform.KeyBackspace) {
		if len(p.commandInput) != 0 {
			r := []rune(p.commandInput)
			p.commandInput = string(r[:len(r)-1])
		} else if p.commandMode == CommandModeMultiFunc && p.multiFuncPrefix != "" {
			p.multiFuncPrefix = ""
		}
	}

	// Echo printable operator input exactly into the Preview Area. VICE/STARS
	// treats the first Multi Func character as a prefix displayed beside the
	// "F" mode indicator (so <MULTI FUNC>, T, X displays "FT" then "X").
	// belongs to typed command matchers (for example [RANGE]) and therefore
	// occurs on <ENTER>, just as VICE's generic command path does.
	for _, r := range keyboard.Text {
		if r < ' ' || r == 0x7f {
			continue
		}
		if p.commandMode == CommandModeNone && p.commandInput == "" {
			p.commandResponse = ""
		}
		s := strings.ToUpper(string(r))
		if p.commandMode == CommandModeMultiFunc && p.multiFuncPrefix == "" {
			p.multiFuncPrefix = s
			continue
		}
		p.commandInput += s
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
	p.multiFuncPrefix = ""
	p.activeBrightnessControl = ""
	p.brightnessDragAccumY = 0
	p.activeCharSizeControl = ""
	p.charSizeDragAccumY = 0
	p.rangeRingDragAccumY = 0
	p.leaderDirectionDragAccumY = 0
	p.leaderLengthDragAccumY = 0
	p.ptlLengthDragAccumY = 0
}

func (p *STARSPane) commitCommand() {
	if p == nil || (p.commandMode == CommandModeNone && p.commandInput == "") {
		return
	}

	input := p.commandInput
	if p.commandMode == CommandModeMultiFunc {
		input = p.multiFuncPrefix + input
	}
	status, err := p.executeCommand(p.commandMode, input)
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
