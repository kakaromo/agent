package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agent/adb"
	"agent/benchmark"
	"agent/config"
	"agent/monitor"
	pb "agent/pb"
	"agent/server"
	"agent/storage"
	"agent/trace"

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

	// Discover connected devices via "adb devices"
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

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Server.Port))
	if err != nil {
		slog.Error("failed to listen", "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(64*1024*1024),
		grpc.MaxSendMsgSize(64*1024*1024),
		// Keepalive: server pings client every 30s, client must respond within 5s
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second, // ping client every 30s
			Timeout: 5 * time.Second,  // wait 5s for ping ack
		}),
		// Enforce client keepalive: allow ping every 10s minimum
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	agentServer := server.NewDeviceAgentServer(mgr, orch, coll, traceMgr, minioClient)
	pb.RegisterDeviceAgentServer(grpcServer, agentServer)
	reflection.Register(grpcServer)

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		slog.Info("shutting down, notifying clients...")

		// Give GracefulStop 5 seconds, then force stop
		// GracefulStop stops accepting new RPCs and waits for existing ones.
		// Streaming RPCs will receive context cancellation.
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
		traceMgr.StopTraceServer()
		cancel()
	}()

	slog.Info("gRPC server starting", "port", cfg.Server.Port)
	if err := grpcServer.Serve(lis); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
