<script lang="ts">
	import Check from '@lucide/svelte/icons/check';

	interface Props {
		name: string;
		picked: boolean;
	}
	let { name, picked }: Props = $props();
</script>

<span class="inline-flex items-center gap-1">
	{#if picked}
		<Check class="w-3 h-3 text-primary shrink-0" />
	{/if}
	<span>{name}</span>
</span>
