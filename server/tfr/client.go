package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type mirror struct {
	cfg        config
	httpClient *http.Client
	state      snapshotStore
	now        func() time.Time
}

func newMirror(cfg config) (*mirror, error) {
	existing, err := loadSnapshot(cfg.StatePath, cfg.SourceURL, cfg.AllowEmpty)
	if err != nil {
		return nil, err
	}
	m := &mirror{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout:       40 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
		now: time.Now,
	}
	m.state.set(existing)
	return m, nil
}

func (m *mirror) snapshot() snapshotFile { return m.state.get() }

func (m *mirror) sync(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.cfg.SourceURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/geo+json, application/json")
	req.Header.Set("User-Agent", "REDS-TFR-Mirror/1.0 (+https://tfr.faa.gov)")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch FAA WFS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("FAA WFS HTTP status %s", resp.Status)
	}
	// Reading at most limit+1 detects truncated-by-limit responses. Decode
	// only after the *entire* bounded body has been received successfully.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read FAA WFS response: %w", err)
	}
	if len(raw) > int(maxResponseBytes) {
		return errors.New("FAA WFS response exceeds 32 MiB")
	}
	features, notams, err := validateGeoJSON(raw, m.cfg.AllowEmpty)
	if err != nil {
		return fmt.Errorf("reject FAA WFS response: %w", err)
	}
	s := snapshotFile{
		Version:      1,
		Source:       m.cfg.SourceURL,
		LastSuccess:  m.now().UTC(),
		FeatureCount: features,
		NOTAMCount:   notams,
		GeoJSON:      append([]byte(nil), raw...),
	}
	if err := persistSnapshot(m.cfg.StatePath, s); err != nil {
		return fmt.Errorf("save FAA TFR snapshot: %w", err)
	}
	m.state.set(s)
	return nil
}
