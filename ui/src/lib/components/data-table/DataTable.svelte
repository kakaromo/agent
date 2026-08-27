<script lang="ts" generics="TData">
	import {
		type ColumnDef,
		type PaginationState,
		type SortingState,
		type ColumnFiltersState,
		type RowSelectionState,
		type VisibilityState,
		getCoreRowModel,
		getFilteredRowModel,
		getPaginationRowModel,
		getSortedRowModel
	} from '@tanstack/table-core';
	import { createSvelteTable, FlexRender } from '$lib/components/ui/data-table/index.js';
	import { renderComponent } from '$lib/components/ui/data-table/render-helpers.js';
	import { Virtualizer, elementScroll, observeElementRect, observeElementOffset } from '@tanstack/virtual-core';
	import { untrack, onMount, onDestroy } from 'svelte';
	import type { Snippet } from 'svelte';
	import SearchXIcon from '@lucide/svelte/icons/search-x';
	import ArrowUp from '@lucide/svelte/icons/arrow-up';
	import ArrowDown from '@lucide/svelte/icons/arrow-down';
	import ArrowUpDown from '@lucide/svelte/icons/arrow-up-down';
	import ChevronDown from '@lucide/svelte/icons/chevron-down';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import GripVertical from '@lucide/svelte/icons/grip-vertical';
	import DataTableToolbar, { type ActionButton } from './DataTableToolbar.svelte';
	import DataTablePagination from './DataTablePagination.svelte';
	import { SelectCell } from './cells';
	import type { GroupByOption, ServerSideConfig, ServerGroupByConfig } from './types.js';

	interface Props {
		data: TData[];
		columns: ColumnDef<TData, unknown>[];
		filterColumn?: string;
		filterPlaceholder?: string;
		enableRowSelection?: boolean;
		enableMultiRowSelection?: boolean;
		enableColumnVisibility?: boolean;
		actions?: ActionButton[];
		showPagination?: boolean;
		compact?: boolean;
		getRowId?: (row: TData) => string;
		onSelectionChange?: (selectedRows: TData[]) => void;
		groupByOptions?: GroupByOption<TData>[];
		serverSide?: ServerSideConfig;
		serverGroupBy?: ServerGroupByConfig;
		initialSorting?: SortingState;
		initialPageSize?: number;
		expandableRowContent?: Snippet<[{ row: TData; expanded: boolean; toggle: () => void }]>;
		onRowExpand?: (row: TData) => void;
		canExpandRow?: (row: TData) => boolean;
		canDragRow?: (row: TData) => boolean;
		onRowDrop?: (fromIndex: number, toIndex: number) => void;
		onRowDoubleClick?: (row: TData) => void;
		/** Fixed-height scrollable body with sticky header. e.g. "400px" */
		scrollHeight?: string;
		/** Click cell to copy value to clipboard */
		enableCellCopy?: boolean;
		/** External row selection — set of row IDs to select. Syncs with internal rowSelection. */
		selectedRowIds?: Set<string>;
		/** Purely visual highlight — row IDs to mark with the selected background, without triggering row selection state. */
		highlightedRowIds?: Set<string>;
	}

	let {
		data,
		columns,
		filterColumn = '',
		filterPlaceholder = 'Search...',
		enableRowSelection = false,
		enableMultiRowSelection = true,
		enableColumnVisibility = true,
		actions = [],
		showPagination = true,
		compact = false,
		getRowId = (row: TData) => String((row as Record<string, unknown>).id ?? Math.random()),
		onSelectionChange,
		groupByOptions = [],
		serverSide,
		serverGroupBy,
		initialSorting = [],
		initialPageSize,
		expandableRowContent,
		onRowExpand,
		canExpandRow,
		canDragRow,
		onRowDrop,
		onRowDoubleClick,
		scrollHeight,
		enableCellCopy = false,
		selectedRowIds,
		highlightedRowIds
	}: Props = $props();

	// Reactive highlight set — function form loses prop reactivity across component boundary.
	const highlightSet = $derived(highlightedRowIds ?? new Set<string>());

	// Grouping state
	let groupByKey = $state<string | null>(null);
	let collapsedGroups = $state<Set<string>>(new Set());

	interface GroupedData {
		key: string;
		label: string;
		rows: TData[];
	}

	const groupedData = $derived.by((): GroupedData[] | null => {
		if (!groupByKey) return null;

		const option = groupByOptions.find(o => o.key === groupByKey);
		if (!option) return null;

		const map = new Map<string, TData[]>();
		for (const row of data) {
			const value = option.getValue
				? option.getValue(row)
				: String((row as Record<string, unknown>)[option.key] ?? 'Unknown');
			if (!map.has(value)) map.set(value, []);
			map.get(value)!.push(row);
		}

		return [...map.entries()]
			.map(([key, rows]) => ({ key, label: key, rows }))
			.sort((a, b) => b.rows.length - a.rows.length);
	});

	function toggleGroup(key: string) {
		const next = new Set(collapsedGroups);
		if (next.has(key)) next.delete(key);
		else next.add(key);
		collapsedGroups = next;
	}

	// Expandable row accordion state
	let expandedRows = $state<Set<string>>(new Set());

	function toggleExpand(rowId: string, rowData?: TData) {
		const next = new Set(expandedRows);
		const isExpanding = !next.has(rowId);
		if (isExpanding) next.add(rowId);
		else next.delete(rowId);
		expandedRows = next;
		if (isExpanding && rowData && onRowExpand) {
			onRowExpand(rowData);
		}
	}

	let sorting = $state<SortingState>(initialSorting);
	let columnFilters = $state<ColumnFiltersState>([]);
	let pagination = $state<PaginationState>({
		pageIndex: 0,
		pageSize: initialPageSize ?? 20
	});
	let rowSelection = $state<RowSelectionState>({});
	let columnVisibility = $state<VisibilityState>({});

	// Selection column
	const selectionColumn: ColumnDef<TData, unknown> = {
		id: 'select',
		header: ({ table }) => renderComponent(SelectCell, { mode: 'all', table }),
		cell: ({ row }) => renderComponent(SelectCell, { mode: 'row', row }),
		enableSorting: false,
		enableHiding: false,
		size: 44
	};

	const finalColumns = $derived(
		enableRowSelection ? [selectionColumn, ...columns] : columns
	);

	// Large-data windowing: when scrollHeight is set and data exceeds threshold,
	// feed only a window of data to tanstack table to avoid OOM from Row object creation.
	//
	// 주의: window slice 가 갱신될 때마다 (virtualWindowStart 변동) tanstack table 의
	// data prop 이 새 배열로 교체되어 row 인스턴스 재생성 → 사용자 시점에서 scroll 이
	// 위로 튕기는 현상 발생. 따라서 threshold 를 충분히 높여 일반적인 데이터셋
	// (수천 row) 은 그냥 통째로 table 에 넣고, virtual-core 가 DOM 가상화만 담당.
	// windowing 은 진짜 거대한 데이터 (수만 row) 에서 OOM 방지용으로만 발동.
	const VIRTUAL_WINDOW_THRESHOLD = 10000;
	let virtualWindowStart = $state(0);
	const VIRTUAL_WINDOW_BUFFER = 100; // extra rows above/below visible range

	const useVirtualWindow = $derived(!!scrollHeight && data.length > VIRTUAL_WINDOW_THRESHOLD);

	const windowedData = $derived.by(() => {
		if (!useVirtualWindow) return data;
		const start = Math.max(0, virtualWindowStart - VIRTUAL_WINDOW_BUFFER);
		const end = Math.min(data.length, virtualWindowStart + VIRTUAL_WINDOW_BUFFER * 3);
		return data.slice(start, end);
	});

	// Offset for mapping windowed index back to original data index
	const windowOffset = $derived(
		useVirtualWindow ? Math.max(0, virtualWindowStart - VIRTUAL_WINDOW_BUFFER) : 0
	);

	const table = createSvelteTable({
		get data() {
			return windowedData;
		},
		get columns() {
			return finalColumns;
		},
		get enableMultiRowSelection() {
			return enableMultiRowSelection;
		},
		get pageCount() {
			if (serverSide) return Math.ceil(serverSide.totalItems / serverSide.pageSize);
			return undefined;
		},
		get manualPagination() {
			return !!serverSide || !!scrollHeight;
		},
		state: {
			get sorting() {
				return sorting;
			},
			get columnFilters() {
				return columnFilters;
			},
			get pagination() {
				return pagination;
			},
			get rowSelection() {
				return rowSelection;
			},
			get columnVisibility() {
				return columnVisibility;
			}
		},
		enableRowSelection: enableRowSelection,
		onSortingChange(updater) {
			sorting = typeof updater === 'function' ? updater(sorting) : updater;
		},
		onColumnFiltersChange(updater) {
			columnFilters = typeof updater === 'function' ? updater(columnFilters) : updater;
		},
		onPaginationChange(updater) {
			const newPagination = typeof updater === 'function' ? updater(pagination) : updater;
			pagination = newPagination;
			if (serverSide) {
				serverSide.onPageChange(newPagination.pageIndex, newPagination.pageSize);
			}
		},
		onRowSelectionChange(updater) {
			rowSelection = typeof updater === 'function' ? updater(rowSelection) : updater;
		},
		onColumnVisibilityChange(updater) {
			columnVisibility = typeof updater === 'function' ? updater(columnVisibility) : updater;
		},
		getRowId,
		getCoreRowModel: getCoreRowModel(),
		getSortedRowModel: getSortedRowModel(),
		getFilteredRowModel: getFilteredRowModel(),
		getPaginationRowModel: (serverSide || scrollHeight) ? undefined : getPaginationRowModel()
	});

	// Notify selection changes (skip when triggered by external sync)
	let externalSync = false;

	$effect(() => {
		const selectedRows = (table.getFilteredSelectedRowModel?.()?.rows ?? []).map((row) => row.original);
		if (onSelectionChange && !externalSync) {
			untrack(() => onSelectionChange(selectedRows));
		}
	});

	// Sync external selectedRowIds → internal rowSelection
	$effect(() => {
		if (!selectedRowIds) return;
		const ids = selectedRowIds; // track the reactive read
		const newSelection: RowSelectionState = {};
		for (const id of ids) {
			newSelection[id] = true;
		}
		// Only update if different to avoid loops
		const keys = Object.keys(rowSelection);
		if (keys.length !== ids.size || keys.some(k => !ids.has(k))) {
			externalSync = true;
			rowSelection = newSelection;
			// Reset flag after Svelte processes the update
			queueMicrotask(() => { externalSync = false; });
		}
	});

	const selectedCount = $derived(table.getFilteredSelectedRowModel?.()?.rows?.length ?? 0);

	// Scroll hint
	let scrollContainer = $state<HTMLDivElement | null>(null);
	let showScrollHint = $state(false);

	function checkOverflow() {
		if (scrollContainer) {
			showScrollHint = scrollContainer.scrollWidth > scrollContainer.clientWidth;
		}
	}

	$effect(() => {
		data;
		requestAnimationFrame(checkOverflow);
	});

	// Cell range selection & copy (Excel-like: click, drag, Ctrl+A, Ctrl+C)
	let selectedCells = $state<Set<string>>(new Set());
	let copiedCells = $state<Set<string>>(new Set());
	let isDragging = $state(false);
	let anchorPos = $state<{row: number, col: number} | null>(null);
	let currentPos = $state<{row: number, col: number} | null>(null);
	let selectAll = $state(false);
	let copiedAll = $state(false);

	// 다른 DataTable이 전체선택하면 자신의 선택 해제
	const tableInstanceId =
		typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
			? crypto.randomUUID()
			: `dt-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;

	function onOtherTableSelectAll(e: Event) {
		const detail = (e as CustomEvent).detail;
		if (detail !== tableInstanceId) {
			selectAll = false;
			selectedCells = new Set();
			anchorPos = null;
			currentPos = null;
		}
	}

	onMount(() => {
		window.addEventListener('datatable-select-all', onOtherTableSelectAll);
	});
	onDestroy(() => {
		window.removeEventListener('datatable-select-all', onOtherTableSelectAll);
	});

	const selectColOffset = $derived(enableRowSelection ? 1 : 0);

	// Ordered visible rows for coordinate mapping (flat & grouped)
	const visibleRows = $derived.by(() => {
		if (groupedData && !serverGroupBy) {
			const rowMap = new Map(table.getCoreRowModel().rows.map(r => [getRowId(r.original), r]));
			return groupedData
				.filter(g => !collapsedGroups.has(g.key))
				.flatMap(g => g.rows.map(rd => rowMap.get(getRowId(rd))))
				.filter((r): r is NonNullable<typeof r> => r != null);
		}
		return table.getRowModel().rows;
	});

	// Row data ID -> visible row index for grouped view
	const groupedRowIndices = $derived.by(() => {
		if (!groupedData || serverGroupBy) return new Map<string, number>();
		const map = new Map<string, number>();
		let idx = 0;
		for (const group of groupedData) {
			if (collapsedGroups.has(group.key)) continue;
			for (const rd of group.rows) {
				map.set(getRowId(rd), idx++);
			}
		}
		return map;
	});

	function getCellsInRect(anchor: {row: number, col: number}, current: {row: number, col: number}): Set<string> {
		const r0 = Math.min(anchor.row, current.row), r1 = Math.max(anchor.row, current.row);
		const c0 = Math.min(anchor.col, current.col), c1 = Math.max(anchor.col, current.col);
		const set = new Set<string>();
		const rows = visibleRows;
		for (let r = r0; r <= r1 && r < rows.length; r++) {
			const cells = rows[r].getVisibleCells().filter(c => c.column.id !== 'select');
			for (let c = c0; c <= c1 && c < cells.length; c++) {
				set.add(cells[c].id);
			}
		}
		return set;
	}

	/**
	 * 한 셀의 복사 텍스트. 컬럼이 `meta.copyText` 를 선언하면 그걸 쓰고, 없으면 raw 값.
	 *
	 * 셀을 `cell:` 로 가공해 보여주는 컬럼(io_flags 의 `0x... (WRITE, DATA)` 처럼 합성한
	 * 값, hex 표기, 소수점 자리 고정 등)은 raw 값만 복사하면 화면과 다른 내용이 붙는다.
	 * 복사는 "보이는 그대로" 가 기대값이라 컬럼이 직접 직렬화를 정하게 한다.
	 */
	function cellCopyText(colDef: unknown, row: TData, rawValue: unknown): string {
		const fn = (colDef as { meta?: { copyText?: (row: TData) => string } })?.meta?.copyText;
		if (typeof fn === 'function') return fn(row);
		return String(rawValue ?? '');
	}

	function getRectTsv(anchor: {row: number, col: number}, current: {row: number, col: number}): string {
		const r0 = Math.min(anchor.row, current.row), r1 = Math.max(anchor.row, current.row);
		const c0 = Math.min(anchor.col, current.col), c1 = Math.max(anchor.col, current.col);
		const rows = visibleRows;
		const lines: string[] = [];
		for (let r = r0; r <= r1 && r < rows.length; r++) {
			const cells = rows[r].getVisibleCells().filter(c => c.column.id !== 'select');
			const vals: string[] = [];
			for (let c = c0; c <= c1 && c < cells.length; c++) {
				vals.push(
					cellCopyText(cells[c].column.columnDef, rows[r].original as TData, cells[c].getValue())
				);
			}
			lines.push(vals.join('\t'));
		}
		return lines.join('\n');
	}

	function getTableTsv(): string {
		// useVirtualWindow 가 켜진 큰 데이터셋에서는 table 인스턴스의 row 가
		// windowed slice (300~500개) 만 가지므로, Ctrl-A 전체 복사는 원본 data
		// 배열에서 직접 값을 뽑아야 한다.
		const visibleColDefs = columns.filter((c) => {
			const id = (c as { id?: string; accessorKey?: string }).id
				?? (c as { accessorKey?: string }).accessorKey;
			return id !== 'select';
		});
		const headerOf = (c: ColumnDef<TData, unknown>): string => {
			const h = (c as { header?: unknown }).header;
			if (typeof h === 'string') return h;
			const id = (c as { id?: string; accessorKey?: string }).id
				?? (c as { accessorKey?: string }).accessorKey
				?? '';
			return id;
		};
		const valueOf = (c: ColumnDef<TData, unknown>, row: TData): unknown => {
			const af = (c as { accessorFn?: (row: TData, idx: number) => unknown }).accessorFn;
			if (typeof af === 'function') return af(row, 0);
			const ak = (c as { accessorKey?: string }).accessorKey;
			if (ak) {
				const parts = ak.split('.');
				let v: unknown = row;
				for (const p of parts) {
					if (v == null) return '';
					v = (v as Record<string, unknown>)[p];
				}
				return v;
			}
			return '';
		};
		const headers = visibleColDefs.map(headerOf);
		const lines = data.map((row) =>
			visibleColDefs.map((c) => cellCopyText(c, row, valueOf(c, row))).join('\t')
		);
		return [headers.join('\t'), ...lines].join('\n');
	}

	function parseCellPos(td: HTMLElement): {row: number, col: number} | null {
		const r = td.dataset.rowIdx, c = td.dataset.colIdx;
		if (r == null || c == null) return null;
		return { row: parseInt(r), col: parseInt(c) };
	}

	function handleCellMouseDown(e: MouseEvent) {
		if (!enableCellCopy) return;
		const target = e.target as HTMLElement;
		if (target.closest('button, a, input, select, textarea')) return;
		const td = target.closest('td') as HTMLElement | null;
		if (!td?.dataset.cellId) return;
		const pos = parseCellPos(td);
		if (!pos) return;
		isDragging = true;
		anchorPos = pos;
		currentPos = pos;
		selectAll = false;
		selectedCells = getCellsInRect(pos, pos);
	}

	function handleCellMouseMove(e: MouseEvent) {
		if (!enableCellCopy || !isDragging || !anchorPos) return;
		const td = (e.target as HTMLElement).closest('td') as HTMLElement | null;
		if (!td?.dataset.cellId) return;
		const pos = parseCellPos(td);
		if (!pos) return;
		if (currentPos && pos.row === currentPos.row && pos.col === currentPos.col) return;
		currentPos = pos;
		selectedCells = getCellsInRect(anchorPos, pos);
	}

	function handleCellMouseUp() {
		isDragging = false;
	}

	function handleKeyDown(e: KeyboardEvent) {
		if (!enableCellCopy) return;
		if ((e.ctrlKey || e.metaKey) && e.key === 'a' && scrollContainer?.contains(document.activeElement ?? document.body)) {
			e.preventDefault();
			selectAll = true;
			selectedCells = new Set();
			anchorPos = null;
			currentPos = null;
			window.dispatchEvent(new CustomEvent('datatable-select-all', { detail: tableInstanceId }));
			// Ctrl-A 한 번으로 전체 데이터 클립보드 복사 (Ctrl-C 별도 필요 없음).
			const text = getTableTsv();
			navigator.clipboard.writeText(text).then(() => {
				copiedAll = true;
				setTimeout(() => { copiedAll = false; }, 800);
			}).catch(() => {});
			return;
		}
		if ((e.ctrlKey || e.metaKey) && e.key === 'c' && (selectedCells.size > 0 || selectAll)) {
			e.preventDefault();
			const text = selectAll ? getTableTsv() : (anchorPos && currentPos ? getRectTsv(anchorPos, currentPos) : '');
			navigator.clipboard.writeText(text).then(() => {
				if (selectAll) {
					copiedAll = true;
					setTimeout(() => { copiedAll = false; }, 800);
				} else {
					copiedCells = new Set(selectedCells);
					setTimeout(() => { copiedCells = new Set(); }, 800);
				}
			}).catch(() => {});
		}
	}

	// Virtual scrolling for scrollHeight mode
	const ROW_HEIGHT = 28; // matches h-7 (1.75rem = 28px)

	// Svelte 5 rune-compatible virtualizer using @tanstack/virtual-core directly
	let virtualVersion = $state(0);

	function createSvelte5Virtualizer(count: number, getScrollEl: () => HTMLElement | null) {
		const instance = new Virtualizer({
			count,
			getScrollElement: getScrollEl,
			estimateSize: () => ROW_HEIGHT,
			overscan: 5,
			observeElementRect,
			observeElementOffset,
			scrollToFn: elementScroll,
			onChange: (v) => {
				virtualVersion = Date.now();
				// Update virtual window start for large-data windowing
				if (useVirtualWindow) {
					const items = v.getVirtualItems();
					if (items.length > 0) {
						const newStart = items[0].index;
						if (Math.abs(newStart - virtualWindowStart) > VIRTUAL_WINDOW_BUFFER / 2) {
							virtualWindowStart = newStart;
						}
					}
				}
			}
		});
		instance._willUpdate();
		return instance;
	}

	let flatVirtualizer = $state<Virtualizer<HTMLElement, Element> | null>(null);

	// Virtualizer 인스턴스는 scrollContainer/scrollHeight 변경 시에만 _재생성_.
	// data.length 같이 자주 바뀌는 값으로 instance 를 새로 만들면 scrollTop 이 reset 되어
	// 무한 스크롤 시 자꾸 위로 튕기는 버그 발생. count 갱신은 별도 effect 에서 setOptions 로.
	$effect(() => {
		if (!scrollHeight || !scrollContainer) {
			flatVirtualizer = null;
			return;
		}
		const el = scrollContainer;
		const initialCount = untrack(() =>
			useVirtualWindow ? data.length : table.getRowModel().rows.length
		);
		const v = createSvelte5Virtualizer(initialCount, () => el);
		flatVirtualizer = v;
		return () => v._didMount()();
	});

	// data 길이 변동에 따라 instance 의 count 만 갱신 — instance 자체는 유지하므로 scroll 위치 보존.
	$effect(() => {
		const v = flatVirtualizer;
		if (!v) return;
		const totalCount = useVirtualWindow ? data.length : table.getRowModel().rows.length;
		v.setOptions({ ...v.options, count: totalCount });
		v.measure();
	});

	const virtualRows = $derived.by(() => { virtualVersion; return flatVirtualizer?.getVirtualItems() ?? []; });
	const totalVirtualHeight = $derived.by(() => { virtualVersion; return flatVirtualizer?.getTotalSize() ?? 0; });

	// Virtual scrolling for grouped view
	type GroupFlatItem = { type: 'group-header'; key: string; label: string; count: number }
		| { type: 'row'; key: string; groupKey: string; rowData: TData };

	const groupFlatItems = $derived.by((): GroupFlatItem[] => {
		if (!groupedData || serverGroupBy) return [];
		const items: GroupFlatItem[] = [];
		for (const group of groupedData) {
			items.push({ type: 'group-header', key: `gh-${group.key}`, label: group.label, count: group.rows.length });
			if (!collapsedGroups.has(group.key)) {
				for (const rd of group.rows) {
					items.push({ type: 'row', key: getRowId(rd), groupKey: group.key, rowData: rd });
				}
			}
		}
		return items;
	});

	let groupVirtualizer = $state<Virtualizer<HTMLElement, Element> | null>(null);

	$effect(() => {
		// 인스턴스 재생성은 컨테이너 변경 시에만. groupFlatItems.length 의 변동은 setOptions 로 반영.
		if (!scrollHeight || !scrollContainer || !groupedData) {
			groupVirtualizer = null;
			return;
		}
		const el = scrollContainer;
		const initialCount = untrack(() => groupFlatItems.length);
		if (initialCount === 0) {
			groupVirtualizer = null;
			return;
		}
		const v = createSvelte5Virtualizer(initialCount, () => el);
		groupVirtualizer = v;
		return () => v._didMount()();
	});

	$effect(() => {
		const v = groupVirtualizer;
		if (!v) return;
		v.setOptions({ ...v.options, count: groupFlatItems.length });
		v.measure();
	});

	const groupVirtualRows = $derived.by(() => { virtualVersion; return groupVirtualizer?.getVirtualItems() ?? []; });
	const totalGroupVirtualHeight = $derived.by(() => { virtualVersion; return groupVirtualizer?.getTotalSize() ?? 0; });

	const virtualizer = $derived(flatVirtualizer);

	// Drag & drop reorder state
	let dragRowIdx = $state<number | null>(null);
	let dropTargetIdx = $state<number | null>(null);
</script>

<svelte:window onresize={checkOverflow} onkeydown={handleKeyDown} onmouseup={enableCellCopy ? handleCellMouseUp : undefined} />

<div class="space-y-2">
	<!-- Toolbar -->
	<div class="flex items-center gap-2">
		{#if groupByOptions.length > 0}
			<div class="flex items-center gap-1 shrink-0">
				<span class="text-[10px] text-muted-foreground">Group:</span>
				<div class="flex rounded border text-[10px]">
					<button
						class="px-2 py-0.5 transition-colors {groupByKey === null ? 'bg-primary text-primary-foreground' : 'hover:bg-muted'}"
						onclick={() => groupByKey = null}
					>None</button>
					{#each groupByOptions as option (option.key)}
						<button
							class="px-2 py-0.5 border-l transition-colors {groupByKey === option.key ? 'bg-primary text-primary-foreground' : 'hover:bg-muted'}"
							onclick={() => { groupByKey = option.key; collapsedGroups = new Set(); }}
						>{option.label}</button>
					{/each}
				</div>
			</div>
		{/if}
		<DataTableToolbar
			{table}
			{filterColumn}
			{filterPlaceholder}
			{actions}
			{enableColumnVisibility}
			{selectedCount}
		/>
	</div>

	<!-- Table - Clean Style -->
	<div class="relative">
		{#if showScrollHint}
			<div
				class="pointer-events-none absolute right-0 top-0 bottom-0 z-10 w-6 bg-gradient-to-l from-background to-transparent"
			></div>
		{/if}
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			class="overflow-x-auto rounded-lg border border-border bg-card {enableCellCopy ? 'cursor-cell' : ''} {enableCellCopy ? 'focus:outline-none focus:ring-1 focus:ring-primary/30' : ''} {isDragging ? 'select-none' : ''}"
			class:overflow-y-auto={!!scrollHeight}
			style:max-height={scrollHeight}
			bind:this={scrollContainer}
			tabindex={enableCellCopy ? 0 : undefined}
			onmousedown={enableCellCopy ? handleCellMouseDown : undefined}
			onmousemove={enableCellCopy ? handleCellMouseMove : undefined}
			onscroll={() => {
				if (scrollContainer) {
					const atEnd =
						scrollContainer.scrollLeft + scrollContainer.clientWidth >=
						scrollContainer.scrollWidth - 2;
					showScrollHint = !atEnd && scrollContainer.scrollWidth > scrollContainer.clientWidth;
				}
			}}
		>
			<table class="w-full text-[11px]">
				<!-- sticky thead: row 가 뒤로 지나갈 때 비치지 않도록 thead 자체는 투명, 각 <th> 에 불투명 배경 + 하단 border. -->
				<thead class="sticky top-0 z-20">
					{#each table.getHeaderGroups() as headerGroup (headerGroup.id)}
						<tr>
							{#if onRowDrop}<th class="w-7 bg-background border-b border-border"></th>{/if}
							{#if expandableRowContent}<th class="w-7 bg-background border-b border-border"></th>{/if}
							{#each headerGroup.headers as header (header.id)}
								<th
									class="h-7 px-2 text-left text-[11px] font-normal text-muted-foreground whitespace-nowrap bg-muted border-b border-border {header.id === 'select' ? 'w-9 text-center' : ''}"
								>
									{#if !header.isPlaceholder}
										{#if header.column.getCanSort()}
											<button
												class="inline-flex items-center gap-0.5 transition-colors hover:text-foreground"
												onclick={header.column.getToggleSortingHandler()}
											>
												<FlexRender
													content={header.column.columnDef.header}
													context={header.getContext()}
												/>
												{#if header.column.getIsSorted() === 'asc'}
													<ArrowUp class="size-2.5 text-primary" />
												{:else if header.column.getIsSorted() === 'desc'}
													<ArrowDown class="size-2.5 text-primary" />
												{:else}
													<ArrowUpDown class="size-2.5 opacity-20" />
												{/if}
											</button>
										{:else}
											<FlexRender
												content={header.column.columnDef.header}
												context={header.getContext()}
											/>
										{/if}
									{/if}
								</th>
							{/each}
						</tr>
					{/each}
				</thead>
				<tbody>
					{#if serverGroupBy && groupByKey && !serverGroupBy.selectedGroup}
						<!-- Server-side Grouped View: show group list -->
						{#each serverGroupBy.groups as group (group.groupValue)}
							<tr
								class="bg-muted/40 cursor-pointer hover:bg-muted/60 transition-colors"
								onclick={() => serverGroupBy?.onGroupSelect(group.groupValue, 0, pagination.pageSize)}
							>
								<td colspan={finalColumns.length} class="h-7 px-2">
									<div class="flex items-center gap-1 text-[11px] font-medium">
										<ChevronRight class="size-3" />
										<span>{group.groupKey}</span>
										<span class="text-muted-foreground font-normal">({group.count})</span>
									</div>
								</td>
							</tr>
						{/each}
						{#if serverGroupBy.groups.length === 0}
							<tr>
								<td colspan={finalColumns.length} class="px-2 py-8 text-center">
									<div class="flex flex-col items-center gap-1.5"><SearchXIcon class="size-6 text-muted-foreground/40" /><span class="text-sm text-muted-foreground">검색 결과가 없습니다</span></div>
								</td>
							</tr>
						{/if}
					{:else if groupedData && !serverGroupBy}
						<!-- Client-side Grouped View -->
						{@const allRowsMap = new Map(table.getCoreRowModel().rows.map(r => [getRowId(r.original), r]))}
						{#if groupVirtualizer && scrollHeight}
							<!-- Virtualized grouped view -->
							{#if groupVirtualRows.length > 0}
								<tr style="height: {groupVirtualRows[0].start}px">
									<td colspan={finalColumns.length}></td>
								</tr>
							{/if}
							{#each groupVirtualRows as virtualRow (virtualRow.index)}
								{@const item = groupFlatItems[virtualRow.index]}
								{#if item.type === 'group-header'}
									<tr
										class="bg-muted/40 cursor-pointer hover:bg-muted/60 transition-colors"
										onclick={() => toggleGroup(item.key)}
									>
										<td colspan={finalColumns.length} class="h-7 px-2">
											<div class="flex items-center gap-1 text-[11px] font-medium">
												{#if collapsedGroups.has(item.key)}
													<ChevronRight class="size-3" />
												{:else}
													<ChevronDown class="size-3" />
												{/if}
												<span>{item.label}</span>
												<span class="text-muted-foreground font-normal">({item.count})</span>
											</div>
										</td>
									</tr>
								{:else}
									{@const row = allRowsMap.get(item.key)}
									{#if row}
										<tr
											class="border-b border-border/40 transition-colors hover:bg-muted/30 {(row.getIsSelected() || highlightSet.has(row.id)) ? 'bg-primary/5' : ''}"
											data-selected={row.getIsSelected() || undefined}
										>
											{#each row.getVisibleCells() as cell, cellIdx (cell.id)}
												<td
													class="h-7 px-2 whitespace-nowrap transition-colors
														{cell.column.id === 'select' ? 'w-9 text-center' : ''}
														{enableCellCopy && (selectAll || selectedCells.has(cell.id)) ? 'ring-2 ring-primary/60 ring-inset bg-primary/5' : ''}
														{enableCellCopy && (copiedAll || copiedCells.has(cell.id)) ? 'bg-primary/20 text-primary' : ''}"
													data-cell-id={enableCellCopy ? cell.id : undefined}
													data-row-idx={enableCellCopy && cell.column.id !== 'select' ? groupedRowIndices.get(item.key) : undefined}
													data-col-idx={enableCellCopy && cell.column.id !== 'select' ? cellIdx - selectColOffset : undefined}
												>
													<FlexRender
														content={cell.column.columnDef.cell}
														context={cell.getContext()}
													/>
												</td>
											{/each}
										</tr>
									{/if}
								{/if}
							{/each}
							{#if groupVirtualRows.length > 0}
								<tr style="height: {totalGroupVirtualHeight - groupVirtualRows[groupVirtualRows.length - 1].end}px">
									<td colspan={finalColumns.length}></td>
								</tr>
							{/if}
						{:else}
							<!-- Non-virtualized grouped view (no scrollHeight) -->
							{#each groupedData as group (group.key)}
								<tr
									class="bg-muted/40 cursor-pointer hover:bg-muted/60 transition-colors"
									onclick={() => toggleGroup(group.key)}
								>
									<td colspan={finalColumns.length} class="h-7 px-2">
										<div class="flex items-center gap-1 text-[11px] font-medium">
											{#if collapsedGroups.has(group.key)}
												<ChevronRight class="size-3" />
											{:else}
												<ChevronDown class="size-3" />
											{/if}
											<span>{group.label}</span>
											<span class="text-muted-foreground font-normal">({group.rows.length})</span>
										</div>
									</td>
								</tr>
								{#if !collapsedGroups.has(group.key)}
									{#each group.rows as rowData (getRowId(rowData))}
										{@const row = allRowsMap.get(getRowId(rowData))}
										{#if row}
											<tr
												class="border-b border-border/40 transition-colors hover:bg-muted/30 {(row.getIsSelected() || highlightSet.has(row.id)) ? 'bg-primary/5' : ''}"
												data-selected={row.getIsSelected() || undefined}
											>
												{#each row.getVisibleCells() as cell, cellIdx (cell.id)}
													<td
														class="h-7 px-2 whitespace-nowrap transition-colors
															{cell.column.id === 'select' ? 'w-9 text-center' : ''}
															{enableCellCopy && (selectAll || selectedCells.has(cell.id)) ? 'ring-2 ring-primary/60 ring-inset bg-primary/5' : ''}
															{enableCellCopy && (copiedAll || copiedCells.has(cell.id)) ? 'bg-primary/20 text-primary' : ''}"
														data-cell-id={enableCellCopy ? cell.id : undefined}
														data-row-idx={enableCellCopy && cell.column.id !== 'select' ? groupedRowIndices.get(getRowId(rowData)) : undefined}
														data-col-idx={enableCellCopy && cell.column.id !== 'select' ? cellIdx - selectColOffset : undefined}
													>
														<FlexRender
															content={cell.column.columnDef.cell}
															context={cell.getContext()}
														/>
													</td>
												{/each}
											</tr>
										{/if}
									{/each}
								{/if}
							{/each}
						{/if}
						{#if groupedData.length === 0}
							<tr>
								<td colspan={finalColumns.length} class="px-2 py-8 text-center">
									<div class="flex flex-col items-center gap-1.5"><SearchXIcon class="size-6 text-muted-foreground/40" /><span class="text-sm text-muted-foreground">검색 결과가 없습니다</span></div>
								</td>
							</tr>
						{/if}
					{:else if table.getRowModel().rows.length}
						<!-- Flat View -->
						{#if virtualizer && scrollHeight}
							<!-- Virtual scrolling spacer (top) -->
							{#if virtualRows.length > 0}
								<tr style="height: {virtualRows[0].start}px">
									<td colspan={finalColumns.length + (onRowDrop ? 1 : 0) + (expandableRowContent ? 1 : 0)}></td>
								</tr>
							{/if}
							{#each virtualRows as virtualRow (virtualRow.index)}
								{@const windowedIdx = virtualRow.index - windowOffset}
								{@const row = table.getRowModel().rows[windowedIdx]}
								{#if row}
								{@const rowIdx = virtualRow.index}
								{@const isExpanded = expandedRows.has(row.id)}
								{@const isDraggable = onRowDrop != null && (!canDragRow || canDragRow(row.original))}
								<tr
									class="border-b border-border/40 transition-colors hover:bg-muted/30
										{(row.getIsSelected() || highlightSet.has(row.id)) ? 'bg-primary/5' : ''}
										{onRowDoubleClick ? 'cursor-pointer' : ''}"
									data-selected={row.getIsSelected() || undefined}
									tabindex={onRowDoubleClick ? 0 : undefined}
									ondblclick={() => onRowDoubleClick?.(row.original)}
									onkeydown={(e) => { if (e.key === 'Enter' && onRowDoubleClick) { e.preventDefault(); onRowDoubleClick(row.original); } }}
								>
									{#if onRowDrop}
										<td class="w-7 px-1 text-center">
											{#if isDraggable}
												<GripVertical class="size-3 text-muted-foreground cursor-grab inline-block" />
											{/if}
										</td>
									{/if}
									{#if expandableRowContent}
										<td class="w-7 px-1">
											{#if !canExpandRow || canExpandRow(row.original)}
												<button
													class="inline-flex items-center justify-center size-5 rounded hover:bg-muted transition-colors"
													onclick={(e) => { e.stopPropagation(); toggleExpand(row.id, row.original); }}
												>
													{#if isExpanded}
														<ChevronDown class="size-3" />
													{:else}
														<ChevronRight class="size-3" />
													{/if}
												</button>
											{/if}
										</td>
									{/if}
									{#each row.getVisibleCells() as cell, cellIdx (cell.id)}
										<td
											class="h-7 px-2 whitespace-nowrap transition-colors
												{cell.column.id === 'select' ? 'w-9 text-center' : ''}
												{enableCellCopy && (selectAll || selectedCells.has(cell.id)) ? 'ring-2 ring-primary/60 ring-inset bg-primary/5' : ''}
												{enableCellCopy && (copiedAll || copiedCells.has(cell.id)) ? 'bg-primary/20 text-primary' : ''}"
											data-cell-id={enableCellCopy ? cell.id : undefined}
											data-row-idx={enableCellCopy && cell.column.id !== 'select' ? rowIdx : undefined}
											data-col-idx={enableCellCopy && cell.column.id !== 'select' ? cellIdx - selectColOffset : undefined}
										>
											<FlexRender
												content={cell.column.columnDef.cell}
												context={cell.getContext()}
											/>
										</td>
									{/each}
								</tr>
								{#if expandableRowContent && isExpanded && (!canExpandRow || canExpandRow(row.original))}
									<tr class="border-b border-border/40 bg-muted/20">
										<td colspan={finalColumns.length + 1 + (onRowDrop != null ? 1 : 0)} class="px-3 py-2">
											{@render expandableRowContent({ row: row.original, expanded: isExpanded, toggle: () => toggleExpand(row.id) })}
										</td>
									</tr>
								{/if}
								{/if}
							{/each}
							<!-- Virtual scrolling spacer (bottom) -->
							{#if virtualRows.length > 0}
								<tr style="height: {totalVirtualHeight - virtualRows[virtualRows.length - 1].end}px">
									<td colspan={finalColumns.length + (onRowDrop ? 1 : 0) + (expandableRowContent ? 1 : 0)}></td>
								</tr>
							{/if}
						{:else}
						{#each table.getRowModel().rows as row, rowIdx (row.id)}
							{@const isExpanded = expandedRows.has(row.id)}
							{@const isDraggable = onRowDrop != null && (!canDragRow || canDragRow(row.original))}
							<tr
								class="border-b border-border/40 transition-colors hover:bg-muted/30
									{(row.getIsSelected() || highlightSet.has(row.id)) ? 'bg-primary/5' : ''}
									{dragRowIdx === rowIdx ? 'opacity-40' : ''}
									{dropTargetIdx === rowIdx ? 'border-t-2 !border-t-primary' : ''}
									{onRowDoubleClick ? 'cursor-pointer' : ''}"
								data-selected={row.getIsSelected() || undefined}
								tabindex={onRowDoubleClick ? 0 : undefined}
								draggable={isDraggable}
								ondblclick={() => onRowDoubleClick?.(row.original)}
								onkeydown={(e) => { if (e.key === 'Enter' && onRowDoubleClick) { e.preventDefault(); onRowDoubleClick(row.original); } }}
								ondragstart={(e) => {
									if (!isDraggable) return;
									dragRowIdx = rowIdx;
									e.dataTransfer!.effectAllowed = 'move';
								}}
								ondragover={(e) => {
									if (dragRowIdx === null || !onRowDrop) return;
									e.preventDefault();
									e.dataTransfer!.dropEffect = 'move';
									dropTargetIdx = rowIdx;
								}}
								ondragleave={() => {
									if (dropTargetIdx === rowIdx) dropTargetIdx = null;
								}}
								ondrop={(e) => {
									e.preventDefault();
									if (dragRowIdx !== null && dragRowIdx !== rowIdx && onRowDrop) {
										onRowDrop(dragRowIdx, rowIdx);
									}
									dragRowIdx = null;
									dropTargetIdx = null;
								}}
								ondragend={() => {
									dragRowIdx = null;
									dropTargetIdx = null;
								}}
							>
								{#if onRowDrop}
									<td class="w-7 px-1 text-center">
										{#if isDraggable}
											<GripVertical class="size-3 text-muted-foreground cursor-grab inline-block" />
										{/if}
									</td>
								{/if}
								{#if expandableRowContent}
									<td class="w-7 px-1">
										{#if !canExpandRow || canExpandRow(row.original)}
											<button
												class="inline-flex items-center justify-center size-5 rounded hover:bg-muted transition-colors"
												onclick={(e) => { e.stopPropagation(); toggleExpand(row.id, row.original); }}
											>
												{#if isExpanded}
													<ChevronDown class="size-3" />
												{:else}
													<ChevronRight class="size-3" />
												{/if}
											</button>
										{/if}
									</td>
								{/if}
								{#each row.getVisibleCells() as cell, cellIdx (cell.id)}
									<td
										class="h-7 px-2 whitespace-nowrap transition-colors
											{cell.column.id === 'select' ? 'w-9 text-center' : ''}
											{enableCellCopy && (selectAll || selectedCells.has(cell.id)) ? 'ring-2 ring-primary/60 ring-inset bg-primary/5' : ''}
											{enableCellCopy && (copiedAll || copiedCells.has(cell.id)) ? 'bg-primary/20 text-primary' : ''}"
										data-cell-id={enableCellCopy ? cell.id : undefined}
										data-row-idx={enableCellCopy && cell.column.id !== 'select' ? rowIdx : undefined}
										data-col-idx={enableCellCopy && cell.column.id !== 'select' ? cellIdx - selectColOffset : undefined}
									>
										<FlexRender
											content={cell.column.columnDef.cell}
											context={cell.getContext()}
										/>
									</td>
								{/each}
							</tr>
							{#if expandableRowContent && isExpanded && (!canExpandRow || canExpandRow(row.original))}
								<tr class="border-b border-border/40 bg-muted/20">
									<td colspan={finalColumns.length + 1 + (onRowDrop != null ? 1 : 0)} class="px-3 py-2">
										{@render expandableRowContent({ row: row.original, expanded: isExpanded, toggle: () => toggleExpand(row.id) })}
									</td>
								</tr>
							{/if}
						{/each}
						{/if}
					{:else}
						<tr>
							<td
								colspan={finalColumns.length}
								class="px-2 py-8 text-center"
							>
								<div class="flex flex-col items-center gap-1.5">
									<SearchXIcon class="size-6 text-muted-foreground/40" />
									<span class="text-sm text-muted-foreground">검색 결과가 없습니다</span>
								</div>
							</td>
						</tr>
					{/if}
				</tbody>
			</table>
		</div>
	</div>

	<!-- Pagination -->
	{#if showPagination && !scrollHeight && !(serverGroupBy && groupByKey && !serverGroupBy.selectedGroup)}
		<DataTablePagination {table} showSelectedCount={enableRowSelection} serverSide={serverSide} />
	{/if}
</div>
