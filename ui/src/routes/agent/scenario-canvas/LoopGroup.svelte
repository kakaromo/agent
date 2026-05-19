<script lang="ts">
	import { Handle, Position, NodeResizer } from '@xyflow/svelte';
	import type { LoopGroupData } from './types.js';
	import { getContext } from 'svelte';
	import RepeatIcon from '@lucide/svelte/icons/repeat';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import type { NodeExecutionState } from './types.js';

	let { id, data }: { id: string; data: LoopGroupData } = $props();

	const executionStates = getContext<Map<string, NodeExecutionState> | undefined>('executionStates');
	const onEditLoopCount = getContext<((nodeId: string) => void) | undefined>('onEditLoopCount');
	const onDeleteNode = getContext<((nodeId: string) => void) | undefined>('onDeleteNode');

	let execState = $derived(executionStates?.get(id));
</script>

<div class="relative w-full h-full">
	<NodeResizer minWidth={180} minHeight={120} />

	<!-- Loop frame -->
	<div class="w-full h-full border-2 border-dashed border-blue-300 rounded-xl bg-blue-50/30">
		<!-- Header bar -->
		<div class="absolute -top-3 left-3 flex items-center gap-1 bg-background px-2 py-0.5 rounded-full border border-blue-300 shadow-sm">
			<RepeatIcon class="size-3 text-blue-600" />
			<span class="text-[10px] font-medium text-blue-700">x{data.loopCount}</span>

			{#if execState?.loopCurrent != null && execState.loopTotal != null}
				<span class="text-[9px] text-blue-500 ml-1">{execState.loopCurrent}/{execState.loopTotal}</span>
			{/if}

			<div class="flex gap-0.5 ml-1">
				{#if onEditLoopCount}
					<button
						onclick={(e) => { e.stopPropagation(); onEditLoopCount(id); }}
						class="p-0.5 rounded hover:bg-blue-100"
						title="반복 횟수 편집"
					>
						<PencilIcon class="size-2.5 text-blue-600" />
					</button>
				{/if}
				{#if onDeleteNode}
					<button
						onclick={(e) => { e.stopPropagation(); onDeleteNode(id); }}
						class="p-0.5 rounded hover:bg-red-100"
						title="루프 삭제"
					>
						<TrashIcon class="size-2.5 text-red-500" />
					</button>
				{/if}
			</div>
		</div>
	</div>
</div>
