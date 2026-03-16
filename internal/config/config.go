package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	MaxToken     string        `yaml:"max_token"`
	RateLimitRPS int           `yaml:"rate_limit_rps"`
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
		return nil, fmt.Errorf("max_token is required")
	}
	if len(cfg.Mappings) == 0 {
		return nil, fmt.Errorf("at least one mapping is required")
	}
	if cfg.RateLimitRPS == 0 {
		cfg.RateLimitRPS = 25
	}
	if cfg.CursorFile == "" {
		cfg.CursorFile = "cursor.json"
	}

	return &cfg, nil
}
