/**
 * TC status code → label 매핑.
 * HEAD SSE의 testcaseStatus 숫자 코드를 사람이 읽을 수 있는 라벨로 변환.
 */
export function tcStatusLabel(code: string): string {
	if (code === '-' || code === '') return '';
	const n = parseInt(code);
	if (isNaN(n)) return code;
	switch (n) {
		case 0: case 14: return 'NOTSTART';
		case 4: case 11: case 15: case 37: case 38: return 'FAIL';
		case 22: return 'WARNING';
		case 23: return 'PAUSE';
		case 24: case 25: case 26: return 'CHARGE';
		case 27: case 35: case 36: return 'PASS';
		case 28: case 50: return 'WARNING_PASS';
		case 30: case 39: case 40: return 'TIMEOUT_FAIL';
		case 31: case 41: case 42: return 'BOOTING_FAIL_D';
		case 32: case 43: case 44: return 'BOOTING_FAIL_L';
		case 33: case 45: case 46: return 'STOP';
		case 34: case 47: case 48: return 'DISCONNECT';
		case 49: return 'COUNT';
		case 51: case 52: case 53: return 'EMERGENCY_STOP';
		case 55: case 56: case 57: return 'CRITICAL_FAIL';
		default:
			if (n <= 19 || n === 21) return 'RUNNING';
			return code;
	}
}

/** 완료 상태 코드 Set */
export const COMPLETED_STATUSES = new Set([
	4, 11, 15, 37, 38,      // FAIL
	27, 35, 36,              // PASS
	28, 50,                  // WARNING_PASS
	30, 39, 40,              // TIMEOUT_FAIL
	31, 41, 42,              // BOOTING_FAIL_D
	32, 43, 44,              // BOOTING_FAIL_L
	33, 45, 46,              // STOP
	34, 47, 48,              // DISCONNECT
	51, 52, 53,              // EMERGENCY_STOP
	55, 56, 57               // CRITICAL_FAIL
]);

export function isCompletedStatus(status: number): boolean {
	return COMPLETED_STATUSES.has(status);
}

/** 슬래시로 구분된 문자열을 파싱 (앞에 빈 문자열 제거) */
export function parseSlashList(str: string | undefined): string[] {
	if (!str) return [];
	return str.split('/').filter(s => s.trim() !== '');
}
