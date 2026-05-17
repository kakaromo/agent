import { getCsrfToken } from '$lib/stores/auth.svelte.js';

const BASE_URL = '/api';

async function request<T>(path: string, init?: RequestInit): Promise<T> {
	const headers: Record<string, string> = { 'Content-Type': 'application/json' };

	// Add XSRF token for mutation requests
	const method = init?.method?.toUpperCase();
	if (method === 'POST' || method === 'PUT' || method === 'DELETE' || method === 'PATCH') {
		headers['X-XSRF-TOKEN'] = getCsrfToken();
	}

	const res = await fetch(`${BASE_URL}${path}`, {
		headers,
		...init
	});

	if (res.status === 401) {
		window.location.href = '/';
		throw new Error('Unauthorized');
	}

	if (res.status === 403) {
		const errorText = await res.text().catch(() => '');
		let msg = '권한이 없습니다';
		try { const body = JSON.parse(errorText); msg = body.error || msg; } catch { /* ignore */ }
		throw new Error(msg);
	}

	// standalone: 잡 status 가 404 + body 에 {state:"failed"} 면 정상 데이터로 처리.
	// portal Spring 동작과 동일. (잡이 agent 메모리에서 만료된 경우)
	if (res.status === 404) {
		const errorText = await res.text().catch(() => '');
		try {
			const body = JSON.parse(errorText);
			if (body && body.state) return body as T;
		} catch { /* not a state response */ }
		// 그 외 404 는 throw
		throw new Error('Not found');
	}

	if (!res.ok) {
		const errorText = await res.text().catch(() => res.statusText);
		console.error(`API Error [${init?.method || 'GET'}] ${path}:`, res.status, errorText);

		// 비즈니스 에러(409 등)는 서버 메시지를 파싱하여 전달
		let userMessage = '요청을 처리할 수 없습니다.';
		if (res.status === 409) {
			try {
				const body = JSON.parse(errorText);
				userMessage = body.error || userMessage;
			} catch { /* ignore */ }
		}
		throw new Error(userMessage);
	}
	if (res.status === 204) return undefined as T;
	return res.json();
}

export function get<T>(path: string): Promise<T> {
	return request<T>(path);
}

export function post<T>(path: string, body: unknown): Promise<T> {
	return request<T>(path, { method: 'POST', body: JSON.stringify(body) });
}

export function put<T>(path: string, body: unknown): Promise<T> {
	return request<T>(path, { method: 'PUT', body: JSON.stringify(body) });
}

export function patch<T>(path: string, body: unknown): Promise<T> {
	return request<T>(path, { method: 'PATCH', body: JSON.stringify(body) });
}

export function del<T = void>(path: string): Promise<T> {
	return request<T>(path, { method: 'DELETE' });
}
