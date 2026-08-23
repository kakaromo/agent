<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { sectionLabel, captionMuted } from '$lib/styles/common.js';
	import { startTrace, stopTrace } from '$lib/api/agent.js';
	import type { ActiveJob } from './types.js';
	import PlayIcon from '@lucide/svelte/icons/play';
	import SquareIcon from '@lucide/svelte/icons/square';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
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
	const TRACE_TYPES = [
		{ value: 'ufs', label: 'UFS', desc: 'UFS 레이어 I/O' },
		{ value: 'block', label: 'Block', desc: 'Block 레이어 I/O' },
		{ value: 'both', label: 'Both', desc: 'UFS + Block' },
		{ value: 'fsio_ufs', label: 'fsio UFS', desc: 'eBPF · UFS + 파일 귀속' },
		{ value: 'fsio_block', label: 'fsio Block', desc: 'eBPF · Block + 파일 귀속' }
	];
	let windowSeconds = $state(0);
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
		<div class="grid grid-cols-3 gap-2">
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
</div>
