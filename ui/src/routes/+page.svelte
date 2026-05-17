<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	// standalone root → /agent 리다이렉트. portal 와 달리 로그인 단계 없음.
	onMount(() => {
		goto('/agent', { replaceState: true });
	});
</script>

<div class="flex h-full items-center justify-center text-sm text-muted-foreground">
	Loading…
</div>
