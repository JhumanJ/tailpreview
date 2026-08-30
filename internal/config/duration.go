package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a string such as 24h")
	}
	parsed, err := ParseDuration(node.Value)
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

func ParseDuration(value string) (time.Duration, error) {
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", value, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return parsed, nil
}
