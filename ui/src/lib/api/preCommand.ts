import { get, post, put, del } from './client.js';
import { getCsrfToken } from '$lib/stores/auth.svelte.js';

export interface PreCommand {
	id: number;
	name: string;
	description: string | null;
	commands: string; // JSON array string
	createdAt: string;
	updatedAt: string;
}

export function listPreCommands(): Promise<PreCommand[]> {
	return get('/pre-commands');
}

export function createPreCommand(data: {
	name: string;
	description?: string;
	commands: string;
}): Promise<PreCommand> {
	return post('/pre-commands', data);
}

export function updatePreCommand(
	id: number,
	data: { name: string; description?: string; commands: string }
): Promise<PreCommand> {
	return put(`/pre-commands/${id}`, data);
}

export function deletePreCommand(id: number): Promise<{ success: boolean }> {
	return del(`/pre-commands/${id}`);
}

// ── 슬롯별 등록 (setLocation 기반) ──

export interface SlotAssignment {
	setLocation: string;
	preCommandId?: number;
	preCommandName?: string;
	hasTcPreCommand?: boolean;
}

export function listSlotAssignments(setLocations: string[]): Promise<SlotAssignment[]> {
	const params = setLocations.map(l => `setLocations=${encodeURIComponent(l)}`).join('&');
	return get(`/pre-commands/slots?${params}`);
}

export function assignSlots(
	preCommandId: number,
	setLocations: string[]
): Promise<{ success: boolean; count: number }> {
	return post('/pre-commands/slots/assign', { preCommandId, setLocations });
}

/** 슬롯에 TC Pre-Command가 등록되어 있는지 확인 */
export function hasTcPreCommands(
	setLocations: string[]
): Promise<{ hasTc: boolean }> {
	const params = setLocations.map(l => `setLocations=${encodeURIComponent(l)}`).join('&');
	return get(`/pre-commands/slots/has-tc?${params}`);
}

export function unassignSlots(
	setLocations: string[]
): Promise<{ success: boolean }> {
	return post('/pre-commands/slots/unassign', { setLocations });
}

// ── TC별 등록 (setLocation + position 기반) ──

export function getTcAssignments(
	setLocation: string
): Promise<{ tcPreCommandIds: string }> {
	return get(`/pre-commands/tc?setLocation=${encodeURIComponent(setLocation)}`);
}

export function assignTc(
	preCommandId: number,
	setLocation: string,
	tcPosition: number
): Promise<{ success: boolean }> {
	return post('/pre-commands/tc/assign', { preCommandId, setLocation, tcPosition });
}

export function unassignTc(
	setLocation: string,
	tcPosition: number
): Promise<{ success: boolean }> {
	return post('/pre-commands/tc/unassign', { setLocation, tcPosition });
}

/** testcaseIds 변경 시 tcPreCommandIds 동기화 */
export function syncTcPreCommandIds(
	setLocation: string,
	tcPreCommandIds: string
): Promise<{ success: boolean }> {
	return post('/pre-commands/tc/sync', { setLocation, tcPreCommandIds });
}

// ── SSE 실행 ──

export interface PreCommandSlotEvent {
	type:
		| 'start'
		| 'slot-skip'
		| 'slot-start'
		| 'slot-done'
		| 'cmd-start'
		| 'cmd-done'
		| 'summary'
		| 'done'
		| 'error';
	data: Record<string, any>;
}

export function executePreCommand(
	preCommandId: number,
	source: string,
	setLocations: string[],
	onEvent: (event: PreCommandSlotEvent) => void,
	onError: (error: string) => void
): () => void {
	const controller = new AbortController();

	fetch('/api/pre-commands/execute', {
		method: 'POST',
		headers: {
			'Content-Type': 'application/json',
			'X-XSRF-TOKEN': getCsrfToken()
		},
		body: JSON.stringify({ preCommandId, source, setLocations }),
		signal: controller.signal
	})
		.then(async (response) => {
			if (!response.ok) throw new Error(`Execute failed (${response.status})`);
			const reader = response.body?.getReader();
			if (!reader) throw new Error('ReadableStream not supported');

			const decoder = new TextDecoder();
			let buffer = '';

			while (true) {
				const { done, value } = await reader.read();
				if (done) break;

				buffer += decoder.decode(value, { stream: true });

				const blocks = buffer.split('\n\n');
				buffer = blocks.pop() || '';

				for (const block of blocks) {
					if (!block.trim()) continue;
					let eventName = '';
					const dataLines: string[] = [];
					for (const line of block.split('\n')) {
						if (line.startsWith('event:')) {
							eventName = line.substring(6).trim();
						} else if (line.startsWith('data:')) {
							dataLines.push(line.substring(5));
						}
					}
					if (eventName && dataLines.length > 0) {
						const eventData = dataLines.join('').trim();
						try {
							const data = JSON.parse(eventData);
							onEvent({ type: eventName as PreCommandSlotEvent['type'], data });
						} catch {
							// ignore parse errors
						}
					}
				}
			}

			onEvent({ type: 'done', data: {} });
		})
		.catch((e) => {
			if (e.name !== 'AbortError') {
				onError(e instanceof Error ? e.message : String(e));
			}
		});

	return () => controller.abort();
}
