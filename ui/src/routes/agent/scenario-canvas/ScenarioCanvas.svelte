<script lang="ts">
	import { SvelteFlow, Controls, MiniMap, Background, type Connection } from '@xyflow/svelte';
	import '@xyflow/svelte/dist/style.css';
	import { setContext } from 'svelte';
	import type { ScenarioTemplate } from '$lib/api/agent.js';
	import type { ActiveJob } from '../types.js';
	import { captionMuted } from '$lib/styles/common.js';
	import type { StepForm } from '../AgentStepEditDialog.svelte';
	import AgentStepEditDialog from '../AgentStepEditDialog.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import StepNode from './StepNode.svelte';
	import ConditionNode from './ConditionNode.svelte';
	import LoopGroup from './LoopGroup.svelte';
	import NodePalette from './NodePalette.svelte';
	import CanvasToolbar from './CanvasToolbar.svelte';
	import { protoToCanvas } from './serializer.js';
	import { getDefaultParams } from '../benchmarkOptions.js';
	import { stepSummary, type ScenarioNode, type ScenarioEdge, type StepNodeData, type ConditionNodeData, type ConditionRule, type LoopGroupData } from './types.js';
	import Check from '@lucide/svelte/icons/check';
	import X from '@lucide/svelte/icons/x';

	import type { Edge, Node } from '@xyflow/svelte';

	interface Props {
		serverId: number | null;
		selectedDevices: Set<string>;
		serverName: string;
		onJobStarted: (job: Omit<ActiveJob, 'events' | 'state' | 'eventSource'>) => void;
		activeJobs?: ActiveJob[];
	}

	let { serverId, selectedDevices, serverName, onJobStarted, activeJobs = [] }: Props = $props();

	const nodeTypes = { step: StepNode, condition: ConditionNode, loopGroup: LoopGroup };

	let nodes = $state<Node[]>([]);
	let edges = $state<Edge[]>([]);

	// Execution state — context 제거, nodes data에 직접 반영

	// Node action callbacks (context for StepNode / LoopGroup)
	setContext('onEditNode', (nodeId: string) => openStepEditor(nodeId));
	setContext('onDeleteNode', (nodeId: string) => deleteNode(nodeId));
	setContext('onEditLoopCount', (nodeId: string) => openLoopEditor(nodeId));
	setContext('onEditCondition', (nodeId: string) => openConditionEditor(nodeId));
	setContext('onMoveNode', (nodeId: string, dir: -1 | 1) => moveNode(nodeId, dir));

	// Step edit dialog
	let editOpen = $state(false);
	let editingNodeId = $state<string | null>(null);
	let editingStep = $state<StepForm | null>(null);
	let editingStepIndex = $state(0);

	// Loop membership (parentId 대신 사용 — xyflow 그룹 제약 회피)
	// loopGroupId → Set<stepNodeId>
	let loopMembers = $state<Map<string, Set<string>>>(new Map());

	// Loop edit dialog
	let loopEditOpen = $state(false);
	let loopEditNodeId = $state<string | null>(null);
	let loopEditCount = $state(10);
	let loopEditMembers = $state<Set<string>>(new Set());

	// Condition edit dialog
	let condEditOpen = $state(false);
	let condEditNodeId = $state<string | null>(null);
	let condSource = $state('metric');
	let condMetricKey = $state('');
	let condOperator = $state('>');
	let condThreshold = $state(0);
	let condThresholdString = $state('');
	let condShellCommand = $state('');
	let condExtractPattern = $state('');
	let condRules = $state<ConditionRule[]>([]);
	let condLogic = $state('and');
	let showExtractTest = $state(false);
	let extractTestInput = $state('');

	function testExtract(input: string, pattern: string): { value: string | null; type: string; error?: string } {
		try {
			if (pattern) {
				const re = new RegExp(pattern);
				const m = re.exec(input);
				if (m && m[1]) return { value: m[1], type: '캡처 그룹' };
				if (m && m[0]) return { value: m[0], type: '전체 매치' };
				return { value: null, type: '' };
			}
			// 패턴 없으면 첫 번째 숫자
			const numMatch = input.match(/[-+]?\d*\.?\d+/);
			if (numMatch) return { value: numMatch[0], type: '자동 숫자 추출' };
			return { value: null, type: '' };
		} catch (e) {
			return { value: null, type: '', error: `정규식 오류: ${(e as Error).message}` };
		}
	}

	function evalCondition(val: number, op: string, threshold: number): boolean {
		switch (op) {
			case '>': return val > threshold;
			case '>=': return val >= threshold;
			case '<': return val < threshold;
			case '<=': return val <= threshold;
			case '==': return val === threshold;
			case '!=': return val !== threshold;
			default: return false;
		}
	}

	// Execution tracking
	let currentJobId = $state<string | null>(null);
	let currentRepeat = $state<{ current: number; total: number } | null>(null);

	let nodeIdCounter = $state(0);
	let selectedEdgeId = $state<string | null>(null);

	// 엣지 선택 → Delete/Backspace로 삭제
	function handleKeydown(e: KeyboardEvent) {
		if ((e.key === 'Delete' || e.key === 'Backspace') && selectedEdgeId) {
			edges = edges.filter(edge => edge.id !== selectedEdgeId);
			selectedEdgeId = null;
			updateExecOrder();
		}
	}

	// ── SSE 실행 추적 ──
	// SSE 추적 — activeJobs.events 길이가 바뀔 때만 nodes 업데이트
	let lastStateKey = '';

	$effect(() => {
		if (!activeJobs || activeJobs.length === 0) {
			if (currentJobId) { currentJobId = null; currentRepeat = null; }
			return;
		}

		// running job 우선, 없으면 방금 완료/실패된 job
		const runningJob = activeJobs.find(j => j.state === 'running' && j.type === 'scenario');
		const finishedJob = !runningJob ? activeJobs.find(j => (j.state === 'completed' || j.state === 'failed') && j.type === 'scenario' && j.jobId === currentJobId) : null;

		const targetJob = runningJob ?? finishedJob;
		if (!targetJob) return;

		// 이벤트 수가 같고 상태도 같으면 중복 업데이트 방지
		const eventCount = targetJob.events?.length ?? 0;
		const stateKey = `${targetJob.jobId}_${targetJob.state}_${eventCount}`;
		if (stateKey === lastStateKey) return;
		lastStateKey = stateKey;
		currentJobId = targetJob.jobId;

		requestAnimationFrame(() => {
			if (targetJob.state === 'completed') {
				markAllNodes('completed');
			} else if (targetJob.state === 'failed') {
				markFailedNodes(targetJob);
			} else {
				updateNodeStates(targetJob);
			}
		});
	});

	function updateNodeStates(job: ActiveJob) {
		const events = job.events;
		if (!events || events.length === 0) return;

		let parsedStepIndex: number | null = null;
		let loopCurrent: number | undefined;
		let loopTotal: number | undefined;

		for (let i = events.length - 1; i >= 0; i--) {
			const p = parseProgress(events[i].message ?? '');
			if (p && p.stepIndex != null) {
				parsedStepIndex = p.stepIndex;
				loopCurrent = p.loopIndex;
				loopTotal = p.loopTotal;
				if (p.repeatIndex != null && p.repeatTotal != null) {
					currentRepeat = { current: p.repeatIndex, total: p.repeatTotal };
				}
				break;
			}
		}

		if (parsedStepIndex == null) return;

		const stepNodeIds = nodes.filter(n => n.type === 'step' || n.type === 'condition').map(n => n.id);

		// iotest thread 진행률 — events 에서 가장 최신 thread snapshot 들 추출 (현재 step 만)
		const iotestThreadProgresses = extractIOTestThreadProgresses(events);

		nodes = nodes.map(n => {
			if (n.type !== 'step' && n.type !== 'condition') return n;
			const idx = stepNodeIds.indexOf(n.id);
			let execStatus: string | undefined;
			if (idx < parsedStepIndex!) execStatus = 'completed';
			else if (idx === parsedStepIndex) execStatus = 'running';
			const isCurrent = idx === parsedStepIndex;
			return {
				...n,
				data: {
					...n.data,
					execStatus,
					execLoopCurrent: isCurrent ? loopCurrent : undefined,
					execLoopTotal: isCurrent ? loopTotal : undefined,
					threadProgresses: isCurrent && n.type === 'step' && (n.data as any).stepType === 'iotest'
						? iotestThreadProgresses
						: undefined
				}
			};
		});
	}

	/**
	 * events 에서 iotest 의 thread별 progress JSONL 을 추출.
	 *
	 * 현재 Go agent 측 stderr JSONL → SSE forwarding 이 미구현이라 빈 배열 반환.
	 * Go agent 의 iotest 실행이 실시간 progress 이벤트를 SSE 로 흘려보내기 시작하면
	 * 메시지 패턴 (예: `IOTEST|thread=name|completed=N|total=N|status=...|currentOp=...`)
	 * 을 여기서 파싱해 ThreadProgress[] 로 누적·갱신하면 된다.
	 */
	function extractIOTestThreadProgresses(events: ActiveJob['events']): import('../iotest/types.js').ThreadProgress[] {
		if (!events || events.length === 0) return [];
		const map = new Map<string, import('../iotest/types.js').ThreadProgress>();
		for (const e of events) {
			const msg = e.message ?? '';
			if (!msg.startsWith('IOTEST|')) continue;
			const parts: Record<string, string> = {};
			for (const seg of msg.split('|').slice(1)) {
				const eq = seg.indexOf('=');
				if (eq > 0) parts[seg.slice(0, eq)] = seg.slice(eq + 1);
			}
			const name = parts.thread;
			if (!name) continue;
			const totalSteps = +(parts.total ?? '0');
			const completedSteps = +(parts.completed ?? '0');
			const status = (parts.status as 'running' | 'completed' | 'failed' | 'idle') ?? 'running';
			const percent = totalSteps > 0 ? Math.min(100, (completedSteps / totalSteps) * 100) : 0;
			map.set(name, {
				name,
				totalSteps,
				completedSteps,
				currentOp: parts.op ?? '',
				currentIter: parts.iter ? +parts.iter : undefined,
				currentTotal: parts.iterTotal ? +parts.iterTotal : undefined,
				status,
				percent
			});
		}
		return [...map.values()];
	}

	function markAllNodes(finalState: string) {
		const status = finalState === 'completed' ? 'completed' : 'failed';
		nodes = nodes.map(n => {
			if (n.type !== 'step' && n.type !== 'condition') return n;
			return { ...n, data: { ...n.data, execStatus: status, execLoopCurrent: undefined, execLoopTotal: undefined } };
		});
	}

	/**
	 * job 이 FAILED 로 끝났을 때, 실패한 스텝만 빨강으로 칠하고 그 앞 스텝은 completed,
	 * 그 뒤 스텝은 미실행(status 없음)으로 구분한다.
	 * markAllNodes('failed') 처럼 전부 빨강으로 칠하거나, 진행 인덱스만 보고 전부 completed 로
	 * 오인하던 버그를 방지한다.
	 *
	 * 실패 이벤트 메시지는 `step N ... failed: ...` (N 은 0-based stepIndex, backend scenario.go).
	 * 해당 이벤트를 못 찾으면 마지막 진행 스텝을 실패 스텝으로 간주한다.
	 */
	function markFailedNodes(job: ActiveJob) {
		const events = job.events ?? [];
		const stepNodeIds = nodes.filter(n => n.type === 'step' || n.type === 'condition').map(n => n.id);

		// 1) 명시적 실패 이벤트에서 실패 스텝 인덱스 추출 (`... failed: ...`)
		let failedIndex: number | null = null;
		for (let i = events.length - 1; i >= 0; i--) {
			const msg = events[i].message ?? '';
			if (/failed\s*:/i.test(msg)) {
				const m = msg.match(/[Ss]tep\s*(\d+)/);
				if (m) { failedIndex = parseInt(m[1]); break; }
			}
		}

		// 2) 실패 이벤트를 못 찾으면 마지막으로 진행 중이던 스텝을 실패로 간주
		if (failedIndex == null) {
			for (let i = events.length - 1; i >= 0; i--) {
				const p = parseProgress(events[i].message ?? '');
				if (p && p.stepIndex != null) { failedIndex = p.stepIndex; break; }
			}
		}

		// 진행 정보가 전혀 없으면 안전하게 전부 실패로 표기 (기존 동작)
		if (failedIndex == null) { markAllNodes('failed'); return; }

		nodes = nodes.map(n => {
			if (n.type !== 'step' && n.type !== 'condition') return n;
			const idx = stepNodeIds.indexOf(n.id);
			let execStatus: string | undefined;
			if (idx < failedIndex!) execStatus = 'completed';
			else if (idx === failedIndex) execStatus = 'failed';
			// idx > failedIndex → 미실행 (execStatus undefined)
			return { ...n, data: { ...n.data, execStatus, execLoopCurrent: undefined, execLoopTotal: undefined } };
		});
	}

	function parseProgress(message: string): {
		stepIndex?: number; loopIndex?: number; loopTotal?: number;
		repeatIndex?: number; repeatTotal?: number;
	} | null {
		if (!message) return null;
		const result: any = {};
		const stepMatch = message.match(/[Ss]tep\s*(\d+)/);
		if (stepMatch) result.stepIndex = parseInt(stepMatch[1]);
		const loopMatch = message.match(/loop\s*(\d+)\/(\d+)/i);
		if (loopMatch) { result.loopIndex = parseInt(loopMatch[1]); result.loopTotal = parseInt(loopMatch[2]); }
		const repeatMatch = message.match(/repeat\s*(\d+)\/(\d+)/i);
		if (repeatMatch) { result.repeatIndex = parseInt(repeatMatch[1]); result.repeatTotal = parseInt(repeatMatch[2]); }
		return Object.keys(result).length > 0 ? result : null;
	}

	// ── 노드 관리 ──

	function createDefaultStep(type: string): StepForm {
		const tool = type === 'benchmark' ? 'FIO' : '';
		return {
			type, tool,
			formParams: type === 'benchmark' ? getDefaultParams(tool) : {},
			extraText: type === 'sleep' ? 'seconds=5' : type === 'shell' ? 'cmd=' : '',
			showAdvanced: false, useFileFromStep: null,
			cleanupMode: 'all', cleanupSteps: new Set(), cleanupPath: '',
			traceEnabled: false, traceType: 'ufs',
			// 요소 기반 탭 / 텍스트 입력 기본값
			elementResourceId: '', elementText: '', elementContentDesc: '',
			elementX: null, elementY: null, inputText: '', inputSubmit: false,
			tapX: null, tapY: null,
			keycode: 4,
			elementMatchMode: 'exact', elementIndex: 0, elementContainerId: '',
			// scroll 기본값
			scrollDirection: 'down', scrollCount: 3, scrollPause: 1, scrollDuration: 400,
			// launch_app 기본값
			launchPackage: '', launchClearMode: 'force_stop', launchWaitSeconds: 3, launchWaitActivity: '',
			// stop_app 기본값
			stopPackage: ''
		};
	}

	// 라이브 화면(AgentScreenSheet)에서 요소를 클릭하면 tap_element 블록을 캔버스에 추가한다.
	// +page.svelte 가 canvas 인스턴스를 bind 해 호출한다.
	export function addTapElementStep(sel: {
		resourceId: string; text: string; contentDesc: string; x: number; y: number;
		matchMode?: string; index?: number; containerId?: string;
	}) {
		const form = createDefaultStep('tap_element');
		form.elementResourceId = sel.resourceId;
		form.elementText = sel.text;
		form.elementContentDesc = sel.contentDesc;
		form.elementX = sel.x;
		form.elementY = sel.y;
		form.elementMatchMode = sel.matchMode ?? 'exact';
		form.elementIndex = sel.index ?? 0;
		form.elementContainerId = sel.containerId ?? '';

		const id = `step-${nodeIdCounter++}`;
		// 마지막 step 아래에 세로로 쌓는다.
		const stepNodesBefore = nodes.filter(n => n.type === 'step');
		const lastY = stepNodesBefore.length > 0
			? Math.max(...stepNodesBefore.map(n => n.position.y)) + 100
			: 40;
		const newNode: Node = {
			id,
			type: 'step',
			position: { x: 60, y: lastY },
			data: { stepForm: form, label: stepSummary(form), stepType: 'tap_element' } satisfies StepNodeData
		} as any;
		nodes = sortNodesParentFirst([...nodes, newNode]);

		// 직전 step 에 자동 연결
		const stepNodes = nodes.filter(n => n.type === 'step');
		if (stepNodes.length >= 2) {
			const prev = stepNodes[stepNodes.length - 2];
			const hasOutgoing = edges.some(e => e.source === prev.id);
			if (!hasOutgoing) {
				edges = [...edges, { id: `e-${prev.id}-${id}`, source: prev.id, target: id }];
			}
		}
		updateExecOrder();
	}

	function onDrop(event: DragEvent) {
		event.preventDefault();
		const type = event.dataTransfer?.getData('application/step-type');
		if (!type) return;

		const canvasBounds = (event.currentTarget as HTMLElement).getBoundingClientRect();
		const x = event.clientX - canvasBounds.left;
		const y = event.clientY - canvasBounds.top;

		// Condition 노드 드롭
		if (type === '__condition__') {
			const id = `cond-${nodeIdCounter++}`;
			nodes = [...nodes, {
				id,
				type: 'condition',
				position: { x: x - 60, y: y - 40 },
				data: { source: 'metric', metricKey: '', operator: '>', threshold: 0, thresholdString: '', shellCommand: '', extractPattern: '', rules: [], logic: 'and' } satisfies ConditionNodeData
			} as Node];
			condEditNodeId = id;
			condSource = 'metric';
			condMetricKey = '';
			condOperator = '>';
			condThreshold = 0;
			condThresholdString = '';
			condShellCommand = '';
			condExtractPattern = '';
			condRules = [];
			condLogic = 'and';
			condEditOpen = true;
			return;
		}

		// Loop 그룹 드롭
		if (type === '__loop__') {
			const id = `loop-${nodeIdCounter++}`;
			nodes = [...nodes, {
				id,
				type: 'loopGroup',
				position: { x: x - 100, y: y - 60 },
				data: { loopCount: 10, label: 'Loop x10' } satisfies LoopGroupData,
				style: 'width: 240px; height: 200px;'
			} as Node];
			return;
		}

		// Step 노드 드롭 — Loop 안에 드롭했는지 감지
		const form = createDefaultStep(type);
		const id = `step-${nodeIdCounter++}`;

		// 드롭 위치가 Loop 그룹 안인지 확인
		const targetGroup = findLoopGroupAtPosition(x, y);

		const newNode: Node = targetGroup ? {
			id,
			type: 'step',
			parentId: targetGroup.id,
			extent: 'parent' as const,
			position: { x: x - targetGroup.position.x - 70, y: y - targetGroup.position.y - 20 },
			data: { stepForm: form, label: stepSummary(form), stepType: type } satisfies StepNodeData
		} as any : {
			id,
			type: 'step',
			position: { x: x - 70, y: y - 20 },
			data: { stepForm: form, label: stepSummary(form), stepType: type } satisfies StepNodeData
		};

		nodes = sortNodesParentFirst([...nodes, newNode]);

		// 자동 연결 (중복 방지)
		const stepNodes = nodes.filter(n => n.type === 'step');
		if (stepNodes.length >= 2) {
			const prev = stepNodes[stepNodes.length - 2];
			const hasOutgoing = edges.some(e => e.source === prev.id);
			const alreadyConnected = edges.some(e => e.source === prev.id && e.target === id);
			if (!hasOutgoing && !alreadyConnected) {
				edges = [...edges, { id: `e-${prev.id}-${id}`, source: prev.id, target: id }];
			}
		}
		updateExecOrder();
	}

	function onDragOver(event: DragEvent) {
		event.preventDefault();
		if (event.dataTransfer) event.dataTransfer.dropEffect = 'move';
	}

	function onConnect(connection: Connection) {
		if (!connection.source || !connection.target) return;
		// 같은 source→target 중복 방지
		if (edges.some(e => e.source === connection.source && e.target === connection.target)) return;
		const id = `e-${connection.source}-${connection.target}-${connection.sourceHandle ?? ''}`;
		edges = [...edges, { id, source: connection.source, target: connection.target, sourceHandle: connection.sourceHandle ?? undefined }];
		updateExecOrder();
	}

	function openStepEditor(nodeId: string) {
		const node = nodes.find(n => n.id === nodeId);
		if (!node || node.type !== 'step') return;
		const data = node.data as StepNodeData;
		editingNodeId = node.id;
		editingStep = data.stepForm;
		editingStepIndex = nodes.filter(n => n.type === 'step').indexOf(node);
		editOpen = true;
	}

	function deleteNode(nodeId: string) {
		// 루프 그룹 삭제 시 자식 노드의 parentId 해제
		const node = nodes.find(n => n.id === nodeId);
		if (node?.type === 'loopGroup') {
			nodes = nodes.map(n =>
				(n as any).parentId === nodeId ? { ...n, parentId: undefined, extent: undefined } as Node : n
			);
		}
		nodes = nodes.filter(n => n.id !== nodeId);
		edges = edges.filter(e => e.source !== nodeId && e.target !== nodeId);
	}

	function moveNode(nodeId: string, dir: -1 | 1) {
		// 엣지 기반 실행 순서로 정렬
		const ordered = getExecutionOrder();
		const idx = ordered.findIndex(id => id === nodeId);
		if (idx < 0) return;
		const swapIdx = idx + dir;
		if (swapIdx < 0 || swapIdx >= ordered.length) return;

		const nodeAId = ordered[idx];
		const nodeBId = ordered[swapIdx];
		const nodeA = nodes.find(n => n.id === nodeAId);
		const nodeB = nodes.find(n => n.id === nodeBId);
		if (!nodeA || !nodeB) return;

		// 위치 교환
		const posA = { ...nodeA.position };
		const posB = { ...nodeB.position };

		nodes = nodes.map(n => {
			if (n.id === nodeAId) return { ...n, position: posB };
			if (n.id === nodeBId) return { ...n, position: posA };
			return n;
		});

		// 엣지 재연결: A와 B의 모든 연결을 교환
		edges = edges.map(e => {
			let source = e.source;
			let target = e.target;
			if (source === nodeAId) source = nodeBId;
			else if (source === nodeBId) source = nodeAId;
			if (target === nodeAId) target = nodeBId;
			else if (target === nodeBId) target = nodeAId;
			return { ...e, id: `e-${source}-${target}-${e.sourceHandle ?? ''}`, source, target };
		});
	}

	// 엣지 기반 실행 순서 (위상 정렬)
	function getExecutionOrder(): string[] {
		const stepNodes = nodes.filter(n => n.type === 'step' || n.type === 'condition');
		const hasCondition = stepNodes.some(n => n.type === 'condition');

		if (hasCondition) {
			// 분기가 있으면 엣지 기반 위상 정렬
			const nodeIds = new Set(stepNodes.map(n => n.id));
			const inDegree = new Map<string, number>();
			const adj = new Map<string, string[]>();
			for (const n of stepNodes) { inDegree.set(n.id, 0); adj.set(n.id, []); }
			const seenEdges = new Set<string>();
			for (const e of edges) {
				const key = `${e.source}->${e.target}`;
				if (nodeIds.has(e.source) && nodeIds.has(e.target) && !seenEdges.has(key)) {
					seenEdges.add(key);
					adj.get(e.source)!.push(e.target);
					inDegree.set(e.target, (inDegree.get(e.target) ?? 0) + 1);
				}
			}
			const queue = stepNodes.filter(n => (inDegree.get(n.id) ?? 0) === 0).map(n => n.id);
			const result: string[] = [];
			while (queue.length > 0) {
				const id = queue.shift()!;
				result.push(id);
				for (const next of adj.get(id) ?? []) {
					const deg = (inDegree.get(next) ?? 1) - 1;
					inDegree.set(next, deg);
					if (deg === 0) queue.push(next);
				}
			}
			for (const n of stepNodes) { if (!result.includes(n.id)) result.push(n.id); }
			return result;
		}

		// 분기 없으면 Y좌표 기준 정렬 (캔버스 위치 = 실행 순서)
		return [...stepNodes]
			.sort((a, b) => a.position.y - b.position.y)
			.map(n => n.id);
	}

	// 노드 드래그 종료 시 Loop 그룹 안에 들어왔는지 감지
	function handleNodeDragStop({ targetNode }: { targetNode: Node | null; nodes: Node[]; event: MouseEvent | TouchEvent }) {
		const node = targetNode;
		if (!node || node.type === 'loopGroup') return;

		const loopGroups = nodes.filter(n => n.type === 'loopGroup');
		let newParentId: string | undefined;

		for (const group of loopGroups) {
			// 그룹의 영역 계산
			const gx = group.position.x;
			const gy = group.position.y;
			const style = (group as any).style ?? '';
			const wMatch = style.match(/width:\s*(\d+)px/);
			const hMatch = style.match(/height:\s*(\d+)px/);
			const gw = wMatch ? parseInt(wMatch[1]) : 240;
			const gh = hMatch ? parseInt(hMatch[1]) : 200;

			// 노드의 절대 위치 (parentId가 있으면 parent 기준)
			let nx = node.position.x;
			let ny = node.position.y;
			if ((node as any).parentId) {
				const parent = nodes.find(n => n.id === (node as any).parentId);
				if (parent) { nx += parent.position.x; ny += parent.position.y; }
			}

			// 노드 중심이 그룹 내부에 있는지 확인
			if (nx >= gx && nx <= gx + gw && ny >= gy && ny <= gy + gh) {
				newParentId = group.id;
				break;
			}
		}

		const currentParentId = (node as any).parentId;

		if (newParentId && newParentId !== currentParentId) {
			// 그룹 안으로 들어옴 → parentId 설정 + 상대 좌표로 변환
			const group = nodes.find(n => n.id === newParentId)!;
			let absX = node.position.x;
			let absY = node.position.y;
			if (currentParentId) {
				const oldParent = nodes.find(n => n.id === currentParentId);
				if (oldParent) { absX += oldParent.position.x; absY += oldParent.position.y; }
			}
			nodes = nodes.map(n => {
				if (n.id === node.id) {
					return { ...n, parentId: newParentId, extent: 'parent' as const, position: { x: absX - group.position.x, y: absY - group.position.y } } as any;
				}
				return n;
			});
			nodes = sortNodesParentFirst(nodes);
		} else if (!newParentId && currentParentId) {
			// 그룹 밖으로 나감 → parentId 해제 + 절대 좌표로 변환
			const oldParent = nodes.find(n => n.id === currentParentId);
			nodes = nodes.map(n => {
				if (n.id === node.id) {
					const absX = node.position.x + (oldParent?.position.x ?? 0);
					const absY = node.position.y + (oldParent?.position.y ?? 0);
					return { ...n, parentId: undefined, extent: undefined, position: { x: absX, y: absY } } as any;
				}
				return n;
			});
		}
	}

	// parent 노드가 child보다 배열 앞에 오도록 정렬 (xyflow 요구사항)
	function sortNodesParentFirst(nodeList: Node[]): Node[] {
		const parents = nodeList.filter(n => n.type === 'loopGroup');
		const children = nodeList.filter(n => n.type !== 'loopGroup');
		return [...parents, ...children];
	}

	// 좌표가 Loop 그룹 내부에 있는지 확인
	function findLoopGroupAtPosition(x: number, y: number): Node | null {
		const loopGroups = nodes.filter(n => n.type === 'loopGroup');
		for (const group of loopGroups) {
			const gx = group.position.x;
			const gy = group.position.y;
			const style = (group as any).style ?? '';
			const wMatch = style.match(/width:\s*(\d+)px/);
			const hMatch = style.match(/height:\s*(\d+)px/);
			const gw = wMatch ? parseInt(wMatch[1]) : 240;
			const gh = hMatch ? parseInt(hMatch[1]) : 200;
			if (x >= gx && x <= gx + gw && y >= gy && y <= gy + gh) {
				return group;
			}
		}
		return null;
	}

	function openLoopEditor(nodeId: string) {
		const node = nodes.find(n => n.id === nodeId);
		if (!node || node.type !== 'loopGroup') return;
		loopEditNodeId = nodeId;
		loopEditCount = (node.data as LoopGroupData).loopCount;
		loopEditMembers = new Set(loopMembers.get(nodeId) ?? []);
		loopEditOpen = true;
	}

	function openConditionEditor(nodeId: string) {
		const node = nodes.find(n => n.id === nodeId);
		if (!node || node.type !== 'condition') return;
		const data = node.data as ConditionNodeData;
		condEditNodeId = nodeId;
		condSource = data.source || 'metric';
		condMetricKey = data.metricKey;
		condOperator = data.operator;
		condThreshold = data.threshold;
		condThresholdString = data.thresholdString || '';
		condShellCommand = data.shellCommand || '';
		condExtractPattern = data.extractPattern || '';
		condRules = data.rules ? [...data.rules] : [];
		condLogic = data.logic || 'and';
		condEditOpen = true;
	}

	function saveCondition() {
		if (!condEditNodeId) return;
		nodes = nodes.map(n => {
			if (n.id === condEditNodeId) {
				return { ...n, data: { source: condSource, metricKey: condMetricKey, operator: condOperator, threshold: condThreshold, thresholdString: condThresholdString, shellCommand: condShellCommand, extractPattern: condExtractPattern, rules: condRules, logic: condLogic } satisfies ConditionNodeData } as Node;
			}
			return n;
		});
		condEditOpen = false;
		condEditNodeId = null;
	}

	function saveLoop() {
		if (!loopEditNodeId) return;
		const groupId = loopEditNodeId;

		// 그룹 데이터 업데이트
		nodes = nodes.map(n => {
			if (n.id === groupId) {
				return { ...n, data: { ...n.data, loopCount: loopEditCount, label: `Loop x${loopEditCount}` } satisfies LoopGroupData } as Node;
			}
			return n;
		});

		// loopMembers 업데이트 (parentId 사용 안 함)
		const newMap = new Map(loopMembers);
		newMap.set(groupId, new Set(loopEditMembers));
		loopMembers = newMap;

		loopEditOpen = false;
		loopEditNodeId = null;
		updateExecOrder();
	}

	function handleStepSave(updated: StepForm) {
		if (!editingNodeId) return;
		nodes = nodes.map(n => {
			if (n.id === editingNodeId) {
				return { ...n, data: { ...n.data, stepForm: updated, label: stepSummary(updated), stepType: updated.type } satisfies StepNodeData } as Node;
			}
			return n;
		});
		editOpen = false;
		editingNodeId = null;
		editingStep = null;
	}

	function handleStepCancel() {
		editOpen = false;
		editingNodeId = null;
		editingStep = null;
	}

	// steps/loops(proto wire shape) → 캔버스 nodes/edges 주입 공통 로직.
	// 템플릿 로드 / AI 자연어 생성이 공유한다.
	function applyCanvas(steps: any[], loops: any[]) {
		try {
			const result = protoToCanvas(steps, loops);
			nodes = result.nodes;
			edges = result.edges;
			nodeIdCounter = nodes.length + 10;

			// loopMembers 복원: loopGroup 노드의 자식(parentId 기반)을 loopMembers로 변환
			const newLoopMembers = new Map<string, Set<string>>();
			for (const n of nodes) {
				if (n.type === 'loopGroup') continue;
				const pid = (n as any).parentId;
				if (pid) {
					if (!newLoopMembers.has(pid)) newLoopMembers.set(pid, new Set());
					newLoopMembers.get(pid)!.add(n.id);
				}
			}
			loopMembers = newLoopMembers;

			// parentId 제거 + 상대좌표 → 절대좌표 변환
			nodes = nodes.map(n => {
				const pid = (n as any).parentId;
				if (pid) {
					const parent = nodes.find(p => p.id === pid);
					const absX = n.position.x + (parent?.position.x ?? 0);
					const absY = n.position.y + (parent?.position.y ?? 0);
					const { parentId, extent, ...rest } = n as any;
					return { ...rest, position: { x: absX, y: absY } } as Node;
				}
				return n;
			});

			requestAnimationFrame(() => updateExecOrder());
		} catch { nodes = []; edges = []; }
	}

	function handleLoadTemplate(t: ScenarioTemplate) {
		let steps: any[] = [];
		let loops: any[] = [];
		try {
			steps = JSON.parse(t.stepsJson);
			loops = t.loopsJson ? JSON.parse(t.loopsJson) : [];
		} catch { nodes = []; edges = []; return; }
		applyCanvas(steps, loops);
	}

	// AI 자연어 생성 결과 주입 — 템플릿 로드와 동일 경로.
	function handleGenerate(steps: any[], loops: any[]) {
		applyCanvas(steps, loops);
	}

	// ── Metric 정의 (조건 분기용) ──
	interface MetricDef { key: string; label: string; displayUnit: string; rawUnit: string; toDisplay: number; toRaw: number; }
	interface MetricCategory { label: string; items: MetricDef[]; }

	const metricCategories: MetricCategory[] = [
		{ label: 'Throughput', items: [
			{ key: 'read_iops', label: 'Read IOPS', displayUnit: 'KIOPS', rawUnit: 'ops/s', toDisplay: 1/1000, toRaw: 1000 },
			{ key: 'write_iops', label: 'Write IOPS', displayUnit: 'KIOPS', rawUnit: 'ops/s', toDisplay: 1/1000, toRaw: 1000 },
			{ key: 'read_bw_kb', label: 'Read BW', displayUnit: 'MiB/s', rawUnit: 'KB/s', toDisplay: 1/1024, toRaw: 1024 },
			{ key: 'write_bw_kb', label: 'Write BW', displayUnit: 'MiB/s', rawUnit: 'KB/s', toDisplay: 1/1024, toRaw: 1024 },
			{ key: 'read_bw_bytes', label: 'Read BW', displayUnit: 'MiB/s', rawUnit: 'B/s', toDisplay: 1/(1024*1024), toRaw: 1024*1024 },
			{ key: 'write_bw_bytes', label: 'Write BW', displayUnit: 'MiB/s', rawUnit: 'B/s', toDisplay: 1/(1024*1024), toRaw: 1024*1024 },
		]},
		{ label: 'IOPS 통계', items: [
			{ key: 'read_iops_mean', label: 'Read IOPS 평균', displayUnit: 'KIOPS', rawUnit: 'ops/s', toDisplay: 1/1000, toRaw: 1000 },
			{ key: 'write_iops_mean', label: 'Write IOPS 평균', displayUnit: 'KIOPS', rawUnit: 'ops/s', toDisplay: 1/1000, toRaw: 1000 },
			{ key: 'read_iops_min', label: 'Read IOPS 최소', displayUnit: 'KIOPS', rawUnit: 'ops/s', toDisplay: 1/1000, toRaw: 1000 },
			{ key: 'read_iops_max', label: 'Read IOPS 최대', displayUnit: 'KIOPS', rawUnit: 'ops/s', toDisplay: 1/1000, toRaw: 1000 },
			{ key: 'write_iops_min', label: 'Write IOPS 최소', displayUnit: 'KIOPS', rawUnit: 'ops/s', toDisplay: 1/1000, toRaw: 1000 },
			{ key: 'write_iops_max', label: 'Write IOPS 최대', displayUnit: 'KIOPS', rawUnit: 'ops/s', toDisplay: 1/1000, toRaw: 1000 },
		]},
		{ label: 'Latency', items: [
			{ key: 'read_lat_ns_mean', label: 'Read Latency 평균', displayUnit: 'ms', rawUnit: 'ns', toDisplay: 1/1_000_000, toRaw: 1_000_000 },
			{ key: 'write_lat_ns_mean', label: 'Write Latency 평균', displayUnit: 'ms', rawUnit: 'ns', toDisplay: 1/1_000_000, toRaw: 1_000_000 },
			{ key: 'read_clat_ns_mean', label: 'Read Complete Lat 평균', displayUnit: 'ms', rawUnit: 'ns', toDisplay: 1/1_000_000, toRaw: 1_000_000 },
			{ key: 'write_clat_ns_mean', label: 'Write Complete Lat 평균', displayUnit: 'ms', rawUnit: 'ns', toDisplay: 1/1_000_000, toRaw: 1_000_000 },
			{ key: 'read_slat_ns_mean', label: 'Read Submit Lat 평균', displayUnit: 'μs', rawUnit: 'ns', toDisplay: 1/1000, toRaw: 1000 },
			{ key: 'write_slat_ns_mean', label: 'Write Submit Lat 평균', displayUnit: 'μs', rawUnit: 'ns', toDisplay: 1/1000, toRaw: 1000 },
		]},
		{ label: 'IO', items: [
			{ key: 'read_io_bytes', label: 'Read 총 IO', displayUnit: 'MiB', rawUnit: 'bytes', toDisplay: 1/(1024*1024), toRaw: 1024*1024 },
			{ key: 'write_io_bytes', label: 'Write 총 IO', displayUnit: 'MiB', rawUnit: 'bytes', toDisplay: 1/(1024*1024), toRaw: 1024*1024 },
			{ key: 'read_total_ios', label: 'Read IO 횟수', displayUnit: '', rawUnit: '', toDisplay: 1, toRaw: 1 },
			{ key: 'write_total_ios', label: 'Write IO 횟수', displayUnit: '', rawUnit: '', toDisplay: 1, toRaw: 1 },
		]},
		{ label: 'CPU', items: [
			{ key: 'usr_cpu_pct', label: 'User CPU', displayUnit: '%', rawUnit: '%', toDisplay: 1, toRaw: 1 },
			{ key: 'sys_cpu_pct', label: 'System CPU', displayUnit: '%', rawUnit: '%', toDisplay: 1, toRaw: 1 },
		]},
		{ label: 'iozone/tiotest', items: [
			{ key: 'seq_write_kb_sec', label: 'Sequential Write', displayUnit: 'MiB/s', rawUnit: 'KB/s', toDisplay: 1/1024, toRaw: 1024 },
			{ key: 'seq_read_kb_sec', label: 'Sequential Read', displayUnit: 'MiB/s', rawUnit: 'KB/s', toDisplay: 1/1024, toRaw: 1024 },
			{ key: 'rand_write_kb_sec', label: 'Random Write', displayUnit: 'MiB/s', rawUnit: 'KB/s', toDisplay: 1/1024, toRaw: 1024 },
			{ key: 'rand_read_kb_sec', label: 'Random Read', displayUnit: 'MiB/s', rawUnit: 'KB/s', toDisplay: 1/1024, toRaw: 1024 },
		]},
		{ label: 'Storage (/data)', items: [
			{ key: 'data_usage_percent', label: '/data 사용률', displayUnit: '%', rawUnit: '%', toDisplay: 1, toRaw: 1 },
			{ key: 'data_used_gb', label: '/data 사용량', displayUnit: 'GB', rawUnit: 'GB', toDisplay: 1, toRaw: 1 },
			{ key: 'data_avail_gb', label: '/data 여유', displayUnit: 'GB', rawUnit: 'GB', toDisplay: 1, toRaw: 1 },
		]},
	];

	const allMetricDefs = metricCategories.flatMap(c => c.items);
	let selectedMetric = $derived(allMetricDefs.find(m => m.key === condMetricKey));
	let condDisplayValue = $derived(selectedMetric ? condThreshold * selectedMetric.toDisplay : condThreshold);

	function updateExecOrder() {
		const ordered = getExecutionOrder();
		console.log('[ExecOrder] edges:', edges.map(e => `${e.source}→${e.target}`));
		console.log('[ExecOrder] ordered:', ordered);
		nodes = nodes.map(n => {
			if (n.type !== 'step' && n.type !== 'condition') return n;
			const idx = ordered.indexOf(n.id);
			return { ...n, data: { ...n.data, execOrder: idx >= 0 ? idx + 1 : undefined } };
		});
	}

	function autoLayout() {
		const ordered = getExecutionOrder();
		const loopGroups = nodes.filter(n => n.type === 'loopGroup');

		// 그룹별 자식 매핑
		const groupChildren = new Map<string, string[]>();
		for (const g of loopGroups) groupChildren.set(g.id, []);
		for (const id of ordered) {
			const n = nodes.find(nn => nn.id === id);
			const pid = n && (n as any).parentId;
			if (pid && groupChildren.has(pid)) groupChildren.get(pid)!.push(id);
		}

		let y = 0;
		const placed = new Set<string>();
		const newPositions = new Map<string, { x: number; y: number }>();
		const groupPositions = new Map<string, { x: number; y: number; h: number }>();

		for (const id of ordered) {
			if (placed.has(id)) continue;
			const n = nodes.find(nn => nn.id === id);
			const pid = n && (n as any).parentId;

			if (pid && groupChildren.has(pid) && !placed.has(pid)) {
				// 그룹 시작 — 그룹 위치 설정 후 자식들을 그룹 내부에 배치
				const children = groupChildren.get(pid)!;
				const gx = 100;
				const gy = y;
				let cy = 40;
				for (const cid of children) {
					newPositions.set(cid, { x: 30, y: cy });
					cy += 100;
					placed.add(cid);
				}
				groupPositions.set(pid, { x: gx, y: gy, h: cy + 20 });
				y += cy + 40;
			} else if (!pid) {
				// 그룹에 속하지 않은 노드
				newPositions.set(id, { x: 150, y });
				placed.add(id);
				y += 100;
			}
		}

		nodes = nodes.map(n => {
			if (n.type === 'loopGroup') {
				const gp = groupPositions.get(n.id);
				if (gp) return { ...n, position: { x: gp.x, y: gp.y }, style: `width: 280px; height: ${gp.h}px;` } as any;
				return n;
			}
			const pos = newPositions.get(n.id);
			if (pos) return { ...n, position: pos };
			return n;
		});

		nodes = sortNodesParentFirst(nodes);
	}

	function handleClearCanvas() {
		nodes = [];
		edges = [];
		nodeIdCounter = 0;
		currentJobId = null;
		currentRepeat = null;
	}
</script>

<div class="flex flex-col h-full">
	<CanvasToolbar
		{nodes} {edges} {serverId} {selectedDevices} {serverName} {onJobStarted}
		onLoadTemplate={handleLoadTemplate}
		onClearCanvas={handleClearCanvas}
		onAutoLayout={autoLayout}
		onGenerate={handleGenerate}
		{loopMembers}
	/>

	<!-- Repeat counter overlay -->
	{#if currentRepeat}
		<div class="absolute top-12 right-4 z-10 px-2 py-1 rounded-md bg-blue-600 text-white text-[10px] font-medium shadow">
			Repeat {currentRepeat.current}/{currentRepeat.total}
		</div>
	{/if}

	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="flex flex-1 min-h-0 relative" onkeydown={handleKeydown} tabindex="-1">
		<NodePalette />
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div class="flex-1" ondrop={onDrop} ondragover={onDragOver}>
			<SvelteFlow
				bind:nodes bind:edges {nodeTypes}
				onconnect={onConnect}
				onnodedragstop={handleNodeDragStop}
				onedgeclick={({ edge }) => {
					selectedEdgeId = edge.id;
					edges = edges.map(e => ({ ...e, selected: e.id === edge.id }));
				}}
				onpaneclick={() => {
					selectedEdgeId = null;
					edges = edges.map(e => ({ ...e, selected: false }));
				}}
				deleteKey="Backspace"
				fitView
				class="bg-muted/20"
			>
				<Controls position="bottom-right" />
				<MiniMap />
				<Background />
			</SvelteFlow>
		</div>
	</div>
</div>

<!-- Step Edit Dialog -->
<AgentStepEditDialog
	bind:open={editOpen}
	step={editingStep}
	stepIndex={editingStepIndex}
	allSteps={nodes.filter(n => n.type === 'step').map(n => (n.data as StepNodeData).stepForm)}
	onSave={handleStepSave}
	onCancel={handleStepCancel}
	{serverId}
	deviceId={[...selectedDevices][0] ?? null}
/>

<!-- Condition Edit Dialog -->
<Dialog.Root bind:open={condEditOpen}>
	<Dialog.Content class="max-w-md max-h-[85vh] flex flex-col">
		<Dialog.Header>
			<Dialog.Title class="text-sm">조건 분기 설정</Dialog.Title>
			<Dialog.Description class="{captionMuted}">조건을 만족하면 True 경로, 아니면 False 경로로 분기합니다</Dialog.Description>
		</Dialog.Header>
		<div class="flex-1 overflow-y-auto space-y-3 py-3">
			<!-- 단일/복합 모드 + 소스 선택 -->
			{#if condRules.length > 0}
				<!-- 복합 조건 모드 -->
				<div class="flex items-center gap-2 text-[10px]">
					<span class="font-medium">복합 조건</span>
					<select bind:value={condLogic} class="border rounded px-2 py-0.5 text-[10px] bg-background">
						<option value="and">AND (모두 만족)</option>
						<option value="or">OR (하나라도 만족)</option>
					</select>
					<button onclick={() => { condRules = []; }} class="ml-auto text-[9px] text-muted-foreground hover:text-foreground">단일 조건으로</button>
				</div>

				<div class="space-y-2">
					{#each condRules as rule, ri}
						<div class="border rounded p-2 space-y-1 bg-muted/20 relative">
							<div class="flex items-center gap-1 text-[9px]">
								<span class="font-medium text-muted-foreground">규칙 {ri + 1}</span>
								<select bind:value={rule.source} class="border rounded px-1.5 py-0.5 text-[9px] bg-background ml-auto">
									<option value="metric">메트릭</option>
									<option value="shell">Shell</option>
								</select>
								<button onclick={() => { condRules = condRules.filter((_, i) => i !== ri); }}
									class="text-red-500 hover:text-red-700 text-[9px]">삭제</button>
							</div>
							{#if rule.source === 'shell'}
								<input bind:value={rule.shellCommand} class="w-full border rounded px-2 py-0.5 text-[9px] bg-background font-mono" placeholder="shell 명령어" />
								<input bind:value={rule.extractPattern} class="w-full border rounded px-2 py-0.5 text-[9px] bg-background font-mono" placeholder="추출 패턴 (선택)" />
							{:else}
								<input bind:value={rule.metricKey} class="w-full border rounded px-2 py-0.5 text-[9px] bg-background font-mono" placeholder="metric key (예: read_iops)" />
							{/if}
							<div class="flex items-center gap-1">
								<select bind:value={rule.operator} class="border rounded px-1.5 py-0.5 text-[9px] bg-background">
									<option value=">">{'>'}</option>
									<option value=">=">{'≥'}</option>
									<option value="<">{'<'}</option>
									<option value="<=">{'≤'}</option>
									<option value="==">{'='}</option>
									<option value="!=">{'≠'}</option>
									<option value="contains">포함</option>
									<option value="!contains">미포함</option>
								</select>
								{#if rule.operator === 'contains' || rule.operator === '!contains'}
									<input bind:value={rule.thresholdString} class="flex-1 border rounded px-2 py-0.5 text-[9px] bg-background" placeholder="문자열" />
								{:else}
									<input type="number" step="any" bind:value={rule.threshold} class="w-20 border rounded px-2 py-0.5 text-[9px] bg-background text-right" />
								{/if}
							</div>
						</div>
					{/each}
					<button onclick={() => { condRules = [...condRules, { source: 'metric', metricKey: '', operator: '>', threshold: 0, thresholdString: '', shellCommand: '', extractPattern: '' }]; }}
						class="w-full border border-dashed rounded py-1 text-[9px] text-muted-foreground hover:bg-muted">+ 규칙 추가</button>
				</div>
			{:else}
				<!-- 단일 조건 모드 -->
				<div class="flex gap-2">
					<button onclick={() => condSource = 'metric'}
						class="flex-1 px-3 py-1.5 rounded text-[10px] font-medium transition-colors
							{condSource === 'metric' ? 'bg-primary text-primary-foreground' : 'border hover:bg-muted'}">
						벤치마크 메트릭
					</button>
					<button onclick={() => condSource = 'shell'}
						class="flex-1 px-3 py-1.5 rounded text-[10px] font-medium transition-colors
							{condSource === 'shell' ? 'bg-primary text-primary-foreground' : 'border hover:bg-muted'}">
						Shell 명령 결과
					</button>
				</div>
				<div class="text-right">
					<button onclick={() => { condRules = [{ source: condSource, metricKey: condMetricKey, operator: condOperator, threshold: condThreshold, thresholdString: condThresholdString, shellCommand: condShellCommand, extractPattern: condExtractPattern }]; }}
						class="text-[9px] text-blue-600 hover:text-blue-700">복합 조건으로 전환 (AND/OR)</button>
				</div>

			{#if condSource === 'shell'}
				<!-- Shell 모드 -->
				<div class="space-y-1">
					<label class="text-[10px] font-medium text-muted-foreground uppercase">Shell 명령어</label>
					<input bind:value={condShellCommand} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background font-mono"
						placeholder="df /data | awk '{'{'}print $5{'}'}' | tail -1" />
					<p class="text-[9px] text-muted-foreground">디바이스에서 실행할 명령어. 결과에서 값을 추출하여 조건 평가</p>
				</div>

				<div class="space-y-1">
					<label class="text-[10px] font-medium text-muted-foreground uppercase">값 추출 패턴 (정규식, 선택)</label>
					<input bind:value={condExtractPattern} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background font-mono"
						placeholder={'(\\d+)%'} />
					<p class="text-[9px] text-muted-foreground">
						비워두면 첫 번째 숫자를 자동 추출. 캡처 그룹 <code class="bg-muted px-0.5 rounded">(\d+)</code>으로 특정 위치 지정 가능.
					</p>
				</div>

				<!-- 추출 테스트 도우미 -->
				<details class="text-[10px]" bind:open={showExtractTest}>
					<summary class="text-blue-600 cursor-pointer hover:text-blue-700 font-medium">추출 테스트</summary>
					<div class="mt-1.5 space-y-1.5 border rounded p-2 bg-muted/20">
						<div>
							<label class="text-[9px] text-muted-foreground">샘플 출력 (명령 실행 결과를 붙여넣기)</label>
							<textarea bind:value={extractTestInput}
								class="w-full border rounded px-2 py-1 text-[10px] bg-background font-mono h-14 resize-y"
								placeholder={"Filesystem  1K-blocks  Used  Available  Use%  Mounted on\n/dev/sda  61255492  53234816  8020676  87%  /data"}></textarea>
						</div>
						{#if extractTestInput}
							{@const result = testExtract(extractTestInput, condExtractPattern)}
							<div class="flex items-center gap-2">
								<span class="text-[9px] text-muted-foreground">추출 결과:</span>
								{#if result.error}
									<span class="text-[10px] text-red-600">{result.error}</span>
								{:else if result.value != null}
									<span class="text-[10px] font-mono font-bold text-green-700 bg-green-100 px-1.5 py-0.5 rounded">{result.value}</span>
									<span class="text-[9px] text-muted-foreground">({result.type})</span>
								{:else}
									<span class="text-[10px] text-orange-600">값을 추출할 수 없습니다</span>
								{/if}
							</div>
							{#if result.value != null && condOperator && !(condOperator === 'contains' || condOperator === '!contains')}
								{@const num = parseFloat(result.value)}
								{#if !isNaN(num)}
									<div class="flex items-center gap-1 text-[9px]">
										<span class="text-muted-foreground">조건 평가:</span>
										<span class="font-mono">{num} {condOperator} {condThreshold}</span>
										<span class="inline-flex items-center gap-0.5 font-bold {evalCondition(num, condOperator, condThreshold) ? 'text-green-600' : 'text-red-600'}">
											→ {evalCondition(num, condOperator, condThreshold) ? 'True' : 'False'}
											{#if evalCondition(num, condOperator, condThreshold)}<Check class="w-3 h-3" />{:else}<X class="w-3 h-3" />{/if}
										</span>
									</div>
								{/if}
							{/if}
							{#if result.value != null && (condOperator === 'contains' || condOperator === '!contains')}
								{@const has = extractTestInput.includes(condThresholdString)}
								<div class="flex items-center gap-1 text-[9px]">
									<span class="text-muted-foreground">조건 평가:</span>
									<span class="font-mono">output {condOperator} "{condThresholdString}"</span>
									<span class="inline-flex items-center gap-0.5 font-bold {(condOperator === 'contains' ? has : !has) ? 'text-green-600' : 'text-red-600'}">
										→ {(condOperator === 'contains' ? has : !has) ? 'True' : 'False'}
										{#if (condOperator === 'contains' ? has : !has)}<Check class="w-3 h-3" />{:else}<X class="w-3 h-3" />{/if}
									</span>
								</div>
							{/if}
						{/if}
					</div>
				</details>

				<div class="space-y-1">
					<label class="text-[10px] font-medium text-muted-foreground uppercase">조건</label>
					<div class="flex items-center gap-2 border rounded px-2.5 py-2 bg-muted/30 flex-wrap">
						<span class="text-xs font-medium shrink-0">결과값</span>
						<select bind:value={condOperator} class="border rounded px-2 py-1 text-xs bg-background shrink-0">
							<optgroup label="숫자 비교">
								<option value=">">보다 크면 (&gt;)</option>
								<option value=">=">이상이면 (&gt;=)</option>
								<option value="<">보다 작으면 (&lt;)</option>
								<option value="<=">이하이면 (&lt;=)</option>
								<option value="==">같으면 (==)</option>
								<option value="!=">다르면 (!=)</option>
							</optgroup>
							<optgroup label="문자열 비교">
								<option value="contains">포함하면</option>
								<option value="!contains">포함하지 않으면</option>
							</optgroup>
						</select>
						{#if condOperator === 'contains' || condOperator === '!contains'}
							<input bind:value={condThresholdString} class="flex-1 border rounded px-2 py-1 text-xs bg-background"
								placeholder="start, stop, error 등" />
						{:else}
							<input type="number" step="any" bind:value={condThreshold}
								class="w-24 border rounded px-2 py-1 text-xs bg-background text-right" placeholder="90" />
						{/if}
					</div>
				</div>

				<div class="border rounded p-2 bg-muted/20 text-[9px] space-y-1">
					<p class="font-medium">예시:</p>
					<p><code class="font-mono">df /data | awk '{'{'}print $5{'}'}' | tail -1</code> → <code>87%</code> → 숫자 87 추출 → 87 > 90 ?</p>
					<p><code class="font-mono">cat /sys/class/power_supply/battery/status</code> → <code>Charging</code> → contains "stop" ?</p>
					<p><code class="font-mono">echo "test size : 34"</code> → 패턴 <code>: (\d+)</code> → 숫자 34 추출</p>
				</div>

			{:else}
				<!-- Metric 모드 (기존) -->
				<div class="space-y-1">
					<label class="text-[10px] font-medium text-muted-foreground uppercase">Metric</label>
				<select bind:value={condMetricKey} class="w-full border rounded px-2.5 py-1.5 text-xs bg-background">
					<option value="">메트릭 선택...</option>
					{#each metricCategories as cat}
						<optgroup label={cat.label}>
							{#each cat.items as m}
								<option value={m.key}>{m.label} ({m.displayUnit || m.rawUnit})</option>
							{/each}
						</optgroup>
					{/each}
				</select>
				{#if selectedMetric}
					<p class="text-[9px] text-muted-foreground">
						<span class="font-mono">{selectedMetric.key}</span>
						{#if selectedMetric.toDisplay !== 1}
							· 원본: {selectedMetric.rawUnit} → 표시: {selectedMetric.displayUnit}
						{/if}
					</p>
				{/if}
			</div>
			<div class="space-y-1">
				<label class="text-[10px] font-medium text-muted-foreground uppercase">조건</label>
				<div class="flex items-center gap-2 border rounded px-2.5 py-2 bg-muted/30 flex-wrap">
					<span class="text-xs font-medium shrink-0">{selectedMetric?.label ?? '(메트릭)'}</span>
					<select bind:value={condOperator} class="border rounded px-2 py-1 text-xs bg-background shrink-0">
						<option value=">">보다 크면 (&gt;)</option>
						<option value=">=">이상이면 (&gt;=)</option>
						<option value="<">보다 작으면 (&lt;)</option>
						<option value="<=">이하이면 (&lt;=)</option>
						<option value="==">같으면 (==)</option>
						<option value="!=">다르면 (!=)</option>
					</select>
					<input
						type="number"
						step="any"
						value={condDisplayValue}
						oninput={(e) => {
							const v = parseFloat((e.target as HTMLInputElement).value);
							if (!isNaN(v) && selectedMetric) {
								condThreshold = v * selectedMetric.toRaw;
							} else if (!isNaN(v)) {
								condThreshold = v;
							}
						}}
						class="w-24 border rounded px-2 py-1 text-xs bg-background text-right"
						placeholder="100"
					/>
					{#if selectedMetric?.displayUnit}
						<span class="text-[10px] text-muted-foreground shrink-0">{selectedMetric.displayUnit}</span>
					{/if}
				</div>
				{#if selectedMetric && selectedMetric.toDisplay !== 1}
					<p class="text-[9px] text-muted-foreground">
						= {condThreshold.toLocaleString()} {selectedMetric.rawUnit} (원본 값)
					</p>
				{/if}
			</div>
			<details class="text-[10px]">
					<summary class="text-muted-foreground cursor-pointer hover:text-foreground">고급: 직접 입력</summary>
					<div class="space-y-1 mt-1">
						<input bind:value={condMetricKey} class="w-full border rounded px-2 py-1 text-[10px] bg-background font-mono" placeholder="custom_metric_key" />
						<input type="number" bind:value={condThreshold} class="w-full border rounded px-2 py-1 text-[10px] bg-background font-mono" placeholder="raw threshold value" />
					</div>
				</details>
			{/if}
			{/if}
		</div>
		<Dialog.Footer class="gap-2">
			<button onclick={() => { condEditOpen = false; }} class="rounded-md border px-3 py-1.5 text-xs hover:bg-muted">취소</button>
			<button onclick={saveCondition}
				disabled={condRules.length > 0 ? condRules.length === 0 : (condSource === 'metric' ? !condMetricKey : !condShellCommand)}
				class="rounded-md bg-blue-600 text-white px-3 py-1.5 text-xs hover:bg-blue-700 disabled:opacity-50">저장</button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<!-- Loop Edit Dialog -->
<Dialog.Root bind:open={loopEditOpen}>
	<Dialog.Content class="max-w-sm">
		<Dialog.Header>
			<Dialog.Title class="text-sm">루프 설정</Dialog.Title>
		</Dialog.Header>
		<div class="space-y-3 py-3">
			<div class="space-y-1">
				<label class="text-[10px] font-medium text-muted-foreground uppercase">반복 횟수</label>
				<input
					type="number"
					bind:value={loopEditCount}
					min="1"
					class="w-full border rounded px-3 py-2 text-sm bg-background"
					onkeydown={(e) => { if (e.key === 'Enter') saveLoop(); }}
				/>
			</div>
			<div class="space-y-1">
				<label class="text-[10px] font-medium text-muted-foreground uppercase">포함할 Step</label>
				<div class="space-y-1 max-h-40 overflow-y-auto">
					{#each nodes.filter(n => n.type === 'step' || n.type === 'condition') as stepNode}
						{@const isChecked = loopEditMembers.has(stepNode.id)}
						{@const data = stepNode.data as StepNodeData}
						<label class="flex items-center gap-2 px-2 py-1 rounded cursor-pointer text-[10px] transition-colors
							{isChecked ? 'bg-blue-50' : 'hover:bg-muted/50'}">
							<input type="checkbox" checked={isChecked}
								onchange={() => {
									const next = new Set(loopEditMembers);
									if (next.has(stepNode.id)) next.delete(stepNode.id);
									else next.add(stepNode.id);
									loopEditMembers = next;
								}}
								class="size-3" />
							<span class="px-1 py-0.5 rounded text-[8px] {stepNode.type === 'condition' ? 'bg-amber-100 text-amber-700' : 'bg-blue-100 text-blue-700'}">
								{stepNode.type === 'condition' ? 'if' : (data as any).stepType ?? stepNode.type}
							</span>
							<span class="truncate">{stepNode.type === 'step' ? (data as any).label ?? '' : ''}</span>
						</label>
					{/each}
				</div>
				{#if loopEditMembers.size === 0}
					<p class="text-[9px] text-orange-600">최소 1개 step을 선택해주세요</p>
				{/if}
			</div>
		</div>
		<Dialog.Footer class="gap-2">
			<button onclick={() => { loopEditOpen = false; }} class="rounded-md border px-3 py-1.5 text-xs hover:bg-muted">취소</button>
			<button onclick={saveLoop} disabled={loopEditMembers.size === 0} class="rounded-md bg-blue-600 text-white px-3 py-1.5 text-xs hover:bg-blue-700 disabled:opacity-50">저장</button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
