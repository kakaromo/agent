<script lang="ts">
	import { type ColumnDef } from '@tanstack/table-core';
	import { emptyState } from '$lib/styles/common.js';
	import { PerfChart } from '$lib/components/perf-chart';
	import { DataTable } from '$lib/components/data-table';
	import * as Card from '$lib/components/ui/card';
	import type { EChartsOption } from 'echarts';
	import type { PerfChart as PerfChartType } from '$lib/components/perf-chart';
	import { btnBase, btnActive, btnInactive, groupClass } from './perfStyles';
	import { baseChartOption } from './perfChartUtils';

	interface ChunkData {
		chunk: number;
		perf: number;
	}

	interface RangeData {
		range: number;
		chunkdata: ChunkData[];
	}

	interface CycleEntry {
		cycle: number;
		rangedata: RangeData[];
	}

	interface Props {
		data: Record<string, CycleEntry[]>;
		tcName: string;
		fw?: string;
	}

	let { data, tcName, fw }: Props = $props();

	let chartRef: ReturnType<typeof PerfChartType> | undefined = $state();
	let activeType = $state('');
	let activeCycleIdx = $state(0);

	// Type tabs (After_RW, After_SW)
	const typeKeys = $derived(Object.keys(data));

	$effect(() => {
		if (activeType === '' || !typeKeys.includes(activeType)) {
			if (typeKeys.length > 0) activeType = typeKeys[0];
		}
	});

	const currentCycles = $derived(data[activeType] ?? []);

	$effect(() => {
		if (activeCycleIdx >= currentCycles.length) {
			activeCycleIdx = 0;
		}
	});

	const currentCycle = $derived(currentCycles[activeCycleIdx]);
	const rangeData = $derived(currentCycle?.rangedata ?? []);

	// Chunk bytes → KB label
	function chunkLabel(bytes: number): string {
		return `${bytes / 1024}KB`;
	}

	// perf MB/s → IOPS: perf * 1024 * 1024 / chunk
	function toIOPS(perf: number, chunk: number): number {
		return (perf * 1024 * 1024) / chunk;
	}

	// Get sorted unique chunk sizes from first range entry
	const chunkSizes = $derived(() => {
		if (rangeData.length === 0) return [];
		const chunks = rangeData[0].chunkdata.map((c) => c.chunk);
		return chunks.sort((a, b) => a - b);
	});

	// Ranges as x-axis labels
	const ranges = $derived(rangeData.map((r) => r.range));

	// Chart: each chunk size is a series, x-axis is range
	const chartOption: EChartsOption = $derived({
		...baseChartOption(tcName, fw),
		xAxis: {
			type: 'category',
			data: ranges.map(String),
			name: 'Range(GB)',
			nameLocation: 'center',
			nameGap: 25
		},
		yAxis: {
			type: 'value',
			name: 'IOPS',
			nameLocation: 'center',
			nameRotate: 90,
			nameGap: 45
		},
		series: chunkSizes().map((chunk) => ({
			name: chunkLabel(chunk),
			type: 'line' as const,
			data: rangeData.map((r) => {
				const found = r.chunkdata.find((c) => c.chunk === chunk);
				return found ? toIOPS(found.perf, chunk) : 0;
			}),
			smooth: false
		}))
	});

	// Data table: rows = range, columns = chunk sizes (IOPS)
	interface TableRow {
		range: number;
		[key: string]: number;
	}

	const tableRows: TableRow[] = $derived(
		rangeData.map((r) => {
			const row: TableRow = { range: r.range };
			for (const cd of r.chunkdata) {
				row[`c${cd.chunk}`] = toIOPS(cd.perf, cd.chunk);
			}
			return row;
		})
	);

	const tableColumns: ColumnDef<TableRow, unknown>[] = $derived([
		{ accessorKey: 'range', header: '', enableSorting: true },
		...chunkSizes().map((chunk) => ({
			accessorKey: `c${chunk}`,
			header: chunkLabel(chunk),
			cell: ({ row }: { row: any }) => {
				const v = row.original[`c${chunk}`];
				return v != null ? Number(v).toFixed(2) : '—';
			}
		}))
	]);


</script>

<div class="space-y-3">
	<!-- Toolbar -->
	<div class="flex items-center gap-1.5 flex-wrap">
		<!-- Type tabs -->
		{#if typeKeys.length > 1}
			<div class={groupClass}>
				{#each typeKeys as key (key)}
					<button
						class="{btnBase} {activeType === key ? btnActive : btnInactive}"
						onclick={() => (activeType = key)}
					>{key}</button>
				{/each}
			</div>
		{/if}

		<!-- Cycle tabs -->
		{#if currentCycles.length > 1}
			<div class={groupClass}>
				{#each currentCycles as entry, idx (entry.cycle)}
					<button
						class="{btnBase} {activeCycleIdx === idx ? btnActive : btnInactive}"
						onclick={() => (activeCycleIdx = idx)}
					>cycle{entry.cycle}</button>
				{/each}
			</div>
		{/if}

	</div>

	{#if rangeData.length === 0}
		<div class="{emptyState}">
			<span class="text-sm">No data available</span>
		</div>
	{:else}
		<!-- Chart -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<Card.Content class="p-2">
				<PerfChart bind:this={chartRef} option={chartOption} height="420px" />
			</Card.Content>
		</Card.Root>

		<!-- Data Table -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<Card.Content class="p-2">
				<DataTable
					data={tableRows}
					columns={tableColumns}
					showPagination={false}
					compact={true}
					enableColumnVisibility={false}
					enableCellCopy={true}
					getRowId={(row) => String(row.range)}
				/>
			</Card.Content>
		</Card.Root>
	{/if}
</div>
