package benchmark

import (
	"strings"
	"testing"

	pb "agent/pb"
)

// TestValidateScenarioSteps — 실행 전 드라이런 검증.
//
// 핵심은 두 방향 모두다: 잘못된 시나리오를 잡는 것과, 정상 시나리오를
// 막지 않는 것. 후자가 깨지면 사용자가 멀쩡한 시나리오를 못 돌린다.
func TestValidateScenarioSteps(t *testing.T) {
	tests := []struct {
		name  string
		steps []*pb.ScenarioStep
		want  string // 위반 사유에 포함돼야 할 문자열. "" 면 통과해야 함
	}{
		{
			name: "정상 — 유튜브 스크롤 + trace",
			steps: []*pb.ScenarioStep{
				{Type: "launch_app", Params: map[string]string{"package_name": "com.google.android.youtube", "clear_mode": "force_stop", "wait_seconds": "3"}},
				{Type: "trace_start", Params: map[string]string{"trace_type": "ufs"}},
				{Type: "scroll", Params: map[string]string{"direction": "down", "count": "20"}},
				{Type: "trace_stop", Params: map[string]string{"trace_type": "ufs"}},
			},
			want: "",
		},
		{
			name: "정상 — benchmark 는 tool enum 으로 지정",
			steps: []*pb.ScenarioStep{
				{Type: "benchmark", Tool: pb.BenchmarkTool_BENCHMARK_TOOL_FIO, Params: map[string]string{"rw": "randread"}},
			},
			want: "",
		},
		{
			name: "정상 — stop_app package 없으면 실행부가 skip (막지 않음)",
			steps: []*pb.ScenarioStep{
				{Type: "stop_app", Params: map[string]string{}},
			},
			want: "",
		},
		{
			name: "정상 — 빈 param 은 기본값이 적용된다",
			steps: []*pb.ScenarioStep{
				{Type: "trace_start", Params: map[string]string{}},
				{Type: "trace_stop", Params: map[string]string{}},
			},
			want: "",
		},

		{
			name: "거부 — launch_app package_name 누락",
			steps: []*pb.ScenarioStep{
				{Type: "launch_app", Params: map[string]string{"clear_mode": "force_stop"}},
			},
			want: "package_name",
		},
		{
			name: "거부 — clear_mode 오타 (실기기에서만 드러나던 버그)",
			steps: []*pb.ScenarioStep{
				{Type: "launch_app", Params: map[string]string{"package_name": "com.android.chrome", "clear_mode": "forcestop"}},
			},
			want: "clear_mode",
		},
		{
			name: "거부 — benchmark tool 미지정",
			steps: []*pb.ScenarioStep{
				{Type: "benchmark", Params: map[string]string{"rw": "randread"}},
			},
			want: "tool",
		},
		{
			name: "거부 — tap_element 식별자 없음",
			steps: []*pb.ScenarioStep{
				{Type: "tap_element", Params: map[string]string{}},
			},
			want: "식별자",
		},
		{
			name: "거부 — app_macro 매크로 설정 없음",
			steps: []*pb.ScenarioStep{
				{Type: "app_macro", Params: map[string]string{}},
			},
			want: "매크로",
		},
		{
			name: "거부 — 알 수 없는 type",
			steps: []*pb.ScenarioStep{
				{Type: "teleport", Params: map[string]string{}},
			},
			want: "알 수 없는",
		},
		{
			name:  "거부 — type 이 빈 문자열",
			steps: []*pb.ScenarioStep{{Type: "", Params: map[string]string{}}},
			want:  "type",
		},
		{
			name: "위반 메시지에 step 번호가 들어간다",
			steps: []*pb.ScenarioStep{
				{Type: "sleep", Params: map[string]string{"seconds": "1"}},
				{Type: "launch_app", Params: map[string]string{}},
			},
			want: "step 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateScenarioSteps(tt.steps)
			joined := strings.Join(got, "\n")

			if tt.want == "" {
				if len(got) > 0 {
					t.Errorf("정상 시나리오가 거부됨:\n%s", joined)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("거부돼야 하는데 통과됨 (기대 사유: %q)", tt.want)
			}
			if !strings.Contains(joined, tt.want) {
				t.Errorf("위반 사유에 %q 가 없습니다:\n%s", tt.want, joined)
			}
		})
	}
}

// TestValidateScenarioStepsNilSafe — nil step 이 섞여도 패닉하지 않는다.
func TestValidateScenarioStepsNilSafe(t *testing.T) {
	steps := []*pb.ScenarioStep{
		nil,
		{Type: "sleep", Params: map[string]string{"seconds": "1"}},
		nil,
	}
	if got := validateScenarioSteps(steps); len(got) != 0 {
		t.Errorf("nil step 때문에 위반이 생겼습니다: %v", got)
	}
}
