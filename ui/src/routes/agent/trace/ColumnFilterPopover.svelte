<script lang="ts">
	/**
	 * Raw Data 컬럼 헤더의 필터 팝오버.
	 *
	 * 테이블이 **서버 페이징**(1000행씩)이라 필터도 서버로 나가야 한다. 로드된 행만
	 * 거르면 "필터를 걸었는데 뒤쪽 페이지의 매칭 행이 안 나오는" 조용한 오답이 된다.
	 * 그래서 확정(적용) 시 상위로 올려 재조회하게 하고, 입력 중에는 아무것도 하지 않는다.
	 *
	 * 컬럼 타입(숫자/문자)은 클라이언트가 모른다 — 서버가 parquet 스키마에서 읽는다.
	 * 여기서는 "어떤 연산자를 보여줄지" 정도만 힌트로 받는다.
	 */
	import FilterIcon from '@lucide/svelte/icons/filter';
	import XIcon from '@lucide/svelte/icons/x';
	import type { ColumnFilter, ColumnFilterOp } from './types.js';

	interface Props {
		column: string;
		label: string;
		/** 숫자 컬럼이면 RANGE 를 기본 제공. 표시용 힌트일 뿐 서버 판정과 무관. */
		numericHint?: boolean;
		/** 경로/파일명처럼 값이 긴 컬럼 — 기본 연산자를 '포함' 으로 연다. */
		longTextHint?: boolean;
		/** 현재 적용된 필터 (없으면 null). */
		current: ColumnFilter | null;
		/** 이 컬럼에서 자주 쓰는 값 후보 (현재 페이지 기준 — 전체가 아님을 UI 에 명시). */
		suggestions?: string[];
		onApply: (next: ColumnFilter | null) => void;
	}

	let {
		column,
		label,
		numericHint = false,
		longTextHint = false,
		current,
		suggestions = [],
		onApply
	}: Props = $props();

	let open = $state(false);
	/**
	 * 팝오버를 연 시각. 그 클릭이 window 로 올라와 곧바로 다시 닫는 걸 막는다.
	 * 이벤트 객체 비교는 Svelte 핸들러와 window 핸들러가 서로 다른 래퍼를 받는 경우가
	 * 있어 신뢰할 수 없다 — 같은 tick 인지로 판단한다.
	 */
	let openedAt = 0;
	let triggerEl = $state<HTMLElement | null>(null);
	let panelTop = $state(0);
	let panelLeft = $state(0);
	let op = $state<ColumnFilterOp>('IN');

	/** 패널을 body 로 이동 (버튼 중첩 회피). 제거 시 원위치 정리는 노드 삭제로 충분. */
	function portal(node: HTMLElement) {
		document.body.appendChild(node);
		return () => node.remove();
	}

	/** 트리거 위치 기준으로 패널 좌표 계산. 화면 오른쪽/아래로 넘치면 안쪽으로 당긴다. */
	function positionPanel() {
		const r = triggerEl?.getBoundingClientRect();
		if (!r) return;
		const W = 240; // w-60
		panelTop = Math.min(r.bottom + 4, window.innerHeight - 200);
		panelLeft = Math.max(4, Math.min(r.left, window.innerWidth - W - 8));
	}
	let text = $state('');
	let rangeMin = $state('');
	let rangeMax = $state('');

	const active = $derived(!!current);

	// 팝오버를 열 때마다 현재 적용값으로 초기화 — 닫았다 열면 이전 입력이 남아
	// "적용 안 한 값이 적용된 것처럼" 보이는 혼란을 막는다.
	function openPanel() {
		// 기본 연산자 — 숫자는 범위, 경로/파일명처럼 긴 값은 '포함'.
		// name 은 `/data/app/.../base.apk` 같은 전체 경로라 '일치' 를 기본으로 두면
		// 사용자가 기억하는 일부만 넣었을 때 0 행이 나와 "필터가 안 먹는다" 로 읽힌다.
		op = current?.op ?? (numericHint ? 'RANGE' : longTextHint ? 'CONTAINS' : 'IN');
		if (current?.op === 'RANGE') {
			rangeMin = current.values[0] ?? '';
			rangeMax = current.values[1] ?? '';
			text = '';
		} else {
			text = (current?.values ?? []).map(quoteValue).join(', ');
			rangeMin = '';
			rangeMax = '';
		}
		positionPanel();
		open = true;
	}

	function apply() {
		if (op === 'RANGE') {
			const lo = rangeMin.trim();
			const hi = rangeMax.trim();
			if (!lo && !hi) return clear();
			onApply({ column, op, values: [lo, hi] });
		} else if (op === 'CONTAINS') {
			const v = text.trim();
			if (!v) return clear();
			onApply({ column, op, values: [v] });
		} else {
			const vals = splitValues(text);
			if (vals.length === 0) return clear();
			onApply({ column, op, values: vals });
		}
		open = false;
	}

	/**
	 * 입력 문자열 → 값 목록.
	 *
	 * 기본은 쉼표 구분이지만, **값 자체에 쉼표가 들어가는** 컬럼이 있다
	 * (`name` 의 파일 경로 `/data/app/com.foo,bar/base.apk`, `(flush:배리어)` 류 라벨).
	 * 무조건 쪼개면 없는 파일명 두 개를 찾게 되어 "필터가 안 먹는다" 가 된다.
	 *
	 * 그래서 따옴표로 감싼 값은 통째로 하나로 본다 — 쉼표가 든 값을 넣을 방법을 준다.
	 * 따옴표가 없으면 기존대로 쉼표 분리 (여러 값 넣기가 훨씬 흔한 용법이라 기본 유지).
	 */
	function splitValues(input: string): string[] {
		const out: string[] = [];
		let buf = '';
		let quote: '"' | "'" | null = null;
		for (const ch of input) {
			if (quote) {
				if (ch === quote) quote = null;
				else buf += ch;
			} else if (ch === '"' || ch === "'") {
				quote = ch;
			} else if (ch === ',') {
				out.push(buf);
				buf = '';
			} else {
				buf += ch;
			}
		}
		out.push(buf);
		return out.map((s) => s.trim()).filter((s) => s.length > 0);
	}

	function clear() {
		onApply(null);
		text = '';
		rangeMin = '';
		rangeMax = '';
		open = false;
	}

	function onKey(e: KeyboardEvent) {
		if (e.key === 'Enter') {
			e.preventDefault();
			apply();
		} else if (e.key === 'Escape') {
			e.preventDefault();
			open = false;
		}
	}

	/** 값에 쉼표/따옴표가 있으면 따옴표로 감싼다 — 다시 파싱할 때 쪼개지지 않게. */
	function quoteValue(v: string): string {
		if (!v.includes(',') && !v.includes('"') && !v.includes("'")) return v;
		return `"${v.replace(/"/g, '')}"`;
	}

	function pick(v: string) {
		// 후보 클릭 = 목록에 추가 (교체가 아님). 여러 값을 빠르게 모으는 게 목적.
		const cur = splitValues(text);
		if (!cur.includes(v)) cur.push(v);
		text = cur.map(quoteValue).join(', ');
	}
</script>

<!-- 바깥 클릭으로 닫기.
	 열게 만든 그 클릭이 window 까지 올라와 곧바로 다시 닫는 문제가 있어
	 (열림 → 같은 이벤트로 닫힘 → 사용자에겐 "안 열림") 이벤트 자체를 비교해 무시한다.
	 stopPropagation 은 DataTable 의 정렬 핸들러까지 막아버려 쓰지 않는다. -->
<svelte:window
	onclick={(e) => {
		if (!open || performance.now() - openedAt < 150) return;
		const t = e.target as HTMLElement;
		// 패널은 portal 로 body 에 있어 data-colfilter 하위가 아니다 — 별도 표식으로 확인.
		if (!t.closest(`[data-colfilter="${column}"]`) && !t.closest(`[data-colfilter-panel="${column}"]`)) {
			open = false;
		}
	}}
	onresize={() => open && positionPanel()}
/>

<span class="relative inline-flex" data-colfilter={column}>
	<!-- DataTable 이 헤더를 정렬 <button> 안에 넣기 때문에 여기서 <button> 을 쓰면
		 버튼 중첩(HTML 위반)이 되어 브라우저가 DOM 을 재배치해 클릭이 통째로 깨진다.
		 span + role=button 으로 두고, 정렬로 새어나가지 않게 stopPropagation 한다. -->
	<span
		bind:this={triggerEl}
		role="button"
		tabindex="0"
		class="ml-0.5 inline-flex items-center rounded p-0.5 hover:bg-muted transition-colors cursor-pointer
			{active ? 'text-blue-600' : 'text-muted-foreground/50'}"
		title={active ? `${label} 필터 적용 중 — 클릭해서 수정` : `${label} 필터`}
		onclick={(e) => {
			e.stopPropagation();
			e.preventDefault();
			if (open) {
				open = false;
			} else {
				openedAt = performance.now();
				openPanel();
			}
		}}
		onkeydown={(e) => {
			if (e.key === 'Enter' || e.key === ' ') {
				e.stopPropagation();
				e.preventDefault();
				if (open) {
					open = false;
				} else {
					openedAt = performance.now();
					openPanel();
				}
			}
		}}
	>
		<FilterIcon class="size-2.5" />
		{#if active}
			<span class="absolute -top-0.5 -right-0.5 size-1 rounded-full bg-blue-500"></span>
		{/if}
	</span>

	{#if open}
		<!-- 패널은 body 로 옮긴다(portal). 헤더가 정렬 <button> 안이라 그 안에 두면
			 버튼 중첩이 되어 내부 버튼들의 클릭이 브라우저 DOM 재배치로 깨진다.
			 위치는 트리거 좌표 기준으로 fixed 배치. -->
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			{@attach portal}
			data-colfilter-panel={column}
			style="position: fixed; top: {panelTop}px; left: {panelLeft}px;"
			class="z-50 w-60 rounded-md border bg-popover p-2 shadow-md text-[10px] font-normal"
			onclick={(e) => e.stopPropagation()}
			onkeydown={onKey}
		>
			<div class="flex items-center justify-between mb-1.5">
				<span class="font-medium">{label}</span>
				{#if active}
					<button class="text-muted-foreground hover:text-foreground" onclick={clear} title="필터 해제">
						<XIcon class="size-3" />
					</button>
				{/if}
			</div>

			<div class="flex gap-1 mb-1.5">
				{#each (numericHint ? ['RANGE', 'IN', 'NOT_IN', 'CONTAINS'] : ['IN', 'NOT_IN', 'CONTAINS']) as o (o)}
					<button
						class="px-1.5 py-0.5 rounded border transition-colors {op === o
							? 'bg-primary text-primary-foreground border-primary'
							: 'hover:bg-muted'}"
						onclick={() => (op = o as ColumnFilterOp)}
					>
						{o === 'IN' ? '일치' : o === 'NOT_IN' ? '제외' : o === 'CONTAINS' ? '포함' : '범위'}
					</button>
				{/each}
			</div>

			{#if op === 'RANGE'}
				<div class="flex items-center gap-1">
					<input
						bind:value={rangeMin}
						placeholder="min"
						class="w-full border rounded px-1 py-0.5 bg-background font-mono"
					/>
					<span class="text-muted-foreground">~</span>
					<input
						bind:value={rangeMax}
						placeholder="max"
						class="w-full border rounded px-1 py-0.5 bg-background font-mono"
					/>
				</div>
				<p class="text-muted-foreground mt-1">한쪽만 입력하면 그 방향은 무제한</p>
			{:else}
				<!-- svelte-ignore a11y_autofocus -->
				<input
					bind:value={text}
					autofocus
					placeholder={op === 'CONTAINS' ? '부분 일치 문자열' : '값 (쉼표로 여러 개, "..." 로 묶기)'}
					class="w-full border rounded px-1 py-0.5 bg-background font-mono"
				/>
				{#if op !== 'CONTAINS' && suggestions.length > 0}
					<div class="mt-1 max-h-24 overflow-auto flex flex-wrap gap-0.5">
						{#each suggestions as s (s)}
							<button
								class="px-1 py-0.5 rounded border hover:bg-muted font-mono truncate max-w-full"
								title={s}
								onclick={() => pick(s)}
							>
								{s}
							</button>
						{/each}
					</div>
					<p class="text-muted-foreground mt-1">
						※ 후보는 <b>불러온 행</b> 기준 — 전체 목록이 아닙니다. 직접 입력해도 됩니다.
					</p>
				{/if}
			{/if}

			<div class="flex justify-end gap-1 mt-2">
				<button class="px-2 py-0.5 rounded border hover:bg-muted" onclick={() => (open = false)}>
					취소
				</button>
				<button
					class="px-2 py-0.5 rounded border bg-primary text-primary-foreground border-primary hover:opacity-90"
					onclick={apply}
				>
					적용
				</button>
			</div>
		</div>
	{/if}
</span>
