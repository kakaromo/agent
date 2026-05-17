<script lang="ts">
	import { reparseStore } from '$lib/stores/reparse.svelte.js';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import CheckCircleIcon from '@lucide/svelte/icons/check-circle';
	import XCircleIcon from '@lucide/svelte/icons/x-circle';
	import ChevronUpIcon from '@lucide/svelte/icons/chevron-up';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import XIcon from '@lucide/svelte/icons/x';
	import FileTextIcon from '@lucide/svelte/icons/file-text';

	let collapsed = $state(false);

	const visibleJobs = $derived(
		reparseStore.allJobs.filter(j =>
			j.state === 'preparing' || j.state === 'running' ||
			(j.state === 'completed' && Date.now() - j.updatedAt < 60_000) ||
			(j.state === 'failed' && Date.now() - j.updatedAt < 120_000)
		)
	);

	const runningCount = $derived(reparseStore.activeJobs.length);

	function elapsed(startedAt: number): string {
		const sec = Math.floor((Date.now() - startedAt) / 1000);
		if (sec < 60) return `${sec}s`;
		const min = Math.floor(sec / 60);
		if (min < 60) return `${min}m ${sec % 60}s`;
		const hr = Math.floor(min / 60);
		return `${hr}h ${min % 60}m`;
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
				<LoaderIcon class="size-3.5 text-amber-600 shrink-0 animate-spin" />
			{:else}
				<CheckCircleIcon class="size-3.5 text-green-600 shrink-0" />
			{/if}
			<span class="font-medium flex-1 text-left">
				{runningCount > 0 ? `Reparse ${runningCount}건 진행 중` : 'Reparse 완료'}
			</span>
			{#if collapsed}
				<ChevronUpIcon class="size-3.5 text-muted-foreground shrink-0" />
			{:else}
				<ChevronDownIcon class="size-3.5 text-muted-foreground shrink-0" />
			{/if}
		</button>

		{#if collapsed && runningCount > 0}
			<div class="w-full bg-muted h-0.5 rounded-b-lg overflow-hidden">
				<div class="h-0.5 rounded-b-lg bg-amber-600 animate-pulse" style="width: 100%"></div>
			</div>
		{/if}

		<div
			class="grid transition-[grid-template-rows] duration-200 ease-out"
			style="grid-template-rows: {collapsed ? '0fr' : '1fr'}"
		>
			<div class="overflow-hidden">
				<div class="border-t max-h-64 overflow-y-auto">
					{#each visibleJobs as job (job.jobId)}
						{@const pct = job.totalFiles > 0 ? Math.round((job.currentIndex / job.totalFiles) * 100) : 0}
						<div class="px-3 py-2 border-b last:border-b-0">
							<div class="flex items-center gap-1.5 text-[10px] mb-1">
								<span class="px-1 py-0.5 rounded bg-amber-100 text-amber-700">
									reparse
								</span>
								<span class="flex-1 font-mono" title="History #{job.historyId}">
									H#{job.historyId}
								</span>
								<span class="text-muted-foreground">{elapsed(job.startedAt)}</span>

								{#if job.state === 'completed'}
									<button
										class="hover:text-destructive transition-colors"
										onclick={() => reparseStore.dismissJob(job.jobId)}
									>
										<XIcon class="size-3" />
									</button>
								{:else if job.state === 'failed'}
									<button
										class="hover:text-destructive transition-colors"
										onclick={() => reparseStore.dismissJob(job.jobId)}
									>
										<XIcon class="size-3" />
									</button>
								{/if}
							</div>

							{#if job.state === 'preparing'}
								<div class="flex items-center gap-1.5 text-[10px] text-muted-foreground">
									<LoaderIcon class="size-3 animate-spin" />
									<span>준비 중...</span>
								</div>
							{:else if job.state === 'running'}
								<div class="space-y-1">
									<div class="flex items-center gap-1.5 text-[10px]">
										<FileTextIcon class="size-3 text-amber-600 shrink-0" />
										<span class="truncate flex-1" title={job.currentFileName}>
											{job.currentFileName || '...'}
										</span>
										<span class="text-muted-foreground shrink-0">
											{job.currentIndex + 1}/{job.totalFiles}
										</span>
									</div>
									<div class="w-full bg-muted rounded-full h-1.5 overflow-hidden">
										<div
											class="h-full bg-amber-600 rounded-full transition-all duration-300"
											style="width: {pct}%"
										></div>
									</div>
								</div>
							{:else if job.state === 'completed'}
								<div class="flex items-center gap-1.5 text-[10px] text-green-600">
									<CheckCircleIcon class="size-3" />
									<span>{job.totalFiles}개 파일 파싱 완료</span>
								</div>
							{:else if job.state === 'failed'}
								<div class="flex items-center gap-1.5 text-[10px] text-red-600">
									<XCircleIcon class="size-3" />
									<span class="truncate" title={job.error}>{job.error || 'Reparse 실패'}</span>
								</div>
							{/if}
						</div>
					{/each}
				</div>
			</div>
		</div>
	</div>
{/if}

<style>
	@keyframes slideUp {
		from { opacity: 0; transform: translateY(16px); }
		to { opacity: 1; transform: translateY(0); }
	}
</style>
