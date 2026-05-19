<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { auth } from '$lib/stores/auth.svelte.js';
	import { fetchSchedules, createSchedule, updateSchedule, deleteSchedule, triggerSchedule, toggleScheduleEnabled, type ScheduledJobRecord, type AgentServer } from '$lib/api/agent.js';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import type { ActiveJob } from './types.js';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import PlayIcon from '@lucide/svelte/icons/play';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import CalendarIcon from '@lucide/svelte/icons/calendar';
	import ClockIcon from '@lucide/svelte/icons/clock';
	import CheckCircleIcon from '@lucide/svelte/icons/check-circle';
	import XCircleIcon from '@lucide/svelte/icons/x-circle';

	interface Props {
		serverId: number | null;
		enabledServers: AgentServer[];
		onJobStarted: (job: Omit<ActiveJob, 'events' | 'state' | 'eventSource'>) => void;
	}

	let { serverId, enabledServers, onJobStarted }: Props = $props();

	let schedules = $state<ScheduledJobRecord[]>([]);
	let loading = $state(false);
	let triggering = $state<number | null>(null);

	// Confirm dialog
	let confirmOpen = $state(false);
	let confirmDesc = $state('');
	let confirmAction = $state<() => Promise<void>>(async () => {});

	// Edit dialog
	let editOpen = $state(false);
	let editingId = $state<number | null>(null);
	let form = $state({
		name: '',
		description: '',
		type: 'benchmark' as string,
		serverId: 0 as number,
		deviceIds: '' as string,
		config: '' as string,
		cronExpression: '0 2 * * *',
		busyPolicy: 'reject',
		retryCount: 0,
		retryDelaySeconds: 60,
		notifyOnFailure: false,
		notifyOnSuccess: false,
		notifyWebhookUrl: ''
	});
	let saving = $state(false);

	const cronPresets = [
		{ label: '매시간', value: '0 * * * *' },
		{ label: '매일 새벽 2시', value: '0 2 * * *' },
		{ label: '매일 오전 9시', value: '0 9 * * *' },
		{ label: '매주 월요일 9시', value: '0 9 * * 1' },
		{ label: '매월 1일 새벽 2시', value: '0 2 1 * *' }
	];

	$effect(() => {
		loadSchedules();
	});

	async function loadSchedules() {
		loading = true;
		try { schedules = await fetchSchedules(); }
		catch { schedules = []; }
		finally { loading = false; }
	}

	function openCreate() {
		editingId = null;
		form = {
			name: '', description: '', type: 'benchmark',
			serverId: serverId ?? enabledServers[0]?.id ?? 0,
			deviceIds: '[]', config: '{"tool":"FIO","params":{"bs":"4k","rw":"randread","size":"1G","runtime":"60"}}',
			cronExpression: '0 2 * * *', busyPolicy: 'reject',
			retryCount: 0, retryDelaySeconds: 60,
			notifyOnFailure: false, notifyOnSuccess: false, notifyWebhookUrl: ''
		};
		editOpen = true;
	}

	function openEdit(s: ScheduledJobRecord) {
		editingId = s.id;
		form = {
			name: s.name, description: s.description ?? '', type: s.type,
			serverId: s.serverId, deviceIds: s.deviceIds, config: s.config,
			cronExpression: s.cronExpression, busyPolicy: s.busyPolicy,
			retryCount: s.retryCount, retryDelaySeconds: s.retryDelaySeconds,
			notifyOnFailure: s.notifyOnFailure, notifyOnSuccess: s.notifyOnSuccess,
			notifyWebhookUrl: s.notifyWebhookUrl ?? ''
		};
		editOpen = true;
	}

	async function handleSave() {
		if (!form.name.trim()) { toast.error('이름을 입력해주세요'); return; }
		saving = true;
		try {
			const data = { ...form };
			if (editingId != null) {
				await updateSchedule(editingId, data);
				toast.success('스케줄이 수정되었습니다');
			} else {
				await createSchedule(data);
				toast.success('스케줄이 생성되었습니다');
			}
			editOpen = false;
			await loadSchedules();
		} catch { toast.error('저장 실패'); }
		finally { saving = false; }
	}

	function requestDelete(s: ScheduledJobRecord) {
		confirmDesc = `"${s.name}" 스케줄을 삭제하시겠습니까?`;
		confirmAction = async () => {
			await deleteSchedule(s.id);
			toast.success('삭제되었습니다');
			await loadSchedules();
			confirmOpen = false;
		};
		confirmOpen = true;
	}

	async function handleTrigger(s: ScheduledJobRecord) {
		triggering = s.id;
		try {
			const res = await triggerSchedule(s.id);
			toast.success(`실행 시작: ${res.jobId.slice(0, 8)}`);
			const server = enabledServers.find(sv => sv.id === s.serverId);
			onJobStarted({
				jobId: res.jobId,
				serverId: s.serverId,
				serverName: server?.name ?? String(s.serverId),
				type: s.type as 'benchmark' | 'scenario',
				jobName: s.name,
				deviceIds: JSON.parse(s.deviceIds),
				createdAt: Date.now()
			});
			await loadSchedules();
		} catch { toast.error('실행 실패'); }
		finally { triggering = null; }
	}

	async function handleToggle(s: ScheduledJobRecord) {
		try {
			await toggleScheduleEnabled(s.id);
			await loadSchedules();
		} catch { toast.error('상태 변경 실패'); }
	}

	function cronDesc(expr: string): string {
		const preset = cronPresets.find(p => p.value === expr);
		return preset?.label ?? expr;
	}

	function formatTime(ts: string | null | undefined): string {
		if (!ts) return '-';
		return new Date(ts).toLocaleString('ko-KR', {
			month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit'
		});
	}
</script>

<ConfirmDialog bind:open={confirmOpen} title="삭제 확인" description={confirmDesc} confirmLabel="삭제" onConfirm={confirmAction} onCancel={() => { confirmOpen = false; }} />

<div class="max-w-2xl space-y-4 p-1">
	<div class="flex items-center justify-between">
		<h2 class="text-sm font-semibold">Schedule</h2>
		{#if auth.isAdmin}
			<button onclick={openCreate} class="inline-flex items-center gap-1 rounded-md border px-2.5 py-1 text-[10px] hover:bg-muted">
				<PlusIcon class="size-3" /> 새 스케줄
			</button>
		{/if}
	</div>

	{#if loading}
		<div class="flex items-center justify-center py-12">
			<LoaderIcon class="size-5 animate-spin text-muted-foreground" />
		</div>
	{:else if schedules.length === 0}
		<div class="flex flex-col items-center justify-center py-12 text-muted-foreground">
			<CalendarIcon class="size-8 mb-2 opacity-30" />
			<p class="text-xs">등록된 스케줄이 없습니다</p>
			<p class="text-[10px] mt-1">정기적으로 벤치마크나 시나리오를 실행할 수 있습니다</p>
		</div>
	{:else}
		<div class="space-y-2">
			{#each schedules as s (s.id)}
				<div class="border rounded-md p-3 space-y-2 {s.enabled ? '' : 'opacity-50'}">
					<div class="flex items-center gap-2">
						<!-- Toggle (admin 만) -->
						{#if auth.isAdmin}
							<button
								onclick={() => handleToggle(s)}
								class="size-4 rounded-full border-2 shrink-0 transition-colors
									{s.enabled ? 'bg-green-500 border-green-500' : 'border-gray-300'}"
								title={s.enabled ? '비활성화' : '활성화'}
							></button>
						{:else}
							<span
								class="size-4 rounded-full border-2 shrink-0
									{s.enabled ? 'bg-green-500 border-green-500' : 'border-gray-300'}"
								title={s.enabled ? '활성' : '비활성'}
							></span>
						{/if}

						<!-- Name + type -->
						<span class="text-xs font-medium flex-1">{s.name}</span>
						<span class="px-1.5 py-0.5 rounded text-[9px] {s.type === 'benchmark' ? 'bg-blue-100 text-blue-700' : 'bg-purple-100 text-purple-700'}">
							{s.type}
						</span>

						<!-- Actions: trigger 는 모두 가능, 편집/삭제 는 admin 만 -->
						<button onclick={() => handleTrigger(s)} disabled={triggering === s.id} class="p-1 rounded hover:bg-muted" title="즉시 실행">
							{#if triggering === s.id}
								<LoaderIcon class="size-3 animate-spin" />
							{:else}
								<PlayIcon class="size-3 text-blue-600" />
							{/if}
						</button>
						{#if auth.isAdmin}
							<button onclick={() => openEdit(s)} class="p-1 rounded hover:bg-muted" title="편집">
								<PencilIcon class="size-3 text-muted-foreground" />
							</button>
							<button onclick={() => requestDelete(s)} class="p-1 rounded hover:bg-muted" title="삭제">
								<TrashIcon class="size-3 text-red-500" />
							</button>
						{/if}
					</div>

					<div class="flex items-center gap-3 text-[10px] text-muted-foreground">
						<span class="inline-flex items-center gap-0.5"><ClockIcon class="size-2.5" /> {cronDesc(s.cronExpression)}</span>
						{#if s.nextRunAt}
							<span>다음 실행: {formatTime(s.nextRunAt)}</span>
						{/if}
						{#if s.lastRunAt}
							<span class="inline-flex items-center gap-0.5">
								{#if s.lastRunStatus === 'success'}
									<CheckCircleIcon class="size-2.5 text-green-600" />
								{:else if s.lastRunStatus === 'failed'}
									<XCircleIcon class="size-2.5 text-red-600" />
								{/if}
								마지막: {formatTime(s.lastRunAt)}
							</span>
						{/if}
						{#if s.retryCount > 0}
							<span>재시도 {s.retryCount}회</span>
						{/if}
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>

<!-- Edit/Create Dialog -->
<Dialog.Root bind:open={editOpen}>
	<Dialog.Content class="max-w-md max-h-[80vh] flex flex-col">
		<Dialog.Header>
			<Dialog.Title class="text-sm">{editingId != null ? '스케줄 편집' : '새 스케줄'}</Dialog.Title>
		</Dialog.Header>

		<div class="flex-1 overflow-y-auto space-y-3 py-2">
			<div class="space-y-1">
				<label class="text-[10px] font-medium text-muted-foreground uppercase">이름</label>
				<input bind:value={form.name} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background" placeholder="Nightly Benchmark" />
			</div>

			<div class="grid grid-cols-2 gap-2">
				<div class="space-y-1">
					<label class="text-[10px] font-medium text-muted-foreground uppercase">타입</label>
					<select bind:value={form.type} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background">
						<option value="benchmark">Benchmark</option>
						<option value="scenario">Scenario</option>
					</select>
				</div>
				<div class="space-y-1">
					<label class="text-[10px] font-medium text-muted-foreground uppercase">서버</label>
					<select bind:value={form.serverId} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background">
						{#each enabledServers as sv}
							<option value={sv.id}>{sv.name}</option>
						{/each}
					</select>
				</div>
			</div>

			<div class="space-y-1">
				<label class="text-[10px] font-medium text-muted-foreground uppercase">Cron 표현식</label>
				<div class="flex items-center gap-2">
					<input bind:value={form.cronExpression} class="flex-1 border rounded px-2.5 py-1.5 text-xs bg-background font-mono" placeholder="0 2 * * *" />
					<select onchange={(e) => { if ((e.target as HTMLSelectElement).value) form.cronExpression = (e.target as HTMLSelectElement).value; }} class="border rounded px-2 py-1.5 text-[10px] bg-background">
						<option value="">프리셋...</option>
						{#each cronPresets as p}
							<option value={p.value}>{p.label}</option>
						{/each}
					</select>
				</div>
			</div>

			<div class="space-y-1">
				<label class="text-[10px] font-medium text-muted-foreground uppercase">Device IDs (JSON)</label>
				<input bind:value={form.deviceIds} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background font-mono" placeholder={'["device1"]'} />
			</div>

			<div class="space-y-1">
				<label class="text-[10px] font-medium text-muted-foreground uppercase">Config (JSON)</label>
				<textarea bind:value={form.config} class="w-full border rounded px-2 py-1 text-[10px] bg-background font-mono h-20 resize-y" placeholder={'{"tool":"FIO","params":{"bs":"4k",...}}'}></textarea>
			</div>

			<div class="grid grid-cols-3 gap-2">
				<div class="space-y-1">
					<label class="text-[10px] font-medium text-muted-foreground uppercase">Busy Policy</label>
					<select bind:value={form.busyPolicy} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background">
						<option value="reject">Reject</option>
						<option value="wait">Wait</option>
						<option value="force">Force</option>
					</select>
				</div>
				<div class="space-y-1">
					<label class="text-[10px] font-medium text-muted-foreground uppercase">재시도</label>
					<input type="number" bind:value={form.retryCount} min="0" class="w-full border rounded px-2.5 py-1.5 text-xs bg-background" />
				</div>
				<div class="space-y-1">
					<label class="text-[10px] font-medium text-muted-foreground uppercase">간격 (초)</label>
					<input type="number" bind:value={form.retryDelaySeconds} min="1" class="w-full border rounded px-2.5 py-1.5 text-xs bg-background" />
				</div>
			</div>

			<div class="space-y-1.5">
				<label class="text-[10px] font-medium text-muted-foreground uppercase">알림</label>
				<div class="flex items-center gap-4 text-[10px]">
					<label class="flex items-center gap-1 cursor-pointer">
						<input type="checkbox" bind:checked={form.notifyOnFailure} class="size-3" /> 실패 시
					</label>
					<label class="flex items-center gap-1 cursor-pointer">
						<input type="checkbox" bind:checked={form.notifyOnSuccess} class="size-3" /> 성공 시
					</label>
				</div>
				{#if form.notifyOnFailure || form.notifyOnSuccess}
					<input bind:value={form.notifyWebhookUrl} class="w-full border rounded px-2.5 py-1.5 text-[10px] bg-background font-mono" placeholder="https://hooks.slack.com/services/..." />
				{/if}
			</div>
		</div>

		<Dialog.Footer class="gap-2">
			<button onclick={() => editOpen = false} class="rounded-md border px-3 py-1.5 text-xs hover:bg-muted">취소</button>
			<button onclick={handleSave} disabled={saving} class="rounded-md bg-blue-600 text-white px-3 py-1.5 text-xs hover:bg-blue-700 disabled:opacity-50">
				{#if saving}<LoaderIcon class="size-3 animate-spin inline mr-1" />{/if}
				{editingId != null ? '수정' : '생성'}
			</button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
