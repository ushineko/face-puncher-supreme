package structure_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var staticPkgver = regexp.MustCompile(`(?m)^pkgver=(.*)$`)

// pkgverFor maps the version of record to a pacman pkgver, which may not
// contain '-' (1.5.4-rc.1 -> 1.5.4_rc.1). PKGBUILD's pkgver() does the same.
func pkgverFor(version string) string {
	return strings.ReplaceAll(version, "-", "_")
}

// pkgbuildMismatch returns a description of the disagreement between the
// VERSION file and PKGBUILD's static pkgver= line, or "" when they agree.
// The static line is what .SRCINFO and the AUR listing show; a missed bump
// once published a stale version.
func pkgbuildMismatch(version, pkgbuild string) string {
	m := staticPkgver.FindStringSubmatch(pkgbuild)
	if m == nil {
		return "PKGBUILD has no static pkgver= line"
	}
	want := pkgverFor(strings.TrimSpace(version))
	if got := strings.TrimSpace(m[1]); got != want {
		return "PKGBUILD pkgver=" + got + ", VERSION implies " + want
	}
	return ""
}

func TestPKGBUILDVersionMatchesVERSION(t *testing.T) {
	root := filepath.Join("..", "..")
	version, err := os.ReadFile(filepath.Join(root, "VERSION"))
	require.NoError(t, err)
	pkgbuild, err := os.ReadFile(filepath.Join(root, "PKGBUILD"))
	require.NoError(t, err)

	assert.Empty(t, pkgbuildMismatch(string(version), string(pkgbuild)))
}

func TestPKGBUILDVersionMismatchIsFound(t *testing.T) {
	tests := []struct {
		name, version, pkgbuild string
		wantMismatch            bool
	}{
		{"equal", "1.5.3\n", "pkgname=x\npkgver=1.5.3\n", false},
		{"pre-release mapped", "1.5.4-rc.1", "pkgver=1.5.4_rc.1", false},
		{"stale bump", "1.5.4", "pkgver=1.5.3", true},
		{"hyphen kept", "1.5.4-rc.1", "pkgver=1.5.4-rc.1", true},
		{"no static line", "1.5.4", "pkgver() {\n}\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pkgbuildMismatch(tt.version, tt.pkgbuild)
			if tt.wantMismatch {
				assert.NotEmpty(t, got)
			} else {
				assert.Empty(t, got)
			}
		})
	}
}
