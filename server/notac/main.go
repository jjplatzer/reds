// REDS's server-side NOTAC mirror. Run from the REDS repository root with
// `go run ./server/notac`, or build a standalone executable.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type config struct {
	APIKey    string
	Locations []string
	StatePath string
	HTTPAddr  string
	Interval  time.Duration
	PageDelay time.Duration
	MaxPages  int
	BaseURL   string
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("[NOTAC] %v", err)
	}
}

func run() error {
	var once bool
	flag.BoolVar(&once, "once", false, "sync once, then exit (no HTTP listener)")
	flag.Parse()

	cfg, err := configFromEnv()
	if err != nil {
		return err
	}
	log.Printf("[NOTAC] airports=%s interval=%s pageDelay=%s state=%s", strings.Join(cfg.Locations, ","), cfg.Interval, cfg.PageDelay, cfg.StatePath)

	mirror, err := newMirror(cfg)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !once && cfg.HTTPAddr != "off" {
		listener, err := net.Listen("tcp", cfg.HTTPAddr)
		if err != nil {
			return fmt.Errorf("listen %s: %w", cfg.HTTPAddr, err)
		}
		srv := &http.Server{
			Handler:           mirror.handler(),
			ReadHeaderTimeout: 5 * time.Second,
			WriteTimeout:      20 * time.Second,
		}
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdown)
		}()
		go func() {
			if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("[NOTAC] HTTP listener stopped: %v", err)
				stop()
			}
		}()
		log.Printf("[NOTAC] local HTTP listener on %s", listener.Addr())
	}

	poll := func() error {
		started := time.Now()
		err := mirror.sync(ctx)
		if err != nil {
			log.Printf("[NOTAC] sync failed after %s: %v", time.Since(started).Round(time.Millisecond), err)
		} else {
			s := mirror.snapshot()
			log.Printf("[NOTAC] sync complete: %d notices, initialized=%t, pages=%d, credits remaining=%s", len(s.Records), s.Ready, s.Pages, s.CreditsRemaining)
		}
		return err
	}
	if once {
		return poll()
	}

	// Fixed cadence from startup: failures never trigger uncontrolled retries.
	_ = poll()
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = poll()
		}
	}
}

func configFromEnv() (config, error) {
	cfg := config{
		APIKey:    strings.TrimSpace(os.Getenv("NOTAC_API_KEY")),
		StatePath: firstEnv("NOTAC_STATE_PATH", "server/notac/notac-state.json"),
		HTTPAddr:  firstEnv("NOTAC_HTTP_ADDR", "127.0.0.1:8788"),
		BaseURL:   "https://notac.aero/api/v1/notam/delta/",
	}
	if cfg.APIKey == "" {
		return cfg, errors.New("NOTAC_API_KEY is required (never commit the key)")
	}
	var err error
	cfg.Locations, err = normalizeLocations(os.Getenv("NOTAC_LOCATIONS"))
	if err != nil {
		return cfg, err
	}
	cfg.Interval, err = time.ParseDuration(firstEnv("NOTAC_POLL_INTERVAL", "12h"))
	if err != nil || cfg.Interval < time.Hour {
		return cfg, errors.New("NOTAC_POLL_INTERVAL must be a duration of at least 1h (default 12h)")
	}
	cfg.PageDelay, err = time.ParseDuration(firstEnv("NOTAC_PAGE_DELAY", "10s"))
	if err != nil || cfg.PageDelay < time.Second {
		return cfg, errors.New("NOTAC_PAGE_DELAY must be at least 1s (default 10s)")
	}
	cfg.MaxPages = 50
	if s := strings.TrimSpace(os.Getenv("NOTAC_MAX_PAGES_PER_RUN")); s != "" {
		var n int
		if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n < 1 || n > 500 {
			return cfg, errors.New("NOTAC_MAX_PAGES_PER_RUN must be between 1 and 500")
		}
		cfg.MaxPages = n
	}
	if cfg.HTTPAddr != "off" {
		host, _, err := net.SplitHostPort(cfg.HTTPAddr)
		if err != nil {
			return cfg, fmt.Errorf("NOTAC_HTTP_ADDR: %w", err)
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return cfg, errors.New("NOTAC_HTTP_ADDR must bind to loopback (127.0.0.1 or [::1]); public access needs a separate authenticated proxy")
		}
	}
	cfg.StatePath, err = filepath.Abs(cfg.StatePath)
	return cfg, err
}

func firstEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
