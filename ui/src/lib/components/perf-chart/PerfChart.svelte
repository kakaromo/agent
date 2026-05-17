<script lang="ts">
	import * as echarts from 'echarts';
	import { onDestroy } from 'svelte';
	import { lttb } from '$lib/utils/downsample';
	import { sharedColors } from './shine-theme.ts';
	import ChartColorPicker from './ChartColorPicker.svelte';
	import PaletteIcon from '@lucide/svelte/icons/palette';

	interface Props {
		option: echarts.EChartsOption;
		height?: string;
	}

	let { option, height = '400px' }: Props = $props();

	let container: HTMLDivElement;
	let chart: echarts.ECharts | undefined;
	let resizeObserver: ResizeObserver | undefined;
	let loading = $state(true);

	// ── 색상 오버라이드 ──
	let showColorPicker = $state(false);
	let colorOverrides = $state(new Map<string, string>());

	const seriesNames = $derived(
		(Array.isArray(option.series) ? option.series : option.series ? [option.series] : [])
			.map((s: any) => s.name as string)
			.filter(Boolean)
	);

	const seriesInfo = $derived(
		seriesNames.map((name, i) => ({
			name,
			color: colorOverrides.get(name) ?? sharedColors[i % sharedColors.length]
		}))
	);

	function augmentOption(opt: echarts.EChartsOption): echarts.EChartsOption {
		if (colorOverrides.size === 0) return opt;
		const series = Array.isArray(opt.series) ? opt.series : opt.series ? [opt.series] : [];
		const augmented = series.map((s: any, i: number) => {
			const c = colorOverrides.get(s.name) ?? sharedColors[i % sharedColors.length];
			return {
				...s,
				itemStyle: { ...s.itemStyle, color: c },
				lineStyle: { ...s.lineStyle, color: c }
			};
		});
		return { ...opt, series: augmented };
	}

	function handleColorChange(name: string, color: string) {
		const next = new Map(colorOverrides);
		next.set(name, color);
		colorOverrides = next;
	}

	function handleResetAll() {
		colorOverrides = new Map();
	}

	// ── ECharts ──

	const LARGE_THRESHOLD = 10_000;
	const DOWNSAMPLE_THRESHOLD = 50_000;
	const DOWNSAMPLE_TARGET = 5_000;

	function optimizeForLargeData(opt: echarts.EChartsOption): echarts.EChartsOption {
		const series = Array.isArray(opt.series) ? opt.series : opt.series ? [opt.series] : [];
		const totalPoints = series.reduce(
			(sum: number, s: any) => sum + (Array.isArray(s?.data) ? s.data.length : 0),
			0
		);

		if (totalPoints < LARGE_THRESHOLD) return opt;

		const optimizedSeries = series.map((s: any) => {
			const optimized: any = {
				...s,
				large: true,
				largeThreshold: 5000,
				progressive: 3000,
				progressiveThreshold: 5000,
				animation: false
			};

			if (s.type === 'line') {
				optimized.sampling = 'lttb';
			}

			if (
				Array.isArray(s.data) &&
				s.data.length >= DOWNSAMPLE_THRESHOLD &&
				s.type === 'line'
			) {
				const isXYPair =
					s.data.length > 0 &&
					Array.isArray(s.data[0]) &&
					s.data[0].length === 2 &&
					typeof s.data[0][0] === 'number';

				if (isXYPair) {
					optimized.data = lttb(s.data as [number, number][], DOWNSAMPLE_TARGET);
				}
			}

			return optimized;
		});

		let dataZoom = Array.isArray(opt.dataZoom) ? opt.dataZoom : opt.dataZoom ? [opt.dataZoom] : [];
		const hasSlider = dataZoom.some((z: any) => z.type === 'slider');
		if (!hasSlider) {
			dataZoom = [...dataZoom, { type: 'slider', bottom: 10 }];
		}

		return {
			...opt,
			series: optimizedSeries,
			dataZoom
		};
	}

	function getTheme(): string {
		return 'shine';
	}

	function bindFinished(c: echarts.ECharts) {
		loading = true;
		c.on('finished', function handler() {
			c.off('finished', handler);
			loading = false;
		});
	}

	function rebuildChart() {
		if (!container) return;
		chart?.dispose();
		chart = echarts.init(container, getTheme());
		bindFinished(chart);
		chart.setOption(optimizeForLargeData(augmentOption(option)), true);
	}

	$effect(() => {
		if (!container) return;

		if (!chart) {
			chart = echarts.init(container, getTheme());
			resizeObserver = new ResizeObserver(() => {
				chart?.resize();
			});
			resizeObserver.observe(container);
		}

		bindFinished(chart);
		chart.setOption(optimizeForLargeData(augmentOption(option)), true);
	});

	// colorOverrides 변경 시 차트 즉시 업데이트
	$effect(() => {
		if (!chart) return;
		// colorOverrides를 읽어서 반응성 트리거
		const _ = colorOverrides;
		chart.setOption(optimizeForLargeData(augmentOption(option)), true);
	});

	export function getImageDataURL(): string | null {
		if (!chart) return null;
		return chart.getDataURL({ type: 'png', pixelRatio: 2, backgroundColor: '#ffffff' });
	}

	export function getImageDataURLAsync(): Promise<string | null> {
		return new Promise((resolve) => {
			if (!chart) {
				resolve(null);
				return;
			}
			chart.on('finished', function handler() {
				chart!.off('finished', handler);
				resolve(chart!.getDataURL({ type: 'png', pixelRatio: 2, backgroundColor: '#ffffff' }));
			});
		});
	}

	onDestroy(() => {
		resizeObserver?.disconnect();
		chart?.dispose();
	});
</script>

<div class="relative" style="height: {height};">
	<div bind:this={container} style="width: 100%; height: 100%;"></div>

	{#if loading}
		<div class="absolute inset-0 flex items-center justify-center bg-background/50">
			<span class="dsy-loading dsy-loading-spinner dsy-loading-md text-primary"></span>
		</div>
	{/if}

	<!-- Palette button -->
	{#if seriesNames.length > 0}
		<button
			class="absolute top-1 right-1 z-10 p-1.5 rounded-md hover:bg-muted/80 text-muted-foreground/50 hover:text-muted-foreground transition-colors"
			onclick={() => (showColorPicker = !showColorPicker)}
			title="Legend 색상 변경"
		>
			<PaletteIcon class="size-3.5" />
		</button>
	{/if}

	<!-- Color picker panel -->
	{#if showColorPicker}
		<ChartColorPicker
			{seriesInfo}
			onColorChange={handleColorChange}
			onResetAll={handleResetAll}
			onClose={() => (showColorPicker = false)}
		/>
	{/if}
</div>
