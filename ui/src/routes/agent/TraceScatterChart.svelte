<script lang="ts">
	import * as echarts from 'echarts';
	import { onDestroy } from 'svelte';

	export interface BrushRange {
		timeMin: number; timeMax: number;
		yMin: number; yMax: number;
		chartKey?: string;
	}

	interface Props {
		option: echarts.EChartsOption;
		height?: string;
		chartKey?: string;
		legendSelected?: Record<string, boolean>;
		onLegendChanged?: (selected: Record<string, boolean>) => void;
		onBrushSelected?: (ranges: BrushRange) => void;
	}

	let { option, height = '200px', chartKey, legendSelected, onLegendChanged, onBrushSelected }: Props = $props();

	let container: HTMLDivElement;
	let chart: echarts.ECharts | undefined;
	let showContextMenu = $state(false);
	let menuX = $state(0);
	let menuY = $state(0);
	let resizeObserver: ResizeObserver | undefined;

	function rebuildChart() {
		if (!container) return;
		chart?.dispose();
		chart = echarts.init(container);
		bindEvents();
		applyOption();
	}

	function applyOption() {
		if (!chart) return;
		const opt = { ...option } as any;
		// Inject brush config
		if (!opt.brush) {
			opt.brush = {
				toolbox: ['rect', 'clear'],
				brushType: false,
				xAxisIndex: 0,
				yAxisIndex: 0,
				brushStyle: { borderWidth: 1, color: 'rgba(59, 130, 246, 0.1)', borderColor: '#3b82f6' }
			};
		}
		// Inject large mode for scatter
		if (Array.isArray(opt.series)) {
			opt.series = opt.series.map((s: any) => ({
				...s,
				large: true,
				largeThreshold: 5000,
				progressive: 5000,
				progressiveThreshold: 10000
			}));
		}
		// Apply legend selection
		if (legendSelected && opt.legend) {
			opt.legend.selected = legendSelected;
		}
		chart.setOption(opt, true);
	}

	function bindEvents() {
		if (!chart) return;

		chart.on('brushEnd', (params: any) => {
			if (!onBrushSelected) return;
			const areas = params.areas;
			if (!areas || areas.length === 0) return;
			const area = areas[0];
			if (area.coordRange) {
				const xRange = area.coordRange[0];
				const yRange = area.coordRange[1];
				if (Array.isArray(xRange) && Array.isArray(yRange)) {
					if (Math.abs(xRange[1] - xRange[0]) > 0.01) {
						onBrushSelected({ timeMin: xRange[0], timeMax: xRange[1], yMin: yRange[0], yMax: yRange[1], chartKey });
					}
				}
			}
			// 드래그 완료 후 brush 비활성화 (일회성)
			chart?.dispatchAction({ type: 'brush', areas: [] });
			chart?.dispatchAction({ type: 'takeGlobalCursor', key: 'brush', brushOption: { brushType: false } });
		});

		chart.on('brushEnd', (params: any) => {
			if (!onBrushSelected) return;
			const areas = params.areas;
			if (!areas || areas.length === 0) return;
			const area = areas[0];
			if (area.coordRange) {
				const xRange = area.coordRange[0];
				const yRange = area.coordRange[1];
				if (Array.isArray(xRange) && Array.isArray(yRange)) {
					onBrushSelected({ timeMin: xRange[0], timeMax: xRange[1], yMin: yRange[0], yMax: yRange[1], chartKey });
				}
			}
		});

		chart.on('legendselectchanged', (params: any) => {
			if (onLegendChanged) {
				onLegendChanged(params.selected);
			}
		});
	}

	$effect(() => {
		if (!container) return;
		if (!chart) {
			chart = echarts.init(container);
			resizeObserver = new ResizeObserver(() => chart?.resize());
			resizeObserver.observe(container);
			bindEvents();
		}
		applyOption();
	});

	// Sync legend from parent
	$effect(() => {
		if (chart && legendSelected) {
			const currentOpt = chart.getOption() as any;
			if (currentOpt?.legend?.[0]?.selected) {
				const current = currentOpt.legend[0].selected;
				for (const [name, val] of Object.entries(legendSelected)) {
					if (current[name] !== val) {
						chart.dispatchAction({
							type: val ? 'legendSelect' : 'legendUnSelect',
							name
						});
					}
				}
			}
		}
	});

	function activateBrush() {
		if (!chart) return;
		chart.dispatchAction({ type: 'takeGlobalCursor', key: 'brush', brushOption: { brushType: 'rect', brushMode: 'single' } });
		showContextMenu = false;
	}

	function handleContextMenu(e: MouseEvent) {
		e.preventDefault();
		menuX = e.clientX;
		menuY = e.clientY;
		showContextMenu = true;
	}

	function handleClickOutside() {
		showContextMenu = false;
	}

	onDestroy(() => {
		resizeObserver?.disconnect();
		chart?.dispose();
	});
</script>

<svelte:window onclick={handleClickOutside} />

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="relative" style="height: {height};" oncontextmenu={handleContextMenu}>
	<div bind:this={container} style="width: 100%; height: 100%;"></div>

	{#if showContextMenu}
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<div
			class="fixed z-50 bg-background border rounded-md shadow-lg py-1 min-w-[120px]"
			style="left: {menuX}px; top: {menuY}px;"
			onclick={(e) => e.stopPropagation()}
		>
			<button
				class="w-full text-left px-3 py-1.5 text-xs hover:bg-muted transition-colors flex items-center gap-2"
				onclick={activateBrush}
			>
				<svg class="size-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2" /><path d="M9 3v18M3 9h18" /></svg>
				Zoom 선택
			</button>
		</div>
	{/if}
</div>
