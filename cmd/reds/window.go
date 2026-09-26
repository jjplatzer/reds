package main

import (
	"fmt"
	"os"
	"os/exec"
)

// launchNewWindow starts another REDS application instance. Each instance owns
// its GLFW window, ImGui context, renderer, and active pane, which lets multiple
// ERAM, STARS, and ASDE-X views remain live at the same time without coupling
// their display/input state.
//
// REDS' current platform backend intentionally owns a single native window and
// ImGui/OpenGL backend per process. Starting a sibling instance therefore keeps
// the existing window untouched while the new instance opens at the normal
// startup menu.
func launchNewWindow() (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("locate REDS executable: %w", err)
	}

	cmd := exec.Command(executable, os.Args[1:]...)
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start REDS window: %w", err)
	}

	pid := cmd.Process.Pid

	// Reap the sibling while this instance remains alive. The operating system
	// keeps the sibling process running independently if this window exits first.
	go func() {
		_ = cmd.Wait()
	}()

	return pid, nil
}
