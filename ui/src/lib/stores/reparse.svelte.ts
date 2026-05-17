import type { ReparseJob } from '$lib/api/reparse.js';
import { startReparse as apiStartReparse, fetchReparseJobs, createReparseSSE } from '$lib/api/reparse.js';

const STORAGE_KEY = 'reparse-active-jobs';

let jobs = $state<Map<string, ReparseJob>>(new Map());
let eventSource = $state<EventSource | null>(null);
let connected = $state(false);
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

function saveToStorage() {
	const active = [...jobs.values()].filter(j => j.state === 'preparing' || j.state === 'running');
	if (active.length > 0) {
		localStorage.setItem(STORAGE_KEY, JSON.stringify(active.map(j => j.jobId)));
	} else {
		localStorage.removeItem(STORAGE_KEY);
	}
}

function updateJobs(serverJobs: ReparseJob[]) {
	const newMap = new Map<string, ReparseJob>();
	for (const j of serverJobs) {
		newMap.set(j.jobId, j);
	}
	jobs = newMap;
	saveToStorage();
}

function connect() {
	if (eventSource) return;

	const es = createReparseSSE();
	eventSource = es;

	es.addEventListener('init', (e: MessageEvent) => {
		connected = true;
		try {
			const data = JSON.parse(e.data);
			updateJobs(data.jobs ?? []);
		} catch { /* ignore */ }
	});

	es.addEventListener('update', (e: MessageEvent) => {
		try {
			const data = JSON.parse(e.data);
			updateJobs(data.jobs ?? []);
		} catch { /* ignore */ }
	});

	es.onerror = () => {
		disconnect();
		// 활성 job이 있으면 재연결
		const stored = localStorage.getItem(STORAGE_KEY);
		if (stored) {
			reconnectTimer = setTimeout(() => connect(), 5000);
		}
	};
}

function disconnect() {
	if (eventSource) {
		eventSource.close();
		eventSource = null;
	}
	connected = false;
	if (reconnectTimer) {
		clearTimeout(reconnectTimer);
		reconnectTimer = null;
	}
}

export const reparseStore = {
	get jobs() { return jobs; },
	get connected() { return connected; },

	get activeJobs(): ReparseJob[] {
		return [...jobs.values()].filter(j => j.state === 'preparing' || j.state === 'running');
	},

	get completedJobs(): ReparseJob[] {
		return [...jobs.values()].filter(j => j.state === 'completed' || j.state === 'failed');
	},

	get hasActiveJobs(): boolean {
		return [...jobs.values()].some(j => j.state === 'preparing' || j.state === 'running');
	},

	get allJobs(): ReparseJob[] {
		return [...jobs.values()];
	},

	isReparsing(historyId: number): boolean {
		return [...jobs.values()].some(
			j => j.historyId === historyId && (j.state === 'preparing' || j.state === 'running')
		);
	},

	async startReparse(historyId: number) {
		const job = await apiStartReparse(historyId);
		jobs = new Map(jobs).set(job.jobId, job);
		saveToStorage();
		if (!eventSource) connect();
		return job;
	},

	dismissJob(jobId: string) {
		const newMap = new Map(jobs);
		newMap.delete(jobId);
		jobs = newMap;
		saveToStorage();
	},

	/** 앱 초기화 시 호출 — localStorage에 활성 job이 있으면 SSE 연결 */
	init() {
		const stored = localStorage.getItem(STORAGE_KEY);
		if (stored) {
			try {
				const ids = JSON.parse(stored);
				if (Array.isArray(ids) && ids.length > 0) {
					connect();
				}
			} catch {
				localStorage.removeItem(STORAGE_KEY);
			}
		}
	},

	destroy() {
		disconnect();
	}
};
