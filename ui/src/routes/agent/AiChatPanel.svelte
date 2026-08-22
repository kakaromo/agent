<script lang="ts">
	import { onDestroy } from 'svelte';
	import SparklesIcon from '@lucide/svelte/icons/sparkles';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import SendIcon from '@lucide/svelte/icons/send';
	import ChevronRight from '@lucide/svelte/icons/chevron-right';
	import {
		createAiChatStream,
		type AiChatMessage,
		type AiToolEvidence
	} from '$lib/api/agent.js';

	// AI 채팅 패널 — trace/benchmark 결과 시트가 공유한다.
	//
	// 설계 핵심: **답변마다 근거(실행한 집계)를 항상 표시**한다. 로컬 소형 모델이 도구를
	// 잘못 골라도 숫자 자체는 검증된 Go 코드가 계산한 값이라, 뱃지를 펼쳐 원본을 보면
	// 어디가 틀렸는지 바로 짚을 수 있다.
	//
	// 서버는 대화 상태를 보관하지 않으므로(stateless) 히스토리를 매 턴 그대로 되돌려준다.
	// 이때 이전 답변의 근거 집계(tool/aggJson)도 함께 보내야 "그 184초 근처" 같은 후속
	// 질문이 무엇을 가리키는지 모델이 알 수 있다.

	interface Turn {
		role: 'user' | 'assistant';
		content: string;
		evidence?: AiToolEvidence;
		expanded?: boolean;
	}

	interface Props {
		serverId: number | null;
		jobId: string | null;
		kind: 'trace' | 'benchmark';
		reachable: boolean;
		// 시트가 닫히면 부모가 이 값을 false 로 → 스트림 중단 + 대화 초기화
		open?: boolean;
	}

	let { serverId, jobId, kind, reachable, open = true }: Props = $props();

	let turns = $state<Turn[]>([]);
	let question = $state('');
	let running = $state(false);
	let errorMsg = $state('');
	let abort: (() => void) | null = null;
	let logEl: HTMLDivElement | null = $state(null);

	// 대화가 시작됐는지 (패널 노출 판단은 부모가 하고, 여기선 빈 상태 안내에 쓴다)
	export function hasConversation(): boolean {
		return turns.length > 0;
	}

	// 첫 진입 자동 해석 — 부모의 "AI 해석" 버튼이 호출한다.
	export function startOverview() {
		if (running) return;
		send('이 결과를 전반적으로 해석해줘.');
	}

	export function reset() {
		stop();
		turns = [];
		question = '';
		errorMsg = '';
	}

	function stop() {
		if (abort) {
			abort();
			abort = null;
		}
		running = false;
	}

	function scrollToBottom() {
		// 스트리밍 중 새 토큰이 붙을 때 아래로 따라간다.
		queueMicrotask(() => {
			if (logEl) logEl.scrollTop = logEl.scrollHeight;
		});
	}

	function send(text: string) {
		const q = text.trim();
		if (!q || !serverId || !jobId || running) return;

		stop();
		errorMsg = '';
		turns = [...turns, { role: 'user', content: q }, { role: 'assistant', content: '' }];
		running = true;
		scrollToBottom();

		// 서버로 보낼 히스토리 — 방금 추가한 빈 assistant 턴은 제외한다.
		const history: AiChatMessage[] = turns.slice(0, -1).map((t) => ({
			role: t.role,
			content: t.content,
			tool: t.evidence?.tool,
			aggJson: t.evidence?.data
		}));

		abort = createAiChatStream(serverId, jobId, kind, history, {
			onTool: (ev) => {
				const last = turns[turns.length - 1];
				if (last?.role === 'assistant') {
					last.evidence = ev;
					turns = [...turns];
				}
			},
			onToken: (t) => {
				const last = turns[turns.length - 1];
				if (last?.role === 'assistant') {
					last.content += t;
					turns = [...turns];
					scrollToBottom();
				}
			},
			onDone: () => {
				running = false;
				abort = null;
				// 답변이 비어 있으면(모델이 아무것도 못 냄) 빈 말풍선을 남기지 않는다.
				const last = turns[turns.length - 1];
				if (last?.role === 'assistant' && !last.content.trim() && !last.evidence) {
					turns = turns.slice(0, -1);
				}
				scrollToBottom();
			},
			onError: (msg) => {
				running = false;
				abort = null;
				const last = turns[turns.length - 1];
				// 토큰이 하나도 안 온 경우에만 빈 턴을 걷어내고 에러를 표시한다.
				if (last?.role === 'assistant' && !last.content.trim()) {
					turns = turns.slice(0, -1);
					errorMsg = msg;
				} else {
					errorMsg = msg;
				}
			}
		});
	}

	function submit(e: Event) {
		e.preventDefault();
		const q = question;
		question = '';
		send(q);
	}

	function toggleEvidence(i: number) {
		const t = turns[i];
		if (!t) return;
		t.expanded = !t.expanded;
		turns = [...turns];
	}

	// 근거 뱃지의 파라미터를 짧게 표기 (예: "n=10", "cmd=0x2A")
	function paramLabel(ev: AiToolEvidence): string {
		const p = ev.params ?? {};
		const parts = Object.entries(p)
			.filter(([, v]) => v !== '' && v != null)
			.map(([k, v]) => `${k}=${v}`);
		return parts.join(', ');
	}

	// 집계 결과 렌더 형태를 한 번에 결정한다.
	// (Svelte 는 {@const} 를 일반 요소 바로 아래 둘 수 없어, 마크업에서 계산하지 않는다.)
	interface EvidenceView {
		rows: Record<string, unknown>[] | null;
		cols: string[];
		pairs: Record<string, unknown> | null;
	}

	function evidenceView(ev: AiToolEvidence): EvidenceView {
		const d = ev.data;
		if (!d) return { rows: null, cols: [], pairs: null };

		// 배열이 있으면 표로 (tail_latency events, cmd_breakdown commands 등)
		for (const v of Object.values(d)) {
			if (Array.isArray(v) && v.length > 0 && typeof v[0] === 'object') {
				const rows = v as Record<string, unknown>[];
				return { rows, cols: Object.keys(rows[0] ?? {}), pairs: null };
			}
		}
		// 배열이 없으면 key/value (filtered_stats 의 scoped/overall 비교 등)
		const entries = Object.entries(d).filter(([, v]) => !Array.isArray(v));
		return { rows: null, cols: [], pairs: entries.length ? Object.fromEntries(entries) : null };
	}

	function fmtCell(v: unknown): string {
		if (typeof v === 'number') {
			return Number.isInteger(v) ? v.toLocaleString() : v.toFixed(3);
		}
		if (v == null) return '';
		if (typeof v === 'object') return JSON.stringify(v);
		return String(v);
	}

	$effect(() => {
		if (!open) reset();
	});

	onDestroy(() => stop());
</script>

{#if reachable}
	<div class="border rounded-md bg-muted/20 flex flex-col">
		<div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b text-[10px] font-semibold text-muted-foreground">
			<SparklesIcon class="size-3 text-primary" /> AI 해석
			{#if running}<LoaderIcon class="size-3 animate-spin" />{/if}
			{#if turns.length > 0}
				<button
					type="button"
					class="ml-auto text-[10px] font-normal hover:text-foreground"
					onclick={reset}>대화 지우기</button>
			{/if}
		</div>

		{#if turns.length > 0 || errorMsg}
			<div bind:this={logEl} class="flex flex-col gap-2.5 p-2.5 max-h-[420px] overflow-y-auto">
				{#each turns as turn, i (i)}
					{#if turn.role === 'user'}
						<div class="self-end max-w-[85%] rounded-md border bg-background px-2 py-1 text-[11px]">
							{turn.content}
						</div>
					{:else}
						<div class="flex flex-col gap-1.5">
							{#if turn.evidence}
								{@const ev = turn.evidence}
								<div class="w-fit max-w-full">
									<button
										type="button"
										class="flex items-center gap-1.5 rounded border border-primary/30 bg-primary/10 px-1.5 py-0.5 text-[10px] font-mono text-primary hover:bg-primary/15"
										onclick={() => toggleEvidence(i)}>
										<ChevronRight class="size-2.5 transition-transform {turn.expanded ? 'rotate-90' : ''}" />
										<span class="font-semibold">{ev.tool}</span>
										{#if paramLabel(ev)}<span class="opacity-80">{paramLabel(ev)}</span>{/if}
										{#if ev.rowCount}<span class="opacity-60">· {ev.rowCount} rows</span>{/if}
										{#if ev.truncated}<span class="opacity-60">(일부)</span>{/if}
									</button>
								</div>

								{#if turn.expanded}
									{@const view = evidenceView(ev)}
									<div class="rounded border bg-background p-2 text-[10px] font-mono overflow-x-auto">
										{#if ev.note}
											<div class="text-muted-foreground mb-1.5">{ev.note}</div>
										{/if}
										{#if ev.query}
											<div class="text-[9px] uppercase tracking-wide text-muted-foreground mb-1">실행한 쿼리</div>
											<pre class="whitespace-pre mb-2 text-muted-foreground">{ev.query}</pre>
										{/if}
										{#if view.rows}
											<div class="text-[9px] uppercase tracking-wide text-muted-foreground mb-1">결과</div>
											<table class="w-full border-collapse tabular-nums">
												<thead>
													<tr>
														{#each view.cols as c (c)}
															<th class="text-left text-[9px] uppercase text-muted-foreground font-medium pr-3 pb-0.5 border-b whitespace-nowrap">{c}</th>
														{/each}
													</tr>
												</thead>
												<tbody>
													{#each view.rows as row, ri (ri)}
														<tr>
															{#each view.cols as c (c)}
																<td class="pr-3 py-0.5 border-b border-border/40 whitespace-nowrap">{fmtCell(row[c])}</td>
															{/each}
														</tr>
													{/each}
												</tbody>
											</table>
										{:else if view.pairs}
											<div class="text-[9px] uppercase tracking-wide text-muted-foreground mb-1">결과</div>
											<pre class="whitespace-pre-wrap">{JSON.stringify(view.pairs, null, 2)}</pre>
										{/if}
									</div>
								{/if}
							{/if}

							<div class="text-[11px] leading-relaxed whitespace-pre-wrap">{turn.content}{#if running && i === turns.length - 1}<span class="inline-block w-1.5 h-3 -mb-0.5 bg-foreground/60 animate-pulse"></span>{/if}</div>
						</div>
					{/if}
				{/each}

				{#if errorMsg}
					<div class="text-[11px] text-destructive">{errorMsg}</div>
				{/if}
			</div>
		{/if}

		<form onsubmit={submit} class="flex items-center gap-1.5 px-2.5 py-2 border-t">
			<input
				bind:value={question}
				type="text"
				placeholder={turns.length ? '이어서 물어보세요…' : '이 결과에 대해 물어보세요…'}
				disabled={running || !jobId}
				class="flex-1 min-w-0 rounded border bg-background px-2 py-1 text-[11px] disabled:opacity-50" />
			<button
				type="submit"
				disabled={running || !question.trim() || !jobId}
				class="flex items-center gap-1 rounded border px-2 py-1 text-[11px] hover:bg-muted disabled:opacity-40">
				<SendIcon class="size-3" />
			</button>
		</form>
	</div>
{/if}
