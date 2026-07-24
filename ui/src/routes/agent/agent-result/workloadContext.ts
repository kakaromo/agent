// ──────────────────────────────────────────────────────────────
// 워크로드 컨텍스트 — job 상세 "무엇이 돌았고 왜 이렇게 동작했나"
// ──────────────────────────────────────────────────────────────
//
// 두 축으로 구성:
//   1. whatRan   — executionConfig.steps 에서 "무엇이 돌았나" (앱/시나리오/벤치 스텝)
//   2. whyLikeThis — metrics 패턴에서 "왜 이렇게 보이나" (규칙 기반 자동 해석)
//
// LLM 호출 없이 결정적(규칙 테이블). 오프라인/출장 standalone 에서도 동작.
// 사용자가 job 마다 메모(workloadNote)를 남기면 자동 해석을 오버라이드/보강한다.
//
// 관련: AgentResultRenderer(step 인라인), WorkloadContextBanner(상단 요약).

import type { ToolType } from './types.js';

// ── 무엇이 돌았나 ─────────────────────────────────────────────

export interface StepSummary {
	index: number;
	kind: string;      // 'benchmark' | 'app_macro' | 'iotest' | 'trace_start' | ...
	title: string;     // "FIO randread bs=4k" / "삼성 노트 AI 요약(앱 매크로)" / ...
	detail?: string;   // 부가 설명 (선택)
}

export interface WhatRan {
	steps: StepSummary[];
	traceEnabled: boolean;   // trace_start/stop 이 시나리오에 포함됐는지
	traceTypes: string[];    // ['ufs', 'block'] 등
	oneLine: string;         // 한 줄 요약 ("앱 실행 → AI 요약 → trace(ufs) 측정")
}

/** executionConfig.steps 를 사람이 읽는 워크로드 서술로 변환. */
export function describeWorkload(
	steps: any[] | null | undefined,
	loops: any[] | null | undefined
): WhatRan {
	const list: StepSummary[] = [];
	const traceTypes = new Set<string>();
	let traceEnabled = false;

	for (let i = 0; i < (steps?.length ?? 0); i++) {
		const s = steps![i];
		if (!s || typeof s !== 'object') continue;
		const type = String(s.type ?? '').toLowerCase();

		if (type === 'trace_start' || type === 'trace_stop') {
			traceEnabled = true;
			const t = String(s.params?.trace_type ?? s.trace_type ?? '').toLowerCase();
			if (t) for (const part of t.split(/[,+]/)) if (part) traceTypes.add(part.trim());
			// trace_start/stop 자체는 워크로드가 아니라 "측정 래퍼"라 step 목록엔 넣지 않음
			continue;
		}

		list.push(summarizeStep(i, type, s));
	}

	// loop 가 있으면 반복 서술.
	// 주의: 여러 loop 의 count 를 단순 합산한 근사치다. 중첩/독립 loop 이 섞이면
	// 실제 반복 횟수와 다를 수 있으나, 한 줄 요약의 개략 표기라 근사로 충분하다.
	let loopSuffix = '';
	if (loops && loops.length > 0) {
		const total = loops.reduce((n: number, l: any) => n + (Number(l?.count) || 0), 0);
		if (total > 0) loopSuffix = ` · 반복 ${total}회(근사)`;
	}

	const oneLine = list.length > 0
		? list.map(s => s.title).join(' → ') +
		  (traceEnabled ? ` · trace(${[...traceTypes].join('/') || 'on'}) 측정` : '') +
		  loopSuffix
		: (traceEnabled ? `trace(${[...traceTypes].join('/') || 'on'}) 측정` : '측정 항목 정보 없음');

	return { steps: list, traceEnabled, traceTypes: [...traceTypes], oneLine };
}

function summarizeStep(index: number, type: string, s: any): StepSummary {
	if (type === 'benchmark') {
		const tool = String(s.tool ?? '').toUpperCase() || 'BENCHMARK';
		const hint = benchmarkHint(String(s.tool ?? '').toLowerCase(), s.params);
		return {
			index,
			kind: 'benchmark',
			title: hint ? `${tool} ${hint}` : tool,
			detail: '스토리지 I/O 벤치마크'
		};
	}
	if (type === 'app_macro') {
		const name = s.macroName ?? s.macro_name ?? '앱 매크로';
		return {
			index,
			kind: 'app_macro',
			title: `${name}`,
			detail: '실제 앱 조작 재현 (매크로)'
		};
	}
	if (type === 'iotest') {
		return { index, kind: 'iotest', title: 'I/O Test', detail: '커스텀 멀티스레드 I/O' };
	}
	if (type === 'install_apk' || type === 'uninstall_apk') {
		const pkg = s.params?.package_name ?? s.params?.apk_filename ?? '';
		return {
			index,
			kind: type,
			title: type === 'install_apk' ? `APK 설치${pkg ? ` (${pkg})` : ''}` : `APK 제거${pkg ? ` (${pkg})` : ''}`
		};
	}
	if (type === 'shell') {
		const cmd = String(s.params?.command ?? s.params?.cmd ?? '').slice(0, 40);
		return { index, kind: 'shell', title: cmd ? `shell: ${cmd}` : 'shell 명령' };
	}
	if (type === 'sleep' || type === 'wait') {
		const sec = s.params?.seconds ?? s.params?.duration ?? '';
		return { index, kind: type, title: sec ? `대기 ${sec}s` : '대기' };
	}
	if (type === 'cleanup') {
		return { index, kind: 'cleanup', title: '정리 (cleanup)' };
	}
	// 알 수 없는 type — 그대로 표시
	return { index, kind: type || 'step', title: type || `Step ${index}` };
}

function benchmarkHint(tool: string, paramsRaw: any): string {
	const p = (paramsRaw && typeof paramsRaw === 'object') ? paramsRaw : {};
	if (tool === 'fio') {
		const parts: string[] = [];
		const rw = p.rw ?? p.rwmode;
		if (rw) parts.push(String(rw));
		if (p.bs) parts.push(`bs=${p.bs}`);
		if (p.iodepth) parts.push(`qd=${p.iodepth}`);
		return parts.join(' ');
	}
	if (tool === 'tiotest') return String(p.test ?? p.skip ?? '');
	if (tool === 'iozone') return p.reclen ? `reclen=${p.reclen}` : '';
	return '';
}

// ── 왜 이렇게 동작했나 (규칙 기반 metric 해석) ──────────────────

export type InsightTone = 'info' | 'good' | 'warn';

export interface WorkloadInsight {
	tone: InsightTone;
	text: string;      // "READ 가 작음 → warm start, 모델이 이미 로드됨"
}

/**
 * I/O 볼륨(bytes) 요약. trace 로 측정된 워크로드에선 READ/WRITE/DISCARD 총량으로
 * "무슨 성격의 I/O 였나"를 판단한다. metrics 에 io_bytes 계열이 없으면 null.
 */
interface IoVolume {
	readBytes: number;
	writeBytes: number;
	discardBytes: number;
	totalBytes: number;
}

/**
 * metrics 에서 read/write/discard "총 바이트(볼륨)" 를 추출.
 *
 * 볼륨 키만 매칭한다 (io_bytes / total_bytes / readTotalBytes / *_mb).
 * bw_kb / bw_bytes / throughput_bps / iops 같은 rate(속도) 키는 제외 —
 * 이걸 볼륨으로 합산하면 해석이 오염된다 (리뷰 #3).
 */
function extractIoVolume(metrics: Record<string, number>): IoVolume | null {
	let read = 0, write = 0, discard = 0;
	let found = false;

	for (const [k, v] of Object.entries(metrics)) {
		const key = k.toLowerCase();
		if (!isVolumeKey(key)) continue;
		if (key.includes('discard')) { discard += normalizeToBytes(key, v); found = true; }
		else if (key.includes('read')) { read += normalizeToBytes(key, v); found = true; }
		else if (key.includes('write')) { write += normalizeToBytes(key, v); found = true; }
	}

	if (!found || (read === 0 && write === 0 && discard === 0)) return null;
	return { readBytes: read, writeBytes: write, discardBytes: discard, totalBytes: read + write + discard };
}

/** rate(속도) 키 표식 — 볼륨과 헷갈리면 안 되는 것들. */
function isRateKey(key: string): boolean {
	return key.includes('bw') || key.includes('bandwidth') || key.includes('throughput')
		|| key.includes('_bps') || key.includes('iops') || key.includes('_kb');
}

/** 총 바이트(볼륨) 키인가 — 명시적 접미사만 인정하고 rate 키는 배제. */
function isVolumeKey(key: string): boolean {
	if (isRateKey(key)) return false;
	// io_bytes / total_bytes / readTotalBytes / discard_total_bytes 등
	if (key.includes('totalbytes') || key.includes('total_bytes') || key.includes('io_bytes')) return true;
	// trace summary 가 MB 단위로 줄 경우 (_mb 접미사)
	if (key.endsWith('_mb') || key.includes('_mb_')) return true;
	return false;
}

function normalizeToBytes(key: string, v: number): number {
	// 이미 bytes 면 그대로, _mb 접미사면 *1MiB
	if (key.endsWith('_mb') || key.includes('_mb_')) return v * 1_048_576;
	return v;
}

/**
 * 규칙 기반 자동 해석. whatRan(무엇이 돌았나) + metrics 패턴을 보고
 * "왜 수치가 이런가"를 문장으로 생성한다. LLM 없음, 결정적.
 */
export function deriveInsights(
	metrics: Record<string, number>,
	whatRan: WhatRan
): WorkloadInsight[] {
	const out: WorkloadInsight[] = [];
	const vol = extractIoVolume(metrics);

	// ── I/O 볼륨 성격 해석 (trace 측정 워크로드에 특히 유용) ──
	if (vol && vol.totalBytes > 0) {
		const mb = (b: number) => b / 1_048_576;
		const readMb = mb(vol.readBytes);
		const writeMb = mb(vol.writeBytes);
		const discardMb = mb(vol.discardBytes);

		// READ 가 작음 → warm start / 데이터가 이미 캐시/로드됨
		if (vol.readBytes > 0 && vol.readBytes < 2 * 1_048_576 && (vol.writeBytes > vol.readBytes || vol.discardBytes > vol.readBytes)) {
			out.push({
				tone: 'info',
				text: `READ 가 작음 (${readMb.toFixed(2)}MB) → warm start 로 보임. 필요한 데이터(모델·리소스)가 이미 메모리/캐시에 로드된 상태.`
			});
		}

		// WRITE 가 READ 보다 지배적 → 결과 저장 / 캐시 write-back 워크로드
		if (vol.writeBytes > 0 && vol.writeBytes > vol.readBytes * 1.5) {
			out.push({
				tone: 'info',
				text: `WRITE(${writeMb.toFixed(2)}MB) 가 READ 보다 큼 → 결과·중간산출물 저장 또는 캐시 write-back 성격의 워크로드.`
			});
		}

		// DISCARD 가 큼 → 캐시/임시파일 정리 (TRIM)
		if (vol.discardBytes > 0 && vol.discardBytes > vol.readBytes + vol.writeBytes) {
			out.push({
				tone: 'info',
				text: `DISCARD(${discardMb.toFixed(2)}MB) 가 지배적 → 캐시·임시파일 정리(TRIM) 가 I/O 의 대부분. 실제 데이터 이동보다 정리 부하가 큼.`
			});
		} else if (vol.discardBytes > 0 && vol.discardBytes > 1_048_576) {
			out.push({
				tone: 'info',
				text: `DISCARD(${discardMb.toFixed(2)}MB) 관측 → 결과 확정 후 캐시/임시영역 정리 발생.`
			});
		}
	}

	// ── latency / QD 해석 ──
	const qdMax = pickMetric(metrics, ['qd_max', 'queue_depth_max', 'qd']);
	if (qdMax != null && qdMax >= 16) {
		out.push({
			tone: 'info',
			text: `Queue Depth 최대 ${qdMax.toFixed(0)} → I/O 가 상당히 병렬로 몰림. 순차 접근이 아니라 다중 요청이 겹친 구간이 있었음.`
		});
	}

	// dtoc / clat p99 지연 — 튀는 지연 경고.
	// 단위는 키 이름으로 판별: '_ns' 포함 키(fio clat 등)는 ns → ms 변환,
	// 그 외(trace dtoc_p99 는 이미 ms)는 그대로. (매그니튜드 추측 대신 소스 기반 — 리뷰 #4)
	const lat = pickMetricWithKey(metrics, ['dtoc_p99', 'read_clat_ns_p99.000000', 'write_clat_ns_p99.000000', 'clat_ns_p99.000000', 'lat_ns_p99']);
	if (lat != null) {
		const p99Ms = lat.key.includes('_ns') ? lat.value / 1_000_000 : lat.value;
		if (p99Ms > 5) {
			out.push({
				tone: 'warn',
				text: `지연 p99 ${p99Ms.toFixed(2)}ms — 일부 요청이 눈에 띄게 느림. 컨텐션/GC/스로틀링 구간이 있었을 수 있음.`
			});
		} else if (p99Ms > 0) {
			out.push({
				tone: 'good',
				text: `지연 p99 ${p99Ms.toFixed(2)}ms — tail latency 가 낮아 안정적으로 처리됨.`
			});
		}
	}

	// ── 워크로드 종류별 맥락 힌트 ──
	if (whatRan.steps.some(s => s.kind === 'app_macro')) {
		out.push({
			tone: 'info',
			text: `앱 매크로 워크로드 — 합성 벤치가 아니라 실제 앱 조작을 재현한 결과. 수치는 그 앱이 유발한 실사용 I/O 를 반영.`
		});
	}
	if (whatRan.steps.length > 1 && whatRan.steps.some(s => s.kind === 'benchmark')) {
		out.push({
			tone: 'info',
			text: `여러 스텝이 순차 실행됨 — step 탭에서 각 단계의 기여를 나눠 볼 수 있음.`
		});
	}

	if (out.length === 0) {
		out.push({
			tone: 'info',
			text: `자동 해석할 뚜렷한 I/O 패턴이 없습니다. 아래 메모에 이 job 의 맥락(cold/warm, 조건 등)을 직접 남겨두세요.`
		});
	}

	return out;
}

function pickMetric(metrics: Record<string, number>, keys: string[]): number | null {
	return pickMetricWithKey(metrics, keys)?.value ?? null;
}

/** pickMetric 과 동일하나 매칭된 실제 키도 반환 (단위 판별 등에 필요). */
function pickMetricWithKey(metrics: Record<string, number>, keys: string[]): { key: string; value: number } | null {
	for (const k of keys) {
		if (metrics[k] != null && isFinite(metrics[k])) return { key: k, value: metrics[k] };
	}
	// prefix(r1_step0_ 등) 붙은 키도 탐색
	for (const [mk, v] of Object.entries(metrics)) {
		for (const k of keys) {
			if ((mk.endsWith('_' + k) || mk === k) && isFinite(v)) {
				return { key: mk, value: v };
			}
		}
	}
	return null;
}

// ── step 인라인 해석 (특정 step 의 metrics 만) ──────────────────

/** 한 step 의 metrics 로 짧은 해석 1~2줄 생성. 없으면 빈 배열. */
export function deriveStepInsights(
	stepMetrics: Record<string, number>,
	tool: ToolType,
	stepTitle: string
): WorkloadInsight[] {
	const pseudoWhatRan: WhatRan = {
		steps: [{ index: 0, kind: tool, title: stepTitle }],
		traceEnabled: false,
		traceTypes: [],
		oneLine: stepTitle
	};
	// step 단위에선 "앱 매크로/멀티스텝" 류 일반 힌트는 빼고 metric 기반 해석만.
	return deriveInsights(stepMetrics, pseudoWhatRan).filter(
		i => !i.text.includes('여러 스텝') && !i.text.includes('step 탭')
	);
}
