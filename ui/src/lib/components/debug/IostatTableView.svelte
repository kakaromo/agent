<script lang="ts">
	// iostat_info 전용 뷰.
	// entries: 여러 시점 수집된 iostat JSON 배열
	//   각 entry = { time, WRITE: { metric: { io_bytes, count, avg_bytes } }, READ: {...}, OTHER: {...} }
	// UX:
	//   - 섹션 필터 (WRITE/READ/OTHER 다중 토글)
	//   - metric 검색 + 체크박스 다중 선택
	//   - 최신 스냅샷 표
	//   - io_bytes / count / avg_bytes 컬럼별 3개 라인 차트 (선택된 section·metric이 시리즈)
	//   - 델타 / 누적 모드 토글

	import PerfChart from '$lib/components/perf-chart/PerfChart.svelte';
	import type { EChartsOption } from 'echarts';
	import SearchIcon from '@lucide/svelte/icons/search';
	import XIcon from '@lucide/svelte/icons/x';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import { toast } from 'svelte-sonner';

	interface Props {
		entries: Array<Record<string, any>>;
		fileLabel?: string;
	}
	let { entries, fileLabel = 'iostat' }: Props = $props();

	const STORAGE_KEY = 'iostat.selectedMetrics.v1';

	const latest = $derived<Record<string, any> | null>(entries?.length ? entries[entries.length - 1] : null);

	// 최신 snapshot에서 섹션/컬럼/metric 목록 추출
	interface Snapshot {
		sections: string[]; // 예: [WRITE, READ, OTHER]
		columns: string[]; // 예: [io_bytes, count, avg_bytes]
		metricsBySection: Record<string, string[]>;
	}

	const snapshot = $derived<Snapshot>(buildSnapshot(latest));

	function buildSnapshot(obj: Record<string, any> | null): Snapshot {
		const sections: string[] = [];
		const columnsSet = new Set<string>();
		const metricsBySection: Record<string, string[]> = {};
		if (!obj) return { sections, columns: [], metricsBySection };
		for (const [key, val] of Object.entries(obj)) {
			if (key === 'time' || key === 'path' || key === 'format') continue;
			if (val === null || typeof val !== 'object' || Array.isArray(val)) continue;
			const firstChild = Object.values(val)[0];
			if (firstChild === null || typeof firstChild !== 'object' || Array.isArray(firstChild)) continue;

			const metrics: string[] = [];
			for (const [metric, row] of Object.entries(val as Record<string, any>)) {
				if (!row || typeof row !== 'object') continue;
				metrics.push(metric);
				for (const c of Object.keys(row)) columnsSet.add(c);
			}
			sections.push(key);
			metricsBySection[key] = metrics;
		}
		return { sections, columns: [...columnsSet], metricsBySection };
	}

	// 상태
	let activeSections = $state<Set<string>>(new Set()); // 비어있으면 "전체 허용"
	let selectedMetrics = $state<Set<string>>(new Set()); // key = "SECTION.metric"
	let metricQuery = $state('');
	let deltaMode = $state(true);
	let tableCollapsed = $state(false);

	// localStorage 복원
	$effect(() => {
		if (typeof localStorage === 'undefined') return;
		try {
			const raw = localStorage.getItem(STORAGE_KEY);
			if (!raw) return;
			const parsed = JSON.parse(raw) as { sections?: string[]; metrics?: string[]; delta?: boolean };
			if (Array.isArray(parsed.sections)) activeSections = new Set(parsed.sections);
			if (Array.isArray(parsed.metrics)) selectedMetrics = new Set(parsed.metrics);
			if (typeof parsed.delta === 'boolean') deltaMode = parsed.delta;
		} catch { /* ignore */ }
	});

	function persist() {
		if (typeof localStorage === 'undefined') return;
		try {
			localStorage.setItem(STORAGE_KEY, JSON.stringify({
				sections: [...activeSections],
				metrics: [...selectedMetrics],
				delta: deltaMode
			}));
		} catch { /* ignore */ }
	}

	function toggleSection(s: string) {
		const next = new Set(activeSections);
		if (next.has(s)) next.delete(s);
		else next.add(s);
		activeSections = next;
		persist();
	}

	function isSectionActive(s: string): boolean {
		// 비어있으면 모든 섹션 활성으로 간주 (필터 OFF)
		return activeSections.size === 0 || activeSections.has(s);
	}

	function toggleMetric(section: string, metric: string) {
		const key = `${section}.${metric}`;
		const next = new Set(selectedMetrics);
		if (next.has(key)) next.delete(key);
		else next.add(key);
		selectedMetrics = next;
		persist();
	}

	function setDelta(v: boolean) {
		deltaMode = v;
		persist();
	}

	function clearSelection() {
		selectedMetrics = new Set();
		persist();
	}

	function selectAllVisible() {
		const next = new Set(selectedMetrics);
		for (const row of filteredRows) next.add(`${row.section}.${row.metric}`);
		selectedMetrics = next;
		persist();
	}

	// 필터된 (section, metric) 행 목록 — 표 + 체크박스 공용
	interface MetricRow {
		section: string;
		metric: string;
		values: Record<string, unknown>; // 최신 값
	}

	const filteredRows = $derived.by<MetricRow[]>(() => {
		const q = metricQuery.trim().toLowerCase();
		const out: MetricRow[] = [];
		for (const section of snapshot.sections) {
			if (!isSectionActive(section)) continue;
			const metrics = snapshot.metricsBySection[section] ?? [];
			for (const metric of metrics) {
				if (q && !metric.toLowerCase().includes(q) && !section.toLowerCase().includes(q)) continue;
				const values = (latest?.[section]?.[metric] ?? {}) as Record<string, unknown>;
				out.push({ section, metric, values });
			}
		}
		return out;
	});

	function fmt(n: unknown): string {
		if (typeof n !== 'number' || !Number.isFinite(n)) return '—';
		return n.toLocaleString();
	}

	const timestamp = $derived(typeof latest?.time === 'number' ? new Date(latest.time * 1000) : null);

	// 시계열 계산 — 차트용
	function buildChartOption(column: string): EChartsOption | null {
		if (!entries || entries.length === 0) return null;
		if (selectedMetrics.size === 0) return null;

		const baseTime = typeof entries[0]?.time === 'number' ? entries[0].time : 0;
		const sorted = [...entries].sort((a, b) => (a.time ?? 0) - (b.time ?? 0));

		const series: any[] = [];
		for (const key of selectedMetrics) {
			const dot = key.indexOf('.');
			if (dot < 0) continue;
			const section = key.substring(0, dot);
			const metric = key.substring(dot + 1);
			if (!isSectionActive(section)) continue;

			const pts: Array<[number, number]> = [];
			let prev: number | null = null;
			for (const e of sorted) {
				const t = typeof e.time === 'number' ? e.time : null;
				const v = e?.[section]?.[metric]?.[column];
				if (t == null || typeof v !== 'number' || !Number.isFinite(v)) continue;
				const y = deltaMode ? (prev == null ? 0 : v - prev) : v;
				pts.push([t - baseTime, y]);
				prev = v;
			}
			if (pts.length === 0) continue;
			series.push({
				name: `${section} · ${metric}`,
				type: 'line' as const,
				data: pts,
				showSymbol: pts.length < 50,
				smooth: false,
				areaStyle: deltaMode ? { opacity: 0.08 } : undefined
			});
		}

		if (series.length === 0) return null;

		return {
			title: {
				text: deltaMode ? `Δ ${column}` : column,
				left: 'center',
				textStyle: { fontSize: 12 }
			},
			tooltip: {
				trigger: 'axis',
				formatter: (params: any) => {
					const arr = Array.isArray(params) ? params : [params];
					if (arr.length === 0) return '';
					const dt = arr[0].value?.[0] ?? 0;
					const abs = new Date((baseTime + dt) * 1000).toLocaleTimeString();
					const lines = arr
						.map((p: any) => `${p.marker} ${p.seriesName}: <b>${(p.value?.[1] ?? 0).toLocaleString()}</b>`)
						.join('<br/>');
					return `${abs}<br/>${lines}`;
				}
			},
			legend: {
				bottom: 0,
				type: 'scroll',
				textStyle: { fontSize: 10 }
			},
			grid: { left: 70, right: 20, top: 36, bottom: series.length > 1 ? 50 : 30 },
			xAxis: {
				type: 'value' as const,
				name: 'Time (s)',
				nameLocation: 'middle' as const,
				nameGap: 22,
				nameTextStyle: { fontSize: 10 }
			},
			yAxis: {
				type: 'value' as const,
				name: column,
				nameLocation: 'middle' as const,
				nameGap: 55,
				nameTextStyle: { fontSize: 10 }
			},
			dataZoom: [{ type: 'inside' as const }],
			series
		};
	}

	const chartOptions = $derived(snapshot.columns.map((c) => ({ column: c, option: buildChartOption(c) })));
	const hasMultipleEntries = $derived(entries.length > 1);

	let exporting = $state(false);

	async function exportExcel() {
		if (!latest) return;
		exporting = true;
		try {
			const { exportToExcel } = await import('$lib/utils/excel-export');

			// 스냅샷 시트 — 모든 (section, metric) 행을 columns 포함해 덤프
			const snapshotHeaders = ['section', 'metric', ...snapshot.columns];
			const snapshotRows: (string | number)[][] = [];
			for (const section of snapshot.sections) {
				const metrics = snapshot.metricsBySection[section] ?? [];
				for (const metric of metrics) {
					const row = (latest[section]?.[metric] ?? {}) as Record<string, unknown>;
					snapshotRows.push([
						section,
						metric,
						...snapshot.columns.map((c) => {
							const v = row[c];
							return typeof v === 'number' ? v : (v == null ? '' : String(v));
						})
					]);
				}
			}

			// 시계열 시트 — 선택된 metric이 있을 때만
			const sorted = [...entries].sort((a, b) => (a.time ?? 0) - (b.time ?? 0));
			const selectedList = [...selectedMetrics]
				.map((key) => {
					const dot = key.indexOf('.');
					if (dot < 0) return null;
					return { section: key.substring(0, dot), metric: key.substring(dot + 1), key };
				})
				.filter((x): x is { section: string; metric: string; key: string } => x !== null)
				.filter((s) => isSectionActive(s.section));

			const timeSheets: Array<{ name: string; sections: any[] }> = [];
			if (selectedList.length > 0 && hasMultipleEntries) {
				for (const column of snapshot.columns) {
					// 각 시점 행: [timestamp, s1.m1, s1.m2, ...]
					const headers = ['timestamp', ...selectedList.map((s) => `${s.section}.${s.metric}`)];
					const raw: (string | number)[][] = [];
					const delta: (string | number)[][] = [];
					const prev = new Map<string, number>();
					for (const e of sorted) {
						const t = typeof e.time === 'number' ? new Date(e.time * 1000).toISOString() : '';
						const rawRow: (string | number)[] = [t];
						const deltaRow: (string | number)[] = [t];
						for (const s of selectedList) {
							const v = e?.[s.section]?.[s.metric]?.[column];
							const num = typeof v === 'number' && Number.isFinite(v) ? v : null;
							rawRow.push(num ?? '');
							if (num == null) deltaRow.push('');
							else {
								const p = prev.get(s.key);
								deltaRow.push(p == null ? 0 : num - p);
								prev.set(s.key, num);
							}
						}
						raw.push(rawRow);
						delta.push(deltaRow);
					}
					timeSheets.push({
						name: column.slice(0, 27), // reserve ' Δ/Raw' 공간
						sections: [
							{ type: 'table' as const, title: `${column} (raw)`, headers, rows: raw },
							{ type: 'table' as const, title: `${column} (Δ delta)`, headers, rows: delta }
						]
					});
				}
			}

			const snapshotTs = typeof latest.time === 'number' ? new Date(latest.time * 1000) : new Date();
			const tsStr = snapshotTs.toISOString().replace(/[:.]/g, '-').slice(0, 19);

			await exportToExcel({
				fileName: `${fileLabel}_${tsStr}.xlsx`,
				sheets: [
					{
						name: 'Snapshot',
						sections: [
							{
								type: 'table' as const,
								title: `Snapshot @ ${snapshotTs.toLocaleString()}`,
								headers: snapshotHeaders,
								rows: snapshotRows
							}
						]
					},
					...timeSheets
				]
			});
			toast.success('Excel 다운로드 완료');
		} catch (e: any) {
			toast.error('Excel 내보내기 실패: ' + (e?.message ?? 'unknown'));
		} finally {
			exporting = false;
		}
	}
</script>

<div class="space-y-3">
	{#if !latest}
		<div class="text-center py-8 text-muted-foreground text-sm">데이터 없음</div>
	{:else}
		<!-- 메타 정보 바 -->
		<div class="flex items-center justify-between flex-wrap gap-2 text-[11px] text-muted-foreground">
			<div>
				{#if timestamp}
					Snapshot: <span class="font-mono">{timestamp.toLocaleString()}</span>
				{/if}
				{#if entries.length > 1}
					· {entries.length}개 시점
				{/if}
			</div>
			<div class="flex items-center gap-2">
				{#if hasMultipleEntries}
					<span>모드:</span>
					<div class="flex rounded-md border overflow-hidden">
						<button
							class="px-2 py-0.5 text-[10px] transition-colors {deltaMode ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground'}"
							onclick={() => setDelta(true)}
						>Δ 델타</button>
						<button
							class="px-2 py-0.5 text-[10px] transition-colors border-l {!deltaMode ? 'bg-primary text-primary-foreground' : 'hover:bg-muted text-muted-foreground'}"
							onclick={() => setDelta(false)}
						>누적</button>
					</div>
				{/if}
				<button
					class="inline-flex items-center gap-1 px-2 py-0.5 text-[10px] rounded-md border border-border hover:bg-muted text-muted-foreground disabled:opacity-50"
					onclick={exportExcel}
					disabled={exporting || !latest}
					title="Excel 내보내기"
				>
					<DownloadIcon class="size-3" />
					{exporting ? '생성 중...' : 'Excel'}
				</button>
			</div>
		</div>

		<!-- 섹션 필터 -->
		<div class="flex items-center gap-2 flex-wrap">
			<span class="text-[11px] text-muted-foreground">섹션:</span>
			{#each snapshot.sections as s (s)}
				{@const active = isSectionActive(s)}
				<button
					class="px-2 py-0.5 text-[10px] rounded border transition-colors {active ? 'bg-primary/10 text-primary border-primary/40' : 'border-border text-muted-foreground hover:bg-muted'}"
					onclick={() => toggleSection(s)}
				>
					{s}
				</button>
			{/each}
			{#if activeSections.size > 0}
				<button
					class="text-[10px] text-muted-foreground hover:text-foreground px-1"
					onclick={() => { activeSections = new Set(); persist(); }}
				>
					필터 해제
				</button>
			{/if}

			<div class="flex items-center gap-1.5 flex-1 min-w-[220px] h-7 rounded-md border border-border bg-background px-2 focus-within:ring-2 focus-within:ring-primary/40">
				<SearchIcon class="size-3.5 text-muted-foreground shrink-0" />
				<input
					type="text"
					class="flex-1 min-w-0 h-full text-[11px] bg-transparent placeholder:text-muted-foreground focus:outline-none"
					placeholder="metric 검색"
					bind:value={metricQuery}
				/>
				{#if metricQuery}
					<button
						class="inline-flex items-center justify-center size-4 rounded text-muted-foreground hover:text-foreground hover:bg-muted shrink-0"
						onclick={() => (metricQuery = '')}
						aria-label="검색어 지우기"
					>
						<XIcon class="size-3" />
					</button>
				{/if}
			</div>

			<span class="text-[10px] text-muted-foreground whitespace-nowrap">
				{selectedMetrics.size} 선택
			</span>
			<button
				class="px-2 py-0.5 text-[10px] rounded border border-border text-muted-foreground hover:bg-muted"
				onclick={selectAllVisible}
				disabled={filteredRows.length === 0}
			>보이는 것 전체 선택</button>
			{#if selectedMetrics.size > 0}
				<button
					class="px-2 py-0.5 text-[10px] text-muted-foreground hover:text-destructive"
					onclick={clearSelection}
				>선택 해제</button>
			{/if}
		</div>

		<!-- 표 (접기/펼치기) -->
		<div class="rounded border bg-card overflow-hidden">
			<button
				class="w-full flex items-center gap-2 px-2 py-1.5 bg-muted/40 hover:bg-muted/60 transition-colors"
				onclick={() => (tableCollapsed = !tableCollapsed)}
				aria-expanded={!tableCollapsed}
			>
				<span class="text-[11px] font-medium">최신 스냅샷 ({filteredRows.length} rows)</span>
				<span class="ml-auto text-[10px] text-muted-foreground">{tableCollapsed ? '펼치기' : '접기'}</span>
			</button>
			{#if !tableCollapsed}
				<div class="overflow-x-auto max-h-[40vh] overflow-y-auto">
					<table class="w-full text-[11px]">
						<thead class="bg-muted/20 text-muted-foreground sticky top-0">
							<tr>
								<th class="w-8 px-2 py-1"></th>
								<th class="text-left px-2 py-1 font-medium">section</th>
								<th class="text-left px-2 py-1 font-medium">metric</th>
								{#each snapshot.columns as col (col)}
									<th class="text-right px-2 py-1 font-medium tabular-nums">{col}</th>
								{/each}
							</tr>
						</thead>
						<tbody>
							{#each filteredRows as row (row.section + '.' + row.metric)}
								{@const key = `${row.section}.${row.metric}`}
								{@const checked = selectedMetrics.has(key)}
								<tr
									class="border-t border-border/40 cursor-pointer hover:bg-primary/5 {checked ? 'bg-primary/5' : ''}"
									onclick={() => toggleMetric(row.section, row.metric)}
								>
									<td class="px-2 py-1 text-center">
										<input
											type="checkbox"
											class="size-3"
											{checked}
											onclick={(e) => { e.stopPropagation(); toggleMetric(row.section, row.metric); }}
										/>
									</td>
									<td class="px-2 py-1 text-[10px] text-muted-foreground uppercase tracking-wide">{row.section}</td>
									<td class="px-2 py-1 font-mono text-foreground">{row.metric}</td>
									{#each snapshot.columns as col (col)}
										<td class="text-right px-2 py-1 tabular-nums">{fmt(row.values[col])}</td>
									{/each}
								</tr>
							{/each}
							{#if filteredRows.length === 0}
								<tr>
									<td colspan={3 + snapshot.columns.length} class="text-center py-4 text-muted-foreground">
										일치하는 metric이 없습니다
									</td>
								</tr>
							{/if}
						</tbody>
					</table>
				</div>
			{/if}
		</div>

		<!-- 컬럼별 차트 3개 -->
		{#if hasMultipleEntries}
			<div class="grid grid-cols-1 xl:grid-cols-3 gap-3">
				{#each chartOptions as co (co.column)}
					<div class="rounded border bg-card p-2">
						{#if co.option}
							<div class="resize-y overflow-hidden" style="height: 260px; min-height: 180px;">
								<PerfChart option={co.option} height="100%" />
							</div>
						{:else}
							<div class="flex items-center justify-center h-[200px] text-[11px] text-muted-foreground text-center">
								<div>
									<div class="text-sm font-medium text-foreground mb-0.5">{co.column}</div>
									<div>metric 선택 시 시계열 표시</div>
								</div>
							</div>
						{/if}
					</div>
				{/each}
			</div>
		{:else}
			<div class="text-[10px] text-muted-foreground text-center py-2">
				수집 시점이 1개뿐이라 시계열 차트는 표시되지 않습니다.
			</div>
		{/if}
	{/if}
</div>
