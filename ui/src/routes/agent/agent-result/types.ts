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
