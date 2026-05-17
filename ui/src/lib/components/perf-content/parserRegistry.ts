import type { Component } from 'svelte';
import GenPerf from './GenPerf.svelte';
import KernelLatency from './KernelLatency.svelte';
import VluLatency from './VluLatency.svelte';
import LongTermTC from './LongTermTC.svelte';
import WearLeveling from './WearLeveling.svelte';
import FragmentWrite from './FragmentWrite.svelte';
import PerfByChunk from './PerfByChunk.svelte';
import VluDirtyCase4 from './VluDirtyCase4.svelte';
import VluRandReadPerThread from './VluRandReadPerThread.svelte';
import UnmapThroughput from './UnmapThroughput.svelte';
import IntervaledReadLatency from './IntervaledReadLatency.svelte';
import WideRandRead from './WideRandRead.svelte';
import WbFlushThroughput from './WbFlushThroughput.svelte';
import WriteAndDelete from './WriteAndDelete.svelte';

export type YAxisMaxType = false | 'number' | 'record';

export interface ParserEntry {
	/** Svelte component */
	component: Component<any>;
	/**
	 * yAxisMax prop 타입:
	 * - false: yAxisMax 미지원
	 * - 'number': 단일 숫자 (CycleEntry[] 데이터)
	 * - 'record': Record<string, number> — 탭/키별 max (Record<string, ...> 데이터)
	 */
	yAxisMaxType: YAxisMaxType;
}

/**
 * parserId → 컴포넌트 매핑 레지스트리.
 * 새 파서를 추가할 때 이 맵만 수정하면 됩니다.
 */
const PARSER_REGISTRY = new Map<number, ParserEntry>();

function register(ids: number[], component: Component<any>, yAxisMaxType: YAxisMaxType = false) {
	const entry: ParserEntry = { component, yAxisMaxType };
	for (const id of ids) {
		PARSER_REGISTRY.set(id, entry);
	}
}

// --- 등록 ---
register([2, 3, 16], GenPerf, 'record');
register([4], FragmentWrite, 'number');
register([5], PerfByChunk);
register([6], IntervaledReadLatency, 'number');
register([10], WearLeveling);
register([15], LongTermTC);
register([20], KernelLatency);
register([21], WriteAndDelete, 'number');
register([23], UnmapThroughput);
register([24], VluLatency);
register([25, 28], VluDirtyCase4, 'record');
register([26], VluRandReadPerThread, 'record');
register([27], WideRandRead);
register([29], WbFlushThroughput, 'record');

export { PARSER_REGISTRY };

/** GenPerf parserId 목록 (Chart Overlay, Delta Table 등에서 사용) */
export const GENPERF_IDS = [2, 3, 16];

/** parserId로 컴포넌트 조회. 없으면 undefined. */
export function getParserEntry(parserId: number): ParserEntry | undefined {
	// Ensure numeric lookup — JSON may deliver parserId as string
	return PARSER_REGISTRY.get(Number(parserId));
}
