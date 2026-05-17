<script lang="ts">
	import Plus from '@lucide/svelte/icons/plus';
	import Check from '@lucide/svelte/icons/check';
	import Ban from '@lucide/svelte/icons/ban';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';
	import { fetchPerformanceResultData, type PerformanceResultData } from '$lib/api/testdb.js';
	import { PerfRenderer } from '$lib/components/perf-content';

	interface Props {
		selected: boolean;
		onToggle: () => void;
		disabled?: boolean;
		disabledReason?: string;
		historyId?: number;
	}

	const { selected, onToggle, disabled = false, disabledReason = '', historyId }: Props = $props();

	let showPreview = $state(false);
	let previewData = $state<PerformanceResultData | null>(null);
	let previewLoading = $state(false);
	let previewError = $state('');
	let hoverTimer: ReturnType<typeof setTimeout> | null = null;
	let popoverEl = $state<HTMLDivElement | null>(null);
	let buttonEl = $state<HTMLElement | null>(null);

	function handleMouseEnter() {
		if (disabled || !historyId) return;
		hoverTimer = setTimeout(async () => {
			showPreview = true;
			if (!previewData && !previewLoading) {
				previewLoading = true;
				previewError = '';
				try {
					previewData = await fetchPerformanceResultData(historyId!);
				} catch (e: any) {
					previewError = e.message ?? '로드 실패';
				} finally {
					previewLoading = false;
				}
			}
		}, 400);
	}

	function handleMouseLeave(e: MouseEvent) {
		if (hoverTimer) { clearTimeout(hoverTimer); hoverTimer = null; }
		// popover 위로 이동하면 닫지 않음
		const related = e.relatedTarget as HTMLElement | null;
		if (popoverEl?.contains(related) || buttonEl?.contains(related)) return;
		showPreview = false;
	}

	function handlePopoverLeave(e: MouseEvent) {
		const related = e.relatedTarget as HTMLElement | null;
		if (popoverEl?.contains(related) || buttonEl?.contains(related)) return;
		showPreview = false;
	}
</script>

<div class="relative inline-flex">
	{#if disabled}
		<span
			class="inline-flex items-center justify-center rounded px-1.5 py-0.5 gap-1 text-muted-foreground/30 cursor-not-allowed"
			title={disabledReason}
		>
			<Ban class="size-3" />
			<span class="text-[10px]">VS</span>
		</span>
	{:else}
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<button
			bind:this={buttonEl}
			class="inline-flex items-center justify-center rounded transition-colors {selected
				? 'bg-primary text-primary-foreground px-1.5 py-0.5 gap-1'
				: 'border border-border hover:bg-muted hover:border-primary/40 text-muted-foreground hover:text-primary px-1.5 py-0.5 gap-1'}"
			onclick={(e) => { e.stopPropagation(); onToggle(); }}
			onmouseenter={handleMouseEnter}
			onmouseleave={handleMouseLeave}
			title={selected ? '비교에서 제거' : '비교에 추가'}
		>
			{#if selected}
				<Check class="size-3" />
				<span class="text-[10px]">VS</span>
			{:else}
				<Plus class="size-3" />
				<span class="text-[10px]">VS</span>
			{/if}
		</button>
	{/if}

	<!-- Chart Preview Popover -->
	{#if showPreview && historyId}
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			bind:this={popoverEl}
			class="absolute z-50 right-full mr-2 top-1/2 -translate-y-1/2 w-[520px] max-h-[400px] overflow-auto rounded-lg border bg-card shadow-xl"
			onmouseleave={handlePopoverLeave}
		>
			{#if previewLoading}
				<div class="flex items-center justify-center h-48 gap-2 text-muted-foreground">
					<LoaderCircle class="size-4 animate-spin" />
					<span class="text-xs">차트 로딩 중...</span>
				</div>
			{:else if previewError}
				<div class="flex items-center justify-center h-24 text-xs text-muted-foreground">
					{previewError}
				</div>
			{:else if previewData}
				<div class="p-2">
					<div class="text-[10px] text-muted-foreground mb-1 px-1">
						#{historyId} · {previewData.parserName} · {previewData.tcName}
					</div>
					<PerfRenderer
						parserId={previewData.parserId}
						data={previewData.data}
						tcName={previewData.tcName}
					/>
				</div>
			{/if}
		</div>
	{/if}
</div>
