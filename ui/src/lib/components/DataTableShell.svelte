<script lang="ts" generics="TData">
	import {
		type ColumnDef,
		type PaginationState,
		type SortingState,
		type ColumnFiltersState,
		getCoreRowModel,
		getFilteredRowModel,
		getPaginationRowModel,
		getSortedRowModel
	} from '@tanstack/table-core';
	import { createSvelteTable, FlexRender } from '$lib/components/ui/data-table/index.js';
	import * as Select from '$lib/components/ui/select/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
	import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
	import ArrowUpDownIcon from '@lucide/svelte/icons/arrow-up-down';
	import SearchXIcon from '@lucide/svelte/icons/search-x';
	import ChevronsLeftIcon from '@lucide/svelte/icons/chevrons-left';
	import ChevronsRightIcon from '@lucide/svelte/icons/chevrons-right';
	import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';

	let {
		data,
		columns,
		filterColumn = '',
		filterPlaceholder = 'Filter...'
	}: {
		data: TData[];
		columns: ColumnDef<TData, unknown>[];
		filterColumn?: string;
		filterPlaceholder?: string;
	} = $props();

	let sorting = $state<SortingState>([]);
	let columnFilters = $state<ColumnFiltersState>([]);
	let pagination = $state<PaginationState>({ pageIndex: 0, pageSize: 20 });

	const pageSizeOptions = [10, 20, 50, 100];

	const table = createSvelteTable({
		get data() { return data; },
		get columns() { return columns; },
		state: {
			get sorting() { return sorting; },
			get columnFilters() { return columnFilters; },
			get pagination() { return pagination; }
		},
		onSortingChange(updater) {
			sorting = typeof updater === 'function' ? updater(sorting) : updater;
		},
		onColumnFiltersChange(updater) {
			columnFilters = typeof updater === 'function' ? updater(columnFilters) : updater;
		},
		onPaginationChange(updater) {
			pagination = typeof updater === 'function' ? updater(pagination) : updater;
		},
		getCoreRowModel: getCoreRowModel(),
		getSortedRowModel: getSortedRowModel(),
		getFilteredRowModel: getFilteredRowModel(),
		getPaginationRowModel: getPaginationRowModel()
	});

	let scrollContainer: HTMLDivElement;
	let showScrollHint = $state(false);

	function checkOverflow() {
		if (scrollContainer) {
			showScrollHint = scrollContainer.scrollWidth > scrollContainer.clientWidth;
		}
	}

	$effect(() => {
		data;
		requestAnimationFrame(checkOverflow);
	});
</script>

<svelte:window onresize={checkOverflow} />

<div class="space-y-3">
	<!-- Toolbar -->
	<div class="flex items-center justify-between gap-2">
		{#if filterColumn}
			<Input
				placeholder={filterPlaceholder}
				value={(table.getColumn(filterColumn)?.getFilterValue() as string) ?? ''}
				oninput={(e) => table.getColumn(filterColumn)?.setFilterValue(e.currentTarget.value)}
				class="h-8 max-w-sm text-xs"
			/>
		{:else}
			<div></div>
		{/if}
		<div class="flex items-center gap-2 text-xs text-muted-foreground">
			<span class="hidden sm:inline">Rows</span>
			<Select.Root type="single" value={String(pagination.pageSize)} onValueChange={(v) => {
				pagination = { pageIndex: 0, pageSize: Number(v) };
			}}>
				<Select.Trigger class="h-7 w-[60px] text-xs">
					{pagination.pageSize}
				</Select.Trigger>
				<Select.Content>
					{#each pageSizeOptions as size}
						<Select.Item value={String(size)}>{size}</Select.Item>
					{/each}
				</Select.Content>
			</Select.Root>
		</div>
	</div>

	<!-- Table -->
	<div class="relative">
		{#if showScrollHint}
			<div class="pointer-events-none absolute right-0 top-0 bottom-0 z-10 w-6 bg-gradient-to-l from-background to-transparent"></div>
		{/if}
		<div
			class="overflow-x-auto rounded-md border"
			bind:this={scrollContainer}
			onscroll={() => {
				if (scrollContainer) {
					const atEnd = scrollContainer.scrollLeft + scrollContainer.clientWidth >= scrollContainer.scrollWidth - 2;
					showScrollHint = !atEnd && scrollContainer.scrollWidth > scrollContainer.clientWidth;
				}
			}}
		>
			<table class="w-full text-xs">
				<thead class="sticky top-0 z-20 bg-muted/50 backdrop-blur-sm">
					{#each table.getHeaderGroups() as headerGroup (headerGroup.id)}
						<tr>
							{#each headerGroup.headers as header (header.id)}
								<th class="px-3 py-2 text-left font-medium text-muted-foreground whitespace-nowrap">
									{#if !header.isPlaceholder}
										{#if header.column.getCanSort()}
											<button
												class="inline-flex items-center gap-1 transition-colors hover:text-foreground"
												onclick={header.column.getToggleSortingHandler()}
											>
												<FlexRender content={header.column.columnDef.header} context={header.getContext()} />
												{#if header.column.getIsSorted() === 'asc'}
													<ArrowUpIcon class="size-3 text-primary" />
												{:else if header.column.getIsSorted() === 'desc'}
													<ArrowDownIcon class="size-3 text-primary" />
												{:else}
													<ArrowUpDownIcon class="size-3 opacity-25" />
												{/if}
											</button>
										{:else}
											<FlexRender content={header.column.columnDef.header} context={header.getContext()} />
										{/if}
									{/if}
								</th>
							{/each}
						</tr>
					{/each}
				</thead>
				<tbody class="divide-y divide-border">
					{#if table.getRowModel().rows.length}
						{#each table.getRowModel().rows as row (row.id)}
							<tr class="transition-colors hover:bg-muted/30">
								{#each row.getVisibleCells() as cell (cell.id)}
									<td class="px-3 py-1.5 whitespace-nowrap">
										<FlexRender content={cell.column.columnDef.cell} context={cell.getContext()} />
									</td>
								{/each}
							</tr>
						{/each}
					{:else}
						<tr>
							<td colspan={columns.length} class="px-3 py-8 text-center text-muted-foreground">
								No results.
							</td>
						</tr>
					{/if}
				</tbody>
			</table>
		</div>
	</div>

	<!-- Pagination -->
	<div class="flex items-center justify-between text-xs text-muted-foreground">
		<div>
			{#if table.getFilteredRowModel().rows.length > 0}
				{@const start = pagination.pageIndex * pagination.pageSize + 1}
				{@const end = Math.min((pagination.pageIndex + 1) * pagination.pageSize, table.getFilteredRowModel().rows.length)}
				{start}–{end} of {table.getFilteredRowModel().rows.length}
			{:else}
				0 rows
			{/if}
		</div>
		<div class="flex items-center gap-0.5">
			<button class="inline-flex items-center justify-center size-7 rounded-md border border-border transition-colors hover:bg-muted disabled:opacity-30 disabled:pointer-events-none" onclick={() => table.setPageIndex(0)} disabled={!table.getCanPreviousPage()} aria-label="First page">
				<ChevronsLeftIcon class="size-3.5" />
			</button>
			<button class="inline-flex items-center justify-center size-7 rounded-md border border-border transition-colors hover:bg-muted disabled:opacity-30 disabled:pointer-events-none" onclick={() => table.previousPage()} disabled={!table.getCanPreviousPage()} aria-label="Previous page">
				<ChevronLeftIcon class="size-3.5" />
			</button>
			<span class="mx-2 tabular-nums">
				{table.getState().pagination.pageIndex + 1} / {table.getPageCount()}
			</span>
			<button class="inline-flex items-center justify-center size-7 rounded-md border border-border transition-colors hover:bg-muted disabled:opacity-30 disabled:pointer-events-none" onclick={() => table.nextPage()} disabled={!table.getCanNextPage()} aria-label="Next page">
				<ChevronRightIcon class="size-3.5" />
			</button>
			<button class="inline-flex items-center justify-center size-7 rounded-md border border-border transition-colors hover:bg-muted disabled:opacity-30 disabled:pointer-events-none" onclick={() => table.setPageIndex(table.getPageCount() - 1)} disabled={!table.getCanNextPage()} aria-label="Last page">
				<ChevronsRightIcon class="size-3.5" />
			</button>
		</div>
	</div>
</div>
