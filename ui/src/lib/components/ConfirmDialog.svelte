<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import AlertTriangleIcon from '@lucide/svelte/icons/triangle-alert';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';

	import type { Snippet } from 'svelte';

	interface Props {
		open: boolean;
		title?: string;
		description: string;
		confirmLabel?: string;
		cancelLabel?: string;
		variant?: 'destructive' | 'default';
		onConfirm: () => void | Promise<void>;
		onCancel: () => void;
		children?: Snippet;
	}

	let {
		open = $bindable(),
		title = '확인',
		description,
		confirmLabel = '확인',
		cancelLabel = '취소',
		variant = 'destructive',
		onConfirm,
		onCancel,
		children
	}: Props = $props();

	let busy = $state(false);

	async function handleConfirm() {
		busy = true;
		try {
			await onConfirm();
			open = false;
		} finally {
			busy = false;
		}
	}

	function handleCancel() {
		if (busy) return;
		onCancel();
	}
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) handleCancel(); }}>
	<!-- z-[60]: ConfirmDialog 는 다른 Dialog/Sheet(z-50) 위에서 열리는 경우가 많아
	     항상 그 위로 겹쳐 보이도록 content/overlay z-index 를 올린다. -->
	<Dialog.Content class="sm:max-w-sm z-[60]" overlayClass="z-[60]" showCloseButton={false}>
		<Dialog.Header>
			<Dialog.Title class="flex items-center gap-2 text-sm">
				{#if variant === 'destructive'}
					<AlertTriangleIcon class="size-4 text-destructive" />
				{/if}
				{title}
			</Dialog.Title>
			<Dialog.Description class="text-xs text-muted-foreground">
				{description}
			</Dialog.Description>
		</Dialog.Header>
		{#if children}
			<div class="py-2">
				{@render children()}
			</div>
		{/if}
		<Dialog.Footer class="gap-2">
			<Button variant="outline" size="sm" onclick={handleCancel} disabled={busy}>
				{cancelLabel}
			</Button>
			<Button
				variant={variant === 'destructive' ? 'destructive' : 'default'}
				size="sm"
				onclick={handleConfirm}
				disabled={busy}
			>
				{#if busy}
					<LoaderIcon class="size-3.5 animate-spin" />
				{/if}
				{confirmLabel}
			</Button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
