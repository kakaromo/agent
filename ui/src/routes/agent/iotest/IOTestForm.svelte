<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { sectionLabel, captionMuted } from '$lib/styles/common.js';
	import { runBenchmark } from '$lib/api/agent.js';
	import IOTestEditor from './IOTestEditor.svelte';
	import type { IOTestConfig } from './types.js';
	import type { ActiveJob } from '../types.js';
	import PlayIcon from '@lucide/svelte/icons/play';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';

	interface Props {
		serverId: number | null;
		selectedDevices: Set<string>;
		serverName: string;
		onJobStarted: (job: Omit<ActiveJob, 'events' | 'state' | 'eventSource'>) => void;
	}

	let { serverId, selectedDevices, serverName, onJobStarted }: Props = $props();

	let jobName = $state('');
	let busyPolicy = $state('reject');
	let running = $state(false);

	let iotestConfig = $state<IOTestConfig>({ threads: [], duration_seconds: 0, sync_start: true });

	let deviceCount = $derived(selectedDevices.size);

	async function handleRun() {
		if (serverId == null || deviceCount === 0) return;
		if (iotestConfig.threads.length === 0) {
			toast.error('스레드를 1개 이상 추가해주세요');
			return;
		}
		running = true;
		try {
			const params = { config: JSON.stringify(iotestConfig) };
			const res = await runBenchmark(serverId, {
				deviceIds: [...selectedDevices],
				tool: 'IOTEST',
				params,
				jobName: jobName || undefined,
				busyPolicy
			});
			toast.success(`I/O Test 시작: ${res.jobId}`);
			onJobStarted({
				jobId: res.jobId,
				serverId,
				serverName,
				type: 'benchmark',
				tool: 'IOTEST',
				jobName: jobName || undefined,
				deviceIds: [...selectedDevices],
				createdAt: Date.now()
			});
		} catch { toast.error('I/O Test 시작 실패'); }
		finally { running = false; }
	}
</script>

<div class="max-w-2xl space-y-4 p-1">
	<div>
		<h2 class="text-sm font-semibold">I/O Test</h2>
		{#if deviceCount > 0}
			<p class="{captionMuted} mt-0.5">{deviceCount}개 디바이스에서 syscall 레벨 I/O 테스트를 실행합니다</p>
		{:else}
			<p class="text-[10px] text-orange-600 mt-0.5">왼쪽에서 디바이스를 선택해주세요</p>
		{/if}
	</div>

	<!-- Job Name + Busy Policy -->
	<div class="grid grid-cols-2 gap-3">
		<div class="space-y-1">
			<label class="{sectionLabel}">Job Name</label>
			<input bind:value={jobName} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background" placeholder="선택 사항" />
		</div>
		<div class="space-y-1">
			<label class="{sectionLabel}">Busy Policy</label>
			<select bind:value={busyPolicy} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background">
				<option value="reject">Reject (BUSY 시 거부)</option>
				<option value="wait">Wait (대기 후 순차 실행)</option>
				<option value="force">Force (동시 실행)</option>
			</select>
		</div>
	</div>

	<!-- IOTest Editor -->
	<IOTestEditor bind:config={iotestConfig} onUpdate={(c) => { iotestConfig = c; }} />

	<!-- Run -->
	<button
		onclick={handleRun}
		disabled={running || deviceCount === 0 || serverId == null || iotestConfig.threads.length === 0}
		class="w-full inline-flex items-center justify-center gap-2 rounded-md bg-blue-600 text-white px-4 py-2.5 text-xs font-medium hover:bg-blue-700 disabled:opacity-50 transition-colors"
	>
		{#if running}
			<LoaderIcon class="size-4 animate-spin" /> 실행 중...
		{:else}
			<PlayIcon class="size-4" /> I/O Test 실행
		{/if}
	</button>
</div>
