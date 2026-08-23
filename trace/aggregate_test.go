package trace

import (
	"strings"
	"testing"
)

// 집계 도구 레지스트리 드리프트 가드.
//
// AggSpecs 가 프롬프트(AggToolReference)와 실행 dispatch(RunAggregation) 양쪽을
// 파생시키므로, 셋이 어긋나면 LLM 이 존재하지 않는 도구를 고르거나 고른 도구가 실행되지
// 않는다. scenario/steptypes_test.go 와 같은 역할이다.

func TestAggSpecsUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range AggSpecs {
		if s.Name == "" {
			t.Fatal("이름 없는 AggSpec")
		}
		if seen[s.Name] {
			t.Fatalf("중복 도구 이름: %s", s.Name)
		}
		seen[s.Name] = true
		if s.Desc == "" {
			t.Errorf("%s: Desc 가 비어 있으면 LLM 이 도구를 고를 수 없다", s.Name)
		}
	}
}

// none 은 반드시 있어야 한다 — 없으면 답할 수 없는 질문에도 억지로 집계를 고른다.
func TestAggNonePresent(t *testing.T) {
	spec, ok := AggSpecByName(AggNone)
	if !ok {
		t.Fatal("none 도구가 없다. 답할 수 없는 질문에 억지 집계를 고르게 된다")
	}
	if spec.NeedsDB {
		t.Error("none 은 parquet 접근이 없어야 한다")
	}
}

// overview 도 parquet 직접 접근 없이 기존 summary 를 재사용한다.
func TestAggOverviewNoDB(t *testing.T) {
	spec, ok := AggSpecByName(AggOverview)
	if !ok {
		t.Fatal("overview 도구가 없다")
	}
	if spec.NeedsDB {
		t.Error("overview 는 buildTraceSummary 재사용이라 NeedsDB=false 여야 한다")
	}
}

// 프롬프트 텍스트에 모든 도구 이름이 실제로 등장하는지 — 누락되면 LLM 이 그 도구를 모른다.
func TestAggToolReferenceCoversAll(t *testing.T) {
	ref := AggToolReference()
	for _, name := range AggNames() {
		if !strings.Contains(ref, name) {
			t.Errorf("프롬프트 레퍼런스에 %s 가 없다", name)
		}
	}
}

// NeedsDB=true 인 도구는 전부 RunAggregation 의 switch 에 있어야 한다.
// (없으면 default 로 떨어져 "실행 대상이 아닙니다" 에러가 난다.)
func TestAggDispatchCoversDBTools(t *testing.T) {
	// parquet 없는 infos 로 호출하면 dispatch 이전에 hasParquet 에서 끊긴다.
	// 그 에러 메시지로 "도구는 인식됐다" 를 확인한다 — 알 수 없는 도구는 다른 메시지가 나온다.
	for _, s := range AggSpecs {
		if !s.NeedsDB {
			continue
		}
		_, err := RunAggregation(nil, s.Name, nil)
		if err == nil {
			t.Errorf("%s: parquet 없이 성공하면 안 된다", s.Name)
			continue
		}
		if strings.Contains(err.Error(), "알 수 없는 집계 도구") {
			t.Errorf("%s: 레지스트리에 있으나 조회되지 않는다", s.Name)
		}
	}
}

func TestRunAggregationUnknownTool(t *testing.T) {
	_, err := RunAggregation(nil, "made_up_tool", nil)
	if err == nil || !strings.Contains(err.Error(), "알 수 없는 집계 도구") {
		t.Fatalf("알 수 없는 도구는 명확히 거절해야 한다: %v", err)
	}
}

// LLM 이 파라미터를 문자열로 낼 수 있어 방어 변환이 필요하다.
func TestParamCoercion(t *testing.T) {
	p := map[string]any{"n": "15", "start_time": 180.5, "bins": float64(8), "cmd": " 0x2A "}

	if got := paramInt(p, "n", 10); got != 15 {
		t.Errorf("문자열 숫자 변환 실패: %d", got)
	}
	if got := paramInt(p, "bins", 12); got != 8 {
		t.Errorf("float64 → int 변환 실패: %d", got)
	}
	if got := paramInt(p, "missing", 7); got != 7 {
		t.Errorf("기본값 미적용: %d", got)
	}
	if v, ok := paramFloat(p, "start_time"); !ok || v != 180.5 {
		t.Errorf("float 파라미터 실패: %v %v", v, ok)
	}
}

// 상한을 넘는 n 은 잘려야 한다 — payload 폭증과 모델의 "구조 나열" 전환을 막는다.
func TestClampInt(t *testing.T) {
	if got := clampInt(999, 1, MaxAggRows); got != MaxAggRows {
		t.Errorf("상한 clamp 실패: %d", got)
	}
	if got := clampInt(0, 1, MaxAggRows); got != 1 {
		t.Errorf("하한 clamp 실패: %d", got)
	}
}

// histogram 컬럼은 SQL 에 들어가므로 화이트리스트 밖 값이 통과하면 안 된다.
func TestParamLatencyColWhitelist(t *testing.T) {
	cases := map[string]string{
		"dtoc":               "dtoc",
		"ctod":               "ctod",
		"CTOC":               "ctoc",
		"":                   "dtoc",
		"dtoc; DROP TABLE x": "dtoc",
		"1=1 OR true":        "dtoc",
	}
	for in, want := range cases {
		if got := paramLatencyCol(map[string]any{"column": in}); got != want {
			t.Errorf("paramLatencyCol(%q) = %q, want %q", in, got, want)
		}
	}
}

// filtered_stats 는 조건이 하나도 없으면 nil 을 반환해 호출자가 안내하도록 한다.
func TestBuildAggFilterEmpty(t *testing.T) {
	if f, _, _ := buildAggFilter(nil); f != nil {
		t.Error("빈 params 는 nil filter 여야 한다")
	}
	if f, _, _ := buildAggFilter(map[string]any{"cmd": "  "}); f != nil {
		t.Error("공백 cmd 는 조건으로 치면 안 된다")
	}
}

func TestBuildAggFilterTimeRange(t *testing.T) {
	f, desc, _ := buildAggFilter(map[string]any{"start_time": 180.0, "end_time": 190.0})
	if f == nil {
		t.Fatal("시간 범위가 filter 로 만들어지지 않았다")
	}
	if f.StartTime != 180.0 || f.EndTime != 190.0 {
		t.Errorf("시간 범위 불일치: %v ~ %v", f.StartTime, f.EndTime)
	}
	if !strings.Contains(desc, "180") || !strings.Contains(desc, "190") {
		t.Errorf("필터 설명에 값이 없다: %q", desc)
	}
}

// opcode 는 parquet 에 소문자로 저장되는데 모델은 대문자를 내기 쉽다.
// 한쪽만 넣으면 0건 매칭 → 조용한 오답이 된다(실측 확인).
func TestBuildAggFilterCmdCaseInsensitive(t *testing.T) {
	f, desc, _ := buildAggFilter(map[string]any{"cmd": "0x2A"})
	if f == nil {
		t.Fatal("cmd filter 가 만들어지지 않았다")
	}
	has := map[string]bool{}
	for _, c := range f.CmdList {
		has[c] = true
	}
	if !has["0x2a"] {
		t.Errorf("소문자 표기가 빠졌다 — parquet 실제 값과 매칭되지 않는다: %v", f.CmdList)
	}
	if !has["0x2A"] {
		t.Errorf("원본 표기가 빠졌다: %v", f.CmdList)
	}
	// 설명은 사용자가 입력한 표기 그대로.
	if !strings.Contains(desc, "0x2A") {
		t.Errorf("필터 설명에 cmd 가 없다: %q", desc)
	}
}

func TestCmdCaseVariants(t *testing.T) {
	v := cmdCaseVariants("0x2a")
	joined := strings.Join(v, ",")
	for _, want := range []string{"0x2a", "0x2A"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q 변형이 없다: %v", want, v)
		}
	}
	// 중복이 없어야 한다.
	seen := map[string]bool{}
	for _, x := range v {
		if seen[x] {
			t.Errorf("중복 변형: %q in %v", x, v)
		}
		seen[x] = true
	}
}

// 모델이 실제 값 대신 자리표시자를 넣으면 조용히 무시하지 말고 사유를 남겨야 한다.
// (실측: 후속 질문에서 14b 가 start_time="value_from_previous_question" 을 냈다.)
func TestBuildAggFilterRejectsPlaceholder(t *testing.T) {
	cases := []string{"value_from_previous_question", "이전 값", "same_as_above", "<시각>", "unknown"}
	for _, c := range cases {
		f, _, problem := buildAggFilter(map[string]any{"start_time": c})
		if f != nil {
			t.Errorf("%q: 자리표시자가 filter 로 통과했다", c)
		}
		if problem == "" {
			t.Errorf("%q: 조용히 무시됐다 — 사유를 남겨야 한다", c)
		}
	}
}

// 실제 숫자 문자열은 통과해야 한다(자리표시자 판별이 과하게 잡으면 안 된다).
func TestBuildAggFilterAcceptsNumericStrings(t *testing.T) {
	f, desc, problem := buildAggFilter(map[string]any{"start_time": "947262", "end_time": "947268"})
	if problem != "" {
		t.Fatalf("숫자 문자열이 거부됐다: %s", problem)
	}
	if f == nil || f.StartTime != 947262 || f.EndTime != 947268 {
		t.Fatalf("숫자 문자열 파싱 실패: %+v", f)
	}
	if !strings.Contains(desc, "947262") {
		t.Errorf("설명 누락: %q", desc)
	}
}

// paramFloat 는 숫자를 하나로 확정할 수 있을 때만 통과시킨다.
//
// "184초" 처럼 단위가 붙은 것은 값이 명확하므로 허용하고, 숫자가 여럿이거나 없는 것은
// 거부한다. Sscanf 시절엔 "1,2,3" 도 1 로 통과했다 — 그게 조용한 오답의 원인이었다.
func TestParamFloatSingleNumberOnly(t *testing.T) {
	if v, ok := paramFloat(map[string]any{"x": "184초"}, "x"); !ok || v != 184 {
		t.Errorf("단위 붙은 단일 숫자는 통과해야 한다: %v %v", v, ok)
	}
	if v, ok := paramFloat(map[string]any{"x": "184.5"}, "x"); !ok || v != 184.5 {
		t.Errorf("정상 숫자가 거부됐다: %v %v", v, ok)
	}
	if v, ok := paramFloat(map[string]any{"x": "1, 2, 3"}, "x"); ok {
		t.Errorf("숫자가 여럿이면 거부해야 한다: %v", v)
	}
	if _, ok := paramFloat(map[string]any{"x": "이전 값"}, "x"); ok {
		t.Error("숫자 없는 값이 통과했다")
	}
}

// 모델이 값에 설명을 붙여도 숫자가 하나뿐이면 추출해 쓴다.
// (실측: 14b 가 "With the actual number ...: 947257" 을 냈다.)
func TestParseNumericParamExtractsSingleNumber(t *testing.T) {
	cases := map[string]float64{
		"947257":    947257,
		"  184.5  ": 184.5,
		"With the actual number from the question, it should be: 947257": 947257,
		"184초":  184,
		"-12.5": -12.5,
	}
	for in, want := range cases {
		got, ok := parseNumericParam(in)
		if !ok || got != want {
			t.Errorf("parseNumericParam(%q) = %v,%v want %v", in, got, ok, want)
		}
	}
}

// 숫자가 여럿이면 어느 것이 값인지 알 수 없으므로 실패해야 한다.
// (아무거나 고르면 조용히 틀린 구간을 계산하고 근거 뱃지는 정상처럼 보인다.)
func TestParseNumericParamAmbiguous(t *testing.T) {
	for _, in := range []string{"184 ~ 190", "between 947257 and 947267", "1, 2, 3"} {
		if v, ok := parseNumericParam(in); ok {
			t.Errorf("모호한 값 %q 가 %v 로 통과했다", in, v)
		}
	}
}

// 숫자가 전혀 없는 자리표시자는 여전히 거부.
func TestParseNumericParamNoNumber(t *testing.T) {
	for _, in := range []string{"value_from_previous_question", "이전 값", "unknown", ""} {
		if _, ok := parseNumericParam(in); ok {
			t.Errorf("숫자 없는 %q 가 통과했다", in)
		}
	}
}

// cmd 는 buildFilterWhere 에서 이스케이프 없이 SQL 에 들어가고, 값의 출처가 자유 서술
// 채팅에서 파생된 LLM 출력이다. 화이트리스트를 벗어나면 반드시 거부해야 한다.
func TestBuildAggFilterRejectsUnsafeCmd(t *testing.T) {
	unsafe := []string{
		"0x2A' OR 1=1 --",
		"READ'",
		"a'; DROP TABLE x; --",
		"read OR true",
		"0x2a;",
		"0x 2a",
		strings.Repeat("a", 33), // 길이 상한
	}
	for _, in := range unsafe {
		f, _, problem := buildAggFilter(map[string]any{"cmd": in})
		if f != nil {
			t.Errorf("%q: 위험한 cmd 가 filter 로 통과했다", in)
		}
		if problem == "" {
			t.Errorf("%q: 조용히 무시됐다 — 사유를 남겨야 한다", in)
		}
	}
}

// 정상 cmd 는 통과해야 한다(화이트리스트가 과하게 막으면 기능이 죽는다).
func TestBuildAggFilterAcceptsValidCmd(t *testing.T) {
	// 앞뒤 공백/개행은 TrimSpace 로 정리된 뒤 검사되므로 통과가 정상이다.
	for _, in := range []string{"0x2a", "0x2A", "0x42", "read", "write", "discard", "flush", " 0x2a ", "0x2a\n"} {
		f, _, problem := buildAggFilter(map[string]any{"cmd": in})
		if f == nil {
			t.Errorf("%q: 정상 cmd 가 거부됐다 (%s)", in, problem)
		}
	}
}
