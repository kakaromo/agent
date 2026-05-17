<script lang="ts">
	import XIcon from '@lucide/svelte/icons/x';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';

	interface SeriesInfo {
		name: string;
		color: string;
	}

	interface Props {
		seriesInfo: SeriesInfo[];
		onColorChange: (name: string, color: string) => void;
		onResetAll: () => void;
		onClose: () => void;
	}

	let { seriesInfo, onColorChange, onResetAll, onClose }: Props = $props();
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
	class="absolute top-10 right-2 z-20 w-56 rounded-lg border bg-popover shadow-lg"
	onclick={(e) => e.stopPropagation()}
>
	<div class="flex items-center justify-between px-3 py-2 border-b">
		<span class="text-[11px] font-medium">Legend Colors</span>
		<button class="p-0.5 rounded hover:bg-muted" onclick={onClose}>
			<XIcon class="size-3 text-muted-foreground" />
		</button>
	</div>

	<div class="p-2 space-y-1 max-h-60 overflow-y-auto">
		{#each seriesInfo as item}
			<label class="flex items-center gap-2 px-1.5 py-1 rounded hover:bg-muted/50 cursor-pointer">
				<div class="size-3 rounded-full shrink-0 border border-border" style="background-color: {item.color}"></div>
				<span class="text-[10px] flex-1 truncate">{item.name}</span>
				<input
					type="color"
					value={item.color}
					class="size-5 shrink-0 cursor-pointer border-0 p-0 bg-transparent"
					oninput={(e) => onColorChange(item.name, (e.target as HTMLInputElement).value)}
				/>
			</label>
		{/each}

		{#if seriesInfo.length === 0}
			<p class="text-[10px] text-muted-foreground text-center py-2">No series</p>
		{/if}
	</div>

	<div class="border-t px-3 py-1.5">
		<button
			class="flex items-center gap-1 text-[10px] text-muted-foreground hover:text-foreground transition-colors"
			onclick={onResetAll}
		>
			<RotateCcwIcon class="size-2.5" />
			Reset All
		</button>
	</div>
</div>
