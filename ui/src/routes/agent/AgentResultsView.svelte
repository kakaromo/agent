<script lang="ts">
	import * as Table from '$lib/components/ui/table/index.js';
	import { SvelteSet } from 'svelte/reactivity';
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
		// jobType 은 서버 레코드(executions)의 값이다.
		// 예전엔 안 넘겨서 호출부가 localStorage(jobHistory)로 타입을 되찾았는데,
		// 그 목록엔 **이 브라우저에서 시작한 잡만** 들어있다. 다른 세션/PC 에서 돌린
		// trace 잡은 타입 판별에 실패해 benchmark 상세로 열렸다 — fsio 결과가
		// "안 나온다" 의 정체가 이것이었다.
		onViewDetail: (serverId: number, jobId: string, jobType?: string) => void;
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

	// 선택 (일괄 삭제용). 현재 페이지 기준 — 페이지 이동/필터 변경 시 초기화한다.
	let selectedIds = $state<Set<number>>(new SvelteSet());
	let bulkDeleting = $state(false);

	const selectedCount = $derived(selectedIds.size);
	// 헤더 체크박스 상태. 현재 페이지가 전부 선택됐는지로 판정한다.
	const allSelected = $derived(executions.length > 0 && executions.every((j) => selectedIds.has(j.id)));

	function clearSelection() {
		selectedIds = new SvelteSet();
	}

	function toggleOne(id: number) {
		// SvelteSet 은 mutate 로 반응성이 전파된다.
		if (selectedIds.has(id)) selectedIds.delete(id);
		else selectedIds.add(id);
	}

	function toggleAll() {
		if (allSelected) clearSelection();
		else selectedIds = new SvelteSet(executions.map((j) => j.id));
	}

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
			// 목록이 바뀌면 선택은 무효 — 안 지우면 화면에 없는 행이 삭제 대상으로 남는다.
			clearSelection();
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
				await deleteOne(j);
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

	/** 한 건 삭제. 일괄 삭제와 단건 삭제가 공유한다. 실패하면 throw 해서 호출부가 집계한다. */
	async function deleteOne(j: JobExecutionRecord) {
		await deleteExecution(j.id);
		try { await deleteJob(j.serverId, j.jobId); } catch { /* Go Agent에서 이미 삭제됐을 수 있음 */ }
		onDeleteJob(j.jobId, j.serverId);
	}

	function requestBulkDelete() {
		const targets = executions.filter((j) => selectedIds.has(j.id));
		if (targets.length === 0) return;
		confirmDesc = `선택한 ${targets.length}건을 삭제하시겠습니까?`;
		confirmAction = async () => {
			confirmOpen = false;
			bulkDeleting = true;
			// 순차 삭제. 동시에 던지면 SQLite 쓰기 경합 + agent 측 부하가 겹친다.
			// 한 건 실패해도 나머지는 계속 진행하고, 끝에 실패 건수를 보고한다.
			let failed = 0;
			for (const j of targets) {
				try {
					await deleteOne(j);
				} catch {
					failed++;
				}
			}
			bulkDeleting = false;
			if (failed === 0) toast.success(`${targets.length}건 삭제되었습니다`);
			else if (failed === targets.length) toast.error('삭제 실패');
			else toast.warning(`${targets.length - failed}건 삭제, ${failed}건 실패`);
			// 마지막 페이지를 통째로 지우면 그 페이지가 사라진다 — 범위를 넘지 않게 되돌린다.
			const remaining = Math.max(0, totalElements - (targets.length - failed));
			const lastPage = Math.max(0, Math.ceil(remaining / 30) - 1);
			if (currentPage > lastPage) currentPage = lastPage;
			loadExecutions();
			loadStats();
		};
		confirmOpen = true;
	}

	/**
	 * 성공률 표시. API 는 비율(0~1)로 준다 (docs/05-rest-api.md: "successRate":0.0556).
	 * 그대로 뿌리면 54.7% 가 "0.5471698113207547%" 로 나온다.
	 */
	function formatSuccessRate(ratio: number): string {
		if (!Number.isFinite(ratio)) return '-';
		return `${(ratio * 100).toFixed(1)}%`;
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
				<span>성공률 {formatSuccessRate(stats.successRate)}</span>
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

	<!-- 선택 액션 바 -->
	{#if selectedCount > 0}
		<div class="flex items-center gap-2 rounded-md border bg-muted/40 px-2.5 py-1.5 text-[10px]">
			<span class="font-medium">{selectedCount}건 선택됨</span>
			<button
				onclick={requestBulkDelete}
				disabled={bulkDeleting}
				class="inline-flex items-center gap-1 rounded border border-red-200 px-2 py-0.5 text-red-600 hover:bg-red-50 disabled:opacity-50"
			>
				{#if bulkDeleting}
					<LoaderIcon class="size-3 animate-spin" />
				{:else}
					<TrashIcon class="size-3" />
				{/if}
				선택 삭제
			</button>
			<button
				onclick={clearSelection}
				disabled={bulkDeleting}
				class="text-muted-foreground underline hover:text-foreground disabled:opacity-50"
			>
				선택 해제
			</button>
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
					<Table.Head class="w-8">
						<input
							type="checkbox"
							class="size-3 cursor-pointer align-middle"
							checked={allSelected}
							indeterminate={selectedCount > 0 && !allSelected}
							onchange={toggleAll}
							title="현재 페이지 전체 선택"
						/>
					</Table.Head>
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
						onclick={() => onViewDetail(j.serverId, j.jobId, j.type)}
					>
						<Table.Cell onclick={(e: MouseEvent) => e.stopPropagation()}>
							<input
								type="checkbox"
								class="size-3 cursor-pointer align-middle"
								checked={selectedIds.has(j.id)}
								onchange={() => toggleOne(j.id)}
							/>
						</Table.Cell>
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
									onclick={(e) => { e.stopPropagation(); onViewDetail(j.serverId, j.jobId, j.type); }}
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
