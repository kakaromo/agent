<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import PerfChart from '$lib/components/perf-chart/PerfChart.svelte';
	import DataTableShell from '$lib/components/DataTableShell.svelte';
	import { toast } from 'svelte-sonner';
	import { onDestroy } from 'svelte';
	import { getJobStatus, getBenchmarkResult, fetchExecutionByJobId, getAiStatus, createAiAnalyzeSource, type JobStatus, type BenchmarkResult, type JobProgress, type TraceJobMapping, type StepBoundary, type JobExecutionRecord } from '$lib/api/agent.js';
	import type { ActiveJob } from './types.js';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import type { ColumnDef } from '@tanstack/table-core';

	import ScanSearchIcon from '@lucide/svelte/icons/scan-search';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import InboxIcon from '@lucide/svelte/icons/inbox';
	import ExecutionMiniCanvas from './scenario-canvas/ExecutionMiniCanvas.svelte';
	import { AgentResultRenderer, WorkloadContextBanner } from './agent-result/index.js';

	interface Props {
		open: boolean;
		serverId: number | null;
		jobId: string | null;
		activeJobs: ActiveJob[];
		onViewTrace?: (deviceId: string, jobIds: string[], mappings?: { traceJobId: string; stepIndex?: number; loopIndex?: number; repeatIndex?: number }[], boundaries?: StepBoundary[]) => void;
	}

	let { open = $bindable(), serverId, jobId, activeJobs, onViewTrace }: Props = $props();

	// 스텝 구간 — behavior 레인/구간 밴드의 시간 축.
	// trace_jobs 와 달리 raw_output fallback 이 없다 (텍스트에 안 실린다).
	function getStepBoundaries(): StepBoundary[] {
		const out: StepBoundary[] = [];
		if (result) {
			for (const r of result.results) {
				if (r.stepBoundaries) out.push(...r.stepBoundaries);
			}
		}
		// live 결과가 없거나 비었으면 DB 영속본으로 폴백 — 만료된 잡 경로.
		if (out.length === 0) return persistedBoundaries;
		return out;
	}

	// Trace jobs — 구조화 데이터 우선, fallback으로 raw_output 파싱
	function getTraceJobMappings(): TraceJobMapping[] {
		// 1. 구조화 데이터 (BenchmarkResult.trace_jobs)
		if (result) {
			const mappings: TraceJobMapping[] = [];
			for (const r of result.results) {
				if (r.traceJobs) mappings.push(...r.traceJobs);
			}
			if (mappings.length > 0) return mappings;
		}

		// 2. SSE events + result rawOutput에서 TRACE_STOP 파싱 (stop된 trace만 분석 가능)
		const mappings: TraceJobMapping[] = [];
		const seen = new Set<string>();

		function addFromText(text: string) {
			for (const line of text.split('\n')) {
				// TRACE_STOP|loop=N|step=N|repeat=N|job_id=xxx|trace_type=ufs
				const m = line.match(/TRACE_STOP\|/);
				if (!m) continue;
				const jobIdMatch = line.match(/job_id=([^\s|]+)/);
				if (!jobIdMatch || seen.has(jobIdMatch[1])) continue;
				seen.add(jobIdMatch[1]);

				const loopMatch = line.match(/loop=(\d+)/);
				const stepMatch = line.match(/step=(\d+)/);
				const repeatMatch = line.match(/repeat=(\d+)/);
				const typeMatch = line.match(/trace_type=(\w+)/);

				mappings.push({
					traceJobId: jobIdMatch[1],
					stepIndex: stepMatch ? +stepMatch[1] : 0,
					loopIndex: loopMatch ? +loopMatch[1] : 0,
					repeatIndex: repeatMatch ? +repeatMatch[1] : mappings.length + 1,
					traceType: typeMatch ? typeMatch[1] : 'ufs'
				});
			}
		}

		// SSE events에서 수집한 trace outputs (incremental)
		for (const text of liveTraceOutputs) { addFromText(text); }
		// Final result
		if (result) { for (const r of result.results) { if (r.rawOutput) addFromText(r.rawOutput); } }

		// 3. 영속화된 trace job (만료된 잡 — live/result 둘 다 없을 때)
		if (mappings.length === 0 && persistedTraceJobs.length > 0) {
			return persistedTraceJobs;
		}

		return mappings;
	}

	let allTraceJobMappings = $derived(getTraceJobMappings());

	// Cycle(repeat) 필터 적용된 trace (selectedRepeat=0이면 전체)
	let traceJobMappings = $derived.by(() => {
		const all = allTraceJobMappings;
		if (all.length === 0) return [];
		if (selectedRepeat > 0 && all.some(m => m.repeatIndex > 0)) {
			return all.filter(m => m.repeatIndex === selectedRepeat);
		}
		return all;
	});
	let traceJobIds = $derived(traceJobMappings.map(m => m.traceJobId));

	// Trace에서 사용 가능한 cycle(repeat) 목록
	let traceRepeatList = $derived(
		[...new Set(allTraceJobMappings.map(m => m.repeatIndex))].filter(r => r > 0).sort((a, b) => a - b)
	);

	// Trace multi-selection
	let selectedTraceIds = $state<Set<string>>(new Set());

	// MinIO 업로드 UI 제거 — standalone 에서는 MinIO 미사용 (archive 는 로컬 디스크).
	let showTraceList = $state(false);
	let showScenarioCanvas = $state(true); // 기본 펼침

	function toggleTraceSelection(id: string) {
		const next = new Set(selectedTraceIds);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		selectedTraceIds = next;
	}

	let loading = $state(false);
	let jobStatus = $state<JobStatus | null>(null);
	let result = $state<BenchmarkResult | null>(null);
	let selectedDevice = $state<string | null>(null);
	let pollTimer: ReturnType<typeof setInterval> | null = null;
	let executionConfig = $state<{ steps?: any[]; loops?: any[] } | null>(null);
	let workloadNote = $state<string | null>(null);
	// DB 에 영구 저장된 metrics summary — 만료된(agent 메모리에서 사라진) 잡의
	// 워크로드 컨텍스트 해석에 fallback 으로 사용.
	let persistedSummary = $state<Record<string, number> | null>(null);
	// DB 에 영구 저장된 trace job 매핑 — live 결과가 없는 만료된 잡에서도 기존 trace UI 진입용.
	let persistedTraceJobs = $state<TraceJobMapping[]>([]);
	// 영속화된 스텝 구간 — 만료된 잡에서도 Behavior 탭을 볼 수 있게 (traceJobs 와 같은 이유).
	let persistedBoundaries = $state<StepBoundary[]>([]);
	// 만료된 잡의 device id fallback (execution.deviceIds JSON 의 첫 device).
	let persistedDeviceId = $state<string>('');

	// ── AI 해석 ──
	// reachable=true 일 때만 버튼 노출. 시트 로컬 state.
	let aiReachable = $state(false);
	let aiRunning = $state(false);
	let aiText = $state('');
	let aiError = $state('');
	let aiSource: EventSource | null = null;

	function closeAiSource() {
		if (aiSource) { aiSource.close(); aiSource = null; }
	}

	function startAiAnalyze() {
		if (serverId == null || !jobId) return;
		closeAiSource();
		aiRunning = true;
		aiText = '';
		aiError = '';
		const es = createAiAnalyzeSource(serverId, jobId, 'benchmark');
		aiSource = es;

		es.addEventListener('token', (e: MessageEvent) => {
			try {
				const d = JSON.parse(e.data);
				if (typeof d?.text === 'string') aiText += d.text;
			} catch { /* ignore */ }
		});

		es.addEventListener('done', () => {
			closeAiSource();
			aiRunning = false;
		});

		es.addEventListener('error', (e: MessageEvent) => {
			let msg = 'AI 해석 실패';
			try { const d = JSON.parse((e as MessageEvent).data); if (d?.error) msg = d.error; } catch { /* SSE 연결 에러엔 data 없음 */ }
			closeAiSource();
			aiRunning = false;
			if (!aiText) aiError = msg;
		});
	}

	$effect(() => {
		if (open && serverId != null && jobId != null) {
			loadDetail(); loadExecutionConfig();
			getAiStatus().then(s => { aiReachable = !!(s.enabled && s.reachable); }).catch(() => { aiReachable = false; });
		}
		if (!open) {
			stopPolling();
			closeAiSource(); aiRunning = false; aiText = ''; aiError = '';
		}
	});
	onDestroy(() => { stopPolling(); closeAiSource(); });

	async function loadExecutionConfig() {
		if (!jobId) return;
		executionConfig = null;
		workloadNote = null;
		persistedSummary = null;
		persistedTraceJobs = [];
		persistedBoundaries = [];
		persistedDeviceId = '';
		try {
			const exec = await fetchExecutionByJobId(jobId);
			if (exec.config) {
				executionConfig = JSON.parse(exec.config);
			}
			workloadNote = exec.workloadNote ?? null;
			persistedTraceJobs = Array.isArray(exec.traceJobs) ? exec.traceJobs : [];
			persistedBoundaries = Array.isArray(exec.stepBoundaries) ? exec.stepBoundaries : [];
			if (exec.deviceIds) {
				try {
					const ids = JSON.parse(exec.deviceIds);
					if (Array.isArray(ids) && ids.length > 0) persistedDeviceId = String(ids[0]);
				} catch { /* deviceIds 가 JSON 이 아닐 수 있음 */ }
			}
			if (exec.resultSummary) {
				try {
					const parsed = JSON.parse(exec.resultSummary);
					// flat 숫자 + 1-depth 중첩 latency 객체(dtoc/qd/ctoc/ctod)를 {key}_{stat} 로 편다.
					// (trace summary 는 dtoc:{p99,avg,...} / qd:{max,...} 형태라 flat 화 안 하면 배너가 못 읽음)
					const flat: Record<string, number> = {};
					for (const [k, v] of Object.entries(parsed)) {
						if (typeof v === 'number' && isFinite(v)) {
							flat[k] = v;
						} else if (v && typeof v === 'object' && !Array.isArray(v)) {
							for (const [sk, sv] of Object.entries(v as Record<string, unknown>)) {
								if (typeof sv === 'number' && isFinite(sv)) flat[`${k}_${sk}`] = sv;
							}
						}
					}
					persistedSummary = Object.keys(flat).length > 0 ? flat : null;
				} catch { persistedSummary = null; }
			}
		} catch { /* DB에 없을 수 있음 */ }
	}

	function startPolling() { stopPolling(); pollTimer = setInterval(() => refreshStatus(), 2000); }
	function stopPolling() { if (pollTimer) { clearInterval(pollTimer); pollTimer = null; } }
	function isTerminal(s: string) { return s === 'completed' || s === 'failed' || s === 'partially_failed' || s === 'cancelled'; }

	async function refreshStatus() {
		if (serverId == null || jobId == null) return;
		try {
			const res = await getJobStatus(serverId, jobId);
			jobStatus = res;
			if (isTerminal(res.state)) {
				stopPolling();
				try {
					result = await getBenchmarkResult(serverId, jobId);
					if (result.results.length > 0 && !selectedDevice) selectedDevice = result.results[0].deviceId;
				} catch {}
			}
		} catch {
			// 404 등 에러 시 polling 중단
			stopPolling();
		}
	}

	/**
	 * agent 재시작으로 메모리에서 만료된 잡의 상태를 DB 값으로 되살린다.
	 *
	 * GetJobStatus 는 만료 시 404 + {error, state:"failed"} 를 주고 client.ts 가 이를
	 * 정상 데이터로 통과시킨다(portal 호환). 하지만 그 "failed" 는 "조회 불가"라는 뜻일 뿐이며,
	 * 실제 종료 상태는 job_executions 에 영구 저장돼 있다. 구분하지 않으면 agent 재시작 후
	 * 과거 성공 잡이 모두 '실패'로 표시된다.
	 *
	 * 만료 응답은 error 필드가 있고 deviceStatuses 가 비어 있어 정상 응답과 구별된다.
	 */
	async function resolveExpiredStatus(res: JobStatus): Promise<JobStatus> {
		const expired = !!(res as any).error && !(res.deviceStatuses?.length);
		if (!expired || !jobId) return res;
		try {
			const exec = await fetchExecutionByJobId(jobId);
			if (!exec?.state) return res;
			return {
				...res,
				state: exec.state,
				deviceStatuses: exec.errorMessage
					? [{ deviceId: '', state: exec.state, message: exec.errorMessage, progressPercent: 0 } as any]
					: []
			} as JobStatus;
		} catch {
			return res; // DB 에도 없으면 원래 응답(failed) 유지
		}
	}

	async function loadDetail() {
		if (serverId == null || jobId == null) return;
		loading = true; jobStatus = null; result = null; selectedDevice = null; stopPolling();
		try {
			const res = await getJobStatus(serverId, jobId);
			jobStatus = await resolveExpiredStatus(res);
			if (isTerminal(jobStatus!.state)) {
				try {
					result = await getBenchmarkResult(serverId, jobId);
					if (result.results.length > 0) selectedDevice = result.results[0].deviceId;
				} catch {}
			} else { startPolling(); }
		} catch {
			// Job not found — DB 에도 없으면 실패로 표시
			jobStatus = { jobId: jobId ?? '', state: 'failed', totalDevices: 0, completedDevices: 0, failedDevices: 0, deviceStatuses: [] } as any;
		}
		finally { loading = false; }
	}

	// ══════════════════════════════════════════
	// 실시간 metrics from SSE (incremental + throttled)
	// ══════════════════════════════════════════
	let activeJob = $derived(activeJobs.find(j => j.jobId === jobId));
	let isRunning = $derived(activeJob?.state === 'running');

	// Incremental live metrics — 매번 전체 events를 순회하지 않음
	let liveMetricsCache = $state<Record<string, number>>({});
	let liveTraceOutputs = $state<string[]>([]); // TRACE_STOP 포함 rawOutput 수집
	let lastProcessedEventCount = 0;
	let metricsUpdateTimer: ReturnType<typeof setTimeout> | null = null;

	$effect(() => {
		if (!activeJob) { liveMetricsCache = {}; liveTraceOutputs = []; lastProcessedEventCount = 0; return; }
		const events = activeJob.events;
		if (events.length === lastProcessedEventCount) return;

		let hasNewTrace = false;
		// 새 이벤트만 처리 (incremental)
		for (let i = lastProcessedEventCount; i < events.length; i++) {
			const e = events[i];
			if (e.metrics) {
				for (const [k, v] of Object.entries(e.metrics)) {
					liveMetricsCache[k] = v;
				}
			}
			if (e.rawOutput && e.rawOutput.includes('TRACE_STOP|')) {
				liveTraceOutputs = [...liveTraceOutputs, e.rawOutput];
				hasNewTrace = true;
			}
		}
		lastProcessedEventCount = events.length;

		// Trace 발견 시 즉시 업데이트
		if (hasNewTrace) {
			liveMetricsCache = { ...liveMetricsCache };
			return;
		}

		// Throttle: 1초에 최대 1번 UI 업데이트
		if (!metricsUpdateTimer) {
			metricsUpdateTimer = setTimeout(() => {
				liveMetricsCache = { ...liveMetricsCache }; // trigger reactivity
				metricsUpdateTimer = null;
			}, 1000);
		}
	});

	let selectedResult = $derived(result?.results.find(r => r.deviceId === selectedDevice) ?? null);

	let effectiveMetrics = $derived.by(() => {
		if (selectedResult) return selectedResult.metrics;
		return Object.keys(liveMetricsCache).length > 0 ? liveMetricsCache : {};
	});

	// 워크로드 컨텍스트 배너용 metrics — 라이브 결과가 없으면(만료된 잡) DB summary 로 fallback.
	// (full 결과 렌더러는 step prefix 가 필요해 effectiveMetrics 를 그대로 쓰지만,
	//  배너 해석은 flat metric 만으로 충분해서 summary fallback 이 가능.)
	let bannerMetrics = $derived.by(() => {
		if (Object.keys(effectiveMetrics).length > 0) return effectiveMetrics;
		return persistedSummary ?? {};
	});

	// ══════════════════════════════════════════
	// Metrics 파싱
	// 키: r{cycle}_step{step}_{read|write}_{metric_name}
	// ══════════════════════════════════════════

	interface ParsedMetric {
		cycle: number; step: number;
		repeat: number;      // repeat 번호 (1-based)
		iteration: number;   // loop iteration 번호 (0 = no loop)
		rw: 'read' | 'write' | 'other';
		name: string; category: string; value: number;
	}

	// tiotest rw 매핑: seq_write→write, seq_read→read, rand_write→write, rand_read→read
	function parseTiotestRw(prefix: string): { rw: 'read' | 'write' | 'other'; testType: string } {
		if (prefix === 'seq_write' || prefix === 'rand_write') return { rw: 'write', testType: prefix };
		if (prefix === 'seq_read' || prefix === 'rand_read') return { rw: 'read', testType: prefix };
		return { rw: 'other', testType: prefix };
	}

	function parseMetricKey(raw: string) {
		// loop: r{repeat}_loop{iteration}_step{step}_{read|write}_{metric}
		const ml = raw.match(/^r(\d+)_loop(\d+)_step(\d+)_(.+)$/);
		if (ml) {
			const repeat = +ml[1];
			const iteration = +ml[2];
			const cycle = iteration;  // loop iteration = X축 (cycle 역할)
			const step = +ml[3];
			let rest = ml[4];
			if (rest.startsWith('read_')) return { cycle, step, repeat, iteration, rw: 'read' as const, name: rest.slice(5) };
			if (rest.startsWith('write_')) return { cycle, step, repeat, iteration, rw: 'write' as const, name: rest.slice(6) };
			const tio = rest.match(/^(seq_write|seq_read|rand_write|rand_read)_(.+)$/);
			if (tio) {
				const { rw } = parseTiotestRw(tio[1]);
				return { cycle, step, repeat, iteration, rw, name: tio[1] + '_' + tio[2] };
			}
			return { cycle, step, repeat, iteration, rw: 'other' as const, name: rest };
		}

		// fio: r{cycle}_step{step}_{read|write}_{metric}  (no loop → repeat=cycle, iteration=0)
		const m = raw.match(/^r(\d+)_step(\d+)_(.+)$/);
		if (m) {
			let rest = m[3];
			if (rest.startsWith('read_')) return { cycle: +m[1], step: +m[2], repeat: +m[1], iteration: 0, rw: 'read' as const, name: rest.slice(5) };
			if (rest.startsWith('write_')) return { cycle: +m[1], step: +m[2], repeat: +m[1], iteration: 0, rw: 'write' as const, name: rest.slice(6) };
			const tio = rest.match(/^(seq_write|seq_read|rand_write|rand_read)_(.+)$/);
			if (tio) {
				const { rw } = parseTiotestRw(tio[1]);
				return { cycle: +m[1], step: +m[2], repeat: +m[1], iteration: 0, rw, name: tio[1] + '_' + tio[2] };
			}
			return { cycle: +m[1], step: +m[2], repeat: +m[1], iteration: 0, rw: 'other' as const, name: rest };
		}

		// tiotest without prefix
		const tio = raw.match(/^(seq_write|seq_read|rand_write|rand_read)_(.+)$/);
		if (tio) {
			const { rw } = parseTiotestRw(tio[1]);
			return { cycle: 0, step: 0, repeat: 0, iteration: 0, rw, name: tio[1] + '_' + tio[2] };
		}

		// fio without prefix
		if (raw.startsWith('read_')) return { cycle: 0, step: 0, repeat: 0, iteration: 0, rw: 'read' as const, name: raw.slice(5) };
		if (raw.startsWith('write_')) return { cycle: 0, step: 0, repeat: 0, iteration: 0, rw: 'write' as const, name: raw.slice(6) };

		return { cycle: 0, step: 0, repeat: 0, iteration: 0, rw: 'other' as const, name: raw };
	}

	function formatCycleLabel(c: number): string {
		return hasLoopData ? `${c}` : `#${c}`;
	}

	function getCategory(name: string): string {
		// fio categories
		if (/^iops/.test(name)) return 'iops';
		if (/^bw/.test(name)) return 'bw';

		// tiotest categories: seq_write_mb_sec → throughput, seq_write_lat_avg_ms → latency
		if (/mb_sec$/.test(name)) return 'bw';
		if (/lat_/.test(name)) return 'lat';
		if (/cpu_pct$/.test(name)) return 'cpu';
		if (/^slat/.test(name)) return 'slat';
		if (/^clat/.test(name)) return 'clat';
		if (/^lat/.test(name)) return 'lat';
		if (/cpu/.test(name)) return 'cpu';
		if (/runtime/.test(name)) return 'runtime';
		if (/^io_bytes|^total_ios/.test(name)) return 'io';
		return 'other';
	}

	const CATEGORY_LABELS: Record<string, string> = {
		iops: 'IOPS', bw: 'Bandwidth', slat: 'Submission Latency',
		clat: 'Completion Latency', lat: 'Total Latency', io: 'I/O Summary',
		cpu: 'CPU', runtime: 'Runtime', other: 'Other'
	};

	let parsedMetrics = $derived.by<ParsedMetric[]>(() => {
		const metrics = effectiveMetrics;
		return Object.entries(metrics).map(([raw, value]) => {
			const { cycle, step, repeat, iteration, rw, name } = parseMetricKey(raw);
			return { cycle, step, repeat, iteration, rw, name, category: getCategory(name), value };
		});
	});

	// Loop 여부 감지
	let hasLoopData = $derived(parsedMetrics.some(m => m.iteration > 0));

	// Repeat 탭 (loop가 있을 때만)
	let availableRepeats = $derived(
		hasLoopData
			? [...new Set(parsedMetrics.map(m => m.repeat))].sort((a, b) => a - b)
			: []
	);
	let selectedRepeat = $state(1);

	$effect(() => {
		if (availableRepeats.length > 0 && !availableRepeats.includes(selectedRepeat)) {
			selectedRepeat = availableRepeats[0];
		}
	});

	// repeat 필터: loop가 있으면 선택된 repeat만
	let repeatFiltered = $derived.by(() => {
		const all = parsedMetrics;
		if (hasLoopData && availableRepeats.length > 0) {
			return all.filter(m => m.repeat === selectedRepeat);
		}
		return all;
	});

	// ── Steps & Cycles ──
	let steps = $derived.by(() => [...new Set(repeatFiltered.map(m => m.step))].sort((a, b) => a - b));
	let hasSteps = $derived(steps.length > 1);
	let selectedStep = $state(0);
	let mergedStepIds = $state<Set<number>>(new Set()); // 비어있으면 단일 step 모드

	let mergeActive = $state(false);  // merge 모드 활성 여부
	let isMergeMode = $derived(mergeActive && mergedStepIds.size > 0);

	$effect(() => {
		const s = steps;
		if (s.length > 0 && !s.includes(selectedStep)) selectedStep = s[0];
	});

	function toggleMergeStep(stepId: number) {
		const next = new Set(mergedStepIds);
		if (next.has(stepId)) next.delete(stepId);
		else next.add(stepId);
		mergedStepIds = next;
	}

	function enterMergeMode() {
		mergeActive = true;
		mergedStepIds = new Set(steps); // 기본: 전체 선택
	}

	function clearMerge() {
		mergeActive = false;
		mergedStepIds = new Set();
	}

	// Metrics filtered by repeat + selected step or merged steps
	let viewMetrics = $derived(
		mergeActive && mergedStepIds.size > 0
			? repeatFiltered.filter(m => mergedStepIds.has(m.step))
			: repeatFiltered.filter(m => m.step === selectedStep)
	);

	const MAX_CHART_CYCLES = 100;
	let allCycles = $derived.by(() => [...new Set(viewMetrics.map(m => m.cycle))].sort((a, b) => a - b));
	// 차트에는 최근 MAX_CHART_CYCLES개만 표시 (성능)
	let cycles = $derived.by(() => {
		const all = allCycles;
		return all.length > MAX_CHART_CYCLES ? all.slice(-MAX_CHART_CYCLES) : all;
	});
	let hasCycles = $derived(cycles.length > 1);

	let availableRw = $derived.by(() => {
		const rws = new Set(viewMetrics.map(m => m.rw));
		const dirs: ('read' | 'write' | 'other')[] = [];
		if (rws.has('read')) dirs.push('read');
		if (rws.has('write')) dirs.push('write');
		if (rws.has('other')) dirs.push('other');
		return dirs;
	});

	// ── Performance Charts ──
	const STEP_COLORS = ['#5470c6', '#fc8452', '#91cc75', '#fac858', '#ee6666', '#73c0de'];

	// 단일 step 모드: rw별 단일 라인
	function buildSingleStepChart(metricName: string, rw: 'read' | 'write', unit: string, color: string) {
		const cycs = cycles;
		if (cycs.length === 0) return null;
		const rawData = cycs.map(c => viewMetrics.find(m => m.cycle === c && m.rw === rw && m.name === metricName)?.value ?? 0);
		if (rawData.every(v => v === 0)) return null;
		const data = rawData.map(v => convertValue(v, metricName));
		return {
			tooltip: { trigger: 'axis' as const },
			xAxis: { type: 'category' as const, name: hasLoopData ? 'Iteration' : 'Cycle', data: cycs.map(c => formatCycleLabel(c)), axisLabel: { fontSize: 9 } },
			yAxis: { type: 'value' as const, name: unit, nameTextStyle: { fontSize: 9 }, axisLabel: { fontSize: 9 } },
			series: [{ name: rw, type: 'line' as const, data, smooth: true, itemStyle: { color }, areaStyle: { opacity: 0.1 } }],
			grid: { left: 60, right: 20, top: 20, bottom: 40 }, dataZoom: [{ type: 'inside' as const }]
		};
	}

	// Merge 모드: 특정 rw 방향에 대해, step별로 라인을 나눠서 한 차트에 표시
	// X축 = cycle (같은 번호 유지), 시리즈 = "Step0", "Step1" 등
	function buildMergedChart(metricName: string, rw: 'read' | 'write', unit: string) {
		const cycs = cycles;
		if (cycs.length === 0) return null;
		const stps = [...mergedStepIds].sort((a, b) => a - b);
		const allMetrics = parsedMetrics;

		const series: any[] = [];
		for (const s of stps) {
			const stepData = allMetrics.filter(m => m.step === s && m.name === metricName && m.rw === rw);
			if (stepData.length === 0) continue;
			const rawData = cycs.map(c => stepData.find(m => m.cycle === c)?.value ?? 0);
			if (rawData.every(v => v === 0)) continue;
			const data = rawData.map(v => convertValue(v, metricName));
			series.push({
				name: `Step${s}`,
				type: 'line' as const,
				data,
				smooth: true,
				itemStyle: { color: STEP_COLORS[s % STEP_COLORS.length] }
			});
		}
		if (series.length === 0) return null;

		return {
			tooltip: { trigger: 'axis' as const },
			legend: { data: series.map((s: any) => s.name), top: 0, right: 0, textStyle: { fontSize: 9 } },
			xAxis: { type: 'category' as const, name: hasLoopData ? 'Iteration' : 'Cycle', data: cycs.map(c => formatCycleLabel(c)), axisLabel: { fontSize: 9 } },
			yAxis: { type: 'value' as const, name: unit, nameTextStyle: { fontSize: 9 }, axisLabel: { fontSize: 9 } },
			series,
			grid: { left: 60, right: 20, top: 30, bottom: 40 }, dataZoom: [{ type: 'inside' as const }]
		};
	}

	// 통합 buildCycleChart: 모드에 따라 분기
	// 단일 step → 단일 라인, merge → step별 멀티 라인
	function buildCycleChart(metricName: string, rw: 'read' | 'write', unit: string, color: string) {
		if (isMergeMode) return buildMergedChart(metricName, rw, unit);
		return buildSingleStepChart(metricName, rw, unit, color);
	}

	// 단위 변환 정의
	interface MetricUnitConversion {
		name: string;
		label: string;
		displayUnit: string;
		divisor: number; // raw 값을 이 값으로 나누면 displayUnit
	}

	const UNIT_CONVERSIONS: Record<string, MetricUnitConversion> = {
		'iops': { name: 'iops', label: 'IOPS', displayUnit: 'KIOPS', divisor: 1000 },
		'iops_mean': { name: 'iops_mean', label: 'IOPS 평균', displayUnit: 'KIOPS', divisor: 1000 },
		'iops_min': { name: 'iops_min', label: 'IOPS 최소', displayUnit: 'KIOPS', divisor: 1000 },
		'iops_max': { name: 'iops_max', label: 'IOPS 최대', displayUnit: 'KIOPS', divisor: 1000 },
		'iops_stddev': { name: 'iops_stddev', label: 'IOPS 표준편차', displayUnit: 'KIOPS', divisor: 1000 },
		'bw_kb': { name: 'bw_kb', label: 'Bandwidth', displayUnit: 'MiB/s', divisor: 1024 },
		'bw_bytes': { name: 'bw_bytes', label: 'Bandwidth', displayUnit: 'MiB/s', divisor: 1024 * 1024 },
		'bw_min_kb': { name: 'bw_min_kb', label: 'BW 최소', displayUnit: 'MiB/s', divisor: 1024 },
		'bw_max_kb': { name: 'bw_max_kb', label: 'BW 최대', displayUnit: 'MiB/s', divisor: 1024 },
		'bw_mean_kb': { name: 'bw_mean_kb', label: 'BW 평균', displayUnit: 'MiB/s', divisor: 1024 },
		'io_bytes': { name: 'io_bytes', label: '총 IO', displayUnit: 'MiB', divisor: 1024 * 1024 },
		'slat_ns_mean': { name: 'slat_ns_mean', label: 'Submit Lat 평균', displayUnit: 'ms', divisor: 1_000_000 },
		'slat_ns_min': { name: 'slat_ns_min', label: 'Submit Lat 최소', displayUnit: 'ms', divisor: 1_000_000 },
		'slat_ns_max': { name: 'slat_ns_max', label: 'Submit Lat 최대', displayUnit: 'ms', divisor: 1_000_000 },
		'slat_ns_stddev': { name: 'slat_ns_stddev', label: 'Submit Lat 표준편차', displayUnit: 'ms', divisor: 1_000_000 },
		'clat_ns_mean': { name: 'clat_ns_mean', label: 'Complete Lat 평균', displayUnit: 'ms', divisor: 1_000_000 },
		'clat_ns_min': { name: 'clat_ns_min', label: 'Complete Lat 최소', displayUnit: 'ms', divisor: 1_000_000 },
		'clat_ns_max': { name: 'clat_ns_max', label: 'Complete Lat 최대', displayUnit: 'ms', divisor: 1_000_000 },
		'clat_ns_stddev': { name: 'clat_ns_stddev', label: 'Complete Lat 표준편차', displayUnit: 'ms', divisor: 1_000_000 },
		'lat_ns_mean': { name: 'lat_ns_mean', label: 'Latency 평균', displayUnit: 'ms', divisor: 1_000_000 },
		'lat_ns_min': { name: 'lat_ns_min', label: 'Latency 최소', displayUnit: 'ms', divisor: 1_000_000 },
		'lat_ns_max': { name: 'lat_ns_max', label: 'Latency 최대', displayUnit: 'ms', divisor: 1_000_000 },
		'lat_ns_stddev': { name: 'lat_ns_stddev', label: 'Latency 표준편차', displayUnit: 'ms', divisor: 1_000_000 },
	};

	function getUnitConversion(metricName: string): { displayUnit: string; divisor: number } {
		const conv = UNIT_CONVERSIONS[metricName];
		if (conv) return { displayUnit: conv.displayUnit, divisor: conv.divisor };
		// ns 계열 → ms
		if (metricName.includes('_ns_')) return { displayUnit: 'ms', divisor: 1_000_000 };
		// tiotest/iozone: mb_sec 계열
		if (metricName.endsWith('_mb_sec')) return { displayUnit: 'MiB/s', divisor: 1 };
		if (metricName.endsWith('_kb_sec')) return { displayUnit: 'MiB/s', divisor: 1024 };
		if (metricName.endsWith('_lat_avg_ms')) return { displayUnit: 'ms', divisor: 1 };
		// iops 계열 (명시 안 된 것)
		if (metricName.includes('iops')) return { displayUnit: 'KIOPS', divisor: 1000 };
		// bw_kb 계열
		if (metricName.includes('bw_kb') || metricName.includes('bw_stddev_kb')) return { displayUnit: 'MiB/s', divisor: 1024 };
		return { displayUnit: '', divisor: 1 };
	}

	function convertValue(value: number, metricName: string): number {
		const { divisor } = getUnitConversion(metricName);
		return divisor !== 1 ? value / divisor : value;
	}

	function formatConvertedValue(value: number, metricName: string): string {
		const converted = convertValue(value, metricName);
		return converted >= 100 ? converted.toLocaleString('en-US', { maximumFractionDigits: 1 })
			: converted >= 1 ? converted.toFixed(2)
			: converted.toFixed(3);
	}

	// 차트에 사용할 metric 자동 감지
	let chartMetrics = $derived.by<{ name: string; label: string; unit: string }[]>(() => {
		const names = new Set(viewMetrics.map(m => m.name));
		const metrics: { name: string; label: string; unit: string }[] = [];

		// fio
		if (names.has('iops')) metrics.push({ name: 'iops', label: 'IOPS', unit: 'KIOPS' });
		if (names.has('bw_kb')) metrics.push({ name: 'bw_kb', label: 'Bandwidth', unit: 'MiB/s' });
		else if (names.has('bw_bytes')) metrics.push({ name: 'bw_bytes', label: 'Bandwidth', unit: 'MiB/s' });

		// tiotest
		for (const prefix of ['seq_write', 'seq_read', 'rand_write', 'rand_read']) {
			if (names.has(`${prefix}_mb_sec`)) metrics.push({ name: `${prefix}_mb_sec`, label: `${prefix} Throughput`, unit: 'MiB/s' });
			if (names.has(`${prefix}_lat_avg_ms`)) metrics.push({ name: `${prefix}_lat_avg_ms`, label: `${prefix} Avg Latency`, unit: 'ms' });
		}

		return metrics;
	});

	let hasCharts = $derived(chartMetrics.length > 0);

	// 매크로 결과 감지 (_score, _speed_mbs 키 패턴)
	let isMacroMetrics = $derived(
		Object.keys(effectiveMetrics).some(k => k.endsWith('_score') || k.endsWith('_speed_mbs'))
	);
	let activeChartRwTab = $state('read');

	// ══════════════════════════════════════════
	// Stats DataTable: 카테고리 탭 > Read/Write 탭
	// 행 = Cycle, 열 = metric별 컬럼, step 필터 적용
	// ══════════════════════════════════════════

	// Merge 모드에서 사용 가능한 step+rw 조합 목록
	let mergedStepRws = $derived.by(() => {
		if (!isMergeMode) return [];
		const combos: { step: number; rw: string; label: string }[] = [];
		const allMetrics = parsedMetrics;
		for (const s of [...mergedStepIds].sort((a, b) => a - b)) {
			const rwsInStep = [...new Set(allMetrics.filter(m => m.step === s).map(m => m.rw))].filter(r => r !== 'other');
			for (const rw of rwsInStep) {
				combos.push({ step: s, rw, label: `Step${s} ${rw}` });
			}
			// other
			if (allMetrics.some(m => m.step === s && m.rw === 'other')) {
				combos.push({ step: s, rw: 'other', label: `Step${s} other` });
			}
		}
		return combos;
	});

	let activeMergedRwTab = $state('');

	$effect(() => {
		const combos = mergedStepRws;
		if (combos.length > 0 && !combos.some(c => c.label === activeMergedRwTab)) {
			activeMergedRwTab = combos[0].label;
		}
	});

	function buildPivotData(cat: string, rw: 'read' | 'write' | 'other', stepFilter?: number) {
		const catMetrics = stepFilter != null
			? parsedMetrics.filter(m => m.category === cat && m.rw === rw && m.step === stepFilter)
			: viewMetrics.filter(m => m.category === cat && m.rw === rw);
		if (catMetrics.length === 0) return { rows: [] as Record<string, string>[], columns: [] as ColumnDef<Record<string, string>, unknown>[] };

		const metricNames = [...new Set(catMetrics.map(m => m.name))].sort();
		const cycs = [...new Set(catMetrics.map(m => m.cycle))].sort((a, b) => a - b);

		const columns: ColumnDef<Record<string, string>, unknown>[] = [];
		if (hasCycles) columns.push({ accessorKey: 'cycle', header: 'Cycle' });
		for (const name of metricNames) {
			const { displayUnit } = getUnitConversion(name);
			const header = displayUnit ? `${name} (${displayUnit})` : name;
			columns.push({ accessorKey: name, header });
		}

		const rows = cycs.map(c => {
			const row: Record<string, string> = {};
			if (hasCycles) row.cycle = formatCycleLabel(c);
			for (const name of metricNames) {
				const val = catMetrics.find(m => m.cycle === c && m.name === name)?.value;
				row[name] = val != null ? formatConvertedValue(val, name) : '-';
			}
			return row;
		});

		return { rows, columns };
	}

	let categoriesList = $derived.by(() => {
		const cats = new Set(viewMetrics.map(m => m.category));
		return ['iops', 'bw', 'slat', 'clat', 'lat', 'io', 'cpu', 'runtime', 'other'].filter(c => cats.has(c));
	});

	let activeStatTab = $state('iops');
	let activeRwTab = $state('read');

	$effect(() => {
		const cats = categoriesList;
		if (cats.length > 0 && !cats.includes(activeStatTab)) activeStatTab = cats[0];
		const rws = availableRw;
		if (rws.length > 0 && !rws.includes(activeRwTab as any)) activeRwTab = rws[0];
	});

	let hasMetrics = $derived(Object.keys(effectiveMetrics).length > 0);

	// ── Helpers ──
	function stateColor(s: string) {
		switch (s) {
			case 'completed': return 'bg-green-100 text-green-800';
			case 'running': case 'pushing_tools': case 'collecting': return 'bg-blue-100 text-blue-800';
			case 'failed': return 'bg-red-100 text-red-800';
			case 'partially_failed': return 'bg-orange-100 text-orange-800';
			case 'cancelled': return 'bg-orange-100 text-orange-800';
			default: return 'bg-gray-100 text-gray-800';
		}
	}
	function stateLabel(s: string) {
		switch (s) {
			case 'queued': return 'Queued'; case 'pushing_tools': return 'Pushing Tools';
			case 'running': return 'Running'; case 'collecting': return 'Collecting';
			case 'completed': return 'Completed'; case 'failed': return 'Failed';
			case 'partially_failed': return 'Partial Fail'; case 'cancelled': return 'Cancelled'; default: return s;
		}
	}
</script>

{#snippet chartGrid(rw: string)}
	{@const metrics = chartMetrics}
	{@const charts = metrics.map(m => ({ ...m, opt: buildCycleChart(m.name, rw as 'read' | 'write', m.unit, rw === 'read' ? '#5470c6' : '#fc8452') })).filter(c => c.opt != null)}
	<div class="grid grid-cols-2 gap-3">
		{#each charts as ch}
			<div class="border rounded-md p-2">
				<div class="text-[10px] font-medium mb-1">{ch.label} ({rw})</div>
				<PerfChart option={ch.opt} height="220px" />
			</div>
		{/each}
	</div>
	{#if charts.length === 0}
		<div class="text-[10px] text-muted-foreground text-center py-4">{rw} 데이터 없음</div>
	{/if}
{/snippet}

<Sheet.Root bind:open>
	<Sheet.Content side="bottom" class="h-screen flex flex-col">
		<Sheet.Header class="pb-2">
			<Sheet.Title class="text-sm flex items-center gap-2">
				Job 상세
				{#if jobStatus}
					<span class="px-1.5 py-0.5 rounded text-[10px] {stateColor(jobStatus.state)}">{stateLabel(jobStatus.state)}</span>
				{/if}
				{#if isRunning}
					<LoaderIcon class="size-3 animate-spin text-blue-600" />
					<span class="text-[10px] text-blue-600">실시간</span>
				{/if}
				<span class="font-mono text-[10px] text-muted-foreground ml-1" title={jobId ?? ''}>{(jobId ?? '').slice(0, 8)}</span>
				{#if jobId}
					<button onclick={() => { navigator.clipboard.writeText(jobId); toast.success('Job ID 복사됨'); }} class="p-0.5 rounded hover:bg-muted"><CopyIcon class="size-2.5 text-muted-foreground" /></button>
				{/if}
				{#if pollTimer}<LoaderIcon class="size-3 animate-spin text-muted-foreground" />{/if}
				<span class="ml-auto"></span>
				{#if traceJobIds.length > 0 && onViewTrace}
					<button
						onclick={() => {
							const ids = selectedTraceIds.size > 0 ? [...selectedTraceIds] : traceJobIds;
							onViewTrace(selectedResult?.deviceId ?? '', ids, allTraceJobMappings, getStepBoundaries());
						}}
						class="inline-flex items-center gap-1 rounded border px-2 py-0.5 text-[10px] hover:bg-muted"
					>
						<ScanSearchIcon class="size-3" /> Trace 분석 ({selectedTraceIds.size > 0 ? selectedTraceIds.size : traceJobIds.length})
					</button>
				{/if}
				{#if aiReachable && jobId}
					<button
						onclick={startAiAnalyze}
						disabled={aiRunning}
						class="inline-flex items-center gap-1 rounded border px-2 py-0.5 text-[10px] hover:bg-muted disabled:opacity-50"
						title="AI 로 벤치마크 결과를 자연어 해석"
					>
						{#if aiRunning}
							<LoaderIcon class="size-3 animate-spin" /> 해석 중...
						{:else}
							<SparklesIcon class="size-3" /> AI 해석
						{/if}
					</button>
				{/if}
				<button onclick={loadDetail} disabled={loading} class="p-1 rounded hover:bg-muted">
					<RefreshCwIcon class="size-3.5 {loading ? 'animate-spin' : ''}" />
				</button>
			</Sheet.Title>
			{#if jobStatus}
				<Sheet.Description class="text-xs">
					{jobStatus.completedDevices}/{jobStatus.totalDevices} completed
					{#if jobStatus.failedDevices > 0}&middot; <span class="text-red-600">{jobStatus.failedDevices} failed</span>{/if}
				</Sheet.Description>
			{/if}
		</Sheet.Header>

		<div class="flex-1 overflow-y-auto space-y-4 px-1">
			<!-- AI 해석 패널 (스트리밍) -->
			{#if aiReachable && (aiRunning || aiText || aiError)}
				<div class="border rounded-md bg-muted/20 p-2.5 space-y-1.5">
					<div class="flex items-center gap-1.5 text-[10px] font-semibold text-muted-foreground">
						<SparklesIcon class="size-3" /> AI 해석
						{#if aiRunning}<LoaderIcon class="size-3 animate-spin" />{/if}
					</div>
					{#if aiError}
						<div class="text-[11px] text-destructive">{aiError}</div>
					{:else}
						<div class="text-[11px] leading-relaxed whitespace-pre-wrap">{aiText}{#if aiRunning}<span class="inline-block w-1.5 h-3 -mb-0.5 bg-foreground/60 animate-pulse"></span>{/if}</div>
					{/if}
				</div>
			{/if}

			<!-- 시나리오 캔버스 추적 (접기/펼치기) -->
			{#if executionConfig?.steps && executionConfig.steps.length > 0}
				<div class="border rounded-md overflow-hidden">
					<button
						onclick={() => { showScenarioCanvas = !showScenarioCanvas; }}
						class="w-full flex items-center gap-1.5 px-3 py-1.5 text-xs font-semibold hover:bg-muted/50 transition-colors"
					>
						<span class="text-[10px]">{showScenarioCanvas ? '▾' : '▸'}</span>
						시나리오 진행
						{#if isRunning}
							<LoaderIcon class="size-3 animate-spin text-blue-600" />
							<span class="text-[10px] text-blue-600 font-normal">실행 중</span>
						{:else if jobStatus?.state === 'completed'}
							<span class="text-[10px] text-green-600 font-normal">완료</span>
						{:else if jobStatus?.state === 'failed' || jobStatus?.state === 'partially_failed'}
							<span class="text-[10px] text-red-600 font-normal">실패</span>
						{:else if jobStatus?.state === 'cancelled'}
							<span class="text-[10px] text-orange-600 font-normal">취소됨</span>
						{/if}
						<span class="ml-auto text-[9px] text-muted-foreground font-normal">{executionConfig.steps.length} steps</span>
					</button>
					{#if showScenarioCanvas}
						<div class="border-t">
							<ExecutionMiniCanvas
								stepsJson={JSON.stringify(executionConfig.steps)}
								loopsJson={executionConfig.loops ? JSON.stringify(executionConfig.loops) : undefined}
								activeJob={activeJob ?? null}
								finishedState={jobStatus?.state ?? null}
								finishedMessages={jobStatus?.deviceStatuses?.map(d => d.message).filter(Boolean) ?? []}
							/>
						</div>
					{/if}
				</div>
			{/if}

			{#if loading}
				<div class="flex items-center justify-center py-16"><LoaderIcon class="size-6 animate-spin text-muted-foreground" /></div>
			{:else if jobStatus}
				<!-- 워크로드 컨텍스트: 무엇이 돌았고 왜 이렇게 동작했나 (모든 job 공통) -->
				<!-- {#key jobId}: job 전환 시 배너를 재생성해 내부 localNote(메모) stale 방지 -->
				{#if Object.keys(bannerMetrics).length > 0 || (executionConfig?.steps && executionConfig.steps.length > 0)}
					{#key jobId}
						<WorkloadContextBanner
							{jobId}
							metrics={bannerMetrics}
							{executionConfig}
							{workloadNote}
						/>
					{/key}
				{/if}

				<!-- 전체 trace 확인 CTA — 이 job 에 trace 가 있으면 기존 trace UI(패턴/QD/CPU/latency)로 바로 진입 -->
				{#if allTraceJobMappings.length > 0 && onViewTrace}
					<button
						onclick={() => onViewTrace?.(selectedResult?.deviceId ?? persistedDeviceId, allTraceJobMappings.map(m => m.traceJobId), allTraceJobMappings, getStepBoundaries())}
						class="w-full flex items-center gap-2 rounded-md border border-blue-300 bg-blue-50 dark:bg-blue-950/30 px-3 py-2 text-left hover:bg-blue-100 dark:hover:bg-blue-950/50 transition-colors"
					>
						<ScanSearchIcon class="size-4 text-blue-600 shrink-0" />
						<div class="min-w-0">
							<div class="text-xs font-semibold text-blue-700 dark:text-blue-400">전체 trace 확인</div>
							<div class="text-[10px] text-muted-foreground">
								I/O 패턴 · QD · CPU · latency 분포를 한눈에 ({allTraceJobMappings.length}개 trace)
							</div>
						</div>
						<span class="ml-auto text-[10px] text-blue-600 shrink-0">→</span>
					</button>
				{/if}

				<!-- Device statuses -->
				<div>
					<h3 class="text-xs font-semibold mb-1">Devices</h3>
					<div class="flex flex-wrap gap-2">
						{#each jobStatus.deviceStatuses as ds}
							<div class="inline-flex items-center gap-1.5 border rounded-md px-2 py-1 text-[10px]">
								<span class="font-mono">{ds.deviceId}</span>
								<span class="px-1 py-0.5 rounded {stateColor(ds.state)}">{stateLabel(ds.state)}</span>
								<span class="text-muted-foreground">{ds.progressPercent}%</span>
							</div>
						{/each}
					</div>
				</div>

				{#if result && result.results.length > 1}
					<div class="flex items-center gap-2">
						<span class="text-[10px] font-medium text-muted-foreground">Device:</span>
						<select bind:value={selectedDevice} class="border rounded px-2 py-0.5 text-[10px] bg-background">
							{#each result.results as r}
								<option value={r.deviceId}>{r.deviceId} — {r.tool} {r.success ? '' : '(FAIL)'}</option>
							{/each}
						</select>
					</div>
				{/if}

				{#if selectedResult}
					<div class="flex items-center gap-2 text-[10px]">
						<span class="font-mono font-medium">{selectedResult.deviceId}</span>
						<span class="text-muted-foreground">{selectedResult.tool}</span>
						{#if selectedResult.success}
							<span class="px-1 py-0.5 rounded bg-green-100 text-green-700">OK</span>
						{:else}
							<span class="px-1 py-0.5 rounded bg-red-100 text-red-700">Fail</span>
						{/if}
						{#if hasCycles}<span class="text-muted-foreground">{cycles.length} cycles</span>{/if}
						{#if selectedResult.error}<span class="text-red-600">{selectedResult.error}</span>{/if}
					</div>
				{/if}

				{#if hasMetrics}
					{#if isRunning}
						<div class="flex items-center gap-1.5 text-[10px] text-blue-600">
							<LoaderIcon class="size-3 animate-spin" />
							<span>실시간 결과 수신 중... ({hasLoopData ? `${cycles.length} iterations` : `${cycles.length} cycles`} 완료)</span>
						</div>
					{/if}

					<!-- Repeat 탭 (loop가 있을 때) -->
					{#if hasLoopData && availableRepeats.length > 0}
						<div class="flex items-center gap-2 flex-wrap">
							<span class="text-[10px] font-medium text-muted-foreground">Cycle:</span>
							{#each availableRepeats as r}
								<button
									onclick={() => { selectedRepeat = r; }}
									class="px-2.5 py-0.5 rounded text-[10px] transition-colors
										{selectedRepeat === r ? 'bg-primary text-primary-foreground' : 'border hover:bg-muted'}"
								>
									#{r}
								</button>
							{/each}
						</div>
					{/if}

					<!-- Step selector -->
					{#if hasSteps}
						<div class="flex items-center gap-2 flex-wrap">
							<span class="text-[10px] font-medium text-muted-foreground">Step:</span>
							{#each steps as s}
								{#if mergeActive}
									<label
										class="inline-flex items-center gap-1 px-2.5 py-0.5 rounded text-[10px] cursor-pointer transition-colors
											{mergedStepIds.has(s) ? 'bg-primary text-primary-foreground' : 'border hover:bg-muted'}"
									>
										<input
											type="checkbox"
											checked={mergedStepIds.has(s)}
											onchange={() => toggleMergeStep(s)}
											class="size-2.5"
										/>
										Step {s}
									</label>
								{:else}
									<button
										onclick={() => { selectedStep = s; }}
										class="px-2.5 py-0.5 rounded text-[10px] transition-colors
											{selectedStep === s ? 'bg-primary text-primary-foreground' : 'border hover:bg-muted'}"
									>
										Step {s}
									</button>
								{/if}
							{/each}
							<span class="border-l h-4"></span>
							{#if mergeActive}
								<button
									onclick={clearMerge}
									class="px-2 py-0.5 rounded text-[10px] border hover:bg-muted text-muted-foreground"
								>
									개별 보기
								</button>
							{:else}
								<button
									onclick={enterMergeMode}
									class="px-2 py-0.5 rounded text-[10px] border hover:bg-muted font-medium text-blue-600 border-blue-300"
								>
									Step 비교
								</button>
							{/if}
						</div>
						{#if mergeActive}
							<p class="text-[9px] text-muted-foreground">
								{#if mergedStepIds.size >= 2}
									{mergedStepIds.size}개 Step 비교 중 — 체크 해제로 비교 대상을 조절하세요
								{:else if mergedStepIds.size === 1}
									1개 Step 선택됨 — 비교하려면 Step을 추가 선택하세요
								{:else}
									비교할 Step을 선택하세요
								{/if}
							</p>
						{/if}
					{/if}

					<!-- Trace Analysis: 요약 + 접기 -->
					{#if allTraceJobMappings.length > 0 && onViewTrace}
						{@const ids = traceJobIds}
						{@const mappings = traceJobMappings}

						<div class="border rounded-md p-2 space-y-2 bg-muted/20">
							<!-- 요약 바: 전체 분석 버튼 + 개수 + 접기 -->
							<div class="flex items-center gap-2">
								<span class="text-[10px] font-medium">Trace ({allTraceJobMappings.length}개)</span>

								{#if traceRepeatList.length > 1}
									<select
										value={selectedRepeat}
										onchange={(e) => { selectedRepeat = Number((e.target as HTMLSelectElement).value); selectedTraceIds = new Set(); }}
										class="border rounded px-1.5 py-0.5 text-[9px] bg-background"
									>
										<option value={0}>전체 Cycle</option>
										{#each traceRepeatList as r}
											<option value={r}>Cycle #{r} ({allTraceJobMappings.filter(m => m.repeatIndex === r).length}개)</option>
										{/each}
									</select>
								{/if}

								<div class="flex-1"></div>

								<button
									onclick={() => {
										const selected = selectedTraceIds.size > 0 ? [...selectedTraceIds] : ids;
										// mappings·boundaries 를 같이 넘긴다 — 빠지면 이 버튼으로 열었을 때만
										// Loop 필터와 Behavior 탭이 사라져 "버튼마다 화면이 다른" 것처럼 보인다.
										onViewTrace(selectedResult?.deviceId ?? '', selected, allTraceJobMappings, getStepBoundaries());
									}}
									class="px-2.5 py-1 rounded text-[10px] bg-blue-600 text-white hover:bg-blue-700 transition-colors"
								>
									분석 ({selectedTraceIds.size > 0 ? selectedTraceIds.size : ids.length}개)
								</button>

								<button
									onclick={() => { showTraceList = !showTraceList; }}
									class="px-2 py-0.5 rounded text-[9px] border hover:bg-muted text-muted-foreground"
								>
									{showTraceList ? '접기' : '개별 선택'}
								</button>
							</div>

							<!-- 개별 선택 (접기 가능) -->
							{#if showTraceList}
								<div class="flex items-center gap-1 text-[9px] mb-1">
									<button onclick={() => { selectedTraceIds = new Set(ids); }} class="px-1.5 py-0.5 rounded border hover:bg-muted">전체 선택</button>
									<button onclick={() => { selectedTraceIds = new Set(); }} class="px-1.5 py-0.5 rounded border hover:bg-muted">전체 해제</button>
									<span class="text-muted-foreground ml-1">{selectedTraceIds.size}개 선택됨</span>
								</div>
								<div class="max-h-32 overflow-y-auto flex flex-wrap gap-1">
									{#each mappings as m}
										<label
											class="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded text-[9px] cursor-pointer transition-colors
												{selectedTraceIds.has(m.traceJobId) ? 'bg-blue-100 text-blue-700' : 'border hover:bg-muted'}"
											title={m.traceJobId}
										>
											<input type="checkbox" checked={selectedTraceIds.has(m.traceJobId)}
												onchange={() => toggleTraceSelection(m.traceJobId)} class="size-2" />
											S{m.stepIndex}{m.loopIndex > 0 ? `-${m.loopIndex}` : ''}
										</label>
									{/each}
								</div>
							{/if}
						</div>
					{/if}

					<!-- 통합 결과 렌더러: step별 탭 + GenPerf-스타일 cycle 비교 -->
					<AgentResultRenderer metrics={effectiveMetrics} executionConfig={executionConfig} />
				{:else if isRunning}
					<div class="text-center text-xs text-muted-foreground py-8">
						<LoaderIcon class="size-4 animate-spin mx-auto mb-2" />
						벤치마크 실행 중... 결과가 들어오면 자동으로 표시됩니다
					</div>
				{:else}
					<div class="flex flex-col items-center justify-center py-8 text-muted-foreground space-y-2">
						<InboxIcon class="size-8 mb-1 opacity-30" />
						{#if jobStatus && (jobStatus.state === 'failed' || jobStatus.state === 'partially_failed' || jobStatus.state === 'cancelled')}
							<p class="text-xs font-medium {jobStatus.state === 'cancelled' ? 'text-orange-600' : 'text-red-600'}">
								{jobStatus.state === 'cancelled' ? '실행이 중단되었습니다' : '실행 중 오류가 발생했습니다'}
							</p>
							{#each jobStatus.deviceStatuses as ds}
								{#if ds.message && ds.state !== 'completed'}
									<div class="border rounded px-3 py-2 text-[10px] bg-muted/30 max-w-lg w-full">
										<div class="font-mono text-[9px] text-muted-foreground mb-0.5">{ds.deviceId}</div>
										<div class="text-red-600 break-all">{ds.message}</div>
									</div>
								{/if}
							{/each}
							{#if selectedResult?.rawOutput}
								<details class="text-[10px] max-w-lg w-full">
									<summary class="text-muted-foreground cursor-pointer hover:text-foreground">Raw Output 보기</summary>
									<pre class="mt-1 border rounded px-2 py-1 bg-muted/30 text-[9px] font-mono overflow-x-auto max-h-40 overflow-y-auto whitespace-pre-wrap">{selectedResult.rawOutput}</pre>
								</details>
							{/if}
						{:else}
							<p class="text-xs">결과 데이터가 없습니다</p>
							<p class="text-[10px]">벤치마크가 정상적으로 완료되었는지 확인해주세요</p>
						{/if}
					</div>
				{/if}
			{:else}
				<div class="text-center text-xs text-muted-foreground py-16">Job을 선택해주세요</div>
			{/if}
		</div>
	</Sheet.Content>
</Sheet.Root>
