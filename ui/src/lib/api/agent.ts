import { get, post, put, del } from './client.js';

// ── Types ──

export interface AgentServer {
	id: number;
	name: string;
	host: string;
	port: number;
	enabled: boolean;
	description?: string;
	createdAt?: string;
	updatedAt?: string;
}

export interface Device {
	deviceId: string;
	serial: string;
	state: string;  // online, offline, busy, unknown
	androidVersion: string;
	model: string;
	board?: string;          // ro.product.board
	platform?: string;       // ro.board.platform
	hardware?: string;       // ro.hardware
	cpuAbi?: string;         // ro.product.cpu.abi
	buildId?: string;        // ro.build.display.id
	manufacturer?: string;   // ro.product.manufacturer
	sdkVersion?: number;     // ro.build.version.sdk
}

export interface DeviceJobStatus {
	deviceId: string;
	state: string;  // queued, pushing_tools, running, collecting, completed, failed, partially_failed
	message: string;
	progressPercent: number;
}

export interface JobStatus {
	jobId: string;
	state: string;
	totalDevices: number;
	completedDevices: number;
	failedDevices: number;
	deviceStatuses: DeviceJobStatus[];
}

export interface JobProgress {
	jobId: string;
	deviceId: string;
	state: string;
	message: string;
	progressPercent: number;
	error: string;
	metrics?: Record<string, number>;
	rawOutput?: string;
}

export interface TraceJobMapping {
	traceJobId: string;
	stepIndex: number;
	loopIndex: number;
	repeatIndex: number;
	traceType: string;
}

/**
 * StepBoundary — 시나리오 스텝 하나의 실행 구간.
 *
 * behavior 구간별 IO 분석의 시간 축이다. `startedMono`/`finishedMono` 는 parquet
 * `time` 과 **같은 축**(기기 monotonic 초)이라 구간 질의에 그대로 쓸 수 있다.
 *
 * ⚠ mono 가 0 이면 clock offset 을 못 쟀거나 못 믿는다는 뜻 — 그 구간은 분할에
 * 쓰지 않는다. 호스트 시각(startedAt/finishedAt)은 남아 있어 로그 대조에는 쓸 수 있다.
 */
export interface StepBoundary {
	stepIndex: number;
	loopIndex: number;
	repeatIndex: number;
	type: string;
	label: string;
	startedAt: number;
	finishedAt: number;
	startedMono: number;
	finishedMono: number;
	success: boolean;
	error: string;
}

export interface BenchmarkResultItem {
	deviceId: string;
	tool: string;
	rawOutput: string;
	metrics: Record<string, number>;
	startedAt: number;
	finishedAt: number;
	success: boolean;
	error: string;
	traceJobs?: TraceJobMapping[];
	stepBoundaries?: StepBoundary[];
}

export interface BenchmarkResult {
	results: BenchmarkResultItem[];
}

export interface CpuMetrics {
	usagePercent: number;
	perCorePercent: number[];
}

export interface MemoryMetrics {
	totalKb: number;
	availableKb: number;
	usedKb: number;
	usagePercent: number;
}

export interface DiskMetrics {
	readBytes: number;
	writeBytes: number;
	readIos: number;
	writeIos: number;
}

export interface FilesystemInfo {
	mountPoint: string;
	filesystem: string;
	totalBytes: number;
	usedBytes: number;
	availableBytes: number;
	usagePercent: number;
}

export interface DeviceMetricsData {
	deviceId: string;
	timestamp: number;
	cpu?: CpuMetrics;
	memory?: MemoryMetrics;
	disk?: DiskMetrics;
	dataPartition?: FilesystemInfo;
}

export interface ScenarioStep {
	type: string;       // benchmark, shell, cleanup, sleep, trace_start, trace_stop, condition, app_macro
	tool?: string;      // FIO, IOZONE, TIOTEST
	params?: Record<string, string>;
	condition?: Record<string, unknown>;
	macroId?: number;
	macroName?: string;
}

export interface ScenarioLoop {
	startStep: number;
	endStep: number;
	count: number;
}

// ── Server CRUD ──

export function fetchAgentServers(): Promise<AgentServer[]> {
	return get('/agent/servers');
}

export function createAgentServer(data: Omit<AgentServer, 'id' | 'createdAt' | 'updatedAt'>): Promise<AgentServer> {
	return post('/agent/servers', data);
}

export function updateAgentServer(id: number, data: Omit<AgentServer, 'id' | 'createdAt' | 'updatedAt'>): Promise<AgentServer> {
	return put(`/agent/servers/${id}`, data);
}

export function deleteAgentServer(id: number): Promise<{ success: boolean }> {
	return del(`/agent/servers/${id}`);
}

export function testAgentServerById(id: number): Promise<{ success: boolean; message: string }> {
	return post(`/agent/servers/${id}/test`, {});
}

export function testAgentConnection(host: string, port: number): Promise<{ success: boolean; message: string }> {
	return post('/agent/servers/test', { host, port });
}

export function getServerConnectionStatus(id: number): Promise<{ serverId: number; state: string; connected: boolean }> {
	return get(`/agent/servers/${id}/status`);
}

export function reconnectServer(id: number): Promise<{ success: boolean; state: string; message: string }> {
	return post(`/agent/servers/${id}/reconnect`, {});
}

// ── Devices ──

export function fetchDevices(serverId: number): Promise<{ devices: Device[] }> {
	return get(`/agent/devices?serverId=${serverId}`);
}

export function connectDevice(serverId: number, serial: string): Promise<{ success: boolean; message: string }> {
	return post(`/agent/devices/${encodeURIComponent(serial)}/connect?serverId=${serverId}`, {});
}

export function disconnectDevice(serverId: number, serial: string): Promise<{ success: boolean }> {
	return post(`/agent/devices/${encodeURIComponent(serial)}/disconnect?serverId=${serverId}`, {});
}

// ── Benchmark ──

export function runBenchmark(serverId: number, data: {
	deviceIds: string[];
	tool: string;
	params: Record<string, string>;
	jobName?: string;
	busyPolicy?: string;
}): Promise<{ jobId: string }> {
	return post(`/agent/benchmark/run?serverId=${serverId}`, data);
}

export function getJobStatus(serverId: number, jobId: string): Promise<JobStatus> {
	return get(`/agent/benchmark/status?serverId=${serverId}&jobId=${encodeURIComponent(jobId)}`);
}

export function getBenchmarkResult(serverId: number, jobId: string, deviceId?: string): Promise<BenchmarkResult> {
	let url = `/agent/benchmark/result?serverId=${serverId}&jobId=${encodeURIComponent(jobId)}`;
	if (deviceId) url += `&deviceId=${encodeURIComponent(deviceId)}`;
	return get(url);
}

// ── Scenario ──

export function runScenario(serverId: number, data: {
	deviceIds: string[];
	scenarioName?: string;
	steps: ScenarioStep[];
	loops?: ScenarioLoop[];
	repeat?: number;
	busyPolicy?: string;
}): Promise<{ jobId: string }> {
	return post(`/agent/scenario/run?serverId=${serverId}`, data);
}

// ── Scenario Templates ──

export interface ScenarioTemplate {
	id: number;
	name: string;
	description?: string;
	repeatCount: number;
	stepsJson: string;
	loopsJson?: string;
	createdAt?: string;
	updatedAt?: string;
}

export function fetchScenarioTemplates(): Promise<ScenarioTemplate[]> {
	return get('/agent/scenario-templates');
}

export function createScenarioTemplate(data: Omit<ScenarioTemplate, 'id' | 'createdAt' | 'updatedAt'>): Promise<ScenarioTemplate> {
	return post('/agent/scenario-templates', data);
}

export function updateScenarioTemplate(id: number, data: Omit<ScenarioTemplate, 'id' | 'createdAt' | 'updatedAt'>): Promise<ScenarioTemplate> {
	return put(`/agent/scenario-templates/${id}`, data);
}

export function deleteScenarioTemplate(id: number): Promise<{ success: boolean }> {
	return del(`/agent/scenario-templates/${id}`);
}

export function duplicateScenarioTemplate(id: number): Promise<ScenarioTemplate> {
	return post(`/agent/scenario-templates/${id}/duplicate`, {});
}

// ── Scenario Template export/import (이식 계층, ADR-0001) ──

export interface ScenarioImportResult {
	imported: ScenarioTemplate[];
	skipped?: string[];
	warnings?: string[];
}

// 브라우저 다운로드를 트리거한다. export 는 attachment 응답이라 fetch→blob→a[download] 로 저장.
async function downloadFromApi(path: string, fallbackFilename: string): Promise<void> {
	const res = await fetch(`/api${path}`);
	if (!res.ok) throw new Error(`export 실패 (${res.status})`);
	// 서버가 준 파일명(Content-Disposition) 우선, 없으면 fallback
	let filename = fallbackFilename;
	const cd = res.headers.get('Content-Disposition') || '';
	const m = cd.match(/filename="?([^"]+)"?/);
	if (m) filename = decodeURIComponent(m[1]);
	const blob = await res.blob();
	const url = URL.createObjectURL(blob);
	const a = document.createElement('a');
	a.href = url;
	a.download = filename;
	document.body.appendChild(a);
	a.click();
	a.remove();
	URL.revokeObjectURL(url);
}

export function exportScenarioTemplate(id: number): Promise<void> {
	return downloadFromApi(`/agent/scenario-templates/${id}/export`, 'scenario.scenario.json');
}

export function exportAllScenarioTemplates(): Promise<void> {
	return downloadFromApi('/agent/scenario-templates/export-all', 'scenarios.scenariopack.json');
}

// import — 파일에서 읽은 JSON(단일 또는 배열)을 그대로 POST. 응답에 imported/skipped/warnings.
export function importScenarioTemplates(payload: unknown): Promise<ScenarioImportResult> {
	return post('/agent/scenario-templates/import', payload);
}

// ── Benchmark Presets ──

export interface BenchmarkPreset {
	id: number;
	name: string;
	description?: string;
	tool: string;
	paramsJson: string;
	createdAt?: string;
	updatedAt?: string;
}

export function fetchBenchmarkPresets(): Promise<BenchmarkPreset[]> {
	return get('/agent/benchmark-presets');
}

export function createBenchmarkPreset(data: Omit<BenchmarkPreset, 'id' | 'createdAt' | 'updatedAt'>): Promise<BenchmarkPreset> {
	return post('/agent/benchmark-presets', data);
}

export function updateBenchmarkPreset(id: number, data: Omit<BenchmarkPreset, 'id' | 'createdAt' | 'updatedAt'>): Promise<BenchmarkPreset> {
	return put(`/agent/benchmark-presets/${id}`, data);
}

export function deleteBenchmarkPreset(id: number): Promise<{ success: boolean }> {
	return del(`/agent/benchmark-presets/${id}`);
}

// ── I/O Test Presets ──

export interface IOTestPresetDB {
	id: number;
	name: string;
	description?: string;
	category: string;
	configJson: string;
	createdAt?: string;
	updatedAt?: string;
}

export function fetchIOTestPresets(): Promise<IOTestPresetDB[]> {
	return get('/agent/iotest-presets');
}

export function createIOTestPreset(data: Omit<IOTestPresetDB, 'id' | 'createdAt' | 'updatedAt'>): Promise<IOTestPresetDB> {
	return post('/agent/iotest-presets', data);
}

export function deleteIOTestPreset(id: number): Promise<{ success: boolean }> {
	return del(`/agent/iotest-presets/${id}`);
}

// ── I/O Trace ──

export interface TraceFilter {
	startTime?: number;
	endTime?: number;
	startLba?: number;
	endLba?: number;
	minDtoc?: number;
	maxDtoc?: number;
	minCtoc?: number;
	maxCtoc?: number;
	minCtod?: number;
	maxCtod?: number;
	minQd?: number;
	maxQd?: number;
	cpuList?: number[];
	cmdList?: string[];
	sizeList?: number[];
	actionList?: string[];

	/**
	 * fsio_* 전용 cross-layer 필터. Attribution 드릴다운이 여기로 흘러
	 * Charts / Statistics / Raw Data / Attribution 이 같은 모수를 본다.
	 * 해당 컬럼이 없는 parquet(ftrace)에서는 서버가 조건을 조용히 skip 한다.
	 */
	commList?: string[];
	pidList?: number[];
	syscallList?: string[];
	fsList?: string[];
	nameList?: string[];
	inoList?: number[];
	lunList?: number[];
	/** "major:minor" (예 "8:0") */
	devList?: string[];
	/** 파일명 부분일치 — 상위 N 밖의 파일을 찾을 때 */
	nameContains?: string;
	/**
	 * io_flags 비트 마스크. **문자열이다** — u64 를 number 로 실으면 2^53 넘는
	 * f2fs 힌트 비트가 조용히 반올림된다.
	 */
	ioFlagsAny?: string;
	ioFlagsAll?: string;
	ioFlagsNone?: string;
}

export interface LatencyStats {
	min: number; max: number; avg: number; stddev: number; median: number;
	p99: number; p999: number; p9999: number; p99999: number; p999999: number;
}

export interface CmdStatsItem {
	cmd: string; count: number; ratio: number;
	dtoc: LatencyStats; ctod: LatencyStats; ctoc: LatencyStats; qd: LatencyStats;
	totalSizeBytes: number; continuousCount: number; continuousRatio: number;
	sendCount: number;
}

export interface LatencyBucket {
	rangeStartMs: number; rangeEndMs: number; count: number;
}

export interface LatencyHistogramItem {
	cmd: string; latencyType: string; buckets: LatencyBucket[];
}

export interface CmdSizeCountItem {
	cmd: string; size: number; count: number;
}

export interface TraceStats {
	totalEvents: number; durationSeconds: number;
	dtoc: LatencyStats; ctod: LatencyStats; ctoc: LatencyStats; qd: LatencyStats;
	cmdStats: CmdStatsItem[];
	latencyHistograms: LatencyHistogramItem[];
	cmdSizeCounts: CmdSizeCountItem[];
	continuousCount: number; continuousRatio: number;
	alignedCount: number; alignedRatio: number;
	readTotalBytes: number; writeTotalBytes: number; discardTotalBytes: number;
	sendCount: number;
	/** UFS management 이벤트 집계 (fsio_ufs 전용, 없으면 빈 배열). */
	mgmtStats: MgmtStatsItem[];
}

/**
 * UFS management 이벤트(Query/TM UPIU, UIC) 집계.
 *
 * 핵심은 `totalTimeMs` — 데이터 전송이 아니라 **링크 점유 시간**이다.
 * durationSeconds 와 비교하면 "관측 기간 중 몇 %" 가 나온다. idle 구간에서는
 * mgmt 가 행의 대부분이라 이 집계가 사실상 유일한 산출물이 된다.
 */
export interface MgmtStatsItem {
	/** 표시 이름 — "Read Descriptor(geometry)" / "DME_HIBER_ENTER" */
	name: string;
	/** "query" | "tm" | "uic" | "other" — UI 그룹핑용 */
	kind: string;
	/** 전체 행 수 (send + complete 양쪽) */
	count: number;
	/** 짝지어져 latency 가 계산된 건수 */
	pairedCount: number;
	/** dtoc 합계(ms) = 링크 점유 시간 */
	totalTimeMs: number;
	dtoc: LatencyStats;
}

/** I/O 귀속 집계 축. */
export type AttributionDim =
	| 'comm' | 'pid' | 'tid' | 'syscall' | 'fs'
	| 'file' | 'ino' | 'flow' | 'cmd' | 'lun' | 'device';

export interface AttributionEntry {
	/** 표시값. 롤업 행은 "(other)", 빈 값은 "(none)" */
	key: string;
	count: number;
	sendCount: number;
	ratio: number;
	readBytes: number;
	writeBytes: number;
	totalBytes: number;
	/** 이 키에 귀속된 총 장치 시간(ms) */
	dtocSumMs: number;
	dtocMaxMs: number;
	/**
	 * ⚠ (other) 롤업 행은 percentile 이 **undefined** 다.
	 * 0 으로 폴백하면 "0ms = 빠름" 으로 읽혀 unknown 의 정반대 의미가 된다 — "—" 로 렌더할 것.
	 */
	dtocAvgMs?: number;
	dtocP50Ms?: number;
	dtocP99Ms?: number;
	/** comm/pid/tid 축에서만 채워짐 */
	distinctFiles?: number;
	isOther: boolean;
}

export interface AttributionGroup {
	dim: AttributionDim;
	entries: AttributionEntry[];
	/** top-N 자르기 **전** 전체 카디널리티 — "전체 N개 중 상위 20개" 표시용 */
	distinctKeys: number;
}

export interface AttributionResult {
	totalEvents: number;
	groups: AttributionGroup[];
	/** parquet 에 컬럼이 없어 건너뛴 축 — 에러가 아니라 "못 했다" 는 알림 */
	unsupportedDims: AttributionDim[];
}

export interface TraceEvent {
	time: number; lba: number; qd: number; cpu: number;
	dtoc: number; ctod: number; ctoc: number;
	cmd: string; size: number; continuous: boolean;
	action: string;  // "send_req"/"complete_rsp" (UFS) or "block_rq_issue"/"block_rq_complete" (Block)
}

export interface TraceRawDataResult {
	jobId: string; totalEvents: number; sampledEvents: number; isSampled: boolean;
	/**
	 * 조회된 잡의 trace_type. 컬럼 세트와 fsio 전용 UI 노출 판단에 쓴다.
	 * 시나리오 경유가 아닌 단독 trace 실행에는 mappings 가 없어 이 값이 유일한 출처다.
	 */
	traceType?: string;
	events: TraceEvent[];
}

export function startTrace(serverId: number, data: {
	deviceId: string; traceType: string; windowSeconds?: number; jobName?: string;
}): Promise<{ jobId: string }> {
	return post(`/agent/trace/start?serverId=${serverId}`, data);
}

export function stopTrace(serverId: number, jobId: string): Promise<{ success: boolean; message: string }> {
	return post(`/agent/trace/${encodeURIComponent(jobId)}/stop?serverId=${serverId}`, {});
}

export function reparseTrace(serverId: number, jobId: string): Promise<{ success: boolean; message: string }> {
	return post(`/agent/trace/${encodeURIComponent(jobId)}/reparse?serverId=${serverId}`, {});
}

export function getTraceResult(serverId: number, data: {
	jobIds: string[]; filter?: TraceFilter; latencyRangesMs?: number[];
}): Promise<{ jobId: string; stats: TraceStats }> {
	return post(`/agent/trace/result?serverId=${serverId}`, data);
}

/**
 * 잡별 시계 정합 상태.
 *
 * 스텝 구간 분할이 **가능한지**와 불가능하면 **왜인지**를 준다. 구간이 안 보일 때
 * "기능이 사라진 것" 처럼 보이면 안 되므로, 화면이 이유를 그대로 인용한다.
 */
export interface ClockSyncOffset {
	offset: number;
	rttSec: number;
	measuredAtSec: number;
	samples: number;
	uncertaintySec: number;
}

export interface ClockSyncInfo {
	usable: boolean;
	reason: string;
	rttThresholdSec: number;
	/** 측정이 아예 없으면 없는 필드 (0 을 "완벽" 으로 오독하지 않도록 서버가 생략한다). */
	uncertaintySec?: number;
	driftSec?: number;
	notFound?: boolean;
	start?: ClockSyncOffset;
	stop?: ClockSyncOffset;
}

export function getTraceClockSync(
	serverId: number,
	jobIds: string[]
): Promise<{ clockSync: Record<string, ClockSyncInfo> }> {
	return post(`/agent/trace/clocksync?serverId=${serverId}`, { jobIds });
}

/**
 * I/O 귀속 집계 — "이 IO 를 누가/무엇이 만들었나".
 *
 * fsio_* 산출물에서만 의미가 있다. ftrace 산출물로 호출하면 대부분의 축이
 * `unsupportedDims` 로 돌아온다 (에러가 아니다).
 */
export function getTraceAttribution(serverId: number, data: {
	jobIds: string[];
	filter?: TraceFilter;
	dims: AttributionDim[];
	topN?: number;
	sortBy?: 'count' | 'bytes' | 'latency';
}): Promise<AttributionResult> {
	return post(`/agent/trace/attribution?serverId=${serverId}`, data);
}

export function getTraceRawData(serverId: number, data: {
	jobIds: string[]; filter?: TraceFilter;
}): Promise<TraceRawDataResult> {
	return post(`/agent/trace/raw?serverId=${serverId}`, data);
}

// ── MinIO Upload ──

export function uploadTrace(serverId: number, data: {
	jobIds: string[]; remotePath: string;
}): Promise<{ success: boolean; message: string; uploadedFiles: string[] }> {
	return post(`/agent/upload/trace?serverId=${serverId}`, data);
}

export function uploadBenchmark(serverId: number, data: {
	jobId: string; remotePath: string;
}): Promise<{ success: boolean; message: string; uploadedFiles: string[] }> {
	return post(`/agent/upload/benchmark?serverId=${serverId}`, data);
}

// ── Job Management ──

export function deleteJob(serverId: number, jobId: string): Promise<{ success: boolean; message: string }> {
	return del(`/agent/jobs/${encodeURIComponent(jobId)}?serverId=${serverId}`);
}

export function cancelJob(serverId: number, jobId: string): Promise<{ success: boolean; message: string }> {
	return post(`/agent/jobs/${encodeURIComponent(jobId)}/cancel?serverId=${serverId}`, {});
}

// ── Screen streaming ──

export function getScreenWebSocketUrl(serverId: number, deviceId: string): string {
	const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
	return `${wsProtocol}//${window.location.host}/api/agent/screen/${encodeURIComponent(deviceId)}?serverId=${serverId}`;
}

// ── Job Executions (DB history) ──

export interface JobExecutionRecord {
	id: number;
	jobId: string;
	serverId: number;
	serverName: string;
	type: string;
	tool?: string;
	jobName?: string;
	deviceIds: string; // JSON string
	state: string;
	config?: string;
	resultSummary?: string;
	scheduledJobId?: number;
	retryAttempt: number;
	errorMessage?: string;
	startedAt?: string;
	completedAt?: string;
	createdAt: string;
	// Trace Archive (옵션 A) — agent 종료 후에도 조회 가능한 영속 상태
	traceRawKey?: string | null;
	traceRawFormat?: string | null;
	traceRawSize?: number | null;
	traceRawUploadedAt?: string | null;
	traceParquetKeys?: string | null; // JSON: {"ufs":[keys], "block":[...]}
	traceParsedAt?: string | null;
	traceParseState?: string | null; // IDLE|UPLOADING|UPLOADED|PARSING|PARSED|PARSE_FAILED
	traceParseError?: string | null;
	// 워크로드 컨텍스트 — 사용자가 남긴 "무엇이 돌았고 왜 이렇게 동작했나" 메모 (규칙 자동 해석 오버라이드)
	workloadNote?: string | null;
	// 이 job 에 연결된 trace job 매핑 (영속화) — 만료된 job 도 기존 trace UI 로 진입 가능
	traceJobs?: TraceJobMapping[] | null;
}

export interface JobExecutionPage {
	content: JobExecutionRecord[];
	totalElements: number;
	totalPages: number;
	page: number;
	size: number;
}

export function fetchExecutions(params: {
	serverId?: number; type?: string; state?: string;
	from?: string; to?: string; page?: number; size?: number;
}): Promise<JobExecutionPage> {
	const q = new URLSearchParams();
	if (params.serverId != null) q.set('serverId', String(params.serverId));
	if (params.type) q.set('type', params.type);
	if (params.state) q.set('state', params.state);
	if (params.from) q.set('from', params.from);
	if (params.to) q.set('to', params.to);
	q.set('page', String(params.page ?? 0));
	q.set('size', String(params.size ?? 30));
	return get(`/agent/executions?${q.toString()}`);
}

export function fetchExecutionByJobId(jobId: string): Promise<JobExecutionRecord> {
	return get(`/agent/executions/by-job-id/${encodeURIComponent(jobId)}`);
}

/** 워크로드 컨텍스트 메모 저장. 빈 문자열이면 규칙 자동 해석으로 되돌린다. */
export function updateWorkloadNote(jobId: string, note: string): Promise<{ success: boolean; workloadNote: string }> {
	return put(`/agent/executions/by-job-id/${encodeURIComponent(jobId)}/workload-note`, { note });
}

export function deleteExecution(id: number): Promise<{ success: boolean }> {
	return del(`/agent/executions/${id}`);
}

export function fetchExecutionStats(serverId?: number): Promise<{ total: number; completed: number; failed: number; successRate: number }> {
	const q = serverId != null ? `?serverId=${serverId}` : '';
	return get(`/agent/executions/stats${q}`);
}

// ── Scheduled Jobs ──

export interface ScheduledJobRecord {
	id: number;
	name: string;
	description?: string;
	enabled: boolean;
	type: string;
	serverId: number;
	deviceIds: string;
	config: string;
	cronExpression: string;
	busyPolicy: string;
	retryCount: number;
	retryDelaySeconds: number;
	notifyOnFailure: boolean;
	notifyOnSuccess: boolean;
	notifyWebhookUrl?: string;
	lastRunAt?: string;
	lastRunStatus?: string;
	nextRunAt?: string;
	createdAt?: string;
	updatedAt?: string;
}

export function fetchSchedules(): Promise<ScheduledJobRecord[]> {
	return get('/agent/schedules');
}

export function createSchedule(data: Record<string, unknown>): Promise<ScheduledJobRecord> {
	return post('/agent/schedules', data);
}

export function updateSchedule(id: number, data: Record<string, unknown>): Promise<ScheduledJobRecord> {
	return put(`/agent/schedules/${id}`, data);
}

export function deleteSchedule(id: number): Promise<{ success: boolean }> {
	return del(`/agent/schedules/${id}`);
}

export function triggerSchedule(id: number): Promise<{ success: boolean; jobId: string }> {
	return post(`/agent/schedules/${id}/trigger`, {});
}

export function toggleScheduleEnabled(id: number): Promise<ScheduledJobRecord> {
	return post(`/agent/schedules/${id}/enable`, {}); // PATCH via post
}

// ── App Macros ──

export interface AppMacro {
	id: number;
	name: string;
	description?: string;
	packageName?: string;
	eventsJson: string;
	deviceWidth?: number;
	deviceHeight?: number;
	createdAt?: string;
	updatedAt?: string;
}

export interface MacroEvent {
	t: number;
	type: string; // "tap" | "tap_element" | "text" | "swipe" | "key" | "wait" | "wait_until" | "screenshot" | "scroll_capture"
	x?: number;
	y?: number;
	x2?: number;
	y2?: number;
	duration?: number;
	keycode?: number;
	seconds?: number;
	waitMethod?: string;
	waitPattern?: string;
	timeout?: number;
	pollInterval?: number;
	name?: string;
	direction?: string;
	maxScrolls?: number;
	scrollPause?: number;
	ocrPattern?: string;
	ocrRegion?: OcrRegion;
	// 요소 기반 탭(tap_element) 셀렉터 + 폴백 좌표(x,y 재사용)
	elementResourceId?: string;
	elementText?: string;
	elementContentDesc?: string;
	// text 이벤트: input text 로 입력할 문자열
	inputText?: string;
	// 패턴 매칭 (동적 콘텐츠 재현)
	elementMatchMode?: string;    // exact | contains | prefix | suffix | regex
	elementIndex?: number;
	elementContainerId?: string;
}

// 요소 기반 시나리오 빌더 — 현재 화면의 uiautomator 요소.
export interface UIElement {
	resourceId: string;
	text: string;
	contentDesc: string;
	class: string;
	clickable: boolean;
	centerX: number;
	centerY: number;
	bounds: [number, number, number, number]; // [x1, y1, x2, y2]
	containerId: string; // 가장 가까운 스크롤 컨테이너 id (자동 채움용)
}

export interface OcrRegion {
	x: number;
	y: number;
	width: number;
	height: number;
}

export function fetchAppMacros(): Promise<AppMacro[]> {
	return get('/agent/app-macros');
}

export function fetchAppMacro(id: number): Promise<AppMacro> {
	return get(`/agent/app-macros/${id}`);
}

export function createAppMacro(data: Omit<AppMacro, 'id' | 'createdAt' | 'updatedAt'>): Promise<AppMacro> {
	return post('/agent/app-macros', data);
}

export function updateAppMacro(id: number, data: Omit<AppMacro, 'id' | 'createdAt' | 'updatedAt'>): Promise<AppMacro> {
	return put(`/agent/app-macros/${id}`, data);
}

export function deleteAppMacro(id: number): Promise<{ success: boolean }> {
	return del(`/agent/app-macros/${id}`);
}

export function duplicateAppMacro(id: number): Promise<AppMacro> {
	return post(`/agent/app-macros/${id}/duplicate`, {});
}

// ── Macro Recording / Replay / OCR ──

export interface InstalledApp {
	packageName: string;
	appName: string;
}

export function listInstalledApps(serverId: number, deviceId: string): Promise<InstalledApp[]> {
	return get(`/agent/macro/installed-apps?serverId=${serverId}&deviceId=${encodeURIComponent(deviceId)}`);
}

export interface CurrentActivity {
	component: string;  // "package/activity" (없으면 빈 문자열)
	package: string;    // 패키지명
	raw: string;        // mCurrentFocus 원본
}

// 현재 포그라운드 activity 조회 — wait_until(activity) 의 waitPattern 자동 채움용.
export function fetchCurrentActivity(serverId: number, deviceId: string): Promise<CurrentActivity> {
	return get(`/agent/macro/current-activity?serverId=${serverId}&deviceId=${encodeURIComponent(deviceId)}`);
}

export function startRecording(serverId: number, deviceId: string): Promise<{ success: boolean; sessionId: string }> {
	return post(`/agent/macro/start-recording?serverId=${serverId}`, { deviceId });
}

export function stopRecording(serverId: number, deviceId: string, sessionId: string): Promise<{
	success: boolean; events: MacroEvent[]; deviceWidth: number; deviceHeight: number;
}> {
	return post(`/agent/macro/stop-recording?serverId=${serverId}`, { deviceId, sessionId });
}

export function replayMacro(serverId: number, data: {
	deviceId: string; events: MacroEvent[]; sourceWidth: number; sourceHeight: number; jobId?: string;
}): Promise<{ success: boolean; message: string; ocrResults: Record<string, string>; metrics: Record<string, number> }> {
	return post(`/agent/macro/replay?serverId=${serverId}`, data);
}

export function takeScreenshot(serverId: number, deviceId: string): Promise<{
	success: boolean; width: number; height: number; imageBase64: string;
}> {
	return post(`/agent/macro/screenshot?serverId=${serverId}`, { deviceId });
}

// 현재 화면의 클릭 가능 요소 목록 (요소 기반 시나리오 빌더용 오버레이).
export function listUiElements(
	serverId: number,
	deviceId: string,
	clickableOnly = true
): Promise<{ success: boolean; deviceWidth: number; deviceHeight: number; elements: UIElement[] }> {
	return get(
		`/agent/macro/ui-elements?serverId=${serverId}&deviceId=${encodeURIComponent(deviceId)}&clickableOnly=${clickableOnly}`
	);
}

export function screenshotOcr(serverId: number, data: {
	deviceId: string; region?: OcrRegion; extractPattern?: string;
}): Promise<{ success: boolean; fullText: string; extractedValue: string; imageBase64: string }> {
	return post(`/agent/macro/ocr?serverId=${serverId}`, data);
}

// ── APK Management ──

export interface BundledApk {
	filename: string;
	sizeBytes: number;
	modifiedAt: string;
}

export function listBundledApks(): Promise<BundledApk[]> {
	return get(`/agent/apks`);
}

export function installApk(data: {
	deviceId: string; apkFilename: string; grantPermissions?: boolean;
}): Promise<{ success: boolean; message: string; packageName: string }> {
	return post(`/agent/apks/install`, data);
}

export function uninstallApk(data: {
	deviceId: string; packageName: string; keepData?: boolean;
}): Promise<{ success: boolean; message: string }> {
	return post(`/agent/apks/uninstall`, data);
}

// ── SSE helpers ──

export function createJobProgressSource(serverId: number, jobId: string): EventSource {
	return new EventSource(`/api/agent/benchmark/progress?serverId=${serverId}&jobId=${encodeURIComponent(jobId)}`);
}

export function createMonitoringSource(serverId: number, deviceIds: string[], interval = 5): EventSource {
	const params = new URLSearchParams();
	params.set('serverId', String(serverId));
	deviceIds.forEach(id => params.append('deviceIds', id));
	params.set('interval', String(interval));
	return new EventSource(`/api/agent/monitoring/stream?${params.toString()}`);
}

// ── Filesystem (standalone 전용) ──
// 로컬 파일 탐색기로 archive / trace 폴더 열기.
//   target='archive'      → $HOME/.agent-standalone/archive/
//   target='trace'        → $HOME/agent_trace/{jobId}/
//   target='archive-job'  → $HOME/.agent-standalone/archive/.../{jobId}/  (검색)
export function openLocalFolder(target: 'archive' | 'trace' | 'archive-job', jobId?: string): Promise<{
	success: boolean;
	path: string;
	message: string;
}> {
	return post('/agent/fs/open', { target, jobId });
}

// Device list SSE — adb.Manager 의 listener 가 push 하는 풀 device 목록.
// 페이지가 connect 직후 + 변경 시 즉시 'event: devices' 수신.
export function createDevicesSource(serverId: number): EventSource {
	return new EventSource(`/api/agent/devices/stream?serverId=${serverId}`);
}

// ── AI (로컬 ollama 기반) ──

export interface AiStatus {
	enabled: boolean;
	reachable: boolean;
	model: string;
	endpoint: string;
}

// AI 활성/도달 가능 여부 조회. reachable=true 일 때만 AI 버튼 노출.
export function getAiStatus(): Promise<AiStatus> {
	return get('/agent/ai/status');
}

// AI 해석 SSE — 명명 이벤트: 'token'(data {text}), 'done'(data {}), 'error'(data {error}).
// kind 는 'trace' 또는 'benchmark'. jobId 로 서버가 통계를 알아서 조달한다.
export function createAiAnalyzeSource(
	serverId: number,
	jobId: string,
	kind: 'trace' | 'benchmark'
): EventSource {
	const params = new URLSearchParams();
	params.set('serverId', String(serverId));
	params.set('jobId', jobId);
	params.set('kind', kind);
	return new EventSource(`/api/agent/ai/analyze/stream?${params.toString()}`);
}

// ── AI 채팅 (멀티턴 + 근거 노출) ──

// 집계 근거. 답변 토큰보다 먼저 도착해 UI 가 뱃지를 먼저 그린다.
export interface AiToolEvidence {
	tool: string;
	params?: Record<string, unknown>;
	query?: string;
	rowCount?: number;
	truncated?: boolean;
	data?: Record<string, unknown>;
	note?: string;
}

export interface AiChatMessage {
	role: 'user' | 'assistant';
	content: string;
	// 이 답변의 근거. 다음 턴에 서버로 되돌려줘야 후속 질문이 맥락을 잃지 않는다
	// (서버는 대화 상태를 보관하지 않는다).
	tool?: string;
	aggJson?: unknown;
}

export interface AiChatHandlers {
	onTool?: (ev: AiToolEvidence) => void;
	onToken?: (text: string) => void;
	onDone?: () => void;
	onError?: (msg: string) => void;
}

// AI 채팅 SSE — POST 라서 EventSource 를 못 쓰고 fetch + ReadableStream 으로 파싱한다.
// (대화 히스토리가 쿼리스트링에 담기지 않는다.)
//
// 반환값을 호출하면 스트림이 중단된다 (시트 닫기 → ollama 요청까지 취소).
export function createAiChatStream(
	serverId: number,
	jobId: string,
	kind: 'trace' | 'benchmark',
	messages: AiChatMessage[],
	handlers: AiChatHandlers
): () => void {
	const controller = new AbortController();

	(async () => {
		try {
			const resp = await fetch(`/api/agent/ai/chat/stream?serverId=${serverId}`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({ jobId, kind, messages }),
				signal: controller.signal
			});

			if (!resp.ok || !resp.body) {
				let msg = `AI 채팅 실패 (${resp.status})`;
				try {
					const j = await resp.json();
					if (j?.error) msg = j.error;
					else if (j?.message) msg = j.message;
				} catch {
					/* body 가 JSON 이 아니면 기본 메시지 */
				}
				handlers.onError?.(msg);
				return;
			}

			const reader = resp.body.getReader();
			const decoder = new TextDecoder();
			let buf = '';

			// SSE 프레임: "event: X\ndata: {...}\n\n"
			for (;;) {
				const { done, value } = await reader.read();
				if (done) break;
				buf += decoder.decode(value, { stream: true });

				let sep: number;
				while ((sep = buf.indexOf('\n\n')) !== -1) {
					const frame = buf.slice(0, sep);
					buf = buf.slice(sep + 2);
					if (!frame.trim() || frame.startsWith(':')) continue; // keepalive

					let event = 'message';
					const dataLines: string[] = [];
					for (const line of frame.split('\n')) {
						if (line.startsWith('event:')) event = line.slice(6).trim();
						else if (line.startsWith('data:')) dataLines.push(line.slice(5).trim());
					}
					if (!dataLines.length) continue;

					let payload: Record<string, unknown> = {};
					try {
						payload = JSON.parse(dataLines.join('\n'));
					} catch {
						continue;
					}

					if (event === 'token') handlers.onToken?.(String(payload.text ?? ''));
					else if (event === 'tool') handlers.onTool?.(payload as unknown as AiToolEvidence);
					else if (event === 'done') handlers.onDone?.();
					else if (event === 'error') handlers.onError?.(String(payload.error ?? 'AI 오류'));
				}
			}
		} catch (e) {
			// abort 는 정상 종료 (사용자가 시트를 닫았거나 새 질문을 보냈다).
			if ((e as Error)?.name === 'AbortError') return;
			handlers.onError?.((e as Error)?.message ?? 'AI 채팅 실패');
		}
	})();

	return () => controller.abort();
}

// 시나리오 wire shape (ScenarioStep). params 는 문자열 맵.
export interface AiScenarioStep {
	type: string;
	tool?: string;
	params?: Record<string, string>;
	macroId?: string;
}

export interface AiScenarioLoop {
	startStep: number;
	endStep: number;
	count: number;
}

export interface GenerateScenarioResult {
	steps: AiScenarioStep[];
	loops: AiScenarioLoop[];
	warnings: string[];
}

// 자연어 → 시나리오 생성. 백엔드 응답 필드명이 최종본과 다를 수 있어 방어적으로 파싱.
// deviceId 를 넘기면 백엔드가 해당 기기의 설치앱/현재 화면을 반영해 더 정확히 생성한다
// (없어도 일반 생성으로 fallback).
export async function generateScenario(prompt: string, deviceId?: string): Promise<GenerateScenarioResult> {
	const body: { prompt: string; deviceId?: string } = { prompt };
	if (deviceId) body.deviceId = deviceId;
	const raw = await post<{
		steps?: AiScenarioStep[];
		loops?: AiScenarioLoop[];
		warnings?: string[];
	}>('/agent/ai/scenario/generate', body);
	return {
		steps: Array.isArray(raw?.steps) ? raw.steps : [],
		// 백엔드는 loop 값을 **문자열**로 보낸다(server/rest_ai.go 의 aiScenarioLoop 은
		// 전부 string). 캔버스는 이 값으로 비교/반복을 하는데 문자열이면 사전순 비교가
		// 되어 두 자리 인덱스에서 깨진다 — "9" <= "10" 은 false 라 loop 노드가 하나도
		// 안 그려진다. TS 선언은 number 라 컴파일러가 못 잡으므로 여기서 변환한다.
		loops: Array.isArray(raw?.loops) ? raw.loops.map(normalizeLoop) : [],
		warnings: Array.isArray(raw?.warnings) ? raw.warnings : []
	};
}

// normalizeLoop — wire(문자열) → 캔버스가 쓰는 숫자.
// 숫자로 못 바꾸는 값은 0 으로 두어 상위 검증에서 걸리게 한다.
function normalizeLoop(l: AiScenarioLoop): AiScenarioLoop {
	const num = (v: unknown): number => {
		const n = Number(v);
		return Number.isFinite(n) ? n : 0;
	};
	return { startStep: num(l?.startStep), endStep: num(l?.endStep), count: num(l?.count) };
}
