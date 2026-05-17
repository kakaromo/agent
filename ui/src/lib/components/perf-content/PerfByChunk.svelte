<script lang="ts">
	import { type ColumnDef } from '@tanstack/table-core';
	import { emptyState } from '$lib/styles/common.js';
	import { PerfChart } from '$lib/components/perf-chart';
	import { DataTable } from '$lib/components/data-table';
	import * as Card from '$lib/components/ui/card';
	import type { EChartsOption } from 'echarts';
	import type { PerfChart as PerfChartType } from '$lib/components/perf-chart';
	import SectionHeader from './SectionHeader.svelte';
	import { baseChartOption } from './perfChartUtils';

	interface ChunkEntry {
		cycle: number;
		[chunkSize: string]: number;
	}

	interface Props {
		data: { write: ChunkEntry[]; read: ChunkEntry[] };
		tcName: string;
		fw?: string;
	}

	let { data, tcName, fw }: Props = $props();

	let writeChartRef: ReturnType<typeof PerfChartType> | undefined = $state();
	let readChartRef: ReturnType<typeof PerfChartType> | undefined = $state();

	// Parse chunk key "12KB" → numeric KB value for sorting
	function parseChunkKB(key: string): number {
		const match = key.match(/^(\d+)KB$/i);
		return match ? Number(match[1]) : 0;
	}

	// Extract and sort chunk keys from entries
	function getSortedChunkKeys(entries: ChunkEntry[]): string[] {
		if (entries.length === 0) return [];
		const keys = Object.keys(entries[0]).filter((k) => k !== 'cycle');
		return keys.sort((a, b) => parseChunkKB(a) - parseChunkKB(b));
	}

	const writeEntries = $derived(data.write ?? []);
	const readEntries = $derived(data.read ?? []);
	const writeChunkKeys = $derived(getSortedChunkKeys(writeEntries));
	const readChunkKeys = $derived(getSortedChunkKeys(readEntries));

	function buildChartOption(entries: ChunkEntry[], chunkKeys: string[], label: string): EChartsOption {
		return {
			...baseChartOption(`${tcName} ${label}`, fw),
			xAxis: {
				type: 'category',
				data: chunkKeys,
				name: 'chunk',
				nameLocation: 'center',
				nameGap: 25,
				axisLabel: { rotate: 45 }
			},
			yAxis: {
				type: 'value',
				name: 'MB/s',
				nameLocation: 'center',
				nameRotate: 90,
				nameGap: 45
			},
			series: entries.map((entry) => ({
				name: `cycle${entry.cycle}`,
				type: 'line' as const,
				data: chunkKeys.map((k) => entry[k] ?? 0),
				smooth: false
			}))
		};
	}

	const writeChartOption: EChartsOption = $derived(buildChartOption(writeEntries, writeChunkKeys, 'Write'));
	const readChartOption: EChartsOption = $derived(buildChartOption(readEntries, readChunkKeys, 'Read'));

	// Data table: rows = chunk sizes, columns = cycles
	interface ChunkRow {
		chunk: string;
		[key: string]: string | number;
	}

	function buildRows(entries: ChunkEntry[], chunkKeys: string[]): ChunkRow[] {
		return chunkKeys.map((chunk) => {
			const row: ChunkRow = { chunk };
			for (const entry of entries) {
				row[`c${entry.cycle}`] = entry[chunk] ?? 0;
			}
			return row;
		});
	}

	function buildColumns(entries: ChunkEntry[]): ColumnDef<ChunkRow, unknown>[] {
		return [
			{ accessorKey: 'chunk', header: '' },
			...entries.map((entry) => ({
				accessorKey: `c${entry.cycle}`,
				header: `cycle${entry.cycle}`,
				cell: ({ row }: { row: any }) => {
					const v = row.original[`c${entry.cycle}`];
					return v != null ? Number(v).toFixed(2) : '—';
				}
			}))
		];
	}

	const writeRows = $derived(buildRows(writeEntries, writeChunkKeys));
	const readRows = $derived(buildRows(readEntries, readChunkKeys));
	const writeColumns = $derived(buildColumns(writeEntries));
	const readColumns = $derived(buildColumns(readEntries));

	const hasWrite = $derived(writeEntries.length > 0);
	const hasRead = $derived(readEntries.length > 0);


</script>

<div class="space-y-3">
	{#if !hasWrite && !hasRead}
		<div class="{emptyState}">
			<span class="text-sm">No Performance by Chunk data available</span>
		</div>
	{:else}
		<!-- Charts side by side -->
		<div class="grid grid-cols-2 gap-3">
			{#if hasWrite}
				<Card.Root class="gap-0 p-0 overflow-hidden">
					<Card.Content class="p-2">
						<PerfChart bind:this={writeChartRef} option={writeChartOption} height="380px" />
					</Card.Content>
				</Card.Root>
			{/if}
			{#if hasRead}
				<Card.Root class="gap-0 p-0 overflow-hidden">
					<Card.Content class="p-2">
						<PerfChart bind:this={readChartRef} option={readChartOption} height="380px" />
					</Card.Content>
				</Card.Root>
			{/if}
		</div>

		<!-- Data Tables side by side -->
		<div class="grid grid-cols-2 gap-3">
			{#if hasWrite}
				<Card.Root class="gap-0 p-0 overflow-hidden">
					<SectionHeader title="Write" />
					<Card.Content class="p-2">
						<DataTable
							data={writeRows}
							columns={writeColumns}
							showPagination={false}
							compact={true}
							enableColumnVisibility={false}
							enableCellCopy={true}
							getRowId={(row) => row.chunk}
						/>
					</Card.Content>
				</Card.Root>
			{/if}
			{#if hasRead}
				<Card.Root class="gap-0 p-0 overflow-hidden">
					<SectionHeader title="Read" />
					<Card.Content class="p-2">
						<DataTable
							data={readRows}
							columns={readColumns}
							showPagination={false}
							compact={true}
							enableColumnVisibility={false}
							enableCellCopy={true}
							getRowId={(row) => row.chunk}
						/>
					</Card.Content>
				</Card.Root>
			{/if}
		</div>
	{/if}
</div>
