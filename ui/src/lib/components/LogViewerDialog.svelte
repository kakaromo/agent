<script lang="ts">
	import { tick } from 'svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import SearchIcon from '@lucide/svelte/icons/search';
	import ArrowDownToLineIcon from '@lucide/svelte/icons/arrow-down-to-line';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import ChevronUpIcon from '@lucide/svelte/icons/chevron-up';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import AlertCircleIcon from '@lucide/svelte/icons/alert-circle';
	import XIcon from '@lucide/svelte/icons/x';
	import MaximizeIcon from '@lucide/svelte/icons/maximize';
	import MinimizeIcon from '@lucide/svelte/icons/minimize';

	interface Props {
		open: boolean;
		tentacleName: string;
		filePath: string;
		onClose: () => void;
	}

	let { open = $bindable(), tentacleName, filePath, onClose }: Props = $props();

	// View state
	let lines: { lineNumber: number; text: string }[] = $state([]);
	let totalLines = $state(0);
	let loading = $state(false);
	let loadingMore = $state(false);
	let error = $state('');
	let currentEndLine = $state(0);
	let currentStartLine = $state(0);
	let hasMore = $state(true);
	let highlightLine = $state(-1);

	// Search state
	let searchQuery = $state('');
	let lastSearchQuery = $state('');
	let searchResults: { lineNumber: number; text: string }[] = $state([]);
	let searching = $state(false);
	let showSearchResults = $state(false);
	let searchError = $state('');
	// 리터럴(기본) ↔ 정규식 토글. UltraEdit/VS Code 의 .* 토글과 동일.
	let regexMode = $state(false);
	let currentMatchIndex = $state(-1);
	let fullscreen = $state(false);

	// Scroll position indicator
	let visibleStartLine = $state(0);
	let visibleEndLine = $state(0);

	// Refs
	let scrollContainer: HTMLDivElement | undefined = $state(undefined);

	// Binary force-open state
	let isBinaryError = $state(false);
	let forceOpen = $state(false);

	// Track loaded file to avoid re-fetching
	let lastLoadedFile = '';

	// Set of matching line numbers for highlight in file content view
	const matchingLines = $derived(new Set(searchResults.map(r => r.lineNumber)));
	const hasActiveSearch = $derived(lastSearchQuery.length > 0 && searchResults.length > 0);

	$effect(() => {
		if (open && filePath) {
			const key = `${tentacleName}:${filePath}`;
			if (lastLoadedFile !== key) {
				lastLoadedFile = key;
				resetState();
				loadLines(1, 1000, true);
			}
		}
		if (!open) {
			lastLoadedFile = '';
		}
	});

	function resetState() {
		lines = [];
		totalLines = 0;
		error = '';
		isBinaryError = false;
		forceOpen = false;
		currentEndLine = 0;
		currentStartLine = 0;
		hasMore = true;
		highlightLine = -1;
		searchQuery = '';
		lastSearchQuery = '';
		searchResults = [];
		showSearchResults = false;
		searchError = '';
		currentMatchIndex = -1;
		visibleStartLine = 0;
		visibleEndLine = 0;
	}

	async function loadLines(startLine: number, limit: number, replace = false) {
		if (replace) {
			loading = true;
		} else {
			loadingMore = true;
		}
		error = '';

		try {
			const params = new URLSearchParams({
				tentacleName,
				path: filePath,
				startLine: String(startLine),
				limit: String(limit)
			});
			if (forceOpen) params.set('force', 'true');
			const res = await fetch(`/api/log-browser/view/lines?${params}`);

			if (res.status === 422) {
				const data = await res.json();
				if (data.binary) {
					isBinaryError = true;
				}
				error = data.error || 'Binary file — cannot be viewed';
				return;
			}
			if (!res.ok) {
				throw new Error(await res.text().catch(() => res.statusText));
			}

			const data = await res.json();
			totalLines = data.totalLines;

			const newLines = parseContentToLines(data.content, data.startLine);

			if (replace) {
				lines = newLines;
			} else {
				lines = [...lines, ...newLines];
			}

			if (lines.length > 0) {
				currentStartLine = lines[0].lineNumber;
				currentEndLine = lines[lines.length - 1].lineNumber;
			}
			hasMore = currentEndLine < totalLines;
		} catch (e: any) {
			error = e.message || 'Failed to load file';
		} finally {
			loading = false;
			loadingMore = false;
		}
	}

	function parseContentToLines(content: string, startLine: number) {
		if (!content) return [];
		const rawLines = content.split('\n');
		if (rawLines.length > 0 && rawLines[rawLines.length - 1] === '' && content.endsWith('\n')) {
			rawLines.pop();
		}
		return rawLines.map((text: string, i: number) => ({
			lineNumber: startLine + i,
			text
		}));
	}

	function loadMore() {
		if (loadingMore || !hasMore) return;
		loadLines(currentEndLine + 1, 1000);
	}

	async function loadLast() {
		showSearchResults = false;
		highlightLine = -1;

		loading = true;
		error = '';

		try {
			const params = new URLSearchParams({
				tentacleName,
				path: filePath,
				lines: '2000'
			});
			const res = await fetch(`/api/log-browser/view/last?${params}`);
			if (!res.ok) throw new Error(await res.text().catch(() => res.statusText));

			const data = await res.json();
			totalLines = data.totalLines;

			lines = parseContentToLines(data.content, data.startLine);
			if (lines.length > 0) {
				currentStartLine = lines[0].lineNumber;
				currentEndLine = lines[lines.length - 1].lineNumber;
			}
			hasMore = false;
		} catch (e: any) {
			error = e.message || 'Failed to load last lines';
		} finally {
			loading = false;
		}

		await tick();
		if (scrollContainer) {
			scrollContainer.scrollTop = scrollContainer.scrollHeight;
		}
	}

	async function search() {
		if (!searchQuery.trim()) return;
		searching = true;
		searchError = '';
		searchResults = [];
		currentMatchIndex = -1;

		try {
			const params = new URLSearchParams({
				tentacleName,
				path: filePath,
				pattern: searchQuery,
				regex: String(regexMode)
			});
			const res = await fetch(`/api/log-browser/view/search?${params}`);
			if (!res.ok) throw new Error(await res.text().catch(() => res.statusText));

			searchResults = await res.json();
			lastSearchQuery = searchQuery;
			showSearchResults = true;

			if (searchResults.length === 0) {
				searchError = 'No matches found';
			}
		} catch (e: any) {
			searchError = e.message || 'Search failed';
			showSearchResults = true;
		} finally {
			searching = false;
		}
	}

	async function goToSearchResult(result: { lineNumber: number; text: string }, index?: number) {
		showSearchResults = false;
		highlightLine = result.lineNumber;
		if (index !== undefined) {
			currentMatchIndex = index;
		}

		const startLine = Math.max(1, result.lineNumber - 1000);
		const limit = 2001;
		await loadLines(startLine, limit, true);

		await tick();
		requestAnimationFrame(() => {
			const el = document.getElementById(`log-line-${result.lineNumber}`);
			if (el) {
				el.scrollIntoView({ block: 'center', behavior: 'smooth' });
			}
		});
	}

	function goToPrevMatch() {
		if (searchResults.length === 0) return;
		const newIndex = currentMatchIndex <= 0 ? searchResults.length - 1 : currentMatchIndex - 1;
		goToSearchResult(searchResults[newIndex], newIndex);
	}

	function goToNextMatch() {
		if (searchResults.length === 0) return;
		const newIndex = currentMatchIndex >= searchResults.length - 1 ? 0 : currentMatchIndex + 1;
		goToSearchResult(searchResults[newIndex], newIndex);
	}

	function backToResults() {
		showSearchResults = true;
		highlightLine = -1;
	}

	function clearSearch() {
		searchQuery = '';
		lastSearchQuery = '';
		searchResults = [];
		showSearchResults = false;
		searchError = '';
		currentMatchIndex = -1;
		highlightLine = -1;
	}

	function handleScroll(e: Event) {
		const el = e.target as HTMLElement;
		// Infinite scroll
		if (el.scrollHeight - el.scrollTop - el.clientHeight < 200 && hasMore && !loadingMore && !loading) {
			loadMore();
		}
		// Update visible line indicator
		updateVisibleLines();
	}

	function updateVisibleLines() {
		if (!scrollContainer || lines.length === 0) return;
		const containerRect = scrollContainer.getBoundingClientRect();
		// Find first visible line
		const firstEl = document.elementFromPoint(containerRect.left + 10, containerRect.top + 5);
		const firstLine = firstEl?.closest('[id^="log-line-"]');
		// Find last visible line
		const lastEl = document.elementFromPoint(containerRect.left + 10, containerRect.bottom - 5);
		const lastLine = lastEl?.closest('[id^="log-line-"]');

		if (firstLine) {
			visibleStartLine = parseInt(firstLine.id.replace('log-line-', '')) || 0;
		}
		if (lastLine) {
			visibleEndLine = parseInt(lastLine.id.replace('log-line-', '')) || 0;
		}
	}

	function handleSearchKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter') {
			if (e.shiftKey && hasActiveSearch && !showSearchResults) {
				goToPrevMatch();
			} else if (hasActiveSearch && !showSearchResults) {
				goToNextMatch();
			} else {
				search();
			}
		}
	}

	// Ctrl+A: 검색 결과가 있을 때 전체 텍스트를 클립보드에 복사
	let copied = $state(false);

	let copyArea: HTMLTextAreaElement | undefined = $state(undefined);

	function copyToClipboard(text: string) {
		if (!copyArea) return;
		copyArea.value = text;
		copyArea.focus();
		copyArea.select();
		document.execCommand('copy');
		copied = true;
		setTimeout(() => (copied = false), 2000);
	}

	$effect(() => {
		if (!open) return;
		function onKeydown(e: KeyboardEvent) {
			if (!hasActiveSearch) return;
			if ((e.ctrlKey || e.metaKey) && e.key === 'a') {
				e.preventDefault();
				e.stopPropagation();
				const text = searchResults.map((r) => `${r.lineNumber}: ${r.text}`).join('\n');
				copyToClipboard(text);
			}
		}
		window.addEventListener('keydown', onKeydown, true);
		return () => window.removeEventListener('keydown', onKeydown, true);
	});

	function getFileName(path: string): string {
		return path.split('/').pop() || path;
	}

	// Highlight matching text in a line
	function highlightText(text: string, query: string): { text: string; match: boolean }[] {
		if (!query) return [{ text, match: false }];
		try {
			const regex = new RegExp(`(${query})`, 'gi');
			const parts: { text: string; match: boolean }[] = [];
			let lastIndex = 0;
			let m: RegExpExecArray | null;
			while ((m = regex.exec(text)) !== null) {
				if (m.index > lastIndex) {
					parts.push({ text: text.slice(lastIndex, m.index), match: false });
				}
				parts.push({ text: m[1], match: true });
				lastIndex = regex.lastIndex;
				if (regex.lastIndex === m.index) regex.lastIndex++;
			}
			if (lastIndex < text.length) {
				parts.push({ text: text.slice(lastIndex), match: false });
			}
			return parts.length > 0 ? parts : [{ text, match: false }];
		} catch {
			return [{ text, match: false }];
		}
	}

	// Line number column width based on total lines
	let lineNumWidth = $derived(Math.max(4, String(totalLines || 1000).length) + 1);
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) { fullscreen = false; onClose(); } }}>
	<Dialog.Content class="{fullscreen ? 'sm:max-w-none w-screen h-screen !rounded-none' : 'sm:max-w-6xl h-[85vh]'} flex flex-col p-0 transition-all">
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
		<Dialog.Header class="shrink-0 px-4 pt-4 pb-2">
			<Dialog.Title class="text-sm font-semibold pr-16">{getFileName(filePath)}</Dialog.Title>
			<Dialog.Description>
				<span class="font-mono text-xs text-muted-foreground break-all">{tentacleName}:{filePath}</span>
				{#if totalLines > 0}
					<span class="text-xs text-muted-foreground ml-2">({totalLines.toLocaleString()} lines)</span>
				{/if}
			</Dialog.Description>
		</Dialog.Header>

		<!-- Toolbar -->
		<div class="flex items-center gap-2 shrink-0 px-4 pb-2">
			<div class="flex-1 flex items-center gap-2">
				<div class="relative flex-1">
					<SearchIcon class="absolute left-3 top-1/2 -translate-y-1/2 size-4 text-muted-foreground pointer-events-none" />
					{#if searching}
						<LoaderIcon class="absolute right-12 top-1/2 -translate-y-1/2 size-4 animate-spin text-muted-foreground" />
					{:else if searchQuery}
						<button
							class="absolute right-11 top-1/2 -translate-y-1/2 p-1 rounded-full hover:bg-muted text-muted-foreground transition-colors"
							onclick={() => { searchQuery = ''; }}
							aria-label="검색어 지우기"
							title="검색어 지우기"
						>
							<XIcon class="size-3.5" />
						</button>
					{/if}
					<button
						class="absolute right-1.5 top-1/2 -translate-y-1/2 px-1.5 py-1 rounded-md text-[11px] font-mono font-semibold transition-colors {regexMode ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted'}"
						onclick={() => { regexMode = !regexMode; if (lastSearchQuery) search(); }}
						aria-pressed={regexMode}
						title={regexMode ? '정규식 모드 (켜짐) — 클릭하면 리터럴로' : '리터럴 모드 — 클릭하면 정규식으로'}
					>
						.*
					</button>
					<input
						type="text"
						bind:value={searchQuery}
						onkeydown={handleSearchKeydown}
						placeholder={regexMode ? '정규식 패턴 입력 후 Enter' : '검색어 입력 후 Enter'}
						class="w-full h-10 rounded-lg border-2 border-blue-500/60 bg-blue-50/40 pl-10 pr-16 text-sm placeholder:text-blue-700/60 transition-colors hover:border-blue-500 focus-visible:outline-none focus-visible:bg-background focus-visible:border-blue-600 focus-visible:ring-2 focus-visible:ring-blue-500/30"
					/>
				</div>
				{#if hasActiveSearch}
					<!-- Prev/Next match navigation -->
					<div class="flex items-center gap-0.5 shrink-0">
						<Button size="sm" variant="ghost" onclick={goToPrevMatch} title="이전 일치 (Shift+Enter)" class="px-1.5 h-9">
							<ChevronUpIcon class="size-4" />
						</Button>
						<Button size="sm" variant="ghost" onclick={goToNextMatch} title="다음 일치 (Enter)" class="px-1.5 h-9">
							<ChevronDownIcon class="size-4" />
						</Button>
					</div>
					<span class="text-xs text-muted-foreground whitespace-nowrap shrink-0">
						{#if currentMatchIndex >= 0}
							{currentMatchIndex + 1}/{searchResults.length}
						{:else}
							{searchResults.length} matches
						{/if}
					</span>
					<Button size="sm" variant="ghost" onclick={clearSearch} title="검색 결과 초기화" class="px-1.5 h-9 shrink-0">
						<XIcon class="size-4" />
					</Button>
				{/if}
			</div>
			<Button size="sm" variant="outline" onclick={loadLast} class="h-9 shrink-0">
				<ArrowDownToLineIcon class="size-3.5 mr-1" />
				Last
			</Button>
		</div>

		<!-- Content area -->
		<div class="flex-1 overflow-hidden min-h-0 mx-4 mb-4 border rounded-md bg-slate-950 relative">
			{#if loading}
				<div class="flex items-center justify-center h-full text-slate-400">
					<LoaderIcon class="size-5 animate-spin mr-2" />
					Loading...
				</div>
			{:else if error}
				<div class="flex flex-col items-center justify-center h-full gap-3">
					<div class="flex items-center gap-2 text-red-400">
						<AlertCircleIcon class="size-5" />
						<span class="text-sm">{error}</span>
					</div>
					{#if isBinaryError}
						<button
							class="px-3 py-1.5 text-xs rounded-md border border-slate-600 text-slate-300 hover:bg-slate-800 hover:text-slate-100 transition-colors"
							onclick={() => {
								forceOpen = true;
								isBinaryError = false;
								error = '';
								loadLines(1, 1000, true);
							}}
						>
							Open Anyway
						</button>
					{/if}
				</div>
			{:else if showSearchResults}
				<!-- Search Results -->
				<div class="h-full overflow-auto">
					<div class="sticky top-0 bg-slate-900 border-b border-slate-800 px-3 py-2 flex items-center justify-between">
						<span class="text-xs text-slate-400">
							{searchResults.length} match{searchResults.length !== 1 ? 'es' : ''}
							for "<span class="text-yellow-400">{lastSearchQuery}</span>"
						</span>
						{#if hasActiveSearch}
							<span class="text-[10px] text-slate-600">Ctrl+A to copy all</span>
						{/if}
					</div>
					{#if searchError && searchResults.length === 0}
						<div class="text-center text-slate-500 text-sm py-12">{searchError}</div>
					{/if}
					{#each searchResults as result, idx}
						<div
							class="w-full text-left px-3 py-1.5 hover:bg-slate-800/70 transition-colors border-b border-slate-900 flex items-start gap-2 cursor-text
								{currentMatchIndex === idx ? 'bg-slate-800/50' : ''}"
							ondblclick={() => goToSearchResult(result, idx)}
							role="listitem"
						>
							<span class="text-yellow-600 font-mono text-xs shrink-0 select-none min-w-12 text-right">
								{result.lineNumber}:
							</span>
							<span class="font-mono text-xs break-all whitespace-pre-wrap select-text">
								{#each highlightText(result.text, lastSearchQuery) as part}
									{#if part.match}
										<mark class="bg-yellow-500/40 text-yellow-200 rounded-sm px-0.5">{part.text}</mark>
									{:else}
										<span class="text-slate-300">{part.text}</span>
									{/if}
								{/each}
							</span>
						</div>
					{/each}
				</div>
			{:else}
				<!-- File Content -->
				<div
					bind:this={scrollContainer}
					onscroll={handleScroll}
					class="h-full overflow-auto"
				>
					<!-- Back to search results bar -->
					{#if hasActiveSearch && !showSearchResults}
						<div class="sticky top-0 z-10 bg-slate-900/95 border-b border-slate-800 px-3 py-1.5 flex items-center gap-2 backdrop-blur-sm">
							<button
								class="flex items-center gap-1 text-xs text-blue-400 hover:text-blue-300 transition-colors"
								onclick={backToResults}
							>
								<ArrowLeftIcon class="size-3" />
								Back to results
							</button>
							<span class="text-xs text-slate-500">|</span>
							<span class="text-xs text-slate-400">
								{#if currentMatchIndex >= 0}
									Match {currentMatchIndex + 1} of {searchResults.length}
								{:else}
									{searchResults.length} matches
								{/if}
								for "<span class="text-yellow-400">{lastSearchQuery}</span>"
							</span>
							<div class="flex items-center gap-1.5 ml-auto">
								<button
									class="p-0.5 rounded hover:bg-slate-800 text-slate-400 hover:text-slate-200 transition-colors"
									onclick={goToPrevMatch}
									title="Previous match"
								>
									<ChevronUpIcon class="size-3.5" />
								</button>
								<button
									class="p-0.5 rounded hover:bg-slate-800 text-slate-400 hover:text-slate-200 transition-colors"
									onclick={goToNextMatch}
									title="Next match"
								>
									<ChevronDownIcon class="size-3.5" />
								</button>
							</div>
						</div>
					{/if}

					{#each lines as line (line.lineNumber)}
						<div
							id="log-line-{line.lineNumber}"
							class="flex hover:bg-slate-900/70
								{line.lineNumber === highlightLine ? 'bg-yellow-900/30 hover:bg-yellow-900/40' : ''}
								{hasActiveSearch && matchingLines.has(line.lineNumber) && line.lineNumber !== highlightLine ? 'bg-yellow-900/10' : ''}"
						>
							<span
								class="text-right select-none shrink-0 px-2 border-r border-slate-800/50 leading-5
									{hasActiveSearch && matchingLines.has(line.lineNumber) ? 'text-yellow-600' : 'text-slate-600'}"
								style="min-width: {lineNumWidth}ch;"
							>
								<span class="font-mono text-xs">{line.lineNumber}</span>
							</span>
							<pre class="text-xs leading-5 pl-2 pr-4 whitespace-pre overflow-x-auto flex-1 m-0 bg-transparent border-0">{#if hasActiveSearch}{#each highlightText(line.text, lastSearchQuery) as part}{#if part.match}<mark class="bg-yellow-500/40 text-yellow-200 rounded-sm">{part.text}</mark>{:else}<span class="text-slate-300">{part.text}</span>{/if}{/each}{:else}<span class="text-slate-300">{line.text}</span>{/if}</pre>
						</div>
					{/each}

					{#if loadingMore}
						<div class="flex items-center justify-center py-4 text-slate-500">
							<LoaderIcon class="size-4 animate-spin mr-2" />
							<span class="text-xs">Loading more...</span>
						</div>
					{/if}

					{#if !hasMore && lines.length > 0}
						<div class="text-center text-slate-700 text-xs py-3">
							— End of file —
						</div>
					{/if}

					{#if lines.length === 0 && !loading}
						<div class="flex items-center justify-center h-full text-slate-500 text-sm">
							Empty file
						</div>
					{/if}
				</div>
			{/if}

			<!-- Scroll position indicator -->
			{#if !loading && !error && !showSearchResults && lines.length > 0 && totalLines > 0}
				<div class="absolute bottom-2 right-4 bg-slate-800/90 text-slate-400 text-[10px] font-mono px-2 py-1 rounded backdrop-blur-sm pointer-events-none">
					{currentStartLine.toLocaleString()}–{currentEndLine.toLocaleString()} / {totalLines.toLocaleString()}
				</div>
			{/if}

			<!-- Copy toast -->
			{#if copied}
				<div class="absolute inset-0 flex items-center justify-center pointer-events-none z-20">
					<div class="bg-slate-800/95 text-green-400 text-sm font-medium px-5 py-3 rounded-lg shadow-lg backdrop-blur-sm border border-slate-700">
						Copied {searchResults.length} lines to clipboard
					</div>
				</div>
			{/if}
		</div>
		<textarea bind:this={copyArea} class="sr-only" tabindex={-1} aria-hidden="true"></textarea>
	</Dialog.Content>
</Dialog.Root>
