<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { emptyState, tagMuted } from '$lib/styles/common.js';
	import { Skeleton } from '$lib/components/ui/skeleton/index.js';
	import {
		fetchPerformanceTestRequests,
		fetchPerformanceHistory,
		fetchPerformanceResultData,
		type PerformanceResultData
	} from '$lib/api/testdb.js';
	import type { PerformanceTestRequest, PerformanceHistory } from '$lib/api/types.js';
	import { tentacle } from '$lib/stores/tentacle.svelte.js';
	import { PerfRenderer } from '$lib/components/perf-content';
	import { ResultCell, DateCell } from '$lib/components/data-table';
	import LogBrowserDialog from '$lib/components/LogBrowserDialog.svelte';
	import MetadataDialog from '$lib/components/debug/MetadataDialog.svelte';
	import FileDown from '@lucide/svelte/icons/file-down';
	import FolderOpen from '@lucide/svelte/icons/folder-open';
	import DatabaseIcon from '@lucide/svelte/icons/database';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';
	import MaximizeIcon from '@lucide/svelte/icons/maximize-2';
	import MinimizeIcon from '@lucide/svelte/icons/minimize-2';
	import XIcon from '@lucide/svelte/icons/x';

	let isFullscreen = $state(false);

	$effect(() => {
		if (!open) isFullscreen = false;
	});
	import { renderComponent } from '$lib/components/ui/data-table/render-helpers.js';

	interface Props {
		open: boolean;
		historyId: number | null;
		trId?: string | number | null;
		onClose: () => void;
	}

	let { open = $bindable(), historyId, trId, onClose }: Props = $props();

	let trInfo = $state<PerformanceTestRequest | null>(null);
	let historyInfo = $state<PerformanceHistory | null>(null);
	let resultData = $state<PerformanceResultData | null>(null);
	let loading = $state(false);
	let error = $state('');

	// Auto-refresh for RUNNING
	let refreshTimer: ReturnType<typeof setInterval> | undefined;
	let isRefreshing = $state(false);

	// Log browser
	let logBrowserOpen = $state(false);
	let logBrowserTentacle = $state('');
	let logBrowserPath = $state('');
	let logBrowserTitle = $state('');

	// Metadata dialog
	let metadataOpen = $state(false);
	let metadataTentacle = $state('');
	let metadataSlotNumber = $state(0);
	let metadataLogPath = $state<string | undefined>();

	$effect(() => {
		if (open && historyId) {
			loadData();
		} else if (!open) {
			stopRefresh();
			reset();
		}
		return () => stopRefresh();
	});

	function reset() {
		trInfo = null;
		historyInfo = null;
		resultData = null;
		error = '';
		isRefreshing = false;
	}

	async function loadData() {
		if (!historyId) return;
		loading = true;
		error = '';
		try {
			const [trs, his, rd] = await Promise.all([
				fetchPerformanceTestRequests(),
				fetchPerformanceHistory(historyId),
				fetchPerformanceResultData(historyId)
			]);
			if (trId) {
				trInfo = trs.find((t) => String(t.id) === String(trId)) ?? null;
			}
			historyInfo = his;
			resultData = rd;

			// RUNNING 자동 갱신
			if (his.result === 'RUNNING' || rd.status === 'collecting') {
				startRefresh();
			}
		} catch (e: any) {
			error = e?.message ?? 'Failed to load result data';
		} finally {
			loading = false;
		}
	}

	function startRefresh() {
		stopRefresh();
		isRefreshing = true;
		refreshTimer = setInterval(async () => {
			if (!historyId) return;
			try {
				const [his, rd] = await Promise.all([
					fetchPerformanceHistory(historyId),
					fetchPerformanceResultData(historyId)
				]);
				historyInfo = his;
				resultData = rd;
				// 완료되면 갱신 중지
				if (his.result !== 'RUNNING' && rd.status !== 'collecting') {
					stopRefresh();
				}
			} catch {
				// silent
			}
		}, 10000);
	}

	function stopRefresh() {
		if (refreshTimer) {
			clearInterval(refreshTimer);
			refreshTimer = undefined;
		}
		isRefreshing = false;
	}

	async function openLogBrowser() {
		if (!historyInfo?.logPath) return;
		if (!historyInfo.logPath.includes('UFS')) return;
		await tentacle.fetchPrefix();

		const hasExt = /\.[^/]+$/.test(historyInfo.logPath);
		const dirPath = hasExt ? historyInfo.logPath.replace(/\/[^/]+$/, '') : historyInfo.logPath;
		logBrowserTentacle = tentacle.headHost;
		logBrowserPath = `${tentacle.headPrefix}/NAS/${dirPath}`;
		logBrowserTitle = `Log - ${tcName} (${historyInfo.slotLocation})`;
		logBrowserOpen = true;
	}

	function openMetadata() {
		if (!historyInfo?.logPath) return;
		const loc = historyInfo.slotLocation ?? '';
		const dashIdx = loc.lastIndexOf('-');
		metadataTentacle = dashIdx >= 0 ? loc.substring(0, dashIdx) : loc;
		metadataSlotNumber = dashIdx >= 0 ? parseInt(loc.substring(dashIdx + 1).replace(/\D/g, '')) || 0 : 0;
		metadataLogPath = historyInfo.logPath;
		metadataOpen = true;
	}

	const tcName = $derived(resultData?.tcName ?? '');
	const fw = $derived(trInfo?.fw ?? trInfo?.fwVersion ?? '');
	const hasLog = $derived(!!historyInfo?.logPath && (historyInfo.logPath.includes('UFS') || /^T\d+/.test(historyInfo.slotLocation ?? '')));
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) onClose(); }}>
	<Dialog.Content
		showCloseButton={false}
		class="{isFullscreen
			? 'top-0 left-0 translate-x-0 translate-y-0 max-w-none sm:max-w-none w-screen h-screen max-h-none rounded-none p-4'
			: 'max-w-[calc(100vw-2rem)] sm:max-w-[calc(100vw-2rem)] w-full max-h-[calc(100vh-2rem)]'}
			overflow-hidden flex flex-col transition-all duration-300 ease-out"
	>
		<Dialog.Header class="shrink-0">
			<div class="flex items-center gap-2">
				<Dialog.Title class="flex items-center gap-2 text-sm flex-1">
					Performance Result — History #{historyId}
					{#if isRefreshing}
						<span class="inline-flex items-center gap-1 text-[10px] text-blue-500">
							<LoaderCircle class="size-3 animate-spin" />
							자동 갱신 중
						</span>
					{/if}
				</Dialog.Title>
				<button
					onclick={() => isFullscreen = !isFullscreen}
					class="p-1.5 rounded-md hover:bg-muted transition-colors"
					title={isFullscreen ? '기본 크기' : '전체화면'}
				>
					{#if isFullscreen}
						<MinimizeIcon class="size-4" />
					{:else}
						<MaximizeIcon class="size-4" />
					{/if}
				</button>
				<button
					onclick={() => { open = false; onClose(); }}
					class="p-1.5 rounded-md hover:bg-muted transition-colors"
				>
					<XIcon class="size-4" />
				</button>
			</div>
			<Dialog.Description class="sr-only">성능 테스트 결과 상세 보기</Dialog.Description>
		</Dialog.Header>

		<div class="flex-1 overflow-auto px-1">
			{#if loading}
				<div class="space-y-3 py-4">
					<Skeleton class="h-4 w-48 rounded" />
					<Skeleton class="h-72 rounded-lg" />
				</div>
			{:else if error}
				<div class="rounded border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive flex items-center justify-between">
					<span>{error}</span>
					<button
						class="shrink-0 ml-3 px-3 py-1 rounded text-xs border border-destructive/30 hover:bg-destructive/10 transition-colors"
						onclick={loadData}
					>Retry</button>
				</div>
			{:else if resultData}
				<!-- Meta info -->
				<div class="flex items-center gap-1.5 flex-wrap mb-3">
					{#if historyInfo?.result}
						<span class="inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-[10px] font-medium
							{historyInfo.result === 'PASS' ? 'bg-green-100 text-green-700' :
							 historyInfo.result === 'FAIL' ? 'bg-red-100 text-red-700' :
							 historyInfo.result === 'RUNNING' ? 'bg-blue-100 text-blue-700' :
							 'bg-muted text-muted-foreground'}">
							{historyInfo.result}
						</span>
					{/if}
					{#if tcName}
						<span class="{tagMuted}">
							<span class="text-muted-foreground">TC</span>
							<span class="font-medium">{tcName}</span>
						</span>
					{/if}
					{#if fw}
						<span class="{tagMuted}">
							<span class="text-muted-foreground">FW</span>
							<span class="font-medium">{fw}</span>
						</span>
					{/if}
					{#if historyInfo?.slotLocation}
						<span class="{tagMuted}">
							<span class="text-muted-foreground">Slot</span>
							<span class="font-medium">{historyInfo.slotLocation}</span>
						</span>
					{/if}
					{#if historyInfo?.runningTime}
						<span class="{tagMuted}">
							<span class="text-muted-foreground">Time</span>
							<span class="font-medium">{historyInfo.runningTime}</span>
						</span>
					{/if}

					<!-- Action buttons -->
					<div class="flex items-center gap-1 ml-auto">
						<button
							class="inline-flex items-center gap-1 h-6 px-2 text-[10px] rounded-md border bg-background hover:bg-muted transition-colors"
							onclick={() => window.location.href = `/api/performance-results/${historyId}/excel`}
						>
							<FileDown class="size-3" />
							Export Excel
						</button>
						{#if hasLog}
							<button
								class="inline-flex items-center gap-1 h-6 px-2 text-[10px] rounded-md border bg-background hover:bg-muted transition-colors"
								onclick={openLogBrowser}
							>
								<FolderOpen class="size-3" />
								Log
							</button>
						{/if}
						<!-- {#if historyInfo?.logPath}
							<button
								class="inline-flex items-center gap-1 h-6 px-2 text-[10px] rounded-md border bg-background hover:bg-muted transition-colors"
								onclick={openMetadata}
							>
								<DatabaseIcon class="size-3" />
								Meta
							</button>
						{/if} -->
					</div>
				</div>

				<!-- Result renderer -->
				<PerfRenderer parserId={resultData.parserId} data={resultData.data} {tcName} {fw} />
			{:else}
				<div class="{emptyState}">
					<span class="text-sm">No result data available</span>
					<span class="text-[11px] text-muted-foreground/60">This history record may not have any parsed result data yet.</span>
				</div>
			{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>

<LogBrowserDialog
	bind:open={logBrowserOpen}
	tentacleName={logBrowserTentacle}
	initialPath={logBrowserPath}
	title={logBrowserTitle}
	onClose={() => { logBrowserOpen = false; }}
/>

<MetadataDialog
	bind:open={metadataOpen}
	tentacleName={metadataTentacle}
	slotNumber={metadataSlotNumber}
	logPath={metadataLogPath}
	readOnly
	onClose={() => { metadataOpen = false; }}
/>
