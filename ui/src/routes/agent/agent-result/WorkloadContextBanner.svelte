<script lang="ts">
	// job 상세 상단 "무엇이 돌았고 왜 이렇게 동작했나" 워크로드 컨텍스트 배너.
	// 모든 benchmark/scenario/trace/macro job 공통. 규칙 자동 해석 + 사용자 메모 오버라이드.
	import ActivityIcon from '@lucide/svelte/icons/activity';
	import LightbulbIcon from '@lucide/svelte/icons/lightbulb';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import InfoIcon from '@lucide/svelte/icons/info';
	import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
	import CircleCheckIcon from '@lucide/svelte/icons/circle-check';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import { toast } from 'svelte-sonner';
	import { updateWorkloadNote } from '$lib/api/agent.js';
	import {
		describeWorkload,
		deriveInsights,
		type WorkloadInsight,
		type InsightTone
	} from './workloadContext.js';

	interface Props {
		jobId: string | null;
		metrics: Record<string, number>;
		executionConfig?: { steps?: any[]; loops?: any[] } | null;
		/** DB 에 저장된 사용자 메모 (없으면 규칙 자동 해석만) */
		workloadNote?: string | null;
	}

	let { jobId, metrics, executionConfig = null, workloadNote = null }: Props = $props();

	const whatRan = $derived(describeWorkload(executionConfig?.steps, executionConfig?.loops));
	const insights = $derived<WorkloadInsight[]>(deriveInsights(metrics, whatRan));

	// ── 메모 편집 상태 ──
	let editing = $state(false);
	let draft = $state('');
	let saving = $state(false);
	// 로컬로 관리하는 현재 메모 (저장 성공 시 갱신)
	let localNote = $state<string | null>(null);
	const effectiveNote = $derived(localNote !== null ? localNote : (workloadNote ?? ''));

	function startEdit() {
		draft = effectiveNote;
		editing = true;
	}
	function cancelEdit() {
		editing = false;
	}
	async function save() {
		if (!jobId) return;
		saving = true;
		try {
			await updateWorkloadNote(jobId, draft.trim());
			localNote = draft.trim();
			editing = false;
			toast.success(draft.trim() ? '워크로드 메모 저장됨' : '메모 삭제됨 (자동 해석으로 복귀)');
		} catch (e) {
			toast.error('저장 실패: ' + (e instanceof Error ? e.message : String(e)));
		} finally {
			saving = false;
		}
	}

	function toneClass(tone: InsightTone): string {
		switch (tone) {
			case 'good': return 'text-green-700 dark:text-green-400';
			case 'warn': return 'text-amber-700 dark:text-amber-400';
			default: return 'text-muted-foreground';
		}
	}
</script>

<div class="border rounded-md overflow-hidden bg-muted/20">
	<!-- 무엇이 돌았나 -->
	<div class="flex items-start gap-2 px-3 py-2 border-b bg-muted/30">
		<ActivityIcon class="size-3.5 mt-0.5 shrink-0 text-blue-600" />
		<div class="min-w-0">
			<div class="text-[10px] font-semibold text-muted-foreground">이 Job 은 무엇이었나</div>
			<div class="text-xs font-medium leading-snug">{whatRan.oneLine}</div>
			{#if whatRan.steps.length > 1}
				<div class="mt-1 flex flex-wrap gap-1">
					{#each whatRan.steps as st (st.index)}
						<span class="inline-flex items-center gap-1 rounded border bg-background px-1.5 py-0.5 text-[9px]">
							<span class="text-muted-foreground">S{st.index}</span>
							<span class="font-medium">{st.title}</span>
						</span>
					{/each}
				</div>
			{/if}
		</div>
	</div>

	<!-- 왜 이렇게 동작했나 (자동 해석) -->
	<div class="px-3 py-2 space-y-1">
		<div class="flex items-center gap-1.5">
			<LightbulbIcon class="size-3.5 text-amber-500" />
			<span class="text-[10px] font-semibold text-muted-foreground">수치 해석 (자동)</span>
		</div>
		<ul class="space-y-1 pl-0.5">
			{#each insights as ins}
				<li class="flex items-start gap-1.5 text-[11px] leading-snug {toneClass(ins.tone)}">
					{#if ins.tone === 'warn'}
						<TriangleAlertIcon class="size-3 mt-0.5 shrink-0" />
					{:else if ins.tone === 'good'}
						<CircleCheckIcon class="size-3 mt-0.5 shrink-0" />
					{:else}
						<InfoIcon class="size-3 mt-0.5 shrink-0 opacity-60" />
					{/if}
					<span>{ins.text}</span>
				</li>
			{/each}
		</ul>
	</div>

	<!-- 사용자 메모 (오버라이드/보강) -->
	<div class="px-3 py-2 border-t bg-background/40">
		<div class="flex items-center justify-between mb-1">
			<div class="flex items-center gap-1.5">
				<PencilIcon class="size-3 text-muted-foreground" />
				<span class="text-[10px] font-semibold text-muted-foreground">내 메모 (이 job 의 맥락 — cold/warm, 조건 등)</span>
			</div>
			{#if !editing}
				<button
					onclick={startEdit}
					disabled={!jobId}
					class="text-[10px] px-2 py-0.5 rounded border hover:bg-muted disabled:opacity-40"
				>
					{effectiveNote ? '수정' : '메모 추가'}
				</button>
			{/if}
		</div>

		{#if editing}
			<textarea
				bind:value={draft}
				rows="3"
				placeholder="예) 삼성 노트 AI 요약 1회, warm start(SM-S938N). READ 작은 건 모델이 이미 로드돼서. DISCARD 큰 건 요약 후 캐시 정리."
				class="w-full text-[11px] rounded border bg-background px-2 py-1.5 resize-y focus:outline-none focus:ring-1 focus:ring-primary"
			></textarea>
			<div class="flex items-center gap-1.5 mt-1">
				<button
					onclick={save}
					disabled={saving}
					class="text-[10px] px-2.5 py-0.5 rounded bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50 inline-flex items-center gap-1"
				>
					{#if saving}<LoaderIcon class="size-3 animate-spin" />{/if}
					저장
				</button>
				<button onclick={cancelEdit} class="text-[10px] px-2 py-0.5 rounded border hover:bg-muted">취소</button>
				<span class="text-[9px] text-muted-foreground ml-1">비우고 저장하면 자동 해석으로 되돌아갑니다</span>
			</div>
		{:else if effectiveNote}
			<p class="text-[11px] leading-snug whitespace-pre-wrap text-foreground/90">{effectiveNote}</p>
		{:else}
			<p class="text-[10px] text-muted-foreground/70 italic">
				아직 메모 없음 — 위 자동 해석 외에 이 job 만의 조건이 있으면 남겨두세요.
			</p>
		{/if}
	</div>
</div>
