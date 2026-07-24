<script lang="ts">
	import { PerfChart } from '$lib/components/perf-chart';
	import DataTableShell from '$lib/components/DataTableShell.svelte';
	import * as Card from '$lib/components/ui/card';
	import SectionHeader from '$lib/components/perf-content/SectionHeader.svelte';
	import { btnBase, btnActive, btnInactive, groupClass } from '$lib/components/perf-content/perfStyles';
	import type { EChartsOption } from 'echarts';
	import { type ColumnDef } from '@tanstack/table-core';
	import {
		buildCycleMatrix,
		buildMetricChartGroups,
		classifyRw,
		formatMetricValue,
		getMetricUnit,
		stripRw,
		type CycleStepMetrics,
		type MetricChartGroup,
	} from './types.js';

	interface Props {
		metrics: Record<string, number>;        // 이 step 의 단일-cycle metrics (prefix 제거됨)
		cycleMetrics?: CycleStepMetrics[];      // cycle 별 metric (없으면 metrics 1개로 가공)
	}

	let { metrics, cycleMetrics = [] }: Props = $props();

	// cycle 데이터가 비어있으면 props.metrics 를 cycle 1 로 wrapping (single-shot 결과)
	const effectiveCycles: CycleStepMetrics[] = $derived(
		cycleMetrics.length > 0
			? cycleMetrics
			: [{ cycle: 1, repeat: 1, iteration: 0, metrics }]
	);

	const matrix = $derived(buildCycleMatrix(effectiveCycles));
	const chartGroups: MetricChartGroup[] = $derived(buildMetricChartGroups(matrix));
	const hasMultiCycle = $derived(matrix.cycles.length > 1);

	let chartType = $state<'line' | 'bar'>('line');
	let showMinMax = $state(false);
	let showAllMetrics = $state(false);

	// hasMultiCycle 가 false 면 bar 만 의미있음 → 자동
	$effect(() => {
		if (!hasMultiCycle && chartType !== 'bar') {
			chartType = 'bar';
		}
	});

	function buildChartOption(group: MetricChartGroup): EChartsOption {
		if (hasMultiCycle) {
			// x = cycle, series = RW 카테고리 별
			const series = group.members.map(m => {
				const data = matrix.cycles.map(c => {
					const raw = matrix.values[m.rawKey]?.[c] ?? 0;
					return raw / getMetricUnit(m.rawKey).divisor;
				});
				return {
					name: m.rwCategory,
					type: chartType,
					data,
					smooth: false,
					itemStyle: { color: m.color },
					lineStyle: chartType === 'line' ? { color: m.color, width: 2 } : undefined,
					...(showMinMax && chartType === 'line'
						? {
							markPoint: {
								data: [
									{ type: 'max', name: 'Max' },
									{ type: 'min', name: 'Min' },
								],
								symbolSize: 36,
								label: {
									fontSize: 9,
									formatter: (p: any) => `${p.name}\n${Number(p.value).toFixed(1)}`,
								},
								animation: false,
							},
						}
						: {}),
				};
			});
			return {
				tooltip: { trigger: 'axis' as const },
				legend: { type: 'scroll', top: 0, textStyle: { fontSize: 10 } },
				xAxis: {
					type: 'category' as const,
					name: 'Cycle',
					nameGap: 22,
					data: matrix.cycles.map(c => `C${c}`),
					axisLabel: { fontSize: 9 },
				},
				yAxis: {
					type: 'value' as const,
					name: group.unit,
					nameTextStyle: { fontSize: 9 },
					axisLabel: { fontSize: 9 },
				},
				grid: { left: 60, right: 20, top: 36, bottom: 36 },
				series: series as any,
			};
		}
		// single cycle bar — x = RW 카테고리
		return {
			tooltip: { trigger: 'axis' as const },
			xAxis: {
				type: 'category' as const,
				data: group.members.map(m => m.rwCategory),
				axisLabel: { fontSize: 10 },
			},
			yAxis: {
				type: 'value' as const,
				name: group.unit,
				nameTextStyle: { fontSize: 9 },
				axisLabel: { fontSize: 9 },
			},
			grid: { left: 60, right: 20, top: 24, bottom: 36 },
			series: [
				{
					type: 'bar' as const,
					data: group.members.map(m => {
						const raw = matrix.values[m.rawKey]?.[matrix.cycles[0]] ?? 0;
						return { value: raw / getMetricUnit(m.rawKey).divisor, itemStyle: { color: m.color, borderRadius: [4, 4, 0, 0] } };
					}),
					barMaxWidth: 50,
					label: {
						show: true,
						position: 'top' as const,
						fontSize: 10,
						formatter: (p: any) => Number(p.value).toFixed(1),
					},
				} as any,
			],
		};
	}

	// ─── Statistics pivot — cycle × 핵심 metric ───────────────────────────────
	interface StatsRow {
		cycle: number;
		[metricCol: string]: string | number;
	}

	const statsColumns: ColumnDef<StatsRow>[] = $derived.by(() => {
		const cols: ColumnDef<StatsRow>[] = [
			{ accessorKey: 'cycle', header: 'Cycle', cell: ({ row }) => `C${row.original.cycle}` },
		];
		// 각 chartGroup × member 를 컬럼으로
		for (const g of chartGroups) {
			for (const m of g.members) {
				const colKey = m.rawKey;
				cols.push({
					accessorKey: colKey,
					header: `${m.rwCategory} ${g.label} (${g.unit})`,
				});
			}
		}
		return cols;
	});

	const statsRows: StatsRow[] = $derived.by(() => {
		const rows: StatsRow[] = [];
		for (const c of matrix.cycles) {
			const row: StatsRow = { cycle: c };
			for (const g of chartGroups) {
				for (const m of g.members) {
					const raw = matrix.values[m.rawKey]?.[c] ?? 0;
					const v = raw / getMetricUnit(m.rawKey).divisor;
					row[m.rawKey] = v.toLocaleString('en-US', { maximumFractionDigits: 2 });
				}
			}
			rows.push(row);
		}
		return rows;
	});

	// vs C1 delta — multi-cycle 에서만 추가
	const deltaColumns: ColumnDef<StatsRow>[] = $derived.by(() => {
		if (!hasMultiCycle || chartGroups.length === 0) return [];
		// 첫 chart group 의 첫 member 기준 delta 한 컬럼만 (시야 정리)
		const refMember = chartGroups[0].members[0];
		if (!refMember) return [];
		const refKey = refMember.rawKey;
		const refLabel = `vs C1 (${refMember.rwCategory} ${chartGroups[0].label})`;
		return [
			{
				id: 'delta',
				header: refLabel,
				cell: ({ row }) => {
					if (row.index === 0) return '—';
					const baseRaw = matrix.values[refKey]?.[matrix.cycles[0]] ?? 0;
					const curRaw = matrix.values[refKey]?.[row.original.cycle] ?? 0;
					if (baseRaw === 0) return '—';
					const d = ((curRaw - baseRaw) / baseRaw) * 100;
					const sign = d >= 0 ? '+' : '';
					return `${sign}${d.toFixed(1)}%`;
				},
			} as ColumnDef<StatsRow>,
		];
	});

	const fullStatsColumns = $derived([...statsColumns, ...deltaColumns]);

	// ─── Full Metrics 표 (collapsed) ──────────────────────────────────────────
	interface FullMetricRow {
		category: string;       // 'read' / 'write' / 'other'
		metric: string;
		value: string;
		unit: string;
	}

	const fullMetricRows: FullMetricRow[] = $derived.by(() => {
		const rows: FullMetricRow[] = [];
		for (const [k, v] of Object.entries(metrics)) {
			const cat = classifyRw(k);
			const name = stripRw(k);
			const u = getMetricUnit(k);
			rows.push({
				category: cat,
				metric: name,
				value: formatMetricValue(v, k),
				unit: u.unit,
			});
		}
		return rows.sort((a, b) => a.category.localeCompare(b.category) || a.metric.localeCompare(b.metric));
	});

	const fullMetricColumns: ColumnDef<FullMetricRow>[] = [
		{ accessorKey: 'category', header: 'RW' },
		{ accessorKey: 'metric', header: 'Metric' },
		{ accessorKey: 'value', header: 'Value' },
		{ accessorKey: 'unit', header: 'Unit' },
	];

	const noChartData = $derived(chartGroups.length === 0);
</script>

<div class="space-y-3">
	<!-- Toolbar -->
	<div class="flex items-center gap-1.5 flex-wrap">
		{#if hasMultiCycle}
			<div class={groupClass}>
				<button
					class="{btnBase} {chartType === 'line' ? btnActive : btnInactive}"
					onclick={() => (chartType = 'line')}>Line</button>
				<button
					class="{btnBase} {chartType === 'bar' ? btnActive : btnInactive}"
					onclick={() => (chartType = 'bar')}>Bar</button>
			</div>
			<div class={groupClass}>
				<button
					class="{btnBase} {showMinMax ? btnActive : btnInactive}"
					onclick={() => (showMinMax = !showMinMax)}
					title="Show min/max markers">Min/Max</button>
			</div>
		{/if}
		<div class={groupClass}>
			<button
				class="{btnBase} {showAllMetrics ? btnActive : btnInactive}"
				onclick={() => (showAllMetrics = !showAllMetrics)}
				title="Show full metric table">Show All Metrics</button>
		</div>
	</div>

	<!-- Chart Card -->
	<Card.Root class="gap-0 p-0 overflow-hidden">
		<Card.Content class="p-2">
			{#if noChartData}
				<div class="flex flex-col items-center justify-center h-[200px] text-muted-foreground gap-1">
					<span class="text-sm">차트로 그릴 metric 이 없습니다</span>
					<span class="text-[11px] text-muted-foreground/60">"Show All Metrics" 로 전체 metric 을 확인하세요</span>
				</div>
			{:else}
				<div class="grid grid-cols-1 md:grid-cols-2 gap-3">
					{#each chartGroups as group (group.groupKey)}
						<div>
							<div class="text-[10px] text-muted-foreground font-medium mb-1">
								{group.label} <span class="opacity-70">({group.unit})</span>
							</div>
							<PerfChart option={buildChartOption(group)} height="240px" />
						</div>
					{/each}
				</div>
			{/if}
		</Card.Content>
	</Card.Root>

	<!-- Statistics Card -->
	{#if !noChartData}
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Statistics" />
			<Card.Content class="p-2">
				<DataTableShell data={statsRows} columns={fullStatsColumns}  />
			</Card.Content>
		</Card.Root>
	{/if}

	<!-- Full Metrics (collapsible) -->
	{#if showAllMetrics}
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="All Metrics ({fullMetricRows.length})" />
			<Card.Content class="p-2">
				<DataTableShell data={fullMetricRows} columns={fullMetricColumns}  />
			</Card.Content>
		</Card.Root>
	{/if}
</div>
