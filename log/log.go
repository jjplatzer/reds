package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

type Logger struct {
	*slog.Logger

	LogDir  string
	LogFile string
	Start   time.Time
}

func DefaultLogDir(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}

	dir, err := os.UserConfigDir()
	if err != nil {
		return "."
	}

	return filepath.Join(dir, "REDS")
}

func New(level string, dir string) (*Logger, error) {
	dir = DefaultLogDir(dir)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create REDS log directory %s: %w", dir, err)
	}

	logFile := filepath.Join(dir, "reds.slog")
	writer := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    32,
		MaxBackups: 1,
		Compress:   false,
	}

	slogLevel, err := parseLevel(level)
	if err != nil {
		return nil, err
	}

	logger := &Logger{
		Logger: slog.New(newHandler(
			writer,
			&slog.HandlerOptions{
				Level: slogLevel,
			},
		)),
		LogDir:  dir,
		LogFile: logFile,
		Start:   time.Now(),
	}

	logger.Info(
		"REDS started",
		slog.Time("start", logger.Start),
		slog.String("goos", runtime.GOOS),
		slog.String("goarch", runtime.GOARCH),
		slog.String("go_version", runtime.Version()),
		slog.Int("cpus", runtime.NumCPU()),
	)

	if build, ok := debug.ReadBuildInfo(); ok {
		logger.Info(
			"Build information",
			slog.String("path", build.Path),
			slog.String("go_version", build.GoVersion),
		)
	}

	return logger, nil
}

func (logger *Logger) With(args ...any) *Logger {
	if logger == nil {
		return nil
	}

	return &Logger{
		Logger:  logger.Logger.With(args...),
		LogDir:  logger.LogDir,
		LogFile: logger.LogFile,
		Start:   logger.Start,
	}
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf(
			"invalid log level %q; expected debug, info, warn, or error",
			value,
		)
	}
}

type handler struct {
	json slog.Handler
	text slog.Handler
}

func newHandler(writer io.Writer, options *slog.HandlerOptions) slog.Handler {
	return &handler{
		json: slog.NewJSONHandler(writer, options),
		text: slog.NewTextHandler(
			os.Stderr,
			&slog.HandlerOptions{
				Level: slog.LevelWarn,
			},
		),
	}
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.json.Enabled(ctx, level) ||
		h.text.Enabled(ctx, level)
}

func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	if h.text.Enabled(ctx, record.Level) {
		_ = h.text.Handle(ctx, record)
	}

	if h.json.Enabled(ctx, record.Level) {
		return h.json.Handle(ctx, record)
	}

	return nil
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{
		json: h.json.WithAttrs(slices.Clone(attrs)),
		text: h.text.WithAttrs(slices.Clone(attrs)),
	}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{
		json: h.json.WithGroup(name),
		text: h.text.WithGroup(name),
	}
}
