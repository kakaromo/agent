<script lang="ts">
	import {
		fetchAgentServers,
		fetchDevices,
		getJobStatus,
		createJobProgressSource,
		createMonitoringSource,
		type AgentServer,
		type Device,
		type JobProgress,
		type DeviceMetricsData,
		fetchExecutions
	} from '$lib/api/agent.js';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';
	import AgentContextPanel from './AgentContextPanel.svelte';
	import AgentBenchmarkForm from './AgentBenchmarkForm.svelte';
	import AgentScenarioBuilder from './AgentScenarioBuilder.svelte';
	import AgentResultsView from './AgentResultsView.svelte';
	import AgentFloatingJobCard from './AgentFloatingJobCard.svelte';
	import AgentServerSheet from './AgentServerSheet.svelte';
	import AgentMonitoringSheet from './AgentMonitoringSheet.svelte';
	import AgentResultDetailSheet from './AgentResultDetailSheet.svelte';
	import ScenarioCanvas from './scenario-canvas/ScenarioCanvas.svelte';
	import AgentTraceForm from './AgentTraceForm.svelte';
	import AgentTraceResultSheet from './AgentTraceResultSheet.svelte';
	import AgentScreenSheet from './AgentScreenSheet.svelte';
	import TerminalDialog from '$lib/components/remote/TerminalDialog.svelte';
	import AgentScheduleView from './AgentScheduleView.svelte';
	import AgentMacroRecorder from './AgentMacroRecorder.svelte';
	import IOTestForm from './iotest/IOTestForm.svelte';

	// ── Types (re-export from types.ts) ──
	import type { ActiveJob, JobRecord } from './types.js';
	export type { ActiveJob, JobRecord };

	// ── Global state ──

	let servers = $state<AgentServer[]>([]);
	let selectedServerId = $state<number | null>(null);
	let devices = $state<Device[]>([]);
	let loadingDevices = $state(false);
	let selectedDeviceIds = $state<Set<string>>(new Set());
	let centerMode = $state<'benchmark' | 'scenario' | 'trace' | 'results' | 'schedule' | 'macro' | 'iotest'>('results');
	let activeJobs = $state<ActiveJob[]>([]);
	let jobHistory = $state<JobRecord[]>([]);

	// Active trace state (survives mode switch)
	let activeTraceJobId = $state<string | null>(null);

	// Sheet state
	let serverSheetOpen = $state(false);
	let monitoringSheetOpen = $state(false);
	let monitoringDeviceId = $state<string | null>(null);
	let resultDetailSheetOpen = $state(false);
	let viewingJobId = $state<string | null>(null);
	let viewingServerId = $state<number | null>(null);

	// Screen share sheet
	let screenSheetOpen = $state(false);
	let screenDeviceId = $state<string | null>(null);
	let macroScreenJobId = $state<string | null>(null); // macro 모드로 자동 열린 job 추적

	// Trace result sheet
	let traceSheetOpen = $state(false);
	let traceJobIds = $state<string[]>([]);
	let traceDeviceId = $state<string | null>(null);

	// ── Storage Info (선택된 모든 디바이스의 df /data 정보) ──
	let storageMetricsMap = $state<Map<string, DeviceMetricsData>>(new Map());
	let storageEventSource: EventSource | null = null;

	function connectStorageSSE() {
		closeStorageSSE();
		if (selectedServerId == null || selectedDeviceIds.size === 0) return;
		const deviceIds = [...selectedDeviceIds];
		storageEventSource = createMonitoringSource(selectedServerId, deviceIds, 2);
		storageEventSource.addEventListener('metrics', (e: MessageEvent) => {
			const m: DeviceMetricsData = JSON.parse(e.data);
			storageMetricsMap = new Map(storageMetricsMap).set(m.deviceId, m);
		});
		storageEventSource.onerror = () => {
			closeStorageSSE();
			setTimeout(() => connectStorageSSE(), 5000);
		};
	}

	function closeStorageSSE() {
		if (storageEventSource) { storageEventSource.close(); storageEventSource = null; }
	}

	// 디바이스 선택 변경 시 자동 시작
	$effect(() => {
		if (selectedServerId != null && selectedDeviceIds.size > 0) {
			connectStorageSSE();
		} else {
			closeStorageSSE();
			storageMetricsMap = new Map();
		}
		return () => closeStorageSSE();
	});

	// ── Monitoring (page-level, survives sheet close) ──
	const MAX_MONITOR_POINTS = 120;
	let monitoringActive = $state(false);
	let monitorEventSource: EventSource | null = null;
	let monitorReconnectTimer: ReturnType<typeof setTimeout> | null = null;
	let monitorUserStopped = $state(false);
	let cpuHistory = $state<[number, number][]>([]);
	let memHistory = $state<[number, number][]>([]);
	let diskReadHistory = $state<[number, number][]>([]);
	let diskWriteHistory = $state<[number, number][]>([]);
	let latestMetrics = $state<DeviceMetricsData | null>(null);

	function connectMonitorSSE() {
		if (selectedServerId == null || !monitoringDeviceId) return;
		closeMonitorSSE();

		monitorEventSource = createMonitoringSource(selectedServerId, [monitoringDeviceId], 5);
		monitorEventSource.addEventListener('metrics', (e: MessageEvent) => {
			const m: DeviceMetricsData = JSON.parse(e.data);
			latestMetrics = m;
			const ts = m.timestamp || Date.now();
			if (m.cpu) cpuHistory = [...cpuHistory, [ts, m.cpu.usagePercent]].slice(-MAX_MONITOR_POINTS);
			if (m.memory) memHistory = [...memHistory, [ts, m.memory.usagePercent]].slice(-MAX_MONITOR_POINTS);
			if (m.disk) {
				diskReadHistory = [...diskReadHistory, [ts, m.disk.readIos]].slice(-MAX_MONITOR_POINTS);
				diskWriteHistory = [...diskWriteHistory, [ts, m.disk.writeIos]].slice(-MAX_MONITOR_POINTS);
			}
		});
		monitorEventSource.onerror = () => {
			closeMonitorSSE();
			if (!monitorUserStopped) {
				monitorReconnectTimer = setTimeout(() => connectMonitorSSE(), 3000);
			}
		};
		monitoringActive = true;
	}

	function closeMonitorSSE() {
		if (monitorEventSource) { monitorEventSource.close(); monitorEventSource = null; }
		if (monitorReconnectTimer) { clearTimeout(monitorReconnectTimer); monitorReconnectTimer = null; }
	}

	function startMonitoring() {
		monitorUserStopped = false;
		cpuHistory = []; memHistory = []; diskReadHistory = []; diskWriteHistory = [];
		latestMetrics = null;
		connectMonitorSSE();
	}

	function stopMonitoring() {
		monitorUserStopped = true;
		closeMonitorSSE();
		monitoringActive = false;
	}

	// ── Initialization ──

	// Load from localStorage
	function loadFromStorage() {
		try {
			const lastServer = localStorage.getItem('agent:lastServerId');
			if (lastServer) selectedServerId = Number(lastServer);

			const savedHistory = localStorage.getItem('agent:jobHistory');
			if (savedHistory) jobHistory = JSON.parse(savedHistory);
		} catch { /* ignore */ }
	}

	function saveHistory() {
		try {
			const trimmed = jobHistory.slice(0, 100);
			localStorage.setItem('agent:jobHistory', JSON.stringify(trimmed));
		} catch { /* ignore */ }
	}

	loadFromStorage();
	restoreRunningJobs();
	restoreFromDB();

	/**
	 * 페이지 로드 시 running 상태의 job을 복원.
	 * 먼저 GetJobStatus로 실제 상태 확인 → 아직 running이면 SSE 재구독.
	 */
	async function restoreRunningJobs() {
		const runningJobs = jobHistory.filter(j => j.state === 'running');
		for (const record of runningJobs) {
			// 먼저 실제 상태 확인
			try {
				const status = await getJobStatus(record.serverId, record.jobId);
				const terminal = ['completed', 'failed', 'partially_failed', 'cancelled'].includes(status.state);

				if (terminal) {
					// 이미 완료/실패 — 상태만 업데이트
					jobHistory = jobHistory.map(j =>
						j.jobId === record.jobId ? { ...j, state: status.state } : j
					);
					saveHistory();
					continue;
				}
			} catch {
				// job 조회 실패 (Go Agent 재시작 등) — failed로 마킹하여 재시도 방지
				jobHistory = jobHistory.map(j =>
					j.jobId === record.jobId ? { ...j, state: 'failed' } : j
				);
				saveHistory();
				continue;
			}

			// Trace job은 SSE 없이 상태만 복원 (벤치마크 SSE는 trace에 적용 불가)
			if (record.type === 'trace') {
				activeTraceJobId = record.jobId;
				// Floating card에도 표시
				const activeJob: ActiveJob = {
					jobId: record.jobId,
					serverId: record.serverId,
					serverName: record.serverName,
					type: record.type,
					tool: record.tool,
					jobName: record.jobName,
					deviceIds: record.deviceIds,
					createdAt: record.createdAt,
					events: [],
					state: 'running'
				};
				activeJobs = [...activeJobs, activeJob];
				continue;
			}

			// 벤치마크/시나리오 — SSE 재구독
			const activeJob: ActiveJob = {
				jobId: record.jobId,
				serverId: record.serverId,
				serverName: record.serverName,
				type: record.type,
				tool: record.tool,
				jobName: record.jobName,
				deviceIds: record.deviceIds,
				createdAt: record.createdAt,
				events: [],
				state: 'running'
			};

			const es = createJobProgressSource(record.serverId, record.jobId);
			activeJob.eventSource = es;

			es.addEventListener('progress', (e: MessageEvent) => {
				const p: JobProgress = JSON.parse(e.data);
				activeJobs = activeJobs.map(j =>
					j.jobId === record.jobId ? { ...j, events: [...j.events, p] } : j
				);
			});

			es.addEventListener('complete', () => {
				es.close();
				activeJobs = activeJobs.map(j =>
					j.jobId === record.jobId ? { ...j, state: 'completed', eventSource: undefined } : j
				);
				jobHistory = jobHistory.map(j =>
					j.jobId === record.jobId ? { ...j, state: 'completed' } : j
				);
				saveHistory();
			});

			es.addEventListener('error', () => {
				es.close();
				activeJobs = activeJobs.map(j =>
					j.jobId === record.jobId ? { ...j, state: 'failed', eventSource: undefined } : j
				);
				jobHistory = jobHistory.map(j =>
					j.jobId === record.jobId ? { ...j, state: 'failed' } : j
				);
				saveHistory();
			});

			activeJobs = [...activeJobs, activeJob];
		}
	}

	/**
	 * DB에서 running 상태 job을 조회 → SSE 구독 (다른 브라우저/컴퓨터에서도 진행 상황 확인)
	 */
	async function restoreFromDB() {
		try {
			const res = await fetchExecutions({ state: 'running', size: 20 });
			for (const exec of res.content) {
				// 이미 activeJobs에 있으면 스킵
				if (activeJobs.some(j => j.jobId === exec.jobId)) continue;

				// 실제 상태 확인
				try {
					const status = await getJobStatus(exec.serverId, exec.jobId);
					const terminal = ['completed', 'failed', 'partially_failed', 'cancelled'].includes(status.state);
					if (terminal) continue;
				} catch {
					// Go Agent에서 job을 찾을 수 없음 — 이미 정리됨, 스킵
					continue;
				}

				// trace job은 SSE 없이 표시
				if (exec.type === 'trace') {
					activeTraceJobId = exec.jobId;
					activeJobs = [...activeJobs, {
						jobId: exec.jobId, serverId: exec.serverId, serverName: exec.serverName ?? '',
						type: 'trace', tool: exec.tool, jobName: exec.jobName,
						deviceIds: parseDeviceIds(exec.deviceIds), createdAt: Date.now(),
						events: [], state: 'running'
					}];
					continue;
				}

				// benchmark/scenario — SSE 구독
				const activeJob: ActiveJob = {
					jobId: exec.jobId, serverId: exec.serverId, serverName: exec.serverName ?? '',
					type: exec.type as 'benchmark' | 'scenario', tool: exec.tool, jobName: exec.jobName,
					deviceIds: parseDeviceIds(exec.deviceIds), createdAt: Date.now(),
					events: [], state: 'running'
				};

				const es = createJobProgressSource(exec.serverId, exec.jobId);
				activeJob.eventSource = es;

				es.addEventListener('progress', (e: MessageEvent) => {
					const p: JobProgress = JSON.parse(e.data);
					activeJobs = activeJobs.map(j =>
						j.jobId === exec.jobId ? { ...j, events: [...j.events, p] } : j
					);
				});
				es.addEventListener('complete', () => {
					es.close();
					activeJobs = activeJobs.map(j =>
						j.jobId === exec.jobId ? { ...j, state: 'completed', eventSource: undefined } : j
					);
				});
				es.addEventListener('error', () => {
					es.close();
					// SSE 에러 시 재시도 (1회) — 타이밍 이슈로 실패할 수 있음
					setTimeout(async () => {
						try {
							const status = await getJobStatus(exec.serverId, exec.jobId);
							if (['completed', 'failed', 'partially_failed', 'cancelled'].includes(status.state)) {
								activeJobs = activeJobs.map(j =>
									j.jobId === exec.jobId ? { ...j, state: status.state as any, eventSource: undefined } : j
								);
							}
							// 아직 running이면 무시 (다음 페이지 로드 시 재시도)
						} catch {
							activeJobs = activeJobs.map(j =>
								j.jobId === exec.jobId ? { ...j, state: 'failed', eventSource: undefined } : j
							);
						}
					}, 2000);
				});

				activeJobs = [...activeJobs, activeJob];

				// localStorage에도 추가 (이 브라우저에서 복원 가능하도록)
				if (!jobHistory.some(j => j.jobId === exec.jobId)) {
					jobHistory = [{
						jobId: exec.jobId, serverId: exec.serverId, serverName: exec.serverName ?? '',
						type: exec.type as 'benchmark' | 'scenario' | 'trace',
						tool: exec.tool, jobName: exec.jobName,
						deviceIds: parseDeviceIds(exec.deviceIds),
						state: 'running', createdAt: Date.now()
					}, ...jobHistory];
					saveHistory();
				}
			}
		} catch { /* DB 조회 실패 무시 */ }
	}

	function parseDeviceIds(json: string | null): string[] {
		if (!json) return [];
		try { return JSON.parse(json); } catch { return []; }
	}

	function jobExecutionService_updateState(jobId: string, state: string) {
		// 간단한 상태 업데이트 (별도 API 없으므로 무시 — 서버에서 SSE로 처리됨)
	}

	async function loadServers() {
		try {
			servers = await fetchAgentServers();
			// Auto-select first enabled if no saved selection
			if (selectedServerId == null || !servers.some(s => s.id === selectedServerId)) {
				const first = servers.find(s => s.enabled);
				if (first) selectedServerId = first.id;
			}
		} catch {
			servers = [];
		}
	}

	async function loadDevices() {
		if (selectedServerId == null) { devices = []; return; }
		loadingDevices = true;
		try {
			const res = await fetchDevices(selectedServerId);
			devices = res.devices;
		} catch {
			devices = [];
		} finally {
			loadingDevices = false;
		}
	}

	loadServers();

	// Auto-load devices when server changes + SSE 로 USB 연결/끊김 즉시 반영.
	// SSE 가 연결되면 server 가 'devices' 이벤트로 풀 목록을 push → polling 불필요.
	$effect(() => {
		if (selectedServerId != null) {
			localStorage.setItem('agent:lastServerId', String(selectedServerId));
			loadDevices();
			selectedDeviceIds = new Set();

			// SSE subscription — adb.Manager.AddDeviceChangeListener 와 연동
			const sid = selectedServerId;
			const url = `/api/agent/devices/stream?serverId=${sid}`;
			const es = new EventSource(url);
			es.addEventListener('devices', (e: MessageEvent) => {
				if (selectedServerId !== sid) return; // 그 사이 서버 바뀌었으면 무시
				try {
					const data = JSON.parse(e.data);
					devices = data.devices ?? [];
				} catch { /* ignore */ }
			});
			// onerror 는 자동 재연결되므로 명시적 처리 안 함.
			return () => es.close();
		}
	});

	let enabledServers = $derived(servers.filter(s => s.enabled));

	// ── Job management ──

	function startJob(job: Omit<ActiveJob, 'events' | 'state' | 'eventSource'>) {
		const activeJob: ActiveJob = {
			...job,
			events: [],
			state: 'running'
		};

		// SSE subscription
		const es = createJobProgressSource(job.serverId, job.jobId);
		activeJob.eventSource = es;

		es.addEventListener('progress', (e: MessageEvent) => {
			const p: JobProgress = JSON.parse(e.data);
			activeJobs = activeJobs.map(j =>
				j.jobId === job.jobId ? { ...j, events: [...j.events, p] } : j
			);
			// TRACE_SKIPPED 감지
			if (p.message?.includes('TRACE_SKIPPED')) {
				toast.warning('Trace가 이미 실행 중이어서 해당 step의 trace가 무시되었습니다');
			}
		});

		es.addEventListener('complete', () => {
			es.close();
			activeJobs = activeJobs.map(j =>
				j.jobId === job.jobId ? { ...j, state: 'completed', eventSource: undefined } : j
			);
			jobHistory = jobHistory.map(j =>
				j.jobId === job.jobId ? { ...j, state: 'completed' } : j
			);
			if (activeTraceJobId === job.jobId) activeTraceJobId = null;
			// macro 모드로 열린 화면 자동 닫기
			if (macroScreenJobId === job.jobId) {
				screenSheetOpen = false;
				macroScreenJobId = null;
			}
			saveHistory();
		});

		es.addEventListener('error', () => {
			es.close();
			activeJobs = activeJobs.map(j =>
				j.jobId === job.jobId ? { ...j, state: 'failed', eventSource: undefined } : j
			);
			jobHistory = jobHistory.map(j =>
				j.jobId === job.jobId ? { ...j, state: 'failed' } : j
			);
			if (activeTraceJobId === job.jobId) activeTraceJobId = null;
			// macro 모드로 열린 화면 자동 닫기
			if (macroScreenJobId === job.jobId) {
				screenSheetOpen = false;
				macroScreenJobId = null;
			}
			saveHistory();
		});

		activeJobs = [...activeJobs, activeJob];

		// app_macro 관련 실행 시에만 디바이스 화면 자동 열기
		if (centerMode === 'macro' && job.deviceIds.length > 0) {
			const firstDevice = job.deviceIds[0];
			if (!screenSheetOpen || screenDeviceId !== firstDevice) {
				screenDeviceId = firstDevice;
				screenSheetOpen = true;
			}
			macroScreenJobId = job.jobId;
		}

		jobHistory = [{
			jobId: job.jobId,
			serverId: job.serverId,
			serverName: job.serverName,
			type: job.type,
			tool: job.tool,
			jobName: job.jobName,
			deviceIds: job.deviceIds,
			state: 'running',
			createdAt: job.createdAt
		}, ...jobHistory];
		saveHistory();
	}

	function dismissJob(jobId: string) {
		const job = activeJobs.find(j => j.jobId === jobId);
		if (job?.eventSource) job.eventSource.close();
		activeJobs = activeJobs.filter(j => j.jobId !== jobId);
	}

	function deleteJobFromHistory(jobId: string) {
		dismissJob(jobId);
		jobHistory = jobHistory.filter(j => j.jobId !== jobId);
		saveHistory();
	}

	function viewResult(serverId: number, jobId: string) {
		// trace job이면 trace 시트로 바로 열기
		const record = jobHistory.find(j => j.jobId === jobId);
		const active = activeJobs.find(j => j.jobId === jobId);
		const jobType = record?.type ?? active?.type;

		if (jobType === 'trace') {
			viewingServerId = serverId;
			traceJobIds = [jobId];
			traceSheetOpen = true;
			return;
		}

		viewingServerId = serverId;
		viewingJobId = jobId;
		resultDetailSheetOpen = true;
	}

	function viewTraceResult(serverId: number, deviceId: string, jobIds: string[]) {
		viewingServerId = serverId;
		traceDeviceId = deviceId;
		traceJobIds = jobIds;
		traceSheetOpen = true;
	}

	function openScreen(deviceId: string) {
		screenDeviceId = deviceId;
		screenSheetOpen = true;
	}

	// Terminal (adb shell PTY) — TerminalDialog 가 다중 탭을 지원해서 그냥 push.
	let terminalOpen = $state(false);
	let terminalTabs = $state<Array<{ id: string; vmName: string; slotName: string; protocol: 'adb'; deviceId: string }>>([]);
	function openTerminal(deviceId: string) {
		// 같은 디바이스 탭이 이미 있으면 dialog 만 다시 열고 끝
		const exists = terminalTabs.some(t => t.deviceId === deviceId);
		if (!exists) {
			terminalTabs = [...terminalTabs, {
				id: `term-${deviceId}-${Date.now()}`,
				vmName: deviceId,
				slotName: deviceId,
				protocol: 'adb',
				deviceId,
			}];
		}
		terminalOpen = true;
	}

	function openMonitoring(deviceId: string) {
		if (monitoringDeviceId !== deviceId || !monitoringActive) {
			// Different device or not running → start fresh
			monitoringDeviceId = deviceId;
			startMonitoring();
		}
		monitoringSheetOpen = true;
	}

	function getServerName(serverId: number): string {
		return servers.find(s => s.id === serverId)?.name ?? String(serverId);
	}

	onDestroy(() => {
		activeJobs.forEach(j => j.eventSource?.close());
		monitorUserStopped = true;
		closeMonitorSSE();
	});
</script>

<div class="space-y-2">
	<!-- Header -->
	<div class="flex items-center justify-between">
		<h1 class="text-sm font-semibold">Agent</h1>
	</div>

	{#if servers.length === 0 && !loadingDevices}
		<!-- Empty state: no servers -->
		<div class="flex flex-col items-center justify-center py-20 text-center">
			<div class="text-muted-foreground text-xs mb-3">Agent 서버가 등록되어 있지 않습니다</div>
			<button
				onclick={() => serverSheetOpen = true}
				class="inline-flex items-center gap-1.5 rounded-md bg-blue-600 text-white px-4 py-2 text-xs hover:bg-blue-700 transition-colors"
			>
				서버 추가
			</button>
		</div>
	{:else}
		<!-- Main 3-panel layout -->
		<div class="flex gap-3" style="height: calc(100vh - 7rem);">
			<!-- LEFT: Context Panel -->
			<AgentContextPanel
				{enabledServers}
				bind:selectedServerId
				{devices}
				{loadingDevices}
				bind:selectedDeviceIds
				{centerMode}
				onModeChange={(mode) => centerMode = mode}
				onRefreshDevices={loadDevices}
				onOpenServerSheet={() => serverSheetOpen = true}
				onOpenMonitoring={openMonitoring}
				onOpenScreen={openScreen}
				onOpenTerminal={openTerminal}
				activeJobCount={activeJobs.filter(j => j.state === 'running').length}
				{storageMetricsMap}
			/>

			<!-- CENTER: Action area -->
			<div class="flex-1 overflow-y-auto">
				{#if centerMode === 'benchmark'}
					<AgentBenchmarkForm
						serverId={selectedServerId}
						selectedDevices={selectedDeviceIds}
						serverName={selectedServerId ? getServerName(selectedServerId) : ''}
						onJobStarted={startJob}
					/>
				{:else if centerMode === 'scenario'}
					<ScenarioCanvas
						serverId={selectedServerId}
						selectedDevices={selectedDeviceIds}
						serverName={selectedServerId ? getServerName(selectedServerId) : ''}
						onJobStarted={startJob}
						{activeJobs}
					/>
				{:else if centerMode === 'trace'}
					<AgentTraceForm
						serverId={selectedServerId}
						selectedDevices={selectedDeviceIds}
						serverName={selectedServerId ? getServerName(selectedServerId) : ''}
						onJobStarted={startJob}
						bind:activeTraceJobId
					/>
				{:else if centerMode === 'iotest'}
					<IOTestForm
						serverId={selectedServerId}
						selectedDevices={selectedDeviceIds}
						serverName={selectedServerId ? getServerName(selectedServerId) : ''}
						onJobStarted={startJob}
					/>
				{:else if centerMode === 'macro'}
					<AgentMacroRecorder
						serverId={selectedServerId}
						selectedDevices={selectedDeviceIds}
					/>
				{:else if centerMode === 'schedule'}
					<AgentScheduleView
						serverId={selectedServerId}
						enabledServers={enabledServers}
						onJobStarted={startJob}
					/>
				{:else}
					<AgentResultsView
						{jobHistory}
						serverId={selectedServerId}
						onViewDetail={viewResult}
						onDeleteJob={deleteJobFromHistory}
					/>
				{/if}
			</div>
		</div>
	{/if}
</div>

<!-- Floating Job Progress Card -->
<AgentFloatingJobCard
	{activeJobs}
	onDismiss={dismissJob}
	onViewResult={viewResult}
/>

<!-- Sheets -->
<AgentServerSheet
	bind:open={serverSheetOpen}
	bind:servers
	onRefresh={loadServers}
/>

<AgentMonitoringSheet
	bind:open={monitoringSheetOpen}
	deviceId={monitoringDeviceId}
	monitoring={monitoringActive}
	{latestMetrics}
	{cpuHistory}
	{memHistory}
	{diskReadHistory}
	{diskWriteHistory}
	onStart={startMonitoring}
	onStop={stopMonitoring}
/>

<AgentResultDetailSheet
	bind:open={resultDetailSheetOpen}
	serverId={viewingServerId}
	jobId={viewingJobId}
	{activeJobs}
	onViewTrace={(deviceId, jobIds) => viewTraceResult(viewingServerId!, deviceId, jobIds)}
/>

<AgentTraceResultSheet
	bind:open={traceSheetOpen}
	serverId={viewingServerId}
	jobIds={traceJobIds}
/>

<AgentScreenSheet
	bind:open={screenSheetOpen}
	serverId={selectedServerId}
	deviceId={screenDeviceId}
/>

<TerminalDialog
	bind:open={terminalOpen}
	terminals={terminalTabs}
	onClose={() => { terminalTabs = []; }}
/>
