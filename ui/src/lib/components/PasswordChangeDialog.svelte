<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Input } from '$lib/components/ui/input/index.js';
	import { Button } from '$lib/components/ui/button/index.js';
	import { auth } from '$lib/stores/auth.svelte.js';
	import { toast } from 'svelte-sonner';
	import LoaderCircleIcon from '@lucide/svelte/icons/loader-circle';
	import KeyRoundIcon from '@lucide/svelte/icons/key-round';

	interface Props {
		open: boolean;
		isFirstTime?: boolean;
	}

	let { open = $bindable(), isFirstTime = false }: Props = $props();

	let currentPassword = $state('');
	let newPassword = $state('');
	let confirmPassword = $state('');
	let saving = $state(false);
	let error = $state('');

	function reset() {
		currentPassword = '';
		newPassword = '';
		confirmPassword = '';
		error = '';
		saving = false;
	}

	async function handleSubmit() {
		error = '';

		if (newPassword.length < 4) {
			error = '비밀번호는 4자 이상이어야 합니다';
			return;
		}
		if (newPassword !== confirmPassword) {
			error = '비밀번호가 일치하지 않습니다';
			return;
		}

		saving = true;
		const result = await auth.changePassword(
			isFirstTime ? null : currentPassword,
			newPassword
		);
		saving = false;

		if (result.success) {
			toast.success('비밀번호가 설정되었습니다');
			open = false;
			reset();
		} else {
			error = result.error ?? '비밀번호 변경 실패';
		}
	}
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) reset(); }}>
	<Dialog.Content class="sm:max-w-md">
		<Dialog.Header>
			<Dialog.Title class="flex items-center gap-2">
				<KeyRoundIcon class="size-5" />
				{isFirstTime ? '폐쇄망 비밀번호 설정' : '비밀번호 변경'}
			</Dialog.Title>
			<Dialog.Description>
				{#if isFirstTime}
					폐쇄망에서 로그인하려면 비밀번호를 설정해주세요.
				{:else}
					현재 비밀번호를 확인한 후 새 비밀번호를 설정합니다.
				{/if}
			</Dialog.Description>
		</Dialog.Header>

		<form class="space-y-4 py-2" onsubmit={(e) => { e.preventDefault(); handleSubmit(); }}>
			{#if auth.username}
				<div class="flex items-center gap-2 text-sm">
					<span class="text-muted-foreground">계정:</span>
					<span class="font-medium">{auth.username}</span>
				</div>
			{/if}

			{#if !isFirstTime}
				<div class="space-y-1.5">
					<label for="currentPw" class="text-sm font-medium">현재 비밀번호</label>
					<Input id="currentPw" type="password" bind:value={currentPassword} disabled={saving} autocomplete="current-password" />
				</div>
			{/if}

			<div class="space-y-1.5">
				<label for="newPw" class="text-sm font-medium">새 비밀번호</label>
				<Input id="newPw" type="password" bind:value={newPassword} disabled={saving} autocomplete="new-password" />
			</div>

			<div class="space-y-1.5">
				<label for="confirmPw" class="text-sm font-medium">비밀번호 확인</label>
				<Input id="confirmPw" type="password" bind:value={confirmPassword} disabled={saving} autocomplete="new-password" />
			</div>

			{#if error}
				<p class="text-xs text-destructive">{error}</p>
			{/if}

			<div class="flex justify-end gap-2 pt-2">
				<Button variant="outline" type="button" onclick={() => { open = false; reset(); }} disabled={saving}>
					취소
				</Button>
				<Button type="submit" disabled={saving}>
					{#if saving}
						<LoaderCircleIcon class="size-4 animate-spin mr-1" />
					{/if}
					{isFirstTime ? '설정' : '변경'}
				</Button>
			</div>
		</form>
	</Dialog.Content>
</Dialog.Root>
