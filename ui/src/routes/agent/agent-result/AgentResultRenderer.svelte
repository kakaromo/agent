<script lang="ts">
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { splitByStep, mergeByTool, extractCycles, extractCyclesForTool, TOOL_STYLES, type StepMetrics, type MergedToolGroup } from './types.js';
	import FioResultView from './FioResultView.svelte';
	import MacroResultView from './MacroResultView.svelte';
	import IOTestResultView from './IOTestResultView.svelte';
	import LayersIcon from '@lucide/svelte/icons/layers';
	import ListIcon from '@lucide/svelte/icons/list';

	interface Props {
		metrics: Record<string, number>;
	}

	let { metrics }: Props = $props();

	const steps = $derived(splitByStep(metrics));
	const toolGroups = $derived(mergeByTool(steps));
	const hasMultipleSteps = $derived(steps.length > 1);

	// 뷰 모드: merged (tool별 합산) / step (개별 step)
	let viewMode = $state<'merged' | 'step'>('merged');
	let activeTab = $state('');

	// merged 모드에서 같은 tool에 step이 2개 이상인 경우에만 토글 표시
	const canToggle = $derived(hasMultipleSteps && toolGroups.some(g => g.steps.length > 1));

	$effect(() => {
		if (viewMode === 'merged') {
			if (toolGroups.length > 0 && !toolGroups.find(g => g.tool === activeTab)) {
				activeTab = toolGroups[0].tool;
			}
		} else {
			if (steps.length > 0 && !steps.find(s => `step-${s.step}` === activeTab)) {
				activeTab = `step-${steps[0].step}`;
			}
		}
	});

	function getCyclesMerged(group: MergedToolGroup) {
		return extractCyclesForTool(metrics, group.steps);
	}

	function getCyclesStep(step: StepMetrics) {
		return extractCycles(metrics, step.step);
	}
</script>

{#snippet resultView(tool: string, m: Record<string, number>, cycles: any[])}
	{#if tool === 'macro'}
		<MacroResultView metrics={m} cycleMetrics={cycles} />
	{:else if tool === 'iotest'}
		<IOTestResultView metrics={m} cycleMetrics={cycles} />
	{:else}
		<FioResultView metrics={m} cycleMetrics={cycles} />
	{/if}
{/snippet}

{#if steps.length === 0}
	<div class="text-xs text-muted-foreground py-4 text-center">No metrics data</div>
{:else}
	<!-- 뷰 모드 토글 -->
	{#if canToggle}
		<div class="flex items-center justify-end mb-2">
			<div class="inline-flex items-center rounded-md border p-0.5 gap-0.5">
				<button
					class="inline-flex items-center gap-1 px-2 py-1 rounded text-[10px] transition-colors
						{viewMode === 'merged' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground'}"
					onclick={() => viewMode = 'merged'}
				>
					<LayersIcon class="size-3" /> Merged
				</button>
				<button
					class="inline-flex items-center gap-1 px-2 py-1 rounded text-[10px] transition-colors
						{viewMode === 'step' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground'}"
					onclick={() => viewMode = 'step'}
				>
					<ListIcon class="size-3" /> Step별
				</button>
			</div>
		</div>
	{/if}

	{#if viewMode === 'merged'}
		<!-- Merged: tool별 탭 -->
		{#if toolGroups.length > 1}
			<Tabs.Root bind:value={activeTab}>
				<Tabs.List class="flex flex-wrap gap-0.5">
					{#each toolGroups as group}
						{@const style = TOOL_STYLES[group.tool]}
						<Tabs.Trigger value={group.tool} class="text-[10px] px-3 py-1 flex items-center gap-1.5">
							<span class="size-1.5 rounded-full {style.bg}"></span>
							{group.label}
							{#if group.steps.length > 1}
								<span class="text-[8px] text-muted-foreground">(Step {group.steps.join(',')})</span>
							{/if}
						</Tabs.Trigger>
					{/each}
				</Tabs.List>
				{#each toolGroups as group}
					<Tabs.Content value={group.tool} class="mt-3">
						{@render resultView(group.tool, group.metrics, getCyclesMerged(group))}
					</Tabs.Content>
				{/each}
			</Tabs.Root>
		{:else}
			{@const group = toolGroups[0]}
			{@render resultView(group.tool, group.metrics, getCyclesMerged(group))}
		{/if}
	{:else}
		<!-- Step별: 개별 step 탭 -->
		{#if steps.length > 1}
			<Tabs.Root bind:value={activeTab}>
				<Tabs.List class="flex flex-wrap gap-0.5">
					{#each steps as step}
						{@const style = TOOL_STYLES[step.tool]}
						<Tabs.Trigger value={`step-${step.step}`} class="text-[10px] px-3 py-1 flex items-center gap-1.5">
							<span class="size-1.5 rounded-full {style.bg}"></span>
							{step.label}
						</Tabs.Trigger>
					{/each}
				</Tabs.List>
				{#each steps as step}
					<Tabs.Content value={`step-${step.step}`} class="mt-3">
						{@render resultView(step.tool, step.metrics, getCyclesStep(step))}
					</Tabs.Content>
				{/each}
			</Tabs.Root>
		{:else}
			{@const step = steps[0]}
			{@render resultView(step.tool, step.metrics, getCyclesStep(step))}
		{/if}
	{/if}
{/if}
