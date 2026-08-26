<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { DataTable } from '$lib/components/data-table';
	import TraceScatterChart from './TraceScatterChart.svelte';
	import AiChatPanel from './AiChatPanel.svelte';
	import AgentAttributionView from './AgentAttributionView.svelte';
	import { columnsFor } from './rawDataColumns.js';
	import { captionMuted } from '$lib/styles/common.js';
	import { toast } from 'svelte-sonner';
	import { onDestroy } from 'svelte';
	import { getTraceResult, getTraceRawData, reparseTrace, getJobStatus, fetchExecutionByJobId, getAiStatus, type TraceFilter, type TraceStats, type TraceEvent, type TraceRawDataResult, type LatencyStats, type StepBoundary, type ClockSyncInfo, type JobExecutionRecord, getTraceClockSync } from '$lib/api/agent.js';
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
		// 시나리오 스텝 구간. 있으면 Charts 에 구간 밴드 + Behavior 탭을 노출한다.
		boundaries?: StepBoundary[];
	}

	let { open = $bindable(), serverId, jobIds, mappings = [], boundaries = [] }: Props = $props();

	// ── 스텝 구간 (behavior) ──
	//
	// mono 축(기기 monotonic 초)이 채워진 구간만 쓴다. 0 이면 clock offset 을 못 쟀거나
	// 못 믿는다는 뜻이라, 그 구간을 그리면 **통째로 밀린 자리에 밴드가 그려진다** —
	// 그래프는 정상으로 보이므로 눈으로 못 걸러낸다. 그래서 아예 안 그린다.
	// ⚠ Loop 필터를 구간에도 **똑같이** 적용한다. activeJobIds 는 선택한 loop 의
	// trace 잡만 조회하는데 구간만 전체를 그리면, 화면엔 loop 2 데이터가 떠 있는데
	// loop 1·3 밴드가 겹쳐 그려지고 그 행들은 이벤트 0 으로 나온다.
	//
	// trace_start/trace_stop 은 **제외한다.** 계측을 켜고 끄는 동작 자체라 분석 대상이
	// 아니고, 순간에 끝나 레인에서 실오라기처럼 보이며 자리만 차지한다.
	const BOUNDARY_SKIP_TYPES = new Set(['trace_start', 'trace_stop']);

	const allBoundaries = $derived(
		boundaries.filter(b =>
			b.finishedMono > b.startedMono &&
			b.startedMono > 0 &&
			!BOUNDARY_SKIP_TYPES.has(b.type) &&
			(selectedLoop <= 0 || b.loopIndex === selectedLoop)
		)
	);

	// 구간 토글 — 끈 구간은 차트 밴드·표·레인에서 모두 빠진다.
	// 구간이 많으면(loop 반복) 다 겹쳐 보여서 하나씩 떼어 봐야 읽힌다.
	let hiddenSteps = $state<Set<string>>(new Set());
	function boundaryKey(b: StepBoundary, i: number): string {
		return `${b.stepIndex}-${b.loopIndex}-${b.repeatIndex}-${i}`;
	}
	function toggleStep(key: string) {
		const next = new Set(hiddenSteps);
		if (next.has(key)) next.delete(key);
		else next.add(key);
		hiddenSteps = next;
	}

	// 화면에 실제로 그릴 구간. 토글로 숨긴 것을 뺀다.
	const usableBoundaries = $derived(
		allBoundaries.filter((b, i) => !hiddenSteps.has(boundaryKey(b, i)))
	);
	// 구간 데이터는 왔는데 mono 가 없는 경우 — 왜 분할이 안 되는지 알려야 한다.
	// ⚠ allBoundaries 기준이다 — 토글로 전부 숨긴 것과 "시계를 못 믿어서 못 그림" 은 다르다.
	const boundariesUnusable = $derived(boundaries.length > 0 && allBoundaries.length === 0);
	const hasBehavior = $derived(allBoundaries.length > 0);

	// 구간 색 — **순번대로 여러 색**을 돌린다.
	//
	// 한때 타입별 고정색으로 바꿨다가 되돌렸다: 실제 시나리오는 같은 타입이 연달아
	// 오는 경우가 많아(app_macro 여러 개) 타입 기준이면 옆 구간과 색이 같아져
	// **구간 경계가 안 보인다.** 순번 순환이 인접 구분에는 더 낫다.
	const BEHAVIOR_HUES = [
		'59,130,246',   // 파랑
		'16,185,129',   // 초록
		'245,158,11',   // 주황
		'139,92,246',   // 보라
		'236,72,153',   // 분홍
		'20,184,166'    // 청록
	];
	function behaviorRgb(i: number): string {
		return BEHAVIOR_HUES[i % BEHAVIOR_HUES.length];
	}
	/**
	 * 색 인덱스는 **allBoundaries 상의 위치**로 고정한다.
	 *
	 * ⚠ 화면에 그리는 목록(usableBoundaries)의 순번을 쓰면, 구간 하나를 숨기는 순간
	 * 뒤엣것들의 색이 앞으로 밀려 **범례와 밴드 색이 어긋난다.** 토글은 보이고 안 보이고만
	 * 바꿔야지 색까지 바꾸면 안 된다.
	 */
	function colorIndexOf(b: StepBoundary): number {
		const i = allBoundaries.indexOf(b);
		return i >= 0 ? i : 0;
	}
	/** 차트 밴드용 — 옅게. */
	function behaviorColor(i: number): string {
		return `rgba(${behaviorRgb(i)},0.10)`;
	}
	/** 레인 바·범례용 — 진하게. */
	function behaviorSolid(i: number): string {
		return `rgb(${behaviorRgb(i)})`;
	}
	/** sleep 처럼 "아무것도 안 한" 구간은 점선 빈 바로. */
	function isIdleStep(type: string): boolean {
		return type === 'sleep';
	}

	function behaviorLabel(b: StepBoundary) {
		const base = b.label || b.type || `step ${b.stepIndex}`;
		return b.loopIndex > 0 ? `${base} (loop ${b.loopIndex})` : base;
	}

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

	// ── cross-layer 필터 (fsio 전용) ──
	//
	// Attribution 드릴다운이 채운다. 시간/LBA 등 기존 필터와 **같은 파이프라인**을 타서
	// Charts / Statistics / Raw Data / Attribution 이 항상 같은 모수를 본다.
	let filterComm = $state<string[]>([]);
	let filterName = $state<string[]>([]);
	let filterSyscall = $state<string[]>([]);
	let filterFs = $state<string[]>([]);
	let filterPid = $state<number[]>([]);
	let filterIno = $state<number[]>([]);
	let filterLun = $state<number[]>([]);
	let filterDev = $state<string[]>([]);
	let filterIoFlagsAny = $state('');

	// 활성 cross-layer 필터를 칩으로. 각 칩은 클릭하면 그 조건만 해제한다.
	const crossLayerChips = $derived.by<{ label: string; value: string; clear: () => void }[]>(() => {
		const out: { label: string; value: string; clear: () => void }[] = [];
		const pushStr = (label: string, vals: string[], set: (v: string[]) => void) => {
			for (const v of vals) {
				out.push({ label, value: v, clear: () => { set(vals.filter(x => x !== v)); applyFilter(); } });
			}
		};
		const pushNum = (label: string, vals: number[], set: (v: number[]) => void) => {
			for (const v of vals) {
				out.push({ label, value: String(v), clear: () => { set(vals.filter(x => x !== v)); applyFilter(); } });
			}
		};
		pushStr('comm', filterComm, v => filterComm = v);
		pushStr('file', filterName, v => filterName = v);
		pushStr('syscall', filterSyscall, v => filterSyscall = v);
		pushStr('fs', filterFs, v => filterFs = v);
		pushNum('pid', filterPid, v => filterPid = v);
		pushNum('ino', filterIno, v => filterIno = v);
		pushNum('lun', filterLun, v => filterLun = v);
		pushStr('device', filterDev, v => filterDev = v);
		if (filterIoFlagsAny) {
			const name = Object.entries(FLOW_BITS).find(([, b]) => b === filterIoFlagsAny)?.[0];
			out.push({
				label: 'flow',
				value: name ?? `io_flags&${filterIoFlagsAny}`,
				clear: () => { filterIoFlagsAny = ''; applyFilter(); }
			});
		}
		return out;
	});

	const hasCrossLayerFilter = $derived(
		filterComm.length > 0 || filterName.length > 0 || filterSyscall.length > 0 ||
		filterFs.length > 0 || filterPid.length > 0 || filterIno.length > 0 ||
		filterLun.length > 0 || filterDev.length > 0 || filterIoFlagsAny !== ''
	);

	// Attribution 에 넘길 필터 = **적용된** 필터.
	//
	// buildFilter() 를 직접 파생시키면 입력창의 draft 값까지 따라가서 ① 키 입력마다
	// attribution 요청이 나가고 ② 조회 버튼을 누르기 전까지 Attribution 만 다른 모수를
	// 보여준다 — "모든 탭이 같은 모수" 라는 목표와 정반대다.
	// applyFilter/resetFilter 가 커밋한 스냅샷만 쓴다.
	let appliedFilter = $state<TraceFilter>({});
	const attributionFilter = $derived(appliedFilter);

	/**
	 * Attribution 행 클릭 → 해당 값으로 좁혀 보기.
	 *
	 * additive(Ctrl/⌘/Shift)면 기존 선택에 더한다 — 파일 여러 개를 한 번에 볼 때.
	 * 일반 클릭은 단일 선택이고, 같은 값을 다시 누르면 해제(토글)된다.
	 *
	 * 롤업 행 "(other)" 과 "(파일 아님)"/"(none)" 은 **실제 값이 아니라 묶음 라벨**이라
	 * 필터로 쓸 수 없다 — 그걸로 좁히면 0건이 되므로 클릭을 무시한다.
	 */
	function handleAttrDrillDown(dim: string, key: string, additive: boolean) {
		if (key === '(other)' || key === '(none)' || key === '(파일 아님)') {
			toast.info(`${key} 은(는) 묶음 라벨이라 필터로 쓸 수 없습니다`);
			return;
		}
		const toggleStr = (cur: string[]) => {
			if (!additive) return cur.length === 1 && cur[0] === key ? [] : [key];
			return cur.includes(key) ? cur.filter(v => v !== key) : [...cur, key];
		};
		const toggleNum = (cur: number[]) => {
			const n = Number(key);
			if (!Number.isFinite(n)) return cur;
			if (!additive) return cur.length === 1 && cur[0] === n ? [] : [n];
			return cur.includes(n) ? cur.filter(v => v !== n) : [...cur, n];
		};

		switch (dim) {
			case 'comm': filterComm = toggleStr(filterComm); break;
			case 'file': filterName = toggleStr(filterName); break;
			case 'syscall': filterSyscall = toggleStr(filterSyscall); break;
			case 'fs': filterFs = toggleStr(filterFs); break;
			case 'pid': filterPid = toggleNum(filterPid); break;
			case 'ino': filterIno = toggleNum(filterIno); break;
			case 'lun': {
				// 표시값은 "LU1" 형태라 숫자만 떼어낸다.
				const n = Number(key.replace(/^LU/, ''));
				if (Number.isFinite(n)) {
					filterLun = additive
						? (filterLun.includes(n) ? filterLun.filter(v => v !== n) : [...filterLun, n])
						: (filterLun.length === 1 && filterLun[0] === n ? [] : [n]);
				}
				break;
			}
			case 'device': filterDev = toggleStr(filterDev); break;
			case 'flow': {
				// flow 는 io_flags 파생 라벨이라 역매핑이 필요하다.
				// ⚠ 서버의 flowClassExpr 은 **우선순위 CASE** 라 정확한 역변환이 아니다
				// (GC 이면서 DATA 인 행은 GC 로 분류된다). 여기서는 "그 비트가 켜진 행"
				// 으로 좁히므로 서버 집계보다 넓게 잡힐 수 있다.
				const bit = FLOW_BITS[key];
				if (!bit) { toast.info(`${key} 는 비트 필터로 옮길 수 없습니다`); return; }
				filterIoFlagsAny = filterIoFlagsAny === bit ? '' : bit;
				break;
			}
			default:
				toast.info(`${dim} 축은 아직 필터로 연결되지 않았습니다`);
				return;
		}
		showFilter = true;
		applyFilter();
	}

	// flow 라벨 → io_flags 비트(10진 문자열). 서버 flowClassExpr 와 같은 값.
	const FLOW_BITS: Record<string, string> = {
		GC: '16777216',
		Checkpoint: '8388608',
		Journal: '4194304',
		'Writeback(kworker)': '34359738368',
		fsync: '68719476736',
		DirectIO: '8589934592',
		'mmap-writeback': '17179869184',
		Metadata: '131072',
		'Buffered(app)': '4294967296',
		Data: '65536'
	};

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
		   filterMinCtoc || filterMaxCtoc || filterMinQd || filterMaxQd) || hasCrossLayerFilter
	);
	let mainTab = $state('raw');

	// 선택된 탭이 사라지면 기본 탭으로 되돌린다.
	//
	// mainTab 은 시트를 닫아도 유지되는데, Behavior/Attribution 은 조건부 노출이라
	// 그 탭을 보던 중 닫고 **구간(또는 fsio)이 없는 잡**을 열면 트리거와 콘텐츠가
	// 둘 다 사라져 **아무 탭도 선택 안 된 빈 본문**이 된다.
	$effect(() => {
		if (mainTab === 'behavior' && !hasBehavior) mainTab = 'raw';
		if (mainTab === 'attribution' && !isFsio) mainTab = 'raw';
	});

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
	// ⚠ `$derived(() => ...)` 로 쓰면 **함수 자체가 값**이 되어 타입이 TraceEvent[] 가
	// 아니게 된다(호출부는 filteredEvents() 로 쓰고 있어 런타임은 맞지만 타입이 어긋난다).
	// `$derived.by` 가 이 형태의 올바른 룬이다.
	const filteredEvents = $derived.by<TraceEvent[]>(() => {
		if (!rawResult) return [];
		if (activeActionTab === 'all') return rawResult.events;
		return rawResult.events.filter(e => actionToTab(e.action) === activeActionTab);
	});

	// Raw Data 표에 넘길 행. DataTable 이 컬럼 정의의 키로 값을 뽑는다.
	const tableRows = $derived(filteredEvents as unknown as Record<string, unknown>[]);

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
			// 구간이 안 보일 때 이유를 대려면 필요하다. 실패해도 나머지 조회엔 영향 없다.
			loadClockSync();
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
		// cross-layer (fsio 전용) — 없는 컬럼 조건은 서버가 조용히 skip 한다.
		if (filterComm.length) f.commList = filterComm;
		if (filterName.length) f.nameList = filterName;
		if (filterSyscall.length) f.syscallList = filterSyscall;
		if (filterFs.length) f.fsList = filterFs;
		if (filterPid.length) f.pidList = filterPid;
		if (filterIno.length) f.inoList = filterIno;
		if (filterLun.length) f.lunList = filterLun;
		if (filterDev.length) f.devList = filterDev;
		if (filterIoFlagsAny) f.ioFlagsAny = filterIoFlagsAny;
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
			// 단독 trace 실행(AgentTraceForm)에는 mappings 가 없다 — 서버가 알려준
			// trace_type 이 fsio UI 노출과 컬럼 세트 결정의 유일한 출처다.
			if (rawResult?.traceType) fallbackTraceType = rawResult.traceType;
		} catch (e) { console.error('Trace raw error:', e); toast.error('Raw data 조회 실패'); }
		finally { loadingRaw = false; }
	}

	// ── 시계 정합 상태 ──
	//
	// 구간이 안 보일 때 **왜인지** 를 화면이 말하게 한다. 이유 없이 기능이 사라지면
	// 버그로 읽히고, 반대로 못 믿을 offset 으로 조용히 구간을 나누면 틀린 그래프가
	// 정상으로 보인다. 서버 판정(reason)을 그대로 인용하는 게 안전하다.
	let clockSync = $state<Record<string, ClockSyncInfo> | null>(null);

	async function loadClockSync() {
		if (serverId == null || activeJobIds.length === 0) return;
		try {
			const res = await getTraceClockSync(serverId, activeJobIds);
			clockSync = res.clockSync ?? null;
		} catch (e) {
			// 조회 실패는 조회 자체를 막지 않는다 — 배너가 일반 문구로 내려갈 뿐.
			console.error('clock sync 조회 실패:', e);
			clockSync = null;
		}
	}

	// 경계 불확실 폭(± 초). 조회된 잡 중 **가장 나쁜 쪽**을 택한다 — 낙관적으로 잡으면
	// "경계 모호" 로 표시해야 할 구간을 놓친다.
	//
	// 이 값이 곧 "이 구간 경계를 어디까지 믿을 수 있나" 다. 없으면 화면이 ±10ms 짜리
	// 측정과 ±250ms 짜리를 **똑같이** 그리게 된다.
	const edgeUncertaintySec = $derived.by<number>(() => {
		if (!clockSync) return 0;
		let worst = 0;
		for (const id of activeJobIds) {
			const u = clockSync[id]?.uncertaintySec;
			if (typeof u === 'number' && u > worst) worst = u;
		}
		return worst;
	});

	// 조회된 잡 중 하나라도 못 믿으면 그 이유를 보여 준다.
	const clockSyncReason = $derived.by<string>(() => {
		if (!clockSync) return '';
		for (const id of activeJobIds) {
			const c = clockSync[id];
			if (c && !c.usable && c.reason) return c.reason;
		}
		return '';
	});

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
		appliedFilter = f ?? {};
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
		filterComm = []; filterName = []; filterSyscall = []; filterFs = [];
		filterPid = []; filterIno = []; filterLun = []; filterDev = [];
		filterIoFlagsAny = '';
		appliedFilter = {};
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

	// ── Behavior 구간별 집계 ──
	//
	// 이미 클라이언트에 있는 raw 이벤트를 구간으로 자른다. `time` 은 parquet 원본
	// 그대로(기기 monotonic 초)이고 StepBoundary.*Mono 도 같은 축이라 바로 비교된다.
	//
	// 서버 왕복을 안 하는 이유: 구간마다 질의하면 스텝 수만큼 라운드트립이 생기는데,
	// 필요한 값(건수·바이트·latency 분위수)은 전부 이 배열에서 나온다.
	// ⚠ 단 raw 가 샘플링됐으면(50만 초과) 절대 건수는 표본 기준이다 — 비율은 유효하다.
	function quantile(sorted: number[], q: number): number {
		if (sorted.length === 0) return 0;
		const pos = (sorted.length - 1) * q;
		const lo = Math.floor(pos);
		const hi = Math.ceil(pos);
		if (lo === hi) return sorted[lo];
		return sorted[lo] + (sorted[hi] - sorted[lo]) * (pos - lo);
	}

	interface BehaviorRow {
		key: string;
		/** 색 인덱스 — allBoundaries 기준 고정 (colorIndexOf 주석 참고). */
		colorIndex: number;
		label: string;
		type: string;
		durationSec: number;
		events: number;
		readBytes: number;
		writeBytes: number;
		p50: number;
		p99: number;
		maxLatency: number;
		success: boolean;
	}

	const behaviorRows = $derived.by<BehaviorRow[]>(() => {
		if (!hasBehavior) return [];
		// 구간마다 전체 배열을 훑으면 O(구간 × 이벤트)다. 이벤트는 최대 50만(샘플링
		// 상한)이고 loop 시나리오는 구간이 수십~수백 개라 수천만 번 비교가 되는데,
		// 필터/탭을 건드릴 때마다 메인 스레드에서 다시 돈다.
		// 시각순으로 한 번 정렬해 두고 구간별로 이분 탐색해 잘라 쓴다.
		const sorted = [...filteredEvents].sort((a, b2) => a.time - b2.time);
		const lowerBound = (t: number) => {
			let lo = 0, hi = sorted.length;
			while (lo < hi) {
				const mid = (lo + hi) >> 1;
				if (sorted[mid].time < t) lo = mid + 1;
				else hi = mid;
			}
			return lo;
		};
		return usableBoundaries.map((b, i) => {
			const from = lowerBound(b.startedMono);
			let to = from;
			while (to < sorted.length && sorted[to].time <= b.finishedMono) to++;
			const inRange = sorted.slice(from, to);
			// latency 는 완료 이벤트에만 실린다 (send 행의 dtoc 는 0).
			const lat = inRange.map(e => e.dtoc).filter(v => v > 0).sort((a, c) => a - c);
			// ⚠ 바이트는 **완료 이벤트만** 센다. action 탭이 'all' 이면 한 요청이
			// send/complete 두 행으로 들어와 그냥 더하면 전부 2배가 된다 (latency 는
			// dtoc>0 필터가 있어 영향이 없어서 바이트만 조용히 어긋난다).
			const isComplete = (a: string) => a === 'complete_rsp' || a === 'block_rq_complete';
			let readBytes = 0, writeBytes = 0;
			for (const e of inRange) {
				if (!isComplete(e.action)) continue;
				// ⚠ read/write 판정은 **getCmdGroup 을 그대로 쓴다.** UFS 의 cmd 는
				// "READ" 가 아니라 SCSI hex opcode(0x28/0x2a)라, 문자열에 "read" 가
				// 들어있는지 보는 방식은 UFS 에서 통째로 0 이 된다. 이 함수는 opcode·
				// 키워드·Block prefix(R/W/D/F) 를 모두 처리하고 차트 색도 같은 기준이라
				// 표와 그래프가 어긋나지 않는다.
				const g = getCmdGroup(e.cmd);
				if (g === 'read') readBytes += e.size;
				else if (g === 'write') writeBytes += e.size;
			}
			return {
				key: boundaryKey(b, i),
				colorIndex: colorIndexOf(b),
				label: behaviorLabel(b),
				type: b.type,
				durationSec: b.finishedMono - b.startedMono,
				events: inRange.length,
				readBytes,
				writeBytes,
				p50: quantile(lat, 0.5),
				p99: quantile(lat, 0.99),
				maxLatency: lat.length ? lat[lat.length - 1] : 0,
				success: b.success
			};
		});
	});

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
		// 구간 밴드 — 스텝 경계를 차트 위에 겹친다.
		//
		// 별도 레인(간트)을 위에 얹지 않는 이유: 차트마다 ECharts 인스턴스가 독립이고
		// echarts.connect 도 안 걸려 있어서, 사용자가 한 차트를 zoom 하면 레인과
		// 어긋난다. markArea 는 **차트 좌표계 안**이라 zoom/pan 을 데이터와 함께
		// 따라가므로 어긋날 수가 없다.
		if (hasBehavior) {
			// ⚠ 밴드를 series[0] 에 붙이면 안 된다. series[0] 은 임의의 cmd(UFS 면
			// 보통 0x28)라, 사용자가 legend 에서 그 cmd 를 끄는 순간 ECharts 가
			// markArea 까지 같이 숨겨 **모든 차트의 밴드가 한꺼번에 사라진다.**
			// legend 에 안 잡히는 전용 빈 series 에 매단다.
			series.push({
				name: '',
				type: 'scatter' as const,
				data: [],
				silent: true,
				markArea: {
					silent: true,
					itemStyle: { opacity: 1 },
					// 라벨은 안 그린다 — 구간이 촘촘하면 글자가 겹쳐 오히려 지저분하다.
					// 어느 구간인지는 위 범례(색)와 점 tooltip("구간: ...")으로 확인한다.
					label: { show: false },
					emphasis: { disabled: true },
					data: usableBoundaries.map(b => [
						{
							xAxis: b.startedMono,
							itemStyle: { color: behaviorColor(colorIndexOf(b)) },
							name: behaviorLabel(b)
						},
						{ xAxis: b.finishedMono }
					])
				}
			} as any);
		}
		return {
			tooltip: {
				trigger: 'item' as const,
				// 점을 짚으면 **그 IO 가 어느 스텝 구간인지**까지 알려준다. 밴드 라벨은
				// 구간이 촘촘하면 읽기 어려워서, 확실한 확인 수단이 하나 더 필요하다.
				formatter: (p: any) => {
					const t = p.data[0];
					const base = `${p.seriesName}<br/>time: ${t}<br/>${yLabel}: ${p.data[1]}`;
					const b = usableBoundaries.find(x => t >= x.startedMono && t <= x.finishedMono);
					return b ? `${base}<br/><b>구간: ${behaviorLabel(b)}</b>` : base;
				}
			},
			legend: { data: cmdSet, top: 0, right: 0, textStyle: { fontSize: 9 }, selected: legendSelected },
			xAxis: { type: 'value' as const, name: 'Time (s)', min: 'dataMin', nameTextStyle: { fontSize: 9 }, axisLabel: { fontSize: 8 } },
			yAxis: { type: 'value' as const, name: yLabel, nameTextStyle: { fontSize: 9 }, axisLabel: { fontSize: 8 } },
			series,
			grid: { left: 60, right: 20, top: 25, bottom: 35 },
			dataZoom: [{ type: 'inside' as const }]
		};
	}

	function getChartOption(key: string): ReturnType<typeof buildScatter> | null {
		const events = filteredEvents;
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

	// ── Behavior 레인 (목업 스타일) ──
	//
	// 스텝마다 한 줄. 트랙 안에서 시각을 % 로 환산해 바를 놓는다. 차트 밴드와 달리
	// 여기는 zoom 이 없어 어긋날 일이 없다 — 그래서 목업 형태를 그대로 쓸 수 있다.
	const laneSpan = $derived.by(() => {
		if (usableBoundaries.length === 0) return { t0: 0, t1: 1 };
		let t0 = Infinity, t1 = -Infinity;
		for (const b of usableBoundaries) {
			if (b.startedMono < t0) t0 = b.startedMono;
			if (b.finishedMono > t1) t1 = b.finishedMono;
		}
		// 폭이 0 이면 나눗셈이 깨진다 (스텝 하나가 순간에 끝난 경우).
		if (!(t1 > t0)) t1 = t0 + 1;
		return { t0, t1 };
	});

	// 해칭 표시 폭(px) — 비율대로 그리면 sub-pixel 이라 안 보인다(실측 ±11.5ms 는
	// 30초 트랙에서 0.7px). 눈에 걸리는 최소 폭으로 고정하고, 대신 **실제 값을
	// 숫자로** 함께 밝힌다.
	const edgeHatchPx = 3;

	// 오차가 구간 길이에 비해 무시할 수준이면 빗금을 아예 안 그린다.
	// 0.7px 짜리를 3px 로 부풀려 그리면 없는 불확실성을 있는 것처럼 보이게 만든다 —
	// 가장 짧은 구간의 2% 미만이면 숫자로만 알린다.
	const showEdgeHatch = $derived.by<boolean>(() => {
		if (edgeUncertaintySec <= 0 || allBoundaries.length === 0) return false;
		let shortest = Infinity;
		for (const b of allBoundaries) {
			const d = b.finishedMono - b.startedMono;
			if (d > 0 && d < shortest) shortest = d;
		}
		if (!isFinite(shortest)) return false;
		return (edgeUncertaintySec * 2) / shortest >= 0.02;
	});

	function fmtEdgeMs(sec: number): string {
		const ms = sec * 1000;
		return ms >= 10 ? `${ms.toFixed(0)}ms` : `${ms.toFixed(1)}ms`;
	}

	function lanePct(t: number): number {
		const { t0, t1 } = laneSpan;
		return ((t - t0) / (t1 - t0)) * 100;
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
						{#if hasCrossLayerFilter}
							<!-- cross-layer 필터는 대부분 Attribution 드릴다운으로 들어온다.
							     어떤 값이 걸렸는지 보이고 개별 해제가 되어야 "왜 데이터가
							     적지?" 를 되짚을 수 있다. -->
							<div class="flex flex-wrap items-center gap-1 text-[9px]">
								<span class="text-muted-foreground">귀속 필터</span>
								{#each crossLayerChips as chip}
									<button
										onclick={chip.clear}
										class="inline-flex items-center gap-0.5 rounded border border-blue-500/40 bg-blue-500/10 px-1.5 py-0.5 hover:bg-blue-500/20"
										title="클릭하면 해제"
									>
										<span class="text-muted-foreground">{chip.label}</span>
										<span class="font-mono max-w-[16rem] truncate">{chip.value}</span>
										<XIcon class="size-2.5" />
									</button>
								{/each}
							</div>
						{/if}
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
					<Tabs.Trigger value="raw" class="text-[10px] px-3 py-1">Charts</Tabs.Trigger>
					<Tabs.Trigger value="stats" class="text-[10px] px-3 py-1">Statistics</Tabs.Trigger>
					<Tabs.Trigger value="table" class="text-[10px] px-3 py-1">Raw Data</Tabs.Trigger>
					{#if isFsio}
						<!-- 귀속 집계는 cross-layer 메타가 있는 fsio 에서만 답이 나온다. -->
						<Tabs.Trigger value="attribution" class="text-[10px] px-3 py-1">Attribution</Tabs.Trigger>
					{/if}
					{#if hasBehavior}
						<!-- 스텝 구간이 있을 때만. Attribution 과 같은 조건부 노출 방식. -->
						<Tabs.Trigger value="behavior" class="text-[10px] px-3 py-1">Behavior</Tabs.Trigger>
					{/if}
				</Tabs.List>

				<!-- Raw Data Tab -->
				<Tabs.Content value="raw" class="pt-2">
					{#if boundariesUnusable}
						<!-- 구간 데이터는 왔는데 시계 정합이 안 된 경우.
						     조용히 숨기면 "왜 밴드가 없지?" 를 알 수 없다 — 기능이
						     사라진 것처럼 보이는 게 가장 나쁜 실패다. -->
						<div class="mb-2 rounded border border-amber-500/40 bg-amber-500/10 p-2 text-[9px] leading-relaxed">
							<b>스텝 구간을 표시할 수 없습니다.</b>
							{#if clockSyncReason}
								<span class="font-mono">{clockSyncReason}</span>
							{:else}
								수집 시점의 clock offset 을 측정하지 못했거나 신뢰할 수 없습니다
								(느린 adb 연결, 수집 중 시계 변경 등).
							{/if}
							<div class="mt-0.5 {captionMuted}">
								틀린 위치에 그리면 그래프가 정상으로 보여 오히려 잘못된 결론으로
								이어지므로 표시하지 않습니다.
							</div>
						</div>
					{/if}

					{#if hasBehavior}
						<!-- 구간 범례 — 색 ↔ 스텝. 밴드에 라벨을 안 그리므로(촘촘하면 겹친다)
						     여기가 유일한 색 대조표다. 클릭하면 그 구간만 껐다 켤 수 있다. -->
						<div class="mb-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-[9px]">
							<span class={captionMuted}>구간</span>
							{#each allBoundaries as b, i (boundaryKey(b, i))}
								{@const key = boundaryKey(b, i)}
								{@const hidden = hiddenSteps.has(key)}
								<button
									onclick={() => toggleStep(key)}
									class="inline-flex items-center gap-1 rounded border px-1.5 py-0.5 hover:bg-muted"
									class:opacity-40={hidden}
									title="클릭하면 {hidden ? '다시 표시' : '숨김'}">
									<span class="inline-block size-2 rounded-sm"
										style="background:{behaviorSolid(i)}; {hidden ? 'filter:grayscale(1);' : ''}"></span>
									{behaviorLabel(b)}
									{#if !b.success}<span class="text-red-500">실패</span>{/if}
								</button>
							{/each}
							{#if hiddenSteps.size > 0}
								<button onclick={() => (hiddenSteps = new Set())}
									class="rounded border px-1.5 py-0.5 hover:bg-muted {captionMuted}">
									전체 표시
								</button>
							{/if}
						</div>
					{/if}
					{#if loadingRaw}
						<div class="flex items-center justify-center py-12"><LoaderIcon class="size-5 animate-spin text-muted-foreground" /></div>
					{:else if rawResult && rawResult.events.length > 0}
						<div class="flex items-center gap-2 mb-1">
							<div class="text-[9px] text-muted-foreground">
								{filteredEvents.length.toLocaleString()} events
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

				<!-- Raw Data Tab — 행 단위 표 -->
				<Tabs.Content value="table" class="pt-2">
					{#if loadingRaw}
						<div class="flex items-center justify-center py-12"><LoaderIcon class="size-5 animate-spin text-muted-foreground" /></div>
					{:else if rawResult && rawResult.events.length > 0}
						<div class="flex items-center gap-2 mb-1">
							<div class="text-[9px] text-muted-foreground">
								{tableRows.length.toLocaleString()} 행
								{#if rawResult.isSampled}
									· 전체 {rawResult.totalEvents.toLocaleString()} 중 샘플
								{/if}
								{#if activeTraceType}· {activeTraceType}{/if}
							</div>
							<div class="text-[9px] text-muted-foreground ml-auto">
								셀 클릭/드래그 · Ctrl+A 전체 · Ctrl+C 복사
							</div>
						</div>
						<DataTable
							data={tableRows}
							columns={columnsFor(activeTraceType ?? 'ufs')}
							enableCellCopy={true}
							showPagination={false}
							compact
							scrollHeight="calc(100vh - 260px)"
						/>
					{:else}
						<div class="text-center text-xs text-muted-foreground py-8">데이터 없음</div>
					{/if}
				</Tabs.Content>

				<!-- Attribution Tab (fsio 전용) -->
				{#if isFsio}
					<Tabs.Content value="attribution" class="pt-2">
						<AgentAttributionView
							serverId={serverId ?? 0}
							jobIds={activeJobIds}
							traceType={activeTraceType}
							filter={attributionFilter}
							onDrillDown={handleAttrDrillDown}
						/>
					</Tabs.Content>
				{/if}

				{#if hasBehavior}
					<Tabs.Content value="behavior" class="pt-2 space-y-3">
						<!-- 스텝 레인 (목업 형태) — 스텝마다 한 줄, 시간축 공유.
						     Charts 밴드와 달리 zoom 이 없어 어긋날 일이 없다. -->
						<div class="rounded border p-2">
							<div class="flex items-center justify-between mb-1.5">
								<span class="text-[10px] font-semibold">스텝 구간</span>
								<span class="{captionMuted} text-[9px] font-mono">
									{laneSpan.t0.toFixed(2)}s → {laneSpan.t1.toFixed(2)}s
									({(laneSpan.t1 - laneSpan.t0).toFixed(2)}s)
								</span>
							</div>

							<!-- ⚠ allBoundaries 를 순회한다 — usableBoundaries 는 숨긴 걸 이미 뺀
							     목록이라, 그걸 돌면 끈 구간이 화면에서 사라져 **되돌릴 방법이 없다.** -->
							{#each allBoundaries as b, i (boundaryKey(b, i))}
								{@const key = boundaryKey(b, i)}
								{@const hidden = hiddenSteps.has(key)}
								{@const left = lanePct(b.startedMono)}
								{@const width = Math.max(lanePct(b.finishedMono) - left, 0.4)}
								{@const idle = isIdleStep(b.type)}
								<div class="grid grid-cols-[124px_1fr] items-center gap-0 min-h-[24px]"
									class:opacity-40={hidden}>
									<button
										onclick={() => toggleStep(key)}
										class="pr-2 text-right text-[9px] font-mono text-muted-foreground truncate hover:text-foreground"
										title="{behaviorLabel(b)} — 클릭하면 {hidden ? '다시 표시' : '숨김'}">
										{hidden ? '☐' : '☑'} {behaviorLabel(b)}
									</button>
									<div class="relative h-[20px] rounded bg-muted/50">
										<!-- 경계 불확실(±RTT/2) — 해칭. **강조가 아니라 "모름" 으로 읽혀야 한다.**
										     이게 없으면 ±10ms 측정과 ±250ms 측정이 화면상 똑같아 보인다. -->
										<!-- ⚠ 실측 RTT 23ms 기준 불확실 폭은 ±11.5ms 로, 30초 시나리오에서
										     트랙의 0.08%(≈0.7px)다. 비율 그대로 그리면 **렌더링이 안 된다.**
										     그래서 눈에 걸리는 최소 폭(3px)을 보장하되, 그렇게 키운 것이
										     실제보다 넓다는 사실을 아래 범례에서 숫자로 밝힌다 — 폭을
										     부풀린 채 설명이 없으면 오차를 과대평가하게 된다. -->
										{#if showEdgeHatch}
											{#each [b.startedMono, b.finishedMono] as edge}
												<div class="absolute top-0 bottom-0 pointer-events-none"
													style="left:calc({lanePct(edge)}% - {edgeHatchPx / 2}px); width:{edgeHatchPx}px;
														background-image: repeating-linear-gradient(45deg,
															rgba(242,153,0,0.44) 0 3px, transparent 3px 6px);"
													title="경계 불확실 ±{fmtEdgeMs(edgeUncertaintySec)} — 이 안의 IO 는 어느 구간인지 단정할 수 없습니다"
												></div>
											{/each}
										{/if}

										<div class="absolute top-[2px] bottom-[2px] rounded-sm flex items-center px-1.5 overflow-hidden whitespace-nowrap text-[9px]"
											style="left:{left}%; width:{width}%;
												{idle
													? 'border:1px dashed var(--border); color:var(--muted-foreground);'
													: `background:${behaviorSolid(i)}; color:#fff;`}"
											title="{behaviorLabel(b)} — {b.startedMono.toFixed(2)}s → {b.finishedMono.toFixed(2)}s">
											<span class="truncate">{b.type}</span>
											<span class="ml-auto pl-1.5 font-mono opacity-80">
												{(b.finishedMono - b.startedMono).toFixed(2)}s
											</span>
										</div>
									</div>
								</div>
							{/each}

							{#if edgeUncertaintySec > 0}
								<div class="mt-1.5 flex items-center gap-1.5 {captionMuted} text-[9px]">
									{#if showEdgeHatch}
										<span class="inline-block w-6 h-2 rounded-sm shrink-0"
											style="background-image: repeating-linear-gradient(45deg,
												rgba(242,153,0,0.44) 0 3px, transparent 3px 6px);"></span>
									{/if}
									<span>
										경계 불확실 <b>±{fmtEdgeMs(edgeUncertaintySec)}</b> —
										이 안에 걸친 IO 는 어느 구간인지 단정할 수 없습니다
										(호스트↔기기 시각 측정의 원리적 한계, adb 왕복의 절반).
										{#if showEdgeHatch}
											빗금은 <b>보이도록 넓힌 것</b>이라 실제 폭보다 큽니다.
										{:else}
											구간 길이에 비해 무시할 수준이라 표시하지 않습니다.
										{/if}
									</span>
								</div>
							{/if}
						</div>

						<!-- 지표 읽는 법 — 숫자만 주면 장식이 된다.
						     이 화면을 보는 사람이 성능 분석 경험이 없을 수 있다. -->
						<div class="rounded border bg-muted/30 p-2 text-[9px] leading-relaxed">
							<div class="font-semibold mb-0.5">구간별로 보면 무엇을 알 수 있나</div>
							<div class={captionMuted}>
								시나리오 스텝(앱 실행·스크롤·영상 재생 등)마다 그 구간에 실제로 내려간 IO 를 끊어 봅니다.
								잡 전체 평균으로는 <b>한 구간만 나빠도 묻힙니다</b> —
								예를 들어 스크롤 구간의 p99 만 튀는 상황은 전체 p99 로는 안 보입니다.
								Charts 탭의 옅은 세로 밴드가 같은 구간이며, 확대해도 데이터와 함께 움직입니다.
							</div>
							<div class="mt-1 {captionMuted}">
								<b>p99</b> 는 느린 쪽 1% 의 지연입니다. 평균이 좋아도 p99 가 크면 사용자는 멈칫하는 걸 느낍니다.
								<b>Read/Write</b> 비중은 그 구간이 무엇을 했는지 말해 줍니다 (콜드 실행은 보통 read 우위,
								촬영·다운로드는 write 우위).
							</div>
						</div>

						<div class="overflow-x-auto">
							<table class="w-full text-[10px]">
								<thead class="text-muted-foreground">
									<tr class="border-b">
										<th class="text-left py-1 pr-2 font-medium">구간</th>
										<th class="text-right py-1 px-2 font-medium">길이</th>
										<th class="text-right py-1 px-2 font-medium">이벤트</th>
										<th class="text-right py-1 px-2 font-medium">Read</th>
										<th class="text-right py-1 px-2 font-medium">Write</th>
										<th class="text-right py-1 px-2 font-medium">p50</th>
										<th class="text-right py-1 px-2 font-medium">p99</th>
										<th class="text-right py-1 pl-2 font-medium">max</th>
									</tr>
								</thead>
								<tbody class="font-mono">
									{#each behaviorRows as row, i (row.key)}
										<tr class="border-b border-border/40">
											<td class="py-1 pr-2 font-sans">
												<span class="inline-block w-2 h-2 rounded-sm mr-1 align-middle"
													style="background:{behaviorSolid(row.colorIndex)}"></span>
												{row.label}
												{#if !row.success}
													<span class="ml-1 text-red-500" title="이 스텝은 실패했습니다">실패</span>
												{/if}
											</td>
											<td class="text-right py-1 px-2">{row.durationSec.toFixed(2)}s</td>
											<td class="text-right py-1 px-2">{row.events.toLocaleString()}</td>
											<td class="text-right py-1 px-2">{fmtBytes(row.readBytes)}</td>
											<td class="text-right py-1 px-2">{fmtBytes(row.writeBytes)}</td>
											<td class="text-right py-1 px-2">{fmtLatency(row.p50)}</td>
											<td class="text-right py-1 px-2">{fmtLatency(row.p99)}</td>
											<td class="text-right py-1 pl-2">{fmtLatency(row.maxLatency)}</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</div>

						<div class={captionMuted + ' text-[9px]'}>
							구간 밖(스텝 사이 전환 구간)의 IO 는 어느 행에도 안 들어갑니다 —
							그래서 각 행의 합이 전체 이벤트 수보다 적을 수 있습니다.
							{#if rawResult?.isSampled}
								또한 이 잡은 <b>샘플링된 raw</b> 를 보고 있어 건수·바이트는 표본 기준입니다 (비율은 유효).
							{/if}
						</div>
					</Tabs.Content>
				{/if}
			</Tabs.Root>
		</div>
	</Sheet.Content>
</Sheet.Root>
