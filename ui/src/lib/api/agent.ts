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
}

export interface TraceEvent {
	time: number; lba: number; qd: number; cpu: number;
	dtoc: number; ctod: number; ctoc: number;
	cmd: string; size: number; continuous: boolean;
	action: string;  // "send_req"/"complete_rsp" (UFS) or "block_rq_issue"/"block_rq_complete" (Block)
}

export interface TraceRawDataResult {
	jobId: string; totalEvents: number; sampledEvents: number; isSampled: boolean;
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
