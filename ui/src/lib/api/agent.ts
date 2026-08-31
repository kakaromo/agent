import { get, post, put, del, postForm } from './client.js';

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
	/**
	 * 사용자가 분석 화면에서 붙인 이름. 있으면 이걸 우선 표시한다.
	 * label 을 덮어쓰지 않는 이유 — 덮어쓰면 자동 요약이 사라져 되돌릴 수 없다.
	 */
	labelOverride?: string;
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
	/**
	 * 잡 전체에 걸리는 부가 옵션 (step 이 아니라 잡 단위).
	 *
	 *   logcat=on            — logcat 수집 켜기 (on-device AI 측정)
	 *   logcat_tags=A,B      — measure 모드. 없으면 explore(전체 버퍼)
	 *   logcat_profile_id=3  — 저장된 프로파일의 태그를 쓴다 (서버가 풀어준다)
	 *
	 * ⚠ 실측정엔 태그를 반드시 좁힌다. 전체 수집은 그 자체가 IO/CPU 를 써서
	 * 수백 ms 단위 TTFT 를 흔든다.
	 */
	params?: Record<string, string>;
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
	/**
	 * 집계에 실제로 쓰인 행 수(모수).
	 *
	 * optional 인 이유 — 구버전 agent 는 이 필드를 안 보낸다. 그때는 표가 '-' 를
	 * 그려야 한다. **0 으로 채우면 안 된다**: 0 은 "0ms" 도 "없음" 도 아니고
	 * "모름" 인데, 0 을 넣으면 "집계 대상이 없다" 는 틀린 사실이 된다.
	 * totalEvents-sendCount 같은 것으로 **짐작해서도 안 된다** — 필터를 걸면
	 * 실제 모수는 줄어드는데 짐작은 그대로라 조용히 틀린 수가 나온다.
	 */
	count?: number;
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

/**
 * VFS buffered read 의 page-cache 판정 통계 (fsio_read 형제 parquet).
 *
 * ⚠ CACHE_HIT_INFERRED 는 하드웨어 cache hit 이벤트가 아니다. "read 가 도는 동안 FS
 *   page-fill 훅이 한 번도 안 불렸다" 는 **음성 증거** 추론이라, 훅이 안 붙은
 *   파일시스템의 read 는 hit 이 아니라 UNKNOWN 으로 떨어진다.
 */
export interface FsioReadClassStats {
	/** CACHE_HIT_INFERRED | CACHE_MISS | DIRECT_IO | EOF | ERROR | UNKNOWN */
	cacheClass: string;
	requests: number;
	requestedBytes: number;
	/** ⚠ "캐시에 있던 바이트" 가 아니다 — 이 class 로 분류된 request 의 반환량이다. */
	returnedBytes: number;
	/**
	 * dur_ns 가 실린 행 수. 진입을 못 본 exit 는 빠지므로 requests 보다 작을 수 있다.
	 * 0 이면 아래 백분위는 전부 undefined 다.
	 */
	durationSamples: number;
	/** ⚠ 표본이 없으면 **undefined**. 0 으로 폴백하면 "0ns 였다" 로 오독된다 — "—" 로 렌더할 것. */
	durationAvgNs?: number;
	durationP50Ns?: number;
	durationP95Ns?: number;
	durationP99Ns?: number;
}

export interface FsioReadFileStats {
	/** 실제 경로 → "ino:N" → "(meta:<fs>)" → "(unknown)" 폴백 */
	key: string;
	requests: number;
	hitRequests: number;
	missRequests: number;
	unknownRequests: number;
	requestedBytes: number;
	returnedBytes: number;
	/** ⚠ 훅 발화 횟수다. page 수도 byte 수도 아니다. */
	fillUnits: number;
	readaheadRequests: number;
	readaheadUnits: number;
	totalDurationNs: number;
}

/**
 * mmap page fault 요약 — read 통계와 **모집단이 다르다**.
 *
 * ⚠ 적중률 필드가 없는 것은 누락이 아니라 의도다. fault-around 때문에 캐시에 있는
 *   페이지는 fault 를 아예 안 내므로 이 모집단은 사실상 miss 만 모인다. 비율을 만들면
 *   구조적으로 0% 에 가깝게 나오고, 그걸 "mmap 이 캐시를 못 맞춘다" 로 읽으면 완전히
 *   틀린 결론이 된다. 진짜 분모(접근한 페이지 수)는 이 계층에 없다.
 */
export interface FsioReadMmapStats {
	requests: number;
	missRequests: number;
	fillUnits: number;
	durationSamples: number;
	durationAvgNs?: number;
	durationP50Ns?: number;
	durationP99Ns?: number;
}

export interface FsioReadStatsResult {
	totalRequests: number;
	byClass: FsioReadClassStats[];
	/**
	 * ⚠ 분모(hit+miss)가 0 이면 **undefined** 다. 0 으로 폴백하면 "전부 miss" 와
	 * "판정할 게 없음" 이 구분되지 않는다. 분모에서 DIRECT_IO/EOF/ERROR/UNKNOWN 은 제외.
	 */
	requestHitRatio?: number;
	requestMissRatio?: number;
	unknownRatio?: number;
	fillUnits: number;
	readaheadRequests: number;
	readaheadUnits: number;
	syncRaUnits: number;
	shortReads: number;
	durationUnknown: number;
	topFiles: FsioReadFileStats[];
	/** 수집 품질 경고. **숨기지 말 것** — 근거가 부족한 걸 모르고 hit ratio 를 읽으면 위험하다. */
	qualityWarnings: string[];
	schemaVersion: string;
	/** mmap page fault — 위 값들에서 **제외된** 별도 모집단. 없으면 undefined. */
	mmap?: FsioReadMmapStats;
}

export interface TraceEvent {
	time: number; lba: number; qd: number; cpu: number;
	dtoc: number; ctod: number; ctoc: number;
	cmd: string; size: number; continuous: boolean;
	action: string;  // "send_req"/"complete_rsp" (UFS) or "block_rq_issue"/"block_rq_complete" (Block)

	// ── 아래는 fsio_*(bpftrace) 산출물에만 채워진다. ftrace 잡에서는 undefined. ──
	//
	// 서버는 예전부터 이 값들을 다 보내고 있었는데 여기 타입이 11개 필드에서 멈춰
	// 있었다. 런타임엔 그대로 들어오니 표는 그려지지만, TS 로는 존재하지 않는
	// 필드라 "백엔드가 안 보내나" 로 오해하기 쉽다. 계약을 실제 응답에 맞춘다.
	aligned?: boolean;
	line_number?: number;
	pid?: number;
	tid?: number;
	comm?: string;
	process?: string;   // comm 별칭 (표의 process 컬럼)
	syscall?: string;
	fs?: string;
	ino?: number;
	name?: string;      // 풀패스 / "ino:N" / "(라벨)"
	io_flags?: string;  // u64 라 문자열로 온다 (JSON number 는 2^53 초과분이 깨진다)

	// fsio_ufs 전용
	tag?: number; opcode?: number; lun?: number; groupid?: number; hwqid?: number;
	txn?: number; upiu_flags?: number; upiu_func?: number; upiu_attr?: string; upiu_cp?: number;
	is_mgmt?: boolean; mgmt_name?: string;
	upiu_resp?: number; upiu_status?: number;
	query_opcode?: number; query_idn?: number; query_index?: number; query_selector?: number;
	uic_cmd?: number;

	// fsio_block 전용
	devmajor?: number; devminor?: number; rwbs?: string; flags?: string; extra?: number; sector?: number;

	// 미완결 IO — 이 행의 dtoc 0 은 "0ms" 가 아니라 "모름" 이다.
	is_unfinished?: boolean;
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
	/** fsio_* 에서 VFS 레이어도 수집 — page cache hit/miss·mmap 통계에 필요. */
	includeVfs?: boolean;
}): Promise<{ jobId: string }> {
	return post(`/agent/trace/start?serverId=${serverId}`, data);
}

/**
 * 파일 업로드 → 포맷 자동 판별 → 파싱/저장.
 *
 * 포맷은 서버가 내용으로 정한다 — 사용자가 trace_type 을 고르게 하면 잘못 골랐을 때
 * 파서가 **에러 없이 0건**을 내서 "수집은 됐는데 비어 있다" 로 보인다.
 * 판별 실패는 에러로 돌아온다(추측해서 진행하지 않는다).
 */
export function uploadFile(file: File, name?: string): Promise<{
	kind: 'trace' | 'benchmark';
	reason: string;
	name: string;
	/** kind==='trace' */
	jobId?: string;
	traceType?: string;
	/** kind==='benchmark' */
	path?: string;
	deviceId?: string;
	tool?: string;
}> {
	const fd = new FormData();
	fd.append('file', file);
	if (name) fd.append('name', name);
	return postForm('/agent/upload/file', fd);
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

/**
 * page-cache 통계 — VFS buffered read 가 캐시를 맞췄나.
 *
 * fsio_read 형제 parquet 이 없으면 **에러가 아니라 totalRequests=0** 이 온다
 * (ftrace 계열·구버전 수집엔 애초에 없는 산출물이다). 호출부는 그걸로 탭을 숨긴다.
 */
export function getFsioReadStats(serverId: number, data: {
	jobIds: string[];
	filter?: TraceFilter;
	topN?: number;
}): Promise<FsioReadStatsResult> {
	return post(`/agent/trace/fsio-read-stats?serverId=${serverId}`, data);
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
	// 스텝 구간 (영속화) — 만료된 job 도 Behavior 탭을 볼 수 있게. traceJobs 와 같은 이유.
	stepBoundaries?: StepBoundary[] | null;
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


/**
 * 구간을 화면에 표시할 이름.
 *
 * 사용자가 붙인 이름(labelOverride) → agent 자동 요약(label) → 타입 순.
 * ⚠ 라벨을 그리는 모든 곳이 이 함수를 써야 한다 — 한 곳이라도 label 을 직접
 * 읽으면 바꾼 이름이 그 화면에만 반영 안 돼서 "저장이 안 됐나" 로 보인다.
 */
export function boundaryLabel(b: StepBoundary): string {
	return (b.labelOverride ?? '').trim() || b.label || b.type;
}

/**
 * 구간 이름 저장. 서버가 step_boundaries JSON 에서 해당 항목만 고친다.
 * 빈 문자열이면 override 해제 = 원래 이름 복귀.
 */
export function setBoundaryLabel(
	jobId: string,
	b: { stepIndex: number; loopIndex: number; repeatIndex: number },
	labelOverride: string
): Promise<{ success: boolean }> {
	return put(`/agent/executions/by-job-id/${encodeURIComponent(jobId)}/boundary-label`, {
		stepIndex: b.stepIndex,
		loopIndex: b.loopIndex,
		repeatIndex: b.repeatIndex,
		labelOverride
	});
}

// ── AI Log Profiles (on-device AI logcat 패턴) ──
//
// 런타임이 logcat 에 찍는 문구에서 TTFT/TPOT 를 뽑기 위한 정규식 묶음.
// AP·세트·런타임 버전마다 문자열이 달라 코드에 박을 수 없어 프리셋으로 둔다.

export interface AILogProfile {
	id: number;
	name: string;
	description?: string;
	/**
	 * 어디서 읽는 패턴인가 — "logcat"(기본) 또는 "marker".
	 *
	 * ⚠ 두 소스는 patternsJson 의 **필드 이름이 다르다**(marks/series vs
	 * counters/sections). 섞으면 조용히 0건이 되므로 서버가 파싱 전에 막는다.
	 */
	source?: string;
	runtime: string;
	soc?: string;
	patternsJson: string;
	createdAt?: string;
	updatedAt?: string;
}

/** mark — 걸린 줄의 **시각**만 써서 구간 경계로 쓴다 (예: `prefill begin`). */
export interface AILogMark {
	key: string;
	regex: string;
}

/** series — 캡처 그룹에서 **숫자**를 뽑는다. 같은 키가 반복되면 시계열이 된다. */
export interface AILogSeries {
	key: string;
	regex: string;
	unit?: string;
}

export interface AILogPatterns {
	tags?: string[];
	minPriority?: string;
	marks?: AILogMark[];
	series?: AILogSeries[];
}

export function fetchAILogProfiles(params?: { runtime?: string; soc?: string }): Promise<AILogProfile[]> {
	const q = new URLSearchParams();
	if (params?.runtime) q.set('runtime', params.runtime);
	if (params?.soc) q.set('soc', params.soc);
	const qs = q.toString();
	return get(`/agent/ai-log-profiles${qs ? `?${qs}` : ''}`);
}

export function createAILogProfile(data: Omit<AILogProfile, 'id' | 'createdAt' | 'updatedAt'>): Promise<AILogProfile> {
	return post('/agent/ai-log-profiles', data);
}

export function updateAILogProfile(id: number, data: Omit<AILogProfile, 'id' | 'createdAt' | 'updatedAt'>): Promise<AILogProfile> {
	return put(`/agent/ai-log-profiles/${id}`, data);
}

export function deleteAILogProfile(id: number): Promise<{ success: boolean }> {
	return del(`/agent/ai-log-profiles/${id}`);
}

// ── Logcat 탐색 / 파싱 ──

export interface LogcatExploreCandidate {
	tag: string;
	pids: number[];
	lines: number;
	unitHits: number;
	keywordHits: number;
	/** LLM 고유 신호(tok/s·ms/tok·TTFT·prefill/decode)가 걸린 줄 수. 가장 믿을 만하다. */
	strongHits: number;
	/** 유휴 구간엔 없고 추론 구간에만 나타났는가. */
	onlyDuringRun: boolean;
	samples: string[];
	score: number;
}

export interface LogcatExploreResult {
	totalLines: number;
	parsedLines: number;
	distinctTags: number;
	candidates: LogcatExploreCandidate[];
	/**
	 * 후보는 있으나 LLM 고유 신호가 **하나도 없다.**
	 * 낱말만 겹치는 다른 온디바이스 ML(음성 wakeword·얼굴인식 등)일 수 있다는 뜻.
	 * 화면에서 반드시 눈에 띄게 구분해야 한다 — 목록이 있다는 것만으로 답이 있다고
	 * 읽히면 안 된다.
	 */
	weakOnly: boolean;
	diagnosis: string[];
}

export interface LogcatExploreRequest {
	jobId?: string;
	path?: string;
	idleFrom?: number;
	idleTo?: number;
	runFrom?: number;
	runTo?: number;
}

export function exploreLogcat(req: LogcatExploreRequest): Promise<{ path: string; result: LogcatExploreResult }> {
	return post('/agent/logcat/explore', req);
}

export interface LogcatSeriesPoint {
	timeSec: number;
	value: number;
	raw: string;
}

export interface LogcatSeriesResult {
	key: string;
	unit?: string;
	points: LogcatSeriesPoint[];
	count: number;
	min: number;
	max: number;
	mean: number;
	median: number;
	p99: number;
}

export interface LogcatMarkHit {
	key: string;
	timeSec: number;
	tag: string;
	raw: string;
}

export interface LogcatPatternStat {
	key: string;
	kind: string;
	hits: number;
	/** 정규식은 맞았는데 캡처 값이 숫자가 아니었던 횟수 — 캡처 그룹 위치 문제다. */
	parseFailures: number;
}

export interface LogcatParseResult {
	totalLines: number;
	parsedLines: number;
	matchedTags: string[];
	marks: LogcatMarkHit[];
	series: Record<string, LogcatSeriesResult>;
	stats: LogcatPatternStat[];
	totalHits: number;
	/** 패턴 일부만 맞았다 — 반쪽 지표를 정상으로 읽으면 안 된다. */
	partial: boolean;
	missingKeys: string[];
	diagnosis: string[];
}

export interface LogcatParseRequest {
	jobId?: string;
	path?: string;
	profileId?: number;
	patternsJson?: string;
}

export function parseLogcat(req: LogcatParseRequest): Promise<{ path: string; result: LogcatParseResult }> {
	return post('/agent/logcat/parse', req);
}


// ── trace_marker 기반 AI 지표 (logcat 의 자매 경로) ──
//
// ⚠ logcat 과 **소스만 다르고 계약은 같다.** 결과 타입(LogcatParseResult)도 공유하므로
// 측정 화면은 두 소스를 같은 코드로 그린다.
//
// 왜 두 경로가 필요한가: 런타임이 **stderr 로 뱉으면 logcat 에 안 남는다**
// (llama.cpp `llama_print_timings()`). trace_marker 는 파일 write 라 그 제약이 없고,
// IO 트레이스와 같은 clock 이라 축 변환도 필요 없다.

/** marker 패턴 — logcat 과 달리 **캡처 그룹이 필요 없다** (`C|이름|값` 이라 값이 이미 분리). */
export interface MarkerPatterns {
	/** `C|pid|<name>|<value>` 에서 값을 뽑을 카운터. */
	counters?: MarkerCounter[];
	/** `B|pid|<name>` 구간. 시작 시각을 mark 로 쓴다. */
	sections?: MarkerSection[];
}

export interface MarkerCounter {
	key: string;
	/** 기기가 찍는 이름. 정확히 일치하면 이것만으로 충분하다. */
	name?: string;
	/** 이름이 버전마다 다를 때. name 이 비면 이쪽을 쓴다. 캡처 그룹 불필요. */
	regex?: string;
	unit?: string;
}

export interface MarkerSection {
	key: string;
	name?: string;
	regex?: string;
}

export interface MarkerCandidate {
	name: string;
	/** "counter"(C|, 값 있음) 또는 "section"(B|, 구간). */
	kind: string;
	count: number;
	pids: number[];
	/** 숫자값이 실렸는가. 지표라면 여기가 true 다. */
	hasValue: boolean;
	min: number;
	max: number;
	/** 이름에 LLM 고유 어휘(토큰·prefill/decode·TTFT)가 있는가. */
	llmSignal: boolean;
	onlyDuringRun: boolean;
	samples: string[];
	score: number;
}

export interface MarkerExploreResult {
	totalLines: number;
	markerLines: number;
	distinctNames: number;
	candidates: MarkerCandidate[];
	/** LLM 고유 신호가 **하나도 없다** — logcat 의 weakOnly 와 같은 경고다. */
	weakOnly: boolean;
	diagnosis: string[];
}

export interface MarkerExploreRequest {
	/** trace 잡 ID. ⚠ path 는 받지 않는다 (임의 경로 노출 방지). */
	traceJobId: string;
	idleFrom?: number;
	idleTo?: number;
	runFrom?: number;
	runTo?: number;
}

export function exploreMarkers(req: MarkerExploreRequest): Promise<{ path: string; result: MarkerExploreResult }> {
	return post('/agent/marker/explore', req);
}

export interface MarkerParseRequest {
	traceJobId: string;
	profileId?: number;
	patternsJson?: string | MarkerPatterns;
}

/** 결과 타입은 logcat 과 같다 — 측정 화면을 공유하기 위해서다. */
export function parseMarkers(req: MarkerParseRequest): Promise<{ path: string; result: LogcatParseResult }> {
	return post('/agent/marker/parse', req);
}
