package blocklist

import (
	"bufio"
	"io"
	"strings"
)

// ParseDomains reads a blocklist in hosts or adblock format and returns
// unique, lowercased domains. Comments (#, !) and blank lines are skipped.
// Supported formats:
//   - Hosts: "0.0.0.0 ad.example.com" or "127.0.0.1 ad.example.com"
//   - Adblock: "||ad.example.com^"
//   - Domain-only: "ad.example.com"
func ParseDomains(r io.Reader) []string {
	seen := make(map[string]struct{})
	var domains []string

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Skip comments.
		if line[0] == '#' || line[0] == '!' {
			continue
		}

		domain := parseLine(line)
		if domain == "" {
			continue
		}

		domain = strings.ToLower(domain)

		// Skip localhost entries that appear in hosts files.
		if domain == "localhost" || domain == "localhost.localdomain" ||
			domain == "local" || domain == "broadcasthost" ||
			domain == "ip6-localhost" || domain == "ip6-loopback" ||
			domain == "ip6-localnet" || domain == "ip6-mcastprefix" ||
			domain == "ip6-allnodes" || domain == "ip6-allrouters" ||
			domain == "ip6-allhosts" {
			continue
		}

		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		domains = append(domains, domain)
	}

	return domains
}

// parseLine extracts a domain from a single blocklist line.
func parseLine(line string) string {
	// Adblock format: ||domain^
	if strings.HasPrefix(line, "||") {
		domain := strings.TrimPrefix(line, "||")
		domain = strings.TrimSuffix(domain, "^")
		// Some adblock lines have additional modifiers after ^
		if idx := strings.IndexByte(domain, '^'); idx >= 0 {
			domain = domain[:idx]
		}
		return cleanDomain(domain)
	}

	// Hosts format: "0.0.0.0 domain" or "127.0.0.1 domain"
	fields := strings.Fields(line)
	if len(fields) >= 2 && isHostsIP(fields[0]) {
		// Take the domain (second field), strip inline comments.
		domain := fields[1]
		return cleanDomain(domain)
	}

	// Domain-only format: bare domain (one field, looks like a domain).
	if len(fields) == 1 && looksLikeDomain(fields[0]) {
		return cleanDomain(fields[0])
	}

	return ""
}

// isHostsIP returns true if s is a sinkhole IP used in hosts files.
func isHostsIP(s string) bool {
	return s == "0.0.0.0" || s == "127.0.0.1" || s == "::1" || s == "::0" || s == "::"
}

// looksLikeDomain does a minimal check: contains a dot, no spaces, no special chars.
func looksLikeDomain(s string) bool {
	if !strings.Contains(s, ".") {
		return false
	}
	for _, c := range s {
		if c == ' ' || c == '\t' || c == '/' || c == ':' {
			return false
		}
	}
	return true
}

// cleanDomain strips trailing dots and inline comments from a domain string.
func cleanDomain(s string) string {
	// Strip inline comment (some lists have "domain #comment").
	if idx := strings.IndexByte(s, '#'); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".")
	if s == "" || !looksLikeDomain(s) {
		return ""
	}
	return s
}
