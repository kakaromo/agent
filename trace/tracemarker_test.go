package trace

import (
	"strings"
	"testing"
)

// ⚠⚠ 라벨은 스텝 이름이라 **사용자 입력이 섞인다.** 그대로 셸 명령에 넣으므로
// 따옴표·백틱·`$`·개행이 남으면 명령이 깨지거나 임의 명령이 실행된다.
func TestSanitizeMarkerLabel(t *testing.T) {
	dangerous := []string{
		"a'; rm -rf /; echo '",
		"`whoami`",
		"$(id)",
		"back\\slash",
		"new\nline",
		"pipe|separator",
		"tab\there",
	}
	for _, in := range dangerous {
		got := sanitizeMarkerLabel(in)
		for _, bad := range []string{"'", "\"", "`", "\\", "$", "\n", "\r", "\t", "|"} {
			if strings.Contains(got, bad) {
				t.Errorf("sanitize(%q) = %q — 위험 문자 %q 가 남았다", in, got, bad)
			}
		}
	}
}

// `|` 는 marker 줄의 필드 구분자다. 라벨에 남으면 파싱 시 필드가 밀린다.
func TestSanitizeMarkerLabelKeepsReadable(t *testing.T) {
	if got := sanitizeMarkerLabel("launch_app: com.foo.bar"); got != "launch_app: com.foo.bar" {
		t.Errorf("멀쩡한 라벨이 훼손됐다: %q", got)
	}
	// 빈 값은 marker 줄이 `AGENT_BOUNDARY|B|` 로 끝나 파싱이 애매해지므로 대체한다.
	if got := sanitizeMarkerLabel(""); got == "" {
		t.Error("빈 라벨이 그대로 나왔다")
	}
	// 길이 상한 — 커널 버퍼에 들어가는 줄이다.
	long := strings.Repeat("x", 500)
	if got := sanitizeMarkerLabel(long); len(got) > 64 {
		t.Errorf("라벨 길이 상한이 안 걸렸다: %d", len(got))
	}
}

// ⚠ marker 쓰기가 실패했으면(rc != 0) uptime 이 읽혔더라도 **성공이 아니다.**
// marker 없이 시각만 쓰면 그건 offset 방식보다 부정확한 호스트 시각이라
// (adb 왕복이 다시 들어간다) 폴백의 의미가 사라진다.
func TestParseMarkerOutput(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want float64
		ok   bool
	}{
		{"정상", "3953689.67\nrc=0\n", 3953689.67, true},
		{"marker 실패", "3953689.67\nrc=1\n", 0, false},
		{"rc 없음", "3953689.67\n", 0, false},
		{"uptime 없음", "rc=0\n", 0, false},
		{"빈 출력", "", 0, false},
		{"쓰레기", "hello\nworld\n", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseMarkerOutput(c.out)
			if ok != c.ok || (ok && got != c.want) {
				t.Errorf("parseMarkerOutput(%q) = (%v, %v), want (%v, %v)",
					c.out, got, ok, c.want, c.ok)
			}
		})
	}
}

// ⚠ marker 는 전역 공유 자원(trace_marker)에 쓴다. 접두사가 없으면 atrace·앱이 찍은
// 줄을 우리 구간으로 오인한다.
func TestMarkerPrefixIsDistinct(t *testing.T) {
	if markerPrefix == "" || !strings.HasPrefix(markerPrefix, "AGENT") {
		t.Errorf("markerPrefix=%q — 남의 marker 와 구분되는 접두사여야 한다", markerPrefix)
	}
}
