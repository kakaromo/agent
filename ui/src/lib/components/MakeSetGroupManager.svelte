<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import LogBrowserDialog from './LogBrowserDialog.svelte';
	import {
		fetchMakesetGroups,
		createMakesetGroup,
		updateMakesetGroup,
		deleteMakesetGroup,
		fetchMakesetBoards,
		fetchDdOptions,
		type MakesetGroup,
		type MakesetGroupItem,
		type DdOption
	} from '$lib/api/testdb.js';
	import ConfirmDialog from './ConfirmDialog.svelte';
	import { toast } from 'svelte-sonner';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import FolderOpenIcon from '@lucide/svelte/icons/folder-open';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import XIcon from '@lucide/svelte/icons/x';

	interface Props {
		open: boolean;
		source: string;
		onClose: () => void;
		onGroupsChanged: () => void;
	}

	let { open = $bindable(), source, onClose, onGroupsChanged }: Props = $props();

	let groups = $state<MakesetGroup[]>([]);
	let loading = $state(false);

	// Available boards from onedown -h
	let availableBoards = $state<string[]>([]);
	let boardsLoading = $state(false);

	// Edit form state
	let editing = $state(false);
	let editId = $state<number | null>(null);
	let editName = $state('');
	let editDesc = $state('');
	let editItems = $state<MakesetGroupItem[]>([]);

	// Per-item DD options: index → DdOption[]
	let itemDdOptions = $state<Record<number, DdOption[]>>({});
	let itemDdLoading = $state<Record<number, boolean>>({});

	// Confirm dialog
	let confirmOpen = $state(false);
	let confirmDeleteId = $state<number>(0);

	// LogBrowserDialog state
	let browseOpen = $state(false);
	let browsePath = $state('');
	let browseTitle = $state('');
	let browseSelectMode = $state(false);
	let browseFolderSelect = $state(false);
	let browseFileFilter: ((entry: { name: string; directory: boolean; size: number; lastModified: number }) => boolean) | undefined = $state(undefined);
	let browseTargetIndex = $state(0);
	let browseTargetField = $state<'provision' | 'image'>('provision');

	// open 이 false→true 로 전환되는 순간에만 1회 로드.
	// 단순히 `if (open)` 하면 dialog 가 열려있는 동안 다른 상태 변화로 effect 가 재실행될 때
	// 매번 fetch 가 발사되어 "로딩 중 / 목록" 이 깜빡일 수 있음.
	let lastOpen = false;
	$effect(() => {
		if (open && !lastOpen) {
			loadGroups();
			loadBoards();
		}
		lastOpen = open;
	});

	async function loadGroups() {
		if (loading) return;
		loading = true;
		try {
			groups = await fetchMakesetGroups();
		} catch (e) {
			console.error('Failed to load makeset groups:', e);
		} finally {
			loading = false;
		}
	}

	async function loadBoards() {
		if (boardsLoading) return;
		boardsLoading = true;
		try {
			availableBoards = await fetchMakesetBoards();
		} catch (e) {
			console.error('Failed to load boards:', e);
		} finally {
			boardsLoading = false;
		}
	}

	function startCreate() {
		loadBoards();
		editId = null;
		editName = '';
		editDesc = '';
		editItems = [{ board: '', provisionPath: '', imagePath: '', ddValue: 'none' }];
		itemDdOptions = {};
		itemDdLoading = {};
		editing = true;
	}

	function startEdit(group: MakesetGroup) {
		loadBoards();
		editId = group.id;
		editName = group.name;
		editDesc = group.description ?? '';
		editItems = group.items.map(i => ({
			board: i.board,
			provisionPath: i.provisionPath,
			imagePath: i.imagePath,
			ddValue: i.ddValue
		}));
		itemDdOptions = {};
		itemDdLoading = {};
		editItems.forEach((item, i) => {
			if (item.board && item.imagePath) {
				loadDdOptionsForItem(i, item.board);
			}
		});
		editing = true;
	}

	function addItem() {
		editItems = [...editItems, { board: '', provisionPath: '', imagePath: '', ddValue: 'none' }];
	}

	function removeItem(index: number) {
		editItems = editItems.filter((_, i) => i !== index);
		const newOpts: Record<number, DdOption[]> = {};
		const newLoading: Record<number, boolean> = {};
		for (const [k, v] of Object.entries(itemDdOptions)) {
			const ki = parseInt(k);
			if (ki < index) { newOpts[ki] = v; }
			else if (ki > index) { newOpts[ki - 1] = v; }
		}
		for (const [k, v] of Object.entries(itemDdLoading)) {
			const ki = parseInt(k);
			if (ki < index) { newLoading[ki] = v; }
			else if (ki > index) { newLoading[ki - 1] = v; }
		}
		itemDdOptions = newOpts;
		itemDdLoading = newLoading;
	}

	function handleBoardChange(index: number, board: string) {
		editItems[index].board = board;
		// board 변경 시 경로/DD 초기화
		editItems[index].provisionPath = '';
		editItems[index].imagePath = '';
		editItems[index].ddValue = 'none';
		editItems = [...editItems];
		// DD 옵션 초기화
		const { [index]: _, ...rest } = itemDdOptions;
		itemDdOptions = rest;
	}

	async function loadDdOptionsForItem(index: number, board: string) {
		if (!board) return;
		itemDdLoading = { ...itemDdLoading, [index]: true };
		try {
			const opts = await fetchDdOptions('HEAD', board);
			itemDdOptions = { ...itemDdOptions, [index]: opts };
		} catch (e) {
			console.error(`Failed to load DD options for ${board}:`, e);
			itemDdOptions = { ...itemDdOptions, [index]: [{ name: 'none', enabled: true }] };
		} finally {
			itemDdLoading = { ...itemDdLoading, [index]: false };
		}
	}

	function openProvisionBrowse(index: number) {
		const item = editItems[index];
		const boardName = item.board || '';
		// 보드 plat 결정:
		// - sm... : qct (Qualcomm)
		// - erd...: lsi (Samsung LSI) — 뒤 "_xxx" 접미사는 무시하고 erdNNNN 부분만 폴더명으로 사용
		// - 그 외 : mtk (MediaTek)
		const lower = boardName.toLowerCase();
		let boardSuffix: string;
		let boardDir: string;
		if (lower.startsWith('sm')) {
			boardSuffix = 'qct';
			boardDir = boardName;
		} else if (lower.startsWith('erd')) {
			boardSuffix = 'lsi';
			const underscoreIdx = boardName.indexOf('_');
			boardDir = underscoreIdx > 0 ? boardName.substring(0, underscoreIdx) : boardName;
		} else {
			boardSuffix = 'mtk';
			boardDir = boardName;
		}
		browsePath = boardName ? `/home/octo/testboard/onedown/${boardSuffix}/${boardDir}/provision` : '/home/octo/testboard/onedown';
		browseTitle = `Provision XML — ${boardName || '보드'}`;
		browseSelectMode = true;
		browseFolderSelect = false;
		browseFileFilter = (entry) => entry.name.toLowerCase().endsWith('.xml');
		browseTargetIndex = index;
		browseTargetField = 'provision';
		browseOpen = true;
	}

	function openImageBrowse(index: number) {
		const item = editItems[index];
		const boardName = item.board || '';
		browsePath = boardName ? `/home/octo/testboard/qual_image/${boardName}/1.image` : '/home/octo/testboard/qual_image';
		browseTitle = `Image Folder — ${boardName || '보드'}`;
		browseSelectMode = false;
		browseFolderSelect = true;
		browseFileFilter = undefined;
		browseTargetIndex = index;
		browseTargetField = 'image';
		browseOpen = true;
	}

	function handleBrowseSelect(filePath: string) {
		const idx = browseTargetIndex;
		if (idx < 0 || idx >= editItems.length) return;
		if (browseTargetField === 'provision') {
			editItems[idx].provisionPath = filePath;
		} else {
			editItems[idx].imagePath = filePath;
			const board = editItems[idx].board;
			if (board) {
				loadDdOptionsForItem(idx, board);
			}
		}
		editItems = [...editItems];
	}

	function displayPath(path: string): string {
		if (!path) return '';
		return path.split('/').pop() || path;
	}

	// 이미 선택된 보드 목록 (중복 방지)
	const usedBoards = $derived(new Set(editItems.map(i => i.board).filter(Boolean)));

	async function save() {
		if (!editName.trim()) {
			toast.error('그룹 이름을 입력하세요');
			return;
		}
		const validItems = editItems.filter(i => i.board.trim());
		if (validItems.length === 0) {
			toast.error('보드를 하나 이상 추가하세요');
			return;
		}

		try {
			const data = { name: editName.trim(), description: editDesc.trim() || undefined, items: validItems };
			if (editId) {
				await updateMakesetGroup(editId, data);
			} else {
				await createMakesetGroup(data);
			}
			editing = false;
			toast.success(editId ? '그룹이 수정되었습니다' : '그룹이 생성되었습니다');
			await loadGroups();
			onGroupsChanged();
		} catch (e: any) {
			toast.error(e instanceof Error ? e.message : '저장 실패');
		}
	}

	function requestDelete(id: number) {
		confirmDeleteId = id;
		confirmOpen = true;
	}

	async function executeDelete() {
		try {
			await deleteMakesetGroup(confirmDeleteId);
			toast.success('그룹이 삭제되었습니다');
			confirmOpen = false;
			await loadGroups();
			onGroupsChanged();
		} catch (e: any) {
			toast.error(e instanceof Error ? e.message : '삭제 실패');
		}
	}

	function cancelEdit() {
		editing = false;
	}
</script>

<ConfirmDialog
	bind:open={confirmOpen}
	title="삭제 확인"
	description="이 그룹을 삭제하시겠습니까?"
	confirmLabel="삭제"
	onConfirm={executeDelete}
	onCancel={() => { confirmOpen = false; }}
/>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) onClose(); }}>
	<Dialog.Content class="sm:max-w-[90vw] lg:max-w-4xl max-h-[80vh] overflow-y-auto">
		<Dialog.Header>
			<Dialog.Title class="text-sm font-semibold">
				{editing ? (editId ? 'MakeSet 그룹 편집' : '새 MakeSet 그룹') : 'MakeSet 그룹 관리'}
			</Dialog.Title>
		</Dialog.Header>

		{#if editing}
			<div class="space-y-3 py-2">
				<div class="flex items-center gap-2">
					<label class="text-xs text-muted-foreground w-16">이름:</label>
					<input
						type="text"
						bind:value={editName}
						class="flex-1 border rounded px-2 py-1 text-xs bg-background"
						placeholder="그룹 이름"
					/>
				</div>
				<div class="flex items-center gap-2">
					<label class="text-xs text-muted-foreground w-16">설명:</label>
					<input
						type="text"
						bind:value={editDesc}
						class="flex-1 border rounded px-2 py-1 text-xs bg-background"
						placeholder="선택사항"
					/>
				</div>

				<div class="border rounded-md overflow-x-auto">
					<table class="w-full text-xs">
						<thead>
							<tr class="border-b bg-muted/50">
								<th class="px-2 py-1.5 text-left font-medium whitespace-nowrap">Board</th>
								<th class="px-2 py-1.5 text-left font-medium">Provision</th>
								<th class="px-2 py-1.5 text-left font-medium">Image</th>
								<th class="px-2 py-1.5 text-left font-medium whitespace-nowrap">DD</th>
								<th class="px-2 py-1.5 w-8"></th>
							</tr>
						</thead>
						<tbody>
							{#each editItems as item, i}
								<tr class="border-b last:border-b-0">
									<td class="px-1 py-1">
										{#if boardsLoading}
											<LoaderIcon class="size-3 animate-spin text-muted-foreground" />
										{:else}
											<select
												value={item.board}
												onchange={(e) => handleBoardChange(i, e.currentTarget.value)}
												class="w-full border rounded px-1.5 py-0.5 text-xs bg-background"
											>
												<option value="">-- 보드 선택 --</option>
												{#each availableBoards as b}
													<option value={b} disabled={usedBoards.has(b) && item.board !== b}>
														{b}{usedBoards.has(b) && item.board !== b ? ' (사용중)' : ''}
													</option>
												{/each}
											</select>
										{/if}
									</td>
									<td class="px-1 py-1">
										<div class="flex items-center gap-1">
											<span class="flex-1 truncate font-mono text-[10px] text-foreground/70 min-w-0" title={item.provisionPath}>
												{item.provisionPath ? displayPath(item.provisionPath) : '(선택 안됨)'}
											</span>
											<button
												class="px-1 py-0.5 rounded border text-[10px] hover:bg-muted transition-colors shrink-0 flex items-center gap-0.5 disabled:opacity-40"
												onclick={() => openProvisionBrowse(i)}
												disabled={!item.board}
												title="Provision XML 선택"
											>
												<FolderOpenIcon class="size-2.5" />
											</button>
										</div>
									</td>
									<td class="px-1 py-1">
										<div class="flex items-center gap-1">
											<span class="flex-1 truncate font-mono text-[10px] text-foreground/70 min-w-0" title={item.imagePath}>
												{item.imagePath ? displayPath(item.imagePath) : '(선택 안됨)'}
											</span>
											<button
												class="px-1 py-0.5 rounded border text-[10px] hover:bg-muted transition-colors shrink-0 flex items-center gap-0.5 disabled:opacity-40"
												onclick={() => openImageBrowse(i)}
												disabled={!item.board}
												title="Image 폴더 선택"
											>
												<FolderOpenIcon class="size-2.5" />
											</button>
										</div>
									</td>
									<td class="px-1 py-1">
										{#if itemDdLoading[i]}
											<LoaderIcon class="size-3 animate-spin text-muted-foreground" />
										{:else if itemDdOptions[i] && itemDdOptions[i].length > 0}
											<select
												bind:value={item.ddValue}
												class="w-full border rounded px-1 py-0.5 text-xs bg-background"
											>
												{#each itemDdOptions[i] as opt (opt.name)}
													<option value={opt.name} disabled={!opt.enabled}>
														{opt.name}{opt.enabled ? '' : ' (N/A)'}
													</option>
												{/each}
											</select>
										{:else}
											<span class="text-[10px] text-muted-foreground px-1">
												{item.imagePath ? item.ddValue || 'none' : '—'}
											</span>
										{/if}
									</td>
									<td class="px-1 py-1 text-center">
										<button
											class="p-0.5 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors"
											onclick={() => removeItem(i)}
											title="삭제"
										>
											<XIcon class="size-3.5" />
										</button>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>

				<button
					class="flex items-center gap-1 text-xs text-blue-600 hover:text-blue-700"
					onclick={addItem}
				>
					<PlusIcon class="size-3" />
					보드 추가
				</button>
			</div>

			<Dialog.Footer>
				<button
					class="px-3 py-1.5 rounded border text-xs hover:bg-muted transition-colors"
					onclick={cancelEdit}
				>
					취소
				</button>
				<button
					class="px-3 py-1.5 rounded bg-blue-600 text-white text-xs font-medium hover:bg-blue-700 transition-colors"
					onclick={save}
				>
					{editId ? '수정' : '생성'}
				</button>
			</Dialog.Footer>
		{:else}
			<div class="py-2">
				{#if loading}
					<p class="text-xs text-muted-foreground text-center py-4">로딩 중...</p>
				{:else if groups.length === 0}
					<p class="text-xs text-muted-foreground text-center py-4">그룹이 없습니다</p>
				{:else}
					<div class="border rounded-md">
						<table class="w-full text-xs">
							<thead>
								<tr class="border-b bg-muted/50">
									<th class="px-2 py-1.5 text-left font-medium">이름</th>
									<th class="px-2 py-1.5 text-left font-medium">설명</th>
									<th class="px-2 py-1.5 text-center font-medium w-16">보드 수</th>
									<th class="px-2 py-1.5 w-20"></th>
								</tr>
							</thead>
							<tbody>
								{#each groups as group (group.id)}
									<tr class="border-b last:border-b-0 hover:bg-muted/30">
										<td class="px-2 py-1.5 font-medium">{group.name}</td>
										<td class="px-2 py-1.5 text-muted-foreground">{group.description ?? ''}</td>
										<td class="px-2 py-1.5 text-center">{group.items.length}</td>
										<td class="px-2 py-1.5">
											<div class="flex items-center gap-1 justify-end">
												<button
													class="p-1 rounded hover:bg-muted transition-colors"
													onclick={() => startEdit(group)}
													title="편집"
												>
													<PencilIcon class="size-3.5" />
												</button>
												<button
													class="p-1 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors"
													onclick={() => requestDelete(group.id)}
													title="삭제"
												>
													<TrashIcon class="size-3.5" />
												</button>
											</div>
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				{/if}
			</div>

			<Dialog.Footer>
				<button
					class="px-3 py-1.5 rounded border text-xs hover:bg-muted transition-colors"
					onclick={() => { open = false; onClose(); }}
				>
					닫기
				</button>
				<button
					class="px-3 py-1.5 rounded bg-blue-600 text-white text-xs font-medium hover:bg-blue-700 transition-colors flex items-center gap-1"
					onclick={startCreate}
				>
					<PlusIcon class="size-3" />
					새 그룹
				</button>
			</Dialog.Footer>
		{/if}
	</Dialog.Content>
</Dialog.Root>

<LogBrowserDialog
	bind:open={browseOpen}
	tentacleName="HEAD"
	initialPath={browsePath}
	title={browseTitle}
	selectMode={browseSelectMode}
	folderSelect={browseFolderSelect}
	fileFilter={browseFileFilter}
	onClose={() => { browseOpen = false; }}
	onSelect={handleBrowseSelect}
/>
