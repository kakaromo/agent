package trace

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	pb "agent/pb"
)

// ftrace `trace_marker` 로 구간 경계를 **기기 축에 직접** 찍는 폴백.
//
// **왜 폴백인가.** 기본 경로는 호스트가 시각을 적고 ClockOffset 으로 기기 축에 옮기는
// 것이다(clockoffset.go). 그쪽이 기기에 아무것도 안 써서 더 깨끗하고, 실측 오차도
// ±11~31ms 라 화면상 sub-pixel 이다. 다만 **adb RTT 가 커지면 그 방식이 통째로
// 비활성화된다**(OffsetRTTThresholdSec). 그때 구간 분할을 포기하는 대신 쓸 수 있는
// 유일한 수단이 이것이다.
//
// **왜 이쪽이 RTT 에 안 흔들리나.** marker 는 커널이 자기 시계로 타임스탬프를 찍는다.
// 호스트는 "찍어라" 만 보내므로 **adb 왕복이 오차에 안 들어간다.** 반면 offset 방식은
// 왕복 중앙을 기기 시점으로 가정하는 것이라 정확도 상한이 RTT/2 다.
//
// **축.** marker ts 는 ftrace 버퍼의 다른 이벤트와 같은 타임스탬프다. tracer.go 가
// `trace_clock` 을 boot 로 고정하므로 parquet `time` 과 **같은 축**이고, 그래서
// StepBoundary.started_mono 에 그대로 넣을 수 있다 (변환 없음).
//
// 실측 (S25, non-root shell, 2026-09-01):
//   - marker ts 3953689.6547 vs 같은 호출의 /proc/uptime 3953689.67 → 15ms (셸 순차 실행분)
//   - marker 구간 안에서 dd 를 일으키니 UFS 이벤트 238건이 그 구간에 잡혔다
//
// ⚠ **`tracing_on=1` 일 때만 쓸 수 있다.** 버퍼가 할당돼 있지 않으면 write 가
// `EBADF`("Bad file descriptor")로 실패한다 — 권한 문제가 아니다. 그래서 trace 수집이
// 도는 중에만 유효하고, trace 없는 잡에서는 이 폴백을 쓸 수 없다.

// markerPrefix — 우리 marker 임을 구분하는 접두사.
//
// ⚠ trace_marker 는 **전역 공유 자원**이다. atrace·앱·다른 도구가 같은 파일에 쓰므로
// 접두사 없이 `tracing_mark_write` 를 다 긁으면 남의 것을 우리 구간으로 오인한다.
//
// 실측 (S25, 앱 전환 시나리오 1회, 2026-09-01): 로그 44,787줄 중
// `tracing_mark_write` 가 **1,168줄**인데 그중 우리 것은 **11줄**뿐이었다.
// 나머지는 삼성 키보드(honeyboard)의 atrace 마커이고, ⚠ **형식이 우리와 똑같다**:
//
//	남의 것: tracing_mark_write: B|28925|android.os.Handler: ...
//	우리 것: tracing_mark_write: AGENT_BOUNDARY|B|스크롤 down ×4
//
// `B|`/`E|` 를 파이프로 나누는 것까지 같아서, 접두사가 없었으면 키보드 마커
// 1,157개를 구간으로 오인했을 것이다.
const markerPrefix = "AGENT_BOUNDARY"

// 경계의 종류. benchmark 패키지가 trace 를 import 하면 순환이라(TraceController 가
// 인터페이스로 끊는 이유) 전용 타입 대신 문자열로 주고받는다.
const (
	MarkerBegin = "B"
	MarkerEnd   = "E"
)

// markerTimeout — marker 쓰기 1회의 상한.
//
// 이 폴백이 필요한 상황 자체가 "adb 가 느리다" 이므로 넉넉히 준다. 다만 무한정
// 기다리면 시나리오 스텝이 밀리므로(측정 대상을 흔든다) 상한은 둔다.
const markerTimeout = 5 * time.Second

// WriteBoundaryMarker — 기기 ftrace 버퍼에 구간 경계를 찍고 **그 시각을 돌려준다.**
//
// 반환값은 기기 축(boot clock) 초다. ok=false 면 marker 를 못 썼다는 뜻이고,
// 그때 호출자는 기존 offset 경로로 되돌아가거나 구간을 포기한다.
//
// ⚠ 반환 시각은 marker ts **자체가 아니라** 같은 셸 호출에서 읽은 `/proc/uptime` 이다.
// marker ts 를 직접 읽으려면 버퍼를 grep 해야 하는데, 그건 (a) 수집 중 버퍼를 읽어
// trace_pipe 와 경합하고 (b) 라인 수만큼 느려서 스텝을 밀리게 한다. 두 값의 차이는
// 셸 순차 실행분(실측 15ms)뿐이고 **adb 왕복은 안 들어간다** — 이 폴백의 이점은 그대로다.
// marker 줄 자체는 나중에 로그에서 사람이 대조할 근거로 남는다.
func (m *Manager) WriteBoundaryMarker(ctx context.Context, traceJobID string,
	kind, label string) (float64, bool) {

	job, err := m.GetJob(traceJobID)
	if err != nil {
		return 0, false
	}
	job.Mu.Lock()
	tracingDir := job.TracingDir
	deviceID := job.DeviceID
	state := job.State
	job.Mu.Unlock()

	// tracingDir 이 없으면 ftrace 경로가 아니다 (fsio 는 ftrace 를 안 쓴다).
	if tracingDir == "" || deviceID == "" {
		return 0, false
	}
	// ⚠ 수집 중이 아니면 tracing_on 이 꺼져 있어 write 가 EBADF 로 실패한다.
	// (권한 문제가 아니다 — 버퍼 미할당 시 커널이 그렇게 돌려준다.)
	if state != pb.JobState_JOB_STATE_RUNNING {
		return 0, false
	}

	md, derr := m.adbMgr.GetDevice(deviceID)
	if derr != nil {
		return 0, false
	}

	cctx, cancel := context.WithTimeout(ctx, markerTimeout)
	defer cancel()

	// ⚠ marker 쓰기와 uptime 읽기를 **한 번의 셸 호출**로 묶는다. 나누면 그 사이에
	// adb 왕복이 끼어 이 폴백의 존재 이유(RTT 비의존)가 사라진다.
	//
	// 실패해도 uptime 은 찍히게 `;` 로 잇지 않고, marker 실패를 알 수 있도록
	// 종료 코드를 함께 싣는다.
	// ⚠ 라벨을 format 문자열에 넣지 않는다. `%` 가 printf 지시자로 먹혀 **조용히
	// 깨진다** — 실기기 확인: `printf 'AGENT_BOUNDARY|B|50% off\n'` → `500ff` 이고
	// rc=0 이라 실패로도 안 잡힌다. 스텝 이름엔 `%` 가 충분히 들어온다("50% 할인").
	// 그래서 `%s` 인자로 넘긴다.
	cmd := fmt.Sprintf(
		`printf '%%s|%%s|%%s\n' %s %s %s > %s/trace_marker 2>/dev/null; rc=$?; `+
			`cut -d" " -f1 /proc/uptime; echo "rc=$rc"`,
		shellQuote(markerPrefix), shellQuote(kind), shellQuote(sanitizeMarkerLabel(label)), tracingDir)

	out, serr := md.Device.Shell(cctx, cmd)
	if serr != nil {
		return 0, false
	}
	return parseMarkerOutput(out)
}

// parseMarkerOutput — `<uptime>\nrc=<n>` 형태를 읽는다.
//
// rc != 0 이면 marker 를 못 쓴 것이다 — uptime 은 읽혔더라도 **성공으로 치지 않는다.**
// marker 없이 시각만 쓰면 그건 그냥 offset 방식보다 부정확한 호스트 시각이라
// (adb 왕복이 다시 들어간다) 폴백의 의미가 없다.
func parseMarkerOutput(out string) (float64, bool) {
	var uptime float64
	var haveUptime, rcOK bool

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "rc="); ok {
			rcOK = rest == "0"
			continue
		}
		if v, err := strconv.ParseFloat(line, 64); err == nil && v > 0 {
			uptime, haveUptime = v, true
		}
	}
	if !haveUptime || !rcOK {
		return 0, false
	}
	return uptime, true
}

// shellQuote — 셸 인자로 안전하게 감싼다.
//
// sanitizeMarkerLabel 이 따옴표류를 이미 걸러내지만, 인자 경로에서는 **공백이 있는
// 라벨이 여러 인자로 쪼개지는** 문제가 남는다("스크롤 down ×4" → 3개 인자).
// 작은따옴표로 감싸 한 인자로 만든다.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// sanitizeMarkerLabel — 라벨을 marker 한 줄에 안전하게 싣는다.
//
// ⚠ 이 값은 스텝 이름이라 사용자 입력이 섞인다. 그대로 셸에 넣으면 따옴표·개행으로
// 명령이 깨지거나 임의 명령이 실행된다. 파싱 쪽에서도 `|` 가 구분자라 라벨에 들어가면
// 필드가 밀린다.
func sanitizeMarkerLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '|' || r == '\'' || r == '"' || r == '`' || r == '\\' || r == '$':
			return '_'
		case r == '\n' || r == '\r' || r == '\t':
			return '_'
		case r < 0x20:
			return -1
		}
		return r
	}, s)
	// marker 한 줄은 커널 버퍼에 들어가므로 길이를 제한한다.
	//
	// ⚠ **바이트로 자르되 UTF-8 경계를 지킨다.** 이 프로젝트의 스텝 이름은 한국어라
	// ("스크롤 down ×4", "youtube 실행(warm)") 한 글자가 3바이트다. 단순히
	// `s[:maxLabel]` 로 자르면 문자 중간이 잘려 깨진 바이트가 커널 버퍼에 들어가고,
	// 나중에 로그를 읽을 때 그 줄이 통째로 이상해진다.
	const maxLabel = 64
	if len(s) > maxLabel {
		cut := maxLabel
		// 잘린 지점이 이어지는 바이트(10xxxxxx)면 문자 시작까지 되돌린다.
		for cut > 0 && s[cut]&0xC0 == 0x80 {
			cut--
		}
		s = s[:cut]
	}
	if s == "" {
		s = "step"
	}
	return s
}
