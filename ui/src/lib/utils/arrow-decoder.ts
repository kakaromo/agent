/**
 * Arrow IPC stream → ECharts-friendly columnar arrays.
 *
 * Portal 의 `POST /api/trace/chart` 는 Arrow IPC stream 바이너리(body) + 메타데이터 헤더로 응답.
 * 이 모듈은:
 *   1) arrayBuffer → apache-arrow Table
 *   2) ECharts scatter data 배열 생성 (x=time, y=value)
 *   3) BigInt 컬럼(LBA) 은 Float64 로 다운캐스트 — ECharts 가 BigInt 를 직접 렌더 못 함
 */

import { tableFromIPC, type Table } from 'apache-arrow';

export type ChartLatencyStats = {
	min: number;
	max: number;
	avg: number;
	p50: number;
	p95: number;
	p99: number;
	count: number;
};

export type ChartStats = {
	dtoc: ChartLatencyStats;
	ctod: ChartLatencyStats;
	ctoc: ChartLatencyStats;
	qd: ChartLatencyStats;
};

export type ChartMeta = {
	totalEvents: number;
	sampledEvents: number;
	schemaVersion: string;
	stats: ChartStats | null;
};

export type ChartResponse = {
	meta: ChartMeta;
	table: Table;
};

/** fetch Response → ChartResponse. 실패 시 throw (caller 가 abort 구분). */
export async function decodeChartResponse(res: Response): Promise<ChartResponse> {
	if (!res.ok) {
		const txt = await res.text().catch(() => '');
		throw new Error(`chart fetch failed: ${res.status} ${txt}`);
	}
	const buf = await res.arrayBuffer();
	const table = tableFromIPC(new Uint8Array(buf));
	const meta = parseMetaHeaders(res.headers);
	return { meta, table };
}

function parseMetaHeaders(h: Headers): ChartMeta {
	const total = Number(h.get('X-Trace-Total-Events') ?? 0);
	const sampled = Number(h.get('X-Trace-Sampled-Events') ?? 0);
	const schemaVersion = h.get('X-Trace-Schema-Version') ?? '';
	const statsB64 = h.get('X-Trace-Stats');
	let stats: ChartStats | null = null;
	if (statsB64) {
		try {
			const json = atob(statsB64);
			stats = JSON.parse(json) as ChartStats;
		} catch (e) {
			console.warn('X-Trace-Stats parse failed:', e);
		}
	}
	return { totalEvents: total, sampledEvents: sampled, schemaVersion, stats };
}

/**
 * 컬럼을 number 배열로 추출. BigInt (u64) → Number 다운캐스트.
 * 컬럼이 없거나 읽을 수 없으면 null 반환.
 *
 * **성능 경로**: apache-arrow Vector 의 `toArray()` 는 단일 chunk 인 경우
 * 내부 TypedArray (Float64Array / BigUint64Array …) 를 zero-copy 로 반환한다.
 * 기존 `vec.get(i)` 루프 대비 100K~1M 행에서 5~20× 빠름.
 *
 * 호출자는 `number[]` 를 기대하므로:
 *   - Float32/64Array → 그대로 캐스팅 (TypedArray 도 number[] interface 호환)
 *   - BigInt64/Uint64Array → Number 다운캐스트한 일반 배열로 변환
 *   - 그 외 (mixed null 등) → 기존 fallback 루프
 */
export function columnAsNumbers(table: Table, name: string): number[] | null {
	const vec = table.getChild(name);
	if (!vec) return null;
	const n = vec.length;

	// 빠른 경로: 단일 chunk + numeric typed array
	const data = (vec as { data?: Array<{ values?: ArrayLike<number | bigint> }> }).data;
	if (data && data.length === 1 && data[0]?.values) {
		const values = data[0].values as ArrayLike<number | bigint>;
		// BigInt typed array → number[] 다운캐스트 (lba 등 u64 컬럼)
		if (values.length > 0 && typeof values[0] === 'bigint') {
			const out = new Array<number>(n);
			for (let i = 0; i < n; i++) out[i] = Number((values as unknown as bigint[])[i]);
			return out;
		}
		// Float32/64Array, Int32Array 등 — TypedArray 자체가 number[] 호환
		// (단, ECharts/소비자가 Array.isArray() 검사하면 깨질 수 있어 호환을 위해 Array 로 변환할지는
		//  소비자에 따라 결정. 여기서는 zero-copy 우선 — 길이/인덱스 접근만 쓰므로 안전.)
		return values as unknown as number[];
	}

	// fallback: 다중 chunk 또는 nullable 컬럼 — 기존 루프 유지
	const out = new Array<number>(n);
	for (let i = 0; i < n; i++) {
		const v = vec.get(i);
		if (v == null) {
			out[i] = NaN;
		} else if (typeof v === 'bigint') {
			out[i] = Number(v);
		} else {
			out[i] = v as number;
		}
	}
	return out;
}

export function columnAsStrings(table: Table, name: string): string[] | null {
	const vec = table.getChild(name);
	if (!vec) return null;
	const n = vec.length;
	// 문자열 컬럼은 zero-copy 경로가 없음 (Utf8 vector 는 offsets+values 분리 저장).
	// vec.toArray() 가 vec.get(i) 루프 보다 약간 빠르고 dictionary-encoded 도 자동 처리.
	try {
		const arr = (vec as unknown as { toArray: () => unknown[] }).toArray();
		if (Array.isArray(arr) && arr.length === n) {
			const out = new Array<string>(n);
			for (let i = 0; i < n; i++) {
				const v = arr[i];
				out[i] = v == null ? '' : String(v);
			}
			return out;
		}
	} catch { /* fallback 사용 */ }
	const out = new Array<string>(n);
	for (let i = 0; i < n; i++) {
		const v = vec.get(i);
		out[i] = v == null ? '' : String(v);
	}
	return out;
}

/**
 * ECharts scatter 용 [x, y] pair 배열.
 * NaN 값은 스킵. 두 컬럼 길이는 같다고 가정 (같은 RecordBatch 에서 추출).
 */
export function toScatterPairs(xs: number[], ys: number[]): [number, number][] {
	const n = Math.min(xs.length, ys.length);
	const out: [number, number][] = [];
	for (let i = 0; i < n; i++) {
		const x = xs[i];
		const y = ys[i];
		if (Number.isNaN(x) || Number.isNaN(y)) continue;
		out.push([x, y]);
	}
	return out;
}

/** action 별로 인덱스 분류. action 값이 없으면 "unknown" 버킷. */
export function groupIndicesByAction(actions: string[]): Map<string, number[]> {
	const m = new Map<string, number[]>();
	for (let i = 0; i < actions.length; i++) {
		const key = actions[i] || 'unknown';
		let arr = m.get(key);
		if (!arr) {
			arr = [];
			m.set(key, arr);
		}
		arr.push(i);
	}
	return m;
}
