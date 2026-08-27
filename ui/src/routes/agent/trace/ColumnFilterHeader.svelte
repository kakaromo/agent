<script lang="ts">
	/**
	 * Raw Data 헤더 셀 — 컬럼명 + 필터 버튼.
	 *
	 * DataTable 의 헤더는 정렬 버튼 안에서 FlexRender 로 렌더되므로, 필터 버튼 클릭이
	 * 정렬로 새어나가지 않게 popover 쪽에서 stopPropagation 한다.
	 */
	import ColumnFilterPopover from './ColumnFilterPopover.svelte';
	import type { ColumnFilter } from './types.js';

	interface Props {
		label: string;
		column: string;
		numericHint?: boolean;
		longTextHint?: boolean;
		current: ColumnFilter | null;
		suggestions?: string[];
		onApply: (next: ColumnFilter | null) => void;
	}

	let {
		label,
		column,
		numericHint = false,
		longTextHint = false,
		current,
		suggestions = [],
		onApply
	}: Props = $props();
</script>

<span class="inline-flex items-center">
	{label}
	<ColumnFilterPopover
		{column}
		{label}
		{numericHint}
		{longTextHint}
		{current}
		{suggestions}
		{onApply}
	/>
</span>
