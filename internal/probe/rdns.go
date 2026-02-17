package probe

import (
	"net"
	"strings"
	"sync"
	"time"
)

// ReverseDNS is a cached reverse DNS resolver. Entries are cached for a
// configurable TTL to avoid repeated lookups on every stats refresh.
type ReverseDNS struct {
	mu    sync.Mutex
	cache map[string]rdnsEntry
	ttl   time.Duration
}

type rdnsEntry struct {
	hostname  string
	expiresAt time.Time
}

// NewReverseDNS creates a resolver with the given cache TTL.
func NewReverseDNS(ttl time.Duration) *ReverseDNS {
	return &ReverseDNS{
		cache: make(map[string]rdnsEntry),
		ttl:   ttl,
	}
}

// Lookup returns the hostname for the given IP address, using cached results
// when available. Returns "" if reverse lookup fails or times out.
func (r *ReverseDNS) Lookup(ip string) string {
	r.mu.Lock()
	if entry, ok := r.cache[ip]; ok && time.Now().Before(entry.expiresAt) {
		r.mu.Unlock()
		return entry.hostname
	}
	r.mu.Unlock()

	// Do the lookup outside the lock.
	names, err := net.LookupAddr(ip)
	hostname := ""
	if err == nil && len(names) > 0 {
		// LookupAddr returns FQDNs with trailing dot — strip it.
		hostname = strings.TrimSuffix(names[0], ".")
	}

	r.mu.Lock()
	r.cache[ip] = rdnsEntry{hostname: hostname, expiresAt: time.Now().Add(r.ttl)}
	r.mu.Unlock()

	return hostname
}
