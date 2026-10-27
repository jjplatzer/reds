package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type snapshotFile struct {
	Version      int             `json:"version"`
	Source       string          `json:"source"`
	LastSuccess  time.Time       `json:"lastSuccess"`
	FeatureCount int             `json:"featureCount"`
	NOTAMCount   int             `json:"notamCount"`
	GeoJSON      json.RawMessage `json:"geojson"`
}

type snapshotStore struct {
	mu   sync.RWMutex
	data snapshotFile
}

func (s *snapshotStore) get() snapshotFile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data // GeoJSON is immutable after publishing.
}
func (s *snapshotStore) set(data snapshotFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = data
}

func loadSnapshot(path, source string, allowEmpty bool) (snapshotFile, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return snapshotFile{}, nil
	}
	if err != nil {
		return snapshotFile{}, err
	}
	var s snapshotFile
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("decode saved TFR snapshot: %w", err)
	}
	if s.Version != 1 || s.Source != source || s.LastSuccess.IsZero() {
		return s, errors.New("saved TFR snapshot version/source/time mismatch; use a separate TFR_STATE_PATH if the source changed")
	}
	count, notams, err := validateGeoJSON(s.GeoJSON, allowEmpty)
	if err != nil {
		return s, fmt.Errorf("saved TFR snapshot invalid: %w", err)
	}
	if count != s.FeatureCount || notams != s.NOTAMCount {
		return s, errors.New("saved TFR snapshot counts do not match geometry")
	}
	return s, nil
}

// persistSnapshot writes to a sibling temporary file, fsyncs, then atomically
// renames it. A failed refresh cannot destroy the last good snapshot.
func persistSnapshot(path string, s snapshotFile) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tfr-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
