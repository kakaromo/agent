<script lang="ts">
	import { toast } from 'svelte-sonner';
	import {
		fetchScenarioTemplates,
		createScenarioTemplate,
		updateScenarioTemplate,
		deleteScenarioTemplate,
		duplicateScenarioTemplate,
		runScenario,
		type ScenarioTemplate
	} from '$lib/api/agent.js';
	import type { ActiveJob } from '../types.js';
	import type { ScenarioNode, ScenarioEdge } from './types.js';
	import { canvasToProto, validateGraph } from './serializer.js';
	import PlayIcon from '@lucide/svelte/icons/play';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import SaveIcon from '@lucide/svelte/icons/save';
	import LayoutGridIcon from '@lucide/svelte/icons/layout-grid';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';

	interface Props {
		nodes: ScenarioNode[];
		edges: ScenarioEdge[];
		serverId: number | null;
		selectedDevices: Set<string>;
		serverName: string;
		onJobStarted: (job: Omit<ActiveJob, 'events' | 'state' | 'eventSource'>) => void;
		onLoadTemplate: (t: ScenarioTemplate) => void;
		onClearCanvas: () => void;
		onAutoLayout: () => void;
		loopMembers: Map<string, Set<string>>;
	}

	let { nodes, edges, serverId, selectedDevices, serverName, onJobStarted, onLoadTemplate, onClearCanvas, onAutoLayout, loopMembers }: Props = $props();

	let templates = $state<ScenarioTemplate[]>([]);
	let selectedTemplateId = $state<number | null>(null);
	let scenarioName = $state('');
	let repeat = $state(1);
	let busyPolicy = $state('reject');
	let running = $state(false);

	let showSave = $state(false);
	let saveName = $state('');
	let confirmOpen = $state(false);
	let confirmDesc = $state('');
	let confirmAction = $state<() => Promise<void>>(async () => {});

	let deviceCount = $derived(selectedDevices.size);

	loadTemplates();

	async function loadTemplates() {
		try { templates = await fetchScenarioTemplates(); }
		catch { templates = []; }
	}

	function handleTemplateSelect(e: Event) {
		const val = (e.target as HTMLSelectElement).value;
		if (val) {
			const t = templates.find(t => t.id === Number(val));
			if (t) { selectedTemplateId = t.id; scenarioName = t.name; repeat = t.repeatCount; onLoadTemplate(t); }
		} else {
			selectedTemplateId = null;
			scenarioName = '';
			repeat = 1;
			onClearCanvas();
		}
	}

	async function handleRun() {
		if (serverId == null || deviceCount === 0) return;
		const validation = validateGraph(nodes, edges);
		if (!validation.valid) { toast.error(validation.errors[0]); return; }

		const result = canvasToProto(nodes, edges, loopMembers);
		if (result.steps.length === 0) { toast.error('Step이 없습니다'); return; }

		running = true;
		try {
			const payload: Record<string, unknown> = {
				deviceIds: [...selectedDevices],
				scenarioName: scenarioName || undefined,
				steps: result.steps,
				loops: result.loops.length > 0 ? result.loops : undefined,
				repeat,
				busyPolicy
			};
			if (result.hasBranching) {
				payload.hasBranching = true;
				payload.edges = result.edges.map(e => ({
					fromStep: e.fromStep, toStep: e.toStep, label: e.label
				}));
			}
			const res = await runScenario(serverId, payload as any);
			toast.success(`Scenario 시작: ${res.jobId.slice(0, 8)}`);
			onJobStarted({
				jobId: res.jobId, serverId, serverName, type: 'scenario',
				jobName: scenarioName || undefined,
				deviceIds: [...selectedDevices], createdAt: Date.now()
			});
		} catch { toast.error('Scenario 시작 실패'); }
		finally { running = false; }
	}

	async function handleSave() {
		const name = saveName.trim() || scenarioName.trim();
		if (!name) { toast.error('이름을 입력해주세요'); return; }
		const protoResult = canvasToProto(nodes, edges, loopMembers);
		console.log('[Save] nodes:', nodes.map(n => ({ id: n.id, type: n.type, parentId: (n as any).parentId })));
		console.log('[Save] loops:', protoResult.loops);
		console.log('[Save] steps:', protoResult.steps.length);
		try {
			const data = {
				name,
				description: '',
				repeatCount: repeat,
				stepsJson: JSON.stringify(protoResult.steps),
				loopsJson: protoResult.loops.length > 0 ? JSON.stringify(protoResult.loops) : undefined
			};
			if (selectedTemplateId != null) {
				await updateScenarioTemplate(selectedTemplateId, data);
				toast.success('템플릿 수정됨');
			} else {
				await createScenarioTemplate(data);
				toast.success('템플릿 저장됨');
			}
			showSave = false;
			saveName = '';
			await loadTemplates();
		} catch { toast.error('저장 실패'); }
	}

	function requestDelete(id: number) {
		confirmDesc = '이 시나리오 템플릿을 삭제하시겠습니까?';
		confirmAction = async () => {
			await deleteScenarioTemplate(id);
			if (selectedTemplateId === id) { selectedTemplateId = null; onClearCanvas(); }
			toast.success('삭제됨');
			await loadTemplates();
			confirmOpen = false;
		};
		confirmOpen = true;
	}
</script>

<ConfirmDialog bind:open={confirmOpen} title="삭제 확인" description={confirmDesc} confirmLabel="삭제" onConfirm={confirmAction} onCancel={() => { confirmOpen = false; }} />

<div class="flex items-center gap-2 px-2 py-1.5 border-b bg-background/80 backdrop-blur-sm">
	<!-- Template -->
	<select value={selectedTemplateId ?? ''} onchange={handleTemplateSelect} class="border rounded px-2 py-1 text-[10px] bg-background w-40">
		<option value="">새 시나리오</option>
		{#each templates as t (t.id)}
			<option value={t.id}>{t.name}</option>
		{/each}
	</select>
	{#if selectedTemplateId != null}
		<button onclick={() => duplicateScenarioTemplate(selectedTemplateId!).then(() => { loadTemplates(); toast.success('복제됨'); })} class="p-1 rounded hover:bg-muted" title="복제"><CopyIcon class="size-3" /></button>
		<button onclick={() => requestDelete(selectedTemplateId!)} class="p-1 rounded hover:bg-muted text-red-500" title="삭제"><TrashIcon class="size-3" /></button>
	{/if}

	<div class="border-l h-5"></div>

	<!-- Name + Repeat + Policy -->
	<input bind:value={scenarioName} class="border rounded px-2 py-1 text-[10px] bg-background w-28" placeholder="이름" />
	<div class="flex items-center gap-0.5 text-[9px] text-muted-foreground">
		<span>x</span>
		<input type="number" bind:value={repeat} min="1" class="w-10 border rounded px-1 py-1 text-[10px] bg-background text-center" />
	</div>
	<select bind:value={busyPolicy} class="border rounded px-1.5 py-1 text-[9px] bg-background">
		<option value="reject">Reject</option>
		<option value="wait">Wait</option>
		<option value="force">Force</option>
	</select>

	<button onclick={onAutoLayout} class="inline-flex items-center gap-1 rounded border px-1.5 py-1 text-[9px] hover:bg-muted" title="자동 정렬">
		<LayoutGridIcon class="size-3" /> 정렬
	</button>

	<div class="flex-1"></div>

	<!-- Device count -->
	{#if deviceCount > 0}
		<span class="text-[9px] text-muted-foreground">{deviceCount}개 디바이스</span>
	{:else}
		<span class="text-[9px] text-orange-600">디바이스 선택 필요</span>
	{/if}

	<!-- Save -->
	{#if showSave}
		<div class="flex items-center gap-1">
			<input bind:value={saveName} class="border rounded px-1.5 py-1 text-[9px] bg-background w-24" placeholder="이름"
				onkeydown={(e) => { if (e.key === 'Enter') handleSave(); }} />
			<button onclick={handleSave} class="p-1 rounded bg-blue-600 text-white hover:bg-blue-700"><SaveIcon class="size-3" /></button>
			<button onclick={() => { showSave = false; }} class="p-1 rounded border hover:bg-muted text-[9px]">취소</button>
		</div>
	{:else}
		<button onclick={() => { showSave = true; saveName = scenarioName; }} class="inline-flex items-center gap-1 rounded border px-2 py-1 text-[9px] hover:bg-muted">
			<SaveIcon class="size-3" /> 저장
		</button>
	{/if}

	<!-- Run -->
	<button
		onclick={handleRun}
		disabled={running || deviceCount === 0 || serverId == null}
		class="inline-flex items-center gap-1 rounded-md bg-blue-600 text-white px-3 py-1 text-[10px] font-medium hover:bg-blue-700 disabled:opacity-50"
	>
		{#if running}<LoaderIcon class="size-3 animate-spin" />{:else}<PlayIcon class="size-3" />{/if}
		실행
	</button>
</div>
