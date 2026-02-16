package blocklist

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// FetchFunc downloads a blocklist URL and returns parsed domains.
// This is a function type to allow injection of test doubles.
type FetchFunc func(url string) ([]string, error)

// HTTPFetcher returns a FetchFunc that downloads blocklists via HTTP
// and parses domains from the response body.
//
// Only http:// and https:// URLs are accepted. The --blocklist-url flags
// are operator-controlled CLI input; do not expose to untrusted users.
func HTTPFetcher() FetchFunc {
	client := &http.Client{
		Timeout: 60 * time.Second,
	}

	return func(url string) ([]string, error) {
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return nil, fmt.Errorf("fetch %s: only http:// and https:// URLs are supported", url)
		}

		resp, err := client.Get(url) //nolint:gosec // URL comes from operator config, validated above
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", url, err)
		}
		defer resp.Body.Close() //nolint:errcheck // response body close in defer

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
		}

		domains := ParseDomains(resp.Body)
		return domains, nil
	}
}
