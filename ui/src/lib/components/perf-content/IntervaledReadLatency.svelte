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

	interface CycleEntry {
		cycle: number;
		data: number[];
		max: number;
	}

	interface Props {
		data: CycleEntry[];
		tcName: string;
		fw?: string;
		yAxisMax?: number;
	}

	let { data, tcName, fw, yAxisMax }: Props = $props();

	let chartRef: ReturnType<typeof PerfChartType> | undefined = $state();
	let showRawData = $state(true);

	const indices = $derived(() => {
		const maxLen = data.reduce((m, c) => Math.max(m, c.data?.length ?? 0), 0);
		return Array.from({ length: maxLen }, (_, i) => i + 1);
	});

	const chartOption: EChartsOption = $derived({
		...baseChartOption(tcName, fw),
		xAxis: {
			type: 'category',
			data: indices().map(String),
			name: 'interval(ms)',
			nameLocation: 'center',
			nameGap: 25
		},
		yAxis: {
			type: 'value',
			name: 'ms',
			nameLocation: 'center',
			nameRotate: 90,
			nameGap: 45,
			...(yAxisMax != null ? { max: yAxisMax } : {})
		},
		series: data.map((entry) => ({
			name: `cycle${entry.cycle}`,
			type: 'line' as const,
			data: entry.data,
			smooth: false
		}))
	});

	// Max stats table
	interface StatRow {
		label: string;
		[key: string]: string | number;
	}

	const maxRow: StatRow = $derived(() => {
		const row: StatRow = { label: 'max' };
		for (const entry of data) {
			row[`c${entry.cycle}`] = entry.max;
		}
		return row;
	});

	const statColumns: ColumnDef<StatRow, unknown>[] = $derived([
		{ accessorKey: 'label', header: '' },
		...data.map((entry) => ({
			accessorKey: `c${entry.cycle}`,
			header: `cycle${entry.cycle}`,
			cell: ({ row }: { row: any }) => {
				const v = row.original[`c${entry.cycle}`];
				return v != null ? String(v) : '—';
			}
		}))
	]);

	// Raw data table
	type DataRow = Record<string, number>;

	const dataRows: DataRow[] = $derived(
		indices().map((idx, i) => {
			const row: DataRow = { index: idx };
			for (const entry of data) {
				row[`c${entry.cycle}`] = entry.data[i] ?? 0;
			}
			return row;
		})
	);

	const dataColumns: ColumnDef<DataRow, unknown>[] = $derived([
		{ accessorKey: 'index', header: '', enableSorting: true },
		...data.map((entry) => ({
			accessorKey: `c${entry.cycle}`,
			header: `cycle${entry.cycle}`,
			enableSorting: true,
			cell: ({ row }: { row: any }) => {
				const v = row.original[`c${entry.cycle}`];
				return v != null ? String(v) : '—';
			}
		}))
	]);


</script>

<div class="space-y-3">
	{#if data.length === 0}
		<div class="{emptyState}">
			<span class="text-sm">No Intervaled Read Latency data available</span>
		</div>
	{:else}
		<!-- Chart -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<Card.Content class="p-2">
				<PerfChart bind:this={chartRef} option={chartOption} height="420px" />
			</Card.Content>
		</Card.Root>

		<!-- Max Stats Table -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<Card.Content class="p-2">
				<DataTable
					data={[maxRow()]}
					columns={statColumns}
					showPagination={false}
					compact={true}
					enableColumnVisibility={false}
					enableCellCopy={true}
					getRowId={() => 'max'}
				/>
			</Card.Content>
		</Card.Root>

		<!-- Raw Data Table -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Data ({dataRows.length} points)">
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
