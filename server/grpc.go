package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"

	"agent/adb"
	"agent/apkmgr"
	"agent/benchmark"
	"agent/macro"
	"agent/monitor"
	pb "agent/pb"
	"agent/storage"
	"agent/storage/sqlitedb"
	"agent/trace"
)

// DeviceAgentServer implements the DeviceAgent gRPC service.
type DeviceAgentServer struct {
	pb.UnimplementedDeviceAgentServer
	manager      *adb.Manager
	orchestrator *benchmark.Orchestrator
	collector    *monitor.Collector
	traceMgr     *trace.Manager
	minioClient  *storage.MinioClient
	macroMgr     *macro.Manager
	apkMgr       *apkmgr.Manager
	// db 는 standalone 에서만 주입된다(SetDB). 스케줄 자동 실행 시 app_macro step 의
	// macroId → events hydrate 에 필요. office 모드에선 nil.
	db *sqlitedb.DB
}

// SetDB — standalone 초기화 시 SQLite 핸들을 주입한다. 스케줄러의 scenario
// 자동 dispatch(app_macro hydrate) 경로에서 사용. office 모드에선 호출되지 않아 db=nil.
func (s *DeviceAgentServer) SetDB(db *sqlitedb.DB) {
	s.db = db
}

// RunScenarioFromScheduleConfig — schedule.Runner 가 호출하는 scenario 자동 실행 진입점.
// ScheduledJob.Config(JSON) 를 수동 경로(/scenario/run)와 동일한 변환으로 RunScenarioRequest 로
// 만든 뒤 실행한다. schedule 패키지는 server 를 import 할 수 없어(cycle) 이 메서드로 위임한다.
func (s *DeviceAgentServer) RunScenarioFromScheduleConfig(ctx context.Context, config string, deviceIDs []string, scenarioName, busyPolicy string) (string, error) {
	req, err := ScenarioRequestFromScheduleConfig(ctx, s.db, config, deviceIDs, scenarioName, busyPolicy)
	if err != nil {
		return "", err
	}
	resp, err := s.RunScenario(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.GetJobId(), nil
}

func NewDeviceAgentServer(manager *adb.Manager, orchestrator *benchmark.Orchestrator, collector *monitor.Collector, traceMgr *trace.Manager, minioClient *storage.MinioClient, macroMgr *macro.Manager, apkMgr *apkmgr.Manager) *DeviceAgentServer {
	return &DeviceAgentServer{
		manager:      manager,
		orchestrator: orchestrator,
		collector:    collector,
		traceMgr:     traceMgr,
		minioClient:  minioClient,
		macroMgr:     macroMgr,
		apkMgr:       apkMgr,
	}
}

// ApkManager exposes the APK manager for callers that need to dispatch install/uninstall
// from outside the gRPC layer (예: scenario orchestrator).
func (s *DeviceAgentServer) ApkManager() *apkmgr.Manager { return s.apkMgr }

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
	// Try benchmark/scenario job first
	resp, err := s.orchestrator.GetJobStatus(req.JobId)
	if err == nil {
		return resp, nil
	}
	// Try trace job
	traceJob, traceErr := s.traceMgr.GetJob(req.JobId)
	if traceErr != nil {
		return nil, fmt.Errorf("job not found: %s", req.JobId)
	}
	traceJob.Mu.Lock()
	state := traceJob.State
	deviceID := traceJob.DeviceID
	traceJob.Mu.Unlock()
	return &pb.GetJobStatusResponse{
		JobId:        req.JobId,
		State:        state,
		TotalDevices: 1,
		DeviceStatuses: []*pb.DeviceJobStatus{{
			DeviceId: deviceID,
			State:    state,
		}},
	}, nil
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

func (s *DeviceAgentServer) CancelJob(ctx context.Context, req *pb.CancelJobRequest) (*pb.CancelJobResponse, error) {
	// Try benchmark/scenario job
	if err := s.orchestrator.CancelJob(req.JobId); err == nil {
		return &pb.CancelJobResponse{Success: true, Message: "cancel requested"}, nil
	}
	// Try trace job
	if err := s.traceMgr.StopTrace(req.JobId); err == nil {
		return &pb.CancelJobResponse{Success: true, Message: "trace cancelled"}, nil
	}
	return &pb.CancelJobResponse{Success: false, Message: "job not found or not running: " + req.JobId}, nil
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
	infos, err := s.collectTraceJobInfos(req.JobIds)
	if err != nil {
		return nil, err
	}
	stats, err := trace.ComputeStats(infos, req.Filter, req.LatencyRangesMs)
	if err != nil {
		return nil, fmt.Errorf("compute stats: %w", err)
	}
	return &pb.GetTraceResultResponse{Stats: stats}, nil
}

func (s *DeviceAgentServer) GetTraceRawData(ctx context.Context, req *pb.GetTraceRawDataRequest) (*pb.GetTraceRawDataResponse, error) {
	infos, err := s.collectTraceJobInfos(req.JobIds)
	if err != nil {
		return nil, err
	}
	resp, err := trace.GetRawData(infos, req.Filter)
	if err != nil {
		return nil, fmt.Errorf("get raw data: %w", err)
	}
	return resp, nil
}

// GetIoAttribution — "이 IO 를 누가/무엇이 만들었나" 축별 집계 (fsio_* 전용).
//
// parquet 에 cross-layer 컬럼이 없는 trace_type(ftrace 계열) 은 에러가 아니라
// 응답의 unsupported_dims 로 알린다 — 클라이언트가 축을 골라 보낼 수 있어야 하므로
// "그 축은 못 한다" 가 정상 응답이다.
func (s *DeviceAgentServer) GetIoAttribution(ctx context.Context, req *pb.GetIoAttributionRequest) (*pb.GetIoAttributionResponse, error) {
	infos, err := s.collectTraceJobInfos(req.JobIds)
	if err != nil {
		return nil, err
	}
	resp, err := trace.ComputeAttribution(infos, req)
	if err != nil {
		return nil, fmt.Errorf("compute attribution: %w", err)
	}
	return resp, nil
}

// collectTraceJobInfos resolves job IDs to parquet directories and trace types.
//
// parquet-only 단일화 후 RUNNING/COLLECTING/REPARSING 동안에는 result_*.parquet 가
// 존재하지 않으므로 조회를 명시적으로 차단한다.
func (s *DeviceAgentServer) collectTraceJobInfos(jobIDs []string) ([]*trace.TraceJobInfo, error) {
	if len(jobIDs) == 0 {
		return nil, fmt.Errorf("no job_ids provided")
	}
	var infos []*trace.TraceJobInfo
	for _, id := range jobIDs {
		if job, err := s.traceMgr.GetJob(id); err == nil {
			job.Mu.Lock()
			state := job.State
			job.Mu.Unlock()
			switch state {
			case pb.JobState_JOB_STATE_RUNNING:
				return nil, fmt.Errorf("job %s is still collecting — stop trace first", id)
			case pb.JobState_JOB_STATE_COLLECTING:
				return nil, fmt.Errorf("job %s is parsing trace.log — wait for COMPLETED", id)
			case pb.JobState_JOB_STATE_REPARSING:
				return nil, fmt.Errorf("job %s is currently being reparsed", id)
			}
		}
		info, err := s.traceMgr.GetTraceJobInfo(id)
		if err != nil {
			return nil, fmt.Errorf("job %s: %w", id, err)
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// ==================== Upload to MinIO ====================

func (s *DeviceAgentServer) UploadTraceToMinio(ctx context.Context, req *pb.UploadTraceRequest) (*pb.UploadTraceResponse, error) {
	if s.minioClient == nil {
		return &pb.UploadTraceResponse{Success: false, Message: "minio not configured"}, nil
	}
	if len(req.JobIds) == 0 {
		return &pb.UploadTraceResponse{Success: false, Message: "no job_ids provided"}, nil
	}

	var allUploaded []string
	for _, jobID := range req.JobIds {
		info, err := s.traceMgr.GetTraceJobInfo(jobID)
		if err != nil {
			return &pb.UploadTraceResponse{Success: false, Message: fmt.Sprintf("job %s: %v", jobID, err)}, nil
		}
		remotePath := req.RemotePath + "/" + jobID
		uploaded, err := s.minioClient.UploadParquetFiles(ctx, info.Dir, remotePath)
		if err != nil {
			return &pb.UploadTraceResponse{Success: false, Message: fmt.Sprintf("upload %s: %v", jobID, err)}, nil
		}
		allUploaded = append(allUploaded, uploaded...)
	}

	return &pb.UploadTraceResponse{
		Success:       true,
		Message:       fmt.Sprintf("uploaded %d files", len(allUploaded)),
		UploadedFiles: allUploaded,
	}, nil
}

func (s *DeviceAgentServer) UploadBenchmarkToMinio(ctx context.Context, req *pb.UploadBenchmarkRequest) (*pb.UploadBenchmarkResponse, error) {
	if s.minioClient == nil {
		return &pb.UploadBenchmarkResponse{Success: false, Message: "minio not configured"}, nil
	}

	results, err := s.orchestrator.GetBenchmarkResults(req.JobId, "")
	if err != nil {
		return &pb.UploadBenchmarkResponse{Success: false, Message: err.Error()}, nil
	}

	var allUploaded []string
	for _, r := range results {
		// Upload result as JSON
		data, err := json.Marshal(r)
		if err != nil {
			continue
		}
		remotePath := fmt.Sprintf("%s/%s_result.json", req.RemotePath, r.DeviceId)
		if err := s.minioClient.UploadResultJSON(ctx, data, remotePath); err != nil {
			return &pb.UploadBenchmarkResponse{Success: false, Message: err.Error()}, nil
		}
		allUploaded = append(allUploaded, remotePath)
	}

	return &pb.UploadBenchmarkResponse{
		Success:       true,
		Message:       fmt.Sprintf("uploaded %d files", len(allUploaded)),
		UploadedFiles: allUploaded,
	}, nil
}

// ==================== Trace Archive — file info ====================

// GetArchiveFilesInfo — portal 이 init 단계에서 정확한 file size/count 를 알기 위한 메타 조회.
// 단방향 원칙 유지: portal 이 호출, agent 가 디스크 스캔만 수행.
func (s *DeviceAgentServer) GetArchiveFilesInfo(ctx context.Context, req *pb.GetArchiveFilesInfoRequest) (*pb.GetArchiveFilesInfoResponse, error) {
	if req.JobId == "" {
		return nil, fmt.Errorf("job_id is required")
	}
	rawPath, rawSize, parquetFiles, err := s.traceMgr.GetArchiveFiles(req.JobId)
	if err != nil {
		return nil, err
	}
	rawBase := rawPath
	if idx := strings.LastIndex(rawBase, "/"); idx >= 0 {
		rawBase = rawBase[idx+1:]
	}
	resp := &pb.GetArchiveFilesInfoResponse{
		JobId:        req.JobId,
		RawSize:      rawSize,
		RawLocalPath: rawBase,
	}
	for _, pf := range parquetFiles {
		resp.ParquetFiles = append(resp.ParquetFiles, &pb.ArchiveParquetInfo{
			LocalPath: pf.BaseName,
			TraceType: pf.TraceType,
			Size:      pf.Size,
		})
	}
	return resp, nil
}

// ==================== Trace Archive (presigned multipart) ====================

// archiveSender — server-streaming RPC 의 stream.Send 는 동시 호출 시 race / panic 가능.
// multipart 4-way 병렬 PUT goroutine 들이 progress 보고를 동시에 보내므로 mutex 로 직렬화.
type archiveSender struct {
	mu     sync.Mutex
	stream grpc.ServerStreamingServer[pb.UploadTraceArchiveProgress]
}

func (s *archiveSender) send(msg *pb.UploadTraceArchiveProgress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.stream.Send(msg)
}

// UploadTraceArchive — portal 이 발급한 presigned URL 로 trace.log + realtime parquet 을 nginx 경유 PUT.
// SDK 미사용 (legacy UploadTraceToMinio 와 다름) — 표준 HTTP PUT 만 사용해 endpoint 협상 우회.
//
// 흐름:
//  1. trace.log 검증 + 각 parquet 검증 (req.parquet_files 의 local_path 가 archive 후보와 일치하는지)
//  2. raw 먼저 PUT (단일 또는 multipart) → 각 part complete 마다 stream.Send
//  3. parquet 순차 PUT → 각 part complete 마다 stream.Send
//  4. 마지막에 finished=true 메시지
func (s *DeviceAgentServer) UploadTraceArchive(req *pb.UploadTraceArchiveRequest, stream grpc.ServerStreamingServer[pb.UploadTraceArchiveProgress]) error {
	if req.JobId == "" {
		return fmt.Errorf("job_id is required")
	}
	if req.Raw == nil {
		return fmt.Errorf("raw target is required")
	}
	ctx := stream.Context()
	sender := &archiveSender{stream: stream}

	// archive 파일 목록 조회 — agent 가 가진 실제 파일과 portal 의 presigned 매핑이 일치하는지 검증
	rawPath, _, parquetFiles, err := s.traceMgr.GetArchiveFiles(req.JobId)
	if err != nil {
		sender.send(&pb.UploadTraceArchiveProgress{
			JobId: req.JobId,
			Error: ptrString(fmt.Sprintf("archive files: %v", err)),
		})
		return err
	}

	// 총 바이트/파일 수 (진행률 표시용)
	bytesTotal := req.Raw.TotalBytes
	for _, pf := range req.ParquetFiles {
		bytesTotal += pf.TotalBytes
	}
	filesTotal := int32(1 + len(req.ParquetFiles))
	var bytesUploaded int64
	var filesDone int32

	// 1) raw trace.log 업로드
	if err := uploadOneTarget(ctx, sender, req.JobId, rawPath, req.Raw,
		&bytesUploaded, bytesTotal, &filesDone, filesTotal); err != nil {
		sender.send(&pb.UploadTraceArchiveProgress{
			JobId: req.JobId,
			Error: ptrString(fmt.Sprintf("raw upload: %v", err)),
		})
		return err
	}

	// 2) parquet 파일 업로드 — req.parquet_files 의 local_path 가 agent 의 archive 후보와 매칭돼야 함.
	knownByName := map[string]string{}
	for _, e := range parquetFiles {
		knownByName[e.BaseName] = e.LocalPath
	}
	for _, pf := range req.ParquetFiles {
		baseName := pf.LocalPath
		if idx := strings.LastIndex(baseName, "/"); idx >= 0 {
			baseName = baseName[idx+1:]
		}
		actualPath, ok := knownByName[baseName]
		if !ok {
			err := fmt.Errorf("parquet not found in archive: %s", baseName)
			sender.send(&pb.UploadTraceArchiveProgress{
				JobId: req.JobId,
				Error: ptrString(err.Error()),
			})
			return err
		}
		if err := uploadOneTarget(ctx, sender, req.JobId, actualPath, pf,
			&bytesUploaded, bytesTotal, &filesDone, filesTotal); err != nil {
			sender.send(&pb.UploadTraceArchiveProgress{
				JobId: req.JobId,
				Error: ptrString(fmt.Sprintf("parquet upload %s: %v", baseName, err)),
			})
			return err
		}
	}

	finished := true
	sender.send(&pb.UploadTraceArchiveProgress{
		JobId:         req.JobId,
		BytesUploaded: bytesUploaded,
		BytesTotal:    bytesTotal,
		FilesDone:     filesDone,
		FilesTotal:    filesTotal,
		Finished:      &finished,
	})
	return nil
}

// uploadOneTarget — 단일 PresignedTarget 처리. parts 가 있으면 multipart, 없으면 single PUT.
// 진행률(bytesUploaded/filesDone)을 카운터로 갱신하며 part complete 마다 sender.send (race-safe).
func uploadOneTarget(
	ctx context.Context,
	sender *archiveSender,
	jobID, localPath string,
	target *pb.PresignedTarget,
	bytesUploaded *int64,
	bytesTotal int64,
	filesDone *int32,
	filesTotal int32,
) error {
	// 표시용 baseName
	displayName := localPath
	if idx := strings.LastIndex(displayName, "/"); idx >= 0 {
		displayName = displayName[idx+1:]
	}

	if target.SinglePutUrl != "" && len(target.Parts) == 0 {
		// 단일 PUT (0-byte 또는 partSize 이하 작은 파일)
		etag, err := storage.UploadFilePresigned(ctx, localPath, target.SinglePutUrl)
		if err != nil {
			return err
		}
		atomic.AddInt64(bytesUploaded, target.TotalBytes)
		atomic.AddInt32(filesDone, 1)
		sender.send(&pb.UploadTraceArchiveProgress{
			JobId:         jobID,
			CurrentFile:   displayName,
			BytesUploaded: atomic.LoadInt64(bytesUploaded),
			BytesTotal:    bytesTotal,
			FilesDone:     atomic.LoadInt32(filesDone),
			FilesTotal:    filesTotal,
			CompletedPart: &pb.CompletedPartReport{
				LocalPath:  target.LocalPath,
				PartNumber: 1,
				Etag:       etag,
			},
		})
		return nil
	}

	// multipart
	parts := make([]storage.PartURL, 0, len(target.Parts))
	for _, p := range target.Parts {
		parts = append(parts, storage.PartURL{PartNumber: p.PartNumber, URL: p.Url})
	}
	_, err := storage.UploadFileMultipart(ctx, localPath, parts, target.PartSize, func(pp storage.PartProgress) {
		// part 완료 시 ETag stream-back (sender.send 가 mutex 로 직렬화)
		atomic.AddInt64(bytesUploaded, pp.BytesPut)
		sender.send(&pb.UploadTraceArchiveProgress{
			JobId:         jobID,
			CurrentFile:   displayName,
			BytesUploaded: atomic.LoadInt64(bytesUploaded),
			BytesTotal:    bytesTotal,
			FilesDone:     atomic.LoadInt32(filesDone),
			FilesTotal:    filesTotal,
			CompletedPart: &pb.CompletedPartReport{
				LocalPath:  target.LocalPath,
				PartNumber: pp.PartNumber,
				Etag:       pp.ETag,
			},
		})
	})
	if err != nil {
		return err
	}
	atomic.AddInt32(filesDone, 1)
	return nil
}

func ptrString(s string) *string { return &s }

// ==================== Monitoring ====================

func (s *DeviceAgentServer) MonitorDevices(req *pb.MonitorDevicesRequest, stream pb.DeviceAgent_MonitorDevicesServer) error {
	deviceIDs := req.DeviceIds
	if len(deviceIDs) == 0 {
		deviceIDs = s.manager.GetOnlineDevices()
	}
	if len(deviceIDs) == 0 {
		return fmt.Errorf("no online devices to monitor")
	}

	// stream.Context() 는 RPC 종료 시 cancel 되지만, stream.Send 실패로 핸들러가 먼저
	// 리턴해야 하는 경우 collector goroutine 들을 즉시 깨우려면 자식 ctx 가 필요하다.
	// 또한 핸들러 리턴 후 collector goroutine 이 ch 에 쓰려다 hang 되는 것을 막기 위해
	// 종료 시 명시적 cancel + WaitGroup 으로 모든 collector 가 멈출 때까지 대기한다.
	ctx, cancel := context.WithCancel(stream.Context())
	ch := make(chan *pb.DeviceMetrics, len(deviceIDs)*4)
	var wg sync.WaitGroup
	for _, id := range deviceIDs {
		wg.Add(1)
		go func(devID string) {
			defer wg.Done()
			s.collector.StreamMetrics(ctx, devID, req.IntervalSeconds, ch)
		}(id)
	}

	// 정리 순서:
	//   1) cancel() — collector 들이 ctx.Done 을 감지하고 종료에 들어감
	//   2) drain goroutine — collector 가 ctx 체크 직전 ch <- 시도 중일 수 있어 막힘 방지
	//   3) wg.Wait() — 모든 collector 가 실제로 리턴할 때까지 대기
	//   4) close(ch) — drain goroutine 종료
	defer func() {
		cancel()
		drainDone := make(chan struct{})
		go func() {
			for range ch {
			}
			close(drainDone)
		}()
		wg.Wait()
		close(ch)
		<-drainDone
	}()

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

// ==================== App Macro ====================

func (s *DeviceAgentServer) ListInstalledApps(ctx context.Context, req *pb.ListInstalledAppsRequest) (*pb.ListInstalledAppsResponse, error) {
	if s.macroMgr == nil {
		return nil, fmt.Errorf("macro manager not configured")
	}
	return s.macroMgr.ListInstalledApps(ctx, req)
}

func (s *DeviceAgentServer) StartRecording(ctx context.Context, req *pb.StartRecordingRequest) (*pb.StartRecordingResponse, error) {
	if s.macroMgr == nil {
		return &pb.StartRecordingResponse{Success: false}, fmt.Errorf("macro manager not configured")
	}
	return s.macroMgr.StartRecording(ctx, req)
}

func (s *DeviceAgentServer) StopRecording(ctx context.Context, req *pb.StopRecordingRequest) (*pb.StopRecordingResponse, error) {
	if s.macroMgr == nil {
		return &pb.StopRecordingResponse{Success: false}, fmt.Errorf("macro manager not configured")
	}
	return s.macroMgr.StopRecording(ctx, req)
}

func (s *DeviceAgentServer) ReplayMacro(ctx context.Context, req *pb.ReplayMacroRequest) (*pb.ReplayMacroResponse, error) {
	if s.macroMgr == nil {
		return nil, fmt.Errorf("macro manager not configured")
	}
	return s.macroMgr.ReplayMacro(ctx, req)
}

func (s *DeviceAgentServer) TakeScreenshot(ctx context.Context, req *pb.TakeScreenshotRequest) (*pb.TakeScreenshotResponse, error) {
	if s.macroMgr == nil {
		return &pb.TakeScreenshotResponse{Success: false}, fmt.Errorf("macro manager not configured")
	}
	return s.macroMgr.TakeScreenshot(ctx, req)
}

func (s *DeviceAgentServer) ScreenshotOcr(ctx context.Context, req *pb.ScreenshotOcrRequest) (*pb.ScreenshotOcrResponse, error) {
	if s.macroMgr == nil {
		return &pb.ScreenshotOcrResponse{Success: false}, fmt.Errorf("macro manager not configured")
	}
	return s.macroMgr.ScreenshotOcr(ctx, req)
}

// GetCurrentActivity — 디바이스의 현재 포그라운드 activity 조회 (REST 전용, proto RPC 아님).
// wait_until(activity) 스텝의 waitPattern 을 UI 에서 자동 채우는 데 쓴다.
func (s *DeviceAgentServer) GetCurrentActivity(ctx context.Context, deviceID string) (*macro.CurrentActivity, error) {
	if s.macroMgr == nil {
		return nil, fmt.Errorf("macro manager not configured")
	}
	return s.macroMgr.GetCurrentActivity(ctx, deviceID)
}

func (s *DeviceAgentServer) ListUiElements(ctx context.Context, req *pb.ListUiElementsRequest) (*pb.ListUiElementsResponse, error) {
	if s.macroMgr == nil {
		return &pb.ListUiElementsResponse{Success: false}, fmt.Errorf("macro manager not configured")
	}
	return s.macroMgr.ListUiElements(ctx, req)
}

// ==================== Trace Reparse ====================

func (s *DeviceAgentServer) ReparseTrace(ctx context.Context, req *pb.ReparseTraceRequest) (*pb.ReparseTraceResponse, error) {
	if err := s.traceMgr.ReparseTrace(req.JobId); err != nil {
		return &pb.ReparseTraceResponse{Success: false, Message: err.Error()}, nil
	}
	return &pb.ReparseTraceResponse{Success: true, Message: "reparse started"}, nil
}

// ==================== APK Management ====================

func (s *DeviceAgentServer) ListBundledApks(ctx context.Context, req *pb.ListBundledApksRequest) (*pb.ListBundledApksResponse, error) {
	if s.apkMgr == nil {
		return nil, fmt.Errorf("apk manager not configured")
	}
	return s.apkMgr.List(ctx, req)
}

func (s *DeviceAgentServer) InstallApk(ctx context.Context, req *pb.InstallApkRequest) (*pb.InstallApkResponse, error) {
	if s.apkMgr == nil {
		return &pb.InstallApkResponse{Success: false, Message: "apk manager not configured"}, fmt.Errorf("apk manager not configured")
	}
	return s.apkMgr.Install(ctx, req)
}

func (s *DeviceAgentServer) UninstallApk(ctx context.Context, req *pb.UninstallApkRequest) (*pb.UninstallApkResponse, error) {
	if s.apkMgr == nil {
		return &pb.UninstallApkResponse{Success: false, Message: "apk manager not configured"}, fmt.Errorf("apk manager not configured")
	}
	return s.apkMgr.Uninstall(ctx, req)
}
