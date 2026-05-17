<script lang="ts">
	import type { Snippet } from 'svelte';

	interface Props {
		title: string;
		children?: Snippet;
	}

	let { title, children }: Props = $props();
</script>

<div class="flex items-center justify-between border-b border-l-[3px] border-l-primary/30 px-4 py-2.5">
	<span class="text-sm font-semibold text-foreground">{title}</span>
	{#if children}
		{@render children()}
	{/if}
</div>
