<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { DataTable } from '$lib/components/data-table';
	import TraceScatterChart from './TraceScatterChart.svelte';
	import AiChatPanel from './AiChatPanel.svelte';
	import AgentAttributionView from './AgentAttributionView.svelte';
	import { captionMuted } from '$lib/styles/common.js';
	import { toast } from 'svelte-sonner';
	import { onDestroy } from 'svelte';
	import { getTraceResult, getTraceRawData, reparseTrace, getJobStatus, fetchExecutionByJobId, getAiStatus, type TraceFilter, type TraceStats, type TraceEvent, type TraceRawDataResult, type LatencyStats, type JobExecutionRecord } from '$lib/api/agent.js';
	import { getArchivedStats } from '$lib/api/agentTraceArchive.js';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import FilterIcon from '@lucide/svelte/icons/filter';
	import XIcon from '@lucide/svelte/icons/x';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import type { ColumnDef } from '@tanstack/table-core';

	// trace job → loop/step/repeat 매핑 (loop 필터용). 없으면 필터 UI 를 숨긴다.
	interface TraceJobMapping {
		traceJobId: string;
		stepIndex?: number;
		loopIndex?: number;
		repeatIndex?: number;
		/** "ufs" | "block" | "both" | "fsio_ufs" | "fsio_block". mgmt/Attribution 노출 판단용. */
		traceType?: string;
	}

	interface Props {
		open: boolean;
		serverId: number | null;
		jobIds: string[];
		// 넘겨받은 jobIds 각각의 loop/step 정보. 있으면 "Loop N" 필터를 노출한다.
		mappings?: TraceJobMapping[];
	}

	let { open = $bindable(), serverId, jobIds, mappings = [] }: Props = $props();

	// ── Loop 필터 ──
	// selectedLoop: 0 = 전체, 그 외 = 해당 loopIndex 만.
	let selectedLoop = $state(0);
	// 사용 가능한 loop 목록 (오름차순, loopIndex>0 인 것만).
	const loopOptions = $derived.by(() => {
		const set = new Set<number>();
		for (const m of mappings) {
			if (m.loopIndex && m.loopIndex > 0) set.add(m.loopIndex);
		}
		return [...set].sort((a, b) => a - b);
	});
	// 실제 조회에 쓸 jobIds — loop 선택 시 해당 loop 의 traceJobId 만.
	const activeJobIds = $derived.by(() => {
		if (selectedLoop <= 0 || mappings.length === 0) return jobIds;
		const ids = mappings.filter(m => m.loopIndex === selectedLoop).map(m => m.traceJobId);
		return ids.length > 0 ? ids : jobIds;
	});

	// Attribution 탭이 쓰는 필터. 현재 시트는 서버사이드 필터를 쓰지 않으므로 빈 객체를
	// 넘긴다 — 드릴다운은 아래 핸들러가 받아 UI 안내만 한다(서버 필터 연동은 후속).
	function attributionFilter(): TraceFilter {
		return {};
	}

	/**
	 * Attribution 행 클릭 → 해당 값으로 좁혀 보기.
	 *
	 * portal 은 이걸 서버사이드 필터로 흘려 chart/stats/raw 를 함께 좁힌다. agent 시트는
	 * 아직 그 필터 파이프라인이 없어서 지금은 알림만 띄운다 — 값을 조용히 무시하면
	 * "클릭했는데 아무 일도 안 일어난다" 가 되므로 명시적으로 알린다.
	 */
	function handleAttrDrillDown(dim: string, key: string, _additive: boolean) {
		toast.info(`${dim} = ${key}`, {
			description: '필터 연동은 후속 작업입니다 — 현재는 값 확인만 가능합니다.'
		});
	}

	// 활성 잡의 trace_type. mgmt 섹션과 Attribution 탭은 fsio 에서만 의미가 있다.
	// mappings 가 비어 있으면(단독 trace 실행) 통계 조회 때 알아낸 값으로 폴백한다.
	let fallbackTraceType = $state<string | null>(null);
	const activeTraceType = $derived.by<string | null>(() => {
		const ids = new Set(activeJobIds);
		for (const m of mappings) {
			if (ids.has(m.traceJobId) && m.traceType) return m.traceType;
		}
		return fallbackTraceType;
	});
	const isFsio = $derived(activeTraceType === 'fsio_ufs' || activeTraceType === 'fsio_block');

	// 다른 job 의 trace 를 열면 loop 선택을 초기화한다 (이전 job 의 Loop N 잔존 방지).
	// jobIds 첫 값만 의존 → selectedLoop 쓰기가 재실행을 유발하지 않음.
	let lastFirstJob = $state('');
	$effect(() => {
		const first = jobIds[0] ?? '';
		if (first !== lastFirstJob) {
			lastFirstJob = first;
			selectedLoop = 0;
		}
	});

	// ── State ──
	let loadingRaw = $state(false);
	let loadingStats = $state(false);
	let rawResult = $state<TraceRawDataResult | null>(null);
	let statsResult = $state<TraceStats | null>(null);
	let showFilter = $state(true);
	let showBrushInfo = $state(true);
	let reparsing = $state(false);
	let reparseError = $state('');
	let reparsePollTimer: ReturnType<typeof setInterval> | null = null;

	// ── Archive state (옵션 A — agent 종료 후에도 영속 조회) ──
	let archiveExec = $state<JobExecutionRecord | null>(null);

	async function loadArchiveState() {
		if (jobIds.length !== 1) { archiveExec = null; return; }
		try {
			archiveExec = await fetchExecutionByJobId(jobIds[0]);
		} catch {
			archiveExec = null;
		}
	}

	// 모드: live | archived | unparsed | parsing
	const archiveMode = $derived.by(() => {
		if (!archiveExec) return 'live';
		const s = archiveExec.traceParseState;
		if (s === 'PARSING' || s === 'UPLOADING') return 'parsing';
		if (archiveExec.traceParsedAt) return 'archived';
		if (archiveExec.traceRawKey) return 'unparsed';
		return 'live';
	});

	async function handleReparse() {
		if (!serverId || jobIds.length !== 1) return;
		reparsing = true;
		reparseError = '';
		try {
			const res = await reparseTrace(serverId, jobIds[0]);
			if (!res.success) {
				reparseError = res.message;
				reparsing = false;
				toast.error(res.message);
				return;
			}
			toast.success('Reparse 시작됨');
			startReparsePoll();
		} catch (e: any) {
			reparseError = e.message ?? 'Reparse 실패';
			reparsing = false;
			toast.error(reparseError);
		}
	}

	function startReparsePoll() {
		stopReparsePoll();
		reparsePollTimer = setInterval(async () => {
			if (!serverId || jobIds.length === 0) return;
			try {
				const status = await getJobStatus(serverId, jobIds[0]);
				if (status.state === 'completed') {
					stopReparsePoll();
					reparsing = false;
					toast.success('Reparse 완료');
					loadData();
				} else if (status.state === 'failed') {
					stopReparsePoll();
					reparsing = false;
					reparseError = 'Reparse 실패';
					toast.error('Reparse 실패');
				}
			} catch { /* ignore */ }
		}, 2000);
	}

	function stopReparsePoll() {
		if (reparsePollTimer) { clearInterval(reparsePollTimer); reparsePollTimer = null; }
	}

	// ── AI 해석 (채팅) ──
	// reachable=true 일 때만 노출. 대화 상태는 AiChatPanel 이 소유하고, 시트는 참조만 쥔다.
	let aiReachable = $state(false);
	let aiPanel = $state<AiChatPanel | null>(null);

	// "AI 해석" 버튼 — 첫 턴(전체 해석)을 자동 실행하고, 이후엔 패널에서 이어서 질문한다.
	function startAiAnalyze() {
		if (!serverId || activeJobIds.length === 0) return;
		// 패널은 Statistics 탭에 있으므로, 어느 탭에서 눌러도 결과가 보이게 탭을 전환한다.
		mainTab = 'stats';
		aiPanel?.startOverview();
	}

	// 시트 열릴 때 reparsing 상태 확인
	onDestroy(() => { stopReparsePoll(); });

	$effect(() => {
		if (open && serverId && jobIds.length === 1) {
			getJobStatus(serverId, jobIds[0]).then(status => {
				if (status.state === 'reparsing') {
					reparsing = true;
					startReparsePoll();
				}
			}).catch(() => {});
			loadArchiveState();
			// AI 도달 가능 여부 조회 (실패/비활성이면 조용히 숨김)
			getAiStatus().then(s => { aiReachable = !!(s.enabled && s.reachable); }).catch(() => { aiReachable = false; });
		}
		if (!open) {
			stopReparsePoll(); reparsing = false; archiveExec = null;
			aiPanel?.reset();
		}
	});

	const hasActiveFilter = $derived(
		!!(filterStartTime || filterEndTime || filterStartLba || filterEndLba ||
		   filterMinDtoc || filterMaxDtoc || filterMinCtod || filterMaxCtod ||
		   filterMinCtoc || filterMaxCtoc || filterMinQd || filterMaxQd)
	);
	let mainTab = $state('raw');

	// Filter state
	let filterStartTime = $state('');
	let filterEndTime = $state('');
	let filterStartLba = $state('');
	let filterEndLba = $state('');
	let filterMinDtoc = $state('');
	let filterMaxDtoc = $state('');
	let filterMinCtod = $state('');
	let filterMaxCtod = $state('');
	let filterMinCtoc = $state('');
	let filterMaxCtoc = $state('');
	let filterMinQd = $state('');
	let filterMaxQd = $state('');
	let latencyRangesText = $state('0.1, 0.5, 1, 5, 10, 50, 100, 500, 1000');

	// Legend sync across charts
	let legendSelected = $state<Record<string, boolean>>({});

	// Chart item selection (sidebar toggle)
	const CHART_ITEMS = [
		{ key: 'lba', label: 'LBA', yLabel: 'LBA', group: 'common' },
		{ key: 'qd', label: 'Queue Depth', yLabel: 'QD', group: 'common' },
		{ key: 'cpu', label: 'CPU', yLabel: 'CPU', group: 'common' },
		{ key: 'ctod', label: 'CtoD Latency', yLabel: 'CtoD (ms)', group: 'send' },
		{ key: 'dtoc', label: 'DtoC Latency', yLabel: 'DtoC (ms)', group: 'complete' },
		{ key: 'ctoc', label: 'CtoC Latency', yLabel: 'CtoC (ms)', group: 'complete' }
	] as const;
	let visibleCharts = $state<Set<string>>(new Set(['lba', 'qd', 'dtoc']));

	// Action tab: Send vs Complete
	let activeActionTab = $state('complete');

	// Map action to tab: send_req/block_rq_issue → 'send', complete_rsp/block_rq_complete → 'complete'
	function actionToTab(action: string): string {
		if (action.includes('send') || action.includes('issue')) return 'send';
		if (action.includes('complete') || action.includes('rsp')) return 'complete';
		return 'other';
	}

	// Filtered events by action tab
	let filteredEvents = $derived<TraceEvent[]>(() => {
		if (!rawResult) return [];
		if (activeActionTab === 'all') return rawResult.events;
		return rawResult.events.filter(e => actionToTab(e.action) === activeActionTab);
	});

	// Available chart items based on action tab
	let availableChartItems = $derived(
		activeActionTab === 'send'
			? CHART_ITEMS.filter(c => c.group === 'common' || c.group === 'send')
			: activeActionTab === 'complete'
				? CHART_ITEMS.filter(c => c.group === 'common' || c.group === 'complete')
				: CHART_ITEMS // all
	);

	function toggleChart(key: string) {
		const next = new Set(visibleCharts);
		if (next.has(key)) { if (next.size > 1) next.delete(key); } // 최소 1개
		else next.add(key);
		visibleCharts = next;
	}

	const defaultChartHeight = $derived(visibleCharts.size === 1 ? 500 : visibleCharts.size === 2 ? 350 : 280);
	let userChartHeight = $state<number | null>(null);
	let userChartWidth = $state<string | null>(null);
	const chartHeightPx = $derived(userChartHeight ?? defaultChartHeight);
	const chartHeight = $derived(`${chartHeightPx}px`);

	// 카드 리사이즈 감지 → 모든 카드 높이+너비 동기화
	function observeResize(node: HTMLElement) {
		const ro = new ResizeObserver(() => {
			const newH = node.offsetHeight;
			const newW = node.offsetWidth;
			if (newH > 0 && Math.abs(newH - chartHeightPx) > 5) {
				userChartHeight = Math.max(150, Math.min(800, newH));
			}
			if (newW > 0) {
				userChartWidth = `${newW}px`;
			}
		});
		ro.observe(node);
		return { destroy: () => ro.disconnect() };
	}

	// CMD colors — 계열별 색상 톤
	// Read 계열: 파란색, Write 계열: 주황/빨강, Flush 계열: 초록, Discard/Trim 계열: 보라
	const CMD_COLOR_MAP: Record<string, string[]> = {
		// Read 계열 (파랑)
		read: ['#3b82f6', '#2563eb', '#1d4ed8', '#60a5fa', '#93c5fd'],
		// Write 계열 (주황/빨강)
		write: ['#f97316', '#ea580c', '#dc2626', '#fb923c', '#fdba74'],
		// Flush 계열 (초록)
		flush: ['#22c55e', '#16a34a', '#15803d', '#4ade80', '#86efac'],
		// Discard/Trim/Unmap 계열 (보라)
		discard: ['#a855f7', '#9333ea', '#7c3aed', '#c084fc', '#d8b4fe'],
		// 기타 (회색/청록)
		other: ['#64748b', '#475569', '#6b7280', '#94a3b8', '#78716c']
	};

	// cmd별 계열 인덱스 카운터
	const cmdColorAssigned: Record<string, string> = {};
	const groupCounters: Record<string, number> = { read: 0, write: 0, flush: 0, discard: 0, other: 0 };

	// SCSI opcode → group 매핑 (UFS)
	const SCSI_CMD_GROUPS: Record<string, string> = {
		'0x28': 'read',   // READ(10)
		'0xa8': 'read',   // READ(12)
		'0x88': 'read',   // READ(16)
		'0x08': 'read',   // READ(6)
		'0x2a': 'write',  // WRITE(10)
		'0xaa': 'write',  // WRITE(12)
		'0x8a': 'write',  // WRITE(16)
		'0x0a': 'write',  // WRITE(6)
		'0x2e': 'write',  // WRITE AND VERIFY(10)
		'0x35': 'flush',  // SYNCHRONIZE CACHE(10)
		'0x91': 'flush',  // SYNCHRONIZE CACHE(16)
		'0x42': 'discard', // UNMAP
		'0x12': 'other',  // INQUIRY
		'0x1a': 'other',  // MODE SENSE(6)
		'0x5a': 'other',  // MODE SENSE(10)
		'0x25': 'other',  // READ CAPACITY(10)
		'0x00': 'other',  // TEST UNIT READY
	};

	function getCmdGroup(cmd: string): string {
		const lower = cmd.toLowerCase().trim();
		if (!lower) return 'other';

		// SCSI hex opcode: 0x28, 0x2a 등
		if (lower.startsWith('0x')) {
			return SCSI_CMD_GROUPS[lower] ?? 'other';
		}

		// 전체 단어 매칭 (긴 키워드 우선)
		if (lower.includes('discard') || lower.includes('trim') || lower.includes('unmap')) return 'discard';
		if (lower.includes('flush') || lower.includes('sync')) return 'flush';
		if (lower.includes('write')) return 'write';
		if (lower.includes('read')) return 'read';

		// Block trace io_type prefix (R/W/D/F + RA, WS, WSF, FUA 등 변형) — Rust 파서가 첫 글자로 분류.
		const first = lower[0];
		if (first === 'r') return 'read';
		if (first === 'w') return 'write';
		if (first === 'd') return 'discard';
		if (first === 'f') return 'flush';

		return 'other';
	}

	function getCmdColor(cmd: string): string {
		if (!cmdColorAssigned[cmd]) {
			const group = getCmdGroup(cmd);
			const palette = CMD_COLOR_MAP[group];
			const idx = groupCounters[group] % palette.length;
			groupCounters[group]++;
			cmdColorAssigned[cmd] = palette[idx];
		}
		return cmdColorAssigned[cmd];
	}

	// ── Load on open / loop 필터 변경 시 재로딩 ──
	// activeJobIds 를 참조하므로 selectedLoop 가 바뀌면 이 effect 가 다시 돈다.
	$effect(() => {
		const ids = activeJobIds; // 반응성 의존성 등록
		if (open && serverId != null && ids.length > 0) {
			loadRawData();
			loadStats();
		}
	});

	function buildFilter(): TraceFilter | undefined {
		const f: TraceFilter = {};
		if (filterStartTime) f.startTime = Number(filterStartTime);
		if (filterEndTime) f.endTime = Number(filterEndTime);
		if (filterStartLba) f.startLba = Math.max(0, Number(filterStartLba));
		if (filterEndLba) f.endLba = Math.max(0, Number(filterEndLba));
		if (filterMinDtoc) f.minDtoc = Number(filterMinDtoc);
		if (filterMaxDtoc) f.maxDtoc = Number(filterMaxDtoc);
		if (filterMinCtod) f.minCtod = Number(filterMinCtod);
		if (filterMaxCtod) f.maxCtod = Number(filterMaxCtod);
		if (filterMinCtoc) f.minCtoc = Number(filterMinCtoc);
		if (filterMaxCtoc) f.maxCtoc = Number(filterMaxCtoc);
		if (filterMinQd) f.minQd = Number(filterMinQd);
		if (filterMaxQd) f.maxQd = Number(filterMaxQd);
		return Object.keys(f).length > 0 ? f : undefined;
	}

	function parseLatencyRanges(): number[] {
		return latencyRangesText.split(',').map(s => Number(s.trim())).filter(n => !isNaN(n) && n > 0);
	}

	async function loadRawData(filter?: TraceFilter) {
		if (serverId == null || activeJobIds.length === 0) return;
		loadingRaw = true;
		try {
			rawResult = await getTraceRawData(serverId, { jobIds: activeJobIds, filter });
		} catch (e) { console.error('Trace raw error:', e); toast.error('Raw data 조회 실패'); }
		finally { loadingRaw = false; }
	}

	async function loadStats(filter?: TraceFilter) {
		if (serverId == null || activeJobIds.length === 0) return;
		loadingStats = true;
		try {
			// archived 모드 (Rust 정확 파서 결과) → Rust trace 서비스 경로로 조회
			if (archiveMode === 'archived' && jobIds.length === 1 && archiveExec?.tool) {
				const traceType = (archiveExec.tool || 'ufs').toLowerCase();
				const tt = traceType === 'both' ? 'ufs' : traceType;
				const res = await getArchivedStats({
					serverId,
					jobId: jobIds[0],
					traceType: tt,
					filter: filter as Record<string, unknown> | undefined,
					latencyRangesMs: parseLatencyRanges()
				});
				statsResult = res.stats as unknown as TraceStats;
			} else {
				const res = await getTraceResult(serverId, { jobIds: activeJobIds, filter, latencyRangesMs: parseLatencyRanges() });
				statsResult = res.stats;
			}
		} catch (e) {
			console.error('Trace stats error:', e);
			toast.error('통계 조회 실패');
		}
		finally { loadingStats = false; }
	}

	function applyFilter() {
		const f = buildFilter();
		loadRawData(f);
		loadStats(f);
	}

	function resetFilter() {
		filterStartTime = ''; filterEndTime = '';
		filterStartLba = ''; filterEndLba = '';
		filterMinDtoc = ''; filterMaxDtoc = '';
		filterMinCtod = ''; filterMaxCtod = '';
		filterMinCtoc = ''; filterMaxCtoc = '';
		filterMinQd = ''; filterMaxQd = '';
		statsResult = null;
		loadRawData();
	}

	function handleBrushSelected(ranges: { timeMin: number; timeMax: number; yMin: number; yMax: number; chartKey?: string }) {
		filterStartTime = String(ranges.timeMin);
		filterEndTime = String(ranges.timeMax);

		// Y축 필터: chartKey에 따라 해당 필드에 반영
		if (ranges.chartKey && ranges.yMin != null && ranges.yMax != null) {
			switch (ranges.chartKey) {
				case 'lba':
					filterStartLba = String(Math.max(0, Math.floor(ranges.yMin)));
					filterEndLba = String(Math.max(0, Math.ceil(ranges.yMax)));
					break;
				case 'qd':
					filterMinQd = String(Math.max(0, Math.floor(ranges.yMin)));
					filterMaxQd = String(Math.max(0, Math.ceil(ranges.yMax)));
					break;
				case 'dtoc':
					filterMinDtoc = String(ranges.yMin);
					filterMaxDtoc = String(ranges.yMax);
					break;
				case 'ctod':
					filterMinCtod = String(ranges.yMin);
					filterMaxCtod = String(ranges.yMax);
					break;
				case 'ctoc':
					filterMinCtoc = String(ranges.yMin);
					filterMaxCtoc = String(ranges.yMax);
					break;
			}
		}

		showFilter = true;
		applyFilter();
	}

	function handleLegendChanged(selected: Record<string, boolean>) {
		legendSelected = selected;
	}

	// ── Scatter chart builders ──
	// latency 필드는 0값 제외 (send 이벤트의 latency는 0)
	const LATENCY_FIELDS = new Set(['dtoc', 'ctod', 'ctoc']);

	function buildScatter(events: TraceEvent[], yField: keyof TraceEvent, yLabel: string) {
		const excludeZero = LATENCY_FIELDS.has(yField);
		const cmdSet = [...new Set(events.map(e => e.cmd))];
		const series = cmdSet.map(cmd => ({
			name: cmd,
			type: 'scatter' as const,
			data: events
				.filter(e => e.cmd === cmd && (!excludeZero || (e[yField] as number) > 0))
				.map(e => [e.time, e[yField]]),
			symbolSize: 2,
			itemStyle: { color: getCmdColor(cmd) }
		}));
		return {
			tooltip: { trigger: 'item' as const, formatter: (p: any) => `${p.seriesName}<br/>time: ${p.data[0]}<br/>${yLabel}: ${p.data[1]}` },
			legend: { data: cmdSet, top: 0, right: 0, textStyle: { fontSize: 9 }, selected: legendSelected },
			xAxis: { type: 'value' as const, name: 'Time (s)', min: 'dataMin', nameTextStyle: { fontSize: 9 }, axisLabel: { fontSize: 8 } },
			yAxis: { type: 'value' as const, name: yLabel, nameTextStyle: { fontSize: 9 }, axisLabel: { fontSize: 8 } },
			series,
			grid: { left: 60, right: 20, top: 25, bottom: 35 },
			dataZoom: [{ type: 'inside' as const }]
		};
	}

	function getChartOption(key: string): ReturnType<typeof buildScatter> {
		const events = filteredEvents();
		if (events.length === 0) return null;
		const item = CHART_ITEMS.find(c => c.key === key);
		if (!item) return null;
		return buildScatter(events, key as keyof TraceEvent, item.yLabel);
	}

	// ── Stats helpers ──
	function fmtDuration(seconds: number): string {
		if (seconds < 0) seconds = Math.abs(seconds);
		if (seconds < 60) return `${seconds.toFixed(2)}s`;
		const min = Math.floor(seconds / 60);
		const sec = seconds % 60;
		return `${min}m ${sec.toFixed(1)}s`;
	}

	function fmtBytes(bytes: number): string {
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
		if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
		return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`;
	}

	function fmtLatency(v: number): string {
		if (v < 0.001) return v.toFixed(6);
		if (v < 1) return v.toFixed(3);
		return v.toLocaleString(undefined, { maximumFractionDigits: 2 });
	}

	const latencyFields: { key: keyof LatencyStats; label: string }[] = [
		{ key: 'min', label: 'Min' }, { key: 'max', label: 'Max' },
		{ key: 'avg', label: 'Avg' }, { key: 'stddev', label: 'StdDev' },
		{ key: 'median', label: 'Median' }, { key: 'p99', label: 'P99' },
		{ key: 'p999', label: 'P99.9' }, { key: 'p9999', label: 'P99.99' },
		{ key: 'p99999', label: 'P99.999' }, { key: 'p999999', label: 'P99.9999' }
	];

	let activeLatencyTab = $state('dtoc');

	// Duration from raw data (max time - min time)
	let rawDurationSeconds = $derived<number | null>(() => {
		if (!rawResult || rawResult.events.length < 2) return null;
		return rawResult.events[rawResult.events.length - 1].time - rawResult.events[0].time;
	});
</script>

<Sheet.Root bind:open>
	<Sheet.Content side="bottom" class="h-screen flex flex-col">
		<Sheet.Header class="pb-2">
			<Sheet.Title class="text-sm flex items-center gap-2">
				Trace Analysis
				{#if rawResult?.isSampled}
					<span class="px-1.5 py-0.5 rounded text-[10px] bg-yellow-100 text-yellow-800">
						Sampled: {rawResult.sampledEvents?.toLocaleString()}/{rawResult.totalEvents?.toLocaleString()}
					</span>
				{/if}
				<span class="font-mono text-[10px] text-muted-foreground">{activeJobIds.length}/{jobIds.length} job(s)</span>
				{#if loopOptions.length > 1}
					<div class="inline-flex items-center gap-0.5 rounded border p-0.5" title="반복(loop)별로 나눠 보기">
						<button
							class="px-1.5 py-0.5 rounded text-[10px] transition-colors {selectedLoop === 0 ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300' : 'hover:bg-muted'}"
							onclick={() => selectedLoop = 0}>전체</button>
						{#each loopOptions as lp}
							<button
								class="px-1.5 py-0.5 rounded text-[10px] transition-colors {selectedLoop === lp ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300' : 'hover:bg-muted'}"
								onclick={() => selectedLoop = lp}>Loop {lp}</button>
						{/each}
					</div>
				{/if}
				<div class="ml-auto mr-6 inline-flex items-center gap-1">
					{#if aiReachable && activeJobIds.length > 0}
						<button
							class="inline-flex items-center gap-1 px-2 py-1 text-[10px] rounded border hover:bg-muted transition-colors"
							onclick={startAiAnalyze}
							title="AI 로 trace 통계를 해석하고 이어서 질문"
						>
							<SparklesIcon class="size-3" /> AI 해석
						</button>
					{/if}
					{#if jobIds.length === 1 && !reparsing}
						<button
							class="inline-flex items-center gap-1 px-2 py-1 text-[10px] rounded border hover:bg-muted transition-colors"
							onclick={handleReparse}
						>
							<RefreshCwIcon class="size-3" /> Reparse
						</button>
					{:else if reparsing}
						<span class="inline-flex items-center gap-1 text-[10px] text-amber-600">
							<LoaderIcon class="size-3 animate-spin" /> Reparsing...
						</span>
					{/if}
				</div>
				</Sheet.Title>
			<Sheet.Description class="text-xs">
				{#if jobIds.length <= 5}
					<div class="flex flex-wrap gap-1 mt-1">
						{#each jobIds as id}
							<span class="font-mono text-[9px] border rounded px-1.5 py-0.5">{id.slice(0, 8)}</span>
						{/each}
					</div>
				{:else}
					<span class="text-[9px] text-muted-foreground">{jobIds.length}개 trace job 분석</span>
				{/if}
			</Sheet.Description>
		</Sheet.Header>

		<div class="flex-1 overflow-y-auto space-y-3 px-1 relative">
			{#if reparsing}
				<div class="absolute inset-0 bg-background/80 z-40 flex flex-col items-center justify-center gap-3 backdrop-blur-sm">
					<LoaderIcon class="size-8 animate-spin text-primary" />
					<div class="text-sm font-medium">Trace 데이터를 재분석 중입니다...</div>
					<div class="{captionMuted}">페이지를 닫아도 백그라운드에서 계속 진행됩니다</div>
				</div>
			{/if}

			<!-- Archive Panel 제거: standalone 에서는 MinIO archive 흐름 미사용
			     (trace 결과는 로컬 parquet 으로 즉시 조회) -->

			<!-- Filter bar (collapsible) -->
			<div class="border rounded-md bg-muted/30 overflow-hidden">
				<button
					class="w-full flex items-center gap-1.5 px-2 py-1.5 text-[10px] font-medium hover:bg-muted/50 transition-colors"
					onclick={() => showFilter = !showFilter}
				>
					<ChevronRight class="size-3 transition-transform duration-200 {showFilter ? 'rotate-90' : ''}" />
					<FilterIcon class="size-3" />
					Filter
					{#if hasActiveFilter}
						<span class="size-1.5 rounded-full bg-blue-500"></span>
					{/if}
				</button>
				{#if showFilter}
					<div class="px-2 pb-2 space-y-2 border-t">
						<div class="grid grid-cols-6 gap-2 text-[9px] mt-2">
							<div><label class="text-muted-foreground">Time min (s)</label><input bind:value={filterStartTime} class="w-full border rounded px-1 py-0.5 bg-background font-mono" /></div>
							<div><label class="text-muted-foreground">Time max (s)</label><input bind:value={filterEndTime} class="w-full border rounded px-1 py-0.5 bg-background font-mono" /></div>
							<div><label class="text-muted-foreground">LBA min</label><input bind:value={filterStartLba} class="w-full border rounded px-1 py-0.5 bg-background font-mono" /></div>
							<div><label class="text-muted-foreground">LBA max</label><input bind:value={filterEndLba} class="w-full border rounded px-1 py-0.5 bg-background font-mono" /></div>
							<div><label class="text-muted-foreground">QD min</label><input bind:value={filterMinQd} class="w-full border rounded px-1 py-0.5 bg-background font-mono" /></div>
							<div><label class="text-muted-foreground">QD max</label><input bind:value={filterMaxQd} class="w-full border rounded px-1 py-0.5 bg-background font-mono" /></div>
						</div>
						<div class="grid grid-cols-6 gap-2 text-[9px]">
							<div><label class="text-muted-foreground">DtoC min</label><input bind:value={filterMinDtoc} class="w-full border rounded px-1 py-0.5 bg-background font-mono" /></div>
							<div><label class="text-muted-foreground">DtoC max</label><input bind:value={filterMaxDtoc} class="w-full border rounded px-1 py-0.5 bg-background font-mono" /></div>
							<div><label class="text-muted-foreground">CtoD min</label><input bind:value={filterMinCtod} class="w-full border rounded px-1 py-0.5 bg-background font-mono" /></div>
							<div><label class="text-muted-foreground">CtoD max</label><input bind:value={filterMaxCtod} class="w-full border rounded px-1 py-0.5 bg-background font-mono" /></div>
							<div><label class="text-muted-foreground">CtoC min</label><input bind:value={filterMinCtoc} class="w-full border rounded px-1 py-0.5 bg-background font-mono" /></div>
							<div><label class="text-muted-foreground">CtoC max</label><input bind:value={filterMaxCtoc} class="w-full border rounded px-1 py-0.5 bg-background font-mono" /></div>
						</div>
						<div class="text-[9px]">
							<label class="text-muted-foreground">Latency Ranges (ms, comma-separated)</label>
							<input bind:value={latencyRangesText} class="w-full border rounded px-1 py-0.5 bg-background font-mono" />
						</div>
						<div class="flex gap-1">
							<button onclick={applyFilter} class="inline-flex items-center gap-1 rounded bg-blue-600 text-white px-2 py-0.5 text-[9px] hover:bg-blue-700">
								{#if loadingStats}<LoaderIcon class="size-2.5 animate-spin" />{/if} 조회
							</button>
							<button onclick={resetFilter} class="inline-flex items-center gap-1 rounded border px-2 py-0.5 text-[9px] hover:bg-muted">
								<XIcon class="size-2.5" /> 초기화
							</button>
						</div>
					</div>
				{/if}
			</div>

			{#if filterStartTime || filterEndTime}
				<div class="flex items-center gap-2 text-[10px] text-blue-600">
					<button
						class="flex items-center gap-1 hover:text-blue-800 transition-colors"
						onclick={() => showBrushInfo = !showBrushInfo}
					>
						<ChevronRight class="size-3 transition-transform duration-200 {showBrushInfo ? 'rotate-90' : ''}" />
						선택 영역
					</button>
					{#if showBrushInfo}
						<span>Time {filterStartTime || '0'} ~ {filterEndTime || '∞'} s</span>
						{#if !statsResult && !loadingStats}
							<button onclick={applyFilter} class="rounded border border-blue-300 px-2 py-0.5 text-[9px] hover:bg-blue-50">통계 + Raw 조회</button>
						{/if}
					{/if}
				</div>
			{/if}

			<Tabs.Root bind:value={mainTab}>
				<Tabs.List class="flex gap-0.5">
					<Tabs.Trigger value="raw" class="text-[10px] px-3 py-1">Raw Data</Tabs.Trigger>
					<Tabs.Trigger value="stats" class="text-[10px] px-3 py-1">Statistics</Tabs.Trigger>
					{#if isFsio}
						<!-- 귀속 집계는 cross-layer 메타가 있는 fsio 에서만 답이 나온다. -->
						<Tabs.Trigger value="attribution" class="text-[10px] px-3 py-1">Attribution</Tabs.Trigger>
					{/if}
				</Tabs.List>

				<!-- Raw Data Tab -->
				<Tabs.Content value="raw" class="pt-2">
					{#if loadingRaw}
						<div class="flex items-center justify-center py-12"><LoaderIcon class="size-5 animate-spin text-muted-foreground" /></div>
					{:else if rawResult && rawResult.events.length > 0}
						<div class="flex items-center gap-2 mb-1">
							<div class="text-[9px] text-muted-foreground">
								{filteredEvents().length.toLocaleString()} events
								{#if rawResult.isSampled} (sampled from {rawResult.totalEvents.toLocaleString()}){/if}
							</div>
							<!-- Action tabs: Send / Complete -->
							<div class="flex gap-0.5 ml-auto">
								{#each ['send', 'complete'] as tab}
									<button
										onclick={() => activeActionTab = tab}
										class="px-2 py-0.5 rounded text-[9px] transition-colors
											{activeActionTab === tab ? 'bg-primary text-primary-foreground' : 'border hover:bg-muted'}"
									>
										{tab === 'send' ? 'Send' : 'Complete'}
									</button>
								{/each}
								<button
									onclick={() => activeActionTab = 'all'}
									class="px-2 py-0.5 rounded text-[9px] transition-colors
										{activeActionTab === 'all' ? 'bg-primary text-primary-foreground' : 'border hover:bg-muted'}"
								>
									All
								</button>
							</div>
						</div>
						<div class="flex gap-2 min-h-[400px]">
							<!-- Sidebar: chart selector -->
							<div class="w-32 shrink-0 space-y-0.5 border-r pr-2 sticky top-0 self-start">
								{#each availableChartItems as item}
									<button
										onclick={() => toggleChart(item.key)}
										class="w-full text-left px-2 py-1.5 rounded text-[10px] transition-colors
											{visibleCharts.has(item.key)
												? 'bg-primary/10 text-primary font-medium'
												: 'text-muted-foreground hover:bg-muted hover:text-foreground'}"
									>
										{item.label}
									</button>
								{/each}
							</div>
							<!-- Charts -->
							<div class="flex-1 space-y-2 relative">
								{#each availableChartItems as item}
									{#if visibleCharts.has(item.key)}
										{@const opt = getChartOption(item.key)}
										{#if opt}
											<div
												class="border rounded overflow-hidden resize"
												style="height: {chartHeight}; {userChartWidth ? `width: ${userChartWidth};` : ''} min-height: 150px; max-height: 800px;"
												use:observeResize
											>
												<div class="text-[10px] font-medium px-2 py-0.5 shrink-0">{item.label}</div>
												<TraceScatterChart option={opt} height="100%" chartKey={item.key} {legendSelected} onLegendChanged={handleLegendChanged} onBrushSelected={handleBrushSelected} />
											</div>
										{/if}
									{/if}
								{/each}
								</div>
						</div>
					{:else}
						<div class="text-center text-xs text-muted-foreground py-8">데이터 없음</div>
					{/if}
				</Tabs.Content>

				<!-- Statistics Tab -->
				<Tabs.Content value="stats" class="pt-2 space-y-3">
					<!-- AI 채팅 패널 (근거 집계 표시 + 멀티턴) -->
					<AiChatPanel
						bind:this={aiPanel}
						{serverId}
						jobId={activeJobIds[0] ?? null}
						kind="trace"
						reachable={aiReachable}
						{open} />
					{#if loadingStats}
						<div class="flex items-center justify-center py-12"><LoaderIcon class="size-5 animate-spin text-muted-foreground" /></div>
					{:else if statsResult}
						<!-- Overview -->
						<div class="grid grid-cols-4 gap-2">
							<div class="border rounded-md p-2">
								<div class="text-[9px] text-muted-foreground">Total Events</div>
								<div class="text-sm font-semibold">{statsResult.totalEvents.toLocaleString()}</div>
								<div class="text-[9px] text-muted-foreground">Send: {statsResult.sendCount.toLocaleString()}</div>
							</div>
							<div class="border rounded-md p-2">
								<div class="text-[9px] text-muted-foreground">Duration</div>
								<div class="text-sm font-semibold">{fmtDuration(rawDurationSeconds() ?? statsResult.durationSeconds)}</div>
								{#if rawDurationSeconds() && Math.abs(rawDurationSeconds()! - statsResult.durationSeconds) > 1}
									<div class="text-[8px] text-muted-foreground">server: {fmtDuration(statsResult.durationSeconds)}</div>
								{/if}
							</div>
							<div class="border rounded-md p-2">
								<div class="text-[9px] text-muted-foreground">Continuous</div>
								<div class="text-sm font-semibold">{statsResult.continuousRatio.toFixed(1)}%</div>
								<div class="text-[9px] text-muted-foreground">{statsResult.continuousCount.toLocaleString()} / {statsResult.sendCount.toLocaleString()}</div>
							</div>
							<div class="border rounded-md p-2">
								<div class="text-[9px] text-muted-foreground">Aligned</div>
								<div class="text-sm font-semibold">{statsResult.alignedRatio.toFixed(1)}%</div>
								<div class="text-[9px] text-muted-foreground">{statsResult.alignedCount.toLocaleString()}</div>
							</div>
							</div>
						<!-- Read/Write/Discard I/O Amount -->
						<div class="grid grid-cols-3 gap-2">
							<div class="border rounded-md p-2">
								<div class="text-[9px] text-muted-foreground">Read Total</div>
								<div class="text-sm font-semibold">{fmtBytes(statsResult.readTotalBytes)}</div>
							</div>
							<div class="border rounded-md p-2">
								<div class="text-[9px] text-muted-foreground">Write Total</div>
								<div class="text-sm font-semibold">{fmtBytes(statsResult.writeTotalBytes)}</div>
							</div>
							<div class="border rounded-md p-2">
								<div class="text-[9px] text-muted-foreground">Discard Total</div>
								<div class="text-sm font-semibold">{fmtBytes(statsResult.discardTotalBytes)}</div>
							</div>
						</div>

						<!-- Latency Stats -->
						<div>
							<div class="flex items-baseline justify-between mb-1">
								<h3 class="text-xs font-semibold">Latency Statistics</h3>
								<span class="text-[9px] text-muted-foreground">셀 클릭/드래그 · Ctrl+A 전체 · Ctrl+C 복사</span>
							</div>
							<Tabs.Root bind:value={activeLatencyTab}>
								<Tabs.List class="flex gap-0.5 mb-1">
									<Tabs.Trigger value="dtoc" class="text-[10px] px-2 py-0.5">DtoC</Tabs.Trigger>
									<Tabs.Trigger value="ctod" class="text-[10px] px-2 py-0.5">CtoD</Tabs.Trigger>
									<Tabs.Trigger value="ctoc" class="text-[10px] px-2 py-0.5">CtoC</Tabs.Trigger>
									<Tabs.Trigger value="qd" class="text-[10px] px-2 py-0.5">QD</Tabs.Trigger>
								</Tabs.List>
								{#each ['dtoc', 'ctod', 'ctoc', 'qd'] as lt}
									<Tabs.Content value={lt}>
										{@const s = statsResult[lt as keyof TraceStats] as LatencyStats}
										{#if s}
											{@const latRow = Object.fromEntries(latencyFields.map(f => [f.key, fmtLatency(s[f.key])]))}
											<DataTable
												data={[latRow]}
												columns={latencyFields.map(f => ({ accessorKey: f.key as string, header: f.label }))}
												showPagination={false}
												enableCellCopy={true}
												getRowId={() => `agent-lat-${lt}`}
											/>
										{/if}
									</Tabs.Content>
								{/each}
							</Tabs.Root>
						</div>

						<!-- CMD Stats -->
						{#if statsResult.cmdStats.length > 0}
							<div>
								<h3 class="text-xs font-semibold mb-1">CMD Statistics</h3>
								<Tabs.Root value="overview">
									<Tabs.List class="flex gap-0.5 mb-1">
										<Tabs.Trigger value="overview" class="text-[10px] px-2 py-0.5">Overview</Tabs.Trigger>
										<Tabs.Trigger value="dtoc" class="text-[10px] px-2 py-0.5">DtoC</Tabs.Trigger>
										<Tabs.Trigger value="ctod" class="text-[10px] px-2 py-0.5">CtoD</Tabs.Trigger>
										<Tabs.Trigger value="ctoc" class="text-[10px] px-2 py-0.5">CtoC</Tabs.Trigger>
										<Tabs.Trigger value="qd" class="text-[10px] px-2 py-0.5">QD</Tabs.Trigger>
									</Tabs.List>
									<Tabs.Content value="overview">
										{@const rows = statsResult.cmdStats.map(c => ({
											cmd: c.cmd, count: c.count.toLocaleString(),
											send: c.sendCount.toLocaleString(),
											ratio: c.ratio.toFixed(1) + '%',
											totalSize: fmtBytes(c.totalSizeBytes),
											continuous: `${c.continuousCount.toLocaleString()} (${c.continuousRatio.toFixed(1)}%)`,
											dtocAvg: fmtLatency(c.dtoc.avg), ctodAvg: fmtLatency(c.ctod.avg), ctocAvg: fmtLatency(c.ctoc.avg), qdAvg: fmtLatency(c.qd.avg)
										}))}
										<DataTable data={rows} columns={[
											{ accessorKey: 'cmd', header: 'CMD' }, { accessorKey: 'count', header: 'Total' }, { accessorKey: 'send', header: 'Send' }, { accessorKey: 'ratio', header: 'Ratio' },
											{ accessorKey: 'totalSize', header: 'Size' }, { accessorKey: 'continuous', header: 'Continuous' },
											{ accessorKey: 'dtocAvg', header: 'DtoC Avg' }, { accessorKey: 'ctodAvg', header: 'CtoD Avg' }, { accessorKey: 'ctocAvg', header: 'CtoC Avg' }, { accessorKey: 'qdAvg', header: 'QD Avg' }
										]} filterColumn="cmd" filterPlaceholder="CMD 검색..."
											enableCellCopy={true}
											getRowId={(r: any) => `agent-cmd-overview-${r.cmd}`}
										/>
									</Tabs.Content>
									{#each ['dtoc', 'ctod', 'ctoc', 'qd'] as lt}
										<Tabs.Content value={lt}>
											{@const rows = statsResult.cmdStats.map(c => {
												const s = c[lt as keyof typeof c] as LatencyStats;
												return {
													cmd: c.cmd, count: c.count.toLocaleString(), ratio: c.ratio.toFixed(1) + '%',
													min: fmtLatency(s.min), max: fmtLatency(s.max), avg: fmtLatency(s.avg), stddev: fmtLatency(s.stddev),
													median: fmtLatency(s.median), p99: fmtLatency(s.p99), p999: fmtLatency(s.p999), p9999: fmtLatency(s.p9999)
												};
											})}
											<DataTable data={rows} columns={[
												{ accessorKey: 'cmd', header: 'CMD' }, { accessorKey: 'count', header: 'Count' }, { accessorKey: 'ratio', header: 'Ratio' },
												{ accessorKey: 'min', header: 'Min' }, { accessorKey: 'max', header: 'Max' }, { accessorKey: 'avg', header: 'Avg' },
												{ accessorKey: 'stddev', header: 'StdDev' }, { accessorKey: 'median', header: 'Median' },
												{ accessorKey: 'p99', header: 'P99' }, { accessorKey: 'p999', header: 'P99.9' }, { accessorKey: 'p9999', header: 'P99.99' }
											]} filterColumn="cmd" filterPlaceholder="CMD 검색..."
												enableCellCopy={true}
												getRowId={(r: any) => `agent-cmd-${lt}-${r.cmd}`}
											/>
										</Tabs.Content>
									{/each}
								</Tabs.Root>
							</div>
						{/if}

						<!-- UFS Management Events (fsio_ufs 전용) -->
						{#if (statsResult.mgmtStats?.length ?? 0) > 0}
							{@const mgmt = statsResult.mgmtStats}
							{@const mgmtTotalMs = mgmt.reduce((a, m) => a + m.totalTimeMs, 0)}
							{@const mgmtRatio = statsResult.durationSeconds > 0
								? (mgmtTotalMs / (statsResult.durationSeconds * 1000)) * 100 : 0}
							<div>
								<div class="flex items-baseline gap-2 mb-1">
									<h3 class="text-xs font-semibold">UFS Management Events</h3>
									<!-- 핵심 지표는 건수가 아니라 링크 점유 시간이다. idle 구간에서는
									     데이터 IO 가 거의 없고 mgmt 가 행의 대부분을 차지한다. -->
									<span class="text-[9px] text-muted-foreground">
										링크 점유 {fmtLatency(mgmtTotalMs)}ms · 관측 기간의 {mgmtRatio.toFixed(1)}%
									</span>
								</div>
								<div class="border rounded-md overflow-x-auto">
									<table class="w-full text-[10px]">
										<thead class="bg-muted/50">
											<tr>
												<th class="text-left px-2 py-1 font-medium">Event</th>
												<th class="text-left px-2 py-1 font-medium">Kind</th>
												<th class="text-right px-2 py-1 font-medium">Count</th>
												<th class="text-right px-2 py-1 font-medium">Paired</th>
												<th class="text-right px-2 py-1 font-medium">Total (ms)</th>
												<th class="text-right px-2 py-1 font-medium">Share</th>
												<th class="text-right px-2 py-1 font-medium">Avg</th>
												<th class="text-right px-2 py-1 font-medium">Max</th>
											</tr>
										</thead>
										<tbody>
											{#each mgmt as m}
												<tr class="border-t">
													<td class="px-2 py-0.5">{m.name}</td>
													<td class="px-2 py-0.5 text-muted-foreground">{m.kind}</td>
													<td class="text-right px-2 py-0.5">{m.count.toLocaleString()}</td>
													<td class="text-right px-2 py-0.5">{m.pairedCount.toLocaleString()}</td>
													<td class="text-right px-2 py-0.5">{fmtLatency(m.totalTimeMs)}</td>
													<td class="text-right px-2 py-0.5">
														{mgmtTotalMs > 0 ? ((m.totalTimeMs / mgmtTotalMs) * 100).toFixed(1) : '0.0'}%
													</td>
													<td class="text-right px-2 py-0.5">{fmtLatency(m.dtoc?.avg ?? 0)}</td>
													<td class="text-right px-2 py-0.5">{fmtLatency(m.dtoc?.max ?? 0)}</td>
												</tr>
											{/each}
										</tbody>
									</table>
								</div>
							</div>
						{/if}

						<!-- Latency Histogram: type별 탭 -->
						{#if statsResult.latencyHistograms.length > 0}
							{@const histTypes = [...new Set(statsResult.latencyHistograms.map(h => h.latencyType))]}
							<div>
								<h3 class="text-xs font-semibold mb-1">Latency Histogram</h3>
								<Tabs.Root value={histTypes[0]}>
									<Tabs.List class="flex gap-0.5 mb-1">
										{#each histTypes as lt}
											<Tabs.Trigger value={lt} class="text-[10px] px-2 py-0.5 uppercase">{lt}</Tabs.Trigger>
										{/each}
									</Tabs.List>
									{#each histTypes as lt}
										<Tabs.Content value={lt}>
											{@const rows = statsResult.latencyHistograms
												.filter(h => h.latencyType === lt)
												.flatMap(h => h.buckets.map(b => ({
													cmd: h.cmd,
													range: b.rangeEndMs > 0 ? `${b.rangeStartMs} ~ ${b.rangeEndMs}` : `${b.rangeStartMs}+`,
													count: b.count.toLocaleString()
												})))}
											<DataTable
												data={rows}
												columns={[
													{ accessorKey: 'cmd', header: 'CMD' },
													{ accessorKey: 'range', header: 'Range (ms)' },
													{ accessorKey: 'count', header: 'Count' }
												]}
												filterColumn="cmd"
												filterPlaceholder="CMD 검색..."
												enableCellCopy={true}
												getRowId={(r: any) => `agent-hist-${lt}-${r.cmd}-${r.range}`}
											/>
										</Tabs.Content>
									{/each}
								</Tabs.Root>
							</div>
						{/if}

						<!-- CMD+Size Count: cmd별 탭 -->
						{#if statsResult.cmdSizeCounts.length > 0}
							{@const sizeCmds = [...new Set(statsResult.cmdSizeCounts.map(c => c.cmd))]}
							<div>
								<h3 class="text-xs font-semibold mb-1">CMD + Size Count</h3>
								<Tabs.Root value={sizeCmds[0]}>
									<Tabs.List class="flex gap-0.5 mb-1">
										{#each sizeCmds as cmd}
											<Tabs.Trigger value={cmd} class="text-[10px] px-2 py-0.5">{cmd}</Tabs.Trigger>
										{/each}
									</Tabs.List>
									{#each sizeCmds as cmd}
										<Tabs.Content value={cmd}>
											{@const rows = statsResult.cmdSizeCounts
												.filter(c => c.cmd === cmd)
												.map(c => ({ size: String(c.size), count: c.count.toLocaleString() }))}
											<DataTable
												data={rows}
												columns={[
													{ accessorKey: 'size', header: 'Size' },
													{ accessorKey: 'count', header: 'Count' }
												]}
												enableCellCopy={true}
												getRowId={(r: any) => `agent-size-${cmd}-${r.size}`}
											/>
										</Tabs.Content>
									{/each}
								</Tabs.Root>
							</div>
						{/if}
					{:else}
						<div class="text-center text-xs text-muted-foreground py-8">
							Raw Data 차트에서 드래그로 영역을 선택하거나, 필터를 설정 후 "조회" 버튼을 눌러주세요.
						</div>
					{/if}
				</Tabs.Content>

				<!-- Attribution Tab (fsio 전용) -->
				{#if isFsio}
					<Tabs.Content value="attribution" class="pt-2">
						<AgentAttributionView
							serverId={serverId ?? 0}
							jobIds={activeJobIds}
							traceType={activeTraceType}
							filter={attributionFilter()}
							onDrillDown={handleAttrDrillDown}
						/>
					</Tabs.Content>
				{/if}
			</Tabs.Root>
		</div>
	</Sheet.Content>
</Sheet.Root>
