<script lang="ts">
	import FolderOpenIcon from '@lucide/svelte/icons/folder-open';

	interface Props {
		tcState: string;
		logPath?: string;
		onBrowse: () => void;
	}

	const { tcState, logPath, onBrowse }: Props = $props();

	// 표시 정책: NOTSTART 와 빈 상태만 제외. 그 외 (RUNNING / WARNING / WARNING_PASS / PASS / FAIL / ...)
	// 모두 보임. 호출자(slot 상세) 가 logPath 유무로 history 폴더 vs tentacle 현재 log 폴더 자동 라우팅.
	const showButton = $derived.by(() => {
		const t = (tcState ?? '').toLowerCase().trim();
		if (!t || t === 'notstart') return !!logPath;
		return true;
	});
</script>

{#if showButton}
	<button
		class="inline-flex items-center gap-1 rounded px-2 py-1 text-[10px] font-medium bg-blue-100 text-blue-700 hover:bg-blue-200 transition-colors"
		onclick={onBrowse}
		title="Browse log files"
	>
		<FolderOpenIcon class="size-3" />
		Log
	</button>
{:else}
	<span class="text-muted-foreground text-[10px]">—</span>
{/if}
