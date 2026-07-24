<script lang="ts">
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { splitByStep, extractCycles, TOOL_STYLES } from './types.js';
	import { buildStepLabel } from './stepLabel.js';
	import { deriveStepInsights, type WorkloadInsight, type InsightTone } from './workloadContext.js';
	import StepCycleView from './StepCycleView.svelte';
	import MacroResultView from './MacroResultView.svelte';
	import IOTestResultView from './IOTestResultView.svelte';
	import LightbulbIcon from '@lucide/svelte/icons/lightbulb';
	import InfoIcon from '@lucide/svelte/icons/info';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';

	interface Props {
		metrics: Record<string, number>;
		executionConfig?: { steps?: any[]; loops?: any[] } | null;
	}

	let { metrics, executionConfig = null }: Props = $props();

	const steps = $derived(splitByStep(metrics));

	let activeTab = $state('');

	$effect(() => {
		if (steps.length === 0) return;
		if (!steps.find(s => `step-${s.step}` === activeTab)) {
			activeTab = `step-${steps[0].step}`;
		}
	});

	function labelFor(stepIdx: number, detectedTool: import('./types.js').ToolType): string {
		const cfg = executionConfig?.steps?.[stepIdx] ?? null;
		return buildStepLabel(stepIdx, cfg, detectedTool);
	}

	function toneClass(tone: InsightTone): string {
		switch (tone) {
			case 'good': return 'text-green-700 dark:text-green-400';
			case 'warn': return 'text-amber-700 dark:text-amber-400';
			default: return 'text-muted-foreground';
		}
	}
</script>

{#snippet stepInterpretation(stepMetrics: Record<string, number>, tool: import('./types.js').ToolType, title: string)}
	{@const ins = deriveStepInsights(stepMetrics, tool, title)}
	{#if ins.length > 0}
		<div class="rounded-md border bg-muted/20 px-2.5 py-1.5 mb-2">
			<div class="flex items-center gap-1 mb-0.5">
				<LightbulbIcon class="size-3 text-amber-500" />
				<span class="text-[9px] font-semibold text-muted-foreground">이 Step 해석</span>
			</div>
			<ul class="space-y-0.5">
				{#each ins as i}
					<li class="flex items-start gap-1 text-[10px] leading-snug {toneClass(i.tone)}">
						{#if i.tone === 'warn'}
							<TriangleAlertIcon class="size-2.5 mt-0.5 shrink-0" />
						{:else if i.tone === 'good'}
							<CircleCheckIcon class="size-2.5 mt-0.5 shrink-0" />
						{:else}
							<InfoIcon class="size-2.5 mt-0.5 shrink-0 opacity-60" />
						{/if}
						<span>{i.text}</span>
					</li>
				{/each}
			</ul>
		</div>
	{/if}
{/snippet}

{#if steps.length === 0}
	<div class="text-xs text-muted-foreground py-4 text-center">No metrics data</div>
{:else if steps.length === 1}
	{@const step = steps[0]}
	{@const cycles = extractCycles(metrics, step.step)}
	{@render stepInterpretation(step.metrics, step.tool, labelFor(step.step, step.tool))}
	{#if step.tool === 'macro'}
		<MacroResultView metrics={step.metrics} cycleMetrics={cycles} />
	{:else if step.tool === 'iotest'}
		<IOTestResultView metrics={step.metrics} cycleMetrics={cycles} />
	{:else}
		<StepCycleView metrics={step.metrics} cycleMetrics={cycles} />
	{/if}
{:else}
	<Tabs.Root bind:value={activeTab}>
		<Tabs.List class="flex flex-wrap gap-0.5">
			{#each steps as step (step.step)}
				{@const style = TOOL_STYLES[step.tool]}
				<Tabs.Trigger
					value={`step-${step.step}`}
					class="text-[10px] px-3 py-1 flex items-center gap-1.5"
				>
					<span class="size-1.5 rounded-full {style.bg}"></span>
					{labelFor(step.step, step.tool)}
				</Tabs.Trigger>
			{/each}
		</Tabs.List>
		{#each steps as step (step.step)}
			<Tabs.Content value={`step-${step.step}`} class="mt-3">
				{@const cycles = extractCycles(metrics, step.step)}
				{@render stepInterpretation(step.metrics, step.tool, labelFor(step.step, step.tool))}
				{#if step.tool === 'macro'}
					<MacroResultView metrics={step.metrics} cycleMetrics={cycles} />
				{:else if step.tool === 'iotest'}
					<IOTestResultView metrics={step.metrics} cycleMetrics={cycles} />
				{:else}
					<StepCycleView metrics={step.metrics} cycleMetrics={cycles} />
				{/if}
			</Tabs.Content>
		{/each}
	</Tabs.Root>
{/if}
