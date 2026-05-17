<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import XtermClient from '$lib/components/remote/XtermClient.svelte';
	import SftpPanel from '$lib/components/remote/SftpPanel.svelte';
	import TerminalIcon from '@lucide/svelte/icons/terminal';
	import XIcon from '@lucide/svelte/icons/x';
	import SendIcon from '@lucide/svelte/icons/send';
	import MaximizeIcon from '@lucide/svelte/icons/maximize-2';
	import MinimizeIcon from '@lucide/svelte/icons/minimize-2';
	import FolderIcon from '@lucide/svelte/icons/folder';

	interface TerminalInfo {
		id: string;
		vmName: string;
		slotName: string;
		usbId?: string;
		slotNumber?: number;
		host?: string;
	}

	interface Props {
		open: boolean;
		terminals: TerminalInfo[];
		onClose: () => void;
	}

	let { open = $bindable(), terminals, onClose }: Props = $props();

	// Internal state for managing open terminals
	let openTerminals = $state<TerminalInfo[]>([]);
	let activeTabId = $state<string | null>(null);
	let broadcastCommand = $state('');
	let isFullscreen = $state(false);
	let showSftp = $state(false);
	let lastTerminalsJson = '';

	// Store references to XtermClient instances
	let clientRefs: Record<string, XtermClient | undefined> = {};

	// Sync internal state when props change (only when terminals actually change)
	$effect(() => {
		if (open && terminals.length > 0) {
			const newJson = JSON.stringify(terminals.map(t => t.id));
			if (newJson !== lastTerminalsJson) {
				lastTerminalsJson = newJson;
				openTerminals = [...terminals];
				activeTabId = terminals[0].id;
			}
		} else if (!open) {
			lastTerminalsJson = '';
		}
	});

	function handleClose() {
		// Close all clients
		Object.values(clientRefs).forEach(client => client?.close());
		clientRefs = {};
		openTerminals = [];
		activeTabId = null;
		broadcastCommand = '';
		isFullscreen = false;
		showSftp = false;
		open = false;
		onClose();
	}

	function handleCloseTab(id: string) {
		// Close specific client
		clientRefs[id]?.close();
		delete clientRefs[id];

		openTerminals = openTerminals.filter(t => t.id !== id);

		if (openTerminals.length === 0) {
			handleClose();
		} else if (activeTabId === id) {
			activeTabId = openTerminals[0].id;
		}
	}

	function handleConnect(terminal: TerminalInfo) {
		if (terminal.usbId) {
			// Wait briefly for the shell prompt to appear, then send adb command
			setTimeout(() => {
				const client = clientRefs[terminal.id];
				if (client) {
					client.sendText(`adb -s ${terminal.usbId} shell`);
					client.sendEnter();
				}
			}, 500);
		}
	}

	function handleDisconnect(id: string) {
		console.log(`Terminal ${id} disconnected`);
	}

	function handleError(id: string, message: string) {
		console.error(`Terminal ${id} error:`, message);
	}

	function sendBroadcastCommand() {
		if (!broadcastCommand.trim()) return;

		// Send command to all connected terminals
		Object.values(clientRefs).forEach((client) => {
			if (client) {
				client.sendText(broadcastCommand);
				client.sendEnter();
			}
		});

		broadcastCommand = '';
	}

	function toggleFullscreen() {
		isFullscreen = !isFullscreen;
	}

	let activeTerminal = $derived(openTerminals.find(t => t.id === activeTabId));
	let activeVm = $derived(activeTerminal?.host || activeTerminal?.vmName);
	let sftpInitialPath = $derived(
		activeTerminal?.slotNumber != null
			? `/home/octo/tentacle/slot${activeTerminal.slotNumber}/log`
			: '/'
	);

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			if (isFullscreen) {
				isFullscreen = false;
			} else {
				handleClose();
			}
			e.preventDefault();
		}
	}
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open && openTerminals.length > 0}
	<!-- Wrapper that handles both modes -->
	<div
		class={isFullscreen
			? 'fixed inset-0 z-[100] bg-background flex flex-col'
			: 'fixed inset-0 z-50 flex items-center justify-center'}
	>
		<!-- Backdrop for dialog mode -->
		{#if !isFullscreen}
			<div
				class="absolute inset-0 bg-black/80"
				onclick={handleClose}
				role="button"
				tabindex="-1"
				onkeydown={(e) => e.key === 'Enter' && handleClose()}
			></div>
		{/if}

		<!-- Main container -->
		<div
			class={isFullscreen
				? 'flex flex-col w-full h-full'
				: 'relative flex flex-col max-w-6xl w-[95vw] h-[85vh] bg-background rounded-lg shadow-lg border'}
		>
			<!-- Header with tabs -->
			<div class="flex items-center bg-muted/50 border-b shrink-0 {isFullscreen ? '' : 'rounded-t-lg'} overflow-hidden">
				<div class="flex items-center overflow-x-auto flex-1">
					{#each openTerminals as terminal (terminal.id)}
						<div
							class="group flex items-center gap-2 px-3 py-2 text-sm border-r transition-colors shrink-0 cursor-pointer
								{activeTabId === terminal.id
								? 'bg-primary text-primary-foreground'
								: 'text-muted-foreground hover:bg-background/50'}"
							onclick={() => (activeTabId = terminal.id)}
							role="tab"
							tabindex="0"
							onkeydown={(e) => e.key === 'Enter' && (activeTabId = terminal.id)}
						>
							<TerminalIcon class="size-3.5" />
							<span class="font-medium">{terminal.slotName}</span>
							{#if openTerminals.length > 1}
								<button
									class="ml-1 p-0.5 rounded hover:bg-white/20 transition-colors opacity-0 group-hover:opacity-100"
									onclick={(e) => {
										e.stopPropagation();
										handleCloseTab(terminal.id);
									}}
									title="Close tab"
								>
									<XIcon class="size-3" />
								</button>
							{/if}
						</div>
					{/each}
				</div>

				<!-- Toolbar -->
				<div class="flex items-center gap-1 px-2 shrink-0">
					<button
						class="p-1.5 rounded hover:bg-muted transition-colors {showSftp ? 'bg-primary/10 text-primary' : ''}"
						onclick={() => (showSftp = !showSftp)}
						title={showSftp ? 'Close file explorer' : 'Open file explorer'}
					>
						<FolderIcon class="size-4" />
					</button>
					<button
						class="p-1.5 rounded hover:bg-muted transition-colors"
						onclick={toggleFullscreen}
						title={isFullscreen ? 'Exit fullscreen' : 'Fullscreen'}
					>
						{#if isFullscreen}
							<MinimizeIcon class="size-4" />
						{:else}
							<MaximizeIcon class="size-4" />
						{/if}
					</button>
					<button
						class="p-1.5 rounded hover:bg-destructive/20 hover:text-destructive transition-colors"
						onclick={handleClose}
						title="Close all"
					>
						<XIcon class="size-4" />
					</button>
				</div>
			</div>

			<!-- Terminal Content + SFTP Panel -->
			<div class="flex-1 min-h-0 flex">
				<!-- Terminal area -->
				<div class="flex-1 min-h-0 min-w-0 relative bg-black">
					{#each openTerminals as terminal (terminal.id)}
						<div
							class="absolute inset-0 {activeTabId === terminal.id ? 'visible' : 'invisible'}"
						>
							<XtermClient
								bind:this={clientRefs[terminal.id]}
								vm={terminal.vmName}
								host={terminal.host}
								protocol="ssh"
								sessionId={terminal.id}
								onConnect={() => handleConnect(terminal)}
								onDisconnect={() => handleDisconnect(terminal.id)}
								onError={(msg) => handleError(terminal.id, msg)}
							/>
						</div>
					{/each}
				</div>

				<!-- SFTP Side Panel -->
				{#if showSftp && activeVm}
					<SftpPanel vm={activeVm} initialPath={sftpInitialPath} onClose={() => (showSftp = false)} />
				{/if}
			</div>

			<!-- Broadcast Command Input -->
			{#if openTerminals.length > 1}
				<div class="border-t bg-muted/30 p-3 shrink-0 {isFullscreen ? '' : 'rounded-b-lg'}">
					<div class="flex items-center gap-2">
						<div class="flex items-center gap-2 text-xs text-muted-foreground shrink-0">
							<SendIcon class="size-3.5" />
							<span>Broadcast to all ({openTerminals.length})</span>
						</div>
						<input
							type="text"
							bind:value={broadcastCommand}
							placeholder="Type command and press Enter..."
							class="flex-1 px-3 py-1.5 text-sm rounded-md border bg-background focus:outline-none focus:ring-2 focus:ring-primary"
							onkeydown={(e) => {
								if (e.key === 'Enter') {
									e.preventDefault();
									sendBroadcastCommand();
								}
							}}
						/>
						<button
							class="px-3 py-1.5 text-sm rounded-md bg-primary text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-50"
							onclick={sendBroadcastCommand}
							disabled={!broadcastCommand.trim()}
						>
							Send
						</button>
					</div>
				</div>
			{/if}
		</div>
	</div>
{/if}
