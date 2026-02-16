package mitm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"sync"
	"time"
)

const (
	leafValidity     = 24 * time.Hour
	leafRenewBefore  = 1 * time.Hour // regenerate if less than this remaining
)

// cachedCert holds a leaf certificate and its expiry time.
type cachedCert struct {
	cert      *tls.Certificate
	expiresAt time.Time
}

// CertCache generates and caches per-domain leaf certificates signed by a CA.
type CertCache struct {
	ca    *CA
	mu    sync.RWMutex
	certs map[string]*cachedCert
}

// NewCertCache creates a certificate cache backed by the given CA.
func NewCertCache(ca *CA) *CertCache {
	return &CertCache{
		ca:    ca,
		certs: make(map[string]*cachedCert),
	}
}

// GetCert returns a TLS certificate for the given domain, generating and
// caching one if needed. Cached certs are reused until near expiry.
func (c *CertCache) GetCert(domain string) (*tls.Certificate, error) {
	c.mu.RLock()
	if entry, ok := c.certs[domain]; ok {
		if time.Until(entry.expiresAt) > leafRenewBefore {
			c.mu.RUnlock()
			return entry.cert, nil
		}
	}
	c.mu.RUnlock()

	// Generate a new leaf cert (write lock).
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check under write lock.
	if entry, ok := c.certs[domain]; ok {
		if time.Until(entry.expiresAt) > leafRenewBefore {
			return entry.cert, nil
		}
	}

	cert, expiresAt, err := c.generateLeaf(domain)
	if err != nil {
		return nil, err
	}

	c.certs[domain] = &cachedCert{cert: cert, expiresAt: expiresAt}
	return cert, nil
}

// generateLeaf creates a new leaf certificate for the given domain.
func (c *CertCache) generateLeaf(domain string) (*tls.Certificate, time.Time, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("generate leaf key for %s: %w", domain, err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("generate leaf serial for %s: %w", domain, err)
	}

	now := time.Now()
	notAfter := now.Add(leafValidity)

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: domain,
		},
		DNSNames:    []string{domain},
		NotBefore:   now.Add(-5 * time.Minute), // small backdate for clock skew
		NotAfter:    notAfter,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, c.ca.Cert, &key.PublicKey, c.ca.Key)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("create leaf certificate for %s: %w", domain, err)
	}

	leafCert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("parse leaf certificate for %s: %w", domain, err)
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
		Leaf:        leafCert,
	}

	return tlsCert, notAfter, nil
}
