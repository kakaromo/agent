<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import * as Table from '$lib/components/ui/table/index.js';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import { toast } from 'svelte-sonner';
	import {
		fetchAgentServers,
		createAgentServer,
		updateAgentServer,
		deleteAgentServer,
		testAgentServerById,
		testAgentConnection,
		type AgentServer
	} from '$lib/api/agent.js';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import SaveIcon from '@lucide/svelte/icons/save';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import XIcon from '@lucide/svelte/icons/x';
	import PlugIcon from '@lucide/svelte/icons/plug';
	import CheckIcon from '@lucide/svelte/icons/check';
	import XCircleIcon from '@lucide/svelte/icons/x-circle';

	interface Props {
		open: boolean;
		servers: AgentServer[];
		onRefresh: () => Promise<void>;
	}

	let { open = $bindable(), servers = $bindable(), onRefresh }: Props = $props();

	let saving = $state(false);
	let testing = $state(false);
	let testResult = $state<{ success: boolean; message: string } | null>(null);

	let showForm = $state(false);
	let editingId = $state<number | null>(null);
	let form = $state({ name: '', host: '', port: 50051, enabled: true, description: '' });

	let confirmOpen = $state(false);
	let confirmDesc = $state('');
	let confirmAction = $state<() => Promise<void>>(async () => {});

	function startCreate() {
		editingId = null;
		form = { name: '', host: '', port: 50051, enabled: true, description: '' };
		testResult = null;
		showForm = true;
	}

	function startEdit(s: AgentServer) {
		editingId = s.id;
		form = { name: s.name, host: s.host, port: s.port, enabled: s.enabled, description: s.description ?? '' };
		testResult = null;
		showForm = true;
	}

	function cancelForm() {
		showForm = false;
		editingId = null;
		testResult = null;
	}

	async function testBeforeSave() {
		testing = true;
		testResult = null;
		try {
			testResult = await testAgentConnection(form.host, form.port);
		} catch {
			testResult = { success: false, message: '접속 테스트 실패' };
		} finally {
			testing = false;
		}
	}

	async function save() {
		saving = true;
		try {
			const isEdit = editingId != null;
			if (isEdit) {
				await updateAgentServer(editingId!, form);
			} else {
				await createAgentServer(form);
			}
			showForm = false;
			editingId = null;
			testResult = null;
			toast.success(isEdit ? '서버가 수정되었습니다' : '서버가 생성되었습니다');
			await onRefresh();
		} catch {
			toast.error('저장에 실패했습니다');
		} finally {
			saving = false;
		}
	}

	function requestDelete(id: number) {
		confirmDesc = '이 Agent 서버를 삭제하시겠습니까?';
		confirmAction = async () => {
			await deleteAgentServer(id);
			toast.success('서버가 삭제되었습니다');
			await onRefresh();
			confirmOpen = false;
		};
		confirmOpen = true;
	}

	async function testExisting(id: number) {
		try {
			const result = await testAgentServerById(id);
			toast[result.success ? 'success' : 'error'](result.message);
		} catch {
			toast.error('접속 테스트 실패');
		}
	}
</script>

<ConfirmDialog
	bind:open={confirmOpen}
	title="삭제 확인"
	description={confirmDesc}
	confirmLabel="삭제"
	onConfirm={confirmAction}
	onCancel={() => { confirmOpen = false; }}
/>

<Sheet.Root bind:open>
	<Sheet.Content side="right" class="w-[480px] flex flex-col max-h-[100dvh]">
		<Sheet.Header>
			<Sheet.Title class="text-sm">Agent 서버 관리</Sheet.Title>
			<Sheet.Description class="text-xs">gRPC Agent 서버를 추가하고 관리합니다</Sheet.Description>
		</Sheet.Header>

		<div class="flex-1 overflow-y-auto py-3 space-y-3">
			<button
				onclick={startCreate}
				class="inline-flex items-center gap-1 rounded-md border px-2.5 py-1 text-[10px] hover:bg-muted transition-colors"
			>
				<PlusIcon class="size-3" /> 서버 추가
			</button>

			{#if showForm}
				<div class="border rounded-md p-3 space-y-2 bg-muted/30">
					<div class="grid grid-cols-3 gap-2">
						<div>
							<label class="text-[10px] font-medium">Name</label>
							<input bind:value={form.name} class="w-full border rounded px-2 py-1 text-xs bg-background" placeholder="agent-1" />
						</div>
						<div>
							<label class="text-[10px] font-medium">Host</label>
							<input bind:value={form.host} class="w-full border rounded px-2 py-1 text-xs bg-background" placeholder="192.168.1.100" />
						</div>
						<div>
							<label class="text-[10px] font-medium">Port</label>
							<input type="number" bind:value={form.port} class="w-full border rounded px-2 py-1 text-xs bg-background" />
						</div>
					</div>
					<div>
						<label class="text-[10px] font-medium">Description</label>
						<input bind:value={form.description} class="w-full border rounded px-2 py-1 text-xs bg-background" />
					</div>
					<label class="flex items-center gap-1 text-[10px]">
						<input type="checkbox" bind:checked={form.enabled} class="size-3" /> Enabled
					</label>

					{#if testResult}
						<div class="flex items-center gap-1 text-[10px] {testResult.success ? 'text-green-600' : 'text-red-600'}">
							{#if testResult.success}<CheckIcon class="size-3" />{:else}<XCircleIcon class="size-3" />{/if}
							{testResult.message}
						</div>
					{/if}

					<div class="flex gap-1">
						<button onclick={testBeforeSave} disabled={testing || !form.host} class="inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[10px] hover:bg-muted disabled:opacity-50">
							{#if testing}<LoaderIcon class="size-3 animate-spin" />{:else}<PlugIcon class="size-3" />{/if}
							접속 테스트
						</button>
						<button onclick={save} disabled={saving} class="inline-flex items-center gap-1 rounded-md bg-blue-600 text-white px-2 py-0.5 text-[10px] hover:bg-blue-700 disabled:opacity-50">
							{#if saving}<LoaderIcon class="size-3 animate-spin" />{:else}<SaveIcon class="size-3" />{/if}
							저장
						</button>
						<button onclick={cancelForm} class="inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-[10px] hover:bg-muted">
							<XIcon class="size-3" /> 취소
						</button>
					</div>
				</div>
			{/if}

			<Table.Root>
				<Table.Header>
					<Table.Row class="text-[10px]">
						<Table.Head>Name</Table.Head>
						<Table.Head>Host:Port</Table.Head>
						<Table.Head>Enabled</Table.Head>
						<Table.Head class="w-20">Actions</Table.Head>
					</Table.Row>
				</Table.Header>
				<Table.Body>
					{#each servers as s (s.id)}
						<Table.Row class="text-xs">
							<Table.Cell class="font-medium">{s.name}</Table.Cell>
							<Table.Cell class="font-mono text-[10px]">{s.host}:{s.port}</Table.Cell>
							<Table.Cell>
								<span class="size-1.5 rounded-full inline-block {s.enabled ? 'bg-green-500' : 'bg-gray-400'}"></span>
							</Table.Cell>
							<Table.Cell>
								<div class="flex gap-1">
									<button onclick={() => testExisting(s.id)} class="p-0.5 rounded hover:bg-muted" title="테스트"><PlugIcon class="size-3" /></button>
									<button onclick={() => startEdit(s)} class="p-0.5 rounded hover:bg-muted"><PencilIcon class="size-3" /></button>
									<button onclick={() => requestDelete(s.id)} class="p-0.5 rounded hover:bg-muted text-red-600"><TrashIcon class="size-3" /></button>
								</div>
							</Table.Cell>
						</Table.Row>
					{/each}
				</Table.Body>
			</Table.Root>
		</div>
	</Sheet.Content>
</Sheet.Root>
