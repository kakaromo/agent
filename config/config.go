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
	Tools      ToolsConfig      `toml:"tools"`
}

// ToolsConfig — `tools_dir` 안의 실제 바이너리 파일명 매핑.
//
// 사용 사례: fio 3.36 을 받아서 `tools/fio-3.36` 으로 저장했다면 toml 에
// `[tools] fio = "fio-3.36"` 를 적으면 된다. 디바이스에는 같은 이름으로 push 된다
// (즉 remote 도 `/data/local/tmp/fio-3.36`). 비워두면 도구 기본명을 사용.
//
// 모든 필드는 bare 파일명 (`/`, `\`, `..` 거부) 만 허용. 절대경로/상대경로 트래버설은
// 매니저에서 검증.
type ToolsConfig struct {
	Fio     string `toml:"fio"`     // 기본 "fio"
	Iozone  string `toml:"iozone"`  // 기본 "iozone"
	Tiotest string `toml:"tiotest"` // 기본 "tiotest"
	Iotest  string `toml:"iotest"`  // 기본 "iotest"
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
	Port int `toml:"port"`
	// Bind 는 listen 호스트. 비우면 모드별 기본값 적용:
	//   - 사무실 모드: "0.0.0.0" (모든 인터페이스)
	//   - standalone:  "127.0.0.1" (로컬만)
	// LAN 공유 필요 시 "0.0.0.0" 또는 특정 IP("192.168.1.10") 지정.
	// 인증 스텁 환경(standalone)에서 LAN 공유는 신뢰된 사내망에서만 사용할 것.
	Bind     string `toml:"bind"`
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
	if cfg.Tools.Fio == "" {
		cfg.Tools.Fio = "fio"
	}
	if cfg.Tools.Iozone == "" {
		cfg.Tools.Iozone = "iozone"
	}
	if cfg.Tools.Tiotest == "" {
		cfg.Tools.Tiotest = "tiotest"
	}
	if cfg.Tools.Iotest == "" {
		cfg.Tools.Iotest = "iotest"
	}
	for _, p := range []struct {
		key, val string
	}{
		{"tools.fio", cfg.Tools.Fio},
		{"tools.iozone", cfg.Tools.Iozone},
		{"tools.tiotest", cfg.Tools.Tiotest},
		{"tools.iotest", cfg.Tools.Iotest},
	} {
		if err := validateToolFilename(p.val); err != nil {
			return nil, fmt.Errorf("%s: %w", p.key, err)
		}
	}
	return &cfg, nil
}

// validateToolFilename 은 path traversal 을 방지한다. tools/ 안의 bare 파일명만 허용.
func validateToolFilename(name string) error {
	if name == "" {
		return nil
	}
	if name == "." || name == ".." {
		return fmt.Errorf("invalid tool name %q", name)
	}
	for _, c := range name {
		if c == '/' || c == '\\' {
			return fmt.Errorf("tool name %q must be a bare filename (no path separators)", name)
		}
	}
	return nil
}
