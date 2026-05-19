<script lang="ts">
	import * as Table from '$lib/components/ui/table/index.js';
	import { toast } from 'svelte-sonner';
	import { btnIcon } from '$lib/styles/common.js';
	import { getJobStatus, deleteJob, fetchExecutions, deleteExecution, fetchExecutionStats, openLocalFolder, type JobExecutionRecord } from '$lib/api/agent.js';
	import type { JobRecord } from './types.js';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import SearchIcon from '@lucide/svelte/icons/search';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import InboxIcon from '@lucide/svelte/icons/inbox';
	import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import FilterIcon from '@lucide/svelte/icons/filter';
	import ArchiveIcon from '@lucide/svelte/icons/archive';
	import FileTextIcon from '@lucide/svelte/icons/file-text';
	import FolderOpenIcon from '@lucide/svelte/icons/folder-open';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';

	interface Props {
		jobHistory: JobRecord[]; // 활성 job용 (localStorage, 하위 호환)
		serverId: number | null;
		onViewDetail: (serverId: number, jobId: string) => void;
		onDeleteJob: (jobId: string, serverId: number) => void;
	}

	let { jobHistory, serverId, onViewDetail, onDeleteJob }: Props = $props();

	let confirmOpen = $state(false);
	let confirmDesc = $state('');
	let confirmAction = $state<() => Promise<void>>(async () => {});

	let jobIdInput = $state('');
	let searching = $state(false);

	// DB 이력
	let executions = $state<JobExecutionRecord[]>([]);
	let totalElements = $state(0);
	let totalPages = $state(0);
	let currentPage = $state(0);
	let loading = $state(false);

	// 필터
	let filterType = $state('');
	let filterState = $state('');
	let showFilter = $state(false);

	// 통계
	let stats = $state<{ total: number; completed: number; failed: number; successRate: number } | null>(null);

	$effect(() => {
		if (serverId != null) {
			loadExecutions();
			loadStats();
		}
	});

	async function loadExecutions() {
		if (serverId == null) return;
		loading = true;
		try {
			const res = await fetchExecutions({
				serverId,
				type: filterType || undefined,
				state: filterState || undefined,
				page: currentPage,
				size: 30
			});
			executions = res.content;
			totalElements = res.totalElements;
			totalPages = res.totalPages;
		} catch {
			executions = [];
		} finally {
			loading = false;
		}
	}

	async function loadStats() {
		if (serverId == null) return;
		try {
			stats = await fetchExecutionStats(serverId);
		} catch { stats = null; }
	}

	function applyFilter() {
		currentPage = 0;
		loadExecutions();
	}

	function clearFilter() {
		filterType = '';
		filterState = '';
		currentPage = 0;
		loadExecutions();
	}

	async function queryManual() {
		if (serverId == null || !jobIdInput.trim()) return;
		searching = true;
		try {
			await getJobStatus(serverId, jobIdInput.trim());
			onViewDetail(serverId, jobIdInput.trim());
		} catch {
			toast.error('Job 조회 실패');
		} finally {
			searching = false;
		}
	}

	function copyJobId(jobId: string) {
		navigator.clipboard.writeText(jobId);
		toast.success('Job ID 복사됨');
	}

	async function openArchiveBase() {
		try {
			const res = await openLocalFolder('archive');
			if (!res.success) toast.error(res.message || 'archive 폴더 열기 실패');
		} catch {
			toast.error('archive 폴더 열기 실패');
		}
	}

	async function openJobFolder(j: JobExecutionRecord) {
		// trace 잡 → trace 디렉토리, 그 외(benchmark/scenario) → archive 디렉토리 검색.
		const target = j.type === 'trace' ? 'trace' : 'archive-job';
		try {
			const res = await openLocalFolder(target, j.jobId);
			if (!res.success) toast.error(res.message || '폴더 열기 실패');
		} catch {
			toast.error('폴더 열기 실패');
		}
	}

	function requestDelete(j: JobExecutionRecord) {
		confirmDesc = `Job ${j.jobId.slice(0, 8)}… 을 삭제하시겠습니까?`;
		confirmAction = async () => {
			try {
				await deleteExecution(j.id);
				try { await deleteJob(j.serverId, j.jobId); } catch { /* Go Agent에서 이미 삭제됐을 수 있음 */ }
				onDeleteJob(j.jobId, j.serverId);
				toast.success('삭제되었습니다');
				loadExecutions();
				loadStats();
			} catch {
				toast.error('삭제 실패');
			}
			confirmOpen = false;
		};
		confirmOpen = true;
	}

	function parseDeviceCount(deviceIds: string): number {
		try { return JSON.parse(deviceIds).length; } catch { return 0; }
	}

	function stateColor(state: string): string {
		switch (state) {
			case 'completed': return 'bg-green-100 text-green-800';
			case 'running': case 'pushing_tools': case 'collecting': return 'bg-blue-100 text-blue-800';
			case 'failed': return 'bg-red-100 text-red-800';
			case 'partially_failed': return 'bg-orange-100 text-orange-800';
			case 'cancelled': return 'bg-orange-100 text-orange-800';
			default: return 'bg-gray-100 text-gray-800';
		}
	}

	function stateLabel(state: string): string {
		switch (state) {
			case 'completed': return 'Completed';
			case 'running': return 'Running';
			case 'failed': return 'Failed';
			case 'partially_failed': return 'Partial Fail';
			case 'cancelled': return 'Cancelled';
			default: return state;
		}
	}

	function formatTime(ts: string | null): string {
		if (!ts) return '-';
		return new Date(ts).toLocaleString('ko-KR', {
			month: '2-digit', day: '2-digit',
			hour: '2-digit', minute: '2-digit', second: '2-digit'
		});
	}

	function typeBadgeClass(type: string): string {
		if (type === 'benchmark') return 'bg-blue-100 text-blue-700';
		if (type === 'trace') return 'bg-emerald-100 text-emerald-700';
		return 'bg-purple-100 text-purple-700';
	}
</script>

<ConfirmDialog
	bind:open={confirmOpen}
	title="삭제 확인"
	description={confirmDesc}
	confirmLabel="삭제"
	onConfirm={confirmAction}
	onCancel={() => { confirmOpen = false; }}
/>

<div class="space-y-3 p-1">
	<div class="flex items-center justify-between">
		<div class="flex items-center gap-2">
			<h2 class="text-sm font-semibold">Results</h2>
			<button
				onclick={openArchiveBase}
				class="inline-flex items-center gap-1 rounded border px-2 py-0.5 text-[10px] hover:bg-muted"
				title="Archive 폴더를 파일 탐색기로 열기"
			>
				<FolderOpenIcon class="size-3" /> Archive 폴더
			</button>
		</div>
		{#if stats}
			<div class="flex items-center gap-3 text-[10px] text-muted-foreground">
				<span>총 {stats.total}건</span>
				<span class="text-green-600">{stats.completed} 성공</span>
				<span class="text-red-600">{stats.failed} 실패</span>
				<span>성공률 {stats.successRate}%</span>
			</div>
		{/if}
	</div>

	<!-- Manual query + Filter -->
	<div class="flex items-center gap-2">
		<input
			bind:value={jobIdInput}
			placeholder="Job ID로 직접 조회"
			class="border rounded px-2.5 py-1.5 text-xs bg-background w-64 font-mono"
			onkeydown={(e) => { if (e.key === 'Enter') queryManual(); }}
		/>
		<button
			onclick={queryManual}
			disabled={searching || !jobIdInput.trim() || serverId == null}
			class="{btnIcon}"
		>
			{#if searching}
				<LoaderIcon class="size-3 animate-spin" />
			{:else}
				<SearchIcon class="size-3" />
			{/if}
			조회
		</button>
		<button
			onclick={() => showFilter = !showFilter}
			class="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-[10px] hover:bg-muted transition-colors
				{showFilter ? 'bg-primary/10 text-primary' : ''}"
		>
			<FilterIcon class="size-3" /> 필터
		</button>
	</div>

	<!-- Filter panel -->
	{#if showFilter}
		<div class="flex items-center gap-2 text-[10px]">
			<select bind:value={filterType} onchange={applyFilter} class="border rounded px-2 py-1 bg-background text-[10px]">
				<option value="">전체 타입</option>
				<option value="benchmark">Benchmark</option>
				<option value="scenario">Scenario</option>
				<option value="trace">Trace</option>
			</select>
			<select bind:value={filterState} onchange={applyFilter} class="border rounded px-2 py-1 bg-background text-[10px]">
				<option value="">전체 상태</option>
				<option value="completed">Completed</option>
				<option value="running">Running</option>
				<option value="failed">Failed</option>
				<option value="partially_failed">Partial Fail</option>
				<option value="cancelled">Cancelled</option>
			</select>
			{#if filterType || filterState}
				<button onclick={clearFilter} class="text-[10px] text-muted-foreground hover:text-foreground underline">초기화</button>
			{/if}
		</div>
	{/if}

	<!-- Results table -->
	{#if loading}
		<div class="flex items-center justify-center py-12">
			<LoaderIcon class="size-5 animate-spin text-muted-foreground" />
		</div>
	{:else if executions.length > 0}
		<Table.Root>
			<Table.Header>
				<Table.Row class="text-[10px]">
					<Table.Head>Job ID</Table.Head>
					<Table.Head>Type</Table.Head>
					<Table.Head>Tool/Name</Table.Head>
					<Table.Head>Server</Table.Head>
					<Table.Head>Devices</Table.Head>
					<Table.Head>State</Table.Head>
					<Table.Head>Time</Table.Head>
					<Table.Head class="w-20"></Table.Head>
				</Table.Row>
			</Table.Header>
			<Table.Body>
				{#each executions as j (j.id)}
					<Table.Row
						class="text-xs cursor-pointer hover:bg-muted/50"
						onclick={() => onViewDetail(j.serverId, j.jobId)}
					>
						<Table.Cell>
							<div class="flex items-center gap-1">
								<span class="font-mono text-[10px]" title={j.jobId}>{j.jobId.slice(0, 8)}</span>
								<button
									onclick={(e) => { e.stopPropagation(); copyJobId(j.jobId); }}
									class="p-0.5 rounded hover:bg-muted"
								>
									<CopyIcon class="size-2.5" />
								</button>
							</div>
						</Table.Cell>
						<Table.Cell>
							<div class="flex items-center gap-1">
								<span class="px-1 py-0.5 rounded text-[9px] {typeBadgeClass(j.type)}">
									{j.type}
								</span>
								{#if j.type === 'trace'}
									{#if j.traceParseState === 'PARSING' || j.traceParseState === 'UPLOADING'}
										<span title="{j.traceParseState}" class="text-sky-600">
											<LoaderCircle class="size-3 animate-spin" />
										</span>
									{:else if j.traceParsedAt}
										<span title="Archived (parsed) — {j.traceParsedAt}" class="text-emerald-600">
											<ArchiveIcon class="size-3" />
										</span>
									{:else if j.traceRawKey}
										<span title="Raw archived (unparsed)" class="text-amber-600">
											<FileTextIcon class="size-3" />
										</span>
									{/if}
								{/if}
							</div>
						</Table.Cell>
						<Table.Cell class="text-[10px]">{j.tool ?? j.jobName ?? '-'}</Table.Cell>
						<Table.Cell class="text-[10px]">{j.serverName}</Table.Cell>
						<Table.Cell class="text-[10px]">{parseDeviceCount(j.deviceIds)}</Table.Cell>
						<Table.Cell>
							<span class="px-1.5 py-0.5 rounded text-[10px] {stateColor(j.state)}">
								{stateLabel(j.state)}
							</span>
						</Table.Cell>
						<Table.Cell class="text-[10px] text-muted-foreground">{formatTime(j.createdAt)}</Table.Cell>
						<Table.Cell>
							<div class="flex items-center gap-0.5">
								<button
									onclick={(e) => { e.stopPropagation(); onViewDetail(j.serverId, j.jobId); }}
									class="p-0.5 rounded hover:bg-muted"
									title="상세 보기"
								>
									<ExternalLinkIcon class="size-3 text-muted-foreground" />
								</button>
								<button
									onclick={(e) => { e.stopPropagation(); openJobFolder(j); }}
									class="p-0.5 rounded hover:bg-muted"
									title={j.type === 'trace' ? 'Trace 출력 폴더 열기' : 'Archive 폴더 열기'}
								>
									<FolderOpenIcon class="size-3 text-muted-foreground" />
								</button>
								<button
									onclick={(e) => { e.stopPropagation(); requestDelete(j); }}
									class="p-0.5 rounded hover:bg-muted"
									title="삭제"
								>
									<TrashIcon class="size-3 text-red-500" />
								</button>
							</div>
						</Table.Cell>
					</Table.Row>
				{/each}
			</Table.Body>
		</Table.Root>

		<!-- Pagination -->
		{#if totalPages > 1}
			<div class="flex items-center justify-between text-[10px] text-muted-foreground">
				<span>{totalElements}건 중 {currentPage * 30 + 1}–{Math.min((currentPage + 1) * 30, totalElements)}</span>
				<div class="flex items-center gap-1">
					<button
						onclick={() => { currentPage = Math.max(0, currentPage - 1); loadExecutions(); }}
						disabled={currentPage === 0}
						class="p-0.5 rounded hover:bg-muted disabled:opacity-30"
					>
						<ChevronLeftIcon class="size-3.5" />
					</button>
					<span>{currentPage + 1} / {totalPages}</span>
					<button
						onclick={() => { currentPage = Math.min(totalPages - 1, currentPage + 1); loadExecutions(); }}
						disabled={currentPage >= totalPages - 1}
						class="p-0.5 rounded hover:bg-muted disabled:opacity-30"
					>
						<ChevronRightIcon class="size-3.5" />
					</button>
				</div>
			</div>
		{/if}
	{:else}
		<div class="flex flex-col items-center justify-center py-12 text-muted-foreground">
			<InboxIcon class="size-8 mb-2 opacity-30" />
			<p class="text-xs">벤치마크 또는 시나리오를 실행하면</p>
			<p class="text-xs">이력이 여기에 표시됩니다</p>
		</div>
	{/if}
</div>
