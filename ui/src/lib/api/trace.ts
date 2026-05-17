import { getCsrfToken } from '$lib/stores/auth.svelte.js';
import { decodeChartResponse, type ChartResponse } from '$lib/utils/arrow-decoder.js';

const BASE = '/api/trace';

async function getJson<T>(path: string): Promise<T> {
	const res = await fetch(path);
	if (res.status === 401) { window.location.href = '/'; throw new Error('Unauthorized'); }
	if (!res.ok) throw new Error(`${res.status} ${await res.text().catch(() => res.statusText)}`);
	return res.json();
}

async function delJson(path: string): Promise<void> {
	const res = await fetch(path, { method: 'DELETE', headers: { 'X-XSRF-TOKEN': getCsrfToken() } });
	if (!res.ok) throw new Error(`${res.status} ${await res.text().catch(() => res.statusText)}`);
}

export type TraceJobStatus = 'UPLOADING' | 'UPLOADED' | 'PARSING' | 'PARSED' | 'FAILED';

export type TraceParquet = {
	id: number;
	traceType: 'ufs' | 'block' | 'ufscustom' | 'fsio_ufs' | 'fsio_block';
	totalEvents: number | null;
	schemaVersion: string | null;
	sizeBytes: number | null;
	createdAt: string | null;
};

export type TraceJob = {
	id: number;
	originalFilename: string;
	sizeBytes: number | null;
	status: TraceJobStatus;
	progressPercent: number | null;
	/** Rust 파싱 단계 ("DOWNLOADING" | "PARSING" | "CONVERTING" | "UPLOADING" | "COMPLETED" | "FAILED" | null) */
	currentStage: string | null;
	errorMessage: string | null;
	createdAt: string | null;
	parsedAt: string | null;
	/** 소유자 username (AD loginId) */
	ownerUsername: string | null;
	/** 소유자 표시명 (없으면 username 으로 폴백됨) */
	ownerDisplayName: string | null;
	/** 내가 업로드한 Job 인지 */
	mine: boolean;
	/** 삭제/재파싱 가능 여부 (owner 또는 admin) */
	canModify: boolean;
	parquets?: TraceParquet[];
	/** parsed 후 사용 가능한 trace_type 목록 (예: ["ufs", "block"]). list 응답에도 포함. */
	traceTypes?: string[];
	/** trace 모노토닉 time(s) → wall-clock 환산용 anchor (epoch ms). null = 미설정. */
	bootEpochMs?: number | null;
};

export function listJobs(): Promise<TraceJob[]> {
	return getJson<TraceJob[]>(`${BASE}/jobs`);
}

export function getJob(id: number): Promise<TraceJob> {
	return getJson<TraceJob>(`${BASE}/jobs/${id}`);
}

export function deleteJob(id: number): Promise<void> {
	return delJson(`${BASE}/jobs/${id}`);
}

export async function setJobAnchor(
	id: number,
	bootEpochMs: number | null
): Promise<{ success: boolean; jobId: number; bootEpochMs: number | null }> {
	const res = await fetch(`${BASE}/jobs/${id}/anchor`, {
		method: 'PATCH',
		headers: { 'Content-Type': 'application/json', 'X-XSRF-TOKEN': getCsrfToken() },
		body: JSON.stringify({ bootEpochMs })
	});
	if (!res.ok) throw new Error(`set anchor ${res.status}: ${await res.text().catch(() => '')}`);
	return res.json();
}

export async function reparseJob(id: number): Promise<{ success: boolean; jobId: number; status: string }> {
	const res = await fetch(`${BASE}/jobs/${id}/reparse`, {
		method: 'POST',
		headers: { 'X-XSRF-TOKEN': getCsrfToken() }
	});
	if (!res.ok) {
		const t = await res.text().catch(() => res.statusText);
		throw new Error(`${res.status} ${t}`);
	}
	return res.json();
}

/**
 * Presigned multipart 업로드 — Spring 우회.
 *   1) /upload/init    : Job row + S3 multipart 시작 + part 별 presigned URL 받음
 *   2) 각 part 를 동시 N개(기본 4) 로 PUT (브라우저 → nginx → MinIO 직행)
 *   3) /upload/complete: ETag 모아서 S3 complete + 파싱 시작
 *   실패/취소: /upload/abort (S3 abort + Job 삭제)
 *
 * 시그니처는 기존과 동일. cancel 함수를 리턴.
 */
const UPLOAD_CONCURRENCY = 4;
const PART_RETRY = 2;

type InitResponse = {
	jobId: number;
	uploadId: string;
	bucket: string;
	objectKey: string;
	partSize: number;
	partCount: number;
	parts: { partNumber: number; url: string }[];
};

export type UploadStage = 'probing' | 'init' | 'uploading' | 'finalizing' | 'fallback';

/**
 * MinIO 직접 PUT 단계에서 발생하는 에러가 "방화벽/네트워크 차단" 으로 의심되면 true.
 * 이 경우 Spring 경유 streaming(/upload-stream) 으로 자동 전환한다.
 *   - network error          : XHR error/abort (DNS/연결 실패, 프록시 거부)
 *   - 0 / 502 / 503 / 504    : 게이트웨이 단계 실패
 *   - 403 SignatureDoesNotMatch 같은 MinIO 정상 응답은 fallback 안 함 (서명 문제이지 차단 아님)
 */
function isFallbackable(err: Error): boolean {
	const m = err.message || '';
	if (m.includes('network error')) return true;
	if (m.includes('aborted')) return false; // 사용자 cancel
	const statusMatch = m.match(/PUT (\d+)/);
	if (statusMatch) {
		const code = parseInt(statusMatch[1]);
		return code === 0 || code === 502 || code === 503 || code === 504;
	}
	return false;
}

/**
 * /minio-upload/ 경로가 클라이언트 환경에서 닿는지 사전 확인.
 * - 결과는 sessionStorage 에 1시간 캐시 (TTL).
 * - 3초 타임아웃: MinIO health 엔드포인트는 정상이면 수십 ms 응답.
 * - 응답 status 200 이면 reachable, 그 외(404, 5xx, network error, timeout) 모두 차단으로 판단.
 *
 * 일부 운영 환경은 nginx 에서 `/minio-upload/minio/health/live` 를 별도로 expose 하지 않을 수도 있음.
 * 그 경우 health probe 가 404 로 떨어져 항상 fallback 이 되는데, 이는 안전한 쪽 (느리더라도 동작).
 */
const PROBE_CACHE_KEY = 'trace.minioReachable';
const PROBE_CACHE_TTL_MS = 60 * 60 * 1000; // 1시간

export async function probeMinioReachable(force = false): Promise<boolean> {
	if (!force && typeof sessionStorage !== 'undefined') {
		try {
			const raw = sessionStorage.getItem(PROBE_CACHE_KEY);
			if (raw) {
				const cached = JSON.parse(raw) as { ok: boolean; ts: number };
				if (Date.now() - cached.ts < PROBE_CACHE_TTL_MS) return cached.ok;
			}
		} catch { /* ignore */ }
	}

	let ok = false;
	try {
		const ctrl = new AbortController();
		const t = setTimeout(() => ctrl.abort(), 3000);
		const res = await fetch('/minio-upload/minio/health/live', {
			method: 'GET',
			signal: ctrl.signal,
			cache: 'no-store'
		});
		clearTimeout(t);
		ok = res.ok;
	} catch {
		ok = false;
	}

	if (typeof sessionStorage !== 'undefined') {
		try {
			sessionStorage.setItem(PROBE_CACHE_KEY, JSON.stringify({ ok, ts: Date.now() }));
		} catch { /* ignore */ }
	}
	return ok;
}

/**
 * @param logType Rust 의 ProcessLogs.log_type hint. "auto"(기본) | "ufs" | "block" | "ufscustom" |
 *                "fsio_ufs" | "fsio_block". 어차피 Rust 가 라인 형태로 자동 감지하므로 hint 일 뿐.
 */
export function uploadTrace(
	file: File,
	onProgress: (percent: number, loaded: number, total: number) => void,
	onComplete: (jobId: number) => void,
	onError: (message: string) => void,
	onStageChange?: (stage: UploadStage) => void,
	logType?: string
): () => void {
	let aborted = false;
	let currentXhrs = new Set<XMLHttpRequest>();
	let init: InitResponse | null = null;
	let fallbackXhr: XMLHttpRequest | null = null;

	const cancel = () => {
		aborted = true;
		for (const x of currentXhrs) {
			try { x.abort(); } catch { /* ignore */ }
		}
		currentXhrs.clear();
		if (fallbackXhr) {
			try { fallbackXhr.abort(); } catch { /* ignore */ }
			fallbackXhr = null;
		}
		if (init) {
			fetch(`${BASE}/upload/abort`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', 'X-XSRF-TOKEN': getCsrfToken() },
				body: JSON.stringify({ jobId: init.jobId, uploadId: init.uploadId })
			}).catch(() => {});
		}
	};

	/**
	 * Spring 경유 streaming 업로드 fallback.
	 * MinIO 직접 PUT 이 방화벽/프록시로 막힌 환경에서 사용.
	 * 속도는 느리지만 동일 origin Spring 만 거치므로 거의 모든 환경에서 동작.
	 */
	function uploadViaStream() {
		onStageChange?.('fallback');
		onProgress(0, 0, file.size);

		const xhr = new XMLHttpRequest();
		fallbackXhr = xhr;
		xhr.upload.addEventListener('progress', (e) => {
			if (aborted) return;
			if (e.lengthComputable) {
				const pct = Math.min(100, Math.round((e.loaded / file.size) * 100));
				onProgress(pct, e.loaded, file.size);
			}
		});
		xhr.addEventListener('load', () => {
			fallbackXhr = null;
			if (aborted) return;
			if (xhr.status >= 200 && xhr.status < 300) {
				try {
					const body = JSON.parse(xhr.responseText);
					if (body?.success && typeof body.jobId === 'number') {
						onComplete(body.jobId);
					} else {
						onError(body?.message || 'upload-stream response missing jobId');
					}
				} catch (e) {
					onError(`upload-stream parse error: ${(e as Error).message}`);
				}
			} else {
				onError(`upload-stream ${xhr.status}: ${xhr.responseText.slice(0, 200)}`);
			}
		});
		xhr.addEventListener('error', () => {
			fallbackXhr = null;
			if (aborted) return;
			onError('upload-stream: network error (Spring 경유 업로드도 실패)');
		});
		xhr.addEventListener('abort', () => {
			fallbackXhr = null;
		});
		xhr.open('POST', `${BASE}/upload-stream`);
		xhr.setRequestHeader('X-File-Name', encodeURIComponent(file.name));
		xhr.setRequestHeader('X-XSRF-TOKEN', getCsrfToken());
		xhr.setRequestHeader('Content-Type', file.type || 'application/octet-stream');
		if (logType) xhr.setRequestHeader('X-Log-Type', logType);
		xhr.send(file);
	}

	(async () => {
		try {
			// Pre-flight: MinIO 직접 PUT 경로가 닿는지 확인.
			// 차단된 환경이면 multipart 시도조차 하지 않고 곧장 streaming fallback 으로 진입한다.
			onStageChange?.('probing');
			const reachable = await probeMinioReachable();
			if (aborted) return;
			if (!reachable) {
				console.warn('[trace upload] MinIO health probe 실패 → 처음부터 Spring 경유 streaming 사용');
				uploadViaStream();
				return;
			}

			onStageChange?.('init');
			// Step 1: init
			const initRes = await fetch(`${BASE}/upload/init`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', 'X-XSRF-TOKEN': getCsrfToken() },
				body: JSON.stringify({
					filename: file.name,
					sizeBytes: file.size,
					contentType: file.type || 'application/octet-stream'
				})
			});
			if (!initRes.ok) {
				throw new Error(`init failed (${initRes.status}): ${await initRes.text().catch(() => '')}`);
			}
			init = await initRes.json();
			if (aborted) return;
			onStageChange?.('uploading');

			// Step 2: 병렬 part PUT
			const partProgress = new Array(init!.partCount).fill(0);
			const etags: { partNumber: number; etag: string }[] = [];
			const queue = [...init!.parts];

			const reportProgress = () => {
				const total = partProgress.reduce((a, b) => a + b, 0);
				const pct = Math.min(100, Math.round((total / file.size) * 100));
				onProgress(pct, total, file.size);
			};

			async function worker() {
				while (!aborted && queue.length > 0) {
					const p = queue.shift()!;
					const start = (p.partNumber - 1) * init!.partSize;
					const end = Math.min(start + init!.partSize, file.size);
					const blob = file.slice(start, end);

					let lastErr: Error | null = null;
					for (let attempt = 0; attempt <= PART_RETRY; attempt++) {
						if (aborted) return;
						try {
							const etag = await putPart(p.url, blob, (loaded) => {
								partProgress[p.partNumber - 1] = loaded;
								reportProgress();
							}, currentXhrs);
							etags.push({ partNumber: p.partNumber, etag });
							partProgress[p.partNumber - 1] = blob.size;
							reportProgress();
							lastErr = null;
							break;
						} catch (e) {
							lastErr = e as Error;
							if (aborted) return;
						}
					}
					if (lastErr) throw new Error(`part ${p.partNumber} failed: ${lastErr.message}`);
				}
			}

			const workers = Array.from(
				{ length: Math.min(UPLOAD_CONCURRENCY, init!.partCount) },
				() => worker()
			);
			await Promise.all(workers);
			if (aborted) return;

			etags.sort((a, b) => a.partNumber - b.partNumber);
			onStageChange?.('finalizing');

			// Step 3: complete (서버가 MinIO multipart 완료 + Job 생성 + 파싱 트리거 — 큰 파일은 수 초~수십 초 걸릴 수 있음)
			const completeRes = await fetch(`${BASE}/upload/complete`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', 'X-XSRF-TOKEN': getCsrfToken() },
				body: JSON.stringify({
					jobId: init!.jobId,
					uploadId: init!.uploadId,
					parts: etags,
					logType: logType ?? null
				})
			});
			if (!completeRes.ok) {
				throw new Error(`complete failed (${completeRes.status}): ${await completeRes.text().catch(() => '')}`);
			}
			const body = await completeRes.json();
			if (body?.success && typeof body.jobId === 'number') {
				onComplete(body.jobId);
			} else {
				throw new Error(body?.message || 'complete response missing jobId');
			}
		} catch (e) {
			if (aborted) return;
			const err = e as Error;

			// MinIO 직접 PUT 단계 실패가 방화벽 의심이면 → /upload-stream 으로 자동 전환
			const canFallback = !!init && err.message.includes('part ') && isFallbackable(err);

			// 기존 multipart upload 정리 (성공 여부와 무관하게 abort)
			if (init) {
				fetch(`${BASE}/upload/abort`, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json', 'X-XSRF-TOKEN': getCsrfToken() },
					body: JSON.stringify({ jobId: init.jobId, uploadId: init.uploadId })
				}).catch(() => {});
				init = null;
			}

			if (canFallback) {
				console.warn('[trace upload] MinIO 직접 PUT 실패 → Spring 경유 streaming 으로 전환:', err.message);
				uploadViaStream();
				return;
			}

			onError(err.message || 'upload failed');
		}
	})();

	return cancel;
}

/** Presigned URL 로 part 1개 PUT — 진행률 콜백 + ETag 추출. */
function putPart(
	url: string,
	blob: Blob,
	onLoaded: (loaded: number) => void,
	tracker: Set<XMLHttpRequest>
): Promise<string> {
	return new Promise((resolve, reject) => {
		const xhr = new XMLHttpRequest();
		tracker.add(xhr);
		xhr.upload.addEventListener('progress', (e) => {
			if (e.lengthComputable) onLoaded(e.loaded);
		});
		xhr.addEventListener('load', () => {
			tracker.delete(xhr);
			if (xhr.status >= 200 && xhr.status < 300) {
				const etag = xhr.getResponseHeader('ETag') || xhr.getResponseHeader('etag');
				if (!etag) {
					reject(new Error('missing ETag header'));
					return;
				}
				resolve(etag.replace(/"/g, ''));
			} else {
				reject(new Error(`PUT ${xhr.status}: ${xhr.responseText.slice(0, 200)}`));
			}
		});
		xhr.addEventListener('error', () => {
			tracker.delete(xhr);
			reject(new Error('network error'));
		});
		xhr.addEventListener('abort', () => {
			tracker.delete(xhr);
			reject(new Error('aborted'));
		});
		xhr.open('PUT', url);
		// 주의: Content-Type 을 직접 설정하지 않는다 — presigned URL 의 서명에
		// Content-Type 이 포함되지 않았으므로 헤더를 추가하면 SignatureDoesNotMatch.
		xhr.send(blob);
	});
}

export type TraceFilter = {
	timeStartMs?: number | null;
	timeEndMs?: number | null;
	minDtoc?: number | null; maxDtoc?: number | null;
	minCtoc?: number | null; maxCtoc?: number | null;
	minCtod?: number | null; maxCtod?: number | null;
	startLba?: number | null; endLba?: number | null;
	minQd?: number | null; maxQd?: number | null;
};

export type ChartRequest = {
	parquetId: number;
	timeStartMs?: number | null;
	timeEndMs?: number | null;
	targetPoints?: number;
	actions?: string[];
	filter?: TraceFilter | null;
};

/**
 * Arrow IPC 차트 페치. AbortController 지원 — 줌 re-fetch 시 이전 요청 취소.
 * 리턴은 {meta, table} 또는 abort 시 null.
 */
export async function getChart(
	req: ChartRequest,
	signal?: AbortSignal
): Promise<ChartResponse | null> {
	const res = await fetch(`${BASE}/chart`, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			'X-XSRF-TOKEN': getCsrfToken()
		},
		body: JSON.stringify(req),
		signal
	}).catch((e) => {
		if (e?.name === 'AbortError') return null;
		throw e;
	});
	if (!res) return null;
	return decodeChartResponse(res);
}

// ─── Stats API ───

export type StatsLatency = {
	min: number; max: number; avg: number; stddev: number; median: number;
	p99: number; p999: number; p9999: number; p99999: number; p999999: number;
	count: number;
};

export type StatsCmd = {
	cmd: string; count: number; sendCount: number; ratio: number;
	totalSizeBytes: number; continuousCount: number; continuousRatio: number;
	dtoc: StatsLatency; ctod: StatsLatency; ctoc: StatsLatency; qd: StatsLatency;
};

export type StatsHistogram = {
	latencyType: 'dtoc' | 'ctod' | 'ctoc';
	cmd: string;
	buckets: { rangeStartMs: number; rangeEndMs: number; count: number }[];
};

export type StatsResponse = {
	totalEvents: number;
	sendCount: number;
	durationSeconds: number;
	continuousCount: number;
	continuousRatio: number;
	alignedCount: number;
	alignedRatio: number;
	readTotalBytes: number;
	writeTotalBytes: number;
	discardTotalBytes: number;
	dtoc: StatsLatency;
	ctod: StatsLatency;
	ctoc: StatsLatency;
	qd: StatsLatency;
	cmdStats: StatsCmd[];
	latencyHistograms: StatsHistogram[];
	cmdSizeCounts: { cmd: string; size: number; count: number }[];
	schemaVersion: string;
};

export type StatsRequest = {
	parquetId: number;
	filter?: TraceFilter | null;
	latencyRangesMs?: number[] | null;
};

export async function getStats(
	req: StatsRequest,
	signal?: AbortSignal
): Promise<StatsResponse> {
	const res = await fetch(`${BASE}/stats`, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			'X-XSRF-TOKEN': getCsrfToken()
		},
		body: JSON.stringify(req),
		signal
	});
	if (!res.ok) {
		const text = await res.text().catch(() => res.statusText);
		throw new Error(`stats ${res.status}: ${text}`);
	}
	return res.json();
}

// ─────────────────── Raw Data DataTable ───────────────────

export type RawDataPageRequest = {
	parquetId: number;
	offset: number;
	limit: number;          // 100 / 500 / 1000
	timeStartMs?: number | null;
	timeEndMs?: number | null;
	filter?: TraceFilter | null;
	// keyset pagination — 채워지면 OFFSET 무시, 직전 응답의 nextCursor* 를 그대로 보냄.
	// 무한 스크롤 권장 (깊은 페이지에서 OFFSET 의 누적 스캔을 피함).
	cursorTime?: number | null;
	cursorLineNumber?: number | null;
};

export type RawDataPageResponse = {
	total: number;          // -1 = unknown (필터 적용 시 stats 로 보완)
	offset: number;
	limit: number;
	traceType: 'ufs' | 'block' | 'ufscustom' | string;
	columns: string[];
	schemaVersion: string;
	rows: Record<string, unknown>[];
	// keyset cursor — null 또는 undefined 면 다음 페이지 없음.
	nextCursorTime?: number | null;
	nextCursorLineNumber?: number | null;
};

export async function fetchRawPage(
	req: RawDataPageRequest,
	signal?: AbortSignal
): Promise<RawDataPageResponse> {
	const res = await fetch(`${BASE}/raw`, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			'X-XSRF-TOKEN': getCsrfToken()
		},
		body: JSON.stringify(req),
		signal
	});
	if (!res.ok) {
		const text = await res.text().catch(() => res.statusText);
		throw new Error(`raw ${res.status}: ${text}`);
	}
	return res.json();
}

// ─────────────────── Raw Log Lines ───────────────────

export type RawLogLinesRequest = {
	parquetId: number;
	lineNumbers: number[];
	contextBefore?: number;
	contextAfter?: number;
};

export type RawLogBlock = {
	targetLineNumber: number;
	startLineNumber: number;
	lines: string[];
};

export type RawLogLinesResponse = {
	totalLines: number;
	blocks: RawLogBlock[];
};

export async function fetchRawLogLines(
	req: RawLogLinesRequest,
	signal?: AbortSignal
): Promise<RawLogLinesResponse> {
	const res = await fetch(`${BASE}/raw-line`, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			'X-XSRF-TOKEN': getCsrfToken()
		},
		body: JSON.stringify(req),
		signal
	});
	if (!res.ok) {
		const text = await res.text().catch(() => res.statusText);
		throw new Error(`raw-line ${res.status}: ${text}`);
	}
	return res.json();
}
