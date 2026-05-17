import { get, post } from './client.js';

export interface ReparseJob {
	jobId: string;
	historyId: number;
	tcId: number;
	tentacleName: string;
	state: 'preparing' | 'running' | 'completed' | 'failed';
	totalFiles: number;
	currentIndex: number;
	currentFileName: string;
	error: string;
	startedAt: number;
	updatedAt: number;
}

export const startReparse = (historyId: number) =>
	post<ReparseJob>(`/reparse/${historyId}`, {});

export const fetchReparseJobs = () =>
	get<ReparseJob[]>('/reparse/jobs');

export function createReparseSSE(): EventSource {
	return new EventSource('/api/reparse/stream');
}
