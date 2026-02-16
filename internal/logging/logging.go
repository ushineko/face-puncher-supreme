/*
Package logging configures structured logging with file rotation.

Logs are written to both stderr (text format, for human reading) and a
rotated JSON log file (for machine parsing and post-hoc analysis).
The file logger uses lumberjack for size-based rotation.
*/
package logging

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Config holds logging configuration.
type Config struct {
	// LogDir is the directory for log files. If empty, file logging is disabled.
	LogDir string
	// Verbose enables DEBUG-level logging. Default is INFO.
	Verbose bool
}

// Setup creates a logger that writes to stderr and optionally to a rotated
// log file. Returns the logger and a cleanup function to close the file.
func Setup(cfg Config) (logger *slog.Logger, cleanup func()) {
	level := slog.LevelInfo
	if cfg.Verbose {
		level = slog.LevelDebug
	}

	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})

	if cfg.LogDir == "" {
		return slog.New(stderrHandler), func() {}
	}

	// Ensure log directory exists.
	if err := os.MkdirAll(cfg.LogDir, 0o750); err != nil { //nolint:gosec // log directory
		// Fall back to stderr-only if we can't create the directory.
		slog.New(stderrHandler).Warn("failed to create log directory, file logging disabled",
			"dir", cfg.LogDir,
			"error", err,
		)
		return slog.New(stderrHandler), func() {}
	}

	lj := &lumberjack.Logger{
		Filename:   filepath.Join(cfg.LogDir, "fpsd.log"),
		MaxSize:    10, // MB per file
		MaxBackups: 3,  // keep 3 old files
		MaxAge:     7,  // days to retain
		Compress:   true,
	}

	fileHandler := slog.NewJSONHandler(lj, &slog.HandlerOptions{
		Level: level,
	})

	multi := &multiHandler{
		handlers: []slog.Handler{stderrHandler, fileHandler},
	}

	cleanup = func() {
		_ = lj.Close()
	}

	return slog.New(multi), cleanup
}

// multiHandler fans out log records to multiple slog.Handlers.
type multiHandler struct {
	handlers []slog.Handler
}

func (m *multiHandler) Enabled(_ context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(nil, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error { //nolint:gocritic // slog.Handler interface requires value receiver
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: handlers}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: handlers}
}
