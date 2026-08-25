package server

import (
	pb "agent/pb"
	"strconv"
)

// portal AgentController 와 동일한 enum 문자열 변환.
// portal frontend 가 이 정확한 문자열을 기대한다.

func deviceStateString(s pb.DeviceState) string {
	switch s {
	case pb.DeviceState_DEVICE_STATE_ONLINE:
		return "online"
	case pb.DeviceState_DEVICE_STATE_OFFLINE:
		return "offline"
	case pb.DeviceState_DEVICE_STATE_BUSY:
		return "busy"
	default:
		return "unknown"
	}
}

func jobStateString(s pb.JobState) string {
	switch s {
	case pb.JobState_JOB_STATE_QUEUED:
		return "queued"
	case pb.JobState_JOB_STATE_PUSHING_TOOLS:
		return "pushing_tools"
	case pb.JobState_JOB_STATE_RUNNING:
		return "running"
	case pb.JobState_JOB_STATE_COLLECTING:
		return "collecting"
	case pb.JobState_JOB_STATE_COMPLETED:
		return "completed"
	case pb.JobState_JOB_STATE_FAILED:
		return "failed"
	case pb.JobState_JOB_STATE_PARTIALLY_FAILED:
		return "partially_failed"
	case pb.JobState_JOB_STATE_CANCELLED:
		return "cancelled"
	case pb.JobState_JOB_STATE_REPARSING:
		return "reparsing"
	default:
		return "unknown"
	}
}

func benchmarkToolString(t pb.BenchmarkTool) string {
	switch t {
	case pb.BenchmarkTool_BENCHMARK_TOOL_FIO:
		return "fio"
	case pb.BenchmarkTool_BENCHMARK_TOOL_IOZONE:
		return "iozone"
	case pb.BenchmarkTool_BENCHMARK_TOOL_TIOTEST:
		return "tiotest"
	case pb.BenchmarkTool_BENCHMARK_TOOL_IOTEST:
		return "iotest"
	default:
		return "unspecified"
	}
}

func parseBenchmarkTool(s string) pb.BenchmarkTool {
	switch s {
	case "FIO", "fio", "BENCHMARK_TOOL_FIO":
		return pb.BenchmarkTool_BENCHMARK_TOOL_FIO
	case "IOZONE", "iozone", "BENCHMARK_TOOL_IOZONE":
		return pb.BenchmarkTool_BENCHMARK_TOOL_IOZONE
	case "TIOTEST", "tiotest", "BENCHMARK_TOOL_TIOTEST":
		return pb.BenchmarkTool_BENCHMARK_TOOL_TIOTEST
	case "IOTEST", "iotest", "BENCHMARK_TOOL_IOTEST":
		return pb.BenchmarkTool_BENCHMARK_TOOL_IOTEST
	default:
		return pb.BenchmarkTool_BENCHMARK_TOOL_UNSPECIFIED
	}
}

// ---------- proto → map (portal LinkedHashMap 직렬화 흉내) ----------

func deviceToMap(d *pb.DeviceInfo) map[string]any {
	return map[string]any{
		"deviceId":       d.GetDeviceId(),
		"serial":         d.GetSerial(),
		"state":          deviceStateString(d.GetState()),
		"androidVersion": d.GetAndroidVersion(),
		"model":          d.GetModel(),
		"board":          d.GetBoard(),
		"platform":       d.GetPlatform(),
		"hardware":       d.GetHardware(),
		"cpuAbi":         d.GetCpuAbi(),
		"buildId":        d.GetBuildId(),
		"manufacturer":   d.GetManufacturer(),
		"sdkVersion":     d.GetSdkVersion(),
	}
}

func deviceJobStatusToMap(s *pb.DeviceJobStatus) map[string]any {
	return map[string]any{
		"deviceId":        s.GetDeviceId(),
		"state":           jobStateString(s.GetState()),
		"message":         s.GetMessage(),
		"progressPercent": s.GetProgressPercent(),
	}
}

func benchmarkResultToMap(r *pb.BenchmarkResult) map[string]any {
	m := map[string]any{
		"deviceId":   r.GetDeviceId(),
		"tool":       benchmarkToolString(r.GetTool()),
		"rawOutput":  r.GetRawOutput(),
		"metrics":    r.GetMetrics(),
		"startedAt":  r.GetStartedAt(),
		"finishedAt": r.GetFinishedAt(),
		"success":    r.GetSuccess(),
		"error":      r.GetError(),
	}
	if len(r.GetTraceJobs()) > 0 {
		jobs := make([]map[string]any, 0, len(r.GetTraceJobs()))
		for _, tj := range r.GetTraceJobs() {
			jobs = append(jobs, map[string]any{
				"traceJobId":  tj.GetTraceJobId(),
				"stepIndex":   tj.GetStepIndex(),
				"loopIndex":   tj.GetLoopIndex(),
				"repeatIndex": tj.GetRepeatIndex(),
				"traceType":   tj.GetTraceType(),
			})
		}
		m["traceJobs"] = jobs
	}
	return m
}

func metricsToMap(m *pb.DeviceMetrics) map[string]any {
	result := map[string]any{
		"deviceId":  m.GetDeviceId(),
		"timestamp": m.GetTimestamp(),
	}
	if c := m.GetCpu(); c != nil {
		result["cpu"] = map[string]any{
			"usagePercent":   c.GetUsagePercent(),
			"perCorePercent": c.GetPerCorePercent(),
		}
	}
	if mem := m.GetMemory(); mem != nil {
		result["memory"] = map[string]any{
			"totalKb":      mem.GetTotalKb(),
			"availableKb":  mem.GetAvailableKb(),
			"usedKb":       mem.GetUsedKb(),
			"usagePercent": mem.GetUsagePercent(),
		}
	}
	if d := m.GetDisk(); d != nil {
		result["disk"] = map[string]any{
			"readBytes":  d.GetReadBytes(),
			"writeBytes": d.GetWriteBytes(),
			"readIos":    d.GetReadIos(),
			"writeIos":   d.GetWriteIos(),
		}
	}
	if dp := m.GetDataPartition(); dp != nil {
		result["dataPartition"] = map[string]any{
			"mountPoint":     dp.GetMountPoint(),
			"filesystem":     dp.GetFilesystem(),
			"totalBytes":     dp.GetTotalBytes(),
			"usedBytes":      dp.GetUsedBytes(),
			"availableBytes": dp.GetAvailableBytes(),
			"usagePercent":   dp.GetUsagePercent(),
		}
	}
	return result
}

// ---------- TraceStats / TraceEvent → map (portal toTraceStatsMap 그대로) ----------

func latencyStatsToMap(l *pb.LatencyStats) map[string]any {
	if l == nil {
		return nil
	}
	return map[string]any{
		"min":     l.GetMin(),
		"max":     l.GetMax(),
		"avg":     l.GetAvg(),
		"stddev":  l.GetStddev(),
		"median":  l.GetMedian(),
		"p99":     l.GetP99(),
		"p999":    l.GetP999(),
		"p9999":   l.GetP9999(),
		"p99999":  l.GetP99999(),
		"p999999": l.GetP999999(),
	}
}

func traceStatsToMap(s *pb.TraceStats) map[string]any {
	if s == nil {
		return nil
	}
	cmdStats := make([]map[string]any, 0, len(s.GetCmdStats()))
	for _, c := range s.GetCmdStats() {
		cmdStats = append(cmdStats, map[string]any{
			"cmd":             c.GetCmd(),
			"count":           c.GetCount(),
			"ratio":           c.GetRatio(),
			"dtoc":            latencyStatsToMap(c.GetDtoc()),
			"ctod":            latencyStatsToMap(c.GetCtod()),
			"ctoc":            latencyStatsToMap(c.GetCtoc()),
			"qd":              latencyStatsToMap(c.GetQd()),
			"totalSizeBytes":  c.GetTotalSizeBytes(),
			"continuousCount": c.GetContinuousCount(),
			"continuousRatio": c.GetContinuousRatio(),
			"sendCount":       c.GetSendCount(),
		})
	}
	hists := make([]map[string]any, 0, len(s.GetLatencyHistograms()))
	for _, h := range s.GetLatencyHistograms() {
		buckets := make([]map[string]any, 0, len(h.GetBuckets()))
		for _, b := range h.GetBuckets() {
			buckets = append(buckets, map[string]any{
				"rangeStartMs": b.GetRangeStartMs(),
				"rangeEndMs":   b.GetRangeEndMs(),
				"count":        b.GetCount(),
			})
		}
		hists = append(hists, map[string]any{
			"cmd":         h.GetCmd(),
			"latencyType": h.GetLatencyType(),
			"buckets":     buckets,
		})
	}
	sizes := make([]map[string]any, 0, len(s.GetCmdSizeCounts()))
	for _, c := range s.GetCmdSizeCounts() {
		sizes = append(sizes, map[string]any{
			"cmd":   c.GetCmd(),
			"size":  c.GetSize(),
			"count": c.GetCount(),
		})
	}
	// mgmt 는 fsio_ufs 에만 있다. 없으면 빈 배열 — 프론트가 `?? []` 안 해도 되게.
	mgmt := make([]map[string]any, 0, len(s.GetMgmtStats()))
	for _, m := range s.GetMgmtStats() {
		mgmt = append(mgmt, map[string]any{
			"name":        m.GetName(),
			"kind":        m.GetKind(),
			"count":       m.GetCount(),
			"pairedCount": m.GetPairedCount(),
			"totalTimeMs": m.GetTotalTimeMs(),
			"dtoc":        latencyStatsToMap(m.GetDtoc()),
		})
	}
	return map[string]any{
		"totalEvents":       s.GetTotalEvents(),
		"durationSeconds":   s.GetDurationSeconds(),
		"mgmtStats":         mgmt,
		"dtoc":              latencyStatsToMap(s.GetDtoc()),
		"ctod":              latencyStatsToMap(s.GetCtod()),
		"ctoc":              latencyStatsToMap(s.GetCtoc()),
		"qd":                latencyStatsToMap(s.GetQd()),
		"cmdStats":          cmdStats,
		"latencyHistograms": hists,
		"cmdSizeCounts":     sizes,
		"continuousCount":   s.GetContinuousCount(),
		"continuousRatio":   s.GetContinuousRatio(),
		"alignedCount":      s.GetAlignedCount(),
		"alignedRatio":      s.GetAlignedRatio(),
		"readTotalBytes":    s.GetReadTotalBytes(),
		"writeTotalBytes":   s.GetWriteTotalBytes(),
		"discardTotalBytes": s.GetDiscardTotalBytes(),
		"sendCount":         s.GetSendCount(),
	}
}

// attributionDimName — proto enum → portal 프론트가 쓰는 소문자 축 이름.
func attributionDimName(d pb.AttributionDim) string {
	switch d {
	case pb.AttributionDim_ATTR_DIM_COMM:
		return "comm"
	case pb.AttributionDim_ATTR_DIM_PID:
		return "pid"
	case pb.AttributionDim_ATTR_DIM_TID:
		return "tid"
	case pb.AttributionDim_ATTR_DIM_SYSCALL:
		return "syscall"
	case pb.AttributionDim_ATTR_DIM_FS:
		return "fs"
	case pb.AttributionDim_ATTR_DIM_FILE:
		return "file"
	case pb.AttributionDim_ATTR_DIM_INO:
		return "ino"
	case pb.AttributionDim_ATTR_DIM_FLOW:
		return "flow"
	case pb.AttributionDim_ATTR_DIM_CMD:
		return "cmd"
	case pb.AttributionDim_ATTR_DIM_LUN:
		return "lun"
	case pb.AttributionDim_ATTR_DIM_DEVICE:
		return "device"
	}
	return "unspecified"
}

// attributionDimFromName — 위의 역변환. 모르는 이름은 UNSPECIFIED (호출부가 건너뛴다).
func attributionDimFromName(n string) pb.AttributionDim {
	switch n {
	case "comm":
		return pb.AttributionDim_ATTR_DIM_COMM
	case "pid":
		return pb.AttributionDim_ATTR_DIM_PID
	case "tid":
		return pb.AttributionDim_ATTR_DIM_TID
	case "syscall":
		return pb.AttributionDim_ATTR_DIM_SYSCALL
	case "fs":
		return pb.AttributionDim_ATTR_DIM_FS
	case "file":
		return pb.AttributionDim_ATTR_DIM_FILE
	case "ino":
		return pb.AttributionDim_ATTR_DIM_INO
	case "flow":
		return pb.AttributionDim_ATTR_DIM_FLOW
	case "cmd":
		return pb.AttributionDim_ATTR_DIM_CMD
	case "lun":
		return pb.AttributionDim_ATTR_DIM_LUN
	case "device":
		return pb.AttributionDim_ATTR_DIM_DEVICE
	}
	return pb.AttributionDim_ATTR_DIM_UNSPECIFIED
}

func attributionSortFromName(n string) pb.AttributionSort {
	switch n {
	case "bytes":
		return pb.AttributionSort_ATTR_SORT_BYTES
	case "latency":
		return pb.AttributionSort_ATTR_SORT_LATENCY_SUM
	default:
		return pb.AttributionSort_ATTR_SORT_COUNT
	}
}

func attributionToMap(r *pb.GetIoAttributionResponse) map[string]any {
	if r == nil {
		return nil
	}
	groups := make([]map[string]any, 0, len(r.GetGroups()))
	for _, g := range r.GetGroups() {
		entries := make([]map[string]any, 0, len(g.GetEntries()))
		for _, e := range g.GetEntries() {
			m := map[string]any{
				"key":        e.GetKey(),
				"count":      e.GetCount(),
				"sendCount":  e.GetSendCount(),
				"ratio":      e.GetRatio(),
				"readBytes":  e.GetReadBytes(),
				"writeBytes": e.GetWriteBytes(),
				"totalBytes": e.GetTotalBytes(),
				"dtocSumMs":  e.GetDtocSumMs(),
				"dtocMaxMs":  e.GetDtocMaxMs(),
				"isOther":    e.GetIsOther(),
			}
			// optional 필드는 **없으면 아예 안 넣는다** — JSON null 로 나가야
			// 프론트가 "—" 로 렌더한다. 0 으로 채우면 "0ms = 빠름" 으로 오독된다.
			if e.DtocAvgMs != nil {
				m["dtocAvgMs"] = e.GetDtocAvgMs()
			}
			if e.DtocP50Ms != nil {
				m["dtocP50Ms"] = e.GetDtocP50Ms()
			}
			if e.DtocP99Ms != nil {
				m["dtocP99Ms"] = e.GetDtocP99Ms()
			}
			if e.DistinctFiles != nil {
				m["distinctFiles"] = e.GetDistinctFiles()
			}
			entries = append(entries, m)
		}
		groups = append(groups, map[string]any{
			"dim":          attributionDimName(g.GetDim()),
			"entries":      entries,
			"distinctKeys": g.GetDistinctKeys(),
		})
	}
	unsupported := make([]string, 0, len(r.GetUnsupportedDims()))
	for _, d := range r.GetUnsupportedDims() {
		unsupported = append(unsupported, attributionDimName(d))
	}
	return map[string]any{
		"totalEvents":     r.GetTotalEvents(),
		"groups":          groups,
		"unsupportedDims": unsupported,
	}
}

// traceEventToMap — raw 이벤트 한 행.
//
// fsio 확장 필드는 **값이 있을 때만** 넣는다. ftrace 산출물에서 전부 빈 값으로 채우면
// 표에 의미 없는 0/빈칸 컬럼이 20개 생기고, 클라이언트가 "이 trace_type 에 이 컬럼이
// 있나" 를 값으로 판단할 수 없게 된다.
func traceEventToMap(e *pb.TraceEvent) map[string]any {
	m := map[string]any{
		"time":       e.GetTime(),
		"lba":        e.GetLba(),
		"qd":         e.GetQd(),
		"cpu":        e.GetCpu(),
		"dtoc":       e.GetDtoc(),
		"ctod":       e.GetCtod(),
		"ctoc":       e.GetCtoc(),
		"cmd":        e.GetCmd(),
		"size":       e.GetSize(),
		"continuous": e.GetContinuous(),
		"action":     e.GetAction(),
	}
	// line_number 는 fsio 산출물에만 채워진다 — 이걸 fsio 여부의 신호로 쓴다.
	if e.GetLineNumber() == 0 && e.GetComm() == "" && e.GetIoFlags() == 0 {
		return m
	}
	m["aligned"] = e.GetAligned()
	m["line_number"] = e.GetLineNumber()
	m["pid"] = e.GetPid()
	m["tid"] = e.GetTid()
	m["comm"] = e.GetComm()
	m["process"] = e.GetComm() // portal 표의 process 컬럼 = comm 별칭
	m["syscall"] = e.GetSyscall()
	m["fs"] = e.GetFs()
	m["ino"] = e.GetIno()
	m["name"] = e.GetName()
	// io_flags 는 u64 라 JSON number 로 보내면 2^53 넘는 f2fs 비트가 깨진다.
	// 문자열로 보내고 클라이언트가 BigInt 로 푼다.
	m["io_flags"] = strconv.FormatUint(e.GetIoFlags(), 10)

	if e.GetRwbs() != "" || e.GetDevmajor() != 0 || e.GetDevminor() != 0 {
		// fsio_block
		m["devmajor"] = e.GetDevmajor()
		m["devminor"] = e.GetDevminor()
		m["rwbs"] = e.GetRwbs()
		m["flags"] = e.GetFlags()
		m["extra"] = e.GetExtra()
		m["sector"] = e.GetLba() // block 은 lba 자리에 sector 가 온다
	} else {
		// fsio_ufs
		m["tag"] = e.GetTag()
		m["opcode"] = e.GetOpcode()
		m["lun"] = e.GetLun()
		m["groupid"] = e.GetGroupid()
		m["hwqid"] = e.GetHwqid()
		m["upiu_attr"] = e.GetUpiuAttr()
		// UPIU 헤더는 없으면 키를 빼 JSON null 로 — 0 과 "없음" 을 구분한다.
		if e.Txn != nil {
			m["txn"] = e.GetTxn()
		}
		if e.UpiuFlags != nil {
			m["upiu_flags"] = e.GetUpiuFlags()
		}
		if e.UpiuFunc != nil {
			m["upiu_func"] = e.GetUpiuFunc()
		}
		if e.UpiuCp != nil {
			m["upiu_cp"] = e.GetUpiuCp()
		}
	}
	return m
}

// ---------- TraceFilter body → proto ----------

// buildTraceFilter — portal 의 buildTraceFilter 와 동일한 키 매핑.
// 전달된 map 은 body["filter"] 의 내용물.
func buildTraceFilter(f map[string]any) *pb.TraceFilter {
	if f == nil {
		return nil
	}
	out := &pb.TraceFilter{}
	if v, ok := numberOf(f["startTime"]); ok {
		out.StartTime = v
	}
	if v, ok := numberOf(f["endTime"]); ok {
		out.EndTime = v
	}
	if v, ok := numberOf(f["startLba"]); ok {
		out.StartLba = uint64(v)
	}
	if v, ok := numberOf(f["endLba"]); ok {
		out.EndLba = uint64(v)
	}
	if v, ok := numberOf(f["minDtoc"]); ok {
		out.MinDtoc = v
	}
	if v, ok := numberOf(f["maxDtoc"]); ok {
		out.MaxDtoc = v
	}
	if v, ok := numberOf(f["minCtoc"]); ok {
		out.MinCtoc = v
	}
	if v, ok := numberOf(f["maxCtoc"]); ok {
		out.MaxCtoc = v
	}
	if v, ok := numberOf(f["minCtod"]); ok {
		out.MinCtod = v
	}
	if v, ok := numberOf(f["maxCtod"]); ok {
		out.MaxCtod = v
	}
	if v, ok := numberOf(f["minQd"]); ok {
		out.MinQd = uint32(v)
	}
	if v, ok := numberOf(f["maxQd"]); ok {
		out.MaxQd = uint32(v)
	}
	if arr, ok := f["cpuList"].([]any); ok {
		for _, x := range arr {
			if n, ok := numberOf(x); ok {
				out.CpuList = append(out.CpuList, uint32(n))
			}
		}
	}
	if arr, ok := f["cmdList"].([]any); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok {
				out.CmdList = append(out.CmdList, s)
			}
		}
	}
	if arr, ok := f["sizeList"].([]any); ok {
		for _, x := range arr {
			if n, ok := numberOf(x); ok {
				out.SizeList = append(out.SizeList, uint32(n))
			}
		}
	}
	if arr, ok := f["actionList"].([]any); ok {
		for _, x := range arr {
			if s, ok := x.(string); ok {
				out.ActionList = append(out.ActionList, s)
			}
		}
	}

	// ── fsio cross-layer 필터 ──
	// Attribution 드릴다운이 여기로 흘러 모든 탭이 같은 모수를 보게 된다.
	out.CommList = jsonStrings(f["commList"])
	out.SyscallList = jsonStrings(f["syscallList"])
	out.FsList = jsonStrings(f["fsList"])
	out.NameList = jsonStrings(f["nameList"])
	out.DevList = jsonStrings(f["devList"])
	for _, v := range jsonNumbers(f["pidList"]) {
		out.PidList = append(out.PidList, uint32(v))
	}
	for _, v := range jsonNumbers(f["inoList"]) {
		out.InoList = append(out.InoList, uint64(v))
	}
	for _, v := range jsonNumbers(f["lunList"]) {
		out.LunList = append(out.LunList, uint32(v))
	}
	if s, ok := f["nameContains"].(string); ok {
		out.NameContains = s
	}
	// io_flags 마스크는 **문자열**로 받는다 — u64 를 JSON number 로 실으면
	// 2^53 넘는 f2fs 힌트 비트가 조용히 반올림된다.
	if s, ok := f["ioFlagsAny"].(string); ok {
		out.IoFlagsAny = s
	}
	if s, ok := f["ioFlagsAll"].(string); ok {
		out.IoFlagsAll = s
	}
	if s, ok := f["ioFlagsNone"].(string); ok {
		out.IoFlagsNone = s
	}
	return out
}

// jsonStrings — JSON 배열에서 문자열만 뽑는다.
func jsonStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// jsonNumbers — JSON 배열에서 숫자만 뽑는다.
func jsonNumbers(v any) []float64 {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []float64
	for _, x := range arr {
		if n, ok := numberOf(x); ok {
			out = append(out, n)
		}
	}
	return out
}

// numberOf — JSON unmarshal 시 모든 숫자는 float64. int/float 어떤 형태로 와도 float64 변환.
func numberOf(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}
