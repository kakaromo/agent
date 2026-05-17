<script lang="ts">
	import TrashIcon from '@lucide/svelte/icons/trash-2';

	interface Props {
		visible: boolean;
		disabled?: boolean;
		onDelete: () => void;
	}

	let { visible, disabled = false, onDelete }: Props = $props();
</script>

{#if visible}
	<button
		class="p-1 rounded hover:bg-destructive/10 transition-colors disabled:opacity-30"
		title="삭제"
		{disabled}
		onclick={(e) => { e.stopPropagation(); onDelete(); }}
	>
		<TrashIcon class="size-3.5 text-muted-foreground/50 hover:text-destructive" />
	</button>
{/if}
