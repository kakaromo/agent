/**
 * 중첩 JSON object를 dot notation으로 flatten.
 * { gc: { nMinEc: 1, nMaxEc: 2 } } → { "gc.nMinEc": 1, "gc.nMaxEc": 2 }
 * 배열은 flatten하지 않고 그대로 유지.
 */
export function flattenObject(
	obj: Record<string, any>,
	prefix = ''
): Record<string, any> {
	return Object.entries(obj).reduce(
		(acc, [key, val]) => {
			const newKey = prefix ? `${prefix}.${key}` : key;
			if (val !== null && typeof val === 'object' && !Array.isArray(val)) {
				Object.assign(acc, flattenObject(val, newKey));
			} else {
				acc[newKey] = val;
			}
			return acc;
		},
		{} as Record<string, any>
	);
}

/**
 * MetadataEntry 배열에서 키를 분류.
 * 첫 번째 entry(time 제외)의 값 타입으로 판단.
 * arrayKeys: 숫자 배열 (히트맵 차트용)
 */
export function classifyKeys(entries: Record<string, any>[]): {
	numberKeys: string[];
	stringKeys: string[];
	objectKeys: string[];
	arrayKeys: string[];
} {
	if (entries.length === 0) return { numberKeys: [], stringKeys: [], objectKeys: [], arrayKeys: [] };

	const first = entries[0];
	const numberKeys: string[] = [];
	const stringKeys: string[] = [];
	const objectKeys: string[] = [];
	const arrayKeys: string[] = [];

	for (const [key, val] of Object.entries(first)) {
		if (key === 'time') continue;
		if (typeof val === 'number') {
			numberKeys.push(key);
		} else if (typeof val === 'string') {
			stringKeys.push(key);
		} else if (Array.isArray(val) && val.length > 0 && val.every((v) => typeof v === 'number')) {
			arrayKeys.push(key);
		} else if (typeof val === 'object' && val !== null) {
			objectKeys.push(key);
		}
	}

	return { numberKeys, stringKeys, objectKeys, arrayKeys };
}

/**
 * 특정 키들의 값을 delta(차분)로 변환.
 * 숫자: 원본 [100, 105, 112] → delta [0, 5, 7]
 * 배열: 원소별 delta 적용
 */
export function applyDelta(
	entries: Record<string, any>[],
	deltaKeys: Set<string>
): Record<string, any>[] {
	if (entries.length === 0 || deltaKeys.size === 0) return entries;

	return entries.map((entry, i) => {
		const result = { ...entry };
		for (const key of deltaKeys) {
			if (!(key in entry)) continue;
			const val = entry[key];
			const prev = i > 0 ? entries[i - 1][key] : null;

			if (typeof val === 'number') {
				result[key] = i === 0 ? 0 : (typeof prev === 'number' ? val - prev : 0);
			} else if (Array.isArray(val) && Array.isArray(prev)) {
				result[key] = i === 0
					? val.map(() => 0)
					: val.map((v, j) => typeof v === 'number' && typeof prev[j] === 'number' ? v - prev[j] : 0);
			} else if (Array.isArray(val)) {
				result[key] = val.map(() => 0);
			}
		}
		return result;
	});
}
