/**
 * Agent Trace Archive — agent 종료 후에도 trace 결과를 영속 조회하기 위한 API.
 *
 * 흐름: init → upload(SSE) → complete → parse(SSE) → result/chart 라우팅.
 * Probe / fallback 없음 (agent ↔ nginx 단일 경로).
 */

import { post } from './client';

export interface ParquetFileMeta {
	localPath: string;       // agent 디스크 base name (e.g. "realtime_ufs_001.parquet")
	traceType: 'ufs' | 'block' | 'ufscustom';
	size: number;
}

export interface InitRequest {
	serverId: number;
	jobId: string;
	rawSize: number;
	parquetFiles: ParquetFileMeta[];
}

export interface PresignedPart {
	partNumber: number;
	url: string;
}

export interface PresignedTargetDto {
	localPath: string;
	traceType: string;
	objectKey: string;
	uploadId: string;
	parts: PresignedPart[];
	singlePutUrl: string | null;
	totalBytes: number;
	partSize: number;
}

export interface InitResponse {
	jobId: string;
	rawBucket: string;
	parquetBucket: string;
	raw: PresignedTargetDto;
	parquetFiles: PresignedTargetDto[];
}

export interface CompletedPart {
	partNumber: number;
	etag: string;
}

export interface CompleteFile {
	localPath: string;
	objectKey: string;
	uploadId: string;
	parts: CompletedPart[];
}

export interface CompleteRequest {
	serverId: number;
	jobId: string;
	raw: CompleteFile;
	parquetFiles: CompleteFile[];
}

export interface AbortRequest {
	serverId: number;
	jobId: string;
	rawObjectKey: string;
	rawUploadId: string;
	parquetFiles: { localPath: string; objectKey: string; uploadId: string }[];
}

export function initArchive(req: InitRequest): Promise<InitResponse> {
	return post('/agent/trace/archive/init', req);
}

export function completeArchive(req: CompleteRequest): Promise<unknown> {
	return post('/agent/trace/archive/complete', req);
}

export function abortArchive(req: AbortRequest): Promise<void> {
	return post('/agent/trace/archive/abort', req);
}

/**
 * Upload SSE — agent UploadTraceArchive RPC 진행률을 받는다.
 * 호출 후 반환된 EventSource 를 close() 하면 클라이언트 측에서 종료 (서버 RPC 는 background 계속).
 */
export function subscribeUpload(
	req: { serverId: number; jobId: string; init: InitResponse },
	handlers: {
		onProgress?: (ev: {
			currentFile: string;
			bytesUploaded: number;
			bytesTotal: number;
			filesDone: number;
			filesTotal: number;
			finished?: boolean;
		}) => void;
		onError?: (msg: string) => void;
		onDone?: () => void;
	}
): () => void {
	// EventSource 는 GET 만 지원 → POST + ReadableStream 사용
	const ctrl = new AbortController();
	(async () => {
		try {
			const res = await fetch('/api/agent/trace/archive/upload', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(req),
				signal: ctrl.signal
			});
			if (!res.ok || !res.body) {
				handlers.onError?.(`upload failed: HTTP ${res.status}`);
				return;
			}
			const reader = res.body.getReader();
			const decoder = new TextDecoder();
			let buf = '';
			while (true) {
				const { value, done } = await reader.read();
				if (done) break;
				buf += decoder.decode(value, { stream: true });
				let idx;
				while ((idx = buf.indexOf('\n\n')) >= 0) {
					const chunk = buf.slice(0, idx);
					buf = buf.slice(idx + 2);
					parseSseChunk(chunk, handlers);
				}
			}
			handlers.onDone?.();
		} catch (e) {
			if ((e as Error).name !== 'AbortError') {
				handlers.onError?.((e as Error).message);
			}
		}
	})();
	return () => ctrl.abort();
}

export function subscribeReparse(
	req: { serverId: number; jobId: string },
	handlers: {
		onProgress?: (ev: {
			stage: string;
			progressPercent: number;
			recordsProcessed: number;
			message: string;
		}) => void;
		onError?: (msg: string) => void;
		onDone?: (data: unknown) => void;
	}
): () => void {
	const ctrl = new AbortController();
	(async () => {
		try {
			const res = await fetch('/api/agent/trace/archive/parse', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(req),
				signal: ctrl.signal
			});
			if (!res.ok || !res.body) {
				handlers.onError?.(`parse failed: HTTP ${res.status}`);
				return;
			}
			const reader = res.body.getReader();
			const decoder = new TextDecoder();
			let buf = '';
			while (true) {
				const { value, done } = await reader.read();
				if (done) break;
				buf += decoder.decode(value, { stream: true });
				let idx;
				while ((idx = buf.indexOf('\n\n')) >= 0) {
					const chunk = buf.slice(0, idx);
					buf = buf.slice(idx + 2);
					parseSseChunk(chunk, handlers);
				}
			}
		} catch (e) {
			if ((e as Error).name !== 'AbortError') {
				handlers.onError?.((e as Error).message);
			}
		}
	})();
	return () => ctrl.abort();
}

function parseSseChunk(
	chunk: string,
	handlers: {
		onProgress?: (data: any) => void;
		onError?: (msg: string) => void;
		onDone?: (data: any) => void;
	}
) {
	let event = 'message';
	let data = '';
	for (const line of chunk.split('\n')) {
		if (line.startsWith('event:')) event = line.slice(6).trim();
		else if (line.startsWith('data:')) data += line.slice(5).trim();
	}
	if (!data) return;
	try {
		const parsed = JSON.parse(data);
		if (event === 'progress') handlers.onProgress?.(parsed);
		else if (event === 'error') handlers.onError?.(typeof parsed === 'string' ? parsed : JSON.stringify(parsed));
		else if (event === 'done') handlers.onDone?.(parsed);
	} catch {
		if (event === 'error') handlers.onError?.(data);
	}
}

export interface ArchiveStats {
	jobId: string;
	stats: Record<string, unknown>;
}

export function getArchivedStats(req: {
	serverId: number;
	jobId: string;
	traceType: string;
	filter?: Record<string, unknown>;
	latencyRangesMs?: number[];
}): Promise<ArchiveStats> {
	return post(`/agent/trace/archive/result?serverId=${req.serverId}`, {
		jobId: req.jobId,
		traceType: req.traceType,
		filter: req.filter ?? null,
		latencyRangesMs: req.latencyRangesMs ?? null
	});
}

export async function getArchivedChart(req: {
	jobId: string;
	traceType: string;
	targetPoints?: number;
	filter?: Record<string, unknown>;
}): Promise<{ arrowIpc: ArrayBuffer; totalEvents: number; sampledEvents: number; schemaVersion: string }> {
	const res = await fetch('/api/agent/trace/archive/chart', {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(req)
	});
	if (!res.ok) throw new Error(`chart failed: HTTP ${res.status}`);
	const arrowIpc = await res.arrayBuffer();
	return {
		arrowIpc,
		totalEvents: Number(res.headers.get('X-Total-Events') ?? '0'),
		sampledEvents: Number(res.headers.get('X-Sampled-Events') ?? '0'),
		schemaVersion: res.headers.get('X-Schema-Version') ?? ''
	};
}
