package server

import (
	"context"
	"encoding/json"
	"testing"

	pb "agent/pb"
)

// TestScenarioRequestFromScheduleConfig — ScheduledJob.Config(JSON) → RunScenarioRequest 변환 검증.
// stepsJson/loopsJson 은 배열이 문자열로 이스케이프된 필드라, 이를 풀어 protojson 경로로 태우는 게 핵심.
// db=nil(office 경로)로 macro hydrate 없이 순수 변환만 확인한다.
func TestScenarioRequestFromScheduleConfig(t *testing.T) {
	// 카탈로그(youtube-homefeed) 를 축약한 steps: trace_start → app_macro → trace_stop
	steps := []map[string]any{
		{"type": "trace_start", "params": map[string]any{"trace_type": "ufs"}},
		{"type": "app_macro", "macro": map[string]any{
			"packageName": "com.google.android.youtube",
			"clearMode":   "none",
			"events": []map[string]any{
				{"type": "scroll_capture", "direction": "down", "maxScrolls": 8},
				{"type": "key", "keycode": 86},
			},
		}},
		{"type": "trace_stop", "params": map[string]any{}},
	}
	loops := []map[string]any{
		{"startStep": 0, "endStep": 2, "count": 5},
	}
	stepsJSON, _ := json.Marshal(steps)
	loopsJSON, _ := json.Marshal(loops)

	// ScheduledJob.Config 형태(JSON 객체 문자열, stepsJson/loopsJson 은 이스케이프된 문자열)
	config, _ := json.Marshal(map[string]any{
		"stepsJson":   string(stepsJSON),
		"loopsJson":   string(loopsJSON),
		"repeatCount": 2,
	})

	req, err := ScenarioRequestFromScheduleConfig(
		context.Background(), nil, string(config),
		[]string{"emulator-5554"}, "야간 유튜브", "wait",
	)
	if err != nil {
		t.Fatalf("변환 실패: %v", err)
	}

	if got := len(req.GetSteps()); got != 3 {
		t.Fatalf("steps 개수: want 3, got %d", got)
	}
	if req.GetSteps()[0].GetType() != "trace_start" {
		t.Errorf("step0 type: want trace_start, got %q", req.GetSteps()[0].GetType())
	}
	if tt := req.GetSteps()[0].GetParams()["trace_type"]; tt != "ufs" {
		t.Errorf("step0 trace_type: want ufs, got %q", tt)
	}

	// app_macro step + events 보존
	macro := req.GetSteps()[1].GetMacro()
	if macro == nil {
		t.Fatal("step1 macro 가 nil")
	}
	if macro.GetPackageName() != "com.google.android.youtube" {
		t.Errorf("packageName: got %q", macro.GetPackageName())
	}
	if got := len(macro.GetEvents()); got != 2 {
		t.Fatalf("events 개수: want 2, got %d", got)
	}
	if kc := macro.GetEvents()[1].GetKeycode(); kc != 86 {
		t.Errorf("event1 keycode: want 86, got %d", kc)
	}

	// loops (endStep inclusive) 보존
	if got := len(req.GetLoops()); got != 1 {
		t.Fatalf("loops 개수: want 1, got %d", got)
	}
	lp := req.GetLoops()[0]
	if lp.GetStartStep() != 0 || lp.GetEndStep() != 2 || lp.GetCount() != 5 {
		t.Errorf("loop: want (0,2,5), got (%d,%d,%d)", lp.GetStartStep(), lp.GetEndStep(), lp.GetCount())
	}

	// repeat / deviceIds / scenarioName / busyPolicy
	if req.GetRepeat() != 2 {
		t.Errorf("repeat: want 2, got %d", req.GetRepeat())
	}
	if got := req.GetDeviceIds(); len(got) != 1 || got[0] != "emulator-5554" {
		t.Errorf("deviceIds: got %v", got)
	}
	if req.GetScenarioName() != "야간 유튜브" {
		t.Errorf("scenarioName: got %q", req.GetScenarioName())
	}
	if req.GetBusyPolicy() != "wait" {
		t.Errorf("busyPolicy: want wait, got %q", req.GetBusyPolicy())
	}
}

// TestScenarioRequestFromScheduleConfig_ToolNormalize — 짧은 tool 이름("FIO")이 proto enum 으로 보정되는지.
func TestScenarioRequestFromScheduleConfig_ToolNormalize(t *testing.T) {
	steps := []map[string]any{
		{"type": "benchmark", "tool": "FIO", "params": map[string]any{"rw": "read"}},
	}
	stepsJSON, _ := json.Marshal(steps)
	config, _ := json.Marshal(map[string]any{"stepsJson": string(stepsJSON)})

	req, err := ScenarioRequestFromScheduleConfig(context.Background(), nil, string(config), nil, "", "")
	if err != nil {
		t.Fatalf("변환 실패: %v", err)
	}
	if got := req.GetSteps()[0].GetTool(); got != pb.BenchmarkTool_BENCHMARK_TOOL_FIO {
		t.Errorf("tool 정규화 실패: want FIO, got %v", got)
	}
	// busyPolicy 미지정 시 기본 reject
	if req.GetBusyPolicy() != "reject" {
		t.Errorf("기본 busyPolicy: want reject, got %q", req.GetBusyPolicy())
	}
}

// TestScenarioRequestFromScheduleConfig_BadJSON — 깨진 stepsJson 은 에러.
func TestScenarioRequestFromScheduleConfig_BadJSON(t *testing.T) {
	config, _ := json.Marshal(map[string]any{"stepsJson": "{not valid json"})
	if _, err := ScenarioRequestFromScheduleConfig(context.Background(), nil, string(config), nil, "", ""); err == nil {
		t.Fatal("깨진 stepsJson 인데 에러가 안 남")
	}
}
