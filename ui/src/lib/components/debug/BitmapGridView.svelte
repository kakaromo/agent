<script lang="ts">
	// victim_bits / segment_bits Canvas 뷰어 — 시계열 + 지속성 색상
	//
	// 입력:
	//   entries?: Array<{ time, <format>: { lines } }>
	//   lines?: string[] (단일, 하위 호환)
	// 각 엔트리의 lines를 Frame(row 배열)으로 파싱.
	// 여러 시점 있으면 타임라인/재생/카운트 라인/지속성 모드 제공.

	import PerfChart from '$lib/components/perf-chart/PerfChart.svelte';
	import type { EChartsOption } from 'echarts';
	import PlayIcon from '@lucide/svelte/icons/play';
	import PauseIcon from '@lucide/svelte/icons/pause';
	import { onDestroy } from 'svelte';

	interface Props {
		entries?: Array<Record<string, any>>;
		lines?: string[];
		format: string;
	}
	let { entries, lines, format }: Props = $props();

	interface Row {
		offset: number;
		bits: Uint8Array;
	}
	interface Frame {
		time: number | null;
		rows: Row[];
		totalBits: number;
		setCount: number;
		widest: number;
	}

	function parseLines(ls: string[], fmt: string): Row[] {
		const out: Row[] = [];
		for (const raw of ls ?? []) {
			const line = (raw ?? '').trim();
			if (!line) continue;
			if (line.startsWith('#') || line.startsWith('=')) continue;
			if (line.startsWith('format:')) continue;

			// "OFFSET: bits"
			if (/^\d+\s*:/.test(line)) {
				const colon = line.indexOf(':');
				const offset = parseInt(line.substring(0, colon), 10);
				const rest = line.substring(colon + 1).trim().replace(/\s+/g, '');
				const bits = new Uint8Array(rest.length);
				for (let i = 0; i < rest.length; i++) {
					const ch = rest.charCodeAt(i);
					if (ch === 49) bits[i] = 1;
					else if (ch === 48) bits[i] = 0;
					else {
						const n = parseInt(rest[i], 16);
						bits[i] = Number.isFinite(n) ? n & 1 : 0;
					}
				}
				out.push({ offset, bits });
				continue;
			}

			// "  0      3|512|ffff ffff ..."
			const m = line.match(/^\s*(\d+)\s+(\d+)\|(\d+)\|(.+)$/);
			if (m) {
				const seg = parseInt(m[1], 10);
				const hexPart = m[4].replace(/\s+/g, '');
				const bits = new Uint8Array(hexPart.length * 4);
				for (let i = 0; i < hexPart.length; i++) {
					const n = parseInt(hexPart[i], 16);
					if (!Number.isFinite(n)) continue;
					bits[i * 4] = (n >> 3) & 1;
					bits[i * 4 + 1] = (n >> 2) & 1;
					bits[i * 4 + 2] = (n >> 1) & 1;
					bits[i * 4 + 3] = n & 1;
				}
				out.push({ offset: seg, bits });
			}
		}
		return out;
	}

	function buildFrame(time: number | null, rows: Row[]): Frame {
		let setCount = 0;
		let totalBits = 0;
		let widest = 0;
		for (const r of rows) {
			widest = Math.max(widest, r.bits.length);
			totalBits += r.bits.length;
			for (let i = 0; i < r.bits.length; i++) if (r.bits[i]) setCount++;
		}
		return { time, rows, totalBits, setCount, widest };
	}

	function extractLines(e: Record<string, any>, fmt: string): string[] {
		// entries 형태: { time, victim_bits: { lines } } 혹은 { time, segment_bits: { lines } }
		const block = e?.[fmt] ?? e;
		return Array.isArray(block?.lines) ? block.lines : [];
	}

	const frames = $derived<Frame[]>(
		entries && entries.length > 0
			? entries.map((e) => buildFrame(
				typeof e.time === 'number' ? e.time : null,
				parseLines(extractLines(e, format), format)
			))
			: lines
				? [buildFrame(null, parseLines(lines, format))]
				: []
	);

	let frameIdx = $state(0);
	let playing = $state(false);
	let playTimer: ReturnType<typeof setInterval> | null = null;
	let playSpeedMs = $state(800);
	let cellSize = $state(6);
	let showPersistence = $state(false);
	let canvas: HTMLCanvasElement | undefined = $state();
	let hover = $state<{ rowOffset: number; bit: number; value: number; persistence?: number } | null>(null);

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
			playTimer = setInterval(() => { frameIdx = (frameIdx + 1) % frames.length; }, playSpeedMs);
		}
	}

	const activeFrame = $derived<Frame | null>(frames[frameIdx] ?? null);
	const usage = $derived(activeFrame && activeFrame.totalBits > 0
		? activeFrame.setCount / activeFrame.totalBits
		: 0);

	// ── persistence 계산: 같은 (rowOffset, bit) 좌표가 몇 프레임 연속 켜져 있는가
	// entries 수집 순서를 유지하므로 시간 순서대로 프레임마다 비교
	const persistence = $derived.by<Map<string, number>>(() => {
		const map = new Map<string, number>();
		if (!activeFrame || frames.length < 2 || !showPersistence) return map;
		// 현재 프레임까지 연속으로 1인 지속 카운트 계산
		// key: "rowOffset_bitIdx"
		for (let r = 0; r < activeFrame.rows.length; r++) {
			const row = activeFrame.rows[r];
			for (let b = 0; b < row.bits.length; b++) {
				if (!row.bits[b]) continue;
				let count = 1;
				// 뒤로 거슬러 올라가며 해당 좌표가 계속 1이었는지 확인
				for (let f = frameIdx - 1; f >= 0; f--) {
					const prev = frames[f];
					const prevRow = prev.rows.find((pr) => pr.offset === row.offset);
					if (!prevRow || b >= prevRow.bits.length || !prevRow.bits[b]) break;
					count++;
				}
				map.set(`${row.offset}_${b}`, count);
			}
		}
		return map;
	});

	function colorFor(value: number, persist: number | undefined): string | null {
		if (!value) return null;
		if (!showPersistence || persist == null) {
			return format === 'segment_bits' ? '#10b981' : '#3b82f6';
		}
		// 지속 프레임 수에 따라: 1프레임=옅은 노랑, 중간=주황, 전체=진한 빨강
		const ratio = Math.min(persist / Math.max(1, frames.length), 1);
		if (ratio < 0.25) return '#fde68a'; // 옅은 노랑
		if (ratio < 0.5) return '#fb923c'; // 주황
		if (ratio < 0.75) return '#ef4444'; // 빨강
		return '#991b1b'; // 진한 빨강 — 거의 계속 victim
	}

	function draw() {
		if (!canvas || !activeFrame) return;
		const ctx = canvas.getContext('2d');
		if (!ctx) return;
		const dpr = window.devicePixelRatio || 1;
		const w = activeFrame.widest * cellSize;
		const h = activeFrame.rows.length * cellSize;
		canvas.width = Math.max(1, Math.floor(w * dpr));
		canvas.height = Math.max(1, Math.floor(h * dpr));
		canvas.style.width = `${w}px`;
		canvas.style.height = `${h}px`;
		ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
		ctx.fillStyle = '#f3f4f6';
		ctx.fillRect(0, 0, w, h);
		for (let r = 0; r < activeFrame.rows.length; r++) {
			const row = activeFrame.rows[r];
			for (let c = 0; c < row.bits.length; c++) {
				if (!row.bits[c]) continue;
				const p = showPersistence ? persistence.get(`${row.offset}_${c}`) : undefined;
				const color = colorFor(row.bits[c], p);
				if (!color) continue;
				ctx.fillStyle = color;
				ctx.fillRect(c * cellSize, r * cellSize, cellSize, cellSize);
			}
		}
	}

	$effect(() => {
		void activeFrame;
		void cellSize;
		void showPersistence;
		void persistence;
		draw();
	});

	function onMove(e: MouseEvent) {
		if (!canvas || !activeFrame) return;
		const rect = canvas.getBoundingClientRect();
		const x = e.clientX - rect.left;
		const y = e.clientY - rect.top;
		const c = Math.floor(x / cellSize);
		const r = Math.floor(y / cellSize);
		if (r < 0 || r >= activeFrame.rows.length) { hover = null; return; }
		const row = activeFrame.rows[r];
		if (c < 0 || c >= row.bits.length) { hover = null; return; }
		const persist = persistence.get(`${row.offset}_${c}`);
		hover = { rowOffset: row.offset, bit: c, value: row.bits[c], persistence: persist };
	}
	function onLeave() { hover = null; }

	// ── 카운트 시계열 차트 ──
	const countOption = $derived<EChartsOption | null>(buildCountOption());
	function buildCountOption(): EChartsOption | null {
		if (frames.length < 2) return null;
		const baseTime = frames[0].time ?? 0;
		const counts = frames.map((f, i) => [
			f.time != null ? f.time - baseTime : i,
			f.setCount
		] as [number, number]);
		const usageSeries = frames.map((f, i) => [
			f.time != null ? f.time - baseTime : i,
			f.totalBits > 0 ? (f.setCount / f.totalBits) * 100 : 0
		] as [number, number]);
		return {
			title: {
				text: `${format} — set 비트 카운트 & 사용률`,
				left: 'center',
				textStyle: { fontSize: 11 }
			},
			tooltip: {
				trigger: 'axis',
				formatter: (params: any) => {
					const arr = Array.isArray(params) ? params : [params];
					if (arr.length === 0) return '';
					const dt = arr[0].value?.[0] ?? 0;
					const label = frames[0].time != null
						? new Date((frames[0].time + dt) * 1000).toLocaleTimeString()
						: `frame ${arr[0].dataIndex}`;
					return label + '<br/>' + arr.map((p: any) =>
						`${p.marker} ${p.seriesName}: <b>${(p.value?.[1] ?? 0).toLocaleString()}${p.seriesName.includes('%') ? '%' : ''}</b>`
					).join('<br/>');
				}
			},
			legend: { bottom: 0, textStyle: { fontSize: 10 } },
			grid: { left: 60, right: 60, top: 30, bottom: 40 },
			xAxis: {
				type: 'value' as const,
				name: frames[0].time != null ? 'Time (s)' : 'Frame',
				nameLocation: 'middle' as const,
				nameGap: 22,
				nameTextStyle: { fontSize: 10 }
			},
			yAxis: [
				{ type: 'value' as const, name: 'count', nameTextStyle: { fontSize: 10 } },
				{ type: 'value' as const, name: '%', position: 'right', min: 0, max: 100, nameTextStyle: { fontSize: 10 } }
			],
			dataZoom: [{ type: 'inside' as const }],
			series: [
				{
					name: 'set 비트 수',
					type: 'line' as const,
					data: counts,
					yAxisIndex: 0,
					showSymbol: frames.length < 50,
					itemStyle: { color: '#3b82f6' },
					lineStyle: { color: '#3b82f6' }
				},
				{
					name: '사용률 %',
					type: 'line' as const,
					data: usageSeries,
					yAxisIndex: 1,
					showSymbol: frames.length < 50,
					itemStyle: { color: '#f97316' },
					lineStyle: { color: '#f97316', type: 'dashed' }
				}
			]
		};
	}

	const frameLabel = $derived(() => {
		if (!activeFrame) return '';
		if (activeFrame.time != null) return new Date(activeFrame.time * 1000).toLocaleString();
		return `frame ${frameIdx}`;
	});
</script>

<div class="space-y-3">
	<!-- 상단 옵션 바 -->
	<div class="flex items-center gap-3 text-[10px] text-muted-foreground flex-wrap">
		<span>{format}</span>
		<span>·</span>
		<label class="flex items-center gap-1">
			<span>cell</span>
			<input type="range" min="3" max="14" bind:value={cellSize} class="w-24" />
			<span class="tabular-nums w-6 text-right">{cellSize}px</span>
		</label>
		{#if frames.length > 1}
			<label class="flex items-center gap-1 cursor-pointer">
				<input type="checkbox" bind:checked={showPersistence} class="size-3" />
				지속성 색상 (계속 켜진 비트 진한 빨강)
			</label>
		{/if}
		{#if hover}
			<span class="ml-auto font-mono tabular-nums">
				row {hover.rowOffset} · bit {hover.bit} · <span class={hover.value ? 'text-blue-600 font-semibold' : 'text-muted-foreground'}>{hover.value}</span>
				{#if showPersistence && hover.value && hover.persistence != null}
					· persist {hover.persistence}/{frameIdx + 1}
				{/if}
			</span>
		{/if}
	</div>

	<!-- 프레임 컨트롤 -->
	{#if activeFrame}
		<div class="rounded border bg-card p-2 space-y-1.5">
			<div class="flex items-center gap-2 text-[11px]">
				<span class="font-medium">{frameLabel()}</span>
				<span class="text-muted-foreground">·</span>
				<span>
					set: <span class="font-mono tabular-nums">{activeFrame.setCount.toLocaleString()}</span> /
					<span class="font-mono tabular-nums">{activeFrame.totalBits.toLocaleString()}</span>
					<span class="text-muted-foreground">({(usage * 100).toFixed(2)}%)</span>
				</span>
				<div class="ml-auto flex items-center gap-1">
					{#if frames.length > 1}
						<button
							class="inline-flex items-center justify-center size-6 rounded border hover:bg-muted"
							onclick={togglePlay}
							title={playing ? '일시정지' : '재생'}
						>
							{#if playing}<PauseIcon class="size-3" />{:else}<PlayIcon class="size-3" />{/if}
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

	<!-- Canvas 그리드 -->
	{#if !activeFrame || activeFrame.rows.length === 0}
		<div class="text-center py-8 text-muted-foreground text-sm">파싱 가능한 비트맵 행이 없습니다</div>
	{:else}
		<div
			class="overflow-auto rounded border bg-card"
			style="max-height: 60vh;"
			role="img"
			aria-label="{format} bitmap"
		>
			<canvas
				bind:this={canvas}
				onmousemove={onMove}
				onmouseleave={onLeave}
				class="block"
			></canvas>
		</div>
		{#if showPersistence}
			<div class="flex items-center gap-2 text-[10px] text-muted-foreground">
				<span>지속성:</span>
				<span class="inline-flex items-center gap-1"><span class="inline-block w-3 h-3 rounded-sm" style:background-color="#fde68a"></span>짧음</span>
				<span class="inline-flex items-center gap-1"><span class="inline-block w-3 h-3 rounded-sm" style:background-color="#fb923c"></span>중간</span>
				<span class="inline-flex items-center gap-1"><span class="inline-block w-3 h-3 rounded-sm" style:background-color="#ef4444"></span>김</span>
				<span class="inline-flex items-center gap-1"><span class="inline-block w-3 h-3 rounded-sm" style:background-color="#991b1b"></span>거의 상시</span>
			</div>
		{/if}
	{/if}

	<!-- 카운트 시계열 -->
	{#if countOption}
		<div class="resize-y overflow-hidden rounded border bg-card" style="height: 220px; min-height: 160px;">
			<PerfChart option={countOption} height="100%" />
		</div>
	{/if}
</div>
