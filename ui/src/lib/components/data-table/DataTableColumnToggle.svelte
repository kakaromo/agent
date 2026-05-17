<script lang="ts">
	import Columns3 from '@lucide/svelte/icons/columns-3';
	import * as DropdownMenu from '$lib/components/ui/dropdown-menu';
	import type { Table } from '@tanstack/table-core';

	interface Props {
		table: Table<any>;
	}

	const { table }: Props = $props();

	const columns = $derived(
		table
			.getAllColumns()
			.filter((column) => typeof column.accessorFn !== 'undefined' && column.getCanHide())
	);
</script>

<DropdownMenu.Root>
	<DropdownMenu.Trigger>
		{#snippet child({ props })}
			<button
				class="inline-flex items-center gap-0.5 px-2 py-0.5 rounded border text-[10px] hover:bg-muted transition-colors"
				{...props}
			>
				<Columns3 class="size-2.5" />
				<span class="hidden sm:inline">Columns</span>
			</button>
		{/snippet}
	</DropdownMenu.Trigger>
	<DropdownMenu.Content align="end" class="w-36">
		<DropdownMenu.Label class="text-[10px]">Toggle columns</DropdownMenu.Label>
		<DropdownMenu.Separator />
		{#each columns as column (column.id)}
			<DropdownMenu.CheckboxItem
				class="text-[10px]"
				checked={column.getIsVisible()}
				onCheckedChange={(value) => column.toggleVisibility(!!value)}
			>
				{column.id}
			</DropdownMenu.CheckboxItem>
		{/each}
	</DropdownMenu.Content>
</DropdownMenu.Root>
