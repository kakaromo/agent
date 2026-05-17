<script lang="ts">
	import type { MappedField, MappedInstance } from '$lib/api/binMapper.js';

	let {
		instances,
		highlightedOffset = $bindable(-1)
	}: {
		instances: MappedInstance[];
		highlightedOffset: number;
	} = $props();

	let expandedPaths = $state<Set<string>>(new Set());

	function toggleExpand(path: string) {
		if (expandedPaths.has(path)) {
			expandedPaths.delete(path);
		} else {
			expandedPaths.add(path);
		}
		expandedPaths = new Set(expandedPaths);
	}

	// "ENUM_NAME (숫자)" 패턴 감지
	const ENUM_PATTERN = /^([A-Z][A-Z0-9_]+)\s+\((.+)\)$/;

	function isEnumValue(val: unknown): { label: string; raw: string } | null {
		if (typeof val !== 'string') return null;
		const m = val.match(ENUM_PATTERN);
		return m ? { label: m[1], raw: m[2] } : null;
	}

	function formatValue(val: unknown): string {
		if (val === null || val === undefined) return '-';
		if (typeof val === 'number') return val.toString();
		if (typeof val === 'boolean') return val ? 'true' : 'false';
		if (typeof val === 'string') return val;
		return JSON.stringify(val);
	}

	function formatOffset(offset: number): string {
		return `0x${offset.toString(16).toUpperCase().padStart(4, '0')} (${offset})`;
	}

	// ── Column resize ──
	const columns = [
		{ key: 'field', label: 'Field', width: 180 },
		{ key: 'type', label: 'Type', width: 120 },
		{ key: 'offset', label: 'Offset', width: 130 },
		{ key: 'size', label: 'Size', width: 50 },
		{ key: 'hex', label: 'Hex', width: 200 },
		{ key: 'value', label: 'Value', width: 220 }
	];

	let colWidths = $state(columns.map((c) => c.width));

	let resizing: { colIdx: number; startX: number; startW: number } | null = $state(null);

	function onResizeStart(e: MouseEvent, colIdx: number) {
		e.preventDefault();
		resizing = { colIdx, startX: e.clientX, startW: colWidths[colIdx] };

		const onMove = (ev: MouseEvent) => {
			if (!resizing) return;
			const delta = ev.clientX - resizing.startX;
			const newW = Math.max(40, resizing.startW + delta);
			colWidths[colIdx] = newW;
		};

		const onUp = () => {
			resizing = null;
			window.removeEventListener('mousemove', onMove);
			window.removeEventListener('mouseup', onUp);
		};

		window.addEventListener('mousemove', onMove);
		window.addEventListener('mouseup', onUp);
	}

	let totalWidth = $derived(colWidths.reduce((a, b) => a + b, 0));
</script>

<div class="overflow-x-auto" class:select-none={resizing}>
	<table class="table table-xs table-pin-rows" style="width:{totalWidth}px;table-layout:fixed">
		<colgroup>
			{#each colWidths as w}
				<col style="width:{w}px" />
			{/each}
		</colgroup>
		<thead>
			<tr class="bg-base-200 text-xs">
				{#each columns as col, i}
					<th class="relative overflow-hidden" style="width:{colWidths[i]}px">
						<span class="truncate">{col.label}</span>
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<span
							class="absolute right-0 top-0 h-full w-1.5 cursor-col-resize hover:bg-primary/30 active:bg-primary/50 transition-colors"
							onmousedown={(e) => onResizeStart(e, i)}
						></span>
					</th>
				{/each}
			</tr>
		</thead>
		<tbody>
			{#each instances as inst}
				{#if instances.length > 1}
					<tr class="bg-base-300">
						<td colspan="6" class="font-bold text-xs">Instance [{inst.index}] @ 0x{inst.offset.toString(16).toUpperCase()}</td>
					</tr>
				{/if}
				{#each inst.fields as field}
					{@render fieldRow(field, 0, `${inst.index}.${field.fieldName}`)}
				{/each}
			{/each}
		</tbody>
	</table>
</div>

{#snippet fieldRow(field: MappedField, depth: number, path: string)}
	<tr
		class="hover:bg-primary/5 cursor-pointer text-xs {highlightedOffset === field.offset ? 'bg-primary/10' : ''}"
		onmouseenter={() => (highlightedOffset = field.offset)}
		onmouseleave={() => (highlightedOffset = -1)}
	>
		<td class="truncate" style="padding-left: {8 + depth * 16}px">
			{#if field.children && field.children.length > 0}
				<button class="mr-1" onclick={() => toggleExpand(path)}>
					{expandedPaths.has(path) ? '▼' : '▶'}
				</button>
			{/if}
			<span class="font-mono">{field.fieldName}</span>
		</td>
		<td class="font-mono text-muted-foreground truncate">{field.typeName}</td>
		<td class="font-mono truncate">{formatOffset(field.offset)}</td>
		<td class="font-mono truncate">{field.size}</td>
		<td class="font-mono text-xs truncate" title={field.hexBytes}>{field.hexBytes}</td>
		<td class="font-mono truncate" title={formatValue(field.parsedValue)}>
			{#if field.children}
				{`{${field.children.length} fields}`}
			{:else}
				{@const enumVal = isEnumValue(field.parsedValue)}
				{#if enumVal}
					<span class="inline-flex items-center gap-1">
						<span class="badge badge-sm badge-primary badge-outline font-semibold">{enumVal.label}</span>
						<span class="text-muted-foreground">{enumVal.raw}</span>
					</span>
				{:else}
					{formatValue(field.parsedValue)}
				{/if}
			{/if}
		</td>
	</tr>
	{#if field.children && expandedPaths.has(path)}
		{#each field.children as child}
			{@render fieldRow(child, depth + 1, `${path}.${child.fieldName}`)}
		{/each}
	{/if}
{/snippet}
