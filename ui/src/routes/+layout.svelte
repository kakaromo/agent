<script lang="ts">
	import '../app.css';
	import { Toaster } from 'svelte-sonner';

	let { children } = $props();
	// standalone 은 /agent 한 페이지가 전부 (Trace 결과는 AgentTraceResultSheet 안에서 즉시 시각화).
	// portal 의 /trace 페이지는 MinIO archive 파싱 흐름이라 standalone 에선 의미 없어 제거.
</script>

<div class="min-h-screen flex flex-col bg-background text-foreground">
	<header class="border-b px-4 py-2 flex items-center">
		<h1 class="text-sm font-semibold tracking-tight">Agent Standalone</h1>
	</header>
	<main class="flex-1 overflow-hidden">
		{@render children()}
	</main>
	<Toaster richColors position="top-right" />
</div>
