<script lang="ts">
	import type { MappedField, MappedInstance } from '$lib/api/binMapper.js';

	let {
		rawBytes,
		instances,
		highlightedOffset = $bindable(-1)
	}: {
		rawBytes: number[];
		instances: MappedInstance[];
		highlightedOffset: number;
	} = $props();

	const FIELD_COLORS = [
		{ bg: '59,130,246', hi: '37,99,235' },
		{ bg: '34,197,94', hi: '22,163,74' },
		{ bg: '234,179,8', hi: '202,138,4' },
		{ bg: '236,72,153', hi: '219,39,119' },
		{ bg: '168,85,247', hi: '147,51,234' },
		{ bg: '249,115,22', hi: '234,88,12' },
		{ bg: '20,184,166', hi: '13,148,136' },
		{ bg: '239,68,68', hi: '220,38,38' }
	];

	const BORDER_COLORS = [
		'border-blue-400', 'border-green-400', 'border-yellow-400', 'border-pink-400',
		'border-purple-400', 'border-orange-400', 'border-teal-400', 'border-red-400'
	];

	const ROW_HEIGHT = 22;
	const BYTES_PER_ROW = 16;
	const OVERSCAN = 10;

	interface FieldRange {
		fieldName: string;
		typeName: string;
		offset: number;
		size: number;
		hexBytes: string;
		value: string;
		colorIdx: number;
	}

	let selectedOffset = $state(-1);
	let cursorOffset = $state(-1);
	let hexOffsetFormat = $state<'hex' | 'dec'>('hex');
	let hexGridEl: HTMLDivElement | null = $state(null);
	let hexScrollEl: HTMLDivElement | null = $state(null);
	let scrollTop = $state(0);
	let containerHeight = $state(400);

	// --- Resizable panel ---
	let panelWidth = $state(320);
	let totalHeight_ = $state(500);
	let resizingX = $state(false);
	let resizingY = $state(false);
	let resizeStartX = 0;
	let resizeStartY = 0;
	let resizeStartW = 0;
	let resizeStartH = 0;

	function startResizeX(e: MouseEvent) {
		resizingX = true;
		resizeStartX = e.clientX;
		resizeStartW = panelWidth;
		e.preventDefault();
	}

	function startResizeY(e: MouseEvent) {
		resizingY = true;
		resizeStartY = e.clientY;
		resizeStartH = totalHeight_;
		e.preventDefault();
	}

	function handleMouseMove(e: MouseEvent) {
		if (resizingX) {
			panelWidth = Math.max(200, Math.min(800, resizeStartW - (e.clientX - resizeStartX)));
		}
		if (resizingY) {
			totalHeight_ = Math.max(300, Math.min(1200, resizeStartH + (e.clientY - resizeStartY)));
		}
	}

	function handleMouseUp() {
		resizingX = false;
		resizingY = false;
	}

	// --- Virtual scroll derived ---
	let totalRows = $derived(Math.ceil(rawBytes.length / BYTES_PER_ROW));
	let totalHeight = $derived(totalRows * ROW_HEIGHT);
	let startRow = $derived(Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN));
	let endRow = $derived(Math.min(totalRows, Math.ceil((scrollTop + containerHeight) / ROW_HEIGHT) + OVERSCAN));
	let visibleRowIndices = $derived(Array.from({ length: endRow - startRow }, (_, i) => startRow + i));
	let offsetY = $derived(startRow * ROW_HEIGHT);

	function handleScroll() {
		if (hexScrollEl) {
			scrollTop = hexScrollEl.scrollTop;
			containerHeight = hexScrollEl.clientHeight;
		}
	}

	// Initial measure
	$effect(() => {
		if (hexScrollEl) {
			containerHeight = hexScrollEl.clientHeight;
		}
	});

	// --- Field data ---
	let fieldRanges = $derived.by(() => {
		const ranges: FieldRange[] = [];
		let colorIdx = 0;
		for (const inst of instances) {
			for (const field of flattenFields(inst.fields)) {
				if (field.size > 0) {
					ranges.push({
						fieldName: field.fieldName,
						typeName: field.typeName,
						offset: field.offset,
						size: field.size,
						hexBytes: field.hexBytes,
						value: formatValue(field.parsedValue),
						colorIdx: colorIdx % FIELD_COLORS.length
					});
					colorIdx++;
				}
			}
		}
		return ranges;
	});

	let byteFieldIdx = $derived.by(() => {
		const arr = new Int16Array(rawBytes.length).fill(-1);
		for (let fi = 0; fi < fieldRanges.length; fi++) {
			const fr = fieldRanges[fi];
			const end = Math.min(fr.offset + fr.size, rawBytes.length);
			for (let i = fr.offset; i < end; i++) arr[i] = fi;
		}
		return arr;
	});

	function getFieldAt(offset: number): FieldRange | null {
		if (offset < 0 || offset >= byteFieldIdx.length) return null;
		const idx = byteFieldIdx[offset];
		return idx >= 0 ? fieldRanges[idx] : null;
	}

	function getFieldIndex(offset: number): number {
		if (offset < 0 || offset >= byteFieldIdx.length) return -1;
		return byteFieldIdx[offset];
	}

	let selectedField = $derived.by(() => selectedOffset < 0 ? null : getFieldAt(selectedOffset));
	let hoveredField = $derived.by(() => highlightedOffset < 0 ? null : getFieldAt(highlightedOffset));
	let activeField = $derived(hoveredField ?? selectedField);

	const ENUM_PATTERN = /^([A-Z][A-Z0-9_]+)\s+\((.+)\)$/;
	function isEnumValue(val: string): { label: string; raw: string } | null {
		const m = val.match(ENUM_PATTERN);
		return m ? { label: m[1], raw: m[2] } : null;
	}

	function flattenFields(fields: MappedField[]): MappedField[] {
		const result: MappedField[] = [];
		for (const f of fields) {
			if (f.children && f.children.length > 0 && !f.typeName.includes('[')) {
				result.push(...flattenFields(f.children));
			} else {
				result.push(f);
			}
		}
		return result;
	}

	function formatValue(val: unknown): string {
		if (val === null || val === undefined) return '-';
		if (typeof val === 'object') return JSON.stringify(val);
		return String(val);
	}

	const HEX_TABLE: string[] = [];
	for (let i = 0; i < 256; i++) HEX_TABLE.push(i.toString(16).toUpperCase().padStart(2, '0'));

	function toHex(b: number): string { return HEX_TABLE[b]; }
	function toAscii(b: number): string { return b >= 32 && b < 127 ? String.fromCharCode(b) : '\u00B7'; }

	function formatOffset(offset: number): string {
		if (hexOffsetFormat === 'dec') return offset.toString().padStart(8, ' ');
		return offset.toString(16).toUpperCase().padStart(8, '0');
	}

	function formatOffsetShort(offset: number): string {
		if (hexOffsetFormat === 'dec') return String(offset);
		return '0x' + offset.toString(16).toUpperCase();
	}

	function getCellStyle(absOffset: number): string {
		const fr = getFieldAt(absOffset);
		if (!fr) return '';
		const c = FIELD_COLORS[fr.colorIdx];
		const isAct = activeField && absOffset >= activeField.offset && absOffset < activeField.offset + activeField.size;
		const isSel = selectedField && absOffset >= selectedField.offset && absOffset < selectedField.offset + selectedField.size;
		const rgb = isAct ? c.hi : c.bg;
		const alpha = isAct ? '0.25' : '0.15';
		let style = `background:rgba(${rgb},${alpha});`;
		if (isSel) style += `box-shadow:inset 0 0 0 1.5px rgba(${c.hi},0.7);`;
		if (absOffset === cursorOffset) style += `outline:1.5px solid rgba(${c.hi},0.9);outline-offset:-1.5px;`;
		return style;
	}

	function handleByteClick(absOffset: number) {
		cursorOffset = absOffset;
		const fr = getFieldAt(absOffset);
		if (fr) selectedOffset = fr.offset;
		hexGridEl?.focus();
	}

	function handleByteEnter(absOffset: number) {
		const fr = getFieldAt(absOffset);
		if (fr) highlightedOffset = fr.offset;
	}

	function handleFieldClick(fr: FieldRange) {
		selectedOffset = fr.offset;
		cursorOffset = fr.offset;
		// Scroll hex to show field
		const rowIdx = Math.floor(fr.offset / BYTES_PER_ROW);
		if (hexScrollEl) {
			hexScrollEl.scrollTop = rowIdx * ROW_HEIGHT - containerHeight / 2;
		}
		hexGridEl?.focus();
	}

	function scrollToRow(rowIdx: number) {
		if (hexScrollEl) {
			hexScrollEl.scrollTop = rowIdx * ROW_HEIGHT - containerHeight / 2;
		}
	}

	function handleKeydown(e: KeyboardEvent) {
		if (cursorOffset < 0 && selectedOffset < 0) return;
		const cur = cursorOffset >= 0 ? cursorOffset : selectedOffset;
		let next = cur;

		switch (e.key) {
			case 'ArrowRight': next = Math.min(cur + 1, rawBytes.length - 1); e.preventDefault(); break;
			case 'ArrowLeft': next = Math.max(cur - 1, 0); e.preventDefault(); break;
			case 'ArrowDown': next = Math.min(cur + BYTES_PER_ROW, rawBytes.length - 1); e.preventDefault(); break;
			case 'ArrowUp': next = Math.max(cur - BYTES_PER_ROW, 0); e.preventDefault(); break;
			case 'Tab': {
				e.preventDefault();
				const curFieldIdx = getFieldIndex(cur);
				const nextFieldIdx = e.shiftKey
					? (curFieldIdx > 0 ? curFieldIdx - 1 : fieldRanges.length - 1)
					: (curFieldIdx < fieldRanges.length - 1 ? curFieldIdx + 1 : 0);
				const nextField = fieldRanges[nextFieldIdx];
				if (nextField) { next = nextField.offset; selectedOffset = nextField.offset; }
				break;
			}
			case 'Escape': selectedOffset = -1; cursorOffset = -1; highlightedOffset = -1; e.preventDefault(); return;
			default: return;
		}
		cursorOffset = next;
		const fr = getFieldAt(next);
		if (fr) selectedOffset = fr.offset;
		scrollToRow(Math.floor(next / BYTES_PER_ROW));
	}
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
	class="flex flex-col"
	onmousemove={handleMouseMove}
	onmouseup={handleMouseUp}
	onmouseleave={handleMouseUp}
>
<div class="flex" style="height:{totalHeight_}px">
	<!-- Hex Grid (left) -->
	<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
	<div
		bind:this={hexGridEl}
		class="flex-1 min-w-0 flex flex-col border rounded-lg bg-background overflow-hidden focus:outline-none focus:ring-1 focus:ring-primary/30"
		tabindex="0"
		onkeydown={handleKeydown}
	>
		<!-- Toolbar -->
		<div class="flex items-center gap-2 px-2 bg-muted text-[10px] font-mono text-muted-foreground select-none shrink-0 border-b" style="height:24px">
			<button
				class="px-1.5 py-0.5 rounded hover:bg-muted-foreground/10 transition-colors"
				onclick={() => hexOffsetFormat = hexOffsetFormat === 'hex' ? 'dec' : 'hex'}
				title="Offset 표시 형식 전환"
			>{hexOffsetFormat === 'hex' ? 'HEX' : 'DEC'}</button>
			<span class="opacity-40">|</span>
			<span class="ml-auto">{rawBytes.length.toLocaleString()} bytes</span>
		</div>
		<!-- Column header -->
		<div class="flex items-center bg-muted/60 text-[10px] font-mono text-muted-foreground select-none shrink-0 border-b" style="height:{ROW_HEIGHT}px">
			<span class="w-[72px] px-2 shrink-0 text-right">Offset</span>
			{#each { length: 16 } as _, i}
				<span class="shrink-0 text-center {i === 8 ? 'ml-1' : ''}" style="width:26px">{HEX_TABLE[i]}</span>
			{/each}
			<span class="w-3 shrink-0"></span>
			<span class="px-1">ASCII</span>
		</div>
		<!-- Virtual scroll area -->
		<div
			bind:this={hexScrollEl}
			class="flex-1 overflow-auto min-h-0"
			onscroll={handleScroll}
		>
			<div style="height:{totalHeight}px;position:relative">
				<div style="position:absolute;top:{offsetY}px;left:0;right:0">
					{#each visibleRowIndices as rowIdx (rowIdx)}
						{@const rowOffset = rowIdx * BYTES_PER_ROW}
						{@const rowEnd = Math.min(rowOffset + BYTES_PER_ROW, rawBytes.length)}
						{@const byteCount = rowEnd - rowOffset}
						<div class="flex items-center text-[11px] font-mono" style="height:{ROW_HEIGHT}px">
							<span class="w-[72px] px-2 shrink-0 text-right text-muted-foreground select-none">{formatOffset(rowOffset)}</span>
							{#each { length: BYTES_PER_ROW } as _, col}
								{@const abs = rowOffset + col}
								{@const hasByte = col < byteCount}
								{#if col === 8}<span class="w-1 shrink-0"></span>{/if}
								{#if hasByte}
									<!-- svelte-ignore a11y_no_static_element_interactions a11y_click_events_have_key_events -->
									<span
										class="shrink-0 text-center cursor-pointer select-none leading-[22px]"
										style="width:26px;{getCellStyle(abs)}"
										onclick={() => handleByteClick(abs)}
										onmouseenter={() => handleByteEnter(abs)}
										onmouseleave={() => (highlightedOffset = -1)}
									>{toHex(rawBytes[abs])}</span>
								{:else}
									<span class="shrink-0" style="width:26px"></span>
								{/if}
							{/each}
							<span class="w-3 shrink-0"></span>
							<span class="select-none whitespace-pre text-muted-foreground/70">
								{#each { length: byteCount } as _, col}
									{@const abs = rowOffset + col}
									<!-- svelte-ignore a11y_no_static_element_interactions a11y_click_events_have_key_events -->
									<span
										class="inline-block text-center cursor-pointer"
										style="width:8px;{getCellStyle(abs)}"
										onclick={() => handleByteClick(abs)}
										onmouseenter={() => handleByteEnter(abs)}
										onmouseleave={() => (highlightedOffset = -1)}
									>{toAscii(rawBytes[abs])}</span>
								{/each}
							</span>
						</div>
					{/each}
				</div>
			</div>
		</div>
	</div>

	<!-- Horizontal resize handle -->
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="w-1.5 shrink-0 cursor-col-resize hover:bg-primary/20 active:bg-primary/30 transition-colors rounded"
		onmousedown={startResizeX}
	></div>

	<!-- Right panel -->
	<div class="shrink-0 flex flex-col gap-2 min-h-0" style="width:{panelWidth}px">
		{#if selectedField}
			<div
				class="border rounded-lg bg-background p-3 text-xs font-mono shrink-0 border-l-4 {BORDER_COLORS[selectedField.colorIdx]}"
				style="background:rgba({FIELD_COLORS[selectedField.colorIdx].bg},0.08)"
			>
				<div class="flex items-center gap-2 mb-2">
					<div class="w-3 h-3 rounded-sm shrink-0" style="background:rgba({FIELD_COLORS[selectedField.colorIdx].bg},0.5)"></div>
					<span class="font-bold text-sm">{selectedField.fieldName}</span>
					<span class="text-muted-foreground ml-auto">{selectedField.typeName}</span>
				</div>
				<div class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
					<span class="text-muted-foreground">Offset</span>
					<span>0x{selectedField.offset.toString(16).toUpperCase()} ({selectedField.offset})</span>
					<span class="text-muted-foreground">Size</span>
					<span>{selectedField.size} byte{selectedField.size > 1 ? 's' : ''}</span>
					<span class="text-muted-foreground">Value</span>
					<span class="break-all">
						{#if isEnumValue(selectedField.value)}
							{@const ev = isEnumValue(selectedField.value)}
							<span class="px-1.5 py-0.5 rounded text-[10px] border border-primary text-primary font-semibold">{ev?.label}</span>
							<span class="text-muted-foreground ml-1">{ev?.raw}</span>
						{:else}
							{selectedField.value}
						{/if}
					</span>
					<span class="text-muted-foreground">Hex</span>
					<span class="break-all text-muted-foreground/80">{selectedField.hexBytes}</span>
				</div>
			</div>
		{:else}
			<div class="border rounded-lg bg-background p-3 text-xs text-muted-foreground text-center shrink-0">
				바이트를 클릭하거나 <kbd class="px-1 py-0.5 bg-muted rounded text-[10px]">Tab</kbd>으로 필드를 탐색하세요
			</div>
		{/if}

		<!-- Field list -->
		<div class="flex-1 min-h-0 overflow-auto border rounded-lg bg-background p-1.5">
			{#each fieldRanges as fr, fi (fi)}
				{@const isSelected = selectedOffset === fr.offset}
				{@const isHovered = highlightedOffset === fr.offset && !isSelected}
				<button
					class="w-full flex items-center gap-1.5 px-2 py-1 rounded text-xs font-mono border-l-4 text-left transition-all duration-100 mb-0.5
						{BORDER_COLORS[fr.colorIdx]}"
					style="background:rgba({FIELD_COLORS[fr.colorIdx].bg},{isSelected ? '0.2' : isHovered ? '0.15' : '0.08'});
						{isSelected ? `box-shadow:inset 0 0 0 1.5px rgba(${FIELD_COLORS[fr.colorIdx].hi},0.5)` : ''}"
					onclick={() => handleFieldClick(fr)}
					onmouseenter={() => (highlightedOffset = fr.offset)}
					onmouseleave={() => (highlightedOffset = -1)}
				>
					<span class="w-3 h-3 rounded-sm shrink-0" style="background:rgba({FIELD_COLORS[fr.colorIdx].bg},0.4)"></span>
					<span class="w-[4.5rem] text-muted-foreground text-right shrink-0">{formatOffsetShort(fr.offset)}</span>
					<span class="flex-1 truncate font-semibold min-w-0" title={fr.fieldName}>{fr.fieldName}</span>
					<span class="w-28 truncate shrink-0 text-right" title={fr.value}>
						{#if isEnumValue(fr.value)}
							<span class="px-1 py-0.5 rounded text-[10px] border border-primary text-primary">{isEnumValue(fr.value)?.label}</span>
						{:else}
							<span class="text-muted-foreground">{fr.value}</span>
						{/if}
					</span>
				</button>
			{/each}
		</div>
	</div>
</div>
<!-- Vertical resize handle -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
	class="h-1.5 cursor-row-resize hover:bg-primary/20 active:bg-primary/30 transition-colors rounded mx-4"
	onmousedown={startResizeY}
></div>
</div>
