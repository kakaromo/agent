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
		rand: { data: number[]; min: number };
		seq: { data: number[]; min: number };
	}

	interface Props {
		data: CycleEntry[];
		tcName: string;
		fw?: string;
		yAxisMax?: number;
	}

	let { data, tcName, fw, yAxisMax }: Props = $props();

	let chartRef: ReturnType<typeof PerfChartType> | undefined = $state();
	let activeTab = $state<'seq' | 'rand'>('seq');
	let showRawData = $state(true);

	const currentEntries = $derived(
		data.filter((d) => d[activeTab]?.data?.length > 0)
	);

	const indices = $derived(() => {
		const maxLen = currentEntries.reduce((m, c) => Math.max(m, c[activeTab]?.data?.length ?? 0), 0);
		return Array.from({ length: maxLen }, (_, i) => i + 1);
	});

	const chartOption: EChartsOption = $derived({
		...baseChartOption(`${tcName} ${activeTab.toUpperCase()}`, fw),
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
		series: currentEntries.map((entry) => ({
			name: `cycle${entry.cycle}`,
			type: 'line' as const,
			data: entry[activeTab].data,
			smooth: false
		}))
	});

	// Min stats table
	interface MinRow {
		label: string;
		[key: string]: string | number;
	}

	const minRow: MinRow = $derived(() => {
		const row: MinRow = { label: 'min' };
		for (const entry of currentEntries) {
			row[`c${entry.cycle}`] = entry[activeTab].min;
		}
		return row;
	});

	const minColumns: ColumnDef<MinRow, unknown>[] = $derived([
		{ accessorKey: 'label', header: '' },
		...currentEntries.map((entry) => ({
			accessorKey: `c${entry.cycle}`,
			header: `cycle${entry.cycle}`,
			cell: ({ row }: { row: any }) => {
				const v = row.original[`c${entry.cycle}`];
				return v != null ? (typeof v === 'number' ? v.toFixed(2) : String(v)) : '—';
			}
		}))
	]);

	// Raw data table
	type DataRow = Record<string, number>;

	const dataRows: DataRow[] = $derived(
		indices().map((idx, i) => {
			const row: DataRow = { index: idx };
			for (const entry of currentEntries) {
				row[`c${entry.cycle}`] = entry[activeTab].data[i] ?? 0;
			}
			return row;
		})
	);

	const dataColumns: ColumnDef<DataRow, unknown>[] = $derived([
		{ accessorKey: 'index', header: '', enableSorting: true },
		...currentEntries.map((entry) => ({
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
		<div class={groupClass}>
			<button
				class="{btnBase} {activeTab === 'seq' ? btnActive : btnInactive}"
				onclick={() => (activeTab = 'seq')}
			>seq</button>
			<button
				class="{btnBase} {activeTab === 'rand' ? btnActive : btnInactive}"
				onclick={() => (activeTab = 'rand')}
			>rand</button>
		</div>

	</div>

	{#if currentEntries.length === 0}
		<div class="{emptyState}">
			<span class="text-sm">No {activeTab} data available</span>
		</div>
	{:else}
		<!-- Chart -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<Card.Content class="p-2">
				<PerfChart bind:this={chartRef} option={chartOption} height="420px" />
			</Card.Content>
		</Card.Root>

		<!-- Min Stats Table -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Statistics" />
			<Card.Content class="p-2">
				<DataTable
					data={[minRow()]}
					columns={minColumns}
					showPagination={false}
					compact={true}
					enableColumnVisibility={false}
					enableCellCopy={true}
					getRowId={() => 'min'}
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
