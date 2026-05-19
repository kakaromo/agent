<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import { getScreenWebSocketUrl } from '$lib/api/agent.js';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';
	import JMuxer from 'jmuxer';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import HomeIcon from '@lucide/svelte/icons/house';
	import LayoutGridIcon from '@lucide/svelte/icons/layout-grid';
	import XIcon from '@lucide/svelte/icons/x';

	interface Props {
		open: boolean;
		serverId: number | null;
		deviceId: string | null;
	}

	let { open = $bindable(), serverId, deviceId }: Props = $props();

	let videoEl: HTMLVideoElement;
	let ws: WebSocket | null = null;
	let jmuxer: JMuxer | null = null;
	let connected = $state(false);
	let connecting = $state(false);
	let deviceInfo = $state<{ serial?: string; width?: number; height?: number; name?: string; message?: string } | null>(null);
	let deviceWidth = $state(720);
	let deviceHeight = $state(1280);

	let feedReady = false;
	let pendingFrames: Uint8Array[] = [];
	let lastConfigPacket: Uint8Array | null = null; // SPS/PPS — needed to reinit decoder

	// Track which device we're connected to — avoid reconnecting same device
	let connectedDeviceKey: string | null = null;

	$effect(() => {
		if (open && serverId != null && deviceId) {
			const key = `${serverId}:${deviceId}`;
			if (connectedDeviceKey !== key) {
				// Different device or first connect — start new connection
				connect();
			} else if (connected && !jmuxer) {
				// Same device, connection alive but jmuxer destroyed (sheet was closed) — reinit
				initJMuxer();
				// Request cached config+keyframe from server to reinit decoder
				requestSync();
			}
		}
	});

	// When sheet closes, destroy jmuxer (video element will unmount) but keep WebSocket alive
	$effect(() => {
		if (!open && jmuxer) {
			try { jmuxer.destroy(); } catch { /* ignore */ }
			jmuxer = null;
			feedReady = false;
			pendingFrames = [];
		}
	});

	/** Check if H.264 data contains SPS NAL (type 7) — indicates config/codec init packet */
	function isConfigPacket(data: Uint8Array): boolean {
		for (let i = 0; i < data.length - 4; i++) {
			if (data[i] === 0 && data[i + 1] === 0 && data[i + 2] === 0 && data[i + 3] === 1) {
				if (i + 4 < data.length && (data[i + 4] & 0x1f) === 7) return true;
			} else if (data[i] === 0 && data[i + 1] === 0 && data[i + 2] === 1) {
				if (i + 3 < data.length && (data[i + 3] & 0x1f) === 7) return true;
			}
		}
		return false;
	}

	function concatUint8Arrays(arrays: Uint8Array[]): Uint8Array {
		const totalLen = arrays.reduce((sum, a) => sum + a.length, 0);
		const result = new Uint8Array(totalLen);
		let offset = 0;
		for (const a of arrays) {
			result.set(a, offset);
			offset += a.length;
		}
		return result;
	}

	function connect() {
		if (serverId == null || !deviceId) return;
		disconnect();
		connecting = true;
		deviceInfo = null;

		const url = getScreenWebSocketUrl(serverId, deviceId);
		ws = new WebSocket(url);
		ws.binaryType = 'arraybuffer';
		connectedDeviceKey = `${serverId}:${deviceId}`;

		ws.onopen = () => {
			connecting = false;
			connected = true;
			initJMuxer();
		};

		ws.onmessage = (e) => {
			if (typeof e.data === 'string') {
				try {
					const msg = JSON.parse(e.data);
					if (msg.type === 'info') {
						deviceInfo = msg;
						deviceWidth = msg.width || 720;
						deviceHeight = msg.height || 1280;
						toast.success(msg.message || '화면 연결됨');
					} else if (msg.type === 'error') {
						toast.error(msg.message || '연결 오류');
					}
				} catch { /* ignore */ }
			} else if (e.data instanceof ArrayBuffer) {
				const data = new Uint8Array(e.data);
				if (data.length === 0) return;

				// Save config packet (SPS/PPS) — needed to reinit decoder after sheet reopen
				if (isConfigPacket(data)) {
					lastConfigPacket = data;
				}

				if (!jmuxer || !feedReady) {
					pendingFrames.push(data);
					// Cap buffer to avoid memory leak when sheet is closed
					if (pendingFrames.length > 300) pendingFrames.splice(0, pendingFrames.length - 60);
					return;
				}

				try {
					jmuxer.feed({ video: data });
				} catch {
					// SourceBuffer busy, ignore
				}
			}
		};

		ws.onclose = () => {
			connected = false;
			connecting = false;
			connectedDeviceKey = null;
		};

		ws.onerror = () => {
			connected = false;
			connecting = false;
			connectedDeviceKey = null;
			toast.error('화면 연결 실패');
		};
	}

	/** Fully disconnect — called by user action (stop button) or component destroy */
	function disconnect() {
		if (ws) { ws.close(); ws = null; }
		if (jmuxer) {
			try { jmuxer.destroy(); } catch { /* ignore */ }
			jmuxer = null;
		}
		connected = false;
		connecting = false;
		feedReady = false;
		pendingFrames = [];
		lastConfigPacket = null;
		connectedDeviceKey = null;
	}

	/** Ask server to resend cached SPS/PPS + keyframe for decoder reinit */
	function requestSync() {
		if (ws && ws.readyState === WebSocket.OPEN) {
			ws.send(JSON.stringify({ type: 'requestSync' }));
		}
	}

	function stopAndClose() {
		disconnect();
		open = false;
	}

	function initJMuxer() {
		requestAnimationFrame(() => {
			if (!videoEl) {
				console.warn('[Screen] video element not ready, retrying...');
				setTimeout(initJMuxer, 100);
				return;
			}

			jmuxer = new JMuxer({
				node: 'agent-screen-video',
				mode: 'video',
				flushingTime: 1,
				fps: 30,
				debug: false,
				clearBuffer: true,
				onReady: () => {
					console.log('[Screen] JMuxer ready');
					feedReady = true;
					// Feed saved SPS/PPS first so decoder can initialize
					if (lastConfigPacket && pendingFrames.length === 0) {
						try { jmuxer?.feed({ video: lastConfigPacket }); } catch {}
					}
					if (pendingFrames.length > 0) {
						// Ensure config packet is at the front if not already there
						if (lastConfigPacket && !isConfigPacket(pendingFrames[0])) {
							pendingFrames.unshift(lastConfigPacket);
						}
						const merged = concatUint8Arrays(pendingFrames);
						pendingFrames = [];
						try { jmuxer?.feed({ video: merged }); } catch {}
					}
					videoEl?.play().catch(() => {});
				},
				onError: (err: unknown) => {
					console.warn('[Screen] jmuxer decode error:', err);
				}
			});

			if (videoEl) {
				videoEl.addEventListener('pause', () => {
					videoEl?.play().catch(() => {});
				});
			}
		});
	}

	// ── Input events ──

	function getVideoRect(): { left: number; top: number; width: number; height: number } | null {
		if (!videoEl) return null;
		const rect = videoEl.getBoundingClientRect();
		const videoRatio = deviceWidth / deviceHeight;
		const containerRatio = rect.width / rect.height;

		let vLeft = rect.left, vTop = rect.top, vWidth = rect.width, vHeight = rect.height;
		if (containerRatio > videoRatio) {
			vWidth = rect.height * videoRatio;
			vLeft = rect.left + (rect.width - vWidth) / 2;
		} else {
			vHeight = rect.width / videoRatio;
			vTop = rect.top + (rect.height - vHeight) / 2;
		}
		return { left: vLeft, top: vTop, width: vWidth, height: vHeight };
	}

	function sendTouch(action: number, e: MouseEvent) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		const vr = getVideoRect();
		if (!vr) return;

		const x = Math.max(0, Math.min(1, (e.clientX - vr.left) / vr.width));
		const y = Math.max(0, Math.min(1, (e.clientY - vr.top) / vr.height));

		ws.send(JSON.stringify({
			type: 'touch',
			touch: { action, x, y, width: deviceWidth, height: deviceHeight, pressure: 1.0, pointer_id: 0 }
		}));
	}

	function handleMouseDown(e: MouseEvent) { e.preventDefault(); sendTouch(0, e); }
	function handleMouseUp(e: MouseEvent) { sendTouch(1, e); }
	function handleMouseMove(e: MouseEvent) {
		if (e.buttons > 0) sendTouch(2, e);
	}

	function handleWheel(e: WheelEvent) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		e.preventDefault();
		const vr = getVideoRect();
		if (!vr) return;

		const x = Math.max(0, Math.min(1, (e.clientX - vr.left) / vr.width));
		const y = Math.max(0, Math.min(1, (e.clientY - vr.top) / vr.height));

		ws.send(JSON.stringify({
			type: 'scroll',
			scroll: { x, y, width: deviceWidth, height: deviceHeight, h_scroll: 0, v_scroll: e.deltaY > 0 ? -1 : 1 }
		}));
	}

	function sendKey(keycode: number) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(JSON.stringify({ type: 'key', key: { action: 0, keycode, repeat: 0, meta_state: 0 } }));
		setTimeout(() => {
			ws?.send(JSON.stringify({ type: 'key', key: { action: 1, keycode, repeat: 0, meta_state: 0 } }));
		}, 50);
	}

	function sendBack() {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(JSON.stringify({ type: 'back' }));
	}

	onDestroy(() => disconnect());
</script>

<Sheet.Root bind:open>
	<Sheet.Content side="right" class="w-[400px] flex flex-col max-h-[100dvh]">
		<Sheet.Header>
			<Sheet.Title class="text-sm flex items-center gap-2">
				디바이스 화면
				{#if connected}
					<span class="size-1.5 rounded-full bg-green-500"></span>
				{/if}
			</Sheet.Title>
			<Sheet.Description class="text-xs font-mono">
				{deviceId ?? ''}
				{#if deviceInfo?.serial} · {deviceInfo.serial}{/if}
				{#if deviceInfo?.name} ({deviceInfo.name}){/if}
			</Sheet.Description>
		</Sheet.Header>

		<div class="flex-1 flex flex-col items-center gap-2 py-2 overflow-hidden">
			{#if connecting}
				<div class="flex-1 flex items-center justify-center">
					<LoaderIcon class="size-6 animate-spin text-muted-foreground" />
					<span class="ml-2 text-xs text-muted-foreground">연결 중... (2~3초 소요)</span>
				</div>
			{:else if connected}
				<!-- Video element -->
				<div
					class="flex-1 flex items-center justify-center w-full overflow-hidden"
					style="max-height: calc(100vh - 10rem);"
				>
					<!-- svelte-ignore a11y_media_has_caption -->
					<video
						id="agent-screen-video"
						bind:this={videoEl}
						class="max-w-full max-h-full border rounded bg-black cursor-pointer"
						style="aspect-ratio: {deviceWidth}/{deviceHeight};"
						autoplay
						muted
						playsinline
						onmousedown={handleMouseDown}
						onmouseup={handleMouseUp}
						onmousemove={handleMouseMove}
						onwheel={handleWheel}
						oncontextmenu={(e) => e.preventDefault()}
					></video>
				</div>

				<!-- Soft buttons + Stop -->
				<div class="flex items-center gap-2 shrink-0">
					<button onclick={sendBack} class="inline-flex items-center gap-1 rounded border px-3 py-1.5 text-xs hover:bg-muted">
						<ArrowLeftIcon class="size-3.5" /> Back
					</button>
					<button onclick={() => sendKey(3)} class="inline-flex items-center gap-1 rounded border px-3 py-1.5 text-xs hover:bg-muted">
						<HomeIcon class="size-3.5" /> Home
					</button>
					<button onclick={() => sendKey(187)} class="inline-flex items-center gap-1 rounded border px-3 py-1.5 text-xs hover:bg-muted">
						<LayoutGridIcon class="size-3.5" /> Recent
					</button>
					<div class="w-px h-4 bg-border"></div>
					<button onclick={stopAndClose} class="inline-flex items-center gap-1 rounded border border-destructive/50 px-3 py-1.5 text-xs text-destructive hover:bg-destructive/10">
						<XIcon class="size-3.5" /> 연결 끊기
					</button>
				</div>
			{:else}
				<div class="flex-1 flex items-center justify-center">
					<div class="text-center text-xs text-muted-foreground">
						<p>화면 연결이 끊겼습니다</p>
						<button onclick={connect} class="mt-2 rounded border px-3 py-1 text-xs hover:bg-muted">
							재연결
						</button>
					</div>
				</div>
			{/if}
		</div>
	</Sheet.Content>
</Sheet.Root>
