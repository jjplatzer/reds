//go:build windows

package log

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

var (
	crashStderrMu   sync.Mutex
	crashStderrFile *os.File
	crashStderrPath string
)

// RedirectStderrToCrashFile redirects stderr only when the application does
// not already have a real console.
//
// A clean REDS shutdown removes the file. A fatal runtime/cgo crash skips
// deferred cleanup and leaves the file behind for diagnosis.
func RedirectStderrToCrashFile(logDir string) string {
	if logDir == "" {
		return ""
	}

	// Preserve a real console when REDS was started from PowerShell/cmd.
	if stderr, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE); err == nil && stderr != 0 {
		var mode uint32
		if windows.GetConsoleMode(stderr, &mode) == nil {
			return ""
		}
	}

	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return ""
	}

	path := filepath.Join(
		logDir,
		"crash-stderr-"+time.Now().Format("20060102T150405")+".txt",
	)

	file, err := os.OpenFile(
		path,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0o600,
	)
	if err != nil {
		return ""
	}

	_, _ = fmt.Fprintf(
		file,
		"=== REDS crash stderr capture, start %s ===\n",
		time.Now().Format(time.RFC3339),
	)

	if err := windows.SetStdHandle(
		windows.STD_ERROR_HANDLE,
		windows.Handle(file.Fd()),
	); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return ""
	}

	// The slog text handler uses os.Stderr.
	os.Stderr = file

	crashStderrMu.Lock()
	crashStderrFile = file
	crashStderrPath = path
	crashStderrMu.Unlock()

	return path
}

func RemoveCurrentCrashStderrFile() {
	crashStderrMu.Lock()
	file := crashStderrFile
	path := crashStderrPath
	crashStderrFile = nil
	crashStderrPath = ""
	crashStderrMu.Unlock()

	if file == nil {
		return
	}

	_ = file.Close()

	if path != "" {
		_ = os.Remove(path)
	}
}
