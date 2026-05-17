<script lang="ts">
	import { type ColumnDef } from '@tanstack/table-core';
	import { emptyState } from '$lib/styles/common.js';
	import { PerfChart } from '$lib/components/perf-chart';
	import { DataTable } from '$lib/components/data-table';
	import * as Card from '$lib/components/ui/card';
	import type { EChartsOption } from 'echarts';
	import type { PerfChart as PerfChartType } from '$lib/components/perf-chart';
	import SectionHeader from './SectionHeader.svelte';
	import { btnBase, btnActive, btnInactive, groupClass } from './perfStyles';
	import { baseChartOption } from './perfChartUtils';

	interface OpStats {
		min: number;
		max: number;
		avg: number;
		med: number;
		std: number;
		total: number;
		'9.99th': number;
		'99.9999th': number;
		percentile: number[];
		[key: string]: number | number[] | null;
	}

	interface CycleEntry {
		cycle: number;
		elu?: Record<string, OpStats>;
		nlu?: Record<string, OpStats>;
		[key: string]: number | Record<string, OpStats> | undefined;
	}

	const STAT_KEYS = ['min', 'max', 'avg', 'med', 'std', '9.99th', '99.9999th', 'total'] as const;
	const BUCKET_ORDER = [
		'< 1ms', '< 5ms', '< 10ms', '< 50ms', '< 100ms', '< 300ms',
		'< 500ms', '< 1s', '< 5s', '< 10s', '10s <='
	] as const;

	interface Props {
		data: CycleEntry[];
		tcName: string;
		fw?: string;
	}

	let { data, tcName, fw }: Props = $props();

	let chartRef: ReturnType<typeof PerfChartType> | undefined = $state();
	let activeLu = $state('');
	let activeOp = $state('');
	let activeCycle = $state<number>(0);

	// Discover available LU types (elu, nlu, vlu, etc.)
	const LU_SKIP = new Set(['cycle']);
	const availableLus = $derived(
		(() => {
			const set = new Set<string>();
			for (const c of data) {
				for (const key of Object.keys(c)) {
					if (!LU_SKIP.has(key) && typeof c[key] === 'object' && c[key] !== null) {
						set.add(key);
					}
				}
			}
			return [...set];
		})()
	);

	// Available operations for current LU
	const availableOps = $derived(
		(() => {
			const set = new Set<string>();
			for (const c of data) {
				const luData = c[activeLu] as Record<string, OpStats> | undefined;
				if (luData) {
					for (const op of Object.keys(luData)) set.add(op);
				}
			}
			return [...set];
		})()
	);

	// Auto-select LU
	$effect(() => {
		if (!availableLus.includes(activeLu)) {
			activeLu = availableLus[0] ?? '';
		}
	});

	// Auto-select operation
	$effect(() => {
		if (!availableOps.includes(activeOp)) {
			activeOp = availableOps[0] ?? '';
		}
	});

	// Auto-select cycle
	$effect(() => {
		if (data.length > 0 && !data.some((c) => c.cycle === activeCycle)) {
			activeCycle = data[0].cycle;
		}
	});

	// Get stats for a specific cycle/lu/op
	function getStats(cycle: number): OpStats | null {
		const entry = data.find((c) => c.cycle === cycle);
		if (!entry) return null;
		const luData = entry[activeLu] as Record<string, OpStats> | undefined;
		return luData?.[activeOp] ?? null;
	}

	// Current stats for all cycles
	const allCycleStats = $derived(
		data
			.map((c) => {
				const stats = getStats(c.cycle);
				if (!stats) return null;
				return { cycle: c.cycle, ...stats };
			})
			.filter((s): s is NonNullable<typeof s> => s != null)
	);

	// Percentile labels: 0, 0.1, 0.2, ..., 99.9, 100 = 1001개 (0.1 간격)
	const PERCENTILE_LABELS: string[] = Array.from({ length: 1001 }, (_, i) => (i / 10).toFixed(1));


	// Chart: 같은 cycle + op에서 모든 LU를 한 차트에 비교
	function getLuStats(cycle: number, lu: string): OpStats | null {
		const entry = data.find((c) => c.cycle === cycle);
		if (!entry) return null;
		const luData = entry[lu] as Record<string, OpStats> | undefined;
		return luData?.[activeOp] ?? null;
	}

	const chartOption: EChartsOption = $derived({
		...baseChartOption(`${tcName} — ${activeOp} Cycle ${activeCycle}`, fw, { left: 90 }),
		tooltip: {
			trigger: 'axis',
			formatter: (params: any) => {
				const arr = Array.isArray(params) ? params : [params];
				if (arr.length === 0 || !arr[0]?.value) return '';
				const pct = arr[0].value[1];
				const pctLabel = pct >= 99.99 ? pct.toFixed(4) : pct >= 99 ? pct.toFixed(1) : String(Math.round(pct));
				let html = `<b>${pctLabel}th percentile</b><br/>`;
				for (const p of arr) {
					html += `${p.marker} ${p.seriesName}: <b>${p.value?.[0]?.toFixed(3) ?? '—'}</b> ms<br/>`;
				}
				return html;
			}
		},
		xAxis: {
			type: 'value',
			name: 'Latency (ms)',
			nameLocation: 'center',
			nameGap: 25,
			min: (() => {
				let minVal = Infinity;
				for (const lu of availableLus) {
					const stats = getLuStats(activeCycle, lu);
					if (stats?.min != null && stats.min > 0 && stats.min < minVal) minVal = stats.min;
				}
				return minVal === Infinity ? 0 : minVal;
			})(),
			axisLabel: { fontSize: 10 }
		},
		yAxis: {
			type: 'value',
			name: 'Percentile',
			nameLocation: 'center',
			nameRotate: 90,
			nameGap: 60,
			min: 0,
			max: 100,
			axisLabel: {
				formatter: (v: number) => {
					if (v >= 99.99) return v.toFixed(2) + '%';
					if (v >= 99) return v.toFixed(1) + '%';
					return String(Math.round(v)) + '%';
				},
				fontSize: 10
			}
		},
		series: availableLus.map((lu) => {
			const stats = getLuStats(activeCycle, lu);
			const pctData = (stats?.percentile ?? []).slice(0, PERCENTILE_LABELS.length);
			return {
				name: lu.toUpperCase(),
				type: 'line' as const,
				data: pctData.map((latency, i) => [latency, parseFloat(PERCENTILE_LABELS[i])]),
				smooth: false,
				symbol: 'none',
				emphasis: { focus: 'series' as const }
			};
		})
	});

	// Stats table columns
	const statsColumns: ColumnDef<(typeof allCycleStats)[number], unknown>[] = $derived([
		{ accessorKey: 'cycle', header: 'Cycle', cell: ({ row }) => `Cycle ${row.original.cycle}` },
		...STAT_KEYS.filter((k) => k !== 'total').map((key) => ({
			accessorKey: key,
			header: key,
			cell: ({ row }: { row: any }) => {
				const v = row.original[key];
				return v != null ? Number(v).toFixed(3) : '—';
			}
		})),
		{
			accessorKey: 'total',
			header: 'Total IOs',
			cell: ({ row }: { row: any }) => {
				const v = row.original.total;
				return v != null ? Number(v).toLocaleString() : '—';
			}
		}
	]);

	// Distribution table
	const distRows = $derived(
		BUCKET_ORDER.map((bucket) => {
			const row: Record<string, string | number> = { bucket };
			for (const s of allCycleStats) {
				row[`c${s.cycle}`] = (s as any)[bucket] ?? 0;
			}
			return row;
		})
	);

	const distColumns: ColumnDef<(typeof distRows)[number], unknown>[] = $derived([
		{ accessorKey: 'bucket', header: 'Latency Range' },
		...allCycleStats.map((s) => ({
			accessorKey: `c${s.cycle}`,
			header: `Cycle ${s.cycle}`,
			cell: ({ row }: { row: any }) => {
				const v = row.original[`c${s.cycle}`];
				return v != null ? Number(v).toLocaleString() : '0';
			}
		}))
	]);


</script>

<div class="space-y-3">
	<!-- Toolbar: Op + Cycle + Excel -->
	<div class="flex items-center gap-1.5 flex-wrap">
		{#if availableOps.length > 1}
			<div class={groupClass}>
				{#each availableOps as op (op)}
					<button
						class="{btnBase} {activeOp === op ? btnActive : btnInactive}"
						onclick={() => (activeOp = op)}
					>
						{op}
					</button>
				{/each}
			</div>
		{/if}

		{#if data.length > 1}
			<div class="w-px h-5 bg-border"></div>
			<div class={groupClass}>
				{#each data as c (c.cycle)}
					<button
						class="{btnBase} {activeCycle === c.cycle ? btnActive : btnInactive}"
						onclick={() => activeCycle = c.cycle}
					>
						C{c.cycle}
					</button>
				{/each}
			</div>
		{/if}

	</div>

	{#if allCycleStats.length === 0}
		<div class="{emptyState}">
			<span class="text-sm">No latency data for "{activeOp}"</span>
		</div>
	{:else}
		<!-- Percentile Chart (모든 LU를 한 차트에) -->
		{#if PERCENTILE_LABELS.length > 0}
			<Card.Root class="gap-0 p-0 overflow-hidden">
				<Card.Content class="p-2">
					<PerfChart bind:this={chartRef} option={chartOption} height="420px" />
				</Card.Content>
			</Card.Root>
		{/if}

		<!-- Stats + Latency Tables: LU별 양옆 배치 (DataTable) -->
		<div style="display: grid; grid-template-columns: repeat({availableLus.length}, minmax(0, 1fr)); gap: 0.75rem;">
			{#each availableLus as lu (lu)}
				{@const luStats = getLuStats(activeCycle, lu)}
				{@const luStatsRows = luStats ? STAT_KEYS.map(key => ({ metric: key, value: typeof luStats[key] === 'number' ? (luStats[key] as number).toFixed(3) : '—' })) : []}
				{@const luDistRows = luStats ? BUCKET_ORDER.map(bucket => ({ bucket, count: (luStats[bucket] as number | null) ?? 0 })).filter(r => r.count !== 0) : []}
				<div class="space-y-3">
					<!-- Statistics -->
					<Card.Root class="gap-0 p-0 overflow-hidden">
						<SectionHeader title="Statistics — {lu.toUpperCase()}" />
						<Card.Content class="p-2">
							<DataTable
								data={luStatsRows}
								columns={[
									{ accessorKey: 'metric', header: 'Metric' },
									{ accessorKey: 'value', header: 'Value' }
								]}
								showPagination={false}
								compact={true}
								enableColumnVisibility={false}
								enableCellCopy={true}
								getRowId={(row) => row.metric}
							/>
						</Card.Content>
					</Card.Root>

					<!-- Latency Distribution -->
					<Card.Root class="gap-0 p-0 overflow-hidden">
						<SectionHeader title="Latency — {lu.toUpperCase()}" />
						<Card.Content class="p-2">
							<DataTable
								data={luDistRows}
								columns={[
									{ accessorKey: 'bucket', header: 'Range' },
									{ accessorKey: 'count', header: 'Count' }
								]}
								showPagination={false}
								compact={true}
								enableColumnVisibility={false}
								enableCellCopy={true}
								getRowId={(row) => row.bucket}
							/>
						</Card.Content>
					</Card.Root>
				</div>
			{/each}
		</div>
	{/if}
</div>
