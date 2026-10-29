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
	// The WFS geometry does not supply ARTCC assignments. Join the separate
	// FAA TFR list on the short NOTAM ID. Never publish an unassigned snapshot
	// as a successful refresh: doing so would silently empty facility lists.
	listURL := m.cfg.ListURL
	if listURL == "" {
		listURL = defaultListURL
	}
	listReq, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return err
	}
	listReq.Header.Set("Accept", "application/json")
	listReq.Header.Set("User-Agent", "REDS-TFR-Mirror/1.0")
	listResp, err := m.httpClient.Do(listReq)
	if err != nil {
		return fmt.Errorf("fetch FAA TFR facility list: %w", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		return fmt.Errorf("FAA TFR facility list HTTP status %s", listResp.Status)
	}
	listRaw, err := io.ReadAll(io.LimitReader(listResp.Body, 4<<20+1))
	if err != nil {
		return fmt.Errorf("read FAA TFR facility list: %w", err)
	}
	if len(listRaw) > 4<<20 {
		return errors.New("FAA TFR facility list exceeds 4 MiB")
	}
	facilities, err := decodeTFRFacilities(listRaw)
	if err != nil {
		return err
	}
	raw, err = annotateTFRFacilities(raw, facilities)
	if err != nil {
		return err
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
