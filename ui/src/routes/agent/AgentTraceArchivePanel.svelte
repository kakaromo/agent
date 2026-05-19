<script lang="ts">
	/**
	 * Agent Trace Archive 트리거 + 진행률 패널.
	 *
	 * - "Archive Now": init → /upload SSE → /complete 호출 (agent → nginx → MinIO)
	 * - "Reparse": /parse SSE 트리거 (Rust ProcessLogs 진행률)
	 *
	 * agent → MinIO 직접 PUT 은 server (agent) 가 수행. 이 컴포넌트는 portal API 만 호출.
	 * 따라서 이 컴포넌트 자체는 큰 데이터 전송 없음.
	 */
	import { Button } from '$lib/components/ui/button/index.js';
	import {
		initArchive,
		subscribeUpload,
		subscribeReparse,
		type InitResponse
	} from '$lib/api/agentTraceArchive';
	import { toast } from 'svelte-sonner';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import ArchiveIcon from '@lucide/svelte/icons/archive';
	import RefreshCw from '@lucide/svelte/icons/refresh-cw';

	interface Props {
		serverId: number;
		jobId: string;
		// archive 상태 (DB 컬럼 거울)
		traceParseState: string | null;
		traceRawKey: string | null;
		traceParsedAt: string | null;
		onUpdated?: () => void;
	}

	let {
		serverId,
		jobId,
		traceParseState,
		traceRawKey,
		traceParsedAt,
		onUpdated
	}: Props = $props();

	let uploading = $state(false);
	let parsing = $state(false);
	let progressLine = $state('');

	let cancelUpload: (() => void) | null = null;
	let cancelParse: (() => void) | null = null;

	async function handleArchive() {
		uploading = true;
		progressLine = 'init...';
		try {
			// rawSize/parquetFiles 비워두면 portal 이 agent 의 GetArchiveFilesInfo RPC 로 자동 조회.
			const init: InitResponse = await initArchive({
				serverId,
				jobId,
				rawSize: 0,
				parquetFiles: []
			});

			progressLine = 'uploading...';
			// portal 이 agent SSE 끝에 자동으로 multipart complete + DB 갱신 → frontend 는 done 만 대기.
			cancelUpload = subscribeUpload(
				{ serverId, jobId, init },
				{
					onProgress: (p) => {
						progressLine = `${p.currentFile}: ${p.bytesUploaded}/${p.bytesTotal} (${p.filesDone}/${p.filesTotal})`;
					},
					onError: (e) => {
						progressLine = `error: ${e}`;
						uploading = false;
						toast.error(`Archive 실패: ${e}`);
					},
					onDone: () => {
						progressLine = 'archived';
						uploading = false;
						toast.success('Archive 완료');
						onUpdated?.();
					}
				}
			);
		} catch (e) {
			uploading = false;
			toast.error(`Init 실패: ${(e as Error).message}`);
		}
	}

	async function handleReparse() {
		parsing = true;
		progressLine = 'parse triggered...';
		cancelParse = subscribeReparse(
			{ serverId, jobId },
			{
				onProgress: (p) => {
					progressLine = `${p.stage} ${p.progressPercent}% — ${p.message}`;
				},
				onError: (e) => {
					parsing = false;
					progressLine = `error: ${e}`;
					toast.error(`Reparse 실패: ${e}`);
				},
				onDone: () => {
					parsing = false;
					progressLine = 'parsed';
					toast.success('Reparse 완료');
					onUpdated?.();
				}
			}
		);
	}

	// SSE 만 닫고 서버 측 작업(agent→MinIO PUT, Rust ProcessLogs)은 백그라운드 계속 진행됨.
	// upload 의 경우 SSE 종료 후에도 portal 의 server-side auto-complete + DB 갱신은 정상 동작.
	// 진정한 abort 가 필요하면 `/api/agent/trace/archive/abort` REST 를 별도 호출해야 한다
	// (멀티파트 abort + DB clearArchive). stale UPLOADING 은 30분마다 reconciler 가 정리.
	function cancelOps() {
		cancelUpload?.();
		cancelParse?.();
		cancelUpload = null;
		cancelParse = null;
	}

	const mode = $derived.by(() => {
		if (traceParseState === 'PARSING' || traceParseState === 'UPLOADING') return 'busy';
		if (traceParsedAt) return 'archived';
		if (traceRawKey) return 'unparsed';
		return 'none';
	});
</script>

<div class="flex items-center gap-2 rounded border border-dashed border-muted-foreground/30 px-3 py-2 text-xs">
	<div class="flex items-center gap-1.5 text-muted-foreground">
		<ArchiveIcon class="size-3.5" />
		<span class="font-medium">
			{#if mode === 'archived'}Archived ({traceParsedAt}){:else if mode === 'unparsed'}Raw archived (unparsed){:else if mode === 'busy'}{traceParseState}{:else}Not archived{/if}
		</span>
	</div>

	<div class="flex-1 truncate text-muted-foreground/70">
		{progressLine}
	</div>

	{#if mode === 'none' || mode === 'archived'}
		<Button size="sm" variant="outline" onclick={handleArchive} disabled={uploading || parsing}>
			{#if uploading}<LoaderIcon class="size-3 animate-spin" />{:else}<ArchiveIcon class="size-3" />{/if}
			Archive Now
		</Button>
	{/if}

	{#if mode === 'unparsed' || mode === 'archived'}
		<Button size="sm" variant="outline" onclick={handleReparse} disabled={uploading || parsing}>
			{#if parsing}<LoaderIcon class="size-3 animate-spin" />{:else}<RefreshCw class="size-3" />{/if}
			Re-parse
		</Button>
	{/if}

	{#if uploading || parsing}
		<Button size="sm" variant="ghost" onclick={cancelOps}>Cancel</Button>
	{/if}
</div>
