<script lang="ts">
	let {
		items,
		maxBars = 10
	}: {
		items: { label: string; value: number; color?: string }[];
		maxBars?: number;
	} = $props();

	const displayed = $derived(items.slice(0, maxBars));
	const maxValue = $derived(Math.max(...displayed.map((i) => i.value), 1));
</script>

<div class="space-y-1.5">
	{#each displayed as item}
		{@const pct = (item.value / maxValue) * 100}
		<div class="flex items-center gap-2 text-xs">
			<span class="w-24 truncate text-muted-foreground text-right" title={item.label}>{item.label}</span>
			<div class="flex-1 h-5 rounded-sm bg-muted overflow-hidden">
				<div
					class="h-full rounded-sm transition-all duration-500"
					style="width: {pct}%; background-color: {item.color ?? 'var(--primary)'}"
				></div>
			</div>
			<span class="w-8 text-right font-mono font-medium">{item.value}</span>
		</div>
	{/each}
</div>
