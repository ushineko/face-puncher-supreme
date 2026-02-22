package stats_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ushineko/face-puncher-supreme/internal/stats"
)

func TestCollector_RecordRequest(t *testing.T) {
	c := stats.NewCollector()

	c.RecordRequest("10.0.0.1", "example.com", false, 100, 5000)
	c.RecordRequest("10.0.0.1", "example.com", false, 200, 3000)
	c.RecordRequest("10.0.0.2", "ads.bad.com", true, 0, 0)

	assert.Equal(t, int64(3), c.TotalRequests())
	assert.Equal(t, int64(1), c.TotalBlocked())
	assert.Equal(t, int64(300), c.TotalBytesIn())
	assert.Equal(t, int64(8000), c.TotalBytesOut())
}

func TestCollector_RecordBytes(t *testing.T) {
	c := stats.NewCollector()

	// Record initial request (0 bytes for CONNECT).
	c.RecordRequest("10.0.0.1", "example.com", false, 0, 0)

	// Add bytes after tunnel close.
	c.RecordBytes("10.0.0.1", 1024, 2048)

	assert.Equal(t, int64(1024), c.TotalBytesIn())
	assert.Equal(t, int64(2048), c.TotalBytesOut())
}

func TestCollector_SnapshotClients(t *testing.T) {
	c := stats.NewCollector()
	c.RecordRequest("10.0.0.1", "a.com", false, 100, 200)
	c.RecordRequest("10.0.0.1", "b.com", true, 0, 0)
	c.RecordRequest("10.0.0.2", "a.com", false, 50, 100)

	snaps := c.SnapshotClients()
	assert.Len(t, snaps, 2)

	// Find 10.0.0.1.
	var found bool
	for _, s := range snaps {
		if s.IP != "10.0.0.1" {
			continue
		}
		found = true
		assert.Equal(t, int64(2), s.Requests)
		assert.Equal(t, int64(1), s.Blocked)
		assert.Equal(t, int64(100), s.BytesIn)
		assert.Equal(t, int64(200), s.BytesOut)
	}
	assert.True(t, found, "10.0.0.1 should be in snapshot")
}

func TestCollector_SnapshotDomainRequests(t *testing.T) {
	c := stats.NewCollector()
	c.RecordRequest("10.0.0.1", "a.com", false, 0, 0)
	c.RecordRequest("10.0.0.2", "a.com", false, 0, 0)
	c.RecordRequest("10.0.0.1", "b.com", false, 0, 0)

	snaps := c.SnapshotDomainRequests()
	assert.Len(t, snaps, 2)

	for _, s := range snaps {
		if s.Domain == "a.com" {
			assert.Equal(t, int64(2), s.Count)
		}
	}
}

func TestCollector_SnapshotDomainBlocks(t *testing.T) {
	c := stats.NewCollector()
	c.RecordRequest("10.0.0.1", "ads.com", true, 0, 0)
	c.RecordRequest("10.0.0.2", "ads.com", true, 0, 0)
	c.RecordRequest("10.0.0.1", "ok.com", false, 0, 0)

	snaps := c.SnapshotDomainBlocks()
	assert.Len(t, snaps, 1) // only ads.com was blocked
	assert.Equal(t, "ads.com", snaps[0].Domain)
	assert.Equal(t, int64(2), snaps[0].Count)
}

func TestCollector_Watermarks(t *testing.T) {
	c := stats.NewCollector()
	c.StartSampler()
	defer c.StopSampler()

	// Wait for the sampler to take its first baseline sample.
	time.Sleep(1500 * time.Millisecond)

	// Generate traffic AFTER the first tick so the next tick sees a delta.
	for i := 0; i < 100; i++ {
		c.RecordRequest("10.0.0.1", "example.com", false, 1024, 0)
	}

	// Wait for the sampler to compute the rate from the delta.
	time.Sleep(1500 * time.Millisecond)

	assert.Greater(t, c.PeakReqPerSec(), 0.0, "peak req/sec should be > 0 after traffic")
	assert.Greater(t, c.PeakBytesInSec(), int64(0), "peak bytes-in/sec should be > 0 after traffic")
}

func TestCollector_WatermarkMonotonic(t *testing.T) {
	c := stats.NewCollector()
	c.StartSampler()
	defer c.StopSampler()

	// Burst of traffic.
	for i := 0; i < 200; i++ {
		c.RecordRequest("10.0.0.1", "example.com", false, 2048, 0)
	}

	// Wait for sampler to capture the burst.
	time.Sleep(2500 * time.Millisecond)

	peakReq := c.PeakReqPerSec()
	peakBytes := c.PeakBytesInSec()

	// No more traffic — let sampler record zero-rate ticks.
	time.Sleep(2500 * time.Millisecond)

	assert.Equal(t, peakReq, c.PeakReqPerSec(), "peak req/sec should not decrease")
	assert.Equal(t, peakBytes, c.PeakBytesInSec(), "peak bytes-in/sec should not decrease")
}

func TestCollector_StopSamplerClean(t *testing.T) {
	c := stats.NewCollector()
	c.StartSampler()

	// StopSampler should return promptly without blocking.
	done := make(chan struct{})
	go func() {
		c.StopSampler()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(3 * time.Second):
		t.Fatal("StopSampler did not return within 3 seconds")
	}
}

func _openTestDB(t *testing.T) (*stats.DB, *stats.Collector) {
	t.Helper()
	collector := stats.NewCollector()
	logger := slog.Default()
	db, err := stats.Open(":memory:", collector, logger, time.Minute)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, collector
}

func TestDB_Flush(t *testing.T) {
	db, collector := _openTestDB(t)

	collector.RecordRequest("10.0.0.1", "example.com", false, 100, 500)
	collector.RecordRequest("10.0.0.1", "ads.com", true, 0, 0)
	collector.RecordRequest("10.0.0.2", "example.com", false, 50, 200)

	err := db.Flush()
	require.NoError(t, err)
}

func TestDB_TopBlocked(t *testing.T) {
	db, collector := _openTestDB(t)

	collector.RecordRequest("10.0.0.1", "ads1.com", true, 0, 0)
	collector.RecordRequest("10.0.0.1", "ads1.com", true, 0, 0)
	collector.RecordRequest("10.0.0.1", "ads2.com", true, 0, 0)

	require.NoError(t, db.Flush())

	top := db.TopBlocked(10)
	require.Len(t, top, 2)
	assert.Equal(t, "ads1.com", top[0].Domain)
	assert.Equal(t, int64(2), top[0].Count)
	assert.Equal(t, "ads2.com", top[1].Domain)
	assert.Equal(t, int64(1), top[1].Count)
}

func TestDB_TopRequested(t *testing.T) {
	db, collector := _openTestDB(t)

	collector.RecordRequest("10.0.0.1", "popular.com", false, 0, 0)
	collector.RecordRequest("10.0.0.2", "popular.com", false, 0, 0)
	collector.RecordRequest("10.0.0.1", "other.com", false, 0, 0)

	require.NoError(t, db.Flush())

	top := db.TopRequested(10)
	require.Len(t, top, 2)
	assert.Equal(t, "popular.com", top[0].Domain)
	assert.Equal(t, int64(2), top[0].Count)
}

func TestDB_TopClients(t *testing.T) {
	db, collector := _openTestDB(t)

	collector.RecordRequest("10.0.0.1", "a.com", false, 100, 500)
	collector.RecordRequest("10.0.0.1", "b.com", true, 0, 0)
	collector.RecordRequest("10.0.0.2", "a.com", false, 50, 200)

	require.NoError(t, db.Flush())

	top := db.TopClients(10)
	require.Len(t, top, 2)
	assert.Equal(t, "10.0.0.1", top[0].IP)
	assert.Equal(t, int64(2), top[0].Requests)
	assert.Equal(t, int64(1), top[0].Blocked)
}

func TestDB_MergedTopBlocked(t *testing.T) {
	db, collector := _openTestDB(t)

	// Flush some data to DB.
	collector.RecordRequest("10.0.0.1", "ads.com", true, 0, 0)
	require.NoError(t, db.Flush())

	// Add more data to in-memory (not yet flushed).
	collector.RecordRequest("10.0.0.1", "ads.com", true, 0, 0)
	collector.RecordRequest("10.0.0.1", "tracker.com", true, 0, 0)

	merged := db.MergedTopBlocked(10)
	require.NotEmpty(t, merged)

	// ads.com: DB(1) + unflushed delta(2-1=1) = 2 total.
	var adsCount int64
	for _, dc := range merged {
		if dc.Domain == "ads.com" {
			adsCount = dc.Count
		}
	}
	assert.Equal(t, int64(2), adsCount, "merged count should be DB + unflushed delta")
}

func TestDB_MergedTopClients(t *testing.T) {
	db, collector := _openTestDB(t)

	collector.RecordRequest("10.0.0.1", "a.com", false, 100, 500)
	require.NoError(t, db.Flush())

	// More requests in memory.
	collector.RecordRequest("10.0.0.1", "b.com", false, 200, 1000)
	collector.RecordRequest("10.0.0.2", "a.com", false, 50, 200)

	merged := db.MergedTopClients(10)
	require.NotEmpty(t, merged)

	// 10.0.0.1 should have more requests than 10.0.0.2.
	assert.Equal(t, "10.0.0.1", merged[0].IP)
}

func TestDB_FlushMultipleTimes(t *testing.T) {
	db, collector := _openTestDB(t)

	collector.RecordRequest("10.0.0.1", "a.com", false, 100, 500)
	require.NoError(t, db.Flush())

	// Second request + flush — should add delta, not cumulative.
	collector.RecordRequest("10.0.0.1", "a.com", false, 200, 300)
	require.NoError(t, db.Flush())

	top := db.TopRequested(10)
	require.Len(t, top, 1)
	assert.Equal(t, int64(2), top[0].Count, "DB should have exactly 2 requests")
}

func TestDB_FlushIdempotentWithoutNewData(t *testing.T) {
	db, collector := _openTestDB(t)

	collector.RecordRequest("10.0.0.1", "a.com", false, 100, 500)
	require.NoError(t, db.Flush())

	// Flush again with NO new data — DB should not change.
	require.NoError(t, db.Flush())
	require.NoError(t, db.Flush())

	top := db.TopRequested(10)
	require.Len(t, top, 1)
	assert.Equal(t, int64(1), top[0].Count, "repeated flush without new data should not change DB")

	reqs, _, bytesIn, bytesOut := db.TrafficTotalsSince(time.Now().Add(-24 * time.Hour))
	assert.Equal(t, int64(1), reqs)
	assert.Equal(t, int64(100), bytesIn)
	assert.Equal(t, int64(500), bytesOut)
}

func TestDB_TopBlockedLimit(t *testing.T) {
	db, collector := _openTestDB(t)

	for i := 0; i < 20; i++ {
		domain := "ads" + string(rune('a'+i)) + ".com"
		collector.RecordRequest("10.0.0.1", domain, true, 0, 0)
	}
	require.NoError(t, db.Flush())

	top := db.TopBlocked(5)
	assert.Len(t, top, 5, "should limit to 5 results")
}

func TestDB_TrafficTotalsSince(t *testing.T) {
	db, collector := _openTestDB(t)

	collector.RecordRequest("10.0.0.1", "a.com", false, 100, 500)
	collector.RecordRequest("10.0.0.1", "b.com", true, 0, 0)
	require.NoError(t, db.Flush())

	// Query with a "since" far in the past — should include everything.
	reqs, blocked, bytesIn, bytesOut := db.TrafficTotalsSince(time.Now().Add(-24 * time.Hour))
	assert.Equal(t, int64(2), reqs)
	assert.Equal(t, int64(1), blocked)
	assert.Equal(t, int64(100), bytesIn)
	assert.Equal(t, int64(500), bytesOut)

	// Query with a "since" in the future — should return zeros.
	reqs, blocked, bytesIn, bytesOut = db.TrafficTotalsSince(time.Now().Add(24 * time.Hour))
	assert.Equal(t, int64(0), reqs)
	assert.Equal(t, int64(0), blocked)
	assert.Equal(t, int64(0), bytesIn)
	assert.Equal(t, int64(0), bytesOut)
}
