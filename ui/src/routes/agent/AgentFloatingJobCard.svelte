<script lang="ts">
	import type { ActiveJob } from './types.js';
	import { cancelJob } from '$lib/api/agent.js';
	import { toast } from 'svelte-sonner';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import CheckCircleIcon from '@lucide/svelte/icons/check-circle';
	import XCircleIcon from '@lucide/svelte/icons/x-circle';
	import BanIcon from '@lucide/svelte/icons/ban';
	import ChevronUpIcon from '@lucide/svelte/icons/chevron-up';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import XIcon from '@lucide/svelte/icons/x';
	import ExternalLinkIcon from '@lucide/svelte/icons/external-link';
	import SquareIcon from '@lucide/svelte/icons/square';

	interface Props {
		activeJobs: ActiveJob[];
		onDismiss: (jobId: string) => void;
		onViewResult: (serverId: number, jobId: string) => void;
	}

	let { activeJobs, onDismiss, onViewResult }: Props = $props();
	let cancelling = $state<string | null>(null);
	let confirmCancelOpen = $state(false);
	let cancelTargetJob = $state<ActiveJob | null>(null);

	async function handleCancel(job: ActiveJob) {
		cancelling = job.jobId;
		try {
			const res = await cancelJob(job.serverId, job.jobId);
			toast[res.success ? 'success' : 'error'](res.message);
		} catch { toast.error('중단 실패'); }
		finally { cancelling = null; }
	}

	let collapsed = $state(false);

	let visibleJobs = $derived(activeJobs.filter(j => j.state !== 'completed' || Date.now() - j.createdAt < 60_000));
	let runningCount = $derived(activeJobs.filter(j => j.state === 'running').length);

	function getDeviceProgress(job: ActiveJob): Map<string, { state: string; percent: number }> {
		const map = new Map<string, { state: string; percent: number }>();
		for (const e of job.events) {
			map.set(e.deviceId, { state: e.state, percent: e.progressPercent });
		}
		return map;
	}

	function stateColor(state: string) {
		if (state === 'completed') return 'text-green-600';
		if (state === 'failed') return 'text-red-600';
		if (state === 'cancelled') return 'text-orange-600';
		return 'text-blue-600';
	}
</script>

{#if visibleJobs.length > 0}
	<div
		class="fixed bottom-4 right-4 z-40 w-80 bg-popover text-popover-foreground border rounded-lg shadow-lg"
		style="animation: slideUp 200ms ease-out"
	>
		<button
			class="flex items-center gap-2 w-full px-3 py-2 text-xs hover:bg-muted/50 transition-colors rounded-t-lg"
			onclick={() => collapsed = !collapsed}
		>
			{#if runningCount > 0}
				<LoaderIcon class="size-3.5 text-blue-600 shrink-0 animate-spin" />
			{:else}
				<CheckCircleIcon class="size-3.5 text-green-600 shrink-0" />
			{/if}
			<span class="font-medium flex-1 text-left">
				{runningCount > 0 ? `${runningCount}개 Job 실행 중` : '모든 Job 완료'}
			</span>
			{#if collapsed}
				<ChevronUpIcon class="size-3.5 text-muted-foreground shrink-0" />
			{:else}
				<ChevronDownIcon class="size-3.5 text-muted-foreground shrink-0" />
			{/if}
		</button>

		{#if collapsed && runningCount > 0}
			<div class="w-full bg-muted h-0.5 rounded-b-lg overflow-hidden">
				<div class="h-0.5 rounded-b-lg bg-blue-600 animate-pulse" style="width: 100%"></div>
			</div>
		{/if}

		<div
			class="grid transition-[grid-template-rows] duration-200 ease-out"
			style="grid-template-rows: {collapsed ? '0fr' : '1fr'}"
		>
			<div class="overflow-hidden">
				<div class="border-t max-h-64 overflow-y-auto">
					{#each visibleJobs as job (job.jobId)}
						{@const progress = getDeviceProgress(job)}
						<div class="px-3 py-2 border-b last:border-b-0">
							<div class="flex items-center gap-1.5 text-[10px] mb-1">
								<span class="px-1 py-0.5 rounded {job.type === 'benchmark' ? 'bg-blue-100 text-blue-700' : job.type === 'trace' ? 'bg-emerald-100 text-emerald-700' : 'bg-purple-100 text-purple-700'}">
									{job.type}
								</span>
								<span class="flex-1 font-mono" title={job.jobId}>{job.jobId.slice(0, 8)}</span>
								<span class="{stateColor(job.state)}">
									{#if job.state === 'running'}
										<LoaderIcon class="size-3 animate-spin" />
									{:else if job.state === 'completed'}
										<CheckCircleIcon class="size-3" />
									{:else if job.state === 'cancelled'}
										<BanIcon class="size-3" />
									{:else}
										<XCircleIcon class="size-3" />
									{/if}
								</span>
							</div>

							{#if progress.size > 0}
								{#each [...progress.entries()] as [deviceId, dp]}
									<div class="flex items-center gap-1 text-[9px] mb-0.5">
										<span class="font-mono w-20 truncate">{deviceId}</span>
										<div class="flex-1 bg-muted rounded-full h-1">
											<div class="bg-blue-600 h-1 rounded-full transition-all" style="width: {dp.percent}%"></div>
										</div>
										<span class="w-8 text-right tabular-nums">{dp.percent}%</span>
									</div>
								{/each}
							{/if}

							<div class="flex gap-1 mt-1">
								<button
									onclick={() => onViewResult(job.serverId, job.jobId)}
									class="inline-flex items-center gap-0.5 rounded border px-1.5 py-0.5 text-[9px] hover:bg-muted"
								>
									<ExternalLinkIcon class="size-2.5" />
									{job.state === 'running' ? '실시간 결과' : '결과 보기'}
								</button>
								{#if job.state === 'running'}
									<button
										onclick={() => { cancelTargetJob = job; confirmCancelOpen = true; }}
										disabled={cancelling === job.jobId}
										class="inline-flex items-center gap-0.5 rounded border border-red-300 px-1.5 py-0.5 text-[9px] text-red-600 hover:bg-red-50 disabled:opacity-50"
									>
										{#if cancelling === job.jobId}
											<LoaderIcon class="size-2.5 animate-spin" />
										{:else}
											<SquareIcon class="size-2.5" />
										{/if}
										중단
									</button>
								{:else}
									<button
										onclick={() => onDismiss(job.jobId)}
										class="p-0.5 rounded hover:bg-muted text-muted-foreground"
									>
										<XIcon class="size-3" />
									</button>
								{/if}
							</div>
						</div>
					{/each}
				</div>
			</div>
		</div>
	</div>
{/if}

<ConfirmDialog
	bind:open={confirmCancelOpen}
	title="Job 중단"
	description="실행 중인 Job을 중단하시겠습니까? 이 작업은 되돌릴 수 없습니다."
	confirmLabel="중단"
	variant="destructive"
	onConfirm={async () => { if (cancelTargetJob) await handleCancel(cancelTargetJob); confirmCancelOpen = false; cancelTargetJob = null; }}
	onCancel={() => { confirmCancelOpen = false; cancelTargetJob = null; }}
/>

<style>
	@keyframes slideUp {
		from { transform: translateY(20px); opacity: 0; }
		to { transform: translateY(0); opacity: 1; }
	}
</style>
