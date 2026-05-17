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

	interface FileSizeEntry {
		FileSize: string;
		data: number[];
		avg: number;
		min: number;
		max: number;
	}

	interface CycleEntry {
		cycle: number;
		data: FileSizeEntry[];
	}

	interface Props {
		data: CycleEntry[];
		tcName: string;
		fw?: string;
		yAxisMax?: number;
	}

	let { data, tcName, fw, yAxisMax }: Props = $props();

	let chartRef: ReturnType<typeof PerfChartType> | undefined = $state();
	let activeCycleIdx = $state(0);
	let showRawData = $state(true);

	$effect(() => {
		if (activeCycleIdx >= data.length) activeCycleIdx = 0;
	});

	const currentCycle = $derived(data[activeCycleIdx]);
	const fileSizeEntries = $derived(currentCycle?.data ?? []);

	// Sort FileSize entries by MB value
	function parseMB(s: string): number {
		const match = s.match(/^(\d+)/);
		return match ? Number(match[1]) : 0;
	}

	const sortedEntries = $derived(
		[...fileSizeEntries].sort((a, b) => parseMB(a.FileSize) - parseMB(b.FileSize))
	);

	const indices = $derived(() => {
		const maxLen = sortedEntries.reduce((m, e) => Math.max(m, e.data?.length ?? 0), 0);
		return Array.from({ length: maxLen }, (_, i) => i + 1);
	});

	const chartOption: EChartsOption = $derived({
		...baseChartOption(tcName, fw),
		xAxis: {
			type: 'category',
			data: indices().map(String),
			name: 'GB',
			nameLocation: 'center',
			nameGap: 25
		},
		yAxis: {
			type: 'value',
			name: 'MB/s',
			nameLocation: 'center',
			nameRotate: 90,
			nameGap: 45,
			...(yAxisMax != null ? { max: yAxisMax } : {})
		},
		series: sortedEntries.map((entry) => ({
			name: entry.FileSize,
			type: 'line' as const,
			data: entry.data,
			smooth: false
		}))
	});

	// Statistics table: rows = min/max/avg, columns = FileSize
	interface StatRow {
		label: string;
		[key: string]: string | number;
	}

	const statRows: StatRow[] = $derived(
		['min', 'max', 'avg'].map((stat) => {
			const row: StatRow = { label: stat };
			for (const entry of sortedEntries) {
				row[entry.FileSize] = (entry as any)[stat] ?? 0;
			}
			return row;
		})
	);

	const statColumns: ColumnDef<StatRow, unknown>[] = $derived([
		{ accessorKey: 'label', header: '' },
		...sortedEntries.map((entry) => ({
			accessorKey: entry.FileSize,
			header: entry.FileSize,
			cell: ({ row }: { row: any }) => {
				const v = row.original[entry.FileSize];
				return v != null ? Number(v).toFixed(2) : '—';
			}
		}))
	]);

	// Throughput table: rows = index, columns = FileSize
	type DataRow = Record<string, number>;

	const dataRows: DataRow[] = $derived(
		indices().map((idx, i) => {
			const row: DataRow = { index: idx };
			for (const entry of sortedEntries) {
				row[entry.FileSize] = entry.data[i] ?? 0;
			}
			return row;
		})
	);

	const dataColumns: ColumnDef<DataRow, unknown>[] = $derived([
		{ accessorKey: 'index', header: '', enableSorting: true },
		...sortedEntries.map((entry) => ({
			accessorKey: entry.FileSize,
			header: entry.FileSize,
			enableSorting: true,
			cell: ({ row }: { row: any }) => {
				const v = row.original[entry.FileSize];
				return v != null ? Number(v).toFixed(2) : '—';
			}
		}))
	]);


</script>

<div class="space-y-3">
	<!-- Toolbar -->
	<div class="flex items-center gap-1.5 flex-wrap">
		{#if data.length > 1}
			<div class={groupClass}>
				{#each data as entry, idx (entry.cycle)}
					<button
						class="{btnBase} {activeCycleIdx === idx ? btnActive : btnInactive}"
						onclick={() => (activeCycleIdx = idx)}
					>cycle{entry.cycle}</button>
				{/each}
			</div>
		{/if}

	</div>

	{#if sortedEntries.length === 0}
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
	{/if}
</div>
