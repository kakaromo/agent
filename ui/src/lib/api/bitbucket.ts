import { get, post, put, del } from './client.js';
import { getCsrfToken } from '$lib/stores/auth.svelte.js';

export interface BitbucketRepo {
	id: number;
	name: string;
	serverUrl: string;
	projectKey: string;
	repoSlug: string;
	pat: string;
	controller?: string;
	targetPath: string;
	autoDownload: boolean;
	enabled: boolean;
	createdAt?: string;
	updatedAt?: string;
	lastPolledAt?: string;
}

export interface BitbucketBranch {
	id: number;
	repoId: number;
	branchName: string;
	latestCommitId?: string;
	status: 'DETECTED' | 'DOWNLOADING' | 'DOWNLOADED' | 'FAILED';
	filePath?: string;
	fileSizeBytes: number;
	commitDate?: string;
	downloadedAt?: string;
	errorMessage?: string;
}

// ── Repo CRUD ──

export function fetchRepos(): Promise<BitbucketRepo[]> {
	return get('/bitbucket/repos');
}

export function createRepo(data: Omit<BitbucketRepo, 'id' | 'createdAt' | 'updatedAt' | 'lastPolledAt'>): Promise<BitbucketRepo> {
	return post('/bitbucket/repos', data);
}

export function updateRepo(id: number, data: Partial<BitbucketRepo>): Promise<BitbucketRepo> {
	return put(`/bitbucket/repos/${id}`, data);
}

export function deleteRepo(id: number): Promise<void> {
	return del(`/bitbucket/repos/${id}`);
}

// ── Branch History ──

export function fetchBranches(repoId: number): Promise<BitbucketBranch[]> {
	return get(`/bitbucket/repos/${repoId}/branches`);
}

// ── Actions ──

export function pollRepo(repoId: number): Promise<{ message: string; downloaded: number }> {
	return post(`/bitbucket/repos/${repoId}/poll`, {});
}

export function downloadBranch(repoId: number, branch: string): Promise<BitbucketBranch> {
	return post(`/bitbucket/repos/${repoId}/download?branch=${encodeURIComponent(branch)}`, {});
}

/**
 * 브랜치 개별 다운로드 (SSE 진행률 스트리밍)
 * onProgress(mb): MB 단위 진행
 * onDone(): 완료
 * 반환: abort 함수
 */
export function downloadDetectedBranch(
	branchId: number,
	onProgress?: (mb: number) => void,
	onDone?: (status: string) => void,
	onError?: (msg: string) => void
): () => void {
	const controller = new AbortController();
	fetch(`/api/bitbucket/branches/${branchId}/download`, {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			'X-XSRF-TOKEN': getCsrfToken()
		},
		signal: controller.signal
	}).then(async (res) => {
		if (!res.ok) { onError?.(`HTTP ${res.status}`); return; }
		const reader = res.body?.getReader();
		if (!reader) return;
		const decoder = new TextDecoder();
		let buffer = '';
		while (true) {
			const { done, value } = await reader.read();
			if (done) break;
			buffer += decoder.decode(value, { stream: true });
			const parts = buffer.split('\n\n');
			buffer = parts.pop() ?? '';
			for (const part of parts) {
				if (!part.trim()) continue;
				let eventType = 'message';
				let dataStr = '';
				for (const line of part.split('\n')) {
					if (line.startsWith('event:')) eventType = line.slice(6).trim();
					else if (line.startsWith('data:')) dataStr = line.slice(5).trim();
				}
				if (!dataStr) continue;
				try {
					const data = JSON.parse(dataStr);
					if (eventType === 'download-progress') onProgress?.(data.mb ?? 0);
					else if (eventType === 'extract-start') onProgress?.(-1); // -1 = extracting
					else if (eventType === 'done') onDone?.(data.status ?? '');
					else if (eventType === 'error') onError?.(data.message ?? '');
				} catch { /* ignore */ }
			}
		}
	}).catch(e => {
		if (e.name !== 'AbortError') onError?.(e.message || '');
	});
	return () => controller.abort();
}

/** 파일만 삭제 (ZIP + 폴더), DB는 DETECTED로 복원 */
export function deleteBranchFiles(branchId: number): Promise<{ success: boolean; message: string }> {
	return post(`/bitbucket/branches/${branchId}/delete-files`, {});
}

/** DB 레코드 삭제 */
export function deleteBranchRecord(branchId: number): Promise<{ success: boolean; message: string }> {
	return del(`/bitbucket/branches/${branchId}`);
}

export function testConnection(repoId: number): Promise<{ result: string }> {
	return post(`/bitbucket/repos/${repoId}/test`, {});
}

export interface TestConnectionResult {
	success: boolean;
	message: string;
	branches: { name: string; commitId: string; timestamp: number | null }[];
}

export function testConnectionDirect(data: {
	serverUrl: string;
	projectKey: string;
	repoSlug: string;
	pat: string;
}): Promise<TestConnectionResult> {
	return post('/bitbucket/test-connection', data);
}
