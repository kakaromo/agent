<script lang="ts">
	import {
		fetchAppMacros, createAppMacro, updateAppMacro, deleteAppMacro, duplicateAppMacro,
		listInstalledApps, replayMacro, getScreenWebSocketUrl,
		type AppMacro, type MacroEvent, type InstalledApp
	} from '$lib/api/agent.js';
	import { toast } from 'svelte-sonner';
	import { sectionLabel, captionMuted } from '$lib/styles/common.js';
	import JMuxer from 'jmuxer';
	import CircleDotIcon from '@lucide/svelte/icons/circle-dot';
	import SquareIcon from '@lucide/svelte/icons/square';
	import PlayIcon from '@lucide/svelte/icons/play';
	import SaveIcon from '@lucide/svelte/icons/save';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import CopyIcon from '@lucide/svelte/icons/copy';
	import CameraIcon from '@lucide/svelte/icons/camera';
	import ClockIcon from '@lucide/svelte/icons/clock';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import ArrowLeftIcon from '@lucide/svelte/icons/arrow-left';
	import HomeIcon from '@lucide/svelte/icons/house';
	import LayoutGridIcon from '@lucide/svelte/icons/layout-grid';
	import XIcon from '@lucide/svelte/icons/x';
	import CheckIcon from '@lucide/svelte/icons/check';
	import SearchIcon from '@lucide/svelte/icons/search';
	import SmartphoneIcon from '@lucide/svelte/icons/smartphone';
	import SunIcon from '@lucide/svelte/icons/sun';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
	import PackageIcon from '@lucide/svelte/icons/package';
	import ListIcon from '@lucide/svelte/icons/list';

	interface Props {
		serverId: number | null;
		selectedDevices: Set<string>;
	}

	let { serverId, selectedDevices }: Props = $props();

	// ── Wizard steps ──
	type WizardStep = 'list' | 'app' | 'record' | 'events' | 'save';
	let wizardStep = $state<WizardStep>('list');

	// ── Macro list ──
	let macros = $state<AppMacro[]>([]);
	let selectedMacroId = $state<number | null>(null);

	// ── App selection ──
	let installedApps = $state<InstalledApp[]>([]);
	let appsLoading = $state(false);
	let appSearch = $state('');
	let selectedApp = $state<InstalledApp | null>(null);

	const filteredApps = $derived.by(() => {
		if (!appSearch.trim()) return installedApps;
		const q = appSearch.toLowerCase();
		return installedApps.filter(a =>
			a.appName.toLowerCase().includes(q) || a.packageName.toLowerCase().includes(q)
		);
	});

	// ── Recording ──
	let editingMacro = $state<{
		name: string; description: string; packageName: string; events: MacroEvent[];
		deviceWidth: number; deviceHeight: number;
	} | null>(null);
	let recording = $state(false);
	let recordStartTime = $state(0);
	let swipeStart: { x: number; y: number; t: number } | null = null;
	let replaying = $state(false);

	// ── Screen ──
	let videoEl: HTMLVideoElement | undefined;
	let ws: WebSocket | null = null;
	let jmuxer: JMuxer | null = null;
	let screenConnected = $state(false);
	let screenConnecting = $state(false);
	let deviceWidth = $state(1080);
	let deviceHeight = $state(2400);
	let feedReady = false;
	let pendingFrames: Uint8Array[] = [];
	let lastConfigPacket: Uint8Array | null = null;

	// ── Insert dialog ──
	let showInsertDialog = $state(false);
	let insertType = $state<'wait' | 'wait_until' | 'screenshot' | 'scroll_capture'>('wait');
	let insertForm = $state({
		seconds: 5,
		waitMethod: 'activity' as 'activity' | 'ui_text' | 'screen_stable',
		waitPattern: '',
		timeout: 900,
		pollInterval: 5,
		screenshotName: 'result',
		ocrPattern: '',
		direction: 'down',
		maxScrolls: 10,
		scrollPause: 1
	});

	const firstSelectedDevice = $derived([...selectedDevices][0] ?? null);

	$effect(() => { loadMacros(); });

	async function loadMacros() {
		try { macros = await fetchAppMacros(); } catch { /* ignore */ }
	}

	// ── Wizard navigation ──

	function startNewMacro() {
		selectedMacroId = null;
		selectedApp = null;
		editingMacro = null;
		wizardStep = 'app';
		loadInstalledApps();
	}

	function editMacro(macro: AppMacro) {
		selectedMacroId = macro.id;
		selectedApp = { packageName: macro.packageName ?? '', appName: macro.packageName ?? '' };
		editingMacro = {
			name: macro.name,
			description: macro.description ?? '',
			packageName: macro.packageName ?? '',
			events: JSON.parse(macro.eventsJson || '[]'),
			deviceWidth: macro.deviceWidth ?? 1080,
			deviceHeight: macro.deviceHeight ?? 2400
		};
		wizardStep = 'events';
	}

	function selectApp(app: InstalledApp) {
		selectedApp = app;
		editingMacro = {
			name: app.appName || app.packageName,
			description: '',
			packageName: app.packageName,
			events: [],
			deviceWidth: 1080,
			deviceHeight: 2400
		};
		wizardStep = 'record';
	}

	function goToEvents() {
		wizardStep = 'events';
	}

	function goToSave() {
		wizardStep = 'save';
	}

	async function loadInstalledApps() {
		if (serverId == null || !firstSelectedDevice) {
			toast.error('서버와 디바이스를 선택해주세요');
			return;
		}
		appsLoading = true;
		try {
			installedApps = await listInstalledApps(serverId, firstSelectedDevice);
		} catch (e: any) {
			toast.error('앱 목록 조회 실패: ' + (e.message ?? ''));
		} finally {
			appsLoading = false;
		}
	}

	// ── Screen connection ──

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

	function connectScreen() {
		if (serverId == null || !firstSelectedDevice) return;
		disconnectScreen();
		screenConnecting = true;
		const url = getScreenWebSocketUrl(serverId, firstSelectedDevice);
		ws = new WebSocket(url);
		ws.binaryType = 'arraybuffer';
		ws.onopen = () => { screenConnecting = false; screenConnected = true; initJMuxer(); };
		ws.onmessage = (e) => {
			if (typeof e.data === 'string') {
				try { const msg = JSON.parse(e.data); if (msg.type === 'info') { deviceWidth = msg.width || 1080; deviceHeight = msg.height || 2400; } } catch {}
			} else if (e.data instanceof ArrayBuffer) {
				const data = new Uint8Array(e.data);
				if (data.length === 0) return;
				if (isConfigPacket(data)) lastConfigPacket = data;
				if (!jmuxer || !feedReady) { pendingFrames.push(data); if (pendingFrames.length > 300) pendingFrames.splice(0, pendingFrames.length - 60); return; }
				try { jmuxer.feed({ video: data }); } catch {}
			}
		};
		ws.onclose = () => { screenConnected = false; screenConnecting = false; };
		ws.onerror = () => { screenConnected = false; screenConnecting = false; };
	}

	function disconnectScreen() {
		if (ws) { ws.close(); ws = null; }
		if (jmuxer) { try { jmuxer.destroy(); } catch {} jmuxer = null; }
		screenConnected = false; screenConnecting = false; feedReady = false; pendingFrames = []; lastConfigPacket = null;
	}

	function initJMuxer() {
		requestAnimationFrame(() => {
			if (!videoEl) { setTimeout(initJMuxer, 100); return; }
			jmuxer = new JMuxer({
				node: 'macro-screen-video', mode: 'video', flushingTime: 1, fps: 30, debug: false, clearBuffer: true,
				onReady: () => {
					feedReady = true;
					if (lastConfigPacket && pendingFrames.length === 0) try { jmuxer?.feed({ video: lastConfigPacket }); } catch {}
					if (pendingFrames.length > 0) {
						if (lastConfigPacket && !isConfigPacket(pendingFrames[0])) pendingFrames.unshift(lastConfigPacket);
						const total = pendingFrames.reduce((s, a) => s + a.length, 0);
						const merged = new Uint8Array(total); let off = 0;
						for (const a of pendingFrames) { merged.set(a, off); off += a.length; }
						pendingFrames = []; try { jmuxer?.feed({ video: merged }); } catch {}
					}
					videoEl?.play().catch(() => {});
				}, onError: () => {}
			});
		});
	}

	// ── Touch/Key ──

	function getVideoRect() {
		if (!videoEl) return null;
		const rect = videoEl.getBoundingClientRect();
		const vr = deviceWidth / deviceHeight; const cr = rect.width / rect.height;
		let vL = rect.left, vT = rect.top, vW = rect.width, vH = rect.height;
		if (cr > vr) { vW = rect.height * vr; vL = rect.left + (rect.width - vW) / 2; }
		else { vH = rect.width / vr; vT = rect.top + (rect.height - vH) / 2; }
		return { left: vL, top: vT, width: vW, height: vH };
	}

	function sendTouch(action: number, e: MouseEvent) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		const vr = getVideoRect(); if (!vr) return;
		const x = Math.max(0, Math.min(1, (e.clientX - vr.left) / vr.width));
		const y = Math.max(0, Math.min(1, (e.clientY - vr.top) / vr.height));
		ws.send(JSON.stringify({ type: 'touch', touch: { action, x, y, width: deviceWidth, height: deviceHeight, pressure: 1.0, pointer_id: 0 } }));

		if (recording && editingMacro) {
			const px = Math.round(x * deviceWidth), py = Math.round(y * deviceHeight);
			const t = Date.now() - recordStartTime;
			if (action === 0) { swipeStart = { x: px, y: py, t }; }
			else if (action === 1 && swipeStart) {
				const dx = Math.abs(px - swipeStart.x), dy = Math.abs(py - swipeStart.y), dt = t - swipeStart.t;
				if (dx < 20 && dy < 20) {
					editingMacro.events = [...editingMacro.events, { t: swipeStart.t, type: 'tap', x: swipeStart.x, y: swipeStart.y }];
				} else {
					editingMacro.events = [...editingMacro.events, { t: swipeStart.t, type: 'swipe', x: swipeStart.x, y: swipeStart.y, x2: px, y2: py, duration: Math.max(dt, 100) }];
				}
				swipeStart = null;
			}
		}
	}

	function handleWheel(e: WheelEvent) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		e.preventDefault(); const vr = getVideoRect(); if (!vr) return;
		const x = Math.max(0, Math.min(1, (e.clientX - vr.left) / vr.width));
		const y = Math.max(0, Math.min(1, (e.clientY - vr.top) / vr.height));
		ws.send(JSON.stringify({ type: 'scroll', scroll: { x, y, width: deviceWidth, height: deviceHeight, h_scroll: 0, v_scroll: e.deltaY > 0 ? -1 : 1 } }));
	}

	function sendKey(keycode: number) {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(JSON.stringify({ type: 'key', key: { action: 0, keycode, repeat: 0, meta_state: 0 } }));
		setTimeout(() => ws?.send(JSON.stringify({ type: 'key', key: { action: 1, keycode, repeat: 0, meta_state: 0 } })), 50);
		if (recording && editingMacro) { editingMacro.events = [...editingMacro.events, { t: Date.now() - recordStartTime, type: 'key', keycode }]; }
	}

	function sendBack() {
		if (!ws || ws.readyState !== WebSocket.OPEN) return;
		ws.send(JSON.stringify({ type: 'back' }));
		if (recording && editingMacro) { editingMacro.events = [...editingMacro.events, { t: Date.now() - recordStartTime, type: 'key', keycode: 4 }]; }
	}

	// ── Recording ──

	function startRec() {
		if (!screenConnected) { toast.error('화면을 먼저 연결해주세요'); return; }
		recording = true; recordStartTime = Date.now(); swipeStart = null;
		if (editingMacro) { editingMacro.events = []; editingMacro.deviceWidth = deviceWidth; editingMacro.deviceHeight = deviceHeight; }
		toast.success('녹화 시작 — 화면을 조작하세요');
	}

	function stopRec() {
		const count = editingMacro?.events.length ?? 0;
		recording = false;
		toast.success(`녹화 완료 · ${count}개 이벤트`);
		goToEvents();
	}

	// ── Insert events ──

	function insertEvent() {
		if (!editingMacro) return;
		const t = editingMacro.events.length > 0 ? editingMacro.events[editingMacro.events.length - 1].t + 1000 : 0;
		let event: MacroEvent;
		switch (insertType) {
			case 'wait': event = { t, type: 'wait', seconds: insertForm.seconds }; break;
			case 'wait_until': event = { t, type: 'wait_until', waitMethod: insertForm.waitMethod, waitPattern: insertForm.waitPattern, timeout: insertForm.timeout, pollInterval: insertForm.pollInterval }; break;
			case 'screenshot': event = { t, type: 'screenshot', name: insertForm.screenshotName, ocrPattern: insertForm.ocrPattern || undefined }; break;
			case 'scroll_capture': event = { t, type: 'scroll_capture', direction: insertForm.direction, maxScrolls: insertForm.maxScrolls, scrollPause: insertForm.scrollPause, ocrPattern: insertForm.ocrPattern || undefined }; break;
		}
		editingMacro.events = [...editingMacro.events, event];
		showInsertDialog = false;
		toast.success(`${insertType} 이벤트 추가됨`);
	}

	function removeEvent(index: number) { if (editingMacro) editingMacro.events = editingMacro.events.filter((_, i) => i !== index); }

	// ── Save ──

	async function saveMacro() {
		if (!editingMacro) return;
		if (!editingMacro.name.trim()) { toast.error('매크로 이름을 입력해주세요'); return; }
		try {
			const data = { name: editingMacro.name, description: editingMacro.description, packageName: editingMacro.packageName, eventsJson: JSON.stringify(editingMacro.events), deviceWidth: editingMacro.deviceWidth, deviceHeight: editingMacro.deviceHeight };
			if (selectedMacroId) { await updateAppMacro(selectedMacroId, data); toast.success('매크로 업데이트 완료'); }
			else { const created = await createAppMacro(data); selectedMacroId = created.id; toast.success('매크로 저장 완료'); }
			await loadMacros();
			wizardStep = 'list';
		} catch (e: any) { toast.error('저장 실패: ' + (e.message ?? '')); }
	}

	async function deleteMacroById(id: number) {
		try { await deleteAppMacro(id); if (selectedMacroId === id) { selectedMacroId = null; editingMacro = null; } await loadMacros(); toast.success('삭제됨'); } catch { toast.error('삭제 실패'); }
	}

	async function replay() {
		if (serverId == null || !firstSelectedDevice || !editingMacro || editingMacro.events.length === 0) { toast.error('이벤트가 없습니다'); return; }
		replaying = true;
		try {
			const res = await replayMacro(serverId, { deviceId: firstSelectedDevice, events: editingMacro.events, sourceWidth: editingMacro.deviceWidth, sourceHeight: editingMacro.deviceHeight });
			if (res.success) toast.success('재생 완료'); else toast.error('재생 실패: ' + res.message);
		} catch (e: any) { toast.error('재생 실패: ' + (e.message ?? '')); }
		finally { replaying = false; }
	}

	// ── Event labels ──
	function eventLabel(ev: MacroEvent): string {
		switch (ev.type) {
			case 'tap': return `Tap (${ev.x}, ${ev.y})`;
			case 'swipe': return `Swipe → (${ev.x2}, ${ev.y2})`;
			case 'key': return `Key ${ev.keycode}`;
			case 'wait': return `Wait ${ev.seconds}s`;
			case 'wait_until': return `Until: ${ev.waitMethod} "${ev.waitPattern?.slice(0, 20)}"`;
			case 'screenshot': return `Screenshot "${ev.name}"`;
			case 'scroll_capture': return `Scroll OCR (${ev.direction})`;
			default: return ev.type;
		}
	}
	const eventColor: Record<string, string> = {
		tap: 'text-blue-600', swipe: 'text-purple-600', key: 'text-gray-600',
		wait: 'text-yellow-600', wait_until: 'text-amber-600',
		screenshot: 'text-emerald-600', scroll_capture: 'text-teal-600'
	};

	// ── Wizard steps config ──
	const STEPS: { key: WizardStep; label: string; num: number }[] = [
		{ key: 'app', label: '앱 선택', num: 1 },
		{ key: 'record', label: '조작 녹화', num: 2 },
		{ key: 'events', label: '완료 조건', num: 3 },
		{ key: 'save', label: '저장', num: 4 },
	];
	const currentStepNum = $derived(STEPS.find(s => s.key === wizardStep)?.num ?? 0);
</script>

<div class="h-full flex flex-col overflow-hidden">

{#if wizardStep === 'list'}
	<!-- ════ 매크로 목록 ════ -->
	<div class="flex items-center justify-between px-4 py-3 border-b">
		<h2 class="text-sm font-semibold">App Macro</h2>
		<button onclick={startNewMacro} disabled={!firstSelectedDevice}
			class="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-40 transition-colors">
			<PlusIcon class="size-3.5" /> 새 매크로
		</button>
	</div>

	{#if !firstSelectedDevice}
		<div class="flex-1 flex flex-col items-center justify-center text-center p-8 gap-3">
			<SmartphoneIcon class="size-10 text-muted-foreground/20" />
			<p class="text-sm text-muted-foreground">왼쪽에서 디바이스를 선택해주세요</p>
			<p class="text-[10px] text-muted-foreground/60">매크로 녹화에는 디바이스 연결이 필요합니다</p>
		</div>
	{:else if macros.length === 0}
		<div class="flex-1 flex flex-col items-center justify-center text-center p-8 gap-4">
			<div class="size-16 rounded-2xl bg-primary/5 flex items-center justify-center">
				<CircleDotIcon class="size-8 text-primary/40" />
			</div>
			<div>
				<p class="text-sm font-medium">저장된 매크로가 없습니다</p>
				<p class="text-xs text-muted-foreground mt-1">앱 벤치마크를 자동화하려면 매크로를 만들어보세요</p>
			</div>
			<div class="text-[10px] text-muted-foreground/60 space-y-0.5">
				<p>① 앱을 선택하고</p>
				<p>② 화면을 녹화하고</p>
				<p>③ 완료 조건을 추가하면 끝!</p>
			</div>
		</div>
	{:else}
		<div class="flex-1 overflow-y-auto">
			{#each macros as macro (macro.id)}
				<div class="flex items-center gap-3 px-4 py-3 border-b hover:bg-muted/30 transition-colors cursor-pointer" onclick={() => editMacro(macro)}>
					<div class="size-9 rounded-lg bg-violet-500/10 flex items-center justify-center shrink-0">
						<PackageIcon class="size-4 text-violet-600" />
					</div>
					<div class="flex-1 min-w-0">
						<div class="text-xs font-medium truncate">{macro.name}</div>
						{#if macro.packageName}
							<div class="text-[10px] text-muted-foreground font-mono truncate">{macro.packageName}</div>
						{/if}
					</div>
					<div class="flex items-center gap-1 shrink-0">
						<button onclick={(e) => { e.stopPropagation(); deleteMacroById(macro.id); }} class="p-1.5 rounded hover:bg-destructive/10 transition-colors" title="삭제">
							<TrashIcon class="size-3.5 text-muted-foreground hover:text-destructive" />
						</button>
						<ChevronRightIcon class="size-4 text-muted-foreground/40" />
					</div>
				</div>
			{/each}
		</div>
	{/if}

{:else}
	<!-- ════ 위자드 (Step 1~4) ════ -->

	<!-- Progress bar -->
	<div class="flex items-center gap-2 px-4 py-2 border-b bg-muted/20 shrink-0">
		{#if wizardStep !== 'list'}
			<button onclick={() => {
				if (wizardStep === 'app') wizardStep = 'list';
				else if (wizardStep === 'record') wizardStep = 'app';
				else if (wizardStep === 'events') wizardStep = 'record';
				else if (wizardStep === 'save') wizardStep = 'events';
			}} class="p-1 rounded hover:bg-muted transition-colors">
				<ChevronLeftIcon class="size-4" />
			</button>
		{/if}

		{#each STEPS as step, i}
			{@const active = step.key === wizardStep}
			{@const done = step.num < currentStepNum}
			<div class="flex items-center gap-1.5 {i > 0 ? '' : ''}">
				{#if i > 0}
					<div class="w-6 h-px {done ? 'bg-primary' : 'bg-border'}"></div>
				{/if}
				<div class="flex items-center gap-1">
					<span class="size-5 rounded-full text-[9px] font-bold flex items-center justify-center
						{done ? 'bg-primary text-primary-foreground' : active ? 'bg-primary/10 text-primary border border-primary' : 'bg-muted text-muted-foreground'}">
						{#if done}<CheckIcon class="size-3" />{:else}{step.num}{/if}
					</span>
					<span class="text-[10px] {active ? 'font-medium text-foreground' : 'text-muted-foreground'}">{step.label}</span>
				</div>
			</div>
		{/each}
	</div>

	<!-- Step content -->
	<div class="flex-1 overflow-hidden">

	{#if wizardStep === 'app'}
		<!-- ════ Step 1: 앱 선택 ════ -->
		<div class="h-full flex flex-col p-4">
			<div class="mb-4">
				<h3 class="text-sm font-semibold">어떤 앱을 자동화할까요?</h3>
				<p class="text-xs text-muted-foreground mt-1">디바이스에 설치된 앱 중에서 선택하세요</p>
			</div>

			<!-- Search -->
			<div class="relative mb-3">
				<SearchIcon class="absolute left-2.5 top-1/2 -translate-y-1/2 size-3.5 text-muted-foreground" />
				<input type="text" bind:value={appSearch} placeholder="앱 이름 또는 패키지명 검색..."
					class="w-full pl-8 pr-3 py-2 text-xs rounded-md border bg-background focus:outline-none focus:ring-1 focus:ring-ring" />
			</div>

			{#if appsLoading}
				<div class="flex-1 flex items-center justify-center gap-2">
					<LoaderIcon class="size-4 animate-spin text-muted-foreground" />
					<span class="text-xs text-muted-foreground">앱 목록을 불러오는 중...</span>
				</div>
			{:else}
				<div class="flex-1 overflow-y-auto -mx-4">
					{#each filteredApps as app (app.packageName)}
						<button
							class="w-full flex items-center gap-3 px-4 py-2.5 text-left hover:bg-muted/50 transition-colors
								{selectedApp?.packageName === app.packageName ? 'bg-primary/5' : ''}"
							onclick={() => selectApp(app)}
						>
							<div class="size-8 rounded-lg bg-muted/50 flex items-center justify-center shrink-0">
								<PackageIcon class="size-4 text-muted-foreground" />
							</div>
							<div class="flex-1 min-w-0">
								<div class="text-xs font-medium truncate">{app.appName}</div>
								<div class="text-[10px] text-muted-foreground font-mono truncate">{app.packageName}</div>
							</div>
							<ChevronRightIcon class="size-4 text-muted-foreground/30 shrink-0" />
						</button>
					{/each}
					{#if filteredApps.length === 0 && !appsLoading}
						<div class="text-center py-8 text-xs text-muted-foreground">
							{appSearch ? `"${appSearch}"에 대한 결과 없음` : '설치된 앱이 없습니다'}
						</div>
					{/if}
				</div>
			{/if}

			<!-- 직접 입력 -->
			<div class="border-t pt-3 mt-3 shrink-0">
				<p class="text-[10px] text-muted-foreground mb-2">목록에 없다면 직접 입력:</p>
				<div class="flex gap-2">
					<input type="text" bind:value={appSearch} placeholder="com.example.app"
						class="flex-1 rounded border px-2 py-1.5 text-xs bg-background font-mono" />
					<button onclick={() => selectApp({ packageName: appSearch, appName: appSearch.split('.').pop() ?? appSearch })}
						disabled={!appSearch.trim()}
						class="rounded bg-primary px-3 py-1.5 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-40">
						선택
					</button>
				</div>
			</div>
		</div>

	{:else if wizardStep === 'record'}
		<!-- ════ Step 2: 조작 녹화 ════ -->
		<div class="h-full flex flex-col items-center p-4 overflow-hidden">
			{#if !screenConnected && !screenConnecting}
				<div class="flex-1 flex flex-col items-center justify-center text-center gap-4">
					<div class="size-16 rounded-2xl bg-blue-500/5 flex items-center justify-center">
						<SmartphoneIcon class="size-8 text-blue-500/40" />
					</div>
					<div>
						<h3 class="text-sm font-semibold">화면을 연결하세요</h3>
						<p class="text-xs text-muted-foreground mt-1">디바이스 화면을 보면서 앱을 조작하고 녹화합니다</p>
					</div>
					<button onclick={connectScreen} class="rounded-md bg-primary px-4 py-2 text-xs text-primary-foreground hover:bg-primary/90">
						화면 연결
					</button>
					{#if selectedApp}
						<div class="{captionMuted}">
							<span class="font-medium">{selectedApp.appName}</span> · {selectedApp.packageName}
						</div>
					{/if}
				</div>
			{:else if screenConnecting}
				<div class="flex-1 flex items-center justify-center gap-2">
					<LoaderIcon class="size-5 animate-spin text-muted-foreground" />
					<span class="text-xs text-muted-foreground">화면 연결 중...</span>
				</div>
			{:else}
				<!-- 녹화 상태 -->
				{#if recording}
					<div class="w-full flex items-center justify-center gap-2 py-1.5 bg-red-50 rounded mb-2 shrink-0">
						<span class="size-2 rounded-full bg-red-500 animate-pulse"></span>
						<span class="text-xs text-red-600 font-medium">녹화 중 · {editingMacro?.events.length ?? 0}개 이벤트</span>
					</div>
				{:else if editingMacro && editingMacro.events.length === 0}
					<div class="w-full text-center py-1.5 text-[10px] text-muted-foreground shrink-0">
						"녹화 시작"을 누르고 앱을 조작하세요
					</div>
				{/if}

				<!-- Video -->
				<div class="flex-1 flex items-center justify-center w-full overflow-hidden" style="max-height: calc(100vh - 16rem);">
					<!-- svelte-ignore a11y_media_has_caption -->
					<video id="macro-screen-video" bind:this={videoEl}
						class="max-w-full max-h-full border rounded bg-black cursor-pointer {recording ? 'ring-2 ring-red-500/50' : ''}"
						style="aspect-ratio: {deviceWidth}/{deviceHeight};" autoplay muted playsinline
						onmousedown={(e) => { e.preventDefault(); sendTouch(0, e); }}
						onmouseup={(e) => sendTouch(1, e)}
						onmousemove={(e) => { if (e.buttons > 0) sendTouch(2, e); }}
						onwheel={handleWheel} oncontextmenu={(e) => e.preventDefault()}
					></video>
				</div>

				<!-- Controls -->
				<div class="flex items-center gap-2 mt-2 shrink-0 flex-wrap justify-center">
					<button onclick={() => sendKey(224)} class="inline-flex items-center gap-1 rounded border px-2 py-1 text-[10px] hover:bg-muted"><SunIcon class="size-3" /> Wake</button>
					<button onclick={sendBack} class="inline-flex items-center gap-1 rounded border px-2 py-1 text-[10px] hover:bg-muted"><ArrowLeftIcon class="size-3" /> Back</button>
					<button onclick={() => sendKey(3)} class="inline-flex items-center gap-1 rounded border px-2 py-1 text-[10px] hover:bg-muted"><HomeIcon class="size-3" /> Home</button>
					<button onclick={() => sendKey(187)} class="inline-flex items-center gap-1 rounded border px-2 py-1 text-[10px] hover:bg-muted"><LayoutGridIcon class="size-3" /> Recent</button>
					<div class="w-px h-4 bg-border"></div>
					{#if !recording}
						<button onclick={startRec} class="inline-flex items-center gap-1 rounded border border-red-300 px-3 py-1 text-[10px] text-red-600 hover:bg-red-50">
							<CircleDotIcon class="size-3" /> 녹화 시작
						</button>
					{:else}
						<button onclick={stopRec} class="inline-flex items-center gap-1 rounded bg-red-600 px-3 py-1 text-[10px] text-white hover:bg-red-700">
							<SquareIcon class="size-3" /> 녹화 중지
						</button>
					{/if}
					{#if !recording && editingMacro && editingMacro.events.length > 0}
						<button onclick={goToEvents} class="inline-flex items-center gap-1 rounded bg-primary px-3 py-1 text-[10px] text-primary-foreground hover:bg-primary/90">
							다음 <ChevronRightIcon class="size-3" />
						</button>
					{/if}
				</div>
			{/if}
		</div>

	{:else if wizardStep === 'events'}
		<!-- ════ Step 3: 완료 조건 추가 ════ -->
		<div class="h-full flex flex-col p-4">
			<div class="mb-3">
				<h3 class="text-sm font-semibold">완료 조건을 추가하세요</h3>
				<p class="text-xs text-muted-foreground mt-1">벤치마크가 끝날 때까지 대기하고, 결과를 자동으로 수집합니다</p>
			</div>

			<!-- 추천 카드 — 항상 표시, 추가된 항목은 체크 표시 -->
			{#if editingMacro}
				{@const hasWaitUntil = editingMacro.events.some(e => e.type === 'wait_until')}
				{@const hasScrollCapture = editingMacro.events.some(e => e.type === 'scroll_capture')}
				<div class="rounded-lg border border-primary/20 bg-primary/5 p-3 mb-3">
					<p class="text-[10px] font-medium text-primary mb-2">앱 벤치마크라면 이렇게 추가하세요:</p>
					<div class="space-y-1.5">
						<button onclick={() => { insertType = 'wait_until'; insertForm.waitMethod = 'ui_text'; insertForm.waitPattern = '다시 테스트'; showInsertDialog = true; }}
							class="w-full flex items-center gap-2 px-3 py-2 rounded-md border text-left transition-colors
								{hasWaitUntil ? 'bg-emerald-50 border-emerald-200' : 'bg-background hover:border-primary/40'}">
							{#if hasWaitUntil}
								<CheckIcon class="size-4 text-emerald-600 shrink-0" />
							{:else}
								<ClockIcon class="size-4 text-amber-600 shrink-0" />
							{/if}
							<div class="flex-1">
								<div class="text-[10px] font-medium">완료 대기 {hasWaitUntil ? '' : '추가'}</div>
								<div class="text-[9px] text-muted-foreground">벤치마크가 끝날 때까지 기다립니다</div>
							</div>
							{#if hasWaitUntil}
								<span class="text-[9px] text-emerald-600 font-medium shrink-0">추가됨</span>
							{/if}
						</button>
						<button onclick={() => { insertType = 'scroll_capture'; showInsertDialog = true; }}
							class="w-full flex items-center gap-2 px-3 py-2 rounded-md border text-left transition-colors
								{hasScrollCapture ? 'bg-emerald-50 border-emerald-200' : 'bg-background hover:border-primary/40'}">
							{#if hasScrollCapture}
								<CheckIcon class="size-4 text-emerald-600 shrink-0" />
							{:else}
								<ListIcon class="size-4 text-teal-600 shrink-0" />
							{/if}
							<div class="flex-1">
								<div class="text-[10px] font-medium">결과 수집 {hasScrollCapture ? '' : '추가'}</div>
								<div class="text-[9px] text-muted-foreground">스크롤하며 점수를 자동으로 수집합니다</div>
							</div>
							{#if hasScrollCapture}
								<span class="text-[9px] text-emerald-600 font-medium shrink-0">추가됨</span>
							{/if}
						</button>
					</div>
				</div>
			{/if}

			<!-- 이벤트 목록 -->
			<div class="flex items-center justify-between mb-2">
				<span class="{captionMuted}">{editingMacro?.events.length ?? 0}개 이벤트</span>
				<button onclick={() => { showInsertDialog = true; }} class="text-[10px] text-primary hover:text-primary/80">+ 이벤트 추가</button>
			</div>
			<div class="flex-1 overflow-y-auto border rounded-md divide-y">
				{#each editingMacro?.events ?? [] as ev, i (i)}
					<div class="flex items-center gap-2 px-3 py-2 hover:bg-muted/30 transition-colors">
						<span class="text-[9px] text-muted-foreground/60 w-10 text-right font-mono shrink-0">{(ev.t / 1000).toFixed(1)}s</span>
						<span class="text-xs {eventColor[ev.type] ?? 'text-foreground'} truncate flex-1">{eventLabel(ev)}</span>
						<button onclick={() => removeEvent(i)} class="p-1 rounded hover:bg-destructive/10 transition-colors shrink-0" title="삭제">
							<TrashIcon class="size-3.5 text-muted-foreground/40 hover:text-destructive" />
						</button>
					</div>
				{:else}
					<div class="text-[10px] text-muted-foreground/50 text-center py-8">이벤트가 없습니다</div>
				{/each}
			</div>

			<!-- 하단 버튼 -->
			<div class="flex items-center gap-2 mt-3 shrink-0">
				<button onclick={() => wizardStep = 'record'} class="inline-flex items-center gap-1 rounded border px-3 py-1.5 text-xs hover:bg-muted">
					<CircleDotIcon class="size-3" /> 다시 녹화
				</button>
				<button onclick={replay} disabled={replaying || !editingMacro?.events.length || !firstSelectedDevice}
					class="inline-flex items-center gap-1 rounded border px-3 py-1.5 text-xs hover:bg-muted disabled:opacity-40">
					{#if replaying}<LoaderIcon class="size-3 animate-spin" />{:else}<PlayIcon class="size-3" />{/if} 테스트 재생
				</button>
				<div class="flex-1"></div>
				<button onclick={goToSave} disabled={!editingMacro?.events.length}
					class="inline-flex items-center gap-1 rounded bg-primary px-4 py-1.5 text-xs text-primary-foreground hover:bg-primary/90 disabled:opacity-40">
					다음 <ChevronRightIcon class="size-3" />
				</button>
			</div>
		</div>

	{:else if wizardStep === 'save'}
		<!-- ════ Step 4: 저장 ════ -->
		<div class="h-full flex flex-col p-4">
			<div class="mb-4">
				<h3 class="text-sm font-semibold">매크로 저장</h3>
				<p class="text-xs text-muted-foreground mt-1">이름을 확인하고 저장하세요. 시나리오 캔버스에서 사용할 수 있습니다.</p>
			</div>

			{#if editingMacro}
				<div class="space-y-3 flex-1">
					<div>
						<label class="{sectionLabel}">매크로 이름</label>
						<input bind:value={editingMacro.name} placeholder="예: AnTuTu 스토리지 테스트"
							class="w-full rounded border px-3 py-2 text-xs mt-1 bg-background" />
					</div>
					<div>
						<label class="{sectionLabel}">앱 패키지</label>
						<input bind:value={editingMacro.packageName} disabled
							class="w-full rounded border px-3 py-2 text-xs mt-1 bg-muted font-mono" />
					</div>
					<div>
						<label class="{sectionLabel}">설명 (선택)</label>
						<input bind:value={editingMacro.description} placeholder="이 매크로가 하는 일을 간단히 적어주세요"
							class="w-full rounded border px-3 py-2 text-xs mt-1 bg-background" />
					</div>

					<!-- 요약 -->
					<div class="rounded-lg bg-muted/30 border p-3 space-y-1">
						<div class="text-[10px] font-medium text-muted-foreground">요약</div>
						<div class="text-xs">{editingMacro.events.length}개 이벤트 · {editingMacro.deviceWidth}x{editingMacro.deviceHeight}</div>
						<div class="flex flex-wrap gap-1 mt-1">
							{#each ['tap', 'swipe', 'key', 'wait', 'wait_until', 'screenshot', 'scroll_capture'] as type}
								{@const count = editingMacro.events.filter(e => e.type === type).length}
								{#if count > 0}
									<span class="px-1.5 py-0.5 rounded text-[9px] bg-background border {eventColor[type]}">{type} x{count}</span>
								{/if}
							{/each}
						</div>
					</div>
				</div>

				<div class="flex items-center gap-2 mt-4 shrink-0">
					<button onclick={() => wizardStep = 'events'} class="inline-flex items-center gap-1 rounded border px-3 py-1.5 text-xs hover:bg-muted">
						<ChevronLeftIcon class="size-3" /> 이전
					</button>
					<div class="flex-1"></div>
					<button onclick={saveMacro}
						class="inline-flex items-center gap-1 rounded bg-primary px-4 py-1.5 text-xs text-primary-foreground hover:bg-primary/90">
						<SaveIcon class="size-3" /> 저장
					</button>
				</div>
			{/if}
		</div>
	{/if}

	</div>
{/if}
</div>

<!-- Insert Event Dialog -->
{#if showInsertDialog}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="fixed inset-0 bg-black/50 z-50 flex items-center justify-center" onmousedown={(e) => { if (e.target === e.currentTarget) showInsertDialog = false; }}>
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<div class="bg-background rounded-lg shadow-lg w-96 max-h-[80vh] overflow-y-auto" onclick={(e) => e.stopPropagation()}>
			<div class="px-4 py-3 border-b flex items-center justify-between">
				<span class="text-sm font-medium">이벤트 추가</span>
				<button onclick={() => showInsertDialog = false} class="p-0.5 rounded hover:bg-muted"><XIcon class="size-4" /></button>
			</div>
			<div class="p-4 space-y-3">
				<div class="flex gap-1">
					{#each [['wait', '대기'], ['wait_until', '완료 대기'], ['screenshot', '스크린샷'], ['scroll_capture', '스크롤 수집']] as [t, label]}
						<button onclick={() => insertType = t as typeof insertType}
							class="px-2 py-1 rounded text-[10px] border {insertType === t ? 'bg-primary text-primary-foreground' : 'hover:bg-muted'}">
							{label}
						</button>
					{/each}
				</div>

				{#if insertType === 'wait'}
					<label class="block"><span class="text-xs text-muted-foreground">대기 시간 (초)</span>
						<input type="number" bind:value={insertForm.seconds} min="1" class="w-full rounded border px-2 py-1 text-xs mt-1 bg-background" />
					</label>
				{:else if insertType === 'wait_until'}
					<label class="block"><span class="text-xs text-muted-foreground">감지 방법</span>
						<select bind:value={insertForm.waitMethod} class="w-full rounded border px-2 py-1 text-xs mt-1 bg-background">
							<option value="activity">Activity 감지</option>
							<option value="ui_text">UI 텍스트 감지</option>
							<option value="screen_stable">화면 안정화</option>
						</select>
					</label>
					{#if insertForm.waitMethod !== 'screen_stable'}
						<label class="block"><span class="text-xs text-muted-foreground">감지 패턴</span>
							<input bind:value={insertForm.waitPattern} placeholder="예: 다시 테스트" class="w-full rounded border px-2 py-1 text-xs mt-1 bg-background" />
						</label>
					{/if}
					<div class="grid grid-cols-2 gap-2">
						<label><span class="text-xs text-muted-foreground">타임아웃 (초)</span>
							<input type="number" bind:value={insertForm.timeout} min="10" class="w-full rounded border px-2 py-1 text-xs mt-1 bg-background" />
						</label>
						<label><span class="text-xs text-muted-foreground">폴링 간격 (초)</span>
							<input type="number" bind:value={insertForm.pollInterval} min="1" class="w-full rounded border px-2 py-1 text-xs mt-1 bg-background" />
						</label>
					</div>
				{:else if insertType === 'screenshot'}
					<label class="block"><span class="text-xs text-muted-foreground">스크린샷 이름</span>
						<input bind:value={insertForm.screenshotName} placeholder="result" class="w-full rounded border px-2 py-1 text-xs mt-1 bg-background" />
					</label>
				{:else if insertType === 'scroll_capture'}
					<div class="grid grid-cols-2 gap-2">
						<label><span class="text-xs text-muted-foreground">방향</span>
							<select bind:value={insertForm.direction} class="w-full rounded border px-2 py-1 text-xs mt-1 bg-background">
								<option value="down">아래로</option><option value="up">위로</option>
							</select>
						</label>
						<label><span class="text-xs text-muted-foreground">최대 스크롤</span>
							<input type="number" bind:value={insertForm.maxScrolls} min="1" class="w-full rounded border px-2 py-1 text-xs mt-1 bg-background" />
						</label>
					</div>
				{/if}

				<button onclick={insertEvent} class="w-full rounded bg-primary px-3 py-1.5 text-xs text-primary-foreground hover:bg-primary/90">추가</button>
			</div>
		</div>
	</div>
{/if}
