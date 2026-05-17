import { getCsrfToken } from '$lib/stores/auth.svelte.js';

const BASE = '/api/log-search';

export type LogFormat = 'auto' | 'logcat-short' | 'logcat-long' | 'kmsg';

export type LogSearchRequest = {
	bucket?: string;
	objectKey?: string;
	timeStartMs: number;
	timeEndMs: number;
	format?: LogFormat;
	referenceYear?: number;
	bootEpochMs?: number;
	limit?: number;
	// 설정 시 portal_trace_logs 에서 bucket/objectKey/format/anchor 자동 채움.
	traceLogId?: number | null;
};

export type LogMatchedLine = {
	lineNumber: number;
	byteOffset: number;
	timestampMs: number;
	text: string;
};

export type LogSearchResponse = {
	matchedLines: LogMatchedLine[];
	bytesScanned: number;
	truncated: boolean;
	effectiveFormat: string;
	fileSize: number;
};

export async function searchLogsByTime(
	req: LogSearchRequest,
	signal?: AbortSignal
): Promise<LogSearchResponse> {
	const res = await fetch(`${BASE}/timestamps`, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			'X-XSRF-TOKEN': getCsrfToken()
		},
		body: JSON.stringify({
			bucket: req.bucket ?? '',
			objectKey: req.objectKey ?? '',
			timeStartMs: req.timeStartMs,
			timeEndMs: req.timeEndMs,
			format: req.format ?? 'auto',
			referenceYear: req.referenceYear ?? 0,
			bootEpochMs: req.bootEpochMs ?? 0,
			limit: req.limit ?? 0,
			traceLogId: req.traceLogId ?? null
		}),
		signal
	});
	if (!res.ok) {
		const text = await res.text().catch(() => res.statusText);
		throw new Error(`log-search ${res.status}: ${text}`);
	}
	return res.json();
}
