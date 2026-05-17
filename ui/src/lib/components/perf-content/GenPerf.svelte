<script lang="ts">
	import { type ColumnDef } from '@tanstack/table-core';
	import { PerfChart } from '$lib/components/perf-chart';
	import { DataTable } from '$lib/components/data-table';
	import * as Card from '$lib/components/ui/card';
	import type { EChartsOption } from 'echarts';
	import type { PerfChart as PerfChartType } from '$lib/components/perf-chart';
	import SectionHeader from './SectionHeader.svelte';
	import { btnBase, btnActive, btnInactive, btnDisabled, groupClass } from './perfStyles';
	import { baseChartOption } from './perfChartUtils';

	interface CycleEntry {
		avg: number;
		cycle: number;
		data: number[];
		max: number;
		min: number;
	}

	// Known tab definitions: key (lowercase) → display label
	const TAB_DEFS = [
		{ key: 'read', label: 'Read' },
		{ key: 'write', label: 'Write' },
		{ key: 'flushtime', label: 'Flush Time' }
	] as const;

	interface Props {
		data: Record<string, CycleEntry[]>;
		tcName: string;
		fw?: string;
		yAxisMax?: Record<string, number>;
	}

	let { data, tcName, fw, yAxisMax }: Props = $props();

	let chartRef: ReturnType<typeof PerfChartType> | undefined = $state();
	let activeTab = $state('');
	let chartType = $state<'line' | 'scatter'>('line');
	let showRawData = $state(true);
	let showOutliers = $state(false);

	// Normalize all data keys to lowercase
	const normalizedData = $derived(() => {
		const result: Record<string, CycleEntry[]> = {};
		for (const [key, value] of Object.entries(data)) {
			if (Array.isArray(value)) {
				result[key.toLowerCase()] = value;
			}
		}
		return result;
	});

	// Check if cycles have valid data
	function hasValidData(cycles: CycleEntry[]): boolean {
		return cycles.length > 0 && cycles.some((c) => c.data?.length > 0);
	}

	// Show tabs whose key exists in data (even if empty/invalid → shown as disabled)
	const availableTabs = $derived(
		TAB_DEFS.filter((tab) => tab.key in normalizedData())
	);

	// Auto-select first tab with valid data
	$effect(() => {
		const currentValid = availableTabs.some(
			(t) => t.key === activeTab && hasValidData(normalizedData()[t.key] ?? [])
		);
		if (!currentValid) {
			const firstValid = availableTabs.find((t) => hasValidData(normalizedData()[t.key] ?? []));
			if (firstValid) activeTab = firstValid.key;
		}
	});

	const currentCycles = $derived(normalizedData()[activeTab] ?? []);
	const activeLabel = $derived(availableTabs.find((t) => t.key === activeTab)?.label ?? '');
	const chartTitle = $derived(`${tcName} \u2013 ${activeLabel}`);

	// X-axis indices from the longest data array
	const indices = $derived(() => {
		const maxLen = currentCycles.reduce((m, c) => Math.max(m, c.data?.length ?? 0), 0);
		return Array.from({ length: maxLen }, (_, i) => i);
	});

	// Y-axis unit: "rand" in tcName → IOPS, "seq" → MB/s
	const yAxisUnit = $derived(
		/rand/i.test(tcName) ? 'IOPS' : /seq/i.test(tcName) ? 'MB/s' : 'Value'
	);

	// Build chart option
	const chartOption: EChartsOption = $derived({
		...baseChartOption(chartTitle, fw, { left: 90 }),
		xAxis: {
			type: 'category',
			data: indices().map(String),
			name: 'GB',
			nameLocation: 'center',
			nameGap: 25
		},
		yAxis: {
			type: 'value',
			name: yAxisUnit,
			nameLocation: 'center',
			nameRotate: 90,
			nameGap: 50,
			...(yAxisMax?.[activeTab] != null ? { max: yAxisMax[activeTab] } : {})
		},
		series: currentCycles.map((entry) => ({
			name: `Cycle ${entry.cycle}`,
			type: chartType,
			data: entry.data,
			symbolSize: chartType === 'scatter' ? 4 : undefined,
			smooth: false,
			...(showOutliers
				? {
						markPoint: {
							data: [
								{ type: 'max', name: 'Max' },
								{ type: 'min', name: 'Min' }
							],
							symbolSize: 40,
							label: {
								fontSize: 10,
								formatter: (p: any) => `${p.name}\n${Number(p.value).toFixed(1)}`
							},
							animation: false
						}
					}
				: {})
		}))
	});

	// --- Data Table: pivot cycles into rows by index ---
	type DataRow = Record<string, number>;

	const dataRows: DataRow[] = $derived(
		indices().map((idx) => {
			const row: DataRow = { index: idx };
			for (const entry of currentCycles) {
				row[`c${entry.cycle}`] = entry.data[idx] ?? 0;
			}
			return row;
		})
	);

	const dataColumns: ColumnDef<DataRow, unknown>[] = $derived([
		{ accessorKey: 'index', header: 'Index', enableSorting: true },
		...currentCycles.map((entry) => ({
			accessorKey: `c${entry.cycle}`,
			header: `Cycle ${entry.cycle}`,
			enableSorting: true
		}))
	]);

	// --- Stats Table ---
	const baselineAvg = $derived(currentCycles.length > 0 ? currentCycles[0].avg : 0);

	const statsColumns: ColumnDef<CycleEntry, unknown>[] = $derived([
		{
			accessorKey: 'cycle',
			header: 'Cycle',
			cell: ({ row }) => `Cycle ${row.original.cycle}`
		},
		{
			accessorKey: 'min',
			header: `Min (${yAxisUnit})`,
			cell: ({ row }) => row.original.min.toFixed(2)
		},
		{
			accessorKey: 'max',
			header: `Max (${yAxisUnit})`,
			cell: ({ row }) => row.original.max.toFixed(2)
		},
		{
			accessorKey: 'avg',
			header: `Avg (${yAxisUnit})`,
			cell: ({ row }) => row.original.avg.toFixed(2)
		},
		{
			id: 'delta',
			header: 'vs C1',
			cell: ({ row }) => {
				if (row.index === 0 || baselineAvg === 0) return '—';
				const delta = ((row.original.avg - baselineAvg) / baselineAvg) * 100;
				const sign = delta >= 0 ? '+' : '';
				return `${sign}${delta.toFixed(1)}%`;
			}
		}
	]);

	// --- Range Stats: 처음 N개 데이터의 min/max/avg ---
	const RANGE_PRESETS = [10, 20, 30, 50, 100] as const;
	let rangeCount = $state(20);
	let rangeCustomInput = $state('');
	let showRangeStats = $state(true);

	function setRangeCount(n: number) {
		if (n > 0) rangeCount = n;
	}

	function applyCustomRange() {
		const n = parseInt(rangeCustomInput, 10);
		if (n > 0) {
			rangeCount = n;
			rangeCustomInput = '';
		}
	}

	const rangeDataRows: DataRow[] = $derived(
		indices().slice(0, rangeCount).map((idx) => {
			const row: DataRow = { index: idx };
			for (const entry of currentCycles) {
				row[`c${entry.cycle}`] = entry.data[idx] ?? 0;
			}
			return row;
		})
	);

	interface RangeStatEntry {
		cycle: number;
		min: number;
		max: number;
		avg: number;
	}

	const rangeStats: RangeStatEntry[] = $derived(
		currentCycles.map((entry) => {
			const sliced = entry.data.slice(0, rangeCount).filter(v => v != null);
			if (sliced.length === 0) return { cycle: entry.cycle, min: 0, max: 0, avg: 0 };
			const min = Math.min(...sliced);
			const max = Math.max(...sliced);
			const avg = sliced.reduce((a, b) => a + b, 0) / sliced.length;
			return { cycle: entry.cycle, min, max, avg };
		})
	);

	const rangeBaselineAvg = $derived(rangeStats.length > 0 ? rangeStats[0].avg : 0);

	const rangeStatsColumns: ColumnDef<RangeStatEntry, unknown>[] = $derived([
		{
			accessorKey: 'cycle',
			header: 'Cycle',
			cell: ({ row }) => `Cycle ${row.original.cycle}`
		},
		{
			accessorKey: 'min',
			header: `Min (${yAxisUnit})`,
			cell: ({ row }) => row.original.min.toFixed(2)
		},
		{
			accessorKey: 'max',
			header: `Max (${yAxisUnit})`,
			cell: ({ row }) => row.original.max.toFixed(2)
		},
		{
			accessorKey: 'avg',
			header: `Avg (${yAxisUnit})`,
			cell: ({ row }) => row.original.avg.toFixed(2)
		},
		{
			id: 'delta',
			header: 'vs C1',
			cell: ({ row }) => {
				if (row.index === 0 || rangeBaselineAvg === 0) return '—';
				const delta = ((row.original.avg - rangeBaselineAvg) / rangeBaselineAvg) * 100;
				const sign = delta >= 0 ? '+' : '';
				return `${sign}${delta.toFixed(1)}%`;
			}
		}
	]);

	const rangeDataColumns: ColumnDef<DataRow, unknown>[] = $derived([
		{ accessorKey: 'index', header: 'Index', enableSorting: true },
		...currentCycles.map((entry) => ({
			accessorKey: `c${entry.cycle}`,
			header: `Cycle ${entry.cycle}`,
			enableSorting: true
		}))
	]);


</script>

<div class="space-y-3">
	<!-- Toolbar -->
	<div class="flex items-center gap-1.5 flex-wrap">
		{#if availableTabs.length > 1}
			<div class={groupClass}>
				{#each availableTabs as tab (tab.key)}
					{@const valid = hasValidData(normalizedData()[tab.key] ?? [])}
					<button
						class="{btnBase} {activeTab === tab.key ? btnActive : !valid ? btnDisabled : btnInactive}"
						onclick={() => valid && (activeTab = tab.key)}
						disabled={!valid}
					>
						{tab.label} <span class="opacity-60">({normalizedData()[tab.key]?.length ?? 0})</span>
					</button>
				{/each}
			</div>
			<div class="w-px h-5 bg-border"></div>
		{/if}

		<div class={groupClass}>
			<button
				class="{btnBase} {chartType === 'line' ? btnActive : btnInactive}"
				onclick={() => (chartType = 'line')}
			>Line</button>
			<button
				class="{btnBase} {chartType === 'scatter' ? btnActive : btnInactive}"
				onclick={() => (chartType = 'scatter')}
			>Scatter</button>
		</div>

		<div class={groupClass}>
			<button
				class="{btnBase} {showOutliers ? btnActive : btnInactive}"
				onclick={() => (showOutliers = !showOutliers)}
				title="Show min/max markers on chart"
			>Min/Max</button>
		</div>

	</div>

	<!-- Chart Card -->
	<Card.Root class="gap-0 p-0 overflow-hidden">
		<Card.Content class="p-2">
			{#if currentCycles.length === 0}
				<div class="flex flex-col items-center justify-center h-[420px] text-muted-foreground gap-1">
					<span class="text-sm">No data for "{activeLabel || 'this tab'}"</span>
					<span class="text-[11px] text-muted-foreground/60">Try selecting a different tab above, or the test may not have produced data for this category.</span>
				</div>
			{:else}
				<PerfChart bind:this={chartRef} option={chartOption} height="420px" />
			{/if}
		</Card.Content>
	</Card.Root>

	<!-- Tables -->
	{#if currentCycles.length > 0}
		<!-- Statistics (compact, full width) -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Statistics" />
			<Card.Content class="p-2">
				<DataTable
					data={currentCycles}
					columns={statsColumns}
					showPagination={false}
					compact={true}
					enableColumnVisibility={false}
					enableCellCopy={true}
					getRowId={(row) => String(row.cycle)}
				/>
			</Card.Content>
		</Card.Root>

		<!-- Range Stats: 처음 N개 데이터 통계 -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Range Statistics (First {rangeCount})">
				<div class="flex items-center gap-1.5">
					<select
						class="h-6 rounded border border-input bg-background px-1.5 text-[11px]"
						value={rangeCount}
						onchange={(e) => setRangeCount(Number((e.target as HTMLSelectElement).value))}
					>
						{#each RANGE_PRESETS as n}
							<option value={n}>{n}</option>
						{/each}
					</select>
					<input
						class="h-6 w-14 rounded border border-input bg-background px-1.5 text-[11px] placeholder:text-muted-foreground"
						type="number"
						min="1"
						placeholder="직접"
						bind:value={rangeCustomInput}
						onkeydown={(e) => { if (e.key === 'Enter') applyCustomRange(); }}
					/>
					<button
						class="text-[11px] text-muted-foreground hover:text-foreground transition-colors"
						onclick={() => (showRangeStats = !showRangeStats)}
					>
						{showRangeStats ? 'Hide' : 'Show'}
					</button>
				</div>
			</SectionHeader>
			{#if showRangeStats}
				<Card.Content class="p-2 space-y-2">
					<DataTable
						data={rangeStats}
						columns={rangeStatsColumns}
						showPagination={false}
						compact={true}
						enableColumnVisibility={false}
						enableCellCopy={true}
						getRowId={(row) => String(row.cycle)}
					/>
					<DataTable
						data={rangeDataRows}
						columns={rangeDataColumns}
						compact={true}
						enableColumnVisibility={false}
						scrollHeight="280px"
						enableCellCopy={true}
						getRowId={(row) => String(row.index)}
					/>
				</Card.Content>
			{/if}
		</Card.Root>

		<!-- Raw Data (collapsible) -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Raw Data ({dataRows.length} points)">
				<button
					class="text-[11px] text-muted-foreground hover:text-foreground transition-colors"
					onclick={() => (showRawData = !showRawData)}
				>
					{showRawData ? 'Hide' : 'Show'}
				</button>
			</SectionHeader>
			{#if showRawData}
				<Card.Content class="p-2">
					<DataTable
						data={dataRows}
						columns={dataColumns}
						compact={true}
						enableColumnVisibility={false}
						scrollHeight="320px"
						enableCellCopy={true}
						getRowId={(row) => String(row.index)}
					/>
				</Card.Content>
			{/if}
		</Card.Root>
	{/if}
</div>
