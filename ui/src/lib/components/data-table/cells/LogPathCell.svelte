<script lang="ts">
	import FileText from '@lucide/svelte/icons/file-text';

	interface Props {
		logPath?: string;
	}

	const { logPath }: Props = $props();
</script>

{#if logPath}
	<button
		class="inline-flex items-center gap-1 rounded px-2 py-1 text-[10px] font-medium bg-blue-100 text-blue-700 hover:bg-blue-200 transition-colors"
		onclick={() => window.open(logPath, '_blank')}
		title={logPath}
	>
		<FileText class="size-3" />
		Log
	</button>
{:else}
	<span class="text-muted-foreground text-[10px]">—</span>
{/if}
