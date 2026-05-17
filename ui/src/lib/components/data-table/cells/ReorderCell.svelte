<script lang="ts">
	import ChevronUpIcon from '@lucide/svelte/icons/chevron-up';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';

	interface Props {
		visible: boolean;
		disabled: boolean;
		isFirst: boolean;
		isLast: boolean;
		onMoveUp: () => void;
		onMoveDown: () => void;
	}

	let { visible, disabled, isFirst, isLast, onMoveUp, onMoveDown }: Props = $props();
</script>

{#if visible}
	<div class="flex items-center gap-0">
		<button
			class="p-0.5 rounded hover:bg-muted transition-colors disabled:opacity-20"
			disabled={disabled || isFirst}
			onclick={(e) => { e.stopPropagation(); onMoveUp(); }}
			title="위로 이동 (행 자체를 드래그해도 됩니다)"
		>
			<ChevronUpIcon class="size-3" />
		</button>
		<button
			class="p-0.5 rounded hover:bg-muted transition-colors disabled:opacity-20"
			disabled={disabled || isLast}
			onclick={(e) => { e.stopPropagation(); onMoveDown(); }}
			title="아래로 이동 (행 자체를 드래그해도 됩니다)"
		>
			<ChevronDownIcon class="size-3" />
		</button>
	</div>
{/if}
