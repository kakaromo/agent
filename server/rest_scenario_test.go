package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pb "agent/pb"
	"agent/storage/sqlitedb"
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


// ⚠⚠ 탐색 → 프로파일 → 측정 루프의 **핸드오프**를 지킨다.
//
// UI 는 탐색에서 찾은 태그를 프로파일의 tags 에 넣어 저장하는데, 수집 쪽은 잡
// 파라미터 logcat_tags 만 본다. 이 다리가 없으면 사용자는 measure 를 설정했다고
// 믿지만 실제로는 explore(전체 버퍼)로 수집된다 — 전체 수집은 그 자체가 IO/CPU 를
// 써서 수백 ms 단위 TTFT 를 흔들므로 **측정값이 조용히 오염된다.**
func TestHydrateLogcatProfile(t *testing.T) {
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	prof, err := db.CreateAILogProfile(context.Background(), &sqlitedb.AILogProfile{
		Name: "QNN", Runtime: "qnn",
		PatternsJSON: `{"tags":["Genie","QnnHtp"],"series":[{"key":"ttft","regex":"TTFT ([0-9.]+)"}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("프로파일의 태그가 logcat_tags 로 풀린다", func(t *testing.T) {
		req := &pb.RunScenarioRequest{Params: map[string]string{
			"logcat": "on", "logcat_profile_id": itoa(prof.ID),
		}}
		if err := hydrateLogcatProfile(context.Background(), db, req); err != nil {
			t.Fatalf("hydrate 실패: %v", err)
		}
		if got := req.GetParams()["logcat_tags"]; got != "Genie,QnnHtp" {
			t.Errorf("태그가 안 풀렸다: %q — measure 가 explore 로 떨어져 측정이 오염된다", got)
		}
	})

	t.Run("직접 지정한 logcat_tags 가 이긴다", func(t *testing.T) {
		req := &pb.RunScenarioRequest{Params: map[string]string{
			"logcat": "on", "logcat_profile_id": itoa(prof.ID), "logcat_tags": "MyTag",
		}}
		if err := hydrateLogcatProfile(context.Background(), db, req); err != nil {
			t.Fatal(err)
		}
		if got := req.GetParams()["logcat_tags"]; got != "MyTag" {
			t.Errorf("사용자가 직접 쓴 태그를 덮어썼다: %q", got)
		}
	})

	t.Run("없는 프로파일은 에러 — 조용히 explore 로 떨어지면 안 된다", func(t *testing.T) {
		req := &pb.RunScenarioRequest{Params: map[string]string{
			"logcat": "on", "logcat_profile_id": "99999",
		}}
		if err := hydrateLogcatProfile(context.Background(), db, req); err == nil {
			t.Error("없는 프로파일인데 에러가 없다")
		}
	})

	t.Run("태그 없는 프로파일도 에러", func(t *testing.T) {
		empty, err := db.CreateAILogProfile(context.Background(), &sqlitedb.AILogProfile{
			Name: "no-tags", Runtime: "x",
			PatternsJSON: `{"series":[{"key":"ttft","regex":"TTFT ([0-9.]+)"}]}`,
		})
		if err != nil {
			t.Fatal(err)
		}
		req := &pb.RunScenarioRequest{Params: map[string]string{
			"logcat": "on", "logcat_profile_id": itoa(empty.ID),
		}}
		if err := hydrateLogcatProfile(context.Background(), db, req); err == nil {
			t.Error("태그 없는 프로파일인데 에러가 없다 — explore 로 떨어져 측정이 오염된다")
		}
	})

	t.Run("profile_id 가 없으면 아무 일도 없다", func(t *testing.T) {
		req := &pb.RunScenarioRequest{Params: map[string]string{"logcat": "on"}}
		if err := hydrateLogcatProfile(context.Background(), db, req); err != nil {
			t.Errorf("profile_id 없는데 에러가 났다: %v", err)
		}
	})
}

// ⚠ 함수가 있어도 **부르지 않으면** 소용없다. 단위 테스트는 함수를 직접 부르므로
// 호출부 누락을 못 잡는다 (DAG 배선 누락과 같은 함정).
func TestScenarioRequestCallsLogcatHydrate(t *testing.T) {
	src, err := os.ReadFile("rest_scenario.go")
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(src), "func scenarioRequestFromRawBody(")
	if i < 0 {
		t.Fatal("scenarioRequestFromRawBody 를 찾지 못했다 — 테스트가 낡았다")
	}
	body := string(src)[i:]
	if j := strings.Index(body, "\nfunc "); j > 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "hydrateLogcatProfile(") {
		t.Error("변환 경로가 hydrateLogcatProfile 을 부르지 않는다 — 수동/스케줄 " +
			"양쪽 모두 프로파일 태그가 안 풀려 explore 로 떨어진다")
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
