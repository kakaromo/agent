<script lang="ts">
	import { type ColumnDef } from '@tanstack/table-core';
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
	}

	interface Props {
		data: Record<string, CycleEntry[]>;
		tcName: string;
		fw?: string;
		yAxisMax?: Record<string, number>;
	}

	let { data, tcName, fw, yAxisMax }: Props = $props();

	// Cycle view uses global max (all idle keys), idle view uses per-key max
	const globalYAxisMax = $derived(
		yAxisMax ? Math.max(...Object.values(yAxisMax)) : undefined
	);

	let chartRef: ReturnType<typeof PerfChartType> | undefined = $state();
	let viewMode = $state<'cycle' | 'idle'>('cycle');
	let activeCycleIdx = $state(0);
	let activeIdleKey = $state('');
	let showRawData = $state(true);

	// Sort idle keys numerically (idle 0s, idle 1s, idle 5s, idle 10s, ...)
	const idleKeys = $derived(
		Object.keys(data).sort((a, b) => {
			const na = parseInt(a.replace(/\D/g, '')) || 0;
			const nb = parseInt(b.replace(/\D/g, '')) || 0;
			return na - nb;
		})
	);

	// All unique cycles across all idle keys
	const allCycles = $derived(() => {
		const set = new Set<number>();
		for (const entries of Object.values(data)) {
			for (const e of entries) set.add(e.cycle);
		}
		return Array.from(set).sort((a, b) => a - b);
	});

	$effect(() => {
		if (activeIdleKey === '' || !idleKeys.includes(activeIdleKey)) {
			if (idleKeys.length > 0) activeIdleKey = idleKeys[0];
		}
	});

	$effect(() => {
		if (activeCycleIdx >= allCycles().length) activeCycleIdx = 0;
	});

	const activeCycle = $derived(allCycles()[activeCycleIdx]);

	// ===== Cycle view: selected cycle, all idle keys as series =====
	const cycleViewIndices = $derived(() => {
		let maxLen = 0;
		for (const key of idleKeys) {
			const entry = data[key]?.find((e) => e.cycle === activeCycle);
			if (entry) maxLen = Math.max(maxLen, entry.data?.length ?? 0);
		}
		return Array.from({ length: maxLen }, (_, i) => i + 1);
	});

	const cycleChartOption: EChartsOption = $derived({
		...baseChartOption(tcName, fw),
		xAxis: {
			type: 'category',
			data: cycleViewIndices().map(String),
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
			...(globalYAxisMax != null ? { max: globalYAxisMax } : {})
		},
		series: idleKeys.map((key) => {
			const entry = data[key]?.find((e) => e.cycle === activeCycle);
			return {
				name: key,
				type: 'line' as const,
				data: entry?.data ?? [],
				smooth: false
			};
		})
	});

	// Cycle view stats: rows = min/max/avg, columns = idle keys
	interface StatRow {
		label: string;
		[key: string]: string | number;
	}

	const cycleStatRows: StatRow[] = $derived(
		['min', 'max', 'avg'].map((stat) => {
			const row: StatRow = { label: stat };
			for (const key of idleKeys) {
				const entry = data[key]?.find((e) => e.cycle === activeCycle);
				row[key] = entry ? (entry as any)[stat] ?? 0 : 0;
			}
			return row;
		})
	);

	const cycleStatColumns: ColumnDef<StatRow, unknown>[] = $derived([
		{ accessorKey: 'label', header: '' },
		...idleKeys.map((key) => ({
			accessorKey: key,
			header: key,
			cell: ({ row }: { row: any }) => {
				const v = row.original[key];
				return v != null ? Number(v).toFixed(2) : '—';
			}
		}))
	]);

	// Cycle view throughput: rows = index, columns = idle keys
	type DataRow = Record<string, number>;

	const cycleDataRows: DataRow[] = $derived(
		cycleViewIndices().map((idx, i) => {
			const row: DataRow = { index: idx };
			for (const key of idleKeys) {
				const entry = data[key]?.find((e) => e.cycle === activeCycle);
				row[key] = entry?.data[i] ?? 0;
			}
			return row;
		})
	);

	const cycleDataColumns: ColumnDef<DataRow, unknown>[] = $derived([
		{ accessorKey: 'index', header: '', enableSorting: true },
		...idleKeys.map((key) => ({
			accessorKey: key,
			header: key,
			enableSorting: true,
			cell: ({ row }: { row: any }) => {
				const v = row.original[key];
				return v != null ? Number(v).toFixed(2) : '—';
			}
		}))
	]);

	// ===== Idle view: selected idle key, all cycles as series =====
	const currentIdleCycles = $derived(data[activeIdleKey] ?? []);

	const idleViewIndices = $derived(() => {
		const maxLen = currentIdleCycles.reduce((m, c) => Math.max(m, c.data?.length ?? 0), 0);
		return Array.from({ length: maxLen }, (_, i) => i + 1);
	});

	const idleChartOption: EChartsOption = $derived({
		...baseChartOption(`${tcName} ${activeIdleKey}`, fw),
		xAxis: {
			type: 'category',
			data: idleViewIndices().map(String)
		},
		yAxis: {
			type: 'value',
			name: 'MB/s',
			nameLocation: 'center',
			nameRotate: 90,
			nameGap: 45,
			...(yAxisMax?.[activeIdleKey] != null ? { max: yAxisMax[activeIdleKey] } : {})
		},
		series: currentIdleCycles.map((entry) => ({
			name: `cycle${entry.cycle}`,
			type: 'line' as const,
			data: entry.data,
			smooth: false
		}))
	});

	const idleStatRows: StatRow[] = $derived(
		['min', 'max', 'avg'].map((stat) => {
			const row: StatRow = { label: stat };
			for (const entry of currentIdleCycles) {
				row[`c${entry.cycle}`] = (entry as any)[stat] ?? 0;
			}
			return row;
		})
	);

	const idleStatColumns: ColumnDef<StatRow, unknown>[] = $derived([
		{ accessorKey: 'label', header: '' },
		...currentIdleCycles.map((entry) => ({
			accessorKey: `c${entry.cycle}`,
			header: `cycle${entry.cycle}`,
			cell: ({ row }: { row: any }) => {
				const v = row.original[`c${entry.cycle}`];
				return v != null ? Number(v).toFixed(2) : '—';
			}
		}))
	]);

	const idleDataRows: DataRow[] = $derived(
		idleViewIndices().map((idx, i) => {
			const row: DataRow = { index: idx };
			for (const entry of currentIdleCycles) {
				row[`c${entry.cycle}`] = entry.data[i] ?? 0;
			}
			return row;
		})
	);

	const idleDataColumns: ColumnDef<DataRow, unknown>[] = $derived([
		{ accessorKey: 'index', header: '', enableSorting: true },
		...currentIdleCycles.map((entry) => ({
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
		<!-- View mode toggle -->
		<div class={groupClass}>
			<button
				class="{btnBase} {viewMode === 'cycle' ? btnActive : btnInactive}"
				onclick={() => (viewMode = 'cycle')}
			>By Cycle</button>
			<button
				class="{btnBase} {viewMode === 'idle' ? btnActive : btnInactive}"
				onclick={() => (viewMode = 'idle')}
			>By Idle</button>
		</div>

		{#if viewMode === 'cycle'}
			<!-- Cycle tabs -->
			{#if allCycles().length > 1}
				<div class={groupClass}>
					{#each allCycles() as cycle, idx (cycle)}
						<button
							class="{btnBase} {activeCycleIdx === idx ? btnActive : btnInactive}"
							onclick={() => (activeCycleIdx = idx)}
						>cycle{cycle}</button>
					{/each}
				</div>
			{/if}
		{:else}
			<!-- Idle tabs -->
			{#if idleKeys.length > 1}
				<div class={groupClass}>
					{#each idleKeys as key (key)}
						<button
							class="{btnBase} {activeIdleKey === key ? btnActive : btnInactive}"
							onclick={() => (activeIdleKey = key)}
						>{key}</button>
					{/each}
				</div>
			{/if}
		{/if}

	</div>

	{#if viewMode === 'cycle'}
		<!-- Cycle view -->
		<h3 class="text-sm font-semibold">Cycle {activeCycle}</h3>

		<Card.Root class="gap-0 p-0 overflow-hidden">
			<Card.Content class="p-2">
				<PerfChart bind:this={chartRef} option={cycleChartOption} height="420px" />
			</Card.Content>
		</Card.Root>

		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Statistics" />
			<Card.Content class="p-2">
				<DataTable
					data={cycleStatRows}
					columns={cycleStatColumns}
					showPagination={false}
					compact={true}
					enableColumnVisibility={false}
					enableCellCopy={true}
					getRowId={(row) => row.label}
				/>
			</Card.Content>
		</Card.Root>

		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Throughput ({cycleDataRows.length} points)">
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
						data={cycleDataRows}
						columns={cycleDataColumns}
						compact={true}
						enableColumnVisibility={false}
						scrollHeight="320px"
						enableCellCopy={true}
						getRowId={(row) => String(row.index)}
					/>
				</Card.Content>
			{/if}
		</Card.Root>
	{:else}
		<!-- Idle view -->
		<h3 class="text-sm font-semibold">{activeIdleKey}</h3>

		<Card.Root class="gap-0 p-0 overflow-hidden">
			<Card.Content class="p-2">
				<PerfChart bind:this={chartRef} option={idleChartOption} height="420px" />
			</Card.Content>
		</Card.Root>

		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Statistics" />
			<Card.Content class="p-2">
				<DataTable
					data={idleStatRows}
					columns={idleStatColumns}
					showPagination={false}
					compact={true}
					enableColumnVisibility={false}
					enableCellCopy={true}
					getRowId={(row) => row.label}
				/>
			</Card.Content>
		</Card.Root>

		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Throughput ({idleDataRows.length} points)">
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
						data={idleDataRows}
						columns={idleDataColumns}
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
