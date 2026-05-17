import type { SlotInfomation } from '$lib/api/types.js';
import type { HeadSlotData } from '$lib/api/headSlotStore.svelte.js';

export interface SlotHoverInfo {
	slot: SlotInfomation;
	headData?: HeadSlotData;
	hasMemo?: boolean;
	hasSlotPreCmd?: boolean;
	hasTcPreCmd?: boolean;
	hasPreCommand?: boolean;
	preCommandName?: string;
	metaMonitoring?: boolean;
}

let current = $state<SlotHoverInfo | null>(null);
// hover 떼면 즉시 비우지 않고 짧은 유예 두기 — 다른 슬롯으로 이동 중이면 setHover 가 타이머 취소.
let clearTimer: ReturnType<typeof setTimeout> | null = null;
const CLEAR_DELAY_MS = 250;

export const slotHover = {
	get current() {
		return current;
	},

	/** 슬롯에 hover 진입 — 즉시 표시. */
	set(info: SlotHoverInfo) {
		if (clearTimer) {
			clearTimeout(clearTimer);
			clearTimer = null;
		}
		current = info;
	},

	/** hover 종료 — 짧은 유예 후 비움. 다른 슬롯으로 즉시 이동 시 set 이 타이머 취소. */
	clearSoon() {
		if (clearTimer) clearTimeout(clearTimer);
		clearTimer = setTimeout(() => {
			current = null;
			clearTimer = null;
		}, CLEAR_DELAY_MS);
	},

	/** 즉시 비움 (페이지 이동 등). */
	clearNow() {
		if (clearTimer) {
			clearTimeout(clearTimer);
			clearTimer = null;
		}
		current = null;
	}
};
