/*
Package relay provides bidirectional byte copying between two network
connections, used by the proxy and transparent listener tunnel paths.
*/
package relay

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
)

// closeWriter is implemented by connections that support half-closing the
// write side (e.g. *net.TCPConn, *tls.Conn).
type closeWriter interface {
	CloseWrite() error
}

// closeWrite half-closes the write side of c when supported, signalling EOF to
// the peer so the opposite copy direction can drain and finish. Connections
// without CloseWrite (e.g. net.Pipe) are left untouched.
func closeWrite(c net.Conn) {
	if cw, ok := c.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}

// Bidirectional copies bytes in both directions between client and upstream
// until both directions reach EOF or error, then returns the byte counts
// client→upstream (fromClient) and upstream→client (fromUpstream). Each
// direction half-closes its destination's write side on completion so the peer
// can drain. It blocks until both directions finish and does not close either
// connection — callers own connection lifetime (typically via defer).
func Bidirectional(client, upstream net.Conn) (fromClient, fromUpstream int64) {
	var sent, received atomic.Int64
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		n, _ := io.Copy(upstream, client) //nolint:errcheck // tunnel streaming
		sent.Store(n)
		closeWrite(upstream)
	}()

	go func() {
		defer wg.Done()
		n, _ := io.Copy(client, upstream) //nolint:errcheck // tunnel streaming
		received.Store(n)
		closeWrite(client)
	}()

	wg.Wait()
	return sent.Load(), received.Load()
}
