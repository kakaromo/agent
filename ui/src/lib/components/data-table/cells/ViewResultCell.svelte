<script lang="ts">
	import EyeIcon from '@lucide/svelte/icons/eye';

	interface Props {
		hasResult: boolean;
		onView: () => void;
	}

	const { hasResult, onView }: Props = $props();
</script>

{#if hasResult}
	<button
		class="inline-flex items-center gap-1 rounded px-2 py-1 text-[10px] font-medium bg-emerald-100 text-emerald-700 hover:bg-emerald-200 transition-colors"
		onclick={onView}
		title="View performance result"
	>
		<EyeIcon class="size-3" />
		View
	</button>
{:else}
	<span class="text-muted-foreground text-[10px]">—</span>
{/if}
