<script lang="ts">
	import Star from '@lucide/svelte/icons/star';
	import X from '@lucide/svelte/icons/x';
	import Plus from '@lucide/svelte/icons/plus';
	import type { PerformanceHistory } from '$lib/api/types.js';
	import type { PerformanceResultData } from '$lib/api/testdb.js';

	export interface CompareItem {
		history: PerformanceHistory;
		result: PerformanceResultData;
		label: string;
		fw: string;
		isCollecting: boolean;
		isPartial: boolean;
	}

	interface Props {
		items: CompareItem[];
		baselineIndex: number;
		onBaselineChange: (index: number) => void;
		onRemove: (index: number) => void;
		onAdd: () => void;
		minItems?: number;
	}

	let { items, baselineIndex, onBaselineChange, onRemove, onAdd, minItems = 2 }: Props = $props();
</script>

<div class="flex items-center gap-1.5 flex-wrap">
	{#each items as item, idx (item.history.id)}
		{@const isBase = idx === baselineIndex}
		<div
			class="group inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs transition-all cursor-pointer
				{isBase
					? 'bg-primary/10 text-primary border border-primary/30 shadow-sm'
					: 'bg-muted/60 text-muted-foreground hover:bg-muted border border-transparent'}"
			role="button"
			tabindex="0"
			onclick={() => onBaselineChange(idx)}
			onkeydown={(e) => { if (e.key === 'Enter') onBaselineChange(idx); }}
			title={isBase ? 'Baseline (비교 기준)' : 'Click: 이 항목을 비교 기준으로 변경'}
		>
			{#if isBase}
				<Star class="size-3 fill-primary text-primary shrink-0" />
			{/if}
			{#if item.isCollecting}
				<span class="dsy-loading dsy-loading-spinner size-3 shrink-0"></span>
			{/if}
			<span class="truncate max-w-[160px]">{item.label}</span>
			{#if items.length > minItems}
				<button
					class="ml-0.5 opacity-0 group-hover:opacity-100 transition-opacity hover:text-destructive shrink-0"
					onclick={(e) => { e.stopPropagation(); onRemove(idx); }}
					title="Remove"
				>
					<X class="size-3" />
				</button>
			{/if}
		</div>
	{/each}
	<button
		class="inline-flex items-center gap-1 rounded-lg px-3 py-1.5 text-xs border border-dashed border-muted-foreground/30 text-muted-foreground hover:border-primary/50 hover:text-primary transition-all"
		onclick={onAdd}
	>
		<Plus class="size-3" />
		항목 추가
	</button>
</div>
