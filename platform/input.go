package platform

import redsmath "github.com/juliusplatzer/reds/math"

// MouseButton identifies a mouse button in REDS' platform-independent order.
type MouseButton int

const (
	MouseButtonLeft MouseButton = iota
	MouseButtonRight
	MouseButtonMiddle
	MouseButtonCount
)

// MouseState is the mouse input for one frame. Coordinates are in logical
// GLFW window coordinates with origin at the top-left and y increasing down.
type MouseState struct {
	Pos   redsmath.Vec2
	Delta redsmath.Vec2
	Wheel redsmath.Vec2

	Down     [MouseButtonCount]bool
	Pressed  [MouseButtonCount]bool
	Released [MouseButtonCount]bool
}

func (m MouseState) IsDown(b MouseButton) bool {
	if b < 0 || b >= MouseButtonCount {
		return false
	}
	return m.Down[b]
}

func (m MouseState) WasPressed(b MouseButton) bool {
	if b < 0 || b >= MouseButtonCount {
		return false
	}
	return m.Pressed[b]
}

func (m MouseState) WasReleased(b MouseButton) bool {
	if b < 0 || b >= MouseButtonCount {
		return false
	}
	return m.Released[b]
}

// Key identifies a small platform-independent set of keys needed by panes and
// scopes. The set can grow as ASDE-X/STARS/ERAM command handling is ported.
type Key int

const (
	KeyEscape Key = iota
	KeyEnter
	KeyKeypadEnter
	KeyBackspace
	KeyDelete
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
	KeyAlt
	KeyShift
	KeyControl
)

// KeyboardState is the keyboard input for one frame.
type KeyboardState struct {
	Down     map[Key]bool
	Pressed  map[Key]bool
	Released map[Key]bool

	Text []rune
}

func (k KeyboardState) IsDown(key Key) bool     { return k.Down[key] }
func (k KeyboardState) WasPressed(key Key) bool { return k.Pressed[key] }
func (k KeyboardState) WasReleased(key Key) bool {
	return k.Released[key]
}
