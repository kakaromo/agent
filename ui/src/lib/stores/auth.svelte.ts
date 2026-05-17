// standalone: 인증 없음. portal 의 auth API 시그니처는 그대로 유지하되,
// 항상 ADMIN 으로 인증된 상태로 응답해 페이지 가드를 우회한다.

function getCsrfToken(): string {
	// standalone REST 핸들러는 XSRF 검증 안 함. 빈 토큰 OK.
	return '';
}

interface AuthState {
	authenticated: boolean;
	disabled: boolean;
	name: string;
	email: string;
	role: string;
	username: string;
	loading: boolean;
	needsPassword: boolean;
	permissions: Record<string, boolean>;
	testInstance: boolean;
}

let state = $state<AuthState>({
	authenticated: true,
	disabled: false,
	name: 'standalone',
	email: 'standalone@local',
	role: 'ADMIN',
	username: 'standalone',
	loading: false,
	needsPassword: false,
	permissions: {},
	testInstance: false
});

export const auth = {
	get authenticated() { return state.authenticated; },
	get disabled() { return state.disabled; },
	get name() { return state.name; },
	get email() { return state.email; },
	get role() { return state.role; },
	get username() { return state.username; },
	get loading() { return state.loading; },
	get isAdmin() { return true; },
	get needsPassword() { return false; },
	get permissions() { return state.permissions; },
	get isTestInstance() { return false; },

	hasPermission(_key: string): boolean { return true; },
	hasMenu(_menuId: string): boolean { return true; },
	hasAction(_menuId: string, _action: string): boolean { return true; },

	async fetchMe() {
		// no-op — standalone 은 항상 인증
		state.loading = false;
	},
	async login(_u: string, _p: string) {
		return { success: true };
	},
	async logout() {
		// no-op
	},
	async changePassword(_c: string | null, _n: string) {
		return { success: true };
	},
	redirectToLogin() { /* no-op */ }
};

export { getCsrfToken };
