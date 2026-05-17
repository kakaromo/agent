<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import * as Card from '$lib/components/ui/card';
	import { Skeleton } from '$lib/components/ui/skeleton/index.js';
	import {
		fetchPerformanceHistory,
		fetchPerformanceResultData,
		fetchPerformanceTestRequests,
		fetchPerformanceHistoryTcGroupsByTr,
		fetchPerformanceHistoryByTrAndTc,
		type PerformanceResultData
	} from '$lib/api/testdb.js';
	import type { PerformanceTestRequest, PerformanceHistory, GroupCount } from '$lib/api/types.js';
	import CompareItemStrip from './CompareItemStrip.svelte';
	import type { CompareItem } from './CompareItemStrip.svelte';
	import CompareSummaryCard from './CompareSummaryCard.svelte';
	import CompareItemCard from './CompareItemCard.svelte';
	import { getParserEntry } from '$lib/components/perf-content/parserRegistry.js';
	import ChevronLeft from '@lucide/svelte/icons/chevron-left';
	import Check from '@lucide/svelte/icons/check';
	import Search from '@lucide/svelte/icons/search';
	import SlidersHorizontal from '@lucide/svelte/icons/sliders-horizontal';
	import FileDown from '@lucide/svelte/icons/file-down';

	interface Props {
		open: boolean;
		ids: number[];
		onOpenChange: (open: boolean) => void;
		onIdsChange: (ids: number[]) => void;
	}

	let { open = $bindable(), ids, onOpenChange, onIdsChange }: Props = $props();

	// Sheet manages its own ids internally — parent ids are initial seed
	let internalIds = $state<number[]>([]);

	// Sync from parent when ids change (e.g., tray selection changes)
	let lastParentKey = $state('');
	$effect(() => {
		const key = ids.join(',');
		if (key !== lastParentKey) {
			lastParentKey = key;
			internalIds = [...ids];
		}
	});

	let loading = $state(true);
	let error = $state('');
	let baselineIndex = $state(0);

	// Y축 스케일 설정
	let showScalePanel = $state(false);
	let yAxisScales = $state<Record<string, number>>({});  // key → max value

	// 현재 비교 항목의 parserId에서 Y축 탭 키 추출
	const yAxisKeys = $derived.by(() => {
		const firstItem = compareItems.find(Boolean);
		if (!firstItem) return [];
		const entry = getParserEntry(firstItem.result.parserId);
		if (!entry) return [];
		if (entry.yAxisMaxType === 'number') return ['Y-Max'];
		if (entry.yAxisMaxType === 'record') {
			// 데이터에서 탭 키 추출
			const data = firstItem.result.data;
			if (data && typeof data === 'object' && !Array.isArray(data)) {
				return Object.keys(data);
			}
		}
		return [];
	});

	const yAxisMaxProp = $derived.by(() => {
		const firstItem = compareItems.find(Boolean);
		if (!firstItem) return undefined;
		const entry = getParserEntry(firstItem.result.parserId);
		if (!entry) return undefined;

		const hasValues = Object.values(yAxisScales).some(v => v > 0);
		if (!hasValues) return undefined;

		if (entry.yAxisMaxType === 'number') return yAxisScales['Y-Max'] || undefined;
		if (entry.yAxisMaxType === 'record') return yAxisScales;
		return undefined;
	});

	// Data cache
	let dataCache = $state<Map<number, { history: PerformanceHistory; result: PerformanceResultData }>>(new Map());
	let trMap = $state<Map<number, PerformanceTestRequest>>(new Map());

	function resolveFw(trId?: string): string {
		const tr = trMap.get(Number(trId));
		return tr?.fw ?? tr?.fwVersion ?? `TR#${trId}`;
	}

	const compareItems: CompareItem[] = $derived(
		internalIds.map((id) => {
			const cached = dataCache.get(id);
			if (!cached) return null;
			const { history: h, result: r } = cached;
			const fw = resolveFw(h.trId);
			const isCollecting = r?.status === 'collecting';
			const isPartial = r?.partial === true && !isCollecting;
			return {
				history: h,
				result: r,
				label: `${fw} / ${r?.tcName ?? 'TC?'}`,
				fw,
				isCollecting,
				isPartial
			};
		}).filter((it): it is CompareItem => it != null)
	);

	const displayItems = $derived(compareItems.filter((it) => !it.isCollecting));
	const collectingIds = $derived(compareItems.filter((it) => it.isCollecting).map((it) => it.history.id));

	const displayBaselineIndex = $derived.by(() => {
		const baseItem = compareItems[baselineIndex];
		if (!baseItem || baseItem.isCollecting) return 0;
		const idx = displayItems.findIndex((it) => it.history.id === baseItem.history.id);
		return idx >= 0 ? idx : 0;
	});

	async function loadData() {
		if (internalIds.length === 0) {
			loading = false;
			return;
		}
		loading = true;
		error = '';
		try {
			if (trMap.size === 0) {
				const trs = await fetchPerformanceTestRequests();
				trMap = new Map(trs.map((t) => [t.id, t]));
			}
			const uncachedIds = internalIds.filter((id) => !dataCache.has(id));
			if (uncachedIds.length > 0) {
				const results = await Promise.all(
					uncachedIds.map((id) => Promise.all([fetchPerformanceHistory(id), fetchPerformanceResultData(id)]))
				);
				const newCache = new Map(dataCache);
				for (let i = 0; i < uncachedIds.length; i++) {
					newCache.set(uncachedIds[i], { history: results[i][0], result: results[i][1] });
				}
				dataCache = newCache;
			}
		} catch (e: any) {
			error = e?.message ?? 'Failed to load data';
		} finally {
			loading = false;
		}
	}

	async function refreshCollecting() {
		if (collectingIds.length === 0) return;
		try {
			const results = await Promise.all(
				collectingIds.map((id) => Promise.all([fetchPerformanceHistory(id), fetchPerformanceResultData(id)]))
			);
			const newCache = new Map(dataCache);
			for (let i = 0; i < collectingIds.length; i++) {
				newCache.set(collectingIds[i], { history: results[i][0], result: results[i][1] });
			}
			dataCache = newCache;
		} catch {}
	}

	// internalIds나 open이 바뀔 때만 loadData 트리거
	let lastLoadKey = $state('');
	$effect(() => {
		const key = `${open}:${internalIds.join(',')}`;
		if (key !== lastLoadKey && open && internalIds.length > 0) {
			lastLoadKey = key;
			loadData();
		}
	});

	$effect(() => {
		if (!open || collectingIds.length === 0) return;
		const timer = setInterval(refreshCollecting, 10000);
		return () => clearInterval(timer);
	});

	function removeItem(index: number) {
		const item = compareItems[index];
		if (!item) return;
		const newIds = internalIds.filter((id) => id !== item.history.id);
		if (newIds.length < 2) return;
		if (index < baselineIndex) baselineIndex--;
		else if (index === baselineIndex) baselineIndex = 0;
		internalIds = newIds;
		onIdsChange(newIds);
	}

	// ── Inline Picker (Add item) ──
	let showPicker = $state(false);
	let pickerStep = $state<'tr' | 'history'>('tr');
	let pickerTrs = $state<PerformanceTestRequest[]>([]);
	let pickerHistories = $state<PerformanceHistory[]>([]);
	let pickerSelectedTr = $state<PerformanceTestRequest | null>(null);
	let pickerSearch = $state('');
	let pickerLoading = $state(false);
	let pickerSelectedIds = $state<Set<number>>(new Set());

	// TC lock: 현재 비교 아이템들의 tcId
	const currentTcId = $derived(compareItems[0]?.history.tcId ?? null);

	async function openPicker() {
		showPicker = true;
		pickerStep = 'tr';
		pickerSelectedTr = null;
		pickerHistories = [];
		pickerSearch = '';
		pickerSelectedIds = new Set();

		if (pickerTrs.length === 0) {
			pickerLoading = true;
			try {
				pickerTrs = trMap.size > 0
					? [...trMap.values()]
					: await fetchPerformanceTestRequests();
			} finally {
				pickerLoading = false;
			}
		}
	}

	function closePicker() {
		showPicker = false;
	}

	const filteredPickerTrs = $derived(
		pickerSearch
			? pickerTrs.filter((tr) => {
				const q = pickerSearch.toLowerCase();
				const fw = (tr.fw ?? tr.fwVersion ?? '').toLowerCase();
				const meta = [tr.controller, tr.nandType, tr.cellType, tr.nandSize].filter(Boolean).join(' ').toLowerCase();
				return fw.includes(q) || meta.includes(q);
			})
			: pickerTrs
	);

	const existingIdSet = $derived(new Set(internalIds));

	const filteredPickerHistories = $derived(
		pickerSearch
			? pickerHistories.filter((h) => {
				const q = pickerSearch.toLowerCase();
				return String(h.id).includes(q) || (h.startTime ?? '').toLowerCase().includes(q) || (h.result ?? '').toLowerCase().includes(q);
			})
			: pickerHistories
	);

	let pickerNoResults = $state(false);

	async function selectPickerTr(tr: PerformanceTestRequest) {
		pickerSelectedTr = tr;
		pickerStep = 'history';
		pickerSearch = '';
		pickerSelectedIds = new Set();
		pickerLoading = true;
		pickerNoResults = false;
		try {
			if (currentTcId) {
				const result = await fetchPerformanceHistoryByTrAndTc(String(tr.id), currentTcId, 0, 100);
				pickerHistories = result.content;
			} else {
				// No TC lock — show all histories for this TR
				const groups = await fetchPerformanceHistoryTcGroupsByTr(String(tr.id));
				const allHistories: PerformanceHistory[] = [];
				for (const g of groups) {
					const result = await fetchPerformanceHistoryByTrAndTc(String(tr.id), g.groupValue, 0, 50);
					allHistories.push(...result.content);
				}
				pickerHistories = allHistories;
			}
			if (pickerHistories.length === 0) {
				pickerNoResults = true;
			}
		} catch {
			// 서버 에러 (해당 TR에 이 TC 결과가 없는 경우 등)
			pickerHistories = [];
			pickerNoResults = true;
		} finally {
			pickerLoading = false;
		}
	}

	function togglePickerHistory(id: number) {
		const next = new Set(pickerSelectedIds);
		if (next.has(id)) next.delete(id);
		else next.add(id);
		pickerSelectedIds = next;
	}

	function confirmPicker() {
		const newIds = [...pickerSelectedIds].filter((id) => !existingIdSet.has(id));
		if (newIds.length > 0) {
			internalIds = [...internalIds, ...newIds];
			onIdsChange(internalIds);
		}
		closePicker();
	}

	function pickerGoBack() {
		pickerStep = 'tr';
		pickerSelectedTr = null;
		pickerHistories = [];
		pickerSearch = '';
		pickerSelectedIds = new Set();
	}

	function pickerResolveFw(tr: PerformanceTestRequest): string {
		return tr.fw ?? tr.fwVersion ?? `TR#${tr.id}`;
	}

	// ── Preview state ──
	let previewId = $state<number | null>(null);
	let previewData = $state<{ result: PerformanceResultData } | null>(null);
	let previewLoading = $state(false);

	async function loadPreview(id: number) {
		if (previewId === id) return;
		previewId = id;
		// Check cache first
		const cached = dataCache.get(id);
		if (cached) {
			previewData = { result: cached.result };
			return;
		}
		previewLoading = true;
		previewData = null;
		try {
			const result = await fetchPerformanceResultData(id);
			previewData = { result };
			// Also cache it
			const history = await fetchPerformanceHistory(id);
			const newCache = new Map(dataCache);
			newCache.set(id, { history, result });
			dataCache = newCache;
		} catch {
			previewData = null;
		} finally {
			previewLoading = false;
		}
	}

	function clearPreview() {
		previewId = null;
		previewData = null;
	}

	let exporting = $state(false);

	async function exportAllExcel() {
		if (displayItems.length === 0) return;
		exporting = true;
		try {
			for (const item of displayItems) {
				const url = `/api/performance-results/${item.history.id}/excel`;
				const a = document.createElement('a');
				a.href = url;
				a.download = '';
				a.click();
				// 브라우저가 동시 다운로드를 처리할 수 있도록 약간 대기
				await new Promise(r => setTimeout(r, 500));
			}
		} finally {
			exporting = false;
		}
	}
</script>

<Sheet.Root bind:open onOpenChange={(v) => onOpenChange(v)}>
	<Sheet.Content side="right" class="w-[calc(100vw-2rem)] max-w-none p-0 overflow-hidden flex flex-col">
		<Sheet.Header class="px-6 pt-5 pb-3 border-b shrink-0">
			<div class="flex items-center justify-between">
				<div>
					<Sheet.Title class="text-base font-semibold">Performance Compare</Sheet.Title>
					<Sheet.Description class="text-xs text-muted-foreground">
						{compareItems.length}개 항목 비교 중 · ★ 표시가 비교 기준이며, 클릭하여 변경할 수 있습니다
					</Sheet.Description>
				</div>
				<div class="flex items-center gap-2">
					{#if displayItems.length > 0}
						<button
							class="inline-flex items-center gap-1.5 px-3 py-1.5 text-[11px] rounded-md border hover:bg-muted transition-colors disabled:opacity-40"
							onclick={exportAllExcel}
							disabled={exporting}
						>
							{#if exporting}
								<span class="dsy-loading dsy-loading-spinner dsy-loading-xs"></span>
							{:else}
								<FileDown class="size-3.5" />
							{/if}
							Excel Export ({displayItems.length})
						</button>
					{/if}
					{#if yAxisKeys.length > 0}
						<button
							class="inline-flex items-center gap-1.5 px-3 py-1.5 text-[11px] rounded-md border transition-colors
								{showScalePanel ? 'bg-primary/10 text-primary border-primary/30' : 'hover:bg-muted'}"
							onclick={() => showScalePanel = !showScalePanel}
						>
							<SlidersHorizontal class="size-3.5" />
							Y축 스케일
						</button>
					{/if}
				</div>
			</div>

			<!-- Y축 스케일 설정 패널 -->
			{#if showScalePanel && yAxisKeys.length > 0}
				<div class="mt-3 p-3 rounded-lg border bg-muted/20 space-y-2">
					<div class="text-[10px] text-muted-foreground font-medium">차트 Y축 최대값 (비워두면 자동)</div>
					<div class="flex flex-wrap gap-3">
						{#each yAxisKeys as key}
							<div class="flex items-center gap-1.5">
								<label class="text-[10px] text-muted-foreground w-20 truncate" title={key}>{key}</label>
								<input
									type="number"
									class="w-24 h-6 px-2 text-[10px] rounded border bg-background tabular-nums"
									placeholder="auto"
									value={yAxisScales[key] || ''}
									oninput={(e) => {
										const v = Number(e.currentTarget.value);
										if (v > 0) yAxisScales[key] = v;
										else delete yAxisScales[key];
										yAxisScales = { ...yAxisScales };
									}}
								/>
							</div>
						{/each}
						{#if Object.values(yAxisScales).some(v => v > 0)}
							<button
								class="text-[10px] text-muted-foreground hover:text-foreground"
								onclick={() => { yAxisScales = {}; }}
							>
								초기화
							</button>
						{/if}
					</div>
				</div>
			{/if}
		</Sheet.Header>

		<div class="flex-1 overflow-y-auto">
			{#if loading}
				<div class="p-6 space-y-4">
					<div class="flex gap-2">
						<Skeleton class="h-8 w-32 rounded-lg" />
						<Skeleton class="h-8 w-32 rounded-lg" />
					</div>
					<Skeleton class="h-48 rounded-lg" />
					<Skeleton class="h-32 rounded-lg" />
				</div>
			{:else if error}
				<div class="p-6">
					<div class="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">
						{error}
					</div>
				</div>
			{:else if compareItems.length === 0}
				<div class="flex flex-col items-center justify-center py-16 gap-3 text-muted-foreground">
					<p class="text-sm">비교할 데이터를 불러오는 중입니다...</p>
				</div>
			{:else}
				<div class="p-6 space-y-4">
					<!-- ① Item Strip -->
					<CompareItemStrip
						items={compareItems}
						{baselineIndex}
						onBaselineChange={(i) => (baselineIndex = i)}
						onRemove={removeItem}
						onAdd={openPicker}
					/>

					<!-- ② Summary Card or prompt -->
					{#if displayItems.length >= 2}
						<CompareSummaryCard items={displayItems} baselineIndex={displayBaselineIndex} />
					{:else if displayItems.length === 1 && !showPicker}
						<div class="rounded-lg border border-dashed border-muted-foreground/30 p-6 text-center">
							<p class="text-sm text-muted-foreground mb-3">비교할 다른 FW의 항목을 추가하세요</p>
							<button
								class="inline-flex items-center gap-1.5 px-4 py-2 text-xs font-medium rounded-lg bg-primary text-primary-foreground hover:bg-primary/90 transition-colors"
								onclick={openPicker}
							>
								다른 FW 추가
							</button>
						</div>
					{/if}

					<!-- ③ Individual Cards (가로 나란히) -->
					<div class="flex gap-3 overflow-x-auto pb-2">
						{#each displayItems as item, idx (item.history.id)}
							<div class="min-w-[320px] flex-1">
								<CompareItemCard
									{item}
									isBaseline={idx === displayBaselineIndex}
									baselineItem={displayItems[displayBaselineIndex] ?? null}
									defaultExpanded={true}
									yAxisMax={yAxisMaxProp}
								/>
							</div>
						{/each}
					</div>
				</div>
			{/if}
		</div>

		<!-- ── Inline Picker Panel (right side) ── -->
		{#if showPicker}
			<div class="absolute inset-y-0 right-0 w-[320px] bg-background border-l shadow-lg z-10 flex flex-col" style="animation: slideIn 200ms ease-out">
				<!-- Picker Header -->
				<div class="px-6 pt-5 pb-3 border-b shrink-0">
					<div class="flex items-center gap-2">
						{#if pickerStep === 'history'}
							<button class="hover:text-foreground text-muted-foreground transition-colors" onclick={pickerGoBack}>
								<ChevronLeft class="size-4" />
							</button>
						{/if}
						<div class="flex-1">
							<h3 class="text-sm font-semibold">
								{pickerStep === 'tr' ? '비교 항목 추가' : pickerResolveFw(pickerSelectedTr!)}
							</h3>
							<p class="text-[10px] text-muted-foreground mt-0.5">
								{#if pickerStep === 'tr'}
									{currentTcId ? '같은 TC의 다른 FW를 선택하세요' : 'FW를 선택하세요'}
								{:else}
									History를 선택하세요 · 호버하면 미리보기
								{/if}
							</p>
						</div>
						<button class="text-xs text-muted-foreground hover:text-foreground transition-colors" onclick={closePicker}>
							닫기
						</button>
					</div>
					<!-- Search -->
					<div class="relative mt-2">
						<Search class="absolute left-3 top-1/2 -translate-y-1/2 size-3.5 text-muted-foreground" />
						<input
							type="text"
							bind:value={pickerSearch}
							placeholder="Search..."
							class="w-full h-8 pl-9 pr-3 text-xs rounded-lg border bg-muted/30 focus:outline-none focus:ring-1 focus:ring-ring"
						/>
					</div>
				</div>

				<!-- Picker Content -->
				<div class="flex-1 overflow-y-auto px-6 py-3">
					{#if pickerLoading}
						<div class="flex justify-center py-12">
							<span class="dsy-loading dsy-loading-spinner dsy-loading-md"></span>
						</div>
					{:else if pickerStep === 'tr'}
						<div class="space-y-1">
							{#each filteredPickerTrs as tr (tr.id)}
								<button
									class="w-full text-left px-3 py-2.5 rounded-lg text-xs hover:bg-muted/60 transition-colors flex items-center justify-between group"
									onclick={() => selectPickerTr(tr)}
								>
									<div>
										<div class="font-medium">{pickerResolveFw(tr)}</div>
										<div class="text-[10px] text-muted-foreground mt-0.5">
											{[tr.controller, tr.nandType, tr.cellType, tr.nandSize].filter(Boolean).join(' · ')}
										</div>
									</div>
									<ChevronLeft class="size-3.5 rotate-180 text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity" />
								</button>
							{/each}
							{#if filteredPickerTrs.length === 0}
								<p class="text-center text-xs text-muted-foreground py-8">No TRs found</p>
							{/if}
						</div>
					{:else}
						<div class="space-y-1">
							{#each filteredPickerHistories as h (h.id)}
								{@const alreadyAdded = existingIdSet.has(h.id)}
								{@const isSelected = pickerSelectedIds.has(h.id)}
								<button
									class="w-full text-left px-3 py-2.5 rounded-lg text-xs transition-colors flex items-center gap-2.5
										{alreadyAdded ? 'opacity-40 cursor-not-allowed' : isSelected ? 'bg-primary/10 border border-primary/30' : 'hover:bg-muted/60'}"
									onclick={() => !alreadyAdded && togglePickerHistory(h.id)}
									onpointerenter={() => !alreadyAdded && loadPreview(h.id)}
									onpointerleave={clearPreview}
									disabled={alreadyAdded}
								>
									<div class="size-4 shrink-0 rounded border flex items-center justify-center
										{isSelected ? 'bg-primary border-primary text-primary-foreground' : alreadyAdded ? 'bg-muted' : 'border-muted-foreground/30'}">
										{#if isSelected || alreadyAdded}
											<Check class="size-3" />
										{/if}
									</div>
									<div class="flex-1 min-w-0">
										<div class="font-medium">
											#{h.id}
											{#if alreadyAdded}
												<span class="text-[10px] text-muted-foreground ml-1">(already added)</span>
											{/if}
										</div>
										<div class="text-[10px] text-muted-foreground mt-0.5">
											{h.startTime ?? '—'} · {h.slotLocation ?? '—'} · {h.result ?? '—'}
										</div>
									</div>
								</button>
							{/each}
							{#if filteredPickerHistories.length === 0}
								<div class="text-center py-8">
									{#if pickerNoResults && currentTcId}
										<p class="text-xs text-muted-foreground">이 FW에 해당 TC의 결과가 없습니다</p>
										<button
											class="mt-2 text-[11px] text-primary hover:underline"
											onclick={pickerGoBack}
										>
											다른 FW 선택
										</button>
									{:else}
										<p class="text-xs text-muted-foreground">{pickerSearch ? '검색 결과 없음' : '결과가 없습니다'}</p>
									{/if}
								</div>
							{/if}
						</div>

						<!-- Preview Panel (Picker 왼쪽에 표시) -->
						{#if previewId !== null}
							<div class="fixed right-[320px] top-1/4 w-[340px] z-50 mr-2" style="animation: slideIn 150ms ease-out">
								<Card.Root class="gap-0 p-0 overflow-hidden shadow-lg border">
									<Card.Header class="px-3 py-2 border-b bg-muted/30">
										<Card.Title class="text-[11px] font-medium text-muted-foreground">
											Preview — #{previewId}
										</Card.Title>
									</Card.Header>
									<Card.Content class="p-2 max-h-[400px] overflow-y-auto">
										{#if previewLoading}
											<div class="flex items-center justify-center py-8">
												<span class="dsy-loading dsy-loading-spinner dsy-loading-sm"></span>
											</div>
										{:else if previewData}
											{@const r = previewData.result}
											{#await import('$lib/components/perf-content/PerfRenderer.svelte') then mod}
												<mod.default
													parserId={r.parserId}
													data={r.data}
													tcName={r.tcName ?? ''}
												/>
											{/await}
										{:else}
											<p class="text-xs text-muted-foreground text-center py-4">No preview available</p>
										{/if}
									</Card.Content>
								</Card.Root>
							</div>
						{/if}
					{/if}
				</div>

				<!-- Picker Footer -->
				{#if pickerStep === 'history' && pickerSelectedIds.size > 0}
					<div class="px-6 py-3 border-t shrink-0 flex justify-end">
						<button
							class="px-4 py-2 text-xs font-medium rounded-lg bg-primary text-primary-foreground hover:bg-primary/90 transition-colors"
							onclick={confirmPicker}
						>
							Add {pickerSelectedIds.size} item{pickerSelectedIds.size > 1 ? 's' : ''}
						</button>
					</div>
				{/if}
			</div>
		{/if}
	</Sheet.Content>
</Sheet.Root>

<style>
	@keyframes fadeIn {
		from { opacity: 0; }
		to { opacity: 1; }
	}
	@keyframes slideIn {
		from { opacity: 0; transform: translateX(8px); }
		to { opacity: 1; transform: translateX(0); }
	}
</style>
