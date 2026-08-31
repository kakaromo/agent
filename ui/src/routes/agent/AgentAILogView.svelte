<script lang="ts">
	/**
	 * on-device AI (LLM) 측정 — logcat 패턴 기반 TTFT/TPOT.
	 *
	 * 3단계 흐름을 그대로 화면으로 옮긴다:
	 *   ① 탐색  — 런타임의 로그 형식을 모를 때 후보 태그를 찾는다
	 *   ② 프로파일 — 확인된 패턴을 저장해 재사용한다
	 *   ③ 측정  — 저장된 패턴으로 지표를 뽑는다
	 *
	 * ⚠ 이 화면의 설계 원칙: **모르는 것을 아는 척하지 않는다.**
	 * 탐색 결과가 빈약하면 빈약하다고, 매칭이 0건이면 왜 0건인지 화면에 그대로 쓴다.
	 * 측정 도구에서 "그럴듯하게 틀린 값" 이 가장 나쁘기 때문이다.
	 */
	import { toast } from 'svelte-sonner';
	import {
		fetchAILogProfiles, createAILogProfile, updateAILogProfile, deleteAILogProfile,
		exploreLogcat, parseLogcat,
		exploreMarkers, parseMarkers,
		type AILogProfile, type AILogPatterns,
		type LogcatExploreResult, type LogcatParseResult,
		type MarkerExploreResult, type MarkerPatterns
	} from '$lib/api/agent.js';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import PencilIcon from '@lucide/svelte/icons/pencil';
	import SearchIcon from '@lucide/svelte/icons/search';
	import PlayIcon from '@lucide/svelte/icons/play';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import AlertTriangleIcon from '@lucide/svelte/icons/triangle-alert';
	import InfoIcon from '@lucide/svelte/icons/info';
	import CheckIcon from '@lucide/svelte/icons/check';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';

	interface Props {
		/** 최근 잡의 logcat job id (있으면 탐색/측정 대상 기본값). */
		logcatJobId?: string | null;
	}
	let { logcatJobId = null }: Props = $props();

	type Stage = 'explore' | 'profiles' | 'measure';
	let stage = $state<Stage>('explore');

	// ── 지표 소스 ──
	//
	// ⚠ 두 경로가 다 필요한 이유: 런타임이 **stderr 로 뱉으면 logcat 에 안 남는다**
	// (llama.cpp `llama_print_timings()`). trace_marker 는 파일 write 라 그 제약이 없고,
	// IO 트레이스와 같은 clock 이라 축 변환도 필요 없다.
	//
	// ⚠ 두 소스는 패턴 구조가 다르다(marks/series vs counters/sections). 프로파일에
	// source 가 박혀 있고 서버가 파싱 전에 막지만, 화면도 소스에 맞는 것만 보여준다.
	type Source = 'logcat' | 'marker';
	let source = $state<Source>('logcat');
	/** marker 는 trace 잡의 trace.log 를 읽는다 (logcat job 이 아니다). */
	let traceJobId = $state('');
	let markerRes = $state<MarkerExploreResult | null>(null);
	let expandedName = $state<string | null>(null);

	// ── 공통 입력 (로그 출처) ──
	let sourceJobId = $state(logcatJobId ?? '');
	let sourcePath = $state('');

	function sourcePayload() {
		const jid = sourceJobId.trim();
		if (jid) return { jobId: jid };
		const p = sourcePath.trim();
		if (p) return { path: p };
		return null;
	}

	// ── ① 탐색 ──
	let exploring = $state(false);
	let exploreRes = $state<LogcatExploreResult | null>(null);
	let explorePath = $state('');
	// 구간 지정 — 유휴/추론을 나눠주면 "추론 때만 나타난 태그" 를 가려낼 수 있다.
	let idleFrom = $state('');
	let idleTo = $state('');
	let runFrom = $state('');
	let runTo = $state('');
	let expandedTag = $state<string | null>(null);

	const numOf = (s: string) => { const v = parseFloat(s); return isNaN(v) ? undefined : v; };

	async function runExplore() {
		if (source === 'marker') { await runExploreMarker(); return; }
		const src = sourcePayload();
		if (!src) { toast.error('logcat job id 또는 파일 경로가 필요합니다'); return; }
		exploring = true;
		exploreRes = null;
		try {
			const num = numOf;
			const { path, result } = await exploreLogcat({
				...src,
				idleFrom: num(idleFrom), idleTo: num(idleTo),
				runFrom: num(runFrom), runTo: num(runTo)
			});
			explorePath = path;
			exploreRes = result;
			if (result.candidates.length === 0) toast.warning('후보를 찾지 못했습니다');
		} catch (e) {
			toast.error(e instanceof Error ? e.message : '탐색 실패');
		} finally {
			exploring = false;
		}
	}

	async function runExploreMarker() {
		const tid = traceJobId.trim();
		if (!tid) { toast.error('trace job id 가 필요합니다'); return; }
		exploring = true;
		markerRes = null;
		try {
			const { path, result } = await exploreMarkers({
				traceJobId: tid,
				idleFrom: numOf(idleFrom), idleTo: numOf(idleTo),
				runFrom: numOf(runFrom), runTo: numOf(runTo)
			});
			explorePath = path;
			markerRes = result;
			if (result.candidates.length === 0) toast.warning('후보를 찾지 못했습니다');
		} catch (e) {
			toast.error(e instanceof Error ? e.message : '탐색 실패');
		} finally {
			exploring = false;
		}
	}

	/** marker 후보를 프로파일 초안으로 — counters/sections 구조로 넣는다. */
	function seedProfileFromMarker(c: MarkerCandidateLike) {
		// ⚠ **초안을 깨끗이 비우고 시작한다.** Cancel 은 form 을 안 지우므로, 편집하다
		// 취소한 뒤 여기로 오면 직전 프로파일의 이름·설명·soc 와 **죽은 logcat 패턴**
		// (marks/series)이 그대로 섞여 저장된다. marker 검증기는 counters/sections 만
		// 보므로 그 잔재를 못 잡고 조용히 통과시킨다.
		// runtime/soc 은 사용자가 고른 값이라 유지한다 (위 seedProfileFromTag 와 같은 이유).
		form = { ...form, name: `${c.name} profile`, description: '',
			source: 'marker', patternsJson: '{}' };
		editingId = null;
		const cur: MarkerPatterns = {};
		const key = c.name.replace(/[^A-Za-z0-9_]+/g, '_').replace(/^_+|_+$/g, '').toLowerCase() || 'metric';
		if (c.kind === 'counter') {
			cur.counters = [...(cur.counters ?? []), { key, name: c.name }];
		} else {
			cur.sections = [...(cur.sections ?? []), { key, name: c.name }];
		}
		form.patternsJson = JSON.stringify(cur, null, 2);
		stage = 'profiles';
		editOpen = true;
		toast.info(`"${c.name}" 을 초안에 넣었습니다.`, { description: c.samples[0]?.slice(0, 120) });
	}
	type MarkerCandidateLike = { name: string; kind: string; samples: string[] };

	/** 후보를 프로파일 초안으로 옮긴다 — 사람이 확인한 것만 저장된다. */
	function seedProfileFromTag(tag: string, samples: string[]) {
		// ⚠ marker 쪽과 같은 이유 — 초안을 비우고 시작한다 (Cancel 이 form 을 안 지운다).
		// ⚠ **examplePatterns 를 쓰면 안 된다.** 그럴듯하지만 **가짜인 정규식**
		// (`TTFT ([0-9.]+) ms` 등)이 초안에 박히는데, 클라이언트·서버 검증을 모두
		// 통과하므로 그대로 저장하면 **어디에도 안 맞는 프로파일**이 조용히 만들어진다.
		// 토스트는 "원문을 보고 정규식을 채우세요" 라고 말하는데 이미 채워져 있는 셈이다.
		// ⚠ runtime/soc 은 사용자가 고른 값이라 유지한다 — 초기화하면 조회 필터가
		// 바뀌어 저장한 프로파일이 목록에서 사라진 것처럼 보인다.
		form = { ...form, name: `${tag} profile`, description: '',
			source: 'logcat', patternsJson: JSON.stringify(emptyPatterns, null, 2) };
		editingId = null;
		const cur = parsePatterns(form.patternsJson);
		cur.tags = [tag];
		form.patternsJson = JSON.stringify(cur, null, 2);
		stage = 'profiles';
		editOpen = true;
		toast.info(`태그 "${tag}" 를 초안에 넣었습니다. 원문을 보고 정규식을 채우세요.`, {
			description: samples[0]?.slice(0, 120)
		});
	}

	// ── ② 프로파일 ──
	let profiles = $state<AILogProfile[]>([]);
	let loadingProfiles = $state(false);
	let editOpen = $state(false);
	let editingId = $state<number | null>(null);
	let saving = $state(false);
	let confirmOpen = $state(false);
	let confirmDesc = $state('');
	let confirmAction = $state<() => Promise<void>>(async () => {});

	const emptyPatterns: AILogPatterns = { tags: [], marks: [], series: [] };
	let form = $state({
		name: '', description: '', runtime: 'qnn', soc: '',
		// source — 저장 시 서버가 이 값으로 검증기를 고른다 (marks/series vs counters/sections).
		source: 'logcat' as Source,
		patternsJson: JSON.stringify(emptyPatterns, null, 2)
	});

	function markerPatternIssues(): string[] {
		const out: string[] = [];
		let p: MarkerPatterns;
		try { p = JSON.parse(form.patternsJson) as MarkerPatterns; }
		catch (e) { return [`JSON 형식이 아닙니다: ${e instanceof Error ? e.message : ''}`]; }
		const counters = p.counters ?? [], sections = p.sections ?? [];
		if (counters.length === 0 && sections.length === 0)
			out.push('counters 또는 sections 중 최소 하나는 있어야 합니다 (없으면 매칭이 항상 0건입니다)');
		const seen = new Set<string>();
		for (const m of [...counters, ...sections]) {
			if (!m.key) out.push('key 가 빈 항목이 있습니다');
			else if (seen.has(m.key)) out.push(`key 중복: ${m.key} — 나중 것이 앞의 것을 덮어씁니다`);
			else seen.add(m.key);
			if (!m.name && !m.regex) {
				out.push(`${m.key || '(이름없음)'}: name 또는 regex 중 하나는 있어야 합니다`);
				continue;
			}
			if (m.regex) {
				try { new RegExp(m.regex); }
				catch { out.push(`${m.key}: 정규식이 잘못됐습니다`); }
			}
		}
		return out;
	}

	function parsePatterns(s: string): AILogPatterns {
		try { return JSON.parse(s) as AILogPatterns; } catch { return { ...emptyPatterns }; }
	}

	/** 저장 전에 화면에서 먼저 잡아준다 — 서버도 막지만 왕복 전에 알려주는 편이 낫다. */
	const patternIssues = $derived.by(() => {
		const out: string[] = [];
		// ⚠ marker 는 검증 항목이 다르다 — `C|이름|값` 이라 **캡처 그룹이 필요 없고**,
		// 대신 "무엇을 찾을지"(name 또는 regex)가 있어야 한다.
		if (form.source === 'marker') return markerPatternIssues();
		let p: AILogPatterns;
		try { p = JSON.parse(form.patternsJson) as AILogPatterns; }
		catch (e) { return [`JSON 형식이 아닙니다: ${e instanceof Error ? e.message : ''}`]; }
		const marks = p.marks ?? [], series = p.series ?? [];
		if (marks.length === 0 && series.length === 0)
			out.push('marks 또는 series 중 최소 하나는 있어야 합니다 (없으면 매칭이 항상 0건입니다)');
		const seen = new Set<string>();
		for (const m of [...marks, ...series]) {
			if (!m.key) out.push('key 가 빈 항목이 있습니다');
			else if (seen.has(m.key)) out.push(`key 중복: ${m.key} — 나중 것이 앞의 것을 덮어씁니다`);
			else seen.add(m.key);
			if (!m.regex) { out.push(`${m.key || '(이름없음)'}: regex 가 없습니다`); continue; }
			try { new RegExp(m.regex); }
			catch { out.push(`${m.key}: 정규식이 잘못됐습니다`); }
		}
		for (const s of series) {
			if (s.regex && !s.regex.includes('('))
				out.push(`${s.key}: 값을 뽑을 캡처 그룹 () 이 없습니다 (예: \`TTFT ([0-9.]+) ms\`)`);
		}
		return out;
	});

	async function loadProfiles() {
		loadingProfiles = true;
		try { profiles = await fetchAILogProfiles(); }
		catch (e) { toast.error(e instanceof Error ? e.message : '프로파일 로드 실패'); }
		finally { loadingProfiles = false; }
	}

	function openCreate() {
		editingId = null;
		// ⚠ source 를 빠뜨리면 undefined 가 되어 marker 패턴이 logcat 규칙으로 검증되고
		// (거부됨), 저장 시엔 서버가 logcat 으로 정규화해 **측정 시 0건**이 된다.
		// 현재 고른 출처를 그대로 물려주고 예시도 그쪽 형식으로 넣는다.
		form = { name: '', description: '', runtime: 'qnn', soc: '', source,
			patternsJson: JSON.stringify(
				source === 'marker' ? exampleMarkerPatterns : examplePatterns, null, 2) };
		editOpen = true;
	}

	function openEdit(p: AILogProfile) {
		editingId = p.id;
		let pretty = p.patternsJson;
		try { pretty = JSON.stringify(JSON.parse(p.patternsJson), null, 2); } catch { /* 원문 유지 */ }
		form = { name: p.name, description: p.description ?? '', runtime: p.runtime,
			soc: p.soc ?? '', source: (p.source === 'marker' ? 'marker' : 'logcat') as Source,
			patternsJson: pretty };
		editOpen = true;
	}

	async function saveProfile() {
		if (!form.name.trim() || !form.runtime.trim()) { toast.error('이름과 runtime 은 필수입니다'); return; }
		saving = true;
		try {
			const data = { name: form.name.trim(), description: form.description.trim(),
				runtime: form.runtime.trim(), soc: form.soc.trim(),
				// ⚠ 반드시 보낸다 — 빠뜨리면 marker 패턴이 logcat 으로 저장돼 측정 시 0건이 된다.
				source: form.source, patternsJson: form.patternsJson };
			if (editingId == null) await createAILogProfile(data);
			else await updateAILogProfile(editingId, data);
			toast.success('저장했습니다');
			editOpen = false;
			await loadProfiles();
		} catch (e) {
			toast.error(e instanceof Error ? e.message : '저장 실패');
		} finally { saving = false; }
	}

	function askDelete(p: AILogProfile) {
		confirmDesc = `프로파일 "${p.name}" 을 삭제할까요?`;
		confirmAction = async () => {
			await deleteAILogProfile(p.id);
			toast.success('삭제했습니다');
			await loadProfiles();
		};
		confirmOpen = true;
	}

	const examplePatterns: AILogPatterns = {
		tags: ['Genie', 'QnnHtp'],
		marks: [{ key: 'load_start', regex: 'model load start' }],
		series: [
			{ key: 'ttft_ms', regex: 'TTFT ([0-9.]+) ms', unit: 'ms' },
			{ key: 'tpot_ms', regex: 'decode ([0-9.]+) ms/tok', unit: 'ms' }
		]
	};

	/** marker 예시 — `C|이름|값` 이라 캡처 그룹이 없다. 이름만 적으면 된다. */
	const exampleMarkerPatterns = {
		counters: [
			{ key: 'ttft_ms', name: 'llm.ttft_ms', unit: 'ms' },
			{ key: 'tpot_ms', name: 'decode_ms_per_token', unit: 'ms' }
		],
		sections: [{ key: 'prefill', name: 'prefill' }]
	};

	// ── ③ 측정 ──
	let measuring = $state(false);
	let parseRes = $state<LogcatParseResult | null>(null);
	let selectedProfileId = $state<number | null>(null);

	async function runMeasure() {
		if (selectedProfileId == null) { toast.error('프로파일을 고르세요'); return; }
		// marker 는 trace 잡을, logcat 은 logcat 잡/경로를 쓴다.
		let call: () => Promise<{ result: LogcatParseResult }>;
		if (source === 'marker') {
			const tid = traceJobId.trim();
			if (!tid) { toast.error('trace job id 가 필요합니다'); return; }
			call = () => parseMarkers({ traceJobId: tid, profileId: selectedProfileId! });
		} else {
			const src = sourcePayload();
			if (!src) { toast.error('logcat job id 또는 파일 경로가 필요합니다'); return; }
			call = () => parseLogcat({ ...src, profileId: selectedProfileId! });
		}
		measuring = true;
		parseRes = null;
		try {
			const { result } = await call();
			parseRes = result;
			if (result.totalHits === 0) toast.error('매칭 0건 — 아래 진단을 확인하세요');
			else if (result.partial) toast.warning('일부 패턴만 맞았습니다');
		} catch (e) {
			toast.error(e instanceof Error ? e.message : '측정 실패');
		} finally { measuring = false; }
	}

	const seriesList = $derived(parseRes ? Object.values(parseRes.series).filter((s) => s.count > 0) : []);

	function fmt(n: number): string {
		if (!isFinite(n)) return '-';
		return Math.abs(n) >= 100 ? n.toFixed(0) : n.toFixed(2);
	}

	$effect(() => { if (stage === 'profiles' || stage === 'measure') { if (profiles.length === 0) loadProfiles(); } });
</script>

<div class="flex h-full flex-col gap-3 overflow-hidden p-3">
	<!-- 단계 탭 -->
	<div class="flex shrink-0 items-center gap-1 border-b pb-2">
		{#each [['explore', '① 탐색'], ['profiles', '② 프로파일'], ['measure', '③ 측정']] as [key, label] (key)}
			<button
				class="rounded px-3 py-1.5 text-xs font-medium transition-colors {stage === key
					? 'bg-primary text-primary-foreground'
					: 'text-muted-foreground hover:bg-muted'}"
				onclick={() => (stage = key as Stage)}
			>{label}</button>
		{/each}
		<div class="ml-auto text-[11px] text-muted-foreground">
			on-device AI (TTFT / TPOT)
		</div>
	</div>

	<!-- 지표 출처 (모든 단계 공통) -->
	<div class="shrink-0 rounded border bg-muted/30 p-2">
		<div class="mb-1.5 flex items-center gap-1.5 text-[11px] font-medium text-muted-foreground">
			<InfoIcon class="size-3" /> 지표 출처
		</div>
		<div class="mb-1.5 flex items-center gap-1">
			{#each [['logcat', 'logcat'], ['marker', 'trace_marker']] as [key, label] (key)}
				<button
					class="rounded px-2 py-0.5 text-[11px] {source === key
						? 'bg-primary text-primary-foreground'
						: 'bg-muted text-muted-foreground hover:bg-muted/70'}"
					onclick={() => (source = key as Source)}
				>{label}</button>
			{/each}
		</div>
		{#if source === 'marker'}
			<div class="flex flex-wrap items-center gap-2">
				<input
					class="h-7 w-72 rounded border bg-background px-2 text-xs font-mono"
					placeholder="trace job id"
					bind:value={traceJobId}
				/>
			</div>
			<p class="mt-1 text-[10px] text-muted-foreground">
				trace 수집의 <code class="rounded bg-muted px-1">trace.log</code> 에서 읽습니다 —
				런타임이 <code class="rounded bg-muted px-1">ATrace_setCounter()</code> 로 찍은 값입니다.
				<strong>logcat 에 안 남는 경우</strong>(런타임이 stderr 로 출력)에도 여기엔 남을 수 있고,
				IO 트레이스와 시각 축이 같아 구간과 바로 겹쳐 볼 수 있습니다.
			</p>
		{:else}
			<div class="flex flex-wrap items-center gap-2">
				<input
					class="h-7 w-64 rounded border bg-background px-2 text-xs"
					placeholder="logcat job id"
					bind:value={sourceJobId}
				/>
				<span class="text-[11px] text-muted-foreground">또는</span>
				<input
					class="h-7 flex-1 min-w-48 rounded border bg-background px-2 text-xs font-mono"
					placeholder="logcat.log 파일 경로 (수집 폴더 안)"
					bind:value={sourcePath}
				/>
			</div>
			<p class="mt-1 text-[10px] text-muted-foreground">
				시나리오 잡 파라미터에 <code class="rounded bg-muted px-1">logcat=on</code> 을 넣으면 수집됩니다.
				태그를 아직 모르면 <code class="rounded bg-muted px-1">logcat_tags</code> 를 비워 넓게 받으세요.
			</p>
		{/if}
	</div>

	<!-- ① 탐색 -->
	{#if stage === 'explore'}
		<div class="flex min-h-0 flex-1 flex-col gap-2 overflow-hidden">
			<div class="shrink-0 rounded border p-2">
				<p class="mb-2 text-[11px] text-muted-foreground">
					런타임이 어떤 태그·문구로 찍는지 모를 때 후보를 찾습니다.
					<strong>유휴 구간</strong>을 함께 지정하면 "추론 때만 나타난 태그" 를 가려낼 수 있어
					정확도가 크게 올라갑니다 (벤더 이름을 몰라도 걸립니다).
				</p>
				<p class="mb-2 text-[11px] text-amber-600 dark:text-amber-500">
					⚠ 시각은 <strong>로그 파일에 찍힌 값 그대로</strong> 넣으세요 (잡 수집은 epoch =
					UNIX 초). 아래 후보의 원문 샘플 앞머리에 보이는 숫자와 같은 축입니다 — trace
					차트의 상대 시각을 그대로 넣으면 엉뚱한 구간이 잡힙니다.
				</p>
				<div class="flex flex-wrap items-end gap-2">
					{#each [['유휴 시작', idleFrom, (v: string) => (idleFrom = v)], ['유휴 끝', idleTo, (v: string) => (idleTo = v)], ['추론 시작', runFrom, (v: string) => (runFrom = v)], ['추론 끝', runTo, (v: string) => (runTo = v)]] as [label, val, set] (label)}
						<div class="flex flex-col gap-0.5">
							<span class="text-[10px] text-muted-foreground">{label}</span>
							<input
								class="h-7 w-28 rounded border bg-background px-2 text-xs font-mono"
								placeholder="로그 시각(초)"
								value={val as string}
								oninput={(e) => (set as (v: string) => void)(e.currentTarget.value)}
							/>
						</div>
					{/each}
					<button
						class="ml-auto flex h-7 items-center gap-1 rounded bg-primary px-3 text-xs font-medium text-primary-foreground disabled:opacity-50"
						onclick={runExplore}
						disabled={exploring}
					>
						{#if exploring}<LoaderIcon class="size-3 animate-spin" />{:else}<SearchIcon class="size-3" />{/if}
						탐색
					</button>
				</div>
			</div>

			{#if source === 'marker' && markerRes}
				<div class="min-h-0 flex-1 overflow-auto rounded border">
					<div class="sticky top-0 z-10 border-b bg-background p-2 text-[11px]">
						<span class="text-muted-foreground">
							marker {markerRes.markerLines.toLocaleString()}줄 · 이름 {markerRes.distinctNames.toLocaleString()}종
						</span>
						{#if explorePath}
							<span class="ml-2 font-mono text-[10px] text-muted-foreground">{explorePath}</span>
						{/if}
					</div>

					{#if markerRes.weakOnly}
						<!-- ⚠ logcat 과 같은 경고. 목록이 있다는 것만으로 답이 있다고 읽히면 안 된다. -->
						<div class="m-2 rounded border border-amber-500/50 bg-amber-500/10 p-2 text-[11px]">
							<div class="flex items-center gap-1 font-medium text-amber-700 dark:text-amber-400">
								<AlertTriangleIcon class="size-3" /> LLM 고유 신호가 하나도 없습니다
							</div>
							<div class="mt-0.5 text-[10px] text-muted-foreground">
								아래 후보는 전부 무관한 시스템 카운터일 수 있습니다. 값 범위와 원문을 보고
								판단하세요 — <strong>토큰 단위(tok/s, ms/tok)가 안 보이면 LLM 이 아닐 가능성이 높습니다.</strong>
							</div>
						</div>
					{/if}
					{#each markerRes.diagnosis as d, i (i)}
						<div class="mx-2 mt-1 text-[10px] text-muted-foreground">· {d}</div>
					{/each}

					{#each markerRes.candidates as c, i (i)}
						<div class="border-b px-2 py-1.5 last:border-b-0">
							<div class="flex items-center gap-2">
								<button
									class="flex items-center gap-1 text-left text-xs font-medium hover:underline"
									onclick={() => (expandedName = expandedName === c.name ? null : c.name)}
								>
									<ChevronDownIcon class="size-3 {expandedName === c.name ? '' : '-rotate-90'}" />
									<span class="font-mono">{c.name}</span>
								</button>
								<span class="rounded bg-muted px-1 text-[10px] text-muted-foreground">{c.kind}</span>
								{#if c.llmSignal}
									<span class="rounded bg-emerald-500/15 px-1 text-[10px] text-emerald-700 dark:text-emerald-400">LLM 신호</span>
								{/if}
								{#if c.onlyDuringRun}
									<span class="rounded bg-sky-500/15 px-1 text-[10px] text-sky-700 dark:text-sky-400">추론 구간에만</span>
								{/if}
								<span class="ml-auto text-[10px] text-muted-foreground">
									{c.count.toLocaleString()}회
									{#if c.hasValue} · {c.min}~{c.max}{/if}
								</span>
							</div>
							{#if expandedName === c.name}
								<div class="mt-1 space-y-1 rounded bg-muted/40 p-1.5">
									<div class="text-[10px] text-muted-foreground">원문 샘플 — 이 줄을 보고 판단하세요</div>
									{#each c.samples as sm, si (si)}
										<pre class="overflow-x-auto whitespace-pre-wrap break-all rounded bg-background/70 p-1.5 text-[10px] font-mono leading-relaxed">{sm}</pre>
									{/each}
									<button
										class="flex h-6 items-center gap-1 rounded border px-2 text-[10px] hover:bg-muted"
										onclick={() => seedProfileFromMarker(c)}
									>
										<PlusIcon class="size-3" /> 이 이름으로 프로파일 만들기
									</button>
								</div>
							{/if}
						</div>
					{/each}
				</div>
			{:else if source !== 'marker' && exploreRes}
				<div class="min-h-0 flex-1 overflow-auto rounded border">
					<!-- 요약 -->
					<div class="sticky top-0 z-10 border-b bg-background p-2 text-[11px]">
						<span class="text-muted-foreground">
							{exploreRes.totalLines.toLocaleString()}줄 수집 ·
							{exploreRes.parsedLines.toLocaleString()}줄 파싱 ·
							태그 {exploreRes.distinctTags.toLocaleString()}개 ·
							후보 <strong class="text-foreground">{exploreRes.candidates.length}</strong>개
						</span>
						{#if explorePath}
							<span class="ml-2 font-mono text-[10px] text-muted-foreground">{explorePath}</span>
						{/if}
					</div>

					<!-- ⚠ 약한 신호 경고 — 목록이 있다는 것만으로 답이 있다고 읽히면 안 된다 -->
					{#if exploreRes.weakOnly}
						<div class="m-2 rounded border border-amber-500/50 bg-amber-500/10 p-2">
							<div class="flex items-center gap-1.5 text-xs font-semibold text-amber-700 dark:text-amber-400">
								<AlertTriangleIcon class="size-3.5" /> LLM 고유 신호가 없습니다
							</div>
							<p class="mt-1 text-[11px] leading-relaxed text-amber-800/90 dark:text-amber-300/90">
								아래 후보는 <strong>낱말만 겹치는 다른 온디바이스 ML</strong> 일 수 있습니다.
								음성 wakeword·얼굴인식·사진 분류도 똑같이 "모델 로드" 와 "추론" 을 찍습니다.
								원문(샘플)을 반드시 눈으로 확인하세요 — <strong>토큰 단위가 안 보이면 LLM 이 아닐 가능성이 높습니다.</strong>
							</p>
						</div>
					{/if}

					{#if exploreRes.diagnosis.length > 0}
						<div class="m-2 rounded border bg-muted/40 p-2">
							{#each exploreRes.diagnosis as d (d)}
								<div class="text-[11px] leading-relaxed text-muted-foreground">{d}</div>
							{/each}
						</div>
					{/if}

					<!-- 후보 목록 -->
					{#each exploreRes.candidates as c (c.tag)}
						<div class="border-b last:border-b-0">
							<button
								class="flex w-full items-center gap-2 px-2 py-1.5 text-left hover:bg-muted/50"
								onclick={() => (expandedTag = expandedTag === c.tag ? null : c.tag)}
							>
								<ChevronDownIcon class="size-3 shrink-0 text-muted-foreground transition-transform {expandedTag === c.tag ? '' : '-rotate-90'}" />
								<span class="font-mono text-xs font-medium">{c.tag}</span>
								{#if c.strongHits > 0}
									<span class="rounded bg-emerald-500/15 px-1.5 py-0.5 text-[10px] font-semibold text-emerald-700 dark:text-emerald-400">
										LLM 신호 {c.strongHits}
									</span>
								{/if}
								{#if c.onlyDuringRun}
									<span class="rounded bg-blue-500/15 px-1.5 py-0.5 text-[10px] text-blue-700 dark:text-blue-400">
										추론 구간 전용
									</span>
								{/if}
								<span class="ml-auto text-[10px] text-muted-foreground">
									{c.lines}줄 · 숫자 {c.unitHits} · 단어 {c.keywordHits} · pid {c.pids.join(',')}
								</span>
							</button>
							{#if expandedTag === c.tag}
								<div class="bg-muted/30 px-2 pb-2">
									<div class="mb-1 text-[10px] font-medium text-muted-foreground">
										원문 샘플 — 이 줄을 보고 판단하세요
									</div>
									<!-- ⚠ 인덱스 키. 원문 샘플은 **같은 줄이 반복되는 것이 정상**이라
									     (같은 ms 에 같은 메시지가 두 번 찍히는 일이 흔하다) 값으로 키를
									     만들면 each_key_duplicate 로 패널이 통째로 죽는다. 재정렬 없는
									     읽기 전용 목록이라 인덱스로 충분하다. -->
									{#each c.samples as s, i (i)}
										<pre class="overflow-x-auto whitespace-pre-wrap break-all rounded bg-background/70 p-1.5 text-[10px] font-mono leading-relaxed">{s}</pre>
									{/each}
									<button
										class="mt-1.5 flex items-center gap-1 rounded border px-2 py-1 text-[11px] hover:bg-muted"
										onclick={() => seedProfileFromTag(c.tag, c.samples)}
									>
										<PlusIcon class="size-3" /> 이 태그로 프로파일 만들기
									</button>
								</div>
							{/if}
						</div>
					{/each}
				</div>
			{:else if !exploring}
				<div class="flex flex-1 items-center justify-center text-xs text-muted-foreground">
					탐색을 실행하면 후보 태그가 여기 나옵니다
				</div>
			{/if}
		</div>

	<!-- ② 프로파일 -->
	{:else if stage === 'profiles'}
		<div class="flex min-h-0 flex-1 flex-col gap-2 overflow-hidden">
			<div class="flex shrink-0 items-center justify-between">
				<p class="text-[11px] text-muted-foreground">
					확인된 패턴을 저장해 재사용합니다. AP·런타임 버전마다 문구가 달라 프리셋으로 둡니다.
				</p>
				<button
					class="flex h-7 items-center gap-1 rounded bg-primary px-3 text-xs font-medium text-primary-foreground"
					onclick={openCreate}
				><PlusIcon class="size-3" /> 새 프로파일</button>
			</div>

			<div class="min-h-0 flex-1 overflow-auto rounded border">
				{#if loadingProfiles}
					<div class="p-4 text-center text-xs text-muted-foreground">불러오는 중…</div>
				{:else if profiles.length === 0}
					<div class="p-4 text-center text-xs text-muted-foreground">
						저장된 프로파일이 없습니다. 탐색으로 태그를 찾은 뒤 만들어 보세요.
					</div>
				{:else}
					{#each profiles as p (p.id)}
						{@const pat = parsePatterns(p.patternsJson)}
						<div class="flex items-start gap-2 border-b p-2 last:border-b-0">
							<div class="min-w-0 flex-1">
								<div class="flex items-center gap-1.5">
									<span class="text-xs font-medium">{p.name}</span>
									<span class="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">{p.runtime}</span>
									{#if p.soc}
										<span class="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">{p.soc}</span>
									{:else}
										<span class="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">런타임 공용</span>
									{/if}
								</div>
								{#if p.description}
									<div class="mt-0.5 text-[11px] text-muted-foreground">{p.description}</div>
								{/if}
								<div class="mt-1 flex flex-wrap gap-1">
									<!-- ⚠ 인덱스 키. key 중복은 UI 가 경고로 잡아주는 "있을 수 있는
									     상태" 라, 그 상태에서 목록이 죽으면 정작 경고를 못 본다. -->
									{#each pat.marks ?? [] as m, i (i)}
										<span class="rounded bg-blue-500/10 px-1.5 py-0.5 text-[10px] font-mono text-blue-700 dark:text-blue-400">mark:{m.key}</span>
									{/each}
									{#each pat.series ?? [] as s, i (i)}
										<span class="rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-mono text-emerald-700 dark:text-emerald-400">
											{s.key}{s.unit ? ` (${s.unit})` : ''}
										</span>
									{/each}
								</div>
							</div>
							<button class="rounded p-1 hover:bg-muted" onclick={() => openEdit(p)} aria-label="수정">
								<PencilIcon class="size-3.5 text-muted-foreground" />
							</button>
							<button class="rounded p-1 hover:bg-muted" onclick={() => askDelete(p)} aria-label="삭제">
								<TrashIcon class="size-3.5 text-destructive" />
							</button>
						</div>
					{/each}
				{/if}
			</div>
		</div>

	<!-- ③ 측정 -->
	{:else}
		<div class="flex min-h-0 flex-1 flex-col gap-2 overflow-hidden">
			<div class="flex shrink-0 items-center gap-2 rounded border p-2">
				<select class="h-7 min-w-48 rounded border bg-background px-2 text-xs" bind:value={selectedProfileId}>
					<option value={null}>프로파일 선택…</option>
					{#each profiles as p (p.id)}
						<option value={p.id}>{p.name} ({p.runtime}{p.soc ? ` / ${p.soc}` : ''})</option>
					{/each}
				</select>
				<button
					class="flex h-7 items-center gap-1 rounded bg-primary px-3 text-xs font-medium text-primary-foreground disabled:opacity-50"
					onclick={runMeasure}
					disabled={measuring}
				>
					{#if measuring}<LoaderIcon class="size-3 animate-spin" />{:else}<PlayIcon class="size-3" />{/if}
					측정
				</button>
			</div>

			{#if parseRes}
				<div class="min-h-0 flex-1 space-y-2 overflow-auto">
					<!-- ⚠ 0건 / 부분 매칭을 가장 먼저, 가장 크게 보여준다 -->
					{#if parseRes.totalHits === 0}
						<div class="rounded border border-destructive/50 bg-destructive/10 p-2">
							<div class="flex items-center gap-1.5 text-xs font-semibold text-destructive">
								<AlertTriangleIcon class="size-3.5" /> 매칭 0건 — 측정하지 못했습니다
							</div>
							{#each parseRes.diagnosis as d (d)}
								<div class="mt-1 whitespace-pre-wrap text-[11px] leading-relaxed text-destructive/90">{d}</div>
							{/each}
						</div>
					{:else if parseRes.partial}
						<div class="rounded border border-amber-500/50 bg-amber-500/10 p-2">
							<div class="flex items-center gap-1.5 text-xs font-semibold text-amber-700 dark:text-amber-400">
								<AlertTriangleIcon class="size-3.5" /> 일부 패턴만 맞았습니다
							</div>
							<p class="mt-1 text-[11px] text-amber-800/90 dark:text-amber-300/90">
								안 맞은 지표: <code class="font-mono">{parseRes.missingKeys.join(', ')}</code> —
								<strong>이 값들은 없습니다.</strong> 아래 수치만 보고 정상이라 판단하지 마세요.
							</p>
						</div>
					{/if}

					<!-- 지표 카드 -->
					{#if seriesList.length > 0}
						<div class="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
							{#each seriesList as s (s.key)}
								<div class="rounded border p-2">
									<div class="flex items-baseline justify-between">
										<span class="font-mono text-[11px] font-medium">{s.key}</span>
										<span class="text-[10px] text-muted-foreground">{s.count}건</span>
									</div>
									{#if s.count === 1}
										<!-- 단일값 (TTFT·load 등) — 분포가 의미 없다 -->
										<div class="mt-1 text-xl font-semibold tabular-nums">
											{fmt(s.points[0].value)}<span class="ml-1 text-xs font-normal text-muted-foreground">{s.unit ?? ''}</span>
										</div>
									{:else}
										<!-- 시계열 (TPOT 등) — 평균만 보면 "뒤로 갈수록 느려짐" 을 놓친다 -->
										<div class="mt-1 text-xl font-semibold tabular-nums">
											{fmt(s.median)}<span class="ml-1 text-xs font-normal text-muted-foreground">{s.unit ?? ''} (중앙값)</span>
										</div>
										<div class="mt-1 grid grid-cols-4 gap-1 text-[10px] text-muted-foreground">
											<div>min<div class="font-medium tabular-nums text-foreground">{fmt(s.min)}</div></div>
											<div>평균<div class="font-medium tabular-nums text-foreground">{fmt(s.mean)}</div></div>
											<div>p99<div class="font-medium tabular-nums text-foreground">{fmt(s.p99)}</div></div>
											<div>max<div class="font-medium tabular-nums text-foreground">{fmt(s.max)}</div></div>
										</div>
										<!-- 추세 스파크라인 — 뒤로 갈수록 느려지는지 눈으로 본다 -->
										<svg class="mt-1.5 w-full" height="24" viewBox="0 0 100 24" preserveAspectRatio="none">
											{#if s.max > s.min}
												<polyline
													fill="none" stroke="currentColor" stroke-width="1"
													class="text-primary"
													points={s.points.map((p, i) =>
														`${(i / Math.max(1, s.points.length - 1)) * 100},${24 - ((p.value - s.min) / (s.max - s.min)) * 22}`
													).join(' ')}
												/>
											{/if}
										</svg>
									{/if}
								</div>
							{/each}
						</div>
					{/if}

					<!-- 구간 마커 -->
					{#if parseRes.marks.length > 0}
						<div class="rounded border p-2">
							<div class="mb-1 text-[11px] font-medium text-muted-foreground">구간 경계 (mark)</div>
							<!-- ⚠ 인덱스 키. logcat 은 같은 밀리초에 여러 줄을 찍으므로
							     key+timeSec 이 중복될 수 있다 (mark 정규식이 버스트에 두 번
							     걸리는 경우). 값으로 키를 만들면 측정 탭이 죽는다. -->
							{#each parseRes.marks as m, i (i)}
								<div class="flex items-center gap-2 border-b py-0.5 text-[11px] last:border-b-0">
									<span class="font-mono tabular-nums text-muted-foreground">{m.timeSec.toFixed(3)}</span>
									<span class="rounded bg-blue-500/10 px-1.5 text-[10px] font-mono text-blue-700 dark:text-blue-400">{m.key}</span>
									<span class="truncate font-mono text-[10px] text-muted-foreground">{m.raw}</span>
								</div>
							{/each}
						</div>
					{/if}

					<!-- 매칭 통계 — 무엇이 몇 건 걸렸는지 항상 보여준다 -->
					<div class="rounded border p-2">
						<div class="mb-1 text-[11px] font-medium text-muted-foreground">
							매칭 통계 · {parseRes.parsedLines.toLocaleString()}줄 파싱
							{#if parseRes.matchedTags.length > 0}
								· 태그 {parseRes.matchedTags.join(', ')}
							{/if}
						</div>
						{#each parseRes.stats as st (st.key)}
							<div class="flex items-center gap-2 py-0.5 text-[11px]">
								{#if st.hits > 0}
									<CheckIcon class="size-3 text-emerald-600" />
								{:else}
									<AlertTriangleIcon class="size-3 text-amber-600" />
								{/if}
								<span class="font-mono">{st.key}</span>
								<span class="text-[10px] text-muted-foreground">{st.kind}</span>
								<span class="ml-auto tabular-nums {st.hits > 0 ? '' : 'text-amber-600'}">{st.hits}건</span>
								{#if st.parseFailures > 0}
									<span class="text-[10px] text-amber-600">캡처 실패 {st.parseFailures}</span>
								{/if}
							</div>
						{/each}
					</div>
				</div>
			{:else if !measuring}
				<div class="flex flex-1 items-center justify-center text-xs text-muted-foreground">
					프로파일을 고르고 측정을 실행하세요
				</div>
			{/if}
		</div>
	{/if}
</div>

<!-- 프로파일 편집 -->
<Dialog.Root bind:open={editOpen}>
	<Dialog.Content class="max-w-2xl">
		<Dialog.Header>
			<Dialog.Title>{editingId == null ? '새 프로파일' : '프로파일 수정'}</Dialog.Title>
		</Dialog.Header>
		<div class="space-y-2">
			<div class="grid grid-cols-2 gap-2">
				<label class="flex flex-col gap-1">
					<span class="text-[11px] text-muted-foreground">이름 *</span>
					<input class="h-8 rounded border bg-background px-2 text-xs" bind:value={form.name} placeholder="QNN / Genie" />
				</label>
				<label class="flex flex-col gap-1">
					<span class="text-[11px] text-muted-foreground">runtime *</span>
					<input class="h-8 rounded border bg-background px-2 text-xs" bind:value={form.runtime} placeholder="qnn / llamacpp / neuropilot" />
				</label>
			</div>
			<div class="grid grid-cols-2 gap-2">
				<label class="flex flex-col gap-1">
					<span class="text-[11px] text-muted-foreground">SoC (비우면 런타임 공용)</span>
					<input class="h-8 rounded border bg-background px-2 text-xs" bind:value={form.soc} placeholder="SM8975" />
				</label>
				<label class="flex flex-col gap-1">
					<span class="text-[11px] text-muted-foreground">설명</span>
					<input class="h-8 rounded border bg-background px-2 text-xs" bind:value={form.description} />
				</label>
			</div>
			<label class="flex flex-col gap-1">
				<span class="text-[11px] text-muted-foreground">
					패턴 (JSON) — <strong>mark</strong> 는 시각만, <strong>series</strong> 는 캡처 그룹의 숫자를 뽑습니다
				</span>
				<textarea
					class="h-56 rounded border bg-background p-2 font-mono text-[11px] leading-relaxed"
					bind:value={form.patternsJson}
					spellcheck="false"
				></textarea>
			</label>

			{#if patternIssues.length > 0}
				<div class="rounded border border-destructive/50 bg-destructive/10 p-2">
					<!-- ⚠ 인덱스 키. 같은 경고 문구가 두 번 나올 수 있다
					     (예: key 가 빈 항목이 둘이면 같은 메시지가 반복). -->
					{#each patternIssues as issue, i (i)}
						<div class="text-[11px] text-destructive">{issue}</div>
					{/each}
				</div>
			{/if}
		</div>
		<Dialog.Footer>
			<button class="h-8 rounded border px-3 text-xs" onclick={() => (editOpen = false)}>취소</button>
			<button
				class="flex h-8 items-center gap-1 rounded bg-primary px-3 text-xs font-medium text-primary-foreground disabled:opacity-50"
				onclick={saveProfile}
				disabled={saving || patternIssues.length > 0}
			>
				{#if saving}<LoaderIcon class="size-3 animate-spin" />{/if}
				저장
			</button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>

<ConfirmDialog
	bind:open={confirmOpen}
	description={confirmDesc}
	variant="destructive"
	onConfirm={confirmAction}
	onCancel={() => (confirmOpen = false)}
/>
