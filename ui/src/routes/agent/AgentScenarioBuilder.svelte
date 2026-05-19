<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { sectionLabel, captionMuted } from '$lib/styles/common.js';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import AgentStepEditDialog, { type StepForm } from './AgentStepEditDialog.svelte';
	import {
		runScenario,
		fetchScenarioTemplates,
		createScenarioTemplate,
		updateScenarioTemplate,
		deleteScenarioTemplate,
		duplicateScenarioTemplate,
		type ScenarioStep,
		type ScenarioLoop,
		type ScenarioTemplate
	} from '$lib/api/agent.js';
	import { getBasicOptions, getDefaultParams, mergeParams } from './benchmarkOptions.js';
	import type { ActiveJob } from './types.js';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
	import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
	import PlayIcon from '@lucide/svelte/icons/play';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import SaveIcon from '@lucide/svelte/icons/save';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import ScanSearchIcon from '@lucide/svelte/icons/scan-search';
	import ActivityIcon from '@lucide/svelte/icons/activity';
	import RepeatIcon from '@lucide/svelte/icons/repeat';

	interface Props {
		serverId: number | null;
		selectedDevices: Set<string>;
		serverName: string;
		onJobStarted: (job: Omit<ActiveJob, 'events' | 'state' | 'eventSource'>) => void;
	}

	let { serverId, selectedDevices, serverName, onJobStarted }: Props = $props();

	// Templates
	let templates = $state<ScenarioTemplate[]>([]);
	let selectedTemplateId = $state<number | null>(null);

	let scenarioName = $state('');
	let repeat = $state(1);
	let busyPolicy = $state('reject');
	let running = $state(false);

	let steps = $state<StepForm[]>([
		{ type: 'benchmark', tool: 'FIO', formParams: getDefaultParams('FIO'), extraText: '', showAdvanced: false, useFileFromStep: null, cleanupMode: 'all', cleanupSteps: new Set(), cleanupPath: '', traceEnabled: false, traceType: 'ufs' }
	]);

	interface LoopForm { startStep: number; endStep: number; count: number; }
	let loops = $state<LoopForm[]>([]);

	let deviceCount = $derived(selectedDevices.size);

	// Save dialog
	let showSave = $state(false);
	let saveName = $state('');
	let confirmOpen = $state(false);
	let confirmDesc = $state('');
	let confirmAction = $state<() => Promise<void>>(async () => {});

	// Step edit dialog
	let editDialogOpen = $state(false);
	let editingStepIndex = $state(-1);
	let editingStep = $state<StepForm | null>(null);

	loadTemplates();

	async function loadTemplates() {
		try { templates = await fetchScenarioTemplates(); } catch { templates = []; }
	}

	function applyTemplate(t: ScenarioTemplate) {
		selectedTemplateId = t.id;
		scenarioName = t.name;
		repeat = t.repeatCount;
		try {
			const parsedSteps = JSON.parse(t.stepsJson) as { type: string; tool?: string; params?: Record<string, string> }[];
			steps = parsedSteps.map(s => {
				const tool = s.tool ?? 'FIO';
				const opts = getBasicOptions(tool);
				const formP: Record<string, string> = {};
				const extraLines: string[] = [];
				if (s.params) {
					for (const [k, v] of Object.entries(s.params)) {
						if (opts.some(o => o.key === k)) formP[k] = v;
						else extraLines.push(`${k}=${v}`);
					}
				}
				for (const opt of opts) {
					if (!(opt.key in formP)) formP[opt.key] = opt.defaultValue;
				}
				const useFile = s.params?.use_file_from_step != null ? Number(s.params.use_file_from_step) : null;

				let cleanupMode: 'all' | 'steps' | 'path' = 'all';
				let cleanupSteps = new Set<number>();
				let cleanupPath = '';
				if (s.type === 'cleanup' && s.params) {
					if (s.params.delete_files_from_steps) {
						cleanupMode = 'steps';
						cleanupSteps = new Set(s.params.delete_files_from_steps.split(',').map(Number));
					} else if (s.params.path) {
						cleanupMode = 'path';
						cleanupPath = s.params.path;
					}
				}

				return {
					type: s.type, tool, formParams: formP,
					extraText: extraLines.filter(l => !l.startsWith('use_file_from_step=') && !l.startsWith('delete_files_from_steps=') && !l.startsWith('path=') && !l.startsWith('trace=') && !l.startsWith('trace_type=')).join('\n'),
					showAdvanced: false, useFileFromStep: useFile,
					cleanupMode, cleanupSteps, cleanupPath,
					traceEnabled: s.params?.trace === 'on',
					traceType: s.params?.trace_type ?? 'ufs'
				};
			});
			if (t.loopsJson) {
				loops = JSON.parse(t.loopsJson);
			} else {
				loops = [];
			}
		} catch {
			steps = [{ type: 'benchmark', tool: 'FIO', formParams: getDefaultParams('FIO'), extraText: '', showAdvanced: false, useFileFromStep: null, cleanupMode: 'all', cleanupSteps: new Set(), cleanupPath: '', traceEnabled: false, traceType: 'ufs' }];
			loops = [];
		}
	}

	function clearTemplate() {
		selectedTemplateId = null;
		scenarioName = '';
		repeat = 1;
		steps = [{ type: 'benchmark', tool: 'FIO', formParams: getDefaultParams('FIO'), extraText: '', showAdvanced: false, useFileFromStep: null, cleanupMode: 'all', cleanupSteps: new Set(), cleanupPath: '', traceEnabled: false, traceType: 'ufs' }];
		loops = [];
	}

	function buildStepParams(s: StepForm): Record<string, string> {
		if (s.type === 'cleanup') {
			const params: Record<string, string> = {};
			if (s.cleanupMode === 'steps' && s.cleanupSteps.size > 0) {
				params.delete_files_from_steps = [...s.cleanupSteps].sort((a, b) => a - b).join(',');
			} else if (s.cleanupMode === 'path' && s.cleanupPath.trim()) {
				params.path = s.cleanupPath.trim();
			}
			return params;
		}
		const params = mergeParams(s.formParams, s.extraText);
		if (s.type === 'benchmark' && s.useFileFromStep != null) {
			params.use_file_from_step = String(s.useFileFromStep);
		}
		if ((s.type === 'benchmark' || s.type === 'shell') && s.traceEnabled) {
			params.trace = 'on';
			params.trace_type = s.traceType;
		}
		return params;
	}

	function buildStepsJson(): string {
		return JSON.stringify(steps.map(s => ({
			type: s.type,
			tool: s.type === 'benchmark' ? s.tool : undefined,
			params: buildStepParams(s)
		})));
	}

	function buildLoopsJson(): string | undefined {
		return loops.length > 0 ? JSON.stringify(loops) : undefined;
	}

	async function saveTemplate() {
		const name = saveName.trim() || scenarioName.trim();
		if (!name) { toast.error('이름을 입력해주세요'); return; }
		try {
			const data = {
				name,
				description: '',
				repeatCount: repeat,
				stepsJson: buildStepsJson(),
				loopsJson: buildLoopsJson()
			};
			if (selectedTemplateId != null) {
				await updateScenarioTemplate(selectedTemplateId, data);
				toast.success('템플릿이 수정되었습니다');
			} else {
				await createScenarioTemplate(data);
				toast.success('템플릿이 저장되었습니다');
			}
			showSave = false;
			saveName = '';
			await loadTemplates();
		} catch { toast.error('저장 실패'); }
	}

	function requestDeleteTemplate(id: number) {
		confirmDesc = '이 시나리오 템플릿을 삭제하시겠습니까?';
		confirmAction = async () => {
			await deleteScenarioTemplate(id);
			if (selectedTemplateId === id) clearTemplate();
			toast.success('삭제되었습니다');
			await loadTemplates();
			confirmOpen = false;
		};
		confirmOpen = true;
	}

	async function handleDuplicate(id: number) {
		try {
			await duplicateScenarioTemplate(id);
			toast.success('복제되었습니다');
			await loadTemplates();
		} catch { toast.error('복제 실패'); }
	}

	// Step management
	function addStep() {
		steps = [...steps, { type: 'benchmark', tool: 'FIO', formParams: getDefaultParams('FIO'), extraText: '', showAdvanced: false, useFileFromStep: null, cleanupMode: 'all', cleanupSteps: new Set(), cleanupPath: '', traceEnabled: false, traceType: 'ufs' }];
	}
	function removeStep(i: number) { steps = steps.filter((_, idx) => idx !== i); }
	function moveStep(i: number, dir: -1 | 1) {
		const j = i + dir;
		if (j < 0 || j >= steps.length) return;
		const arr = [...steps]; [arr[i], arr[j]] = [arr[j], arr[i]]; steps = arr;
	}
	function addLoop() { loops = [...loops, { startStep: 0, endStep: steps.length - 1, count: 10 }]; }
	function removeLoop(i: number) { loops = loops.filter((_, idx) => idx !== i); }

	// Loop 선택 모드: step 체크박스로 선택 → loop로 감싸기
	let selectedSteps = $state<Set<number>>(new Set());

	function toggleStepSelect(i: number) {
		const s = new Set(selectedSteps);
		if (s.has(i)) s.delete(i); else s.add(i);
		selectedSteps = s;
	}

	function wrapSelectedAsLoop() {
		if (selectedSteps.size < 1) return;
		const sorted = [...selectedSteps].sort((a, b) => a - b);
		loops = [...loops, { startStep: sorted[0], endStep: sorted[sorted.length - 1], count: 10 }];
		selectedSteps = new Set();
	}

	function getLoopForStep(stepIdx: number): { loopIdx: number; loop: LoopForm } | null {
		for (let li = 0; li < loops.length; li++) {
			if (stepIdx >= loops[li].startStep && stepIdx <= loops[li].endStep) {
				return { loopIdx: li, loop: loops[li] };
			}
		}
		return null;
	}

	function getLoopColor(loopIdx: number): string {
		const colors = ['border-l-blue-400', 'border-l-purple-400', 'border-l-amber-400', 'border-l-emerald-400'];
		return colors[loopIdx % colors.length];
	}

	function getLoopBg(loopIdx: number): string {
		const colors = ['bg-blue-50/50', 'bg-purple-50/50', 'bg-amber-50/50', 'bg-emerald-50/50'];
		return colors[loopIdx % colors.length];
	}

	function openStepEditor(i: number) {
		editingStepIndex = i;
		editingStep = steps[i];
		editDialogOpen = true;
	}

	function handleStepSave(updated: StepForm) {
		steps[editingStepIndex] = updated;
		steps = [...steps];
		editDialogOpen = false;
		editingStep = null;
	}

	function handleStepCancel() {
		editDialogOpen = false;
		editingStep = null;
	}

	function stepSummary(s: StepForm): string {
		switch (s.type) {
			case 'benchmark': return `${s.tool} · ${s.formParams.rw ?? ''} · ${s.formParams.bs ?? ''}`;
			case 'shell': return s.extraText.slice(0, 30) || 'shell command';
			case 'cleanup': return s.cleanupMode === 'all' ? '전체 삭제' : s.cleanupMode === 'steps' ? `Step ${[...s.cleanupSteps].join(',')} 파일 삭제` : s.cleanupPath || '경로 삭제';
			case 'sleep': return `${s.extraText.replace('seconds=', '')}s` || 'sleep';
			case 'trace_start': return `${s.formParams.trace_type ?? 'ufs'} trace`;
			case 'trace_stop': return 'stop';
			default: return s.type;
		}
	}

	function stepTypeColor(type: string): string {
		switch (type) {
			case 'benchmark': return 'bg-blue-100 text-blue-700';
			case 'shell': return 'bg-gray-100 text-gray-700';
			case 'cleanup': return 'bg-orange-100 text-orange-700';
			case 'sleep': return 'bg-yellow-100 text-yellow-700';
			case 'trace_start': case 'trace_stop': return 'bg-emerald-100 text-emerald-700';
			default: return 'bg-gray-100 text-gray-700';
		}
	}

	async function handleRun() {
		if (serverId == null || deviceCount === 0 || steps.length === 0) return;
		running = true;
		const scenarioSteps: ScenarioStep[] = steps.map(s => ({
			type: s.type,
			tool: s.type === 'benchmark' ? s.tool : undefined,
			params: buildStepParams(s)
		}));
		const scenarioLoops: ScenarioLoop[] = loops.map(l => ({ startStep: l.startStep, endStep: l.endStep, count: l.count }));
		try {
			const res = await runScenario(serverId, {
				deviceIds: [...selectedDevices],
				scenarioName: scenarioName || undefined,
				steps: scenarioSteps,
				loops: scenarioLoops.length > 0 ? scenarioLoops : undefined,
				repeat,
				busyPolicy
			});
			toast.success(`Scenario 시작: ${res.jobId.slice(0, 8)}`);
			onJobStarted({ jobId: res.jobId, serverId, serverName, type: 'scenario', jobName: scenarioName || undefined, deviceIds: [...selectedDevices], createdAt: Date.now() });
		} catch { toast.error('Scenario 시작 실패'); }
		finally { running = false; }
	}
</script>

<ConfirmDialog bind:open={confirmOpen} title="삭제 확인" description={confirmDesc} confirmLabel="삭제" onConfirm={confirmAction} onCancel={() => { confirmOpen = false; }} />

<AgentStepEditDialog
	bind:open={editDialogOpen}
	step={editingStep}
	stepIndex={editingStepIndex}
	allSteps={steps}
	onSave={handleStepSave}
	onCancel={handleStepCancel}
/>

<div class="max-w-2xl space-y-4 p-1">
	<div>
		<h2 class="text-sm font-semibold">Scenario 실행</h2>
		{#if deviceCount > 0}
			<p class="text-[10px] text-muted-foreground mt-0.5">{deviceCount}개 디바이스에서 실행합니다</p>
		{:else}
			<p class="text-[10px] text-orange-600 mt-0.5">왼쪽에서 디바이스를 선택해주세요</p>
		{/if}
	</div>

	<!-- Template selector -->
	<div class="flex items-center gap-2">
		<select
			value={selectedTemplateId ?? ''}
			onchange={(e) => {
				const val = (e.target as HTMLSelectElement).value;
				if (val) { const t = templates.find(t => t.id === Number(val)); if (t) applyTemplate(t); }
				else clearTemplate();
			}}
			class="border rounded px-2 py-1 text-xs bg-background flex-1"
		>
			<option value="">새 시나리오 (직접 구성)</option>
			{#each templates as t (t.id)}
				<option value={t.id}>{t.name} ({repeat}x, {JSON.parse(t.stepsJson).length} steps)</option>
			{/each}
		</select>
		{#if selectedTemplateId != null}
			<button onclick={() => handleDuplicate(selectedTemplateId!)} class="p-1 rounded hover:bg-muted" title="복제"><CopyIcon class="size-3" /></button>
			<button onclick={() => requestDeleteTemplate(selectedTemplateId!)} class="p-1 rounded hover:bg-muted text-red-500" title="삭제"><TrashIcon class="size-3" /></button>
		{/if}
	</div>

	<!-- Name + Repeat + Busy Policy -->
	<div class="grid grid-cols-3 gap-2">
		<div class="space-y-1">
			<label class="{sectionLabel}">Scenario Name</label>
			<input bind:value={scenarioName} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background" placeholder="선택 사항" />
		</div>
		<div class="space-y-1">
			<label class="{sectionLabel}">Repeat</label>
			<input type="number" bind:value={repeat} min="1" class="w-full border rounded px-2.5 py-1.5 text-xs bg-background" />
		</div>
		<div class="space-y-1">
			<label class="{sectionLabel}">Busy Policy</label>
			<select bind:value={busyPolicy} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background">
				<option value="reject">Reject</option>
				<option value="wait">Wait</option>
				<option value="force">Force</option>
			</select>
		</div>
	</div>

	<!-- Steps (compact summary list) -->
	<div class="space-y-1.5">
		<div class="flex items-center justify-between">
			<label class="{sectionLabel}">Steps ({steps.length})</label>
			<button onclick={addStep} class="inline-flex items-center gap-0.5 rounded border px-1.5 py-0.5 text-[10px] hover:bg-muted">
				<PlusIcon class="size-3" /> Step 추가
			</button>
		</div>

		<div class="space-y-1">
			{#each steps as step, i}
				{@const loopInfo = getLoopForStep(i)}
				<div class="flex items-center gap-1.5 border rounded-md px-2 py-1.5 hover:bg-muted/50 transition-colors group
					{loopInfo ? `border-l-2 ${getLoopColor(loopInfo.loopIdx)} ${getLoopBg(loopInfo.loopIdx)}` : ''}">
					<!-- Select checkbox -->
					<input type="checkbox" checked={selectedSteps.has(i)}
						onchange={() => toggleStepSelect(i)}
						class="size-3 shrink-0" />
					<!-- Step number -->
					<span class="text-[9px] font-mono text-muted-foreground w-4 shrink-0">{i + 1}</span>

					<!-- Type badge -->
					<span class="px-1.5 py-0.5 rounded text-[9px] shrink-0 {stepTypeColor(step.type)}">
						{step.type}
					</span>

					<!-- Summary (clickable to edit) -->
					<button
						onclick={() => openStepEditor(i)}
						class="flex-1 text-left text-[10px] truncate hover:text-primary transition-colors"
						title="클릭하여 편집"
					>
						{stepSummary(step)}
					</button>

					<!-- Trace badge -->
					{#if step.traceEnabled}
						<span class="inline-flex items-center gap-0.5 px-1 py-0.5 rounded text-[8px] bg-emerald-100 text-emerald-700 shrink-0">
							<ScanSearchIcon class="size-2" />
							{step.traceType}
						</span>
					{/if}

					<!-- Action buttons -->
					<div class="flex gap-0.5 shrink-0 opacity-0 group-hover:opacity-100 transition-opacity">
						<button onclick={() => openStepEditor(i)} class="p-0.5 rounded hover:bg-muted" title="편집"><PencilIcon class="size-3 text-muted-foreground" /></button>
						<button onclick={() => moveStep(i, -1)} disabled={i === 0} class="p-0.5 rounded hover:bg-muted disabled:opacity-30"><ArrowUpIcon class="size-3" /></button>
						<button onclick={() => moveStep(i, 1)} disabled={i === steps.length - 1} class="p-0.5 rounded hover:bg-muted disabled:opacity-30"><ArrowDownIcon class="size-3" /></button>
						<button onclick={() => removeStep(i)} class="p-0.5 rounded hover:bg-muted text-red-600"><TrashIcon class="size-3" /></button>
					</div>
				</div>
			{/each}
		</div>

		<!-- Loop controls -->
		<div class="space-y-1">
			<div class="flex items-center justify-between">
				<label class="{sectionLabel}">Loops ({loops.length})</label>
				<div class="flex items-center gap-1">
					{#if selectedSteps.size > 0}
						<button onclick={wrapSelectedAsLoop} class="inline-flex items-center gap-0.5 rounded border border-blue-300 bg-blue-50 px-1.5 py-0.5 text-[10px] text-blue-700 hover:bg-blue-100 transition-colors">
							<RepeatIcon class="size-3" /> 선택 Step을 Loop로 ({selectedSteps.size}개)
						</button>
						<button onclick={() => selectedSteps = new Set()} class="text-[10px] text-muted-foreground hover:text-foreground px-1">
							선택 해제
						</button>
					{/if}
					<button onclick={addLoop} disabled={steps.length < 2} class="inline-flex items-center gap-0.5 rounded border px-1.5 py-0.5 text-[10px] hover:bg-muted disabled:opacity-50">
						<PlusIcon class="size-3" /> 수동 추가
					</button>
				</div>
			</div>
			{#each loops as loop, li}
				<div class="flex items-center gap-1.5 text-[10px] px-1 py-0.5 rounded {getLoopBg(li)} border-l-2 {getLoopColor(li)}">
					<RepeatIcon class="size-3 text-muted-foreground shrink-0" />
					<span>Step {loop.startStep + 1}~{loop.endStep + 1}</span>
					<span>x</span>
					<input type="number" bind:value={loop.count} min="1" class="w-14 border rounded px-1 py-0.5 bg-background" />
					<button onclick={() => removeLoop(li)} class="p-0.5 rounded hover:bg-muted text-red-600"><TrashIcon class="size-3" /></button>
				</div>
			{/each}
		</div>
	</div>

	<!-- Actions -->
	<div class="flex items-center gap-2">
		<button
			onclick={handleRun}
			disabled={running || deviceCount === 0 || steps.length === 0 || serverId == null}
			class="flex-1 inline-flex items-center justify-center gap-2 rounded-md bg-blue-600 text-white px-4 py-2.5 text-xs font-medium hover:bg-blue-700 disabled:opacity-50 transition-colors"
		>
			{#if running}<LoaderIcon class="size-4 animate-spin" /> 실행 중...{:else}<PlayIcon class="size-4" /> Scenario 실행{/if}
		</button>

		{#if showSave}
			<div class="flex items-center gap-1">
				<input bind:value={saveName} class="border rounded px-2 py-1 text-[10px] bg-background w-32" placeholder="템플릿 이름" onkeydown={(e) => { if (e.key === 'Enter') saveTemplate(); }} />
				<button onclick={saveTemplate} class="p-1 rounded bg-blue-600 text-white hover:bg-blue-700"><SaveIcon class="size-3" /></button>
				<button onclick={() => { showSave = false; }} class="p-1 rounded border hover:bg-muted text-[10px]">취소</button>
			</div>
		{:else}
			<button onclick={() => { showSave = true; saveName = scenarioName; }} class="inline-flex items-center gap-1 rounded-md border px-3 py-2.5 text-[10px] hover:bg-muted">
				<SaveIcon class="size-3" /> 템플릿 저장
			</button>
		{/if}
	</div>
</div>
