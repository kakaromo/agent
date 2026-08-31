package parser

import "testing"

// ⚠ 안드로이드 스레드 이름에 대괄호가 흔하다 (highpool[392]). 첫 `[` 를 CPU 로
// 읽으면 392 를 CPU 로 착각해 줄을 버린다 — send/complete 균형이 깨져 QD 가
// 회수되지 않고 하드웨어 상한을 넘어 누적된다.
func TestParseFtraceHeaderBracketInComm(t *testing.T) {
	cases := []struct {
		line string
		cpu  uint32
		proc string
	}{
		{"   highpool[392]-7685    [002] d.h1. 3956435.102281: ufshcd_command: complete_rsp: x", 2, "highpool[392]-7685"},
		{"     highpool[3]-23193   [003] ..... 3956433.500003: ufshcd_command: send_req: x", 3, "highpool[3]-23193"},
		{"    kworker/5:1H-29566   [005] ..... 3956435.113658: ufshcd_command: send_req: x", 5, "kworker/5:1H-29566"},
		{"          <idle>-0       [003] d.h2. 3953468.344671: ufshcd_command: complete_rsp: x", 3, "<idle>-0"},
	}
	for _, c := range cases {
		h, ok := parseFtraceHeader(c.line)
		if !ok {
			t.Errorf("파싱 실패: %q", c.line)
			continue
		}
		if h.CPU != c.cpu {
			t.Errorf("CPU 오독: %q → %d (want %d)", c.proc, h.CPU, c.cpu)
		}
		if h.Process != c.proc {
			t.Errorf("process 오독: %q (want %q)", h.Process, c.proc)
		}
	}
}

// ⚠ comm 은 16자에서 잘린다. 여는 대괄호만 남는 경우가 실제로 있다
// (`IntentService[C-9374`). 닫는 짝이 없다고 포기하면 그 줄을 통째로 버린다.
func TestParseFtraceHeaderTruncatedComm(t *testing.T) {
	line := " IntentService[C-9374    [005] ..... 3956444.695241: ufshcd_command: send_req: x"
	h, ok := parseFtraceHeader(line)
	if !ok {
		t.Fatal("잘린 comm 줄을 버렸다 — send/complete 균형이 깨져 QD 가 누적된다")
	}
	if h.CPU != 5 {
		t.Errorf("CPU=%d (want 5)", h.CPU)
	}
}
