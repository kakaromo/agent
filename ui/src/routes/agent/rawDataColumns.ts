// trace_type 별 정적 컬럼 정의. row 클릭 → RawLogSheet 연결을 위해 line_number 포함.
import type { ColumnDef } from '@tanstack/table-core';

type AnyRow = Record<string, unknown>;

const numFmt = (digits: number) => (v: unknown) => {
	const n = typeof v === 'number' ? v : Number(v);
	return Number.isFinite(n) ? n.toFixed(digits) : '';
};

const intFmt = (v: unknown) => {
	const n = typeof v === 'number' ? v : Number(v);
	return Number.isFinite(n) ? String(n) : '';
};

const strFmt = (v: unknown) => (v == null ? '' : String(v));

// true 일 때만 표시한다. false 를 전부 찍으면 열이 노이즈가 되는데, 여기서 중요한
// 건 "예외적으로 참인 행" 을 눈에 띄게 하는 것이다.
const boolFmt = (v: unknown) => (v === true || v === 'true' ? 'Y' : '');

// 화면 표기가 hex 인 컬럼 집합. 필터가 "범위" 연산자를 먼저 보여줄지 판단하는 데 쓴다 —
// 사용자가 보는 값(0x2a)은 범위 입력에 넣을 수 있는 모양이 아니다.
const HEX_FMTS = new Set<(v: unknown) => string>();

const hexFmt = (digits: number) => {
	const f = (v: unknown) => {
		if (v == null) return '';
		const n = typeof v === 'number' ? v : Number(v);
		if (!Number.isFinite(n)) return '';
		return '0x' + n.toString(16).padStart(digits, '0');
	};
	HEX_FMTS.add(f);
	return f;
};

function col(key: string, header: string, fmt: (v: unknown) => string, w = 110): ColumnDef<AnyRow> {
	return {
		accessorKey: key,
		header,
		cell: ({ row }) => fmt((row.original as AnyRow)[key]),
		// 복사도 화면과 같은 표기로 (hex 는 0x28, time 은 자리수 고정).
		// 없으면 DataTable 이 raw 값을 복사해 opcode 가 40, time 이 2.785376214 로 붙는다.
		meta: { copyText: (row: AnyRow) => fmt(row[key]), hex: HEX_FMTS.has(fmt) },
		size: w
	};
}

// ftrace 계열(ufs/block/ufscustom) 컬럼.
//
// ⚠ 여기 키는 **raw API 가 실제로 내려주는 필드**여야 한다. portal 은 parquet 을
// 직접 읽어 30컬럼을 그리지만, agent 의 `/trace/raw` 는 TraceEvent 로 정규화해
// 아래 11개만 준다 (server/rest_convert.go traceEventToMap). 없는 키를 넣으면
// 컬럼만 생기고 값은 전부 빈칸이 된다 — 실제로 process/tag/groupid/hwqid/aligned/
// line_number 를 넣어 뒀다가 전부 빈 컬럼으로 나왔다.
//
// fsio 는 서버가 cross-layer 를 함께 실어 주므로 아래 fsio*Columns 가 더 넓다.
const ftraceCommonColumns: ColumnDef<AnyRow>[] = [
	col('time', 'time(s)', numFmt(6), 130),
	col('action', 'action', strFmt, 140),
	col('cmd', 'cmd', strFmt, 90),
	col('lba', 'lba', intFmt, 110),
	col('size', 'size', intFmt, 90),
	col('qd', 'qd', intFmt, 60),
	col('cpu', 'cpu', intFmt, 60),
	col('dtoc', 'DtoC(ms)', numFmt(3), 100),
	col('ctoc', 'CtoC(ms)', numFmt(3), 100),
	col('ctod', 'CtoD(ms)', numFmt(3), 100),
	col('continuous', 'cont', strFmt, 60)
];

export const ufsColumns: ColumnDef<AnyRow>[] = ftraceCommonColumns;
export const blockColumns: ColumnDef<AnyRow>[] = ftraceCommonColumns;
export const ufscustomColumns: ColumnDef<AnyRow>[] = ftraceCommonColumns;

export const fsioUfsColumns: ColumnDef<AnyRow>[] = [
	// 기본 UFS 17
	col('time', 'time(s)', numFmt(6), 130),
	col('process', 'process', strFmt, 160),
	col('cpu', 'cpu', intFmt, 60),
	col('action', 'action', strFmt, 130),
	col('tag', 'tag', intFmt, 60),
	col('opcode', 'opcode', hexFmt(2), 80),
	// LU 마다 LBA 주소공간이 독립 — lba 를 읽을 때 반드시 함께 봐야 한다.
	col('lun', 'lun', intFmt, 60),
	col('lba', 'lba', intFmt, 110),
	col('size', 'size', intFmt, 90),
	col('groupid', 'group', intFmt, 70),
	col('hwqid', 'hwq', intFmt, 60),
	col('qd', 'qd', intFmt, 60),
	col('dtoc', 'DtoC(ms)', numFmt(3), 100),
	col('ctoc', 'CtoC(ms)', numFmt(3), 100),
	col('ctod', 'CtoD(ms)', numFmt(3), 100),
	col('continuous', 'cont', strFmt, 60),
	col('aligned', 'align', strFmt, 60),
	col('line_number', 'line', intFmt, 80),
	// cross-layer 메타 7
	col('pid', 'pid', intFmt, 80),
	col('tid', 'tid', intFmt, 80),
	col('comm', 'comm', strFmt, 140),
	col('syscall', 'syscall', strFmt, 130),
	col('fs', 'fs', strFmt, 80),
	col('ino', 'ino', intFmt, 100),
	col('name', 'name', strFmt, 220),
	// io_flags — fsio_block 과 **동일한 표시 방식**으로 통일.
	// 예전엔 여기만 15개 불리언 컬럼으로 펼쳐서, 같은 데이터 모델을 두 fsio 타입이
	// 다르게 보여줬다. 게다가 백엔드가 39개를 다 보내는데 15개만 노출해 24개를 버렸다.
	// 이제 hex + bpftrace -x 와 동일한 풀이를 한 셀에 표시 → 39개 전부 반영되고
	// 가로 스크롤도 크게 줄어든다.
	{
		accessorKey: 'io_flags',
		header: 'io_flags',
		cell: ({ row }) => ioFlagsText(row.original as AnyRow),
		// 셀에 합성해 보여주는 값이라 raw 를 복사하면 `(WRITE, DATA)` 풀이가 통째로 빠진다.
		// 표시와 같은 함수를 써서 화면 = 클립보드 를 보장.
		meta: { copyText: (row: AnyRow) => ioFlagsText(row) },
		size: 360
	},
	// UPIU 헤더 5 (nullable)
	col('txn', 'txn', hexFmt(2), 70),
	col('upiu_flags', 'upiuFlg', hexFmt(2), 80),
	col('upiu_func', 'upiuFn', hexFmt(2), 80),
	col('upiu_attr', 'upiuAttr', strFmt, 90),
	col('upiu_cp', 'upiuCp', intFmt, 70),
	// ── mgmt (Query/TM UPIU, UIC) ──
	// cmd 열에 이미 mgmt_name 이 들어가지만, Query 가 **어느 IDN 을** 건드렸는지와
	// TM 이 성공했는지(resp/status)는 이름만으로 알 수 없다.
	// ⚠ query_idn 은 **query_opcode 에 따라 값 공간이 다르다** — descriptor 면
	// 0x07=Geometry, attribute 면 0x05=bBackgroundOpStatus. 둘을 같이 봐야 한다.
	col('mgmt_name', 'mgmt', strFmt, 200),
	col('query_opcode', 'qop', hexFmt(2), 70),
	col('query_idn', 'idn', hexFmt(2), 70),
	col('query_index', 'qIdx', intFmt, 60),
	col('query_selector', 'qSel', intFmt, 60),
	col('uic_cmd', 'uicCmd', hexFmt(2), 80),
	col('upiu_resp', 'resp', hexFmt(2), 70),
	col('upiu_status', 'status', hexFmt(2), 70),
	// 미완결 IO — 이 행의 DtoC 0 은 "0ms" 가 아니라 "모름" 이다. 표에서 0ms 로
	// 읽히면 "엄청 빠른 IO" 로 오해하므로 플래그를 눈에 보이게 둔다.
	col('is_unfinished', 'unfin', boolFmt, 70)
];

// bpftrace `-x` (--decode) 출력의 비트 이름 풀이와 동일.
// Rust src/parsers/bpftrace_tsv.rs::decode_flags 가 풀어둔 39개 is_* 필드를
// CSV 헤더(src/output/fsio_csv.rs) 표시 순서대로 재조합 → "(WRITE, O_SYNC, DATA)".
/**
 * io_flags 비트 → 표시 이름. bpftrace `-x`(--decode) 출력과 동일한 이름·순서.
 *
 * ⚠ 비트값은 `trace/parser/fsio_line.go` 의 f* 상수, `trace/fsio_agg.go` 의
 * flowClassExpr 와 **3중 중복**이다. 셋을 항상 같이 고칠 것.
 *
 * BigInt 인 이유 — f2fs 힌트 비트가 2^53 을 넘어 number 로는 정확히 표현되지 않는다.
 */
const FSIO_FLAG_BITS: ReadonlyArray<readonly [bigint, string]> = [
	[0x1n, 'READ'],
	[0x2n, 'WRITE'],
	[0x4n, 'DISCARD'],
	[0x8n, 'FLUSH'],
	[0x10n, 'TRIM'],
	[0x100n, 'O_SYNC'],
	[0x200n, 'O_DIRECT'],
	[0x400n, 'O_APPEND'],
	[0x800n, 'O_DSYNC'],
	[0x1000n, 'SYNC_PATH'],
	[0x2000n, 'REQ_SYNC'],
	[0x4000n, 'REQ_PRIO'],
	[0x8000n, 'REQ_RAHEAD'],
	[0x10000n, 'DATA'],
	[0x20000n, 'METADATA'],
	[0x40000n, 'INODE'],
	[0x80000n, 'BITMAP'],
	[0x100000n, 'DIRENT'],
	[0x200000n, 'XATTR'],
	[0x400000n, 'JOURNAL'],
	[0x800000n, 'CHECKPOINT'],
	[0x1000000n, 'GC'],
	[0x2000000n, 'EXTENT_ALLOC'],
	[0x4000000n, 'EXTENT_FREE'],
	[0x8000000n, 'BMAP'],
	[0x100000000n, 'BUFFERED'],
	[0x200000000n, 'DIRECT_IO'],
	[0x400000000n, 'MMAP_WRITEBACK'],
	[0x800000000n, 'WRITEBACK_KWORKER'],
	[0x1000000000n, 'FSYNC_TRIGGERED'],
	[0x10000000000n, 'SAW_VFS'],
	[0x1000000000000n, 'F2FS_NODE_WRITE'],
	[0x2000000000000n, 'F2FS_DATA_WRITE'],
	[0x4000000000000n, 'F2FS_META_WRITE'],
	[0x8000000000000n, 'F2FS_NODE_GC'],
	[0x10000000000000n, 'F2FS_DATA_GC'],
	[0x20000000000000n, 'F2FS_HOT_DATA'],
	[0x40000000000000n, 'F2FS_WARM_DATA'],
	[0x80000000000000n, 'F2FS_COLD_DATA'],
];

/**
 * io_flags 원본을 BigInt 로 읽는다.
 *
 * 서버가 **문자열**로 보낸다 — u64 를 JSON number 로 실으면 2^53 넘는 f2fs 비트가
 * 조용히 반올림돼 사라진다. number/undefined 로 와도 깨지지 않게 방어한다.
 */
function ioFlagsOf(row: AnyRow): bigint {
	const v = row.io_flags;
	try {
		if (typeof v === 'bigint') return v;
		if (typeof v === 'string') return v === '' ? 0n : BigInt(v);
		if (typeof v === 'number') return BigInt(Math.trunc(v));
	} catch {
		return 0n;
	}
	return 0n;
}

function decodeFsioIoFlags(row: AnyRow): string {
	const f = ioFlagsOf(row);
	if (f === 0n) return '';
	const names: string[] = [];
	for (const [bit, name] of FSIO_FLAG_BITS) {
		if ((f & bit) !== 0n) names.push(name);
	}
	return names.length ? `(${names.join(', ')})` : '';
}

/**
 * io_flags 셀의 표시 텍스트 — `0x0000000000000242 (WRITE, O_SYNC, DATA)`.
 * 표시(cell)와 복사(meta.copyText)가 **같은 함수**를 쓰도록 분리해 둔다.
 */
function ioFlagsText(row: AnyRow): string {
	const hex = '0x' + ioFlagsOf(row).toString(16).padStart(16, '0');
	const decoded = decodeFsioIoFlags(row);
	return decoded ? `${hex} ${decoded}` : hex;
}

// bpftrace fsio_block — block-layer 한 줄 trace.
// Rust src/output/fsio_block_parquet.rs schema 미러 (io_type 제외, 비트 풀이는 합성 컬럼).
// io_type: Rust 정책상 항상 빈 값 (6dcbaf1) — rwbs 와 io_flags 비트 풀이로 대체.
// io_flags: 0x... hex + bpftrace -x 와 동일한 [WRITE|O_SYNC|DATA] 풀이를 한 셀에 표시.
//           is_* 39 컬럼은 그 안에 합쳐졌으므로 별도 컬럼으로 노출하지 않는다.
export const fsioBlockColumns: ColumnDef<AnyRow>[] = [
	// 기본 Block (io_type 제외)
	col('time', 'time(s)', numFmt(6), 130),
	col('process', 'process', strFmt, 160),
	col('cpu', 'cpu', intFmt, 60),
	col('flags', 'flags', strFmt, 90),
	col('action', 'action', strFmt, 150),
	col('devmajor', 'major', intFmt, 70),
	col('devminor', 'minor', intFmt, 70),
	col('extra', 'extra', intFmt, 70),
	col('sector', 'sector', intFmt, 110),
	col('size', 'size', intFmt, 90),
	col('comm', 'comm', strFmt, 140),
	col('qd', 'qd', intFmt, 60),
	col('dtoc', 'DtoC(ms)', numFmt(3), 100),
	col('ctoc', 'CtoC(ms)', numFmt(3), 100),
	col('ctod', 'CtoD(ms)', numFmt(3), 100),
	col('continuous', 'cont', strFmt, 60),
	col('aligned', 'align', strFmt, 60),
	col('line_number', 'line', intFmt, 80),
	// cross-layer 메타 6 (comm 은 기본 Block 19 에 이미 포함)
	col('pid', 'pid', intFmt, 80),
	col('tid', 'tid', intFmt, 80),
	col('syscall', 'syscall', strFmt, 130),
	col('fs', 'fs', strFmt, 80),
	col('ino', 'ino', intFmt, 100),
	col('name', 'name', strFmt, 220),
	// rwbs + io_flags (hex + bpftrace -x 디코드 풀이 합성)
	col('rwbs', 'rwbs', strFmt, 80),
	{
		accessorKey: 'io_flags',
		header: 'io_flags',
		cell: ({ row }) => ioFlagsText(row.original as AnyRow),
		meta: { copyText: (row: AnyRow) => ioFlagsText(row) },
		size: 360
	},
	// 미완결 IO — block 은 (dev, sector, rwbs) 로 짝짓는데 sector 는 재사용 신호가
	// 없어 시간 만료로 닫는다. UFS 와 마찬가지로 이 행의 DtoC 0 은 "모름" 이다.
	col('is_unfinished', 'unfin', boolFmt, 70)
];

export const fsioReadColumns: ColumnDef<AnyRow>[] = [
	// 시각 — time 은 read **종료** 시각이다(진입 아님).
	col('time', 'time(s)', numFmt(6), 130),
	col('duration_ns', 'duration_ns', intFmt, 110),
	col('line_number', 'line', intFmt, 80),
	// 주체
	col('pid', 'pid', intFmt, 70),
	col('tid', 'tid', intFmt, 70),
	col('cpu', 'cpu', intFmt, 60),
	col('comm', 'comm', strFmt, 140),
	col('syscall', 'syscall', strFmt, 100),
	col('action', 'action', strFmt, 120),
	col('read_id', 'rid', intFmt, 80),
	// 대상
	col('fs', 'fs', strFmt, 70),
	col('devmajor', 'major', intFmt, 70),
	col('devminor', 'minor', intFmt, 70),
	col('ino', 'ino', intFmt, 100),
	col('name', 'name', strFmt, 260),
	// 결과 — raw_result 는 부호 있음(음수 = -errno)
	col('offset', 'offset', intFmt, 110),
	col('requested_bytes', 'req(b)', intFmt, 100),
	col('returned_bytes', 'ret(b)', intFmt, 100),
	col('raw_result', 'ret', intFmt, 90),
	// 증거 (원시값 — 판정의 정본)
	{
		accessorKey: 'io_flags',
		header: 'io_flags',
		cell: ({ row }) => ioFlagsText(row.original as AnyRow),
		meta: { copyText: (row: AnyRow) => ioFlagsText(row) },
		size: 360
	},
	col('is_buffered', 'buffered', strFmt, 80),
	col('is_direct', 'direct', strFmt, 70),
	col('fill_units', 'fill', intFmt, 60),
	col('sync_ra_units', 'sync_ra', intFmt, 80),
	col('async_ra_units', 'async_ra', intFmt, 85),
	col('fill_pages', 'fill_pg', intFmt, 75),
	// ⚠ evidence_fs 는 증거를 낸 fs 일 뿐 coverage 조회 키가 아니다 —
	//   진짜 hit 은 fill 이 0 이라 항상 'none' 이다.
	col('evidence_fs', 'evfs', strFmt, 70),
	// 판정 (trace 가 계산)
	col('fs_read_seen', 'fs_read', strFmt, 80),
	col('coverage', 'coverage', strFmt, 90),
	col('cache_class', 'cache_class', strFmt, 170),
	col('quality', 'quality', strFmt, 80),
	col('quality_reason', 'quality_reason', strFmt, 170),
	// BPF 의 1차 판정 — 참고용 보존(대조 디버깅). 정본은 cache_class.
	col('bpf_cls', 'bpf_cls', strFmt, 90)
];

export function columnsFor(traceType: string): ColumnDef<AnyRow>[] {
	switch (traceType) {
		case 'ufs':
			return ufsColumns;
		case 'block':
			return blockColumns;
		case 'ufscustom':
			return ufscustomColumns;
		case 'fsio_ufs':
			return fsioUfsColumns;
		case 'fsio_block':
			return fsioBlockColumns;
		case 'fsio_read':
			return fsioReadColumns;
		default:
			// ⚠ 모르는 타입에 ufsColumns 를 주면 **에러 없이 전 칸이 빈 표**가 된다
			//   (accessorKey 가 하나도 안 맞아 모든 셀이 ''). 새 trace_type 을 추가하고
			//   여기 case 를 빠뜨리면 화면만 조용히 비는데, 원인을 찾기가 매우 어렵다.
			//   빈 배열을 주면 컬럼 없는 표로 렌더돼 "정의가 없다" 가 드러난다.
			//   portal `frontend/src/routes/trace/rawDataColumns.ts` 와 같은 판단.
			console.warn(`[rawDataColumns] 알 수 없는 trace_type: ${traceType} — columnsFor 에 case 추가 필요`);
			return [];
	}
}

// ── Raw Data 컬럼 필터 (클라이언트) ──────────────────────────────────────
//
// portal 은 같은 UI 를 쓰되 필터를 **서버로 보낸다**(parquet 을 페이징하므로 로드된
// 행만 걸러선 안 된다). standalone 은 `/trace/raw` 가 이벤트를 한 번에 다 주므로
// 여기서 거른다 — 왕복이 없어 즉시 반영되고 뒤쪽 페이지를 놓칠 위험도 없다.

/**
 * 한 행·한 컬럼의 **화면 표시값**. 필터는 이 값으로 판정한다.
 *
 * ⚠ raw 값으로 비교하면 안 된다 — 화면엔 `0x2a` 인데 raw 는 `42` 라서,
 * 보이는 대로 `0x2a` 를 입력하면 아무것도 안 걸린다.
 */
export function displayValue(traceType: string, column: string, row: AnyRow): string {
	const c = columnsFor(traceType).find(
		(d) => (d as { accessorKey?: string }).accessorKey === column
	);
	const copy = (c?.meta as { copyText?: (r: AnyRow) => string } | undefined)?.copyText;
	if (copy) return copy(row);
	const v = row[column];
	return v == null ? '' : String(v);
}

/** 화면 표기가 hex 인 컬럼인가 — '범위' 연산자를 기본으로 보여줄지 판단용. */
export function isHexDisplay(traceType: string, column: string): boolean {
	const c = columnsFor(traceType).find(
		(d) => (d as { accessorKey?: string }).accessorKey === column
	);
	return !!(c?.meta as { hex?: boolean } | undefined)?.hex;
}

export type ColumnFilterOp = 'IN' | 'NOT_IN' | 'CONTAINS' | 'RANGE';
export type ColumnFilter = { column: string; op: ColumnFilterOp; values: string[] };

/**
 * 한 행이 필터 전부를 통과하는가 (AND 결합).
 *
 * RANGE 만 raw 숫자로 비교한다 — 크기 비교는 표시 문자열로는 뜻이 없기 때문이다
 * ("10" < "9"). 나머지는 표시값 기준이라 눈에 보이는 대로 걸린다.
 */
export function rowMatchesFilters(
	traceType: string,
	row: AnyRow,
	filters: ColumnFilter[]
): boolean {
	for (const f of filters) {
		if (f.op === 'RANGE') {
			const n = Number(row[f.column]);
			if (!Number.isFinite(n)) return false;
			const lo = f.values[0] === '' ? null : Number(f.values[0]);
			const hi = f.values[1] === '' ? null : Number(f.values[1]);
			if (lo != null && Number.isFinite(lo) && n < lo) return false;
			if (hi != null && Number.isFinite(hi) && n > hi) return false;
			continue;
		}
		const shown = displayValue(traceType, f.column, row);
		if (f.op === 'CONTAINS') {
			const needle = (f.values[0] ?? '').toLowerCase();
			if (needle && !shown.toLowerCase().includes(needle)) return false;
			continue;
		}
		// IN / NOT_IN — 표시값 완전 일치. 대소문자는 무시한다(0X2A 로 쳐도 걸리게).
		const set = f.values.map((v) => v.toLowerCase());
		if (set.length === 0) continue; // 값이 없는 필터는 없는 셈
		const hit = set.includes(shown.toLowerCase());
		if (f.op === 'IN' ? !hit : hit) return false;
	}
	return true;
}
