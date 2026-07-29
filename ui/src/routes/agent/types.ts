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
	// 터미널 상태를 접지 않고 그대로 보존한다 (cancelled 를 failed 로 접으면
	// 사용자가 취소한 잡이 '실패'로 표시된다).
	state: 'running' | 'completed' | 'failed' | 'partially_failed' | 'cancelled';
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
