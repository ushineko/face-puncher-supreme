/*
Package mitm implements per-domain TLS interception for the proxy.

It provides CA certificate generation, dynamic leaf certificate creation,
and an HTTP proxy loop that sits between a client TLS connection and an
upstream TLS connection.
*/
package mitm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"
)

// CA holds a loaded Certificate Authority certificate and private key.
type CA struct {
	Cert        *x509.Certificate
	Key         *ecdsa.PrivateKey
	CertPEM     []byte // Raw PEM bytes for serving at /fps/ca.pem
	Fingerprint string // SHA-256 fingerprint (hex-encoded, colon-separated)
	NotAfter    time.Time
}

// GenerateCA creates a new CA certificate and private key, writing them
// to certPath and keyPath as PEM files. Returns an error if either file
// already exists and force is false.
func GenerateCA(certPath, keyPath string, force bool) error {
	if !force {
		if _, err := os.Stat(certPath); err == nil {
			return fmt.Errorf("CA certificate already exists at %s (use --force to overwrite)", certPath)
		}
		if _, err := os.Stat(keyPath); err == nil {
			return fmt.Errorf("CA private key already exists at %s (use --force to overwrite)", keyPath)
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return fmt.Errorf("generate CA serial: %w", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "Face Puncher Supreme CA",
		},
		NotBefore:             now.Add(-1 * time.Hour), // backdated to avoid clock skew issues
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("create CA certificate: %w", err)
	}

	// Write certificate PEM.
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	writeErr := os.WriteFile(certPath, certPEM, 0644) //nolint:gosec // CA cert is public, not secret
	if writeErr != nil {
		return fmt.Errorf("write CA certificate: %w", writeErr)
	}

	// Write private key PEM with restricted permissions.
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshal CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("write CA key: %w", err)
	}

	return nil
}

// LoadCA reads a CA certificate and private key from PEM files.
func LoadCA(certPath, keyPath string) (*CA, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read CA certificate %s: %w", certPath, err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("CA certificate %s: invalid PEM (expected CERTIFICATE block)", certPath)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA certificate %s: %w", certPath, err)
	}

	if !cert.IsCA {
		return nil, fmt.Errorf("CA certificate %s: not a CA certificate (BasicConstraints CA flag not set)", certPath)
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read CA key %s: %w", keyPath, err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" {
		return nil, fmt.Errorf("CA key %s: invalid PEM (expected EC PRIVATE KEY block)", keyPath)
	}

	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CA key %s: %w", keyPath, err)
	}

	fingerprint := sha256Fingerprint(cert.Raw)

	return &CA{
		Cert:        cert,
		Key:         key,
		CertPEM:     certPEM,
		Fingerprint: fingerprint,
		NotAfter:    cert.NotAfter,
	}, nil
}

// sha256Fingerprint returns the SHA-256 fingerprint of DER-encoded certificate bytes.
func sha256Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	out := make([]byte, 0, len(sum)*3-1)
	for i, b := range sum {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, "0123456789abcdef"[b>>4], "0123456789abcdef"[b&0xf])
	}
	return string(out)
}

// randomSerial generates a random 128-bit serial number for certificates.
func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}
