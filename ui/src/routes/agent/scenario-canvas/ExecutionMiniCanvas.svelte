<script lang="ts">
	import { SvelteFlow, Background } from '@xyflow/svelte';
	import '@xyflow/svelte/dist/style.css';
	import { setContext } from 'svelte';
	import StepNode from './StepNode.svelte';
	import ConditionNode from './ConditionNode.svelte';
	import { protoToCanvas } from './serializer.js';
	import type { StepNodeData, ConditionNodeData } from './types.js';
	import type { ActiveJob } from '../types.js';
	import type { Node, Edge } from '@xyflow/svelte';

	interface Props {
		stepsJson: string;
		loopsJson?: string;
		activeJob?: ActiveJob | null;
		/**
		 * 이미 종료된 잡을 Results 에서 열 때 쓰는 fallback.
		 * activeJobs 에는 실행 중인 잡만 있으므로 과거 잡은 activeJob 이 null 이고,
		 * 그러면 스텝이 전부 무색으로 남는다. 서버 status 의 최종 상태 + 마지막
		 * 진행 메시지로 합성 job 을 만들어 동일 로직을 태운다.
		 */
		finishedState?: string | null;
		finishedMessages?: string[];
	}

	let { stepsJson, loopsJson, activeJob = null, finishedState = null, finishedMessages = [] }: Props = $props();

	// activeJob 우선, 없으면 종료된 잡 정보로 합성
	let effectiveJob = $derived.by<ActiveJob | null>(() => {
		if (activeJob) return activeJob;
		if (!finishedState || finishedState === 'running') return null;
		return {
			jobId: '', serverId: 0, serverName: '', type: 'scenario',
			deviceIds: [], createdAt: 0,
			events: finishedMessages.map(m => ({ message: m, state: finishedState })) as any,
			state: finishedState
		} as ActiveJob;
	});

	const nodeTypes = { step: StepNode, condition: ConditionNode };

	// 스텝 수에 따라 캔버스 높이를 늘린다. 고정 350px + fitView 조합이면 스텝이 많을 때
	// (예: 11 스텝 크롬 서핑 시나리오) 노드가 판독 불가 크기로 축소된다.
	// 노드 간격이 100px 이므로 그에 맞춰 늘리고, 화면을 다 잡아먹지 않게 상한을 둔다.
	let canvasHeight = $derived.by(() => {
		const n = nodes.filter(x => x.type === 'step' || x.type === 'condition').length;
		if (n === 0) return 350;
		return Math.min(760, Math.max(350, n * 78));
	});

	let nodes = $state<Node[]>([]);
	let edges = $state<Edge[]>([]);

	// 읽기 전용 — 편집 콜백 없음
	setContext('onEditNode', undefined);
	setContext('onDeleteNode', undefined);
	setContext('onMoveNode', undefined);
	setContext('onEditCondition', undefined);
	setContext('onEditLoopCount', undefined);

	// stepsJson → 캔버스 로드
	$effect(() => {
		try {
			const steps = JSON.parse(stepsJson);
			const loops = loopsJson ? JSON.parse(loopsJson) : [];
			const result = protoToCanvas(steps, loops);
			nodes = result.nodes;
			edges = result.edges;
		} catch {
			nodes = [];
			edges = [];
		}
	});

	// SSE → 노드 data에 직접 execStatus 설정
	// SSE 추적 — 이벤트 수가 바뀔 때만 업데이트
	let lastEventCount = 0;
	let lastJobState = '';

	$effect(() => {
		if (!effectiveJob) return;

		const eventCount = effectiveJob.events?.length ?? 0;
		const jobState = effectiveJob.state;
		if (eventCount === lastEventCount && jobState === lastJobState) return;
		lastEventCount = eventCount;
		lastJobState = jobState;

		requestAnimationFrame(() => updateMiniNodes());
	});

	function updateMiniNodes() {
		if (!effectiveJob || nodes.length === 0) return;

		const events = effectiveJob.events;
		const stepNodeIds = nodes.filter(n => n.type === 'step' || n.type === 'condition').map(n => n.id);
		if (stepNodeIds.length === 0) return;

		let parsedStepIndex: number | null = null;
		let loopCurrent: number | undefined;
		let loopTotal: number | undefined;

		if (events && events.length > 0) {
			for (let i = events.length - 1; i >= 0; i--) {
				const p = parseProgress(events[i].message ?? '');
				if (p && p.stepIndex != null) {
					parsedStepIndex = p.stepIndex;
					loopCurrent = p.loopIndex;
					loopTotal = p.loopTotal;
					break;
				}
			}

			// 진행 인덱스를 못 뽑았으면 명시적 중단 이벤트
			// (`step N ... failed: ...` / `step N ... cancelled`)에서 중단 스텝을 뽑는다.
			// 이렇게 하지 않으면 아래 fallback 이 모든 스텝을 칠한다(ScenarioCanvas 와 동일 버그).
			if (parsedStepIndex == null && (effectiveJob.state === 'failed' || effectiveJob.state === 'cancelled')) {
				for (let i = events.length - 1; i >= 0; i--) {
					const msg = events[i].message ?? '';
					if (/failed\s*:/i.test(msg) || /\bcancelled\b/i.test(msg)) {
						const m = msg.match(/[Ss]tep\s*(\d+)/);
						if (m) { parsedStepIndex = parseInt(m[1]); break; }
					}
				}
			}
		}

		nodes = nodes.map(n => {
			if (n.type !== 'step' && n.type !== 'condition') return n;
			const idx = stepNodeIds.indexOf(n.id);

			let execStatus: string | undefined;
			let execLoopCurrent: number | undefined;
			let execLoopTotal: number | undefined;

			if (parsedStepIndex != null) {
				if (idx < parsedStepIndex) {
					execStatus = 'completed';
				} else if (idx === parsedStepIndex) {
					// 잡 전체가 종료 상태면 마지막으로 진행 중이던 step 도 종료로 마킹
					// (그렇지 않으면 trace_stop 같은 마지막 step 이 'running' 으로 영원히 남음)
					if (effectiveJob.state === 'completed' || effectiveJob.state === 'partially_failed') {
						execStatus = 'completed';
					} else if (effectiveJob.state === 'failed') {
						execStatus = 'failed';
					} else if (effectiveJob.state === 'cancelled') {
						execStatus = 'cancelled';
					} else {
						execStatus = 'running';
					}
					execLoopCurrent = loopCurrent;
					execLoopTotal = loopTotal;
				} else if (effectiveJob.state === 'completed') {
					// 마지막 progress 이후 step 들도 잡이 끝났으면 completed
					execStatus = 'completed';
				}
			} else if (effectiveJob.state === 'completed') {
				execStatus = 'completed';
			} else if (effectiveJob.state === 'failed' || effectiveJob.state === 'partially_failed') {
				execStatus = 'failed';
			} else if (effectiveJob.state === 'cancelled') {
				execStatus = 'cancelled';
			} else if (effectiveJob.state === 'running' && idx === 0) {
				execStatus = 'running';
			}

			if (n.type === 'condition') {
				return { ...n, data: { ...n.data, execStatus } };
			}
			return {
				...n,
				data: { ...n.data, execStatus, execLoopCurrent, execLoopTotal }
			};
		});
	}

	function parseProgress(message: string) {
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

	let currentStepInfo = $derived.by(() => {
		if (!effectiveJob) return null;
		const latest = effectiveJob.events.at(-1);
		if (!latest?.message) return null;
		const stepMatch = latest.message.match(/step\s*\d+.*$/i);
		return stepMatch ? stepMatch[0] : null;
	});

	let repeatInfo = $derived.by(() => {
		if (!effectiveJob) return null;
		const latest = effectiveJob.events.at(-1);
		if (!latest) return null;
		const parsed = parseProgress(latest.message ?? '');
		if (parsed?.repeatIndex != null && parsed.repeatTotal != null) {
			return { current: parsed.repeatIndex, total: parsed.repeatTotal };
		}
		return null;
	});
</script>

<div class="border-2 border-blue-200 rounded-lg bg-blue-50/30 overflow-hidden relative" style="height: {canvasHeight}px;">
	<!-- 헤더 뱃지 -->
	<div class="absolute top-2 left-2 z-10 flex items-center gap-2">
		{#if repeatInfo}
			<span class="px-2 py-0.5 rounded-full text-[10px] font-medium bg-blue-600 text-white shadow">
				Repeat {repeatInfo.current}/{repeatInfo.total}
			</span>
		{/if}
		{#if currentStepInfo}
			<span class="px-2 py-0.5 rounded-full text-[10px] font-medium bg-blue-100 text-blue-700 shadow">
				{currentStepInfo}
			</span>
		{/if}
	</div>

	{#if nodes.length > 0}
		<SvelteFlow
			{nodes} {edges} {nodeTypes}
			fitView
			fitViewOptions={{ padding: 0.3 }}
			panOnDrag={true}
			zoomOnScroll={true}
			zoomOnPinch={true}
			zoomOnDoubleClick={false}
			nodesDraggable={false}
			nodesConnectable={false}
			elementsSelectable={false}
			class="bg-transparent"
		>
			<Background />
		</SvelteFlow>
	{:else}
		<div class="flex items-center justify-center h-full text-[10px] text-muted-foreground">
			시나리오 정보 없음
		</div>
	{/if}
</div>
