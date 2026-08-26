package benchmark

import (
	"testing"

	pb "agent/pb"
)

// TestDescribeStep — 레인 라벨이 "무슨 행동이었나" 로 읽히는가.
//
// 타입만 쓰면 shell/app_macro/shell 처럼 나와서, 구간별 IO 를 보는 화면인데
// 그 구간이 무슨 행동인지 알 수 없다.
func TestDescribeStep(t *testing.T) {
	tests := []struct {
		name string
		step *pb.ScenarioStep
		want string
	}{
		{"label 이 최우선", &pb.ScenarioStep{
			Type: "shell", Params: map[string]string{"label": "내가 붙인 이름", "cmd": "dd if=..."},
		}, "내가 붙인 이름"},

		{"콜드 실행", &pb.ScenarioStep{
			Type: "launch_app", Params: map[string]string{"package_name": "com.google.android.youtube"},
		}, "youtube 콜드 실행"},
		{"warm 실행", &pb.ScenarioStep{
			Type: "launch_app", Params: map[string]string{"package_name": "com.kakao.talk", "clear_mode": "none"},
		}, "talk 실행(warm)"},

		{"탭 — text 우선", &pb.ScenarioStep{
			Type: "tap_element", Params: map[string]string{"element_text": "전송", "element_resource_id": "btn_send"},
		}, "탭: 전송"},
		{"탭 — text 없으면 resource_id", &pb.ScenarioStep{
			Type: "tap_element", Params: map[string]string{"element_resource_id": "btn_send"},
		}, "탭: btn_send"},

		{"스크롤 반복", &pb.ScenarioStep{
			Type: "scroll", Params: map[string]string{"direction": "down", "count": "5"},
		}, "스크롤 down ×5"},
		{"스크롤 1회는 횟수 생략", &pb.ScenarioStep{
			Type: "scroll", Params: map[string]string{"direction": "up", "count": "1"},
		}, "스크롤 up"},

		{"shell 은 명령 앞부분", &pb.ScenarioStep{
			Type: "shell", Params: map[string]string{"cmd": "dd if=/dev/zero of=/data/local/tmp/x bs=1M count=64"},
		}, "dd if=/dev/zero of=/data/loc…"},

		{"대기", &pb.ScenarioStep{
			Type: "sleep", Params: map[string]string{"seconds": "5"},
		}, "대기 5s"},

		{"매크로 이름", &pb.ScenarioStep{
			Type: "app_macro", Macro: &pb.AppMacroConfig{MacroName: "홈피드 스크롤"},
		}, "홈피드 스크롤"},
		{"매크로 이름 없으면 패키지", &pb.ScenarioStep{
			Type: "app_macro", Macro: &pb.AppMacroConfig{PackageName: "com.instagram.android"},
		}, "android 매크로"},

		{"모르는 타입은 타입 그대로", &pb.ScenarioStep{Type: "future_step"}, "future_step"},
		{"nil 은 빈 문자열", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := describeStep(tt.step); got != tt.want {
				t.Errorf("describeStep() = %q, want %q", got, tt.want)
			}
		})
	}
}
