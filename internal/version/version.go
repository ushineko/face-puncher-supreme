/*
Package version holds build-time version information for fpsd.

Variables are injected at build time via ldflags:

	go build -ldflags "-X .../version.Version=0.1.0 -X .../version.Commit=abc1234 -X .../version.Date=2026-02-16T00:00:00Z"
*/
package version

import (
	"fmt"
	"runtime"
)

// These variables are set at build time via -ldflags.
var (
	// Version is the semantic version (e.g., "0.1.0").
	Version = "dev"
	// Commit is the git commit hash.
	Commit = "unknown"
	// Date is the build timestamp in ISO 8601 format.
	Date = "unknown"
)

// Full returns a human-readable version string.
func Full() string {
	return fmt.Sprintf("fpsd %s (commit: %s, built: %s, %s/%s)",
		Version, short(Commit), Date, runtime.GOOS, runtime.GOARCH)
}

// Short returns just the version number.
func Short() string {
	return Version
}

// short truncates a commit hash to 7 characters.
func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
