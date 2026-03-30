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
	"syscall"
	"time"

	"agent/adb"
	"agent/benchmark"
	"agent/config"
	"agent/macro"
	"agent/monitor"
	pb "agent/pb"
	"agent/screen"
	"agent/server"
	"agent/storage"
	"agent/trace"

	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

func main() {
	configPath := flag.String("config", "config/devices.toml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	slog.Info("config loaded", "port", cfg.Server.Port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Discover connected devices
	mgr := adb.NewManager()
	slog.Info("scanning connected devices...")
	mgr.Refresh(ctx)
	mgr.StartRefreshLoop(ctx, 30*time.Second)

	orch := benchmark.NewOrchestrator(mgr, cfg.Server.ToolsDir)
	coll := monitor.NewCollector(mgr)
	traceMgr := trace.NewManager(mgr, cfg.Server.ToolsDir, cfg.Server.TraceDir, cfg.Server.TraceGrpcPort)
	if err := traceMgr.StartTraceServer(); err != nil {
		slog.Warn("failed to start trace gRPC server", "error", err)
	}
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

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Server.Port))
	if err != nil {
		slog.Error("failed to listen", "error", err)
		os.Exit(1)
	}

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

	// HTTP server for WebSocket screen streaming
	mux := http.NewServeMux()
	mux.Handle("/ws/screen/", screenHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	httpServer := &http.Server{Handler: mux}

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
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
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
		traceMgr.StopTraceServer()
		cancel()
	}()

	slog.Info("agent starting", "port", cfg.Server.Port, "services", "gRPC + WebSocket(screen)")
	if err := m.Serve(); err != nil {
		slog.Error("cmux serve error", "error", err)
		os.Exit(1)
	}
}
