package net

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	redslog "github.com/juliusplatzer/reds/log"

	"github.com/gorilla/websocket"
)

const (
	taisReconnectInitialDelay       = 1 * time.Second
	taisReconnectMaxDelay           = 30 * time.Second
	taisMaxMessageBytes       int64 = 16 << 20
)

var errTaisClientClosed = errors.New("TAIS client closed")

type TaisClient struct {
	logger *redslog.Logger
	url    string
	token  string
	state  *taisState
	dialer *websocket.Dialer

	close chan struct{}

	startOnce sync.Once
	closeOnce sync.Once

	connMu sync.Mutex
	conn   *websocket.Conn

	statusMu  sync.RWMutex
	connected bool
	lastError error
}

func NewTaisClient(url string, logger *redslog.Logger) *TaisClient {
	if logger == nil {
		logger = &redslog.Logger{
			Logger: slog.Default(),
			Start:  time.Now(),
		}
	}
	url = strings.TrimSpace(url)
	if url == "" {
		url = TaisWebSocketURL()
	}

	return &TaisClient{
		logger: logger,
		url:    url,
		token:  LiveServerToken(),
		state:  newTaisState(),
		dialer: websocket.DefaultDialer,
		close:  make(chan struct{}),
	}
}

func (c *TaisClient) Start() {
	if c == nil {
		return
	}
	c.startOnce.Do(func() {
		go c.run()
	})
}

func (c *TaisClient) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.close)
		if c.state != nil {
			c.state.markUnsynced()
		}
		c.statusMu.Lock()
		c.connected = false
		c.lastError = nil
		c.statusMu.Unlock()
		c.closeConnection()
	})
}

// Snapshot returns a detached copy of all TAIS state. For a selected STARS
// facility, SnapshotFacility avoids copying targets from unrelated facilities.
func (c *TaisClient) Snapshot() TaisSnapshot {
	if c == nil || c.state == nil {
		return TaisSnapshot{}
	}
	return c.state.snapshot("")
}

func (c *TaisClient) SnapshotFacility(facility string) TaisSnapshot {
	if c == nil || c.state == nil {
		return TaisSnapshot{}
	}
	return c.state.snapshot(facility)
}

// SnapshotFacilityHistory returns a detached facility snapshot while copying at
// most maxHistory of each target's newest raw history positions. A negative
// limit keeps the complete history; zero omits it. Rendering uses this to avoid
// copying the client's full 64-position trail on every frame.
func (c *TaisClient) SnapshotFacilityHistory(facility string, maxHistory int) TaisSnapshot {
	if c == nil || c.state == nil {
		return TaisSnapshot{}
	}
	return c.state.snapshotHistory(facility, maxHistory)
}

func (c *TaisClient) Target(key string) (TaisTarget, bool) {
	if c == nil || c.state == nil {
		return TaisTarget{}, false
	}
	return c.state.target(strings.TrimSpace(key))
}

func (c *TaisClient) Status() TaisClientStatus {
	if c == nil || c.state == nil {
		return TaisClientStatus{}
	}
	ready, revision, targetCount := c.state.status()

	c.statusMu.RLock()
	status := TaisClientStatus{
		Connected:   c.connected,
		Ready:       ready,
		Revision:    revision,
		TargetCount: targetCount,
		LastError:   c.lastError,
	}
	c.statusMu.RUnlock()
	return status
}

func (c *TaisClient) run() {
	delay := taisReconnectInitialDelay
	for {
		if c.closed() {
			return
		}

		connected, err := c.connectAndServe()
		if errors.Is(err, errTaisClientClosed) {
			return
		}
		if err != nil {
			c.setDisconnected(err)
			c.logger.Warn(
				"TAIS live server disconnected",
				slog.String("url", c.url),
				slog.Any("error", err),
			)
		}

		if connected {
			delay = taisReconnectInitialDelay
		}
		if !c.wait(delay) {
			return
		}
		if delay < taisReconnectMaxDelay {
			delay *= 2
			if delay > taisReconnectMaxDelay {
				delay = taisReconnectMaxDelay
			}
		}
	}
}

func (c *TaisClient) connectAndServe() (bool, error) {
	if c == nil {
		return false, errTaisClientClosed
	}

	headers := http.Header{}
	if c.token != "" {
		headers.Set("Authorization", "Bearer "+c.token)
	}
	c.logger.Info(
		"Connecting to TAIS live server",
		slog.String("url", c.url),
		slog.Bool("auth", c.token != ""),
	)

	conn, response, err := c.dialer.Dial(c.url, headers)
	if err != nil {
		if response != nil && response.Body != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		return false, fmt.Errorf("connect TAIS websocket %s: %w", c.url, err)
	}
	c.setConnection(conn)
	defer func() {
		c.clearConnection(conn)
		_ = conn.Close()
	}()

	conn.SetReadLimit(taisMaxMessageBytes)
	c.state.markUnsynced()
	c.setConnected()

	if err := c.readFrames(conn); err != nil {
		return true, err
	}
	return true, nil
}

func (c *TaisClient) readFrames(conn *websocket.Conn) error {
	for {
		if c.closed() {
			return errTaisClientClosed
		}

		messageType, reader, err := conn.NextReader()
		if err != nil {
			if c.closed() {
				return errTaisClientClosed
			}
			return err
		}
		if messageType != websocket.TextMessage {
			return fmt.Errorf("TAIS websocket sent non-text message type %d", messageType)
		}

		var frame TaisFrame
		decoder := json.NewDecoder(reader)
		if err := decoder.Decode(&frame); err != nil {
			return fmt.Errorf("decode TAIS frame: %w", err)
		}
		if err := c.state.apply(frame); err != nil {
			// A revision gap cannot be repaired from incremental events because
			// protocol-v1 has no replay log. Reconnect so the server sends a fresh,
			// authoritative snapshot.
			c.state.markUnsynced()
			return err
		}

		if frame.Type == "snapshot" {
			status := c.Status()
			c.logger.Info(
				"TAIS snapshot synchronized",
				slog.Uint64("revision", status.Revision),
				slog.Int("targets", status.TargetCount),
			)
		}
	}
}

func (c *TaisClient) setConnection(conn *websocket.Conn) {
	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()
}

func (c *TaisClient) clearConnection(conn *websocket.Conn) {
	c.connMu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.connMu.Unlock()
}

func (c *TaisClient) closeConnection() {
	c.connMu.Lock()
	conn := c.conn
	c.connMu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (c *TaisClient) setConnected() {
	c.statusMu.Lock()
	c.connected = true
	c.lastError = nil
	c.statusMu.Unlock()
}

func (c *TaisClient) setDisconnected(err error) {
	if c.state != nil {
		c.state.markUnsynced()
	}
	c.statusMu.Lock()
	c.connected = false
	c.lastError = err
	c.statusMu.Unlock()
}

func (c *TaisClient) wait(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-c.close:
		return false
	}
}

func (c *TaisClient) closed() bool {
	select {
	case <-c.close:
		return true
	default:
		return false
	}
}

const taisClientHistoryCapacity = 64

type taisState struct {
	mu sync.RWMutex

	ready       bool
	revision    uint64
	generatedAt time.Time
	targets     map[string]TaisTarget
}

func newTaisState() *taisState {
	return &taisState{targets: make(map[string]TaisTarget)}
}

func (s *taisState) markUnsynced() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.ready = false
	s.mu.Unlock()
}

func (s *taisState) apply(frame TaisFrame) error {
	if s == nil {
		return fmt.Errorf("TAIS state is unavailable")
	}
	if frame.Protocol != TaisProtocolVersion {
		return fmt.Errorf("TAIS protocol %d is unsupported; expected %d", frame.Protocol, TaisProtocolVersion)
	}
	if frame.Feed != TaisFeedName {
		return fmt.Errorf("TAIS frame has feed %q; expected %q", frame.Feed, TaisFeedName)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch frame.Type {
	case "snapshot":
		next := make(map[string]TaisTarget, len(frame.Targets))
		for i := range frame.Targets {
			target := frame.Targets[i]
			if strings.TrimSpace(target.Key) == "" {
				return fmt.Errorf("TAIS snapshot contains target with empty key")
			}
			if _, exists := next[target.Key]; exists {
				return fmt.Errorf("TAIS snapshot contains duplicate target %q", target.Key)
			}
			next[target.Key] = target
		}

		// A snapshot is authoritative even if the server restarted and its
		// revision counter is lower than the previous connection's revision.
		s.targets = next
		s.revision = frame.Revision
		s.generatedAt = frame.GeneratedAt
		s.ready = true
		return nil

	case "update":
		if !s.ready {
			return fmt.Errorf("TAIS update received before snapshot")
		}
		if err := s.requireNextRevision(frame.Revision); err != nil {
			return err
		}
		if frame.Target == nil || strings.TrimSpace(frame.Target.Key) == "" {
			return fmt.Errorf("TAIS update revision %d has no target", frame.Revision)
		}

		target := *frame.Target
		previous, hadPrevious := s.targets[target.Key]
		if target.History == nil {
			target.History = updatedTaisHistory(previous, hadPrevious, target)
		}
		s.targets[target.Key] = target
		s.revision = frame.Revision
		return nil

	case "remove":
		if !s.ready {
			return fmt.Errorf("TAIS remove received before snapshot")
		}
		if err := s.requireNextRevision(frame.Revision); err != nil {
			return err
		}
		if strings.TrimSpace(frame.Key) == "" {
			return fmt.Errorf("TAIS remove revision %d has empty key", frame.Revision)
		}
		delete(s.targets, frame.Key)
		s.revision = frame.Revision
		return nil

	default:
		return fmt.Errorf("unknown TAIS frame type %q", frame.Type)
	}
}

func (s *taisState) requireNextRevision(revision uint64) error {
	expected := s.revision + 1
	if revision != expected {
		return fmt.Errorf("TAIS revision gap: have %d, got %d, expected %d", s.revision, revision, expected)
	}
	return nil
}

func updatedTaisHistory(previous TaisTarget, hadPrevious bool, target TaisTarget) []TaisHistory {
	var history []TaisHistory
	if hadPrevious && !target.Track.NewTrack {
		history = previous.History
	}

	// The server only places a point in raw history when mrtTime, lat, and lon
	// are all present. In protocol-v1, missing scalar JSON fields decode to the
	// zero value; a non-zero mrtTime is therefore the reliable discriminator for
	// normal live position updates. Pseudo flight-plan-only targets have no
	// mrtTime and are not appended.
	if target.Track.MRTTime.IsZero() {
		return history
	}

	if n := len(history); n != 0 {
		last := history[n-1].Time
		if target.Track.MRTTime.Equal(last) {
			// Same-MRT reports may contain a flight-plan update. The server updates
			// current state without adding another raw history point.
			return history
		}
		if target.Track.MRTTime.Before(last) {
			// The server should never publish an out-of-order update, but retaining
			// the current trail here is safer than regressing it.
			return history
		}
	}

	history = append(history, TaisHistory{
		Time:             target.Track.MRTTime,
		Lat:              target.Track.Lat,
		Lon:              target.Track.Lon,
		ReportedAltitude: target.Track.ReportedAltitude,
		VX:               target.Track.VX,
		VY:               target.Track.VY,
		VerticalRate:     target.Track.VerticalRate,
		ADSB:             target.Track.ADSB,
	})
	if len(history) > taisClientHistoryCapacity {
		history = history[len(history)-taisClientHistoryCapacity:]
	}
	return history
}

func (s *taisState) snapshot(facility string) TaisSnapshot {
	return s.snapshotHistory(facility, -1)
}

func (s *taisState) snapshotHistory(facility string, maxHistory int) TaisSnapshot {
	if s == nil {
		return TaisSnapshot{}
	}
	facility = strings.ToUpper(strings.TrimSpace(facility))

	s.mu.RLock()
	out := TaisSnapshot{
		Ready:       s.ready,
		Revision:    s.revision,
		GeneratedAt: s.generatedAt,
		Targets:     make([]TaisTarget, 0, len(s.targets)),
	}
	for _, target := range s.targets {
		if facility != "" && !strings.EqualFold(target.Facility, facility) {
			continue
		}
		out.Targets = append(out.Targets, cloneTaisTargetHistory(target, maxHistory))
	}
	s.mu.RUnlock()

	sort.Slice(out.Targets, func(i, j int) bool {
		return out.Targets[i].Key < out.Targets[j].Key
	})
	return out
}

func (s *taisState) target(key string) (TaisTarget, bool) {
	if s == nil {
		return TaisTarget{}, false
	}
	s.mu.RLock()
	target, ok := s.targets[key]
	if ok {
		target = cloneTaisTarget(target)
	}
	s.mu.RUnlock()
	return target, ok
}

func (s *taisState) status() (ready bool, revision uint64, targetCount int) {
	if s == nil {
		return false, 0, 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready, s.revision, len(s.targets)
}

func cloneTaisTarget(target TaisTarget) TaisTarget {
	return cloneTaisTargetHistory(target, -1)
}

func cloneTaisTargetHistory(target TaisTarget, maxHistory int) TaisTarget {
	out := target
	if target.RecordMeta != nil {
		value := *target.RecordMeta
		out.RecordMeta = &value
	}
	if target.FlightPlan != nil {
		value := *target.FlightPlan
		out.FlightPlan = &value
	}
	if target.EnhancedData != nil {
		value := *target.EnhancedData
		out.EnhancedData = &value
	}
	if target.History != nil && maxHistory != 0 {
		history := target.History
		if maxHistory > 0 && len(history) > maxHistory {
			history = history[len(history)-maxHistory:]
		}
		out.History = append([]TaisHistory(nil), history...)
	} else if maxHistory == 0 {
		out.History = nil
	}
	return out
}
