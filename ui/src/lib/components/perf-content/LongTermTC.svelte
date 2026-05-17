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
		W1: number;
		W2: number;
		W3: number;
		Write_Arg: number;
		After_W3_FB: number;
		R1: number;
		R2: number;
		R3: number;
		Before_W1_FB?: number;
		Before_W2_FB?: number;
		Before_W3_FB?: number;
		Before_R1_FB?: number;
		Before_R2_FB?: number;
		Before_R3_FB?: number;
	}

	interface Props {
		data: CycleEntry[];
		tcName: string;
		fw?: string;
	}

	let { data, tcName, fw }: Props = $props();

	let writeChartRef: ReturnType<typeof PerfChartType> | undefined = $state();
	let readChartRef: ReturnType<typeof PerfChartType> | undefined = $state();
	let showRawData = $state(true);

	const cycles = $derived(data.map((d) => d.cycle));

	// Write chart: line chart with W1, W2, W3, Write_Arg on primary axis, After_W3_FB on secondary
	const writeChartOption: EChartsOption = $derived({
		...baseChartOption(`${tcName} Write`, undefined, { right: 70 }),
		xAxis: {
			type: 'category',
			data: cycles.map(String),
			name: 'cycle',
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
				name: 'FB',
				nameLocation: 'center',
				nameRotate: -90,
				nameGap: 40
			}
		],
		series: [
			{
				name: 'W1',
				type: 'line',
				data: data.map((d) => d.W1),
				smooth: false
			},
			{
				name: 'W2',
				type: 'line',
				data: data.map((d) => d.W2),
				smooth: false
			},
			{
				name: 'W3',
				type: 'line',
				data: data.map((d) => d.W3),
				smooth: false
			},
			{
				name: 'Write_Arg',
				type: 'line',
				data: data.map((d) => d.Write_Arg),
				smooth: false
			},
			{
				name: 'After_W3_FB',
				type: 'line',
				yAxisIndex: 1,
				data: data.map((d) => d.After_W3_FB),
				smooth: false,
				lineStyle: { width: 3 }
			}
		]
	});

	// Read chart: scatter chart with R1, R2, R3
	const readChartOption: EChartsOption = $derived({
		...baseChartOption(`${tcName} Read`),
		xAxis: {
			type: 'category',
			data: cycles.map(String),
			name: 'cycle',
			nameLocation: 'center',
			nameGap: 25
		},
		yAxis: {
			type: 'value',
			name: 'MB/s',
			nameLocation: 'center',
			nameRotate: 90,
			nameGap: 45
		},
		series: [
			{
				name: 'R1',
				type: 'scatter',
				data: data.map((d) => d.R1),
				symbolSize: 8
			},
			{
				name: 'R2',
				type: 'scatter',
				data: data.map((d) => d.R2),
				symbolSize: 8
			},
			{
				name: 'R3',
				type: 'scatter',
				data: data.map((d) => d.R3),
				symbolSize: 8
			}
		]
	});

	// Data table columns
	const ALL_KEYS = [
		'cycle', 'R1', 'R2', 'R3', 'W1', 'W2', 'W3', 'Write_Arg', 'After_W3_FB',
		'Before_W1_FB', 'Before_W2_FB', 'Before_W3_FB', 'Before_R1_FB', 'Before_R2_FB', 'Before_R3_FB'
	] as const;

	const dataColumns: ColumnDef<CycleEntry, unknown>[] = $derived(
		ALL_KEYS.filter((key) => data.some((d) => (d as any)[key] != null)).map((key) => ({
			accessorKey: key,
			header: key,
			cell: ({ row }: { row: any }) => {
				const v = row.original[key];
				return v != null ? (typeof v === 'number' ? v.toFixed(2) : String(v)) : '—';
			}
		}))
	);


</script>

<div class="space-y-3">
	{#if data.length === 0}
		<div class="{emptyState}">
			<span class="text-sm">No LongTerm TC data available</span>
		</div>
	{:else}
		<!-- Charts side by side -->
		<div class="grid grid-cols-2 gap-3">
			<Card.Root class="gap-0 p-0 overflow-hidden">
				<Card.Content class="p-2">
					<PerfChart bind:this={writeChartRef} option={writeChartOption} height="380px" />
				</Card.Content>
			</Card.Root>
			<Card.Root class="gap-0 p-0 overflow-hidden">
				<Card.Content class="p-2">
					<PerfChart bind:this={readChartRef} option={readChartOption} height="380px" />
				</Card.Content>
			</Card.Root>
		</div>

		<!-- Data Table -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Data ({data.length} cycles)">
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
						data={data}
						columns={dataColumns}
						compact={true}
						enableColumnVisibility={false}
						scrollHeight="320px"
						enableCellCopy={true}
						getRowId={(row) => String(row.cycle)}
					/>
				</Card.Content>
			{/if}
		</Card.Root>
	{/if}
</div>
