package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"agent/adb"
	"agent/apkmgr"
	"agent/benchmark"
	"agent/config"
	"agent/macro"
	"agent/monitor"
	pb "agent/pb"
	"agent/schedule"
	"agent/screen"
	"agent/server"
	"agent/storage"
	"agent/storage/sqlitedb"
	"agent/trace"

	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

func main() {
	configPath := flag.String("config", "config/devices.toml", "path to config file")
	standaloneFlag := flag.Bool("standalone", false, "run in standalone mode (UI + Go trace parser + SQLite, 기본 127.0.0.1 바인딩)")
	dbPathFlag := flag.String("db-path", "", "SQLite DB path (standalone only, default: $HOME/.agent-standalone/agent.db)")
	bindFlag := flag.String("bind", "", "bind address (예: 0.0.0.0, 192.168.1.10). 비우면 모드별 기본값(사무실=0.0.0.0, standalone=127.0.0.1)")
	archiveBaseFlag := flag.String("archive-base", "", "archive 폴더 (벤치마크 결과 JSON / trace parquet 사본). standalone only. 비우면 [standalone] archive_base 또는 $HOME/.agent-standalone/archive")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	// CLI 플래그가 config 값을 override 한다.
	if *standaloneFlag {
		cfg.Standalone.Enabled = true
	}
	if *dbPathFlag != "" {
		cfg.Standalone.DBPath = *dbPathFlag
	}
	if *bindFlag != "" {
		cfg.Server.Bind = *bindFlag
	}
	if *archiveBaseFlag != "" {
		cfg.Standalone.ArchiveBase = *archiveBaseFlag
	}
	if cfg.Standalone.Enabled {
		// trace/tracer.go:345 의 AGENT_PARSER 분기로 외부 tools/trace 바이너리 우회.
		os.Setenv("AGENT_PARSER", "go")
		slog.Info("standalone mode enabled — UI served, Go trace parser forced (bind 기본 127.0.0.1, --bind 로 override)")
	}
	slog.Info("config loaded", "port", cfg.Server.Port, "standalone", cfg.Standalone.Enabled)

	// SQLite 영속화 — standalone 모드 시에만. 미설정이면 default path 사용.
	var sqliteDB *sqlitedb.DB
	if cfg.Standalone.Enabled {
		dbPath := cfg.Standalone.DBPath
		if dbPath == "" {
			dbPath = sqlitedb.DefaultPath()
		}
		sqliteDB, err = sqlitedb.Open(dbPath)
		if err != nil {
			slog.Error("sqlite open failed", "path", dbPath, "error", err)
			os.Exit(1)
		}
		defer sqliteDB.Close()
		slog.Info("sqlite opened", "path", dbPath)

		// 자기 자신 (localhost agent) 자동 등록 — portal UI 의 server 선택 UX 가 비어있지 않도록.
		seedID, err := sqliteDB.SeedLocalServer("localhost", cfg.Server.Port)
		if err != nil {
			slog.Warn("seed local server failed", "error", err)
		} else {
			slog.Info("local server seeded", "id", seedID, "host", "localhost", "port", cfg.Server.Port)
		}

		// 재시작 시 메모리에서 사라진 잡들의 DB state 정리.
		// running/queued/pushing_tools/collecting/reparsing → failed.
		// (ctx 는 아래에서 만들기 전이므로 Background 사용 — 짧은 단일 UPDATE 라 cancel 불필요)
		if n, err := sqliteDB.MarkStaleRunningAsFailed(context.Background(), "agent restarted before completion"); err != nil {
			slog.Warn("stale job cleanup failed", "error", err)
		} else if n > 0 {
			slog.Info("stale running jobs cleaned", "count", n)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Discover connected devices
	mgr := adb.NewManager()
	slog.Info("scanning connected devices...")
	mgr.Refresh(ctx)
	// standalone 은 USB 핫스왑이 잦으므로 3초 refresh (SSE push 가 그 위에서 react).
	// 사무실 모드는 portal Spring 의 동기 polling 패턴이라 30초 유지.
	refreshInterval := 30 * time.Second
	if cfg.Standalone.Enabled {
		refreshInterval = 3 * time.Second
	}
	mgr.StartRefreshLoop(ctx, refreshInterval)

	orch := benchmark.NewOrchestrator(mgr, cfg.Server.ToolsDir)
	// [tools] 매핑 — tools_dir 안의 실제 파일명 override (예: fio = "fio-3.36").
	orch.SetToolName(pb.BenchmarkTool_BENCHMARK_TOOL_FIO, cfg.Tools.Fio)
	orch.SetToolName(pb.BenchmarkTool_BENCHMARK_TOOL_IOZONE, cfg.Tools.Iozone)
	orch.SetToolName(pb.BenchmarkTool_BENCHMARK_TOOL_TIOTEST, cfg.Tools.Tiotest)
	orch.SetToolName(pb.BenchmarkTool_BENCHMARK_TOOL_IOTEST, cfg.Tools.Iotest)
	coll := monitor.NewCollector(mgr)
	traceMgr := trace.NewManager(mgr, cfg.Server.ToolsDir, cfg.Server.TraceDir)
	orch.SetTraceController(traceMgr)

	// MinIO client (optional)
	var minioClient *storage.MinioClient
	if cfg.Minio.Endpoint != "" {
		mc, err := storage.NewMinioClient(cfg.Minio)
		if err != nil {
			slog.Warn("minio client init failed", "error", err)
		} else {
			minioClient = mc
			slog.Info("minio connected", "endpoint", cfg.Minio.Endpoint, "bucket", cfg.Minio.Bucket)
		}
	}

	// Screen streaming (scrcpy)
	scrcpyMgr := screen.NewManager(cfg.Server.ToolsDir)
	screenHandler := screen.NewHandler(scrcpyMgr, mgr)

	// App Macro (recording, replay, OCR)
	macroMgr := macro.NewManager(mgr, scrcpyMgr)
	orch.SetMacroController(macroMgr)
	screenHandler.SetRecorder(macroMgr)

	// APK management (list/install/uninstall — uses <toolsDir>/apks)
	apkMgr := apkmgr.NewManager(mgr, cfg.Server.ToolsDir)
	orch.SetApkController(apkMgr)

	// bind host 결정 순서:
	//   1) --bind 플래그 또는 [server] bind 값이 있으면 그대로 사용
	//   2) standalone 기본: 127.0.0.1 (외부 노출 차단)
	//   3) 사무실 기본:    0.0.0.0  (모든 인터페이스)
	bindHost := cfg.Server.Bind
	if bindHost == "" {
		if cfg.Standalone.Enabled {
			bindHost = "127.0.0.1"
		} else {
			bindHost = "0.0.0.0"
		}
	}
	bindAddr := fmt.Sprintf("%s:%d", bindHost, cfg.Server.Port)
	if cfg.Standalone.Enabled && bindHost != "127.0.0.1" {
		// 인증 스텁 환경에서 LAN 노출은 사용자 책임 — 명시적 경고.
		slog.Warn("standalone 모드에서 외부 바인딩 사용 — 인증이 없으므로 신뢰된 네트워크에서만 사용할 것", "bind", bindHost)
	}
	lis, err := net.Listen("tcp", bindAddr)
	if err != nil {
		slog.Error("failed to listen", "error", err, "addr", bindAddr)
		os.Exit(1)
	}
	slog.Info("listening", "addr", lis.Addr().String())

	// Use cmux to serve gRPC and HTTP/WebSocket on the same port
	m := cmux.New(lis)
	grpcLis := m.MatchWithWriters(cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc"))
	httpLis := m.Match(cmux.Any())

	// gRPC server
	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(64*1024*1024),
		grpc.MaxSendMsgSize(64*1024*1024),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 5 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	agentServer := server.NewDeviceAgentServer(mgr, orch, coll, traceMgr, minioClient, macroMgr, apkMgr)
	pb.RegisterDeviceAgentServer(grpcServer, agentServer)
	reflection.Register(grpcServer)

	// HTTP server: REST + WebSocket + (옵션) UI 임베드. cmux 의 HTTP 분기에 마운트.
	routerOpts := server.HTTPRouterOptions{
		Agent:         agentServer,
		Manager:       mgr,
		ScreenHandler: screenHandler,
	}
	var scheduleRunner *schedule.Runner
	if cfg.Standalone.Enabled {
		routerOpts.UIFS = uiFS()
		routerOpts.EnableUI = true
		routerOpts.DB = sqliteDB
		// Cron 러너 시작 — agent gRPC 서버를 JobRunner 로 주입.
		scheduleRunner = schedule.New(sqliteDB, agentServer)
		scheduleRunner.Start(ctx)
		routerOpts.ScheduleRunner = scheduleRunner
		defer scheduleRunner.Stop()

		// Archive base 경로 — config 우선, 미설정 시 $HOME/.agent-standalone/archive
		archiveBase := cfg.Standalone.ArchiveBase
		if archiveBase == "" {
			if home, err := os.UserHomeDir(); err == nil && home != "" {
				archiveBase = filepath.Join(home, ".agent-standalone", "archive")
			} else {
				archiveBase = "agent-standalone-archive"
			}
		}
		routerOpts.ArchiveBase = archiveBase
		routerOpts.TraceBase = cfg.Server.TraceDir
		slog.Info("archive base", "path", archiveBase)
		slog.Info("trace base", "path", cfg.Server.TraceDir)
	}
	httpServer := &http.Server{Handler: server.NewHTTPRouter(routerOpts)}

	// Start servers
	go func() {
		if err := grpcServer.Serve(grpcLis); err != nil {
			slog.Error("grpc server error", "error", err)
		}
	}()
	go func() {
		if err := httpServer.Serve(httpLis); err != nil && err != http.ErrServerClosed {
			slog.Error("http server error", "error", err)
		}
	}()

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		slog.Info("shutting down...")

		done := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(done)
		}()

		select {
		case <-done:
			slog.Info("graceful shutdown completed")
		case <-time.After(5 * time.Second):
			slog.Warn("graceful shutdown timed out, forcing stop")
			grpcServer.Stop()
		}

		httpServer.Close()
		cancel()
	}()

	slog.Info("agent starting", "port", cfg.Server.Port, "services", "gRPC + WebSocket(screen)")
	if err := m.Serve(); err != nil {
		slog.Error("cmux serve error", "error", err)
		os.Exit(1)
	}
}
