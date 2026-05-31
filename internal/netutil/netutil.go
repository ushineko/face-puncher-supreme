/*
Package netutil provides small networking helpers shared across the proxy and
transparent listener packages.
*/
package netutil

import (
	"errors"
	"net"
)

// IsUnspecifiedDialError reports whether err is a dial failure whose target
// address is an unspecified IP (IPv4 0.0.0.0 or IPv6 ::). Such failures occur
// when the host's resolver returns an unspecified address for a domain it
// blocks; the dial is meaningless and the resulting refusal is benign noise
// rather than a genuine upstream error.
func IsUnspecifiedDialError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if tcpAddr, ok := opErr.Addr.(*net.TCPAddr); ok {
			return tcpAddr.IP.IsUnspecified()
		}
	}
	return false
}
