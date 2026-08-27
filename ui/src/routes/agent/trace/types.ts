/**
 * portal `/trace` 에서 복사해 온 컴포넌트들이 쓰는 타입 모음.
 *
 * 원본에서 이 타입들은 `$lib/api/trace.js`(portal 전용 REST 계층)와
 * `$lib/utils/arrow-decoder.js`(Arrow IPC 디코더)에 흩어져 있다. standalone 은
 * 그 두 백엔드 경로를 **쓰지 않으므로**(데이터는 `$lib/api/agent.ts` 의 gRPC 경유
 * REST 로 온다) 타입만 여기 한 곳에 모아 둔다.
 *
 * 덕분에 복사해 온 컴포넌트에 가하는 수정이 **import 경로 한 줄**로 끝난다.
 * (README.md 의 "허용되는 유일한 수정" 참고)
 */

/** 시나리오 스텝 구간. agent `lib/api/agent.ts` 의 것과 필드가 동일하다. */
export type StepBoundary = {
	stepIndex: number;
	loopIndex: number;
	repeatIndex: number;
	type: string;
	label: string;
	startedAt: number; // host wall clock (epoch ms) — 로그 대조용
	finishedAt: number;
	startedMono: number; // 기기 boot 기준 절대초
	finishedMono: number;
	success: boolean;
	error: string;
};

/** 차트 상단 meta 바(총/샘플 건수). */
export type ChartMeta = {
	totalEvents: number;
	sampledEvents: number;
	schemaVersion: string;
	stats: unknown | null;
};

export type StatsLatency = {
	min: number;
	max: number;
	avg: number;
	stddev: number;
	median: number;
	p99: number;
	p999: number;
	p9999: number;
	p99999: number;
	p999999: number;
	/** 모수. 모르면 NaN → 표가 '-' 를 그린다. 0 을 넣으면 틀린 사실이 된다. */
	count: number;
};

export type StatsCmd = {
	cmd: string;
	count: number;
	sendCount: number;
	ratio: number;
	totalSizeBytes: number;
	continuousCount: number;
	continuousRatio: number;
	dtoc: StatsLatency;
	ctod: StatsLatency;
	ctoc: StatsLatency;
	qd: StatsLatency;
};

export type StatsHistogram = {
	latencyType: 'dtoc' | 'ctod' | 'ctoc';
	cmd: string;
	buckets: { rangeStartMs: number; rangeEndMs: number; count: number }[];
};

/** UFS management 이벤트 집계 (fsio_ufs 전용). */
export type StatsMgmt = {
	/** 표시 이름 — "Read Descriptor(Geometry)" / "DME_HIBERNATE_EXIT" */
	name: string;
	kind: 'query' | 'tm' | 'uic' | 'other';
	/** send + complete 양쪽 합 */
	count: number;
	/** 짝지어져 latency 가 계산된 건수 */
	pairedCount: number;
	/** dtoc 합계(ms) — 링크 점유 시간 */
	totalTimeMs: number;
	dtoc: StatsLatency;
};

export type StatsResponse = {
	totalEvents: number;
	sendCount: number;
	durationSeconds: number;
	continuousCount: number;
	continuousRatio: number;
	alignedCount: number;
	alignedRatio: number;
	readTotalBytes: number;
	writeTotalBytes: number;
	discardTotalBytes: number;
	dtoc: StatsLatency;
	ctod: StatsLatency;
	ctoc: StatsLatency;
	qd: StatsLatency;
	cmdStats: StatsCmd[];
	latencyHistograms: StatsHistogram[];
	cmdSizeCounts: { cmd: string; size: number; count: number }[];
	schemaVersion: string;
	/**
	 * optional 인 이유 — 구버전 응답은 이 키를 아예 안 보낸다. non-optional 로 두면
	 * 그런 응답에서 `.length` 접근이 터진다. 위 totalEvents/cmdStats 는 전부
	 * mgmt 를 **제외한** 데이터 IO 기준이다.
	 */
	mgmtStats?: StatsMgmt[];
};

/**
 * Raw Data 의 컬럼 필터.
 *
 * ⚠ portal 은 이걸 **서버로 보낸다**(parquet 을 1000행씩 서버 페이징하므로 로드된
 * 행만 걸러선 안 된다). standalone 은 `/trace/raw` 가 이벤트를 **한 번에 다** 주므로
 * 클라이언트에서 거른다 — 왕복이 없어 즉시 반영되고, 뒤쪽 페이지를 놓칠 위험도 없다.
 * 그래서 타입만 공유하고 적용은 각자 한다.
 */
export type ColumnFilterOp = 'IN' | 'NOT_IN' | 'CONTAINS' | 'RANGE';

export type ColumnFilter = {
	column: string;
	op: ColumnFilterOp;
	/** IN/NOT_IN 은 전체, CONTAINS 는 [0], RANGE 는 [min, max] (빈 문자열 = 무제한) */
	values: string[];
};
