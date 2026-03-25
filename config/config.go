package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server ServerConfig `toml:"server"`
	Minio  MinioConfig  `toml:"minio"`
}

type ServerConfig struct {
	Port          int    `toml:"port"`
	ToolsDir      string `toml:"tools_dir"`
	TraceDir      string `toml:"trace_dir"`
	TraceGrpcPort int    `toml:"trace_grpc_port"`
}

type MinioConfig struct {
	Endpoint  string `toml:"endpoint"`
	AccessKey string `toml:"access_key"`
	SecretKey string `toml:"secret_key"`
	Bucket    string `toml:"bucket"`
	UseSSL    bool   `toml:"use_ssl"`
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
		home, _ := os.UserHomeDir()
		cfg.Server.TraceDir = filepath.Join(home, "agent_trace")
	}
	if cfg.Server.TraceGrpcPort == 0 {
		cfg.Server.TraceGrpcPort = 50053
	}
	if cfg.Minio.Bucket == "" {
		cfg.Minio.Bucket = "agent"
	}
	return &cfg, nil
}
