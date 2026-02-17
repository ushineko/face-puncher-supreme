/*
Package logbuf provides a circular buffer that implements slog.Handler.

It stores the most recent N log entries in a ring buffer and notifies
registered subscribers when new entries arrive. Designed to be plugged
into the existing slog handler chain as an additional sink for the
dashboard live log viewer.
*/
package logbuf

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Entry is a single log entry stored in the buffer.
type Entry struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"msg"`
	Attrs     map[string]any `json:"attrs,omitempty"`
}

// Subscriber receives new log entries via a channel.
type Subscriber struct {
	C  chan Entry
	mu sync.Mutex
	// minLevel is the minimum level this subscriber cares about.
	minLevel slog.Level
}

// SetMinLevel changes the subscriber's minimum log level filter.
func (s *Subscriber) SetMinLevel(level slog.Level) {
	s.mu.Lock()
	s.minLevel = level
	s.mu.Unlock()
}

// MinLevel returns the subscriber's current minimum log level.
func (s *Subscriber) MinLevel() slog.Level {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.minLevel
}

// Buffer is a fixed-size circular buffer of log entries with subscriber fan-out.
type Buffer struct {
	mu          sync.Mutex
	entries     []Entry
	size        int
	pos         int // next write position
	count       int // total entries written (wraps are fine, only used for length calc)
	subscribers map[*Subscriber]struct{}
}

// New creates a new circular buffer with the given capacity.
func New(size int) *Buffer {
	if size <= 0 {
		size = 1000
	}
	return &Buffer{
		entries:     make([]Entry, size),
		size:        size,
		subscribers: make(map[*Subscriber]struct{}),
	}
}

// add stores an entry and fans out to subscribers. Must not hold lock on entry.
func (b *Buffer) add(entry Entry) {
	b.mu.Lock()
	b.entries[b.pos] = entry
	b.pos = (b.pos + 1) % b.size
	b.count++

	// Fan out to subscribers.
	entryLevel := ParseLevel(entry.Level)
	for s := range b.subscribers {
		s.mu.Lock()
		if entryLevel >= s.minLevel {
			select {
			case s.C <- entry:
			default:
				// Drop if subscriber is slow — never block the logger.
			}
		}
		s.mu.Unlock()
	}
	b.mu.Unlock()
}

// Recent returns the most recent n entries, filtered by minimum level.
func (b *Buffer) Recent(n int, minLevel slog.Level) []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()

	total := b.count
	if total > b.size {
		total = b.size
	}

	if n <= 0 || n > total {
		n = total
	}

	// Walk the ring in order, collecting entries that pass the level filter.
	result := make([]Entry, 0, n)
	start := (b.pos - total + b.size) % b.size
	for i := 0; i < total; i++ {
		e := b.entries[(start+i)%b.size]
		if ParseLevel(e.Level) >= minLevel {
			result = append(result, e)
		}
	}

	// Only keep the last n after filtering.
	if len(result) > n {
		result = result[len(result)-n:]
	}
	return result
}

// Subscribe creates a new subscriber that receives log entries at or above minLevel.
func (b *Buffer) Subscribe(minLevel slog.Level) *Subscriber {
	s := &Subscriber{
		C:        make(chan Entry, 256),
		minLevel: minLevel,
	}
	b.mu.Lock()
	b.subscribers[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Unsubscribe removes a subscriber and closes its channel.
func (b *Buffer) Unsubscribe(s *Subscriber) {
	b.mu.Lock()
	delete(b.subscribers, s)
	b.mu.Unlock()
}

// Resize changes the buffer capacity. Existing entries are preserved (newest kept).
func (b *Buffer) Resize(newSize int) {
	if newSize <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	old := b.snapshot()
	b.entries = make([]Entry, newSize)
	b.size = newSize
	b.pos = 0
	b.count = 0

	// Copy as many recent entries as fit.
	start := 0
	if len(old) > newSize {
		start = len(old) - newSize
	}
	for _, e := range old[start:] {
		b.entries[b.pos] = e
		b.pos = (b.pos + 1) % b.size
		b.count++
	}
}

// snapshot returns all entries in chronological order (must be called with lock held).
func (b *Buffer) snapshot() []Entry {
	total := b.count
	if total > b.size {
		total = b.size
	}
	result := make([]Entry, total)
	start := (b.pos - total + b.size) % b.size
	for i := 0; i < total; i++ {
		result[i] = b.entries[(start+i)%b.size]
	}
	return result
}

// Handler returns an slog.Handler that writes entries to this buffer.
func (b *Buffer) Handler() slog.Handler {
	return &bufHandler{buf: b}
}

// bufHandler implements slog.Handler, writing records to the Buffer.
type bufHandler struct {
	buf    *Buffer
	attrs  []slog.Attr
	groups []string
}

func (h *bufHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true // capture all levels
}

func (h *bufHandler) Handle(_ context.Context, r slog.Record) error { //nolint:gocritic // slog.Handler interface
	attrs := make(map[string]any)

	// Prefix for group nesting.
	prefix := ""
	if len(h.groups) > 0 {
		prefix = strings.Join(h.groups, ".") + "."
	}

	// Pre-attached attrs from With().
	for _, a := range h.attrs {
		attrs[prefix+a.Key] = a.Value.Any()
	}

	// Attrs from this specific log call.
	r.Attrs(func(a slog.Attr) bool {
		attrs[prefix+a.Key] = a.Value.Any()
		return true
	})

	entry := Entry{
		Timestamp: r.Time,
		Level:     r.Level.String(),
		Message:   r.Message,
		Attrs:     attrs,
	}

	h.buf.add(entry)
	return nil
}

func (h *bufHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)
	return &bufHandler{
		buf:    h.buf,
		attrs:  newAttrs,
		groups: h.groups,
	}
}

func (h *bufHandler) WithGroup(name string) slog.Handler {
	newGroups := make([]string, len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups[len(h.groups)] = name
	return &bufHandler{
		buf:    h.buf,
		attrs:  h.attrs,
		groups: newGroups,
	}
}

// ParseLevel converts a level string to slog.Level.
func ParseLevel(s string) slog.Level {
	switch s {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
