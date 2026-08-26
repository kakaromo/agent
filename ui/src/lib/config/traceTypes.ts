/**
 * trace_type 단일 목록.
 *
 * ⚠ **여기 한 곳에서만 정의한다.** 예전엔 같은 목록이 Trace 폼·스텝 편집
 * 다이얼로그·캔버스 step-contract 에 각각 하드코딩돼 있었고, fsio_* 를 추가할 때
 * 스텝 편집 다이얼로그만 빠뜨려서 **시나리오에서는 fsio 를 고를 수 없는** 상태가
 * 한동안 남아 있었다 (백엔드는 지원하는데 UI 에만 없는 형태라 알아채기 어렵다).
 *
 * 서버 측 진실 소스는 `trace.IsFsioTraceType` / `scenario/steptypes.go` 다.
 */
export interface TraceTypeOption {
	value: string;
	label: string;
	desc: string;
}

export const TRACE_TYPES: TraceTypeOption[] = [
	{ value: 'ufs', label: 'UFS', desc: 'UFS 레이어 I/O' },
	{ value: 'block', label: 'Block', desc: 'Block 레이어 I/O' },
	{ value: 'both', label: 'Both', desc: 'UFS + Block' },
	{ value: 'fsio_ufs', label: 'fsio UFS', desc: 'eBPF · UFS + 파일 귀속 (root 필요)' },
	{ value: 'fsio_block', label: 'fsio Block', desc: 'eBPF · Block + 파일 귀속 (root 필요)' }
];

/** fsio(eBPF) 계열인가 — cross-layer 귀속 UI 노출 판단용. */
export function isFsioTraceType(t: string | undefined): boolean {
	return t === 'fsio_ufs' || t === 'fsio_block';
}
