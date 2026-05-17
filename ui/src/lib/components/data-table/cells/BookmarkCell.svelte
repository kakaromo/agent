<script lang="ts">
	import Bookmark from '@lucide/svelte/icons/bookmark';
	import { Button } from '$lib/components/ui/button';

	interface Props {
		bookmarked?: boolean;
		onToggle?: () => void;
	}

	const { bookmarked = false, onToggle }: Props = $props();
</script>

<Button
	variant="ghost"
	size="icon"
	class="size-8 {bookmarked ? 'text-primary' : 'text-muted-foreground'}"
	onclick={onToggle}
>
	<Bookmark class="size-4" fill={bookmarked ? 'currentColor' : 'none'} />
</Button>
