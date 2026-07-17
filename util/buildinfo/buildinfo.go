package buildinfo

import (
	"fmt"
	"strings"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func String() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		version = "dev"
	}

	commit := strings.TrimSpace(Commit)
	if commit == "" || commit == "unknown" {
		return version
	}

	return fmt.Sprintf("%s (%s)", version, commit)
}
