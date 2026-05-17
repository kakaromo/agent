<script lang="ts">
	import Star from '@lucide/svelte/icons/star';
	import X from '@lucide/svelte/icons/x';
	import GitCompareArrows from '@lucide/svelte/icons/git-compare-arrows';
	import Trash2 from '@lucide/svelte/icons/trash-2';
	import {
		fetchPerformanceResultData,
		type PerformanceResultData
	} from '$lib/api/testdb.js';
	import type { PerformanceHistory } from '$lib/api/types.js';
	import * as Card from '$lib/components/ui/card';
	import { Skeleton } from '$lib/components/ui/skeleton/index.js';

	interface Props {
		items: PerformanceHistory[];
		baselineIndex: number;
		resolveTr: (trId?: string) => string;
		resolveTc: (tcId?: string) => string;
		lockedTcName?: string;
		onRemove: (id: number) => void;
		onBaselineChange: (index: number) => void;
		onCompare: () => void;
		onClear: () => void;
	}

	let { items, baselineIndex, resolveTr, resolveTc, lockedTcName, onRemove, onBaselineChange, onCompare, onClear }: Props = $props();

	// Preview state
	let previewId = $state<number | null>(null);
	let previewData = $state<PerformanceResultData | null>(null);
	let previewLoading = $state(false);
	let previewCache = $state<Map<number, PerformanceResultData>>(new Map());
	let hoverTimer: ReturnType<typeof setTimeout> | undefined;

	function handlePointerEnter(id: number) {
		// Debounce — 300ms 후 로드
		hoverTimer = setTimeout(() => loadPreview(id), 300);
	}

	function handlePointerLeave() {
		clearTimeout(hoverTimer);
		previewId = null;
		previewData = null;
	}

	async function loadPreview(id: number) {
		previewId = id;
		const cached = previewCache.get(id);
		if (cached) {
			previewData = cached;
			return;
		}
		previewLoading = true;
		previewData = null;
		try {
			const result = await fetchPerformanceResultData(id);
			previewCache.set(id, result);
			previewData = result;
		} catch {
			previewData = null;
		} finally {
			previewLoading = false;
		}
	}

	/** 미리보기 데이터 캐시 반환 — CompareSheet에서 재사용 */
	export function getPreviewCache(): Map<number, PerformanceResultData> {
		return previewCache;
	}
</script>

{#if items.length > 0}
	<div
		class="fixed bottom-0 left-0 right-0 z-30 border-t bg-background/95 backdrop-blur-sm shadow-[0_-4px_20px_rgba(0,0,0,0.08)]"
		style="animation: slideUp 250ms ease-out"
	>
		<div class="max-w-screen-xl mx-auto px-4 py-3">
			<div class="flex items-center gap-3">
				<!-- Label + Items -->
				<div class="flex items-center gap-1.5 flex-wrap flex-1 min-w-0">
					<div class="shrink-0 mr-1">
						<span class="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider">Compare</span>
						{#if lockedTcName}
							<span class="text-[9px] text-primary ml-1">({lockedTcName})</span>
						{/if}
					</div>
					{#each items as h, idx (h.id)}
						{@const isBase = idx === baselineIndex}
						<div
							class="group relative inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-[11px] transition-all max-w-[200px]
								{isBase
									? 'bg-primary/10 text-primary border border-primary/30'
									: 'bg-muted/60 text-foreground border border-transparent'}"
							onpointerenter={() => handlePointerEnter(h.id)}
							onpointerleave={handlePointerLeave}
						>
							{#if isBase}
								<Star class="size-3 fill-primary text-primary shrink-0" />
							{/if}
							<span class="truncate">{resolveTr(h.trId)} / {resolveTc(h.tcId)}</span>
							<span class="text-[10px] opacity-50">#{h.id}</span>
							<button
								class="ml-0.5 opacity-0 group-hover:opacity-100 transition-opacity hover:text-destructive shrink-0"
								onclick={() => onRemove(h.id)}
								title="Remove"
							>
								<X class="size-3" />
							</button>
						</div>
					{/each}
				</div>

				<!-- Actions -->
				<div class="flex items-center gap-2 shrink-0">
					{#if items.length === 2}
						<span class="text-[10px] text-muted-foreground hidden sm:inline">마우스를 올리면 미리보기</span>
					{/if}
					<button
						class="inline-flex items-center gap-1.5 px-4 py-2 text-xs font-medium rounded-lg bg-primary text-primary-foreground hover:bg-primary/90 transition-colors"
						onclick={onCompare}
					>
						<GitCompareArrows class="size-3.5" />
						{#if items.length < 2}
							다른 FW 추가
						{:else}
							Compare ({items.length})
						{/if}
					</button>
					<button
						class="p-2 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
						onclick={onClear}
						title="Clear all"
					>
						<Trash2 class="size-3.5" />
					</button>
				</div>
			</div>
		</div>

		<!-- Preview Popover -->
		{#if previewId !== null}
			<div
				class="absolute bottom-full mb-2 left-4 w-[340px] z-50"
				style="animation: popUp 150ms ease-out"
			>
				<Card.Root class="gap-0 p-0 overflow-hidden shadow-xl border">
					<Card.Header class="px-3 py-2 border-b bg-muted/30">
						<Card.Title class="text-[11px] font-medium text-muted-foreground">
							Preview — #{previewId}
						</Card.Title>
					</Card.Header>
					<Card.Content class="p-2 max-h-[350px] overflow-y-auto">
						{#if previewLoading}
							<div class="space-y-2 py-4">
								<Skeleton class="h-32 w-full rounded" />
								<Skeleton class="h-4 w-3/4 rounded" />
								<Skeleton class="h-4 w-1/2 rounded" />
							</div>
						{:else if previewData}
							{#await import('$lib/components/perf-content/PerfRenderer.svelte') then mod}
								<mod.default
									parserId={previewData.parserId}
									data={previewData.data}
									tcName={previewData.tcName ?? ''}
								/>
							{/await}
						{:else}
							<p class="text-xs text-muted-foreground text-center py-4">No preview</p>
						{/if}
					</Card.Content>
				</Card.Root>
			</div>
		{/if}
	</div>
{/if}

<style>
	@keyframes slideUp {
		from { opacity: 0; transform: translateY(100%); }
		to { opacity: 1; transform: translateY(0); }
	}
	@keyframes popUp {
		from { opacity: 0; transform: translateY(8px); }
		to { opacity: 1; transform: translateY(0); }
	}
</style>
