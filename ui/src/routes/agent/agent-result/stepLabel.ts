import type { ScenarioStep } from '$lib/api/agent.js';
import type { ToolType } from './types.js';

// executionConfig.steps[i] 에서 사용자에게 보일 step 라벨을 합성한다.
// 예) "Step 0 · FIO randread"  "Step 2 · TIOTEST all"  "Step 3 · AnTuTu"
//
// step 객체는 portal AgentController 가 보낸 JSON config 라 정형 ScenarioStep 보장이 안 됨 → any 로 받음.
export function buildStepLabel(idx: number, step: any | null, detectedTool: ToolType): string {
	const stepPart = `Step ${idx}`;
	if (!step || typeof step !== 'object') {
		return `${stepPart} · ${labelForTool(detectedTool)}`;
	}

	const type = String(step.type ?? '').toLowerCase();
	if (type === 'benchmark') {
		const tool = String(step.tool ?? detectedTool ?? '').toUpperCase() || labelForTool(detectedTool);
		const hint = benchmarkParamHint(step.tool, step.params);
		return hint ? `${stepPart} · ${tool} ${hint}` : `${stepPart} · ${tool}`;
	}
	if (type === 'app_macro') {
		const name = step.macroName ?? step.macro_name ?? 'Macro';
		return `${stepPart} · ${name}`;
	}
	if (type === 'iotest') {
		return `${stepPart} · IOTest`;
	}
	// 기타 type — 보통 metric 자체가 없어서 탭에 안 나타나지만, fallback
	return `${stepPart} · ${labelForTool(detectedTool)}`;
}

function labelForTool(tool: ToolType): string {
	switch (tool) {
		case 'fio': return 'FIO';
		case 'tiotest': return 'TIOTEST';
		case 'iozone': return 'IOZONE';
		case 'iotest': return 'IOTest';
		case 'macro': return 'Macro';
		default: return tool.toUpperCase();
	}
}

// fio: rw / iozone: -i / tiotest: test param 등 의미있는 파라미터를 짧게 노출.
function benchmarkParamHint(toolRaw: any, paramsRaw: any): string {
	const params = (paramsRaw && typeof paramsRaw === 'object') ? paramsRaw : {};
	const tool = String(toolRaw ?? '').toLowerCase();
	if (tool === 'fio') {
		const rw = params.rw ?? params.rwmode;
		const bs = params.bs;
		const parts: string[] = [];
		if (rw) parts.push(String(rw));
		if (bs) parts.push(`bs=${bs}`);
		return parts.join(' ');
	}
	if (tool === 'tiotest') {
		const test = params.test ?? params.skip;
		return test ? String(test) : '';
	}
	if (tool === 'iozone') {
		const reclen = params.reclen;
		return reclen ? `reclen=${reclen}` : '';
	}
	return '';
}
