/*
Package transparent implements transparent proxy listeners for HTTP and HTTPS.

Transparent proxying accepts connections redirected by iptables REDIRECT
rules. Unlike the explicit proxy, clients do not know they are being proxied.
HTTP requests arrive with relative URIs (destination from Host header),
and HTTPS connections arrive as raw TLS ClientHello (destination from SNI).
*/
package transparent

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
)

// maxClientHelloSize is the maximum number of bytes we read when peeking
// at a TLS ClientHello. 16 KB is far larger than any realistic ClientHello.
const maxClientHelloSize = 16384

// errNotTLS is returned when the first byte is not a TLS handshake record.
var errNotTLS = errors.New("not a TLS handshake")

// errNoSNI is returned when the ClientHello contains no SNI extension.
var errNoSNI = errors.New("no SNI extension in ClientHello")

// peekClientHello reads the TLS ClientHello from conn without consuming it.
// It returns the SNI server name and the raw bytes that were read (for replay).
// If the record is not a TLS handshake or contains no SNI, it returns an error
// along with whatever bytes were read (so the caller can still tunnel them).
func peekClientHello(conn net.Conn) (serverName string, peeked []byte, err error) {
	// Read the TLS record header (5 bytes).
	header := make([]byte, 5)
	if _, readErr := io.ReadFull(conn, header); readErr != nil {
		return "", header, readErr
	}

	// Content type 0x16 = Handshake.
	if header[0] != 0x16 {
		return "", header, errNotTLS
	}

	// Record payload length.
	payloadLen := int(binary.BigEndian.Uint16(header[3:5]))
	if payloadLen <= 0 || payloadLen > maxClientHelloSize {
		return "", header, errors.New("TLS record length out of range")
	}

	payload := make([]byte, payloadLen)
	if _, readErr := io.ReadFull(conn, payload); readErr != nil {
		return "", append(header, payload...), readErr //nolint:gocritic // intentional concat
	}

	peeked = make([]byte, 0, len(header)+len(payload))
	peeked = append(peeked, header...)
	peeked = append(peeked, payload...)

	sni, err := extractSNI(payload)
	if err != nil {
		return "", peeked, err
	}

	return sni, peeked, nil
}

// extractSNI parses a TLS Handshake payload to find the SNI server_name extension.
func extractSNI(payload []byte) (string, error) {
	if len(payload) < 1 || payload[0] != 0x01 {
		return "", errors.New("not a ClientHello handshake message")
	}

	// Handshake message length (3 bytes).
	if len(payload) < 4 {
		return "", errors.New("ClientHello too short")
	}
	msgLen := int(payload[1])<<16 | int(payload[2])<<8 | int(payload[3])
	if len(payload) < 4+msgLen {
		return "", errors.New("ClientHello truncated")
	}

	msg := payload[4 : 4+msgLen]

	// Skip: client version (2) + random (32) = 34 bytes.
	if len(msg) < 34 {
		return "", errors.New("ClientHello too short for version+random")
	}
	pos := 34

	// Session ID (length-prefixed, 1 byte length).
	if pos >= len(msg) {
		return "", errors.New("ClientHello missing session ID")
	}
	sessionIDLen := int(msg[pos])
	pos++
	pos += sessionIDLen
	if pos > len(msg) {
		return "", errors.New("ClientHello session ID overflows")
	}

	// Cipher suites (length-prefixed, 2 byte length).
	if pos+2 > len(msg) {
		return "", errors.New("ClientHello missing cipher suites")
	}
	cipherSuitesLen := int(binary.BigEndian.Uint16(msg[pos : pos+2]))
	pos += 2 + cipherSuitesLen
	if pos > len(msg) {
		return "", errors.New("ClientHello cipher suites overflow")
	}

	// Compression methods (length-prefixed, 1 byte length).
	if pos >= len(msg) {
		return "", errors.New("ClientHello missing compression methods")
	}
	compMethodsLen := int(msg[pos])
	pos++
	pos += compMethodsLen
	if pos > len(msg) {
		return "", errors.New("ClientHello compression methods overflow")
	}

	// Extensions (length-prefixed, 2 byte length). Optional — may not exist.
	if pos+2 > len(msg) {
		return "", errNoSNI
	}
	extensionsLen := int(binary.BigEndian.Uint16(msg[pos : pos+2]))
	pos += 2
	if pos+extensionsLen > len(msg) {
		return "", errors.New("ClientHello extensions overflow")
	}

	extEnd := pos + extensionsLen
	for pos+4 <= extEnd { //nolint:gosec // bounds checked by loop condition
		extType := binary.BigEndian.Uint16(msg[pos : pos+2])   //nolint:gosec // bounds checked
		extLen := int(binary.BigEndian.Uint16(msg[pos+2 : pos+4])) //nolint:gosec // bounds checked
		pos += 4

		if pos+extLen > extEnd {
			break
		}

		// Extension type 0x0000 = server_name.
		if extType == 0x0000 {
			return parseSNIExtension(msg[pos : pos+extLen]) //nolint:gosec // bounds checked above
		}

		pos += extLen
	}

	return "", errNoSNI
}

// parseSNIExtension extracts the host_name from a server_name extension payload.
func parseSNIExtension(data []byte) (string, error) {
	if len(data) < 2 {
		return "", errors.New("SNI extension too short")
	}

	listLen := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+listLen {
		return "", errors.New("SNI name list truncated")
	}

	pos := 2
	end := 2 + listLen
	for pos+3 <= end { //nolint:gosec // bounds checked by loop condition
		nameType := data[pos]                                   //nolint:gosec // bounds checked
		nameLen := int(binary.BigEndian.Uint16(data[pos+1 : pos+3])) //nolint:gosec // bounds checked
		pos += 3

		if pos+nameLen > end {
			break
		}

		// Name type 0x00 = host_name.
		if nameType == 0x00 {
			return string(data[pos : pos+nameLen]), nil //nolint:gosec // bounds checked above
		}

		pos += nameLen
	}

	return "", errNoSNI
}
