<script lang="ts">
	import PerfChart from '$lib/components/perf-chart/PerfChart.svelte';
	import DataTableShell from '$lib/components/DataTableShell.svelte';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { type ColumnDef } from '@tanstack/table-core';
	import type { CycleStepMetrics } from './types.js';

	interface Props {
		metrics: Record<string, number>;
		cycleMetrics?: CycleStepMetrics[];
	}

	let { metrics, cycleMetrics = [] }: Props = $props();

	const hasCycles = $derived(cycleMetrics.length > 1);

	// ── RW 분리 ──
	type RW = 'read' | 'write' | 'other';

	function classifyRw(key: string): RW {
		if (key.startsWith('read_')) return 'read';
		if (key.startsWith('write_')) return 'write';
		return 'other';
	}

	function stripRw(key: string): string {
		if (key.startsWith('read_')) return key.slice(5);
		if (key.startsWith('write_')) return key.slice(6);
		return key;
	}

	// ── 단위 변환 ──
	const UNIT_MAP: Record<string, { label: string; unit: string; divisor: number }> = {
		iops: { label: 'IOPS', unit: 'KIOPS', divisor: 1000 },
		iops_mean: { label: 'IOPS Mean', unit: 'KIOPS', divisor: 1000 },
		bw_kb: { label: 'Bandwidth', unit: 'MiB/s', divisor: 1024 },
		bw_bytes: { label: 'Bandwidth', unit: 'MiB/s', divisor: 1048576 },
		io_bytes: { label: 'Total IO', unit: 'MiB', divisor: 1048576 },
	};

	function getUnit(name: string): { unit: string; divisor: number } {
		const m = UNIT_MAP[name];
		if (m) return { unit: m.unit, divisor: m.divisor };
		if (name.includes('_ns_')) return { unit: 'ms', divisor: 1_000_000 };
		if (name.includes('iops')) return { unit: 'KIOPS', divisor: 1000 };
		if (name.includes('bw_kb')) return { unit: 'MiB/s', divisor: 1024 };
		return { unit: '', divisor: 1 };
	}

	function convert(value: number, name: string): number {
		return value / getUnit(name).divisor;
	}

	function fmt(value: number, name: string): string {
		const v = convert(value, name);
		return v >= 100 ? v.toLocaleString('en-US', { maximumFractionDigits: 1 }) : v >= 1 ? v.toFixed(2) : v.toFixed(3);
	}

	// ── Chart metrics 자동 감지 ──
	const chartDefs = $derived.by(() => {
		const keys = new Set(Object.keys(metrics).map(k => stripRw(k)));
		const defs: { name: string; label: string; unit: string }[] = [];
		if (keys.has('iops')) defs.push({ name: 'iops', label: 'IOPS', unit: 'KIOPS' });
		if (keys.has('bw_kb')) defs.push({ name: 'bw_kb', label: 'Bandwidth', unit: 'MiB/s' });
		else if (keys.has('bw_bytes')) defs.push({ name: 'bw_bytes', label: 'Bandwidth', unit: 'MiB/s' });
		// tiotest/iozone
		for (const prefix of ['seq_write', 'seq_read', 'rand_write', 'rand_read']) {
			if (keys.has(`${prefix}_mb_sec`)) defs.push({ name: `${prefix}_mb_sec`, label: `${prefix} Throughput`, unit: 'MiB/s' });
		}
		return defs;
	});

	const availableRw = $derived.by(() => {
		const rws = new Set(Object.keys(metrics).map(k => classifyRw(k)));
		const dirs: RW[] = [];
		if (rws.has('read')) dirs.push('read');
		if (rws.has('write')) dirs.push('write');
		if (rws.has('other')) dirs.push('other');
		return dirs;
	});

	let activeRw = $state<string>('read');

	// ── Bar chart (single cycle) ──
	function buildBarChart(metricName: string, rw: RW) {
		const { unit, divisor } = getUnit(metricName);
		const rwKeys = Object.entries(metrics)
			.filter(([k]) => classifyRw(k) === rw && stripRw(k) === metricName);
		if (rwKeys.length === 0) return null;

		const value = rwKeys[0][1] / divisor;
		return {
			tooltip: { trigger: 'axis' as const },
			xAxis: { type: 'category' as const, data: [UNIT_MAP[metricName]?.label ?? metricName], axisLabel: { fontSize: 10 } },
			yAxis: { type: 'value' as const, name: unit, nameTextStyle: { fontSize: 9 }, axisLabel: { fontSize: 9 } },
			series: [{ type: 'bar' as const, data: [{ value, itemStyle: { color: rw === 'read' ? '#5470c6' : '#fc8452', borderRadius: [4, 4, 0, 0] } }], barMaxWidth: 60,
				label: { show: true, position: 'top' as const, fontSize: 10, formatter: (p: any) => p.value.toFixed(1) } }],
			grid: { left: 60, right: 20, top: 30, bottom: 40 }
		};
	}

	// ── Line chart (multi cycle) ──
	function buildLineChart(metricName: string, rw: RW) {
		if (cycleMetrics.length <= 1) return null;
		const { unit, divisor } = getUnit(metricName);
		const key = `${rw}_${metricName}`;
		const data = cycleMetrics.map(cm => ({
			cycle: cm.cycle,
			value: (cm.metrics[key] ?? 0) / divisor
		}));
		if (data.every(d => d.value === 0)) return null;

		return {
			tooltip: { trigger: 'axis' as const },
			xAxis: { type: 'category' as const, name: 'Cycle', data: data.map(d => String(d.cycle)), axisLabel: { fontSize: 9 } },
			yAxis: { type: 'value' as const, name: unit, nameTextStyle: { fontSize: 9 }, axisLabel: { fontSize: 9 } },
			series: [{ type: 'line' as const, data: data.map(d => d.value), smooth: true,
				itemStyle: { color: rw === 'read' ? '#5470c6' : '#fc8452' }, areaStyle: { opacity: 0.1 } }],
			grid: { left: 60, right: 20, top: 30, bottom: 40 },
			dataZoom: [{ type: 'inside' as const }]
		};
	}

	// ── DataTable ──
	const CATEGORIES: Record<string, string[]> = {
		'IOPS': ['iops', 'iops_min', 'iops_max', 'iops_mean', 'iops_stddev'],
		'Bandwidth': ['bw_kb', 'bw_bytes', 'bw_min_kb', 'bw_max_kb', 'bw_mean_kb', 'bw_stddev_kb'],
		'Latency': ['clat_ns_mean', 'clat_ns_min', 'clat_ns_max', 'lat_ns_mean', 'lat_ns_min', 'lat_ns_max'],
		'I/O': ['io_bytes', 'total_ios', 'runtime_ms'],
	};

	interface TableRow { metric: string; value: string; unit: string }

	function getTableData(rw: RW): TableRow[] {
		const rows: TableRow[] = [];
		for (const [k, v] of Object.entries(metrics)) {
			if (classifyRw(k) !== rw) continue;
			const name = stripRw(k);
			const { unit } = getUnit(name);
			rows.push({ metric: name, value: fmt(v, name), unit });
		}
		return rows;
	}

	const tableColumns: ColumnDef<TableRow>[] = [
		{ accessorKey: 'metric', header: 'Metric' },
		{ accessorKey: 'value', header: 'Value' },
		{ accessorKey: 'unit', header: 'Unit' },
	];
</script>

<div class="space-y-3">
	<!-- Charts -->
	{#if chartDefs.length > 0}
		{#if availableRw.length > 1}
			<Tabs.Root bind:value={activeRw}>
				<Tabs.List class="w-fit">
					{#each availableRw.filter(r => r !== 'other') as rw}
						<Tabs.Trigger value={rw} class="text-xs capitalize">{rw}</Tabs.Trigger>
					{/each}
				</Tabs.List>
				{#each availableRw.filter(r => r !== 'other') as rw}
					<Tabs.Content value={rw}>
						<div class="grid grid-cols-1 md:grid-cols-2 gap-3 mt-2">
							{#each chartDefs as def}
								{@const chart = hasCycles ? buildLineChart(def.name, rw) : buildBarChart(def.name, rw)}
								{#if chart}
									<div>
										<div class="text-[10px] text-muted-foreground font-medium mb-1">{def.label} ({def.unit})</div>
										<PerfChart option={chart} height="200px" />
									</div>
								{/if}
							{/each}
						</div>
					</Tabs.Content>
				{/each}
			</Tabs.Root>
		{:else if availableRw.length === 1}
			<div class="grid grid-cols-1 md:grid-cols-2 gap-3">
				{#each chartDefs as def}
					{@const chart = hasCycles ? buildLineChart(def.name, availableRw[0]) : buildBarChart(def.name, availableRw[0])}
					{#if chart}
						<div>
							<div class="text-[10px] text-muted-foreground font-medium mb-1">{def.label} ({def.unit})</div>
							<PerfChart option={chart} height="200px" />
						</div>
					{/if}
				{/each}
			</div>
		{/if}
	{/if}

	<!-- DataTable -->
	{#each availableRw.filter(r => r !== 'other') as rw}
		{@const data = getTableData(rw)}
		{#if data.length > 0}
			<div>
				<h4 class="text-xs font-semibold capitalize mb-1">{rw}</h4>
				<DataTableShell {data} columns={tableColumns} pageSize={20} />
			</div>
		{/if}
	{/each}
</div>
