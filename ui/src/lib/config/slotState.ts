import {
	faCircle, faSpinner, faCircleXmark, faStop, faCircleStop,
	faTriangleExclamation, faHourglass, faCircleDot
} from '@fortawesome/free-solid-svg-icons';
import type { IconDefinition } from '@fortawesome/fontawesome-svg-core';

// ---------------------------------------------------------------------------
// Color token — 각 상태가 어떤 색상 계열인지 정의
// 새 상태 추가 시 여기에 한 줄만 추가하면 됩니다.
// ---------------------------------------------------------------------------

export type ColorToken =
	| 'emerald' | 'red' | 'amber' | 'violet' | 'cyan'
	| 'blue' | 'sky' | 'gray' | 'slate' | 'orange' | 'rose' | 'fuchsia' | 'zinc';

/** 정확히 일치하는 상태 → 색상 토큰 (대문자 키) */
const EXACT_STATE_COLORS: Record<string, ColorToken> = {
	PASS:            'emerald',
	WARNING_PASS:    'amber',
	FAIL:            'red',
	TIMEOUT_FAIL:    'red',
	BOOTING_FAIL_D:  'red',
	BOOTING_FAIL_L:  'red',
	RUNNING:         'amber',
	WARNING:         'amber',
	CHARGE:          'blue',
	PAUSE:           'violet',
	STOP:            'cyan',
	EMERGENCY_STOP:  'rose',
	CRITICAL_FAIL:   'fuchsia',
	READY:           'sky',
	DISCONNECT:      'zinc',
	NOTSTART:        'gray',
	COUNT:           'gray',
};

/** 정확한 문자열 매칭 → contains fallback으로 색상 토큰 반환 */
export function getStateColor(state: string): ColorToken {
	// 1. Exact match
	const exact = EXACT_STATE_COLORS[state.toUpperCase()];
	if (exact) return exact;

	// 2. Contains fallback (우선순위 순서)
	const s = state.toLowerCase();
	if (s.includes('critical')) return 'fuchsia';
	if (s.includes('emergency')) return 'rose';
	if (s.includes('timeout') && s.includes('fail')) return 'red';
	if (s.includes('booting') && s.includes('fail')) return 'red';
	if (s.includes('warning') && s.includes('pass')) return 'amber';
	if (s.includes('pass')) return 'emerald';
	if (s.includes('fail')) return 'red';
	if (s.includes('running')) return 'amber';
	if (s.includes('stop')) return 'cyan';
	if (s.includes('warning')) return 'amber';
	if (s.includes('pause')) return 'violet';
	if (s.includes('charge') || s.includes('charging')) return 'blue';
	if (s.includes('ready')) return 'sky';
	if (s.includes('disconnect') || s.includes('inactive')) return 'zinc';

	return 'gray';
}

// ---------------------------------------------------------------------------
// Badge CSS — ResultCell, 대시보드 등에서 사용
// ---------------------------------------------------------------------------

const BADGE_CLASSES: Record<ColorToken, string> = {
	emerald: 'bg-emerald-100 text-emerald-700',
	red:     'bg-red-100 text-red-700',
	amber:   'bg-amber-100 text-amber-700',
	violet:  'bg-violet-100 text-violet-700',
	cyan:    'bg-cyan-100 text-cyan-700',
	blue:    'bg-blue-100 text-blue-700',
	sky:     'bg-sky-100 text-sky-700',
	gray:    'bg-gray-100 text-gray-700',
	slate:   'bg-slate-100 text-slate-700',
	orange:  'bg-orange-100 text-orange-700',
	rose:    'bg-rose-100 text-rose-700',
	fuchsia: 'bg-fuchsia-100 text-fuchsia-700',
	zinc:    'bg-zinc-100 text-zinc-700',
};

/** 상태 문자열 → 배지 CSS 클래스 */
export function getStateBadgeClass(state: string): string {
	return BADGE_CLASSES[getStateColor(state)];
}

// ---------------------------------------------------------------------------
// Border CSS — 대시보드 슬롯 요약 등에서 사용
// ---------------------------------------------------------------------------

const BORDER_CLASSES: Record<ColorToken, string> = {
	emerald: 'bg-emerald-100 border-emerald-300',
	red:     'bg-red-100 border-red-300',
	amber:   'bg-amber-100 border-amber-300',
	violet:  'bg-violet-100 border-violet-300',
	cyan:    'bg-cyan-100 border-cyan-300',
	blue:    'bg-blue-100 border-blue-300',
	sky:     'bg-sky-100 border-sky-300',
	gray:    'bg-muted border-border',
	slate:   'bg-slate-100 border-slate-300',
	orange:  'bg-orange-100 border-orange-300',
	rose:    'bg-rose-100 border-rose-300',
	fuchsia: 'bg-fuchsia-100 border-fuchsia-300',
	zinc:    'bg-zinc-100 border-zinc-300',
};

/** 상태 문자열 → 보더 포함 배경 CSS 클래스 */
export function getStateBorderClass(state: string): string {
	return BORDER_CLASSES[getStateColor(state)];
}

// ---------------------------------------------------------------------------
// Gradient CSS — SlotCard 배경에서 사용
// ---------------------------------------------------------------------------

const GRADIENT_CLASSES: Record<ColorToken, string> = {
	emerald: 'bg-linear-to-t from-emerald-200 to-emerald-50',
	red:     'bg-linear-to-t from-red-200 to-red-50',
	amber:   'bg-linear-to-t from-amber-200 to-amber-50',
	violet:  'bg-linear-to-t from-violet-200 to-violet-50',
	cyan:    'bg-linear-to-t from-cyan-200 to-cyan-50',
	blue:    'bg-linear-to-t from-blue-200 to-blue-50',
	sky:     'bg-linear-to-t from-sky-200 to-sky-50',
	gray:    'bg-linear-to-t from-gray-200 to-gray-50',
	slate:   'bg-linear-to-t from-slate-300 to-slate-100',
	orange:  'bg-linear-to-t from-orange-200 to-orange-50',
	rose:    'bg-linear-to-t from-rose-200 to-rose-50',
	fuchsia: 'bg-linear-to-t from-fuchsia-200 to-fuchsia-50',
	zinc:    'bg-linear-to-t from-zinc-300 to-zinc-100',
};

/** 색상 토큰 → 그라데이션 CSS 클래스 */
export function getGradientClass(color: ColorToken): string {
	return GRADIENT_CLASSES[color];
}

// ---------------------------------------------------------------------------
// Text color CSS — 아이콘·텍스트 색상
// ---------------------------------------------------------------------------

const TEXT_CLASSES: Record<ColorToken, string> = {
	emerald: 'text-emerald-600',
	red:     'text-red-600',
	amber:   'text-amber-600',
	violet:  'text-violet-600',
	cyan:    'text-cyan-600',
	blue:    'text-blue-600',
	sky:     'text-sky-500',
	gray:    'text-gray-400',
	slate:   'text-slate-500',
	orange:  'text-orange-600',
	rose:    'text-rose-600',
	fuchsia: 'text-fuchsia-600',
	zinc:    'text-zinc-800',
};

/** 색상 토큰 → 텍스트 색상 CSS 클래스 */
export function getTextClass(color: ColorToken): string {
	return TEXT_CLASSES[color];
}

// ---------------------------------------------------------------------------
// Icon — SlotCard에서 사용하는 아이콘 + 애니메이션 매핑
// testState 문자열을 includes 기반 우선순위로 매칭
// ---------------------------------------------------------------------------

export type IconInfo = { fa: IconDefinition; animate: boolean; color: ColorToken };

const PROGRESS_KEYWORDS = ['onedown', 'running ffu', 'provisioning', 'downloading', 'getting info'];

/** testState 문자열 → 아이콘 + 애니메이션 + 색상 토큰 */
export function resolveSlotIcon(testState: string): IconInfo {
	const ts = testState.toLowerCase().trim();

	if (PROGRESS_KEYWORDS.some((k) => ts.includes(k))) {
		if (ts.includes('stop')) return { fa: faCircleStop, animate: false, color: 'cyan' };
		if (ts.includes('fail')) return { fa: faCircleXmark, animate: false, color: 'red' };
		if (ts.includes('pass')) return { fa: faCircleDot, animate: false, color: 'violet' };
		return { fa: faSpinner, animate: true, color: 'violet' };
	}
	if (ts.includes('critical')) return { fa: faCircleXmark, animate: false, color: 'red' };
	if (ts.includes('stop')) return { fa: faCircleStop, animate: false, color: 'cyan' };
	if (ts.includes('warning') && ts.includes('pass')) return { fa: faTriangleExclamation, animate: false, color: 'amber' };
	if (ts.includes('warning')) return { fa: faTriangleExclamation, animate: false, color: 'amber' };
	if (ts.includes('ready')) return { fa: faCircleDot, animate: false, color: 'sky' };
	if (ts.includes('dis') || ts.includes('waiting') || ts.includes('inactive'))
		return { fa: faHourglass, animate: false, color: 'zinc' };
	if (ts.includes('booting') && ts.includes('fail')) return { fa: faCircleXmark, animate: false, color: 'red' };
	if (ts.includes('booting')) return { fa: faSpinner, animate: true, color: 'cyan' };
	if (ts.includes('fail')) return { fa: faCircleXmark, animate: false, color: 'red' };
	if (ts.includes('pass')) return { fa: faCircleDot, animate: false, color: 'emerald' };
	if (ts.includes('running')) return { fa: faSpinner, animate: true, color: 'emerald' };
	return { fa: faCircle, animate: false, color: 'gray' };
}

/** testState 문자열 → 그라데이션 CSS
 *  토스 철학: 움직임은 최소한으로 — pulse 대신 좌측 컬러 바로 상태 표현 */
export function resolveSlotGradient(testState: string): string {
	const ts = testState.toLowerCase().trim();

	if (PROGRESS_KEYWORDS.some((k) => ts.includes(k) && !ts.includes('stop') && !ts.includes('fail')))
		return getGradientClass('violet');
	if (ts.includes('running'))
		return getGradientClass('emerald');

	const icon = resolveSlotIcon(testState);
	return getGradientClass(icon.color);
}
