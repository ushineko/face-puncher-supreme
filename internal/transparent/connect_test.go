package transparent

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connectBlocker is a test Blocker that blocks a fixed set of domains.
type connectBlocker struct {
	blocked map[string]bool
}

func (b *connectBlocker) IsBlocked(domain string) bool { return b.blocked[domain] }

// startEchoServer starts a loopback TCP listener that echoes received bytes
// back to the caller. It returns the listen address and a cleanup function.
func startEchoServer(t *testing.T) (addr string, cleanup func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}(c)
		}
	}()

	return ln.Addr().String(), func() {
		_ = ln.Close()
		wg.Wait()
	}
}

// readCONNECTReply reads the status line and headers of a tunnel reply,
// returning the status line. The returned reader retains any buffered tunnel
// bytes and must be used for subsequent reads.
func readCONNECTReply(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	statusLine, err := r.ReadString('\n')
	require.NoError(t, err)
	for {
		line, lineErr := r.ReadString('\n')
		require.NoError(t, lineErr)
		if strings.TrimRight(line, "\r\n") == "" {
			break
		}
	}
	return statusLine
}

func newTestListener(cfg *Config) *Listener {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return New(cfg)
}

func TestHandleConnect_AllowedTunnel(t *testing.T) {
	upstreamAddr, stop := startEchoServer(t)
	defer stop()

	var onRequestCalls int
	var tunnelSent, tunnelRecv int64
	var tunnelClosed sync.WaitGroup
	tunnelClosed.Add(1)

	l := newTestListener(&Config{
		ConnectTimeout: 2 * time.Second,
		OnRequest: func(_, _ string, blocked bool, _, _ int64) {
			require.False(t, blocked)
			onRequestCalls++
		},
		OnTunnelClose: func(_ string, bytesIn, bytesOut int64) {
			tunnelSent = bytesIn
			tunnelRecv = bytesOut
			tunnelClosed.Done()
		},
	})

	clientConn, serverConn := net.Pipe()
	go l.handleHTTP(serverConn)

	require.NoError(t, clientConn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err := fmt.Fprintf(clientConn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", upstreamAddr, upstreamAddr)
	require.NoError(t, err)

	r := bufio.NewReader(clientConn)
	status := readCONNECTReply(t, r)
	assert.Contains(t, status, "200 Connection Established")

	// Send a payload through the tunnel and read it echoed back.
	payload := []byte("hello through the tunnel")
	_, err = clientConn.Write(payload)
	require.NoError(t, err)

	got := make([]byte, len(payload))
	_, err = io.ReadFull(r, got)
	require.NoError(t, err)
	assert.Equal(t, payload, got)

	// Closing the client ends the tunnel; OnTunnelClose must fire with counts.
	_ = clientConn.Close()
	tunnelClosed.Wait()

	assert.Equal(t, 1, onRequestCalls)
	assert.Equal(t, int64(len(payload)), tunnelSent)
	assert.Equal(t, int64(len(payload)), tunnelRecv)
}

func TestHandleConnect_BlockedDomain(t *testing.T) {
	var onRequestBlocked bool
	var transparentBlockCalls int
	var blockDone sync.WaitGroup
	blockDone.Add(1)

	l := newTestListener(&Config{
		ConnectTimeout: 2 * time.Second,
		Blocker:        &connectBlocker{blocked: map[string]bool{"blocked.example.com": true}},
		OnRequest: func(_, _ string, blocked bool, _, _ int64) {
			onRequestBlocked = blocked
		},
		// OnTransparentBlock fires last in the blocked path; use it to
		// establish happens-before with the assertions below.
		OnTransparentBlock: func() {
			transparentBlockCalls++
			blockDone.Done()
		},
	})

	clientConn, serverConn := net.Pipe()
	go l.handleHTTP(serverConn)

	require.NoError(t, clientConn.SetDeadline(time.Now().Add(5*time.Second)))

	_, err := fmt.Fprint(clientConn, "CONNECT blocked.example.com:443 HTTP/1.1\r\nHost: blocked.example.com:443\r\n\r\n")
	require.NoError(t, err)

	r := bufio.NewReader(clientConn)
	status, err := r.ReadString('\n')
	require.NoError(t, err)
	assert.Contains(t, status, "403")

	blockDone.Wait()
	assert.True(t, onRequestBlocked, "OnRequest should report blocked=true")
	assert.Equal(t, 1, transparentBlockCalls)
}
