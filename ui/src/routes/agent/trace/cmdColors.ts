// cmd 계열별 색상 매핑 — AgentTraceResultSheet 의 동일 구현 이식.
// Read(파랑) / Write(주황·빨강) / Flush(초록) / Discard(보라) / Other(회색)

const CMD_COLOR_MAP: Record<string, string[]> = {
	read: ['#3b82f6', '#2563eb', '#1d4ed8', '#60a5fa', '#93c5fd'],
	write: ['#f97316', '#ea580c', '#dc2626', '#fb923c', '#fdba74'],
	flush: ['#22c55e', '#16a34a', '#15803d', '#4ade80', '#86efac'],
	discard: ['#a855f7', '#9333ea', '#7c3aed', '#c084fc', '#d8b4fe'],
	other: ['#64748b', '#475569', '#6b7280', '#94a3b8', '#78716c'],
	// ── UFS management ──
	// 데이터 IO 가 아니라 링크 점유·장치 제어다. 전용 차트(DtoC (mgmt))를 따로 두면서
	// "같은 mgmt" 로 뭉뚱그리지 않고 **성질별로** 색을 가른다. 차트만 봐도
	// "링크가 자주 잠들었다" vs "WB 를 계속 건드린다" 가 구분되도록.
	//
	// mgmt_link  링크 전원/상태 (UIC: hibern8 enter/exit, link startup) — 보라
	// mgmt_read  장치 상태 조회 (Read Attribute/Descriptor/Flag)        — 청록
	// mgmt_write 장치 설정 변경 (Set/Clear/Toggle Flag, Write ...)      — 주황
	// mgmt_tm    Task Management (Abort Task, LU Reset 등)              — 빨강(이상 신호)
	mgmt_link: ['#8b5cf6', '#7c3aed', '#6d28d9', '#a78bfa', '#c4b5fd'],
	mgmt_read: ['#06b6d4', '#0891b2', '#0e7490', '#22d3ee', '#67e8f9'],
	mgmt_write: ['#f59e0b', '#d97706', '#b45309', '#fbbf24', '#fcd34d'],
	mgmt_tm: ['#ef4444', '#dc2626', '#b91c1c', '#f87171', '#fca5a5'],
	// 종류를 특정 못 한 mgmt (producer 가 정보를 안 준 폴백) — 회청색
	mgmt: ['#64748b', '#94a3b8', '#475569', '#cbd5e1', '#78716c']
};

/**
 * mgmt 이벤트의 **성질** 판정. 색 그룹 키를 그대로 돌려준다.
 *
 * 판정 순서가 중요하다:
 *   - TM 을 먼저 — "Query Task" 는 TM 인데 뒤의 Read/Write 규칙에 걸리지 않아야 한다.
 *   - 그 다음 UIC(DME_) — 이름에 다른 키워드가 없어 안전.
 *   - Query 는 opcode 이름이 앞에 오므로(Read Attribute / Set Flag) 그걸로 읽기·쓰기 구분.
 * 서버가 굽는 이름 형태는 trace: models/ufs_names.rs 참고.
 */
function getMgmtGroup(cmd: string): string {
	const s = cmd.trim();
	// Task Management — 이상 신호라 가장 먼저, 그리고 눈에 띄는 색으로.
	if (/^(Abort Task|Clear Task Set|Logical Unit Reset|Query Task|Target Reset)/.test(s)) {
		return 'mgmt_tm';
	}
	// UIC (링크 계층) — DME_HIBER_ENTER/EXIT, DME_LINK_STARTUP ...
	if (s.startsWith('DME_') || /^uic(_|$)/.test(s)) return 'mgmt_link';
	// Query — 장치 설정을 **바꾸는** 쪽
	if (/^(Write Descriptor|Write Attribute|Set Flag|Clear Flag|Toggle Flag)\b/.test(s)) {
		return 'mgmt_write';
	}
	// Query — 장치 상태를 **읽는** 쪽
	if (/^(Read Descriptor|Read Attribute|Read Flag)\b/.test(s)) return 'mgmt_read';
	// 폴백(upiu_send / nop_out / rtt / exception / Unknown(0x..))
	return 'mgmt';
}

/**
 * UFS management 이벤트 이름인가?
 *
 * 아래 getCmdGroup 의 문자열 스니핑보다 **먼저** 판정해야 한다. 그냥 두면
 * "DME_HIBER_EXIT" 는 첫 글자 'd' 로 discard(보라), "Read Flag(...)" 는
 * 'read' 포함으로 read(파랑) 가 되어 **데이터 IO 와 구분이 안 된다.**
 * 깨지는 게 아니라 그럴듯하게 틀려서 더 위험하다.
 *
 * 판정 기준은 서버가 굽는 mgmt_name 의 형태다 (trace: models/ufs_names.rs).
 * 데이터 IO 의 cmd 는 항상 '0x28' 같은 hex 라 겹칠 일이 없다.
 */
export function isMgmtCmd(cmd: string): boolean {
	const s = cmd.trim();
	if (!s || s.startsWith('0x')) return false; // 데이터 IO 는 hex opcode
	if (s.startsWith('DME_')) return true; // UIC
	// Query — opcode 이름 + 괄호 안 IDN
	if (/^(Read|Write|Set|Clear|Toggle)\s+(Descriptor|Attribute|Flag)\b/.test(s)) return true;
	// Task Management
	if (/^(Abort Task|Clear Task Set|Logical Unit Reset|Query Task|Target Reset)/.test(s)) return true;
	// 소비자가 못 푼 값은 "Unknown(0x..)" 로 온다. 데이터 IO 색을 뺏지 않도록 mgmt 로.
	if (s.startsWith('Unknown(')) return true;
	// producer 가 qop/idn/uic_cmd 를 안 준 경우 mgmt_name 이 action 으로 폴백된다
	// (uic / upiu_send / upiu_response / nop_out / rtt / exception). 이것도 mgmt 다.
	if (/^(uic|upiu_|nop_|rtt|exception)/.test(s)) return true;
	return false;
}

/**
 * 차트에서 감출 mgmt 이벤트.
 *
 * upiu_response 는 send 와 1:1 로 붙는 응답이라 차트에 그리면 같은 이벤트가 두 번
 * 보인다. 통계/Raw Data 에는 그대로 남기고 **차트 시리즈에서만** 뺀다.
 */
export function isHiddenInChart(cmd: string): boolean {
	return /^upiu_response\b/.test(cmd.trim());
}

// SCSI opcode → group (UFS). Block layer 는 문자열 매칭으로 분류.
const SCSI_CMD_GROUPS: Record<string, string> = {
	'0x28': 'read',
	'0xa8': 'read',
	'0x88': 'read',
	'0x08': 'read',
	'0x2a': 'write',
	'0xaa': 'write',
	'0x8a': 'write',
	'0x0a': 'write',
	'0x2e': 'write',
	'0x35': 'flush',
	'0x91': 'flush',
	'0x42': 'discard',
	'0x12': 'other',
	'0x1a': 'other',
	'0x5a': 'other',
	'0x25': 'other',
	'0x00': 'other'
};

export function getCmdGroup(cmd: string): string {
	const lower = cmd.toLowerCase().trim();
	if (!lower) return 'other';

	// mgmt 는 아래 문자열 스니핑보다 **먼저** 걸러야 한다 (isMgmtCmd 주석 참고).
	// "Read Attribute(...)" 가 read(파랑)로, "DME_..." 가 discard(보라)로 새는 걸 막는다.
	if (isMgmtCmd(cmd)) return getMgmtGroup(cmd);

	// SCSI hex opcode 우선 매칭
	if (lower.startsWith('0x')) return SCSI_CMD_GROUPS[lower] ?? 'other';

	// 전체 단어 매칭 (긴 키워드 우선 — discard/trim/unmap 가 read 의 'rd' 부분일치보다 앞에)
	if (lower.includes('discard') || lower.includes('trim') || lower.includes('unmap')) return 'discard';
	if (lower.includes('flush') || lower.includes('sync')) return 'flush';
	if (lower.includes('write')) return 'write';
	if (lower.includes('read')) return 'read';

	// Block trace io_type prefix (R/W/D/F + RA, WS, WSF, FUA 등 변형 포함).
	// Rust 파서가 첫 글자로 분류하므로 동일 규칙 적용 — 사용자가 prefix 기준 색 구분 요청.
	const first = lower[0];
	if (first === 'r') return 'read';
	if (first === 'w') return 'write';
	if (first === 'd') return 'discard';
	if (first === 'f') return 'flush';

	return 'other';
}

/**
 * 상태 공유용 카운터 — 같은 세션에서 cmd 가 처음 나온 순서대로 group 내 index 부여.
 * instance 별 분리가 필요하면 createCmdColorAssigner 사용.
 */
export function createCmdColorAssigner() {
	const assigned: Record<string, string> = {};
	// CMD_COLOR_MAP 의 키에서 자동 생성한다. 예전엔 리터럴로 나열해서 그룹을
	// 추가하면 counters[g] 가 undefined → NaN % len → palette[NaN] → 색이
	// 아예 안 나왔다. 팔레트만 늘려도 되도록 여기서 파생시킨다.
	const counters: Record<string, number> = Object.fromEntries(
		Object.keys(CMD_COLOR_MAP).map((k) => [k, 0])
	);
	return (cmd: string): string => {
		if (!assigned[cmd]) {
			const g = getCmdGroup(cmd);
			const palette = CMD_COLOR_MAP[g] ?? CMD_COLOR_MAP.other;
			const idx = (counters[g] ?? 0) % palette.length;
			counters[g] = (counters[g] ?? 0) + 1;
			assigned[cmd] = palette[idx];
		}
		return assigned[cmd];
	};
}
