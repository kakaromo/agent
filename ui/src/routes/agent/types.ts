import type { JobProgress } from '$lib/api/agent.js';

export interface ActiveJob {
	jobId: string;
	serverId: number;
	serverName: string;
	type: 'benchmark' | 'scenario' | 'trace';
	tool?: string;
	jobName?: string;
	deviceIds: string[];
	createdAt: number;
	events: JobProgress[];
	state: 'running' | 'completed' | 'failed';
	eventSource?: EventSource;
}

export interface JobRecord {
	jobId: string;
	serverId: number;
	serverName: string;
	type: 'benchmark' | 'scenario' | 'trace';
	tool?: string;
	jobName?: string;
	deviceIds: string[];
	state: string;
	createdAt: number;
}
