import type { Node, Edge } from '@xyflow/svelte';
import type { StepForm } from '../AgentStepEditDialog.svelte';
import type { ThreadProgress } from '../iotest/types.js';

export interface StepNodeData {
	stepForm: StepForm;
	label: string;
	stepType: string;
	execOrder?: number;  // 실행 순서 (1-based)
	execStatus?: 'idle' | 'running' | 'completed' | 'failed' | 'cancelled' | 'skipped';
	execLoopCurrent?: number;
	execLoopTotal?: number;
	execProgress?: number;
	// iotest stepType 일 때 thread별 진행률 — Go agent 의 stderr JSONL → SSE 로 forward 되는
	// 데이터를 ScenarioCanvas 가 채워준다. 데이터가 없으면 노드는 기존대로 렌더.
	threadProgresses?: ThreadProgress[];
}

export interface ConditionRule {
	source: string;           // "metric" | "shell"
	metricKey: string;
	operator: string;
	threshold: number;
	thresholdString: string;
	shellCommand: string;
	extractPattern: string;
}

export interface ConditionNodeData {
	source: string;           // "metric" | "shell" (단일 조건용, 하위 호환)
	metricKey: string;
	operator: string;
	threshold: number;
	thresholdString: string;
	shellCommand: string;
	extractPattern: string;
	rules: ConditionRule[];   // 복합 조건
	logic: string;            // "and" | "or"
	execOrder?: number;
	execStatus?: 'idle' | 'running' | 'completed' | 'failed' | 'cancelled' | 'skipped';
}

export interface LoopGroupData {
	loopCount: number;
	label: string;
}

export type ScenarioNode =
	| Node<StepNodeData, 'step'>
	| Node<ConditionNodeData, 'condition'>
	| Node<LoopGroupData, 'loopGroup'>;

export type ScenarioEdge = Edge;

export interface NodeExecutionState {
	status: 'idle' | 'running' | 'completed' | 'failed' | 'skipped';
	loopCurrent?: number;
	loopTotal?: number;
	repeatCurrent?: number;
	repeatTotal?: number;
	progressPercent?: number;
	error?: string;
}

// step 계약(색상 포함)은 Go 의 scenario.Specs 에서 생성된다 — step-contract.ts 참고.
// 여기서 재수출해 기존 import 경로를 유지한다.
export { STEP_TYPE_COLORS, STEP_CONTRACTS, STEP_CONTRACT_BY_TYPE } from './step-contract.js';
export type { StepContract, StepParamSpec } from './step-contract.js';

export function stepSummary(form: StepForm): string {
	switch (form.type) {
		case 'benchmark': return `${form.tool} · ${form.formParams.rw ?? ''} · ${form.formParams.bs ?? ''}`;
		case 'iotest': return `${form.iotestConfig?.threads.length ?? 0} threads`;
		case 'shell': return form.extraText.slice(0, 30) || 'shell';
		case 'cleanup': return form.cleanupMode === 'all' ? '전체 삭제' : form.cleanupMode === 'steps' ? 'step 파일 삭제' : form.cleanupPath || '삭제';
		case 'sleep': return `${form.extraText.replace('seconds=', '')}s`;
		case 'trace_start': return `${form.formParams.trace_type ?? 'ufs'} trace`;
		case 'trace_stop': return 'stop';
		case 'launch_app': {
			const pkg = form.launchPackage ? form.launchPackage.split('.').pop() : 'app';
			const m = form.launchClearMode === 'clear' ? '초기화' : form.launchClearMode === 'cache' ? '캐시삭제' : form.launchClearMode === 'none' ? '' : '재시작';
			return m ? `${pkg} (${m})` : pkg ?? 'app';
		}
		case 'stop_app': return (form.stopPackage ? form.stopPackage.split('.').pop() : 'app') + ' 종료';
		case 'app_macro': return form.macroName ?? `Macro #${form.macroId ?? '?'}`;
		case 'tap_element': {
			const sel = form.elementText || form.elementContentDesc || form.elementResourceId || 'element';
			const mode = form.elementMatchMode && form.elementMatchMode !== 'exact' ? `~${sel}` : sel;
			return form.elementIndex ? `${mode} [${form.elementIndex}]` : mode;
		}
		case 'tap': return (form.tapX != null && form.tapY != null) ? `(${form.tapX}, ${form.tapY})` : 'tap';
		case 'text': return (form.inputText ? `"${form.inputText.slice(0, 18)}"` : 'text') + (form.inputSubmit ? ' ⏎' : '');
		case 'scroll': return `${form.scrollDirection === 'up' ? '위로' : '아래로'} ×${form.scrollCount ?? 3}`;
		case 'key': {
			const names: Record<number, string> = { 4: '뒤로가기', 3: '홈', 187: '최근앱', 85: '재생/정지', 86: '정지', 66: '엔터' };
			return names[form.keycode ?? 4] ?? `keycode ${form.keycode ?? 4}`;
		}
		case 'install_apk': return form.formParams.apk_filename ?? 'APK';
		case 'uninstall_apk': return form.formParams.package_name ?? 'package';
		default: return form.type;
	}
}
