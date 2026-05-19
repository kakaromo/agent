<script lang="ts">
	import XtermClient from '$lib/components/remote/XtermClient.svelte';
	import TerminalIcon from '@lucide/svelte/icons/terminal';
	import XIcon from '@lucide/svelte/icons/x';
	import SendIcon from '@lucide/svelte/icons/send';
	import MaximizeIcon from '@lucide/svelte/icons/maximize-2';
	import MinimizeIcon from '@lucide/svelte/icons/minimize-2';

	interface TerminalInfo {
		id: string;
		vmName: string;
		slotName: string;
		usbId?: string;
		slotNumber?: number;
		host?: string;
		// adb 모드: agent 의 ADB PTY 직접 연결. usbId 우회 logic 은 skip.
		protocol?: 'ssh' | 'adb';
		deviceId?: string;
	}

	interface Props {
		open: boolean;
		terminals: TerminalInfo[];
		onClose: () => void;
	}

	let { open = $bindable(), terminals, onClose }: Props = $props();

	// Internal state for managing open terminals.
	// 세션 유지 정책: dialog 가 닫혀도 (open=false) openTerminals 와 client 인스턴스는 유지된다.
	// X 버튼 (handleCloseTab / handleCloseAll) 또는 비정상 종료(onDisconnect) 시에만 정리.
	let openTerminals = $state<TerminalInfo[]>([]);
	let activeTabId = $state<string | null>(null);
	let broadcastCommand = $state('');
	let isFullscreen = $state(false);

	// Store references to XtermClient instances
	let clientRefs: Record<string, XtermClient | undefined> = {};

	// props.terminals 에서 새 entry 가 들어오면 openTerminals 에 merge.
	// 기존 entry 는 그대로 유지 (세션 살아있음).
	$effect(() => {
		if (terminals.length === 0) return;
		const existingIds = new Set(openTerminals.map(t => t.id));
		const newOnes = terminals.filter(t => !existingIds.has(t.id));
		if (newOnes.length > 0) {
			openTerminals = [...openTerminals, ...newOnes];
		}
		// activeTab 이 비어있으면 최신 추가된 것 활성화, 아니면 그대로
		if (!activeTabId || !openTerminals.some(t => t.id === activeTabId)) {
			activeTabId = openTerminals[openTerminals.length - 1]?.id ?? null;
		}
	});

	// 사용자가 dialog 의 X 버튼 / 백드롭 클릭 시 — 단순히 dialog 만 숨김, 세션은 유지.
	function hideDialog() {
		open = false;
		isFullscreen = false;
	}

	// 모든 탭을 명시적으로 닫는 액션 — "Close all" 버튼이 호출 (활성 탭의 X 가 아닌 별도 동작).
	function closeAllSessions() {
		Object.values(clientRefs).forEach(client => client?.close());
		clientRefs = {};
		openTerminals = [];
		activeTabId = null;
		broadcastCommand = '';
		isFullscreen = false;
		open = false;
		onClose();
	}

	function handleCloseTab(id: string) {
		// 명시적 탭 close — 해당 client 정리
		clientRefs[id]?.close();
		delete clientRefs[id];

		openTerminals = openTerminals.filter(t => t.id !== id);

		if (openTerminals.length === 0) {
			closeAllSessions();
		} else if (activeTabId === id) {
			activeTabId = openTerminals[0].id;
		}
	}

	function handleConnect(terminal: TerminalInfo) {
		// adb 모드는 agent 가 직접 PTY 를 열어주므로 자동 'adb shell' 주입 불요.
		if (terminal.protocol === 'adb') return;
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
		// 비정상 종료 (디바이스 disconnect, agent restart 등) — 해당 탭 자동 정리
		console.log(`Terminal ${id} disconnected`);
		clientRefs[id]?.close();
		delete clientRefs[id];
		openTerminals = openTerminals.filter(t => t.id !== id);
		if (openTerminals.length === 0) {
			open = false;
			activeTabId = null;
			onClose();
		} else if (activeTabId === id) {
			activeTabId = openTerminals[0].id;
		}
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

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			if (isFullscreen) {
				isFullscreen = false;
			} else {
				hideDialog();
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
		<!-- Backdrop for dialog mode — 클릭 시 dialog 숨김 (세션 유지) -->
		{#if !isFullscreen}
			<div
				class="absolute inset-0 bg-black/80"
				onclick={hideDialog}
				role="button"
				tabindex="-1"
				onkeydown={(e) => e.key === 'Enter' && hideDialog()}
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
					<!-- 일반 X: dialog 만 숨김, 세션 유지 -->
					<button
						class="p-1.5 rounded hover:bg-muted transition-colors"
						onclick={hideDialog}
						title="Minimize (세션 유지)"
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
								protocol={terminal.protocol ?? 'ssh'}
								deviceId={terminal.deviceId}
								sessionId={terminal.id}
								onConnect={() => handleConnect(terminal)}
								onDisconnect={() => handleDisconnect(terminal.id)}
								onError={(msg) => handleError(terminal.id, msg)}
							/>
						</div>
					{/each}
				</div>

				<!-- SFTP Side Panel 제거 (standalone 무관) -->
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
