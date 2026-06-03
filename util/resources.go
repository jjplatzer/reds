// util/resources.go
//
// Port of the C++ util/resources.cpp helpers. Locates the project root by
// looking for a known resource directory, walking up from both the current
// working directory and the executable's directory. This lets the binary find
// resources/ whether it is run from the repo root or from a build/ subdir.

package util

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// rootMarkers are directories that, if present, identify the project root.
// Mirrors the C++ check for "resources/videomaps" or "resources/bitmaps".
var rootMarkers = []string{
	filepath.Join("resources", "videomaps"),
	filepath.Join("resources", "bitmaps"),
}

var (
	rootOnce sync.Once
	rootDir  string
)

// Unfortunately, unlike io.ReadCloser, zstd.Decoder's Close method doesn't
// return an error, so use the same narrower interface as vice.
type ResourceReadCloser interface {
	io.Reader
	Close()
}

type bytesReadCloser struct {
	*bytes.Reader
}

func (bytesReadCloser) Close() {}

func candidateRoots() []string {
	var roots []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if !seen[p] {
			seen[p] = true
			roots = append(roots, p)
		}
	}

	if cwd, err := os.Getwd(); err == nil {
		add(cwd)
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		add(exeDir)
		add(filepath.Join(exeDir, ".."))
		add(filepath.Join(exeDir, "..", ".."))
		add(filepath.Join(exeDir, "..", "..", ".."))
	}
	return roots
}

func isProjectRoot(path string) bool {
	for _, marker := range rootMarkers {
		if info, err := os.Stat(filepath.Join(path, marker)); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func findProjectRootFrom(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	for {
		if isProjectRoot(path) {
			return path, true
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", false
		}
		path = parent
	}
}

// FindProjectRoot returns the project root directory, cached after first call.
// Falls back to the current working directory if no marker is found.
func FindProjectRoot() string {
	rootOnce.Do(func() {
		for _, c := range candidateRoots() {
			if root, ok := findProjectRootFrom(c); ok {
				rootDir = root
				return
			}
		}
		if cwd, err := os.Getwd(); err == nil {
			rootDir = cwd
		} else {
			rootDir = "."
		}
	})
	return rootDir
}

// FindProjectRelativeDir resolves a path relative to the project root.
func FindProjectRelativeDir(relativePath string) string {
	return filepath.Join(FindProjectRoot(), relativePath)
}

// FindProjectRelativeFile resolves a path relative to the project root.
func FindProjectRelativeFile(relativePath string) string {
	return filepath.Join(FindProjectRoot(), relativePath)
}

func projectFS() fs.StatFS {
	fsys, ok := os.DirFS(FindProjectRoot()).(fs.StatFS)
	if !ok {
		panic("FS from DirFS is not a StatFS?")
	}
	return fsys
}

// LoadResource opens a project-relative resource and transparently
// decompresses zstd-compressed files. It panics if the file cannot be loaded,
// matching vice's resource-loading behavior: missing resources are not
// recoverable at runtime.
func LoadResource(path string) ResourceReadCloser {
	path = filepath.ToSlash(path)
	f, err := fs.ReadFile(projectFS(), path)
	if err != nil {
		panic(err)
	}
	br := bytesReadCloser{bytes.NewReader(f)}

	if filepath.Ext(path) == ".zst" {
		zr, err := zstd.NewReader(br, zstd.WithDecoderConcurrency(0))
		if err != nil {
			panic(err)
		}
		return zr
	}

	return br
}

func LoadResourceBytes(path string) []byte {
	r := LoadResource(path)
	defer r.Close()

	b, err := io.ReadAll(r)
	if err != nil {
		panic(err)
	}
	return b
}

func ResourceExists(path string) bool {
	_, err := projectFS().Stat(filepath.ToSlash(path))
	return err == nil
}
