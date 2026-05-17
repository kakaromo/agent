<script lang="ts">
	import GitCompareArrows from '@lucide/svelte/icons/git-compare-arrows';

	interface Props {
		disabled?: boolean;
		disabledReason?: string;
		onOpen: () => void;
	}

	const { disabled = false, disabledReason = '', onOpen }: Props = $props();
</script>

{#if disabled}
	<span
		class="inline-flex items-center justify-center rounded px-1.5 py-0.5 text-muted-foreground/30 cursor-not-allowed"
		title={disabledReason}
	>
		<GitCompareArrows class="size-3" />
	</span>
{:else}
	<button
		class="inline-flex items-center justify-center rounded border border-border hover:bg-muted hover:border-primary/40 text-muted-foreground hover:text-primary px-1.5 py-0.5 transition-colors"
		onclick={(e) => { e.stopPropagation(); onOpen(); }}
		title="다른 FW와 비교"
	>
		<GitCompareArrows class="size-3" />
	</button>
{/if}
