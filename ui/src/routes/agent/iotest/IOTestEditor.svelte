<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { sectionLabel, inputSm, captionMuted } from '$lib/styles/common.js';
	import IOTestCommandList from './IOTestCommandList.svelte';
	import { IOTEST_PRESETS, PRESET_CATEGORIES } from './presets.js';
	import type { IOTestConfig, IOTestThread } from './types.js';
	import { fetchIOTestPresets, createIOTestPreset, deleteIOTestPreset, type IOTestPresetDB } from '$lib/api/agent.js';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import SaveIcon from '@lucide/svelte/icons/save';

	interface Props {
		config: IOTestConfig;
		onUpdate: (config: IOTestConfig) => void;
	}

	let { config = $bindable(), onUpdate }: Props = $props();

	let expandedThreads = $state<Set<number>>(new Set([0]));

	// User presets (DB)
	let userPresets = $state<IOTestPresetDB[]>([]);
	let showSavePreset = $state(false);
	let savePresetName = $state('');
	let savePresetCategory = $state('Basic I/O');

	loadUserPresets();

	async function loadUserPresets() {
		try { userPresets = await fetchIOTestPresets(); } catch { userPresets = []; }
	}

	async function saveCurrentAsPreset() {
		if (!savePresetName.trim() || config.threads.length === 0) return;
		try {
			await createIOTestPreset({
				name: savePresetName.trim(),
				category: savePresetCategory,
				configJson: JSON.stringify(config)
			});
			toast.success('프리셋 저장됨');
			savePresetName = '';
			showSavePreset = false;
			await loadUserPresets();
		} catch { toast.error('저장 실패'); }
	}

	async function removeUserPreset(id: number) {
		try {
			await deleteIOTestPreset(id);
			toast.success('프리셋 삭제됨');
			await loadUserPresets();
		} catch { toast.error('삭제 실패'); }
	}

	function applyUserPreset(preset: IOTestPresetDB) {
		try {
			const parsed = JSON.parse(preset.configJson) as IOTestConfig;
			config = parsed;
			const expanded = new Set<number>();
			for (let i = 0; i < config.threads.length; i++) expanded.add(i);
			expandedThreads = expanded;
			onUpdate(config);
		} catch { toast.error('프리셋 로드 실패'); }
	}

	function addThread() {
		const idx = config.threads.length + 1;
		config.threads = [...config.threads, { name: `thread_${idx}`, commands: [] }];
		expandedThreads = new Set([...expandedThreads, config.threads.length - 1]);
		onUpdate(config);
	}

	function removeThread(idx: number) {
		config.threads = config.threads.filter((_, i) => i !== idx);
		onUpdate(config);
	}

	function updateThreadName(idx: number, name: string) {
		config.threads[idx].name = name;
		config = { ...config };
		onUpdate(config);
	}

	function updateThreadCommands(idx: number, commands: any[]) {
		config.threads[idx].commands = commands;
		config = { ...config };
		onUpdate(config);
	}

	function toggleThread(idx: number) {
		const s = new Set(expandedThreads);
		if (s.has(idx)) s.delete(idx); else s.add(idx);
		expandedThreads = s;
	}

	function applyPreset(preset: typeof IOTEST_PRESETS[0]) {
		// Deep clone preset threads
		const newThreads = JSON.parse(JSON.stringify(preset.threads)) as IOTestThread[];
		config.threads = [...config.threads, ...newThreads];
		// Expand newly added threads
		const start = config.threads.length - newThreads.length;
		const expanded = new Set(expandedThreads);
		for (let i = start; i < config.threads.length; i++) expanded.add(i);
		expandedThreads = expanded;
		config = { ...config };
		onUpdate(config);
	}

	function countCommands(thread: IOTestThread): number {
		let count = 0;
		function walk(cmds: any[]) {
			for (const c of cmds) {
				count++;
				if (c.commands) walk(c.commands);
				if (c.then) walk(c.then);
				if (c.else) walk(c.else);
			}
		}
		walk(thread.commands);
		return count;
	}
</script>

<div class="space-y-3">
	<!-- Global settings -->
	<div class="grid grid-cols-3 gap-3">
		<div class="flex items-center gap-1.5">
			<label class="text-[10px] text-muted-foreground whitespace-nowrap">Duration</label>
			<input class={inputSm} type="number" min="0" bind:value={config.duration_seconds} oninput={() => onUpdate(config)} />
			<span class={captionMuted}>sec</span>
		</div>
		<div class="flex items-center gap-1.5">
			<label class="text-[10px] text-muted-foreground whitespace-nowrap">Sync Start</label>
			<input type="checkbox" bind:checked={config.sync_start} onchange={() => onUpdate(config)} class="h-3.5 w-3.5" />
		</div>
	</div>

	<!-- Presets -->
	<div class="space-y-1.5">
		<div class="flex items-center justify-between">
			<span class={sectionLabel}>Presets</span>
			<button class="text-[9px] px-2 py-0.5 rounded border hover:bg-muted flex items-center gap-0.5"
				onclick={() => showSavePreset = !showSavePreset}
				disabled={config.threads.length === 0}>
				<SaveIcon class="w-2.5 h-2.5" /> 현재 설정 저장
			</button>
		</div>

		{#if showSavePreset}
			<div class="flex items-center gap-1.5 p-2 border rounded bg-muted/30">
				<input class="{inputSm} flex-1" bind:value={savePresetName} placeholder="프리셋 이름" />
				<select class={inputSm} bind:value={savePresetCategory}>
					{#each PRESET_CATEGORIES as cat}<option value={cat}>{cat}</option>{/each}
				</select>
				<button class="text-[9px] px-2 py-1 rounded bg-blue-600 text-white hover:bg-blue-700"
					onclick={saveCurrentAsPreset} disabled={!savePresetName.trim()}>저장</button>
				<button class="text-[9px] px-1 text-muted-foreground" onclick={() => showSavePreset = false}>취소</button>
			</div>
		{/if}

		<!-- Built-in presets (카테고리별 그룹) -->
		{#each PRESET_CATEGORIES as cat}
			{@const catPresets = IOTEST_PRESETS.filter(p => p.category === cat)}
			{@const catUserPresets = userPresets.filter(p => p.category === cat)}
			{#if catPresets.length > 0 || catUserPresets.length > 0}
				<div>
					<span class="text-[8px] font-medium text-muted-foreground uppercase">{cat}</span>
					<div class="flex flex-wrap gap-1 mt-0.5">
						{#each catPresets as preset}
							<button class="text-[9px] px-2 py-1 rounded border hover:bg-muted transition-colors"
								title={preset.description} onclick={() => applyPreset(preset)}>
								{preset.label}
							</button>
						{/each}
						{#each catUserPresets as up}
							<div class="inline-flex items-center gap-0.5 text-[9px] px-2 py-1 rounded border border-blue-200 bg-blue-50">
								<button class="hover:text-blue-700" title={up.description ?? ''} onclick={() => applyUserPreset(up)}>
									{up.name}
								</button>
								<button class="text-red-400 hover:text-red-600 ml-0.5" onclick={() => removeUserPreset(up.id)} title="삭제">
									<TrashIcon class="w-2.5 h-2.5" />
								</button>
							</div>
						{/each}
					</div>
				</div>
			{/if}
		{/each}
	</div>

	<!-- Threads -->
	<div>
		<div class="flex items-center justify-between">
			<span class={sectionLabel}>Threads ({config.threads.length})</span>
			<button class="text-[9px] px-2 py-0.5 rounded border hover:bg-muted flex items-center gap-0.5" onclick={addThread}>
				<PlusIcon class="w-2.5 h-2.5" /> Add Thread
			</button>
		</div>

		<div class="space-y-2 mt-1">
			{#each config.threads as thread, idx (idx)}
				<div class="border rounded">
					<!-- Thread header -->
					<div class="flex items-center gap-1.5 px-2 py-1.5 bg-muted/50 cursor-pointer" onclick={() => toggleThread(idx)}>
						{#if expandedThreads.has(idx)}
							<ChevronDownIcon class="w-3 h-3 text-muted-foreground" />
						{:else}
							<ChevronRightIcon class="w-3 h-3 text-muted-foreground" />
						{/if}
						<input class="text-[10px] font-medium bg-transparent border-b border-transparent hover:border-border focus:border-border outline-none w-28"
							value={thread.name}
							onclick={(e) => e.stopPropagation()}
							oninput={(e) => updateThreadName(idx, (e.target as HTMLInputElement).value)} />
						<span class={captionMuted}>({countCommands(thread)} commands)</span>
						<div class="flex-1"></div>
						<button class="p-0.5 hover:bg-destructive/10 rounded text-destructive"
							onclick={(e) => { e.stopPropagation(); removeThread(idx); }}>
							<TrashIcon class="w-3 h-3" />
						</button>
					</div>

					<!-- Thread commands -->
					{#if expandedThreads.has(idx)}
						<div class="p-2">
							<IOTestCommandList
								bind:commands={thread.commands}
								onUpdate={(cmds) => updateThreadCommands(idx, cmds)} />
						</div>
					{/if}
				</div>
			{/each}
		</div>
	</div>
</div>
