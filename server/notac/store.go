package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var locationRE = regexp.MustCompile(`^[A-Z0-9]{3,5}$`)

func normalizeLocations(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("NOTAC_LOCATIONS is required (comma-separated ICAO codes; an unfiltered global download is disabled)")
	}
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		loc := strings.ToUpper(strings.TrimSpace(part))
		if !locationRE.MatchString(loc) {
			return nil, fmt.Errorf("invalid NOTAC location %q", part)
		}
		if !seen[loc] {
			seen[loc] = true
			out = append(out, loc)
		}
	}
	sort.Strings(out)
	return out, nil
}

// snapshotFile is saved as one atomic JSON document. Its records and cursor
// are updated *together*, so a restart cannot skip a NOTAM change. The next
// page URL is saved too, allowing a long initial load to resume mid-page.
type snapshotFile struct {
	Version          int                        `json:"version"`
	Locations        []string                   `json:"locations"`
	Ready            bool                       `json:"ready"`
	Cursor           string                     `json:"cursor,omitempty"`
	Next             string                     `json:"next,omitempty"`
	Records          map[string]json.RawMessage `json:"records"`
	LastSync         string                     `json:"lastSync,omitempty"`
	APIAsOf          string                     `json:"apiAsOf,omitempty"`
	CreditsRemaining string                     `json:"creditsRemaining,omitempty"`
	Pages            int                        `json:"pages"`
}

func newEmptySnapshot(locations []string) snapshotFile {
	return snapshotFile{Version: 1, Locations: append([]string(nil), locations...), Records: make(map[string]json.RawMessage)}
}

func readSnapshot(path string, locations []string) (snapshotFile, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return newEmptySnapshot(locations), nil
	}
	if err != nil {
		return snapshotFile{}, err
	}
	var s snapshotFile
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("decode saved NOTAM snapshot %s: %w", path, err)
	}
	if s.Version != 1 || strings.Join(s.Locations, ",") != strings.Join(locations, ",") {
		return s, fmt.Errorf("NOTAM snapshot config changed: saved airports=%v, requested airports=%v; use a new NOTAC_STATE_PATH to do a fresh initial sync", s.Locations, locations)
	}
	if s.Records == nil {
		s.Records = make(map[string]json.RawMessage)
	}
	if !s.Ready && s.Cursor != "" && s.Next == "" {
		return s, errors.New("incomplete NOTAM snapshot without a continuation URL; use a fresh state file")
	}
	return s, nil
}

func cloneSnapshot(s snapshotFile) snapshotFile {
	clone := s
	clone.Records = make(map[string]json.RawMessage, len(s.Records))
	for id, data := range s.Records {
		clone.Records[id] = data
	}
	return clone
}

func writeSnapshot(path string, s snapshotFile) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	// Data belongs to the service account, not a world-readable web directory.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".notac-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// payload retains NOTAC's complete record for later display and parsing. We
// never reconstruct its fields from a lossy internal subset.
type noticeEnvelope struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	LocationCode string `json:"location_code"`
	EffectiveEnd string `json:"effective_end"`
}

func isWithdrawn(status string) bool {
	switch strings.ToLower(status) {
	case "cancelled", "canceled", "withdrawn", "archived", "deleted", "expired":
		return true
	default:
		return false
	}
}

func isCurrent(data json.RawMessage, now time.Time) bool {
	var n noticeEnvelope
	if json.Unmarshal(data, &n) != nil || isWithdrawn(n.Status) {
		return false
	}
	if n.EffectiveEnd != "" {
		if until, err := time.Parse(time.RFC3339Nano, n.EffectiveEnd); err == nil && !now.Before(until) {
			return false
		}
	}
	return true
}

// Read side obtains immutable snapshots while the fetcher persists updates.
type snapshotStore struct {
	mu sync.RWMutex
	s  snapshotFile
}

func (st *snapshotStore) get() snapshotFile {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.s // the published records map is immutable
}

func (st *snapshotStore) publish(s snapshotFile) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.s = s
}

func noUnexpectedJSON(b []byte) bool {
	return json.Valid(b) && !bytes.Equal(bytes.TrimSpace(b), []byte("null"))
}
