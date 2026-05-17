<script lang="ts">
	import PerfChart from '$lib/components/perf-chart/PerfChart.svelte';
	import type { EChartsOption } from 'echarts';
	import PlayIcon from '@lucide/svelte/icons/play';
	import PauseIcon from '@lucide/svelte/icons/pause';
	import { onDestroy } from 'svelte';

	// 입력 형태:
	//   1) entries (여러 시점): [{ time, segment_info: { lines: [...] } }, ...]
	//   2) lines (단일 프레임, 하위 호환)
	interface Props {
		entries?: Array<Record<string, any>>;
		lines?: string[];
		height?: string;
	}
	let { entries, lines, height = '420px' }: Props = $props();

	const TYPE_NAMES = ['HD', 'WD', 'CD', 'HN', 'WN', 'CN'];
	const TYPE_COLORS = ['#ef4444', '#f97316', '#eab308', '#3b82f6', '#8b5cf6', '#10b981'];
	const MAX_VALID = 512;

	interface Cell { row: number; col: number; type: number; valid: number; }
	interface Frame {
		time: number | null;
		cells: Cell[];
		rowCount: number;
		colCount: number;
		totalValid: number;
		totalPossible: number;
		byType: number[]; // valid 합계 by type
	}

	function parseLinesToCells(ls: string[]): { cells: Cell[]; rowCount: number; colCount: number } {
		const cells: Cell[] = [];
		let maxCol = 0;
		let rowCount = 0;
		for (const raw of ls ?? []) {
			if (!raw) continue;
			const line = raw.trim();
			if (!line) continue;
			if (line.startsWith('#') || line.startsWith('=')) continue;
			if (line.startsWith('format:') || line.startsWith('segment_type')) continue;

			const tokens = line.split(/\s+/);
			if (tokens.length < 2) continue;
			const base = parseInt(tokens[0], 10);
			if (!Number.isFinite(base)) continue;
			let colIdx = 0;
			for (let i = 1; i < tokens.length; i++) {
				const m = tokens[i].match(/^(\d+)\|(\d+)/);
				if (!m) continue;
				const type = parseInt(m[1], 10);
				const valid = parseInt(m[2], 10);
				cells.push({ row: rowCount, col: colIdx, type, valid });
				if (colIdx + 1 > maxCol) maxCol = colIdx + 1;
				colIdx++;
			}
			if (colIdx > 0) rowCount++;
		}
		return { cells, rowCount, colCount: maxCol };
	}

	function buildFrame(time: number | null, ls: string[]): Frame {
		const { cells, rowCount, colCount } = parseLinesToCells(ls);
		const byType = [0, 0, 0, 0, 0, 0];
		let totalValid = 0;
		for (const c of cells) {
			totalValid += c.valid;
			if (c.type >= 0 && c.type < 6) byType[c.type] += c.valid;
		}
		return {
			time,
			cells,
			rowCount,
			colCount,
			totalValid,
			totalPossible: cells.length * MAX_VALID,
			byType
		};
	}

	function extractLinesFromEntry(e: Record<string, any>): string[] {
		const segBlock = e?.segment_info ?? e;
		return Array.isArray(segBlock?.lines) ? segBlock.lines : [];
	}

	const frames = $derived<Frame[]>(
		entries && entries.length > 0
			? entries.map((e) => buildFrame(typeof e.time === 'number' ? e.time : null, extractLinesFromEntry(e)))
			: lines
				? [buildFrame(null, lines)]
				: []
	);

	// 프레임 인덱스 (타임라인 슬라이더)
	let frameIdx = $state(0);
	let playing = $state(false);
	let playTimer: ReturnType<typeof setInterval> | null = null;
	let playSpeedMs = $state(800);

	$effect(() => {
		if (frames.length > 0 && frameIdx >= frames.length) frameIdx = frames.length - 1;
	});

	onDestroy(() => { if (playTimer) clearInterval(playTimer); });

	function togglePlay() {
		if (frames.length < 2) return;
		if (playing) {
			playing = false;
			if (playTimer) { clearInterval(playTimer); playTimer = null; }
		} else {
			playing = true;
			playTimer = setInterval(() => {
				frameIdx = (frameIdx + 1) % frames.length;
			}, playSpeedMs);
		}
	}

	// 옵션 상태
	let showLabels = $state(false);
	let selectedTypes = $state<Set<number>>(new Set([0, 1, 2, 3, 4, 5])); // 집계 차트 표시 타입
	function toggleChartType(t: number) {
		const next = new Set(selectedTypes);
		if (next.has(t)) next.delete(t); else next.add(t);
		selectedTypes = next;
	}

	const activeFrame = $derived<Frame | null>(frames[frameIdx] ?? null);
	const usage = $derived(activeFrame && activeFrame.totalPossible > 0
		? activeFrame.totalValid / activeFrame.totalPossible
		: 0);

	// ── 그리드 chart option (custom 시리즈 + 숫자 라벨) ──
	const gridOption = $derived<EChartsOption | null>(buildGridOption(activeFrame, showLabels));

	function buildGridOption(f: Frame | null, withLabels: boolean): EChartsOption | null {
		if (!f || f.cells.length === 0) return null;
		return {
			tooltip: {
				formatter: (p: any) => {
					const info = p.data?.info as { row: number; col: number; type: number; valid: number; seg: number } | undefined;
					if (!info) return '';
					const name = TYPE_NAMES[info.type] ?? String(info.type);
					const pct = ((info.valid / MAX_VALID) * 100).toFixed(1);
					return `seg=${info.seg}<br/>type=${name}<br/>valid=${info.valid}/${MAX_VALID} (${pct}%)`;
				}
			},
			grid: { left: 50, right: 20, top: 30, bottom: 30 },
			xAxis: {
				type: 'category',
				data: Array.from({ length: f.colCount }, (_, i) => String(i)),
				name: 'col',
				nameLocation: 'middle',
				nameGap: 20,
				splitArea: { show: false }
			},
			yAxis: {
				type: 'category',
				data: Array.from({ length: f.rowCount }, (_, i) => String(i)),
				name: 'row',
				inverse: true,
				splitArea: { show: false }
			},
			series: [
				{
					name: 'segments',
					type: 'custom',
					coordinateSystem: 'cartesian2d',
					renderItem: (params: any, api: any) => {
						const col = api.value(0);
						const row = api.value(1);
						const type = api.value(2);
						const valid = api.value(3);
						const point = api.coord([col, row]);
						const size = api.size([1, 1]);
						const color = TYPE_COLORS[type as number] ?? '#999';
						const opacity = 0.25 + 0.75 * (valid / MAX_VALID);
						const children: any[] = [
							{
								type: 'rect',
								shape: {
									x: point[0] - size[0] / 2,
									y: point[1] - size[1] / 2,
									width: size[0],
									height: size[1]
								},
								style: {
									fill: color,
									opacity,
									stroke: 'rgba(0,0,0,0.15)',
									lineWidth: 0.5
								}
							}
						];
						if (withLabels && size[0] >= 22 && size[1] >= 14) {
							children.push({
								type: 'text',
								style: {
									text: String(valid),
									x: point[0],
									y: point[1],
									textAlign: 'center',
									textVerticalAlign: 'middle',
									fill: opacity > 0.6 ? '#fff' : '#111',
									fontSize: Math.min(10, Math.floor(size[1] * 0.6))
								}
							});
						}
						return { type: 'group', children };
					},
					encode: { x: 0, y: 1, tooltip: [0, 1, 2, 3] },
					data: f.cells.map((c) => ({
						value: [c.col, c.row, c.type, c.valid],
						info: {
							row: c.row,
							col: c.col,
							type: c.type,
							valid: c.valid,
							seg: c.row * f.colCount + c.col
						}
					}))
				}
			]
		};
	}

	// ── 집계 시계열 chart option ──
	const aggregateOption = $derived<EChartsOption | null>(buildAggregateOption());

	function buildAggregateOption(): EChartsOption | null {
		if (frames.length < 2) return null;
		const baseTime = frames[0].time ?? 0;
		const baseX = frames.map((f, i) => (f.time != null ? f.time - baseTime : i));
		const series: any[] = [];
		for (let t = 0; t < 6; t++) {
			if (!selectedTypes.has(t)) continue;
			series.push({
				name: TYPE_NAMES[t],
				type: 'line' as const,
				data: frames.map((f, i) => [baseX[i], f.byType[t]]),
				showSymbol: frames.length < 50,
				itemStyle: { color: TYPE_COLORS[t] },
				lineStyle: { color: TYPE_COLORS[t] },
				smooth: false
			});
		}
		if (series.length === 0) return null;
		return {
			title: {
				text: 'Type별 valid blocks 합계 추이',
				left: 'center',
				textStyle: { fontSize: 11 }
			},
			tooltip: {
				trigger: 'axis',
				formatter: (params: any) => {
					const arr = Array.isArray(params) ? params : [params];
					if (arr.length === 0) return '';
					const dt = arr[0].value?.[0] ?? 0;
					const absTime = frames[0].time != null
						? new Date((frames[0].time + dt) * 1000).toLocaleTimeString()
						: `frame ${arr[0].dataIndex}`;
					const body = arr
						.map((p: any) => `${p.marker} ${p.seriesName}: <b>${(p.value?.[1] ?? 0).toLocaleString()}</b>`)
						.join('<br/>');
					return `${absTime}<br/>${body}`;
				}
			},
			legend: {
				bottom: 0,
				textStyle: { fontSize: 10 },
				selected: Object.fromEntries(TYPE_NAMES.map((n, i) => [n, selectedTypes.has(i)]))
			},
			grid: { left: 70, right: 20, top: 30, bottom: 50 },
			xAxis: {
				type: 'value' as const,
				name: frames[0].time != null ? 'Time (s)' : 'Frame',
				nameLocation: 'middle' as const,
				nameGap: 22,
				nameTextStyle: { fontSize: 10 }
			},
			yAxis: {
				type: 'value' as const,
				name: 'valid blocks (sum)',
				nameLocation: 'middle' as const,
				nameGap: 55,
				nameTextStyle: { fontSize: 10 }
			},
			dataZoom: [{ type: 'inside' as const }],
			series
		};
	}

	const frameTimeLabel = $derived(() => {
		if (!activeFrame) return '';
		if (activeFrame.time != null) return new Date(activeFrame.time * 1000).toLocaleString();
		return `frame ${frameIdx}`;
	});
</script>

<div class="space-y-3">
	<!-- 범례 + 옵션 바 -->
	<div class="flex items-center gap-3 flex-wrap text-[10px] text-muted-foreground">
		<span>그리드 {activeFrame?.rowCount ?? 0} × {activeFrame?.colCount ?? 0}</span>
		<span>·</span>
		{#each TYPE_NAMES as name, i (name)}
			<button
				class="inline-flex items-center gap-1 px-1 rounded transition-colors {selectedTypes.has(i) ? '' : 'opacity-40 line-through'} hover:bg-muted"
				onclick={() => toggleChartType(i)}
				title="집계 차트 표시 토글"
			>
				<span class="inline-block w-2 h-2 rounded-sm" style:background-color={TYPE_COLORS[i]}></span>
				{name}
			</button>
		{/each}
		<label class="ml-auto inline-flex items-center gap-1 cursor-pointer">
			<input type="checkbox" bind:checked={showLabels} class="size-3" />
			valid 숫자 표시
		</label>
	</div>

	<!-- 현재 프레임 요약 + 타임라인 컨트롤 -->
	{#if activeFrame}
		<div class="rounded border bg-card p-2 space-y-1.5">
			<div class="flex items-center gap-2 text-[11px]">
				<span class="font-medium">{frameTimeLabel()}</span>
				<span class="text-muted-foreground">·</span>
				<span>
					valid: <span class="font-mono tabular-nums">{activeFrame.totalValid.toLocaleString()}</span> /
					<span class="font-mono tabular-nums">{activeFrame.totalPossible.toLocaleString()}</span>
					<span class="text-muted-foreground">({(usage * 100).toFixed(1)}%)</span>
				</span>
				<div class="ml-auto flex items-center gap-1">
					{#if frames.length > 1}
						<button
							class="inline-flex items-center justify-center size-6 rounded border hover:bg-muted"
							onclick={togglePlay}
							title={playing ? '일시정지' : '재생'}
						>
							{#if playing}
								<PauseIcon class="size-3" />
							{:else}
								<PlayIcon class="size-3" />
							{/if}
						</button>
						<select
							bind:value={playSpeedMs}
							class="h-6 text-[10px] border rounded px-1 bg-background"
							title="재생 속도"
							onchange={() => { if (playing) { togglePlay(); togglePlay(); } }}
						>
							<option value={1500}>0.7x</option>
							<option value={800}>1x</option>
							<option value={400}>2x</option>
							<option value={200}>4x</option>
						</select>
						<span class="text-[10px] text-muted-foreground tabular-nums">{frameIdx + 1}/{frames.length}</span>
					{/if}
				</div>
			</div>
			<!-- usage bar -->
			<div class="h-1.5 rounded bg-muted overflow-hidden">
				<div class="h-full bg-primary transition-all" style:width="{usage * 100}%"></div>
			</div>
			{#if frames.length > 1}
				<input
					type="range"
					min="0"
					max={frames.length - 1}
					bind:value={frameIdx}
					class="w-full h-2 cursor-pointer"
				/>
			{/if}
		</div>
	{/if}

	<!-- 그리드 -->
	{#if !activeFrame || activeFrame.cells.length === 0}
		<div class="text-center py-8 text-muted-foreground text-sm">파싱 가능한 세그먼트 행이 없습니다</div>
	{:else}
		<div class="resize-y overflow-hidden rounded border bg-card" style="height: {height}; min-height: 200px;">
			{#if gridOption}
				<PerfChart option={gridOption} height="100%" />
			{/if}
		</div>
	{/if}

	<!-- 집계 시계열 -->
	{#if aggregateOption}
		<div class="resize-y overflow-hidden rounded border bg-card" style="height: 240px; min-height: 160px;">
			<PerfChart option={aggregateOption} height="100%" />
		</div>
	{:else if frames.length > 1}
		<div class="text-[10px] text-muted-foreground text-center py-2">
			집계 차트를 보려면 범례에서 type을 하나 이상 선택하세요.
		</div>
	{/if}
</div>
