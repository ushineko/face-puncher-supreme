package mitm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"

	utls "github.com/refraction-networking/utls"
)

// Upstream TLS ClientHello fingerprints selectable via mitm.upstream_tls.
const (
	// UpstreamTLSSafari sends a Safari-shaped ClientHello (uTLS preset).
	// Some origins (Reddit's edge, for logged-in app traffic) reject Go's
	// crypto/tls ClientHello outright.
	UpstreamTLSSafari = "safari"
	// UpstreamTLSGo uses Go's crypto/tls client (behavior up to v1.5.2).
	UpstreamTLSGo = "go"
)

// upstreamTLSConn is the subset of *tls.Conn / *utls.UConn the proxy uses.
type upstreamTLSConn interface {
	net.Conn
	HandshakeContext(ctx context.Context) error
}

// newUpstreamTLS wraps conn in a TLS client for domain using the given
// fingerprint. rootCAs nil means the system roots. The proxy loop speaks
// HTTP/1.1 upstream, so only http/1.1 is offered via ALPN.
func newUpstreamTLS(conn net.Conn, domain, fingerprint string, rootCAs *x509.CertPool) (upstreamTLSConn, error) {
	switch fingerprint {
	case UpstreamTLSGo:
		return tls.Client(conn, &tls.Config{
			ServerName: domain,
			NextProtos: []string{"http/1.1"},
			MinVersion: tls.VersionTLS12,
			RootCAs:    rootCAs,
		}), nil
	case UpstreamTLSSafari, "":
		return newSafariUConn(conn, domain, rootCAs)
	default:
		return nil, fmt.Errorf("unknown upstream TLS fingerprint %q", fingerprint)
	}
}

// newSafariUConn builds a uTLS client from the Safari preset with ALPN
// (and ALPS, if the preset carries it) narrowed to http/1.1.
func newSafariUConn(conn net.Conn, domain string, rootCAs *x509.CertPool) (*utls.UConn, error) {
	spec, err := utls.UTLSIdToSpec(utls.HelloSafari_Auto)
	if err != nil {
		return nil, fmt.Errorf("load safari ClientHello spec: %w", err)
	}
	for _, ext := range spec.Extensions {
		switch e := ext.(type) {
		case *utls.ALPNExtension:
			e.AlpnProtocols = []string{"http/1.1"}
		case *utls.ApplicationSettingsExtension:
			e.SupportedProtocols = []string{"http/1.1"}
		case *utls.ApplicationSettingsExtensionNew:
			e.SupportedProtocols = []string{"http/1.1"}
		}
	}

	uc := utls.UClient(conn, &utls.Config{ServerName: domain, RootCAs: rootCAs}, utls.HelloCustom)
	if err := uc.ApplyPreset(&spec); err != nil {
		return nil, fmt.Errorf("apply safari ClientHello spec: %w", err)
	}
	return uc, nil
}
