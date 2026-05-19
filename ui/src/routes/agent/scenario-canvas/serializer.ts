import type { Edge } from '@xyflow/svelte';
import type { StepForm } from '../AgentStepEditDialog.svelte';
import type { ScenarioNode, StepNodeData, ConditionNodeData } from './types.js';
import { getDefaultParams, getBasicOptions } from '../benchmarkOptions.js';
import { stepSummary } from './types.js';
import type { ScenarioStep, ScenarioLoop } from '$lib/api/agent.js';

export interface StepEdge {
	fromStep: number;
	toStep: number;
	label: string;
}

export interface CanvasProtoResult {
	steps: ScenarioStep[];
	loops: ScenarioLoop[];
	hasBranching: boolean;
	edges: StepEdge[];
}

/**
 * Canvas → Proto format (기존 백엔드 호환)
 * 위상 정렬로 노드를 선형 순서로 변환
 */
export function canvasToProto(
	nodes: ScenarioNode[],
	edges: Edge[],
	loopMembers?: Map<string, Set<string>>
): CanvasProtoResult {
	// 실행 가능한 노드 (step + condition)
	const execNodes = nodes.filter(n => n.type === 'step' || n.type === 'condition');
	if (execNodes.length === 0) return { steps: [], loops: [], hasBranching: false, edges: [] };

	const hasCondition = execNodes.some(n => n.type === 'condition');

	// 분기가 없으면 Y좌표 기준 정렬 (직관적), 분기가 있으면 위상 정렬
	const sorted = hasCondition
		? topologicalSort(execNodes, edges)
		: [...execNodes].sort((a, b) => {
			// 절대 Y좌표로 정렬
			const ay = a.position.y;
			const by = b.position.y;
			return ay - by;
		});

	// nodeId → step index 매핑
	const nodeToIndex = new Map<string, number>();
	sorted.forEach((n, i) => nodeToIndex.set(n.id, i));

	const steps: ScenarioStep[] = sorted.map(node => {
		if (node.type === 'condition') {
			const data = node.data as ConditionNodeData;
			// true/false 분기 대상 찾기
			const trueEdge = edges.find(e => e.source === node.id && e.sourceHandle === 'true');
			const falseEdge = edges.find(e => e.source === node.id && e.sourceHandle === 'false');
			return {
				type: 'condition',
				params: {},
				condition: {
					source: data.source || 'metric',
					metricKey: data.metricKey,
					operator: data.operator,
					threshold: data.threshold,
					thresholdString: data.thresholdString || '',
					shellCommand: data.shellCommand || '',
					extractPattern: data.extractPattern || '',
					rules: data.rules && data.rules.length > 0 ? data.rules : undefined,
					logic: data.logic || 'and',
					trueBranchStep: trueEdge ? (nodeToIndex.get(trueEdge.target) ?? -1) : -1,
					falseBranchStep: falseEdge ? (nodeToIndex.get(falseEdge.target) ?? -1) : -1
				}
			};
		}
		const data = node.data as StepNodeData;
		const form = data.stepForm;
		const params = buildStepParams(form);
		const step: ScenarioStep = {
			type: form.type,
			tool: form.type === 'benchmark' ? form.tool : undefined,
			params
		};
		if (form.type === 'app_macro' && form.macroId) {
			(step as any).macroId = form.macroId;
			(step as any).macroName = form.macroName;
			(step as any).macroClearMode = form.macroClearMode ?? 'force_stop';
		}
		return step;
	});

	// 엣지 → StepEdge
	const stepEdges: StepEdge[] = [];
	for (const e of edges) {
		const from = nodeToIndex.get(e.source);
		const to = nodeToIndex.get(e.target);
		if (from != null && to != null) {
			stepEdges.push({
				fromStep: from,
				toStep: to,
				label: e.sourceHandle ?? ''
			});
		}
	}

	// 루프 그룹 → ScenarioLoop (loopMembers Map 사용)
	const loops: ScenarioLoop[] = [];
	if (!hasCondition) {
		const loopGroups = nodes.filter(n => n.type === 'loopGroup');
		for (const group of loopGroups) {
			const members = loopMembers?.get(group.id);
			if (!members || members.size === 0) continue;
			const indices = [...members].map(id => nodeToIndex.get(id)).filter((i): i is number => i != null);
			if (indices.length > 0) {
				loops.push({
					startStep: Math.min(...indices),
					endStep: Math.max(...indices),
					count: (group.data as any).loopCount ?? 1
				});
			}
		}
	}

	return { steps, loops, hasBranching: hasCondition, edges: stepEdges };
}

/**
 * Proto → Canvas (기존 템플릿 로드)
 * condition step과 edges도 복원
 */
export function protoToCanvas(
	steps: { type: string; tool?: string; params?: Record<string, string>; condition?: { metricKey?: string; metric_key?: string; operator?: string; threshold?: number; trueBranchStep?: number; true_branch_step?: number; falseBranchStep?: number; false_branch_step?: number } }[],
	loops: { startStep: number; endStep: number; count: number }[],
	protoEdges?: { fromStep?: number; from_step?: number; toStep?: number; to_step?: number; label?: string }[]
): { nodes: ScenarioNode[]; edges: Edge[] } {
	const nodes: ScenarioNode[] = [];
	const edges: Edge[] = [];

	const hasConditions = steps.some(s => s.type === 'condition');

	// 루프 그룹 먼저 생성 (분기 없을 때만)
	const loopGroups = new Map<number, string>();
	if (!hasConditions) {
		for (let li = 0; li < loops.length; li++) {
			const loop = loops[li];
			const groupId = `loop-${li}`;
			nodes.push({
				id: groupId,
				type: 'loopGroup',
				position: { x: -20, y: loop.startStep * 100 - 20 },
				data: { loopCount: loop.count, label: `Loop x${loop.count}` },
				style: `width: 240px; height: ${(loop.endStep - loop.startStep + 1) * 100 + 40}px;`
			} as any);
			for (let si = loop.startStep; si <= loop.endStep; si++) {
				loopGroups.set(si, groupId);
			}
		}
	}

	// 노드 생성 (step + condition)
	// 분기가 있으면 가로로 펼쳐서 배치
	const conditionIndices = new Set(steps.map((s, i) => s.type === 'condition' ? i : -1).filter(i => i >= 0));
	let yPos = 0;

	for (let i = 0; i < steps.length; i++) {
		const s = steps[i];
		const nodeId = `step-${i}`;

		if (s.type === 'condition') {
			// Condition 노드
			const cond = s.condition ?? {};
			nodes.push({
				id: nodeId,
				type: 'condition',
				position: { x: 200, y: yPos },
				data: {
					source: cond.source ?? 'metric',
					metricKey: cond.metricKey ?? cond.metric_key ?? '',
					operator: cond.operator ?? '>',
					threshold: cond.threshold ?? 0,
					thresholdString: cond.thresholdString ?? cond.threshold_string ?? '',
					shellCommand: cond.shellCommand ?? cond.shell_command ?? '',
					extractPattern: cond.extractPattern ?? cond.extract_pattern ?? '',
					rules: (cond.rules ?? []).map((r: any) => ({
						source: r.source ?? 'metric',
						metricKey: r.metricKey ?? r.metric_key ?? '',
						operator: r.operator ?? '>',
						threshold: r.threshold ?? 0,
						thresholdString: r.thresholdString ?? r.threshold_string ?? '',
						shellCommand: r.shellCommand ?? r.shell_command ?? '',
						extractPattern: r.extractPattern ?? r.extract_pattern ?? ''
					})),
					logic: cond.logic ?? 'and'
				} satisfies ConditionNodeData
			} as any);
			yPos += 120;
		} else {
			// Step 노드
			const tool = s.tool ?? 'FIO';
			const params = s.params ?? {};

			// useFileFromStep 복원
			const useFile = params.use_file_from_step != null ? Number(params.use_file_from_step) : null;

			// cleanup 모드 복원
			let cleanupMode: 'all' | 'steps' | 'path' = 'all';
			let cleanupSteps = new Set<number>();
			let cleanupPath = '';
			if (s.type === 'cleanup') {
				if (params.delete_files_from_steps) {
					cleanupMode = 'steps';
					cleanupSteps = new Set(params.delete_files_from_steps.split(',').map(Number));
				} else if (params.path) {
					cleanupMode = 'path';
					cleanupPath = params.path;
				}
			}

			// IOTEST: restore config from params (independent step type or legacy benchmark+IOTEST)
			let iotestConfig = undefined;
			if ((s.type === 'iotest' || tool === 'IOTEST') && params.config) {
				try { iotestConfig = JSON.parse(params.config); } catch {}
			}

			// extra params (formParams에 없는 것들)
			const knownKeys = new Set(['use_file_from_step', 'delete_files_from_steps', 'path', 'trace', 'trace_type', 'config']);
			const extraLines: string[] = [];
			const formParams: Record<string, string> = {};
			const basicOpts = getBasicOptions(tool);
			const basicKeys = new Set(basicOpts.map(o => o.key));
			for (const [k, v] of Object.entries(params)) {
				if (knownKeys.has(k)) continue;
				if (basicKeys.has(k)) { formParams[k] = v; }
				else { extraLines.push(`${k}=${v}`); }
			}
			// 기본값 채우기
			for (const opt of basicOpts) {
				if (!(opt.key in formParams)) formParams[opt.key] = opt.defaultValue;
			}

			const form: StepForm = {
				type: s.type, tool,
				formParams,
				extraText: extraLines.join('\n'),
				showAdvanced: false,
				useFileFromStep: useFile,
				cleanupMode,
				cleanupSteps,
				cleanupPath,
				traceEnabled: params.trace === 'on',
				traceType: params.trace_type ?? 'ufs',
				macroId: (s as any).macroId ?? null,
				macroName: (s as any).macroName ?? '',
				macroClearMode: (s as any).macroClearMode ?? 'force_stop',
				iotestConfig
			};

			// 분기가 있으면 condition의 true/false 타겟에 따라 좌/우 배치
			let xPos = 50;
			if (hasConditions) {
				// 이 step이 어떤 condition의 true/false 타겟인지 확인
				for (const [ci, cs] of steps.entries()) {
					if (cs.type !== 'condition' || !cs.condition) continue;
					const cond = cs.condition;
					const trueStep = cond.trueBranchStep ?? cond.true_branch_step;
					const falseStep = cond.falseBranchStep ?? cond.false_branch_step;
					if (trueStep === i) { xPos = 400; break; }  // true → 오른쪽
					if (falseStep === i) { xPos = 0; break; }    // false → 왼쪽
				}
			}

			const groupId = loopGroups.get(i);
			const node: any = {
				id: nodeId,
				type: 'step',
				position: groupId
					? { x: 20, y: (i - (loops.find(l => l.startStep <= i && i <= l.endStep)?.startStep ?? i)) * 100 + 20 }
					: { x: xPos, y: yPos },
				data: { stepForm: form, label: stepSummary(form), stepType: s.type }
			};
			if (groupId) {
				node.parentId = groupId;
				node.extent = 'parent';
			}
			nodes.push(node);
			yPos += 100;
		}
	}

	// 엣지 생성
	if (protoEdges && protoEdges.length > 0) {
		// 명시적 엣지가 있으면 그대로 복원
		for (const pe of protoEdges) {
			const from = pe.fromStep ?? pe.from_step ?? 0;
			const to = pe.toStep ?? pe.to_step ?? 0;
			const label = pe.label ?? '';
			const sourceHandle = label === 'true' || label === 'false' ? label : undefined;
			edges.push({
				id: `e-${from}-${to}-${label}`,
				source: `step-${from}`,
				target: `step-${to}`,
				sourceHandle,
				label: label === 'true' ? 'T' : label === 'false' ? 'F' : undefined
			});
		}
	} else if (hasConditions) {
		// 엣지가 없지만 condition이 있으면 condition 데이터에서 복원
		for (let i = 0; i < steps.length; i++) {
			const s = steps[i];
			if (s.type === 'condition' && s.condition) {
				const cond = s.condition;
				const trueStep = cond.trueBranchStep ?? cond.true_branch_step;
				const falseStep = cond.falseBranchStep ?? cond.false_branch_step;
				// 이전 step에서 condition으로 연결
				if (i > 0) {
					edges.push({ id: `e-${i-1}-${i}`, source: `step-${i-1}`, target: `step-${i}` });
				}
				if (trueStep != null && trueStep >= 0) {
					edges.push({ id: `e-${i}-${trueStep}-true`, source: `step-${i}`, target: `step-${trueStep}`, sourceHandle: 'true', label: 'T' });
				}
				if (falseStep != null && falseStep >= 0) {
					edges.push({ id: `e-${i}-${falseStep}-false`, source: `step-${i}`, target: `step-${falseStep}`, sourceHandle: 'false', label: 'F' });
				}
			}
		}
	} else {
		// 순차 엣지
		for (let i = 0; i < steps.length - 1; i++) {
			edges.push({
				id: `e-${i}-${i + 1}`,
				source: `step-${i}`,
				target: `step-${i + 1}`
			});
		}
	}

	// parent 노드가 child보다 배열 앞에 와야 함 (xyflow 요구사항)
	const sortedNodes = [
		...nodes.filter(n => n.type === 'loopGroup'),
		...nodes.filter(n => n.type !== 'loopGroup')
	];
	return { nodes: sortedNodes, edges };
}

/**
 * 그래프 유효성 검증
 */
export function validateGraph(nodes: ScenarioNode[], edges: Edge[]): { valid: boolean; errors: string[] } {
	const errors: string[] = [];
	const stepNodes = nodes.filter(n => n.type === 'step');

	if (stepNodes.length === 0) {
		errors.push('최소 1개 이상의 step이 필요합니다');
	}

	// 연결되지 않은 노드 확인
	const connectedIds = new Set<string>();
	for (const e of edges) {
		connectedIds.add(e.source);
		connectedIds.add(e.target);
	}
	if (stepNodes.length > 1) {
		for (const n of stepNodes) {
			if (!connectedIds.has(n.id)) {
				errors.push(`연결되지 않은 노드: ${(n.data as StepNodeData).stepType}`);
			}
		}
	}

	return { valid: errors.length === 0, errors };
}

// ── Internal helpers ──

function topologicalSort(nodes: ScenarioNode[], edges: Edge[]): ScenarioNode[] {
	const nodeMap = new Map(nodes.map(n => [n.id, n]));
	const inDegree = new Map<string, number>();
	const adj = new Map<string, string[]>();

	for (const n of nodes) {
		inDegree.set(n.id, 0);
		adj.set(n.id, []);
	}

	const seenEdges = new Set<string>();
	for (const e of edges) {
		const key = `${e.source}->${e.target}`;
		if (nodeMap.has(e.source) && nodeMap.has(e.target) && !seenEdges.has(key)) {
			seenEdges.add(key);
			adj.get(e.source)!.push(e.target);
			inDegree.set(e.target, (inDegree.get(e.target) ?? 0) + 1);
		}
	}

	const queue = nodes.filter(n => (inDegree.get(n.id) ?? 0) === 0).map(n => n.id);
	const sorted: ScenarioNode[] = [];

	while (queue.length > 0) {
		const id = queue.shift()!;
		sorted.push(nodeMap.get(id)!);
		for (const next of adj.get(id) ?? []) {
			const deg = (inDegree.get(next) ?? 1) - 1;
			inDegree.set(next, deg);
			if (deg === 0) queue.push(next);
		}
	}

	// 정렬 안 된 노드는 끝에 추가
	for (const n of nodes) {
		if (!sorted.includes(n)) sorted.push(n);
	}

	return sorted;
}

function buildStepParams(s: StepForm): Record<string, string> {
	if (s.type === 'cleanup') {
		const params: Record<string, string> = {};
		if (s.cleanupMode === 'steps' && s.cleanupSteps.size > 0) {
			params.delete_files_from_steps = [...s.cleanupSteps].sort((a, b) => a - b).join(',');
		} else if (s.cleanupMode === 'path' && s.cleanupPath.trim()) {
			params.path = s.cleanupPath.trim();
		}
		return params;
	}

	// IOTEST: config as JSON in params (independent step type)
	if (s.type === 'iotest' && s.iotestConfig) {
		const params: Record<string, string> = { config: JSON.stringify(s.iotestConfig) };
		if (s.traceEnabled) {
			params.trace = 'on';
			params.trace_type = s.traceType;
		}
		return params;
	}

	const params: Record<string, string> = { ...s.formParams };
	// Extra text → params
	if (s.extraText.trim()) {
		for (const line of s.extraText.split('\n')) {
			const [k, ...rest] = line.split('=');
			if (k && rest.length > 0) params[k.trim()] = rest.join('=').trim();
		}
	}
	if (s.type === 'benchmark' && s.useFileFromStep != null) {
		params.use_file_from_step = String(s.useFileFromStep);
	}
	if ((s.type === 'benchmark' || s.type === 'shell') && s.traceEnabled) {
		params.trace = 'on';
		params.trace_type = s.traceType;
	}
	return params;
}
