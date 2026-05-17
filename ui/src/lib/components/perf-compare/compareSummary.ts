/**
 * compareSummary.ts — 모든 파서 데이터에서 핵심 비교 지표를 추출하는 유틸리티.
 *
 * 토스 철학: "숫자는 비교 가능하게" — 파서 종류에 관계없이
 * 일관된 형태의 summary metrics를 뽑아 delta 비교가 즉시 가능하도록 한다.
 */

// ── Types ──

export interface SummaryMetric {
	/** 지표 식별 키 (e.g., "read_avg", "write_max") */
	key: string;
	/** 표시 라벨 (e.g., "Read Avg", "Write Max") */
	label: string;
	/** 단위 (e.g., "IOPS", "MB/s", "ms", "us", "") */
	unit: string;
	/** 값 — 숫자 또는 null(데이터 없음) */
	value: number | null;
	/** 값이 클수록 좋은지 (true: 높을수록 좋음, false: 낮을수록 좋음) */
	higherIsBetter: boolean;
}

// ── Internal helpers ──

interface CycleEntryBasic {
	cycle: number;
	avg: number;
	min: number;
	max: number;
	data?: number[];
	[key: string]: any;
}

function isCycleEntryBasic(v: unknown): v is CycleEntryBasic {
	if (!v || typeof v !== 'object') return false;
	const o = v as Record<string, unknown>;
	return typeof o.cycle === 'number' && typeof o.avg === 'number';
}

function isCycleArray(v: unknown): v is CycleEntryBasic[] {
	return Array.isArray(v) && v.length > 0 && isCycleEntryBasic(v[0]);
}

/** tcName에서 단위 추론 */
function inferUnit(tcName: string): string {
	if (/rand/i.test(tcName)) return 'IOPS';
	if (/seq/i.test(tcName)) return 'MB/s';
	if (/latency/i.test(tcName)) return 'us';
	if (/flush/i.test(tcName)) return 'MB/s';
	return 'Value';
}

/** 성능 수치인지 latency 수치인지 — latency는 낮을수록 좋음 */
function isLatency(tcName: string): boolean {
	return /latency/i.test(tcName);
}

// ── Per-parser extractors ──

/** GenPerf (2, 3, 16): Record<string, CycleEntry[]> — read/write/flushtime */
function extractGenPerf(data: unknown, tcName: string): SummaryMetric[] {
	if (!data || typeof data !== 'object' || Array.isArray(data)) return [];
	const metrics: SummaryMetric[] = [];
	const unit = inferUnit(tcName);
	const hib = !isLatency(tcName);

	for (const [key, value] of Object.entries(data as Record<string, unknown>)) {
		if (!isCycleArray(value)) continue;
		const cycles = value as CycleEntryBasic[];
		const avgOfAvgs = cycles.reduce((s, c) => s + c.avg, 0) / cycles.length;
		const worstMin = Math.min(...cycles.map((c) => c.min));
		const bestMax = Math.max(...cycles.map((c) => c.max));
		const label = key.charAt(0).toUpperCase() + key.slice(1);

		metrics.push({ key: `${key}_avg`, label: `${label} Avg`, unit, value: avgOfAvgs, higherIsBetter: hib });
		metrics.push({ key: `${key}_min`, label: `${label} Min`, unit, value: worstMin, higherIsBetter: hib });
		metrics.push({ key: `${key}_max`, label: `${label} Max`, unit, value: bestMax, higherIsBetter: hib });
	}
	return metrics;
}

/** FragmentWrite (4): CycleEntry[] with 99th, std */
function extractFragmentWrite(data: unknown, tcName: string): SummaryMetric[] {
	if (!isCycleArray(data)) return [];
	const cycles = data as CycleEntryBasic[];
	const unit = inferUnit(tcName);
	const avgOfAvgs = cycles.reduce((s, c) => s + c.avg, 0) / cycles.length;
	const avg99th = cycles.reduce((s, c) => s + (c['99th'] ?? 0), 0) / cycles.length;

	return [
		{ key: 'avg', label: 'Avg', unit, value: avgOfAvgs, higherIsBetter: !isLatency(tcName) },
		{ key: '99th', label: '99th Percentile', unit, value: avg99th, higherIsBetter: false }
	];
}

/** KernelLatency (20): CycleEntry[] with Write/Read/Unmap → dirty/sustain/total → LatencyStats */
function extractKernelLatency(data: unknown): SummaryMetric[] {
	if (!Array.isArray(data) || data.length === 0) return [];
	const metrics: SummaryMetric[] = [];
	const ops = ['Write', 'Read', 'Unmap'] as const;
	const phases = ['total'] as const; // total이 가장 의미 있음

	for (const op of ops) {
		for (const phase of phases) {
			const values: number[] = [];
			for (const cycle of data) {
				const stats = cycle?.[op]?.[phase];
				if (stats && typeof stats.avg === 'number') {
					values.push(stats.avg);
				}
			}
			if (values.length > 0) {
				const avg = values.reduce((s, v) => s + v, 0) / values.length;
				metrics.push({
					key: `${op.toLowerCase()}_${phase}_avg`,
					label: `${op} Avg`,
					unit: 'us',
					value: avg,
					higherIsBetter: false
				});
			}
		}
	}
	return metrics;
}

/** WearLeveling (10): { write: number[], min_ec: number[], max_ec: number[] } */
function extractWearLeveling(data: unknown): SummaryMetric[] {
	if (!data || typeof data !== 'object') return [];
	const d = data as Record<string, number[]>;
	const metrics: SummaryMetric[] = [];

	if (Array.isArray(d.write) && d.write.length > 0) {
		const lastWrite = d.write[d.write.length - 1];
		const avgWrite = d.write.reduce((s, v) => s + v, 0) / d.write.length;
		metrics.push({ key: 'write_last', label: 'Write (final)', unit: 'MB/s', value: lastWrite, higherIsBetter: true });
		metrics.push({ key: 'write_avg', label: 'Write Avg', unit: 'MB/s', value: avgWrite, higherIsBetter: true });
	}
	if (Array.isArray(d.min_ec) && Array.isArray(d.max_ec) && d.min_ec.length > 0) {
		const lastDelta = d.max_ec[d.max_ec.length - 1] - d.min_ec[d.min_ec.length - 1];
		metrics.push({ key: 'ec_delta', label: 'EC Delta (final)', unit: '', value: lastDelta, higherIsBetter: false });
	}
	return metrics;
}

/** LongTermTC (15): CycleEntry[] with W1-W3, R1-R3, Write_Arg, After_W3_FB */
function extractLongTermTC(data: unknown, tcName: string): SummaryMetric[] {
	if (!Array.isArray(data) || data.length === 0) return [];
	const unit = inferUnit(tcName);
	const hib = !isLatency(tcName);
	const metrics: SummaryMetric[] = [];
	const fields = ['W1', 'W2', 'W3', 'R1', 'R2', 'R3'] as const;

	for (const field of fields) {
		const values = data.map((c: any) => c[field]).filter((v: any) => typeof v === 'number');
		if (values.length > 0) {
			const avg = values.reduce((s: number, v: number) => s + v, 0) / values.length;
			metrics.push({ key: field.toLowerCase(), label: field, unit, value: avg, higherIsBetter: hib });
		}
	}
	return metrics;
}

/** PerfByChunk (5): { write: ChunkEntry[], read: ChunkEntry[] } */
function extractPerfByChunk(data: unknown, tcName: string): SummaryMetric[] {
	if (!data || typeof data !== 'object') return [];
	const d = data as Record<string, any[]>;
	const unit = inferUnit(tcName);
	const hib = !isLatency(tcName);
	const metrics: SummaryMetric[] = [];

	for (const rw of ['read', 'write'] as const) {
		const entries = d[rw];
		if (!Array.isArray(entries) || entries.length === 0) continue;
		// ChunkEntry: { cycle, ...chunkSizes } — 각 chunkSize는 숫자
		const allValues: number[] = [];
		for (const entry of entries) {
			for (const [k, v] of Object.entries(entry)) {
				if (k !== 'cycle' && typeof v === 'number') allValues.push(v);
			}
		}
		if (allValues.length > 0) {
			const avg = allValues.reduce((s, v) => s + v, 0) / allValues.length;
			const label = rw.charAt(0).toUpperCase() + rw.slice(1);
			metrics.push({ key: `${rw}_avg`, label: `${label} Avg`, unit, value: avg, higherIsBetter: hib });
		}
	}
	return metrics;
}

/** UnmapThroughput (23): CycleEntry[] with fragmented/unfragmented */
function extractUnmapThroughput(data: unknown): SummaryMetric[] {
	if (!Array.isArray(data) || data.length === 0) return [];
	const metrics: SummaryMetric[] = [];
	const fragValues = data.map((c: any) => c.fragmented).filter((v: any) => typeof v === 'number');
	const unfragValues = data.map((c: any) => c.unfragmented).filter((v: any) => typeof v === 'number');

	if (fragValues.length > 0) {
		const avg = fragValues.reduce((s: number, v: number) => s + v, 0) / fragValues.length;
		metrics.push({ key: 'frag_avg', label: 'Fragmented Avg', unit: 'MB/s', value: avg, higherIsBetter: true });
	}
	if (unfragValues.length > 0) {
		const avg = unfragValues.reduce((s: number, v: number) => s + v, 0) / unfragValues.length;
		metrics.push({ key: 'unfrag_avg', label: 'Unfragmented Avg', unit: 'MB/s', value: avg, higherIsBetter: true });
	}
	return metrics;
}

/** Generic CycleEntry[] with data[] — IntervaledReadLatency(6), etc. */
function extractCycleArraySimple(data: unknown, tcName: string): SummaryMetric[] {
	if (!isCycleArray(data)) return [];
	const cycles = data as CycleEntryBasic[];
	const unit = inferUnit(tcName);
	const hib = !isLatency(tcName);
	const avgOfAvgs = cycles.reduce((s, c) => s + c.avg, 0) / cycles.length;
	const worstMin = Math.min(...cycles.map((c) => c.min));
	const bestMax = Math.max(...cycles.map((c) => c.max));

	return [
		{ key: 'avg', label: 'Avg', unit, value: avgOfAvgs, higherIsBetter: hib },
		{ key: 'min', label: 'Min', unit, value: worstMin, higherIsBetter: hib },
		{ key: 'max', label: 'Max', unit, value: bestMax, higherIsBetter: hib }
	];
}

/** Generic Record<string, CycleEntry[]> — VluRandReadPerThread(26), WbFlushThroughput(29) */
function extractRecordCycleArray(data: unknown, tcName: string): SummaryMetric[] {
	if (!data || typeof data !== 'object' || Array.isArray(data)) return [];
	const metrics: SummaryMetric[] = [];
	const unit = inferUnit(tcName);
	const hib = !isLatency(tcName);

	for (const [key, value] of Object.entries(data as Record<string, unknown>)) {
		if (!isCycleArray(value)) continue;
		const cycles = value as CycleEntryBasic[];
		const avgOfAvgs = cycles.reduce((s, c) => s + c.avg, 0) / cycles.length;
		const label = key;
		metrics.push({ key: `${key}_avg`, label: `${label} Avg`, unit, value: avgOfAvgs, higherIsBetter: hib });
	}
	return metrics;
}

/** VluDirtyCase4 (25,28): Record<string, VluSizeEntry[]> — nested read/write CycleEntry[] */
function extractVluDirtyCase4(data: unknown, tcName: string): SummaryMetric[] {
	if (!data || typeof data !== 'object' || Array.isArray(data)) return [];
	const metrics: SummaryMetric[] = [];
	const unit = inferUnit(tcName);
	const hib = !isLatency(tcName);

	for (const [sizeKey, entries] of Object.entries(data as Record<string, any[]>)) {
		if (!Array.isArray(entries)) continue;
		for (const entry of entries) {
			for (const rw of ['read', 'write'] as const) {
				if (!isCycleArray(entry?.[rw])) continue;
				const cycles = entry[rw] as CycleEntryBasic[];
				const avgOfAvgs = cycles.reduce((s: number, c: CycleEntryBasic) => s + c.avg, 0) / cycles.length;
				const label = `${sizeKey} ${rw.charAt(0).toUpperCase() + rw.slice(1)}`;
				metrics.push({ key: `${sizeKey}_${rw}_avg`, label: `${label} Avg`, unit, value: avgOfAvgs, higherIsBetter: hib });
			}
		}
	}
	return metrics;
}

/** VluLatency (24): CycleEntry[] with elu/nlu → op → OpStats */
function extractVluLatency(data: unknown): SummaryMetric[] {
	if (!Array.isArray(data) || data.length === 0) return [];
	const metrics: SummaryMetric[] = [];
	const units = ['elu', 'nlu'] as const;

	for (const unitType of units) {
		const allAvgs: number[] = [];
		for (const cycle of data) {
			const branch = cycle?.[unitType];
			if (!branch || typeof branch !== 'object') continue;
			for (const [, stats] of Object.entries(branch as Record<string, any>)) {
				if (stats && typeof stats.avg === 'number') {
					allAvgs.push(stats.avg);
				}
			}
		}
		if (allAvgs.length > 0) {
			const avg = allAvgs.reduce((s, v) => s + v, 0) / allAvgs.length;
			metrics.push({
				key: `${unitType}_avg`,
				label: `${unitType.toUpperCase()} Avg`,
				unit: 'us',
				value: avg,
				higherIsBetter: false
			});
		}
	}
	return metrics;
}

/** WideRandRead (27): Record<string, CycleEntry[]> with nested rangedata → chunkdata */
function extractWideRandRead(data: unknown, tcName: string): SummaryMetric[] {
	if (!data || typeof data !== 'object' || Array.isArray(data)) return [];
	const metrics: SummaryMetric[] = [];
	const unit = inferUnit(tcName);
	const hib = !isLatency(tcName);

	for (const [key, cycles] of Object.entries(data as Record<string, any[]>)) {
		if (!Array.isArray(cycles) || cycles.length === 0) continue;
		const allPerfs: number[] = [];
		for (const cycle of cycles) {
			if (!Array.isArray(cycle?.rangedata)) continue;
			for (const range of cycle.rangedata) {
				if (!Array.isArray(range?.chunkdata)) continue;
				for (const chunk of range.chunkdata) {
					if (typeof chunk?.perf === 'number') allPerfs.push(chunk.perf);
				}
			}
		}
		if (allPerfs.length > 0) {
			const avg = allPerfs.reduce((s, v) => s + v, 0) / allPerfs.length;
			metrics.push({ key: `${key}_avg`, label: `${key} Avg`, unit, value: avg, higherIsBetter: hib });
		}
	}
	return metrics;
}

/** WriteAndDelete (21): CycleEntry[] with nested FileSizeEntry[] */
function extractWriteAndDelete(data: unknown, tcName: string): SummaryMetric[] {
	if (!Array.isArray(data) || data.length === 0) return [];
	const unit = inferUnit(tcName);
	const hib = !isLatency(tcName);
	const allAvgs: number[] = [];

	for (const cycle of data) {
		if (!Array.isArray(cycle?.data)) continue;
		for (const entry of cycle.data) {
			if (typeof entry?.avg === 'number') allAvgs.push(entry.avg);
		}
	}

	if (allAvgs.length === 0) return [];
	const avg = allAvgs.reduce((s, v) => s + v, 0) / allAvgs.length;
	return [{ key: 'avg', label: 'Avg', unit, value: avg, higherIsBetter: hib }];
}

// ── Main export ──

/**
 * 파서 데이터에서 핵심 비교 지표를 추출한다.
 * 모든 15개 파서를 지원하며, 미등록 파서는 generic fallback 적용.
 */
export function extractSummary(parserId: number, data: unknown, tcName: string): SummaryMetric[] {
	switch (parserId) {
		// GenPerf
		case 2:
		case 3:
		case 16:
			return extractGenPerf(data, tcName);

		// FragmentWrite
		case 4:
			return extractFragmentWrite(data, tcName);

		// PerfByChunk
		case 5:
			return extractPerfByChunk(data, tcName);

		// IntervaledReadLatency
		case 6:
			return extractCycleArraySimple(data, tcName);

		// WearLeveling
		case 10:
			return extractWearLeveling(data);

		// LongTermTC
		case 15:
			return extractLongTermTC(data, tcName);

		// KernelLatency
		case 20:
			return extractKernelLatency(data);

		// WriteAndDelete
		case 21:
			return extractWriteAndDelete(data, tcName);

		// UnmapThroughput
		case 23:
			return extractUnmapThroughput(data);

		// VluLatency
		case 24:
			return extractVluLatency(data);

		// VluDirtyCase4
		case 25:
		case 28:
			return extractVluDirtyCase4(data, tcName);

		// VluRandReadPerThread
		case 26:
			return extractRecordCycleArray(data, tcName);

		// WideRandRead
		case 27:
			return extractWideRandRead(data, tcName);

		// WbFlushThroughput
		case 29:
			return extractRecordCycleArray(data, tcName);

		default:
			// Fallback: try generic patterns
			if (isCycleArray(data)) return extractCycleArraySimple(data, tcName);
			if (data && typeof data === 'object' && !Array.isArray(data)) {
				return extractRecordCycleArray(data, tcName);
			}
			return [];
	}
}

/**
 * delta 계산: baseline 대비 변화율을 반환한다.
 */
export function computeDelta(value: number, baseline: number): { delta: number; label: string } | null {
	if (baseline === 0) return null;
	const delta = ((value - baseline) / baseline) * 100;
	const sign = delta >= 0 ? '+' : '';
	return { delta, label: `${sign}${delta.toFixed(1)}%` };
}

/**
 * delta 방향에 따른 색상 클래스 반환.
 * higherIsBetter에 따라 +가 좋은지 나쁜지 결정.
 */
export function deltaColorClass(delta: number, higherIsBetter: boolean): string {
	const isGood = higherIsBetter ? delta > 0 : delta < 0;
	const isBad = higherIsBetter ? delta < 0 : delta > 0;
	if (isGood) return 'text-emerald-600';
	if (isBad) return 'text-red-600';
	return 'text-muted-foreground';
}
