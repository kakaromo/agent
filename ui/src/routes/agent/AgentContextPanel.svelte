<script lang="ts">
	import { getServerConnectionStatus, reconnectServer, type AgentServer, type Device, type DeviceMetricsData } from '$lib/api/agent.js';
	import { sectionLabel, captionMuted } from '$lib/styles/common.js';
	import { toast } from 'svelte-sonner';
	import SettingsIcon from '@lucide/svelte/icons/settings';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import PlayIcon from '@lucide/svelte/icons/play';
	import ListOrderedIcon from '@lucide/svelte/icons/list-ordered';
	import ClockIcon from '@lucide/svelte/icons/clock';
	import CalendarIcon from '@lucide/svelte/icons/calendar';
	import ActivityIcon from '@lucide/svelte/icons/activity';
	import ScanSearchIcon from '@lucide/svelte/icons/scan-search';
	import SmartphoneIcon from '@lucide/svelte/icons/smartphone';
	import CircleDotIcon from '@lucide/svelte/icons/circle-dot';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import PlugIcon from '@lucide/svelte/icons/plug';
	import UnplugIcon from '@lucide/svelte/icons/unplug';
	import FlaskConicalIcon from '@lucide/svelte/icons/flask-conical';
	import TerminalIcon from '@lucide/svelte/icons/terminal';

	interface Props {
		enabledServers: AgentServer[];
		selectedServerId: number | null;
		devices: Device[];
		loadingDevices: boolean;
		selectedDeviceIds: Set<string>;
		centerMode: 'benchmark' | 'scenario' | 'trace' | 'results' | 'schedule' | 'macro' | 'iotest';
		onModeChange: (mode: 'benchmark' | 'scenario' | 'trace' | 'results' | 'schedule' | 'macro' | 'iotest') => void;
		onRefreshDevices: () => void;
		onOpenServerSheet: () => void;
		onOpenMonitoring: (deviceId: string) => void;
		onOpenScreen: (deviceId: string) => void;
		onOpenTerminal: (deviceId: string) => void;
		activeJobCount: number;
		storageMetricsMap: Map<string, DeviceMetricsData>;
	}

	let {
		enabledServers,
		selectedServerId = $bindable(),
		devices,
		loadingDevices,
		selectedDeviceIds = $bindable(),
		centerMode,
		onModeChange,
		onRefreshDevices,
		onOpenServerSheet,
		onOpenMonitoring,
		onOpenScreen,
		onOpenTerminal,
		activeJobCount,
		storageMetricsMap
	}: Props = $props();

	function toggleDevice(deviceId: string) {
		const next = new Set(selectedDeviceIds);
		if (next.has(deviceId)) next.delete(deviceId);
		else next.add(deviceId);
		selectedDeviceIds = next;
	}

	// OFFLINE 디바이스는 목록에서 숨김. ADB 가 잠시 offline 으로 남기는 경우 노이즈가 되므로
	// 사용자 요청에 따라 online 만 보여준다.
	let visibleDevices = $derived(devices.filter(d => d.state === 'online'));

	// Connection status
	let connectionState = $state<string>('');
	let isConnected = $state(true);
	let reconnecting = $state(false);
	let statusCheckTimer: ReturnType<typeof setInterval> | null = null;

	$effect(() => {
		if (selectedServerId != null) {
			checkConnectionStatus();
			// 주기적 상태 확인 (10초)
			if (statusCheckTimer) clearInterval(statusCheckTimer);
			statusCheckTimer = setInterval(checkConnectionStatus, 10000);
		}
		return () => { if (statusCheckTimer) clearInterval(statusCheckTimer); };
	});

	async function checkConnectionStatus() {
		if (selectedServerId == null) return;
		try {
			const res = await getServerConnectionStatus(selectedServerId);
			connectionState = res.state;
			isConnected = res.connected;
		} catch {
			connectionState = 'UNAVAILABLE';
			isConnected = false;
		}
	}

	async function handleReconnect() {
		if (selectedServerId == null) return;
		reconnecting = true;
		try {
			const res = await reconnectServer(selectedServerId);
			isConnected = res.success;
			connectionState = res.state;
			toast[res.success ? 'success' : 'error'](res.message);
			if (res.success) onRefreshDevices();
		} catch {
			toast.error('재연결 실패');
		} finally {
			reconnecting = false;
		}
	}

	function stateColor(state: string): string {
		switch (state) {
			case 'online': return 'bg-green-500';
			case 'busy': return 'bg-yellow-500';
			default: return 'bg-gray-400';
		}
	}

	const selectedCount = $derived(selectedDeviceIds.size);

	const modeButtons: { mode: 'benchmark' | 'scenario' | 'trace' | 'results' | 'schedule' | 'macro' | 'iotest'; label: string; icon: any }[] = [
		{ mode: 'benchmark', label: 'Benchmark', icon: PlayIcon },
		{ mode: 'iotest', label: 'I/O Test', icon: FlaskConicalIcon },
		{ mode: 'scenario', label: 'Scenario', icon: ListOrderedIcon },
		{ mode: 'trace', label: 'Trace', icon: ScanSearchIcon },
		{ mode: 'macro', label: 'Macro', icon: CircleDotIcon },
		{ mode: 'results', label: 'Results', icon: ClockIcon },
		{ mode: 'schedule', label: 'Schedule', icon: CalendarIcon }
	];
</script>

<div class="w-60 shrink-0 flex flex-col gap-2 overflow-y-auto border-r pr-3">
	<!-- Server -->
	<div class="flex items-center justify-between mb-1">
		<span class="{sectionLabel}">Server</span>
		<button
			onclick={onOpenServerSheet}
			class="p-0.5 rounded hover:bg-muted transition-colors"
			title="서버 관리"
		>
			<SettingsIcon class="size-3 text-muted-foreground" />
		</button>
	</div>

	{#if enabledServers.length === 0}
		<div class="text-[10px] text-muted-foreground text-center py-3">
			<button onclick={onOpenServerSheet} class="hover:text-primary transition-colors underline underline-offset-2">서버를 추가해주세요</button>
		</div>
	{:else}
		<div class="space-y-1">
			{#each enabledServers as s (s.id)}
				<button
					onclick={() => selectedServerId = s.id}
					class="w-full border rounded-md px-2 py-1.5 text-left transition-all
						{selectedServerId === s.id
							? 'border-primary bg-primary/5 ring-1 ring-primary'
							: 'hover:bg-muted'}"
				>
					<div class="flex items-center gap-1.5">
						{#if selectedServerId === s.id}
							<span class="size-1.5 rounded-full shrink-0 {isConnected ? 'bg-green-500' : 'bg-red-500'}"></span>
						{/if}
						<span class="text-[10px] font-medium truncate">{s.name}</span>
					</div>
					<div class="text-[9px] text-muted-foreground truncate">{s.host}:{s.port}</div>
				</button>
			{/each}
		</div>
	{/if}

	<!-- Connection status (selected server) -->
	{#if selectedServerId != null && !isConnected}
		<div class="flex items-center gap-1.5 text-[10px] mt-1">
			<span class="size-1.5 rounded-full bg-red-500"></span>
			<span class="text-red-600">연결 끊김</span>
			<button
				onclick={handleReconnect}
				disabled={reconnecting}
				class="ml-auto inline-flex items-center gap-0.5 rounded border px-1.5 py-0.5 text-[9px] hover:bg-muted disabled:opacity-50"
			>
				{#if reconnecting}
					<LoaderIcon class="size-2.5 animate-spin" />
				{:else}
					<PlugIcon class="size-2.5" />
				{/if}
				재연결
			</button>
		</div>
	{/if}

	<!-- Devices -->
	<div class="flex-1 min-h-0 flex flex-col">
		<div class="flex items-center justify-between mb-1">
			<span class="{sectionLabel}">Devices</span>
			<button
				onclick={onRefreshDevices}
				disabled={loadingDevices}
				class="p-0.5 rounded hover:bg-muted transition-colors"
			>
				<RefreshCwIcon class="size-3 text-muted-foreground {loadingDevices ? 'animate-spin' : ''}" />
			</button>
		</div>

		<div class="flex-1 overflow-y-auto space-y-0.5">
			{#if loadingDevices}
				<div class="flex items-center justify-center py-4">
					<LoaderIcon class="size-4 animate-spin text-muted-foreground" />
				</div>
			{:else if visibleDevices.length === 0}
				<div class="text-[10px] text-muted-foreground text-center py-4">
					연결된 디바이스가 없습니다
				</div>
			{:else}
				{#each visibleDevices as d (d.deviceId)}
					<label
						class="flex items-center gap-1.5 px-1.5 py-1 rounded cursor-pointer transition-colors
							{selectedDeviceIds.has(d.deviceId) ? 'bg-primary/10' : 'hover:bg-muted/50'}"
					>
						<input
							type="checkbox"
							checked={selectedDeviceIds.has(d.deviceId)}
							onchange={() => toggleDevice(d.deviceId)}
							class="size-3"
						/>
						<span class="size-1.5 rounded-full {stateColor(d.state)} shrink-0"></span>
						<div class="flex-1 min-w-0">
							<div class="text-[10px] font-mono truncate">{d.serial}</div>
							<div class="text-[9px] text-muted-foreground truncate">{d.model}{d.manufacturer ? ` · ${d.manufacturer}` : ''} · {d.deviceId}</div>
						</div>
						<button
							onclick={(e) => { e.preventDefault(); e.stopPropagation(); onOpenScreen(d.deviceId); }}
							class="p-0.5 rounded hover:bg-muted shrink-0"
							title="화면 보기"
						>
							<SmartphoneIcon class="size-3 text-muted-foreground" />
						</button>
						<button
							onclick={(e) => { e.preventDefault(); e.stopPropagation(); onOpenTerminal(d.deviceId); }}
							class="p-0.5 rounded hover:bg-muted shrink-0"
							title="Terminal (adb shell)"
						>
							<TerminalIcon class="size-3 text-muted-foreground" />
						</button>
						<button
							onclick={(e) => { e.preventDefault(); e.stopPropagation(); onOpenMonitoring(d.deviceId); }}
							class="p-0.5 rounded hover:bg-muted shrink-0"
							title="모니터링"
						>
							<ActivityIcon class="size-3 text-muted-foreground" />
						</button>
					</label>
				{/each}
			{/if}
		</div>
	</div>

	<!-- Storage Info (선택된 디바이스별 실시간) -->
	{#if storageMetricsMap.size > 0}
		<div class="space-y-1">
			{#each [...storageMetricsMap.entries()] as [deviceId, metrics]}
				{#if metrics.dataPartition}
					{@const dp = metrics.dataPartition}
					{@const usedGB = (dp.usedBytes / (1024 * 1024 * 1024)).toFixed(1)}
					{@const totalGB = (dp.totalBytes / (1024 * 1024 * 1024)).toFixed(1)}
					{@const availGB = (dp.availableBytes / (1024 * 1024 * 1024)).toFixed(1)}
					{@const pct = dp.usagePercent}
					<div class="border rounded-md p-1.5 space-y-0.5 {pct > 90 ? 'border-red-300 bg-red-50' : pct > 70 ? 'border-orange-300 bg-orange-50' : 'bg-muted/30'}">
						<div class="flex items-center justify-between text-[9px]">
							<span class="font-mono text-[8px] text-muted-foreground truncate max-w-[100px]" title={deviceId}>{deviceId}</span>
							<span class="{pct > 90 ? 'text-red-600 font-bold' : pct > 70 ? 'text-orange-600' : 'text-muted-foreground'}">{pct.toFixed(1)}%</span>
						</div>
						<div class="w-full bg-muted rounded-full h-1.5">
							<div class="h-1.5 rounded-full transition-all duration-500 {pct > 90 ? 'bg-red-500' : pct > 70 ? 'bg-orange-500' : 'bg-blue-500'}" style="width: {Math.min(pct, 100)}%"></div>
						</div>
						<div class="flex justify-between text-[8px] text-muted-foreground">
							<span>{usedGB} / {totalGB} GB</span>
							<span>{availGB} GB 여유</span>
						</div>
					</div>
				{/if}
			{/each}
		</div>
	{/if}

	<!-- Separator -->
	<div class="border-t"></div>

	<!-- Quick Actions -->
	<div class="space-y-1">
		{#if selectedCount > 0}
			<div class="{captionMuted}">{selectedCount}개 디바이스 선택됨</div>
		{:else if visibleDevices.length > 0}
			<div class="text-[9px] text-muted-foreground/60">디바이스를 선택하면 실행할 수 있습니다</div>
		{/if}

		{#each modeButtons as btn}
			<button
				onclick={() => onModeChange(btn.mode)}
				class="w-full flex items-center gap-2 px-2 py-1.5 rounded text-xs transition-colors
					{centerMode === btn.mode
						? 'bg-primary/10 text-primary font-medium'
						: 'text-muted-foreground hover:bg-muted hover:text-foreground'}"
			>
				<btn.icon class="size-3.5" />
				{btn.label}
			</button>
		{/each}
	</div>

	<!-- Active jobs badge (fixed height to prevent layout shift) -->
	<div class="border-t pt-1 h-6">
		{#if activeJobCount > 0}
			<div class="flex items-center gap-1.5 text-[10px] text-blue-600">
				<LoaderIcon class="size-3 animate-spin" />
				<span>{activeJobCount}개 Job 실행 중</span>
			</div>
		{/if}
	</div>
</div>
