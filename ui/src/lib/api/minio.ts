import { get, post, put, del } from './client.js';
import { getCsrfToken } from '$lib/stores/auth.svelte.js';

export interface BucketInfo {
	name: string;
	visible?: boolean;
}

export interface ObjectInfo {
	name: string;
	isDir: boolean;
	size: number;
	lastModified: string | null;
}

export interface UploadProgress {
	bytesRead: number;
	totalSize: number;
	percent: number;
}

// Admin: returns BucketInfo[], User: returns string[]
export function listBuckets(): Promise<BucketInfo[] | string[]> {
	return get('/minio/buckets');
}

export function setBucketVisibility(
	bucketName: string,
	visible: boolean
): Promise<{ success: boolean; bucket: string; visible: boolean }> {
	return put(`/minio/buckets/${encodeURIComponent(bucketName)}/visibility`, { visible });
}

export function createBucket(bucketName: string): Promise<{ success: boolean; message: string }> {
	return post('/minio/buckets', { bucketName });
}

export function deleteBucket(bucketName: string): Promise<{ success: boolean; message: string }> {
	return del(`/minio/buckets/${encodeURIComponent(bucketName)}`);
}

export function listObjects(bucket: string, prefix: string = ''): Promise<ObjectInfo[]> {
	return get<ObjectInfo[]>(
		`/minio/buckets/${encodeURIComponent(bucket)}/objects?prefix=${encodeURIComponent(prefix)}`
	);
}

export interface FileInfo {
	name: string;
	size: number;
	lastModified: string | null;
}

/** 폴더 내 모든 파일을 재귀적으로 나열 (폴더 다운로드용) */
export function listObjectsRecursive(bucket: string, prefix: string = ''): Promise<FileInfo[]> {
	return get<FileInfo[]>(
		`/minio/buckets/${encodeURIComponent(bucket)}/objects/recursive?prefix=${encodeURIComponent(prefix)}`
	);
}

export function deleteObject(
	bucket: string,
	objectName: string
): Promise<{ success: boolean; message: string }> {
	return del(
		`/minio/buckets/${encodeURIComponent(bucket)}/objects?objectName=${encodeURIComponent(objectName)}`
	);
}

export function createFolder(
	bucket: string,
	folderPath: string
): Promise<{ success: boolean; message: string }> {
	return post(`/minio/buckets/${encodeURIComponent(bucket)}/folder`, { folderPath });
}

export function downloadUrl(bucket: string, objectName: string): string {
	return `/api/minio/buckets/${encodeURIComponent(bucket)}/download?objectName=${encodeURIComponent(objectName)}`;
}

export function downloadFolderUrl(bucket: string, prefix: string): string {
	return `/api/minio/buckets/${encodeURIComponent(bucket)}/download-folder?prefix=${encodeURIComponent(prefix)}`;
}

export interface DownloadProgress {
	bytesRead: number;
	totalSize: number;
	percent: number;
	fileName: string;
}

/**
 * 단일 파일 다운로드.
 *  1) /minio-upload/ probe → reachable 이면 presigned GET URL 발급받아 브라우저가 직접
 *     nginx → MinIO 로 다운로드 (Spring JVM 통과 X)
 *  2) probe 실패 또는 presigned 단계 fetch 실패(차단 의심)시 기존 /download (Spring 경유) 로 fallback
 *  폴더 다운로드는 ZIP 묶기가 필요해 별도 함수에서 spring 경유 그대로 사용.
 */
export function downloadWithProgress(
	bucket: string,
	objectName: string,
	onProgress: (progress: DownloadProgress) => void,
	onComplete: () => void,
	onError: (error: string) => void
): () => void {
	const controller = new AbortController();
	const fileName = objectName.includes('/') ? objectName.substring(objectName.lastIndexOf('/') + 1) : objectName;

	let aborted = false;
	const cancel = () => {
		aborted = true;
		try { controller.abort(); } catch { /* ignore */ }
	};

	async function streamAndSave(url: string, signedRedirect: boolean) {
		const response = await fetch(url, {
			signal: controller.signal,
			// presigned URL 은 외부 origin 가능 — credentials 보내면 CORS 깨짐.
			credentials: signedRedirect ? 'omit' : 'same-origin'
		});
		if (!response.ok) throw new Error(`Download failed (${response.status})`);

		const totalSize = parseInt(response.headers.get('Content-Length') || '0', 10);
		const reader = response.body?.getReader();
		if (!reader) throw new Error('ReadableStream not supported');

		const chunks: Uint8Array[] = [];
		let bytesRead = 0;

		while (true) {
			const { done, value } = await reader.read();
			if (done) break;
			chunks.push(value);
			bytesRead += value.length;
			onProgress({
				bytesRead,
				totalSize,
				percent: totalSize > 0 ? Math.round((bytesRead / totalSize) * 100) : 0,
				fileName
			});
		}

		const blob = new Blob(chunks as BlobPart[]);
		const a = document.createElement('a');
		a.href = URL.createObjectURL(blob);
		a.download = fileName;
		document.body.appendChild(a);
		a.click();
		document.body.removeChild(a);
		setTimeout(() => URL.revokeObjectURL(a.href), 5000);
	}

	(async () => {
		try {
			const reachable = await probeMinioReachable();
			if (aborted) return;
			if (reachable) {
				try {
					const res = await fetch(
						`/api/minio/buckets/${encodeURIComponent(bucket)}/download-url?objectName=${encodeURIComponent(objectName)}`,
						{ signal: controller.signal }
					);
					if (!res.ok) throw new Error(`presign ${res.status}`);
					const body = await res.json();
					if (aborted) return;
					await streamAndSave(body.url, true);
					onComplete();
					return;
				} catch (e) {
					if (aborted) return;
					console.warn('[storage download] presigned 다운로드 실패 → Spring 경유 fallback:', (e as Error).message);
				}
			}

			// fallback: Spring 경유 streaming
			await streamAndSave(downloadUrl(bucket, objectName), false);
			onComplete();
		} catch (e: any) {
			if (aborted) return;
			if (e?.name === 'AbortError') return;
			onError(e instanceof Error ? e.message : String(e));
		}
	})();

	return cancel;
}

import { probeMinioReachable } from './trace.js';

const UPLOAD_CONCURRENCY = 4;
const PART_RETRY = 2;

type InitResponse = {
	uploadId: string;
	bucket: string;
	objectKey: string;
	partSize: number;
	partCount: number;
	parts: { partNumber: number; url: string }[];
};

/**
 * MinIO 직접 PUT 단계 에러가 방화벽/네트워크 차단으로 의심되면 true.
 * 그 경우 Spring 경유 (/upload) 로 fallback. trace 의 isFallbackable 과 동일 판정.
 */
function isFallbackable(err: Error): boolean {
	const m = err.message || '';
	if (m.includes('network error')) return true;
	if (m.includes('aborted')) return false;
	const statusMatch = m.match(/PUT (\d+)/);
	if (statusMatch) {
		const code = parseInt(statusMatch[1]);
		return code === 0 || code === 502 || code === 503 || code === 504;
	}
	return false;
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
		// presigned URL 의 서명에 Content-Type 이 포함되지 않으므로 헤더 추가하면 SignatureDoesNotMatch.
		xhr.send(blob);
	});
}

/**
 * Storage 업로드.
 *  1) /minio-upload/ 에 health probe — 닿지 않으면 곧장 Spring fallback
 *  2) /upload/init    : presigned multipart 시작 + part 별 URL 발급
 *  3) 각 part 를 동시 N=4 로 PUT (브라우저 → nginx → MinIO 직행, Spring 통과 X)
 *  4) /upload/complete: ETag 모아 multipart 완료
 *  실패/취소: /upload/abort
 *  PUT 단계 network 차단 의심 시: 자동으로 /upload (FormData multipart) fallback
 */
export function uploadWithProgress(
	bucket: string,
	prefix: string,
	file: File,
	onProgress: (progress: UploadProgress) => void,
	onComplete: () => void,
	onError: (error: string) => void
): () => void {
	let aborted = false;
	const currentXhrs = new Set<XMLHttpRequest>();
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
			fetch(`/api/minio/buckets/${encodeURIComponent(bucket)}/upload/abort`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', 'X-XSRF-TOKEN': getCsrfToken() },
				body: JSON.stringify({ objectKey: init.objectKey, uploadId: init.uploadId })
			}).catch(() => {});
		}
	};

	function uploadViaSpring() {
		onProgress({ bytesRead: 0, totalSize: file.size, percent: 0 });
		const xhr = new XMLHttpRequest();
		fallbackXhr = xhr;
		const url = `/api/minio/buckets/${encodeURIComponent(bucket)}/upload?prefix=${encodeURIComponent(prefix)}`;
		xhr.upload.addEventListener('progress', (e) => {
			if (aborted) return;
			if (e.lengthComputable) {
				onProgress({
					bytesRead: e.loaded,
					totalSize: e.total,
					percent: Math.round((e.loaded / e.total) * 100)
				});
			}
		});
		xhr.addEventListener('load', () => {
			fallbackXhr = null;
			if (aborted) return;
			if (xhr.status >= 200 && xhr.status < 300) {
				onComplete();
			} else {
				onError(`Upload failed (${xhr.status}): ${xhr.responseText}`);
			}
		});
		xhr.addEventListener('error', () => {
			fallbackXhr = null;
			if (!aborted) onError('Upload network error');
		});
		xhr.addEventListener('abort', () => {
			fallbackXhr = null;
		});
		xhr.open('POST', url);
		xhr.setRequestHeader('X-XSRF-TOKEN', getCsrfToken());
		const formData = new FormData();
		formData.append('file', file);
		xhr.send(formData);
	}

	(async () => {
		try {
			const reachable = await probeMinioReachable();
			if (aborted) return;
			if (!reachable) {
				console.warn('[storage upload] MinIO health probe 실패 → Spring 경유 사용');
				uploadViaSpring();
				return;
			}

			const initRes = await fetch(`/api/minio/buckets/${encodeURIComponent(bucket)}/upload/init`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', 'X-XSRF-TOKEN': getCsrfToken() },
				body: JSON.stringify({
					prefix,
					filename: file.name,
					sizeBytes: file.size,
					contentType: file.type || 'application/octet-stream'
				})
			});
			if (!initRes.ok) {
				const text = await initRes.text().catch(() => '');
				// 백엔드 정책 위반(예: 2GB 압축 제한) 은 fallback 안 함 — 그대로 에러.
				if (initRes.status === 400) {
					try {
						const body = JSON.parse(text);
						onError(body?.message || `init failed (${initRes.status})`);
					} catch {
						onError(`init failed (${initRes.status}): ${text}`);
					}
					return;
				}
				throw new Error(`init failed (${initRes.status}): ${text}`);
			}
			init = await initRes.json();
			if (aborted) return;

			const partProgress = new Array(init!.partCount).fill(0);
			const etags: { partNumber: number; etag: string }[] = [];
			const queue = [...init!.parts];

			const reportProgress = () => {
				const total = partProgress.reduce((a, b) => a + b, 0);
				const pct = Math.min(100, Math.round((total / Math.max(file.size, 1)) * 100));
				onProgress({ bytesRead: total, totalSize: file.size, percent: pct });
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

			const completeRes = await fetch(`/api/minio/buckets/${encodeURIComponent(bucket)}/upload/complete`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', 'X-XSRF-TOKEN': getCsrfToken() },
				body: JSON.stringify({
					objectKey: init!.objectKey,
					uploadId: init!.uploadId,
					parts: etags
				})
			});
			if (!completeRes.ok) {
				throw new Error(`complete failed (${completeRes.status}): ${await completeRes.text().catch(() => '')}`);
			}
			init = null; // 정상 완료 — abort 호출 안 하도록 reset
			onComplete();
		} catch (e) {
			if (aborted) return;
			const err = e as Error;
			const canFallback = !!init && err.message.includes('part ') && isFallbackable(err);

			if (init) {
				fetch(`/api/minio/buckets/${encodeURIComponent(bucket)}/upload/abort`, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json', 'X-XSRF-TOKEN': getCsrfToken() },
					body: JSON.stringify({ objectKey: init.objectKey, uploadId: init.uploadId })
				}).catch(() => {});
				init = null;
			}

			if (canFallback) {
				console.warn('[storage upload] MinIO 직접 PUT 실패 → Spring 경유 fallback:', err.message);
				uploadViaSpring();
				return;
			}

			onError(err.message || 'upload failed');
		}
	})();

	return cancel;
}
