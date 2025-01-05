package config

import (
	"os"

	"github.com/goccy/go-yaml"
)

type ConfigManager struct {
	Config *Config
	Check  bool
	path   string
}

func NewConfigManager(path string) (*ConfigManager, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return &ConfigManager{
		Config: cfg,
		path:   path,
	}, nil
}
