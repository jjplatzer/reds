package net

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	redslog "github.com/juliusplatzer/reds/log"
)

// AirportConfiguration is the part of an APTC report used by STARS GI text.
// Keep runway descriptions intact; qualifiers such as OFFSET are significant.
type AirportConfiguration struct {
	Airport          string    `json:"airport"`
	ArrivalRunways   string    `json:"arrRunwayConf"`
	DepartureRunways string    `json:"depRunwayConf"`
	UpdateTime       time.Time `json:"updateTime"`
}

type aptcFrame struct {
	Type           string                 `json:"type"`
	Feed           string                 `json:"feed"`
	Protocol       int                    `json:"protocol"`
	Revision       uint64                 `json:"revision"`
	Configurations []AirportConfiguration `json:"configurations"`
	Configuration  *AirportConfiguration  `json:"configuration"`
}

var errAPTCInactive = errors.New("APTC client inactive")

// APTCClient owns the background connection and detached airport reports.
// An intentional server idle disconnect waits for user input before reconnecting.
type APTCClient struct {
	url       string
	logger    *redslog.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	activity  chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
	connMu    sync.Mutex
	conn      *websocket.Conn
	mu        sync.RWMutex
	airports  map[string]AirportConfiguration
	revision  uint64
	ready     bool
}

func NewAPTCClient(rawURL string, logger *redslog.Logger) *APTCClient {
	if strings.TrimSpace(rawURL) == "" {
		rawURL = APTCWebSocketURL()
	}
	if logger == nil {
		logger = &redslog.Logger{Logger: slog.Default(), Start: time.Now()}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &APTCClient{url: rawURL, logger: logger, ctx: ctx, cancel: cancel,
		activity: make(chan struct{}, 1), airports: make(map[string]AirportConfiguration)}
}

func (c *APTCClient) Start() {
	if c != nil {
		c.startOnce.Do(func() { go c.run() })
	}
}

func (c *APTCClient) Close() {
	if c != nil {
		c.closeOnce.Do(func() {
			c.cancel()
			c.connMu.Lock()
			if c.conn != nil {
				_ = c.conn.Close()
			}
			c.connMu.Unlock()
		})
	}
}

func (c *APTCClient) ReportActivity() {
	if c != nil {
		select {
		case c.activity <- struct{}{}:
		default:
		}
	}
}

// Configuration retains the last good report through transient disconnections.
// A newly received snapshot replaces the whole cache, including removed airports.
func (c *APTCClient) Configuration(airport string) (AirportConfiguration, bool) {
	if c == nil {
		return AirportConfiguration{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	configuration, ok := c.airports[strings.ToUpper(strings.TrimSpace(airport))]
	return configuration, ok
}

func (c *APTCClient) run() {
	delay := time.Second
	for c.ctx.Err() == nil {
		started := time.Now()
		err := c.connect()
		if c.ctx.Err() != nil {
			return
		}
		if errors.Is(err, errAPTCInactive) {
			select {
			case <-c.ctx.Done():
				return
			case <-c.activity:
				delay = time.Second
				continue
			}
		}
		if err != nil {
			c.logger.Warn("APTC live server disconnected", slog.Any("error", err))
		}
		c.mu.RLock()
		stable := c.ready && time.Since(started) >= 30*time.Second
		c.mu.RUnlock()
		if stable {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}

func (c *APTCClient) connect() error {
	conn, response, err := websocket.DefaultDialer.DialContext(c.ctx, c.url, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return err
	}
	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()
	defer func() {
		_ = conn.Close()
		c.connMu.Lock()
		c.conn = nil
		c.connMu.Unlock()
	}()
	conn.SetReadLimit(16 << 20)
	c.mu.Lock()
	c.ready = false
	c.mu.Unlock()

	readError := make(chan error, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		readError <- c.readFrames(conn)
	}()
	defer func() {
		_ = conn.Close()
		<-readDone // Never let an old session apply updates after the next snapshot.
	}()
	var lastActivity time.Time
	for {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		case err := <-readError:
			return err
		case <-c.activity:
			now := time.Now()
			if now.Sub(lastActivity) >= 30*time.Second {
				if err := conn.SetWriteDeadline(now.Add(5 * time.Second)); err != nil {
					return err
				}
				if err := conn.WriteJSON(ActivityMessage{Type: "activity"}); err != nil {
					return err
				}
				lastActivity = now
			}
		}
	}
}

func (c *APTCClient) readFrames(conn *websocket.Conn) error {
	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			var closed *websocket.CloseError
			if errors.As(err, &closed) && closed.Code == websocket.CloseNormalClosure &&
				(strings.EqualFold(closed.Text, "Client inactivity timeout") || closed.Text == "inactivity_timeout") {
				return errAPTCInactive
			}
			return err
		}
		if messageType != websocket.TextMessage {
			return errors.New("APTC websocket sent non-text data")
		}
		var frame aptcFrame
		if err := json.Unmarshal(payload, &frame); err != nil {
			return fmt.Errorf("decode APTC frame: %w", err)
		}
		if err := c.apply(frame); err != nil {
			return err
		}
	}
}

func (c *APTCClient) apply(frame aptcFrame) error {
	if frame.Protocol != 1 || frame.Feed != "aptc" {
		return errors.New("unsupported APTC protocol/feed")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch frame.Type {
	case "snapshot":
		airports := make(map[string]AirportConfiguration, len(frame.Configurations))
		for _, configuration := range frame.Configurations {
			configuration.Airport = strings.ToUpper(strings.TrimSpace(configuration.Airport))
			if configuration.Airport == "" {
				return errors.New("APTC snapshot has no airport identifier")
			}
			airports[configuration.Airport] = configuration
		}
		c.airports, c.revision, c.ready = airports, frame.Revision, true
	case "update":
		if !c.ready {
			return errors.New("APTC update received before snapshot")
		}
		if frame.Revision <= c.revision {
			return nil
		}
		if frame.Revision != c.revision+1 {
			return errors.New("APTC revision gap; reconnecting for a snapshot")
		}
		if frame.Configuration == nil || strings.TrimSpace(frame.Configuration.Airport) == "" {
			return errors.New("APTC update has no airport configuration")
		}
		configuration := *frame.Configuration
		configuration.Airport = strings.ToUpper(strings.TrimSpace(configuration.Airport))
		c.airports[configuration.Airport], c.revision = configuration, frame.Revision
	default:
		return fmt.Errorf("unknown APTC frame type %q", frame.Type)
	}
	return nil
}
