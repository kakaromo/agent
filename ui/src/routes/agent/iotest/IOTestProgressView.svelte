<script lang="ts">
	import { captionMuted } from '$lib/styles/common.js';
	import type { ThreadProgress } from './types.js';
	import Check from '@lucide/svelte/icons/check';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';
	import X from '@lucide/svelte/icons/x';
	import Circle from '@lucide/svelte/icons/circle';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';

	interface Props {
		threadProgresses: ThreadProgress[];
		compact?: boolean;
	}

	let { threadProgresses, compact = false }: Props = $props();

	function statusColor(status: string): string {
		switch (status) {
			case 'completed': return 'text-green-600';
			case 'running': return 'text-blue-600';
			case 'failed': return 'text-red-600';
			default: return 'text-muted-foreground';
		}
	}

	function progressBarColor(status: string): string {
		switch (status) {
			case 'completed': return 'bg-green-500';
			case 'running': return 'bg-blue-500';
			case 'failed': return 'bg-red-500';
			default: return 'bg-gray-300';
		}
	}
</script>

{#if compact}
	<!-- Mini progress bars for canvas node -->
	<div class="space-y-0.5 mt-1">
		{#each threadProgresses as tp}
			<div class="flex items-center gap-1 text-[8px]">
				<span class="w-16 truncate text-muted-foreground">{tp.name}</span>
				<div class="flex-1 h-1.5 bg-muted rounded-full overflow-hidden">
					<div class="h-full {progressBarColor(tp.status)} transition-all rounded-full"
						style:width="{tp.percent}%"></div>
				</div>
				<span class="w-6 text-right {statusColor(tp.status)}">
					{tp.status === 'completed' ? 'done' : `${Math.round(tp.percent)}%`}
				</span>
			</div>
		{/each}
	</div>
{:else}
	<!-- Full progress view for detail sheet -->
	<div class="space-y-2">
		{#each threadProgresses as tp}
			<div class="border rounded p-2">
				<div class="flex items-center gap-1.5">
					<span class="inline-flex items-center {statusColor(tp.status)}">
						{#if tp.status === 'completed'}
							<Check class="w-3.5 h-3.5" />
						{:else if tp.status === 'running'}
							<LoaderCircle class="w-3.5 h-3.5 animate-spin" />
						{:else if tp.status === 'failed'}
							<X class="w-3.5 h-3.5" />
						{:else}
							<Circle class="w-3.5 h-3.5" />
						{/if}
					</span>
					<span class="text-[11px] font-medium">{tp.name}</span>
					<div class="flex-1 h-2 bg-muted rounded-full overflow-hidden mx-2">
						<div class="h-full {progressBarColor(tp.status)} transition-all rounded-full"
							style:width="{tp.percent}%"></div>
					</div>
					<span class={captionMuted}>
						{tp.completedSteps}/{tp.totalSteps}
						{#if tp.currentIter != null && tp.currentTotal != null}
							(loop {tp.currentIter}/{tp.currentTotal})
						{/if}
					</span>
				</div>
				{#if tp.currentOp}
					<div class="mt-0.5 {captionMuted} ml-5 inline-flex items-center gap-1">
						{#if tp.status === 'running'}
							<ChevronRight class="w-3 h-3" />
						{/if}
						<span>{tp.currentOp}</span>
					</div>
				{/if}
			</div>
		{/each}
	</div>
{/if}
