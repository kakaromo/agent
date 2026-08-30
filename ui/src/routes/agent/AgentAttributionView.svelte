<script lang="ts">
	/**
	 * I/O 귀속(Attribution) 뷰 — "이 IO 를 누가/무엇이 만들었나".
	 *
	 * 4개 패널(Flow / Top Processes / Top Files / Syscall)을 **요청 1회**로 채운다
	 * (서버가 base 를 1회만 스캔하도록 dims 를 한꺼번에 보냄).
	 *
	 * 시각화 형태: 순위가 있는 크기 비교 → 가로 막대 + sequential(단일 hue).
	 * 단 Flow 패널만 categorical — GC/Journal 같은 클래스가 그 자체로 진단 의미를 가져
	 * 색이 정체성을 나타내야 하고, 패널·세션 간 **같은 클래스는 항상 같은 색**이어야 한다.
	 * (categorical 팔레트는 dataviz validator 로 light/dark 양쪽 검증 완료.)
	 */
	import { getTraceAttribution, type AttributionResult, type AttributionGroup,
		type AttributionEntry, type AttributionDim, type TraceFilter } from '$lib/api/agent.js';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';

	interface Props {
		serverId: number;
		jobIds: string[];
		traceType: string | null;
		filter: TraceFilter;
		/**
		 * 행 클릭 → 해당 값으로 전체 화면 필터. Phase 1 서버사이드 필터 위에서 동작.
		 * `additive` 면 기존 선택에 더한다 (Ctrl/⌘/Shift + 클릭) — 파일 여러 개를
		 * 한 번에 보고 싶을 때. 일반 클릭은 단일 선택(토글).
		 */
		onDrillDown: (dim: AttributionDim, key: string, additive: boolean) => void;
	}
	let { serverId, jobIds, traceType, filter, onDrillDown }: Props = $props();

	const isFsio = $derived(traceType === 'fsio_ufs' || traceType === 'fsio_block');

	type SortBy = 'latency' | 'count' | 'bytes';
	let sortBy = $state<SortBy>('latency');
	let topN = $state(10);

	let loading = $state(false);
	let error = $state<string | null>(null);
	let data = $state<AttributionResult | null>(null);

	// LU/디바이스 축은 trace_type 에 따라 하나만 의미가 있다 (fsio_ufs=lun, fsio_block=device).
	// 없는 쪽을 요청하면 서버가 unsupportedDims 로 알려주므로 그냥 둘 다 넣지 않고 골라 넣는다.
	const PANELS = $derived<{ dim: AttributionDim; title: string; hint: string }[]>([
		{ dim: 'flow', title: 'I/O Flow', hint: '어떤 경로로 만들어졌나 (io_flags 해석)' },
		{ dim: 'comm', title: 'Top Processes', hint: '어떤 프로세스가 만들었나' },
		{ dim: 'file', title: 'Top Files', hint: '어떤 파일에 대한 IO 인가' },
		{ dim: 'syscall', title: 'Syscall', hint: '어떤 syscall 에서 시작됐나' },
		traceType === 'fsio_ufs'
			? { dim: 'lun' as AttributionDim, title: 'LU (Logical Unit)', hint: 'LU 마다 LBA 주소공간이 독립' }
			: { dim: 'device' as AttributionDim, title: 'Block Device', hint: '섹터 주소공간이 디바이스마다 독립' }
	]);

	// Flow 클래스 → 고정 색. 값이 아니라 **의미**에 색을 묶는다 (순위가 바뀌어도 색 유지).
	// dataviz categorical 팔레트 slot 순서. light/dark 각각 validator PASS.
	const FLOW_COLORS: Record<string, { light: string; dark: string }> = {
		GC:                   { light: '#eb6834', dark: '#d95926' }, // orange — 백그라운드 회수
		Journal:              { light: '#eda100', dark: '#c98500' }, // yellow — 메타데이터 보장
		Checkpoint:           { light: '#e87ba4', dark: '#d55181' }, // magenta
		'Writeback(kworker)': { light: '#1baf7a', dark: '#199e70' }, // aqua — 지연 반영
		fsync:                { light: '#008300', dark: '#008300' }, // green — 앱 강제 flush
		DirectIO:             { light: '#2a78d6', dark: '#3987e5' }, // blue — 앱 직접 경로
		'Buffered(app)':      { light: '#2a78d6', dark: '#3987e5' },
		'mmap-writeback':     { light: '#1baf7a', dark: '#199e70' },
		Metadata:             { light: '#eda100', dark: '#c98500' },
		Data:                 { light: '#2a78d6', dark: '#3987e5' },
		Other:                { light: '#8a8985', dark: '#8a8985' }
	};

	let isDark = $state(false);
	$effect(() => {
		const el = document.documentElement;
		const read = () => {
			const t = el.getAttribute('data-theme');
			isDark = t === 'dark'
				|| (t !== 'light' && window.matchMedia?.('(prefers-color-scheme: dark)').matches);
		};
		read();
		const mo = new MutationObserver(read);
		mo.observe(el, { attributes: true, attributeFilter: ['data-theme'] });
		return () => mo.disconnect();
	});

	function flowColor(key: string): string {
		const c = FLOW_COLORS[key];
		if (!c) return isDark ? '#8a8985' : '#8a8985';
		return isDark ? c.dark : c.light;
	}
	/** sequential(단일 hue) — 순위 막대용. 진할수록 큼. */
	const SEQ = ['#256abf', '#2a78d6', '#3987e5', '#5598e7', '#6da7ec', '#86b6ef', '#9ec5f4', '#b7d3f6'];
	const SEQ_DARK = ['#86b6ef', '#6da7ec', '#5598e7', '#3987e5', '#2a78d6', '#256abf', '#1c5cab', '#184f95'];
	function seqColor(i: number): string {
		const ramp = isDark ? SEQ_DARK : SEQ;
		return ramp[Math.min(i, ramp.length - 1)];
	}

	async function load() {
		if (jobIds.length === 0 || !isFsio) return;
		loading = true;
		error = null;
		try {
			data = await getTraceAttribution(serverId, {
				jobIds,
				dims: PANELS.map((p) => p.dim),
				topN,
				sortBy,
				filter: Object.keys(filter).length > 0 ? filter : undefined
			});
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
			data = null;
		} finally {
			loading = false;
		}
	}

	// 잡 / 정렬 / topN / 필터가 바뀌면 재조회.
	$effect(() => {
		void jobIds;
		void sortBy;
		void topN;
		void JSON.stringify(filter);
		load();
	});

	function groupOf(dim: AttributionDim): AttributionGroup | null {
		return data?.groups.find((g) => g.dim === dim) ?? null;
	}

	function metric(e: AttributionEntry): number {
		if (sortBy === 'count') return e.count;
		if (sortBy === 'bytes') return e.totalBytes;
		return e.dtocSumMs;
	}
	/** 정렬 기준 이름 — "막대 길이 = {…}" 문장에 그대로 들어간다. */
	function metricLabel(): string {
		if (sortBy === 'count') return 'I/O 횟수';
		if (sortBy === 'bytes') return 'Read/Write 양';
		return '걸린 시간';
	}
	function fmtMetric(e: AttributionEntry): string {
		if (sortBy === 'count') return e.count.toLocaleString();
		if (sortBy === 'bytes') return fmtBytes(e.totalBytes);
		return `${e.dtocSumMs.toFixed(1)} ms`;
	}

	function fmtBytes(b: number): string {
		if (b < 1024) return `${b} B`;
		if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
		if (b < 1024 * 1024 * 1024) return `${(b / 1024 / 1024).toFixed(1)} MB`;
		return `${(b / 1024 / 1024 / 1024).toFixed(2)} GB`;
	}
	/**
	 * (other) 롤업 행은 percentile 이 없다 — 0 으로 폴백하면 "빠름" 으로 오독되므로 "—".
	 * 서버가 값 없는 필드를 JSON 에서 아예 빼므로 undefined 로 온다 (null 도 함께 받는다).
	 */
	function fmtLat(v: number | null | undefined): string {
		if (v == null || !isFinite(v)) return '—';
		if (v < 1) return v.toFixed(3);
		return v.toLocaleString(undefined, { maximumFractionDigits: 2 });
	}
</script>

{#if !isFsio}
	<div class="text-center text-sm text-muted-foreground p-8">
		I/O 귀속 분석은 fsiotrace(bpftrace) 결과에서만 제공됩니다.
		<div class="text-xs mt-1 opacity-70">
			현재 타입: {traceType ?? '-'} · ftrace 기반 로그에는 프로세스/파일 정보가 없습니다.
		</div>
	</div>
{:else if loading && !data}
	<div class="flex items-center justify-center gap-2 p-8 text-sm text-muted-foreground">
		<LoaderIcon class="size-4 animate-spin" /> 귀속 집계 중…
	</div>
{:else if error}
	<div class="p-4">
		<div class="border border-destructive/40 bg-destructive/5 rounded p-3 text-sm">
			<div class="font-medium mb-1">귀속 집계 실패</div>
			<div class="text-xs text-muted-foreground break-all">{error}</div>
		</div>
	</div>
{:else if data}
	<div class="space-y-3">
		<!-- 컨트롤 — 필터 한 줄, 차트 위 (interaction.md) -->
		<div class="flex items-center gap-3 flex-wrap text-xs">
			<div class="flex items-center gap-1">
				<span class="text-muted-foreground">정렬</span>
				{#each [['latency', '걸린 시간'], ['count', 'I/O 횟수'], ['bytes', 'Read/Write 양']] as [v, label] (v)}
					<button
						class="px-2 py-0.5 rounded border transition-colors {sortBy === v ? 'bg-primary text-primary-foreground border-primary' : 'hover:bg-muted'}"
						onclick={() => (sortBy = v as SortBy)}
					>{label}</button>
				{/each}
			</div>
			<div class="flex items-center gap-1">
				<span class="text-muted-foreground">상위</span>
				<select class="border rounded px-1 py-0.5 bg-background" bind:value={topN}>
					{#each [5, 10, 20, 50] as n (n)}<option value={n}>{n}</option>{/each}
				</select>
			</div>
			<div class="ml-auto text-muted-foreground">
				전체 {data.totalEvents.toLocaleString()} 이벤트
				{#if loading}<LoaderIcon class="inline size-3 animate-spin ml-1" />{/if}
			</div>
		</div>

		{#if data.unsupportedDims.length > 0}
			<div class="text-[11px] text-amber-600 dark:text-amber-500">
				⚠ 이 parquet 에 없는 축은 건너뜀: {data.unsupportedDims.join(', ')} · 재파싱하면 채워집니다
			</div>
		{/if}

		<div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
			{#each PANELS as panel (panel.dim)}
				{@const g = groupOf(panel.dim)}
				<div class="border rounded-md p-3">
					<div class="flex items-baseline gap-2 mb-2">
						<h3 class="text-xs font-semibold">{panel.title}</h3>
						<span class="text-[10px] text-muted-foreground">{panel.hint}</span>
					</div>

					{#if !g || g.entries.length === 0}
						<div class="text-xs text-muted-foreground py-4 text-center">데이터 없음</div>
					{:else}
						{@const max = Math.max(...g.entries.map(metric), 1)}
						<!-- 롤업 사실을 숨기지 않는다 — top-N 자체가 조용한 거짓말이 되지 않도록 -->
						{#if g.distinctKeys > g.entries.filter((e) => !e.isOther).length}
							<div class="text-[10px] text-muted-foreground mb-1.5">
								전체 {g.distinctKeys.toLocaleString()}개 중 상위
								{g.entries.filter((e) => !e.isOther).length}개 · 나머지는 (other)
							</div>
						{/if}

						<div class="space-y-1">
							{#each g.entries as e, i (e.key)}
								{@const w = (metric(e) / max) * 100}
								<button
									class="w-full text-left group"
									onclick={(ev) =>
										!e.isOther &&
										onDrillDown(panel.dim, e.key, ev.ctrlKey || ev.metaKey || ev.shiftKey)}
									disabled={e.isOther}
									title={e.isOther
										? '롤업 행 — 드릴다운 불가'
										: `${e.key} 로 필터 · Ctrl(⌘)/Shift+클릭 = 여러 개 선택`}
								>
									<div class="flex items-baseline gap-2 text-[11px]">
										<span class="truncate flex-1 {e.isOther ? 'text-muted-foreground italic' : 'group-hover:underline'}">
											{e.key}
										</span>
										<span class="tabular-nums text-muted-foreground shrink-0">{fmtMetric(e)}</span>
									</div>
									<!-- 막대: 2px 표면 간격, 4px 라운드 데이터 엔드 -->
									<div class="h-2 rounded-sm bg-muted/40 overflow-hidden mt-0.5">
										<div
											class="h-full rounded-sm transition-[width] duration-200"
											style="width: {Math.max(w, 1)}%; background-color: {e.isOther
												? (isDark ? '#575654' : '#c8c7c1')
												: panel.dim === 'flow' ? flowColor(e.key) : seqColor(i)}"
										></div>
									</div>
								</button>
							{/each}
						</div>

						<!-- 표 뷰 — 색만으로 정보를 전달하지 않기 위한 보조 (a11y) -->
						<details class="mt-2">
							<summary class="text-[10px] text-muted-foreground cursor-pointer hover:text-foreground">
								표로 보기
							</summary>
							<div class="overflow-x-auto mt-1">
								<table class="w-full text-[10px] tabular-nums">
									<thead class="text-muted-foreground">
										<tr class="border-b">
											<th class="text-left font-medium py-0.5 pr-2">이름</th>
											<th class="text-right font-medium py-0.5 px-1">I/O 횟수</th>
											<th class="text-right font-medium py-0.5 px-1">Ratio</th>
											<th class="text-right font-medium py-0.5 px-1">Read/Write 양</th>
											<th class="text-right font-medium py-0.5 px-1">걸린 시간</th>
											<th class="text-right font-medium py-0.5 px-1">DtoC p99</th>
											{#if panel.dim === 'comm'}
												<th class="text-right font-medium py-0.5 pl-1">파일 개수</th>
											{/if}
										</tr>
									</thead>
									<tbody>
										{#each g.entries as e (e.key)}
											<tr class="border-b border-muted/50">
												<td class="py-0.5 pr-2 truncate max-w-[10rem] {e.isOther ? 'italic text-muted-foreground' : ''}">{e.key}</td>
												<td class="text-right px-1">{e.count.toLocaleString()}</td>
												<td class="text-right px-1">{e.ratio.toFixed(1)}%</td>
												<td class="text-right px-1">{fmtBytes(e.totalBytes)}</td>
												<td class="text-right px-1">{e.dtocSumMs.toFixed(1)}</td>
												<td class="text-right px-1">{fmtLat(e.dtocP99Ms)}</td>
												{#if panel.dim === 'comm'}
													<td class="text-right pl-1">{e.distinctFiles ?? '—'}</td>
												{/if}
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
						</details>
					{/if}
				</div>
			{/each}
		</div>

		<div class="text-[10px] text-muted-foreground">
			막대 길이 = {metricLabel()} · 행 클릭 → chart · Statistics · Raw Data 가 함께 필터됩니다
			· <b>Ctrl(⌘)/Shift + 클릭</b> 으로 여러 개 선택
		</div>
	</div>
{/if}
