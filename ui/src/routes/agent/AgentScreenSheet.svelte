<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import { getScreenWebSocketUrl, listUiElements, type UIElement } from '$lib/api/agent.js';
	import { onDestroy } from 'svelte';
	import { toast } from 'svelte-sonner';
	import JMuxer from 'jmuxer';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import HomeIcon from '@lucide/svelte/icons/house';
	import LayoutGridIcon from '@lucide/svelte/icons/layout-grid';
	import XIcon from '@lucide/svelte/icons/x';
	import MousePointerClickIcon from '@lucide/svelte/icons/mouse-pointer-click';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';

	// tap_element 스텝을 만들 셀렉터. 부모(agent 페이지)가 캔버스에 블록으로 추가한다.
	export interface SelectedElement {
		resourceId: string;
		text: string;
		contentDesc: string;
		x: number;  // 폴백 좌표 (요소 중심, 디바이스 픽셀)
		y: number;
	}

	interface Props {
		open: boolean;
		serverId: number | null;
		deviceId: string | null;
		// 설정되면 "요소 선택 모드" 토글이 나타나고, 요소 클릭 시 호출된다.
		onSelectElement?: (el: SelectedElement) => void;
	}

	let { open = $bindable(), serverId, deviceId, onSelectElement }: Props = $props();

	// ── 요소 선택 모드 (요소 기반 시나리오 빌더) ──
	let elementMode = $state(false);
	let uiElements = $state<UIElement[]>([]);
	let elementsDeviceWidth = $state(0);
	let elementsDeviceHeight = $state(0);
	let loadingElements = $state(false);
	// hover 하이라이트 연동 — 오버레이 박스 ↔ 리스트 항목이 같은 인덱스로 강조된다.
	let hoveredIdx = $state<number | null>(null);
	// 리스트 패널 검색어
	let elementFilter = $state('');

	// 요소 하나의 대표 셀렉터 라벨 (재생 시 실제 매칭될 우선순위: id → text → desc).
	function elementLabel(el: UIElement): string {
		return el.text || el.contentDesc || el.resourceId || '(라벨 없음)';
	}
	// 재생 시 어떤 셀렉터로 매칭되는지 사람이 읽을 설명.
	function selectorKind(el: UIElement): string {
		if (el.resourceId) return 'id';
		if (el.text) return 'text';
		if (el.contentDesc) return 'desc';
		return '좌표';
	}

	// 검색어로 필터링된 요소 목록 (원본 인덱스 유지 — 하이라이트 연동용).
	let filteredElements = $derived(
		uiElements
			.map((el, idx) => ({ el, idx }))
			.filter(({ el }) => {
				if (!elementFilter.trim()) return true;
				const q = elementFilter.toLowerCase();
				return (
					el.text.toLowerCase().includes(q) ||
					el.contentDesc.toLowerCase().includes(q) ||
					el.resourceId.toLowerCase().includes(q)
				);
			})
	);

	async function loadUiElements() {
		if (serverId == null || !deviceId) return;
		loadingElements = true;
		try {
			const res = await listUiElements(serverId, deviceId, true);
			uiElements = res.elements ?? [];
			elementsDeviceWidth = res.deviceWidth || deviceWidth;
			elementsDeviceHeight = res.deviceHeight || deviceHeight;
			hoveredIdx = null;
			if (uiElements.length === 0) {
				toast.info('클릭 가능한 요소를 찾지 못했습니다 (게임/DRM 화면일 수 있음)');
			}
		} catch (err) {
			toast.error('요소 목록을 가져오지 못했습니다');
			uiElements = [];
		} finally {
			loadingElements = false;
		}
	}

	function toggleElementMode() {
		elementMode = !elementMode;
		if (elementMode) {
			loadUiElements();
		} else {
			uiElements = [];
			hoveredIdx = null;
			elementFilter = '';
		}
	}

	// 요소 bounds(디바이스 픽셀) → 화면 오버레이 박스 스타일 (getVideoRect 레터박스 보정 기반).
	function elementBoxStyle(el: UIElement): string {
		const vr = getVideoRect();
		const dw = elementsDeviceWidth || deviceWidth;
		const dh = elementsDeviceHeight || deviceHeight;
		if (!vr || dw <= 0 || dh <= 0) return 'display:none';
		const [x1, y1, x2, y2] = el.bounds;
		const scaleX = vr.width / dw;
		const scaleY = vr.height / dh;
		// 오버레이 컨테이너는 video 와 동일한 박스에 겹쳐 있으므로 vr 기준 상대좌표.
		const left = (x1 * scaleX);
		const top = (y1 * scaleY);
		const w = (x2 - x1) * scaleX;
		const h = (y2 - y1) * scaleY;
		return `left:${left}px; top:${top}px; width:${w}px; height:${h}px;`;
	}

	// 요소 면적(디바이스 픽셀). 겹친 박스 중 최소 면적 우선 선택에 사용.
	function elementArea(el: UIElement): number {
		const [x1, y1, x2, y2] = el.bounds;
		return Math.max(0, x2 - x1) * Math.max(0, y2 - y1);
	}

	// 오버레이 레이어 클릭 → 클릭 지점(디바이스 픽셀)을 포함하는 요소 중 가장 작은 것 선택.
	// 겹쳐도 사용자가 조준한 작은 버튼이 잡히도록 한다.
	function onOverlayClick(e: MouseEvent) {
		const vr = getVideoRect();
		const dw = elementsDeviceWidth || deviceWidth;
		const dh = elementsDeviceHeight || deviceHeight;
		if (!vr || dw <= 0 || dh <= 0) return;
		// 화면 좌표 → 디바이스 픽셀
		const dx = ((e.clientX - vr.left) / vr.width) * dw;
		const dy = ((e.clientY - vr.top) / vr.height) * dh;

		let best: UIElement | null = null;
		let bestArea = Infinity;
		for (const el of uiElements) {
			const [x1, y1, x2, y2] = el.bounds;
			if (dx >= x1 && dx <= x2 && dy >= y1 && dy <= y2) {
				const area = elementArea(el);
				if (area < bestArea) {
					best = el;
					bestArea = area;
				}
			}
		}
		if (best) pickElement(best);
	}

	function pickElement(el: UIElement) {
		onSelectElement?.({
			resourceId: el.resourceId,
			text: el.text,
			contentDesc: el.contentDesc,
			x: el.centerX,
			y: el.centerY
		});
		toast.success(`요소 추가: ${selectorKind(el)}=${elementLabel(el)}`);
	}

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
	<Sheet.Content side="right" class="{elementMode ? 'w-[640px]' : 'w-[400px]'} flex flex-col max-h-[100dvh] transition-[width]">
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
				<div class="flex-1 flex w-full gap-2 overflow-hidden" style="max-height: calc(100vh - 10rem);">
					<!-- Video element (요소 선택 모드일 때 오버레이 박스 겹침) -->
					<div class="relative flex-1 flex items-center justify-center overflow-hidden">
						<!-- svelte-ignore a11y_media_has_caption -->
						<video
							id="agent-screen-video"
							bind:this={videoEl}
							class="max-w-full max-h-full border rounded bg-black {elementMode ? 'cursor-crosshair' : 'cursor-pointer'}"
							style="aspect-ratio: {deviceWidth}/{deviceHeight};"
							autoplay
							muted
							playsinline
							onmousedown={elementMode ? undefined : handleMouseDown}
							onmouseup={elementMode ? undefined : handleMouseUp}
							onmousemove={elementMode ? undefined : handleMouseMove}
							onwheel={elementMode ? undefined : handleWheel}
							oncontextmenu={(e) => e.preventDefault()}
						></video>

						{#if elementMode}
							<!-- 요소 오버레이 레이어: video 레터박스 rect 기준 절대배치 -->
							{@const vr = getVideoRect()}
							{#if vr}
								{@const wrap = videoEl?.parentElement?.getBoundingClientRect()}
								<!-- 하이라이트 박스 (비인터랙티브) — hover 된 것만 진하게 -->
								<div
									class="absolute pointer-events-none"
									style="left:{vr.left - (wrap?.left ?? 0)}px; top:{vr.top - (wrap?.top ?? 0)}px; width:{vr.width}px; height:{vr.height}px;"
								>
									{#each uiElements as el, i (i)}
										<div
											class="absolute rounded-sm border-2 transition-colors {hoveredIdx === i
												? 'border-fuchsia-500 bg-fuchsia-400/30'
												: 'border-fuchsia-500/40 bg-fuchsia-400/5'}"
											style={elementBoxStyle(el)}
										></div>
									{/each}
									{#if hoveredIdx !== null && uiElements[hoveredIdx]}
										<!-- hover 툴팁: 셀렉터 종류 + 값 -->
										{@const he = uiElements[hoveredIdx]}
										<div
											class="absolute z-10 pointer-events-none rounded bg-black/85 text-white text-[10px] px-1.5 py-1 max-w-[200px] leading-tight"
											style="left:{(he.bounds[0] * vr.width) / (elementsDeviceWidth || deviceWidth)}px; top:{Math.max(0, (he.bounds[1] * vr.height) / (elementsDeviceHeight || deviceHeight) - 26)}px;"
										>
											<span class="text-fuchsia-300">{selectorKind(he)}</span> · {elementLabel(he)}
										</div>
									{/if}
								</div>
								<!-- 클릭 캐처 레이어 — 클릭 지점의 최소 면적 박스 선택 (오조준 방지) -->
								<button
									type="button"
									aria-label="요소 선택 오버레이"
									class="absolute cursor-crosshair"
									style="left:{vr.left - (wrap?.left ?? 0)}px; top:{vr.top - (wrap?.top ?? 0)}px; width:{vr.width}px; height:{vr.height}px; background:transparent;"
									onclick={onOverlayClick}
								></button>
							{/if}
						{/if}
					</div>

					{#if elementMode}
						<!-- 사이드 요소 리스트 패널 -->
						<div class="w-52 shrink-0 flex flex-col border rounded bg-background/50 overflow-hidden">
							<div class="p-1.5 border-b space-y-1">
								<div class="flex items-center justify-between">
									<span class="text-[10px] font-medium text-muted-foreground">요소 {uiElements.length}개</span>
									<button
										onclick={loadUiElements}
										disabled={loadingElements}
										class="inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] hover:bg-muted disabled:opacity-50"
										title="요소 목록 새로고침"
									>
										<RefreshCwIcon class="size-3 {loadingElements ? 'animate-spin' : ''}" /> 새로고침
									</button>
								</div>
								<input
									bind:value={elementFilter}
									placeholder="검색 (text/id/desc)"
									class="w-full border rounded px-1.5 py-1 text-[10px] bg-background"
								/>
							</div>
							<div class="flex-1 overflow-y-auto p-1 space-y-0.5">
								{#each filteredElements as { el, idx } (idx)}
									<button
										type="button"
										class="w-full text-left rounded px-1.5 py-1 text-[10px] leading-tight transition-colors {hoveredIdx === idx
											? 'bg-fuchsia-100 dark:bg-fuchsia-950'
											: 'hover:bg-muted'}"
										onmouseenter={() => (hoveredIdx = idx)}
										onmouseleave={() => (hoveredIdx = null)}
										onclick={() => pickElement(el)}
									>
										<div class="flex items-center gap-1">
											<span class="shrink-0 rounded bg-fuchsia-500/15 text-fuchsia-600 dark:text-fuchsia-400 px-1 text-[8px] font-medium">{selectorKind(el)}</span>
											<span class="truncate font-medium">{elementLabel(el)}</span>
										</div>
										{#if el.resourceId && (el.text || el.contentDesc)}
											<div class="truncate text-muted-foreground text-[9px] font-mono">{el.resourceId.split('/').pop()}</div>
										{/if}
									</button>
								{:else}
									<p class="text-[10px] text-muted-foreground text-center py-4">
										{loadingElements ? '불러오는 중...' : elementFilter ? '검색 결과 없음' : '요소 없음'}
									</p>
								{/each}
							</div>
						</div>
					{/if}
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
					{#if onSelectElement}
						<div class="w-px h-4 bg-border"></div>
						<button
							onclick={toggleElementMode}
							class="inline-flex items-center gap-1 rounded border px-3 py-1.5 text-xs transition-colors {elementMode ? 'bg-fuchsia-600 text-white border-fuchsia-600' : 'hover:bg-muted'}"
							title="요소 선택 모드: 화면에서 요소를 클릭해 tap_element 블록을 추가"
						>
							<MousePointerClickIcon class="size-3.5" /> 요소 선택
						</button>
					{/if}
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
