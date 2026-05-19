<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { sectionLabel, captionMuted } from '$lib/styles/common.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import {
		runBenchmark,
		fetchBenchmarkPresets,
		createBenchmarkPreset,
		deleteBenchmarkPreset,
		type BenchmarkPreset
	} from '$lib/api/agent.js';
	import { getBasicOptions, getAdvancedOptions, getDefaultParams, getOptionsForTool, mergeParams, type OptionDef } from './benchmarkOptions.js';
	import type { ActiveJob } from './types.js';
	import PlayIcon from '@lucide/svelte/icons/play';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import SaveIcon from '@lucide/svelte/icons/save';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import CircleHelpIcon from '@lucide/svelte/icons/circle-help';
	interface Props {
		serverId: number | null;
		selectedDevices: Set<string>;
		serverName: string;
		onJobStarted: (job: Omit<ActiveJob, 'events' | 'state' | 'eventSource'>) => void;
	}

	let { serverId, selectedDevices, serverName, onJobStarted }: Props = $props();

	let tool = $state('FIO');
	let jobName = $state('');
	let formParams = $state<Record<string, string>>(getDefaultParams('FIO'));
	let extraText = $state('');
	let running = $state(false);
	let busyPolicy = $state('reject');


	// Presets
	let presets = $state<BenchmarkPreset[]>([]);
	let selectedPresetId = $state<number | null>(null);
	let presetName = $state('');
	let showSavePreset = $state(false);

	let deviceCount = $derived(selectedDevices.size);
	let basicOptions = $derived<OptionDef[]>(getBasicOptions(tool));
	let advancedOptions = $derived<OptionDef[]>(getAdvancedOptions(tool));
	let showAdvanced = $state(false);

	// Load presets on mount
	loadPresets();

	async function loadPresets() {
		try { presets = await fetchBenchmarkPresets(); } catch { presets = []; }
	}

	// When tool changes, reset form params to defaults
	$effect(() => {
		// Only reset if no preset is selected
		if (selectedPresetId == null) {
			formParams = getDefaultParams(tool);
		}
	});

	function applyPreset(preset: BenchmarkPreset) {
		selectedPresetId = preset.id;
		tool = preset.tool;
		try {
			const parsed = JSON.parse(preset.paramsJson);
			const opts = getOptionsForTool(preset.tool);
			const form: Record<string, string> = {};
			const extra: string[] = [];
			for (const [k, v] of Object.entries(parsed) as [string, string][]) {
				if (opts.some(o => o.key === k)) {
					form[k] = v;
				} else {
					extra.push(`${k}=${v}`);
				}
			}
			// Fill missing form fields with defaults
			for (const opt of opts) {
				if (!(opt.key in form)) form[opt.key] = opt.defaultValue;
			}
			formParams = form;
			extraText = extra.join('\n');
		} catch {
			formParams = getDefaultParams(preset.tool);
			extraText = '';
		}
	}

	function clearPreset() {
		selectedPresetId = null;
		formParams = getDefaultParams(tool);
		extraText = '';
	}

	async function saveAsPreset() {
		if (!presetName.trim()) { toast.error('프리셋 이름을 입력해주세요'); return; }
		const allParams = mergeParams(formParams, extraText);
		try {
			await createBenchmarkPreset({
				name: presetName.trim(),
				tool,
				paramsJson: JSON.stringify(allParams)
			});
			toast.success('프리셋이 저장되었습니다');
			presetName = '';
			showSavePreset = false;
			await loadPresets();
		} catch { toast.error('저장 실패'); }
	}

	async function removePreset(id: number) {
		try {
			await deleteBenchmarkPreset(id);
			toast.success('프리셋이 삭제되었습니다');
			if (selectedPresetId === id) selectedPresetId = null;
			await loadPresets();
		} catch { toast.error('삭제 실패'); }
	}

	async function handleRun() {
		if (serverId == null || deviceCount === 0) return;
		running = true;
		try {
			const params = mergeParams(formParams, extraText);
			const res = await runBenchmark(serverId, {
				deviceIds: [...selectedDevices],
				tool,
				params,
				jobName: jobName || undefined,
				busyPolicy
			});
			toast.success(`Job 시작: ${res.jobId}`);
			onJobStarted({
				jobId: res.jobId,
				serverId,
				serverName,
				type: 'benchmark',
				tool,
				jobName: jobName || undefined,
				deviceIds: [...selectedDevices],
				createdAt: Date.now()
			});
		} catch { toast.error('벤치마크 시작 실패'); }
		finally { running = false; }
	}

	const toolOptions = [
		{ value: 'FIO', label: 'fio', desc: 'Flexible I/O Tester' },
		{ value: 'IOZONE', label: 'iozone', desc: 'I/O Benchmark' },
		{ value: 'TIOTEST', label: 'tiotest', desc: 'Threaded I/O' }
	];
</script>

{#snippet optionGrid(opts: OptionDef[])}
	<div class="grid grid-cols-2 gap-x-4 gap-y-2">
		{#each opts as opt (opt.key)}
			<div class="flex items-center gap-1.5">
				<label class="text-[10px] w-20 shrink-0 text-right text-muted-foreground">{opt.label}</label>
				{#if opt.type === 'select' && opt.choices}
					<select
						value={formParams[opt.key] ?? opt.defaultValue}
						onchange={(e) => { formParams = { ...formParams, [opt.key]: (e.target as HTMLSelectElement).value }; }}
						class="flex-1 border rounded px-1.5 py-0.5 text-[10px] bg-background"
					>
						{#each opt.choices as c}
							<option value={c}>{c}</option>
						{/each}
					</select>
				{:else if opt.type === 'checkbox'}
					<input
						type="checkbox"
						checked={(formParams[opt.key] ?? opt.defaultValue) === '1'}
						onchange={(e) => { formParams = { ...formParams, [opt.key]: (e.target as HTMLInputElement).checked ? '1' : '0' }; }}
						class="size-3"
					/>
				{:else}
					<div class="flex-1 flex items-center gap-1">
						<input
							value={formParams[opt.key] ?? opt.defaultValue}
							oninput={(e) => { formParams = { ...formParams, [opt.key]: (e.target as HTMLInputElement).value }; }}
							class="flex-1 border rounded px-1.5 py-0.5 text-[10px] bg-background font-mono"
						/>
						{#if opt.unit}
							<span class="text-[9px] text-muted-foreground">{opt.unit}</span>
						{/if}
					</div>
				{/if}
				<Tooltip.Provider>
					<Tooltip.Root>
						<Tooltip.Trigger>
							<CircleHelpIcon class="size-3 text-muted-foreground shrink-0 cursor-help" />
						</Tooltip.Trigger>
						<Tooltip.Content side="right" class="max-w-60 text-xs">
							{opt.help}
						</Tooltip.Content>
					</Tooltip.Root>
				</Tooltip.Provider>
			</div>
		{/each}
	</div>
{/snippet}

<div class="max-w-2xl space-y-4 p-1">
	<!-- Context header -->
	<div>
		<h2 class="text-sm font-semibold">Benchmark 실행</h2>
		{#if deviceCount > 0}
			<p class="text-[10px] text-muted-foreground mt-0.5">{deviceCount}개 디바이스에서 실행합니다</p>
		{:else}
			<p class="text-[10px] text-orange-600 mt-0.5">왼쪽에서 디바이스를 선택해주세요</p>
		{/if}
	</div>

	<!-- Preset selector -->
	<div class="flex items-center gap-2">
		<select
			value={selectedPresetId ?? ''}
			onchange={(e) => {
				const val = (e.target as HTMLSelectElement).value;
				if (val) {
					const p = presets.find(p => p.id === Number(val));
					if (p) applyPreset(p);
				} else {
					clearPreset();
				}
			}}
			class="border rounded px-2 py-1 text-xs bg-background flex-1"
		>
			<option value="">프리셋 없음 (직접 입력)</option>
			{#each presets as p (p.id)}
				<option value={p.id}>{p.name} ({p.tool})</option>
			{/each}
		</select>
		{#if selectedPresetId != null}
			{@const current = presets.find(p => p.id === selectedPresetId)}
			{#if current}
				<button onclick={() => removePreset(current.id)} class="p-1 rounded hover:bg-muted text-red-500" title="프리셋 삭제">
					<TrashIcon class="size-3" />
				</button>
			{/if}
		{/if}
	</div>

	<!-- Tool selection -->
	<div class="space-y-1">
		<label class="{sectionLabel}">Tool</label>
		<div class="grid grid-cols-3 gap-2">
			{#each toolOptions as t}
				<button
					onclick={() => { tool = t.value; selectedPresetId = null; }}
					class="border rounded-md px-3 py-2 text-left transition-colors
						{tool === t.value ? 'border-primary bg-primary/5 ring-1 ring-primary' : 'hover:bg-muted'}"
				>
					<div class="text-xs font-medium">{t.label}</div>
					<div class="text-[9px] text-muted-foreground">{t.desc}</div>
				</button>
			{/each}
		</div>
	</div>

	<!-- Job Name + Busy Policy -->
	<div class="grid grid-cols-2 gap-3">
		<div class="space-y-1">
			<label class="{sectionLabel}">Job Name</label>
			<input bind:value={jobName} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background" placeholder="선택 사항" />
		</div>
		<div class="space-y-1">
			<label class="{sectionLabel}">Busy Policy</label>
			<select bind:value={busyPolicy} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background">
				<option value="reject">Reject (BUSY 시 거부)</option>
				<option value="wait">Wait (대기 후 순차 실행)</option>
				<option value="force">Force (동시 실행)</option>
			</select>
		</div>
	</div>

	<!-- Basic parameters -->
	<div class="space-y-1">
			<label class="{sectionLabel}">기본 옵션</label>
			{@render optionGrid(basicOptions)}
		</div>

		<!-- Advanced parameters (collapsible) -->
		{#if advancedOptions.length > 0}
			<div class="space-y-1">
				<button
					onclick={() => showAdvanced = !showAdvanced}
					class="text-[10px] font-medium text-muted-foreground uppercase tracking-wider hover:text-foreground transition-colors"
				>
					고급 옵션 {showAdvanced ? '▾' : '▸'} ({advancedOptions.length})
				</button>
				{#if showAdvanced}
					{@render optionGrid(advancedOptions)}
				{/if}
			</div>
		{/if}

		<!-- Extra parameters (raw textarea) -->
		<div class="space-y-1">
			<label class="{sectionLabel}">추가 파라미터</label>
			<textarea
				bind:value={extraText}
				class="w-full border rounded px-2.5 py-1.5 text-[10px] bg-background font-mono h-16 resize-y"
				placeholder="key=value (한 줄에 하나씩, 위 옵션에 없는 파라미터)"
			></textarea>
		</div>

	<!-- Actions -->
	<div class="flex items-center gap-2">
		<button
			onclick={handleRun}
			disabled={running || deviceCount === 0 || serverId == null}
			class="flex-1 inline-flex items-center justify-center gap-2 rounded-md bg-blue-600 text-white px-4 py-2.5 text-xs font-medium hover:bg-blue-700 disabled:opacity-50 transition-colors"
		>
			{#if running}
				<LoaderIcon class="size-4 animate-spin" /> 실행 중...
			{:else}
				<PlayIcon class="size-4" /> Benchmark 실행
			{/if}
		</button>

		{#if showSavePreset}
			<div class="flex items-center gap-1">
				<input bind:value={presetName} class="border rounded px-2 py-1 text-[10px] bg-background w-32" placeholder="프리셋 이름" onkeydown={(e) => { if (e.key === 'Enter') saveAsPreset(); }} />
				<button onclick={saveAsPreset} class="p-1 rounded bg-blue-600 text-white hover:bg-blue-700"><SaveIcon class="size-3" /></button>
				<button onclick={() => { showSavePreset = false; }} class="p-1 rounded border hover:bg-muted text-[10px]">취소</button>
			</div>
		{:else}
			<button onclick={() => { showSavePreset = true; presetName = jobName || ''; }} class="inline-flex items-center gap-1 rounded-md border px-3 py-2.5 text-[10px] hover:bg-muted">
				<SaveIcon class="size-3" /> 프리셋 저장
			</button>
		{/if}
	</div>
</div>
