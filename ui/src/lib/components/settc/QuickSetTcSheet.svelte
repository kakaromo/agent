<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import ChevronDown from '@lucide/svelte/icons/chevron-down';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import ChevronUp from '@lucide/svelte/icons/chevron-up';
	import ArrowUpDown from '@lucide/svelte/icons/arrow-up-down';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';
	import X from '@lucide/svelte/icons/x';
	import Plus from '@lucide/svelte/icons/plus';
	import { toast } from 'svelte-sonner';
	import { type TcGroup, updateTcGroup } from '$lib/api/testdb.js';
	import type {
		SlotInfomation,
		CompatibilityTestCase,
		PerformanceTestCase
	} from '$lib/api/types.js';
	import type { HeadSlotData } from '$lib/api/headSlotStore.svelte.js';
	import {
		applySettcToSlots,
		QUICK_TIME_PRESETS,
		formatCompatTime
	} from '$lib/utils/settcCommand.js';
	import { findTcNameOverride, tcOptionSchema } from '$lib/utils/tcOptions.js';
	import TcGroupDialog from './TcGroupDialog.svelte';
	import { captionMuted } from '$lib/styles/common.js';

	type TcItem = CompatibilityTestCase | PerformanceTestCase;

	interface SlotItem {
		slot: SlotInfomation;
		headData?: HeadSlotData;
	}

	interface Props {
		open: boolean;
		activeTab: string;
		isCompatTab: boolean;
		currentTCs: TcItem[];
		currentVisibleTCs: TcItem[];
		compatTCs: CompatibilityTestCase[];
		hiddenTcIds: Set<number>;
		selectedIds: Set<number>;
		currentItems: SlotItem[];
		filteredTcGroups: TcGroup[];
		tcGroupDialogRef: TcGroupDialog | null;
		onApplied?: () => void | Promise<void>;
		onGroupsChanged?: () => void | Promise<void>;
	}

	let {
		open = $bindable(),
		activeTab,
		isCompatTab,
		currentTCs,
		currentVisibleTCs,
		compatTCs,
		hiddenTcIds,
		selectedIds,
		currentItems,
		filteredTcGroups,
		tcGroupDialogRef,
		onApplied,
		onGroupsChanged
	}: Props = $props();

	// 내부 상태
	// 다중 그룹 선택 — 칩을 토글해서 여러 그룹의 TC 를 합쳐 적용 가능. 선택 순서를 보존하기 위해
	// Array<number> 사용 (Set 은 insertion order 가 유지되지만 reactive 비교가 까다로움).
	let selectedGroupIds = $state<number[]>([]);
	let localItems = $state<{ tcId: number }[]>([]);
	let tcListOpen = $state(false);
	let busy = $state(false);
	let commandVariant = $state<'settc' | 'settc2'>('settc2');

	// Drag-and-drop reorder
	let dragTcId = $state<number | null>(null);
	let dragOverTcId = $state<number | null>(null);

	// testtime
	let compatTimeDays = $state(0);
	let compatTimeHours = $state(0);
	let compatTimeMins = $state(0);
	let activeQuickTime = $state<string | null>(null);
	const compatTestTimeMin = $derived(
		Number(compatTimeDays) * 24 * 60 + Number(compatTimeHours) * 60 + Number(compatTimeMins)
	);

	// TC 추가 검색
	let addOpen = $state(false);
	let tcSearchQuery = $state('');

	// 선택된 그룹 목록 (선택 순서 유지)
	const selectedGroups = $derived(
		selectedGroupIds
			.map((id) => filteredTcGroups.find((g) => g.id === id))
			.filter((g): g is TcGroup => g != null)
	);

	// 단일 그룹 모드일 때만 그룹 편집(dirty/그룹 저장) UI 가 의미 있음
	const isSingleGroup = $derived(selectedGroups.length === 1);
	const singleGroup = $derived(isSingleGroup ? selectedGroups[0] : null);

	// dirty: localItems 의 tcId 시퀀스가 그룹 원본(sortOrder 정렬) 과 다른가?
	// 다중 그룹 선택 시에는 단일 그룹과 비교 불가하므로 false.
	const dirty = $derived.by(() => {
		if (!singleGroup) return false;
		const original = [...singleGroup.items]
			.filter((i) => !hiddenTcIds.has(i.tcId))
			.sort((a, b) => a.sortOrder - b.sortOrder)
			.map((i) => i.tcId);
		const localIds = localItems.map((i) => i.tcId);
		if (original.length !== localIds.length) return true;
		for (let i = 0; i < original.length; i++) {
			if (original[i] !== localIds[i]) return true;
		}
		return false;
	});

	const visibleItems = $derived(localItems.filter((i) => !hiddenTcIds.has(i.tcId)));

	// TC 검색 결과 (이미 선택된 TC 제외)
	const tcSearchResults = $derived.by(() => {
		const q = tcSearchQuery.trim().toLowerCase();
		const inGroup = new Set(localItems.map((i) => i.tcId));
		return currentVisibleTCs
			.filter((tc) => !inGroup.has(tc.id))
			.filter((tc) => {
				if (!q) return true;
				const name = (tc.name ?? tc.fileName ?? '').toLowerCase();
				return name.includes(q) || String(tc.id).includes(q);
			})
			.slice(0, 30);
	});

	// ── Public API ──
	export function openQuick() {
		selectedGroupIds = [];
		localItems = [];
		tcListOpen = false;
		addOpen = false;
		tcSearchQuery = '';
		busy = false;
		commandVariant = 'settc2';
		// testtime 기본 — 호환 탭일 때 의미 있도록 Aging 기준 3일
		if (isCompatTab) {
			compatTimeDays = 3;
			compatTimeHours = 0;
			compatTimeMins = 0;
		} else {
			compatTimeDays = 0;
			compatTimeHours = 0;
			compatTimeMins = 0;
		}
		activeQuickTime = null;
		open = true;
	}

	export function openQuickWithGroup(groupId: number) {
		openQuick();
		toggleGroup(groupId);
	}

	/**
	 * 그룹 칩 토글. 이미 선택돼있으면 해제, 없으면 추가.
	 * localItems 는 선택된 그룹들의 TC 를 선택 순서대로 concat + dedup 으로 재구성.
	 * 사용자가 손으로 추가/삭제/재정렬한 변경은 그룹 토글 시 초기화됨 (그룹 조합 변경이 의도).
	 */
	function toggleGroup(groupId: number) {
		const exists = selectedGroupIds.includes(groupId);
		selectedGroupIds = exists
			? selectedGroupIds.filter((id) => id !== groupId)
			: [...selectedGroupIds, groupId];
		rebuildLocalItems();
		tcListOpen = false;
		addOpen = false;
		tcSearchQuery = '';
	}

	function rebuildLocalItems() {
		const seen = new Set<number>();
		const merged: { tcId: number }[] = [];
		for (const id of selectedGroupIds) {
			const g = filteredTcGroups.find((x) => x.id === id);
			if (!g) continue;
			const ordered = [...g.items]
				.filter((i) => !hiddenTcIds.has(i.tcId))
				.sort((a, b) => a.sortOrder - b.sortOrder);
			for (const it of ordered) {
				if (seen.has(it.tcId)) continue;
				seen.add(it.tcId);
				merged.push({ tcId: it.tcId });
			}
		}
		localItems = merged;
	}

	function removeTc(tcId: number) {
		localItems = localItems.filter((i) => i.tcId !== tcId);
	}

	function addTc(tcId: number) {
		if (localItems.some((i) => i.tcId === tcId)) return;
		localItems = [...localItems, { tcId }];
		tcSearchQuery = '';
	}

	function moveTc(tcId: number, direction: 'up' | 'down') {
		const idx = localItems.findIndex((i) => i.tcId === tcId);
		if (idx < 0) return;
		const swapIdx = direction === 'up' ? idx - 1 : idx + 1;
		if (swapIdx < 0 || swapIdx >= localItems.length) return;
		const next = [...localItems];
		[next[idx], next[swapIdx]] = [next[swapIdx], next[idx]];
		localItems = next;
	}

	function reverseTcs() {
		if (localItems.length < 2) return;
		localItems = [...localItems].reverse();
	}

	function onDragStart(e: DragEvent, tcId: number) {
		dragTcId = tcId;
		if (e.dataTransfer) {
			e.dataTransfer.effectAllowed = 'move';
			e.dataTransfer.setData('text/plain', String(tcId));
		}
	}

	function onDragOver(e: DragEvent, tcId: number) {
		if (dragTcId == null || dragTcId === tcId) return;
		e.preventDefault();
		if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
		dragOverTcId = tcId;
	}

	function onDragLeave(tcId: number) {
		if (dragOverTcId === tcId) dragOverTcId = null;
	}

	function onDrop(e: DragEvent, targetTcId: number) {
		e.preventDefault();
		const fromId = dragTcId;
		dragTcId = null;
		dragOverTcId = null;
		if (fromId == null || fromId === targetTcId) return;

		const fromIdx = localItems.findIndex((i) => i.tcId === fromId);
		const toIdx = localItems.findIndex((i) => i.tcId === targetTcId);
		if (fromIdx < 0 || toIdx < 0) return;
		const next = [...localItems];
		const [moved] = next.splice(fromIdx, 1);
		next.splice(toIdx, 0, moved);
		localItems = next;
	}

	function onDragEnd() {
		dragTcId = null;
		dragOverTcId = null;
	}

	function applyQuickTime(key: string) {
		const p = QUICK_TIME_PRESETS.find((x) => x.key === key);
		if (!p) return;
		compatTimeDays = p.d;
		compatTimeHours = p.h;
		compatTimeMins = p.m;
		activeQuickTime = key;
	}

	// 성능 탭의 TC 옵션 기본값 (SetTcSheet 의 getTcOptionDefault 동일 로직, 단순 버전)
	function getDefaultOpts(tc: TcItem): Record<string, string> {
		const tcName = tc.name ?? tc.fileName ?? '';
		const opts: Record<string, string> = {};
		const tcOption = tc.tcOption ?? '';
		const keys = tcOption
			.split('/')
			.filter((s) => s.includes(';'))
			.map((part) => part.split(';', 2)[0].trim());
		for (const k of keys) {
			const override = findTcNameOverride(tcName);
			if (override?.defaults?.[k] != null) {
				opts[k] = override.defaults[k];
				continue;
			}
			opts[k] = tcOptionSchema[k]?.defaultValue ?? '';
		}
		// 성능 탭은 trace key 't' 도 기본 0
		if (!isCompatTab && !('t' in opts)) {
			const override = findTcNameOverride(tcName);
			opts['t'] = override?.defaults?.['t'] ?? tcOptionSchema['t']?.defaultValue ?? '0';
		}
		return opts;
	}

	function buildPickedTcs(): Map<number, Record<string, string>> {
		const m = new Map<number, Record<string, string>>();
		for (const item of visibleItems) {
			const tc = currentTCs.find((t) => t.id === item.tcId);
			if (!tc) continue;
			m.set(item.tcId, isCompatTab ? {} : getDefaultOpts(tc));
		}
		return m;
	}

	async function performApply() {
		const pickedTcs = buildPickedTcs();
		if (pickedTcs.size === 0) {
			toast.error('적용할 TC 가 없습니다.');
			return;
		}
		if (isCompatTab && (!Number.isFinite(compatTestTimeMin) || compatTestTimeMin <= 0)) {
			toast.error('Test time을 입력하세요.');
			return;
		}
		const selectedItems = currentItems.filter((it) => selectedIds.has(it.slot.id));
		if (selectedItems.length === 0) {
			toast.error('선택된 슬롯이 없습니다.');
			return;
		}

		busy = true;
		try {
			const res = await applySettcToSlots({
				source: activeTab,
				isCompatTab,
				commandVariant,
				pickedTcs,
				compatTCs,
				compatTestTimeMin,
				selectedItems
			});
			if (res.lastError && res.successCount < res.totalCount) {
				toast.error(`Sent to ${res.successCount}/${res.totalCount} slots. ${res.lastError}`);
			} else {
				toast.success(`Set TC sent to ${res.successCount} slot(s).`);
			}
		} finally {
			busy = false;
			open = false;
			await onApplied?.();
		}
	}

	async function saveGroupChanges() {
		if (!singleGroup) return;
		try {
			await updateTcGroup(singleGroup.id, {
				name: singleGroup.name,
				tcType: singleGroup.tcType,
				description: singleGroup.description ?? '',
				tcIds: visibleItems.map((i) => i.tcId)
			});
			toast.success('그룹이 저장되었습니다.');
			await onGroupsChanged?.();
		} catch (e: any) {
			toast.error(`그룹 저장 실패: ${e?.message ?? e}`);
		}
	}

	async function handleApply() {
		// 다중 그룹 선택이거나 변경 없음 — 그냥 적용
		if (!dirty || !singleGroup) {
			await performApply();
			return;
		}
		// 단일 그룹 + dirty — 그룹에 저장할지 묻고 적용
		toast(`'${singleGroup.name}' 그룹의 TC 가 변경되었습니다.`, {
			action: {
				label: '그룹에 저장',
				onClick: async () => {
					await saveGroupChanges();
					await performApply();
				}
			},
			cancel: {
				label: '적용만',
				onClick: () => {
					performApply();
				}
			},
			duration: 8000
		});
	}

	function openCreateGroup() {
		open = false;
		tcGroupDialogRef?.openSave();
	}
</script>

<Sheet.Root bind:open>
	<Sheet.Content
		side="right"
		class="w-[480px] sm:max-w-[480px] flex flex-col max-h-[100dvh]"
		onInteractOutside={(e) => e.preventDefault()}
		onFocusOutside={(e) => e.preventDefault()}
	>
		<Sheet.Header>
			<Sheet.Title>Set TC (그룹)</Sheet.Title>
			<Sheet.Description>
				Applying to {selectedIds.size} slot(s) on <strong>{activeTab}</strong>
			</Sheet.Description>
		</Sheet.Header>

		<div class="flex-1 overflow-y-auto py-3 space-y-3">
			<!-- 1) 그룹 선택 -->
			<section>
				<div class="text-xs font-medium text-muted-foreground mb-1.5">그룹</div>
				{#if filteredTcGroups.length === 0}
					<div class="text-xs text-muted-foreground italic">
						그룹이 없습니다.
					</div>
				{:else}
					<div class="flex flex-wrap gap-1.5">
						{#each filteredTcGroups as group (group.id)}
							{@const isOn = selectedGroupIds.includes(group.id)}
							<button
								class="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-full text-xs font-medium border transition-colors {isOn
									? 'bg-primary text-primary-foreground border-primary'
									: 'bg-muted/50 hover:bg-muted border-border'}"
								onclick={() => toggleGroup(group.id)}
								title={isOn ? '클릭으로 선택 해제' : '클릭으로 선택 (중복 선택 가능)'}
							>
								{group.name}
								<span class="opacity-60"
									>({group.items.filter((i) => !hiddenTcIds.has(i.tcId)).length})</span
								>
							</button>
						{/each}
						<button
							class="inline-flex items-center gap-0.5 px-2 py-0.5 rounded-full text-xs border border-dashed border-border text-muted-foreground hover:bg-muted transition-colors"
							onclick={openCreateGroup}
							title="새 그룹 만들기"
						>
							+ 새 그룹
						</button>
					</div>
				{/if}
			</section>

			<!-- 2) TC 편집 (그룹 선택 후 inline disclosure) -->
			<section
				class="transition-opacity {selectedGroups.length > 0
					? 'opacity-100'
					: 'opacity-30 pointer-events-none'}"
			>
				{#if selectedGroups.length > 0}
					{@const headerLabel =
						selectedGroups.length === 1
							? selectedGroups[0].name
							: `${selectedGroups.length} groups: ${selectedGroups.map((g) => g.name).join(', ')}`}
					<div class="rounded-md border border-border bg-muted/20">
						<div class="flex items-center w-full">
							<button
								class="flex items-center gap-1 flex-1 px-2 py-1 text-xs font-medium text-foreground hover:bg-muted/40 transition-colors min-w-0"
								onclick={() => (tcListOpen = !tcListOpen)}
							>
								{#if tcListOpen}
									<ChevronDown class="w-3 h-3 shrink-0" />
								{:else}
									<ChevronRight class="w-3 h-3 shrink-0" />
								{/if}
								<span class="truncate">{headerLabel} ({visibleItems.length} TC)</span>{#if dirty}<span
										class="ml-1 text-[10px] text-amber-500 font-normal shrink-0">변경됨</span
									>{/if}
							</button>
							<button
								class="inline-flex items-center gap-1 px-2 py-1 text-xs text-muted-foreground hover:text-foreground hover:bg-muted/40 transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
								disabled={visibleItems.length < 2}
								onclick={reverseTcs}
								title="순서 뒤집기"
							>
								<ArrowUpDown class="w-3 h-3" />
								Reverse
							</button>
						</div>
						{#if tcListOpen}
							<div class="border-t">
								<div class="max-h-48 overflow-y-auto">
									{#if visibleItems.length === 0}
										<div class="text-xs text-muted-foreground italic px-2 py-1.5">
											TC 가 없습니다.
										</div>
									{:else}
										<table class="w-full text-xs">
											<tbody>
												{#each visibleItems as item, i (item.tcId)}
													{@const tc = currentTCs.find((t) => t.id === item.tcId)}
													<tr
														class="border-b last:border-b-0 hover:bg-muted/30 cursor-move transition-colors {dragOverTcId ===
														item.tcId
															? 'bg-primary/10'
															: ''} {dragTcId === item.tcId ? 'opacity-50' : ''}"
														draggable="true"
														ondragstart={(e) => onDragStart(e, item.tcId)}
														ondragover={(e) => onDragOver(e, item.tcId)}
														ondragleave={() => onDragLeave(item.tcId)}
														ondrop={(e) => onDrop(e, item.tcId)}
														ondragend={onDragEnd}
													>
														<td class="w-7 px-1 py-0.5 text-center text-muted-foreground">
															{i + 1}
														</td>
														<td class="px-2 py-0.5 truncate max-w-[260px]">
															<span class="text-muted-foreground mr-1">#{item.tcId}</span>{tc?.name ??
																tc?.fileName ??
																''}
														</td>
														<td class="w-12 px-1 py-0.5 text-center">
															<button
																class="inline-flex items-center px-0.5 text-muted-foreground hover:text-foreground disabled:opacity-30"
																disabled={i === 0}
																onclick={() => moveTc(item.tcId, 'up')}
																title="위로"
															>
																<ChevronUp class="w-3 h-3" />
															</button>
															<button
																class="inline-flex items-center px-0.5 text-muted-foreground hover:text-foreground disabled:opacity-30"
																disabled={i === visibleItems.length - 1}
																onclick={() => moveTc(item.tcId, 'down')}
																title="아래로"
															>
																<ChevronDown class="w-3 h-3" />
															</button>
														</td>
														<td class="w-7 px-1 py-0.5 text-center">
															<button
																class="inline-flex items-center text-muted-foreground hover:text-destructive"
																onclick={() => removeTc(item.tcId)}
																title="제거"
															>
																<X class="w-3 h-3" />
															</button>
														</td>
													</tr>
												{/each}
											</tbody>
										</table>
									{/if}
								</div>
								<!-- TC 추가 -->
								<div class="border-t p-1.5">
									{#if !addOpen}
										<button
											class="inline-flex items-center gap-1 px-2 py-0.5 text-xs text-muted-foreground hover:text-foreground hover:bg-muted/40 rounded transition-colors"
											onclick={() => (addOpen = true)}
										>
											<Plus class="w-3 h-3" /> TC 추가
										</button>
									{:else}
										<div class="space-y-1">
											<div class="flex items-center gap-1.5">
												<input
													type="text"
													class="flex-1 h-6 px-2 text-xs rounded-md border border-border bg-background placeholder:text-muted-foreground"
													placeholder="TC 이름 또는 ID"
													bind:value={tcSearchQuery}
													autofocus
												/>
												<button
													class="text-xs px-1.5 py-0.5 text-muted-foreground hover:text-foreground"
													onclick={() => {
														addOpen = false;
														tcSearchQuery = '';
													}}
												>
													닫기
												</button>
											</div>
											{#if tcSearchResults.length > 0}
												<div class="max-h-32 overflow-y-auto rounded border border-border">
													<table class="w-full text-xs">
														<tbody>
															{#each tcSearchResults as tc (tc.id)}
																<tr
																	class="border-b last:border-b-0 hover:bg-primary/10 cursor-pointer transition-colors"
																	onclick={() => addTc(tc.id)}
																>
																	<td class="w-10 px-1 py-0.5 text-center text-muted-foreground"
																		>{tc.id}</td
																	>
																	<td class="px-2 py-0.5 truncate max-w-[280px]"
																		>{tc.name ?? tc.fileName ?? ''}</td
																	>
																	<td class="w-6 px-1 py-0.5 text-center text-primary">+</td>
																</tr>
															{/each}
														</tbody>
													</table>
												</div>
											{:else if tcSearchQuery.trim()}
												<div class="text-xs text-muted-foreground italic px-2 py-1">
													결과 없음
												</div>
											{/if}
										</div>
									{/if}
								</div>
							</div>
						{/if}
					</div>
				{/if}
			</section>

			<!-- 3) testtime (호환성 탭만) -->
			{#if isCompatTab}
				<section
					class="transition-opacity {selectedGroups.length > 0
						? 'opacity-100'
						: 'opacity-30 pointer-events-none'}"
				>
					<div class="text-xs font-medium text-muted-foreground mb-1.5">Test time</div>
					<div class="border rounded-md p-2 bg-muted/20">
						<div class="flex items-center gap-2 flex-wrap">
							<select
								class="h-6 w-14 px-1 text-xs rounded-md border bg-background"
								bind:value={compatTimeDays}
								onchange={() => (activeQuickTime = null)}
							>
								{#each Array.from({ length: 31 }, (_, i) => i) as d}
									<option value={d}>{d}</option>
								{/each}
							</select>
							<span class={captionMuted}>day</span>
							<select
								class="h-6 w-14 px-1 text-xs rounded-md border bg-background"
								bind:value={compatTimeHours}
								onchange={() => (activeQuickTime = null)}
							>
								{#each Array.from({ length: 24 }, (_, i) => i) as h}
									<option value={h}>{h}</option>
								{/each}
							</select>
							<span class={captionMuted}>hr</span>
							<select
								class="h-6 w-14 px-1 text-xs rounded-md border bg-background"
								bind:value={compatTimeMins}
								onchange={() => (activeQuickTime = null)}
							>
								{#each Array.from({ length: 60 }, (_, i) => i) as m}
									<option value={m}>{m}</option>
								{/each}
							</select>
							<span class={captionMuted}>min</span>
							<div class="flex items-center gap-1 ml-1">
								{#each QUICK_TIME_PRESETS as p (p.key)}
									<button
										type="button"
										class="px-1.5 h-5 text-[10px] rounded border transition-colors {activeQuickTime ===
										p.key
											? 'bg-primary/15 border-primary/40 text-primary'
											: 'border-border/60 text-muted-foreground hover:bg-muted'}"
										onclick={() => applyQuickTime(p.key)}
										title={`${p.d}d ${p.h}h ${p.m}m`}
									>
										{p.label}
									</button>
								{/each}
							</div>
							<span
								class="text-xs ml-2 {compatTestTimeMin <= 0
									? 'text-destructive font-medium'
									: 'text-muted-foreground'}"
							>
								= {compatTestTimeMin === 0 ? 0 : formatCompatTime(compatTestTimeMin)}{compatTestTimeMin <=
								0
									? ' (시간을 설정하세요)'
									: ''}
							</span>
						</div>
					</div>
				</section>
			{/if}
		</div>

		<Sheet.Footer>
			<div class="flex items-center justify-between w-full gap-2">
				<span class={captionMuted}>
					{#if selectedGroups.length > 0}
						{visibleItems.length} TC · {selectedIds.size} slot{#if selectedGroups.length > 1}
							· {selectedGroups.length} groups
						{/if}
					{:else}
						그룹을 선택하세요 (여러 개 선택 가능)
					{/if}
				</span>
				<div class="flex items-center gap-2">
					<!-- settc / settc2 전환 토글 (기본: settc2) -->
					<div
						class="flex rounded-md border overflow-hidden"
						title="HEAD 명령 포맷 — settc2(신규, 백틱 + 다건 일괄) / settc(구, prefix 포함; 호환성은 1 TC씩 반복)"
					>
						<button
							class="px-2 py-1 text-xs transition-colors {commandVariant === 'settc2'
								? 'bg-primary text-primary-foreground'
								: 'hover:bg-muted text-muted-foreground'}"
							onclick={() => (commandVariant = 'settc2')}
							disabled={busy}
						>settc2</button>
						<button
							class="px-2 py-1 text-xs transition-colors border-l {commandVariant === 'settc'
								? 'bg-primary text-primary-foreground'
								: 'hover:bg-muted text-muted-foreground'}"
							onclick={() => (commandVariant = 'settc')}
							disabled={busy}
						>settc</button>
					</div>
					<button
						class="rounded-md border px-3 py-1.5 text-xs hover:bg-muted transition-colors"
						onclick={() => (open = false)}
					>
						Cancel
					</button>
					<button
						class="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-50"
						disabled={busy ||
							selectedGroups.length === 0 ||
							visibleItems.length === 0 ||
							(isCompatTab && compatTestTimeMin <= 0)}
						onclick={handleApply}
					>
						{#if busy}
							<LoaderCircle class="size-3.5 animate-spin" />
							Sending...
						{:else}
							적용 ({commandVariant})
						{/if}
					</button>
				</div>
			</div>
		</Sheet.Footer>
	</Sheet.Content>
</Sheet.Root>
