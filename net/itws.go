package net

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	redslog "github.com/juliusplatzer/reds/log"
)

const (
	itwsMaxCells      = 4_000_000
	itwsMaxTotalCells = 16_000_000
	itwsMaxProducts   = 64
)

// ITWSGrid is the server's cropped, unrotated product 9850 raster. The source
// buffer dimensions describe the original RLE storage; Rows/Columns describe
// the geographic rectangle and the repacked Cells stride. Storage padding must
// not affect map placement. Units and XML precision are applied by server/itws.
type ITWSGrid struct {
	Rows                  int     `json:"rows"`
	Columns               int     `json:"columns"`
	SourceBufferRows      int     `json:"sourceBufferRows"`
	SourceBufferColumns   int     `json:"sourceBufferColumns"`
	TRPLatitude           float64 `json:"trpLatitude"`
	TRPLongitude          float64 `json:"trpLongitude"`
	XOffsetMeters         float64 `json:"xOffsetMeters"`
	YOffsetMeters         float64 `json:"yOffsetMeters"`
	DXMeters              float64 `json:"dxMeters"`
	DYMeters              float64 `json:"dyMeters"`
	RotationDegrees       float64 `json:"rotationDegrees"`
	ClearValue            uint8   `json:"clearValue"`
	AttenuatedValue       uint8   `json:"attenuatedValue"`
	APDetectedValue       uint8   `json:"apDetectedValue"`
	BadValue              uint8   `json:"badValue"`
	NoCoverageValue       uint8   `json:"noCoverageValue"`
	MaxPrecipitationLevel uint8   `json:"maxPrecipitationLevel"`
	Encoding              string  `json:"encoding"`
	RowOrder              string  `json:"rowOrder"`
	RLE                   string  `json:"rle"`
	Cells                 []uint8 `json:"-"`
}

type ITWSProduct struct {
	Key            string    `json:"key"`
	ProductID      int       `json:"productId"`
	Site           string    `json:"site"`
	Source         string    `json:"source"`
	GenerationTime time.Time `json:"generationTime"`
	ExpirationTime time.Time `json:"expirationTime"`
	Grid           ITWSGrid  `json:"grid"`
}

type itwsFrame struct {
	Type     string         `json:"type"`
	Feed     string         `json:"feed"`
	Protocol int            `json:"protocol"`
	Revision uint64         `json:"revision"`
	Time     time.Time      `json:"time"`
	Products []*ITWSProduct `json:"products"`
	Product  *ITWSProduct   `json:"product"`
	Key      string         `json:"key"`
}

func (p *ITWSProduct) decode() error {
	if p == nil || p.ProductID != 9850 || p.Site == "" || p.Source == "" ||
		p.Key != "9850:"+p.Site+":"+p.Source || p.GenerationTime.IsZero() ||
		!p.ExpirationTime.After(p.GenerationTime) {
		return errors.New("invalid ITWS product identity/time")
	}
	g := &p.Grid
	if g.Rows <= 0 || g.Columns <= 0 || g.Rows > itwsMaxCells/g.Columns ||
		g.SourceBufferRows < g.Rows || g.SourceBufferColumns < g.Columns ||
		g.SourceBufferRows > itwsMaxCells/g.SourceBufferColumns ||
		g.Encoding != "rle-value-count" || g.RowOrder != "source" ||
		g.ClearValue != 0 || g.MaxPrecipitationLevel > 6 {
		return errors.New("invalid ITWS raster dimensions/encoding")
	}
	for _, value := range []float64{g.TRPLatitude, g.TRPLongitude, g.XOffsetMeters,
		g.YOffsetMeters, g.DXMeters, g.DYMeters, g.RotationDegrees} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("non-finite ITWS geometry")
		}
	}
	if math.Abs(g.TRPLatitude) >= 90 || math.Abs(g.TRPLongitude) > 180 ||
		math.Abs(g.RotationDegrees) > 360 || g.DXMeters <= 0 || g.DYMeters <= 0 ||
		math.Abs(g.XOffsetMeters) > 2e6 || math.Abs(g.YOffsetMeters) > 2e6 ||
		g.DXMeters*float64(g.SourceBufferColumns) > 2e6 || g.DYMeters*float64(g.SourceBufferRows) > 2e6 {
		return errors.New("invalid ITWS grid coordinates")
	}
	allowed := [16]bool{true, true, true, true, true, true, true}
	for _, value := range []uint8{g.AttenuatedValue, g.APDetectedValue, g.BadValue, g.NoCoverageValue} {
		if value < 7 || value > 15 || allowed[value] {
			return errors.New("invalid ITWS status codes")
		}
		allowed[value] = true
	}
	cells := make([]uint8, g.Rows*g.Columns)
	offset, maxLevel := 0, uint8(0)
	for token := range strings.FieldsSeq(g.RLE) {
		v, n, ok := strings.Cut(token, ",")
		value, valueErr := strconv.ParseUint(v, 10, 4)
		count, countErr := strconv.ParseUint(n, 10, 32)
		if !ok || valueErr != nil || countErr != nil || !allowed[value] || count == 0 || count > uint64(len(cells)-offset) {
			return errors.New("invalid ITWS RLE run")
		}
		for end := offset + int(count); offset < end; offset++ {
			cells[offset] = uint8(value)
		}
		if value <= 6 {
			maxLevel = max(maxLevel, uint8(value))
		}
	}
	if offset != len(cells) || maxLevel != g.MaxPrecipitationLevel {
		return errors.New("incomplete ITWS raster or inconsistent maximum level")
	}
	g.Cells, g.RLE = cells, ""
	return nil
}

var errITWSInactive = errors.New("ITWS client inactive")

// ITWSClient owns a revisioned, bounded cache of immutable decoded products.
// Expiration also runs when disconnected. A server idle close waits for actual
// user input before reconnecting, as for the other STARS feeds.
type ITWSClient struct {
	url         string
	site        string
	logger      *redslog.Logger
	ctx         context.Context
	cancel      context.CancelFunc
	activity    chan struct{}
	startOnce   sync.Once
	closeOnce   sync.Once
	connMu      sync.Mutex
	conn        *websocket.Conn
	mu          sync.RWMutex
	products    map[string]*ITWSProduct
	revision    uint64
	generation  uint64
	ready       bool
	serverTime  time.Time
	clockReadAt time.Time
}

func NewITWSClient(rawURL, site string, logger *redslog.Logger) *ITWSClient {
	site = strings.ToUpper(strings.TrimSpace(site))
	if strings.TrimSpace(rawURL) == "" {
		rawURL = ITWSWebSocketURL(site)
	}
	if logger == nil {
		logger = &redslog.Logger{Logger: slog.Default(), Start: time.Now()}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &ITWSClient{url: rawURL, site: site, logger: logger, ctx: ctx, cancel: cancel,
		activity: make(chan struct{}, 1), products: make(map[string]*ITWSProduct)}
}

// Snapshot returns immutable products only when the cache has changed. Never
// mutate the returned products/cell slices. Server time plus local monotonic
// elapsed time prevents a mis-set desktop wall clock from expiring fresh scans.
func (c *ITWSClient) Snapshot(since uint64, now time.Time) ([]*ITWSProduct, uint64) {
	if c == nil {
		return nil, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	serverNow := c.serverNow(now)
	for key, product := range c.products {
		if !product.ExpirationTime.After(serverNow) {
			delete(c.products, key)
			c.generation++
		}
	}
	if since == c.generation {
		return nil, since
	}
	products := make([]*ITWSProduct, 0, len(c.products))
	for _, product := range c.products {
		products = append(products, product)
	}
	sort.Slice(products, func(i, j int) bool { return products[i].Key < products[j].Key })
	return products, c.generation
}

func (c *ITWSClient) serverNow(now time.Time) time.Time {
	if c.serverTime.IsZero() {
		return now
	}
	return c.serverTime.Add(now.Sub(c.clockReadAt))
}
func (c *ITWSClient) Start() {
	if c != nil {
		c.startOnce.Do(func() { go c.run() })
	}
}

func (c *ITWSClient) Close() {
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

func (c *ITWSClient) ReportActivity() {
	if c != nil {
		select {
		case c.activity <- struct{}{}:
		default:
		}
	}
}

func (c *ITWSClient) run() {
	delay := time.Second
	for c.ctx.Err() == nil {
		started := time.Now()
		err := c.connect()
		if c.ctx.Err() != nil {
			return
		}
		if errors.Is(err, errITWSInactive) {
			select {
			case <-c.ctx.Done():
				return
			case <-c.activity:
				delay = time.Second
				continue
			}
		}
		if err != nil {
			c.logger.Warn("ITWS live server disconnected", slog.Any("error", err))
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

func (c *ITWSClient) connect() error {
	conn, response, err := websocket.DefaultDialer.DialContext(c.ctx, c.url, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if response != nil {
			return fmt.Errorf("ITWS WebSocket upgrade returned HTTP %d: %w", response.StatusCode, err)
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

func (c *ITWSClient) readFrames(conn *websocket.Conn) error {
	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			var closed *websocket.CloseError
			if errors.As(err, &closed) && closed.Code == websocket.CloseNormalClosure &&
				(strings.EqualFold(closed.Text, "Client inactivity timeout") || closed.Text == "inactivity_timeout") {
				return errITWSInactive
			}
			return err
		}
		if messageType != websocket.TextMessage {
			return errors.New("ITWS websocket sent non-text data")
		}
		var frame itwsFrame
		if err := json.Unmarshal(payload, &frame); err != nil {
			return fmt.Errorf("decode ITWS frame: %w", err)
		}
		if err := c.apply(frame, time.Now()); err != nil {
			return err
		}
	}
}

func (c *ITWSClient) apply(frame itwsFrame, now time.Time) error {
	if frame.Protocol != 1 || frame.Feed != "itws" {
		return errors.New("unsupported ITWS protocol/feed")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch frame.Type {
	case "snapshot":
		if len(frame.Products) > itwsMaxProducts || frame.Time.IsZero() {
			return errors.New("invalid ITWS snapshot size/time")
		}
		products := make(map[string]*ITWSProduct)
		cells := 0
		for _, product := range frame.Products {
			if product == nil {
				return errors.New("null ITWS product")
			}
			if product.Site != c.site {
				continue
			}
			if _, exists := products[product.Key]; exists {
				return errors.New("duplicate ITWS source")
			}
			if err := product.decode(); err != nil {
				return err
			}
			cells += len(product.Grid.Cells)
			if cells > itwsMaxTotalCells {
				return errors.New("ITWS snapshot exceeds cell limit")
			}
			if product.ExpirationTime.After(frame.Time) {
				products[product.Key] = product
			}
		}
		c.products, c.revision, c.ready = products, frame.Revision, true
		c.serverTime, c.clockReadAt = frame.Time, now
		c.generation++
	case "update", "remove":
		if !c.ready {
			return errors.New("ITWS update before snapshot")
		}
		if frame.Revision <= c.revision {
			return nil
		}
		// Revisions are global: filtering by site legitimately skips revisions.
		if frame.Type == "remove" {
			if _, exists := c.products[frame.Key]; exists {
				delete(c.products, frame.Key)
				c.generation++
			}
		} else {
			product := frame.Product
			if product == nil {
				return errors.New("ITWS update without product")
			}
			if product.Site == c.site {
				if err := product.decode(); err != nil {
					return err
				}
				previous := c.products[product.Key]
				if product.ExpirationTime.After(c.serverNow(now)) &&
					(previous == nil || !product.GenerationTime.Before(previous.GenerationTime)) {
					cells := len(product.Grid.Cells)
					for key, cached := range c.products {
						if key != product.Key {
							cells += len(cached.Grid.Cells)
						}
					}
					if cells > itwsMaxTotalCells || (previous == nil && len(c.products) >= itwsMaxProducts) {
						return errors.New("ITWS cache exceeds limit")
					}
					c.products[product.Key] = product
					c.generation++
				}
			}
		}
		c.revision = frame.Revision
	default:
		return fmt.Errorf("unknown ITWS frame type %q", frame.Type)
	}
	return nil
}
