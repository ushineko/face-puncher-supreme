package structure_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

var (
	pinnedByCommit = regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}$`)
	pinnedByDigest = regexp.MustCompile(`^docker://[^@\s]+@sha256:[0-9a-f]{64}$`)
)

// A local action runs from this repository's own .github, where this test
// reads it too.
var localAction = regexp.MustCompile(`^\./\.github/\S*$`)

// unpinnedActions says which `uses:` values and container images in a workflow
// can change without the workflow changing. It reads the YAML rather than its
// lines, so a comment says nothing and an anchor, a flow map or a quoted key is
// what it means. A key that is not a string is read too: GitHub turns one into
// a string, and a job named `true:` would otherwise hide the file. This guards
// against a mistake; `sha_pinning_required` is what enforces pins.
func unpinnedActions(src []byte) ([]string, error) {
	var bad []string
	checkUse := func(use string) {
		use = strings.TrimSpace(use)
		if (!localAction.MatchString(use) || strings.Contains(use, "..")) && !pinnedByCommit.MatchString(use) && !pinnedByDigest.MatchString(use) {
			bad = append(bad, use)
		}
	}
	checkImage := func(image string) {
		if !pinnedByDigest.MatchString("docker://" + strings.TrimSpace(image)) {
			bad = append(bad, image)
		}
	}
	var walk func(any)
	visit := func(key string, value any) {
		text, isText := value.(string)
		switch {
		case isText && strings.EqualFold(key, "uses"):
			checkUse(text)
		case isText && (strings.EqualFold(key, "image") || strings.EqualFold(key, "container")):
			checkImage(text)
		default:
			walk(value)
		}
	}
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for key, value := range v {
				visit(key, value)
			}
		case map[any]any:
			for key, value := range v {
				visit(fmt.Sprint(key), value)
			}
		case []any:
			for _, item := range v {
				walk(item)
			}
		}
	}
	decoder := yaml.NewDecoder(bytes.NewReader(src))
	for {
		var doc any
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return bad, nil
		}
		if err != nil {
			return nil, err
		}
		walk(doc)
	}
}

func TestEveryActionIsPinned(t *testing.T) {
	root := filepath.Join("..", "..", ".github")
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ext := filepath.Ext(path)
		inWorkflows := filepath.Base(filepath.Dir(path)) == "workflows"
		isAction := strings.TrimSuffix(d.Name(), ext) == "action"
		if (ext != ".yml" && ext != ".yaml") || (!inWorkflows && !isAction) {
			return nil
		}
		src, err := os.ReadFile(path) //nolint:gosec // G122: test walks this repository's own .github tree
		if err != nil {
			return err
		}
		bad, err := unpinnedActions(src)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		assert.Empty(t, bad, "%s: pin these by full commit SHA, with the version in a comment", path)
		checked++
		return nil
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, checked, 2, "the workflows were not found")
}

func TestAnUnpinnedActionIsFound(t *testing.T) {
	sha := strings.Repeat("0123456789", 4)
	digest := strings.Repeat("0123456789abcdef", 4)

	pinned := map[string]string{
		"a commit":              "- uses: a/b@" + sha + " # v1",
		"a quoted commit":       "- uses: 'a/b@" + sha + "'",
		"a local action":        "- uses: ./.github/actions/x",
		"a container by digest": "jobs:\n  x:\n    container: a/b@sha256:" + digest,
		"a service by digest":   "services:\n  x:\n    image: a/b@sha256:" + digest,
		"a reusable commit":     "- uses: a/b/.github/workflows/c.yml@" + sha,
		"a container digest":    "- uses: docker://a/b@sha256:" + digest,
	}
	for name, src := range pinned {
		t.Run(name, func(t *testing.T) {
			bad, err := unpinnedActions([]byte(src))
			require.NoError(t, err)
			assert.Empty(t, bad)
		})
	}

	unpinned := map[string]string{
		"a tag":                  "- uses: a/b@v1",
		"a commit in a comment":  "- uses: a/b@main # @" + sha,
		"local in a comment":     "- uses: a/b@main # uses: ./x",
		"a flow map":             "- { uses: a/b@main }",
		"a quoted key":           "- \"uses\": a/b@main",
		"a space before":         "- uses : a/b@main",
		"a block scalar":         "- uses: >-\n    a/b@main",
		"the next line":          "- uses:\n    a/b@main",
		"an anchor":              "x: &a a/b@main\ny:\n  - uses: *a",
		"a container tag":        "- uses: docker://a/b:latest",
		"no version":             "- uses: a/b",
		"a short commit":         "- uses: a/b@012345",
		"a reusable tag":         "jobs:\n  x:\n    uses: a/b/.github/workflows/c.yml@v1",
		"a job named true":       "jobs:\n  true:\n    uses: a/b@main",
		"a job named 1":          "jobs:\n  1:\n    uses: a/b@main",
		"a key in capitals":      "- USES: a/b@main",
		"a second document":      "a: b\n---\n- uses: a/b@main",
		"a local action outside": "- uses: ./tools/x",
		"a path up and out":      "- uses: ./../../evil",
		"a container's tag":      "jobs:\n  x:\n    container: a/b:latest",
		"a container map's tag":  "jobs:\n  x:\n    container:\n      image: a/b:latest",
		"a service's tag":        "services:\n  x:\n    image: a/b:latest",
	}
	for name, src := range unpinned {
		t.Run(name, func(t *testing.T) {
			bad, err := unpinnedActions([]byte(src))
			require.NoError(t, err)
			assert.NotEmpty(t, bad)
		})
	}
}
