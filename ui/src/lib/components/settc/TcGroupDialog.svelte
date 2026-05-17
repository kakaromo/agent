<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ChevronUp from '@lucide/svelte/icons/chevron-up';
	import ChevronDown from '@lucide/svelte/icons/chevron-down';
	import ArrowUpDown from '@lucide/svelte/icons/arrow-up-down';
	import X from '@lucide/svelte/icons/x';
	import {
		createTcGroup,
		updateTcGroup,
		type TcGroup
	} from '$lib/api/testdb.js';
	import { suspend as suspendEntityChange } from '$lib/stores/entityChange.svelte.js';
	import type {
		CompatibilityTestCase,
		PerformanceTestCase
	} from '$lib/api/types.js';

	type TcItem = CompatibilityTestCase | PerformanceTestCase;

	interface Props {
		activeTab: string;
		currentTCs: TcItem[];
		currentVisibleTCs: TcItem[];
		pickedTcs: Map<number, Record<string, string>>;
		hiddenTcIds: Set<number>;
		onSaved?: () => void | Promise<void>;
	}

	let {
		activeTab,
		currentTCs,
		currentVisibleTCs,
		pickedTcs,
		hiddenTcIds,
		onSaved
	}: Props = $props();

	let open = $state(false);
	let editId = $state<number | null>(null);
	let name = $state('');
	let desc = $state('');
	let items = $state<{ tcId: number; name: string }[]>([]);
	let searchQuery = $state('');

	// Drag-and-drop reorder
	let dragTcId = $state<number | null>(null);
	let dragOverTcId = $state<number | null>(null);

	// dialog 가 열린 동안엔 외부 변경에 의한 자동 새로고침 보류 — 입력 중 데이터 사라짐 방지.
	$effect(() => {
		if (!open) return;
		return suspendEntityChange(`tcGroupDialog-${editId ?? 'new'}`);
	});

	const searchResults = $derived.by(() => {
		const q = searchQuery.trim().toLowerCase();
		if (!q) return [];
		const existingIds = new Set(items.map((i) => i.tcId));
		return currentVisibleTCs
			.filter((tc) => {
				if (existingIds.has(tc.id)) return false;
				const n = (tc.name ?? tc.fileName ?? '').toLowerCase();
				return n.includes(q) || String(tc.id).includes(q);
			})
			.slice(0, 20);
	});

	export function openSave() {
		editId = null;
		name = '';
		desc = '';
		searchQuery = '';
		items = [...pickedTcs.keys()].map((tcId) => {
			const tc = currentTCs.find((t) => t.id === tcId);
			return { tcId, name: tc?.name ?? tc?.fileName ?? `TC#${tcId}` };
		});
		open = true;
	}

	export function openEdit(group: TcGroup) {
		editId = group.id;
		name = group.name;
		desc = group.description ?? '';
		searchQuery = '';
		items = [...group.items]
			.filter((item) => !hiddenTcIds.has(item.tcId))
			.sort((a, b) => a.sortOrder - b.sortOrder)
			.map((item) => {
				const tc = currentTCs.find((t) => t.id === item.tcId);
				return { tcId: item.tcId, name: tc?.name ?? tc?.fileName ?? `TC#${item.tcId}` };
			});
		open = true;
	}

	function addTc(tc: TcItem) {
		items = [...items, { tcId: tc.id, name: tc.name ?? tc.fileName ?? '' }];
		searchQuery = '';
	}

	function moveItem(index: number, direction: 'up' | 'down') {
		const swapIdx = direction === 'up' ? index - 1 : index + 1;
		if (swapIdx < 0 || swapIdx >= items.length) return;
		const next = [...items];
		[next[index], next[swapIdx]] = [next[swapIdx], next[index]];
		items = next;
	}

	function reverseItems() {
		if (items.length < 2) return;
		items = [...items].reverse();
	}

	function removeItem(index: number) {
		items = items.filter((_, i) => i !== index);
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

		const fromIdx = items.findIndex((i) => i.tcId === fromId);
		const toIdx = items.findIndex((i) => i.tcId === targetTcId);
		if (fromIdx < 0 || toIdx < 0) return;
		const next = [...items];
		const [moved] = next.splice(fromIdx, 1);
		next.splice(toIdx, 0, moved);
		items = next;
	}

	function onDragEnd() {
		dragTcId = null;
		dragOverTcId = null;
	}

	async function save() {
		const tcIds = items.map((i) => i.tcId);
		if (!name.trim() || tcIds.length === 0) return;

		try {
			if (editId) {
				await updateTcGroup(editId, {
					name: name.trim(),
					tcType: activeTab,
					description: desc.trim() || undefined,
					tcIds
				});
			} else {
				await createTcGroup({
					name: name.trim(),
					tcType: activeTab,
					description: desc.trim() || undefined,
					tcIds
				});
			}
			open = false;
			await onSaved?.();
		} catch (e: any) {
			console.error('Failed to save TC group:', e);
		}
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="sm:max-w-[400px]">
		<Dialog.Header>
			<Dialog.Title>{editId ? 'Edit TC Group' : 'Save TC Group'}</Dialog.Title>
			<Dialog.Description>
				{editId ? 'Update the group name, description, and TC list.' : 'Save the currently selected TCs as a reusable group.'}
			</Dialog.Description>
		</Dialog.Header>
		<div class="space-y-3 py-2">
			<div>
				<label class="text-xs font-medium text-muted-foreground">Name</label>
				<input
					type="text"
					class="w-full h-8 px-3 text-xs rounded-md border border-border bg-background mt-1"
					placeholder="Group name"
					bind:value={name}
				/>
			</div>
			<div>
				<label class="text-xs font-medium text-muted-foreground">Description</label>
				<input
					type="text"
					class="w-full h-8 px-3 text-xs rounded-md border border-border bg-background mt-1"
					placeholder="Optional description"
					bind:value={desc}
				/>
			</div>
			<div>
				<div class="flex items-center justify-between">
					<label class="text-xs font-medium text-muted-foreground">TCs ({items.length})</label>
					<button
						class="inline-flex items-center gap-1 px-2 py-0.5 text-xs text-muted-foreground hover:text-foreground hover:bg-muted/40 rounded transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
						disabled={items.length < 2}
						onclick={reverseItems}
						title="Reverse order"
					>
						<ArrowUpDown class="w-3 h-3" />
						Reverse
					</button>
				</div>
				<div class="mt-1 max-h-48 overflow-y-auto rounded-md border border-border bg-muted/30">
					{#if items.length === 0}
						<div class="text-xs text-muted-foreground italic p-2">No TCs</div>
					{:else}
						<table class="w-full text-xs">
							<tbody>
								{#each items as item, i (item.tcId)}
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
										<td class="w-6 px-1 py-0.5 text-center text-muted-foreground">{i + 1}</td>
										<td class="w-10 px-1 py-0.5 text-center text-muted-foreground">{item.tcId}</td>
										<td class="px-2 py-0.5 truncate max-w-[200px]">{item.name}</td>
										<td class="w-14 px-1 py-0.5 text-center">
											<button
												class="inline-flex items-center px-0.5 text-muted-foreground hover:text-foreground disabled:opacity-30"
												disabled={i === 0}
												onclick={() => moveItem(i, 'up')}
											><ChevronUp class="w-3 h-3" /></button>
											<button
												class="inline-flex items-center px-0.5 text-muted-foreground hover:text-foreground disabled:opacity-30"
												disabled={i === items.length - 1}
												onclick={() => moveItem(i, 'down')}
											><ChevronDown class="w-3 h-3" /></button>
										</td>
										<td class="w-6 px-1 py-0.5 text-center">
											<button
												class="inline-flex items-center text-muted-foreground hover:text-destructive"
												onclick={() => removeItem(i)}
											><X class="w-3 h-3" /></button>
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					{/if}
				</div>
			</div>
			<div>
				<label class="text-xs font-medium text-muted-foreground">Add TC</label>
				<input
					type="text"
					class="w-full h-8 px-3 text-xs rounded-md border border-border bg-background mt-1"
					placeholder="Search TC name or ID..."
					bind:value={searchQuery}
				/>
				{#if searchResults.length > 0}
					<div class="mt-1 max-h-32 overflow-y-auto rounded-md border border-border bg-muted/30">
						<table class="w-full text-xs">
							<tbody>
								{#each searchResults as tc (tc.id)}
									<tr
										class="border-b last:border-b-0 hover:bg-primary/10 cursor-pointer transition-colors"
										onclick={() => addTc(tc)}
									>
										<td class="w-10 px-1 py-0.5 text-center text-muted-foreground">{tc.id}</td>
										<td class="px-2 py-0.5 truncate max-w-[260px]">{tc.name ?? tc.fileName ?? ''}</td>
										<td class="w-6 px-1 py-0.5 text-center text-primary">+</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{:else if searchQuery.trim()}
					<div class="mt-1 text-xs text-muted-foreground italic px-2">No matching TCs</div>
				{/if}
			</div>
		</div>
		<Dialog.Footer>
			<button
				class="rounded-md border px-3 py-1.5 text-xs hover:bg-muted transition-colors"
				onclick={() => (open = false)}
			>Cancel</button>
			<button
				class="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-50"
				disabled={!name.trim() || items.length === 0}
				onclick={save}
			>Save</button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
