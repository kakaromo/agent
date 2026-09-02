<script lang="ts">
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { DataTable } from '$lib/components/data-table';
	import { getCmdGroup } from './cmdColors.js';
	import type {
		StatsResponse,
		StatsLatency,
		StatsCmd,
		StatsHistogram,
		StatsMgmt,
		StatsDirContiguity,
		StatsAddressRange
	} from './types.js';

	interface Props {
		stats: StatsResponse;
		traceType?: string;
	}
	let { stats, traceType }: Props = $props();

	// fsio_block 은 예전에 continuous 판정이 구조적으로 불가능했다 (연속성 키를 항상 빈 값인
	// io_type 으로 잡아서 영원히 false). 그 시절 parquet 은 값이 0 으로 구워져 있으므로
	// 0.0% 를 그대로 보여주면 "연속 IO 가 없다"는 잘못된 결론을 준다 → "—" + 재파싱 안내.
	// 지금 파싱한 parquet 은 정상 판정되므로 진짜 0 이면 그때만 오해의 소지가 있는데,
	// 재파싱 안내를 따라 확인하면 되므로 안전한 쪽으로 판단.
	const continuousUnavailable = $derived(traceType === 'fsio_block' && stats.continuousCount === 0);

	function fmtDuration(s: number): string {
		if (s < 0) s = Math.abs(s);
		if (s < 60) return `${s.toFixed(2)}s`;
		const m = Math.floor(s / 60);
		return `${m}m ${(s % 60).toFixed(1)}s`;
	}

	function fmtBytes(b: number): string {
		if (b < 1024) return `${b} B`;
		if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
		if (b < 1024 * 1024 * 1024) return `${(b / 1024 / 1024).toFixed(1)} MB`;
		return `${(b / 1024 / 1024 / 1024).toFixed(2)} GB`;
	}

	/**
	 * 건수. 모수를 모르면 '-' — 0 으로 찍으면 "집계 대상이 없다"는 **틀린 사실**이 된다.
	 * (구버전 agent 응답엔 latency count 가 없다.)
	 */
	function fmtCount(v: number | undefined): string {
		if (v == null || !isFinite(v)) return '-';
		return v.toLocaleString();
	}

	function fmtLat(v: number): string {
		if (v == null || !isFinite(v)) return '-';
		if (v < 0.001) return v.toFixed(6);
		if (v < 1) return v.toFixed(3);
		return v.toLocaleString(undefined, { maximumFractionDigits: 2 });
	}

	const latencyFields: { key: keyof StatsLatency; label: string }[] = [
		{ key: 'min', label: 'Min' },
		{ key: 'max', label: 'Max' },
		{ key: 'avg', label: 'Avg' },
		{ key: 'stddev', label: 'StdDev' },
		{ key: 'median', label: 'Median' },
		{ key: 'p99', label: 'P99' },
		{ key: 'p999', label: 'P99.9' },
		{ key: 'p9999', label: 'P99.99' },
		{ key: 'p99999', label: 'P99.999' },
		{ key: 'p999999', label: 'P99.9999' }
	];

	let activeLatencyTab = $state('dtoc');
	let activeCmdTab = $state('overview');
	/**
	 * Latency Histogram 은 탭이 **두 단계**다.
	 *   1단: CMD    (0x28 / 0x2a / R / W ...)
	 *   2단: 지연 종류 (DtoC / CtoD / CtoC)
	 * 표에는 Range × Count 만 남아 한 조합의 분포가 바로 읽힌다.
	 */
	// $derived 가 참조하므로 위에서 선언한다 (use-before-declare 회피).
	let activeHistCmd = $state('');
	const histCmds = $derived<string[]>([
		...new Set(stats.latencyHistograms.map((h) => h.cmd))
	]);
	/**
	 * 지연 종류 표시 순서 — DtoC → CtoD → CtoC.
	 *
	 * 서버가 주는 순서(알파벳: ctoc, ctod, dtoc)를 그대로 쓰면 CTOC 가 먼저 나온다.
	 * 분석은 "요청→완료(DtoC)" 를 먼저 보므로 고정 순서를 명시한다.
	 */
	const LATENCY_ORDER = ['dtoc', 'ctod', 'ctoc'];

	/** 2단 탭 — 선택한 cmd 에 실제로 존재하는 지연 종류만, 위 순서대로. */
	const histTypesForCmd = $derived.by<string[]>(() => {
		const present = new Set(
			stats.latencyHistograms
				.filter((h: StatsHistogram) => h.cmd === activeHistCmd)
				.map((h) => (h.latencyType as string).toLowerCase())
		);
		// 알려진 것 먼저 고정 순서로, 모르는 종류가 생기면 뒤에 붙인다(누락 방지).
		const known = LATENCY_ORDER.filter((t) => present.has(t));
		const extra = [...present].filter((t) => !LATENCY_ORDER.includes(t)).sort();
		return [...known, ...extra];
	});
	const sizeCmds = $derived<string[]>([
		...new Set(stats.cmdSizeCounts.map((c) => c.cmd))
	]);

	/**
	 * CMD 를 성질별로 묶는 그룹 탭.
	 *
	 * cmd 표기는 trace_type 마다 다르다 — UFS/fsio_ufs 는 SCSI opcode('0x28'),
	 * block/fsio_block 은 io_type/rwbs('R','WS','FUA'...). 둘 다 getCmdGroup 이
	 * 이미 read/write/discard/flush/other 로 정규화하므로 그대로 재사용한다.
	 * (차트 색상과 같은 기준이라 표와 차트의 분류가 어긋나지 않는다)
	 */
	const SIZE_GROUP_LABELS: Record<string, string> = {
		read: 'Read',
		write: 'Write',
		discard: 'Discard',
		flush: 'Flush',
		other: 'Other'
	};

	/** 'all' + 실제로 존재하는 그룹만. 없는 그룹 탭은 만들지 않는다. */
	const sizeGroups = $derived<string[]>([
		'all',
		...['read', 'write', 'discard', 'flush', 'other'].filter((g) =>
			sizeCmds.some((c) => getCmdGroup(c) === g)
		)
	]);

	let activeSizeGroup = $state('all');

	/** 현재 그룹 탭에 속한 cmd 들. 'all' 이면 전부. */
	const visibleSizeCmds = $derived<string[]>(
		activeSizeGroup === 'all'
			? sizeCmds
			: sizeCmds.filter((c) => getCmdGroup(c) === activeSizeGroup)
	);
	// UFS management 이벤트 (Query/TM UPIU, UIC). 구버전 Java 배포는 이 키를 안 보내므로
	// optional — `?? []` 로 받아야 아래 .length 가 터지지 않는다.
	const mgmtStats = $derived<StatsMgmt[]>(stats.mgmtStats ?? []);

	// ── read/write × 주소 연속성 ──
	//
	// 위 Continuous 카드와 **다른 수치다.** 저건 방향 구분 없이 직전 요청 1개와만
	// 비교하고, 이건 read 끼리/write 끼리 독립 체인이다. 둘 다 맞고 묻는 질문이
	// 다르므로 배너로 갈라 준다.
	const dirCont = $derived<StatsDirContiguity[]>(stats.directionContiguity ?? []);
	/** 방향별로 { cont, disc, total, ratio } 를 뽑는다. */
	const dirRows = $derived(
		(['read', 'write'] as const).map((dir) => {
			const c = dirCont.find((d) => d.direction === dir && d.contiguous);
			const d = dirCont.find((x) => x.direction === dir && !x.contiguous);
			const total = (c?.count ?? 0) + (d?.count ?? 0);
			return { dir, cont: c, disc: d, total };
		}).filter((r) => r.total > 0)
	);
	/**
	 * 도넛 원둘레. 방향마다 **자기 분모**를 갖는다 — read 와 write 를 한 원에 넣으면
	 * 조각 넓이로 둘을 비교하게 되는데, 그 차이는 요청 수가 달라서지 순차성 차이가
	 * 아니다. 정작 묻고 싶은 "read 중 몇 %가 순차인가" 는 그 그림에 안 나온다.
	 */
	const DONUT_R = 30;
	const DONUT_C = 2 * Math.PI * DONUT_R;

	// ── 주소(LBA/sector) 범위 ──
	//
	// 차트 y축은 자동 스케일이고 툴팁은 점 하나만 보여줘서, 이 표가 없으면
	// "이 워크로드가 주소 공간의 어느 대역을 건드렸나" 를 답할 수단이 없다.
	//
	// ⚠ 값은 **주소 단위**지 바이트가 아니다. unitBytes(UFS 4096 / Block 512 /
	// fsio 1)를 곱해야 바이트가 된다. 안 곱하면 UFS 와 Block 이 8배 어긋난다.
	const addrRange = $derived<StatsAddressRange[]>(stats.addressRange ?? []);
	//
	// ⚠ unitBytes 가 없는 행은 **버린다.** archived 경로는 다른 서비스가 주고
	// `as unknown as` 로 캐스팅돼 들어와서 이 필드가 없을 수 있는데, 그때
	// `|| 1` 로 때우면 "1 addr unit = 1 B" 라고 **단정해서 틀린 사실**을 쓰고
	// Range size 도 4096배 작게 나온다. 표를 안 그리는 쪽이 맞다.
	const addrRows = $derived(
		(['all', 'read', 'write'] as const)
			.map((dir) => addrRange.find((a) => a.direction === dir))
			.filter(
				(a): a is StatsAddressRange =>
					a != null && typeof a.unitBytes === 'number' && a.unitBytes > 0
			)
	);

	/** 표 행 — 방향 머리행 + cont./discont. 두 하위행. */
	const dirTableRows = $derived(
		dirRows.flatMap((r) => {
			const bytes = (r.cont?.totalBytes ?? 0) + (r.disc?.totalBytes ?? 0);
			return [
				{
					k: r.dir,
					label: r.dir,
					reqs: r.total.toLocaleString(),
					ratio: '100%',
					total: fmtBytes(bytes),
					avg: r.total > 0 ? fmtBytes(bytes / r.total) : '-'
				},
				...([
					['cont.', r.cont],
					['discont.', r.disc]
				] as const).map(([lb, e]) => ({
					k: `${r.dir}-${lb}`,
					label: `　${lb}`,
					reqs: (e?.count ?? 0).toLocaleString(),
					ratio: e ? `${e.ratioWithinDirection.toFixed(1)}%` : '-',
					total: fmtBytes(e?.totalBytes ?? 0),
					avg: e ? fmtBytes(e.avgRequestBytes) : '-'
				}))
			];
		})
	);
	// 링크 점유 합계(ms). "관측 기간 중 몇 %를 프로토콜 오버헤드로 썼나" 가 idle 분석의 핵심.
	const mgmtTotalMs = $derived(mgmtStats.reduce((a, m) => a + m.totalTimeMs, 0));
	const mgmtRatio = $derived(
		stats.durationSeconds > 0 ? (mgmtTotalMs / (stats.durationSeconds * 1000)) * 100 : 0
	);
	let activeMgmtTab = $state('overview');

	let activeHistTab = $state('');
	let activeSizeTab = $state('');
	$effect(() => {
		// 1단(cmd) 먼저 확정 → 그 cmd 안에서 2단(지연 종류) 확정.
		// cmd 를 바꿨을 때 이전 지연 종류가 없으면 첫 번째로 옮긴다.
		if (histCmds.length > 0 && !histCmds.includes(activeHistCmd)) activeHistCmd = histCmds[0];
		if (histTypesForCmd.length > 0 && !histTypesForCmd.includes(activeHistTab)) {
			activeHistTab = histTypesForCmd[0];
		}
		// 그룹을 바꾸면 그 그룹의 첫 cmd 로 옮긴다 — 이전 cmd 가 없는 그룹이면 빈 화면이 된다.
		if (visibleSizeCmds.length > 0 && !visibleSizeCmds.includes(activeSizeTab)) {
			activeSizeTab = visibleSizeCmds[0];
		}
		if (sizeGroups.length > 0 && !sizeGroups.includes(activeSizeGroup)) activeSizeGroup = 'all';
	});
</script>

<div class="space-y-3">
	<!-- Overview 4 cards -->
	<div class="grid grid-cols-4 gap-2">
		<div class="border rounded-md p-2">
			<div class="text-[9px] text-muted-foreground">Total Events</div>
			<div class="text-sm font-semibold">{stats.totalEvents.toLocaleString()}</div>
			<div class="text-[9px] text-muted-foreground">Send: {stats.sendCount.toLocaleString()}</div>
		</div>
		<div class="border rounded-md p-2">
			<div class="text-[9px] text-muted-foreground">Duration</div>
			<div class="text-sm font-semibold">{fmtDuration(stats.durationSeconds)}</div>
		</div>
		<div class="border rounded-md p-2">
			<div class="text-[9px] text-muted-foreground">Continuous</div>
			{#if continuousUnavailable}
				<div class="text-sm font-semibold text-muted-foreground" title="구버전 parquet 은 연속 판정이 되지 않아 값이 0 으로 저장돼 있습니다. 재파싱하면 정상 집계됩니다.">
					—
				</div>
				<div class="text-[9px] text-amber-600 dark:text-amber-500">재파싱 필요</div>
			{:else}
				<div class="text-sm font-semibold">{stats.continuousRatio.toFixed(1)}%</div>
				<div class="text-[9px] text-muted-foreground">
					{stats.continuousCount.toLocaleString()}
				</div>
			{/if}
		</div>
		<div class="border rounded-md p-2">
			<div class="text-[9px] text-muted-foreground">Aligned</div>
			<div class="text-sm font-semibold">{stats.alignedRatio.toFixed(1)}%</div>
			<div class="text-[9px] text-muted-foreground">{stats.alignedCount.toLocaleString()}</div>
		</div>
	</div>

	<!-- I/O Amount -->
	<div class="grid grid-cols-3 gap-2">
		<div class="border rounded-md p-2">
			<div class="text-[9px] text-muted-foreground">Read Total</div>
			<div class="text-sm font-semibold">{fmtBytes(stats.readTotalBytes)}</div>
		</div>
		<div class="border rounded-md p-2">
			<div class="text-[9px] text-muted-foreground">Write Total</div>
			<div class="text-sm font-semibold">{fmtBytes(stats.writeTotalBytes)}</div>
		</div>
		<div class="border rounded-md p-2">
			<div class="text-[9px] text-muted-foreground">Discard Total</div>
			<div class="text-sm font-semibold">{fmtBytes(stats.discardTotalBytes)}</div>
		</div>
	</div>

	<!-- Address Range (all/read/write) -->
	{#if addrRows.length > 0}
		<div>
			<div class="flex items-baseline gap-2 mb-1">
				<h3 class="text-xs font-semibold">Address Range</h3>
				<span class="text-[9px] text-muted-foreground">
					by send order · mgmt rows excluded
				</span>
			</div>
			<DataTable
				data={addrRows.map((a) => ({
					k: a.direction,
					label: a.direction,
					min: a.minAddr.toLocaleString(),
					max: a.maxAddr.toLocaleString(),
					span: a.span.toLocaleString(),
					size: fmtBytes(a.span * a.unitBytes),
					reqs: a.count.toLocaleString()
				}))}
				columns={[
					{ accessorKey: 'label', header: 'Direction' },
					{ accessorKey: 'min', header: 'Min' },
					{ accessorKey: 'max', header: 'Max' },
					{ accessorKey: 'span', header: 'Span' },
					{ accessorKey: 'size', header: 'Range size' },
					{ accessorKey: 'reqs', header: 'Reqs' }
				]}
				showPagination={false}
				enableCellCopy={true}
				getRowId={(r: any) => `ar-${r.k}`}
			/>
			<div class="mt-1 text-[9px] text-muted-foreground">
				1 addr unit = {addrRows[0].unitBytes.toLocaleString()} B.
				“all” reqs can exceed read+write — discard/flush have an address but no direction.
			</div>
		</div>
	{/if}

	<!-- Address Continuity (read/write × contiguous) -->
	{#if dirRows.length > 0}
		<div>
			<div class="flex items-baseline gap-2 mb-1">
				<h3 class="text-xs font-semibold">Address Continuity</h3>
				<span class="text-[9px] text-muted-foreground">
					by send order · read chains with prev read, write with prev write
				</span>
			</div>

			<div class="grid grid-cols-2 gap-2">
				{#each dirRows as r (r.dir)}
					{@const pct = r.cont?.ratioWithinDirection ?? 0}
					{@const isRead = r.dir === 'read'}
					<div class="flex items-center gap-3 border rounded-md p-2">
						<svg width="72" height="72" viewBox="0 0 72 72" class="shrink-0"
							role="img" aria-label="{r.dir} contiguous {pct.toFixed(1)}%">
							<circle cx="36" cy="36" r={DONUT_R} fill="none" stroke-width="11"
								class={isRead ? 'stroke-blue-200 dark:stroke-blue-900' : 'stroke-orange-200 dark:stroke-orange-900'} />
							<circle cx="36" cy="36" r={DONUT_R} fill="none" stroke-width="11"
								stroke-dasharray="{(DONUT_C * pct) / 100} {DONUT_C}"
								transform="rotate(-90 36 36)"
								class={isRead ? 'stroke-blue-500' : 'stroke-orange-500'} />
							<text x="36" y="34" text-anchor="middle" class="text-[11px] font-bold tabular-nums"
								fill="currentColor">{pct.toFixed(1)}%</text>
							<text x="36" y="44" text-anchor="middle" class="text-[6px] fill-muted-foreground"
								style="letter-spacing:.06em">CONT.</text>
						</svg>
						<div class="min-w-0">
							<div class="flex items-center gap-1.5 text-[11px] font-semibold capitalize">
								<span class="size-2 rounded-sm {isRead ? 'bg-blue-500' : 'bg-orange-500'}"></span>
								{r.dir}
							</div>
							<div class="mt-1 grid grid-cols-[9px_1fr_auto] items-center gap-x-1.5 gap-y-0.5 text-[10px]">
								<span class="size-2 rounded-sm {isRead ? 'bg-blue-500' : 'bg-orange-500'}"></span>
								<span class="text-muted-foreground">cont.</span>
								<span class="font-semibold tabular-nums">{(r.cont?.count ?? 0).toLocaleString()}</span>
								<span class="size-2 rounded-sm {isRead ? 'bg-blue-200 dark:bg-blue-900' : 'bg-orange-200 dark:bg-orange-900'}"></span>
								<span class="text-muted-foreground">discont.</span>
								<span class="font-semibold tabular-nums">{(r.disc?.count ?? 0).toLocaleString()}</span>
							</div>
							<div class="mt-1 border-t pt-1 text-[9px] text-muted-foreground tabular-nums">
								{r.total.toLocaleString()} reqs · {fmtBytes(
									(r.cont?.totalBytes ?? 0) + (r.disc?.totalBytes ?? 0)
								)}
							</div>
						</div>
					</div>
				{/each}
			</div>

			<div class="mt-1.5">
				<DataTable
					data={dirTableRows}
					columns={[
						{ accessorKey: 'label', header: 'Direction' },
						{ accessorKey: 'reqs', header: 'Reqs' },
						{ accessorKey: 'ratio', header: 'Ratio' },
						{ accessorKey: 'total', header: 'Total' },
						{ accessorKey: 'avg', header: 'Avg size' }
					]}
					showPagination={false}
					enableCellCopy={true}
					getRowId={(r: any) => `dc-${r.k}`}
				/>
			</div>

			{#if stats.classifiedSendCount != null && stats.classifiedSendCount < stats.sendCount}
				<div class="mt-1 text-[9px] text-muted-foreground">
					Classified {stats.classifiedSendCount.toLocaleString()} of
					{stats.sendCount.toLocaleString()} sends —
					{(stats.sendCount - stats.classifiedSendCount).toLocaleString()} reqs are
					neither read nor write (discard/flush/control-plane)
				</div>
			{/if}
		</div>
	{/if}

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
					{@const s = stats[lt as keyof StatsResponse] as StatsLatency}
					{@const latRow = {
						min: fmtLat(s.min),
						max: fmtLat(s.max),
						avg: fmtLat(s.avg),
						stddev: fmtLat(s.stddev),
						median: fmtLat(s.median),
						p99: fmtLat(s.p99),
						p999: fmtLat(s.p999),
						p9999: fmtLat(s.p9999),
						p99999: fmtLat(s.p99999),
						p999999: fmtLat(s.p999999),
						count: fmtCount(s.count)
					}}
					<DataTable
						data={[latRow]}
						columns={[
							...latencyFields.map((f) => ({ accessorKey: f.key as string, header: f.label })),
							{ accessorKey: 'count', header: 'Count' }
						]}
						showPagination={false}
						enableCellCopy={true}
						getRowId={() => `lat-${lt}`}
					/>
				</Tabs.Content>
			{/each}
		</Tabs.Root>
	</div>

	<!-- CMD Statistics -->
	{#if stats.cmdStats.length > 0}
		<div>
			<h3 class="text-xs font-semibold mb-1">CMD Statistics</h3>
			<Tabs.Root bind:value={activeCmdTab}>
				<Tabs.List class="flex gap-0.5 mb-1">
					<Tabs.Trigger value="overview" class="text-[10px] px-2 py-0.5">Overview</Tabs.Trigger>
					<Tabs.Trigger value="dtoc" class="text-[10px] px-2 py-0.5">DtoC</Tabs.Trigger>
					<Tabs.Trigger value="ctod" class="text-[10px] px-2 py-0.5">CtoD</Tabs.Trigger>
					<Tabs.Trigger value="ctoc" class="text-[10px] px-2 py-0.5">CtoC</Tabs.Trigger>
					<Tabs.Trigger value="qd" class="text-[10px] px-2 py-0.5">QD</Tabs.Trigger>
				</Tabs.List>
				<Tabs.Content value="overview">
					{@const rows = stats.cmdStats.map((c: StatsCmd) => ({
						cmd: c.cmd,
						count: c.count.toLocaleString(),
						send: c.sendCount.toLocaleString(),
						ratio: c.ratio.toFixed(1) + '%',
						totalSize: fmtBytes(c.totalSizeBytes),
						continuous: continuousUnavailable
							? '—'
							: `${c.continuousCount.toLocaleString()} (${c.continuousRatio.toFixed(1)}%)`,
						dtocAvg: fmtLat(c.dtoc.avg),
						ctodAvg: fmtLat(c.ctod.avg),
						ctocAvg: fmtLat(c.ctoc.avg),
						qdAvg: fmtLat(c.qd.avg)
					}))}
					<DataTable
						data={rows}
						columns={[
							{ accessorKey: 'cmd', header: 'CMD' },
							{ accessorKey: 'count', header: 'Total' },
							{ accessorKey: 'send', header: 'Send' },
							{ accessorKey: 'ratio', header: 'Ratio' },
							{ accessorKey: 'totalSize', header: 'Size' },
							{ accessorKey: 'continuous', header: 'Continuous' },
							{ accessorKey: 'dtocAvg', header: 'DtoC Avg' },
							{ accessorKey: 'ctodAvg', header: 'CtoD Avg' },
							{ accessorKey: 'ctocAvg', header: 'CtoC Avg' },
							{ accessorKey: 'qdAvg', header: 'QD Avg' }
						]}
						filterColumn="cmd"
						filterPlaceholder="CMD 검색..."
						enableCellCopy={true}
						getRowId={(r: any) => `cmd-overview-${r.cmd}`}
					/>
				</Tabs.Content>
				{#each ['dtoc', 'ctod', 'ctoc', 'qd'] as lt}
					<Tabs.Content value={lt}>
						{@const rows = stats.cmdStats.map((c: StatsCmd) => {
							const s = c[lt as keyof StatsCmd] as StatsLatency;
							return {
								cmd: c.cmd,
								count: c.count.toLocaleString(),
								ratio: c.ratio.toFixed(1) + '%',
								min: fmtLat(s.min), max: fmtLat(s.max), avg: fmtLat(s.avg),
								stddev: fmtLat(s.stddev), median: fmtLat(s.median),
								p99: fmtLat(s.p99), p999: fmtLat(s.p999), p9999: fmtLat(s.p9999)
							};
						})}
						<DataTable
							data={rows}
							columns={[
								{ accessorKey: 'cmd', header: 'CMD' },
								{ accessorKey: 'count', header: 'Count' },
								{ accessorKey: 'ratio', header: 'Ratio' },
								{ accessorKey: 'min', header: 'Min' },
								{ accessorKey: 'max', header: 'Max' },
								{ accessorKey: 'avg', header: 'Avg' },
								{ accessorKey: 'stddev', header: 'StdDev' },
								{ accessorKey: 'median', header: 'Median' },
								{ accessorKey: 'p99', header: 'P99' },
								{ accessorKey: 'p999', header: 'P99.9' },
								{ accessorKey: 'p9999', header: 'P99.99' }
							]}
							filterColumn="cmd"
							filterPlaceholder="CMD 검색..."
							enableCellCopy={true}
							getRowId={(r: any) => `cmd-${lt}-${r.cmd}`}
						/>
					</Tabs.Content>
				{/each}
			</Tabs.Root>
		</div>
	{/if}

	<!--
		UFS Management Events (Query/TM UPIU, UIC) — fsio_ufs 전용.

		위 Overview/CMD Statistics 는 전부 mgmt 를 **제외한** 데이터 IO 기준이다.
		idle 구간에서는 데이터 IO 가 거의 없고 mgmt(hibern8 enter/exit 쌍, BKOPS 폴링)가
		행의 대부분이라, 이 섹션이 그 구간의 사실상 유일한 산출물이 된다.
	-->
	{#if mgmtStats.length > 0}
		<div>
			<div class="flex items-baseline gap-2 mb-1">
				<h3 class="text-xs font-semibold">UFS Management Events</h3>
				<span class="text-[10px] text-muted-foreground">
					링크 점유 {fmtLat(mgmtTotalMs)}ms
					{#if stats.durationSeconds > 0}
						· 관측 기간의 {mgmtRatio.toFixed(1)}%
					{/if}
				</span>
			</div>
			<Tabs.Root bind:value={activeMgmtTab}>
				<Tabs.List class="flex gap-0.5 mb-1">
					<Tabs.Trigger value="overview" class="text-[10px] px-2 py-0.5">Overview</Tabs.Trigger>
					<Tabs.Trigger value="dtoc" class="text-[10px] px-2 py-0.5">DtoC</Tabs.Trigger>
				</Tabs.List>
				<Tabs.Content value="overview">
					{@const rows = mgmtStats.map((m: StatsMgmt) => ({
						name: m.name,
						kind: m.kind,
						count: m.count.toLocaleString(),
						paired: m.pairedCount.toLocaleString(),
						totalTime: fmtLat(m.totalTimeMs),
						share: mgmtTotalMs > 0 ? ((m.totalTimeMs / mgmtTotalMs) * 100).toFixed(1) + '%' : '-',
						avg: fmtLat(m.dtoc.avg),
						max: fmtLat(m.dtoc.max)
					}))}
					<DataTable
						data={rows}
						columns={[
							{ accessorKey: 'name', header: 'Event' },
							{ accessorKey: 'kind', header: 'Kind' },
							{ accessorKey: 'count', header: 'Count' },
							{ accessorKey: 'paired', header: 'Paired' },
							{ accessorKey: 'totalTime', header: 'Total (ms)' },
							{ accessorKey: 'share', header: 'Share' },
							{ accessorKey: 'avg', header: 'Avg' },
							{ accessorKey: 'max', header: 'Max' }
						]}
						filterColumn="name"
						filterPlaceholder="Event 검색..."
						enableCellCopy={true}
						getRowId={(r: any) => `mgmt-overview-${r.name}`}
					/>
				</Tabs.Content>
				<Tabs.Content value="dtoc">
					{@const rows = mgmtStats.map((m: StatsMgmt) => ({
						name: m.name,
						paired: m.pairedCount.toLocaleString(),
						min: fmtLat(m.dtoc.min),
						max: fmtLat(m.dtoc.max),
						avg: fmtLat(m.dtoc.avg),
						stddev: fmtLat(m.dtoc.stddev),
						median: fmtLat(m.dtoc.median),
						p99: fmtLat(m.dtoc.p99),
						p999: fmtLat(m.dtoc.p999)
					}))}
					<DataTable
						data={rows}
						columns={[
							{ accessorKey: 'name', header: 'Event' },
							{ accessorKey: 'paired', header: 'Paired' },
							{ accessorKey: 'min', header: 'Min' },
							{ accessorKey: 'max', header: 'Max' },
							{ accessorKey: 'avg', header: 'Avg' },
							{ accessorKey: 'stddev', header: 'StdDev' },
							{ accessorKey: 'median', header: 'Median' },
							{ accessorKey: 'p99', header: 'P99' },
							{ accessorKey: 'p999', header: 'P99.9' }
						]}
						filterColumn="name"
						filterPlaceholder="Event 검색..."
						enableCellCopy={true}
						getRowId={(r: any) => `mgmt-dtoc-${r.name}`}
					/>
				</Tabs.Content>
			</Tabs.Root>
		</div>
	{/if}

	<!-- Latency Histogram -->
	{#if stats.latencyHistograms.length > 0 && activeHistTab}
		<div>
			<h3 class="text-xs font-semibold mb-1">Latency Histogram</h3>
			<!-- 1단: CMD -->
			<div class="text-[10px] text-muted-foreground mb-0.5">CMD</div>
			<div class="flex gap-0.5 mb-1 flex-wrap">
				{#each histCmds as hc}
					<button
						class="text-[10px] px-2 py-0.5 rounded border transition-colors
							{activeHistCmd === hc
								? 'bg-primary text-primary-foreground border-primary'
								: 'hover:bg-muted'}"
						onclick={() => (activeHistCmd = hc)}
					>
						{hc}
					</button>
				{/each}
			</div>
			<!-- 2단: 지연 종류 -->
			<Tabs.Root bind:value={activeHistTab}>
				<Tabs.List class="flex gap-0.5 mb-1">
					{#each histTypesForCmd as lt}
						<Tabs.Trigger value={lt} class="text-[10px] px-2 py-0.5 uppercase">{lt}</Tabs.Trigger>
					{/each}
				</Tabs.List>
				{#each histTypesForCmd as lt}
					<Tabs.Content value={lt}>
						{@const rows = stats.latencyHistograms
							.filter((h: StatsHistogram) => h.cmd === activeHistCmd && (h.latencyType as string).toLowerCase() === lt)
							.flatMap((h: StatsHistogram) =>
								h.buckets.map((b) => ({
									range: b.rangeEndMs > 0 ? `${b.rangeStartMs} ~ ${b.rangeEndMs}` : `${b.rangeStartMs}+`,
									count: b.count.toLocaleString()
								}))
							)}
						<DataTable
							data={rows}
							columns={[
								{ accessorKey: 'range', header: 'Range (ms)' },
								{ accessorKey: 'count', header: 'Count' }
							]}
							enableCellCopy={true}
							getRowId={(r: any) => `hist-${activeHistCmd}-${lt}-${r.range}`}
						/>
					</Tabs.Content>
				{/each}
			</Tabs.Root>
		</div>
	{/if}

	<!-- CMD + Size Count -->
	{#if stats.cmdSizeCounts.length > 0 && activeSizeTab}
		<div>
			<h3 class="text-xs font-semibold mb-1">CMD + Size Count</h3>
			<!-- 성질별 그룹 탭 — cmd 가 수십 개로 늘어나면 아래 CMD 탭만으로는 찾기 어렵다.
			     "전체" 를 남겨 기존처럼 한 번에 보는 것도 가능하게 둔다. -->
			{#if sizeGroups.length > 2}
				<div class="flex gap-0.5 mb-1 flex-wrap">
					{#each sizeGroups as g}
						{@const n = g === 'all'
							? sizeCmds.length
							: sizeCmds.filter((c) => getCmdGroup(c) === g).length}
						<button
							class="text-[10px] px-2 py-0.5 rounded border transition-colors
								{activeSizeGroup === g
									? 'bg-primary text-primary-foreground border-primary'
									: 'hover:bg-muted'}"
							onclick={() => (activeSizeGroup = g)}
						>
							{g === 'all' ? '전체' : SIZE_GROUP_LABELS[g]} ({n})
						</button>
					{/each}
				</div>
			{/if}
			<Tabs.Root bind:value={activeSizeTab}>
				<Tabs.List class="flex gap-0.5 mb-1 flex-wrap">
					{#each visibleSizeCmds as cmd}
						<Tabs.Trigger value={cmd} class="text-[10px] px-2 py-0.5">{cmd}</Tabs.Trigger>
					{/each}
				</Tabs.List>
				{#each visibleSizeCmds as cmd}
					<Tabs.Content value={cmd}>
						{@const rows = stats.cmdSizeCounts
							.filter((c) => c.cmd === cmd)
							.map((c) => ({ size: String(c.size), count: c.count.toLocaleString() }))}
						<DataTable
							data={rows}
							columns={[
								{ accessorKey: 'size', header: 'Size' },
								{ accessorKey: 'count', header: 'Count' }
							]}
							enableCellCopy={true}
							getRowId={(r: any) => `size-${cmd}-${r.size}`}
						/>
					</Tabs.Content>
				{/each}
			</Tabs.Root>
		</div>
	{/if}
</div>
