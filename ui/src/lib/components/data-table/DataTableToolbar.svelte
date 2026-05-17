<script lang="ts">
	import Search from '@lucide/svelte/icons/search';
	import DataTableColumnToggle from './DataTableColumnToggle.svelte';
	import type { Table } from '@tanstack/table-core';
	import type { Snippet, Component } from 'svelte';

	export interface ActionButton {
		label: string;
		icon?: Component;
		variant?: 'default' | 'outline' | 'ghost' | 'destructive';
		onclick?: () => void;
		requiresSelection?: boolean;
		disabled?: boolean;
		title?: string;
	}

	interface Props {
		table: Table<any>;
		filterColumn?: string;
		filterPlaceholder?: string;
		actions?: ActionButton[];
		enableColumnVisibility?: boolean;
		selectedCount?: number;
		children?: Snippet;
	}

	const {
		table,
		filterColumn = '',
		filterPlaceholder = 'Search...',
		actions = [],
		enableColumnVisibility = true,
		selectedCount = 0,
		children
	}: Props = $props();
</script>

<div class="flex items-center justify-between gap-2">
	<!-- Left: Filter -->
	<div class="flex items-center gap-1.5 flex-1">
		{#if filterColumn}
			<div class="relative">
				<Search class="absolute left-1.5 top-1/2 -translate-y-1/2 size-2.5 text-muted-foreground" />
				<input
					type="text"
					placeholder={filterPlaceholder}
					value={(table.getColumn(filterColumn)?.getFilterValue() as string) ?? ''}
					oninput={(e) => table.getColumn(filterColumn)?.setFilterValue(e.currentTarget.value)}
					class="pl-5 pr-2 py-0.5 rounded border text-[10px] w-36 bg-background focus:outline-none focus:ring-1 focus:ring-ring"
				/>
			</div>
		{/if}
		{@render children?.()}
	</div>

	<!-- Right: Actions + Column Toggle -->
	<div class="flex items-center gap-1">
		{#each actions as action}
			<button
				class="inline-flex items-center gap-0.5 px-2 py-0.5 rounded border text-[10px] transition-colors
					{action.variant === 'default' ? 'bg-primary text-primary-foreground hover:bg-primary/90' : ''}
					{action.variant === 'destructive' ? 'border-destructive/50 text-destructive hover:bg-destructive/10' : ''}
					{action.variant === 'outline' || !action.variant ? 'hover:bg-muted' : ''}
					{(action.disabled || (action.requiresSelection && selectedCount === 0)) ? 'opacity-50 cursor-not-allowed' : ''}"
				disabled={action.disabled || (action.requiresSelection && selectedCount === 0)}
				title={action.title}
				onclick={action.onclick}
			>
				{#if action.icon}
					{@const Icon = action.icon}
					<Icon class="size-2.5" />
				{/if}
				{action.label}
			</button>
		{/each}

		{#if enableColumnVisibility}
			<DataTableColumnToggle {table} />
		{/if}
	</div>
</div>
