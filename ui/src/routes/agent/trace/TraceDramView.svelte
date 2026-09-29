<script lang="ts">
	/**
	 * DRAM 대역폭 뷰 — Qualcomm bw_hwmon_meas(bwmon-ddr) 시계열 + 요약 (dram_bw 전용).
	 *
	 * IO trace 와 **같은 시간축(초)** 이다. ftrace 로그에 bw_hwmon_meas 줄이 섞여 있으면
	 * Rust 가 dram_bw 형제 parquet 을 자동으로 만든다.
	 *
	 * 해석 주의 (bpftrace/docs/DRAM_BW.md):
	 *  - 단위는 MiB/s. 서버가 변환하지 않고 그대로 준다 — 여기서도 곱하거나 나누지 않는다.
	 *  - 커널이 올림으로 계산한다 → 낮은 값은 상한이다.
	 *  - CPU/LLCC 경로의 DDR 트래픽이다. 전체 DRAM 이 아닐 수 있고, idle 에도 바닥값이 있다.
	 *  - 이벤트를 찍는 건 kworker 다. 특정 프로세스가 쓴 대역폭이 아니다.
	 *
	 * 줌은 페이지의 timeRange 를 공유한다 — 여기서 휠로 확대하면 Raw Chart 도 같은 구간이 된다.
	 */
	import * as echarts from 'echarts';
	import { onDestroy } from 'svelte';
	import type { DramBandwidthResponse, DramBwSummary, DramBwQuery, StepBoundary } from './types.js';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';

	interface SpanInfo {
		start: number;
		end: number;
		label: string;
		color: string;
	}

	interface Props {
		/**
		 * 데이터를 가져오는 함수 — 호출부가 준다. portal 은 parquetId 로, standalone(agent)은
		 * jobIds 로 조회하므로 fetch 를 여기 두면 두 사본이 갈라진다. 이 컴포넌트는 표현만 한다.
		 */
		load: (q: DramBwQuery, signal: AbortSignal) => Promise<DramBandwidthResponse>;
		/** 조회 대상이 바뀌었는지 판단하는 키 (portal: parquetId, agent: jobIds). */
		sourceKey: string;
		/** 줌 범위 (초). null = 전체. */
		timeWindow: { start: number; end: number } | null;
		/** x축 기본 범위 (초) — IO 차트와 맞추려고 페이지가 넘긴다. 줌 중엔 timeWindow 가 우선. */
		domain: { min: number; max: number } | null;
		/** 켜진 구간들 (전부 켜져 있거나 구간이 없으면 null) — 요약은 이 구간들의 합. */
		spans: SpanInfo[] | null;
		boundaries: StepBoundary[];
		hiddenBoundaries: Set<number>;
		boundaryColor: (i: number) => string;
		onZoomChange: (start: number, end: number) => void;
		onResetZoom: () => void;
	}
	let {
		load,
		sourceKey,
		timeWindow,
		domain,
		spans,
		boundaries,
		hiddenBoundaries,
		boundaryColor,
		onZoomChange,
		onResetZoom
	}: Props = $props();

	let loading = $state(false);
	let error = $state<string | null>(null);
	let data = $state<DramBandwidthResponse | null>(null);

	const LINE_COLOR = '#6366f1';

	function fmtMib(v: number | null | undefined, digits = 0): string {
		if (v == null || !isFinite(v)) return '—';
		return v.toLocaleString(undefined, { maximumFractionDigits: digits, minimumFractionDigits: digits });
	}
	function fmtMs(v: number | null | undefined): string {
		if (v == null || !isFinite(v)) return '—';
		return v < 10 ? v.toFixed(1) : v.toLocaleString(undefined, { maximumFractionDigits: 0 });
	}
	function fmtSec(v: number): string {
		return v.toFixed(3);
	}

	// 조회 키 — 객체 identity 가 아니라 값이 바뀔 때만 다시 부른다.
	const queryKey = $derived(
		JSON.stringify({
			src: sourceKey,
			w: timeWindow,
			s: spans?.map((sp) => [sp.start, sp.end]) ?? null
		})
	);

	let abort: AbortController | null = null;
	async function fetchData() {
		abort?.abort();
		const ac = new AbortController();
		abort = ac;
		loading = true;
		error = null;
		try {
			// ⚠ load 가 signal 을 무시할 수 있다(agent API) — 늦게 온 옛 응답이 새 결과를 덮지 않게
			//   받은 뒤에 한 번 더 확인한다.
			const r = await load(
				{
					timeStart: timeWindow?.start ?? null,
					timeEnd: timeWindow?.end ?? null,
					spans: spans?.map(({ start, end }) => ({ start, end })) ?? null,
					targetPoints: 2000
				},
				ac.signal
			);
			if (ac.signal.aborted) return;
			data = r;
		} catch (e) {
			if (ac.signal.aborted) return;
			error = e instanceof Error ? e.message : String(e);
			data = null;
		} finally {
			if (abort === ac) loading = false;
		}
	}

	$effect(() => {
		void queryKey;
		void fetchData();
	});

	const summary = $derived<DramBwSummary | null>(data?.summary ?? null);

	/**
	 * x축 범위. 줌 중이면 그 범위 그대로, 아니면 IO 와 DRAM 을 모두 덮는 범위를
	 * 깔끔한 눈금으로 바깥 정렬 (TraceChartView.timeDomain 과 같은 규칙).
	 */
	const xDomain = $derived.by(() => {
		if (timeWindow) return { min: timeWindow.start, max: timeWindow.end };
		let lo = domain?.min ?? Infinity;
		let hi = domain?.max ?? -Infinity;
		const t = data?.time ?? [];
		if (t.length) {
			lo = Math.min(lo, t[0]);
			hi = Math.max(hi, t[t.length - 1]);
		}
		if (!isFinite(lo) || !isFinite(hi)) return null;
		const span = hi - lo;
		if (span <= 0) return { min: lo - 0.001, max: hi + 0.001 };
		const mag = Math.pow(10, Math.floor(Math.log10(span / 10)));
		const norm = span / 10 / mag;
		const step = (norm >= 5 ? 5 : norm >= 2 ? 2 : 1) * mag;
		const decimals = Math.max(0, -Math.floor(Math.log10(step)));
		const round = (v: number) => Number(v.toFixed(decimals));
		return { min: round(Math.floor(lo / step) * step), max: round(Math.ceil(hi / step) * step) };
	});

	/**
	 * 수집 공백에서 선을 끊는다. 빈 버킷은 서버가 싣지 않으므로, 이웃 점 간격이
	 * 평소보다 크게 벌어진 곳에 null 을 끼워 넣는다 — 안 끊으면 공백을 가로지르는
	 * 직선이 "그동안 이 값이었다" 로 읽힌다.
	 */
	function withGaps(time: number[], ys: number[]): (number | null)[][] {
		const med = (summary?.intervalMedianMs ?? 4) / 1000;
		const threshold = Math.max((data?.bucketSec ?? 0) * 3, med * 5);
		const out: (number | null)[][] = [];
		for (let i = 0; i < time.length; i++) {
			if (i > 0 && time[i] - time[i - 1] > threshold) {
				out.push([(time[i] + time[i - 1]) / 2, null]);
			}
			out.push([time[i], ys[i]]);
		}
		return out;
	}

	const bands = $derived(
		boundaries
			.map((b, i) => ({ b, i }))
			.filter(({ i }) => !hiddenBoundaries.has(i))
			.map(({ b, i }) => [
				{
					xAxis: b.startedMono,
					itemStyle: { color: boundaryColor(i).replace('rgb(', 'rgba(').replace(')', ',0.10)') }
				},
				{ xAxis: b.finishedMono }
			])
	);

	function buildOption(): echarts.EChartsCoreOption {
		const d = data!;
		const bucketed = d.bucketed;
		return {
			animation: false,
			grid: { left: 56, right: 16, top: 28, bottom: 36 },
			legend: { top: 0, right: 8, textStyle: { fontSize: 10 }, itemWidth: 14, itemHeight: 8 },
			tooltip: {
				trigger: 'axis',
				axisPointer: { type: 'line' },
				formatter: (params: unknown) => {
					const ps = (params as { seriesName: string; value: (number | null)[] }[]).filter(
						(p) => p.value?.[1] != null
					);
					if (!ps.length) return '';
					const t = ps[0].value[0] as number;
					const rows = ps
						.map((p) => `${p.seriesName}: <b>${fmtMib(p.value[1] as number, bucketed && p.seriesName.startsWith('평균') ? 1 : 0)}</b> MiB/s`)
						.join('<br/>');
					return `${fmtSec(t)} s<br/>${rows}`;
				}
			},
			xAxis: {
				type: 'value',
				name: 'time (s)',
				nameLocation: 'middle',
				nameGap: 22,
				nameTextStyle: { fontSize: 10 },
				min: xDomain?.min,
				max: xDomain?.max,
				axisLabel: { fontSize: 10 }
			},
			yAxis: {
				type: 'value',
				name: 'MiB/s',
				nameTextStyle: { fontSize: 10 },
				min: 0,
				axisLabel: { fontSize: 10 }
			},
			dataZoom: [{ type: 'inside', xAxisIndex: 0, filterMode: 'none' }],
			series: [
				...(bucketed
					? [
							{
								type: 'line' as const,
								name: '최대',
								data: withGaps(d.time, d.maxMibps),
								showSymbol: false,
								lineStyle: { width: 1, color: LINE_COLOR, opacity: 0.35 },
								itemStyle: { color: LINE_COLOR, opacity: 0.35 }
							}
						]
					: []),
				{
					type: 'line' as const,
					name: bucketed ? '평균' : 'DRAM',
					data: withGaps(d.time, d.avgMibps),
					showSymbol: d.time.length < 200,
					symbolSize: 3,
					lineStyle: { width: 1.5, color: LINE_COLOR },
					itemStyle: { color: LINE_COLOR },
					markArea: bands.length ? { silent: true, label: { show: false }, data: bands } : undefined
				}
			]
		};
	}

	let el = $state<HTMLDivElement | null>(null);
	let chart = $state.raw<echarts.ECharts | null>(null);
	let ro: ResizeObserver | null = null;
	let zoomTimer: ReturnType<typeof setTimeout> | null = null;

	$effect(() => {
		if (!el) return;
		const c = echarts.init(el);
		chart = c;
		c.on('datazoom', () => {
			const opt = c.getOption() as { dataZoom?: { startValue?: number; endValue?: number }[] };
			const z = opt.dataZoom?.[0];
			if (z?.startValue == null || z?.endValue == null) return;
			const s = z.startValue;
			const e = z.endValue;
			if (zoomTimer) clearTimeout(zoomTimer);
			// 페이지 onZoomChange 도 디바운스하지만, 여기서 먼저 묶어 두면 휠 한 번에
			// 부모 상태가 여러 번 흔들리지 않는다.
			zoomTimer = setTimeout(() => onZoomChange(s, e), 150);
		});
		ro = new ResizeObserver(() => {
			if (!c.isDisposed()) c.resize();
		});
		ro.observe(el);
		return () => {
			ro?.disconnect();
			ro = null;
			chart = null;
			c.dispose();
		};
	});

	$effect(() => {
		// 데이터/축/밴드가 바뀌면 다시 그린다. notMerge — 줌 상태를 새 범위 기준으로 초기화.
		void xDomain;
		void bands;
		if (!chart || !data || data.time.length === 0) return;
		chart.setOption(buildOption(), { notMerge: true });
	});

	onDestroy(() => {
		abort?.abort();
		if (zoomTimer) clearTimeout(zoomTimer);
	});

	const spanRows = $derived(
		(spans ?? []).map((sp, i) => ({ ...sp, summary: data?.spans?.[i]?.summary ?? null }))
	);
</script>

<div class="flex flex-col gap-3 p-3">
	{#if error}
		<div class="border border-destructive/40 bg-destructive/5 rounded p-3 text-sm">
			<div class="font-medium mb-1">DRAM 대역폭을 불러오지 못했어요</div>
			<div class="text-xs text-muted-foreground break-all">{error}</div>
		</div>
	{:else if !data && loading}
		<div class="flex items-center justify-center gap-2 p-8 text-sm text-muted-foreground">
			<LoaderIcon class="size-4 animate-spin" /> DRAM 대역폭 조회 중…
		</div>
	{:else if data && (summary?.samples ?? 0) === 0 && data.time.length === 0}
		<div class="text-center text-sm text-muted-foreground p-8">
			{timeWindow ? '이 구간에는 DRAM 샘플이 없어요' : 'DRAM 샘플이 없어요'}
		</div>
	{:else if data}
		<!-- 요약 문장 -->
		<div class="flex items-start gap-2">
			<div class="text-xs leading-relaxed">
				{#if summary && summary.samples > 0}
					{spans ? '켜진 구간에서' : timeWindow ? '이 구간에서' : '전체 구간에서'} DRAM 대역폭은 평균
					<b class="tabular-nums">{fmtMib(summary.avgMibps, 1)} MiB/s</b>, 최대
					<b class="tabular-nums">{fmtMib(summary.maxMibps)} MiB/s</b>였어요.
					<span class="text-muted-foreground">
						샘플 {summary.samples.toLocaleString()}개 · 보통 {fmtMs(summary.intervalMedianMs)}ms 간격
					</span>
				{:else}
					<span class="text-muted-foreground">켜진 구간 안에는 DRAM 샘플이 없어요.</span>
				{/if}
			</div>
			<div class="ml-auto flex items-center gap-2 shrink-0">
				{#if loading}<LoaderIcon class="size-3.5 animate-spin text-muted-foreground" />{/if}
				{#if timeWindow}
					<button
						class="inline-flex items-center gap-1 rounded border px-2 py-0.5 text-[11px] hover:bg-muted"
						onclick={onResetZoom}
					>
						<RotateCcwIcon class="size-3" /> 전체 보기
					</button>
				{/if}
			</div>
		</div>

		<div class="grid grid-cols-2 md:grid-cols-5 gap-2">
			{#each [['평균', summary?.avgMibps, 1], ['P50', summary?.p50Mibps, 0], ['P95', summary?.p95Mibps, 0], ['P99', summary?.p99Mibps, 0], ['최대', summary?.maxMibps, 0]] as [label, v, digits] (label)}
				<div class="border rounded-md p-2">
					<div class="text-[9px] text-muted-foreground font-semibold">{label}</div>
					<div class="text-sm font-semibold tabular-nums">
						{fmtMib(v as number | null, digits as number)}
						<span class="text-[10px] font-normal text-muted-foreground">MiB/s</span>
					</div>
				</div>
			{/each}
		</div>

		{#if data.qualityWarnings.length > 0}
			<div class="flex flex-col gap-1">
				{#each data.qualityWarnings as w (w)}
					<div class="flex items-start gap-1.5 text-[11px] text-amber-600 dark:text-amber-500">
						<TriangleAlertIcon class="size-3.5 shrink-0 mt-px" />
						<span>{w}</span>
					</div>
				{/each}
			</div>
		{/if}

		<!-- 시계열 -->
		<div>
			<div class="flex items-baseline gap-2 mb-1">
				<h3 class="text-xs font-semibold">시간별 DRAM 대역폭</h3>
				<span class="text-[10px] text-muted-foreground">
					{#if data.bucketed}
						{(data.bucketSec * 1000).toLocaleString(undefined, { maximumFractionDigits: 1 })}ms 묶음의 평균 · 옅은 선은 묶음 안 최대
					{:else}
						샘플 그대로
					{/if}
					· 휠로 확대하면 Raw Chart 도 같은 구간으로 맞춰져요
				</span>
			</div>
			<div bind:this={el} class="w-full h-[320px]"></div>
		</div>

		{#if spanRows.length > 0}
			<div>
				<div class="flex items-baseline gap-2 mb-1">
					<h3 class="text-xs font-semibold">구간별</h3>
					<span class="text-[10px] text-muted-foreground">켜진 구간 각각의 대역폭</span>
				</div>
				<div class="overflow-x-auto">
					<table class="w-full text-[11px]">
						<thead>
							<tr class="border-b text-muted-foreground">
								<th class="text-left font-medium py-0.5 pr-2">구간</th>
								<th class="text-right font-medium py-0.5 px-1">길이 (s)</th>
								<th class="text-right font-medium py-0.5 px-1">샘플</th>
								<th class="text-right font-medium py-0.5 px-1">평균</th>
								<th class="text-right font-medium py-0.5 px-1">P95</th>
								<th class="text-right font-medium py-0.5 pl-1">최대</th>
							</tr>
						</thead>
						<tbody>
							{#each spanRows as r, i (i)}
								<tr class="border-b border-border/50">
									<td class="py-0.5 pr-2">
										<span class="inline-flex items-center gap-1.5">
											<span class="inline-block size-2 rounded-sm" style="background:{r.color}"></span>
											{r.label}
										</span>
									</td>
									<td class="text-right px-1 tabular-nums">{(r.end - r.start).toFixed(2)}</td>
									<td class="text-right px-1 tabular-nums">{(r.summary?.samples ?? 0).toLocaleString()}</td>
									<td class="text-right px-1 tabular-nums">{fmtMib(r.summary?.avgMibps, 1)}</td>
									<td class="text-right px-1 tabular-nums">{fmtMib(r.summary?.p95Mibps)}</td>
									<td class="text-right pl-1 tabular-nums">{fmtMib(r.summary?.maxMibps)}</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			</div>
		{/if}

		<div class="text-[10px] text-muted-foreground leading-relaxed">
			<b>{data.devs.join(', ') || 'bwmon-ddr'}</b> 노드의 읽기+쓰기 합계예요 (Qualcomm bw_hwmon_meas).
			CPU/LLCC 경로의 DDR 트래픽이라 전체 DRAM 이 아닐 수 있고, 아무것도 안 할 때도 수백 MiB/s 가
			나와요 — IO 때문에 늘어난 양은 idle 구간과 비교해 보세요. 커널이 올림으로 계산해서 낮은 값은
			상한이에요. 이벤트는 kworker 가 찍기 때문에 특정 프로세스의 사용량이 아니에요.
		</div>
	{/if}
</div>
