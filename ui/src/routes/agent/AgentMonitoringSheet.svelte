<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import { captionMuted } from '$lib/styles/common.js';
	import PerfChart from '$lib/components/perf-chart/PerfChart.svelte';
	import type { DeviceMetricsData } from '$lib/api/agent.js';
	import PlayIcon from '@lucide/svelte/icons/play';
	import SquareIcon from '@lucide/svelte/icons/square';
	import ActivityIcon from '@lucide/svelte/icons/activity';

	interface Props {
		open: boolean;
		deviceId: string | null;
		monitoring: boolean;
		latestMetrics: DeviceMetricsData | null;
		cpuHistory: [number, number][];
		memHistory: [number, number][];
		diskReadHistory: [number, number][];
		diskWriteHistory: [number, number][];
		onStart: () => void;
		onStop: () => void;
	}

	let {
		open = $bindable(),
		deviceId,
		monitoring,
		latestMetrics,
		cpuHistory,
		memHistory,
		diskReadHistory,
		diskWriteHistory,
		onStart,
		onStop
	}: Props = $props();

	function formatKb(kb: number): string {
		if (kb < 1024) return `${kb} KB`;
		if (kb < 1024 * 1024) return `${(kb / 1024).toFixed(1)} MB`;
		return `${(kb / 1024 / 1024).toFixed(1)} GB`;
	}

	function formatBytes(bytes: number): string {
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
		if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
		return `${(bytes / 1024 / 1024 / 1024).toFixed(1)} GB`;
	}

	let cpuOption = $derived({
		title: { text: 'CPU (%)', textStyle: { fontSize: 11 } },
		tooltip: { trigger: 'axis' as const },
		xAxis: { type: 'time' as const, show: false },
		yAxis: { type: 'value' as const, min: 0, max: 100 },
		series: [{ data: cpuHistory, type: 'line' as const, smooth: true, areaStyle: { opacity: 0.15 }, name: 'CPU', showSymbol: false }],
		dataZoom: [{ type: 'inside' as const }],
		grid: { left: 40, right: 10, top: 30, bottom: 10 }
	});

	let memOption = $derived({
		title: { text: 'Memory (%)', textStyle: { fontSize: 11 } },
		tooltip: { trigger: 'axis' as const },
		xAxis: { type: 'time' as const, show: false },
		yAxis: { type: 'value' as const, min: 0, max: 100 },
		series: [{ data: memHistory, type: 'line' as const, smooth: true, areaStyle: { opacity: 0.15 }, name: 'Mem', itemStyle: { color: '#ee6666' }, showSymbol: false }],
		dataZoom: [{ type: 'inside' as const }],
		grid: { left: 40, right: 10, top: 30, bottom: 10 }
	});

	let diskOption = $derived({
		title: { text: 'Disk I/O (ops)', textStyle: { fontSize: 11 } },
		tooltip: { trigger: 'axis' as const },
		legend: { data: ['Read', 'Write'], top: 0, right: 10, textStyle: { fontSize: 10 } },
		xAxis: { type: 'time' as const, show: false },
		yAxis: { type: 'value' as const },
		series: [
			{ data: diskReadHistory, type: 'line' as const, smooth: true, name: 'Read', itemStyle: { color: '#5470c6' }, showSymbol: false },
			{ data: diskWriteHistory, type: 'line' as const, smooth: true, name: 'Write', itemStyle: { color: '#fc8452' }, showSymbol: false }
		],
		dataZoom: [{ type: 'inside' as const }],
		grid: { left: 40, right: 10, top: 30, bottom: 10 }
	});
</script>

<Sheet.Root bind:open>
	<Sheet.Content side="right" class="w-[480px] flex flex-col max-h-[100dvh]">
		<Sheet.Header>
			<Sheet.Title class="text-sm flex items-center gap-2">
				디바이스 모니터링
				{#if monitoring}
					<ActivityIcon class="size-3 text-green-600 animate-pulse" />
				{/if}
			</Sheet.Title>
			<Sheet.Description class="text-xs font-mono">{deviceId ?? ''}</Sheet.Description>
		</Sheet.Header>

		<div class="flex items-center gap-2 py-2">
			{#if monitoring}
				<button onclick={onStop} class="inline-flex items-center gap-1 rounded-md border border-red-300 px-2.5 py-1 text-[10px] text-red-600 hover:bg-red-50">
					<SquareIcon class="size-3" /> 중지
				</button>
				<span class="text-[10px] text-green-600">시트를 닫아도 연결이 유지됩니다</span>
			{:else}
				<button onclick={onStart} disabled={!deviceId} class="inline-flex items-center gap-1 rounded-md border px-2.5 py-1 text-[10px] hover:bg-muted disabled:opacity-50">
					<PlayIcon class="size-3" /> 시작
				</button>
			{/if}
		</div>

		<div class="flex-1 overflow-y-auto space-y-2">
			{#if latestMetrics}
				<!-- Summary cards -->
				<div class="grid grid-cols-2 gap-2">
					{#if latestMetrics.cpu}
						<div class="border rounded-md p-2">
							<div class="{captionMuted}">CPU</div>
							<div class="text-sm font-semibold">{latestMetrics.cpu.usagePercent.toFixed(1)}%</div>
							<div class="text-[9px] text-muted-foreground">{latestMetrics.cpu.perCorePercent.length} cores</div>
						</div>
					{/if}
					{#if latestMetrics.memory}
						<div class="border rounded-md p-2">
							<div class="{captionMuted}">Memory</div>
							<div class="text-sm font-semibold">{latestMetrics.memory.usagePercent.toFixed(1)}%</div>
							<div class="text-[9px] text-muted-foreground">{formatKb(latestMetrics.memory.usedKb)} / {formatKb(latestMetrics.memory.totalKb)}</div>
						</div>
					{/if}
					{#if latestMetrics.disk}
						<div class="border rounded-md p-2">
							<div class="{captionMuted}">Disk I/O</div>
							<div class="text-sm font-semibold">R:{latestMetrics.disk.readIos} W:{latestMetrics.disk.writeIos}</div>
						</div>
					{/if}
					{#if latestMetrics.dataPartition}
						<div class="border rounded-md p-2">
							<div class="{captionMuted}">{latestMetrics.dataPartition.mountPoint}</div>
							<div class="text-sm font-semibold">{latestMetrics.dataPartition.usagePercent.toFixed(1)}%</div>
							<div class="text-[9px] text-muted-foreground">{formatBytes(latestMetrics.dataPartition.usedBytes)} / {formatBytes(latestMetrics.dataPartition.totalBytes)}</div>
						</div>
					{/if}
				</div>

				<!-- Charts -->
				<div class="space-y-2">
					<div class="border rounded-md p-1"><PerfChart option={cpuOption} height="160px" /></div>
					<div class="border rounded-md p-1"><PerfChart option={memOption} height="160px" /></div>
					<div class="border rounded-md p-1"><PerfChart option={diskOption} height="160px" /></div>
				</div>
			{:else if monitoring}
				<div class="text-center text-xs text-muted-foreground py-8">
					<span class="dsy-loading dsy-loading-spinner dsy-loading-sm"></span>
					<div class="mt-1">데이터 수신 대기 중...</div>
				</div>
			{:else}
				<div class="text-center text-xs text-muted-foreground py-8">
					시작 버튼을 눌러 모니터링을 시작하세요
				</div>
			{/if}
		</div>
	</Sheet.Content>
</Sheet.Root>
