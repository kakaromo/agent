<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { sectionLabel, captionMuted } from '$lib/styles/common.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { toast } from 'svelte-sonner';
	import ConfirmDialog from '../ConfirmDialog.svelte';

	import CircleIcon from '@lucide/svelte/icons/circle';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import CheckCircleIcon from '@lucide/svelte/icons/circle-check';
	import XCircleIcon from '@lucide/svelte/icons/circle-x';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import PlayIcon from '@lucide/svelte/icons/play';
	import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import ServerIcon from '@lucide/svelte/icons/server';
	import MonitorIcon from '@lucide/svelte/icons/monitor';
	import AlertTriangleIcon from '@lucide/svelte/icons/triangle-alert';
	import ImageIcon from '@lucide/svelte/icons/image';
	import FileArchiveIcon from '@lucide/svelte/icons/file-archive';
	import FileIcon from '@lucide/svelte/icons/file';
	import FileTextIcon from '@lucide/svelte/icons/file-text';
	import FolderOpenIcon from '@lucide/svelte/icons/folder-open';
	import XIcon from '@lucide/svelte/icons/x';
	import GitBranchIcon from '@lucide/svelte/icons/git-branch';
	import CloudDownloadIcon from '@lucide/svelte/icons/cloud-download';
	import TimerIcon from '@lucide/svelte/icons/timer';

	import LogBrowserDialog from '../LogBrowserDialog.svelte';
	import { executeT32Dump, checkT32Available, fetchConfigs, type T32Config } from '$lib/api/t32.js';
	import { fetchServers } from '$lib/api/admin.js';
	import { fetchRepos, fetchBranches, downloadDetectedBranch, type BitbucketRepo, type BitbucketBranch } from '$lib/api/bitbucket.js';

	interface Props {
		open: boolean;
		slot: {
			tentacleName: string;
			slotNumber: number;
			vmName: string;
			testState: string;
			testTrName?: string;
			controller?: string;
			fwVer?: string;
			setLocation?: string;
			testToolName?: string;
		};
		source: string;
		onClose: () => void;
	}

	let { open = $bindable(), slot, source, onClose }: Props = $props();

	type StepStatus = 'pending' | 'running' | 'success' | 'failed';

	interface StepState {
		name: string;
		status: StepStatus;
		output: string;
		expanded: boolean;
		startTime: number;
		elapsed: number;
	}

	// Step별 실패 힌트
	const STEP_FAIL_HINTS: Record<number, string> = {
		1: 'TMUX 서버 SSH 접속 가능 여부, JTAG 케이블 연결, Success Pattern 정규식을 확인하세요.',
		2: 'JTAG 연결 상태를 확인하고, 슬롯이 Fail 상태가 아니면 Pre-Command로 Hang을 먼저 실행하세요.',
		3: 'dump.bat 로직과 출력 로그를 확인하세요. stdout에 "fail" 키워드가 포함되면 실패로 처리됩니다.'
	};

	// ── State ──
	let steps = $state<StepState[]>([]);
	let currentStep = $state(0);
	let phase = $state<'idle' | 'running' | 'done' | 'failed'>('idle');
	let resultPath = $state('');
	let abortFn = $state<(() => void) | null>(null);
	let elapsedTimer = $state<ReturnType<typeof setInterval> | null>(null);

	// 결과 파일
	interface ResultFile { name: string; directory: boolean; size: number; isImage: boolean; isZip: boolean; isHtml: boolean; }
	let resultFiles = $state<ResultFile[]>([]);
	let resultDirs = $state<string[]>([]);
	let resultFilesLoading = $state(false);
	let selectedImage = $state<string | null>(null);
	let reportUrl = $state<string | null>(null);

	// 브랜치 폴더 경로 선택
	let branchPath = $state('');
	let branchBrowserOpen = $state(false);

	// T32 config
	let t32Available = $state(false);
	let t32Config = $state<T32Config | null>(null);
	let configLoading = $state(true);
	let serverId = $state<number | null>(null);

	// Bitbucket 브랜치 목록
	let bbRepos = $state<BitbucketRepo[]>([]);
	let bbBranches = $state<BitbucketBranch[]>([]);
	let bbLoading = $state(false);
	let bbSelectedBranchId = $state<number | null>(null);
	let bbDownloading = $state<number | null>(null);
	let bbDownloadProgress = $state(0);
	let bbSearch = $state('');

	// 실행 확인 다이얼로그
	let confirmOpen = $state(false);

	const bbFiltered = $derived(
		bbSearch.trim()
			? bbBranches.filter(b => b.branchName.toLowerCase().includes(bbSearch.trim().toLowerCase()))
			: bbBranches
	);

	const isFail = $derived((slot.testState ?? '').toLowerCase().includes('fail'));

	// Linux → Windows 경로 변환 (표시용)
	function toWindowsPath(linuxPath: string): string {
		const linuxBase = t32Config?.fwCodeLinuxBase?.replace(/\/$/, '') ?? '';
		const winBase = t32Config?.fwCodeWindowsBase?.replace(/[/\\]$/, '') ?? '';
		if (linuxBase && winBase && linuxPath.startsWith(linuxBase)) {
			return winBase + linuxPath.substring(linuxBase.length).replace(/\//g, '\\');
		}
		return linuxPath;
	}

	function formatElapsed(ms: number): string {
		const sec = Math.floor(ms / 1000);
		const m = Math.floor(sec / 60);
		const s = sec % 60;
		return m > 0 ? `${m}:${s.toString().padStart(2, '0')}` : `${s}s`;
	}

	// ── Init ──
	$effect(() => {
		if (open) {
			resetState();
			loadConfig();
		}
	});

	function resetState() {
		if (elapsedTimer) { clearInterval(elapsedTimer); elapsedTimer = null; }
		steps = [
			{ name: 'JTAG 연결', status: 'pending', output: '', expanded: false, startTime: 0, elapsed: 0 },
			{ name: 'T32 Attach', status: 'pending', output: '', expanded: false, startTime: 0, elapsed: 0 },
			{ name: 'Dump 실행', status: 'pending', output: '', expanded: false, startTime: 0, elapsed: 0 },
			{ name: '완료', status: 'pending', output: '', expanded: false, startTime: 0, elapsed: 0 }
		];
		currentStep = 0;
		phase = 'idle';
		resultPath = '';
		branchPath = '';
		abortFn = null;
		bbSelectedBranchId = null;
		bbDownloading = null;
		bbDownloadProgress = 0;
		bbSearch = '';
		confirmOpen = false;
	}

	async function loadConfig() {
		configLoading = true;
		try {
			const servers = await fetchServers();
			const server = servers.find(s => s.name === slot.vmName);
			if (!server) { t32Available = false; return; }
			serverId = server.id;

			const result = await checkT32Available(server.id);
			t32Available = result.available;

			if (result.configId) {
				const configs = await fetchConfigs();
				t32Config = configs.find(c => c.id === result.configId) ?? null;
			}

			if (t32Available) loadBitbucketBranches();
		} catch {
			t32Available = false;
		} finally {
			configLoading = false;
		}
	}

	async function loadBitbucketBranches() {
		bbLoading = true;
		try {
			const repos = await fetchRepos();
			bbRepos = repos;
			const allBranches: BitbucketBranch[] = [];
			for (const repo of repos) {
				const branches = await fetchBranches(repo.id);
				allBranches.push(...branches);
			}
			bbBranches = allBranches.sort((a, b) => {
				const order: Record<string, number> = { DOWNLOADED: 0, DETECTED: 1, DOWNLOADING: 2, FAILED: 3 };
				return (order[a.status] ?? 9) - (order[b.status] ?? 9);
			});
		} catch { /* ignore */ }
		bbLoading = false;
	}

	function selectBranch(branch: BitbucketBranch) {
		if (branch.status === 'DOWNLOADED' && branch.filePath) {
			branchPath = branch.filePath;
			bbSelectedBranchId = branch.id;
		}
	}

	function handleDownloadBranch(branch: BitbucketBranch) {
		if (bbDownloading) return;
		bbDownloading = branch.id;
		bbDownloadProgress = 0;

		const abort = downloadDetectedBranch(
			branch.id,
			(mb) => {
				if (mb === -1) bbDownloadProgress = -1;
				else bbDownloadProgress = mb;
			},
			() => {
				bbDownloading = null;
				toast.success(`${branch.branchName} 다운로드 완료`);
				refreshAndSelectBranch(branch.id);
			},
			(msg) => {
				bbDownloading = null;
				toast.error(`다운로드 실패: ${msg}`);
			}
		);
	}

	async function refreshAndSelectBranch(branchId: number) {
		await loadBitbucketBranches();
		const updated = bbBranches.find(b => b.id === branchId);
		if (updated?.filePath) {
			branchPath = updated.filePath;
			bbSelectedBranchId = updated.id;
		}
	}

	function clearSelection() {
		branchPath = '';
		bbSelectedBranchId = null;
	}

	// ── Dump 실행 ──
	function requestStartDump() {
		confirmOpen = true;
	}

	function startDump() {
		confirmOpen = false;
		if (!serverId) return;
		phase = 'running';
		currentStep = 1;

		// 경과 시간 타이머 시작
		elapsedTimer = setInterval(() => {
			for (const step of steps) {
				if (step.status === 'running' && step.startTime > 0) {
					step.elapsed = Date.now() - step.startTime;
				}
			}
		}, 1000);

		const abort = executeT32Dump(
			serverId, slot.tentacleName, slot.slotNumber, slot.fwVer ?? '', branchPath,
			slot.setLocation ?? '', slot.testToolName ?? '', slot.testTrName ?? '',
			handleEvent, handleError
		);
		abortFn = abort;
	}

	function handleEvent(type: string, data: Record<string, any>) {
		if (type === 'step-start') {
			const idx = (data.step as number) - 1;
			if (idx >= 0 && idx < steps.length) {
				steps[idx].status = 'running';
				steps[idx].startTime = Date.now();
				currentStep = data.step as number;
			}
		} else if (type === 'step-output') {
			const idx = (data.step as number) - 1;
			if (idx >= 0 && idx < steps.length) {
				steps[idx].output += (data.line || '') + '\n';
			}
		} else if (type === 'step-done') {
			const idx = (data.step as number) - 1;
			if (idx >= 0 && idx < steps.length) {
				steps[idx].status = data.status === 'success' ? 'success' : 'failed';
				if (steps[idx].startTime > 0) steps[idx].elapsed = Date.now() - steps[idx].startTime;
				if (!steps[idx].output && data.output) steps[idx].output = data.output;
				if (data.status === 'failed') steps[idx].expanded = true;
			}
		} else if (type === 'dump-progress') {
			const idx = 2;
			if (steps[idx]) {
				steps[idx].output += `[${data.core}] ${data.phase} — ${data.status}\n`;
			}
		} else if (type === 'done') {
			stopTimer();
			if (data.success) {
				phase = 'done';
				resultPath = (data.resultPath as string) || '';
				loadResultFiles();
			} else {
				phase = 'failed';
			}
		} else if (type === 'error') {
			stopTimer();
			phase = 'failed';
			toast.error(data.message || '실행 오류');
		}
	}

	function stopTimer() {
		if (elapsedTimer) { clearInterval(elapsedTimer); elapsedTimer = null; }
	}

	async function loadResultFiles() {
		if (!resultPath) return;
		resultFilesLoading = true;
		reportUrl = null;
		try {
			const params = new URLSearchParams({ tentacleName: 'HEAD', path: resultPath });
			const res = await fetch(`/api/log-browser/files?${params}`);
			if (res.ok) {
				const files = await res.json();
				resultDirs = files
					.filter((f: any) => f.directory && f.name !== '..')
					.map((f: any) => f.name);
				resultFiles = files
					.filter((f: any) => !f.directory && f.name !== '..')
					.map((f: any) => ({
						name: f.name,
						directory: f.directory,
						size: f.size,
						isImage: /\.(png|jpg|jpeg|bmp|gif|tiff?)$/i.test(f.name),
						isZip: /\.zip$/i.test(f.name),
						isHtml: /\.html?$/i.test(f.name)
					}));

				// Canary 폴더 안의 *_report 폴더에서 *_report.html 탐색
				await findCanaryReport();
			}
		} catch { /* ignore */ }
		resultFilesLoading = false;
	}

	async function findCanaryReport() {
		// Canary 폴더 찾기 (대소문자 무시)
		const canaryDir = resultDirs.find(d => d.toLowerCase() === 'canary');
		if (!canaryDir) return;

		try {
			// Canary 하위 폴더 목록
			const params1 = new URLSearchParams({ tentacleName: 'HEAD', path: `${resultPath}/${canaryDir}` });
			const res1 = await fetch(`/api/log-browser/files?${params1}`);
			if (!res1.ok) return;
			const canaryFiles = await res1.json();
			const reportDir = canaryFiles.find((f: any) => f.directory && f.name.endsWith('_report'));
			if (!reportDir) return;

			// *_report 폴더 안에서 *_report.html 찾기
			const params2 = new URLSearchParams({ tentacleName: 'HEAD', path: `${resultPath}/${canaryDir}/${reportDir.name}` });
			const res2 = await fetch(`/api/log-browser/files?${params2}`);
			if (!res2.ok) return;
			const reportFiles = await res2.json();
			const htmlFile = reportFiles.find((f: any) => !f.directory && f.name.endsWith('_report.html'));
			if (htmlFile) {
				reportUrl = `/api/log-browser/serve/HEAD/${resultPath}/${canaryDir}/${reportDir.name}/${htmlFile.name}`;
			}
		} catch { /* ignore */ }
	}

	function downloadFile(fileName: string) {
		const params = new URLSearchParams({
			tentacleName: 'HEAD',
			path: `${resultPath}/${fileName}`
		});
		window.location.href = `/api/log-browser/download?${params}`;
	}

	function downloadAllAsZip() {
		// 결과 폴더를 ZIP으로 다운로드
		const params = new URLSearchParams({
			tentacleName: 'HEAD',
			path: resultPath
		});
		window.location.href = `/api/log-browser/download-dir?${params}`;
	}

	function downloadCanaryZip() {
		// Canary.zip은 dump 완료 시 T32 PC에서 자동 생성됨
		downloadFile('Canary.zip');
	}

	function openReport() {
		if (reportUrl) window.open(reportUrl, '_blank');
	}

	function getImageUrl(fileName: string): string {
		const params = new URLSearchParams({
			tentacleName: 'HEAD',
			path: `${resultPath}/${fileName}`
		});
		return `/api/log-browser/download?${params}`;
	}

	function formatFileSize(bytes: number): string {
		if (bytes < 1024) return bytes + 'B';
		if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + 'KB';
		return (bytes / 1024 / 1024).toFixed(1) + 'MB';
	}

	function handleError(error: string) {
		stopTimer();
		phase = 'failed';
		toast.error('T32 Dump 오류: ' + error);
	}

	function cancelDump() {
		abortFn?.();
		stopTimer();
		phase = 'failed';
	}

	function retryDump() {
		resetState();
		loadBitbucketBranches();
		phase = 'idle';
	}

	function toggleStep(idx: number) {
		steps[idx].expanded = !steps[idx].expanded;
	}

	function handleClose() {
		abortFn?.();
		stopTimer();
		onClose();
	}
</script>

<!-- 실행 확인 다이얼로그 -->
<ConfirmDialog
	bind:open={confirmOpen}
	title="T32 Dump 실행"
	description="선택한 브랜치로 Dump를 시작합니다."
	variant="default"
	confirmLabel="실행"
	onConfirm={startDump}
	onCancel={() => { confirmOpen = false; }}
>
	<div class="space-y-2 text-xs">
		<div class="border rounded-md p-2.5 bg-muted/20 space-y-1">
			<div class="flex items-center gap-2">
				<span class="text-muted-foreground w-16 shrink-0">슬롯</span>
				<span class="font-medium">{slot.vmName} Slot {slot.slotNumber}</span>
			</div>
			<div class="flex items-center gap-2">
				<span class="text-muted-foreground w-16 shrink-0">FW 폴더</span>
				<span class="font-mono text-[10px] break-all">{toWindowsPath(branchPath)}</span>
			</div>
			<div class="flex items-center gap-2">
				<span class="text-muted-foreground w-16 shrink-0">JTAG</span>
				<span>{t32Config?.jtagServerName}</span>
			</div>
			<div class="flex items-center gap-2">
				<span class="text-muted-foreground w-16 shrink-0">T32 PC</span>
				<span>{t32Config?.t32PcName}</span>
			</div>
		</div>
	</div>
</ConfirmDialog>

<!-- 브랜치 폴더 선택 LogBrowser (직접 찾기 fallback) -->
{#if branchBrowserOpen}
	<LogBrowserDialog
		bind:open={branchBrowserOpen}
		tentacleName="HEAD"
		initialPath={t32Config?.fwCodeLinuxBase || '/appdata/samsung/OCTO_HEAD/FW_Code'}
		rootPath={t32Config?.fwCodeLinuxBase || '/appdata/samsung/OCTO_HEAD/FW_Code'}
		title="FW 소스 폴더 선택"
		selectMode={true}
		folderSelect={true}
		onClose={() => { branchBrowserOpen = false; }}
		onSelect={(path) => { branchPath = path; bbSelectedBranchId = null; }}
	/>
{/if}

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) handleClose(); }}>
	<Dialog.Content class="max-w-2xl max-h-[85vh] flex flex-col">
		<Dialog.Header>
			<Dialog.Title class="text-sm">T32 Dump</Dialog.Title>
			<Dialog.Description class="text-xs text-muted-foreground">
				{slot.vmName} Slot {slot.slotNumber}
			</Dialog.Description>
		</Dialog.Header>

		<div class="flex-1 overflow-y-auto space-y-3 py-2">
			{#if configLoading}
				<div class="flex items-center justify-center gap-2 text-xs text-muted-foreground py-8">
					<LoaderIcon class="size-4 animate-spin" /> T32 설정 확인 중...
				</div>

			{:else if !t32Available}
				<div class="text-center py-8 space-y-2">
					<MonitorIcon class="size-8 text-muted-foreground/30 mx-auto" />
					<p class="text-xs text-muted-foreground">이 슬롯에 대한 T32 설정이 없습니다</p>
					<p class="text-[10px] text-muted-foreground/60">Admin → T32 탭에서 설정을 등록하세요</p>
				</div>

			{:else if phase === 'idle'}
				<!-- 연결 정보 -->
				<div class="border rounded-md p-2.5 bg-muted/20 space-y-1.5">
					<p class="{sectionLabel}">연결 정보</p>
					<div class="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
						<div class="flex items-center gap-1.5">
							<ServerIcon class="size-3 text-muted-foreground" />
							<span class="text-muted-foreground">JTAG:</span>
							<span class="font-medium">{t32Config?.jtagServerName}</span>
						</div>
						<div class="flex items-center gap-1.5">
							<MonitorIcon class="size-3 text-muted-foreground" />
							<span class="text-muted-foreground">T32 PC:</span>
							<span class="font-medium">{t32Config?.t32PcName}</span>
						</div>
					</div>
				</div>

				<!-- Fail 아닌 경우 경고 -->
				{#if !isFail}
					<div class="border border-yellow-300 rounded-md p-2.5 bg-yellow-50 flex items-start gap-2">
						<AlertTriangleIcon class="size-4 text-yellow-600 shrink-0 mt-0.5" />
						<div class="text-xs space-y-0.5">
							<p class="font-medium text-yellow-800">슬롯이 Fail 상태가 아닙니다</p>
							<p class="text-yellow-700/80">사전 명령어(Pre-Command)로 Hang을 먼저 걸어주세요</p>
						</div>
					</div>
				{/if}

				<!-- FW 소스 폴더 선택 -->
				<div class="space-y-1.5">
					<div class="flex items-center justify-between">
						<p class="{sectionLabel}">FW 소스 폴더</p>
						<button
							class="text-[10px] text-muted-foreground/60 hover:text-muted-foreground transition-colors"
							onclick={() => branchBrowserOpen = true}
						>
							직접 찾기
						</button>
					</div>

					{#if branchPath}
						<!-- 선택 완료 상태 -->
						{@const winPath = toWindowsPath(branchPath)}
						<div class="flex items-center gap-2 border rounded-md p-2 bg-accent/5 border-accent/30">
							<CheckCircleIcon class="size-4 text-accent shrink-0" />
							<div class="flex-1 min-w-0">
								<p class="text-xs font-medium break-all" title={winPath}>
									{winPath.split('\\').slice(-2).join('\\')}
								</p>
								<p class="text-[10px] text-muted-foreground break-all" title={winPath}>{winPath}</p>
							</div>
							<button onclick={clearSelection} class="p-1 rounded hover:bg-muted shrink-0 transition-colors" title="변경">
								<XIcon class="size-3 text-muted-foreground" />
							</button>
						</div>
					{:else}
						<!-- 브랜치 선택 리스트 -->
						{#if bbLoading}
							<div class="flex items-center justify-center gap-2 text-xs text-muted-foreground py-4 border rounded-md">
								<LoaderIcon class="size-3.5 animate-spin" /> 브랜치 목록 로드 중...
							</div>
						{:else if bbBranches.length === 0}
							<div class="text-center py-4 border rounded-md border-dashed space-y-1.5">
								<GitBranchIcon class="size-5 text-muted-foreground/30 mx-auto" />
								<p class="text-[10px] text-muted-foreground/60">등록된 Bitbucket 브랜치가 없습니다</p>
								<button
									class="text-[10px] text-accent hover:underline"
									onclick={() => branchBrowserOpen = true}
								>
									파일 시스템에서 직접 선택
								</button>
							</div>
						{:else}
							<div class="relative">
								<input
									class="w-full h-7 pl-7 pr-2 text-xs rounded-md border border-input bg-background placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
									placeholder="브랜치 검색..."
									bind:value={bbSearch}
								/>
								<svg class="absolute left-2 top-1.5 size-3.5 text-muted-foreground/50" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>
							</div>
							<div class="border rounded-md overflow-hidden max-h-[50vh] overflow-y-auto">
								{#if bbFiltered.length === 0}
									<div class="px-3 py-4 text-center text-[10px] text-muted-foreground/60">
										"{bbSearch}" 검색 결과 없음
									</div>
								{/if}
								{#each bbFiltered as branch (branch.id)}
									{@const isDownloaded = branch.status === 'DOWNLOADED' && !!branch.filePath}
									{@const isDetected = branch.status === 'DETECTED'}
									{@const isDownloading = bbDownloading === branch.id}
									{@const isBranchFailed = branch.status === 'FAILED'}
									<div
										class="flex items-center gap-2 px-2.5 py-1.5 text-xs border-b last:border-b-0 transition-colors
											{isDownloaded ? 'hover:bg-green-50 cursor-pointer' : ''}
											{isDownloading ? 'bg-blue-50/50' : ''}"
										onclick={() => isDownloaded && selectBranch(branch)}
										role={isDownloaded ? 'button' : undefined}
										tabindex={isDownloaded ? 0 : undefined}
										onkeydown={(e) => e.key === 'Enter' && isDownloaded && selectBranch(branch)}
									>
										<!-- 상태 아이콘 -->
										{#if isDownloaded}
											<FolderOpenIcon class="size-3.5 text-green-600 shrink-0" />
										{:else if isDetected}
											<CloudDownloadIcon class="size-3.5 text-muted-foreground/50 shrink-0" />
										{:else if isBranchFailed}
											<XCircleIcon class="size-3.5 text-red-500/50 shrink-0" />
										{:else}
											<LoaderIcon class="size-3.5 text-blue-500 animate-spin shrink-0" />
										{/if}

										<!-- 브랜치명 -->
										<div class="flex-1 min-w-0">
											<p class="break-all font-mono {isDownloaded ? 'text-foreground' : 'text-muted-foreground'}">
												{branch.branchName}
											</p>
											{#if isDownloaded && branch.filePath}
												{@const winBranchPath = toWindowsPath(branch.filePath)}
												<p class="text-[9px] text-muted-foreground/60 break-all">
													{winBranchPath.split('\\').slice(-2).join('\\')}
												</p>
											{/if}
										</div>

										<!-- 액션 -->
										{#if isDownloading}
											<span class="text-[9px] text-blue-500 shrink-0 flex items-center gap-1">
												<LoaderIcon class="size-2.5 animate-spin" />
												{#if bbDownloadProgress === -1}
													압축 해제 중...
												{:else}
													{bbDownloadProgress}MB
												{/if}
											</span>
										{:else if isDetected || isBranchFailed}
											<button
												class="shrink-0 px-2 py-0.5 text-[9px] rounded-full border border-blue-400 text-blue-600 bg-blue-50 hover:bg-blue-100 transition-colors flex items-center gap-1 active:scale-[0.97]"
												onclick={(e) => { e.stopPropagation(); handleDownloadBranch(branch); }}
											>
												<DownloadIcon class="size-2.5" />
												다운로드
											</button>
										{/if}
									</div>
								{/each}
							</div>
						{/if}
					{/if}
				</div>

			{:else}
				<!-- Stepper -->
				<div class="space-y-1.5">
					{#each steps as step, idx}
						<div class="border rounded-md overflow-hidden {step.status === 'running' ? 'border-blue-300' : ''} {step.status === 'failed' ? 'border-red-300' : ''}">
							<button
								class="flex items-center gap-2 w-full px-3 py-2 text-xs transition-colors
									{step.status === 'running' ? 'bg-blue-50/50' : ''}
									{step.status === 'success' ? 'bg-green-50/30' : ''}
									{step.status === 'failed' ? 'bg-red-50/30' : ''}
									hover:bg-muted/30"
								onclick={() => toggleStep(idx)}
							>
								{#if step.status === 'pending'}
									<CircleIcon class="size-4 text-muted-foreground/30 shrink-0" />
								{:else if step.status === 'running'}
									<LoaderIcon class="size-4 text-blue-500 animate-spin shrink-0" />
								{:else if step.status === 'success'}
									<CheckCircleIcon class="size-4 text-green-600 shrink-0" />
								{:else}
									<XCircleIcon class="size-4 text-red-600 shrink-0" />
								{/if}

								<span class="font-medium {step.status === 'pending' ? 'text-muted-foreground/50' : ''}">
									Step {idx + 1}: {step.name}
								</span>

								<!-- 경과 시간 -->
								{#if step.status === 'running' || ((step.status === 'success' || step.status === 'failed') && step.elapsed > 0)}
									<span class="text-[10px] text-muted-foreground flex items-center gap-0.5 ml-1">
										<TimerIcon class="size-2.5" />
										{formatElapsed(step.elapsed)}
									</span>
								{/if}

								<span class="ml-auto">
									{#if step.output}
										{#if step.expanded}
											<ChevronDownIcon class="size-3.5 text-muted-foreground" />
										{:else}
											<ChevronRightIcon class="size-3.5 text-muted-foreground" />
										{/if}
									{/if}
								</span>
							</button>

							{#if step.expanded && step.output}
								<div class="border-t px-3 py-2 bg-muted/10">
									<pre class="text-[10px] font-mono whitespace-pre-wrap max-h-32 overflow-y-auto text-muted-foreground leading-relaxed">{step.output.trim()}</pre>
								</div>
							{/if}
						</div>
					{/each}
				</div>

				<!-- 결과 -->
				{#if phase === 'done' && resultPath}
					<div class="border border-green-300 rounded-md overflow-hidden bg-green-50/50">
						<div class="px-3 py-2">
							<p class="text-xs font-medium text-green-700 flex items-center gap-1.5">
								<CheckCircleIcon class="size-3.5" /> Dump 완료
							</p>
							<p class="text-[10px] text-muted-foreground font-mono break-all mt-0.5">{resultPath.split('/').slice(-2).join('/')}</p>
						</div>

						{#if resultFilesLoading}
							<div class="px-3 pb-2 flex items-center gap-1.5 text-[10px] text-muted-foreground">
								<LoaderIcon class="size-3 animate-spin" /> 파일 목록 로드 중...
							</div>
						{:else if resultFiles.length > 0 || resultDirs.length > 0}
							<div class="border-t border-green-200">
								<!-- Canary Report + ZIP 버튼 -->
								{#if resultDirs.some(d => d.toLowerCase() === 'canary')}
									<div class="px-3 py-2 flex items-center gap-2 border-b border-green-200 bg-green-50">
										<FolderOpenIcon class="size-3.5 text-green-600" />
										<span class="text-[10px] font-medium">Canary</span>
										{#if reportUrl}
											<button
												class="ml-auto inline-flex items-center gap-1 px-2 py-1 text-[9px] rounded border bg-blue-50 text-blue-700 hover:bg-blue-100 transition-colors"
												onclick={openReport}
											>
												<FileTextIcon class="size-3" />
												Report 보기
											</button>
										{/if}
										<button
											class="px-2 py-1 text-[9px] rounded border hover:bg-muted transition-colors {reportUrl ? '' : 'ml-auto'}"
											onclick={downloadCanaryZip}
										>
											<DownloadIcon class="size-3 inline" /> Canary.zip
										</button>
									</div>
								{/if}

								<!-- 기타 디렉토리 표시 -->
								{#each resultDirs.filter(d => d.toLowerCase() !== 'canary') as dir}
									<div class="flex items-center gap-2 px-3 py-1.5 text-xs border-b border-green-200">
										<FolderOpenIcon class="size-3 text-muted-foreground shrink-0" />
										<span class="flex-1 truncate">{dir}/</span>
										<button
											class="shrink-0 px-1.5 py-0.5 text-[9px] rounded border hover:bg-muted transition-colors"
											onclick={() => {
												const params = new URLSearchParams({ tentacleName: 'HEAD', path: `${resultPath}/${dir}` });
												window.location.href = `/api/log-browser/download-dir?${params}`;
											}}
										>
											<DownloadIcon class="size-3 inline" /> .zip
										</button>
									</div>
								{/each}

								{#if selectedImage}
									<div class="p-2 border-b border-green-200 bg-black/5">
										<div class="flex items-center justify-between mb-1">
											<span class="text-[10px] font-medium">{selectedImage}</span>
											<button class="text-[10px] text-muted-foreground hover:text-foreground" onclick={() => selectedImage = null}>닫기</button>
										</div>
										<img
											src={getImageUrl(selectedImage)}
											alt={selectedImage}
											class="max-w-full max-h-[400px] object-contain rounded mx-auto"
										/>
									</div>
								{/if}

								<div class="divide-y divide-green-200">
									{#each resultFiles as file}
										<div class="flex items-center gap-2 px-3 py-1.5 text-xs hover:bg-green-100/50">
											{#if file.isImage}
												<button
													class="flex-1 text-left text-blue-600 hover:underline truncate flex items-center gap-1"
													onclick={() => selectedImage = file.name}
													title="클릭하여 미리보기"
												>
													<ImageIcon class="size-3 shrink-0" /> {file.name}
												</button>
											{:else if file.isZip}
												<span class="flex-1 truncate flex items-center gap-1"><FileArchiveIcon class="size-3 shrink-0" /> {file.name}</span>
											{:else}
												<span class="flex-1 truncate text-muted-foreground flex items-center gap-1"><FileIcon class="size-3 shrink-0" /> {file.name}</span>
											{/if}
											<span class="text-[9px] text-muted-foreground shrink-0">{formatFileSize(file.size)}</span>
											<button
												class="shrink-0 px-1.5 py-0.5 text-[9px] rounded border hover:bg-muted transition-colors"
												onclick={() => downloadFile(file.name)}
											>
												<DownloadIcon class="size-3 inline" />
											</button>
										</div>
									{/each}
								</div>
							</div>
						{/if}
					</div>
				{/if}

				{#if phase === 'failed'}
					<div class="border border-red-300 rounded-md p-3 bg-red-50/50 space-y-1.5">
						<p class="text-xs font-medium text-red-700 flex items-center gap-1.5">
							<XCircleIcon class="size-3.5" /> Step {currentStep}에서 실패
						</p>
						{#if STEP_FAIL_HINTS[currentStep]}
							<p class="{captionMuted}">{STEP_FAIL_HINTS[currentStep]}</p>
						{:else}
							<p class="{captionMuted}">출력 로그를 펼쳐서 원인을 확인하세요</p>
						{/if}
					</div>
				{/if}
			{/if}
		</div>

		<Dialog.Footer class="gap-2">
			{#if phase === 'idle' && t32Available}
				<Button variant="outline" size="sm" onclick={handleClose}>닫기</Button>
				<Button size="sm" onclick={requestStartDump} disabled={!branchPath} class="gap-1">
					<PlayIcon class="size-3" />
					Dump 시작
				</Button>
			{:else if phase === 'running'}
				<Button variant="destructive" size="sm" onclick={cancelDump}>중단</Button>
			{:else if phase === 'done'}
				{#if resultPath && resultFiles.length > 0}
					<Button variant="outline" size="sm" class="gap-1" onclick={downloadAllAsZip}>
						<DownloadIcon class="size-3" />
						전체 다운로드
					</Button>
				{/if}
				<Button size="sm" onclick={handleClose}>닫기</Button>
			{:else if phase === 'failed'}
				<Button variant="outline" size="sm" onclick={retryDump} class="gap-1">
					<RotateCcwIcon class="size-3" />
					다시 시도
				</Button>
				<Button size="sm" onclick={handleClose}>닫기</Button>
			{:else}
				<Button size="sm" onclick={handleClose}>닫기</Button>
			{/if}
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
