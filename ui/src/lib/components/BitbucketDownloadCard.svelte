<script lang="ts">
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import CheckCircleIcon from '@lucide/svelte/icons/circle-check';
	import XCircleIcon from '@lucide/svelte/icons/circle-x';
	import XIcon from '@lucide/svelte/icons/x';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import ChevronUpIcon from '@lucide/svelte/icons/chevron-up';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';

	export interface DownloadItem {
		id: number; // branchId
		branchName: string;
		status: 'downloading' | 'extracting' | 'done' | 'failed';
		downloadedMb: number;
		error?: string;
	}

	interface Props {
		visible: boolean;
		items: DownloadItem[];
		onClose: () => void;
		onRemove: (id: number) => void;
	}

	let { visible, items, onClose, onRemove }: Props = $props();
	let collapsed = $state(false);

	const activeCount = $derived(items.filter(i => i.status === 'downloading' || i.status === 'extracting').length);
	const doneCount = $derived(items.filter(i => i.status === 'done').length);
	const failedCount = $derived(items.filter(i => i.status === 'failed').length);
	const allDone = $derived(activeCount === 0 && items.length > 0);
</script>

{#if visible && items.length > 0}
	<div class="fixed bottom-4 right-4 z-50 w-80 rounded-lg border bg-card shadow-lg overflow-hidden">
		<!-- Header -->
		<div class="flex items-center gap-2 px-3 py-2 bg-muted/50 border-b">
			<DownloadIcon class="size-3.5 text-muted-foreground" />
			<span class="text-xs font-medium flex-1">
				Downloads
				{#if activeCount > 0}
					<span class="text-blue-600">({activeCount} 진행 중)</span>
				{:else}
					<span class="text-muted-foreground">({doneCount} 완료{failedCount > 0 ? `, ${failedCount} 실패` : ''})</span>
				{/if}
			</span>
			<button onclick={() => collapsed = !collapsed} class="p-0.5 rounded hover:bg-muted">
				{#if collapsed}
					<ChevronUpIcon class="size-3.5 text-muted-foreground" />
				{:else}
					<ChevronDownIcon class="size-3.5 text-muted-foreground" />
				{/if}
			</button>
			{#if allDone}
				<button onclick={onClose} class="p-0.5 rounded hover:bg-muted" title="전체 닫기">
					<XIcon class="size-3.5 text-muted-foreground" />
				</button>
			{/if}
		</div>

		{#if !collapsed}
			<div class="max-h-64 overflow-y-auto divide-y">
				{#each items as item (item.id)}
					<div class="px-3 py-2 space-y-1.5">
						<!-- 브랜치명 + 닫기 -->
						<div class="flex items-center gap-1">
							<p class="text-[10px] font-mono text-muted-foreground truncate flex-1" title={item.branchName}>
								{item.branchName.length > 30 ? item.branchName.substring(0, 30) + '...' : item.branchName}
							</p>
							{#if item.status === 'done' || item.status === 'failed'}
								<button onclick={() => onRemove(item.id)} class="p-0.5 rounded hover:bg-muted shrink-0">
									<XIcon class="size-2.5 text-muted-foreground" />
								</button>
							{/if}
						</div>

						<!-- 상태 -->
						<div class="flex items-center gap-2">
							{#if item.status === 'downloading'}
								<LoaderIcon class="size-3.5 text-blue-500 animate-spin shrink-0" />
								<span class="text-[10px] text-blue-600 font-medium">다운로드 중</span>
								<span class="text-[10px] text-muted-foreground ml-auto">{item.downloadedMb} MB</span>
							{:else if item.status === 'extracting'}
								<LoaderIcon class="size-3.5 text-purple-500 animate-spin shrink-0" />
								<span class="text-[10px] text-purple-600 font-medium">압축 해제 중</span>
								<span class="text-[10px] text-muted-foreground ml-auto">{item.downloadedMb} MB</span>
							{:else if item.status === 'done'}
								<CheckCircleIcon class="size-3.5 text-green-600 shrink-0" />
								<span class="text-[10px] text-green-600 font-medium">완료</span>
								<span class="text-[10px] text-muted-foreground ml-auto">{item.downloadedMb} MB</span>
							{:else}
								<XCircleIcon class="size-3.5 text-red-600 shrink-0" />
								<span class="text-[10px] text-red-600 font-medium truncate">{item.error || '실패'}</span>
							{/if}
						</div>

						<!-- 프로그레스 바 -->
						{#if item.status === 'downloading' || item.status === 'extracting'}
							<div class="h-1 bg-muted rounded-full overflow-hidden">
								<div
									class="h-full rounded-full transition-all duration-300 {item.status === 'downloading' ? 'bg-blue-500' : 'bg-purple-500'}"
									style="width: {item.status === 'extracting' ? '100' : Math.min(item.downloadedMb / 5, 100)}%"
								></div>
							</div>
						{/if}
					</div>
				{/each}
			</div>
		{/if}
	</div>
{/if}
