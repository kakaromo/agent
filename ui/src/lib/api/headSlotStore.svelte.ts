import { reconnectHead } from './testdb.js';

export interface HeadSlotData {
	source: string;
	slotIndex: number;
	connection: number; // 0=not connected, 1=connected, 2=upload possible
	runningTime: string;
	setModelName: string;
	remainBattery: string;
	freeArea: string;
	testToolName: string;
	testTrName: string;
	state: string;
	testState: string;
	testArea: string;
	setLocation: string;
	runningState: string;
	minBattery: string;
	maxBattery: string;
	testHistoryIds: string;
	testCaseIds: string;
	testCaseStatus: string;
	headType: number;           // 0=compatibility, 1=performance
	preIdGlobalCxt: string;
	bootCxt: string;
	smartReport: string;
	osv: string;
	usbId: string;
	npoCount: string;
	productName: string;
	deviceName: string;
	fileSystem: string;
	fwVer: string;
	fwDate: string;
	controller: string;
	nandType: string;
	nandSize: string;
	cellType: string;
	density: string;
	server: string;
	board: string;
	product: string | null;
	tentacleIp?: string;
	testTrId?: number | null;
	testFinishTimes?: string;
}

export interface ConnectionStatus {
	name: string;
	headType: number;       // 0=compatibility, 1=performance
	connected: boolean;
	testMode?: boolean;
	error?: string;
}

interface HeadSsePayload {
	slots: HeadSlotData[];
	version: number;
	connections: ConnectionStatus[];
}

export function createHeadSlotStore(source?: string) {
	let slots = $state<HeadSlotData[]>([]);
	let connections = $state<ConnectionStatus[]>([]);
	let connected = $state(false);
	let connecting = $state(false);
	let version = $state(0);
	let retryCount = $state(0);
	let lastError = $state<string | null>(null);
	let eventSource: EventSource | null = null;

	/** Deduplicate slots by slotIndex (last wins) */
	function dedup(raw: HeadSlotData[]): HeadSlotData[] {
		const map = new Map<number, HeadSlotData>();
		for (const s of raw) map.set(s.slotIndex, s);
		return [...map.values()];
	}

	function connect() {
		if (eventSource) {
			eventSource.close();
		}

		connecting = true;
		lastError = null;

		const params = source ? `?source=${source}` : '';
		eventSource = new EventSource(`/api/head/slots/stream${params}`);

		eventSource.addEventListener('init', (e: MessageEvent) => {
			const payload: HeadSsePayload = JSON.parse(e.data);
			slots = dedup(payload.slots);
			version = payload.version;
			connections = payload.connections;
			connected = true;
			connecting = false;
			retryCount = 0;
			lastError = null;
		});

		eventSource.addEventListener('update', (e: MessageEvent) => {
			const payload: HeadSsePayload = JSON.parse(e.data);
			slots = dedup(payload.slots);
			version = payload.version;
			connections = payload.connections;
		});

		eventSource.onopen = () => {
			connected = true;
			connecting = false;
			retryCount = 0;
			lastError = null;
		};

		eventSource.onerror = () => {
			connected = false;
			connecting = false;
			lastError = 'Connection failed';
			// EventSource auto-reconnects by default
		};
	}

	function disconnect() {
		if (eventSource) {
			eventSource.close();
			eventSource = null;
		}
		connected = false;
		connecting = false;
	}

	async function retry() {
		retryCount++;
		connecting = true;
		lastError = null;

		// 1. Request backend to reconnect to Head server
		if (source) {
			try {
				await reconnectHead(source);
			} catch (e) {
				// Backend reconnect failed, but still try SSE reconnect
				console.warn(`Backend reconnect failed for ${source}:`, e);
			}
		}

		// 2. Reconnect SSE stream
		disconnect();
		connect();
	}

	return {
		get slots() {
			return slots;
		},
		get connections() {
			return connections;
		},
		get connected() {
			return connected;
		},
		get connecting() {
			return connecting;
		},
		get version() {
			return version;
		},
		get retryCount() {
			return retryCount;
		},
		get lastError() {
			return lastError;
		},
		connect,
		disconnect,
		retry
	};
}
