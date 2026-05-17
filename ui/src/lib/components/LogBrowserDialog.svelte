<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import LogViewerDialog from './LogViewerDialog.svelte';
	import FolderIcon from '@lucide/svelte/icons/folder';
	import FolderUpIcon from '@lucide/svelte/icons/folder-up';
	import FileTextIcon from '@lucide/svelte/icons/file-text';
	import EyeIcon from '@lucide/svelte/icons/eye';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import AlertCircleIcon from '@lucide/svelte/icons/alert-circle';
	import MaximizeIcon from '@lucide/svelte/icons/maximize';
	import MinimizeIcon from '@lucide/svelte/icons/minimize';
	import CheckIcon from '@lucide/svelte/icons/check';

	interface FileEntry {
		name: string;
		directory: boolean;
		size: number;
		lastModified: number;
	}

	interface Props {
		open: boolean;
		tentacleName: string;
		initialPath: string;
		title?: string;
		selectMode?: boolean;
		folderSelect?: boolean;
		fileFilter?: (entry: FileEntry) => boolean;
		/** 상위 이동 제한 경로. 설정하면 이 경로 이상으로 올라갈 수 없음 */
		rootPath?: string;
		onClose: () => void;
		onSelect?: (filePath: string) => void;
	}

	let { open = $bindable(), tentacleName, initialPath, title = 'Log Browser', selectMode = false, folderSelect = false, fileFilter, rootPath, onClose, onSelect }: Props = $props();

	const isAtRoot = $derived(rootPath ? currentPath === rootPath || currentPath === rootPath + '/' : false);
	const displayFiles = $derived.by(() => {
		let result = fileFilter ? files.filter(f => f.directory || fileFilter(f)) : files;
		// rootPath 설정 시 상위 이동(..) 엔트리 숨기기
		if (rootPath && isAtRoot) {
			result = result.filter(f => f.name !== '..');
		}
		return result;
	});

	// Log viewer state
	let viewerOpen = $state(false);
	let viewerFilePath = $state('');

	let currentPath = $state('');
	let files = $state<FileEntry[]>([]);
	let loading = $state(false);
	let error = $state('');
	let fullscreen = $state(false);

	// Track last loaded to avoid redundant fetches
	let lastLoaded = '';

	$effect(() => {
		if (open && initialPath) {
			const key = `${tentacleName}:${initialPath}`;
			if (lastLoaded !== key) {
				currentPath = initialPath;
				lastLoaded = key;
				loadFiles(initialPath);
			}
		}
		if (!open) {
			lastLoaded = '';
		}
	});

	async function loadFiles(path: string) {
		loading = true;
		error = '';
		try {
			const params = new URLSearchParams({ tentacleName, path });
			const res = await fetch(`/api/log-browser/files?${params}`);
			if (!res.ok) {
				const text = await res.text().catch(() => res.statusText);
				throw new Error(text);
			}
			files = await res.json();
			currentPath = path;
		} catch (e: any) {
			error = e.message || 'Failed to load files';
			files = [];
		} finally {
			loading = false;
		}
	}

	function navigateTo(entry: FileEntry) {
		if (entry.directory) {
			let newPath = entry.name === '..'
				? currentPath.replace(/\/[^/]+\/?$/, '') || '/'
				: `${currentPath.replace(/\/$/, '')}/${entry.name}`;
			// rootPath 이상으로 올라가지 못하게 방어
			if (rootPath && entry.name === '..') {
				const normalRoot = rootPath.replace(/\/$/, '');
				const normalNew = newPath.replace(/\/$/, '');
				if (normalNew.length < normalRoot.length || !normalNew.startsWith(normalRoot)) {
					return; // 이동 차단
				}
			}
			loadFiles(newPath);
		}
	}

	function selectFile(entry: FileEntry) {
		const filePath = `${currentPath.replace(/\/$/, '')}/${entry.name}`;
		onSelect?.(filePath);
		open = false;
		fullscreen = false;
		onClose();
	}

	function openViewer(entry: FileEntry) {
		viewerFilePath = `${currentPath.replace(/\/$/, '')}/${entry.name}`;
		viewerOpen = true;
	}

	function downloadFile(entry: FileEntry) {
		const filePath = `${currentPath.replace(/\/$/, '')}/${entry.name}`;
		const params = new URLSearchParams({ tentacleName, path: filePath });
		window.open(`/api/log-browser/download?${params}`, '_blank');
	}

	function formatSize(bytes: number): string {
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
		if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
		return `${(bytes / 1024 / 1024 / 1024).toFixed(1)} GB`;
	}

	function formatDate(ts: number): string {
		if (!ts) return '';
		return new Date(ts).toLocaleString('ko-KR', {
			year: 'numeric', month: '2-digit', day: '2-digit',
			hour: '2-digit', minute: '2-digit'
		});
	}
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) { fullscreen = false; onClose(); } }}>
	<Dialog.Content class="{fullscreen ? 'sm:max-w-none w-screen h-screen !rounded-none' : 'sm:max-w-5xl max-h-[80vh]'} flex flex-col transition-all">
		<button
			class="absolute end-12 top-4 rounded-xs opacity-70 transition-opacity hover:opacity-100 p-0 border-0 bg-transparent cursor-pointer"
			onclick={() => fullscreen = !fullscreen}
			title={fullscreen ? 'Exit fullscreen' : 'Fullscreen'}
		>
			{#if fullscreen}
				<MinimizeIcon class="size-4" />
			{:else}
				<MaximizeIcon class="size-4" />
			{/if}
		</button>
		<Dialog.Header>
			<Dialog.Title class="text-sm font-semibold pr-16">{title}</Dialog.Title>
			<Dialog.Description>
				<span class="font-mono text-xs text-muted-foreground break-all">{tentacleName}:{currentPath}</span>
			</Dialog.Description>
		</Dialog.Header>

		<div class="flex-1 overflow-auto min-h-0 border rounded-md">
			{#if loading}
				<div class="flex items-center justify-center p-8 text-muted-foreground">
					<LoaderIcon class="size-5 animate-spin mr-2" />
					Loading...
				</div>
			{:else if error}
				<div class="flex items-center justify-center p-8 text-destructive gap-2">
					<AlertCircleIcon class="size-5" />
					<span class="text-sm">{error}</span>
				</div>
			{:else if displayFiles.length === 0}
				<div class="flex items-center justify-center p-8 text-muted-foreground text-sm">
					Empty directory
				</div>
			{:else}
				<table class="w-full text-xs">
					<thead class="bg-muted/50 sticky top-0">
						<tr class="border-b">
							<th class="text-left px-3 py-2 font-medium">Name</th>
							<th class="text-right px-3 py-2 font-medium w-20">Size</th>
							<th class="text-right px-3 py-2 font-medium w-36">Modified</th>
							<th class="text-center px-3 py-2 font-medium w-16"></th>
						</tr>
					</thead>
					<tbody>
						{#each displayFiles as entry (entry.name)}
							<tr class="border-b last:border-b-0 hover:bg-muted/30 transition-colors">
								<td class="px-3 py-1.5">
									{#if entry.directory}
										<button
											class="flex items-center gap-1.5 text-left hover:text-blue-600 transition-colors w-full"
											onclick={() => navigateTo(entry)}
										>
											{#if entry.name === '..'}
												<FolderUpIcon class="size-4 text-amber-600 shrink-0" />
											{:else}
												<FolderIcon class="size-4 text-amber-500 shrink-0" />
											{/if}
											<span class="truncate">{entry.name}</span>
										</button>
									{:else}
										<button
											class="flex items-center gap-1.5 text-left hover:text-blue-600 transition-colors w-full"
											onclick={() => selectMode ? selectFile(entry) : openViewer(entry)}
										>
											<FileTextIcon class="size-4 text-slate-400 shrink-0" />
											<span class="truncate">{entry.name}</span>
										</button>
									{/if}
								</td>
								<td class="text-right px-3 py-1.5 text-muted-foreground font-mono">
									{entry.directory ? '—' : formatSize(entry.size)}
								</td>
								<td class="text-right px-3 py-1.5 text-muted-foreground">
									{formatDate(entry.lastModified)}
								</td>
								<td class="text-center px-3 py-1.5">
									{#if entry.directory && entry.name !== '..' && folderSelect}
										<button
											class="p-1 rounded hover:bg-emerald-100 text-emerald-600 transition-colors"
											title="Select folder"
											onclick={() => selectFile(entry)}
										>
											<CheckIcon class="size-3.5" />
										</button>
									{:else if !entry.directory}
										<div class="flex items-center justify-center gap-0.5">
											{#if selectMode}
												<button
													class="p-1 rounded hover:bg-emerald-100 text-emerald-600 transition-colors"
													title="Select"
													onclick={() => selectFile(entry)}
												>
													<CheckIcon class="size-3.5" />
												</button>
											{:else}
												<button
													class="p-1 rounded hover:bg-blue-100 text-blue-600 transition-colors"
													title="View"
													onclick={() => openViewer(entry)}
												>
													<EyeIcon class="size-3.5" />
												</button>
											{/if}
											<button
												class="p-1 rounded hover:bg-blue-100 text-blue-600 transition-colors"
												title="Download"
												onclick={() => downloadFile(entry)}
											>
												<DownloadIcon class="size-3.5" />
											</button>
										</div>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>

<LogViewerDialog
	bind:open={viewerOpen}
	{tentacleName}
	filePath={viewerFilePath}
	onClose={() => { viewerOpen = false; }}
/>
