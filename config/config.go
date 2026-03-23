package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server ServerConfig `toml:"server"`
}

type ServerConfig struct {
	Port      int    `toml:"port"`
	ToolsDir  string `toml:"tools_dir"`
	TraceDir  string `toml:"trace_dir"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 50051
	}
	if cfg.Server.ToolsDir == "" {
		cfg.Server.ToolsDir = "./tools"
	}
	if cfg.Server.TraceDir == "" {
		cfg.Server.TraceDir = "/tmp/agent_trace"
	}
	return &cfg, nil
}
