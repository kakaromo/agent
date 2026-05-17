package server

import (
	"context"
	"encoding/json"

	pb "agent/pb"
)

// buildResultSummary — 잡 종료 시 agent 메모리의 결과를 fetch 해 DB 영구 저장용 summary JSON 으로 변환.
//
// 디자인 원칙:
//   - 저장량 적당히 (rawOutput 같은 거대 텍스트는 생략) — Result 페이지의 카드/표 표시에 필요한 metrics 만.
//   - jobType 별 분기 (benchmark/scenario vs trace).
//   - agent 메모리에서 사라진 잡 (404) 은 빈 문자열 반환 → 호출자가 skip.
func buildResultSummary(ctx context.Context, agent *DeviceAgentServer, jobID, jobType string) (string, error) {
	switch jobType {
	case "benchmark", "scenario":
		return buildBenchmarkSummary(ctx, agent, jobID)
	case "trace":
		return buildTraceSummary(ctx, agent, jobID)
	default:
		return "", nil
	}
}

// benchmark summary: device 별 tool, success, 핵심 metrics (read/write IOPS, BW, latency p99 등).
// rawOutput / latency 백분위 전체는 생략 — Result 페이지 첫 화면에 충분한 정도만.
func buildBenchmarkSummary(ctx context.Context, agent *DeviceAgentServer, jobID string) (string, error) {
	resp, err := agent.GetBenchmarkResult(ctx, &pb.GetBenchmarkResultRequest{JobId: jobID})
	if err != nil {
		return "", nil // 메모리에 없으면 skip
	}
	if len(resp.GetResults()) == 0 {
		return "", nil
	}
	devices := make([]map[string]any, 0, len(resp.GetResults()))
	for _, br := range resp.GetResults() {
		m := br.GetMetrics()
		// 핵심 키만 추출 (있으면).
		core := map[string]any{}
		for _, k := range []string{
			"read_iops", "write_iops",
			"read_bw_kb", "write_bw_kb",
			"read_clat_ns_mean", "write_clat_ns_mean",
			"read_clat_ns_p99.000000", "write_clat_ns_p99.000000",
			"read_clat_ns_p99.900000", "write_clat_ns_p99.900000",
			"job_runtime_ms",
		} {
			if v, ok := m[k]; ok {
				core[k] = v
			}
		}
		devices = append(devices, map[string]any{
			"deviceId":   br.GetDeviceId(),
			"tool":       benchmarkToolString(br.GetTool()),
			"success":    br.GetSuccess(),
			"error":      br.GetError(),
			"startedAt":  br.GetStartedAt(),
			"finishedAt": br.GetFinishedAt(),
			"metrics":    core,
		})
	}
	data, err := json.Marshal(map[string]any{
		"jobId":   jobID,
		"devices": devices,
	})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// trace summary: totalEvents, durationSeconds, 주요 latency (dtoc) min/max/avg/p99/p999, cmd top-N.
// cmd 분포 / histogram 풀 데이터는 parquet 에 있으니 여기선 생략 — 재시작 후에도 parquet 가 있으면 stats 재계산 가능.
func buildTraceSummary(ctx context.Context, agent *DeviceAgentServer, jobID string) (string, error) {
	resp, err := agent.GetTraceResult(ctx, &pb.GetTraceResultRequest{JobIds: []string{jobID}})
	if err != nil {
		return "", nil
	}
	s := resp.GetStats()
	if s == nil {
		return "", nil
	}
	// cmd top 5 (count 기준)
	cmds := make([]map[string]any, 0)
	for i, c := range s.GetCmdStats() {
		if i >= 5 {
			break
		}
		cmds = append(cmds, map[string]any{
			"cmd":   c.GetCmd(),
			"count": c.GetCount(),
			"ratio": c.GetRatio(),
		})
	}
	summary := map[string]any{
		"jobId":             jobID,
		"totalEvents":       s.GetTotalEvents(),
		"durationSeconds":   s.GetDurationSeconds(),
		"readTotalBytes":    s.GetReadTotalBytes(),
		"writeTotalBytes":   s.GetWriteTotalBytes(),
		"continuousRatio":   s.GetContinuousRatio(),
		"alignedRatio":      s.GetAlignedRatio(),
		"sendCount":         s.GetSendCount(),
		"dtoc":              latencyStatsToMap(s.GetDtoc()),
		"ctod":              latencyStatsToMap(s.GetCtod()),
		"ctoc":              latencyStatsToMap(s.GetCtoc()),
		"cmdTop":            cmds,
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
