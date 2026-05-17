<script lang="ts">
	import { Checkbox } from '$lib/components/ui/checkbox';
	import type { Row, Table } from '@tanstack/table-core';

	type Props =
		| { mode: 'all'; table: Table<any> }
		| { mode: 'row'; row: Row<any> };

	const props: Props = $props();
</script>

{#if props.mode === 'all'}
	{@const table = props.table}
	{#if table.options.enableMultiRowSelection !== false}
		<Checkbox
			checked={table.getIsAllPageRowsSelected()}
			indeterminate={table.getIsSomePageRowsSelected()}
			onCheckedChange={(value) => table.toggleAllPageRowsSelected(!!value)}
			aria-label="Select all"
			class="translate-y-[2px] size-4 border-2 border-muted-foreground/40"
		/>
	{/if}
{:else}
	{@const row = props.row}
	<Checkbox
		checked={row.getIsSelected()}
		disabled={!row.getCanSelect()}
		onCheckedChange={(value) => row.toggleSelected(!!value)}
		aria-label="Select row"
		class="translate-y-[2px] size-4 border-2 border-muted-foreground/40"
	/>
{/if}
