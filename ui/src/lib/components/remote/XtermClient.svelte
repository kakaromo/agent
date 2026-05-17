<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { Terminal } from '@xterm/xterm';
	import { FitAddon } from '@xterm/addon-fit';
	import { WebLinksAddon } from '@xterm/addon-web-links';
	import '@xterm/xterm/css/xterm.css';

	interface Props {
		vm: string;
		protocol: 'ssh';
		sessionId?: string;
		host?: string;
		onConnect?: () => void;
		onDisconnect?: () => void;
		onError?: (message: string) => void;
	}

	let { vm, protocol, sessionId, host, onConnect, onDisconnect, onError }: Props = $props();

	let containerElement = $state<HTMLElement | null>(null);
	let connected = $state(false);
	let connecting = $state(true);
	let errorMessage = $state<string | null>(null);

	let term: Terminal | null = null;
	let fitAddon: FitAddon | null = null;
	let ws: WebSocket | null = null;
	let resizeObserver: ResizeObserver | null = null;

	function connect() {
		if (!containerElement) return;

		connecting = true;
		errorMessage = null;

		try {
			term = new Terminal({
				cursorBlink: true,
				fontFamily: 'D2Coding, "D2 Coding", monospace',
				fontSize: 14,
				theme: {
					background: '#1a1a2e',
					foreground: '#e0e0e0',
					cursor: '#e0e0e0',
					selectionBackground: '#44475a',
					black: '#000000',
					red: '#ff5555',
					green: '#50fa7b',
					yellow: '#f1fa8c',
					blue: '#bd93f9',
					magenta: '#ff79c6',
					cyan: '#8be9fd',
					white: '#f8f8f2'
				},
				allowProposedApi: true
			});

			fitAddon = new FitAddon();
			term.loadAddon(fitAddon);
			term.loadAddon(new WebLinksAddon());

			term.open(containerElement);
			fitAddon.fit();

			const cols = term.cols;
			const rows = term.rows;

			// WebSocket connection
			const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
			const target = host ? `host=${encodeURIComponent(host)}` : `vm=${encodeURIComponent(vm)}`;
			const url = `${wsProtocol}//${window.location.host}/api/terminal/ssh?${target}&cols=${cols}&rows=${rows}`;

			ws = new WebSocket(url);

			ws.onopen = () => {
				connecting = false;
				connected = true;
				onConnect?.();
				containerElement?.focus();
			};

			ws.onmessage = (event) => {
				term?.write(event.data);
			};

			ws.onclose = () => {
				connected = false;
				connecting = false;
				onDisconnect?.();
			};

			ws.onerror = () => {
				const msg = 'WebSocket connection error';
				errorMessage = msg;
				connecting = false;
				connected = false;
				onError?.(msg);
			};

			// Terminal input → WebSocket
			term.onData((data) => {
				if (ws?.readyState === WebSocket.OPEN) {
					ws.send(data);
				}
			});

			// Handle Cmd+C (copy when selection exists) and Cmd+V (paste)
			term.attachCustomKeyEventHandler((event) => {
				if ((event.metaKey || event.ctrlKey) && event.key === 'c' && term?.hasSelection()) {
					// Allow browser native copy
					return false;
				}
				if ((event.metaKey || event.ctrlKey) && event.key === 'v') {
					// Handle paste
					navigator.clipboard?.readText().then((text) => {
						if (text && ws?.readyState === WebSocket.OPEN) {
							ws.send(text);
						}
					}).catch(() => {});
					return false;
				}
				return true;
			});

			// Resize handling
			resizeObserver = new ResizeObserver(() => {
				if (!connected || !fitAddon || !term) return;
				try {
					fitAddon.fit();
					if (ws?.readyState === WebSocket.OPEN) {
						ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
					}
				} catch {
					// Ignore resize errors during cleanup
				}
			});
			resizeObserver.observe(containerElement);

		} catch (e: any) {
			const msg = e.message || 'Failed to initialize terminal';
			errorMessage = msg;
			connecting = false;
			onError?.(msg);
		}
	}

	function disconnect() {
		resizeObserver?.disconnect();
		resizeObserver = null;

		if (ws) {
			try { ws.close(); } catch {}
			ws = null;
		}

		if (term) {
			try { term.dispose(); } catch {}
			term = null;
		}

		fitAddon = null;
		connected = false;
		connecting = false;
	}

	onMount(() => {
		connect();
	});

	onDestroy(() => {
		disconnect();
	});

	export function close() {
		disconnect();
	}

	export function sendText(text: string) {
		if (ws?.readyState === WebSocket.OPEN) {
			ws.send(text);
		}
	}

	export function sendEnter() {
		if (ws?.readyState === WebSocket.OPEN) {
			ws.send('\r');
		}
	}
</script>

<div class="flex flex-col h-full w-full relative">
	<div
		bind:this={containerElement}
		class="flex-1 overflow-hidden bg-[#1a1a2e]"
		class:hidden={!!(connecting && !term) || !!errorMessage}
	></div>

	{#if connecting && !term}
		<div class="absolute inset-0 flex items-center justify-center bg-black/90">
			<div class="text-center text-white">
				<span class="dsy-loading dsy-loading-spinner dsy-loading-lg"></span>
				<p class="mt-2">Connecting to {vm} (SSH)...</p>
			</div>
		</div>
	{:else if errorMessage}
		<div class="absolute inset-0 flex items-center justify-center bg-black/90">
			<div class="text-center text-red-400">
				<p class="text-lg font-semibold">Connection Failed</p>
				<p class="mt-1 text-sm">{errorMessage}</p>
				<button
					class="mt-4 px-4 py-2 bg-primary text-primary-foreground rounded-md hover:bg-primary/90"
					onclick={() => { disconnect(); connect(); }}
				>
					Retry
				</button>
			</div>
		</div>
	{/if}
</div>
