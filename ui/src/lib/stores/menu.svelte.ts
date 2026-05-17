import { fetchMenus, type MenuItem } from '$lib/api/admin.js';
import { auth } from '$lib/stores/auth.svelte.js';

interface MenuState {
	items: MenuItem[];
	loaded: boolean;
}

let state = $state<MenuState>({
	items: [],
	loaded: false
});

export const menuStore = {
	get items() { return state.items; },
	get loaded() { return state.loaded; },

	async fetchMenus() {
		try {
			state.items = await fetchMenus();
		} catch {
			// 서버 연결 실패 시 기본 메뉴 사용
			state.items = [];
		} finally {
			state.loaded = true;
		}
	},

	isVisible(id: string): boolean {
		if (auth.isAdmin) return true;
		// 전체 메뉴 설정에서 숨김이면 숨김
		if (state.loaded && state.items.length > 0) {
			const item = state.items.find(m => m.id === id);
			if (item && !item.visible) return false;
		}
		// 사용자 개별 권한 체크
		return auth.hasMenu(id);
	}
};
