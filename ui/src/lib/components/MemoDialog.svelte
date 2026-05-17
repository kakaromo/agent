<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import ConfirmDialog from './ConfirmDialog.svelte';
	import TiptapEditor from './TiptapEditor.svelte';
	import { updateSlotMemo } from '$lib/api/testdb.js';
	import { toast } from 'svelte-sonner';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import MaximizeIcon from '@lucide/svelte/icons/maximize';
	import MinimizeIcon from '@lucide/svelte/icons/minimize';

	interface Props {
		open: boolean;
		tentacleName: string;
		slotNumber: number;
		slotLabel: string;
		initialMemo?: string;
		onClose: () => void;
		onSaved?: () => void;
	}

	let { open = $bindable(), tentacleName, slotNumber, slotLabel, initialMemo = '', onClose, onSaved }: Props = $props();

	let editorRef: TiptapEditor | undefined = $state(undefined);
	let currentHtml = $state(initialMemo);
	let saving = $state(false);
	let isDirty = $state(false);
	let fullscreen = $state(false);

	// Unsaved changes confirm
	let discardConfirmOpen = $state(false);

	function handleUpdate(html: string) {
		currentHtml = html;
		isDirty = true;
	}

	async function handleSave() {
		saving = true;
		try {
			await updateSlotMemo(tentacleName, slotNumber, currentHtml);
			isDirty = false;
			toast.success('메모가 저장되었습니다');
			onSaved?.();
			open = false;
			onClose();
		} catch (e) {
			toast.error('메모 저장에 실패했습니다');
			console.error('Failed to save memo:', e);
		} finally {
			saving = false;
		}
	}

	function handleOpenChange(v: boolean) {
		if (!v) {
			if (isDirty) {
				discardConfirmOpen = true;
				return;
			}
			fullscreen = false;
			onClose();
		}
	}

	function discardAndClose() {
		isDirty = false;
		fullscreen = false;
		discardConfirmOpen = false;
		open = false;
		onClose();
	}
</script>

<ConfirmDialog
	bind:open={discardConfirmOpen}
	title="변경사항 폐기"
	description="저장하지 않은 변경사항이 사라집니다. 닫으시겠습니까?"
	confirmLabel="닫기"
	cancelLabel="계속 편집"
	variant="default"
	onConfirm={discardAndClose}
	onCancel={() => { discardConfirmOpen = false; }}
/>

<Dialog.Root bind:open onOpenChange={handleOpenChange}>
	<Dialog.Content class="{fullscreen ? 'sm:max-w-none w-screen h-screen !rounded-none' : 'sm:max-w-4xl max-h-[85vh]'} flex flex-col transition-all">
		<button
			class="absolute end-12 top-4 rounded-xs opacity-70 transition-opacity hover:opacity-100 p-0 border-0 bg-transparent cursor-pointer"
			onclick={() => fullscreen = !fullscreen}
			title={fullscreen ? 'Exit fullscreen' : 'Fullscreen'}
		>
			{#if fullscreen}
				<MinimizeIcon class="size-4" />
			{:else}
				<MaximizeIcon class="size-4" />
			{/if}
		</button>

		<Dialog.Header>
			<Dialog.Title>Memo - {slotLabel}</Dialog.Title>
			<Dialog.Description class="text-xs text-muted-foreground">
				{tentacleName} Slot {slotNumber}
			</Dialog.Description>
		</Dialog.Header>

		<div class="flex-1 overflow-y-auto min-h-0 py-2">
			<TiptapEditor
				bind:this={editorRef}
				content={initialMemo}
				placeholder="Write a memo for this slot..."
				onUpdate={handleUpdate}
			/>
		</div>

		<Dialog.Footer class="shrink-0 pt-2">
			<button
				class="px-4 py-2 text-sm rounded-md border hover:bg-accent transition-colors"
				onclick={() => handleOpenChange(false)}
			>
				Cancel
			</button>
			<button
				class="px-4 py-2 text-sm rounded-md bg-primary text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-50 inline-flex items-center gap-2"
				disabled={saving}
				onclick={handleSave}
			>
				{#if saving}
					<LoaderIcon class="size-4 animate-spin" />
				{/if}
				Save
			</button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
