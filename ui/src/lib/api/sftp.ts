import { get, del } from './client.js';
import { getCsrfToken } from '$lib/stores/auth.svelte.js';

export interface FileEntry {
	name: string;
	directory: boolean;
	size: number;
	lastModified: number;
}

export function getPrefix(): Promise<{ prefix: string; headPrefix?: string; headHost?: string }> {
	return get<{ prefix: string; headPrefix?: string }>('/log-browser/prefix');
}

export function listFiles(vm: string, path: string): Promise<FileEntry[]> {
	return get<FileEntry[]>(
		`/log-browser/files?tentacleName=${encodeURIComponent(vm)}&path=${encodeURIComponent(path)}`
	);
}

export async function uploadFile(vm: string, path: string, file: File): Promise<{ success: boolean; message: string }> {
	const formData = new FormData();
	formData.append('file', file);

	const res = await fetch(
		`/api/log-browser/upload?tentacleName=${encodeURIComponent(vm)}&path=${encodeURIComponent(path)}`,
		{ method: 'POST', body: formData, headers: { 'X-XSRF-TOKEN': getCsrfToken() } }
	);
	if (!res.ok) {
		const text = await res.text().catch(() => res.statusText);
		throw new Error(`Upload failed: ${text}`);
	}
	return res.json();
}

export function deleteFile(vm: string, path: string): Promise<{ success: boolean; message: string }> {
	return del<{ success: boolean; message: string }>(
		`/log-browser/delete?tentacleName=${encodeURIComponent(vm)}&path=${encodeURIComponent(path)}`
	);
}

export function downloadUrl(vm: string, path: string): string {
	return `/api/log-browser/download?tentacleName=${encodeURIComponent(vm)}&path=${encodeURIComponent(path)}`;
}
