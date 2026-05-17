<script lang="ts">
	import { untrack } from 'svelte';
	import type { AnalysisResult, TabInfo } from './PerfGenerator.types';
	import { PerfChart } from '$lib/components/perf-chart';
	import { DataTable } from '$lib/components/data-table';
	import * as Card from '$lib/components/ui/card';
	import { type ColumnDef } from '@tanstack/table-core';
	import type { EChartsOption } from 'echarts';
	import { baseChartOption } from '$lib/components/perf-content/perfChartUtils';
	import { btnBase, btnActive, btnInactive, groupClass } from '$lib/components/perf-content/perfStyles';

	interface Props {
		analysis: AnalysisResult;
		mergedTabs: TabInfo[];
		parsedData: unknown;
		xAxisUnit: string;
		componentName: string;
	}

	let { analysis, mergedTabs, parsedData, xAxisUnit, componentName }: Props = $props();

	let activeTab = $state('');
	let chartType = $state<'line' | 'scatter'>('line');

	// Reset active tab when tabs change
	$effect(() => {
		const tabs = mergedTabs;
		untrack(() => {
			if (tabs.length > 0 && !tabs.some((t) => t.key === activeTab)) {
				activeTab = tabs[0].key;
			}
		});
	});

	const currentTab = $derived(mergedTabs.find((t) => t.key === activeTab));

	// Extract data for current tab
	const tabData = $derived.by(() => {
		if (!parsedData || !currentTab) return { cycles: [] as Record<string, unknown>[] };

		if (analysis.shape === 'object-of-arrays') {
			const obj = parsedData as Record<string, unknown>;
			// Find the matching key (case-insensitive)
			const matchKey = Object.keys(obj).find((k) => k.toLowerCase() === activeTab);
			if (matchKey && Array.isArray(obj[matchKey])) {
				return { cycles: obj[matchKey] as Record<string, unknown>[] };
			}
			return { cycles: [] as Record<string, unknown>[] };
		}

		if (analysis.shape === 'array-of-objects') {
			const arr = parsedData as Record<string, unknown>[];
			return { cycles: arr };
		}

		return { cycles: [] as Record<string, unknown>[] };
	});

	// Find cycle field accessor
	const cycleAccessor = $derived(
		analysis.cycleField ? analysis.cycleField.path[analysis.cycleField.path.length - 1] : null
	);

	// Get data fields and stat fields for current tab
	const dataFields = $derived(currentTab?.fields.filter((f) => f.role === 'data') ?? []);
	const statFields = $derived(currentTab?.fields.filter((f) => f.role === 'stat') ?? []);
	const hasDataFields = $derived(dataFields.length > 0);

	// For array-of-objects, need to access nested tab data
	function getNestedValue(obj: Record<string, unknown>, path: string[]): unknown {
		let current: unknown = obj;
		for (const key of path) {
			if (current === null || current === undefined || typeof current !== 'object') return undefined;
			current = (current as Record<string, unknown>)[key];
		}
		return current;
	}

	// Build chart series data
	const chartSeriesData = $derived.by(() => {
		if (!hasDataFields || !currentTab) return [];

		const firstDataField = dataFields[0];
		const cycles = tabData.cycles;

		return cycles.map((entry, idx) => {
			const cycleLabel = cycleAccessor ? `Cycle ${entry[cycleAccessor]}` : `Entry ${idx + 1}`;

			// For object-of-arrays shape, data is directly on the entry
			// For array-of-objects, data may be nested under the tab key
			let values: number[];
			if (analysis.shape === 'object-of-arrays') {
				values = (getNestedValue(entry, firstDataField.path) as number[]) ?? [];
			} else {
				// array-of-objects: tab data might be nested
				const tabKey = Object.keys(entry).find((k) => k.toLowerCase() === activeTab);
				if (tabKey && typeof entry[tabKey] === 'object' && entry[tabKey] !== null) {
					const nested = entry[tabKey] as Record<string, unknown>;
					values = (getNestedValue(nested, firstDataField.path.slice(firstDataField.path[0] === currentTab.key ? 1 : 0)) as number[]) ?? [];
				} else {
					values = (getNestedValue(entry, firstDataField.path) as number[]) ?? [];
				}
			}

			return { label: cycleLabel, data: values };
		});
	});

	// X-axis indices
	const indices = $derived.by(() => {
		const maxLen = chartSeriesData.reduce((m, s) => Math.max(m, s.data.length), 0);
		return Array.from({ length: maxLen }, (_, i) => i);
	});

	// Chart option
	const chartOption: EChartsOption = $derived.by(() => {
		const base = baseChartOption(`${componentName} - ${currentTab?.label ?? ''}`, undefined);
		return {
			...base,
			xAxis: {
				type: 'category',
				data: indices.map(String),
				name: xAxisUnit,
				nameLocation: 'center',
				nameGap: 25
			},
			yAxis: {
				type: 'value',
				name: currentTab?.yAxisUnit ?? 'Value',
				nameLocation: 'center',
				nameRotate: 90,
				nameGap: 50
			},
			series: chartSeriesData.map((s) => ({
				name: s.label,
				type: chartType,
				data: s.data,
				symbolSize: chartType === 'scatter' ? 4 : undefined,
				smooth: false
			}))
		};
	});

	// Stats table
	type StatRow = Record<string, unknown>;

	const statsRows = $derived.by((): StatRow[] => {
		const cycles = tabData.cycles;
		return cycles.map((entry, idx) => {
			const row: StatRow = {};

			// Cycle column
			if (cycleAccessor) {
				row['_cycle'] = `Cycle ${entry[cycleAccessor]}`;
			} else {
				row['_cycle'] = `Entry ${idx + 1}`;
			}

			// Stat fields
			for (const field of statFields) {
				const key = field.path.join('.');
				let val: unknown;

				if (analysis.shape === 'object-of-arrays') {
					val = getNestedValue(entry, field.path);
				} else {
					const tabKey = Object.keys(entry).find((k) => k.toLowerCase() === activeTab);
					if (tabKey && typeof entry[tabKey] === 'object' && entry[tabKey] !== null) {
						val = getNestedValue(entry[tabKey] as Record<string, unknown>, field.path.slice(field.path[0] === currentTab?.key ? 1 : 0));
					} else {
						val = getNestedValue(entry, field.path);
					}
				}

				row[key] = typeof val === 'number' ? val : 0;
			}

			return row;
		});
	});

	const statsColumns: ColumnDef<StatRow, unknown>[] = $derived([
		{
			accessorKey: '_cycle',
			header: 'Cycle',
			cell: ({ row }) => String(row.original['_cycle'] ?? '')
		},
		...statFields.map((f) => {
			const key = f.path.join('.');
			return {
				accessorKey: key,
				header: f.path[f.path.length - 1],
				cell: ({ row }: { row: { original: StatRow } }) => {
					const v = row.original[key];
					return typeof v === 'number' ? v.toFixed(2) : String(v ?? '');
				}
			};
		})
	]);
</script>

<div class="space-y-3">
	{#if mergedTabs.length === 0}
		<div class="flex items-center justify-center h-48 text-muted-foreground text-sm">
			Paste JSON to see preview
		</div>
	{:else}
		<!-- Sample data banner -->
		<div class="text-[10px] text-muted-foreground bg-muted/50 px-2 py-1 rounded text-center">
			Preview — sample data
		</div>

		<!-- Toolbar -->
		<div class="flex items-center gap-1.5 flex-wrap">
			{#if mergedTabs.length > 1}
				<div class={groupClass}>
					{#each mergedTabs as tab (tab.key)}
						<button
							class="{btnBase} {activeTab === tab.key ? btnActive : btnInactive}"
							onclick={() => (activeTab = tab.key)}
						>
							{tab.label}
						</button>
					{/each}
				</div>
				<div class="w-px h-5 bg-border"></div>
			{/if}

			{#if hasDataFields}
				<div class={groupClass}>
					<button
						class="{btnBase} {chartType === 'line' ? btnActive : btnInactive}"
						onclick={() => (chartType = 'line')}
					>Line</button>
					<button
						class="{btnBase} {chartType === 'scatter' ? btnActive : btnInactive}"
						onclick={() => (chartType = 'scatter')}
					>Scatter</button>
				</div>
			{/if}
		</div>

		<!-- Chart -->
		{#if hasDataFields && chartSeriesData.length > 0 && indices.length > 0}
			<Card.Root class="gap-0 p-0 overflow-hidden">
				<Card.Content class="p-2">
					<PerfChart option={chartOption} height="300px" />
				</Card.Content>
			</Card.Root>
		{/if}

		<!-- Statistics Table -->
		{#if statsRows.length > 0 && statsColumns.length > 1}
			<Card.Root class="gap-0 p-0 overflow-hidden">
				<Card.Header class="border-b px-4 py-2">
					<Card.Title class="text-xs font-medium text-muted-foreground">Statistics</Card.Title>
				</Card.Header>
				<Card.Content class="p-2">
					<DataTable
						data={statsRows}
						columns={statsColumns}
						showPagination={false}
						compact={true}
						enableColumnVisibility={false}
						getRowId={(row) => String(row['_cycle'])}
					/>
				</Card.Content>
			</Card.Root>
		{/if}
	{/if}
</div>
