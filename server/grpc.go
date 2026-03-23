package server

import (
	"context"
	"fmt"
	"strings"

	"agent/adb"
	"agent/benchmark"
	"agent/monitor"
	pb "agent/pb"
	"agent/trace"
)

// DeviceAgentServer implements the DeviceAgent gRPC service.
type DeviceAgentServer struct {
	pb.UnimplementedDeviceAgentServer
	manager      *adb.Manager
	orchestrator *benchmark.Orchestrator
	collector    *monitor.Collector
	traceMgr     *trace.Manager
}

func NewDeviceAgentServer(manager *adb.Manager, orchestrator *benchmark.Orchestrator, collector *monitor.Collector, traceMgr *trace.Manager) *DeviceAgentServer {
	return &DeviceAgentServer{
		manager:      manager,
		orchestrator: orchestrator,
		collector:    collector,
		traceMgr:     traceMgr,
	}
}

// ==================== Device Management ====================

func (s *DeviceAgentServer) ListDevices(ctx context.Context, req *pb.ListDevicesRequest) (*pb.ListDevicesResponse, error) {
	devices := s.manager.ListDevices()
	return &pb.ListDevicesResponse{Devices: devices}, nil
}

func (s *DeviceAgentServer) ConnectDevice(ctx context.Context, req *pb.ConnectDeviceRequest) (*pb.ConnectDeviceResponse, error) {
	if err := s.manager.ConnectDevice(ctx, req.Serial); err != nil {
		return &pb.ConnectDeviceResponse{Success: false, Message: err.Error()}, nil
	}
	return &pb.ConnectDeviceResponse{Success: true, Message: "connected"}, nil
}

func (s *DeviceAgentServer) DisconnectDevice(ctx context.Context, req *pb.DisconnectDeviceRequest) (*pb.DisconnectDeviceResponse, error) {
	if err := s.manager.DisconnectDevice(ctx, req.Serial); err != nil {
		return &pb.DisconnectDeviceResponse{Success: false}, nil
	}
	return &pb.DisconnectDeviceResponse{Success: true}, nil
}

// ==================== Benchmarking ====================

func (s *DeviceAgentServer) RunBenchmark(ctx context.Context, req *pb.RunBenchmarkRequest) (*pb.RunBenchmarkResponse, error) {
	jobID, err := s.orchestrator.RunBenchmark(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("run benchmark: %w", err)
	}
	return &pb.RunBenchmarkResponse{JobId: jobID}, nil
}

func (s *DeviceAgentServer) GetJobStatus(ctx context.Context, req *pb.GetJobStatusRequest) (*pb.GetJobStatusResponse, error) {
	resp, err := s.orchestrator.GetJobStatus(req.JobId)
	if err != nil {
		return nil, fmt.Errorf("get job status: %w", err)
	}
	return resp, nil
}

func (s *DeviceAgentServer) SubscribeJobProgress(req *pb.SubscribeJobProgressRequest, stream pb.DeviceAgent_SubscribeJobProgressServer) error {
	// Try benchmark orchestrator first, then trace manager
	ch, err := s.orchestrator.SubscribeJobProgress(req.JobId)
	if err != nil {
		ch, err = s.traceMgr.SubscribeProgress(req.JobId)
		if err != nil {
			return fmt.Errorf("subscribe job progress: %w", err)
		}
	}

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case progress, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(progress); err != nil {
				return err
			}
		}
	}
}

func (s *DeviceAgentServer) GetBenchmarkResult(ctx context.Context, req *pb.GetBenchmarkResultRequest) (*pb.GetBenchmarkResultResponse, error) {
	results, err := s.orchestrator.GetBenchmarkResults(req.JobId, req.DeviceId)
	if err != nil {
		return nil, fmt.Errorf("get benchmark result: %w", err)
	}
	return &pb.GetBenchmarkResultResponse{Results: results}, nil
}

func (s *DeviceAgentServer) DeleteJob(ctx context.Context, req *pb.DeleteJobRequest) (*pb.DeleteJobResponse, error) {
	// Try benchmark/scenario job first
	// Before deleting, extract trace job IDs from results and clean them up
	if results, err := s.orchestrator.GetBenchmarkResults(req.JobId, ""); err == nil {
		for _, r := range results {
			traceJobIDs := extractTraceJobIDs(r.RawOutput)
			for _, tid := range traceJobIDs {
				s.traceMgr.DeleteJob(tid)
			}
		}
	}
	if err := s.orchestrator.DeleteJob(req.JobId); err == nil {
		return &pb.DeleteJobResponse{Success: true, Message: "deleted"}, nil
	}
	// Try trace job directly
	if err := s.traceMgr.DeleteJob(req.JobId); err == nil {
		return &pb.DeleteJobResponse{Success: true, Message: "deleted"}, nil
	}
	return &pb.DeleteJobResponse{Success: false, Message: "job not found: " + req.JobId}, nil
}

// extractTraceJobIDs parses raw_output for TRACE_START/TRACE_STOP lines and returns job IDs.
func extractTraceJobIDs(rawOutput string) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, line := range strings.Split(rawOutput, "\n") {
		// Format: TRACE_START|loop=1|step=0|job_id=abc-123
		//         TRACE_STOP|loop=1|step=0|job_id=abc-123
		if !strings.HasPrefix(line, "TRACE_START|") && !strings.HasPrefix(line, "TRACE_STOP|") {
			continue
		}
		for _, part := range strings.Split(line, "|") {
			if strings.HasPrefix(part, "job_id=") {
				id := strings.TrimPrefix(part, "job_id=")
				id = strings.TrimSpace(id)
				if id != "" && !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
	}
	return ids
}

// ==================== Scenario ====================

func (s *DeviceAgentServer) RunScenario(ctx context.Context, req *pb.RunScenarioRequest) (*pb.RunScenarioResponse, error) {
	jobID, err := s.orchestrator.RunScenario(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("run scenario: %w", err)
	}
	return &pb.RunScenarioResponse{JobId: jobID}, nil
}

// ==================== Trace ====================

func (s *DeviceAgentServer) StartTrace(ctx context.Context, req *pb.StartTraceRequest) (*pb.StartTraceResponse, error) {
	jobID, err := s.traceMgr.StartTrace(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("start trace: %w", err)
	}
	return &pb.StartTraceResponse{JobId: jobID}, nil
}

func (s *DeviceAgentServer) StopTrace(ctx context.Context, req *pb.StopTraceRequest) (*pb.StopTraceResponse, error) {
	if err := s.traceMgr.StopTrace(req.JobId); err != nil {
		return &pb.StopTraceResponse{Success: false, Message: err.Error()}, nil
	}
	return &pb.StopTraceResponse{Success: true, Message: "trace stopped"}, nil
}

func (s *DeviceAgentServer) GetTraceResult(ctx context.Context, req *pb.GetTraceResultRequest) (*pb.GetTraceResultResponse, error) {
	dirs, err := s.collectParquetDirs(req.JobIds)
	if err != nil {
		return nil, err
	}
	stats, err := trace.ComputeStats(dirs, req.Filter, req.LatencyRangesMs)
	if err != nil {
		return nil, fmt.Errorf("compute stats: %w", err)
	}
	return &pb.GetTraceResultResponse{Stats: stats}, nil
}

func (s *DeviceAgentServer) GetTraceRawData(ctx context.Context, req *pb.GetTraceRawDataRequest) (*pb.GetTraceRawDataResponse, error) {
	dirs, err := s.collectParquetDirs(req.JobIds)
	if err != nil {
		return nil, err
	}
	resp, err := trace.GetRawData(dirs, req.Filter)
	if err != nil {
		return nil, fmt.Errorf("get raw data: %w", err)
	}
	return resp, nil
}

// collectParquetDirs resolves job IDs to parquet directories.
func (s *DeviceAgentServer) collectParquetDirs(jobIDs []string) ([]string, error) {
	if len(jobIDs) == 0 {
		return nil, fmt.Errorf("no job_ids provided")
	}
	var dirs []string
	for _, id := range jobIDs {
		dir, err := s.traceMgr.GetParquetDir(id)
		if err != nil {
			return nil, fmt.Errorf("job %s: %w", id, err)
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

// ==================== Monitoring ====================

func (s *DeviceAgentServer) MonitorDevices(req *pb.MonitorDevicesRequest, stream pb.DeviceAgent_MonitorDevicesServer) error {
	deviceIDs := req.DeviceIds
	if len(deviceIDs) == 0 {
		deviceIDs = s.manager.GetOnlineDevices()
	}
	if len(deviceIDs) == 0 {
		return fmt.Errorf("no online devices to monitor")
	}

	ctx := stream.Context()
	ch := make(chan *pb.DeviceMetrics, len(deviceIDs)*4)

	for _, id := range deviceIDs {
		go s.collector.StreamMetrics(ctx, id, req.IntervalSeconds, ch)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case metrics := <-ch:
			if err := stream.Send(metrics); err != nil {
				return err
			}
		}
	}
}
