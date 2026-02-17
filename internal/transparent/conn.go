package transparent

import (
	"bytes"
	"io"
	"net"
)

// prefixConn wraps a net.Conn, prepending buffered bytes before reads
// from the underlying connection. Used to replay peeked TLS ClientHello
// bytes so the MITM handler or upstream server sees the complete stream.
type prefixConn struct {
	net.Conn
	reader io.Reader
}

// newPrefixConn wraps conn so that reads first return prefix bytes,
// then continue reading from the underlying connection.
func newPrefixConn(conn net.Conn, prefix []byte) net.Conn {
	return &prefixConn{
		Conn:   conn,
		reader: io.MultiReader(bytes.NewReader(prefix), conn),
	}
}

// Read satisfies io.Reader, draining prefix bytes first.
func (c *prefixConn) Read(b []byte) (int, error) {
	return c.reader.Read(b)
}
