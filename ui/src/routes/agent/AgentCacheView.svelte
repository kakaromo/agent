<script lang="ts">
	/**
	 * Page Cache 뷰 — VFS buffered read 가 캐시를 맞췄나 (fsio_read 전용).
	 *
	 * portal `frontend/src/routes/trace/TraceCacheView.svelte` 의 이식본.
	 * ⚠ 복사본이 아니라 **포크**다 — portal 은 parquetId 로, 여기는 (serverId, jobIds)
	 *   로 조회한다. check-trace-sync.sh 대상이 아니므로 portal 이 바뀌면 손으로 맞춰야 한다.
	 *
	 * 형제 parquet 이 없으면 서버가 totalRequests=0 을 주고, 탭 자체가 안 뜬다
	 * (AgentTraceResultSheet 의 hasCacheTab).
	 *
	 * ⚠ CACHE_HIT_INFERRED 는 하드웨어 cache hit 이벤트가 아니다. "read 가 도는 동안 FS
	 *   page-fill 훅이 한 번도 안 불렸다" 는 **음성 증거** 추론이다. 그래서 훅이 안 붙은
	 *   파일시스템의 read 는 hit 이 아니라 UNKNOWN 으로 떨어진다(coverage).
	 *
	 * ⚠ 이 화면은 byte 단위 hit ratio 를 만들지 않는다. 일부 folio 만 uptodate 였던 부분
	 *   hit 도 fill>0 이라 miss 로 분류되는 **request 단위** 판정이다.
	 *
	 * 판정 정확도는 mincore(2) 정답지로 측정했다 — precision 99.1% / recall 92.9%.
	 * recall 이 100 이 아니라서 hit 비율은 **하한**이다(라벨에 표시).
	 *
	 * 용어는 docs/trace-glossary.md §3 을 따른다.
	 */
	import { getFsioReadStats, type FsioReadStatsResult, type TraceFilter } from '$lib/api/agent.js';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';

	interface Props {
		serverId: number;
		jobIds: string[];
		filter: TraceFilter;
	}
	let { serverId, jobIds, filter }: Props = $props();

	let loading = $state(false);
	let error = $state<string | null>(null);
	let data = $state<FsioReadStatsResult | null>(null);
	let topN = $state(10);

	/** 표시 순서 고정 — 서버 응답 순서(건수 desc)에 맡기면 화면이 조회마다 흔들린다. */
	const CLASS_ORDER = [
		'CACHE_HIT_INFERRED',
		'CACHE_MISS',
		'DIRECT_IO',
		'EOF',
		'ERROR',
		'UNKNOWN'
	];

	/** hit 비율 분모에 들어가는 class. 나머지는 "캐시를 맞췄나" 를 물을 대상이 아니다. */
	function inRatio(c: string): boolean {
		return c === 'CACHE_HIT_INFERRED' || c === 'CACHE_MISS';
	}

	function chipClass(c: string): string {
		if (c === 'CACHE_HIT_INFERRED') return 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400';
		if (c === 'CACHE_MISS') return 'bg-red-500/10 text-red-700 dark:text-red-400';
		if (c === 'UNKNOWN') return 'bg-amber-500/15 text-amber-700 dark:text-amber-500';
		return 'bg-muted text-muted-foreground';
	}

	/** TraceStatsView / TraceAttributionView 와 같은 구현. */
	function fmtBytes(b: number): string {
		if (b < 1024) return `${b} B`;
		if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
		if (b < 1024 * 1024 * 1024) return `${(b / 1024 / 1024).toFixed(1)} MB`;
		return `${(b / 1024 / 1024 / 1024).toFixed(2)} GB`;
	}

	/**
	 * ns → ms 표기. portal 의 fmtLat 정밀도 규칙 그대로.
	 * ⚠ 값이 없으면 '—' 로 — 0 으로 채우면 "0ns 였다" 로 오독된다.
	 *
	 * null 과 undefined 를 둘 다 받는다: portal 은 JSON null 로 오지만 standalone REST 는
	 * **키 자체를 안 보내서** undefined 다 (rest_convert.go 가 없는 optional 을 생략한다).
	 */
	function fmtMs(ns: number | null | undefined): string {
		if (ns == null || !isFinite(ns)) return '—';
		const v = ns / 1e6;
		if (v < 0.001) return v.toFixed(6);
		if (v < 1) return v.toFixed(3);
		return v.toLocaleString(undefined, { maximumFractionDigits: 2 });
	}

	function fmtPct(r: number | null | undefined): string {
		if (r == null || !isFinite(r)) return '—';
		return `${(r * 100).toFixed(1)}%`;
	}

	const total = $derived(data?.totalRequests ?? 0);

	const classRows = $derived(
		CLASS_ORDER.map((name) => {
			const found = data?.byClass.find((c) => c.cacheClass === name);
			return (
				found ?? {
					cacheClass: name,
					requests: 0,
					requestedBytes: 0,
					returnedBytes: 0,
					durationSamples: 0,
					durationAvgNs: undefined,
					durationP50Ns: undefined,
					durationP95Ns: undefined,
					durationP99Ns: undefined
				}
			);
		})
	);

	const hitRow = $derived(data?.byClass.find((c) => c.cacheClass === 'CACHE_HIT_INFERRED'));
	const missRow = $derived(data?.byClass.find((c) => c.cacheClass === 'CACHE_MISS'));

	/** miss 가 hit 보다 몇 배 느린가. 캐시가 벌어준 시간을 보여주는 핵심 지표. */
	const penalty = $derived.by(() => {
		const h = hitRow?.durationAvgNs;
		const m = missRow?.durationAvgNs;
		if (h == null || m == null || h <= 0) return null;
		return { ratio: m / h, savedNs: m - h };
	});

	const classifiable = $derived((hitRow?.requests ?? 0) + (missRow?.requests ?? 0));

	async function load() {
		if (!serverId || jobIds.length === 0) return;
		loading = true;
		error = null;
		try {
			data = await getFsioReadStats(serverId, { jobIds, filter, topN });
		} catch (e) {
			error = e instanceof Error ? e.message : String(e);
			data = null;
		} finally {
			loading = false;
		}
	}

	$effect(() => {
		// jobIds / filter / topN 이 바뀌면 다시 조회.
		void jobIds;
		void JSON.stringify(filter);
		void topN;
		void load();
	});
</script>

{#if loading && !data}
	<div class="flex items-center justify-center gap-2 p-8 text-sm text-muted-foreground">
		<LoaderIcon class="size-4 animate-spin" /> 캐시 통계 조회 중…
	</div>
{:else if error}
	<div class="border border-destructive/40 bg-destructive/5 rounded p-3 text-sm m-3">
		<div class="font-medium mb-1">캐시 통계 조회 실패</div>
		<div class="text-xs text-muted-foreground break-all">{error}</div>
	</div>
{:else if !data || total === 0}
	<div class="text-center text-sm text-muted-foreground p-8">데이터 없음</div>
{:else}
	<div class="flex flex-col gap-3 p-3">
		<!-- 요약 -->
		<div class="grid grid-cols-2 md:grid-cols-4 gap-2">
			<div class="border rounded-md p-2 border-l-2 border-l-emerald-500">
				<div class="text-[9px] text-muted-foreground font-semibold">
					Hit Ratio
					<span
						class="ml-0.5 px-1 rounded bg-muted text-[8px] align-top"
						title="precision 99.1% / recall 92.9% — 실제 hit 의 일부를 miss 로 세므로 하한이다"
						>하한</span
					>
				</div>
				<div class="text-sm font-semibold tabular-nums">{fmtPct(data.requestHitRatio)}</div>
				<div class="text-[9px] text-muted-foreground">
					{(hitRow?.requests ?? 0).toLocaleString()} / {classifiable.toLocaleString()} 판정 대상
				</div>
			</div>

			<div class="border rounded-md p-2 border-l-2 border-l-red-500">
				<div class="text-[9px] text-muted-foreground font-semibold">Miss Penalty</div>
				<div class="text-sm font-semibold tabular-nums">
					{penalty ? `${penalty.ratio.toFixed(1)}×` : '—'}
				</div>
				<div class="text-[9px] text-muted-foreground">
					{penalty ? `건당 +${fmtMs(penalty.savedNs)} ms` : '표본 부족'}
				</div>
			</div>

			<div class="border rounded-md p-2">
				<div class="text-[9px] text-muted-foreground font-semibold">Total Requests</div>
				<div class="text-sm font-semibold tabular-nums">{total.toLocaleString()}</div>
				<div class="text-[9px] text-muted-foreground">
					short read {data.shortReads.toLocaleString()}
				</div>
			</div>

			<div class="border rounded-md p-2 border-l-2 border-l-amber-500">
				<div class="text-[9px] text-muted-foreground font-semibold">Unknown</div>
				<div class="text-sm font-semibold tabular-nums">
					{(data.byClass.find((c) => c.cacheClass === 'UNKNOWN')?.requests ?? 0).toLocaleString()}
				</div>
				<div class="text-[9px] text-muted-foreground">{fmtPct(data.unknownRatio)}</div>
			</div>
		</div>

		<!-- 수집 품질 — 숨기지 않는다 -->
		{#if data.qualityWarnings.length > 0}
			<div class="flex flex-col gap-1">
				{#each data.qualityWarnings as w (w)}
					<div class="text-[11px] text-amber-600 dark:text-amber-500">⚠ {w}</div>
				{/each}
			</div>
		{/if}

		<!-- 분류 -->
		<div>
			<div class="flex items-baseline gap-2 mb-1">
				<h3 class="text-xs font-semibold">Cache Class</h3>
				<span class="text-[10px] text-muted-foreground">어떤 판정으로 갈렸나</span>
			</div>
			<div class="overflow-x-auto">
				<table class="w-full text-[11px]">
					<thead>
						<tr class="border-b text-muted-foreground">
							<th class="text-left font-medium py-0.5 pr-2">구분</th>
							<th class="text-right font-medium py-0.5 px-1">건수</th>
							<th class="text-right font-medium py-0.5 px-1">Ratio</th>
							<th class="text-right font-medium py-0.5 px-1">용량</th>
							<th class="text-right font-medium py-0.5 px-1">표본</th>
							<th class="text-right font-medium py-0.5 px-1">Avg (ms)</th>
							<th class="text-right font-medium py-0.5 px-1">Median (ms)</th>
							<th class="text-right font-medium py-0.5 pl-1">P99 (ms)</th>
						</tr>
					</thead>
					<tbody>
						{#each classRows as c (c.cacheClass)}
							<tr class="border-b border-border/50 {inRatio(c.cacheClass) ? 'border-l-2 border-l-primary' : ''}">
								<td class="py-0.5 pr-2">
									<span class="inline-flex items-center rounded-full px-1.5 py-px text-[10px] font-semibold {chipClass(c.cacheClass)}">
										{c.cacheClass}
									</span>
								</td>
								<td class="text-right px-1 tabular-nums">{c.requests.toLocaleString()}</td>
								<td class="text-right px-1 tabular-nums">
									{total > 0 ? `${((c.requests / total) * 100).toFixed(1)}%` : '—'}
								</td>
								<td class="text-right px-1 tabular-nums">
									{c.requests > 0 ? fmtBytes(c.returnedBytes) : '—'}
								</td>
								<td class="text-right px-1 tabular-nums">{c.durationSamples.toLocaleString()}</td>
								<td class="text-right px-1 tabular-nums">{fmtMs(c.durationAvgNs)}</td>
								<td class="text-right px-1 tabular-nums">{fmtMs(c.durationP50Ns)}</td>
								<td class="text-right pl-1 tabular-nums">{fmtMs(c.durationP99Ns)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<div class="text-[10px] text-muted-foreground mt-1.5 leading-relaxed">
				왼쪽 파란 선이 붙은 두 행만 Hit Ratio 분모에 들어갑니다 — 나머지는 캐시 적중 여부를
				물을 대상이 아닙니다. <b>표본</b>이 건수보다 적을 수 있습니다: 진입을 못 본 read 는
				소요 시간이 없어 평균에서 빠집니다 ({data.durationUnknown.toLocaleString()}건).
			</div>
		</div>

		<!-- 증거 -->
		<div class="text-[10px] text-muted-foreground">
			<b>증거 합계</b> — fill {data.fillUnits.toLocaleString()} · sync_ra
			{data.syncRaUnits.toLocaleString()} · async_ra {data.readaheadUnits.toLocaleString()}
			(readahead {data.readaheadRequests.toLocaleString()}건).
			⚠ 훅 발화 횟수지 page 수나 byte 수가 아닙니다.
		</div>

		<!-- 파일별 -->
		<div>
			<div class="flex items-baseline gap-2 mb-1">
				<h3 class="text-xs font-semibold">Top Files</h3>
				<span class="text-[10px] text-muted-foreground">read 에 시간을 가장 많이 쓴 파일이 어디인가</span>
				<div class="ml-auto flex items-center gap-1 text-[10px]">
					<span class="text-muted-foreground">상위</span>
					<select class="border rounded px-1 py-0.5 text-[10px] bg-background" bind:value={topN}>
						{#each [5, 10, 20, 50] as n (n)}
							<option value={n}>{n}</option>
						{/each}
					</select>
				</div>
			</div>
			{#if data.topFiles.length === 0}
				<div class="text-xs text-muted-foreground py-4 text-center">데이터 없음</div>
			{:else}
				<div class="overflow-x-auto">
					<table class="w-full text-[11px]">
						<thead>
							<tr class="border-b text-muted-foreground">
								<th class="text-left font-medium py-0.5 pr-2">파일</th>
								<th class="text-right font-medium py-0.5 px-1">건수</th>
								<th class="text-right font-medium py-0.5 px-1">miss</th>
								<th class="text-right font-medium py-0.5 px-1">hit율</th>
								<th class="text-right font-medium py-0.5 px-1">용량</th>
								<th class="text-right font-medium py-0.5 pl-1">Total (ms)</th>
							</tr>
						</thead>
						<tbody>
							{#each data.topFiles as f (f.key)}
								{@const cls = f.hitRequests + f.missRequests}
								<tr class="border-b border-border/50">
									<td class="py-0.5 pr-2 font-mono truncate max-w-[18rem]" title={f.key}>{f.key}</td>
									<td class="text-right px-1 tabular-nums">{f.requests.toLocaleString()}</td>
									<td class="text-right px-1 tabular-nums">{f.missRequests.toLocaleString()}</td>
									<!-- ⚠ 판정 대상이 0 이면 '—'. 0% 로 찍으면 "전부 miss" 로 오독된다. -->
									<td class="text-right px-1 tabular-nums">
										{cls > 0 ? `${((f.hitRequests / cls) * 100).toFixed(1)}%` : '—'}
									</td>
									<td class="text-right px-1 tabular-nums">{fmtBytes(f.requestedBytes)}</td>
									<td class="text-right pl-1 tabular-nums">{fmtMs(f.totalDurationNs)}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</div>

		<div class="text-[10px] text-muted-foreground leading-relaxed border-t pt-2">
			<b>CACHE_HIT_INFERRED 는 하드웨어 cache hit 이벤트가 아닙니다.</b>
			“read 가 도는 동안 FS page-fill 훅이 한 번도 안 불렸다”는 음성 증거 추론이라,
			훅이 안 붙은 파일시스템의 read 는 HIT 이 아니라 UNKNOWN 으로 떨어집니다.
			소요 시간은 VFS 진입→반환이지 장치 지연이 아닙니다 — MISS 면 그 안에 장치 왕복이
			포함되고, HIT 이면 page cache 복사 시간만 듭니다.
			byte 단위 hit ratio 는 제공하지 않습니다(request 단위 판정입니다).
		</div>
	</div>
{/if}
