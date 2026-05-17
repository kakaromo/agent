import { get, post, put, del } from './client.js';

export interface T32ServerInfo {
	id: number;
	name: string;
	ip: string;
}

export interface T32Config {
	id: number;
	serverGroupId: number;
	serverGroupName: string;
	jtagServerId: number;
	jtagServerName: string;
	jtagServerIp: string;
	jtagUsername?: string;
	jtagHasCustomAccount: boolean;
	t32PcId: number;
	t32PcName: string;
	t32PcIp: string;
	t32PcUsername?: string;
	t32PcHasCustomAccount: boolean;
	jtagCommand: string;
	jtagSuccessPattern: string;
	t32PortCheckCommand: string;
	dumpCommand: string;
	fwCodeLinuxBase: string;
	fwCodeWindowsBase: string;
	resultBasePath: string;
	resultWindowsBasePath: string;
	description: string;
	enabled: boolean;
	createdAt?: string;
	updatedAt?: string;
	assignedServers: T32ServerInfo[];
}

export interface T32ConfigCreateRequest {
	serverGroupId: number;
	jtagServerId: number;
	jtagUsername: string;
	jtagPassword: string;
	t32PcId: number;
	t32PcUsername: string;
	t32PcPassword: string;
	jtagCommand: string;
	jtagSuccessPattern: string;
	t32PortCheckCommand: string;
	dumpCommand: string;
	fwCodeLinuxBase: string;
	fwCodeWindowsBase: string;
	resultBasePath: string;
	resultWindowsBasePath: string;
	description: string;
	enabled: boolean;
	assignedServerIds: number[];
}

export function fetchConfigs(): Promise<T32Config[]> {
	return get('/t32/configs');
}

export function createConfig(data: T32ConfigCreateRequest): Promise<T32Config> {
	return post('/t32/configs', data);
}

export function updateConfig(id: number, data: T32ConfigCreateRequest): Promise<T32Config> {
	return put(`/t32/configs/${id}`, data);
}

export function deleteConfig(id: number): Promise<void> {
	return del(`/t32/configs/${id}`);
}

// ── T32 Dump ──

export function checkT32Available(serverId: number): Promise<{ available: boolean; configId: number }> {
	return get(`/t32/dump/check?serverId=${serverId}`);
}

export interface T32DumpStepEvent {
	type: string;
	data: Record<string, any>;
}

/**
 * T32 Dump SSE 실행. 이벤트를 콜백으로 전달.
 * 반환: abort 함수
 */
export function executeT32Dump(
	serverId: number,
	tentacleName: string,
	slotNumber: number,
	fw: string,
	branchPath: string,
	setLocation: string,
	testToolName: string,
	testTrName: string,
	onEvent: (type: string, data: Record<string, any>) => void,
	onError: (error: string) => void
): () => void {
	const controller = new AbortController();

	fetch('/api/t32/dump/execute', {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ serverId, tentacleName, slotNumber, fw, branchPath, setLocation, testToolName, testTrName }),
		signal: controller.signal
	})
		.then(async (response) => {
			if (!response.ok) {
				onError(`HTTP ${response.status}`);
				return;
			}
			const reader = response.body?.getReader();
			if (!reader) {
				onError('No response body');
				return;
			}
			const decoder = new TextDecoder();
			let buffer = '';

			while (true) {
				const { done, value } = await reader.read();
				if (done) break;
				buffer += decoder.decode(value, { stream: true });

				// SSE 파싱: "event: xxx\ndata: {...}\n\n"
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
					if (dataStr) {
						try {
							onEvent(eventType, JSON.parse(dataStr));
						} catch {
							onEvent(eventType, { raw: dataStr });
						}
					}
				}
			}
		})
		.catch((e) => {
			if (e.name !== 'AbortError') {
				onError(e.message || 'Unknown error');
			}
		});

	return () => controller.abort();
}
