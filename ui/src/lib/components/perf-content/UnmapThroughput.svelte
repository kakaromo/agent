<script lang="ts">
	import { type ColumnDef } from '@tanstack/table-core';
	import { emptyState } from '$lib/styles/common.js';
	import { DataTable } from '$lib/components/data-table';
	import * as Card from '$lib/components/ui/card';

	interface CycleEntry {
		cycle: number;
		fragmented: number;
		unfragmented: number;
	}

	interface Props {
		data: CycleEntry[];
		tcName: string;
		fw?: string;
	}

	let { data, tcName }: Props = $props();

	interface TableRow {
		label: string;
		[key: string]: string | number;
	}

	const tableRows: TableRow[] = $derived(
		['fragmented', 'unfragmented'].map((key) => {
			const row: TableRow = { label: key };
			for (const entry of data) {
				row[`c${entry.cycle}`] = (entry as any)[key] ?? 0;
			}
			return row;
		})
	);

	const tableColumns: ColumnDef<TableRow, unknown>[] = $derived([
		{ accessorKey: 'label', header: '' },
		...data.map((entry) => ({
			accessorKey: `c${entry.cycle}`,
			header: `cycle${entry.cycle}`,
			cell: ({ row }: { row: any }) => {
				const v = row.original[`c${entry.cycle}`];
				return v != null ? Number(v).toFixed(1) : '—';
			}
		}))
	]);


</script>

<div class="space-y-3">
	{#if data.length === 0}
		<div class="{emptyState}">
			<span class="text-sm">No Unmap Throughput data available</span>
		</div>
	{:else}
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<Card.Content class="p-2">
				<DataTable
					data={tableRows}
					columns={tableColumns}
					showPagination={false}
					compact={true}
					enableColumnVisibility={false}
					enableCellCopy={true}
					getRowId={(row) => row.label}
				/>
			</Card.Content>
		</Card.Root>
	{/if}
</div>
