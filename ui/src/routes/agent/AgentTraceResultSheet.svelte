<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { DataTable } from '$lib/components/data-table';
	import TraceChartView from './trace/TraceChartView.svelte';
	import TraceStatsView from './trace/TraceStatsView.svelte';
	import BoundaryLegend from './trace/BoundaryLegend.svelte';
	import type { StatsResponse, StatsLatency } from './trace/types.js';
	import AiChatPanel from './AiChatPanel.svelte';
	import AgentAttributionView from './AgentAttributionView.svelte';
	import BehaviorTimeline from './BehaviorTimeline.svelte';
	import { columnsFor } from './rawDataColumns.js';
	// 차트·통계·Behavior 가 **같은 분류기**를 쓰도록 공유 모듈에서 가져온다.
	// 예전엔 이 파일 안에 사본이 있어 차트 색과 Behavior 색이 갈릴 수 있었다.
	import { getCmdGroup } from './trace/cmdColors.js';
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

	// ⚠ 서버 판정(clockSync.usable)이 false 면 **구간을 아예 안 그린다.**
	//
	// 예전엔 mono 값이 채워졌는지만 봤다. 그런데 mono 는 스텝이 끝나는 순간 시작
	// offset 으로 계산돼 박히고, drift 는 그 뒤 StopTrace 에서야 드러난다 — 즉
	// "수집 중 시계가 움직였다" 를 알아내도 이미 박힌 값은 그대로 쓰였다.
	// 이 기능이 막으려는 실패(밀린 구간이 정상으로 보임)가 정확히 그 경로다.
	const clockSyncUsable = $derived.by<boolean>(() => {
		if (!clockSync) return true; // 아직 조회 전 — 데이터가 없다고 막지는 않는다
		for (const id of activeJobIds) {
			const c = clockSync[id];
			if (c && !c.usable) return false;
		}
		return true;
	});

	const allBoundaries = $derived(
		!clockSyncUsable ? [] : boundaries.filter(b =>
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

	// 구간 선택이 바뀌면 **Statistics 만** 다시 부른다.
	//
	// Charts/Raw/Behavior 는 이미 받아 둔 raw 를 filteredEvents 에서 잘라 쓰므로
	// 서버 왕복이 필요 없다. 반면 Statistics 는 서버가 계산하는 값이라, 다시 안 부르면
	// **표만 전 구간 기준으로 남아** 화면마다 숫자가 달라진다.
	//
	// zoomRange 값 자체를 의존성으로 둔다 — 토글해도 범위가 그대로면(예: 가운데 구간
	// 하나를 껐다 켬) 굳이 다시 부르지 않는다.
	let lastStatsRange = $state('');
	$effect(() => {
		// noneSelected 도 키에 넣는다 — 전부 숨김 ↔ 전부 표시 전환이 zoomRange 로는
		// 둘 다 null 이라 구분되지 않아, 재조회가 안 되고 표만 이전 값으로 남는다.
		const key = noneSelected ? 'none' : zoomRange ? `${zoomRange.min}:${zoomRange.max}` : '';
		if (key === lastStatsRange) return;
		lastStatsRange = key;
		if (!open || serverId == null || activeJobIds.length === 0) return;
		if (noneSelected) {
			// 서버에 물을 게 없다. 빈 결과로 두면 다른 탭(빈 목록)과 말이 맞는다.
			statsResult = null;
			return;
		}
		loadStats(buildFilter());
	});

	// 타임라인 구간 선택 — hiddenSteps(숨김)와 **다른 개념**이다.
	//
	//   hiddenSteps  : 차트/표에서 아예 빼기 (범례 토글)
	//   selectedSteps: 타임라인에서 그 구간만 강조하고 나머지는 흐리게 (필터 미리보기)
	//
	// 섞으면 "왜 표에서 사라졌지" 와 "왜 흐리지" 가 구분이 안 된다.
	let selectedSteps = $state<Set<string>>(new Set());
	function toggleSelectStep(key: string) {
		const next = new Set(selectedSteps);
		if (next.has(key)) next.delete(key);
		else next.add(key);
		selectedSteps = next;
	}

	// 화면에 실제로 그릴 구간. 토글로 숨긴 것을 뺀다.
	const usableBoundaries = $derived(
		allBoundaries.filter((b, i) => !hiddenSteps.has(boundaryKey(b, i)))
	);

	// 구간을 하나도 안 남긴 상태. zoomRange 로는 표현할 수 없다(위 주석 참고).
	const noneSelected = $derived(
		allBoundaries.length > 0 && hiddenSteps.size > 0 && usableBoundaries.length === 0
	);

	// 구간을 골라 보면 차트도 **그 구간으로 확대**한다.
	//
	// 골라 놓고 축은 전체라면 점이 한쪽에 뭉쳐 보여서 고른 의미가 없다. 일부만 켜져
	// 있을 때만 좁힌다 — 전체가 켜져 있으면 원래대로 전 구간을 본다.
	const zoomRange = $derived.by<{ min: number; max: number } | null>(() => {
		if (hiddenSteps.size === 0) return null;          // 전부 보는 중 → 확대 안 함
		// ⚠ 전부 숨긴 경우는 **별도 플래그(noneSelected)로 다룬다.**
		// 여기서 {min:0,max:0} 을 돌려주면 서버 필터에 startTime=0 이 실리는데,
		// 백엔드는 `> 0` 을 "미설정" 으로 보므로 **Statistics 만 전 구간을 보여준다** —
		// 다른 화면은 비어 있는데 표만 꽉 찬, 가장 헷갈리는 상태가 된다.
		if (usableBoundaries.length === 0) return null;
		let lo = Infinity, hi = -Infinity;
		for (const b of usableBoundaries) {
			if (b.startedMono < lo) lo = b.startedMono;
			if (b.finishedMono > hi) hi = b.finishedMono;
		}
		if (!isFinite(lo) || !(hi > lo)) return null;
		// 경계에 딱 붙으면 끝점 IO 가 잘려 보인다. 5% 여유.
		const pad = (hi - lo) * 0.05;
		return { min: lo - pad, max: hi + pad };
	});

	// 구간 데이터는 왔는데 mono 가 없는 경우 — 왜 분할이 안 되는지 알려야 한다.
	// ⚠ allBoundaries 기준이다 — 토글로 전부 숨긴 것과 "시계를 못 믿어서 못 그림" 은 다르다.
	// ⚠ 배너는 **loop 필터 이전** 기준으로 판단한다. allBoundaries 는 selectedLoop 로도
	// 걸러지므로, 그걸 쓰면 "구간이 없는 loop 를 골랐을 때" 도 시계 문제로 오인하게 된다.
	const boundariesUnusable = $derived(
		boundaries.length > 0 &&
		(!clockSyncUsable ||
			boundaries.filter(b => b.finishedMono > b.startedMono && b.startedMono > 0).length === 0)
	);
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
			// ⚠ 구간 선택도 함께 비운다. boundaryKey 는 위치 기반(stepIndex-loop-repeat-i)이라
			// **잡이 달라도 키가 겹친다** — 안 지우면 시나리오 A 에서 숨긴 스텝이 B 에도
			// 적용돼, 아무 조작 없이 연 화면이 이미 일부 구간만 보고 있게 된다.
			hiddenSteps = new Set();
			selectedSteps = new Set();
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


	// Action tab: Send vs Complete
	let activeActionTab = $state('complete');

	/**
	 * 이 action 이 현재 탭에 보여야 하는가.
	 *
	 * ⚠ 예전엔 `action.includes('send')` 식의 부분일치로 'send'/'complete'/'other'
	 * 를 돌려주고 'other' 는 어느 탭에도 안 넣었다. mgmt(UPIU/UIC) 에서 이게 깨진다:
	 *   - 우연히 걸리는 것:  uic_send, upiu_query_rsp
	 *   - 아예 안 걸리는 것: upiu_nop_out, upiu_data_out, upiu_rtt, upiu_reject,
	 *                        exception, 방향 미상 uic
	 * 후자는 'other' 가 되어 기본 탭(complete)에서 **영영 안 보였다.** All 탭으로
	 * 가야만 나오는데, 안 보이는 게 필터 때문인지 데이터가 없어서인지 알 수 없다.
	 *
	 * mgmt 는 방향을 접미사로 명시 판정한다:
	 *   send 쪽: *_send / *_req / *_out
	 *   comp 쪽: *_complete / *_rsp / *_in
	 * 어느 쪽도 아닌 단발 이벤트(exception, 방향 미상 uic)는 **양쪽에 다 보인다** —
	 * 숨기면 그 시점을 영영 못 본다.
	 *
	 * portal `routes/trace/TraceChartView.svelte` 의 actionMatchesTab 과 동일 규칙.
	 */
	function actionMatchesTab(action: string, tab: string): boolean {
		if (tab === 'all') return true;
		const low = (action || '').toLowerCase();
		if (low.startsWith('upiu_') || low.startsWith('uic') || low === 'exception') {
			const isSend = low.endsWith('_send') || low.endsWith('_req') || low.endsWith('_out');
			const isComp = low.endsWith('_complete') || low.endsWith('_rsp') || low.endsWith('_in');
			if (!isSend && !isComp) return true; // 단발 — 항상 표시
			return tab === 'send' ? isSend : isComp;
		}
		if (tab === 'send') return low.includes('send') || low.includes('issue');
		return low.includes('complete') || low.includes('rsp');
	}

	// Filtered events by action tab
	// ⚠ `$derived(() => ...)` 로 쓰면 **함수 자체가 값**이 되어 타입이 TraceEvent[] 가
	// 아니게 된다(호출부는 filteredEvents() 로 쓰고 있어 런타임은 맞지만 타입이 어긋난다).
	// `$derived.by` 가 이 형태의 올바른 룬이다.
	//
	// ⚠ **action 필터를 여기서 걸지 않는다.** TraceChartView 가 Send/Complete/All 탭을
	// 스스로 갖고 있고 내부에서 또 거른다. 여기서 미리 거른 배열을 넘기면 이중 필터가
	// 되어 탭이 조용히 빈다 — 화면엔 "데이터 없음" 으로 보여 원인을 찾기 어렵다.
	// 그래서 차트에는 이 배열(loop + 구간만 반영)을 넘기고, action 은 차트가 소유한다.
	const slicedEvents = $derived.by<TraceEvent[]>(() => {
		if (!rawResult) return [];
		// 구간을 고르면 **데이터 자체**를 그 범위로 좁힌다.
		//
		// 예전엔 차트 x축만 좁혀서, Raw Data 는 전 구간 행을 그대로 보여주고
		// Statistics 도 전체 기준이었다 — 화면마다 모수가 달라 "고른 구간의 p99" 를
		// 물어도 답이 안 나왔다. 여기서 자르면 Raw/Behavior/차트가 같은 모수를 본다.
		if (noneSelected) return [];   // 아무 구간도 안 고름 — 보여줄 게 없다
		if (!zoomRange) return rawResult.events;
		return rawResult.events.filter(e => e.time >= zoomRange.min && e.time <= zoomRange.max);
	});

	// Raw Data / Behavior 용 — 여기는 action 탭을 **적용한다.**
	// 그쪽 화면엔 자체 action 탭이 없고, 헤더의 Send/Complete 선택을 따라야 한다.
	const filteredEvents = $derived.by<TraceEvent[]>(() =>
		activeActionTab === 'all'
			? slicedEvents
			: slicedEvents.filter(e => actionMatchesTab(e.action, activeActionTab))
	);

	/**
	 * agent TraceEvent[] (행) → TraceChartView 의 Series (열).
	 *
	 * 값은 손대지 않는다 — latency 0 제외 같은 판정은 TraceChartView 가 이미 한다
	 * (여기서 또 걸러내면 이중 필터가 된다).
	 */
	const chartSeries = $derived.by(() => {
		const ev = slicedEvents;
		const n = ev.length;
		const time = new Array<number>(n);
		const lba = new Array<number>(n);
		const qd = new Array<number>(n);
		const cpu = new Array<number>(n);
		const dtoc = new Array<number>(n);
		const ctoc = new Array<number>(n);
		const ctod = new Array<number>(n);
		const action = new Array<string>(n);
		const cmd = new Array<string>(n);
		for (let i = 0; i < n; i++) {
			const e = ev[i];
			time[i] = e.time;
			lba[i] = e.lba;
			qd[i] = e.qd;
			cpu[i] = e.cpu;
			dtoc[i] = e.dtoc;
			ctoc[i] = e.ctoc;
			ctod[i] = e.ctod;
			action[i] = e.action;
			cmd[i] = e.cmd;
		}
		return { time, lba, qd, cpu, dtoc, ctoc, ctod, action, cmd };
	});

	/** TraceChartView 상단 meta 바(총/샘플 건수). agent 응답 값 그대로. */
	const chartMeta = $derived(
		rawResult
			? {
					totalEvents: rawResult.totalEvents ?? rawResult.events.length,
					sampledEvents: rawResult.sampledEvents ?? rawResult.events.length,
					schemaVersion: '',
					stats: null
				}
			: null
	);

	/**
	 * TraceChartView 의 traceType — union 이라 'both' 가 없다.
	 *
	 * ⚠ ufs/block 두 값으로 **뭉개면 안 된다**: fsio_* 를 잃으면 mgmt 차트와
	 * fsio Flags 패널이 조용히 사라진다. 'both' 만 ufs 로 떨어뜨린다
	 * (UFS+Block 동시 수집이라 한쪽으로 단정할 수 없고, 이 값은 fsio 전용 패널
	 * 노출만 정하므로 ufs/block 어느 쪽이든 화면 구성이 같다).
	 */
	const chartTraceType = $derived.by<'ufs' | 'block' | 'ufscustom' | 'fsio_ufs' | 'fsio_block'>(() => {
		const t = activeTraceType;
		if (t === 'fsio_ufs' || t === 'fsio_block' || t === 'block' || t === 'ufscustom') return t;
		return 'ufs';
	});

	/**
	 * hiddenSteps(문자열 키) → TraceChartView 의 hiddenBoundaries(배열 index).
	 *
	 * hiddenSteps 는 문자열 키인 채로 둔다 — job 을 바꾸면 위치 index 가 다른 구간을
	 * 가리키게 되는데, 문자열 키라야 그 충돌을 피할 수 있다. 변환은 넘기는 지점에서만.
	 */
	const hiddenBoundaryIdx = $derived(
		new Set(
			allBoundaries
				.map((_, i) => i)
				.filter((i) => hiddenSteps.has(boundaryKey(allBoundaries[i], i)))
		)
	);

	/**
	 * 차트가 "되돌리기(전체 범위로)" 를 요청했을 때.
	 * agent 는 서버사이드 zoom 재조회가 없으므로 시간 필터만 비우고 다시 조회한다.
	 */
	function handleResetZoom() {
		filterStartTime = '';
		filterEndTime = '';
		applyFilter();
	}

	// 통계 표시(포맷터·표 구성·탭)는 전부 TraceStatsView 가 들고 있다.
	// 여기서 다시 정의하면 /trace 와 숫자 표기가 갈라지므로 두지 않는다.

	/**
	 * agent TraceStats → /trace StatsResponse.
	 *
	 * 두 타입은 필드 이름·의미가 그대로 겹친다(연속/정렬 비율도 양쪽 다 이미 % 값).
	 * 그래서 값을 손대지 않고 넘긴다 — 여기서 계산을 끼워 넣으면 화면은 멀쩡한데
	 * 숫자만 조용히 달라진다.
	 *
	 * LatencyStats.count : 서버가 보내주면 그 값을 쓴다(모수를 정확히 안다).
	 * 구버전 agent 는 이 필드가 없으므로 NaN 을 넣어 표가 '-' 로 그리게 한다 —
	 * 여기서 totalEvents-sendCount 같은 걸로 **짐작하면 안 된다**. 필터를 걸면
	 * 실제 모수는 줄어드는데 짐작은 그대로라 조용히 틀린 수가 나온다.
	 */
	function toStatsResponse(s: TraceStats): StatsResponse {
		// count 가 없으면 NaN → TraceStatsView 가 '-' 로 그린다 (0 은 "없다"는 거짓말).
		const withCount = (l: LatencyStats): StatsLatency =>
			({ ...l, count: l.count ?? Number.NaN }) as StatsLatency;
		return {
			...s,
			schemaVersion: '',
			dtoc: withCount(s.dtoc),
			ctoc: withCount(s.ctoc),
			ctod: withCount(s.ctod),
			qd: withCount(s.qd),
			cmdStats: s.cmdStats.map((c) => ({
				...c,
				dtoc: withCount(c.dtoc),
				ctoc: withCount(c.ctoc),
				ctod: withCount(c.ctod),
				qd: withCount(c.qd)
			})),
			latencyHistograms: s.latencyHistograms as StatsResponse['latencyHistograms'],
			// ⚠ **...s 뒤에 덮어써야 한다.** standalone 은 이 필드가 non-optional 이라
			// 스프레드로 이미 들어오지만, 타입(kind: string ↔ 유니온)이 어긋나 캐스팅이
			// 필요하다. 여기서 빠뜨리면 지금 보이던 mgmt 표가 사라지는 회귀가 된다.
			mgmtStats: s.mgmtStats as StatsResponse['mgmtStats']
		};
	}

	// Raw Data 표에 넘길 행. DataTable 이 컬럼 정의의 키로 값을 뽑는다.
	const tableRows = $derived(filteredEvents as unknown as Record<string, unknown>[]);


	// cmd 색상·그룹 분류는 './trace/cmdColors.js' 가 갖는다 (차트와 같은 기준).

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
		// 구간 선택을 서버 질의에도 싣는다 — Statistics 는 서버가 계산하므로 이게
		// 없으면 **표는 전 구간 기준**인데 차트만 좁혀져 화면마다 모수가 달라진다.
		// 사용자가 직접 넣은 Time min/max 가 있으면 그쪽을 존중한다(더 좁은 의도).
		if (zoomRange) {
			if (!filterStartTime) f.startTime = zoomRange.min;
			if (!filterEndTime) f.endTime = zoomRange.max;
		}
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

	// 응답 순서 뒤바뀜 방지용 세대 카운터.
	//
	// loop 를 바꾸면 두 경로가 동시에 loadStats 를 부른다(열림 effect = 필터 없음,
	// 구간 effect = zoomRange 필터). 늦게 도착한 쪽이 최종값이 되므로, 배너는
	// "선택 구간만" 인데 표는 전 구간 수치인 상태가 나올 수 있다.
	let statsGen = 0;

	async function loadStats(filter?: TraceFilter) {
		if (serverId == null || activeJobIds.length === 0) return;
		const gen = ++statsGen;
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
				if (gen !== statsGen) return; // 더 최신 요청이 있다 — 이 응답은 버린다
				statsResult = res.stats as unknown as TraceStats;
			} else {
				const res = await getTraceResult(serverId, { jobIds: activeJobIds, filter, latencyRangesMs: parseLatencyRanges() });
				if (gen !== statsGen) return; // 더 최신 요청이 있다 — 이 응답은 버린다
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
		/** 사용자가 고른 분위수 값들 (behaviorPercentiles 와 같은 순서). */
		percentiles: number[];
		maxLatency: number;
		discardBytes: number;
		discardCount: number;
		success: boolean;
	}

	// 분위수는 사용자가 정한다 — 워크로드마다 봐야 할 꼬리가 다르다(p95 로 충분한 경우도,
	// p99.9 까지 봐야 하는 경우도 있다). 기본은 흔히 쓰는 3개.
	let percentileInput = $state('50, 95, 99');
	const behaviorPercentiles = $derived.by<number[]>(() => {
		const out: number[] = [];
		for (const raw of percentileInput.split(',')) {
			const v = parseFloat(raw.trim().replace(/^p/i, ''));
			// 0 < v < 100. p99.999 처럼 소수 자리 제한은 두지 않는다 — 꼬리를 얼마나
			// 깊게 볼지는 표본 수에 달렸고, 그 판단은 사용자 몫이다.
			if (Number.isFinite(v) && v > 0 && v < 100) out.push(v);
		}
		// 중복 제거 + 오름차순. 비면 기본값으로 되돌린다(빈 표를 주지 않는다).
		const uniq = [...new Set(out)].sort((a, b) => a - b);
		return uniq.length > 0 ? uniq : [50, 95, 99];
	});
	// ⚠ toFixed 로 자르면 안 된다. p99.999 가 "p100.0" 으로 표시돼 **틀린 값**이 되고,
	// p99.9 / p99.99 / p99.999 가 화면에서 구분이 안 된다. 꼬리를 깊게 보는 게 목적인
	// 지표라 자릿수 자체가 정보다. 사용자가 입력한 값을 그대로 쓴다.
	function fmtPct(p: number): string {
		return `p${p}`;
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
			// discard(=trim/unmap)도 센다. 삭제·캐시정리 워크로드에서 핵심 신호인데
			// read/write 만 보면 "IO 가 거의 없다" 로 잘못 읽힌다.
			let readBytes = 0, writeBytes = 0, discardBytes = 0, discardCount = 0;
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
				else if (g === 'discard') { discardBytes += e.size; discardCount++; }
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
				percentiles: behaviorPercentiles.map(p => quantile(lat, p / 100)),
				maxLatency: lat.length ? lat[lat.length - 1] : 0,
				discardBytes,
				discardCount,
				success: b.success
			};
		});
	});


	// ── Stats helpers ──
	// 지연/기간 포맷은 TraceStatsView 가 자체적으로 갖는다.
	// 아래 둘은 Behavior 구간별 표에서만 쓴다.
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
						<!-- /trace 와 같은 구간 범례 (BoundaryLegend).
						     밴드에 라벨을 안 그리므로(촘촘하면 겹친다) 여기가 유일한
						     색 대조표이자 구간을 껐다 켜는 수단이다.

						     ⚠ 컴포넌트는 구간을 **배열 index** 로 식별하고 이 시트는
						     문자열 키로 식별한다. hiddenSteps 를 문자열로 두는 건 job 을
						     바꿨을 때 위치 index 가 다른 구간을 가리키는 걸 막기 위해서다.
						     그래서 변환은 넘기는 이 지점에서만 한다. -->
						<div class="mb-2 space-y-0.5">
							<BoundaryLegend
								boundaries={allBoundaries}
								hidden={hiddenBoundaryIdx}
								color={behaviorSolid}
								onToggle={(i) => toggleStep(boundaryKey(allBoundaries[i], i))}
								onShowAll={() => (hiddenSteps = new Set())}
								onHideAll={() => (hiddenSteps = new Set(allBoundaries.map((b, i) => boundaryKey(b, i))))}
								allHiddenNote="표시할 구간이 없습니다 — “전체 선택”"
							/>
							{#if zoomRange && zoomRange.max > zoomRange.min}
								<!-- 축이 좁혀졌다는 걸 알린다 — 모르면 "데이터가 왜 이것뿐이지" 가 된다. -->
								<div class="text-[10px] {captionMuted}">
									· 선택 구간으로 확대됨 ({(zoomRange.max - zoomRange.min).toFixed(2)}s)
								</div>
							{/if}
						</div>
					{/if}
					{#if loadingRaw}
						<div class="flex items-center justify-center py-12"><LoaderIcon class="size-5 animate-spin text-muted-foreground" /></div>
					{:else if rawResult && rawResult.events.length > 0}
						<!-- /trace 와 **같은 차트 화면**을 그대로 쓴다 (TraceChartView).
						     차트 종류 사이드바, Send/Complete/All 탭, 범례, brush, 구간 밴드가
						     전부 그 안에 있다. 여기서 다시 만들면 두 화면이 또 갈라진다.

						     ⚠ series 에는 slicedEvents(=loop·구간만 반영)를 넘긴다.
						     action 은 TraceChartView 가 소유하므로 미리 거르면 이중 필터가
						     되어 탭이 조용히 빈다. -->
						{#if zoomRange}
							<div class="text-[9px] text-muted-foreground mb-1">· 선택 구간만</div>
						{/if}
						<TraceChartView
							series={chartSeries}
							meta={chartMeta}
							traceType={chartTraceType}
							zoomed={!!zoomRange}
							boundaries={allBoundaries}
							hiddenBoundaries={hiddenBoundaryIdx}
							onZoomChange={(start, end) => {
								filterStartTime = String(start);
								filterEndTime = String(end);
								applyFilter();
							}}
							onResetZoom={handleResetZoom}
							onBrushSelected={handleBrushSelected}
						/>
					{:else}
						<div class="text-center text-xs text-muted-foreground py-8">데이터 없음</div>
					{/if}
				</Tabs.Content>

				<!-- Statistics Tab -->
				<Tabs.Content value="stats" class="pt-2 space-y-3">
					{#if zoomRange}
						<div class="rounded border bg-muted/30 px-2 py-1 text-[9px] {captionMuted}">
							아래 수치는 <b>선택한 구간</b>({(zoomRange.max - zoomRange.min).toFixed(2)}s)만
							집계한 값입니다. 전체를 보려면 구간 범례에서 “전체 선택”.
						</div>
					{/if}
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
						<!-- /trace 와 **같은 통계 화면**을 그대로 쓴다 (TraceStatsView).
						     개요 카드, Latency/CMD 표, 2단 히스토그램 탭, 크기 그룹 탭,
						     mgmt 표가 전부 그 안에 있다. toStatsResponse 가 필드 이름만
						     맞춰 넘긴다. -->
						<TraceStatsView stats={toStatsResponse(statsResult)} traceType={chartTraceType} />
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
						<BehaviorTimeline
							boundaries={allBoundaries}
							events={filteredEvents}
							edgeSec={edgeUncertaintySec}
							selected={selectedSteps}
							keyOf={boundaryKey}
							colorOf={behaviorSolid}
							labelOf={behaviorLabel}
							isIdle={isIdleStep}
							groupOf={getCmdGroup}
							onToggle={toggleSelectStep}
							onClearSelection={() => (selectedSteps = new Set())}
						/>

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
								<b>pNN</b> 은 느린 쪽 (100-NN)% 의 지연입니다 — p99 면 가장 느린 1%. 평균이 좋아도
								p99 가 크면 사용자는 멈칫하는 걸 느낍니다. 볼 분위수는 위 입력칸에서 바꿀 수 있습니다.
								<b>Read/Write</b> 비중은 그 구간이 무엇을 했는지 말해 줍니다 (콜드 실행은 보통 read 우위,
								촬영·다운로드는 write 우위). <b>Discard</b> 는 삭제·캐시정리(trim/unmap)로,
								여기가 크면 저장소를 비우는 작업이 돈 것입니다.
							</div>
						</div>

						<div class="flex items-center gap-2 text-[9px]">
							<span class={captionMuted}>분위수</span>
							<input
								bind:value={percentileInput}
								placeholder="50, 95, 99"
								class="w-40 border rounded px-1.5 py-0.5 bg-background font-mono"
								title="쉼표로 구분. 0~100 사이 값 (예: 50, 95, 99, 99.9)" />
							<span class={captionMuted}>
								꼬리를 어디까지 볼지는 워크로드마다 다릅니다 — p99.9 처럼 소수도 됩니다.
							</span>
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
										<th class="text-right py-1 px-2 font-medium">Discard</th>
										{#each behaviorPercentiles as pv}
											<th class="text-right py-1 px-2 font-medium">{fmtPct(pv)}</th>
										{/each}
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
											<td class="text-right py-1 px-2" title="{row.discardCount.toLocaleString()} events">
												{row.discardBytes > 0 ? fmtBytes(row.discardBytes) : '-'}
											</td>
											{#each row.percentiles as pvv}
												<td class="text-right py-1 px-2">{fmtLatency(pvv)}</td>
											{/each}
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
