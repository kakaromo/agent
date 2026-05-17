<script lang="ts">
	import LogBrowserDialog from '$lib/components/LogBrowserDialog.svelte';
	import { tentacle } from '$lib/stores/tentacle.svelte.js';
	import { fetchVms, type VmInfo } from '$lib/api/guacamole.js';
	import FolderOpenIcon from '@lucide/svelte/icons/folder-open';
	import ServerIcon from '@lucide/svelte/icons/server';
	import XIcon from '@lucide/svelte/icons/x';

	let {
		binaryFile = $bindable<File | null>(null),
		serverPath = $bindable(''),
		serverName = $bindable('')
	} = $props();

	let mode = $state<'upload' | 'server'>('upload');
	let dragOver = $state(false);
	let logBrowserOpen = $state(false);
	let vms = $state<VmInfo[]>([]);
	let vmsLoaded = $state(false);

	async function loadVms() {
		if (vmsLoaded) return;
		try {
			vms = await fetchVms();
		} catch {
			vms = [];
		}
		vmsLoaded = true;
	}

	function handleDrop(e: DragEvent) {
		e.preventDefault();
		dragOver = false;
		const file = e.dataTransfer?.files[0];
		if (file) {
			binaryFile = file;
			mode = 'upload';
		}
	}

	function handleFileSelect(e: Event) {
		const input = e.target as HTMLInputElement;
		if (input.files?.[0]) binaryFile = input.files[0];
	}

	async function selectServer() {
		mode = 'server';
		await loadVms();
	}

	async function openFileBrowser() {
		if (!serverName) return;
		await tentacle.fetchPrefix();
		logBrowserOpen = true;
	}

	function handleFileSelected(filePath: string) {
		serverPath = filePath;
	}

	function clearServerSelection() {
		serverName = '';
		serverPath = '';
	}

	function formatSize(bytes: number): string {
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
		return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
	}

	let browserInitialPath = $derived(
		serverName === 'HEAD' ? (tentacle.headPrefix || '/home/octo/nas') : (tentacle.prefix || '/home/octo/tentacle')
	);
</script>

<div class="space-y-2">
	<div class="flex gap-2 text-xs">
		<button
			class="px-2 py-1 rounded {mode === 'upload' ? 'bg-primary text-primary-foreground' : 'bg-muted'}"
			onclick={() => (mode = 'upload')}
		>
			File Upload
		</button>
		<button
			class="px-2 py-1 rounded {mode === 'server' ? 'bg-primary text-primary-foreground' : 'bg-muted'}"
			onclick={selectServer}
		>
			Server Path
		</button>
	</div>

	{#if mode === 'upload'}
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			class="border-2 border-dashed rounded-lg p-4 text-center text-xs transition-colors
				{dragOver ? 'border-primary bg-primary/5' : 'border-border'}"
			ondragover={(e) => { e.preventDefault(); dragOver = true; }}
			ondragleave={() => (dragOver = false)}
			ondrop={handleDrop}
		>
			{#if binaryFile}
				<div class="flex items-center justify-center gap-2">
					<span class="font-mono">{binaryFile.name}</span>
					<span class="text-muted-foreground">({formatSize(binaryFile.size)})</span>
					<button class="inline-flex items-center text-destructive" onclick={() => (binaryFile = null)} title="Remove"><XIcon class="w-3 h-3" /></button>
				</div>
			{:else}
				<label class="cursor-pointer">
					<span class="text-muted-foreground">Drop binary file here or </span>
					<span class="text-primary underline">browse</span>
					<input type="file" class="hidden" onchange={handleFileSelect} />
				</label>
			{/if}
		</div>
	{:else}
		<div class="space-y-2">
			<!-- Server Selection -->
			<div class="flex gap-1 items-center">
				<ServerIcon class="size-3.5 text-muted-foreground shrink-0" />
				<select
					class="flex-1 h-7 rounded-md border border-input bg-background px-2 text-xs focus:outline-none focus:ring-2 focus:ring-primary"
					bind:value={serverName}
					onchange={() => { serverPath = ''; }}
				>
					<option value="">Select server...</option>
					{#each vms.filter(v => v.connectionType === 1 || v.connectionType === 3) as vm}
						<option value={vm.name}>{vm.name} ({vm.ip})</option>
					{/each}
				</select>
			</div>

			<!-- File Path -->
			{#if serverName}
				<div class="flex gap-1 items-center">
					<input
						type="text"
						class="flex-1 h-7 rounded-md border border-input bg-background px-2 font-mono text-xs focus:outline-none focus:ring-2 focus:ring-primary"
						placeholder="Select file from browser..."
						bind:value={serverPath}
						readonly
					/>
					<button
						class="shrink-0 h-7 px-2 rounded-md border border-input bg-background hover:bg-muted text-xs transition-colors"
						onclick={openFileBrowser}
						title="Browse files on {serverName}"
					>
						<FolderOpenIcon class="size-3.5" />
					</button>
					{#if serverPath}
						<button
							class="shrink-0 h-7 px-1.5 rounded-md border border-input bg-background hover:bg-destructive/10 text-xs transition-colors text-muted-foreground hover:text-destructive"
							onclick={clearServerSelection}
							title="Clear"
						>
							<XIcon class="size-3.5" />
						</button>
					{/if}
				</div>
			{/if}
		</div>
	{/if}
</div>

{#if serverName}
	<LogBrowserDialog
		bind:open={logBrowserOpen}
		tentacleName={serverName}
		initialPath={browserInitialPath}
		title="Select Binary File — {serverName}"
		selectMode={true}
		onSelect={handleFileSelected}
		onClose={() => { logBrowserOpen = false; }}
	/>
{/if}
