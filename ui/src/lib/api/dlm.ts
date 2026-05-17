import { get, post } from './client.js';
import { getCsrfToken } from '$lib/stores/auth.svelte.js';

export interface DlmSlotTarget {
	tentacleName: string;
	slotNumber: number;
	serial: string;
	testToolName: string;
	toolId: number;
}

export interface DlmExecuteResult {
	fileName: string;
	filePath: string;
	stdout: string;
}

export interface DlmTool {
	id: number;
	toolName: string;
	toolPath: string;
	description?: string;
}

export function fetchDlmTools(): Promise<DlmTool[]> {
	return get('/debug/dlm/tools');
}

export function executeDlm(target: DlmSlotTarget): Promise<DlmExecuteResult> {
	return post<DlmExecuteResult>('/debug/dlm/execute', target);
}

export function dlmDownloadUrl(tentacleName: string, filePath: string): string {
	const params = new URLSearchParams({ tentacleName, filePath });
	return `/api/debug/dlm/download?${params}`;
}

export function uploadDlmToMinio(
	tentacleName: string,
	filePath: string,
	fileName: string
): Promise<{ objectName: string }> {
	return post<{ objectName: string }>('/debug/dlm/upload-minio', { tentacleName, filePath, fileName });
}
