package relay

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBidirectional_CopiesBothDirectionsAndCounts(t *testing.T) {
	clientA, clientB := net.Pipe()
	upstreamA, upstreamB := net.Pipe()

	type result struct{ fromClient, fromUpstream int64 }
	done := make(chan result, 1)
	go func() {
		fc, fu := Bidirectional(clientB, upstreamB)
		done <- result{fc, fu}
	}()

	// client → upstream
	go func() { _, _ = clientA.Write([]byte("ping")) }()
	buf := make([]byte, 4)
	_, err := io.ReadFull(upstreamA, buf)
	require.NoError(t, err)
	assert.Equal(t, "ping", string(buf))

	// upstream → client
	go func() { _, _ = upstreamA.Write([]byte("pongpong")) }()
	buf2 := make([]byte, 8)
	_, err = io.ReadFull(clientA, buf2)
	require.NoError(t, err)
	assert.Equal(t, "pongpong", string(buf2))

	// Closing both far ends signals EOF in each direction; Bidirectional returns.
	_ = clientA.Close()
	_ = upstreamA.Close()

	select {
	case res := <-done:
		assert.Equal(t, int64(4), res.fromClient, "client→upstream byte count")
		assert.Equal(t, int64(8), res.fromUpstream, "upstream→client byte count")
	case <-time.After(5 * time.Second):
		t.Fatal("Bidirectional did not return")
	}
}

func TestBidirectional_EmptyTunnel(t *testing.T) {
	clientA, clientB := net.Pipe()
	upstreamA, upstreamB := net.Pipe()

	done := make(chan struct{})
	go func() {
		fc, fu := Bidirectional(clientB, upstreamB)
		assert.Equal(t, int64(0), fc)
		assert.Equal(t, int64(0), fu)
		close(done)
	}()

	// Close both ends immediately without sending data.
	_ = clientA.Close()
	_ = upstreamA.Close()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Bidirectional did not return on empty tunnel")
	}
}
