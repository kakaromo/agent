import { toast } from 'svelte-sonner';

/**
 * Backend 의 `EntityChangeEvent` 와 동일 shape.
 */
export interface EntityChangeEvent {
	entityType: string;
	entityId: string | null;
	action: 'create' | 'update' | 'delete' | 'bulk' | string;
	source: string | null;
	timestamp: number;
}

interface ListenerEntry {
	types: Set<string>;
	handler: (e: EntityChangeEvent) => void;
}

// ── Module-level singleton ──
let eventSource: EventSource | null = null;
let connected = $state(false);
let connecting = $state(false);
let lastError = $state<string | null>(null);

const listeners = new Set<ListenerEntry>();
const suspendTokens = new Set<string>();
const recentSuspendToasts = new Map<string, number>(); // entityType → 마지막 toast 발행 ms

// suspend 상태에서 같은 entityType 으로 toast 가 1초 안에 여러 번 뜨는 것을 막기 위함
const TOAST_DEDUPE_MS = 1000;

function ensureConnected() {
	if (eventSource || connecting) return;
	connecting = true;
	lastError = null;

	eventSource = new EventSource('/api/db-changes/stream');

	eventSource.addEventListener('init', () => {
		connected = true;
		connecting = false;
	});

	eventSource.addEventListener('update', (e: MessageEvent) => {
		try {
			const ev: EntityChangeEvent = JSON.parse(e.data);
			handleEvent(ev);
		} catch (err) {
			console.warn('entityChange: failed to parse event', err);
		}
	});

	eventSource.onopen = () => {
		connected = true;
		connecting = false;
		lastError = null;
	};

	eventSource.onerror = () => {
		connected = false;
		connecting = false;
		lastError = 'Connection failed';
		// EventSource 가 자동 재연결.
	};
}

function disconnectIfIdle() {
	if (listeners.size > 0) return;
	if (!eventSource) return;
	eventSource.close();
	eventSource = null;
	connected = false;
}

function handleEvent(ev: EntityChangeEvent) {
	const matched: ListenerEntry[] = [];
	for (const entry of listeners) {
		if (entry.types.size === 0 || entry.types.has(ev.entityType)) {
			matched.push(entry);
		}
	}
	if (matched.length === 0) return;

	if (suspendTokens.size > 0) {
		// dialog/form 열린 상태 — 자동 새로고침 대신 toast 만.
		const now = Date.now();
		const last = recentSuspendToasts.get(ev.entityType) ?? 0;
		if (now - last >= TOAST_DEDUPE_MS) {
			recentSuspendToasts.set(ev.entityType, now);
			toast.info(`${ev.entityType} 데이터가 변경되었습니다 — 새로고침 권장`);
		}
		return;
	}

	for (const entry of matched) {
		try {
			entry.handler(ev);
		} catch (err) {
			console.warn('entityChange: listener error', err);
		}
	}
}

/**
 * 특정 entityType 변경 이벤트를 listen.
 *
 * @param types  단일 또는 다수 entityType 식별자. 빈 배열은 모두.
 * @param handler 변경 시 호출될 콜백 (e.g. refetch).
 * @returns 등록 해제 함수 — `onMount` return 으로 그대로 사용 가능.
 */
export function onEntityChange(
	types: string | string[],
	handler: (e: EntityChangeEvent) => void
): () => void {
	const list = Array.isArray(types) ? types : [types];
	const entry: ListenerEntry = {
		types: new Set(list),
		handler
	};
	listeners.add(entry);
	ensureConnected();
	return () => {
		listeners.delete(entry);
		disconnectIfIdle();
	};
}

/**
 * dialog/form 이 열린 동안 자동 반영을 잠시 멈추고 toast 만 띄우게 한다.
 *
 * @param token 고유 식별자 (다중 dialog 동시 열림 처리 위해 token 단위 register/release).
 * @returns 해제 함수.
 */
export function suspend(token: string): () => void {
	suspendTokens.add(token);
	return () => {
		suspendTokens.delete(token);
	};
}

/** 현재 SSE 연결 상태 — 디버그/표시 용도. */
export function getEntityChangeState() {
	return { connected, connecting, lastError };
}
