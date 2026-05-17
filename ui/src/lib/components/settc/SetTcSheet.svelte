<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import * as ContextMenu from '$lib/components/ui/context-menu/index.js';
	import { DataTable } from '$lib/components/data-table';
	import type { ColumnDef } from '@tanstack/table-core';
	import ChevronDown from '@lucide/svelte/icons/chevron-down';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import ChevronUp from '@lucide/svelte/icons/chevron-up';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';
	import Check from '@lucide/svelte/icons/check';
	import ArrowUpDown from '@lucide/svelte/icons/arrow-up-down';
	import Trash2 from '@lucide/svelte/icons/trash-2';
	import X from '@lucide/svelte/icons/x';
	import { toast } from 'svelte-sonner';
	import { type TcGroup } from '$lib/api/testdb.js';
	import { applySettcToSlots } from '$lib/utils/settcCommand.js';
	import type {
		SlotInfomation,
		CompatibilityTestCase,
		PerformanceTestCase
	} from '$lib/api/types.js';
	import type { HeadSlotData } from '$lib/api/headSlotStore.svelte.js';
	import TcGroupDialog from './TcGroupDialog.svelte';
	import {
		tcOptionSchema,
		findTcNameOverride,
		getTcOptionSchemaDef,
		validateTcOptionValue
	} from '$lib/utils/tcOptions.js';
	import { captionMuted } from '$lib/styles/common.js';
	import { renderComponent } from '$lib/components/ui/data-table/render-helpers.js';
	import PickedTcNameCell from './PickedTcNameCell.svelte';

	type TcItem = CompatibilityTestCase | PerformanceTestCase;

	interface SlotItem {
		slot: SlotInfomation;
		headData?: HeadSlotData;
	}

	interface Props {
		// Sheet state (bind)
		open: boolean;

		// Picked TCs (bind — shared with TcGroupDialog in parent)
		pickedTcs: Map<number, Record<string, string>>;

		// Tab/data context
		activeTab: string;
		isCompatTab: boolean;
		currentTCs: TcItem[];
		currentVisibleTCs: TcItem[];
		compatTCs: CompatibilityTestCase[];
		hiddenTcIds: Set<number>;

		// Slot selection context
		selectedIds: Set<number>;
		currentItems: SlotItem[];

		// TC Groups
		filteredTcGroups: TcGroup[];
		tcGroupDialogRef: TcGroupDialog | null;
		isGroupFullySelected: (g: TcGroup) => boolean;
		applyTcGroup: (g: TcGroup) => void;
		deleteGroup: (id: number) => Promise<void>;

		// Callbacks
		onApplied?: () => void | Promise<void>;
	}

	let {
		open = $bindable(),
		pickedTcs = $bindable(),
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
		isGroupFullySelected,
		applyTcGroup,
		deleteGroup,
		onApplied
	}: Props = $props();

	// ── Local state ──
	let selectedTcListOpen = $state(true);
	let commandBusy = $state(false);
	let commandVariant = $state<'settc' | 'settc2'>('settc2');

	// TC selection DataTable state
	let tcCategoryTab = $state('All');
	let tcTabSwitching = false;
	let tcSearchQuery = $state('');
	let showPickedOnly = $state(false);

	// 호환성 settc 옵션
	let compatTimeDays = $state(0);
	let compatTimeHours = $state(0);
	let compatTimeMins = $state(0);
	// Quick time preset — 사용자가 quick 버튼으로 설정한 값. select 직접 변경 시 해제.
	type QuickTime = '40min' | '4h' | '7d' | null;
	let activeQuickTime = $state<QuickTime>(null);
	const QUICK_TIME_PRESETS: { key: Exclude<QuickTime, null>; label: string; d: number; h: number; m: number }[] = [
		{ key: '40min', label: '40min', d: 0, h: 0, m: 40 },
		{ key: '4h', label: '4h', d: 0, h: 4, m: 0 },
		{ key: '7d', label: '7d', d: 7, h: 0, m: 0 }
	];

	function applyQuickTime(key: Exclude<QuickTime, null>) {
		const p = QUICK_TIME_PRESETS.find((x) => x.key === key);
		if (!p) return;
		compatTimeDays = p.d;
		compatTimeHours = p.h;
		compatTimeMins = p.m;
		activeQuickTime = key;
	}

	// 분 단위 → "Xd Yh Zm" 짧은 표기 (0 인 단위는 생략, 0이면 "-")
	function formatCompatTime(totalMin: number): string {
		if (!totalMin || totalMin <= 0) return '-';
		const d = Math.floor(totalMin / (24 * 60));
		const h = Math.floor((totalMin - d * 24 * 60) / 60);
		const m = totalMin - d * 24 * 60 - h * 60;
		const parts: string[] = [];
		if (d > 0) parts.push(`${d}d`);
		if (h > 0) parts.push(`${h}h`);
		if (m > 0) parts.push(`${m}m`);
		return parts.join(' ');
	}
	// select bind:value가 string으로 들어올 수 있으므로 Number로 강제 캐스팅 후 분 단위 합산
	const compatTestTimeMin = $derived(
		Number(compatTimeDays) * 24 * 60 + Number(compatTimeHours) * 60 + Number(compatTimeMins)
	);

	// Global TC option defaults (performance)
	let globalTcOpts = $state<Record<string, string>>({});

	// Drag state
	let dragTcId = $state<number | null>(null);
	let dragOverTcId = $state<number | null>(null);

	// ── Public API ──
	export function openSetTC() {
		pickedTcs = new Map();
		commandBusy = false;
		commandVariant = 'settc2';
		tcSearchQuery = '';
		showPickedOnly = false;
		if (isCompatTab) {
			// Category 기본은 'All' — testtime 기본값은 'Aging' 기준(3일)으로 세팅(아래 onCompatTabChange).
			tcCategoryTab = 'All';
			onCompatTabChange('Aging');
			tcCategoryTab = 'All';
		} else {
			tcCategoryTab = 'All';
		}
		initGlobalTcOpts();
		open = true;
	}

	// ── Derived ──
	const pickedTcList = $derived([...pickedTcs.entries()]);
	const tcSelectedRowIds = $derived(new Set([...pickedTcs.keys()].map(String)));

	function getTcCategory(tc: TcItem): string {
		if (isCompatTab) {
			return (tc as CompatibilityTestCase).testType ?? '';
		}
		return (tc as PerformanceTestCase).category ?? '';
	}

	const tcCategories = $derived.by((): string[] => {
		const cats = new Set<string>();
		for (const tc of currentVisibleTCs) {
			const cat = getTcCategory(tc);
			if (cat) cats.add(cat);
		}
		return ['All', ...cats];
	});

	interface TcRow {
		id: number;
		name: string;
		tcOption: string;
	}

	const tcTableData = $derived<TcRow[]>(
		currentVisibleTCs
			.filter((tc) => tcCategoryTab === 'All' || getTcCategory(tc) === tcCategoryTab)
			.filter((tc) => !showPickedOnly || pickedTcs.has(tc.id))
			.filter((tc) => {
				if (!tcSearchQuery.trim()) return true;
				const q = tcSearchQuery.trim().toLowerCase();
				const name = (tc.name ?? tc.fileName ?? '').toLowerCase();
				const idStr = String(tc.id);
				return name.includes(q) || idStr.includes(q);
			})
			.map((tc) => ({
				id: tc.id,
				name: tc.name ?? tc.fileName ?? '',
				tcOption: tc.tcOption ?? ''
			}))
	);

	const tcTableColumns: ColumnDef<TcRow, unknown>[] = [
		{ accessorKey: 'id', header: 'ID', enableSorting: true },
		{
			accessorKey: 'name',
			header: 'TC Name',
			enableSorting: true,
			cell: ({ row }) =>
				renderComponent(PickedTcNameCell, {
					name: row.original.name,
					picked: pickedTcs.has(row.original.id)
				})
		},
		{ accessorKey: 'tcOption', header: 'TC Option' }
	];

	// Compute default Test Size from selected slots' freeArea
	const defaultTestSize = $derived.by((): string => {
		const selectedItems = currentItems.filter((item) => selectedIds.has(item.slot.id));
		let minSize = Infinity;
		for (const item of selectedItems) {
			const fa = item.headData?.freeArea;
			if (!fa) continue;
			const num = parseFloat(fa);
			if (!isNaN(num) && num > 0) {
				const intPart = Math.floor(num);
				minSize = Math.min(minSize, intPart - 1);
			}
		}
		return minSize === Infinity ? '' : String(minSize);
	});

	/** Validate all picked TCs' options. */
	const tcValidationErrors = $derived.by((): string[] => {
		if (isCompatTab) return [];
		const errors: string[] = [];
		for (const [tcId, opts] of pickedTcs) {
			const tc = currentTCs.find((t) => t.id === tcId);
			const tcName = tc?.name ?? tc?.fileName ?? '';
			for (const [key, value] of Object.entries(opts)) {
				const err = validateTcOptionValue(key, value, tcName);
				if (err) errors.push(`TC#${tcId} ${err}`);
			}
		}
		return errors;
	});

	// ── Helpers ──

	// Parse tcOption string like "s;Test Size/l;Test Loop/" into structured array
	function parseTcOption(tcOption: string | undefined | null): { key: string; label: string }[] {
		if (!tcOption) return [];
		return tcOption
			.split('/')
			.filter((s) => s.includes(';'))
			.map((part) => {
				const [key, label] = part.split(';', 2);
				return { key: key.trim(), label: label.trim() };
			});
	}


	function initGlobalTcOpts() {
		globalTcOpts = !isCompatTab ? { s: '', l: '3', t: '0' } : {};
	}

	/** Get the default value for a TC option key (global > tcName override > schema > empty) */
	function getTcOptionDefault(key: string, tcName?: string): string {
		if (key in globalTcOpts) {
			const g = globalTcOpts[key];
			if (key === 's' && g === '') return defaultTestSize;
			return g;
		}
		if (tcName) {
			const override = findTcNameOverride(tcName);
			if (override?.defaults?.[key] != null) return override.defaults[key];
		}
		if (key === 's') return defaultTestSize;
		return tcOptionSchema[key]?.defaultValue ?? '';
	}

	function updateGlobalTcOpt(key: string, value: string) {
		globalTcOpts = { ...globalTcOpts, [key]: value };
		if (pickedTcs.size === 0) return;
		const next = new Map(pickedTcs);
		let changed = false;
		for (const [tcId, opts] of next) {
			if (key in opts && opts[key] !== value) {
				next.set(tcId, { ...opts, [key]: value });
				changed = true;
			}
		}
		if (changed) pickedTcs = next;
	}

	function switchTcCategory(cat: string) {
		tcTabSwitching = true;
		tcCategoryTab = cat;
		requestAnimationFrame(() => {
			tcTabSwitching = false;
		});
	}

	// testType 탭 변경 시 기본 testtime 자동 설정
	function onCompatTabChange(tt: string) {
		switchTcCategory(tt);
		if (tt === 'Aging') {
			compatTimeDays = 3;
			compatTimeHours = 0;
			compatTimeMins = 0;
		} else if (tt === 'Functional') {
			compatTimeDays = 0;
			compatTimeHours = 6;
			compatTimeMins = 0;
		} else {
			compatTimeDays = 0;
			compatTimeHours = 0;
			compatTimeMins = 0;
		}
	}

	// TC toggle — 더블클릭 시 호출
	function toggleTcById(tcId: number) {
		const next = new Map(pickedTcs);
		if (next.has(tcId)) {
			next.delete(tcId);
			pickedTcs = next;
			return;
		}
		const tc = currentTCs.find((t) => t.id === tcId);
		if (!tc) return;
		const defaults: Record<string, string> = {};
		const parsed = parseTcOption(tc.tcOption);
		const tcName = tc.name ?? tc.fileName ?? '';
		for (const opt of parsed) {
			defaults[opt.key] = getTcOptionDefault(opt.key, tcName);
		}
		if (!isCompatTab) defaults['t'] = getTcOptionDefault('t', tcName);
		next.set(tcId, defaults);
		pickedTcs = next;
	}

	function updateTcOption(tcId: number, key: string, value: string) {
		const next = new Map(pickedTcs);
		const opts = { ...next.get(tcId)! };
		opts[key] = value;
		next.set(tcId, opts);
		pickedTcs = next;
	}

	function moveTcInMap(tcId: number, direction: 'up' | 'down') {
		const entries = [...pickedTcs.entries()];
		const idx = entries.findIndex(([id]) => id === tcId);
		if (idx < 0) return;
		const swapIdx = direction === 'up' ? idx - 1 : idx + 1;
		if (swapIdx < 0 || swapIdx >= entries.length) return;
		[entries[idx], entries[swapIdx]] = [entries[swapIdx], entries[idx]];
		pickedTcs = new Map(entries);
	}

	// Drag-and-drop reorder
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

		const entries = [...pickedTcs.entries()];
		const fromIdx = entries.findIndex(([id]) => id === fromId);
		const toIdx = entries.findIndex(([id]) => id === targetTcId);
		if (fromIdx < 0 || toIdx < 0) return;
		const [moved] = entries.splice(fromIdx, 1);
		entries.splice(toIdx, 0, moved);
		pickedTcs = new Map(entries);
	}

	function onDragEnd() {
		dragTcId = null;
		dragOverTcId = null;
	}

	function reverseTcs() {
		if (pickedTcs.size < 2) return;
		pickedTcs = new Map([...pickedTcs.entries()].reverse());
	}

	function clearPickedTcs() {
		if (pickedTcs.size === 0) return;
		if (pickedTcs.size >= 5) {
			const ok = confirm(`선택된 TC ${pickedTcs.size}개를 모두 해제할까요?`);
			if (!ok) return;
		}
		pickedTcs = new Map();
	}

	function removeTc(tcId: number) {
		const next = new Map(pickedTcs);
		next.delete(tcId);
		pickedTcs = next;
	}

	// ── Apply settc ──
	async function applySetTC() {
		if (pickedTcs.size === 0) return;
		if (isCompatTab && (!Number.isFinite(compatTestTimeMin) || compatTestTimeMin <= 0)) {
			toast.error('Test time을 입력하세요 (days/hours/mins 중 하나 이상).');
			return;
		}
		commandBusy = true;

		const selectedItems = currentItems.filter((item) => selectedIds.has(item.slot.id));
		const { successCount, totalCount, lastError } = await applySettcToSlots({
			source: activeTab,
			isCompatTab,
			commandVariant,
			pickedTcs,
			compatTCs,
			compatTestTimeMin,
			selectedItems
		});

		if (lastError && successCount < totalCount) {
			toast.error(`Sent to ${successCount}/${totalCount} slots. Last error: ${lastError}`);
		}

		commandBusy = false;
		open = false;
		await onApplied?.();
	}
</script>

<Sheet.Root bind:open>
	<Sheet.Content
		side="right"
		class="w-screen flex flex-col max-h-[100dvh]"
		onInteractOutside={(e) => e.preventDefault()}
		onFocusOutside={(e) => e.preventDefault()}
	>
		<Sheet.Header>
			<Sheet.Title>Set Test Cases</Sheet.Title>
			<Sheet.Description>
				Applying to {selectedIds.size} slot(s) on <strong>{activeTab}</strong>
			</Sheet.Description>
		</Sheet.Header>

		<div class="flex-1 overflow-y-auto py-3 space-y-1">
			<!-- Global TC option defaults (performance) -->
			{#if !isCompatTab}
				<div class="rounded-md border bg-muted/30 p-2 mb-2 space-y-1.5">
					<div class="text-xs font-medium text-muted-foreground mb-1">Global Defaults</div>
					<div class="flex items-center gap-2">
						<label class="text-xs text-muted-foreground w-24 shrink-0">Test Size (s)</label>
						<input
							type="number"
							class="flex-1 h-6 px-2 text-xs rounded-md border border-border bg-background"
							placeholder={defaultTestSize ? `${defaultTestSize} (auto)` : 'Test Size'}
							value={globalTcOpts['s'] ?? ''}
							oninput={(e) => updateGlobalTcOpt('s', (e.target as HTMLInputElement).value)}
						/>
					</div>
					<div class="flex items-center gap-2">
						<label class="text-xs text-muted-foreground w-24 shrink-0">Test Loop (l)</label>
						<input
							type="number"
							class="flex-1 h-6 px-2 text-xs rounded-md border border-border bg-background"
							placeholder="Test Loop"
							value={globalTcOpts['l'] ?? ''}
							oninput={(e) => updateGlobalTcOpt('l', (e.target as HTMLInputElement).value)}
						/>
					</div>
					<div class="flex items-center gap-2">
						<label class="text-xs text-muted-foreground w-24 shrink-0">Trace (t)</label>
						<select
							class="flex-1 h-6 px-2 text-xs rounded-md border-2 border-border bg-background appearance-auto"
							value={globalTcOpts['t'] ?? '0'}
							onchange={(e) => updateGlobalTcOpt('t', (e.target as HTMLSelectElement).value)}
						>
							<option value="0">0</option>
							<option value="1">1</option>
						</select>
					</div>
				</div>
			{/if}

			<!-- TC Group Chips -->
			<div class="flex items-center gap-1.5 mb-2 flex-wrap">
				<span class="text-xs text-muted-foreground shrink-0">Groups:</span>
				{#each filteredTcGroups as group (group.id)}
					<ContextMenu.Root>
						<ContextMenu.Trigger>
							<button
								class="inline-flex items-center gap-1 px-2.5 py-0.5 rounded-full text-xs font-medium border transition-colors {isGroupFullySelected(
									group
								)
									? 'bg-primary text-primary-foreground border-primary'
									: 'bg-muted/50 hover:bg-muted border-border'}"
								onclick={() => applyTcGroup(group)}
								title="Click to toggle · Right-click to edit/delete"
							>
								{group.name}
								<span class="opacity-60"
									>({group.items.filter((i) => !hiddenTcIds.has(i.tcId)).length})</span
								>
							</button>
						</ContextMenu.Trigger>
						<ContextMenu.Content class="w-36">
							<ContextMenu.Item
								class="text-xs"
								onclick={() => tcGroupDialogRef?.openEdit(group)}
							>
								Edit Group
							</ContextMenu.Item>
							<ContextMenu.Separator />
							<ContextMenu.Item
								class="text-xs text-destructive"
								onclick={() => deleteGroup(group.id)}
							>
								Delete Group
							</ContextMenu.Item>
						</ContextMenu.Content>
					</ContextMenu.Root>
				{/each}
				<button
					class="inline-flex items-center gap-0.5 px-2 py-0.5 rounded-full text-xs border border-dashed border-border text-muted-foreground hover:bg-muted transition-colors"
					onclick={() => tcGroupDialogRef?.openSave()}
					title="Save current selection as a group"
				>
					+ Save Group
				</button>
			</div>

			<!-- Selected TC List (collapsible) — 호환성 탭에서는 testtime 아래에 표시, 그 외에는 여기에 표시 -->
			{#snippet selectedTcList()}
				{#if pickedTcList.length > 0}
					<div class="rounded-md border border-border bg-muted/20 mb-2">
						<div class="flex items-center w-full">
							<button
								class="flex items-center gap-1 flex-1 px-2 py-1 text-xs font-medium text-foreground hover:bg-muted/40 transition-colors"
								onclick={() => (selectedTcListOpen = !selectedTcListOpen)}
							>
								{#if selectedTcListOpen}
									<ChevronDown class="w-3 h-3" />
								{:else}
									<ChevronRight class="w-3 h-3" />
								{/if}
								Selected TCs ({pickedTcList.length})
							</button>
							<button
								class="inline-flex items-center gap-1 px-2 py-1 text-xs text-muted-foreground hover:text-foreground hover:bg-muted/40 transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
								disabled={pickedTcList.length < 2}
								onclick={reverseTcs}
								title="Reverse order"
							>
								<ArrowUpDown class="w-3 h-3" />
								Reverse
							</button>
							<button
								class="inline-flex items-center gap-1 px-2 py-1 text-xs text-muted-foreground hover:text-destructive hover:bg-muted/40 transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
								disabled={pickedTcList.length === 0}
								onclick={clearPickedTcs}
								title="Clear all"
							>
								<Trash2 class="w-3 h-3" />
								Clear
							</button>
						</div>
						{#if selectedTcListOpen}
							<div class="max-h-40 overflow-y-auto">
								<table class="w-full text-xs">
									<thead>
										<tr class="border-t text-muted-foreground">
											<th class="w-7 px-1 py-0.5 text-center">#</th>
											<th class="px-2 py-0.5 text-left">Name</th>
											{#if isCompatTab}
												<th class="w-20 px-1 py-0.5 text-center">Time</th>
											{/if}
											<th class="w-16 px-1 py-0.5 text-center">Order</th>
											<th class="w-7 px-1 py-0.5"></th>
										</tr>
									</thead>
									<tbody>
										{#each pickedTcList as [tcId, _opts], i (tcId)}
											{@const tc = currentTCs.find((t) => t.id === tcId)}
											<tr
												class="border-t hover:bg-muted/30 cursor-move transition-colors {dragOverTcId ===
												tcId
													? 'bg-primary/10'
													: ''} {dragTcId === tcId ? 'opacity-50' : ''}"
												draggable="true"
												ondragstart={(e) => onDragStart(e, tcId)}
												ondragover={(e) => onDragOver(e, tcId)}
												ondragleave={() => onDragLeave(tcId)}
												ondrop={(e) => onDrop(e, tcId)}
												ondragend={onDragEnd}
											>
												<td class="px-1 py-0.5 text-center text-muted-foreground">{i + 1}</td>
												<td class="px-2 py-0.5 truncate max-w-[280px]">
													<span class="text-muted-foreground mr-1">#{tcId}</span>{tc?.name ??
														tc?.fileName ??
														''}</td
												>
												{#if isCompatTab}
													<td
														class="px-1 py-0.5 text-center {compatTestTimeMin <= 0
															? 'text-destructive'
															: 'text-muted-foreground'}"
													>
														{formatCompatTime(compatTestTimeMin)}
													</td>
												{/if}
												<td class="px-1 py-0.5 text-center">
													<button
														class="inline-flex items-center px-0.5 text-muted-foreground hover:text-foreground disabled:opacity-30"
														disabled={i === 0}
														onclick={() => moveTcInMap(tcId, 'up')}
														title="Move up"
														><ChevronUp class="w-3 h-3" /></button
													>
													<button
														class="inline-flex items-center px-0.5 text-muted-foreground hover:text-foreground disabled:opacity-30"
														disabled={i === pickedTcList.length - 1}
														onclick={() => moveTcInMap(tcId, 'down')}
														title="Move down"
														><ChevronDown class="w-3 h-3" /></button
													>
												</td>
												<td class="px-1 py-0.5 text-center">
													<button
														class="inline-flex items-center text-muted-foreground hover:text-destructive"
														onclick={() => removeTc(tcId)}
														title="Remove"
														><X class="w-3 h-3" /></button
													>
												</td>
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
						{/if}
					</div>
				{/if}
			{/snippet}

			{#if !isCompatTab}
				{@render selectedTcList()}
			{/if}

			<!-- TC Search + Category Tabs -->
			<div class="flex items-center gap-2 mb-2">
				{#if isCompatTab || tcCategories.length > 2}
					<span class="text-xs text-muted-foreground shrink-0">Category:</span>
					<div class="flex rounded border text-xs">
						{#each tcCategories as cat, i (cat)}
							<button
								class="px-2 py-0.5 transition-colors {tcCategoryTab === cat
									? 'bg-primary text-primary-foreground'
									: 'hover:bg-muted'}"
								class:border-l={i > 0}
								onclick={() => (isCompatTab ? onCompatTabChange(cat) : switchTcCategory(cat))}
								>{cat}</button
							>
						{/each}
					</div>
				{/if}
				<button
					class="ml-auto inline-flex items-center gap-1 px-2 h-6 text-xs rounded-md border transition-colors {showPickedOnly
						? 'bg-primary text-primary-foreground border-primary'
						: 'border-border hover:bg-muted'}"
					onclick={() => (showPickedOnly = !showPickedOnly)}
					title="담긴 TC만 보기"
				>
					<Check class="w-3 h-3" />
					담긴 것만 ({pickedTcs.size})
				</button>
				<input
					type="text"
					class="h-6 w-48 px-2 text-xs rounded-md border border-border bg-background placeholder:text-muted-foreground"
					placeholder="Search TC name or ID..."
					bind:value={tcSearchQuery}
				/>
			</div>

			{#if !isCompatTab}
				<DataTable
					data={tcTableData}
					columns={tcTableColumns}
					enableRowSelection={false}
					enableColumnVisibility={false}
					showPagination={false}
					initialPageSize={9999}
					compact={true}
					getRowId={(row) => String(row.id)}
					highlightedRowIds={tcSelectedRowIds}
					onRowDoubleClick={(row) => toggleTcById(row.id)}
				>
					{#snippet expandableRowContent({ row: tcRow })}
						{@const tcId = tcRow.id}
						{@const rawOpts = pickedTcs.get(tcId)}
						{@const isSelected = rawOpts != null}
						{@const tc = currentTCs.find((t) => t.id === tcId)}
						{@const tcName = tc?.name ?? tc?.fileName ?? ''}
						{@const tcOpts = parseTcOption(tc?.tcOption)}
						<div class="space-y-1.5 py-1">
							{#each tcOpts as opt (opt.key)}
								{@const val = isSelected
									? (rawOpts[opt.key] ?? getTcOptionDefault(opt.key, tcName))
									: getTcOptionDefault(opt.key, tcName)}
								{@const schema = getTcOptionSchemaDef(opt.key, tcName)}
								{@const err = validateTcOptionValue(opt.key, val, tcName)}
								<div class="flex items-center gap-2">
									<label class="text-xs text-muted-foreground w-20 shrink-0"
										>{opt.label} ({opt.key})</label
									>
									{#if schema?.type === 'select' && schema.choices}
										<select
											class="flex-1 h-6 px-2 text-xs rounded-md border-2 bg-background appearance-auto {err
												? 'border-destructive'
												: 'border-border'}"
											value={val}
											disabled={!isSelected}
											onchange={(e) =>
												updateTcOption(tcId, opt.key, (e.target as HTMLSelectElement).value)}
										>
											<option value="">--</option>
											{#each schema.choices as c (c.value)}
												<option value={c.value}>{c.text}</option>
											{/each}
										</select>
									{:else}
										<input
											type={schema?.type === 'number' ? 'number' : 'text'}
											class="flex-1 h-6 px-2 text-xs rounded-md border-2 bg-background {err
												? 'border-destructive'
												: 'border-border'}"
											placeholder={opt.label}
											value={val}
											disabled={!isSelected}
											oninput={(e) =>
												updateTcOption(tcId, opt.key, (e.target as HTMLInputElement).value)}
										/>
									{/if}
								</div>
							{/each}
							{#if true}
								{@const traceVal = isSelected
									? (rawOpts['t'] ?? getTcOptionDefault('t', tcName))
									: getTcOptionDefault('t', tcName)}
								<div class="flex items-center gap-2">
									<label class="text-xs text-muted-foreground w-20 shrink-0">Trace (t)</label>
									<select
										class="flex-1 h-6 px-2 text-xs rounded-md border-2 border-border bg-background appearance-auto"
										value={traceVal}
										disabled={!isSelected}
										onchange={(e) =>
											updateTcOption(tcId, 't', (e.target as HTMLSelectElement).value)}
									>
										<option value="0">0</option>
										<option value="1">1</option>
									</select>
								</div>
							{/if}
							{#if !isSelected}
								<div class="text-xs text-muted-foreground italic">
									Select this TC to edit options
								</div>
							{/if}
						</div>
					{/snippet}
				</DataTable>
			{:else}
				<!-- 호환성 settc: testtime 설정 -->
				<div class="border rounded-md p-2 mb-2 bg-muted/20">
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
									class="px-1.5 h-5 text-[10px] rounded border transition-colors {activeQuickTime === p.key
										? 'bg-primary/15 border-primary/40 text-primary'
										: 'border-border/60 text-muted-foreground hover:bg-muted'}"
									onclick={() => applyQuickTime(p.key)}
									title={`${p.d}d ${p.h}h ${p.m}m`}
								>
									{p.label}
								</button>
							{/each}
						</div>
						<span class="text-xs ml-2 {compatTestTimeMin <= 0 ? 'text-destructive font-medium' : 'text-muted-foreground'}">
							= {compatTestTimeMin} min{compatTestTimeMin <= 0 ? ' (시간을 설정하세요)' : ''}
						</span>
					</div>
				</div>

				{@render selectedTcList()}

				<DataTable
					data={tcTableData}
					columns={tcTableColumns}
					enableRowSelection={false}
					enableColumnVisibility={false}
					showPagination={false}
					initialPageSize={9999}
					compact={true}
					getRowId={(row) => String(row.id)}
					highlightedRowIds={tcSelectedRowIds}
					onRowDoubleClick={(row) => toggleTcById(row.id)}
				/>
			{/if}

			<!-- Auto-filled info notice -->
			{#if pickedTcs.size > 0}
				<div
					class="mt-2 p-2 rounded-md bg-muted/50 text-xs text-muted-foreground space-y-0.5"
				>
					<div class="font-medium text-foreground">Auto-filled per slot:</div>
					<div>c (NandType) from TR · u (UFS ID) from Head · p (Slot Pos) from setLocation</div>
				</div>
			{/if}
		</div>

		<Sheet.Footer>
			<div class="flex items-center gap-2 w-full">
				<div class="flex flex-col gap-0.5">
					<span class={captionMuted}>
						{pickedTcs.size} selected
					</span>
					{#if tcValidationErrors.length > 0}
						<span class="text-xs text-destructive">{tcValidationErrors[0]}</span>
					{/if}
				</div>
				<div class="ml-auto flex items-center gap-2">
					<!-- settc / settc2 전환 토글 (기본: settc2) -->
					<div
						class="flex rounded-md border overflow-hidden"
						title="HEAD 명령 포맷 선택 — settc2(신규, 백틱 구분자 + 다건 일괄) / settc(구, prefix 포함; 호환성은 1 TC씩 반복 전송)"
					>
						<button
							class="px-2 py-1 text-xs transition-colors {commandVariant === 'settc2' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground'}"
							onclick={() => (commandVariant = 'settc2')}
							disabled={commandBusy}
						>settc2</button>
						<button
							class="px-2 py-1 text-xs transition-colors border-l {commandVariant === 'settc' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground'}"
							onclick={() => (commandVariant = 'settc')}
							disabled={commandBusy}
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
						disabled={commandBusy || pickedTcs.size === 0 || tcValidationErrors.length > 0 || (isCompatTab && compatTestTimeMin <= 0)}
						onclick={applySetTC}
						title={isCompatTab && compatTestTimeMin <= 0 ? 'Test time을 설정하세요' : ''}
					>
						{#if commandBusy}
							<LoaderCircle class="size-3 animate-spin" />
							Sending...
						{:else}
							Apply ({commandVariant})
						{/if}
					</button>
				</div>
			</div>
		</Sheet.Footer>
	</Sheet.Content>
</Sheet.Root>
