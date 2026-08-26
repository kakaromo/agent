<script lang="ts">
	/**
	 * BehaviorTimeline — 스텝 레인 + IO 산점도를 **같은 시간축**으로 겹쳐 그린다.
	 *
	 * ECharts 를 안 쓰고 canvas 로 직접 그리는 이유: 레인(DOM)과 산점도(차트)를 따로
	 * 두면 축을 손으로 맞춰야 하고, 한쪽만 zoom 되면 조용히 어긋난다. 하나의 x 매핑
	 * (pxAt)을 공유해 그리면 어긋날 수가 없다.
	 *
	 * Charts 탭이 markArea 를 쓰는 것과 역할이 다르다 — 거기는 "기존 차트에 구간을
	 * 겹쳐 보기", 여기는 "구간이 주인공인 화면".
	 */
	import type { StepBoundary, TraceEvent } from '$lib/api/agent.js';
	import { captionMuted } from '$lib/styles/common.js';

	interface Props {
		boundaries: StepBoundary[];
		events: TraceEvent[];
		/** 경계 불확실 폭(± 초). 0 이면 해칭을 안 그린다. */
		edgeSec?: number;
		/** 선택된 구간 key 집합. 비어 있으면 전체를 본다. */
		selected?: Set<string>;
		keyOf: (b: StepBoundary, i: number) => string;
		colorOf: (i: number) => string;
		labelOf: (b: StepBoundary) => string;
		isIdle: (type: string) => boolean;
		/** cmd → 'read' | 'write' | 그 외. 차트와 같은 판정을 써야 색이 일치한다. */
		groupOf: (cmd: string) => string;
		onToggle?: (key: string) => void;
	}

	let {
		boundaries, events, edgeSec = 0, selected = new Set(),
		keyOf, colorOf, labelOf, isIdle, groupOf, onToggle
	}: Props = $props();

	const LABEL_W = 124;   // 레인 라벨 열 — 라벨이 서술형이라 넓게
	const PLOT_H = 200;    // 산점도 높이

	// 시간축 — 레인과 산점도가 **같은 값**을 쓴다.
	const span = $derived.by(() => {
		let t0 = Infinity, t1 = -Infinity;
		for (const b of boundaries) {
			if (b.startedMono < t0) t0 = b.startedMono;
			if (b.finishedMono > t1) t1 = b.finishedMono;
		}
		if (!isFinite(t0) || !(t1 > t0)) return { t0: 0, t1: 1 };
		return { t0, t1 };
	});

	/** 시각 → 0~100%. 레인(DOM)과 산점도(canvas)가 공유한다. */
	function pct(t: number): number {
		return ((t - span.t0) / (span.t1 - span.t0)) * 100;
	}

	// 눈금 — 전체 길이에 따라 간격을 고른다.
	const ticks = $derived.by(() => {
		const dur = span.t1 - span.t0;
		const steps = [0.5, 1, 2, 5, 10, 30, 60, 300];
		const step = steps.find(s => dur / s <= 12) ?? Math.ceil(dur / 12);
		const out: { t: number; label: string }[] = [];
		for (let k = 0; k * step <= dur; k++) {
			const rel = k * step;
			out.push({ t: span.t0 + rel, label: rel >= 60 ? `${(rel / 60).toFixed(0)}m` : `${rel}s` });
		}
		return out;
	});

	const dimming = $derived(selected.size > 0);
	function isOn(key: string): boolean {
		return !dimming || selected.has(key);
	}

	// ── 산점도 (canvas) ──
	let canvasEl = $state<HTMLCanvasElement | null>(null);

	// LBA 는 범위가 기기마다 달라 정규화해 쓴다.
	const lbaRange = $derived.by(() => {
		let lo = Infinity, hi = -Infinity;
		for (const e of events) {
			if (e.lba < lo) lo = e.lba;
			if (e.lba > hi) hi = e.lba;
		}
		if (!isFinite(lo) || hi <= lo) return { lo: 0, hi: 1 };
		return { lo, hi };
	});

	/** 이 시각이 어느 구간인가 — 점을 흐리게 할지 판단한다. */
	function keyAt(t: number): string | null {
		for (let i = 0; i < boundaries.length; i++) {
			const b = boundaries[i];
			if (t >= b.startedMono && t <= b.finishedMono) return keyOf(b, i);
		}
		return null;
	}

	function draw() {
		const cv = canvasEl;
		if (!cv) return;
		const dpr = window.devicePixelRatio || 1;
		const rect = cv.getBoundingClientRect();
		const w = Math.max(1, rect.width), h = Math.max(1, rect.height);
		cv.width = Math.round(w * dpr);
		cv.height = Math.round(h * dpr);
		const ctx = cv.getContext('2d');
		if (!ctx) return;
		ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
		ctx.clearRect(0, 0, w, h);

		const xAt = (t: number) => (pct(t) / 100) * w;

		// 눈금선 — 레인의 눈금과 같은 위치라 시선이 이어진다.
		ctx.strokeStyle = 'rgba(128,128,128,0.18)';
		ctx.lineWidth = 1;
		for (const tk of ticks) {
			const x = Math.round(xAt(tk.t)) + 0.5;
			ctx.beginPath(); ctx.moveTo(x, 0); ctx.lineTo(x, h); ctx.stroke();
		}

		// 경계 불확실 해칭 — 강조가 아니라 "모름" 으로 읽혀야 한다.
		if (edgeSec > 0) {
			for (let i = 0; i < boundaries.length; i++) {
				const b = boundaries[i];
				if (!isOn(keyOf(b, i))) continue;
				for (const edge of [b.startedMono, b.finishedMono]) {
					const x0 = xAt(edge - edgeSec);
					const x1 = xAt(edge + edgeSec);
					const bw = Math.max(3, x1 - x0);   // 실측 ±수십ms 는 sub-pixel — 최소 폭 보장
					ctx.save();
					ctx.beginPath(); ctx.rect(x0, 0, bw, h); ctx.clip();
					ctx.strokeStyle = 'rgba(242,153,0,0.45)';
					ctx.lineWidth = 1;
					for (let d = -h; d < bw + h; d += 6) {
						ctx.beginPath(); ctx.moveTo(x0 + d, 0); ctx.lineTo(x0 + d + h, h); ctx.stroke();
					}
					ctx.restore();
				}
			}
		}

		// 점 — write/read 를 색으로 가른다. 판정은 차트와 같은 함수를 쓴다.
		const { lo, hi } = lbaRange;
		for (const e of events) {
			const x = xAt(e.time);
			if (x < -2 || x > w + 2) continue;
			const y = (1 - (e.lba - lo) / (hi - lo)) * (h - 8) + 4;
			const g = groupOf(e.cmd);
			const on = !dimming || isOn(keyAt(e.time) ?? '');
			ctx.globalAlpha = on ? 0.6 : 0.06;
			ctx.fillStyle = g === 'write' ? '#d93025' : g === 'read' ? '#1e8e3e' : '#9aa0a6';
			ctx.fillRect(x, y, 1.7, 1.7);
		}
		ctx.globalAlpha = 1;
	}

	// 데이터·크기가 바뀌면 다시 그린다.
	$effect(() => {
		// 의존성 등록 (본문에서 안 읽으면 반응하지 않는다)
		void events; void boundaries; void edgeSec; void selected; void span;
		draw();
	});

	$effect(() => {
		if (!canvasEl) return;
		const ro = new ResizeObserver(() => draw());
		ro.observe(canvasEl);
		return () => ro.disconnect();
	});
</script>

<div class="rounded border p-2">
	<div class="flex items-center justify-between mb-1">
		<span class="text-[10px] font-semibold">타임라인</span>
		<span class="{captionMuted} text-[9px]">
			{#if dimming}
				{selected.size}개 구간 선택 — 나머지는 흐리게
			{:else}
				레인을 클릭하면 그 구간만 강조됩니다 · 여러 개 선택 가능
			{/if}
		</span>
	</div>

	<!-- 상단 눈금 -->
	<div class="relative h-[14px]" style="margin-left:{LABEL_W}px">
		{#each ticks as tk}
			<span class="absolute text-[9px] font-mono text-muted-foreground -translate-x-1/2"
				style="left:{pct(tk.t)}%">{tk.label}</span>
		{/each}
	</div>

	<!-- 레인 -->
	{#each boundaries as b, i (keyOf(b, i))}
		{@const key = keyOf(b, i)}
		{@const on = isOn(key)}
		{@const left = pct(b.startedMono)}
		{@const width = Math.max(pct(b.finishedMono) - left, 0.4)}
		{@const idle = isIdle(b.type)}
		<div class="grid items-center min-h-[26px]" style="grid-template-columns:{LABEL_W}px 1fr">
			<button
				onclick={() => onToggle?.(key)}
				class="pr-2 text-right text-[9px] font-mono truncate hover:text-foreground"
				class:text-muted-foreground={on}
				class:opacity-40={!on}
				title="{labelOf(b)} — 클릭하면 이 구간만 강조">
				{labelOf(b)}
			</button>
			<div class="relative h-[22px] rounded bg-muted/40">
				<div class="absolute top-[3px] bottom-[3px] rounded-sm flex items-center px-1.5 overflow-hidden whitespace-nowrap text-[9px] cursor-pointer"
					class:opacity-25={!on}
					style="left:{left}%; width:{width}%;
						{idle
							? 'border:1px dashed currentColor; color:var(--muted-foreground);'
							: `background:${colorOf(i)}; color:#fff;`}"
					onclick={() => onToggle?.(key)}
					onkeydown={(e) => { if (e.key === 'Enter') onToggle?.(key); }}
					role="button"
					tabindex="0"
					title="{labelOf(b)} — {b.startedMono.toFixed(2)}s → {b.finishedMono.toFixed(2)}s">
					<!-- 라벨은 왼쪽 열에 있다. 바 안에는 타입과 길이만 —
					     짧은 구간(0.1s)에서는 이마저도 잘리므로 title 로도 준다. -->
					<span class="truncate">{b.type}</span>
					<span class="ml-auto pl-1.5 font-mono opacity-85">
						{(b.finishedMono - b.startedMono).toFixed(2)}s
					</span>
				</div>
			</div>
		</div>
	{/each}

	<!-- IO 산점도 — 위 레인과 **같은 x 매핑**을 쓴다 -->
	<div class="grid items-stretch mt-1.5" style="grid-template-columns:{LABEL_W}px 1fr">
		<div class="pr-2 flex flex-col justify-between py-1 text-right text-[9px] font-mono text-muted-foreground">
			<span>LBA 高</span><span>低</span>
		</div>
		<div class="rounded border" style="height:{PLOT_H}px">
			<canvas bind:this={canvasEl} class="block w-full h-full"></canvas>
		</div>
	</div>

	<!-- 하단 눈금 -->
	<div class="relative h-[14px] mt-0.5" style="margin-left:{LABEL_W}px">
		{#each ticks as tk}
			<span class="absolute text-[9px] font-mono text-muted-foreground -translate-x-1/2"
				style="left:{pct(tk.t)}%">{tk.label}</span>
		{/each}
	</div>

	<!-- 범례 -->
	<div class="flex flex-wrap items-center gap-3 mt-1 text-[9px] {captionMuted}">
		<span class="inline-flex items-center gap-1">
			<span class="inline-block size-2 rounded-full" style="background:#d93025"></span>write
		</span>
		<span class="inline-flex items-center gap-1">
			<span class="inline-block size-2 rounded-full" style="background:#1e8e3e"></span>read
		</span>
		{#if edgeSec > 0}
			<span class="inline-flex items-center gap-1">
				<span class="inline-block w-5 h-2 rounded-sm"
					style="background-image:repeating-linear-gradient(45deg,rgba(242,153,0,0.45) 0 3px,transparent 3px 6px)"></span>
				경계 모호 (±{(edgeSec * 1000).toFixed(0)}ms) — 보이도록 넓힌 폭입니다
			</span>
		{/if}
	</div>
</div>
