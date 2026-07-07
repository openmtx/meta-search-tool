package config

import yaml "gopkg.in/yaml.v3"

// unmarshalYAML is the yaml.Unmarshal wrapper.
func unmarshalYAML(data []byte, v any) error {
	return yaml.Unmarshal(data, v)
}
