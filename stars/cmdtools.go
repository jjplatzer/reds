package stars

import (
	"strings"

	redsnet "github.com/juliusplatzer/reds/net"
)

func init() {
	// TI 6191.409 Rev. 30, 6.21.5 / 6.21.9 / 6.21.11. Single-track
	// TPA commands end with a slew and are handled in consumeMouseEvents; the
	// keyboard-only all-track forms live in the declarative command table.
	registerCommand(CommandModeNone, "**J", func(p *STARSPane, args []any) (CommandStatus, error) {
		for key, state := range p.tpaTracks {
			state.JRingRadius = 0
			p.setTPAState(key, state)
		}
		return CommandStatus{}, nil
	})
	registerCommand(CommandModeNone, "**P", func(p *STARSPane, args []any) (CommandStatus, error) {
		for key, state := range p.tpaTracks {
			state.ConeLength = 0
			p.setTPAState(key, state)
		}
		return CommandStatus{}, nil
	})
	setAllTPASize := func(p *STARSPane, enabled bool) CommandStatus {
		p.currentPrefs().DisplayTPASize = enabled
		for key, state := range p.tpaTracks {
			state.DisplaySize = nil
			p.setTPAState(key, state)
		}
		if enabled {
			return CommandStatus{Output: "TPA SIZE ON"}
		}
		return CommandStatus{Output: "TPA SIZE OFF"}
	}
	registerCommand(CommandModeNone, "*D+", func(p *STARSPane, args []any) (CommandStatus, error) {
		return setAllTPASize(p, !p.currentPrefs().DisplayTPASize), nil
	})
	registerCommand(CommandModeNone, "*D+E", func(p *STARSPane, args []any) (CommandStatus, error) {
		return setAllTPASize(p, true), nil
	})
	registerCommand(CommandModeNone, "*D+I", func(p *STARSPane, args []any) (CommandStatus, error) {
		return setAllTPASize(p, false), nil
	})

}

func parseTPAImpliedCommand(input string) (op byte, distance float32, sizeMode byte, recognized bool, err error) {
	input = strings.ToUpper(strings.TrimSpace(input))
	if input == "" {
		return 0, 0, 0, false, nil
	}

	if input == "*D+" {
		return 'D', 0, 0, true, nil
	}
	if input == "*D+E" {
		return 'D', 0, 'E', true, nil
	}
	if input == "*D+I" {
		return 'D', 0, 'I', true, nil
	}
	if input == "*J" || input == "*P" {
		return input[1], 0, 0, true, nil
	}
	if !strings.HasPrefix(input, "*J") && !strings.HasPrefix(input, "*P") {
		return 0, 0, 0, false, nil
	}

	distanceText := input[2:]
	value, rest, matched, parseErr := (tpaDistanceCommandParser{}).Parse(distanceText)
	if parseErr != nil {
		return 0, 0, 0, true, parseErr
	}
	if !matched || rest != "" {
		return 0, 0, 0, true, ErrSTARSCommandFormat
	}
	return input[1], value.(float32), 0, true, nil
}

func (p *STARSPane) applyTPAImpliedCommand(target *redsnet.TaisTarget, op byte, distance float32, sizeMode byte) error {
	if p == nil || target == nil || !taisTargetHasPosition(target) {
		return ErrSTARSNoTrack
	}
	key := targetDisplayStateKey(target)
	if key == "" {
		return ErrSTARSNoTrack
	}
	state := p.tpaState(key)

	switch op {
	case 'J':
		if distance == 0 {
			if state.JRingRadius == 0 {
				return ErrSTARSIllegalFunction
			}
			state.JRingRadius = 0
			p.setTPAState(key, state)
			return nil
		}
		if !state.hasGraphic() && p.tpaGraphicCount() >= starsMaxTPAGraphics {
			return ErrSTARSCapacity
		}
		state.JRingRadius = distance
		state.ConeLength = 0
		p.setTPAState(key, state)
		return nil

	case 'P':
		if distance == 0 {
			if state.ConeLength == 0 {
				return ErrSTARSIllegalFunction
			}
			state.ConeLength = 0
			p.setTPAState(key, state)
			return nil
		}
		if !state.hasGraphic() && p.tpaGraphicCount() >= starsMaxTPAGraphics {
			return ErrSTARSCapacity
		}
		state.ConeLength = distance
		state.JRingRadius = 0
		p.setTPAState(key, state)
		return nil

	case 'D':
		if !state.hasGraphic() {
			return ErrSTARSIllegalFunction
		}
		switch sizeMode {
		case 'E':
			v := true
			state.DisplaySize = &v
		case 'I':
			v := false
			state.DisplaySize = &v
		default:
			current := p.currentPrefs().DisplayTPASize
			if state.DisplaySize != nil {
				current = *state.DisplaySize
			}
			v := !current
			state.DisplaySize = &v
		}
		p.setTPAState(key, state)
		return nil
	}

	return ErrSTARSCommandFormat
}
