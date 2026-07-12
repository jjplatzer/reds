//go:build !windows

package log

func RedirectStderrToCrashFile(_ string) string {
	return ""
}

func RemoveCurrentCrashStderrFile() {
}
