// REDS's server-side FAA TFR geometry mirror. It deliberately does not infer
// whether a published restriction is active: that requires the NOTAM text.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const defaultListURL = "https://tfr.faa.gov/tfrapi/exportTfrList"

const defaultWFSURL = "https://tfr.faa.gov/geoserver/TFR/ows?service=WFS&version=1.1.0&request=GetFeature&typeName=TFR%3AV_TFR_LOC&outputFormat=application%2Fjson&srsname=EPSG%3A4326"

const maxResponseBytes int64 = 32 << 20

type config struct {
	SourceURL  string
	ListURL    string
	DetailURL  string
	StatePath  string
	HTTPAddr   string
	Interval   time.Duration
	AllowEmpty bool
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("[TFR] %v", err)
	}
}

func run() error {
	once := flag.Bool("once", false, "fetch and save one snapshot, then exit")
	flag.Parse()
	cfg, err := configFromEnv()
	if err != nil {
		return err
	}
	mirror, err := newMirror(cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !*once && cfg.HTTPAddr != "off" {
		listener, err := net.Listen("tcp", cfg.HTTPAddr)
		if err != nil {
			return fmt.Errorf("listen %s: %w", cfg.HTTPAddr, err)
		}
		server := &http.Server{
			Handler:           mirror.handler(),
			ReadHeaderTimeout: 5 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()
		go func() {
			if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("[TFR] HTTP listener stopped: %v", err)
				stop()
			}
		}()
		log.Printf("[TFR] HTTP listening on %s", listener.Addr())
	}

	poll := func() error {
		start := time.Now()
		err := mirror.sync(ctx)
		if err != nil {
			log.Printf("[TFR] refresh failed (%s); retaining previous snapshot: %v", time.Since(start).Round(time.Millisecond), err)
			return err
		}
		snap := mirror.snapshot()
		log.Printf("[TFR] refresh ok: features=%d NOTAMs=%d asOf=%s (%s)", snap.FeatureCount, snap.NOTAMCount, snap.LastSuccess, time.Since(start).Round(time.Millisecond))
		return nil
	}
	if *once {
		return poll()
	}
	log.Printf("[TFR] source=%s, interval=%s, state=%s", cfg.SourceURL, cfg.Interval, cfg.StatePath)
	_ = poll() // Keep serving an existing cached snapshot if the FAA is unavailable.
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
		SourceURL:  envOr("TFR_SOURCE_URL", defaultWFSURL),
		ListURL:    envOr("TFR_LIST_URL", defaultListURL),
		DetailURL:  envOr("TFR_DETAIL_URL", defaultDetailURL),
		StatePath:  envOr("TFR_STATE_PATH", "server/tfr/tfr-state.json"),
		HTTPAddr:   envOr("TFR_HTTP_ADDR", "127.0.0.1:8790"),
		AllowEmpty: strings.EqualFold(os.Getenv("TFR_ALLOW_EMPTY"), "true"),
	}
	var err error
	cfg.Interval, err = time.ParseDuration(envOr("TFR_POLL_INTERVAL", "10m"))
	if err != nil || cfg.Interval < time.Minute {
		return cfg, errors.New("TFR_POLL_INTERVAL must be a duration >= 1m")
	}
	if strings.TrimSpace(cfg.StatePath) == "" {
		return cfg, errors.New("TFR_STATE_PATH is required")
	}
	u, err := url.Parse(cfg.SourceURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return cfg, errors.New("TFR_SOURCE_URL must be an absolute HTTPS URL without credentials or fragment")
	}
	q := u.Query()
	if !strings.EqualFold(q.Get("service"), "WFS") || !strings.EqualFold(q.Get("request"), "GetFeature") ||
		!strings.EqualFold(q.Get("typeName"), "TFR:V_TFR_LOC") || !strings.EqualFold(q.Get("outputFormat"), "application/json") ||
		!strings.EqualFold(q.Get("srsname"), "EPSG:4326") {
		return cfg, errors.New("TFR_SOURCE_URL must request the FAA TFR WFS layer as EPSG:4326 GeoJSON")
	}
	listURL, err := url.Parse(cfg.ListURL)
	if err != nil || listURL.Scheme != "https" || listURL.Host != "tfr.faa.gov" || listURL.User != nil || listURL.Fragment != "" || listURL.Path != "/tfrapi/exportTfrList" {
		return cfg, errors.New("TFR_LIST_URL must be the official FAA TFR list URL")
	}
	detailURL, err := url.Parse(strings.ReplaceAll(cfg.DetailURL, "%s", "EXAMPLE"))
	if err != nil || detailURL.Scheme != "https" || detailURL.Host != "tfr.faa.gov" || detailURL.User != nil || detailURL.Fragment != "" || detailURL.Path != "/download/detail_EXAMPLE.xml" {
		return cfg, errors.New("TFR_DETAIL_URL must be the official FAA detail XML URL template")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if s := strings.TrimSpace(os.Getenv(key)); s != "" {
		return s
	}
	return fallback
}
