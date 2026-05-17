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
	standaloneFlag := flag.Bool("standalone", false, "run in standalone mode (localhost bind + UI + Go trace parser)")
	dbPathFlag := flag.String("db-path", "", "SQLite DB path (standalone only, default: $HOME/.agent-standalone/agent.db)")
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
	if cfg.Standalone.Enabled {
		// trace/tracer.go:345 의 AGENT_PARSER 분기로 외부 tools/trace 바이너리 우회.
		os.Setenv("AGENT_PARSER", "go")
		slog.Info("standalone mode enabled — localhost bind, UI served, Go trace parser forced")
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
	mgr.StartRefreshLoop(ctx, 30*time.Second)

	orch := benchmark.NewOrchestrator(mgr, cfg.Server.ToolsDir)
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

	bindAddr := fmt.Sprintf(":%d", cfg.Server.Port)
	if cfg.Standalone.Enabled {
		// 외부 노출 차단 — 같은 네트워크의 다른 장비에서 접근 불가.
		bindAddr = fmt.Sprintf("127.0.0.1:%d", cfg.Server.Port)
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
	agentServer := server.NewDeviceAgentServer(mgr, orch, coll, traceMgr, minioClient, macroMgr)
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
		slog.Info("archive base", "path", archiveBase)
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
