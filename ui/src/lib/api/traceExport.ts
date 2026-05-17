// Trace Excel Export — SSE-over-POST 클라이언트.
// /api/trace/export/xlsx → progress / done / error 이벤트 수신 → 완료 시 presigned GET URL 자동 다운로드.
//
// 패턴은 lib/api/agentTraceArchive.ts::subscribeUpload 와 동일.

import { getCsrfToken } from '$lib/stores/auth.svelte.js';
import type { TraceFilter } from './trace.js';

export type XlsxStage =
	| 'XLSX_DOWNLOADING'
	| 'XLSX_FILTERING'
	| 'XLSX_WRITING'
	| 'XLSX_ZIPPING'
	| 'XLSX_UPLOADING'
	| 'XLSX_COMPLETED'
	| 'XLSX_FAILED'
	| string;

export interface XlsxProgress {
	stage: XlsxStage;
	progressPercent: number;
	recordsProcessed: number;
	message: string;
}

export interface XlsxDone {
	downloadUrl: string;
	fileName: string;
	fileCount: number;
	totalRows: number;
	/** 파일 크기 (단일 xlsx 또는 분할 시 zip). 이전 zipSizeBytes 명칭에서 일반화. */
	sizeBytes: number;
	files: string[];
}

export interface ExportXlsxRequest {
	parquetId: number;
	timeStartMs?: number | null;
	timeEndMs?: number | null;
	filter?: TraceFilter | null;
}

export interface ExportXlsxHandlers {
	onProgress?: (p: XlsxProgress) => void;
	onDone?: (d: XlsxDone) => void;
	onError?: (msg: string) => void;
}

/**
 * Excel export 시작. 반환된 함수를 호출하면 fetch 가 abort 되어 SSE 가 끊긴다 —
 * 단, 서버 측 RPC 는 background 에서 계속 실행되어 결과 ZIP 은 MinIO 에 업로드된다.
 */
export function exportXlsx(
	req: ExportXlsxRequest,
	handlers: ExportXlsxHandlers
): () => void {
	const ctrl = new AbortController();
	(async () => {
		try {
			const res = await fetch('/api/trace/export/xlsx', {
				method: 'POST',
				headers: {
					'Content-Type': 'application/json',
					'X-XSRF-TOKEN': getCsrfToken()
				},
				body: JSON.stringify(req),
				signal: ctrl.signal
			});
			if (!res.ok || !res.body) {
				handlers.onError?.(`export failed: HTTP ${res.status}`);
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

function parseSseChunk(chunk: string, handlers: ExportXlsxHandlers) {
	let event = 'message';
	let data = '';
	for (const line of chunk.split('\n')) {
		if (line.startsWith('event:')) event = line.slice(6).trim();
		else if (line.startsWith('data:')) data += line.slice(5).trim();
	}
	if (!data) return;
	try {
		const parsed = JSON.parse(data);
		if (event === 'progress') handlers.onProgress?.(parsed as XlsxProgress);
		else if (event === 'done') handlers.onDone?.(parsed as XlsxDone);
		else if (event === 'error')
			handlers.onError?.(
				typeof parsed === 'string'
					? parsed
					: (parsed.message ?? JSON.stringify(parsed))
			);
	} catch {
		if (event === 'error') handlers.onError?.(data);
	}
}

/** 다운로드 URL 로 파일을 자동 다운로드 — 임시 anchor click 방식. */
export function triggerDownload(url: string, filename: string) {
	const a = document.createElement('a');
	a.href = url;
	a.download = filename;
	a.style.display = 'none';
	document.body.appendChild(a);
	a.click();
	setTimeout(() => document.body.removeChild(a), 100);
}
