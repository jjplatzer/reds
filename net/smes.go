package net

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	redslog "github.com/juliusplatzer/reds/log"

	"github.com/gorilla/websocket"
)

const (
	smesReconnectDelay      = 2 * time.Second
	smesActivityMinInterval = 30 * time.Second
)

var errSmesClientClosed = errors.New("SMES client closed")
var errSmesClientInactive = errors.New("SMES client inactive")

type SmesClient struct {
	logger *redslog.Logger
	url    string

	incoming      chan SmesFrame
	errors        chan error
	status        chan SmesStatusEvent
	airportChange chan struct{}
	activity      chan struct{}
	close         chan struct{}

	startOnce sync.Once
	closeOnce sync.Once

	mu             sync.RWMutex
	airport        string
	statusReported bool
	lastStatus     SmesStatus
}

func NewSmesClient(url string, logger *redslog.Logger) *SmesClient {
	if logger == nil {
		logger = &redslog.Logger{
			Logger: slog.Default(),
			Start:  time.Now(),
		}
	}

	return &SmesClient{
		logger:        logger,
		url:           url,
		incoming:      make(chan SmesFrame, 256),
		errors:        make(chan error, 16),
		status:        make(chan SmesStatusEvent, 1),
		airportChange: make(chan struct{}, 1),
		activity:      make(chan struct{}, 1),
		close:         make(chan struct{}),
	}
}

func (c *SmesClient) Start() {
	if c == nil {
		return
	}
	c.startOnce.Do(func() {
		go c.run()
	})
}

func (c *SmesClient) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.close)
	})
}

func (c *SmesClient) SetAirport(icao string) {
	if c == nil {
		return
	}

	icao = strings.ToUpper(strings.TrimSpace(icao))
	c.mu.Lock()
	c.airport = icao
	c.mu.Unlock()

	select {
	case c.airportChange <- struct{}{}:
	default:
	}
}

func (c *SmesClient) ReportActivity() {
	if c == nil {
		return
	}

	select {
	case c.activity <- struct{}{}:
	default:
	}
}

func (c *SmesClient) Frames() <-chan SmesFrame {
	if c == nil {
		return nil
	}
	return c.incoming
}

func (c *SmesClient) Errors() <-chan error {
	if c == nil {
		return nil
	}
	return c.errors
}

func (c *SmesClient) Status() <-chan SmesStatusEvent {
	if c == nil {
		return nil
	}
	return c.status
}

func (c *SmesClient) run() {
	for {
		select {
		case <-c.close:
			return
		default:
		}

		headers := http.Header{}
		c.logger.Info(
			"Connecting to live server",
			slog.String("url", c.url),
		)

		conn, _, err := websocket.DefaultDialer.Dial(c.url, headers)
		if err != nil {
			c.logger.Warn(
				"Live server connection failed",
				slog.String("url", c.url),
				slog.Any("error", err),
			)
			c.reportError(fmt.Errorf("connect SMES websocket %s: %w", c.url, err))
			c.reportStatus(SmesStatusDisconnected, err)
			if !c.waitToReconnect() {
				return
			}
			continue
		}

		err = c.serve(conn)
		_ = conn.Close()
		c.reportStatus(SmesStatusDisconnected, err)
		if errors.Is(err, errSmesClientInactive) {
			c.logger.Info(
				"Live server disconnected",
				slog.String("reason", "inactivity_timeout"),
			)
			if !c.waitForActivity() {
				return
			}
			continue
		}
		if err != nil && !errors.Is(err, errSmesClientClosed) {
			c.logger.Warn(
				"Live server disconnected",
				slog.Any("error", err),
			)
			c.reportError(fmt.Errorf("SMES websocket %s: %w", c.url, err))
		} else if errors.Is(err, errSmesClientClosed) {
			c.logger.Debug("Live server client closed")
		} else {
			c.logger.Info("Live server disconnected")
		}
		if !c.waitToReconnect() {
			return
		}
	}
}

func (c *SmesClient) serve(conn *websocket.Conn) error {
	if err := c.writeAirport(conn); err != nil {
		return err
	}
	c.reportStatus(SmesStatusConnected, nil)
	c.logger.Info(
		"Live server connected",
		slog.String("airport", c.currentAirport()),
	)

	lastActivitySent := time.Now().UTC()

	readError := make(chan error, 1)
	go func() {
		readError <- c.readFrames(conn)
	}()

	for {
		select {
		case <-c.close:
			return errSmesClientClosed
		case <-c.airportChange:
			if err := c.writeAirport(conn); err != nil {
				return err
			}
			c.logger.Debug(
				"Live airport subscription updated",
				slog.String("airport", c.currentAirport()),
			)
		case <-c.activity:
			now := time.Now().UTC()
			if now.Sub(lastActivitySent) >= smesActivityMinInterval {
				if err := c.writeActivity(conn); err != nil {
					return err
				}
				lastActivitySent = now
			}
		case err := <-readError:
			return err
		}
	}
}

func (c *SmesClient) readFrames(conn *websocket.Conn) error {
	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		var frame SmesFrame
		if err := json.Unmarshal(payload, &frame); err != nil {
			c.logger.Warn(
				"Malformed SMES frame",
				slog.Any("error", err),
			)
			c.reportError(fmt.Errorf("decode SMES frame: %w", err))
			continue
		}
		if frame.Type == "limit" {
			if frame.Reason == "inactivity_timeout" {
				return errSmesClientInactive
			}
			continue
		}
		if frame.Type == "connected" || frame.Key == "" {
			continue
		}

		select {
		case c.incoming <- frame:
		case <-c.close:
			return errSmesClientClosed
		}
	}
}

func (c *SmesClient) writeAirport(conn *websocket.Conn) error {
	airport := c.currentAirport()

	airports := []string{}
	if airport != "" {
		airports = append(airports, airport)
	}
	return conn.WriteJSON(SetAirportsMessage{
		Type:     "setAirports",
		Airports: airports,
	})
}

func (c *SmesClient) currentAirport() string {
	if c == nil {
		return ""
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.airport
}

func (c *SmesClient) writeActivity(conn *websocket.Conn) error {
	return conn.WriteJSON(ActivityMessage{
		Type: "activity",
	})
}

func (c *SmesClient) reportError(err error) {
	select {
	case c.errors <- err:
	default:
	}
}

func (c *SmesClient) reportStatus(status SmesStatus, err error) {
	c.mu.Lock()
	if c.statusReported && c.lastStatus == status {
		c.mu.Unlock()
		return
	}
	c.statusReported = true
	c.lastStatus = status
	c.mu.Unlock()

	event := SmesStatusEvent{Status: status, Err: err}
	select {
	case c.status <- event:
		return
	default:
	}

	select {
	case <-c.status:
	default:
	}
	select {
	case c.status <- event:
	default:
	}
}

func (c *SmesClient) waitForActivity() bool {
	c.logger.Info("Waiting for client activity before reconnect")

	select {
	case <-c.activity:
		return true
	case <-c.close:
		return false
	}
}

func (c *SmesClient) waitToReconnect() bool {
	c.logger.Debug(
		"Waiting to reconnect",
		slog.Duration("delay", smesReconnectDelay),
	)

	timer := time.NewTimer(smesReconnectDelay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-c.close:
		return false
	}
}
