export type ToolType = 'fio' | 'tiotest' | 'iozone' | 'macro' | 'iotest' | 'unknown';

export interface StepMetrics {
	step: number;
	tool: ToolType;
	label: string;      // "Step 0 (fio)", "Step 3 (AnTuTu)"
	metrics: Record<string, number>;  // prefix 제거된 metrics
}

export interface CycleStepMetrics {
	cycle: number;
	repeat: number;
	iteration: number;
	metrics: Record<string, number>;
}

/** r{N}_step{N}_ 또는 r{N}_loop{N}_step{N}_ prefix에서 step 번호 추출 */
function parsePrefix(key: string): { repeat: number; loop: number; step: number; rest: string } | null {
	const m = key.match(/^r(\d+)_(?:loop(\d+)_)?step(\d+)_(.+)$/);
	if (!m) return null;
	return { repeat: +m[1], loop: m[2] ? +m[2] : 0, step: +m[3], rest: m[4] };
}

/** metrics의 키 패턴으로 tool type 감지 */
export function detectToolType(metrics: Record<string, number>): ToolType {
	const keys = Object.keys(metrics);
	if (keys.some(k => k.endsWith('_score') || k.endsWith('_speed_mbs'))) return 'macro';
	// iotest: thread_{name}_{throughput_bps|duration_ns} 패턴 (fio 의 iops 와 헷갈리지 않게 우선 검사)
	if (keys.some(k => /^thread_.+_(throughput_bps|duration_ns)$/.test(k))) return 'iotest';
	if (keys.some(k => k.includes('iops') || k.includes('clat_ns') || k.includes('slat_ns'))) return 'fio';
	if (keys.some(k => /^(seq_write|seq_read|rand_write|rand_read)_/.test(k))) return 'tiotest';
	if (keys.some(k => k.includes('_kb_sec') || k.includes('reclen'))) return 'iozone';
	return 'unknown';
}

/** raw effectiveMetrics → step별로 분리 + tool 감지 */
export function splitByStep(effectiveMetrics: Record<string, number>): StepMetrics[] {
	const stepMap = new Map<number, Record<string, number>>();
	const unprefixed: Record<string, number> = {};

	for (const [key, value] of Object.entries(effectiveMetrics)) {
		const parsed = parsePrefix(key);
		if (parsed) {
			if (!stepMap.has(parsed.step)) stepMap.set(parsed.step, {});
			stepMap.get(parsed.step)![parsed.rest] = value;
		} else {
			unprefixed[key] = value;
		}
	}

	// prefix 없는 metrics가 있으면 step 0으로
	if (Object.keys(unprefixed).length > 0 && !stepMap.has(0)) {
		stepMap.set(0, unprefixed);
	} else if (Object.keys(unprefixed).length > 0) {
		for (const [k, v] of Object.entries(unprefixed)) {
			stepMap.get(0)![k] = v;
		}
	}

	const steps = [...stepMap.entries()]
		.sort(([a], [b]) => a - b)
		.map(([step, metrics]) => {
			const tool = detectToolType(metrics);
			const toolLabel = toolDisplayLabel(tool);
			return {
				step,
				tool,
				label: `Step ${step} (${toolLabel})`,
				metrics
			};
		});

	return steps;
}

/** 같은 tool 타입의 step들을 merge */
export interface MergedToolGroup {
	tool: ToolType;
	label: string;        // "FIO", "App Macro"
	steps: number[];      // 포함된 step 번호들
	metrics: Record<string, number>;  // 모든 step의 metrics 합산 (prefix 제거)
}

export function mergeByTool(steps: StepMetrics[]): MergedToolGroup[] {
	const groupMap = new Map<ToolType, MergedToolGroup>();

	for (const step of steps) {
		if (!groupMap.has(step.tool)) {
			groupMap.set(step.tool, {
				tool: step.tool,
				label: toolDisplayLabel(step.tool),
				steps: [],
				metrics: {}
			});
		}
		const group = groupMap.get(step.tool)!;
		group.steps.push(step.step);
		// metrics merge — 동일 키가 있으면 마지막 값 우선
		for (const [k, v] of Object.entries(step.metrics)) {
			group.metrics[k] = v;
		}
	}

	return [...groupMap.values()];
}

/** 특정 tool에 속하는 step들의 cycle 데이터 추출 */
export function extractCyclesForTool(effectiveMetrics: Record<string, number>, stepIndices: number[]): CycleStepMetrics[] {
	const stepSet = new Set(stepIndices);
	const cycleMap = new Map<string, CycleStepMetrics>();

	for (const [key, value] of Object.entries(effectiveMetrics)) {
		const parsed = parsePrefix(key);
		if (!parsed || !stepSet.has(parsed.step)) continue;

		const cycleKey = `r${parsed.repeat}_l${parsed.loop}`;
		if (!cycleMap.has(cycleKey)) {
			cycleMap.set(cycleKey, {
				cycle: parsed.loop > 0 ? parsed.loop : parsed.repeat,
				repeat: parsed.repeat,
				iteration: parsed.loop,
				metrics: {}
			});
		}
		cycleMap.get(cycleKey)!.metrics[parsed.rest] = value;
	}

	return [...cycleMap.values()].sort((a, b) => a.cycle - b.cycle);
}

/** step metrics에서 cycle별 데이터 추출 (loop/repeat 있을 때) */
export function extractCycles(effectiveMetrics: Record<string, number>, stepIndex: number): CycleStepMetrics[] {
	const cycleMap = new Map<string, CycleStepMetrics>();

	for (const [key, value] of Object.entries(effectiveMetrics)) {
		const parsed = parsePrefix(key);
		if (!parsed || parsed.step !== stepIndex) continue;

		const cycleKey = `r${parsed.repeat}_l${parsed.loop}`;
		if (!cycleMap.has(cycleKey)) {
			cycleMap.set(cycleKey, {
				cycle: parsed.loop > 0 ? parsed.loop : parsed.repeat,
				repeat: parsed.repeat,
				iteration: parsed.loop,
				metrics: {}
			});
		}
		cycleMap.get(cycleKey)!.metrics[parsed.rest] = value;
	}

	return [...cycleMap.values()].sort((a, b) => a.cycle - b.cycle);
}

function toolDisplayLabel(tool: ToolType): string {
	switch (tool) {
		case 'macro': return 'App Macro';
		case 'iotest': return 'I/O Test';
		default: return tool.toUpperCase();
	}
}

/** tool별 아이콘/색상 */
export const TOOL_STYLES: Record<ToolType, { color: string; bg: string }> = {
	fio: { color: 'text-blue-600', bg: 'bg-blue-500' },
	tiotest: { color: 'text-orange-600', bg: 'bg-orange-500' },
	iozone: { color: 'text-cyan-600', bg: 'bg-cyan-500' },
	macro: { color: 'text-violet-600', bg: 'bg-violet-500' },
	iotest: { color: 'text-cyan-700', bg: 'bg-cyan-600' },
	unknown: { color: 'text-gray-600', bg: 'bg-gray-500' },
};

// ──────────────────────────────────────────────────────────────
// 단위 정의 (StepCycleView / FioResultView / IOTestResultView 공용)
// ──────────────────────────────────────────────────────────────
//
// 사용자 결정사항:
//   - iops 는 진짜 IOPS 그대로 (divisor=1, 라벨 IOPS)
//   - 그 외 IEC 라벨 (MiB/s, KiB 등) 은 SI 라벨로만 변경 (수치는 그대로)
//
export interface UnitDef {
	unit: string;       // 표시 라벨
	divisor: number;
}

/** stripRw 가 적용된 metric name (예: 'iops', 'bw_kb') 또는 raw key 둘 다 입력 가능. */
export function getMetricUnit(name: string): UnitDef {
	// iops 계열 — 그대로 표시
	if (name === 'iops' || name === 'iops_mean' || name === 'iops_min' || name === 'iops_max' || name === 'iops_stddev') {
		return { unit: 'IOPS', divisor: 1 };
	}
	// 대역폭 (fio bw_kb → MB/s 라벨로 표시, divisor 는 1024 유지)
	if (name === 'bw_kb' || name === 'bw_min_kb' || name === 'bw_max_kb' || name === 'bw_mean_kb' || name === 'bw_stddev_kb') {
		return { unit: 'MB/s', divisor: 1024 };
	}
	if (name === 'bw_bytes') {
		return { unit: 'MB/s', divisor: 1_048_576 };
	}
	if (name === 'io_bytes') {
		return { unit: 'MB', divisor: 1_048_576 };
	}
	// nanosecond latency → ms
	if (name.includes('_ns_')) {
		return { unit: 'ms', divisor: 1_000_000 };
	}
	// tiotest/iozone 은 이미 mb_sec / kb_sec 단위 raw
	if (name.endsWith('_mb_sec')) {
		return { unit: 'MB/s', divisor: 1 };
	}
	if (name.endsWith('_kb_sec')) {
		return { unit: 'KB/s', divisor: 1 };
	}
	// 시간 / 카운트
	if (name === 'job_runtime_ms' || name.endsWith('_runtime_ms') || name.endsWith('_time_sec')) {
		return { unit: name.endsWith('_time_sec') ? 's' : 'ms', divisor: 1 };
	}
	// pct
	if (name.endsWith('_pct') || name.endsWith('_cpu_pct')) {
		return { unit: '%', divisor: 1 };
	}
	// fallback — 단위 모름
	return { unit: '', divisor: 1 };
}

/** 화면 표시용 숫자 포맷. */
export function formatMetricValue(rawValue: number, name: string): string {
	const { divisor } = getMetricUnit(name);
	const v = rawValue / divisor;
	if (!isFinite(v)) return String(rawValue);
	if (v === 0) return '0';
	if (Math.abs(v) >= 100) return v.toLocaleString('en-US', { maximumFractionDigits: 1 });
	if (Math.abs(v) >= 1) return v.toFixed(2);
	return v.toFixed(3);
}

// ──────────────────────────────────────────────────────────────
// RW 분류
// ──────────────────────────────────────────────────────────────

export type RwKind = 'read' | 'write' | 'other';

export function classifyRw(key: string): RwKind {
	if (key.startsWith('read_')) return 'read';
	if (key.startsWith('write_')) return 'write';
	// tiotest 식: seq_read / rand_read / seq_write / rand_write
	if (key.startsWith('seq_read_') || key.startsWith('rand_read_')) return 'read';
	if (key.startsWith('seq_write_') || key.startsWith('rand_write_')) return 'write';
	return 'other';
}

export function stripRw(key: string): string {
	if (key.startsWith('read_')) return key.slice(5);
	if (key.startsWith('write_')) return key.slice(6);
	if (key.startsWith('seq_read_')) return key.slice('seq_read_'.length);
	if (key.startsWith('rand_read_')) return key.slice('rand_read_'.length);
	if (key.startsWith('seq_write_')) return key.slice('seq_write_'.length);
	if (key.startsWith('rand_write_')) return key.slice('rand_write_'.length);
	return key;
}

/** RW prefix 가 아닌 "변종" prefix (seq/rand 등) 까지 보존한 카테고리. tiotest 의 seq_read / rand_read 를 구분해야 차트에서 4 개 series 가 나옴. */
export function rwCategory(key: string): string {
	if (key.startsWith('read_')) return 'read';
	if (key.startsWith('write_')) return 'write';
	if (key.startsWith('seq_read_')) return 'seq_read';
	if (key.startsWith('rand_read_')) return 'rand_read';
	if (key.startsWith('seq_write_')) return 'seq_write';
	if (key.startsWith('rand_write_')) return 'rand_write';
	return 'other';
}

// ──────────────────────────────────────────────────────────────
// Cycle 매트릭스 — StepCycleView 의 핵심 데이터 구조
// ──────────────────────────────────────────────────────────────

export interface CycleMatrix {
	cycles: number[];                                       // [1, 2, 3, ...]
	values: Record<string, Record<number, number>>;        // values[rawKey][cycle] = raw
	rawKeys: string[];                                      // 등장한 metric key 의 union (cycle 합집합)
}

export function buildCycleMatrix(cycleMetrics: CycleStepMetrics[]): CycleMatrix {
	const cycles = cycleMetrics.map(c => c.cycle);
	const values: Record<string, Record<number, number>> = {};
	const rawKeySet = new Set<string>();
	for (const cm of cycleMetrics) {
		for (const [k, v] of Object.entries(cm.metrics)) {
			if (!values[k]) values[k] = {};
			values[k][cm.cycle] = v;
			rawKeySet.add(k);
		}
	}
	return { cycles, values, rawKeys: [...rawKeySet] };
}

// ──────────────────────────────────────────────────────────────
// 차트 그룹 — 단위가 같은 RW 짝을 한 sub-chart 로
// ──────────────────────────────────────────────────────────────

export interface MetricChartMember {
	rwCategory: string;     // 'read' | 'write' | 'seq_read' | ...
	rawKey: string;
	color: string;
}

export interface MetricChartGroup {
	groupKey: string;       // stripRw 결과 (예: 'iops', 'bw_kb', 'lat_avg_ms')
	label: string;          // 표시 이름
	unit: string;
	members: MetricChartMember[];
}

const RW_COLORS: Record<string, string> = {
	read: '#5470c6',
	seq_read: '#5470c6',
	rand_read: '#73c0de',
	write: '#fc8452',
	seq_write: '#fc8452',
	rand_write: '#ee6666',
	other: '#9a60b4',
};

/** metric 그룹별 label — UNIT_MAP 와 무관하게 사용자 친화적 이름. */
const GROUP_LABELS: Record<string, string> = {
	iops: 'IOPS',
	iops_mean: 'IOPS (mean)',
	bw_kb: 'Bandwidth',
	bw_bytes: 'Bandwidth',
	bw_mean_kb: 'Bandwidth (mean)',
	io_bytes: 'Total IO',
	mb_sec: 'Throughput',
	clat_ns_mean: 'Completion Latency (mean)',
	clat_ns_p99: 'Completion Latency (p99)',
	lat_ns_mean: 'Latency (mean)',
	lat_avg_ms: 'Latency (avg)',
	lat_max_ms: 'Latency (max)',
};

/** 차트로 그릴 만한 metric 만 선별 (너무 많아지면 시야 어지러움). */
const CHART_WHITELIST = new Set<string>([
	'iops', 'iops_mean',
	'bw_kb', 'bw_bytes', 'bw_mean_kb',
	'io_bytes',
	'mb_sec',           // tiotest seq_read_mb_sec → stripRw → 'mb_sec'
	'kb_sec',           // iozone seq_read_kb_sec → stripRw → 'kb_sec'
	'clat_ns_mean', 'clat_ns_p99.000000',
	'lat_ns_mean',
	'lat_avg_ms', 'lat_max_ms',
]);

export function buildMetricChartGroups(matrix: CycleMatrix): MetricChartGroup[] {
	const byGroup = new Map<string, MetricChartGroup>();
	for (const rawKey of matrix.rawKeys) {
		const groupKey = stripRw(rawKey);
		if (!CHART_WHITELIST.has(groupKey)) continue;
		const cat = rwCategory(rawKey);
		const { unit } = getMetricUnit(rawKey);
		if (!byGroup.has(groupKey)) {
			byGroup.set(groupKey, {
				groupKey,
				label: GROUP_LABELS[groupKey] ?? groupKey,
				unit,
				members: [],
			});
		}
		byGroup.get(groupKey)!.members.push({
			rwCategory: cat,
			rawKey,
			color: RW_COLORS[cat] ?? RW_COLORS.other,
		});
	}
	// RW (read / write / seq_read / rand_read / seq_write / rand_write) member 가
	// 1 개 이상 있는 group 만 chart 로. 'other' 만 있는 group (job_runtime 등) 은 제외.
	const rwCats = new Set(['read', 'write', 'seq_read', 'rand_read', 'seq_write', 'rand_write']);
	const filtered = [...byGroup.values()].filter(g => g.members.some(m => rwCats.has(m.rwCategory)));
	// 정렬 — iops > bw > lat > 나머지
	const order = ['iops', 'iops_mean', 'bw_kb', 'bw_bytes', 'bw_mean_kb', 'mb_sec', 'kb_sec', 'io_bytes', 'clat_ns_mean', 'clat_ns_p99.000000', 'lat_ns_mean', 'lat_avg_ms', 'lat_max_ms'];
	return filtered.sort((a, b) => {
		const ia = order.indexOf(a.groupKey); const ib = order.indexOf(b.groupKey);
		if (ia < 0 && ib < 0) return a.groupKey.localeCompare(b.groupKey);
		if (ia < 0) return 1;
		if (ib < 0) return -1;
		return ia - ib;
	});
}
