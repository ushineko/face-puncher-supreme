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
	// ExtraHandlers are additional slog.Handlers to include in the fan-out chain
	// (e.g., logbuf.Buffer.Handler() for the dashboard).
	ExtraHandlers []slog.Handler
}

// Result holds the outputs of logging Setup.
type Result struct {
	Logger  *slog.Logger
	Cleanup func()
	// LevelVar allows runtime log level changes (e.g., verbose toggle via reload).
	LevelVar *slog.LevelVar
}

// Setup creates a logger that writes to stderr and optionally to a rotated
// log file. Returns a Result with the logger, cleanup function, and LevelVar
// for runtime level changes.
func Setup(cfg Config) Result {
	levelVar := new(slog.LevelVar)
	if cfg.Verbose {
		levelVar.Set(slog.LevelDebug)
	} else {
		levelVar.Set(slog.LevelInfo)
	}

	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: levelVar,
	})

	handlers := []slog.Handler{stderrHandler}

	var cleanup func()
	if cfg.LogDir != "" {
		// Ensure log directory exists.
		if err := os.MkdirAll(cfg.LogDir, 0o750); err != nil { //nolint:gosec // log directory
			slog.New(stderrHandler).Warn("failed to create log directory, file logging disabled",
				"dir", cfg.LogDir,
				"error", err,
			)
		} else {
			lj := &lumberjack.Logger{
				Filename:   filepath.Join(cfg.LogDir, "fpsd.log"),
				MaxSize:    10, // MB per file
				MaxBackups: 3,  // keep 3 old files
				MaxAge:     7,  // days to retain
				Compress:   true,
			}

			fileHandler := slog.NewJSONHandler(lj, &slog.HandlerOptions{
				Level: levelVar,
			})
			handlers = append(handlers, fileHandler)
			cleanup = func() { _ = lj.Close() }
		}
	}

	// Add any extra handlers (e.g., logbuf for dashboard).
	handlers = append(handlers, cfg.ExtraHandlers...)

	if cleanup == nil {
		cleanup = func() {}
	}

	multi := &multiHandler{handlers: handlers}
	return Result{
		Logger:   slog.New(multi),
		Cleanup:  cleanup,
		LevelVar: levelVar,
	}
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
