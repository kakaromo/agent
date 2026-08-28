<script lang="ts">
	/**
	 * 구간 범례 — 색 ↔ 구간 대조표 + 표시 토글.
	 *
	 * 차트 밴드에는 라벨을 그리지 않는다 (데이터에 파묻혀 그래프만 가린다).
	 * 그래서 이 칩이 유일한 이름표이자, 구간을 껐다 켜는 유일한 수단이다.
	 *
	 * Raw Chart / Behavior / Statistics 세 탭이 **같은 컴포넌트와 같은 상태**를 쓴다.
	 * 탭마다 따로 두면 "지금 보고 있는 구간" 이 화면마다 달라진다.
	 */
	import { boundaryLabel, type StepBoundary } from './types.js';
	import PencilIcon from '@lucide/svelte/icons/pencil';

	interface Props {
		boundaries: StepBoundary[];
		hidden: Set<number>;
		color: (i: number) => string;
		onToggle: (i: number) => void;
		onShowAll: () => void;
		onHideAll: () => void;
		/** 전부 숨겼을 때 덧붙일 안내 문구. 탭마다 결과가 달라 문구도 다르다. */
		allHiddenNote?: string;
		/**
		 * 이름 바꾸기. 안 넘기면 편집 UI 자체가 안 뜬다(하위호환 — 저장할 곳이
		 * 없는 화면에서 편집만 되고 새로고침하면 사라지는 게 제일 나쁘다).
		 * 빈 문자열이면 override 해제(= 원래 이름으로).
		 */
		onRename?: (i: number, next: string) => void;
	}

	let {
		boundaries,
		hidden,
		color,
		onToggle,
		onShowAll,
		onHideAll,
		allHiddenNote,
		onRename
	}: Props = $props();

	const allHidden = $derived(boundaries.length > 0 && hidden.size >= boundaries.length);

	// ── 인라인 이름 편집 ──────────────────────────────────────────────
	// 칩을 그 자리에서 input 으로 바꾼다. 다이얼로그를 띄우면 "어느 구간을
	// 고치는 중인지" 가 화면에서 멀어져 색 대조가 끊긴다.
	let editing = $state<number | null>(null);
	let draft = $state('');

	function startEdit(i: number) {
		if (!onRename) return;
		editing = i;
		draft = boundaryLabel(boundaries[i]);
	}
	/** 빈 이름은 저장하지 않는다 — 칩이 빈칸이 되면 어느 구간인지 알 수 없다. */
	const draftEmpty = $derived(draft.trim() === '');

	function commit() {
		if (editing == null) return;
		const i = editing;
		const next = draft.trim();
		// ⚠ 빈 값은 **저장하지 않고 편집 상태를 유지**한다.
		//
		// 예전엔 빈 값을 "원래 이름으로 되돌리기" 로 처리했는데, 두 가지가 겹쳐
		// 위험했다: 이름을 지우다가 실수로 포커스를 잃으면 조용히 되돌아갔고,
		// 원본 label 마저 비어 있으면 칩이 빈칸이 돼 어느 구간인지 알 수 없었다.
		// 되돌리기는 아래 '되돌리기' 버튼으로 **명시적으로만** 한다.
		if (next === '') return;
		editing = null;
		// 자동 요약과 같아지면 override 를 다는 의미가 없다 → 해제해서 원본을 따르게.
		const base = boundaries[i].label || boundaries[i].type;
		onRename?.(i, next === base ? '' : next);
	}
	function cancel() {
		editing = null;
	}
	/** 사용자가 붙인 이름을 지우고 원래 이름으로. 원본이 있을 때만 노출한다. */
	function resetLabel() {
		if (editing == null) return;
		const i = editing;
		editing = null;
		onRename?.(i, '');
	}
	function onKey(e: KeyboardEvent) {
		if (e.key === 'Enter') { e.preventDefault(); commit(); }
		else if (e.key === 'Escape') { e.preventDefault(); cancel(); }
	}
	/** 편집 input 이 뜨면 바로 타이핑할 수 있게. */
	function focus(node: HTMLInputElement) {
		node.focus();
		node.select();
	}
</script>

<div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-[10px]">
	<span class="text-muted-foreground">구간</span>
	{#each boundaries as b, i (`${b.stepIndex}-${b.loopIndex}-${b.repeatIndex}-${i}`)}
		{@const off = hidden.has(i)}
		{#if editing === i}
			<!-- 편집 중 — 칩 자리를 그대로 input 이 차지한다. 색 점을 남겨 어느
			     구간을 고치는 중인지 잃지 않는다. -->
			<span
				class="inline-flex items-center gap-1 rounded border px-1.5 py-0.5"
				class:border-primary={!draftEmpty}
				class:border-destructive={draftEmpty}
			>
				<span class="inline-block size-2 rounded-sm" style="background:{color(i)}"></span>
				<input
					bind:value={draft}
					use:focus
					onkeydown={onKey}
					onblur={commit}
					size={Math.max(8, draft.length + 1)}
					class="bg-transparent outline-none text-[10px] min-w-16"
					placeholder={b.label || b.type}
					aria-label="구간 이름"
					aria-invalid={draftEmpty}
					title={draftEmpty ? '이름을 비울 수 없습니다' : ''}
				/>
				{#if draftEmpty}
					<!-- 왜 저장이 안 되는지 그 자리에서 말한다. 조용히 안 되면 버그로 읽힌다. -->
					<span class="text-destructive">이름 필요</span>
				{/if}
				{#if (b.labelOverride ?? '').trim()}
					<!-- 되돌리기는 **명시적으로만**. 빈 값 저장으로 겸하면 실수로 지워진다. -->
					<button
						onmousedown={(e) => e.preventDefault()}
						onclick={resetLabel}
						class="text-muted-foreground hover:text-foreground underline"
						title="원래 이름({b.label || b.type})으로"
					>
						되돌리기
					</button>
				{/if}
			</span>
		{:else}
			<span
				class="inline-flex items-center rounded border hover:bg-muted"
				class:opacity-40={off}
			>
				<button
					onclick={() => onToggle(i)}
					ondblclick={() => startEdit(i)}
					class="inline-flex items-center gap-1 px-1.5 py-0.5"
					title="클릭하면 {off ? '다시 표시' : '숨김'}{onRename ? ' · 더블클릭하면 이름 편집' : ''}"
				>
					<span
						class="inline-block size-2 rounded-sm"
						style="background:{color(i)};{off ? ' filter:grayscale(1);' : ''}"
					></span>
					{boundaryLabel(b)}
					{#if b.loopIndex > 0}
						<span class="text-muted-foreground">({b.loopIndex})</span>
					{/if}
					{#if !b.success}<span class="text-destructive">실패</span>{/if}
				</button>
				{#if onRename}
					<!-- 연필은 **항상 보인다.**
					     예전엔 hover 때만 나타나게 했는데(칩이 아이콘 밭이 되는 걸 피하려고)
					     기능이 있는 줄 아무도 몰랐다 — 마우스를 올려야 알 수 있는 기능은
					     없는 것과 같다. 대신 흐리게 깔아 두고 hover 에서 또렷해진다. -->
					<button
						onclick={() => startEdit(i)}
						class="pr-1 opacity-40 transition-opacity hover:opacity-100"
						title="이름 편집"
						aria-label="구간 이름 편집"
					>
						<PencilIcon class="size-2.5" />
					</button>
				{/if}
			</span>
		{/if}
	{/each}
	<!-- 항상 보인다 — 뭔가 숨긴 뒤에만 나오면 구간이 많을 때
		 "하나만 보려면 나머지를 다 눌러야 하나" 가 된다. -->
	<button
		onclick={onShowAll}
		disabled={hidden.size === 0}
		class="rounded border px-1.5 py-0.5 text-muted-foreground hover:bg-muted disabled:opacity-40"
	>
		전체 선택
	</button>
	<button
		onclick={onHideAll}
		disabled={allHidden}
		class="rounded border px-1.5 py-0.5 text-muted-foreground hover:bg-muted disabled:opacity-40"
	>
		전체 해제
	</button>
	{#if allHidden && allHiddenNote}
		<span class="text-muted-foreground">· {allHiddenNote}</span>
	{/if}
</div>
