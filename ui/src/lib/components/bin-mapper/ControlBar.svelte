<script lang="ts">
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';

	let {
		endianness = $bindable('AUTO'),
		repeatAsArray = $bindable(false),
		loading = false,
		onparse
	}: {
		endianness: string;
		repeatAsArray: boolean;
		loading: boolean;
		onparse: () => void;
	} = $props();
</script>

<div class="flex items-center gap-4 flex-wrap">
	<div class="flex items-center gap-2 text-xs">
		<span class="font-medium">Endianness:</span>
		<select class="h-6 px-2 text-xs rounded-md border border-input bg-background" bind:value={endianness}>
			<option value="AUTO">Auto Detect</option>
			<option value="LITTLE">Little Endian</option>
			<option value="BIG">Big Endian</option>
		</select>
	</div>

	<label class="flex items-center gap-1 text-xs cursor-pointer">
		<input type="checkbox" class="mr-1" bind:checked={repeatAsArray} />
		<span>Repeat as array</span>
	</label>

	<button
		class="h-7 px-3 rounded-md bg-primary text-primary-foreground text-xs font-medium hover:bg-primary/90 transition-colors disabled:opacity-50"
		disabled={loading}
		onclick={onparse}
	>
		{#if loading}
			<LoaderIcon class="size-3 animate-spin" />
		{/if}
		Parse
	</button>
</div>
