<script lang="ts">
	import * as Card from '$lib/components/ui/card';
	import { PerfRenderer } from '$lib/components/perf-content';
	import { extractSummary, computeDelta, deltaColorClass } from './compareSummary.js';
	import { captionMuted } from '$lib/styles/common.js';
	import type { CompareItem } from './CompareItemStrip.svelte';
	import Star from '@lucide/svelte/icons/star';
	import ChevronDown from '@lucide/svelte/icons/chevron-down';
	import AlertTriangle from '@lucide/svelte/icons/triangle-alert';

	interface Props {
		item: CompareItem;
		isBaseline: boolean;
		baselineItem: CompareItem | null;
		defaultExpanded?: boolean;
		yAxisMax?: any;
	}

	let { item, isBaseline, baselineItem, defaultExpanded = false, yAxisMax }: Props = $props();

	let expanded = $state(defaultExpanded);

	// Summary metrics for this item
	const metrics = $derived(
		extractSummary(item.result.parserId, item.result.data, item.result.tcName ?? '')
	);

	// Baseline metrics for delta calculation
	const baselineMetrics = $derived(
		baselineItem && !isBaseline
			? extractSummary(baselineItem.result.parserId, baselineItem.result.data, baselineItem.result.tcName ?? '')
			: []
	);

	// Top-level delta: first metric's delta
	const primaryDelta = $derived.by(() => {
		if (isBaseline || metrics.length === 0 || baselineMetrics.length === 0) return null;
		const m = metrics[0];
		const bm = baselineMetrics.find((b) => b.key === m.key);
		if (!bm || m.value === null || bm.value === null) return null;
		const d = computeDelta(m.value, bm.value);
		if (!d) return null;
		return { ...d, colorClass: deltaColorClass(d.delta, m.higherIsBetter), metricLabel: m.label };
	});

	// Compact summary line
	const summaryLine = $derived.by(() => {
		return metrics
			.filter((m) => m.value !== null)
			.slice(0, 4)
			.map((m) => {
				const v = m.value!;
				const formatted = Math.abs(v) >= 1000 ? v.toLocaleString('en-US', { maximumFractionDigits: 0 }) : v.toFixed(1);
				return `${m.label}: ${formatted}`;
			})
			.join('  ·  ');
	});
</script>

<Card.Root class="gap-0 p-0 overflow-hidden transition-all">
	<!-- Header: always visible -->
	<button
		class="group w-full flex items-center gap-3 px-4 py-3 text-left hover:bg-muted/30 transition-colors"
		onclick={() => (expanded = !expanded)}
	>
		<!-- Baseline star -->
		{#if isBaseline}
			<Star class="size-3.5 fill-primary text-primary shrink-0" />
		{/if}

		<!-- Label -->
		<div class="flex-1 min-w-0">
			<div class="flex items-center gap-2">
				<span class="text-sm font-medium truncate">{item.label}</span>
				{#if item.isCollecting}
					<span class="dsy-loading dsy-loading-spinner size-3"></span>
					<span class="{captionMuted}">Collecting...</span>
				{:else if item.isPartial}
					<AlertTriangle class="size-3 text-amber-500" />
					<span class="text-[10px] text-amber-600">Partial</span>
				{/if}
			</div>
			{#if !expanded}
				<p class="text-[11px] text-muted-foreground mt-0.5 truncate">{summaryLine}</p>
			{/if}
		</div>

		<!-- Delta badge -->
		{#if primaryDelta}
			<span class="text-sm font-semibold tabular-nums {primaryDelta.colorClass} shrink-0">
				{primaryDelta.label}
			</span>
			<span class="text-[10px] text-muted-foreground shrink-0">{primaryDelta.metricLabel}</span>
		{:else if isBaseline}
			<span class="text-[10px] text-primary/60 bg-primary/5 px-2 py-0.5 rounded-full shrink-0">Baseline</span>
		{/if}

		<!-- Expand indicator -->
		<div class="flex items-center gap-1 shrink-0">
			{#if !expanded}
				<span class="text-[10px] text-muted-foreground/60 hidden group-hover:inline">차트 보기</span>
			{/if}
			<ChevronDown
				class="size-4 text-muted-foreground transition-transform duration-200 {expanded ? 'rotate-180' : ''}"
			/>
		</div>
	</button>

	<!-- Content: expandable -->
	<div
		class="grid transition-[grid-template-rows] duration-300 ease-in-out"
		style="grid-template-rows: {expanded ? '1fr' : '0fr'}"
	>
		<div class="overflow-hidden">
			<div class="px-4 pb-4 pt-1 border-t">
				<PerfRenderer
					parserId={item.result.parserId}
					data={item.result.data}
					tcName={item.result.tcName ?? ''}
					fw={item.fw}
					{yAxisMax}
				/>
			</div>
		</div>
	</div>
</Card.Root>
