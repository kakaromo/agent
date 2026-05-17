import { auth, getCsrfToken } from '$lib/stores/auth.svelte.js';

interface SessionState {
	expiresAt: number;
	warnBeforeMs: number;
	showWarning: boolean;
	timeRemaining: number;
}

let state = $state<SessionState>({
	expiresAt: 0,
	warnBeforeMs: 5 * 60 * 1000,
	showWarning: false,
	timeRemaining: 0
});

let timer: ReturnType<typeof setInterval> | null = null;

function tick() {
	if (state.expiresAt <= 0) return;
	const remaining = Math.max(0, state.expiresAt - Date.now());
	state.timeRemaining = Math.ceil(remaining / 1000);

	if (remaining <= state.warnBeforeMs && remaining > 0) {
		state.showWarning = true;
	}

	if (remaining <= 0) {
		state.showWarning = false;
		state.expiresAt = 0;
		state.timeRemaining = 0;
		destroy();
		auth.logout();
	}
}

function destroy() {
	if (timer) {
		clearInterval(timer);
		timer = null;
	}
}

export const sessionStore = {
	get showWarning() { return state.showWarning; },
	get timeRemaining() { return state.timeRemaining; },

	get timeRemainingFormatted(): string {
		const m = Math.floor(state.timeRemaining / 60);
		const s = state.timeRemaining % 60;
		return `${m}분 ${s.toString().padStart(2, '0')}초`;
	},

	async init() {
		try {
			const res = await fetch('/api/auth/session-info');
			if (!res.ok) return;
			const data = await res.json();
			state.expiresAt = data.expiresAt;
			state.warnBeforeMs = (data.warnBeforeMinutes ?? 5) * 60 * 1000;
			state.showWarning = false;

			destroy();
			tick();
			timer = setInterval(tick, 1000);
		} catch {
			// 세션 정보 조회 실패 시 타이머 비활성
		}
	},

	async extend() {
		try {
			const res = await fetch('/api/auth/session-extend', {
				method: 'POST',
				headers: { 'X-XSRF-TOKEN': getCsrfToken() }
			});
			if (res.ok) {
				const data = await res.json();
				state.expiresAt = data.expiresAt;
				state.showWarning = false;
			}
		} catch {
			// 연장 실패
		}
	},

	destroy
};
