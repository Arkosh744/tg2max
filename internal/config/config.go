package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	MaxToken     string        `yaml:"max_token"`
	RateLimitRPS float64       `yaml:"rate_limit_rps"`
	CursorFile   string        `yaml:"cursor_file"`
	Mappings     []ChatMapping `yaml:"mappings"`
}

type ChatMapping struct {
	Name         string `yaml:"name"`
	TGExportPath string `yaml:"tg_export_path"`
	MaxChatID    int64  `yaml:"max_chat_id"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.MaxToken == "" {
		cfg.MaxToken = os.Getenv("MAX_TOKEN")
	}
	if cfg.MaxToken == "" {
		return nil, fmt.Errorf("max_token is required (config or MAX_TOKEN env)")
	}

	if len(cfg.Mappings) == 0 {
		return nil, fmt.Errorf("at least one mapping is required")
	}

	if cfg.RateLimitRPS <= 0 {
		cfg.RateLimitRPS = 1
	}

	if cfg.CursorFile == "" {
		cfg.CursorFile = "cursor.json"
	}

	if err := validateMappings(cfg.Mappings); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func validateMappings(mappings []ChatMapping) error {
	seen := make(map[string]struct{}, len(mappings))

	for i, m := range mappings {
		if m.Name == "" {
			return fmt.Errorf("mapping[%d]: name is required", i)
		}

		if _, exists := seen[m.Name]; exists {
			return fmt.Errorf("mapping[%d]: duplicate name %q", i, m.Name)
		}
		seen[m.Name] = struct{}{}

		if m.TGExportPath == "" {
			return fmt.Errorf("mapping[%d] %q: tg_export_path is required", i, m.Name)
		}
		if _, err := os.Stat(m.TGExportPath); err != nil {
			return fmt.Errorf("mapping[%d] %q: tg_export_path %q: %w", i, m.Name, m.TGExportPath, err)
		}

		if m.MaxChatID <= 0 {
			return fmt.Errorf("mapping[%d] %q: max_chat_id must be positive", i, m.Name)
		}
	}

	return nil
}
