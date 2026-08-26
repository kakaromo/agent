package trace

import "testing"

// TestIsFsioReadyLine — attach 완료 신호를 제대로 알아보는가.
//
// 이걸 놓치면 시나리오에서 trace_start 다음 스텝이 곧바로 실행돼 **아직 훅이 안 붙은
// 구간의 IO 를 통째로 놓친다.** (Trace 탭은 사람이 버튼 누르는 시간이 우연히 이 대기를
// 대신해 줘서 증상이 안 보였다.)
func TestIsFsioReadyLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{
			"stdout 모드 — poll 루프 직전 마지막 출력",
			"warn: stdout 은 줄 단위로 flush 합니다 — 고부하에서 이벤트 유실(diag[9])이",
			true,
		},
		{
			"-o 모드",
			"writing events to /data/local/tmp/tr.events (full buffered 1024KB, clock=boot)",
			true,
		},
		// 아래는 attach 전에 나오는 줄들 — 여기서 true 를 주면 너무 일찍 진행한다.
		{"attach 실패 경고는 준비 신호가 아니다", "warn: failed to attach foo (errno=2).", false},
		{"ringbuf 크기 설정 실패", "ringbuf 크기 설정 실패(512 MB): Cannot allocate memory", false},
		{"load 실패", "load skel failed: Operation not permitted (errno=1)", false},
		{"빈 줄", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isFsioReadyLine(tt.line); got != tt.want {
				t.Errorf("isFsioReadyLine(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}
