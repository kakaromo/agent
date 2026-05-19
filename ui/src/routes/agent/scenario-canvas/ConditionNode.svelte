<script lang="ts">
	import { Handle, Position } from '@xyflow/svelte';
	import { getContext } from 'svelte';
	import type { ConditionNodeData } from './types.js';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import CheckCircleIcon from '@lucide/svelte/icons/check-circle';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import GitBranchIcon from '@lucide/svelte/icons/git-branch';

	let { id, data }: { id: string; data: ConditionNodeData } = $props();

	const onEditCondition = getContext<((nodeId: string) => void) | undefined>('onEditCondition');
	const onDeleteNode = getContext<((nodeId: string) => void) | undefined>('onDeleteNode');

	let execStatus = $derived(data.execStatus);

	let label = $derived.by(() => {
		if (data.source === 'shell') {
			const cmd = data.shellCommand ? data.shellCommand.slice(0, 20) : 'shell';
			const op = data.operator;
			if (op === 'contains' || op === '!contains') {
				return `${cmd}… ${op} "${data.thresholdString}"`;
			}
			return `${cmd}… ${op} ${data.threshold}`;
		}
		return data.metricKey
			? `${data.metricKey} ${data.operator} ${data.threshold}`
			: '조건 설정 필요';
	});

	let stateClass = $derived.by(() => {
		if (!execStatus) return 'bg-amber-50 border-amber-400';
		switch (execStatus) {
			case 'running': return 'bg-blue-50 border-blue-500 ring-2 ring-blue-300/50 shadow-blue-200 shadow-lg';
			case 'completed': return 'bg-green-50 border-green-500';
			case 'failed': return 'bg-red-50 border-red-500';
			default: return 'bg-amber-50 border-amber-400';
		}
	});
</script>

<div class="border-2 {stateClass} rounded-lg shadow-sm px-3 py-2 min-w-[140px] max-w-[200px] group transition-all duration-300">
	<Handle type="target" position={Position.Top} class="!bg-muted-foreground !w-2 !h-2" />

	<!-- Header -->
	<div class="flex items-center gap-1.5 mb-1">
		{#if data.execOrder != null}
			<span class="size-4 rounded-full bg-amber-200 text-[8px] font-bold flex items-center justify-center text-amber-700 shrink-0">{data.execOrder}</span>
		{/if}
		<GitBranchIcon class="size-3 text-amber-600" />
		<span class="text-[8px] font-bold text-amber-700 uppercase">if</span>

		{#if execStatus === 'running'}
			<LoaderIcon class="size-3 text-blue-600 animate-spin ml-auto" />
		{:else if execStatus === 'completed'}
			<CheckCircleIcon class="size-3 text-green-600 ml-auto" />
		{:else}
			<div class="ml-auto flex gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
				{#if onEditCondition}
					<button onclick={(e) => { e.stopPropagation(); onEditCondition(id); }}
						class="p-0.5 rounded hover:bg-amber-200" title="조건 편집">
						<PencilIcon class="size-2.5 text-amber-700" />
					</button>
				{/if}
				{#if onDeleteNode}
					<button onclick={(e) => { e.stopPropagation(); onDeleteNode(id); }}
						class="p-0.5 rounded hover:bg-red-100" title="삭제">
						<TrashIcon class="size-2.5 text-red-500" />
					</button>
				{/if}
			</div>
		{/if}
	</div>

	<!-- Condition label -->
	<div class="text-[9px] text-amber-800 font-mono truncate">{label}</div>

	<!-- True / False handles -->
	<div class="flex justify-between mt-1.5 text-[7px] font-bold">
		<span class="text-green-600">T ●</span>
		<span class="text-red-600">● F</span>
	</div>

	<Handle type="source" position={Position.Left} id="false" class="!bg-red-500 !w-2.5 !h-2.5" />
	<Handle type="source" position={Position.Right} id="true" class="!bg-green-500 !w-2.5 !h-2.5" />
</div>
