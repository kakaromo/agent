<script lang="ts">
	import PerfChart from '$lib/components/perf-chart/PerfChart.svelte';
	import DataTableShell from '$lib/components/DataTableShell.svelte';
	import { type ColumnDef } from '@tanstack/table-core';
	import type { CycleStepMetrics } from './types.js';

	interface Props {
		metrics: Record<string, number>;
		cycleMetrics?: CycleStepMetrics[];
	}

	let { metrics, cycleMetrics = [] }: Props = $props();

	const hasCycles = $derived(cycleMetrics.length > 1);

	// metric key 패턴: thread_{name}_{bytes|ops|duration_ns|throughput_bps|iops|errors}
	// 그 외 total_*: total_bytes / total_ops / total_throughput_bps / total_iops / total_duration_ns
	type ThreadRow = {
		name: string;
		bytes: number;
		ops: number;
		durationMs: number;
		throughputMBs: number;
		iops: number;
		errors: number;
	};

	function buildThreads(m: Record<string, number>): ThreadRow[] {
		const map = new Map<string, ThreadRow>();
		for (const [k, v] of Object.entries(m)) {
			const match = k.match(/^thread_(.+)_(bytes|ops|duration_ns|throughput_bps|iops|errors)$/);
			if (!match) continue;
			const name = match[1];
			const field = match[2];
			if (!map.has(name)) {
				map.set(name, {
					name, bytes: 0, ops: 0, durationMs: 0, throughputMBs: 0, iops: 0, errors: 0
				});
			}
			const row = map.get(name)!;
			if (field === 'bytes') row.bytes = v;
			else if (field === 'ops') row.ops = v;
			else if (field === 'duration_ns') row.durationMs = v / 1_000_000;
			else if (field === 'throughput_bps') row.throughputMBs = v / 1_048_576;
			else if (field === 'iops') row.iops = v;
			else if (field === 'errors') row.errors = v;
		}
		return [...map.values()].sort((a, b) => a.name.localeCompare(b.name));
	}

	const threads = $derived(buildThreads(metrics));

	const totals = $derived.by(() => ({
		bytes: metrics['total_bytes'] ?? 0,
		ops: metrics['total_ops'] ?? 0,
		durationMs: (metrics['total_duration_ns'] ?? 0) / 1_000_000,
		throughputMBs: (metrics['total_throughput_bps'] ?? 0) / 1_048_576,
		iops: metrics['total_iops'] ?? 0,
	}));

	function fmtBytes(b: number): string {
		if (b >= 1_073_741_824) return (b / 1_073_741_824).toFixed(2) + ' GB';
		if (b >= 1_048_576) return (b / 1_048_576).toFixed(2) + ' MB';
		if (b >= 1024) return (b / 1024).toFixed(1) + ' KB';
		return b.toFixed(0) + ' B';
	}
	function fmtNum(v: number, digits = 2): string {
		if (v === 0) return '0';
		if (v >= 1000) return v.toLocaleString('en-US', { maximumFractionDigits: 1 });
		return v.toFixed(digits);
	}

	// ── thread별 throughput / iops bar chart (single cycle) ──
	function buildBar(field: 'throughputMBs' | 'iops', unit: string, color: string) {
		if (threads.length === 0) return null;
		return {
			tooltip: { trigger: 'axis' as const },
			xAxis: { type: 'category' as const, data: threads.map(t => t.name), axisLabel: { fontSize: 10, rotate: threads.length > 4 ? 30 : 0 } },
			yAxis: { type: 'value' as const, name: unit, nameTextStyle: { fontSize: 9 }, axisLabel: { fontSize: 9 } },
			series: [{
				type: 'bar' as const,
				data: threads.map(t => t[field]),
				itemStyle: { color, borderRadius: [4, 4, 0, 0] },
				barMaxWidth: 50,
				label: { show: true, position: 'top' as const, fontSize: 10,
					formatter: (p: any) => p.value.toFixed(1) }
			}],
			grid: { left: 60, right: 20, top: 30, bottom: 50 }
		};
	}

	// ── cycle별 line chart (multi cycle) ──
	function buildLine(threadName: string, field: 'throughput_bps' | 'iops') {
		if (!hasCycles) return null;
		const key = `thread_${threadName}_${field}`;
		const divisor = field === 'throughput_bps' ? 1_048_576 : 1;
		const data = cycleMetrics.map(cm => ({
			cycle: cm.cycle,
			value: (cm.metrics[key] ?? 0) / divisor
		}));
		if (data.every(d => d.value === 0)) return null;
		return {
			cycle: data.map(d => String(d.cycle)),
			values: data.map(d => d.value)
		};
	}

	function buildMultiLineChart(field: 'throughput_bps' | 'iops', unit: string) {
		if (!hasCycles || threads.length === 0) return null;
		const series: any[] = [];
		const palette = ['#5470c6', '#91cc75', '#fac858', '#ee6666', '#73c0de', '#3ba272', '#fc8452', '#9a60b4'];
		for (let i = 0; i < threads.length; i++) {
			const data = buildLine(threads[i].name, field);
			if (!data) continue;
			series.push({
				name: threads[i].name,
				type: 'line' as const,
				data: data.values,
				smooth: true,
				itemStyle: { color: palette[i % palette.length] }
			});
		}
		if (series.length === 0) return null;
		return {
			tooltip: { trigger: 'axis' as const },
			legend: { type: 'scroll' as const, top: 0, textStyle: { fontSize: 10 } },
			xAxis: { type: 'category' as const, name: 'Cycle',
				data: cycleMetrics.map(cm => String(cm.cycle)), axisLabel: { fontSize: 9 } },
			yAxis: { type: 'value' as const, name: unit, nameTextStyle: { fontSize: 9 }, axisLabel: { fontSize: 9 } },
			series,
			grid: { left: 60, right: 20, top: 35, bottom: 40 },
			dataZoom: [{ type: 'inside' as const }]
		};
	}

	const throughputChart = $derived(hasCycles
		? buildMultiLineChart('throughput_bps', 'MB/s')
		: buildBar('throughputMBs', 'MB/s', '#5470c6'));
	const iopsChart = $derived(hasCycles
		? buildMultiLineChart('iops', 'IOPS')
		: buildBar('iops', 'IOPS', '#fc8452'));

	interface TableRow {
		thread: string;
		bytes: string;
		ops: string;
		durationMs: string;
		throughputMBs: string;
		iops: string;
		errors: number;
	}

	const tableData = $derived<TableRow[]>(threads.map(t => ({
		thread: t.name,
		bytes: fmtBytes(t.bytes),
		ops: fmtNum(t.ops, 0),
		durationMs: fmtNum(t.durationMs, 1),
		throughputMBs: fmtNum(t.throughputMBs, 2),
		iops: fmtNum(t.iops, 1),
		errors: t.errors
	})));

	const tableColumns: ColumnDef<TableRow>[] = [
		{ accessorKey: 'thread', header: 'Thread' },
		{ accessorKey: 'bytes', header: 'Bytes' },
		{ accessorKey: 'ops', header: 'Ops' },
		{ accessorKey: 'durationMs', header: 'Duration (ms)' },
		{ accessorKey: 'throughputMBs', header: 'Throughput (MB/s)' },
		{ accessorKey: 'iops', header: 'IOPS' },
		{ accessorKey: 'errors', header: 'Errors' },
	];
</script>

<div class="space-y-3">
	<!-- Total summary -->
	{#if totals.bytes > 0 || totals.ops > 0}
		<div class="grid grid-cols-2 md:grid-cols-5 gap-2 rounded border bg-muted/30 px-3 py-2">
			<div>
				<div class="text-[9px] text-muted-foreground">Total Bytes</div>
				<div class="text-xs font-semibold">{fmtBytes(totals.bytes)}</div>
			</div>
			<div>
				<div class="text-[9px] text-muted-foreground">Total Ops</div>
				<div class="text-xs font-semibold">{fmtNum(totals.ops, 0)}</div>
			</div>
			<div>
				<div class="text-[9px] text-muted-foreground">Duration</div>
				<div class="text-xs font-semibold">{fmtNum(totals.durationMs, 1)} ms</div>
			</div>
			<div>
				<div class="text-[9px] text-muted-foreground">Throughput</div>
				<div class="text-xs font-semibold">{fmtNum(totals.throughputMBs, 2)} MB/s</div>
			</div>
			<div>
				<div class="text-[9px] text-muted-foreground">IOPS</div>
				<div class="text-xs font-semibold">{fmtNum(totals.iops, 1)}</div>
			</div>
		</div>
	{/if}

	<!-- Charts -->
	{#if throughputChart || iopsChart}
		<div class="grid grid-cols-1 md:grid-cols-2 gap-3">
			{#if throughputChart}
				<div>
					<div class="text-[10px] text-muted-foreground font-medium mb-1">Throughput per Thread (MB/s)</div>
					<PerfChart option={throughputChart} height="200px" />
				</div>
			{/if}
			{#if iopsChart}
				<div>
					<div class="text-[10px] text-muted-foreground font-medium mb-1">IOPS per Thread</div>
					<PerfChart option={iopsChart} height="200px" />
				</div>
			{/if}
		</div>
	{/if}

	<!-- Thread table -->
	{#if tableData.length > 0}
		<div>
			<h4 class="text-xs font-semibold mb-1">Threads</h4>
			<DataTableShell data={tableData} columns={tableColumns} />
		</div>
	{:else}
		<div class="text-xs text-muted-foreground py-4 text-center">No thread data</div>
	{/if}
</div>
