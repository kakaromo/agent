<script lang="ts">
	import DatabaseIcon from '@lucide/svelte/icons/database';

	interface Props {
		tcState: string;
		logPath?: string;
		onBrowse: () => void;
	}

	const { tcState, logPath, onBrowse }: Props = $props();

	// 표시 정책: NOTSTART 와 빈 상태만 제외. 그 외 모두 보임.
	// 호출자가 logPath 유무로 history vs tentacle 현재 log 폴더 자동 라우팅.
	const showButton = $derived.by(() => {
		const t = (tcState ?? '').toLowerCase().trim();
		if (!t || t === 'notstart') return !!logPath;
		return true;
	});
</script>

{#if showButton}
	<button
		class="inline-flex items-center gap-1 rounded px-2 py-1 text-[10px] font-medium bg-purple-100 text-purple-700 hover:bg-purple-200 transition-colors"
		onclick={onBrowse}
		title="View UFS metadata"
	>
		<DatabaseIcon class="size-3" />
		Meta
	</button>
{:else}
	<span class="text-muted-foreground text-[10px]">—</span>
{/if}
