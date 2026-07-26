package server

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"agent/storage/sqlitedb"
)

// youtube-homefeed 예제와 동일 구조의 steps (docs/examples/youtube-homefeed.scenario.json)
const testStepsJSON = `[
  {"type":"trace_start","params":{"trace_type":"ufs"}},
  {"type":"app_macro","macro":{
    "packageName":"com.google.android.youtube","clearMode":"none",
    "sourceWidth":1080,"sourceHeight":2340,
    "events":[{"type":"scroll_capture","direction":"down","maxScrolls":8},{"type":"key","keycode":86}]
  }},
  {"type":"trace_stop","params":{}}
]`

func testTemplate() *sqlitedb.ScenarioTemplate {
	return &sqlitedb.ScenarioTemplate{
		ID:          7,
		Name:        "유튜브 홈피드",
		Description: "READ 지배",
		RepeatCount: 1,
		StepsJSON:   testStepsJSON,
		LoopsJSON:   sql.NullString{}, // null
	}
}

// TestBuildScenarioExport_RequirementsAutoCollect — export 시 requirements 가 steps 에서 자동 수집되는지.
func TestBuildScenarioExport_RequirementsAutoCollect(t *testing.T) {
	exp, err := buildScenarioExport(testTemplate())
	if err != nil {
		t.Fatalf("export 실패: %v", err)
	}
	if exp.SchemaVersion != scenarioExportSchemaVersion {
		t.Errorf("schemaVersion: want %d, got %d", scenarioExportSchemaVersion, exp.SchemaVersion)
	}
	if exp.Kind != "scenario" {
		t.Errorf("kind: got %q", exp.Kind)
	}
	if len(exp.Steps) != 3 {
		t.Fatalf("steps: want 3, got %d", len(exp.Steps))
	}

	req := exp.Requirements
	if req == nil {
		t.Fatal("requirements 가 nil (자동 수집 실패)")
	}
	if len(req.Packages) != 1 || req.Packages[0] != "com.google.android.youtube" {
		t.Errorf("packages: got %v", req.Packages)
	}
	if req.SourceWidth != 1080 || req.SourceHeight != 2340 {
		t.Errorf("해상도: got %dx%d", req.SourceWidth, req.SourceHeight)
	}
	if req.TraceType != "ufs" {
		t.Errorf("traceType: want ufs, got %q", req.TraceType)
	}

	// origin.contentHash 존재
	if h, ok := exp.Origin["contentHash"].(string); !ok || !strings.HasPrefix(h, "sha256:") {
		t.Errorf("contentHash: got %v", exp.Origin["contentHash"])
	}
}

// TestScenarioExportImport_RoundTrip — export → JSON → parse 가 내용을 보존하는지.
func TestScenarioExportImport_RoundTrip(t *testing.T) {
	exp, err := buildScenarioExport(testTemplate())
	if err != nil {
		t.Fatalf("export 실패: %v", err)
	}
	data, err := json.Marshal(exp)
	if err != nil {
		t.Fatalf("marshal 실패: %v", err)
	}

	parsed, err := parseScenarioImports(data)
	if err != nil {
		t.Fatalf("parse 실패: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("parse 결과: want 1, got %d", len(parsed))
	}
	got := parsed[0]
	if got.Name != "유튜브 홈피드" {
		t.Errorf("name: got %q", got.Name)
	}
	if len(got.Steps) != 3 {
		t.Errorf("steps: want 3, got %d", len(got.Steps))
	}

	// steps 재marshal 이 원본과 논리적으로 동일한지 (contentHash 로 비교)
	stepsJSON, _ := json.Marshal(got.Steps)
	h1 := scenarioContentHash(string(stepsJSON), "")
	// 원본 steps 를 normalize 해서 해시
	var origSteps []any
	_ = json.Unmarshal([]byte(testStepsJSON), &origSteps)
	origJSON, _ := json.Marshal(origSteps)
	h2 := scenarioContentHash(string(origJSON), "")
	if h1 != h2 {
		t.Errorf("round-trip content hash 불일치:\n  %s\n  %s", h1, h2)
	}
}

// TestParseScenarioImports_Array — 배열(scenario-pack) 파싱.
func TestParseScenarioImports_Array(t *testing.T) {
	arr := `[{"schemaVersion":1,"name":"a","steps":[{"type":"sleep"}]},{"schemaVersion":1,"name":"b","steps":[{"type":"sleep"}]}]`
	parsed, err := parseScenarioImports([]byte(arr))
	if err != nil {
		t.Fatalf("배열 parse 실패: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("want 2, got %d", len(parsed))
	}
	if parsed[0].Name != "a" || parsed[1].Name != "b" {
		t.Errorf("names: %q, %q", parsed[0].Name, parsed[1].Name)
	}
}

// TestParseScenarioImports_Empty — 빈 body 는 에러.
func TestParseScenarioImports_Empty(t *testing.T) {
	if _, err := parseScenarioImports([]byte("   ")); err == nil {
		t.Fatal("빈 body 인데 에러 안 남")
	}
}

// TestCollectScenarioRequirements_NoneReturnsNil — 요구사항 없는 steps 는 nil.
func TestCollectScenarioRequirements_NoneReturnsNil(t *testing.T) {
	var steps []any
	_ = json.Unmarshal([]byte(`[{"type":"sleep","params":{"seconds":"1"}}]`), &steps)
	if req := collectScenarioRequirements(steps); req != nil {
		t.Errorf("요구사항 없는데 nil 아님: %+v", req)
	}
}

// TestSanitizeFilename — 파일명 부적합 문자 치환.
func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"유튜브 홈피드":     "유튜브_홈피드",
		"a/b:c*d":     "a_b_c_d",
		"":            "scenario",
		"normal-name": "normal-name",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q): want %q, got %q", in, want, got)
		}
	}
}

// TestScenarioContentHash_Deterministic — 같은 입력 → 같은 해시, loops 반영.
func TestScenarioContentHash_Deterministic(t *testing.T) {
	h1 := scenarioContentHash(testStepsJSON, "")
	h2 := scenarioContentHash(testStepsJSON, "")
	if h1 != h2 {
		t.Error("동일 입력인데 해시 다름")
	}
	h3 := scenarioContentHash(testStepsJSON, `[{"startStep":0,"endStep":1,"count":3}]`)
	if h1 == h3 {
		t.Error("loops 가 다른데 해시 같음")
	}
}
