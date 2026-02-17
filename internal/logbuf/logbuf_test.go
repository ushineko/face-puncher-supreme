package logbuf

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufferAdd(t *testing.T) {
	buf := New(5)

	handler := buf.Handler()
	for i := 0; i < 3; i++ {
		err := handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0))
		require.NoError(t, err)
	}

	entries := buf.Recent(10, slog.LevelDebug)
	assert.Len(t, entries, 3)
}

func TestBufferWrap(t *testing.T) {
	buf := New(3)
	handler := buf.Handler()

	// Write 5 entries into a buffer of size 3 — oldest 2 should be dropped.
	for i := 0; i < 5; i++ {
		r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
		r.AddAttrs(slog.Int("i", i))
		require.NoError(t, handler.Handle(context.Background(), r))
	}

	entries := buf.Recent(10, slog.LevelDebug)
	assert.Len(t, entries, 3)
	// Should contain entries 2, 3, 4 (the 3 most recent).
	assert.Equal(t, int64(2), entries[0].Attrs["i"])
	assert.Equal(t, int64(3), entries[1].Attrs["i"])
	assert.Equal(t, int64(4), entries[2].Attrs["i"])
}

func TestBufferRecentLevelFilter(t *testing.T) {
	buf := New(10)
	handler := buf.Handler()

	levels := []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError, slog.LevelInfo}
	for _, lvl := range levels {
		require.NoError(t, handler.Handle(context.Background(), slog.NewRecord(time.Now(), lvl, "msg", 0)))
	}

	// All entries.
	all := buf.Recent(10, slog.LevelDebug)
	assert.Len(t, all, 5)

	// Only WARN and above.
	warn := buf.Recent(10, slog.LevelWarn)
	assert.Len(t, warn, 2)

	// Only ERROR.
	errs := buf.Recent(10, slog.LevelError)
	assert.Len(t, errs, 1)
}

func TestBufferRecentLimit(t *testing.T) {
	buf := New(10)
	handler := buf.Handler()

	for i := 0; i < 8; i++ {
		require.NoError(t, handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)))
	}

	// Request only 3.
	entries := buf.Recent(3, slog.LevelDebug)
	assert.Len(t, entries, 3)
}

func TestSubscriber(t *testing.T) {
	buf := New(10)
	sub := buf.Subscribe(slog.LevelInfo)
	defer buf.Unsubscribe(sub)

	handler := buf.Handler()

	// Write a DEBUG entry — subscriber at INFO should NOT receive it.
	require.NoError(t, handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelDebug, "debug", 0)))

	select {
	case <-sub.C:
		t.Fatal("subscriber should not receive DEBUG entry")
	case <-time.After(10 * time.Millisecond):
		// expected
	}

	// Write an INFO entry — subscriber should receive it.
	require.NoError(t, handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "info", 0)))

	select {
	case entry := <-sub.C:
		assert.Equal(t, "info", entry.Message)
		assert.Equal(t, "INFO", entry.Level)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("subscriber should receive INFO entry")
	}
}

func TestSubscriberSetMinLevel(t *testing.T) {
	buf := New(10)
	sub := buf.Subscribe(slog.LevelError)
	defer buf.Unsubscribe(sub)

	handler := buf.Handler()

	// Write WARN — subscriber at ERROR should NOT receive it.
	require.NoError(t, handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelWarn, "warn", 0)))
	select {
	case <-sub.C:
		t.Fatal("subscriber should not receive WARN at ERROR level")
	case <-time.After(10 * time.Millisecond):
	}

	// Lower to WARN — next WARN entry should arrive.
	sub.SetMinLevel(slog.LevelWarn)
	require.NoError(t, handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelWarn, "warn2", 0)))
	select {
	case entry := <-sub.C:
		assert.Equal(t, "warn2", entry.Message)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("subscriber should receive WARN after lowering min level")
	}
}

func TestResize(t *testing.T) {
	buf := New(5)
	handler := buf.Handler()

	for i := 0; i < 5; i++ {
		r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
		r.AddAttrs(slog.Int("i", i))
		require.NoError(t, handler.Handle(context.Background(), r))
	}

	assert.Len(t, buf.Recent(10, slog.LevelDebug), 5)

	// Shrink to 3 — should keep the 3 newest.
	buf.Resize(3)
	entries := buf.Recent(10, slog.LevelDebug)
	assert.Len(t, entries, 3)
	assert.Equal(t, int64(2), entries[0].Attrs["i"])

	// Grow to 10 — existing entries preserved.
	buf.Resize(10)
	entries = buf.Recent(10, slog.LevelDebug)
	assert.Len(t, entries, 3)
}

func TestHandlerWithAttrs(t *testing.T) {
	buf := New(10)
	handler := buf.Handler().WithAttrs([]slog.Attr{slog.String("component", "proxy")})

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
	r.AddAttrs(slog.String("key", "val"))
	require.NoError(t, handler.Handle(context.Background(), r))

	entries := buf.Recent(1, slog.LevelDebug)
	require.Len(t, entries, 1)
	assert.Equal(t, "proxy", entries[0].Attrs["component"])
	assert.Equal(t, "val", entries[0].Attrs["key"])
}

func TestHandlerWithGroup(t *testing.T) {
	buf := New(10)
	handler := buf.Handler().WithGroup("server")

	r := slog.NewRecord(time.Now(), slog.LevelInfo, "test", 0)
	r.AddAttrs(slog.String("addr", ":8080"))
	require.NoError(t, handler.Handle(context.Background(), r))

	entries := buf.Recent(1, slog.LevelDebug)
	require.Len(t, entries, 1)
	assert.Equal(t, ":8080", entries[0].Attrs["server.addr"])
}

func TestParseLevel(t *testing.T) {
	assert.Equal(t, slog.LevelDebug, ParseLevel("DEBUG"))
	assert.Equal(t, slog.LevelInfo, ParseLevel("INFO"))
	assert.Equal(t, slog.LevelWarn, ParseLevel("WARN"))
	assert.Equal(t, slog.LevelError, ParseLevel("ERROR"))
	assert.Equal(t, slog.LevelInfo, ParseLevel("UNKNOWN"))
}

func TestUnsubscribe(t *testing.T) {
	buf := New(10)
	sub := buf.Subscribe(slog.LevelDebug)
	buf.Unsubscribe(sub)

	handler := buf.Handler()
	require.NoError(t, handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)))

	// Subscriber should NOT receive entries after unsubscribe.
	select {
	case <-sub.C:
		t.Fatal("unsubscribed subscriber should not receive entries")
	case <-time.After(10 * time.Millisecond):
	}
}
