package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server     ServerConfig     `toml:"server"`
	Minio      MinioConfig      `toml:"minio"`
	Standalone StandaloneConfig `toml:"standalone"`
}

// StandaloneConfig — 출장 시 노트북 단독 사용 모드.
// Enabled=true 시:
//   - 127.0.0.1 만 바인딩 (외부 노출 차단)
//   - UI 임베드(ui/build) 활성화, '/' 에서 Svelte SPA 서빙
//   - AGENT_PARSER=go 자동 설정 → tools/trace 외부 바이너리 미사용
//   - SQLite 영속화 활성화 (DBPath 미지정 시 $HOME/.agent-standalone/agent.db)
type StandaloneConfig struct {
	Enabled     bool   `toml:"enabled"`
	DBPath      string `toml:"db_path"`
	ArchiveBase string `toml:"archive_base"` // 비어있으면 $HOME/.agent-standalone/archive
}

type ServerConfig struct {
	Port     int    `toml:"port"`
	ToolsDir string `toml:"tools_dir"`
	TraceDir string `toml:"trace_dir"`
	// TraceGrpcPort 는 deprecated — 실시간 파싱 경로(50053 gRPC) 제거 후 사용되지 않는다.
	// toml 호환을 위해 필드는 남겨두지만 무시된다.
	TraceGrpcPort int `toml:"trace_grpc_port,omitempty"`
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
	if cfg.Minio.Bucket == "" {
		cfg.Minio.Bucket = "agent"
	}
	return &cfg, nil
}
