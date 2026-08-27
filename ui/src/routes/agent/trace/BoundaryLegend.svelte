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
	import type { StepBoundary } from './types.js';

	interface Props {
		boundaries: StepBoundary[];
		hidden: Set<number>;
		color: (i: number) => string;
		onToggle: (i: number) => void;
		onShowAll: () => void;
		onHideAll: () => void;
		/** 전부 숨겼을 때 덧붙일 안내 문구. 탭마다 결과가 달라 문구도 다르다. */
		allHiddenNote?: string;
	}

	let {
		boundaries,
		hidden,
		color,
		onToggle,
		onShowAll,
		onHideAll,
		allHiddenNote
	}: Props = $props();

	const allHidden = $derived(boundaries.length > 0 && hidden.size >= boundaries.length);
</script>

<div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-[10px]">
	<span class="text-muted-foreground">구간</span>
	{#each boundaries as b, i (`${b.stepIndex}-${b.loopIndex}-${b.repeatIndex}-${i}`)}
		{@const off = hidden.has(i)}
		<button
			onclick={() => onToggle(i)}
			class="inline-flex items-center gap-1 rounded border px-1.5 py-0.5 hover:bg-muted"
			class:opacity-40={off}
			title="클릭하면 {off ? '다시 표시' : '숨김'}"
		>
			<span
				class="inline-block size-2 rounded-sm"
				style="background:{color(i)};{off ? ' filter:grayscale(1);' : ''}"
			></span>
			{b.label || b.type}
			{#if b.loopIndex > 0}
				<span class="text-muted-foreground">({b.loopIndex})</span>
			{/if}
			{#if !b.success}<span class="text-destructive">실패</span>{/if}
		</button>
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
