<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { Tabs, TabsContent, TabsList, TabsTrigger } from '$lib/components/ui/tabs/index.js';
	import { executeDlm, dlmDownloadUrl, uploadDlmToMinio, fetchDlmTools, type DlmSlotTarget, type DlmExecuteResult, type DlmTool } from '$lib/api/dlm.js';
	import { parseBinary, getStructs, type MappedResult, type PredefinedStruct } from '$lib/api/binMapper.js';
	import TableView from '../bin-mapper/TableView.svelte';
	import HexStructView from '../bin-mapper/HexStructView.svelte';
	import JsonView from '../bin-mapper/JsonView.svelte';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import UploadCloudIcon from '@lucide/svelte/icons/upload-cloud';
	import CheckCircleIcon from '@lucide/svelte/icons/circle-check';
	import XCircleIcon from '@lucide/svelte/icons/circle-x';
	import ClockIcon from '@lucide/svelte/icons/clock';
	import { toast } from 'svelte-sonner';

	interface Props {
		open: boolean;
		slots: DlmSlotTarget[];
		onClose: () => void;
	}

	let { open = $bindable(), slots, onClose }: Props = $props();

	type SlotStatus = 'pending' | 'running' | 'done' | 'error';

	interface SlotState {
		target: DlmSlotTarget;
		status: SlotStatus;
		result?: DlmExecuteResult;
		error?: string;
	}

	let slotStates = $state<SlotState[]>([]);
	let step = $state<'idle' | 'executing' | 'results'>('idle');

	// Tool selection
	let tools = $state<DlmTool[]>([]);
	let selectedToolId = $state<number | null>(null);

	// Struct selection
	let structs = $state<PredefinedStruct[]>([]);
	let selectedStructId = $state<number | null>(null);
	let selectedEndianness = $state('AUTO');
	let parsing = $state(false);

	// Parse results per slot (keyed by slotNumber)
	let parseResults = $state<Record<number, MappedResult>>({});
	let parseErrors = $state<Record<number, string>>({});

	// Active slot tab (for multi-slot)
	let activeSlotTab = $state('');

	// View mode for bin-mapper results
	let viewMode = $state<'table' | 'hex' | 'json'>('table');
	let highlightedOffset = $state(-1);

	// MinIO upload state
	let uploadingMinio = $state<Record<number, boolean>>({});
	let uploadedMinio = $state<Record<number, string>>({});

	$effect(() => {
		if (open) {
			resetState();
			loadStructs();
			loadTools();
		}
	});

	function resetState() {
		slotStates = slots.map(s => ({ target: s, status: 'pending' as SlotStatus }));
		step = 'idle';
		selectedToolId = null;
		selectedStructId = null;
		selectedEndianness = 'AUTO';
		parsing = false;
		parseResults = {};
		parseErrors = {};
		viewMode = 'table';
		highlightedOffset = -1;
		uploadingMinio = {};
		uploadedMinio = {};
		activeSlotTab = slots.length > 0 ? String(slots[0].slotNumber) : '';
	}

	async function loadTools() {
		try {
			tools = await fetchDlmTools();
			if (tools.length === 1) selectedToolId = tools[0].id;
		} catch (e) {
			console.error('Failed to load DLM tools:', e);
		}
	}

	async function loadStructs() {
		try {
			structs = await getStructs('dlm');
		} catch (e) {
			console.error('Failed to load structs:', e);
		}
	}

	async function handleExecute() {
		if (selectedToolId == null) return;
		step = 'executing';

		for (let i = 0; i < slotStates.length; i++) {
			slotStates[i].status = 'running';
			try {
				const result = await executeDlm({ ...slotStates[i].target, toolId: selectedToolId });
				slotStates[i] = { ...slotStates[i], status: 'done', result };
			} catch (e: any) {
				slotStates[i] = {
					...slotStates[i],
					status: 'error',
					error: e instanceof Error ? e.message : 'Failed'
				};
			}
		}

		step = 'results';
	}

	async function handleParse() {
		if (selectedStructId == null) return;
		parsing = true;
		parseResults = {};
		parseErrors = {};

		const doneSlots = slotStates.filter(s => s.status === 'done' && s.result);
		const promises = doneSlots.map(async (s) => {
			const r = s.result!;
			try {
				const mapped = await parseBinary({
					serverName: s.target.tentacleName,
					serverPath: r.filePath,
					predefinedStructId: selectedStructId!,
					endianness: selectedEndianness
				});
				parseResults[s.target.slotNumber] = mapped;
			} catch (e: any) {
				parseErrors[s.target.slotNumber] = e instanceof Error ? e.message : 'Parse failed';
			}
		});

		await Promise.all(promises);
		parsing = false;
	}

	async function handleUploadMinio(slotNum: number) {
		const s = slotStates.find(s => s.target.slotNumber === slotNum);
		if (!s?.result) return;

		uploadingMinio[slotNum] = true;
		try {
			const res = await uploadDlmToMinio(s.target.tentacleName, s.result.filePath, s.result.fileName);
			uploadedMinio[slotNum] = res.objectName;
		} catch (e: any) {
			toast.error(e instanceof Error ? e.message : 'MinIO upload failed');
		} finally {
			uploadingMinio[slotNum] = false;
		}
	}

	function getSlotLabel(s: DlmSlotTarget): string {
		return `${s.tentacleName}-${s.slotNumber}`;
	}

	const doneSlots = $derived(slotStates.filter(s => s.status === 'done' && s.result));
	const activeSlotNum = $derived(Number(activeSlotTab));
	const activeResult = $derived(parseResults[activeSlotNum]);
	const hasParsed = $derived(Object.keys(parseResults).length > 0);
</script>

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) onClose(); }}>
	<Dialog.Content class="max-h-[85vh] overflow-y-auto transition-all {hasParsed ? 'sm:max-w-[95vw] lg:max-w-5xl' : 'sm:max-w-[90vw] lg:max-w-3xl'}">
		<Dialog.Header>
			<Dialog.Title class="text-sm font-semibold">DLM Debug</Dialog.Title>
			<Dialog.Description>
				<span class="text-xs text-muted-foreground">
					{slots.length}개 슬롯 선택됨
				</span>
			</Dialog.Description>
		</Dialog.Header>

		<div class="space-y-4 py-2">
			<!-- Slot status table -->
			<div class="border rounded-md overflow-hidden">
				<table class="w-full text-xs">
					<thead>
						<tr class="border-b bg-muted/30">
							<th class="px-3 py-1.5 text-left font-medium">Slot</th>
							<th class="px-3 py-1.5 text-left font-medium">Serial</th>
							<th class="px-3 py-1.5 text-left font-medium">Status</th>
						</tr>
					</thead>
					<tbody>
						{#each slotStates as s (s.target.slotNumber)}
							<tr class="border-b last:border-b-0">
								<td class="px-3 py-1.5 font-medium">{getSlotLabel(s.target)}</td>
								<td class="px-3 py-1.5 font-mono text-[10px] text-foreground/70">{s.target.serial}</td>
								<td class="px-3 py-1.5">
									{#if s.status === 'pending'}
										<span class="flex items-center gap-1 text-muted-foreground">
											<ClockIcon class="size-3" />pending
										</span>
									{:else if s.status === 'running'}
										<span class="flex items-center gap-1 text-blue-600">
											<LoaderIcon class="size-3 animate-spin" />running...
										</span>
									{:else if s.status === 'done'}
										<span class="flex items-center gap-1 text-green-600">
											<CheckCircleIcon class="size-3" />done
										</span>
									{:else}
										<div>
											<span class="flex items-center gap-1 text-red-600">
												<XCircleIcon class="size-3" />error
											</span>
											{#if s.error}
												<pre class="mt-1 text-[10px] text-red-600/80 whitespace-pre-wrap break-all max-h-20 overflow-auto">{s.error}</pre>
											{/if}
										</div>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>

			{#if step === 'idle'}
				<div class="flex items-center gap-2">
					<span class="text-xs font-medium shrink-0">Tool:</span>
					<select
						bind:value={selectedToolId}
						class="flex-1 border rounded px-2 py-1.5 text-xs bg-background"
					>
						<option value={null}>-- select tool --</option>
						{#each tools as t (t.id)}
							<option value={t.id}>{t.toolName}{t.description ? ` (${t.description})` : ''}</option>
						{/each}
					</select>
					<button
						class="px-4 py-1.5 rounded bg-blue-600 text-white text-xs font-medium hover:bg-blue-700 transition-colors disabled:opacity-50"
						onclick={handleExecute}
						disabled={selectedToolId == null}
					>
						Execute
					</button>
				</div>
			{/if}

			{#if step === 'executing'}
				<div class="text-center text-xs text-muted-foreground py-2">
					<LoaderIcon class="size-4 animate-spin inline-block mr-1" />
					실행 중...
				</div>
			{/if}

			{#if step === 'results'}
				<!-- Struct selection + Parse -->
				<div class="border rounded-md p-3 space-y-2">
					<div class="flex items-center gap-2">
						<span class="text-xs font-medium">Struct:</span>
						<select
							bind:value={selectedStructId}
							class="flex-1 border rounded px-2 py-1 text-xs bg-background"
						>
							<option value={null}>-- select predefined struct --</option>
							{#each structs as s (s.id)}
								<option value={s.id}>{s.category ? `[${s.category}] ` : ''}{s.name}</option>
							{/each}
						</select>
						<select
							bind:value={selectedEndianness}
							class="border rounded px-2 py-1 text-xs bg-background w-20"
						>
							<option value="AUTO">Auto</option>
							<option value="LITTLE">LE</option>
							<option value="BIG">BE</option>
						</select>
						<button
							class="px-3 py-1 rounded bg-blue-600 text-white text-xs font-medium hover:bg-blue-700 transition-colors disabled:opacity-50"
							onclick={handleParse}
							disabled={selectedStructId == null || parsing || doneSlots.length === 0}
						>
							{#if parsing}
								<LoaderIcon class="size-3 animate-spin inline-block mr-1" />
							{/if}
							Parse
						</button>
					</div>
				</div>

				<!-- Slot tabs (multi-slot) -->
				{#if doneSlots.length > 1}
					<div class="flex gap-1 border-b">
						{#each doneSlots as s (s.target.slotNumber)}
							<button
								class="px-3 py-1.5 text-xs font-medium border-b-2 transition-colors {activeSlotTab === String(s.target.slotNumber) ? 'border-blue-600 text-blue-600' : 'border-transparent text-muted-foreground hover:text-foreground'}"
								onclick={() => { activeSlotTab = String(s.target.slotNumber); }}
							>
								{getSlotLabel(s.target)}
							</button>
						{/each}
					</div>
				{/if}

				<!-- Parse result view -->
				{#if activeResult}
					<div class="border rounded-md overflow-hidden">
						<div class="flex items-center gap-1 px-3 py-1.5 bg-muted/50 border-b">
							<button
								class="px-2 py-0.5 text-xs rounded {viewMode === 'table' ? 'bg-blue-600 text-white' : 'hover:bg-muted'}"
								onclick={() => { viewMode = 'table'; }}
							>Table</button>
							<button
								class="px-2 py-0.5 text-xs rounded {viewMode === 'hex' ? 'bg-blue-600 text-white' : 'hover:bg-muted'}"
								onclick={() => { viewMode = 'hex'; }}
							>Hex</button>
							<button
								class="px-2 py-0.5 text-xs rounded {viewMode === 'json' ? 'bg-blue-600 text-white' : 'hover:bg-muted'}"
								onclick={() => { viewMode = 'json'; }}
							>JSON</button>
						</div>
						<div class="max-h-[40vh] overflow-auto">
							{#if viewMode === 'table'}
								<TableView instances={activeResult.instances} bind:highlightedOffset />
							{:else if viewMode === 'hex'}
								<HexStructView rawBytes={activeResult.rawBytes} instances={activeResult.instances} bind:highlightedOffset />
							{:else}
								<JsonView result={activeResult} />
							{/if}
						</div>
					</div>
				{:else if parseErrors[activeSlotNum]}
					<div class="border rounded-md p-3 text-xs text-red-600">
						Parse error: {parseErrors[activeSlotNum]}
					</div>
				{/if}

				<!-- Action bar -->
				{#if doneSlots.length > 0}
					{@const activeState = slotStates.find(s => s.target.slotNumber === activeSlotNum)}
					{#if activeState?.result}
						<div class="flex items-center gap-2 pt-2 border-t">
							<a
								href={dlmDownloadUrl(activeState.target.tentacleName, activeState.result.filePath)}
								class="px-3 py-1.5 rounded border text-xs font-medium hover:bg-muted transition-colors inline-flex items-center gap-1"
								download
							>
								<DownloadIcon class="size-3" />
								Download .bin
							</a>
							{#if uploadedMinio[activeSlotNum]}
								<span class="text-xs text-green-600 flex items-center gap-1">
									<CheckCircleIcon class="size-3" />
									Uploaded: {uploadedMinio[activeSlotNum]}
								</span>
							{:else}
								<button
									class="px-3 py-1.5 rounded border text-xs font-medium hover:bg-muted transition-colors inline-flex items-center gap-1 disabled:opacity-50"
									onclick={() => handleUploadMinio(activeSlotNum)}
									disabled={uploadingMinio[activeSlotNum]}
								>
									{#if uploadingMinio[activeSlotNum]}
										<LoaderIcon class="size-3 animate-spin" />
									{:else}
										<UploadCloudIcon class="size-3" />
									{/if}
									Upload to MinIO
								</button>
							{/if}
							<div class="flex-1"></div>
							<button
								class="px-3 py-1.5 rounded border text-xs font-medium hover:bg-muted transition-colors"
								onclick={() => { open = false; onClose(); }}
							>
								Close
							</button>
						</div>
					{/if}
				{/if}
			{/if}
		</div>
	</Dialog.Content>
</Dialog.Root>
