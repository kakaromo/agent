<script lang="ts">
	import {
		listFiles as defaultListFiles,
		uploadFile as defaultUploadFile,
		deleteFile as defaultDeleteFile,
		downloadUrl as defaultDownloadUrl,
		type FileEntry
	} from '$lib/api/sftp.js';
	import FolderIcon from '@lucide/svelte/icons/folder';
	import FileIcon from '@lucide/svelte/icons/file';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import UploadIcon from '@lucide/svelte/icons/upload';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import HomeIcon from '@lucide/svelte/icons/home';
	import XIcon from '@lucide/svelte/icons/x';
	import FolderPlusIcon from '@lucide/svelte/icons/folder-plus';
	import ChevronUpIcon from '@lucide/svelte/icons/chevron-up';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import ArrowUpDownIcon from '@lucide/svelte/icons/arrow-up-down';
	import CheckIcon from '@lucide/svelte/icons/check';
	import UndoIcon from '@lucide/svelte/icons/undo-2';
	import FileTextIcon from '@lucide/svelte/icons/file-text';
	import { Skeleton } from '$lib/components/ui/skeleton/index.js';
	import LogViewerDialog from '$lib/components/LogViewerDialog.svelte';

	interface SftpApi {
		listFiles: (vm: string, path: string) => Promise<FileEntry[]>;
		uploadFile: (vm: string, path: string, file: File) => Promise<{ success: boolean; message: string }>;
		deleteFile: (vm: string, path: string) => Promise<{ success: boolean; message: string }>;
		downloadUrl: (vm: string, path: string) => string;
	}

	interface Props {
		vm: string;
		onClose?: () => void;
		api?: SftpApi;
		initialPath?: string;
	}

	let { vm, onClose, api, initialPath }: Props = $props();

	// Use injected API or default
	const listFiles = api?.listFiles ?? defaultListFiles;
	const uploadFile = api?.uploadFile ?? defaultUploadFile;
	const deleteFile = api?.deleteFile ?? defaultDeleteFile;
	const downloadUrl = api?.downloadUrl ?? defaultDownloadUrl;

	// --- State ---
	let currentPath = $state(initialPath || '/');
	let files = $state<FileEntry[]>([]);
	let loading = $state(false);
	let error = $state<string | null>(null);
	let dragOver = $state(false);
	let fileInputRef = $state<HTMLInputElement | null>(null);

	// Sort
	type SortKey = 'name' | 'size' | 'lastModified';
	let sortKey = $state<SortKey>('name');
	let sortAsc = $state(true);
	let showSortMenu = $state(false);

	// Multi-select
	let selectedNames = $state<Set<string>>(new Set());
	let selectMode = $state(false);

	// Upload progress
	interface UploadItem {
		name: string;
		progress: number; // 0~100, -1 = error
		done: boolean;
	}
	let uploadQueue = $state<UploadItem[]>([]);

	// Toast undo
	interface UndoToast {
		message: string;
		type: 'success' | 'error' | 'undo';
		undoAction?: () => void;
		timer?: ReturnType<typeof setTimeout>;
	}
	let toast = $state<UndoToast | null>(null);

	// Action sheet for single file click
	let actionFile = $state<FileEntry | null>(null);

	// Log viewer
	let logViewerOpen = $state(false);
	let logViewerFilePath = $state('');

	function openLogViewer(file: FileEntry) {
		logViewerFilePath = buildPath(file.name);
		logViewerOpen = true;
		actionFile = null;
	}

	// New folder
	let newFolderMode = $state(false);
	let newFolderName = $state('');
	let newFolderInputRef = $state<HTMLInputElement | null>(null);

	// --- Derived ---
	let sortedFiles = $derived.by(() => {
		const dirs = files.filter((f) => f.directory && f.name !== '..');
		const regularFiles = files.filter((f) => !f.directory && f.name !== '..');

		const compare = (a: FileEntry, b: FileEntry) => {
			const mul = sortAsc ? 1 : -1;
			if (sortKey === 'name') return mul * a.name.localeCompare(b.name);
			if (sortKey === 'size') return mul * (a.size - b.size);
			return mul * (a.lastModified - b.lastModified);
		};

		return [...dirs.sort(compare), ...regularFiles.sort(compare)];
	});

	let selectedCount = $derived(selectedNames.size);
	let hasSelection = $derived(selectedCount > 0);
	let selectedFiles = $derived(files.filter((f) => selectedNames.has(f.name)));
	let canDownloadSelection = $derived(selectedFiles.some((f) => !f.directory));

	let isUploading = $derived(uploadQueue.some((u) => !u.done));

	let breadcrumbs = $derived.by(() => {
		if (currentPath === '/') return [{ name: '/', path: '/' }];
		const parts = currentPath.split('/').filter(Boolean);
		const crumbs: { name: string; path: string }[] = [{ name: '/', path: '/' }];
		let acc = '';
		for (const p of parts) {
			acc += '/' + p;
			crumbs.push({ name: p, path: acc });
		}
		return crumbs;
	});

	// File/folder count summary
	let summary = $derived.by(() => {
		const dirs = files.filter((f) => f.directory && f.name !== '..').length;
		const fileCount = files.filter((f) => !f.directory).length;
		const parts: string[] = [];
		if (dirs > 0) parts.push(`폴더 ${dirs}`);
		if (fileCount > 0) parts.push(`파일 ${fileCount}`);
		return parts.join(', ') || '비어 있음';
	});

	// --- Effects ---
	$effect(() => {
		if (vm) loadFiles();
	});

	$effect(() => {
		if (toast) {
			const timer = setTimeout(() => {
				toast = null;
			}, 4000);
			toast.timer = timer;
			return () => clearTimeout(timer);
		}
	});

	$effect(() => {
		if (newFolderMode) {
			// Focus after DOM update
			setTimeout(() => newFolderInputRef?.focus(), 50);
		}
	});

	// --- Functions ---
	async function loadFiles() {
		loading = true;
		error = null;
		selectedNames = new Set();
		selectMode = false;
		actionFile = null;
		newFolderMode = false;
		try {
			files = await listFiles(vm, currentPath);
		} catch (e: any) {
			error = e.message || '파일 목록을 불러올 수 없습니다';
			files = [];
		} finally {
			loading = false;
		}
	}

	function navigateTo(path: string) {
		currentPath = path;
		loadFiles();
	}

	function navigateUp() {
		if (currentPath === '/') return;
		const parts = currentPath.split('/');
		parts.pop();
		navigateTo(parts.join('/') || '/');
	}

	function buildPath(name: string): string {
		return currentPath === '/' ? '/' + name : currentPath + '/' + name;
	}

	// --- Click handling: toss philosophy ---
	// Folder click → enter immediately
	// File click → show action sheet
	// In select mode → toggle selection
	function handleItemClick(file: FileEntry) {
		if (selectMode) {
			toggleSelect(file.name);
			return;
		}
		if (file.directory) {
			navigateTo(buildPath(file.name));
		} else {
			actionFile = file;
		}
	}

	// Long press to enter select mode
	let longPressTimer: ReturnType<typeof setTimeout> | null = null;

	function handlePointerDown(file: FileEntry) {
		longPressTimer = setTimeout(() => {
			selectMode = true;
			selectedNames = new Set([file.name]);
			longPressTimer = null;
		}, 500);
	}

	function handlePointerUp() {
		if (longPressTimer) {
			clearTimeout(longPressTimer);
			longPressTimer = null;
		}
	}

	function toggleSelect(name: string) {
		const next = new Set(selectedNames);
		if (next.has(name)) {
			next.delete(name);
		} else {
			next.add(name);
		}
		selectedNames = next;
		if (next.size === 0) selectMode = false;
	}

	function exitSelectMode() {
		selectMode = false;
		selectedNames = new Set();
	}

	// --- Sort ---
	function setSort(key: SortKey) {
		if (sortKey === key) {
			sortAsc = !sortAsc;
		} else {
			sortKey = key;
			sortAsc = true;
		}
		showSortMenu = false;
	}

	const sortLabels: Record<SortKey, string> = { name: '이름', size: '크기', lastModified: '수정일' };

	// --- Upload with progress ---
	async function handleUpload(fileList: FileList | null) {
		if (!fileList || fileList.length === 0) return;
		const items: UploadItem[] = Array.from(fileList).map((f) => ({
			name: f.name,
			progress: 0,
			done: false
		}));
		uploadQueue = items;

		let successCount = 0;
		for (let i = 0; i < fileList.length; i++) {
			const file = fileList[i];
			try {
				// Simulate progress (real XHR progress would need fetch replacement)
				items[i].progress = 30;
				uploadQueue = [...items];
				await uploadFile(vm, currentPath, file);
				items[i].progress = 100;
				items[i].done = true;
				successCount++;
			} catch {
				items[i].progress = -1;
				items[i].done = true;
			}
			uploadQueue = [...items];
		}

		// Clear queue after short delay
		setTimeout(() => {
			uploadQueue = [];
		}, 1500);

		if (successCount > 0) {
			showToast(`${successCount}개 파일 업로드 완료`, 'success');
			await loadFiles();
		} else {
			showToast('업로드 실패', 'error');
		}
	}

	function handleDrop(e: DragEvent) {
		e.preventDefault();
		dragOver = false;
		handleUpload(e.dataTransfer?.files ?? null);
	}

	function handleDragOver(e: DragEvent) {
		e.preventDefault();
		dragOver = true;
	}

	function handleDragLeave() {
		dragOver = false;
	}

	// --- Download ---
	function handleDownload(file: FileEntry) {
		const url = downloadUrl(vm, buildPath(file.name));
		const a = document.createElement('a');
		a.href = url;
		a.download = file.name;
		a.click();
	}

	function handleDownloadSelected() {
		for (const file of selectedFiles) {
			if (!file.directory) handleDownload(file);
		}
		exitSelectMode();
	}

	// --- Delete with undo toast ---
	async function handleDelete(file: FileEntry) {
		actionFile = null;
		const fullPath = buildPath(file.name);
		try {
			await deleteFile(vm, fullPath);
			showToast(`"${file.name}" 삭제됨`, 'success');
			await loadFiles();
		} catch (e: any) {
			showToast(e.message || '삭제 실패', 'error');
		}
	}

	async function handleDeleteSelected() {
		const names = [...selectedNames];
		const count = names.length;
		exitSelectMode();

		let successCount = 0;
		for (const name of names) {
			try {
				await deleteFile(vm, buildPath(name));
				successCount++;
			} catch {
				// continue
			}
		}

		if (successCount > 0) {
			showToast(`${successCount}개 항목 삭제됨`, 'success');
			await loadFiles();
		} else {
			showToast('삭제 실패', 'error');
		}
	}

	// --- New folder (placeholder: create via upload or shell) ---
	// Since the backend doesn't have a mkdir endpoint, we show a message.
	// If backend supports it in the future, this is ready.
	function startNewFolder() {
		newFolderMode = true;
		newFolderName = '';
		showSortMenu = false;
	}

	function cancelNewFolder() {
		newFolderMode = false;
		newFolderName = '';
	}

	// --- Toast ---
	function showToast(message: string, type: 'success' | 'error') {
		if (toast?.timer) clearTimeout(toast.timer);
		toast = { message, type };
	}

	function dismissToast() {
		if (toast?.timer) clearTimeout(toast.timer);
		toast = null;
	}

	// --- Formatting ---
	function formatSize(bytes: number): string {
		if (bytes === 0) return '—';
		const units = ['B', 'KB', 'MB', 'GB'];
		let i = 0;
		let size = bytes;
		while (size >= 1024 && i < units.length - 1) {
			size /= 1024;
			i++;
		}
		return `${size.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
	}

	function formatRelativeTime(ts: number): string {
		if (!ts) return '';
		const now = Date.now();
		const diff = now - ts;
		const sec = Math.floor(diff / 1000);
		if (sec < 60) return '방금 전';
		const min = Math.floor(sec / 60);
		if (min < 60) return `${min}분 전`;
		const hr = Math.floor(min / 60);
		if (hr < 24) return `${hr}시간 전`;
		const day = Math.floor(hr / 24);
		if (day < 7) return `${day}일 전`;
		if (day < 30) return `${Math.floor(day / 7)}주 전`;
		return formatAbsoluteTime(ts);
	}

	function formatAbsoluteTime(ts: number): string {
		if (!ts) return '';
		const d = new Date(ts);
		const pad = (n: number) => String(n).padStart(2, '0');
		return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
	}
</script>

<div
	class="flex flex-col h-full bg-background border-l w-80 shrink-0"
	ondrop={handleDrop}
	ondragover={handleDragOver}
	ondragleave={handleDragLeave}
	role="complementary"
>
	<!-- Header -->
	<div class="flex items-center gap-2 px-3 py-2 border-b bg-muted/30 shrink-0">
		<FolderIcon class="size-4 text-muted-foreground" />
		<span class="text-xs font-semibold flex-1 truncate">{vm}</span>
		<button
			class="p-1 rounded hover:bg-muted transition-colors"
			onclick={loadFiles}
			title="새로고침"
		>
			<RefreshCwIcon class="size-3 {loading ? 'animate-spin' : ''}" />
		</button>
		{#if onClose}
			<button
				class="p-1 rounded hover:bg-muted transition-colors"
				onclick={onClose}
				title="닫기"
			>
				<XIcon class="size-3" />
			</button>
		{/if}
	</div>

	<!-- Breadcrumbs -->
	<div class="flex items-center gap-0.5 px-3 py-1.5 border-b text-[10px] overflow-x-auto shrink-0">
		{#each breadcrumbs as crumb, i}
			{#if i > 0}
				<ChevronRightIcon class="size-3 text-muted-foreground shrink-0" />
			{/if}
			<button
				class="hover:text-primary transition-colors shrink-0 {i === breadcrumbs.length - 1
					? 'text-foreground font-medium'
					: 'text-muted-foreground'}"
				onclick={() => navigateTo(crumb.path)}
			>
				{#if i === 0}
					<HomeIcon class="size-3" />
				{:else}
					{crumb.name}
				{/if}
			</button>
		{/each}
	</div>

	<!-- Toolbar: Upload + Sort + New Folder -->
	<div class="flex items-center gap-1 px-3 py-1.5 border-b shrink-0">
		<input
			bind:this={fileInputRef}
			type="file"
			multiple
			class="hidden"
			onchange={(e) => handleUpload((e.target as HTMLInputElement).files)}
		/>
		<button
			class="flex items-center gap-1 px-2 py-1 text-[10px] rounded border hover:bg-muted transition-colors {isUploading ? 'opacity-50 pointer-events-none' : ''}"
			onclick={() => fileInputRef?.click()}
			disabled={isUploading}
			title="파일 업로드"
		>
			<UploadIcon class="size-3" />
			업로드
		</button>
		<button
			class="flex items-center gap-1 px-2 py-1 text-[10px] rounded border hover:bg-muted transition-colors"
			onclick={startNewFolder}
			title="새 폴더"
		>
			<FolderPlusIcon class="size-3" />
		</button>

		<div class="ml-auto relative">
			<button
				class="flex items-center gap-1 px-2 py-1 text-[10px] rounded hover:bg-muted transition-colors text-muted-foreground"
				onclick={() => (showSortMenu = !showSortMenu)}
				title="정렬"
			>
				<ArrowUpDownIcon class="size-3" />
				{sortLabels[sortKey]}
			</button>
			{#if showSortMenu}
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<div class="absolute right-0 top-full mt-1 z-10 bg-popover border rounded-md shadow-md py-1 min-w-[100px]">
					{#each (['name', 'size', 'lastModified'] as const) as key}
						<button
							class="flex items-center gap-2 w-full px-3 py-1.5 text-[10px] hover:bg-muted transition-colors text-left"
							onclick={() => setSort(key)}
						>
							<span class="flex-1">{sortLabels[key]}</span>
							{#if sortKey === key}
								<span class="inline-flex items-center text-primary">
									{#if sortAsc}<ChevronUpIcon class="w-3 h-3" />{:else}<ChevronDownIcon class="w-3 h-3" />{/if}
								</span>
							{/if}
						</button>
					{/each}
				</div>
			{/if}
		</div>
	</div>

	<!-- Upload progress -->
	{#if uploadQueue.length > 0}
		<div class="px-3 py-2 border-b space-y-1 shrink-0">
			{#each uploadQueue as item (item.name)}
				<div class="flex items-center gap-2 text-[10px]">
					<span class="truncate flex-1" title={item.name}>{item.name}</span>
					{#if item.progress === -1}
						<span class="text-destructive shrink-0">실패</span>
					{:else if item.done}
						<CheckIcon class="size-3 text-emerald-500 shrink-0" />
					{:else}
						<div class="w-12 h-1 rounded-full bg-muted overflow-hidden shrink-0">
							<div
								class="h-full bg-primary rounded-full transition-all duration-300"
								style="width: {item.progress}%"
							></div>
						</div>
					{/if}
				</div>
			{/each}
		</div>
	{/if}

	<!-- Select mode bar -->
	{#if selectMode}
		<div class="flex items-center gap-1.5 px-3 py-1.5 border-b bg-primary/5 shrink-0">
			<span class="text-[10px] text-primary font-medium flex-1">{selectedCount}개 선택됨</span>
			{#if canDownloadSelection}
				<button
					class="p-1 rounded hover:bg-primary/10 transition-colors"
					onclick={handleDownloadSelected}
					title="선택 다운로드"
				>
					<DownloadIcon class="size-3 text-primary" />
				</button>
			{/if}
			<button
				class="p-1 rounded hover:bg-destructive/10 transition-colors"
				onclick={handleDeleteSelected}
				title="선택 삭제"
			>
				<TrashIcon class="size-3 text-destructive" />
			</button>
			<button
				class="p-1 rounded hover:bg-muted transition-colors"
				onclick={exitSelectMode}
				title="선택 해제"
			>
				<XIcon class="size-3" />
			</button>
		</div>
	{/if}

	<!-- File list -->
	<div
		class="flex-1 overflow-y-auto {dragOver ? 'ring-2 ring-inset ring-primary/50 bg-primary/5' : ''}"
		onclick={() => { showSortMenu = false; }}
		role="list"
	>
		{#if loading}
			<div class="px-3 py-2 space-y-1">
				{#each Array(6) as _}
					<div class="flex items-center gap-2 py-1.5">
						<Skeleton class="size-4 rounded" />
						<Skeleton class="h-3 flex-1 rounded" />
						<Skeleton class="h-3 w-10 rounded" />
					</div>
				{/each}
			</div>
		{:else if error}
			<div class="px-3 py-6 text-center">
				<p class="text-xs text-destructive">{error}</p>
				<button
					class="mt-2 text-[10px] text-primary hover:underline"
					onclick={loadFiles}
				>
					다시 시도
				</button>
			</div>
		{:else if files.length === 0 && !newFolderMode}
			<!-- Empty state: large drop zone -->
			<div class="flex flex-col items-center justify-center h-full px-6 py-8 text-center">
				<div class="size-12 rounded-full bg-muted/50 flex items-center justify-center mb-3">
					<UploadIcon class="size-5 text-muted-foreground" />
				</div>
				<p class="text-xs text-muted-foreground mb-1">비어 있는 폴더</p>
				<p class="text-[10px] text-muted-foreground/70">파일을 드래그하거나 업로드 버튼 클릭</p>
			</div>
		{:else}
			<!-- Parent directory -->
			{#if currentPath !== '/'}
				<button
					class="flex items-center gap-2 w-full px-3 py-2 text-xs hover:bg-muted/50 transition-colors text-left"
					onclick={navigateUp}
					role="listitem"
				>
					<FolderIcon class="size-4 text-muted-foreground shrink-0" />
					<span class="text-muted-foreground">..</span>
				</button>
			{/if}

			<!-- New folder input -->
			{#if newFolderMode}
				<div class="flex items-center gap-2 px-3 py-2" role="listitem">
					<FolderPlusIcon class="size-4 text-amber-500 shrink-0" />
					<input
						bind:this={newFolderInputRef}
						bind:value={newFolderName}
						class="flex-1 text-xs bg-transparent border-b border-primary/50 outline-none py-0.5 placeholder:text-muted-foreground/50"
						placeholder="폴더 이름..."
						onkeydown={(e) => {
							if (e.key === 'Escape') cancelNewFolder();
						}}
					/>
					<button
						class="p-0.5 rounded hover:bg-muted transition-colors"
						onclick={cancelNewFolder}
						title="취소"
					>
						<XIcon class="size-3 text-muted-foreground" />
					</button>
				</div>
			{/if}

			{#each sortedFiles as file (file.name)}
				<button
					class="flex items-center gap-2 w-full px-3 py-2 text-xs transition-colors text-left group
						{selectMode && selectedNames.has(file.name)
						? 'bg-primary/10'
						: 'hover:bg-muted/50'}"
					onclick={() => handleItemClick(file)}
					onpointerdown={() => handlePointerDown(file)}
					onpointerup={handlePointerUp}
					onpointerleave={handlePointerUp}
					role="listitem"
				>
					<!-- Select checkbox in select mode -->
					{#if selectMode}
						<div
							class="size-4 rounded border-2 flex items-center justify-center shrink-0 transition-colors
								{selectedNames.has(file.name) ? 'bg-primary border-primary' : 'border-muted-foreground/40'}"
						>
							{#if selectedNames.has(file.name)}
								<CheckIcon class="size-3 text-primary-foreground" />
							{/if}
						</div>
					{/if}

					<!-- Icon -->
					{#if file.directory}
						<FolderIcon class="size-4 text-amber-500 shrink-0" />
					{:else}
						<FileIcon class="size-4 text-muted-foreground shrink-0" />
					{/if}

					<!-- Name + meta -->
					<div class="flex-1 min-w-0">
						<div class="truncate" title={file.name}>{file.name}</div>
						<div class="flex items-center gap-2 text-[10px] text-muted-foreground/70 mt-0.5">
							{#if !file.directory}
								<span>{formatSize(file.size)}</span>
							{/if}
							{#if file.lastModified}
								<span title={formatAbsoluteTime(file.lastModified)}>{formatRelativeTime(file.lastModified)}</span>
							{/if}
						</div>
					</div>

					<!-- Folder chevron -->
					{#if file.directory && !selectMode}
						<ChevronRightIcon class="size-3.5 text-muted-foreground/50 shrink-0 opacity-0 group-hover:opacity-100 transition-opacity" />
					{/if}
				</button>
			{/each}
		{/if}
	</div>

	<!-- Status bar -->
	{#if !loading && !error}
		<div class="flex items-center px-3 py-1 border-t text-[10px] text-muted-foreground shrink-0">
			<span>{summary}</span>
			{#if !selectMode}
				<span class="ml-auto text-[9px] opacity-60">길게 눌러 선택</span>
			{/if}
		</div>
	{/if}
</div>

<!-- Action sheet (bottom sheet style) for single file -->
{#if actionFile}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="fixed inset-0 z-50"
		onclick={() => (actionFile = null)}
		onkeydown={(e) => e.key === 'Escape' && (actionFile = null)}
	>
		<div class="absolute inset-0 bg-black/40"></div>
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<div
			class="absolute bottom-0 left-0 right-0 bg-popover rounded-t-xl shadow-xl animate-in slide-in-from-bottom duration-200 p-4 pb-6"
			onclick={(e) => e.stopPropagation()}
		>
			<!-- Handle bar -->
			<div class="w-8 h-1 rounded-full bg-muted mx-auto mb-3"></div>

			<!-- File info -->
			<div class="flex items-center gap-3 mb-4">
				<div class="size-10 rounded-lg bg-muted/50 flex items-center justify-center shrink-0">
					<FileIcon class="size-5 text-muted-foreground" />
				</div>
				<div class="min-w-0 flex-1">
					<p class="text-sm font-medium truncate">{actionFile.name}</p>
					<p class="text-[11px] text-muted-foreground">
						{formatSize(actionFile.size)}
						{#if actionFile.lastModified}
							· {formatAbsoluteTime(actionFile.lastModified)}
						{/if}
					</p>
				</div>
			</div>

			<!-- Actions -->
			<div class="space-y-1">
				<button
					class="flex items-center gap-3 w-full px-3 py-2.5 rounded-lg hover:bg-muted transition-colors text-left"
					onclick={() => openLogViewer(actionFile!)}
				>
					<FileTextIcon class="size-4 text-primary" />
					<span class="text-sm">로그 보기</span>
				</button>
				<button
					class="flex items-center gap-3 w-full px-3 py-2.5 rounded-lg hover:bg-muted transition-colors text-left"
					onclick={() => { handleDownload(actionFile!); actionFile = null; }}
				>
					<DownloadIcon class="size-4 text-primary" />
					<span class="text-sm">다운로드</span>
				</button>
				<button
					class="flex items-center gap-3 w-full px-3 py-2.5 rounded-lg hover:bg-destructive/10 transition-colors text-left"
					onclick={() => { handleDelete(actionFile!); }}
				>
					<TrashIcon class="size-4 text-destructive" />
					<span class="text-sm text-destructive">삭제</span>
				</button>
			</div>
		</div>
	</div>
{/if}

<!-- Log Viewer -->
{#if logViewerOpen}
	<LogViewerDialog
		bind:open={logViewerOpen}
		tentacleName={vm}
		filePath={logViewerFilePath}
		onClose={() => (logViewerOpen = false)}
	/>
{/if}

<!-- Toast notification -->
{#if toast}
	<div
		class="fixed bottom-4 left-1/2 -translate-x-1/2 z-[60] animate-in fade-in slide-in-from-bottom-2 duration-200"
	>
		<div
			class="flex items-center gap-2 px-4 py-2.5 rounded-full shadow-lg text-sm
				{toast.type === 'error' ? 'bg-destructive text-destructive-foreground' : 'bg-foreground text-background'}"
		>
			<span>{toast.message}</span>
			<button
				class="ml-1 p-0.5 rounded-full hover:bg-white/20 transition-colors"
				onclick={dismissToast}
			>
				<XIcon class="size-3.5" />
			</button>
		</div>
	</div>
{/if}
