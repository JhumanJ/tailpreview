package config

import (
	"fmt"

	"github.com/jhumanj/tailpreview/internal/model"
	"gopkg.in/yaml.v3"
)

type HealthCheck struct {
	model.HealthCheck
}

func (h *HealthCheck) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		h.HealthCheck = model.HealthCheck{URL: node.Value, Required: true, MinCode: 200, MaxCode: 399}
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("health entry must be a URL or mapping")
	}
	allowed := map[string]bool{"url": true, "required": true, "min_code": true, "max_code": true, "insecure_skip_verify": true}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if !allowed[key] {
			return fmt.Errorf("unknown health field %q", key)
		}
	}
	type raw model.HealthCheck
	var value raw
	if err := node.Decode(&value); err != nil {
		return err
	}
	h.HealthCheck = model.HealthCheck(value)
	if h.URL == "" {
		return fmt.Errorf("health url is required")
	}
	return nil
}
