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
	"agent/trace"

	"google.golang.org/grpc"
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
	traceMgr := trace.NewManager(mgr, cfg.Server.ToolsDir, cfg.Server.TraceDir)
	orch.SetTraceController(traceMgr)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Server.Port))
	if err != nil {
		slog.Error("failed to listen", "error", err)
		os.Exit(1)
	}

	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(64*1024*1024), // 64MB
		grpc.MaxSendMsgSize(64*1024*1024), // 64MB
	)
	agentServer := server.NewDeviceAgentServer(mgr, orch, coll, traceMgr)
	pb.RegisterDeviceAgentServer(grpcServer, agentServer)
	reflection.Register(grpcServer)

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		slog.Info("shutting down...")
		grpcServer.GracefulStop()
		cancel()
	}()

	slog.Info("gRPC server starting", "port", cfg.Server.Port)
	if err := grpcServer.Serve(lis); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
