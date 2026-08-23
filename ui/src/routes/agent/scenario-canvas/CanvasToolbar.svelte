<script lang="ts">
	import { toast } from 'svelte-sonner';
	import {
		fetchScenarioTemplates,
		createScenarioTemplate,
		updateScenarioTemplate,
		deleteScenarioTemplate,
		duplicateScenarioTemplate,
		exportScenarioTemplate,
		exportAllScenarioTemplates,
		importScenarioTemplates,
		runScenario,
		generateScenario,
		getAiStatus,
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
	import DownloadIcon from '@lucide/svelte/icons/download';
	import UploadIcon from '@lucide/svelte/icons/upload';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';
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
		// AI 자연어 생성 결과(steps/loops proto wire shape)를 캔버스에 주입.
		onGenerate: (steps: any[], loops: any[]) => void;
		loopMembers: Map<string, Set<string>>;
	}

	let { nodes, edges, serverId, selectedDevices, serverName, onJobStarted, onLoadTemplate, onClearCanvas, onAutoLayout, onGenerate, loopMembers }: Props = $props();

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

	// ── AI 자연어 생성 ──
	// reachable=true 일 때만 버튼 노출.
	let aiReachable = $state(false);
	let showGenerate = $state(false);
	let generatePrompt = $state('');
	let generating = $state(false);
	// 생성 대상 디바이스 — 선택된 것 중 첫 device (deviceId 로 백엔드가 설치앱/현재화면 반영).
	let targetDeviceId = $derived([...selectedDevices][0]);

	getAiStatus().then(s => { aiReachable = !!(s.enabled && s.reachable); }).catch(() => { aiReachable = false; });

	async function handleGenerate() {
		const prompt = generatePrompt.trim();
		if (!prompt) { toast.error('생성할 시나리오를 자연어로 입력하세요'); return; }
		generating = true;
		try {
			const res = await generateScenario(prompt, targetDeviceId);
			(res.warnings ?? []).forEach(w => toast.warning(w));
			if (res.steps.length === 0) {
				toast.error('생성된 step 이 없습니다');
				return;
			}
			onGenerate(res.steps, res.loops);
			toast.success(`${res.steps.length}개 step 생성됨`);
			showGenerate = false;
			generatePrompt = '';
		} catch (e) {
			toast.error(e instanceof Error ? e.message : '시나리오 생성 실패');
		} finally {
			generating = false;
		}
	}

	loadTemplates();

	async function loadTemplates() {
		try { templates = await fetchScenarioTemplates(); }
		catch { templates = []; }
	}

	// ── 이식(export/import) — ADR-0001 ──
	let importInputEl = $state<HTMLInputElement | null>(null);

	// import 미리보기 — 파일을 고르면 바로 DB 에 넣지 않고 확인 다이얼로그를 먼저 띄운다.
	interface ImportPreviewItem {
		name: string;
		stepCount: number;
		packages: string[];
		schemaVersion?: number;
	}
	let importDialogOpen = $state(false);
	let importPreview = $state<ImportPreviewItem[]>([]);
	let importPayload = $state<unknown>(null);
	let importFileName = $state('');

	// 파일 JSON(단일/배열)에서 미리보기 정보를 뽑는다 (서버 안 거침).
	function buildImportPreview(payload: unknown): ImportPreviewItem[] {
		const arr = Array.isArray(payload) ? payload : [payload];
		return arr.map((raw) => {
			const s = (raw ?? {}) as Record<string, any>;
			const steps = Array.isArray(s.steps) ? s.steps : [];
			const req = (s.requirements ?? {}) as Record<string, any>;
			return {
				name: typeof s.name === 'string' && s.name ? s.name : '(이름 없음)',
				stepCount: steps.length,
				packages: Array.isArray(req.packages) ? req.packages : [],
				schemaVersion: typeof s.schemaVersion === 'number' ? s.schemaVersion : undefined
			};
		});
	}

	async function handleExport() {
		if (selectedTemplateId == null) { toast.info('내보낼 템플릿을 먼저 선택하세요'); return; }
		try { await exportScenarioTemplate(selectedTemplateId); }
		catch (e) { toast.error(e instanceof Error ? e.message : '내보내기 실패'); }
	}

	async function handleExportAll() {
		if (templates.length === 0) { toast.info('내보낼 시나리오가 없습니다'); return; }
		try { await exportAllScenarioTemplates(); }
		catch (e) { toast.error(e instanceof Error ? e.message : '전체 내보내기 실패'); }
	}

	// 1단계: 파일 선택 → 파싱 + 미리보기 다이얼로그 (아직 DB 에 넣지 않음)
	async function handleImportFile(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) { return; }
		try {
			const text = await file.text();
			let payload: unknown;
			try { payload = JSON.parse(text); }
			catch { toast.error('JSON 파일을 읽을 수 없습니다'); return; }

			const preview = buildImportPreview(payload);
			if (preview.length === 0) { toast.error('불러올 시나리오가 없습니다'); return; }

			importPayload = payload;
			importPreview = preview;
			importFileName = file.name;
			importDialogOpen = true;
		} catch (err) {
			toast.error(err instanceof Error ? err.message : '파일을 읽지 못했습니다');
		} finally {
			input.value = ''; // 같은 파일 재선택 허용
		}
	}

	// 2단계: 확인 후 실제 import. ConfirmDialog.handleConfirm 이 onConfirm 완료 시
	// 자동으로 open=false + busy 를 처리하므로 여기선 결과 안내만 한다.
	async function confirmImport() {
		if (importPayload == null) return;
		try {
			const res = await importScenarioTemplates(importPayload);
			const imported = res.imported?.length ?? 0;
			const skipped = res.skipped?.length ?? 0;
			if (imported > 0) toast.success(`${imported}개 시나리오를 불러왔습니다${skipped ? ` (중복 ${skipped}개 건너뜀)` : ''}`);
			else if (skipped > 0) toast.info(`이미 있는 시나리오입니다 (${skipped}개 건너뜀)`);
			else toast.info('불러온 시나리오가 없습니다');
			(res.warnings ?? []).forEach((w) => toast.warning(w));
			await loadTemplates();
		} catch (err) {
			toast.error(err instanceof Error ? err.message : '가져오기 실패');
		} finally {
			importPayload = null;
		}
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

<!-- import 미리보기 확인 다이얼로그 -->
<ConfirmDialog
	bind:open={importDialogOpen}
	title="시나리오 가져오기"
	description={`${importFileName} — ${importPreview.length}개 시나리오를 불러옵니다. 이미 있는 시나리오(내용 동일)는 자동으로 건너뜁니다.`}
	confirmLabel="가져오기"
	variant="default"
	onConfirm={confirmImport}
	onCancel={() => { importDialogOpen = false; importPayload = null; }}
>
	{#snippet children()}
		<ul class="space-y-1.5 max-h-56 overflow-y-auto text-xs">
			{#each importPreview as item}
				<li class="rounded border px-2 py-1.5">
					<div class="flex items-center justify-between gap-2">
						<span class="font-medium truncate">{item.name}</span>
						<span class="text-[10px] text-muted-foreground shrink-0">{item.stepCount} steps</span>
					</div>
					{#if item.packages.length > 0}
						<div class="mt-1 text-[10px] text-amber-600 dark:text-amber-500">
							필요 앱: {item.packages.join(', ')}
						</div>
					{/if}
					{#if item.schemaVersion == null}
						<div class="mt-1 text-[10px] text-amber-600 dark:text-amber-500">
							⚠ schemaVersion 없음 — 이식 파일이 아닐 수 있습니다
						</div>
					{/if}
				</li>
			{/each}
		</ul>
	{/snippet}
</ConfirmDialog>

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
		<button onclick={handleExport} class="p-1 rounded hover:bg-muted" title="파일로 내보내기 (.scenario.json)"><DownloadIcon class="size-3" /></button>
		<button onclick={() => requestDelete(selectedTemplateId!)} class="p-1 rounded hover:bg-muted text-red-500" title="삭제"><TrashIcon class="size-3" /></button>
	{/if}
	<button onclick={() => importInputEl?.click()} class="p-1 rounded hover:bg-muted" title="파일에서 가져오기 (.scenario.json / .scenariopack.json)"><UploadIcon class="size-3" /></button>
	{#if templates.length > 0}
		<button onclick={handleExportAll} class="p-1 rounded hover:bg-muted" title="전체 내보내기 (.scenariopack.json)"><DownloadIcon class="size-3 opacity-70" /></button>
	{/if}
	<input bind:this={importInputEl} type="file" accept=".json,application/json" class="hidden" onchange={handleImportFile} />

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

	{#if aiReachable}
		<button
			onclick={() => { showGenerate = !showGenerate; }}
			class="inline-flex items-center gap-1 rounded border px-1.5 py-1 text-[9px] hover:bg-muted {showGenerate ? 'bg-muted' : ''}"
			title="AI 로 자연어 시나리오 생성"
		>
			<SparklesIcon class="size-3" /> 자연어로 생성
		</button>
	{/if}

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

<!-- AI 자연어 생성 패널 (toolbar 아래 펼침) -->
{#if aiReachable && showGenerate}
	<div class="border-b bg-muted/20 px-3 py-2.5 space-y-2">
		<div class="flex items-center gap-1.5 text-[10px] font-semibold text-muted-foreground">
			<SparklesIcon class="size-3" /> 자연어로 시나리오 생성
			{#if targetDeviceId}
				<span class="font-mono text-[9px] font-normal text-blue-600" title="이 디바이스의 설치앱·현재 화면을 반영">대상: {targetDeviceId}</span>
			{:else}
				<span class="text-[9px] font-normal text-amber-600">대상 디바이스를 선택하면 더 정확합니다</span>
			{/if}
		</div>
		<textarea
			bind:value={generatePrompt}
			rows="3"
			placeholder="예) 유튜브를 실행하고 검색창에 'lofi' 를 입력한 뒤 첫 영상을 재생, 이 과정을 3번 반복"
			class="w-full resize-y rounded border bg-background px-2 py-1.5 text-[11px] leading-relaxed focus:outline-none focus:ring-1 focus:ring-blue-500"
			onkeydown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) handleGenerate(); }}
		></textarea>
		<div class="flex items-center justify-end gap-2">
			<span class="mr-auto text-[9px] text-muted-foreground">Cmd/Ctrl + Enter 로 생성. 기존 캔버스를 덮어씁니다.</span>
			<button onclick={() => { showGenerate = false; }} class="rounded border px-2 py-1 text-[9px] hover:bg-muted">취소</button>
			<button
				onclick={handleGenerate}
				disabled={generating || !generatePrompt.trim()}
				class="inline-flex items-center gap-1 rounded-md bg-blue-600 px-3 py-1 text-[9px] font-medium text-white hover:bg-blue-700 disabled:opacity-50"
			>
				{#if generating}<LoaderIcon class="size-3 animate-spin" /> 생성 중...{:else}<SparklesIcon class="size-3" /> 생성{/if}
			</button>
		</div>
	</div>
{/if}
