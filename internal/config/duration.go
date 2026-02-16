package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration with YAML marshal/unmarshal support.
// It accepts human-readable Go duration strings like "5s", "1m", "2m30s".
type Duration struct {
	time.Duration
}

// UnmarshalYAML parses a duration string from YAML.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("line %d: duration must be a string (e.g. \"5s\", \"1m\"): %w", value.Line, err)
	}

	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q: %w", value.Line, s, err)
	}

	d.Duration = parsed
	return nil
}

// MarshalYAML writes the duration as a human-readable string.
func (d Duration) MarshalYAML() (any, error) { //nolint:unparam // yaml.Marshaler interface requires error return
	return d.Duration.String(), nil
}
