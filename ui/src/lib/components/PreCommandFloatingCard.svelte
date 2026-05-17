<script lang="ts">
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import { captionMuted } from '$lib/styles/common.js';
	import CheckCircleIcon from '@lucide/svelte/icons/check-circle';
	import XCircleIcon from '@lucide/svelte/icons/x-circle';
	import SkipForwardIcon from '@lucide/svelte/icons/skip-forward';
	import ChevronUpIcon from '@lucide/svelte/icons/chevron-up';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import XIcon from '@lucide/svelte/icons/x';

	export interface SlotExecution {
		slotIndex: number;
		slotLabel: string;
		usbId: string;
		vmName: string;
		status: 'waiting' | 'running' | 'success' | 'failed' | 'skipped';
		skipReason?: string;
		commandCount: number;
		commands: CommandExecution[];
	}

	export interface CommandExecution {
		index: number;
		command: string;
		status: 'waiting' | 'running' | 'success' | 'failed' | 'error';
		exitCode?: number;
		output?: string;
	}

	export interface PreCommandProgress {
		slots: SlotExecution[];
		summary: { total: number; completed: number; failed: number; skipped: number };
		done: boolean;
	}

	interface Props {
		visible: boolean;
		preCommandName: string;
		progress: PreCommandProgress;
		onClose: () => void;
		onAbort: () => void;
	}

	let { visible, preCommandName, progress, onClose, onAbort }: Props = $props();

	let collapsed = $state(false);
	let expandedSlot = $state<number | null>(null);

	let isRunning = $derived(!progress.done && progress.summary.total > 0);
	let successCount = $derived(progress.summary.total - progress.summary.failed - progress.summary.skipped);

	// 새 슬롯이 running 시작하면 자동 expand
	let lastSlotCount = $state(0);
	$effect(() => {
		const runningSlots = progress.slots.filter(s => s.status === 'running');
		if (runningSlots.length > 0 && progress.slots.length > lastSlotCount) {
			expandedSlot = runningSlots[runningSlots.length - 1].slotIndex;
		}
		lastSlotCount = progress.slots.length;
	});

	// done이 되면 collapsed 해제 + 실패 슬롯 자동 펼침
	$effect(() => {
		if (progress.done) {
			collapsed = false;
			const failedSlot = progress.slots.find(s => s.status === 'failed');
			if (failedSlot) expandedSlot = failedSlot.slotIndex;
		}
	});

	function toggleSlot(slotIndex: number) {
		expandedSlot = expandedSlot === slotIndex ? null : slotIndex;
	}

	function handleClose() {
		if (isRunning) {
			onAbort();
		} else {
			onClose();
		}
	}
</script>

{#if visible}
	<div
		class="fixed bottom-4 right-4 z-40 w-96 bg-popover text-popover-foreground border rounded-lg shadow-lg"
		style="animation: slideUp 200ms ease-out"
	>
		<!-- 헤더 -->
		<div class="flex items-center gap-2 px-3 py-2.5">
			<button
				class="flex items-center gap-2 flex-1 min-w-0 hover:opacity-80 transition-opacity"
				onclick={() => collapsed = !collapsed}
			>
				{#if isRunning}
					<LoaderIcon class="size-4 text-blue-600 shrink-0 animate-spin" />
				{:else if progress.summary.failed > 0}
					<XCircleIcon class="size-4 text-red-500 shrink-0" />
				{:else}
					<CheckCircleIcon class="size-4 text-green-600 shrink-0" />
				{/if}
				<span class="text-xs font-medium truncate">{preCommandName}</span>
				<span class="text-[10px] text-muted-foreground shrink-0">
					{#if isRunning}
						{progress.summary.completed}/{progress.summary.total}
					{:else if progress.summary.total > 0}
						{successCount} 성공{#if progress.summary.failed > 0} · {progress.summary.failed} 실패{/if}{#if progress.summary.skipped > 0} · {progress.summary.skipped} 스킵{/if}
					{/if}
				</span>
				{#if collapsed}
					<ChevronUpIcon class="size-3.5 text-muted-foreground shrink-0" />
				{:else}
					<ChevronDownIcon class="size-3.5 text-muted-foreground shrink-0" />
				{/if}
			</button>
			<button
				class="p-0.5 rounded hover:bg-muted text-muted-foreground shrink-0"
				onclick={handleClose}
				title={isRunning ? '중단' : '닫기'}
			>
				<XIcon class="size-3.5" />
			</button>
		</div>

		{#if isRunning}
			<div class="w-full bg-muted h-1 {collapsed ? 'rounded-b-lg' : ''} overflow-hidden">
				<div class="h-1 bg-blue-600 transition-all duration-500 ease-out {collapsed ? 'rounded-b-lg' : ''}"
					style="width: {progress.summary.total > 0 ? (progress.summary.completed / progress.summary.total * 100) : 0}%"></div>
			</div>
		{:else if progress.done && progress.summary.total > 0}
			<div class="w-full h-1 {collapsed ? 'rounded-b-lg' : ''} overflow-hidden {progress.summary.failed > 0 ? 'bg-red-100' : 'bg-green-100'}">
				<div class="h-1 {collapsed ? 'rounded-b-lg' : ''} {progress.summary.failed > 0 ? 'bg-red-500' : 'bg-green-500'}" style="width: 100%"></div>
			</div>
		{/if}

		<div class="grid transition-[grid-template-rows] duration-200 ease-out"
			style="grid-template-rows: {collapsed ? '0fr' : '1fr'}">
			<div class="overflow-hidden">
				<div class="border-t max-h-72 overflow-y-auto">
					{#each progress.slots as slot (slot.slotIndex)}
						{@const isExpanded = expandedSlot === slot.slotIndex}
						<div class="border-b last:border-b-0">
							<button
								class="flex items-center gap-2 w-full px-3 py-1.5 text-left hover:bg-muted/30 transition-colors"
								onclick={() => toggleSlot(slot.slotIndex)}
							>
								{#if slot.status === 'running'}
									<LoaderIcon class="size-3 text-blue-600 shrink-0 animate-spin" />
								{:else if slot.status === 'success'}
									<CheckCircleIcon class="size-3 text-green-600 shrink-0" />
								{:else if slot.status === 'failed'}
									<XCircleIcon class="size-3 text-red-500 shrink-0" />
								{:else if slot.status === 'skipped'}
									<SkipForwardIcon class="size-3 text-amber-500 shrink-0" />
								{:else}
									<div class="size-3 rounded-full border border-muted-foreground/30 shrink-0"></div>
								{/if}

								<span class="text-[11px] font-medium flex-1 truncate">{slot.slotLabel}</span>

								{#if slot.status === 'skipped' && slot.skipReason}
									<span class="text-[10px] text-amber-600 truncate max-w-[140px]">{slot.skipReason}</span>
								{:else if slot.status === 'running' && slot.commands.length > 0}
									<span class="{captionMuted}">
										{slot.commands.filter(c => c.status === 'success').length}/{slot.commandCount}
									</span>
								{/if}

								{#if slot.commands.length > 0}
									<ChevronRightIcon class="size-3 text-muted-foreground shrink-0 transition-transform {isExpanded ? 'rotate-90' : ''}" />
								{/if}
							</button>

							{#if isExpanded && slot.commands.length > 0}
								<div class="px-3 pb-2 space-y-1">
									{#each slot.commands as cmd (cmd.index)}
										<div class="flex items-start gap-1.5">
											{#if cmd.status === 'running'}
												<LoaderIcon class="size-3 text-blue-600 shrink-0 animate-spin mt-0.5" />
											{:else if cmd.status === 'success'}
												<CheckCircleIcon class="size-3 text-green-600 shrink-0 mt-0.5" />
											{:else if cmd.status === 'failed' || cmd.status === 'error'}
												<XCircleIcon class="size-3 text-red-500 shrink-0 mt-0.5" />
											{:else}
												<div class="size-3 shrink-0 mt-0.5"></div>
											{/if}
											<div class="min-w-0 flex-1">
												<div class="text-[10px] font-mono truncate text-muted-foreground" title={cmd.command}>{cmd.command}</div>
												{#if cmd.output && (cmd.status === 'failed' || cmd.status === 'error')}
													<pre class="text-[9px] text-red-500 bg-red-50 rounded px-1.5 py-1 mt-0.5 whitespace-pre-wrap break-all max-h-20 overflow-y-auto">{cmd.output}</pre>
												{:else if cmd.output && cmd.status === 'success'}
													<pre class="text-[9px] text-muted-foreground bg-muted/30 rounded px-1.5 py-1 mt-0.5 whitespace-pre-wrap break-all max-h-16 overflow-y-auto">{cmd.output}</pre>
												{/if}
											</div>
										</div>
									{/each}
								</div>
							{/if}
						</div>
					{/each}

					{#if progress.slots.length === 0 && progress.summary.total > 0}
						<div class="flex items-center justify-center py-4 text-muted-foreground">
							<LoaderIcon class="size-4 animate-spin" />
						</div>
					{/if}
				</div>
			</div>
		</div>
	</div>
{/if}

<style>
	@keyframes slideUp {
		from { transform: translateY(20px); opacity: 0; }
		to { transform: translateY(0); opacity: 1; }
	}
</style>
