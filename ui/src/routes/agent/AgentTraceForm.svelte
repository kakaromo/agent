<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { TRACE_TYPES } from '$lib/config/traceTypes.js';
	import { sectionLabel, captionMuted } from '$lib/styles/common.js';
	import { startTrace, stopTrace, uploadFile } from '$lib/api/agent.js';
	import type { ActiveJob } from './types.js';
	import PlayIcon from '@lucide/svelte/icons/play';
	import SquareIcon from '@lucide/svelte/icons/square';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import UploadIcon from '@lucide/svelte/icons/upload';
	import ActivityIcon from '@lucide/svelte/icons/activity';

	interface Props {
		serverId: number | null;
		selectedDevices: Set<string>;
		serverName: string;
		onJobStarted: (job: Omit<ActiveJob, 'events' | 'state' | 'eventSource'>) => void;
		activeTraceJobId: string | null;
	}

	let { serverId, selectedDevices, serverName, onJobStarted, activeTraceJobId = $bindable() }: Props = $props();

	let traceType = $state('ufs');

	// trace_type 선택지. fsio_* 는 bpftrace(eBPF) 기반이라 수집 방식 자체가 다르다.
	// 한 번에 한 레이어만 받는다 (`--only ufs` / `--only blk`) — ftrace 의 Both 에
	// 해당하는 조합은 두지 않는다.

	let windowSeconds = $state(0);
	// fsio_* 에서 VFS 레이어도 받을지 — Page Cache 통계의 전제.
	let includeVfs = $state(false);
	let jobName = $state('');
	let starting = $state(false);
	let stopping = $state(false);

	let deviceCount = $derived(selectedDevices.size);
	let singleDeviceId = $derived(deviceCount === 1 ? [...selectedDevices][0] : null);

	async function handleStart() {
		if (serverId == null || !singleDeviceId) return;
		starting = true;
		try {
			const res = await startTrace(serverId, {
				deviceId: singleDeviceId,
				traceType,
				windowSeconds: windowSeconds > 0 ? windowSeconds : undefined,
				includeVfs: traceType.startsWith('fsio_') ? includeVfs : undefined,
				jobName: jobName || undefined
			});
			activeTraceJobId = res.jobId;
			toast.success(`Trace 시작: ${res.jobId}`);
			onJobStarted({
				jobId: res.jobId,
				serverId,
				serverName,
				type: 'trace',
				jobName: jobName || `trace-${traceType}`,
				deviceIds: [singleDeviceId],
				createdAt: Date.now()
			});
		} catch {
			toast.error('Trace 시작 실패');
		} finally {
			starting = false;
		}
	}

	// ── 파일 업로드 ──
	//
	// 기기 없이 남의 로그·과거 로그를 보기 위한 경로. 포맷은 **서버가 내용으로 판별**한다
	// — 사용자가 고르게 하면 잘못 골랐을 때 파서가 에러 없이 0건을 내서
	// "수집은 됐는데 비어 있다" 로 보인다.
	let uploading = $state(false);
	let fileInput: HTMLInputElement | null = $state(null);

	async function handleUpload(e: Event) {
		const input = e.target as HTMLInputElement;
		const f = input.files?.[0];
		if (!f) return;
		uploading = true;
		try {
			const res = await uploadFile(f);
			if (res.kind === 'trace' && res.jobId) {
				toast.success(`${res.reason} — 파싱을 시작했습니다`);
				onJobStarted({
					jobId: res.jobId,
					serverId: serverId!,
					serverName,
					type: 'trace',
					jobName: res.name || f.name,
					deviceIds: [],   // 업로드 잡은 기기가 없다
					createdAt: Date.now()
				});
			} else {
				toast.success(`${res.reason} — ${res.deviceId ?? ''} ${res.tool ?? ''}`.trim());
			}
		} catch (err) {
			// 서버가 무엇을 고쳐야 하는지 알려준다 — 그대로 보여준다.
			toast.error(err instanceof Error ? err.message : '업로드 실패');
		} finally {
			uploading = false;
			input.value = '';   // 같은 파일을 다시 고를 수 있게
		}
	}

	async function handleStop() {
		if (serverId == null || !activeTraceJobId) return;
		stopping = true;
		try {
			await stopTrace(serverId, activeTraceJobId);
			toast.success('Trace 중지됨');
			activeTraceJobId = null;
		} catch {
			toast.error('Trace 중지 실패');
		} finally {
			stopping = false;
		}
	}
</script>

<div class="max-w-xl space-y-4 p-1">
	<div>
		<h2 class="text-sm font-semibold">I/O Trace</h2>
		{#if deviceCount === 1}
			<p class="text-[10px] text-muted-foreground mt-0.5">디바이스 1개에서 ftrace I/O 수집</p>
		{:else if deviceCount === 0}
			<p class="text-[10px] text-orange-600 mt-0.5">왼쪽에서 디바이스를 1개 선택해주세요</p>
		{:else}
			<p class="text-[10px] text-orange-600 mt-0.5">Trace는 디바이스 1개만 선택해주세요 (현재 {deviceCount}개)</p>
		{/if}
	</div>

	<!-- Trace Type -->
	<div class="space-y-1">
		<label class="{sectionLabel}">Trace Type</label>
		<div class="grid grid-cols-3 sm:grid-cols-5 gap-2">
			{#each TRACE_TYPES as t}
				<button
					onclick={() => traceType = t.value}
					disabled={!!activeTraceJobId}
					class="border rounded-md px-3 py-2 text-left transition-colors disabled:opacity-50
						{traceType === t.value ? 'border-primary bg-primary/5 ring-1 ring-primary' : 'hover:bg-muted'}"
				>
					<div class="text-xs font-medium">{t.label}</div>
					<div class="text-[9px] text-muted-foreground">{t.desc}</div>
				</button>
			{/each}
		</div>
		{#if traceType.startsWith('fsio_')}
			<!-- eBPF 는 root 가 필수다. 아니면 StartTrace 가 명시적으로 실패한다
			     (조용히 빈 로그를 만드는 것보다 낫다). -->
			<div class="text-[9px] text-amber-600 dark:text-amber-500 leading-relaxed">
				eBPF 기반 — <b>root(userdebug)</b> 필요. 파일명·프로세스·syscall 귀속과
				io_flags(journal/GC/writeback 등), UFS management 이벤트를 함께 수집합니다.
			</div>
			<!-- VFS 레이어는 별도로 켜야 한다.
			     fsiotrace 의 `--only` 는 출력 레이어 필터라, ufs/blk 만 받으면
			     page cache 판정 row(vfs_read:exit / mmap_fault:exit)가 로그에
			     아예 안 남는다 → Page Cache 탭이 통째로 빈다. -->
			<label class="flex items-start gap-1.5 text-[10px] cursor-pointer mt-1.5">
				<input
					type="checkbox"
					bind:checked={includeVfs}
					disabled={!!activeTraceJobId}
					class="size-3 mt-0.5 shrink-0"
				/>
				<span>
					VFS 레이어 함께 수집
					<span class="block text-[9px] text-muted-foreground leading-relaxed">
						Page Cache 적중률과 mmap 통계를 보려면 필요합니다. 켜지 않으면 해당 화면이 빕니다.
						대신 로그가 커집니다.
					</span>
				</span>
			</label>
		{/if}
	</div>

	<!-- Window Seconds -->
	<div class="space-y-1">
		<label class="{sectionLabel}">Window (자동 중지)</label>
		<div class="flex items-center gap-2">
			<input
				type="number"
				bind:value={windowSeconds}
				min="0"
				disabled={!!activeTraceJobId}
				class="w-24 border rounded px-2.5 py-1.5 text-xs bg-background"
			/>
			<span class="{captionMuted}">초 (0 = 수동 중지)</span>
		</div>
	</div>

	<!-- Job Name -->
	<div class="space-y-1">
		<label class="{sectionLabel}">Job Name</label>
		<input
			bind:value={jobName}
			disabled={!!activeTraceJobId}
			class="w-full border rounded px-2.5 py-1.5 text-xs bg-background"
			placeholder="선택 사항"
		/>
	</div>

	<!-- Start / Stop -->
	{#if activeTraceJobId}
		<div class="space-y-2">
			<div class="flex items-center gap-2 text-[10px] text-blue-600">
				<ActivityIcon class="size-3 animate-pulse" />
				<span>Trace 수집 중... Job: <span class="font-mono">{activeTraceJobId}</span></span>
			</div>
			<button
				onclick={handleStop}
				disabled={stopping}
				class="w-full inline-flex items-center justify-center gap-2 rounded-md border border-red-300 text-red-600 px-4 py-2.5 text-xs font-medium hover:bg-red-50 disabled:opacity-50 transition-colors"
			>
				{#if stopping}
					<LoaderIcon class="size-4 animate-spin" /> 중지 중...
				{:else}
					<SquareIcon class="size-4" /> Trace 중지
				{/if}
			</button>
		</div>
	{:else}
		<button
			onclick={handleStart}
			disabled={starting || deviceCount !== 1 || serverId == null}
			class="w-full inline-flex items-center justify-center gap-2 rounded-md bg-blue-600 text-white px-4 py-2.5 text-xs font-medium hover:bg-blue-700 disabled:opacity-50 transition-colors"
		>
			{#if starting}
				<LoaderIcon class="size-4 animate-spin" /> 시작 중...
			{:else}
				<PlayIcon class="size-4" /> Trace 시작
			{/if}
		</button>
	{/if}
	<!-- 파일 업로드 — 기기 없이 로그를 보는 경로 -->
	<div class="pt-3 mt-1 border-t space-y-1.5">
		<label class="{sectionLabel}">파일 열기</label>
		<input
			bind:this={fileInput}
			type="file"
			accept=".log,.txt,.tsv,.json"
			onchange={handleUpload}
			class="hidden"
		/>
		<button
			onclick={() => fileInput?.click()}
			disabled={uploading}
			class="w-full inline-flex items-center justify-center gap-2 rounded-md border px-4 py-2 text-xs font-medium hover:bg-muted disabled:opacity-50 transition-colors"
		>
			{#if uploading}
				<LoaderIcon class="size-4 animate-spin" /> 분석 중...
			{:else}
				<UploadIcon class="size-4" /> trace 로그 · 결과 JSON 열기
			{/if}
		</button>
		<p class="{captionMuted}">
			기기 없이 기존 로그를 분석합니다. 포맷(ftrace / fsio / 벤치마크 JSON)은 자동으로 판별합니다.
		</p>
	</div>
</div>
