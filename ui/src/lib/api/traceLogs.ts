import { getCsrfToken } from '$lib/stores/auth.svelte.js';

export type TraceLogRow = {
	id: number;
	jobId: number;
	kind: string;
	originalFilename: string | null;
	objectKey: string;
	fileSize: number | null;
	format: string | null;
	bootEpochMs: number | null;
	referenceYear: number | null;
	uploadedAt: string;
};

export type PresignedPart = { partNumber: number; url: string };

export type InitResponse = {
	bucket: string;
	objectKey: string;
	uploadId: string;
	partSize: number;
	partCount: number;
	parts: PresignedPart[];
};

export type CompletedPart = { partNumber: number; etag: string };

const csrfHeaders = () => ({
	'Content-Type': 'application/json',
	'X-XSRF-TOKEN': getCsrfToken()
});

export async function listTraceLogs(jobId: number): Promise<TraceLogRow[]> {
	const res = await fetch(`/api/trace/jobs/${jobId}/logs`);
	if (!res.ok) throw new Error(`list logs ${res.status}`);
	return res.json();
}

export async function initTraceLogUpload(
	jobId: number,
	filename: string,
	sizeBytes: number,
	contentType?: string
): Promise<InitResponse> {
	const res = await fetch(`/api/trace/jobs/${jobId}/logs/init`, {
		method: 'POST',
		headers: csrfHeaders(),
		body: JSON.stringify({ filename, sizeBytes, contentType: contentType ?? '' })
	});
	if (!res.ok) {
		throw new Error(`init upload ${res.status}: ${await res.text().catch(() => '')}`);
	}
	return res.json();
}

export async function completeTraceLogUpload(
	jobId: number,
	body: {
		objectKey: string;
		uploadId: string;
		parts: CompletedPart[];
		originalFilename: string;
		fileSize: number;
		kind: string;
		format: string;
		bootEpochMs: number;
		referenceYear: number;
	}
): Promise<TraceLogRow> {
	const res = await fetch(`/api/trace/jobs/${jobId}/logs/complete`, {
		method: 'POST',
		headers: csrfHeaders(),
		body: JSON.stringify(body)
	});
	if (!res.ok) {
		throw new Error(`complete upload ${res.status}: ${await res.text().catch(() => '')}`);
	}
	return res.json();
}

export async function deleteTraceLog(jobId: number, logId: number): Promise<void> {
	const res = await fetch(`/api/trace/jobs/${jobId}/logs/${logId}`, {
		method: 'DELETE',
		headers: { 'X-XSRF-TOKEN': getCsrfToken() }
	});
	if (!res.ok && res.status !== 204) {
		throw new Error(`delete log ${res.status}`);
	}
}

/**
 * presigned multipart upload — 브라우저가 직접 MinIO 에 PUT.
 * 진행률 콜백(0~1) + 취소(AbortSignal). 모든 part 의 etag 를 모아 반환.
 */
export async function uploadFileToPresigned(
	file: File,
	init: InitResponse,
	onProgress?: (ratio: number) => void,
	signal?: AbortSignal
): Promise<CompletedPart[]> {
	const completed: CompletedPart[] = [];
	const partSize = init.partSize;
	let uploadedBytes = 0;
	for (let i = 0; i < init.parts.length; i++) {
		if (signal?.aborted) throw new DOMException('aborted', 'AbortError');
		const part = init.parts[i];
		const start = i * partSize;
		const end = Math.min(start + partSize, file.size);
		const slice = file.slice(start, end);
		const res = await fetch(part.url, { method: 'PUT', body: slice, signal });
		if (!res.ok) throw new Error(`PUT part ${part.partNumber} failed: ${res.status}`);
		const rawEtag = res.headers.get('ETag') ?? res.headers.get('etag');
		// ETag 미노출 시 multipart complete 가 MinIO 단에서 fail — 명시적으로 던져 사용자에게 즉시 보고.
		// 보통 nginx 의 add_header 'Access-Control-Expose-Headers: ETag' 누락이 원인.
		if (!rawEtag) {
			throw new Error(
				`part ${part.partNumber} response missing ETag header — check CORS Access-Control-Expose-Headers`
			);
		}
		const etag = rawEtag.replace(/"/g, '');
		completed.push({ partNumber: part.partNumber, etag });
		uploadedBytes += end - start;
		onProgress?.(file.size > 0 ? uploadedBytes / file.size : 1);
	}
	return completed;
}
