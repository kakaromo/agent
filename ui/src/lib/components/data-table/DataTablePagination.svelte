<script lang="ts">
	import ChevronsLeft from '@lucide/svelte/icons/chevrons-left';
	import ChevronsRight from '@lucide/svelte/icons/chevrons-right';
	import ChevronLeft from '@lucide/svelte/icons/chevron-left';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import * as Select from '$lib/components/ui/select';
	import { Button } from '$lib/components/ui/button';
	import type { Table } from '@tanstack/table-core';
	import type { ServerSideConfig } from './types.js';

	interface Props {
		table: Table<any>;
		pageSizeOptions?: number[];
		showSelectedCount?: boolean;
		serverSide?: ServerSideConfig;
	}

	const {
		table,
		pageSizeOptions = [10, 20, 50, 100],
		showSelectedCount = false,
		serverSide
	}: Props = $props();

	const selectedCount = $derived(table.getFilteredSelectedRowModel?.()?.rows?.length ?? 0);
	const totalCount = $derived(serverSide ? serverSide.totalItems : (table.getFilteredRowModel?.()?.rows?.length ?? table.getCoreRowModel().rows.length));
	const pageIndex = $derived(table.getState().pagination.pageIndex);
	const pageSize = $derived(table.getState().pagination.pageSize);
</script>

<div class="flex items-center justify-between text-xs text-muted-foreground pt-2 border-t">
	<!-- Left: Row info -->
	<div class="flex items-center gap-4">
		{#if showSelectedCount && selectedCount > 0}
			<span class="font-medium text-foreground">
				{selectedCount} of {totalCount} selected
			</span>
		{:else if totalCount > 0}
			{@const start = pageIndex * pageSize + 1}
			{@const end = Math.min((pageIndex + 1) * pageSize, totalCount)}
			<span>{start}–{end} of {totalCount}</span>
		{:else}
			<span>0 rows</span>
		{/if}
	</div>

	<!-- Right: Page size + Navigation -->
	<div class="flex items-center gap-4">
		<div class="flex items-center gap-2">
			<span class="hidden sm:inline">Rows</span>
			<Select.Root
				type="single"
				value={String(pageSize)}
				onValueChange={(v) => table.setPageSize(Number(v))}
			>
				<Select.Trigger class="h-7 w-[60px] text-xs">
					{pageSize}
				</Select.Trigger>
				<Select.Content>
					{#each pageSizeOptions as size}
						<Select.Item value={String(size)}>{size}</Select.Item>
					{/each}
				</Select.Content>
			</Select.Root>
		</div>

		<div class="flex items-center gap-0.5">
			<Button
				variant="outline"
				size="icon"
				class="size-7"
				onclick={() => table.setPageIndex(0)}
				disabled={!table.getCanPreviousPage()}
				aria-label="First page"
			>
				<ChevronsLeft class="size-3.5" />
			</Button>
			<Button
				variant="outline"
				size="icon"
				class="size-7"
				onclick={() => table.previousPage()}
				disabled={!table.getCanPreviousPage()}
				aria-label="Previous page"
			>
				<ChevronLeft class="size-3.5" />
			</Button>
			<span class="mx-2 tabular-nums">
				{pageIndex + 1} / {table.getPageCount()}
			</span>
			<Button
				variant="outline"
				size="icon"
				class="size-7"
				onclick={() => table.nextPage()}
				disabled={!table.getCanNextPage()}
				aria-label="Next page"
			>
				<ChevronRight class="size-3.5" />
			</Button>
			<Button
				variant="outline"
				size="icon"
				class="size-7"
				onclick={() => table.setPageIndex(table.getPageCount() - 1)}
				disabled={!table.getCanNextPage()}
				aria-label="Last page"
			>
				<ChevronsRight class="size-3.5" />
			</Button>
		</div>
	</div>
</div>
