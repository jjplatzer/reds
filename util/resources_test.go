package util

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRootFromMacOSAppResources(t *testing.T) {
	root := t.TempDir()
	resourceRoot := filepath.Join(
		root,
		"REDS.app",
		"Contents",
		"Resources",
	)

	if err := os.MkdirAll(
		filepath.Join(resourceRoot, "resources", "videomaps"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}

	found, ok := findRootFromCandidates([]string{resourceRoot})
	if !ok {
		t.Fatal("resource root was not found")
	}

	if found != resourceRoot {
		t.Fatalf("got %q, expected %q", found, resourceRoot)
	}
}
