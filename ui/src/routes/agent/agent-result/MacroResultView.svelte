<script lang="ts">
	import PerfChart from '$lib/components/perf-chart/PerfChart.svelte';
	import DataTableShell from '$lib/components/DataTableShell.svelte';
	import * as Tabs from '$lib/components/ui/tabs/index.js';
	import { type ColumnDef } from '@tanstack/table-core';

	import type { CycleStepMetrics } from './types.js';

	interface Props {
		/** prefix 제거된 step metrics */
		metrics: Record<string, number>;
		/** loop 있을 때 cycle별 데이터 */
		cycleMetrics?: CycleStepMetrics[];
	}

	let { metrics, cycleMetrics = [] }: Props = $props();

	// ── Score / Speed 분리 ──
	interface MetricEntry {
		key: string;       // 원본 키: seq_read_score
		label: string;     // 표시명: Seq Read
		value: number;
		type: 'score' | 'speed';
	}

	const LABEL_MAP: Record<string, string> = {
		seq_read: 'Seq Read',
		seq_write: 'Seq Write',
		random_access: 'Random Access',
		mixed_multi_random: 'Mixed Multi-Random',
		mixed_random: 'Mixed Random',
		ai_read: 'AI Read',
		ai_write: 'AI Write',
		multi_ai_read: 'Multi-AI Read',
		multi_ai_write: 'Multi-AI Write',
	};

	function keyToLabel(key: string): string {
		// seq_read_score → seq_read → Seq Read
		const base = key.replace(/_score$/, '').replace(/_speed_mbs$/, '').replace(/_read_speed_mbs$/, '').replace(/_write_speed_mbs$/, '');
		return LABEL_MAP[base] ?? base.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
	}

	function keyToSpeedLabel(key: string): string {
		if (key.includes('_read_speed_')) return keyToLabel(key) + ' Read';
		if (key.includes('_write_speed_')) return keyToLabel(key) + ' Write';
		return keyToLabel(key);
	}

	const scoreEntries = $derived.by<MetricEntry[]>(() => {
		return Object.entries(metrics)
			.filter(([k]) => k.endsWith('_score'))
			.filter(([k]) => !k.startsWith('저장소') && !k.startsWith('storage'))
			.map(([k, v]) => ({ key: k, label: keyToLabel(k), value: v, type: 'score' as const }))
			.sort((a, b) => b.value - a.value);
	});

	const speedEntries = $derived.by<MetricEntry[]>(() => {
		return Object.entries(metrics)
			.filter(([k]) => k.endsWith('_mbs'))
			.map(([k, v]) => ({ key: k, label: keyToSpeedLabel(k), value: v, type: 'speed' as const }))
			.sort((a, b) => b.value - a.value);
	});

	// 버전 + 총점 (키에 버전 문자열이 포함된 항목)
	const versionEntry = $derived.by(() => {
		const entry = Object.entries(metrics).find(([k]) =>
			/v\d+\.\d+/i.test(k) || k.includes('저장소') || k.includes('storage')
		);
		if (!entry) return null;
		// "저장소_테스트_v11.1.0_score" → "저장소 테스트 v11.1.0"
		const label = entry[0].replace(/_score$/, '').replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
		return { label, score: entry[1] };
	});

	const totalScore = $derived(
		versionEntry?.score ?? scoreEntries.reduce((sum, e) => sum + e.value, 0)
	);

	const hasLoop = $derived(cycleMetrics.length > 1);

	// ── Tab ──
	let activeTab = $state('score');

	// ── 단일 결과 (no loop): 막대 차트 ──
	function buildBarChart(entries: MetricEntry[], unit: string) {
		return {
			tooltip: { trigger: 'axis' as const, axisPointer: { type: 'shadow' as const } },
			xAxis: {
				type: 'category' as const,
				data: entries.map(e => e.label),
				axisLabel: { fontSize: 9, rotate: 30, interval: 0 }
			},
			yAxis: {
				type: 'value' as const,
				name: unit,
				nameTextStyle: { fontSize: 9 },
				axisLabel: { fontSize: 9 }
			},
			series: [{
				type: 'bar' as const,
				data: entries.map(e => ({
					value: e.value,
					itemStyle: { color: e.type === 'score' ? '#5470c6' : '#91cc75', borderRadius: [4, 4, 0, 0] }
				})),
				barMaxWidth: 40,
				label: {
					show: true, position: 'top' as const, fontSize: 9,
					formatter: (p: any) => p.value >= 1000 ? (p.value / 1000).toFixed(1) + 'K' : p.value.toFixed(1)
				}
			}],
			grid: { left: 60, right: 20, top: 30, bottom: 80 },
			dataZoom: [{ type: 'inside' as const }]
		};
	}

	// ── Loop 결과: key별 탭 + line/scatter 차트 ──
	let loopChartKey = $state('');

	const loopScoreKeys = $derived.by(() => {
		if (cycleMetrics.length === 0) return [];
		const keys = new Set<string>();
		for (const cm of cycleMetrics) {
			for (const k of Object.keys(cm.metrics)) {
				if (k.endsWith('_score') && !k.includes('저장소') && !k.includes('storage')) keys.add(k);
			}
		}
		return [...keys].sort();
	});

	const loopSpeedKeys = $derived.by(() => {
		if (cycleMetrics.length === 0) return [];
		const keys = new Set<string>();
		for (const cm of cycleMetrics) {
			for (const k of Object.keys(cm.metrics)) {
				if (k.endsWith('_mbs')) keys.add(k);
			}
		}
		return [...keys].sort();
	});

	$effect(() => {
		const keys = activeTab === 'score' ? loopScoreKeys : loopSpeedKeys;
		if (keys.length > 0 && !keys.includes(loopChartKey)) loopChartKey = keys[0];
	});

	function buildLoopChart(key: string) {
		const data = cycleMetrics.map(cm => ({ cycle: cm.cycle, value: cm.metrics[key] ?? 0 }));
		const unit = key.endsWith('_mbs') ? 'MB/s' : 'Score';
		return {
			tooltip: {
				trigger: 'axis' as const,
				formatter: (params: any) => {
					const p = Array.isArray(params) ? params[0] : params;
					return `Cycle ${p.name}<br/>${keyToLabel(key)}: <b>${p.value.toLocaleString()}</b> ${unit}`;
				}
			},
			xAxis: {
				type: 'category' as const,
				name: 'Cycle',
				data: data.map(d => String(d.cycle)),
				axisLabel: { fontSize: 9 }
			},
			yAxis: {
				type: 'value' as const,
				name: unit,
				nameTextStyle: { fontSize: 9 },
				axisLabel: { fontSize: 9 }
			},
			series: [{
				type: 'line' as const,
				data: data.map(d => d.value),
				smooth: true,
				symbolSize: 6,
				itemStyle: { color: key.endsWith('_mbs') ? '#91cc75' : '#5470c6' },
				areaStyle: { opacity: 0.08 }
			}],
			grid: { left: 70, right: 20, top: 30, bottom: 40 },
			dataZoom: [{ type: 'inside' as const }]
		};
	}

	// ── DataTable ──
	interface TableRow {
		label: string;
		[key: string]: string | number;
	}

	const scoreColumns = $derived.by<ColumnDef<TableRow>[]>(() => {
		const cols: ColumnDef<TableRow>[] = [{ accessorKey: 'label', header: '' }];
		for (const e of scoreEntries) {
			cols.push({ accessorKey: e.key, header: e.label });
		}
		return cols;
	});

	const speedColumns = $derived.by<ColumnDef<TableRow>[]>(() => {
		const cols: ColumnDef<TableRow>[] = [{ accessorKey: 'label', header: '' }];
		for (const e of speedEntries) {
			cols.push({ accessorKey: e.key, header: e.label });
		}
		return cols;
	});

	const scoreTableData = $derived.by<TableRow[]>(() => {
		if (hasLoop) {
			return cycleMetrics.map(cm => {
				const row: TableRow = { label: `Cycle ${cm.cycle}` };
				for (const e of scoreEntries) {
					row[e.key] = cm.metrics[e.key] ?? 0;
				}
				return row;
			});
		}
		const row: TableRow = { label: 'Result' };
		for (const e of scoreEntries) row[e.key] = e.value;
		return [row];
	});

	const speedTableData = $derived.by<TableRow[]>(() => {
		if (hasLoop) {
			return cycleMetrics.map(cm => {
				const row: TableRow = { label: `Cycle ${cm.cycle}` };
				for (const e of speedEntries) {
					row[e.key] = (cm.metrics[e.key] ?? 0).toFixed(1);
				}
				return row;
			});
		}
		const row: TableRow = { label: 'Result' };
		for (const e of speedEntries) row[e.key] = e.value.toFixed(1);
		return [row];
	});
</script>

<div class="space-y-4">
		<!-- 버전 + 총점 -->
		<div class="flex items-center gap-3 px-3 py-2 rounded-lg bg-primary/5 border border-primary/10">
			{#if versionEntry}
				<span class="text-[10px] text-muted-foreground font-medium px-2 py-0.5 rounded bg-muted">{versionEntry.label}</span>
			{/if}
			{#if totalScore > 0}
				<span class="text-xs text-muted-foreground">Total</span>
				<span class="text-2xl font-bold text-primary tabular-nums">{totalScore.toLocaleString()}</span>
			{/if}
		</div>

		<!-- Score / Speed 탭 -->
		<Tabs.Root bind:value={activeTab}>
			<Tabs.List class="w-fit">
				<Tabs.Trigger value="score" class="text-xs">Score</Tabs.Trigger>
				<Tabs.Trigger value="speed" class="text-xs">Speed (MB/s)</Tabs.Trigger>
			</Tabs.List>

			<Tabs.Content value="score" class="space-y-3 mt-2">
				{#if hasLoop}
					<!-- Loop: key별 탭 + line chart -->
					<div class="flex gap-1 flex-wrap">
						{#each loopScoreKeys as key}
							<button
								class="px-2 py-1 rounded text-[10px] border transition-colors
									{loopChartKey === key ? 'bg-primary text-primary-foreground' : 'hover:bg-muted'}"
								onclick={() => loopChartKey = key}
							>{keyToLabel(key)}</button>
						{/each}
					</div>
					{#if loopChartKey}
						<PerfChart option={buildLoopChart(loopChartKey)} height="280px" />
					{/if}
				{:else}
					<!-- 단일: 막대 차트 -->
					<PerfChart option={buildBarChart(scoreEntries, 'Score')} height="280px" />
				{/if}

				<!-- DataTable -->
				<DataTableShell
					data={scoreTableData}
					columns={scoreColumns}
					pageSize={20}
				/>
			</Tabs.Content>

			<Tabs.Content value="speed" class="space-y-3 mt-2">
				{#if hasLoop}
					<div class="flex gap-1 flex-wrap">
						{#each loopSpeedKeys as key}
							<button
								class="px-2 py-1 rounded text-[10px] border transition-colors
									{loopChartKey === key ? 'bg-primary text-primary-foreground' : 'hover:bg-muted'}"
								onclick={() => loopChartKey = key}
							>{keyToSpeedLabel(key)}</button>
						{/each}
					</div>
					{#if loopChartKey}
						<PerfChart option={buildLoopChart(loopChartKey)} height="280px" />
					{/if}
				{:else}
					<PerfChart option={buildBarChart(speedEntries, 'MB/s')} height="280px" />
				{/if}

				<DataTableShell
					data={speedTableData}
					columns={speedColumns}
					pageSize={20}
				/>
			</Tabs.Content>
		</Tabs.Root>
</div>
