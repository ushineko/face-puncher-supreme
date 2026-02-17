package transparent

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildClientHello constructs a minimal TLS ClientHello with the given SNI.
func buildClientHello(sni string) []byte {
	// Build SNI extension.
	var sniExt []byte
	if sni != "" {
		nameBytes := []byte(sni)
		// server_name_list: type(1) + length(2) + name
		entry := []byte{0x00}                                                // host_name type
		entry = append(entry, byte(len(nameBytes)>>8), byte(len(nameBytes))) // name length
		entry = append(entry, nameBytes...)

		listLen := len(entry)
		sniData := []byte{byte(listLen >> 8), byte(listLen)} // list length
		sniData = append(sniData, entry...)

		// Extension header: type(2) + length(2) + data
		sniExt = []byte{0x00, 0x00} // server_name extension type
		sniExt = append(sniExt, byte(len(sniData)>>8), byte(len(sniData)))
		sniExt = append(sniExt, sniData...)
	}

	// Extensions block.
	var extensions []byte
	if len(sniExt) > 0 {
		extensions = make([]byte, 2+len(sniExt))
		binary.BigEndian.PutUint16(extensions[0:2], uint16(len(sniExt))) //nolint:gosec // test data, bounded
		copy(extensions[2:], sniExt)
	}

	// ClientHello body: version(2) + random(32) + sessionID(1) + cipherSuites(4) + compression(2) + extensions
	hello := []byte{
		0x03, 0x03, // TLS 1.2
	}
	hello = append(hello, make([]byte, 32)...)
	hello = append(hello,
		0x00,       // session ID length = 0
		0x00, 0x02, // cipher suites length = 2
		0x00, 0x2f, // TLS_RSA_WITH_AES_128_CBC_SHA
		0x01, 0x00, // compression methods: 1 method, null
	)
	hello = append(hello, extensions...)

	// Handshake message: type(1) + length(3) + body
	helloLen := len(hello)
	handshake := []byte{0x01, byte(helloLen >> 16), byte(helloLen >> 8), byte(helloLen)} // ClientHello
	handshake = append(handshake, hello...)

	// TLS record: type(1) + version(2) + length(2) + payload
	payloadLen := len(handshake)
	record := []byte{0x16, 0x03, 0x01, byte(payloadLen >> 8), byte(payloadLen)} // Handshake, TLS 1.0
	record = append(record, handshake...)

	return record
}

func TestExtractSNI_ValidClientHello(t *testing.T) {
	hello := buildClientHello("www.example.com")
	// Skip the 5-byte TLS record header to get the handshake payload.
	payload := hello[5:]
	sni, err := extractSNI(payload)
	require.NoError(t, err)
	assert.Equal(t, "www.example.com", sni)
}

func TestExtractSNI_NoSNI(t *testing.T) {
	hello := buildClientHello("")
	payload := hello[5:]
	sni, err := extractSNI(payload)
	assert.ErrorIs(t, err, errNoSNI)
	assert.Empty(t, sni)
}

func TestExtractSNI_NotClientHello(t *testing.T) {
	payload := []byte{0x02, 0x00, 0x00, 0x01, 0x00} // ServerHello type
	_, err := extractSNI(payload)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a ClientHello")
}

func TestExtractSNI_TruncatedPayload(t *testing.T) {
	_, err := extractSNI([]byte{0x01, 0x00})
	assert.Error(t, err)
}

func TestPeekClientHello(t *testing.T) {
	hello := buildClientHello("test.example.org")
	extraData := []byte("extra data after hello")

	// Create a pipe to simulate a network connection.
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close() //nolint:errcheck // test cleanup

	go func() {
		defer serverConn.Close() //nolint:errcheck // test cleanup
		_, _ = serverConn.Write(hello)
		_, _ = serverConn.Write(extraData)
	}()

	sni, peeked, err := peekClientHello(clientConn)
	require.NoError(t, err)
	assert.Equal(t, "test.example.org", sni)
	assert.Equal(t, hello, peeked)
}

func TestPeekClientHello_NotTLS(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close() //nolint:errcheck // test cleanup

	go func() {
		defer serverConn.Close() //nolint:errcheck // test cleanup
		_, _ = serverConn.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
	}()

	_, peeked, err := peekClientHello(clientConn)
	assert.ErrorIs(t, err, errNotTLS)
	assert.Len(t, peeked, 5) // Only the 5-byte header was read.
}

func TestPeekClientHello_NoSNI(t *testing.T) {
	hello := buildClientHello("")

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close() //nolint:errcheck // test cleanup

	go func() {
		defer serverConn.Close() //nolint:errcheck // test cleanup
		_, _ = serverConn.Write(hello)
	}()

	sni, peeked, err := peekClientHello(clientConn)
	assert.ErrorIs(t, err, errNoSNI)
	assert.Empty(t, sni)
	assert.Equal(t, hello, peeked)
}

func TestPrefixConn_Read(t *testing.T) {
	prefix := []byte("hello ")
	underlying := bytes.NewReader([]byte("world"))

	// Create a simple net.Conn wrapper for testing.
	clientConn, serverConn := net.Pipe()
	go func() {
		defer serverConn.Close() //nolint:errcheck // test cleanup
		_, _ = serverConn.Write([]byte("world"))
	}()
	defer clientConn.Close() //nolint:errcheck // test cleanup

	wrapped := newPrefixConn(clientConn, prefix)

	buf := make([]byte, 20)
	n, err := wrapped.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "hello ", string(buf[:n]))

	n, err = wrapped.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "world", string(buf[:n]))

	_ = underlying // suppress unused warning in this test
}

func TestPrefixConn_ReadAll(t *testing.T) {
	prefix := []byte("ABC")

	clientConn, serverConn := net.Pipe()
	go func() {
		defer serverConn.Close() //nolint:errcheck // test cleanup
		_, _ = serverConn.Write([]byte("DEF"))
	}()
	defer clientConn.Close() //nolint:errcheck // test cleanup

	wrapped := newPrefixConn(clientConn, prefix)
	all, err := io.ReadAll(wrapped)
	require.NoError(t, err)
	assert.Equal(t, "ABCDEF", string(all))
}

func TestPrefixConn_EmptyPrefix(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	go func() {
		defer serverConn.Close() //nolint:errcheck // test cleanup
		_, _ = serverConn.Write([]byte("data"))
	}()
	defer clientConn.Close() //nolint:errcheck // test cleanup

	wrapped := newPrefixConn(clientConn, []byte{})
	buf := make([]byte, 10)
	n, err := wrapped.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "data", string(buf[:n]))
}

func TestStripPort(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"192.168.1.1:8080", "192.168.1.1"},
		{"example.com:443", "example.com"},
		{"localhost", "localhost"},
		{"127.0.0.1:0", "127.0.0.1"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, stripPort(tt.input))
	}
}

func TestExtractSNI_RealTLSClientHello(t *testing.T) {
	// Generate a real TLS ClientHello using Go's TLS stack.
	clientConn, serverConn := net.Pipe()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer serverConn.Close() //nolint:errcheck // test cleanup
		// Read whatever the TLS client sends.
		buf := make([]byte, maxClientHelloSize)
		n, _ := serverConn.Read(buf)
		if n > 5 {
			// Verify we can extract SNI from a real ClientHello.
			payload := buf[5:n]
			sni, err := extractSNI(payload)
			assert.NoError(t, err)
			assert.Equal(t, "real.example.com", sni)
		}
	}()

	tlsConn := tls.Client(clientConn, &tls.Config{
		ServerName:         "real.example.com",
		InsecureSkipVerify: true, //nolint:gosec // test only
	})
	// The handshake will fail (no server TLS config), but the ClientHello is sent.
	go func() {
		_ = tlsConn.Handshake()
		_ = tlsConn.Close()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for TLS ClientHello")
	}
}
