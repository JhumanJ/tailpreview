package config

import (
	"fmt"

	"github.com/jhumanj/tailpreview/internal/model"
	"gopkg.in/yaml.v3"
)

type VerificationCheck struct {
	model.VerificationCheck
}

func (v *VerificationCheck) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		v.VerificationCheck = model.VerificationCheck{Path: node.Value, MinCode: 200, MaxCode: 399}
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("verification entry must be a path or mapping")
	}
	allowed := map[string]bool{"path": true, "min_code": true, "max_code": true, "follow_redirects": true}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if !allowed[key] {
			return fmt.Errorf("unknown verification field %q", key)
		}
		if key == "follow_redirects" && node.Content[i+1].Value != "same_origin" {
			return fmt.Errorf("follow_redirects must be same_origin")
		}
	}
	type raw model.VerificationCheck
	var value raw
	if err := node.Decode(&value); err != nil {
		return err
	}
	v.VerificationCheck = model.VerificationCheck(value)
	if v.Path == "" {
		return fmt.Errorf("verification path is required")
	}
	if v.MinCode == 0 {
		v.MinCode = 200
	}
	if v.MaxCode == 0 {
		v.MaxCode = 399
	}
	return nil
}
