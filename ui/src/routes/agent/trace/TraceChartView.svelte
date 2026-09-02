<script lang="ts">
	import type { StepBoundary } from './types.js';
	import * as echarts from 'echarts';
	import { onDestroy, onMount } from 'svelte';
	import type { ChartMeta } from './types.js';
	import { createCmdColorAssigner, isMgmtCmd, isHiddenInChart, getCmdGroup } from './cmdColors.js';
	import BoxSelectIcon from '@lucide/svelte/icons/box-select';

	type Series = {
		time: number[];
		lba: number[];
		qd: number[];
		cpu: number[];
		dtoc: number[];
		ctoc: number[];
		ctod: number[];
		action: string[];
		cmd: string[];
		// IO size — **parquet 원본 단위 그대로**(ftrace UFS=4KB 블록 수, ftrace Block=512B
		// 섹터 수, fsio_*=bytes). KB 환산은 아래 sizeKb 가 traceType 을 보고 한다.
		//
		// optional 인 이유는 두 가지다. 하나는 size 컬럼이 없는 차트 응답 호환,
		// 다른 하나가 더 중요하다 — **단위가 섞인 잡(UFS+Block 동시 수집)에서는
		// 호출부가 이 필드를 아예 안 넘겨** Size 차트를 숨긴다. 4096 배수와 512 배수를
		// 한 축에 올리면 에러 없이 그럴듯하게 틀린 그래프가 나오기 때문이다.
		size?: number[];
		// bpftrace fsio_* (fsio_ufs / fsio_block) 전용 io_flags u64 비트마스크. 다른 type 은 0.
		// f2fs hint 비트가 2^53 을 넘어 bigint 로 보존 (number 다운캐스트 금지).
		ioFlags?: BigUint64Array | bigint[];
		// fsio_* 전용 syscall 문자열(vfs_write/vfs_read/"-" 등). 다른 type 은 빈 문자열.
		syscall?: string[];
	};

	export interface BrushRange {
		timeMin: number;
		timeMax: number;
		yMin: number;
		yMax: number;
		chartKey: string;
	}

	interface Props {
		series: Series;
		meta: ChartMeta | null;
		traceType: 'ufs' | 'block' | 'ufscustom' | 'fsio_ufs' | 'fsio_block';
		zoomed: boolean;
		onZoomChange: (start: number, end: number) => void;
		onResetZoom: () => void;
		onBrushSelected?: (range: BrushRange) => void;
		/**
		 * fsio Flags 패널의 io_flags / syscall 선택을 서버사이드 필터로 올린다.
		 * 미지정이면 예전처럼 클라이언트(샘플) 필터로 동작 — 호출부 없는 곳의 하위호환.
		 */
		onFsioFilterChange?: (f: {
			ioFlagsAny: number | null;
			ioFlagsAll: number | null;
			syscallList: string[] | null;
		}) => void;
		/** agent 시나리오 스텝 구간 — 차트에 밴드로 깔아 "어떤 행동이었나" 를 보여준다. */
		boundaries?: StepBoundary[];
		/**
		 * 숨긴 구간 index 집합. 여기 든 구간은 밴드를 안 그린다.
		 * 전부 넣으면 밴드가 통째로 사라진다 — "구분 없이 보고 싶을 때" 용.
		 */
		hiddenBoundaries?: Set<number>;
	}

	let {
		series,
		meta,
		traceType,
		zoomed,
		boundaries = [],
		hiddenBoundaries = new Set<number>(),
		onZoomChange,
		onResetZoom,
		onBrushSelected,
		onFsioFilterChange
	}: Props = $props();

	// ── 시나리오 구간 밴드 ────────────────────────────────────────────────
	//
	// 구간의 startedMono/finishedMono 는 parquet `time` 과 같은 축(초)이라
	// x 값으로 그대로 쓴다 — 변환하면 밴드가 통째로 밀린다.
	//
	// 색은 agent UI 와 같은 방식으로 순번 순환. 의미가 있는 색이 아니라
	// "옆 구간과 구분" 이 목적이라 채도를 낮게 깐다.
	const BOUNDARY_RGB: ReadonlyArray<string> = [
		'59,130,246',
		'16,185,129',
		'245,158,11',
		'139,92,246',
		'236,72,153',
		'20,184,166'
	];

	// ⚠️ 밴드에 **라벨을 그리지 않는다.** 구간이 촘촘하거나 데이터가 밀집한 구간에서는
	// 라벨이 점 더미에 파묻혀 읽히지도 않으면서 그래프만 가린다. 색 ↔ 구간 대조는
	// 위쪽 범례 칩이 맡는다 (거기엔 항상 온전한 이름이 나온다).
	const boundaryBands = $derived.by(() =>
		boundaries
			.map((b, i) => ({ b, i }))
			.filter(({ i }) => !hiddenBoundaries.has(i))
			.map(({ b, i }) => {
				const rgb = BOUNDARY_RGB[i % BOUNDARY_RGB.length];
				return [
					{
						xAxis: b.startedMono,
						itemStyle: { color: `rgba(${rgb},0.10)` }
					},
					{ xAxis: b.finishedMono }
				];
			})
	);

	/** 서버사이드 필터를 쓸 수 있는가 — 부모가 콜백을 준 경우에만. */
	const serverSideFsioFilter = $derived(!!onFsioFilterChange);

	// fsio io_flags 39비트 전체 정의 — Rust src/parsers/bpftrace_tsv.rs 의 F_* 상수와 동일.
	// 비트값이 2^53 을 넘는 f2fs hint 가 있어 bigint 로 보존한다.
	// group: UI 패널에서 묶어 표시하기 위한 라벨.
	type FlagDef = { bit: bigint; name: string; group: string };
	const FSIO_FLAG_BITS: ReadonlyArray<FlagDef> = [
		// 기본 IO 종류
		{ bit: 0x1n, name: 'READ', group: 'IO' },
		{ bit: 0x2n, name: 'WRITE', group: 'IO' },
		{ bit: 0x4n, name: 'DISCARD', group: 'IO' },
		{ bit: 0x8n, name: 'FLUSH', group: 'IO' },
		{ bit: 0x10n, name: 'TRIM', group: 'IO' },
		// open / IO 모드
		{ bit: 0x100n, name: 'O_SYNC', group: 'Mode' },
		{ bit: 0x200n, name: 'O_DIRECT', group: 'Mode' },
		{ bit: 0x400n, name: 'O_APPEND', group: 'Mode' },
		{ bit: 0x800n, name: 'O_DSYNC', group: 'Mode' },
		{ bit: 0x1000n, name: 'SYNC_PATH', group: 'Mode' },
		{ bit: 0x2000n, name: 'REQ_SYNC', group: 'Mode' },
		{ bit: 0x4000n, name: 'REQ_PRIO', group: 'Mode' },
		{ bit: 0x8000n, name: 'REQ_RAHEAD', group: 'Mode' },
		// data / metadata 종류
		{ bit: 0x10000n, name: 'DATA', group: 'Kind' },
		{ bit: 0x20000n, name: 'METADATA', group: 'Kind' },
		{ bit: 0x40000n, name: 'INODE', group: 'Kind' },
		{ bit: 0x80000n, name: 'BITMAP', group: 'Kind' },
		{ bit: 0x100000n, name: 'DIRENT', group: 'Kind' },
		{ bit: 0x200000n, name: 'XATTR', group: 'Kind' },
		// journal / gc / extent
		{ bit: 0x400000n, name: 'JOURNAL', group: 'Journal/GC' },
		{ bit: 0x800000n, name: 'CHECKPOINT', group: 'Journal/GC' },
		{ bit: 0x1000000n, name: 'GC', group: 'Journal/GC' },
		{ bit: 0x2000000n, name: 'EXTENT_ALLOC', group: 'Journal/GC' },
		{ bit: 0x4000000n, name: 'EXTENT_FREE', group: 'Journal/GC' },
		{ bit: 0x8000000n, name: 'BMAP', group: 'Journal/GC' },
		// path / context
		{ bit: 0x100000000n, name: 'BUFFERED', group: 'Path' },
		{ bit: 0x200000000n, name: 'DIRECT_IO', group: 'Path' },
		{ bit: 0x400000000n, name: 'MMAP_WRITEBACK', group: 'Path' },
		{ bit: 0x800000000n, name: 'WRITEBACK_KWORKER', group: 'Path' },
		{ bit: 0x1000000000n, name: 'FSYNC_TRIGGERED', group: 'Path' },
		{ bit: 0x10000000000n, name: 'SAW_VFS', group: 'Path' },
		// f2fs segment hint
		{ bit: 0x1000000000000n, name: 'F2FS_NODE_WRITE', group: 'f2fs' },
		{ bit: 0x2000000000000n, name: 'F2FS_DATA_WRITE', group: 'f2fs' },
		{ bit: 0x4000000000000n, name: 'F2FS_META_WRITE', group: 'f2fs' },
		{ bit: 0x8000000000000n, name: 'F2FS_NODE_GC', group: 'f2fs' },
		{ bit: 0x10000000000000n, name: 'F2FS_DATA_GC', group: 'f2fs' },
		{ bit: 0x20000000000000n, name: 'F2FS_HOT_DATA', group: 'f2fs' },
		{ bit: 0x40000000000000n, name: 'F2FS_WARM_DATA', group: 'f2fs' },
		{ bit: 0x80000000000000n, name: 'F2FS_COLD_DATA', group: 'f2fs' }
	];

	// bpftrace fsiotrace 기반 두 trace_type 은 동일한 io_flags(u64) 비트마스크 사용.
	const isBpftraceFsio = $derived(traceType === 'fsio_block' || traceType === 'fsio_ufs');

	// 사용자가 선택한 비트 마스크 (체크된 비트들의 OR). 0 = 필터 미사용 = 전체 표시.
	let selectedFlagMask = $state(0n);
	// 모드: 'any' = 선택 비트 중 하나라도 켜진 행, 'all' = 모두 켜진 행.
	let flagFilterMode = $state<'any' | 'all'>('any');
	let flagsPanelOpen = $state(false);

	type AvailableFlag = { bit: bigint; name: string; group: string; count: number };

	// 데이터에 실제 등장하는 비트만 노출 — 카운트 동봉.
	const availableFlags = $derived.by(() => {
		if (!isBpftraceFsio || !series.ioFlags) return [] as AvailableFlag[];
		const flags = series.ioFlags;
		const n = flags.length;
		const counts = new Map<bigint, number>();
		for (const { bit } of FSIO_FLAG_BITS) {
			let c = 0;
			for (let i = 0; i < n; i++) {
				if ((flags[i] & bit) !== 0n) c++;
			}
			if (c > 0) counts.set(bit, c);
		}
		return FSIO_FLAG_BITS.filter((f) => counts.has(f.bit)).map((f) => ({
			...f,
			count: counts.get(f.bit) ?? 0
		}));
	});

	// 패널 표시용 — group 별로 묶어 순서 유지.
	const availableFlagGroups = $derived.by(() => {
		const groups: { group: string; flags: AvailableFlag[] }[] = [];
		for (const f of availableFlags) {
			let g = groups.find((x) => x.group === f.group);
			if (!g) {
				g = { group: f.group, flags: [] };
				groups.push(g);
			}
			g.flags.push(f);
		}
		return groups;
	});

	// 사용자가 선택한 syscall 값 집합. 비어 있으면 필터 미사용 = 전체 표시.
	// OR 매칭(선택된 syscall 중 하나라도 일치) — io_flags 필터와는 AND 로 결합.
	let selectedSyscalls = $state(new Set<string>());

	// 데이터에 실제 등장하는 syscall 값만 노출 — 카운트 동봉, 빈도순 정렬.
	const availableSyscalls = $derived.by(() => {
		if (!isBpftraceFsio || !series.syscall) return [] as { name: string; count: number }[];
		const arr = series.syscall;
		const counts = new Map<string, number>();
		for (let i = 0; i < arr.length; i++) {
			const s = arr[i];
			if (!s) continue; // 빈 값(비-fsio fallback)은 제외
			counts.set(s, (counts.get(s) ?? 0) + 1);
		}
		return [...counts.entries()]
			.map(([name, count]) => ({ name, count }))
			.sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
	});

	// 인덱스가 io_flags 필터를 통과하는지. 선택 0개거나 fsio 아니면 항상 true.
	// 서버사이드 모드에선 이미 서버가 걸러 보냈으므로 여기선 통과시킨다 (이중 필터 방지).
	function passesFlagFilter(i: number): boolean {
		if (serverSideFsioFilter) return true;
		if (!isBpftraceFsio || selectedFlagMask === 0n || !series.ioFlags) return true;
		const f = series.ioFlags[i];
		if (flagFilterMode === 'all') return (f & selectedFlagMask) === selectedFlagMask;
		return (f & selectedFlagMask) !== 0n;
	}

	// 인덱스가 syscall 필터를 통과하는지. 선택 0개거나 fsio 아니면 항상 true.
	function passesSyscallFilter(i: number): boolean {
		if (serverSideFsioFilter) return true;
		if (!isBpftraceFsio || selectedSyscalls.size === 0 || !series.syscall) return true;
		return selectedSyscalls.has(series.syscall[i]);
	}

	/** 현재 선택을 서버 필터 형태로 부모에 올린다. */
	function applyFsioFilter() {
		if (!onFsioFilterChange) return;
		const mask = selectedFlagMask === 0n ? null : Number(selectedFlagMask);
		onFsioFilterChange({
			ioFlagsAny: flagFilterMode === 'any' ? mask : null,
			ioFlagsAll: flagFilterMode === 'all' ? mask : null,
			syscallList: selectedSyscalls.size > 0 ? [...selectedSyscalls] : null
		});
	}

	function toggleFlagBit(bit: bigint) {
		selectedFlagMask =
			(selectedFlagMask & bit) !== 0n ? selectedFlagMask & ~bit : selectedFlagMask | bit;
	}

	function toggleSyscall(name: string) {
		const next = new Set(selectedSyscalls);
		if (next.has(name)) next.delete(name);
		else next.add(name);
		selectedSyscalls = next;
	}

	function clearFlags() {
		selectedFlagMask = 0n;
		selectedSyscalls = new Set();
	}

	function selectAllAvailableFlags() {
		let m = 0n;
		for (const { bit } of availableFlags) m |= bit;
		selectedFlagMask = m;
		selectedSyscalls = new Set(availableSyscalls.map((s) => s.name));
	}

	// Brush context menu state
	let showBrushMenu = $state(false);
	let brushMenuX = $state(0);
	let brushMenuY = $state(0);
	let brushTargetKey = $state<string | null>(null);

	// Agent 패턴: 차트 6종 + group(common/send/complete) 분류
	//   common: 어떤 action 에서도 의미 있음 → 항상 노출 후보
	//   send: send 이벤트에만 값(ctod)
	//   complete: complete 이벤트에만 값(dtoc, ctoc)
	// `as const` 를 쓰지 않는다 — 쓰면 readonly 튜플 + 리터럴 타입이 되어
	// 아래 availableChartItems 의 filter 분기마다 서로 다른 배열 타입이 나오고
	// 삼항으로 합쳐질 때 대입 불가가 된다. 소비처는 key 를 문자열로만 쓰므로
	// (charts[key] / visibleCharts.has(key)) 리터럴 타입이 필요 없다.
	type ChartItem = {
		key: string;
		label: string;
		yLabel: string;
		group: 'common' | 'send' | 'complete';
	};
	const CHART_ITEMS: ChartItem[] = [
		{ key: 'lba', label: 'LBA', yLabel: 'LBA', group: 'common' },
		{ key: 'size', label: 'Size', yLabel: 'Size (KB)', group: 'common' },
		// discard(UNMAP/TRIM) 전용 Size. 데이터 IO 와 크기대가 달라 같은 축에 두면
		// 한쪽이 안 보인다 — dtoc_mgmt 를 분리한 것과 같은 이유다.
		// 실측: read/write 는 1MB 에서 끝나는데 discard 는 27MB 까지 가서, 14행(0.2%)이
		// 축을 27배 늘려 read/write 를 아래 4% 안에 깔아버렸다.
		{ key: 'size_discard', label: 'Size (discard)', yLabel: 'Size (KB)', group: 'common' },
		{ key: 'qd', label: 'Queue Depth', yLabel: 'QD', group: 'common' },
		{ key: 'cpu', label: 'CPU', yLabel: 'CPU', group: 'common' },
		{ key: 'ctod', label: 'CtoD Latency', yLabel: 'CtoD (ms)', group: 'send' },
		{ key: 'dtoc', label: 'DtoC Latency', yLabel: 'DtoC (ms)', group: 'complete' },
		{ key: 'ctoc', label: 'CtoC Latency', yLabel: 'CtoC (ms)', group: 'complete' },
		// mgmt(UIC/Query/TM) 전용 DtoC. 데이터 IO 와 지연 크기대가 달라 같은 축에 두면
		// 한쪽이 바닥에 깔린다. 별도 차트로 분리해 각자 자동 스케일을 쓰게 한다.
		{ key: 'dtoc_mgmt', label: 'DtoC (mgmt)', yLabel: 'DtoC (ms)', group: 'complete' }
	];

	// latency 계열은 0 제외 (zero-valued rows 시각화 배제)
	const LATENCY_KEYS = new Set(['dtoc', 'ctoc', 'ctod', 'dtoc_mgmt']);

	// ── size → KB ────────────────────────────────────────────────────────
	//
	// parquet 의 `size` 는 trace_type 마다 **단위가 다르다**. Go 쪽 fsioCols.SectorBytes
	// (trace/fsio_cols.go) 와 같은 규칙이다:
	//
	//   ftrace ufs   : 4096 B  (1 = 4KB LBA 한 칸)
	//   ftrace block : 512 B   (1 = 섹터 한 칸)
	//   fsio_*       : 1 B     (bpftrace 가 이미 bytes 로 준다)
	//
	// ⚠ **주소 단위와 헷갈리면 안 된다.** fsio_block 의 lba/sector 는 block 과 똑같이
	// 512B 단위지만, 같은 fsio_block 의 `size` 는 이미 bytes 라 계수가 1 이다.
	// (Go 의 AddrUnitBytes vs SectorBytes 가 갈리는 지점이 정확히 여기다.)
	// 주소 쪽 계수를 여기 가져다 쓰면 fsio size 가 512배로 부푼다 — 에러 없이.
	const SIZE_UNIT_BYTES = $derived(
		traceType === 'fsio_ufs' || traceType === 'fsio_block'
			? 1
			: traceType === 'block'
				? 512
				: 4096
	);

	// log 축에서 빠지는 size=0 이벤트 수 (flush 등). 0 이면 배지를 안 띄운다.
	const zeroSizeCount = $derived.by(() => {
		const src = series.size;
		if (!src) return 0;
		let n = 0;
		for (let i = 0; i < src.length; i++) if (!(src[i] > 0)) n++;
		return n;
	});

	/**
	 * Size 차트 y 축 스케일. 'auto' 면 데이터 폭을 보고 정한다.
	 *
	 * log 는 공짜가 아니다 — 64KB 와 128KB 의 차이가 눈에 잘 안 들어와
	 * "이 구간은 write 가 두 배로 커졌다" 같은 관찰을 놓치게 된다.
	 * 그래서 **필요할 때만** 쓴다.
	 *
	 * 판정 기준은 **중앙값 대비 max** 다 (아래 SIZE_LOG_THRESHOLD 주석 참고).
	 * discard 는 별도 차트로 빠지므로 이 판정에는 들어오지 않는다.
	 */
	let sizeScaleMode = $state<'auto' | 'log' | 'linear'>('auto');

	// **중앙값** 대비 max 가 이 배수를 넘으면 log 로 간다.
	//
	// ⚠ p99 가 아니라 p50 이다. p99 는 "이상치가 얼마나 튀나" 를 재는데, 정작 문제는
	// **본체가 읽히느냐**다. 실측(discard 제외): max/p99 = 8배라 선형으로 가는데,
	// 정작 최빈값 4KB(43%)와 두 번째 봉우리 128KB(16%)가 선형 축에선 0% / 12% 높이로
	// 붙어버려 두 모드를 구분할 수 없다. log 면 0% / 63% 로 갈라진다.
	// 같은 데이터의 max/p50 은 128배 — 이 지표가 "본체가 눌리는가" 를 제대로 잡는다.
	//
	// 16배면 선형 축에서 중앙값이 아래 6% 안에 깔린다 — 그 지점을 경계로 잡았다.
	const SIZE_LOG_THRESHOLD = 16;

	/**
	 * Size 계열 차트의 값 분포. discard 차트와 데이터 IO 차트는 **모집단이 다르므로**
	 * 축 범위를 각자 계산한다 — 한쪽 기준을 공유하면 분리한 의미가 없다.
	 */
	function sizeStatsFor(discard: boolean) {
		const src = sizeKb;
		const cmdArr = series.cmd;
		if (!src || src.length === 0) return null;
		const pos: number[] = [];
		for (let i = 0; i < src.length; i++) {
			const v = src[i];
			if (!(v > 0)) continue; // log 축에 0 은 못 올린다
			if ((getCmdGroup(cmdArr[i] || '') === 'discard') !== discard) continue;
			pos.push(v);
		}
		if (pos.length === 0) return null;
		pos.sort((a, b) => a - b);
		return {
			min: pos[0],
			max: pos[pos.length - 1],
			p50: pos[Math.floor((pos.length - 1) * 0.5)],
			n: pos.length
		};
	}

	const sizeStats = $derived.by(() => sizeStatsFor(false));
	const sizeDiscardStats = $derived.by(() => sizeStatsFor(true));

	/**
	 * 데이터가 log 를 필요로 하는가 (max / p99).
	 *
	 * ⚠ **데이터 IO(discard 제외) 기준**으로 판정한다. discard 는 원래 자릿수가 커서
	 * 늘 log 가 나오는데, 그걸로 read/write 축까지 정하면 discard 를 별도 차트로
	 * 분리한 의미가 사라진다.
	 */
	const sizeNeedsLog = $derived.by(() => {
		const st = sizeStats;
		if (!st || st.n < 10 || !(st.p50 > 0)) return false;
		return st.max / st.p50 > SIZE_LOG_THRESHOLD;
	});

	const sizeUseLog = $derived(
		sizeScaleMode === 'auto' ? sizeNeedsLog : sizeScaleMode === 'log'
	);

	/**
	 * Size 차트 y 축 범위 — 데이터를 감싸는 2의 거듭제곱 경계.
	 *
	 * log 축에 min/max 를 안 주면 ECharts 가 여유를 크게 잡아 점이 한쪽에 몰린다.
	 * IO 크기는 4/8/16... 이라 2의 거듭제곱으로 맞춰야 눈금이 실제 값과 겹친다.
	 */
	function sizeDomainOf(st: { min: number; max: number } | null) {
		if (!st) return { min: 1, max: 1024 };
		const floor2 = (v: number) => Math.pow(2, Math.floor(Math.log2(v)));
		const ceil2 = (v: number) => Math.pow(2, Math.ceil(Math.log2(v)));
		const min = floor2(st.min);
		const max = ceil2(st.max);
		return { min, max: max > min ? max : min * 2 };
	}

	// size 배열을 KB 로 환산해 캐시한다. 원본을 그대로 그리면 같은 4KB IO 가
	// trace_type 에 따라 1 / 8 / 4096 으로 찍혀 서로 비교가 안 된다.
	const sizeKb = $derived.by<number[] | null>(() => {
		const src = series.size;
		if (!src) return null;
		const k = SIZE_UNIT_BYTES / 1024;
		const out = new Array<number>(src.length);
		for (let i = 0; i < src.length; i++) out[i] = src[i] * k;
		return out;
	});

	// Action 탭: send / complete / all — 이벤트 필터링
	// UFSCUSTOM 은 모든 이벤트가 완료 상태 + CPU 없음 → Action 탭/CPU 차트 숨김
	const isUfsCustom = $derived(traceType === 'ufscustom');
	let activeActionTab = $state<'send' | 'complete' | 'all'>('complete');

	// Action 탭에 맞게 사이드바에 뜨는 차트 목록이 달라짐 + trace_type 별 가용 차트 필터
	const availableChartItems = $derived.by(() => {
		let items = activeActionTab === 'send'
			? CHART_ITEMS.filter((c) => c.group === 'common' || c.group === 'send')
			: activeActionTab === 'complete'
				? CHART_ITEMS.filter((c) => c.group === 'common' || c.group === 'complete')
				: CHART_ITEMS;
		if (isUfsCustom) {
			// CPU 컬럼 값은 모두 0 이므로 제외
			items = items.filter((c) => c.key !== 'cpu');
		}
		// mgmt(UPIU/UIC) 는 UFS 프로토콜 계층 개념이라 fsio_ufs 에만 존재한다.
		// block 계층에는 해당 이벤트 자체가 없으므로 차트를 내보내지 않는다.
		if (traceType !== 'fsio_ufs') {
			items = items.filter((c) => c.key !== 'dtoc_mgmt');
		}
		// size 는 호출부가 안 넘길 수 있다 — size 컬럼 없는 차트 응답,
		// 그리고 **단위가 섞인 잡(UFS+Block 동시 수집)**. 후자를 그리면 4096 배수와
		// 512 배수가 한 축에 섞여 조용히 틀린 그래프가 되므로 아예 내보내지 않는다.
		if (!series.size) {
			items = items.filter((c) => c.key !== 'size' && c.key !== 'size_discard');
		}
		// discard 가 한 건도 없으면 빈 차트를 내보내지 않는다 (fsio/일부 잡은 없다).
		if (!sizeDiscardStats) {
			items = items.filter((c) => c.key !== 'size_discard');
		}
		return items;
	});

	let visibleCharts = $state<Set<string>>(new Set(['lba', 'qd', 'dtoc', 'dtoc_mgmt']));

	/**
	 * 범례를 접은 차트들.
	 *
	 * mgmt 이름("Read Attribute(bBackgroundOpStatus)")은 길어서 범례가 폭의 상당 부분을
	 * 가져간다. 접으면 그 공간을 플롯이 되찾는다. 차트별로 따로 기억한다.
	 */
	let collapsedLegends = $state<Set<string>>(new Set());

	function toggleLegend(key: string) {
		const next = new Set(collapsedLegends);
		if (next.has(key)) next.delete(key);
		else next.add(key);
		collapsedLegends = next;
		const c = charts[key];
		const item = CHART_ITEMS.find((x) => x.key === key);
		if (c && !c.isDisposed() && item) {
			c.setOption(buildOption(key, item.label, item.yLabel), { notMerge: true });
		}
	}

	// CPU 차트 표시 모드:
	//   'cmd' — x=time, y=cpu, 색상=cmd (기본)
	//   'lba' — x=time, y=lba, 색상=cpu (어느 LBA 요청이 어느 CPU 에서 처리됐는지)
	let cpuColorMode = $state<'cmd' | 'lba'>('cmd');

	// CPU 0~7 고정 팔레트 (LBA 뷰용)
	const CPU_COLORS = [
		'#3b82f6', // 0 blue
		'#22c55e', // 1 green
		'#f97316', // 2 orange
		'#a855f7', // 3 purple
		'#ef4444', // 4 red
		'#06b6d4', // 5 cyan
		'#eab308', // 6 yellow
		'#ec4899'  // 7 pink
	];

	function toggleChart(key: string) {
		const next = new Set(visibleCharts);
		if (next.has(key)) {
			if (next.size > 1) next.delete(key);
		} else {
			next.add(key);
		}
		visibleCharts = next;
	}

	// action tab 변경 시 사용 불가능한 차트 자동 숨김 (최소 1개 유지)
	$effect(() => {
		void activeActionTab;
		const availKeys = new Set<string>(availableChartItems.map((c) => c.key));
		let changed = false;
		const next = new Set(visibleCharts);
		for (const k of [...next]) {
			if (!availKeys.has(k)) {
				next.delete(k);
				changed = true;
			}
		}
		if (next.size === 0) {
			next.add('lba');
			changed = true;
		}
		if (changed) visibleCharts = next;
	});

	// action 분류 헬퍼
	function actionMatchesTab(action: string): boolean {
		if (isUfsCustom) return true; // UFSCUSTOM 은 모든 이벤트가 완료 — 탭 구분 없음
		if (activeActionTab === 'all') return true;
		const low = (action || '').toLowerCase();
		// UFS management 이벤트(upiu_*/uic_*/exception).
		//
		// 아래 부분일치 규칙에 우연히 걸리거나(uic_send, upiu_query_rsp) 아예 안
		// 걸린다(upiu_nop_out, exception). 규칙에 맡기면 기본 탭(complete)에서
		// 일부만 보여 "왜 절반만 나오지" 가 된다. 방향을 명시적으로 판정한다.
		//   send 쪽: *_send / *_req / *_out
		//   comp 쪽: *_complete / *_rsp / *_in
		// 어느 쪽도 아닌 단발 이벤트(exception, 방향 미상 uic)는 양쪽에 다 보인다
		// — 숨기면 그 시점을 영영 못 본다.
		if (low.startsWith('upiu_') || low.startsWith('uic') || low === 'exception') {
			const isSend = low.endsWith('_send') || low.endsWith('_req') || low.endsWith('_out');
			const isComp = low.endsWith('_complete') || low.endsWith('_rsp') || low.endsWith('_in');
			if (!isSend && !isComp) return true; // 단발 — 항상 표시
			return activeActionTab === 'send' ? isSend : isComp;
		}
		if (activeActionTab === 'send') return low.includes('send') || low.includes('issue') || low === 'q';
		// complete
		return low.includes('complete') || low.includes('rsp') || low === 'c';
	}

	// 기본 차트 높이 — visible 갯수에 따라
	const defaultChartHeight = $derived(
		visibleCharts.size === 1 ? 560 : visibleCharts.size === 2 ? 380 : visibleCharts.size === 3 ? 320 : 280
	);
	// 사용자 리사이즈 값 — 한 차트에서 드래그 리사이즈하면 모두 동기화
	let userChartHeight = $state<number | null>(null);
	const chartHeight = $derived(userChartHeight ?? defaultChartHeight);

	let containers: Record<string, HTMLDivElement | null> = $state({});

	// 차트 카드 리사이즈 감지 → userChartHeight 동기화 (모든 차트 같은 높이)
	function observeResize(node: HTMLElement) {
		const ro = new ResizeObserver(() => {
			const h = node.offsetHeight;
			if (h > 0 && Math.abs(h - chartHeight) > 5) {
				userChartHeight = Math.max(150, Math.min(1200, h));
			}
		});
		ro.observe(node);
		return {
			destroy: () => ro.disconnect()
		};
	}
	let charts: Record<string, echarts.ECharts | null> = {};

	// 여러 차트 간 legend 상태 동기화
	let legendSelected = $state<Record<string, boolean>>({});

	// 데이터 변경 시마다 색상 재할당 (cmd 등장 순서로 group 내 index)
	let colorFor = createCmdColorAssigner();

	$effect(() => {
		void series; // 재할당 트리거
		colorFor = createCmdColorAssigner();
	});

	/**
	 * 6개 차트가 공유하는 인덱스 그룹.
	 * series / action 탭 / cpu 모드가 바뀔 때 1회 계산 → 각 차트 buildSeries 는
	 * 미리 모인 indices 만 빠르게 [x,y] 페어로 변환.
	 *
	 * 기존엔 6 × N 풀 순회였는데 → N 풀 순회 1번 + 6 × (cmd 개수 만큼 small loop) 로 줄어든다.
	 */
	type CmdIndexGroups = {
		// cmd → 그 cmd 에 속한 row index 들 (action 탭 필터까지 미리 적용)
		cmdIndices: Map<string, number[]>;
		// CPU LBA 모드용: cpu 번호 → indices
		cpuIndices: Map<number, number[]>;
	};

	const indexGroups = $derived.by<CmdIndexGroups>(() => {
		const time = series.time;
		const cmdArr = series.cmd;
		const actionArr = series.action;
		const cpuArr = series.cpu;
		const lbaArr = series.lba;

		const cmdIndices = new Map<string, number[]>();
		const cpuIndices = new Map<number, number[]>();
		const n = time.length;

		for (let i = 0; i < n; i++) {
			if (!actionMatchesTab(actionArr[i] ?? '')) continue;
			if (!passesFlagFilter(i)) continue;
			if (!passesSyscallFilter(i)) continue;
			const x = time[i];
			if (!Number.isFinite(x)) continue;

			// cmd grouping (대부분의 차트가 사용)
			const c = cmdArr[i] || 'unknown';
			let g = cmdIndices.get(c);
			if (!g) {
				g = [];
				cmdIndices.set(c, g);
			}
			g.push(i);

			// CPU LBA 모드용 — 항상 만들어둠 (cpu 모드 전환 시 즉시 사용)
			const cpu = cpuArr[i];
			const lba = lbaArr[i];
			if (Number.isFinite(cpu) && Number.isFinite(lba)) {
				let cg = cpuIndices.get(cpu);
				if (!cg) {
					cg = [];
					cpuIndices.set(cpu, cg);
				}
				cg.push(i);
			}
		}
		return { cmdIndices, cpuIndices };
	});

	function buildSeries(yKey: keyof Series | 'dtoc_mgmt' | 'size_discard') {
		// ⚠ size 도 0 을 뺀다 — 이유가 latency 와 **다르다.** latency 0 은 "측정 안 됨"
		// 이라 의미가 없어서 빼지만, size 0 은 flush 처럼 **실재하는 이벤트**다.
		// 그런데 log 축에 0 은 올라가지 않는다 (ECharts 가 조용히 버린다).
		// 버리는 건 같지만 사용자가 모르면 안 되므로 아래 zeroSizeCount 로 표시한다.
		const isSizeChart = yKey === 'size' || yKey === 'size_discard';
		const excludeZero =
			LATENCY_KEYS.has(yKey as string) || (isSizeChart && sizeUseLog);
		const time = series.time;
		// dtoc_mgmt 는 가상 키 — y 값은 dtoc 를 그대로 쓰고 cmd 로 mgmt 만 골라낸다.
		const isMgmtChart = yKey === 'dtoc_mgmt';
		// size_discard 도 가상 키 — y 는 size 를 그대로 쓰고 cmd 로 discard 만 고른다.
		const isDiscardChart = yKey === 'size_discard';
		// size 만 원본 배열이 아니라 KB 환산본을 쓴다 (단위가 trace_type 마다 달라서).
		const yArr = isSizeChart
			? (sizeKb ?? [])
			: (series[(isMgmtChart ? 'dtoc' : yKey) as keyof Series] as number[]);

		// CPU 차트 + LBA 뷰: x=time, y=LBA, 시리즈는 CPU 번호별
		if (yKey === 'cpu' && cpuColorMode === 'lba') {
			const lbaArr = series.lba;
			const out: any[] = [];
			const sortedCpus = [...indexGroups.cpuIndices.keys()].sort((a, b) => a - b);
			for (const cpu of sortedCpus) {
				const indices = indexGroups.cpuIndices.get(cpu)!;
				const data: [number, number][] = new Array(indices.length);
				let w = 0;
				for (let k = 0; k < indices.length; k++) {
					const i = indices[k];
					data[w++] = [time[i], lbaArr[i]];
				}
				data.length = w;
				out.push({
					name: `CPU ${cpu}`,
					type: 'scatter' as const,
					data,
					symbolSize: 3,
					large: true,
					largeThreshold: 2000,
					progressive: 5000,
					progressiveThreshold: 10000,
					itemStyle: { color: CPU_COLORS[cpu % CPU_COLORS.length] }
				});
			}
			return out;
		}

		// cmd 별 grouping — indexGroups.cmdIndices 재사용
		const out: any[] = [];
		const sortedCmds = [...indexGroups.cmdIndices.keys()].sort((a, b) => a.localeCompare(b));
		for (const cmd of sortedCmds) {
			const mgmt = isMgmtCmd(cmd);
			// mgmt 전용 차트에는 mgmt 만, 나머지 차트에는 데이터 IO 만.
			// (섞으면 지연 크기대가 달라 한쪽이 안 보인다 — 차트를 나눈 이유)
			if (isMgmtChart !== mgmt) continue;
			// Size 차트는 discard 를 갈라 그린다. 한 축에 두면 27MB discard 가 축을
			// 늘려 1MB 이하인 read/write 가 바닥에 깔린다 (실측 read/write max = 1MB).
			if (isSizeChart && (getCmdGroup(cmd) === 'discard') !== isDiscardChart) continue;
			// 응답 UPIU 는 send 와 짝이라 차트에서만 감춘다 (통계/Raw 에는 남음).
			if (isHiddenInChart(cmd)) continue;
			const indices = indexGroups.cmdIndices.get(cmd)!;
			const data: [number, number][] = new Array(indices.length);
			let w = 0;
			for (let k = 0; k < indices.length; k++) {
				const i = indices[k];
				const y = yArr[i];
				if (!Number.isFinite(y)) continue;
				if (excludeZero && y <= 0) continue;
				data[w++] = [time[i], y];
			}
			data.length = w;
			if (data.length === 0) continue;
			out.push({
				name: cmd,
				type: 'scatter' as const,
				data,
				symbolSize: 3,
				large: true,
				largeThreshold: 2000,
				progressive: 5000,
				progressiveThreshold: 10000,
				itemStyle: { color: colorFor(cmd) }
			});
		}
		return out;
	}

	/**
	 * 모든 차트가 공유할 x축(time) 범위.
	 *
	 * 차트별 자동 맞춤을 쓰면 mgmt 처럼 등장 구간이 다른 시리즈에서 축이 어긋나
	 * 위아래 차트를 같은 시각으로 읽을 수 없다. 전체 이벤트의 min/max 로 고정한다.
	 * (필터/줌으로 데이터가 줄어도 축은 전체 기준을 유지 — 비교 가능성이 우선)
	 */
	const timeDomain = $derived.by(() => {
		const t = series.time;
		if (!t || t.length === 0) return { min: undefined, max: undefined };
		let lo = Infinity, hi = -Infinity;
		for (let i = 0; i < t.length; i++) {
			const v = t[i];
			if (!Number.isFinite(v)) continue;
			if (v < lo) lo = v;
			if (v > hi) hi = v;
		}
		if (!Number.isFinite(lo) || !Number.isFinite(hi)) return { min: undefined, max: undefined };

		// ⚠️ 여기서 반올림하지 않으면 축 양끝 라벨이 "2.7784394299999997" 처럼 찍힌다.
		// ECharts 는 min/max 를 명시하면 그 값을 **그대로** 첫/마지막 눈금으로 쓴다
		// (자동 스케일일 때처럼 예쁜 수로 정리해 주지 않는다).
		//
		// 그래서 데이터 범위를 덮는 "깔끔한 간격"의 배수로 바깥쪽 정렬(floor/ceil)한다.
		// 1·2·5 × 10^n 중에서 전체 폭의 1/10 에 가장 가까운 값을 step 으로 쓴다.
		const span = hi - lo;
		if (span <= 0) return { min: lo - 0.001, max: hi + 0.001 };
		const mag = Math.pow(10, Math.floor(Math.log10(span / 10)));
		const norm = span / 10 / mag;
		const step = (norm >= 5 ? 5 : norm >= 2 ? 2 : 1) * mag;
		// 부동소수 잔여물 제거 — step 자릿수까지만 남긴다.
		const decimals = Math.max(0, -Math.floor(Math.log10(step)));
		const round = (v: number) => Number(v.toFixed(decimals));
		return {
			min: round(Math.floor(lo / step) * step),
			max: round(Math.ceil(hi / step) * step)
		};
	});

	function buildOption(key: string, label: string, yLabel: string) {
		const seriesList = buildSeries(key as keyof Series | 'dtoc_mgmt' | 'size_discard');
		const isCpuLba = key === 'cpu' && cpuColorMode === 'lba';
		const legendNames = seriesList.map((s) => s.name);

		// Y축 설정: 차트 종류/모드별 분기
		//   CPU(cmd 모드): 0~8 고정 (HWQ 0~7 표시)
		//   CPU(lba 모드): LBA auto scale
		//   그 외: data range
		let yAxisConfig: Record<string, unknown> = {
			type: 'value',
			scale: true,
			axisLabel: { fontSize: 10 }
		};
		if (key === 'cpu' && !isCpuLba) {
			yAxisConfig = { type: 'value', scale: false, min: 0, max: 8, axisLabel: { fontSize: 10 } };
		}
		// size 는 **log 축**이다. discard(UNMAP)는 한 번에 수십 MB 를 지우는데 그런 행이
		// 전체의 0.2% 뿐이라, 선형 축이면 그 몇 점이 축을 혼자 늘려 나머지 99%(p99 =
		// 128KB)가 바닥에 깔려 아무것도 안 보인다. 실측: max 27,728KB / p99 128KB.
		// log 면 4KB~27MB 가 12.8 옥타브로 고르게 퍼져 small write 뭉치와 대형 discard 를
		// 한 화면에서 같이 읽을 수 있다.
		const isSizeKey = key === 'size' || key === 'size_discard';
		const sizeDomain = isSizeKey
			? sizeDomainOf(key === 'size_discard' ? sizeDiscardStats : sizeStats)
			: { min: 1, max: 1024 };
		if (isSizeKey && !sizeUseLog) {
			// 선형 — 폭이 좁을 땐 이쪽이 더 잘 읽힌다 (2배 차이가 2배로 보인다).
			yAxisConfig = {
				type: 'value',
				scale: true,
				axisLabel: {
					fontSize: 10,
					formatter: (v: number) =>
						v >= 1024 ? `${Number((v / 1024).toFixed(2))} MB` : `${Number(v.toFixed(2))} KB`
				}
			};
		} else if (isSizeKey) {
			// ⚠ 눈금을 ECharts 기본값에 맡기면 안 된다. base 10 이면 10 / 100 / 1000 으로
			// 찍히는데, 데이터 상한이 1024KB(=1MB)라 맨 위 라벨이 "1000 KB" 가 되고
			// 정작 제일 큰 1MB IO 들이 라벨 없는 축 끝에 붙는다 — "1000KB 가 최대인가?"
			// 로 오해하게 된다.
			//
			// IO 크기는 4/8/16/... 2의 거듭제곱이니 축도 그렇게 간다.
			// logBase 2 + interval N 은 **log 공간의 간격**이라 N=1 이면 한 칸이 2배다
			// (LogScale.getTicks 가 log 공간에서 눈금을 뽑고 base^val 로 되돌린다).
			//
			// 간격은 옥타브 수로 정한다 — 넓은 범위에서 1옥타브씩 찍으면 라벨이 겹친다.
			// 이때 상한이 눈금에 안 걸리면 맨 위가 다시 라벨을 잃으므로, 상한까지
			// 딱 나눠떨어지는 간격만 고른다.
			const octaves = Math.round(Math.log2(sizeDomain.max) - Math.log2(sizeDomain.min));
			let tickStep = 1;
			for (const cand of [1, 2, 3, 4]) {
				if (octaves % cand === 0 && octaves / cand <= 8) {
					tickStep = cand;
					break;
				}
			}
			yAxisConfig = {
				type: 'log',
				logBase: 2,
				interval: tickStep,
				min: sizeDomain.min,
				max: sizeDomain.max,
				axisLabel: {
					fontSize: 10,
					// 2의 거듭제곱이라 소수는 안 나오지만, 방어적으로 정리해 둔다
					// (예전에 base 10 일 때 "9.765625 MB" 가 찍힌 적이 있다).
					formatter: (v: number) =>
						v >= 1024 ? `${Number((v / 1024).toFixed(2))} MB` : `${Number(v.toFixed(2))} KB`
				}
			};
		}

		// legend 폭은 라벨 길이에 따라 크게 다르다.
		//   데이터 IO: "0x28" (4자)      → 90px 로 충분
		//   mgmt:      "Read Attribute(bBackgroundOpStatus)" (35자) → 90px 면 차트를 침범
		// 가장 긴 라벨 기준으로 우측 여백을 잡는다. 10px 폰트에서 글자당 ~5.6px + 아이콘/여백.
		const legendCollapsed = collapsedLegends.has(key);
		const longestLabel = legendNames.reduce((m, n) => Math.max(m, (n ?? '').length), 0);
		// 접었으면 범례 공간을 플롯에 돌려준다.
		const legendWidth = legendCollapsed
			? 12
			: Math.min(280, Math.max(90, Math.round(longestLabel * 5.6) + 28));

		// 구간 밴드 — 데이터가 아니라 배경이라 축 범위/legend 에 영향을 주면 안 된다.
		// 빈 data 의 series 하나에 markArea 만 얹어서 그 둘을 피한다.
		const boundarySeries = boundaryBands.length
			? [{
					type: 'line' as const,
					name: '__boundaries__',
					data: [] as number[][],
					silent: true,
					// legend 목록에 뜨면 사용자가 끌 수 있는 항목처럼 보인다 — 이름을 숨긴다.
					tooltip: { show: false },
					markArea: {
						silent: true,
						label: { show: false },
						data: boundaryBands
					}
				}]
			: [];

		return {
			animation: false,
			// legend 를 오른쪽 세로로 배치 → 우측에 legend 공간 확보
			grid: { left: 12, right: legendWidth, top: 10, bottom: 38, containLabel: true },
			tooltip: {
				trigger: 'item',
				formatter: (p: any) => {
					// markPoint/markLine 또는 빈 series 위 hover 시 value 가 undefined/scalar 일 수 있어 방어.
					const v = p?.value;
					if (!Array.isArray(v) || v.length < 2) {
						return p?.seriesName ?? '';
					}
					const t = Number(v[0]);
					const y = v[1];
					const timeStr = Number.isFinite(t) ? `${t.toFixed(6)}s` : String(v[0]);
					if (isCpuLba) {
						const lbaStr =
							typeof y === 'number' && Number.isFinite(y)
								? y.toLocaleString()
								: String(y);
						return `${p.seriesName}<br/>time: ${timeStr}<br/>LBA: ${lbaStr}`;
					}
					if (isSizeKey && typeof y === 'number' && Number.isFinite(y)) {
						// KB 환산이 부동소수라 512B 섹터 IO 가 0.49999999999999994 로 나올 수
						// 있다. 소수 세 자리까지만 남기고 뒤 0 은 지운다 (0.5 / 4 / 1024).
						const kb = Number(y.toFixed(3));
						return `${p.seriesName}<br/>time: ${timeStr}<br/>${yLabel}: ${kb.toLocaleString()}`;
					}
					return `${p.seriesName}<br/>time: ${timeStr}<br/>${yLabel}: ${y}`;
				}
			},
			legend: {
				show: !legendCollapsed,
				type: 'scroll',
				orient: 'vertical',
				right: 4,
				top: 'middle',
				icon: 'circle',
				itemWidth: 8,
				itemHeight: 8,
				itemGap: 6,
				// 캡(280px)을 넘는 라벨은 잘라서 표시 — 안 자르면 차트 영역을 다시 침범한다.
				// 전체 이름은 hover tooltip 으로 볼 수 있다.
				textStyle: { fontSize: 10, width: 240, overflow: 'truncate' },
				tooltip: { show: true },
				data: legendNames,
				// CPU LBA 모드에선 CPU 0~7 별도 legend. cmd 모드와 legendSelected 공유 X
				selected: isCpuLba ? undefined : legendSelected
			},
			// 차트 우측 상단 brush toolbox 아이콘 숨김 (우클릭 → 영역선택 메뉴 사용)
			toolbox: { show: false },
			brush: {
				brushType: false,
				xAxisIndex: 0,
				yAxisIndex: 0,
				toolbox: [] as any,
				brushStyle: {
					borderWidth: 1,
					color: 'rgba(59, 130, 246, 0.1)',
					borderColor: '#3b82f6'
				}
			},
			xAxis: {
				type: 'value' as const,
				name: 'time (s)',
				nameLocation: 'middle',
				nameGap: 28,
				// ⚠️ scale:true (자동 맞춤) 를 쓰면 차트마다 자기 데이터 범위로 축이 달라진다.
				// mgmt 는 데이터 IO 와 등장 구간이 다르므로 DtoC 는 2.7~3.5, DtoC(mgmt) 는
				// 2.9~3.4 처럼 어긋나 위아래 차트를 시간으로 비교할 수 없었다.
				// 전체 이벤트 기준의 공통 범위로 고정해 모든 차트가 같은 시간축을 쓰게 한다.
				min: timeDomain.min,
				max: timeDomain.max,
				nameTextStyle: { fontSize: 10 },
				axisLabel: { fontSize: 10 }
			},
			yAxis: yAxisConfig,
			dataZoom: [{ type: 'inside' as const, xAxisIndex: 0 }],
			series: [...seriesList, ...boundarySeries]
		} as echarts.EChartsOption;
	}

	// 첫 legend 초기값 — 모두 true. 실제 변경 있을 때만 write (effect loop 방지)
	function ensureLegendSelected() {
		const allCmds = new Set<string>();
		for (const c of series.cmd) allCmds.add(c || 'unknown');
		let mutated = false;
		const next: Record<string, boolean> = { ...legendSelected };
		for (const c of allCmds) {
			if (!(c in next)) {
				next[c] = true;
				mutated = true;
			}
		}
		if (mutated) legendSelected = next;
	}

	let suppressLegendSync = false;
	function attachLegendSync(chart: echarts.ECharts) {
		chart.off('legendselectchanged');
		chart.on('legendselectchanged', (e: any) => {
			if (suppressLegendSync) return;
			legendSelected = { ...(e.selected ?? {}) };
			suppressLegendSync = true;
			for (const [, c] of Object.entries(charts)) {
				if (!c || c === chart || c.isDisposed()) continue;
				c.setOption({ legend: { selected: legendSelected } }, { lazyUpdate: true });
			}
			suppressLegendSync = false;
		});
	}

	function attachZoomSync(lbaChart: echarts.ECharts) {
		lbaChart.on('datazoom', () => {
			const opt = lbaChart.getOption() as any;
			const dz = opt?.dataZoom?.[0];
			if (!dz) return;
			if (typeof dz.startValue === 'number' && typeof dz.endValue === 'number') {
				onZoomChange(dz.startValue, dz.endValue);
			}
		});
	}

	function attachBrush(chart: echarts.ECharts, key: string) {
		chart.off('brushEnd');
		chart.on('brushEnd', (params: any) => {
			const areas = params.areas;
			if (!areas || areas.length === 0) return;
			const area = areas[0];
			if (!area.coordRange) return;
			const xRange = area.coordRange[0];
			const yRange = area.coordRange[1];
			if (!Array.isArray(xRange) || !Array.isArray(yRange)) return;
			if (Math.abs(xRange[1] - xRange[0]) < 0.001) return;
			onBrushSelected?.({
				timeMin: xRange[0],
				timeMax: xRange[1],
				yMin: yRange[0],
				yMax: yRange[1],
				chartKey: key
			});
			// 드래그 완료 후 brush 비활성화 (일회성)
			chart.dispatchAction({ type: 'brush', areas: [] });
			chart.dispatchAction({
				type: 'takeGlobalCursor',
				key: 'brush',
				brushOption: { brushType: false }
			});
		});
	}

	function activateBrush(key: string) {
		const c = charts[key];
		if (!c) return;
		c.dispatchAction({
			type: 'takeGlobalCursor',
			key: 'brush',
			brushOption: { brushType: 'rect', brushMode: 'single' }
		});
		showBrushMenu = false;
	}

	function deactivateBrush(key: string) {
		const c = charts[key];
		if (!c) return;
		c.dispatchAction({
			type: 'takeGlobalCursor',
			key: 'brush',
			brushOption: { brushType: false }
		});
	}

	// Ctrl/Cmd + 드래그 = 사각 박스 영역 선택 (우클릭 메뉴와 동등 기능, 더 빠른 진입).
	//
	// 두 단계 트리거로 robust 하게:
	//   (1) pointerdown 캡처 단계 — Ctrl 누른 채로 클릭 시작하면 즉시 brush 활성화.
	//       echarts 가 mousedown 부터 brush rect 그리기 시작하므로 _pointerdown 이전_ 에
	//       takeGlobalCursor 를 dispatch 해야 첫 클릭이 brush 로 잡힘. 실제로는 capture
	//       phase 에 hook 해서 echarts 의 down handler 보다 먼저 실행되도록 함.
	//   (2) keydown — 차트 위에 마우스가 있을 때 Ctrl 누르면 hover 차트에 brush 활성화
	//       (이 패턴은 사용자가 키 먼저 누르고 마우스 움직이는 시나리오 커버).
	//
	// 둘 다 keyup 에서 자동 비활성. brush rect 한 번 그리면 brushEnd 가 단발성으로 비활성.
	let hoverChartKey: string | null = $state(null);
	let ctrlActiveKey: string | null = null;

	function onChartMouseEnter(key: string) {
		hoverChartKey = key;
	}
	function onChartMouseLeave(key: string) {
		if (hoverChartKey === key) hoverChartKey = null;
		if (ctrlActiveKey === key) {
			deactivateBrush(key);
			ctrlActiveKey = null;
		}
	}
	/** pointerdown 캡처 — Ctrl/Cmd 클릭 시 echarts down handler 보다 먼저 brush 활성화. */
	function onChartPointerDownCapture(e: PointerEvent, key: string) {
		if (!onBrushSelected) return;
		// 좌클릭 + Ctrl/Cmd 일 때만. 우클릭(button === 2) 은 contextmenu 메뉴가 처리.
		if (e.button !== 0) return;
		if (!(e.ctrlKey || e.metaKey)) return;
		ctrlActiveKey = key;
		activateBrush(key);
	}
	function onGlobalKeydown(e: KeyboardEvent) {
		if (!onBrushSelected) return;
		// e.ctrlKey 는 다른 키와 함께 눌렀을 때도 true 라 가짜 trigger 가능 — Control/Meta 단독 키일 때만.
		if (e.key !== 'Control' && e.key !== 'Meta') return;
		if (e.repeat) return;
		if (!hoverChartKey) return;
		if (ctrlActiveKey === hoverChartKey) return;
		ctrlActiveKey = hoverChartKey;
		activateBrush(hoverChartKey);
	}
	function onGlobalKeyup(e: KeyboardEvent) {
		if (e.key !== 'Control' && e.key !== 'Meta') return;
		if (ctrlActiveKey) {
			deactivateBrush(ctrlActiveKey);
			ctrlActiveKey = null;
		}
	}

	function onChartContextMenu(e: MouseEvent, key: string) {
		if (!onBrushSelected) return;
		e.preventDefault();
		brushMenuX = e.clientX;
		brushMenuY = e.clientY;
		brushTargetKey = key;
		showBrushMenu = true;
	}

	function closeBrushMenu() {
		showBrushMenu = false;
		brushTargetKey = null;
	}

	function initChart(key: string) {
		const el = containers[key];
		if (!el) return;
		// dispose 즉시 reference 비움 — 새 instance 가 set 되기 전 사이에 ResizeObserver / setOption
		// 등이 fire 해도 disposed instance 호출을 피하기 위함 (echarts 내부 eachBuiltinLayer NPE 방지).
		const prev = charts[key];
		charts[key] = null;
		prev?.dispose();
		const c = echarts.init(el);
		const item = CHART_ITEMS.find((x) => x.key === key);
		if (!item) return;
		c.setOption(buildOption(key, item.label, item.yLabel));
		attachLegendSync(c);
		attachBrush(c, key);
		// zoom: visible 중 첫 번째 차트를 master 로
		const firstVisible = CHART_ITEMS.find((x) => visibleCharts.has(x.key))?.key;
		if (key === firstVisible) attachZoomSync(c);
		charts[key] = c;
	}

	function rebuildAll() {
		for (const { key, label, yLabel } of CHART_ITEMS) {
			const c = charts[key];
			// disposed instance 호출 방지 — echarts ECharts 인스턴스에 isDisposed() 존재.
			if (!c || c.isDisposed() || !visibleCharts.has(key)) continue;
			// notMerge=true 로 이전 series 모두 교체. lazyUpdate 는 ECharts 의 다음 frame 까지 묶어 처리.
			c.setOption(buildOption(key, label, yLabel), { notMerge: true, lazyUpdate: true });
			attachLegendSync(c);
			attachBrush(c, key);
		}
		// zoom master 재설정
		const first = CHART_ITEMS.find((x) => visibleCharts.has(x.key))?.key;
		const firstChart = first ? charts[first] : null;
		if (firstChart && !firstChart.isDisposed()) attachZoomSync(firstChart);
	}

	let resizeObs: ResizeObserver | null = null;

	onMount(() => {
		ensureLegendSelected();
		for (const { key } of CHART_ITEMS) {
			if (visibleCharts.has(key)) initChart(key);
		}
		resizeObs = new ResizeObserver(() => {
			for (const c of Object.values(charts)) {
				if (c && !c.isDisposed()) c.resize();
			}
		});
		for (const el of Object.values(containers)) if (el) resizeObs.observe(el);
	});

	onDestroy(() => {
		resizeObs?.disconnect();
		for (const c of Object.values(charts)) c?.dispose();
	});

	// visible 변화에 따라 init/dispose
	$effect(() => {
		void visibleCharts;
		setTimeout(() => {
			for (const { key } of CHART_ITEMS) {
				if (visibleCharts.has(key)) {
					if (!charts[key]) initChart(key);
				} else {
					const c = charts[key];
					charts[key] = null;
					c?.dispose();
				}
			}
			for (const c of Object.values(charts)) {
				if (c && !c.isDisposed()) c.resize();
			}
		}, 0);
	});

	// series / action tab / cpu 모드 변화에 따라 rebuild
	$effect(() => {
		void series;
		void activeActionTab;
		void cpuColorMode;
		void sizeUseLog; // 스케일 전환 시 축 타입이 바뀌므로 다시 그려야 한다
		ensureLegendSelected();
		rebuildAll();
	});

</script>

<div class="flex gap-2 h-full">
	<!-- Sidebar: action tab + chart selector -->
	<div class="w-32 shrink-0 space-y-2 border-r pr-2 sticky top-0 self-start text-[10px]">
		{#if !isUfsCustom}
			<div>
				<div class="font-semibold text-muted-foreground mb-1">Action</div>
				<div class="flex gap-0.5">
					{#each ['send', 'complete', 'all'] as tab}
						<button
							onclick={() => (activeActionTab = tab as typeof activeActionTab)}
							class="flex-1 px-1.5 py-1 rounded text-[10px] capitalize transition-colors
								{activeActionTab === tab
									? 'bg-primary text-primary-foreground'
									: 'border hover:bg-muted'}"
						>
							{tab}
						</button>
					{/each}
				</div>
			</div>
		{/if}
		<div>
			<div class="font-semibold text-muted-foreground mb-1">Charts</div>
			{#each availableChartItems as item (item.key)}
				<button
					onclick={() => toggleChart(item.key)}
					class="w-full text-left px-2 py-1.5 rounded transition-colors
						{visibleCharts.has(item.key)
							? 'bg-primary/10 text-primary font-medium'
							: 'text-muted-foreground hover:bg-muted hover:text-foreground'}"
				>
					{item.label}
				</button>
			{/each}
		</div>
		{#if onBrushSelected}
			<div class="text-muted-foreground text-[9px] leading-snug pt-1 border-t">
				<div>우클릭 → 영역 선택</div>
				<div>Ctrl + 드래그 → 영역 선택</div>
				<div>차트 하단 모서리 드래그 → 높이 조절</div>
			</div>
		{/if}
	</div>

	<!-- Main area: meta + stats summary + charts -->
	<div class="flex-1 min-w-0 flex flex-col gap-2">
		<!-- Meta bar -->
		<div class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground px-1 shrink-0">
			<span>type: <b>{traceType}</b></span>
			{#if meta}
				<span>schema: {meta.schemaVersion}</span>
				<span>total: {meta.totalEvents.toLocaleString()}</span>
				<span>sampled: {meta.sampledEvents.toLocaleString()}</span>
			{/if}
			{#if zoomed}
				<button class="underline text-primary" onclick={onResetZoom}>전체 범위로</button>
			{/if}
			{#if isBpftraceFsio && (availableFlags.length > 0 || availableSyscalls.length > 0)}
				{@const selectedCount =
					availableFlags.filter((f) => (selectedFlagMask & f.bit) !== 0n).length +
					selectedSyscalls.size}
				<button
					class="ml-auto inline-flex items-center gap-1 px-2 py-0 rounded border text-[10px] hover:bg-muted transition-colors {selectedCount > 0 ? 'bg-primary/10 border-primary text-primary' : ''}"
					onclick={() => (flagsPanelOpen = !flagsPanelOpen)}
					title="io_flags 비트 + syscall 필터"
				>
					Flags{selectedCount > 0 ? ` (${selectedCount})` : ''}
					<span class="opacity-60">{flagsPanelOpen ? '▴' : '▾'}</span>
				</button>
			{/if}
		</div>

		<!-- Flags 필터 패널 (fsio_ufs / fsio_block 전용) — io_flags 비트 + syscall 값 -->
		{#if isBpftraceFsio && flagsPanelOpen && (availableFlags.length > 0 || availableSyscalls.length > 0)}
			<div class="border rounded bg-card p-2 text-xs space-y-1.5 shrink-0">
				<!--
					⚠️ 이 패널의 카운트와 필터는 전부 **샘플링된 차트 데이터** 기준이다.
					series.ioFlags / series.syscall 는 서버가 targetPoints 로 decimate 한 결과라
					전체 행이 아니다. Statistics 탭은 필터를 무시하고 전체를 집계하므로 두 화면의
					숫자가 다를 수 있다 — 사용자가 이를 전체 수치로 오해하지 않도록 명시한다.
					(근본 해결은 필터를 서버사이드로 내리는 것 — Phase 1)
				-->
				{#if meta && meta.sampledEvents < meta.totalEvents}
					<div class="flex items-start gap-1 text-[10px] text-amber-600 dark:text-amber-500 pb-1">
						<span class="shrink-0">⚠</span>
						{#if serverSideFsioFilter}
							<span>
								아래 <b>카운트</b>는 샘플 {meta.sampledEvents.toLocaleString()}행 기준입니다
								(전체 {meta.totalEvents.toLocaleString()}행). 단, <b>적용</b> 시 필터는
								서버에서 전체 {meta.totalEvents.toLocaleString()}행에 적용됩니다.
							</span>
						{:else}
							<span>
								아래 카운트는 <b>샘플 {meta.sampledEvents.toLocaleString()}행</b> 기준입니다
								(전체 {meta.totalEvents.toLocaleString()}행).
								이 필터는 <b>차트에만</b> 적용되며 Statistics 탭은 전체를 집계합니다.
							</span>
						{/if}
					</div>
				{/if}
				<div class="flex items-center gap-2 pb-1 border-b">
					<span class="font-medium">io_flags · syscall 필터</span>
					{#if availableFlags.length > 0}
						<div class="flex gap-0.5 text-[10px]">
							<button
								class="px-1.5 py-0 rounded transition-colors {flagFilterMode === 'any' ? 'bg-primary text-primary-foreground' : 'border hover:bg-muted'}"
								onclick={() => (flagFilterMode = 'any')}
								title="선택한 비트 중 하나라도 켜진 행"
							>Any</button>
							<button
								class="px-1.5 py-0 rounded transition-colors {flagFilterMode === 'all' ? 'bg-primary text-primary-foreground' : 'border hover:bg-muted'}"
								onclick={() => (flagFilterMode = 'all')}
								title="선택한 비트가 모두 켜진 행"
							>All</button>
						</div>
					{/if}
					<div class="ml-auto flex gap-0.5 text-[10px]">
						<button class="px-1.5 py-0 rounded border hover:bg-muted" onclick={selectAllAvailableFlags}>전체</button>
						<button class="px-1.5 py-0 rounded border hover:bg-muted" onclick={clearFlags}>해제</button>
						{#if serverSideFsioFilter}
							<!-- 서버사이드 모드 — 선택 즉시가 아니라 명시적 적용 (재조회 비용이 크므로) -->
							<button
								class="px-1.5 py-0 rounded bg-primary text-primary-foreground hover:opacity-90"
								onclick={applyFsioFilter}
								title="선택한 io_flags / syscall 을 서버 필터로 적용 (chart · Statistics · Raw Data 모두 반영)"
							>적용</button>
						{/if}
					</div>
				</div>
				<div class="space-y-1.5 max-h-60 overflow-auto">
					<!-- syscall 값 집합 (OR 매칭, io_flags 와 AND 결합) -->
					{#if availableSyscalls.length > 0}
						<div>
							<div class="text-[10px] font-medium text-muted-foreground uppercase tracking-wide mb-0.5">
								syscall <span class="normal-case opacity-70">({availableSyscalls.length}종 · OR)</span>
							</div>
							<div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-x-3 gap-y-0.5">
								{#each availableSyscalls as s (s.name)}
									<label class="flex items-center gap-1.5 cursor-pointer hover:bg-muted/40 px-1 rounded">
										<input
											type="checkbox"
											class="size-3"
											checked={selectedSyscalls.has(s.name)}
											onchange={() => toggleSyscall(s.name)}
										/>
										<span class="truncate" title={s.name}>{s.name || '(빈값)'}</span>
										<span class="ml-auto text-[10px] text-muted-foreground">{s.count.toLocaleString()}</span>
									</label>
								{/each}
							</div>
						</div>
					{/if}
					<!-- io_flags 비트 (group 별) -->
					{#each availableFlagGroups as g (g.group)}
						<div>
							<div class="text-[10px] font-medium text-muted-foreground uppercase tracking-wide mb-0.5">{g.group}</div>
							<div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-x-3 gap-y-0.5">
								{#each g.flags as f (f.bit)}
									<label class="flex items-center gap-1.5 cursor-pointer hover:bg-muted/40 px-1 rounded">
										<input
											type="checkbox"
											class="size-3"
											checked={(selectedFlagMask & f.bit) !== 0n}
											onchange={() => toggleFlagBit(f.bit)}
										/>
										<span class="truncate" title={f.name}>{f.name}</span>
										<span class="ml-auto text-[10px] text-muted-foreground">{f.count.toLocaleString()}</span>
									</label>
								{/each}
							</div>
						</div>
					{/each}
				</div>
				{#if selectedFlagMask === 0n && selectedSyscalls.size === 0}
					<div class="text-[10px] text-muted-foreground italic">선택이 없으면 전체 표시 · io_flags 와 syscall 은 AND 결합</div>
				{/if}
				<!-- 파일/프로세스 필터는 상단 Filter 바에 한 곳으로 모아둔다.
					 같은 필터를 두 군데서 편집하면 어느 쪽이 참인지 헷갈리므로 여기선 안내만. -->
				<div class="text-[10px] text-muted-foreground border-t pt-1">
					파일명 · 프로세스 필터는 상단 <b>Filter</b> 바에서 (검색형 다중 선택)
				</div>
			</div>
		{/if}

		<!-- Charts stacked -->
		<div class="flex-1 min-h-0 overflow-auto space-y-2">
			{#each availableChartItems as item (item.key)}
				{#if visibleCharts.has(item.key)}
					<div
						class="border rounded overflow-hidden flex flex-col resize-y"
						style:height={chartHeight + 'px'}
						style:min-height="180px"
						style:max-height="1200px"
						use:observeResize
					>
						<div class="px-2 py-0.5 text-[10px] font-medium border-b bg-muted/40 flex items-center gap-2 shrink-0">
							<span>{item.label}</span>
							{#if item.key === 'size' || item.key === 'size_discard'}
								<!-- 스케일 전환. 자동 판정을 쓰되 사용자가 덮어쓸 수 있게 한다 —
								     log 는 2배 차이를 눌러 보이게 하므로 "선형으로 보고 싶다" 가
								     정당한 요구다. -->
								<div class="flex gap-0.5">
									{#each [{ v: 'auto' as const, label: '자동' }, { v: 'linear' as const, label: '선형' }, { v: 'log' as const, label: 'log' }] as opt}
										<button
											class="px-1 py-0 rounded text-[9px] border transition-colors {sizeScaleMode === opt.v
												? 'bg-primary text-primary-foreground border-primary'
												: 'hover:bg-muted'}"
											onclick={() => (sizeScaleMode = opt.v)}
											title={opt.v === 'auto'
												? '데이터 폭(max/p99)이 크면 log, 아니면 선형'
												: opt.v === 'log'
													? 'log 축 — discard 처럼 자릿수가 다른 값이 섞였을 때'
													: '선형 축 — 2배 차이가 2배로 보인다'}
										>{opt.label}</button>
									{/each}
								</div>
								{#if sizeUseLog}
									<!-- 축이 log 라는 걸 화면에 적어 둔다. 안 적으면 눈금 간격을
									     선형으로 오해해 "큰 IO 가 별로 없네" 로 잘못 읽는다. -->
									<span class="text-[9px] font-normal text-muted-foreground">log 축</span>
									{#if zeroSizeCount > 0}
										<!-- log 축은 0 을 못 그린다. 조용히 빠지면 flush 가 없었던 걸로
										     보이므로 몇 건이 빠졌는지 반드시 표시한다. -->
										<span
											class="text-[9px] font-normal text-amber-600 dark:text-amber-500"
											title="size=0 인 이벤트(flush 등)는 log 축에 표시할 수 없어 제외됐다. 선형 축으로 바꾸면 보인다."
										>size=0 {zeroSizeCount.toLocaleString()}건 제외</span>
									{/if}
								{/if}
							{/if}
							<!-- 범례 접기 — mgmt 처럼 라벨이 길면 범례가 플롯 폭을 크게 잠식한다. -->
							<button
								class="ml-auto px-1.5 py-0 rounded text-[9px] border hover:bg-muted transition-colors"
								onclick={() => toggleLegend(item.key)}
								title={collapsedLegends.has(item.key) ? '범례 펼치기' : '범례 접기 (차트를 넓게)'}
							>
								{collapsedLegends.has(item.key) ? '범례 ▸' : '범례 ▾'}
							</button>
							{#if item.key === 'cpu'}
								<div class="flex gap-0.5">
									{#each [{ v: 'cmd' as const, label: 'CMD' }, { v: 'lba' as const, label: 'LBA' }] as opt}
										<button
											onclick={() => (cpuColorMode = opt.v)}
											class="px-1.5 py-0 rounded text-[9px] transition-colors
												{cpuColorMode === opt.v ? 'bg-primary text-primary-foreground' : 'border hover:bg-muted'}"
											title={opt.v === 'cmd' ? 'Y=CPU · 색상=CMD' : 'Y=LBA · 색상=CPU'}
										>
											{opt.label}
										</button>
									{/each}
								</div>
							{/if}
						</div>
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div
							bind:this={containers[item.key]}
							class="w-full flex-1 min-h-0"
							oncontextmenu={(e) => onChartContextMenu(e, item.key)}
							onmouseenter={() => onChartMouseEnter(item.key)}
							onmouseleave={() => onChartMouseLeave(item.key)}
							onpointerdowncapture={(e) => onChartPointerDownCapture(e, item.key)}
						></div>
					</div>
				{/if}
			{/each}
		</div>
	</div>
</div>

<svelte:window
	onclick={closeBrushMenu}
	onkeydown={onGlobalKeydown}
	onkeyup={onGlobalKeyup}
	onblur={() => {
		if (ctrlActiveKey) {
			deactivateBrush(ctrlActiveKey);
			ctrlActiveKey = null;
		}
	}}
/>

{#if showBrushMenu && brushTargetKey}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<!-- svelte-ignore a11y_click_events_have_key_events -->
	<div
		class="fixed z-50 bg-background border rounded-md shadow-lg py-1 min-w-[140px]"
		style="left: {brushMenuX}px; top: {brushMenuY}px;"
		onclick={(e) => e.stopPropagation()}
	>
		<button
			class="w-full text-left px-3 py-1.5 text-xs hover:bg-muted transition-colors flex items-center gap-2"
			onclick={() => {
				if (brushTargetKey) activateBrush(brushTargetKey);
			}}
		>
			<BoxSelectIcon class="size-3" /> 영역 선택 (X+Y)
		</button>
	</div>
{/if}
