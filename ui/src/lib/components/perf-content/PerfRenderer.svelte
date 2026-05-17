<script lang="ts">
	import { tick } from 'svelte';
	import { getParserEntry } from './parserRegistry.js';

	interface Props {
		parserId: number;
		data: any;
		tcName: string;
		fw?: string;
		yAxisMax?: any;
	}

	let { parserId, data, tcName, fw, yAxisMax }: Props = $props();

	const entry = $derived(getParserEntry(parserId));

	let rendering = $state(true);

	$effect(() => {
		// Re-trigger loading when data or parserId changes
		void parserId;
		void data;
		rendering = true;
		tick().then(() => {
			requestAnimationFrame(() => { rendering = false; });
		});
	});
</script>

{#if !data}
	<div class="text-xs text-muted-foreground p-4">No data available</div>
{:else}
	{#if rendering}
		<div class="flex items-center justify-center py-16 gap-3 text-muted-foreground">
			<span class="dsy-loading dsy-loading-spinner dsy-loading-md"></span>
			<span class="text-sm">Rendering chart...</span>
		</div>
	{/if}
	<div class:invisible={rendering}>
		{#if entry}
			{#if entry.yAxisMaxType}
				<svelte:component this={entry.component} {data} {tcName} {fw} {yAxisMax} />
			{:else}
				<svelte:component this={entry.component} {data} {tcName} {fw} />
			{/if}
		{:else}
			<pre class="text-[11px] bg-muted/50 rounded p-3 overflow-auto max-h-[80vh]">{JSON.stringify(data, null, 2)}</pre>
		{/if}
	</div>
{/if}
