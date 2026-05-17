<script lang="ts">
	import * as Card from '$lib/components/ui/card';
	import { type ColumnDef } from '@tanstack/table-core';
	import { DataTable } from '$lib/components/data-table';
	import { renderComponent } from '$lib/components/ui/data-table/render-helpers.js';
	import { extractSummary, computeDelta, deltaColorClass, type SummaryMetric } from './compareSummary.js';
	import type { CompareItem } from './CompareItemStrip.svelte';
	import CompareDeltaCell from './CompareDeltaCell.svelte';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import CheckIcon from '@lucide/svelte/icons/check';
	import { toast } from 'svelte-sonner';

	interface Props {
		items: CompareItem[];
		baselineIndex: number;
	}

	let { items, baselineIndex }: Props = $props();

	// Baseline item
	const baseline = $derived(items[baselineIndex] ?? items[0]);
	const otherItems = $derived(items.filter((_, i) => i !== baselineIndex));

	// Extract metrics for baseline
	const baselineMetrics = $derived(
		baseline ? extractSummary(baseline.result.parserId, baseline.result.data, baseline.result.tcName ?? '') : []
	);

	// Pre-compute metrics for all other items
	const otherMetricsMap = $derived(
		new Map(otherItems.map((item) => [
			item.history.id,
			extractSummary(item.result.parserId, item.result.data, item.result.tcName ?? '')
		]))
	);

	// Build flat row data for DataTable
	interface CompareRow {
		key: string;
		metric: string;
		unit: string;
		baselineValue: string;
		[key: string]: string; // dynamic: value_0, delta_0, value_1, delta_1, ...
	}

	const tableData: CompareRow[] = $derived.by(() => {
		if (!baseline || otherItems.length === 0) return [];
		return baselineMetrics
			.filter((m) => m.value !== null)
			.map((metric) => {
				const row: CompareRow = {
					key: metric.key,
					metric: metric.label + (metric.unit ? ` (${metric.unit})` : ''),
					unit: metric.unit,
					baselineValue: formatValue(metric.value, metric.unit)
				};
				otherItems.forEach((item, i) => {
					const itemMetrics = otherMetricsMap.get(item.history.id) ?? [];
					const matched = itemMetrics.find((m) => m.key === metric.key);
					const value = matched?.value ?? null;
					const delta = value !== null && metric.value !== null ? computeDelta(value, metric.value) : null;
					row[`value_${i}`] = formatValue(value, metric.unit);
					row[`delta_${i}`] = delta?.label ?? '—';
					row[`deltaColor_${i}`] = delta ? deltaColorClass(delta.delta, metric.higherIsBetter) : '';
				});
				return row;
			});
	});

	const columns: ColumnDef<CompareRow, unknown>[] = $derived.by(() => {
		const cols: ColumnDef<CompareRow, unknown>[] = [
			{ accessorKey: 'metric', header: 'Metric', enableSorting: true },
			{
				accessorKey: 'baselineValue',
				header: baseline?.label ?? 'Baseline',
				enableSorting: true
			}
		];
		otherItems.forEach((item, i) => {
			cols.push({
				accessorKey: `value_${i}`,
				header: item.label,
				enableSorting: true
			});
			cols.push({
				accessorKey: `delta_${i}`,
				header: 'Delta',
				enableSorting: true,
				cell: ({ row }) =>
					renderComponent(CompareDeltaCell, {
						value: (row.original as any)[`delta_${i}`] ?? '—',
						colorClass: (row.original as any)[`deltaColor_${i}`] ?? ''
					})
			});
		});
		return cols;
	});

	function formatValue(value: number | null, unit: string): string {
		if (value === null) return '—';
		if (Math.abs(value) >= 1000) return `${value.toLocaleString('en-US', { maximumFractionDigits: 1 })}`;
		return value.toFixed(2);
	}

	const hasData = $derived(tableData.length > 0);

	let copied = $state(false);

	async function copyTable() {
		const headers = columns.map(c => typeof c.header === 'string' ? c.header : '');
		const tsvRows = tableData.map((row) =>
			columns.map(c => {
				const key = (c as any).accessorKey as string;
				return key ? (row as any)[key] ?? '' : '';
			}).join('\t')
		);
		const tsv = [headers.join('\t'), ...tsvRows].join('\n');

		try {
			await navigator.clipboard.writeText(tsv);
			copied = true;
			toast.success('테이블이 클립보드에 복사되었습니다');
			setTimeout(() => { copied = false; }, 2000);
		} catch {
			toast.error('복사에 실패했습니다');
		}
	}
</script>

{#if hasData}
	<Card.Root class="gap-0 p-0 overflow-hidden">
		<Card.Header class="border-b px-4 py-2.5 flex flex-row items-center justify-between">
			<Card.Title class="text-xs font-medium text-muted-foreground">
				핵심 지표 비교 — 기준: {baseline?.label ?? ''}
			</Card.Title>
			<button
				class="inline-flex items-center gap-1 px-2 py-1 text-[11px] text-muted-foreground hover:text-foreground rounded border border-transparent hover:border-border transition-colors"
				onclick={copyTable}
				title="테이블 전체 복사 (Excel 붙여넣기 가능)"
			>
				{#if copied}
					<CheckIcon class="size-3 text-green-600" />
					복사됨
				{:else}
					<CopyIcon class="size-3" />
					전체 복사
				{/if}
			</button>
		</Card.Header>
		<Card.Content class="p-2">
			<DataTable
				data={tableData}
				columns={columns}
				showPagination={false}
				compact={true}
				enableColumnVisibility={false}
				enableCellCopy={true}
				getRowId={(row) => row.key}
			/>
		</Card.Content>
	</Card.Root>
{:else}
	<Card.Root class="gap-0 p-0 overflow-hidden">
		<Card.Content class="p-6">
			<p class="text-sm text-muted-foreground text-center">
				비교할 수 있는 지표가 없습니다.
			</p>
		</Card.Content>
	</Card.Root>
{/if}
