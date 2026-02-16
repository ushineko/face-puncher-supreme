package blocklist_test

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ushineko/face-puncher-supreme/internal/blocklist"
)

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// --- Parser tests ---

func TestParseDomains_HostsFormat(t *testing.T) {
	input := `# Comment line
127.0.0.1 localhost
0.0.0.0 ad.example.com
0.0.0.0 tracker.example.org
127.0.0.1 ads.foo.net
`
	domains := blocklist.ParseDomains(strings.NewReader(input))
	assert.Equal(t, []string{"ad.example.com", "tracker.example.org", "ads.foo.net"}, domains)
}

func TestParseDomains_AdblockFormat(t *testing.T) {
	input := `! Adblock list comment
||ad.example.com^
||tracker.example.org^
||analytics.site.io^
`
	domains := blocklist.ParseDomains(strings.NewReader(input))
	assert.Equal(t, []string{"ad.example.com", "tracker.example.org", "analytics.site.io"}, domains)
}

func TestParseDomains_DomainOnlyFormat(t *testing.T) {
	input := `ad.example.com
tracker.example.org
`
	domains := blocklist.ParseDomains(strings.NewReader(input))
	assert.Equal(t, []string{"ad.example.com", "tracker.example.org"}, domains)
}

func TestParseDomains_MixedFormat(t *testing.T) {
	input := `# Mixed list
0.0.0.0 ad.example.com
||tracker.example.org^
bare.domain.net
! adblock comment
`
	domains := blocklist.ParseDomains(strings.NewReader(input))
	assert.Equal(t, []string{"ad.example.com", "tracker.example.org", "bare.domain.net"}, domains)
}

func TestParseDomains_Deduplication(t *testing.T) {
	input := `0.0.0.0 ad.example.com
0.0.0.0 ad.example.com
||ad.example.com^
`
	domains := blocklist.ParseDomains(strings.NewReader(input))
	assert.Equal(t, []string{"ad.example.com"}, domains)
}

func TestParseDomains_CaseInsensitive(t *testing.T) {
	input := `0.0.0.0 AD.Example.COM
0.0.0.0 ad.example.com
`
	domains := blocklist.ParseDomains(strings.NewReader(input))
	assert.Equal(t, []string{"ad.example.com"}, domains)
}

func TestParseDomains_SkipsLocalhostEntries(t *testing.T) {
	input := `127.0.0.1 localhost
127.0.0.1 localhost.localdomain
0.0.0.0 ip6-localhost
0.0.0.0 real-ad.example.com
`
	domains := blocklist.ParseDomains(strings.NewReader(input))
	assert.Equal(t, []string{"real-ad.example.com"}, domains)
}

func TestParseDomains_SkipsBlanksAndComments(t *testing.T) {
	input := `
# comment
! another comment

0.0.0.0 ad.example.com

`
	domains := blocklist.ParseDomains(strings.NewReader(input))
	assert.Equal(t, []string{"ad.example.com"}, domains)
}

func TestParseDomains_TrailingDots(t *testing.T) {
	input := `0.0.0.0 ad.example.com.
`
	domains := blocklist.ParseDomains(strings.NewReader(input))
	assert.Equal(t, []string{"ad.example.com"}, domains)
}

func TestParseDomains_InlineComments(t *testing.T) {
	input := `0.0.0.0 ad.example.com # this is an ad server
`
	domains := blocklist.ParseDomains(strings.NewReader(input))
	assert.Equal(t, []string{"ad.example.com"}, domains)
}

// --- DB tests ---

func TestDBOpenClose(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	assert.Equal(t, 0, db.Size())
	assert.NoError(t, db.Close())
}

func TestDBUpdate(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	fakeFetch := func(url string) ([]string, error) {
		return []string{"ad.example.com", "tracker.example.org"}, nil
	}

	err = db.Update([]string{"http://fake-list"}, blocklist.FetchFunc(fakeFetch))
	require.NoError(t, err)

	assert.Equal(t, 2, db.Size())
	assert.Equal(t, 1, db.SourceCount())
}

func TestDBIsBlocked(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	fakeFetch := func(url string) ([]string, error) {
		return []string{"ad.example.com", "tracker.example.org"}, nil
	}

	err = db.Update([]string{"http://fake-list"}, blocklist.FetchFunc(fakeFetch))
	require.NoError(t, err)

	assert.True(t, db.IsBlocked("ad.example.com"))
	assert.True(t, db.IsBlocked("AD.EXAMPLE.COM"))
	assert.True(t, db.IsBlocked("tracker.example.org"))
	assert.False(t, db.IsBlocked("safe.example.com"))
}

func TestDBBlockCounters(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	fakeFetch := func(url string) ([]string, error) {
		return []string{"ad.example.com", "tracker.example.org"}, nil
	}

	err = db.Update([]string{"http://fake-list"}, blocklist.FetchFunc(fakeFetch))
	require.NoError(t, err)

	// Hit ad.example.com 3 times, tracker 1 time.
	db.IsBlocked("ad.example.com")
	db.IsBlocked("ad.example.com")
	db.IsBlocked("ad.example.com")
	db.IsBlocked("tracker.example.org")
	db.IsBlocked("safe.example.com") // not blocked, shouldn't count

	assert.Equal(t, int64(4), db.BlocksTotal())

	top := db.TopBlocked(10)
	require.Len(t, top, 2)
	assert.Equal(t, "ad.example.com", top[0].Domain)
	assert.Equal(t, int64(3), top[0].Count)
	assert.Equal(t, "tracker.example.org", top[1].Domain)
	assert.Equal(t, int64(1), top[1].Count)
}

func TestDBTopBlockedLimit(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	fakeFetch := func(url string) ([]string, error) {
		return []string{"a.com", "b.com", "c.com"}, nil
	}

	err = db.Update([]string{"http://fake-list"}, blocklist.FetchFunc(fakeFetch))
	require.NoError(t, err)

	db.IsBlocked("a.com")
	db.IsBlocked("b.com")
	db.IsBlocked("c.com")

	top := db.TopBlocked(2)
	assert.Len(t, top, 2)
}

func TestDBUpdateRebuilds(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	// First update with 2 domains.
	err = db.Update([]string{"http://list1"}, blocklist.FetchFunc(func(url string) ([]string, error) {
		return []string{"old1.com", "old2.com"}, nil
	}))
	require.NoError(t, err)
	assert.Equal(t, 2, db.Size())
	assert.True(t, db.IsBlocked("old1.com"))

	// Second update replaces with different domains.
	err = db.Update([]string{"http://list2"}, blocklist.FetchFunc(func(url string) ([]string, error) {
		return []string{"new1.com"}, nil
	}))
	require.NoError(t, err)
	assert.Equal(t, 1, db.Size())
	assert.False(t, db.IsBlocked("old1.com"))
	assert.True(t, db.IsBlocked("new1.com"))
}

func TestDBMultipleSources(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	callCount := 0
	fakeFetch := func(url string) ([]string, error) {
		callCount++
		if callCount == 1 {
			return []string{"a.com", "b.com"}, nil
		}
		return []string{"b.com", "c.com"}, nil
	}

	err = db.Update([]string{"http://list1", "http://list2"}, blocklist.FetchFunc(fakeFetch))
	require.NoError(t, err)

	// b.com appears in both but should be deduplicated.
	assert.Equal(t, 3, db.Size())
	assert.Equal(t, 2, db.SourceCount())
}

func TestDBEmptyBlocklist(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	assert.Equal(t, 0, db.Size())
	assert.Equal(t, 0, db.SourceCount())
	assert.False(t, db.IsBlocked("anything.com"))
	assert.Equal(t, int64(0), db.BlocksTotal())
	assert.Empty(t, db.TopBlocked(10))
}

func TestDBHostStripPort(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	err = db.Update([]string{"http://list"}, blocklist.FetchFunc(func(url string) ([]string, error) {
		return []string{"ad.example.com"}, nil
	}))
	require.NoError(t, err)

	// IsBlocked takes just the domain, not host:port. The caller strips the port.
	assert.True(t, db.IsBlocked("ad.example.com"))
}

// --- Allowlist tests ---

func TestAllowlistExactMatch(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	err = db.Update([]string{"http://list"}, blocklist.FetchFunc(func(url string) ([]string, error) {
		return []string{"ad.example.com", "safe.example.com", "tracker.org"}, nil
	}))
	require.NoError(t, err)

	db.SetAllowlist([]string{"safe.example.com"})

	assert.True(t, db.IsBlocked("ad.example.com"))
	assert.False(t, db.IsBlocked("safe.example.com")) // allowlisted
	assert.True(t, db.IsBlocked("tracker.org"))
}

func TestAllowlistSuffixMatch(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	err = db.Update([]string{"http://list"}, blocklist.FetchFunc(func(url string) ([]string, error) {
		return []string{
			"registry.api.cnn.io",
			"cdn.cnn.io",
			"cnn.io",
			"ad.example.com",
		}, nil
	}))
	require.NoError(t, err)

	db.SetAllowlist([]string{"*.cnn.io"})

	assert.False(t, db.IsBlocked("registry.api.cnn.io")) // suffix match
	assert.False(t, db.IsBlocked("cdn.cnn.io"))           // suffix match
	assert.False(t, db.IsBlocked("cnn.io"))               // base domain match
	assert.True(t, db.IsBlocked("ad.example.com"))         // not allowlisted
}

func TestAllowlistCaseInsensitive(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	err = db.Update([]string{"http://list"}, blocklist.FetchFunc(func(url string) ([]string, error) {
		return []string{"safe.example.com"}, nil
	}))
	require.NoError(t, err)

	db.SetAllowlist([]string{"SAFE.Example.COM"})

	assert.False(t, db.IsBlocked("safe.example.com"))
	assert.False(t, db.IsBlocked("SAFE.EXAMPLE.COM"))
}

func TestAllowlistCounters(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	err = db.Update([]string{"http://list"}, blocklist.FetchFunc(func(url string) ([]string, error) {
		return []string{"safe.example.com", "ad.example.com"}, nil
	}))
	require.NoError(t, err)

	db.SetAllowlist([]string{"safe.example.com"})

	// Trigger allows and blocks.
	db.IsBlocked("safe.example.com") // allowed
	db.IsBlocked("safe.example.com") // allowed
	db.IsBlocked("safe.example.com") // allowed
	db.IsBlocked("ad.example.com")   // blocked

	assert.Equal(t, int64(3), db.AllowsTotal())
	assert.Equal(t, int64(1), db.BlocksTotal())

	top := db.TopAllowed(10)
	require.Len(t, top, 1)
	assert.Equal(t, "safe.example.com", top[0].Domain)
	assert.Equal(t, int64(3), top[0].Count)
}

func TestAllowlistSize(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	db.SetAllowlist([]string{"exact1.com", "exact2.com", "*.suffix.com"})
	assert.Equal(t, 3, db.AllowlistSize())

	db.SetAllowlist(nil)
	assert.Equal(t, 0, db.AllowlistSize())
}

func TestAllowlistNotInBlocklist(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	// Domain is in allowlist but not in blocklist — should not be blocked
	// and should NOT increment allow counters.
	db.SetAllowlist([]string{"safe.example.com"})

	assert.False(t, db.IsBlocked("safe.example.com"))
	assert.Equal(t, int64(0), db.AllowsTotal()) // no counter increment
}

func TestSnapshotAllowCounts(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	err = db.Update([]string{"http://list"}, blocklist.FetchFunc(func(url string) ([]string, error) {
		return []string{"a.com", "b.com"}, nil
	}))
	require.NoError(t, err)

	db.SetAllowlist([]string{"a.com", "b.com"})

	db.IsBlocked("a.com") // allowed
	db.IsBlocked("a.com") // allowed
	db.IsBlocked("b.com") // allowed

	snap := db.SnapshotAllowCounts()
	assert.Equal(t, int64(2), snap["a.com"])
	assert.Equal(t, int64(1), snap["b.com"])
}

// --- Inline blocklist tests ---

func TestAddInlineDomains(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	db.AddInlineDomains([]string{"news.iadsdk.apple.com", "news-events.apple.com"})

	assert.Equal(t, 2, db.Size())
	assert.True(t, db.IsBlocked("news.iadsdk.apple.com"))
	assert.True(t, db.IsBlocked("news-events.apple.com"))
	assert.False(t, db.IsBlocked("safe.example.com"))
}

func TestAddInlineDomainsWithURLDomains(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	// Load from URL source.
	err = db.Update([]string{"http://list"}, blocklist.FetchFunc(func(url string) ([]string, error) {
		return []string{"ad.example.com"}, nil
	}))
	require.NoError(t, err)

	// Add inline domains — these merge with URL-sourced domains.
	db.AddInlineDomains([]string{"news.iadsdk.apple.com"})

	assert.Equal(t, 2, db.Size())
	assert.True(t, db.IsBlocked("ad.example.com"))
	assert.True(t, db.IsBlocked("news.iadsdk.apple.com"))
}

func TestAddInlineDomainsCaseInsensitive(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	db.AddInlineDomains([]string{"NEWS.iAdsdk.Apple.COM"})

	assert.True(t, db.IsBlocked("news.iadsdk.apple.com"))
}

func TestAddInlineDomainsEmpty(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	db.AddInlineDomains(nil)
	db.AddInlineDomains([]string{})
	assert.Equal(t, 0, db.Size())
}

func TestInlineDomainsWithAllowlist(t *testing.T) {
	db, err := blocklist.Open(":memory:", discardLogger)
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	db.AddInlineDomains([]string{"ad.example.com", "safe.example.com"})
	db.SetAllowlist([]string{"safe.example.com"})

	assert.True(t, db.IsBlocked("ad.example.com"))
	assert.False(t, db.IsBlocked("safe.example.com")) // allowlist wins
}
