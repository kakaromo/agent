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
	}

	let { stepsJson, loopsJson, activeJob = null }: Props = $props();

	const nodeTypes = { step: StepNode, condition: ConditionNode };

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
		if (!activeJob) return;

		const eventCount = activeJob.events?.length ?? 0;
		const jobState = activeJob.state;
		if (eventCount === lastEventCount && jobState === lastJobState) return;
		lastEventCount = eventCount;
		lastJobState = jobState;

		requestAnimationFrame(() => updateMiniNodes());
	});

	function updateMiniNodes() {
		if (!activeJob || nodes.length === 0) return;

		const events = activeJob.events;
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
					execStatus = activeJob!.state === 'failed' ? 'failed' : 'running';
					execLoopCurrent = loopCurrent;
					execLoopTotal = loopTotal;
				}
			} else if (activeJob!.state === 'completed') {
				execStatus = 'completed';
			} else if (activeJob!.state === 'failed') {
				execStatus = 'failed';
			} else if (activeJob!.state === 'running' && idx === 0) {
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
		if (!activeJob) return null;
		const latest = activeJob.events.at(-1);
		if (!latest?.message) return null;
		const stepMatch = latest.message.match(/step\s*\d+.*$/i);
		return stepMatch ? stepMatch[0] : null;
	});

	let repeatInfo = $derived.by(() => {
		if (!activeJob) return null;
		const latest = activeJob.events.at(-1);
		if (!latest) return null;
		const parsed = parseProgress(latest.message ?? '');
		if (parsed?.repeatIndex != null && parsed.repeatTotal != null) {
			return { current: parsed.repeatIndex, total: parsed.repeatTotal };
		}
		return null;
	});
</script>

<div class="border-2 border-blue-200 rounded-lg bg-blue-50/30 overflow-hidden relative" style="height: 350px;">
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
