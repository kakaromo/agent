package ai

import (
	"strings"
	"testing"
)

// 토큰을 잘게 쪼개 넣어도 최종 텍스트가 온전해야 한다.
// (스트리밍은 단어 중간에서 잘리므로 이게 깨지면 글자가 사라지거나 중복된다.)
func TestTermStreamerPreservesText(t *testing.T) {
	full := "이들 요청의 지연이 깁니다. 큐 깊이는 낮습니다. 마지막 문장"
	// 한 글자씩(최악의 경우) 넣는다.
	var out strings.Builder
	ts := NewTermStreamer(func(s string) { out.WriteString(s) })
	for _, r := range full {
		ts.Write(string(r))
	}
	ts.Flush()

	got := out.String()
	// 핵심 불변식: 한 글자씩 흘려 넣어도 **한 번에 처리한 것과 같아야** 한다.
	want := NormalizeTerms(full)
	if got != want {
		t.Errorf("조각내어 처리한 결과가 일괄 처리와 다르다:\n got: %q\nwant: %q", got, want)
	}
	// 용어가 실제로 바뀌었는지
	if strings.Contains(got, "요청") || strings.Contains(got, "큐 깊이") {
		t.Errorf("번역어가 남았다: %q", got)
	}
	if !strings.Contains(got, "request") || !strings.Contains(got, "QD") {
		t.Errorf("영어 용어가 없다: %q", got)
	}
}

// Flush 를 안 하면 마지막 문장이 유실된다 — 반드시 호출해야 함을 문서화하는 테스트.
func TestTermStreamerFlushRequired(t *testing.T) {
	var out strings.Builder
	ts := NewTermStreamer(func(s string) { out.WriteString(s) })
	ts.Write("종결부호 없는 마지막")
	if out.Len() != 0 {
		t.Error("종결부호 전에 나가면 안 된다")
	}
	ts.Flush()
	if out.Len() == 0 {
		t.Error("Flush 후에는 나와야 한다")
	}
}

// 종결부호 없이 길어지면 무한 버퍼링하지 않고 흘려보낸다.
func TestTermStreamerLongRunNoStall(t *testing.T) {
	var out strings.Builder
	ts := NewTermStreamer(func(s string) { out.WriteString(s) })
	long := strings.Repeat("가", 500) // 종결부호 없음
	ts.Write(long)
	if out.Len() == 0 {
		t.Error("상한을 넘으면 버퍼를 비워야 한다 (무한 대기 방지)")
	}
	ts.Flush()
	if out.String() != long {
		t.Errorf("텍스트가 손상됐다: %d → %d 글자", len([]rune(long)), len([]rune(out.String())))
	}
}

// 빈 토큰은 무시.
func TestTermStreamerEmptyToken(t *testing.T) {
	var n int
	ts := NewTermStreamer(func(string) { n++ })
	ts.Write("")
	ts.Flush()
	if n != 0 {
		t.Errorf("빈 입력에 emit 이 %d회 발생", n)
	}
}
