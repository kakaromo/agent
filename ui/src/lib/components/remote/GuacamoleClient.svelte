<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import Guacamole from 'guacamole-common-js';
	import type { Client, WebSocketTunnel, Keyboard, Mouse, Status } from 'guacamole-common-js';
	import { fetchViewerCounts } from '$lib/api/guacamole.js';
	import UsersIcon from '@lucide/svelte/icons/users';

	interface Props {
		vm: string;
		protocol: 'ssh' | 'rdp' | 'vnc';
		sessionId?: string;
		user?: string;
		onConnect?: () => void;
		onDisconnect?: () => void;
		onError?: (message: string) => void;
		onSessionLocked?: (lockedBy: string) => void;
	}

	let { vm, protocol, sessionId, user = '', onConnect, onDisconnect, onError, onSessionLocked }: Props = $props();

	let displayElement = $state<HTMLElement | null>(null);
	let containerElement = $state<HTMLElement | null>(null);
	let client: Client | null = null;
	let tunnel: WebSocketTunnel | null = null;
	let keyboard: Keyboard | null = null;
	let mouse: Mouse | null = null;
	let connected = $state(false);
	let connecting = $state(true);
	let errorMessage = $state<string | null>(null);
	let cleanupListeners: (() => void) | null = null;

	// RDP viewer count (화면 공유 시 참여자 수 표시)
	let viewerCount = $state(0);
	let viewerPollTimer: ReturnType<typeof setInterval> | null = null;

	function startViewerPolling() {
		if (protocol !== 'rdp') return;
		pollViewers();
		viewerPollTimer = setInterval(pollViewers, 5000);
	}

	function stopViewerPolling() {
		if (viewerPollTimer) {
			clearInterval(viewerPollTimer);
			viewerPollTimer = null;
		}
		viewerCount = 0;
	}

	async function pollViewers() {
		try {
			const counts = await fetchViewerCounts();
			viewerCount = counts[vm] ?? 0;
		} catch {
			// ignore
		}
	}

	function getWebSocketUrl(): string {
		// Use our backend tunnel which connects directly to guacd
		const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
		const width = containerElement?.clientWidth || 1920;
		const height = containerElement?.clientHeight || 1080;
		const dpi = 96;
		// Include sessionId to ensure each terminal gets a unique connection
		const sid = sessionId || `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
		const u = user || localStorage.getItem('portal_username') || '';
		return `${wsProtocol}//${window.location.host}/api/guacamole/tunnel?vm=${encodeURIComponent(vm)}&protocol=${encodeURIComponent(protocol)}&width=${width}&height=${height}&dpi=${dpi}&sessionId=${encodeURIComponent(sid)}&user=${encodeURIComponent(u)}`;
	}

	function connect() {
		if (!containerElement) return;

		connecting = true;
		errorMessage = null;

		try {
			const wsUrl = getWebSocketUrl();
			tunnel = new Guacamole.WebSocketTunnel(wsUrl);
			client = new Guacamole.Client(tunnel);

			// Handle state changes
			client.onstatechange = (state: number) => {
				switch (state) {
					case 0: // IDLE
						break;
					case 1: // CONNECTING
						connecting = true;
						break;
					case 2: // WAITING
						break;
					case 3: // CONNECTED
						connecting = false;
						connected = true;
						setupInput();
						resizeDisplay();
						startViewerPolling();
						onConnect?.();
						break;
					case 4: // DISCONNECTING
						break;
					case 5: // DISCONNECTED
						connected = false;
						connecting = false;
						onDisconnect?.();
						break;
				}
			};

			// Handle errors
			client.onerror = (error: Status) => {
				const msg = error.message || 'Connection error';
				// SESSION_LOCKED:username 패턴 감지
				if (msg.includes('SESSION_LOCKED:')) {
					const lockedBy = msg.split('SESSION_LOCKED:')[1]?.trim() ?? 'unknown';
					errorMessage = `${lockedBy}님이 사용 중입니다`;
					connecting = false;
					connected = false;
					onSessionLocked?.(lockedBy);
					return;
				}
				errorMessage = msg;
				connecting = false;
				connected = false;
				onError?.(msg);
			};

			// Get display element and add to container
			const display = client.getDisplay();
			displayElement = display.getElement();

			if (displayElement && containerElement) {
				containerElement.innerHTML = '';
				containerElement.appendChild(displayElement);
			}

			// Connect with empty data string (parameters are in WebSocket URL)
			client.connect('');
		} catch (e: any) {
			const msg = e.message || 'Failed to connect';
			errorMessage = msg;
			connecting = false;
			onError?.(msg);
		}
	}

	function scaleToFit() {
		if (!client || !containerElement) return;

		const display = client.getDisplay();
		const displayWidth = display.getWidth();
		const displayHeight = display.getHeight();

		if (displayWidth && displayHeight) {
			const containerWidth = containerElement.clientWidth;
			const containerHeight = containerElement.clientHeight;
			const scale = Math.min(containerWidth / displayWidth, containerHeight / displayHeight);
			display.scale(scale);
		}
	}

	function resizeDisplay() {
		if (!client || !containerElement) return;

		const display = client.getDisplay();

		// Send actual size to guacd so the remote session resizes
		const w = containerElement.clientWidth;
		const h = containerElement.clientHeight;
		if (w > 0 && h > 0) {
			client.sendSize(w, h);
		}

		// Scale current display to fit while waiting for the server to acknowledge the resize
		scaleToFit();

		// Listen for display size changes (server acknowledged resize) and re-scale
		display.onresize = () => {
			scaleToFit();
		};
	}

	let hasFocus = $state(false);

	// ── Clipboard ──

	/** 브라우저 클립보드를 원격 세션의 클립보드로 동기화 (RDP/VNC Ctrl+V 전 호출) */
	async function syncClipboardToRemote() {
		try {
			const text = await navigator.clipboard?.readText();
			if (text && client) {
				const stream = client.createClipboardStream('text/plain');
				const writer = new Guacamole.StringWriter(stream);
				writer.sendText(text);
				writer.sendEnd();
			}
		} catch {
			// Clipboard read may be denied — ignore
		}
	}

	/** Read browser clipboard and type it via sendText (works on SSH) */
	async function pasteFromClipboard() {
		try {
			const text = await navigator.clipboard?.readText();
			if (text) sendText(text);
		} catch {
			// Clipboard read may be denied — ignore
		}
	}

	function setupClipboard() {
		if (!client || !containerElement) return;

		// Remote → local: when the remote session copies text, update browser clipboard
		client.onclipboard = (stream: any, mimetype: string) => {
			if (mimetype !== 'text/plain') {
				stream.sendAck('Unsupported type', Guacamole.Status.Code.UNSUPPORTED);
				return;
			}
			let data = '';
			const reader = new Guacamole.StringReader(stream);
			reader.ontext = (text: string) => { data += text; };
			reader.onend = () => {
				navigator.clipboard?.writeText(data).catch(() => {});
			};
		};
	}

	function setupInput() {
		if (!displayElement || !client || !containerElement) return;

		const display = client.getDisplay();

		// Make container focusable
		containerElement.tabIndex = 0;
		containerElement.style.outline = 'none';

		// Clipboard
		setupClipboard();

		// Keyboard - bind to container element only
		keyboard = new Guacamole.Keyboard(containerElement);

		// Track Ctrl/Meta state for paste interception
		let ctrlDown = false;
		const CTRL_SYMS = new Set([0xFFE3, 0xFFE4, 0xFFE7, 0xFFE8]); // L/R Ctrl, L/R Meta

		keyboard.onkeydown = (keysym: number) => {
			if (!hasFocus) return true;

			if (CTRL_SYMS.has(keysym)) ctrlDown = true;

			// SSH: Ctrl+V 가로채서 브라우저 클립보드 → sendText로 타이핑
			// RDP/VNC: Ctrl+C/V를 그대로 원격에 전달 (내부 복사/붙여넣기 동작)
			if (protocol === 'ssh' && ctrlDown && (keysym === 0x76 || keysym === 0x56)) {
				pasteFromClipboard();
				return false;
			}

			// RDP/VNC: Ctrl+V 시 브라우저 클립보드를 원격 클립보드로 동기화 후 키 전달
			if (protocol !== 'ssh' && ctrlDown && (keysym === 0x76 || keysym === 0x56)) {
				syncClipboardToRemote().then(() => {
					client?.sendKeyEvent(1, keysym);
				});
				return false;
			}

			client?.sendKeyEvent(1, keysym);
			return false; // Prevent default
		};
		keyboard.onkeyup = (keysym: number) => {
			if (!hasFocus) return true;
			if (CTRL_SYMS.has(keysym)) ctrlDown = false;
			client?.sendKeyEvent(0, keysym);
			return false;
		};

		// Track focus state
		const onFocus = () => { hasFocus = true; };
		const onBlur = () => { hasFocus = false; };
		const onClick = () => { containerElement?.focus(); };
		containerElement.addEventListener('focus', onFocus);
		containerElement.addEventListener('blur', onBlur);
		containerElement.addEventListener('click', onClick);
		cleanupListeners = () => {
			containerElement?.removeEventListener('focus', onFocus);
			containerElement?.removeEventListener('blur', onBlur);
			containerElement?.removeEventListener('click', onClick);
		};

		// Mouse - scale coordinates according to display scale
		mouse = new Guacamole.Mouse(displayElement);
		const sendScaledMouseState = (mouseState: any) => {
			const scale = display.getScale();
			const scaledState = new Guacamole.Mouse.State(
				mouseState.x / scale,
				mouseState.y / scale,
				mouseState.left,
				mouseState.middle,
				mouseState.right,
				mouseState.up,
				mouseState.down
			);
			client?.sendMouseState(scaledState);
		};

		mouse.onmousedown = mouse.onmouseup = mouse.onmousemove = sendScaledMouseState;

		// Touch (for mobile)
		const touch = new Guacamole.Mouse.Touchpad(displayElement);
		touch.onmousedown = touch.onmouseup = touch.onmousemove = sendScaledMouseState;

		// Auto-focus on mount
		containerElement.focus();
	}

	function disconnect() {
		stopViewerPolling();
		// Clean up event listeners
		cleanupListeners?.();
		cleanupListeners = null;
		// Clean up keyboard - use reset() if available, otherwise just nullify reference
		if (keyboard) {
			try {
				(keyboard as any).reset?.();
			} catch {
				// Ignore cleanup errors
			}
			keyboard = null;
		}
		// Clean up mouse
		if (mouse) {
			mouse = null;
		}
		if (client) {
			try {
				client.disconnect();
			} catch {
				// Ignore disconnect errors
			}
		}
		// Force close WebSocket if still open
		if (tunnel) {
			try {
				const ws = (tunnel as any).socket;
				if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
					ws.close();
				}
			} catch { /* ignore */ }
		}
		client = null;
		tunnel = null;
		connected = false;
		connecting = false;
	}

	let resizeTimer: ReturnType<typeof setTimeout> | null = null;

	function handleResize() {
		if (!connected) return;

		// Immediate scale to fit so the UI doesn't look broken
		scaleToFit();

		// Debounce the actual sendSize to avoid flooding guacd during drag-resize
		if (resizeTimer) clearTimeout(resizeTimer);
		resizeTimer = setTimeout(() => {
			resizeDisplay();
		}, 300);
	}

	let resizeObserver: ResizeObserver | null = null;

	onMount(() => {
		connect();

		// Use ResizeObserver to detect container size changes (sidebar toggle, etc.)
		if (containerElement) {
			resizeObserver = new ResizeObserver(() => {
				handleResize();
			});
			resizeObserver.observe(containerElement);
		}
	});

	onDestroy(() => {
		if (resizeTimer) clearTimeout(resizeTimer);
		resizeObserver?.disconnect();
		disconnect();
	});

	// Expose disconnect function
	export function close() {
		disconnect();
	}

	// Expose function to send text as key events
	export function sendText(text: string) {
		if (!client || !connected) return;

		for (const char of text) {
			const keysym = char.charCodeAt(0);
			client.sendKeyEvent(1, keysym); // keydown
			client.sendKeyEvent(0, keysym); // keyup
		}
	}

	// Expose function to send Enter key
	export function sendEnter() {
		if (!client || !connected) return;
		client.sendKeyEvent(1, 0xFF0D); // keydown Enter
		client.sendKeyEvent(0, 0xFF0D); // keyup Enter
	}

	// Send Windows key (Super_L) press+release
	function sendWinKey() {
		if (!client || !connected) return;
		client.sendKeyEvent(1, 0xFFEB); // Super_L down
		client.sendKeyEvent(0, 0xFFEB); // Super_L up
	}

	// Send Ctrl+Alt+Del
	function sendCtrlAltDel() {
		if (!client || !connected) return;
		client.sendKeyEvent(1, 0xFFE3); // Ctrl down
		client.sendKeyEvent(1, 0xFFE9); // Alt down
		client.sendKeyEvent(1, 0xFFFF); // Delete down
		client.sendKeyEvent(0, 0xFFFF); // Delete up
		client.sendKeyEvent(0, 0xFFE9); // Alt up
		client.sendKeyEvent(0, 0xFFE3); // Ctrl up
	}
</script>

<div class="flex flex-col h-full w-full relative">
	<!-- Container is always rendered but may be hidden -->
	<div
		bind:this={containerElement}
		class="flex-1 overflow-hidden bg-black"
		class:hidden={connecting || errorMessage}
	></div>

	<!-- RDP 특수키 버튼 + Viewer count -->
	{#if connected && protocol === 'rdp'}
		<div class="absolute top-2 right-2 flex items-center gap-1.5 z-10">
			<button
				onclick={sendWinKey}
				class="px-2 py-1 rounded bg-zinc-800/80 text-white text-[10px] backdrop-blur-sm hover:bg-zinc-700/90 transition-colors shadow-sm"
				title="Windows 키"
			>
				⊞ Win
			</button>
			<button
				onclick={sendCtrlAltDel}
				class="px-2 py-1 rounded bg-zinc-800/80 text-white text-[10px] backdrop-blur-sm hover:bg-zinc-700/90 transition-colors shadow-sm"
				title="Ctrl+Alt+Del"
			>
				Ctrl+Alt+Del
			</button>
			{#if viewerCount > 1}
				<div class="flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-blue-500/80 text-white text-xs backdrop-blur-sm pointer-events-none shadow-sm">
					<UsersIcon class="size-3" />
					<span>{viewerCount}명 시청 중</span>
				</div>
			{/if}
		</div>
	{/if}

	{#if connecting}
		<div class="absolute inset-0 flex items-center justify-center bg-black/90">
			<div class="text-center text-white">
				<span class="dsy-loading dsy-loading-spinner dsy-loading-lg"></span>
				<p class="mt-2">Connecting to {vm} ({protocol.toUpperCase()})...</p>
			</div>
		</div>
	{:else if errorMessage}
		<div class="absolute inset-0 flex items-center justify-center bg-black/90">
			<div class="text-center text-red-400">
				<p class="text-lg font-semibold">Connection Failed</p>
				<p class="mt-1 text-sm">{errorMessage}</p>
				<button
					class="mt-4 px-4 py-2 bg-primary text-primary-foreground rounded-md hover:bg-primary/90"
					onclick={connect}
				>
					Retry
				</button>
			</div>
		</div>
	{/if}
</div>
