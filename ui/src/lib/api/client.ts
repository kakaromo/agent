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

/**
 * multipart/form-data POST.
 *
 * 공용 request() 를 못 쓴다 — 그쪽은 Content-Type: application/json 을 강제하는데
 * multipart 는 브라우저가 boundary 를 붙여 직접 정해야 한다.
 *
 * 에러 메시지도 서버 것을 그대로 올린다. 업로드 실패는 "포맷을 알 수 없습니다" 처럼
 * **무엇을 고쳐야 하는지**가 본문에 있어서, 일반 문구로 덮으면 쓸모가 없어진다.
 */
export async function postForm<T>(path: string, form: FormData): Promise<T> {
	const res = await fetch(`${BASE_URL}${path}`, {
		method: 'POST',
		headers: { 'X-XSRF-TOKEN': getCsrfToken() },
		body: form
	});
	if (!res.ok) {
		const text = await res.text().catch(() => res.statusText);
		let msg = text;
		try { const body = JSON.parse(text); msg = body.error || text; } catch { /* 평문 그대로 */ }
		throw new Error(msg || '업로드에 실패했습니다');
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
