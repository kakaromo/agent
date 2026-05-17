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

	interface CycleEntry {
		cycle: number;
		data: number[];
		avg: number;
		max: number;
		min: number;
		[key: string]: any;
	}

	interface Props {
		data: Record<string, CycleEntry[]>;
		tcName: string;
		fw?: string;
		yAxisMax?: Record<string, number>;
	}

	let { data, tcName, fw, yAxisMax }: Props = $props();

	let chartRef: ReturnType<typeof PerfChartType> | undefined = $state();
	let activeThread = $state('');
	let showRawData = $state(true);

	// Sort thread keys numerically (1-Thread, 2-Thread, 4-Thread, 8-Thread)
	const threadKeys = $derived(
		Object.keys(data).sort((a, b) => {
			const na = parseInt(a) || 0;
			const nb = parseInt(b) || 0;
			return na - nb;
		})
	);

	$effect(() => {
		if (activeThread === '' || !threadKeys.includes(activeThread)) {
			if (threadKeys.length > 0) activeThread = threadKeys[0];
		}
	});

	const currentCycles = $derived(data[activeThread] ?? []);

	const indices = $derived(() => {
		const maxLen = currentCycles.reduce((m, c) => Math.max(m, c.data?.length ?? 0), 0);
		return Array.from({ length: maxLen }, (_, i) => i + 1);
	});

	const chartOption: EChartsOption = $derived({
		...baseChartOption(tcName, fw),
		xAxis: {
			type: 'category',
			data: indices().map(String)
		},
		yAxis: {
			type: 'value',
			nameLocation: 'center',
			nameRotate: 90,
			nameGap: 45,
			...(yAxisMax?.[activeThread] != null ? { max: yAxisMax[activeThread] } : {})
		},
		series: currentCycles.map((entry) => ({
			name: `cycle${entry.cycle}`,
			type: 'line' as const,
			data: entry.data,
			smooth: false
		}))
	});

	// Statistics table (min, max, avg)
	interface StatRow {
		label: string;
		[key: string]: string | number;
	}

	const statRows: StatRow[] = $derived(
		['min', 'max', 'avg'].map((stat) => {
			const row: StatRow = { label: stat };
			for (const entry of currentCycles) {
				row[`c${entry.cycle}`] = (entry as any)[stat] ?? 0;
			}
			return row;
		})
	);

	const statColumns: ColumnDef<StatRow, unknown>[] = $derived([
		{ accessorKey: 'label', header: '' },
		...currentCycles.map((entry) => ({
			accessorKey: `c${entry.cycle}`,
			header: `cycle${entry.cycle}`,
			cell: ({ row }: { row: any }) => {
				const v = row.original[`c${entry.cycle}`];
				return v != null ? Number(v).toFixed(2) : '—';
			}
		}))
	]);

	// Throughput (raw data) table
	type DataRow = Record<string, number>;

	const dataRows: DataRow[] = $derived(
		indices().map((idx, i) => {
			const row: DataRow = { index: idx };
			for (const entry of currentCycles) {
				row[`c${entry.cycle}`] = entry.data[i] ?? 0;
			}
			return row;
		})
	);

	const dataColumns: ColumnDef<DataRow, unknown>[] = $derived([
		{ accessorKey: 'index', header: '', enableSorting: true },
		...currentCycles.map((entry) => ({
			accessorKey: `c${entry.cycle}`,
			header: `cycle${entry.cycle}`,
			enableSorting: true,
			cell: ({ row }: { row: any }) => {
				const v = row.original[`c${entry.cycle}`];
				return v != null ? Number(v).toFixed(2) : '—';
			}
		}))
	]);


</script>

<div class="space-y-3">
	<!-- Toolbar -->
	<div class="flex items-center gap-1.5 flex-wrap">
		{#if threadKeys.length > 1}
			<div class={groupClass}>
				{#each threadKeys as key (key)}
					<button
						class="{btnBase} {activeThread === key ? btnActive : btnInactive}"
						onclick={() => (activeThread = key)}
					>{key}</button>
				{/each}
			</div>
		{/if}

	</div>

	{#if currentCycles.length === 0}
		<div class="{emptyState}">
			<span class="text-sm">No data available for {activeThread}</span>
		</div>
	{:else}
		<!-- Chart -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<Card.Content class="p-2">
				<PerfChart bind:this={chartRef} option={chartOption} height="420px" />
			</Card.Content>
		</Card.Root>

		<!-- Throughput + Statistics side by side -->
		<div class="grid grid-cols-2 gap-3">
			<!-- Throughput -->
			<Card.Root class="gap-0 p-0 overflow-hidden">
				<SectionHeader title="Throughput ({dataRows.length} points)">
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

			<!-- Statistics -->
			<Card.Root class="gap-0 p-0 overflow-hidden">
				<SectionHeader title="Statistics" />
				<Card.Content class="p-2">
					<DataTable
						data={statRows}
						columns={statColumns}
						showPagination={false}
						compact={true}
						enableColumnVisibility={false}
						enableCellCopy={true}
						getRowId={(row) => row.label}
					/>
				</Card.Content>
			</Card.Root>
		</div>
	{/if}
</div>
