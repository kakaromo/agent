<script lang="ts">
	import { Handle, Position } from '@xyflow/svelte';
	import { STEP_TYPE_COLORS, stepSummary, type StepNodeData } from './types.js';
	import ScanSearchIcon from '@lucide/svelte/icons/scan-search';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import CheckCircleIcon from '@lucide/svelte/icons/check-circle';
	import XCircleIcon from '@lucide/svelte/icons/x-circle';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import IOTestProgressView from '../iotest/IOTestProgressView.svelte';
	import { getContext } from 'svelte';

	let { id, data }: { id: string; data: StepNodeData } = $props();

	const onEditNode = getContext<((nodeId: string) => void) | undefined>('onEditNode');
	const onDeleteNode = getContext<((nodeId: string) => void) | undefined>('onDeleteNode');

	let execStatus = $derived(data.execStatus);
	let colors = $derived(STEP_TYPE_COLORS[data.stepType] ?? STEP_TYPE_COLORS.shell);
	let summary = $derived(stepSummary(data.stepForm));

	let stateClass = $derived.by(() => {
		if (!execStatus) return 'bg-background border-border';
		switch (execStatus) {
			case 'running': return 'bg-blue-50 border-blue-500 ring-2 ring-blue-300/50 shadow-blue-200 shadow-lg';
			case 'completed': return 'bg-green-50 border-green-500';
			case 'failed': return 'bg-red-50 border-red-500';
			case 'skipped': return 'bg-gray-100 border-gray-300 opacity-50';
			default: return 'bg-background border-border';
		}
	});
</script>

<div class="border-2 rounded-lg shadow-sm min-w-[150px] max-w-[220px] {stateClass} transition-all duration-300 group">
	<Handle type="target" position={Position.Top} class="!bg-muted-foreground !w-2 !h-2" />

	<!-- Header: order + type badge + actions -->
	<div class="flex items-center gap-1.5 px-2.5 pt-2 pb-1">
		{#if data.execOrder != null}
			<span class="size-4 rounded-full bg-muted text-[8px] font-bold flex items-center justify-center text-muted-foreground shrink-0">{data.execOrder}</span>
		{/if}
		<span class="px-1.5 py-0.5 rounded text-[8px] font-medium {colors.bg} {colors.text}">
			{data.stepType}
		</span>
		{#if data.stepForm.traceEnabled}
			<ScanSearchIcon class="size-2.5 text-emerald-600" />
		{/if}

		<!-- Execution state icon -->
		{#if execStatus === 'running'}
			<LoaderIcon class="size-3 text-blue-600 animate-spin ml-auto" />
		{:else if execStatus === 'completed'}
			<CheckCircleIcon class="size-3 text-green-600 ml-auto" />
		{:else if execStatus === 'failed'}
			<XCircleIcon class="size-3 text-red-600 ml-auto" />
		{:else}
			<!-- Action buttons (hover only) -->
			<div class="ml-auto flex gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity">
				{#if onEditNode}
					<button onclick={(e) => { e.stopPropagation(); onEditNode(id); }}
						class="p-0.5 rounded hover:bg-muted" title="옵션 편집">
						<PencilIcon class="size-3 text-muted-foreground" />
					</button>
				{/if}
				{#if onDeleteNode}
					<button onclick={(e) => { e.stopPropagation(); onDeleteNode(id); }}
						class="p-0.5 rounded hover:bg-muted" title="삭제">
						<TrashIcon class="size-3 text-red-500" />
					</button>
				{/if}
			</div>
		{/if}
	</div>

	<!-- Summary -->
	<div class="text-[10px] text-muted-foreground truncate px-2.5 pb-2">{summary}</div>

	<!-- Loop counter -->
	{#if data.execLoopCurrent != null && data.execLoopTotal != null}
		<div class="text-[8px] text-blue-600 px-2.5 pb-1.5">loop {data.execLoopCurrent}/{data.execLoopTotal}</div>
	{/if}

	<!-- iotest thread별 미니 progress bar (running 중에만 표시) -->
	{#if data.stepType === 'iotest' && execStatus === 'running' && data.threadProgresses && data.threadProgresses.length > 0}
		<div class="px-2.5 pb-2">
			<IOTestProgressView threadProgresses={data.threadProgresses} compact />
		</div>
	{/if}

	<Handle type="source" position={Position.Bottom} class="!bg-muted-foreground !w-2 !h-2" />
</div>
