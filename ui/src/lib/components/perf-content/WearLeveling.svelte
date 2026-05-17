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

	interface WearLevelingData {
		write: number[];
		min_ec: number[];
		max_ec: number[];
	}

	interface Props {
		data: WearLevelingData;
		tcName: string;
		fw?: string;
	}

	let { data, tcName, fw }: Props = $props();

	let chartRef: ReturnType<typeof PerfChartType> | undefined = $state();
	let showRawData = $state(true);

	// Series type toggles: line or scatter for each axis group
	let primaryType = $state<'line' | 'scatter'>('line');   // write (left Y)
	let secondaryType = $state<'line' | 'scatter'>('line'); // min_ec, max_ec (right Y)

	const indices = $derived(
		Array.from({ length: data.write?.length ?? 0 }, (_, i) => i + 1)
	);

	const chartOption: EChartsOption = $derived({
		...baseChartOption(tcName, fw, { right: 70 }),
		dataZoom: [{ type: 'inside' }, { type: 'slider', show: false }],
		xAxis: {
			type: 'category',
			data: indices.map(String),
			name: 'GB',
			nameLocation: 'center',
			nameGap: 25
		},
		yAxis: [
			{
				type: 'value',
				name: 'MB/s',
				nameLocation: 'center',
				nameRotate: 90,
				nameGap: 45
			},
			{
				type: 'value',
				name: 'EC',
				nameLocation: 'center',
				nameRotate: -90,
				nameGap: 40
			}
		],
		series: [
			{
				name: 'write',
				type: primaryType,
				data: data.write,
				smooth: false,
				...(primaryType === 'scatter' ? { symbolSize: 3 } : {})
			},
			{
				name: 'min_ec',
				type: secondaryType,
				yAxisIndex: 1,
				data: data.min_ec,
				smooth: false,
				...(secondaryType === 'scatter'
					? { symbolSize: 3 }
					: { lineStyle: { width: 2 } })
			},
			{
				name: 'max_ec',
				type: secondaryType,
				yAxisIndex: 1,
				data: data.max_ec,
				smooth: false,
				...(secondaryType === 'scatter'
					? { symbolSize: 3 }
					: { lineStyle: { width: 2 } })
			}
		]
	});

	// Data table
	interface DataRow {
		index: number;
		write: number;
		min_ec: number;
		max_ec: number;
	}

	const dataRows: DataRow[] = $derived(
		indices.map((idx, i) => ({
			index: idx,
			write: data.write[i] ?? 0,
			min_ec: data.min_ec[i] ?? 0,
			max_ec: data.max_ec[i] ?? 0
		}))
	);

	const dataColumns: ColumnDef<DataRow, unknown>[] = [
		{ accessorKey: 'index', header: '', enableSorting: true },
		{
			accessorKey: 'write',
			header: 'write',
			cell: ({ row }) => row.original.write.toFixed(2)
		},
		{
			accessorKey: 'min_ec',
			header: 'min_ec',
			cell: ({ row }) => String(row.original.min_ec)
		},
		{
			accessorKey: 'max_ec',
			header: 'max_ec',
			cell: ({ row }) => String(row.original.max_ec)
		}
	];


</script>

<div class="space-y-3">
	<!-- Toolbar -->
	<div class="flex items-center gap-1.5 flex-wrap">
		<!-- Primary axis (write) type toggle -->
		<span class="text-[11px] text-muted-foreground">주축</span>
		<div class={groupClass}>
			<button class="{btnBase} {primaryType === 'line' ? btnActive : btnInactive}" onclick={() => primaryType = 'line'}>Line</button>
			<button class="{btnBase} {primaryType === 'scatter' ? btnActive : btnInactive}" onclick={() => primaryType = 'scatter'}>Scatter</button>
		</div>

		<!-- Secondary axis (EC) type toggle -->
		<span class="text-[11px] text-muted-foreground ml-2">보조축</span>
		<div class={groupClass}>
			<button class="{btnBase} {secondaryType === 'line' ? btnActive : btnInactive}" onclick={() => secondaryType = 'line'}>Line</button>
			<button class="{btnBase} {secondaryType === 'scatter' ? btnActive : btnInactive}" onclick={() => secondaryType = 'scatter'}>Scatter</button>
		</div>

	</div>

	{#if indices.length === 0}
		<div class="{emptyState}">
			<span class="text-sm">No WearLeveling data available</span>
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
