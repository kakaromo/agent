package trace

import (
	"strings"
	"testing"
)

// 실기기(S25) atrace 마커 형식 그대로. 시스템 마커와 LLM 마커가 섞인 상황이 실제다.
const markerFixture = `  binder:2396_4-4474    (   2396) [001] ..... 100.100000: tracing_mark_write: B|2396|setTransactionState: transaction(Id:12850542534543 )-token:0xb40000796fe7eea0
  android.display-3436  (   2992) [000] ..... 100.200000: tracing_mark_write: B|2992|serviceBind: BindServiceData{token=abc}
  LazyTaskWriterT-13262 (   2992) [000] ..... 100.300000: tracing_mark_write: C|2992|Heap size (KB)|161230
  surfaceflinger-3608   (   2396) [000] ..... 100.400000: tracing_mark_write: E|2396
  llama-runner-9001     (   9001) [002] ..... 101.000000: tracing_mark_write: B|9001|prefill
  llama-runner-9001     (   9001) [002] ..... 101.500000: tracing_mark_write: C|9001|llm.ttft_ms|2840
  llama-runner-9001     (   9001) [002] ..... 101.600000: tracing_mark_write: C|9001|decode_ms_per_token|24
  llama-runner-9001     (   9001) [002] ..... 101.700000: tracing_mark_write: C|9001|decode_ms_per_token|26`

func TestParseMarkerLine(t *testing.T) {
	got, ok := parseMarkerLine("  x-1 ( 2992) [000] ..... 100.300000: tracing_mark_write: C|2992|Heap size (KB)|161230")
	if !ok {
		t.Fatal("정상 C| 줄을 못 읽었다")
	}
	if got.Kind != 'C' || got.PID != 2992 || got.Name != "Heap size (KB)" || got.Value != 161230 || !got.HasVal {
		t.Errorf("파싱 결과가 틀렸다: %+v", got)
	}
	if got.TimeSec != 100.3 {
		t.Errorf("시각 오독: %v", got.TimeSec)
	}
	// ⚠ 형식이 아닌 줄은 false 여야 한다. 조용히 0 값을 채우면 그 줄이
	// "시각 0의 이벤트" 로 둔갑해 집계를 오염시킨다.
	for _, bad := range []string{"", "그냥 아무 줄", "  x-1 [000] 100.0: other_event: B|1|x"} {
		if _, ok := parseMarkerLine(bad); ok {
			t.Errorf("형식이 아닌 줄을 통과시켰다: %q", bad)
		}
	}
}

// ⚠⚠ Binder 가 마커 이름에 핸들을 붙인다 (`-token:0x…`, `{token=…}`).
// `token` 을 단독 신호로 쓰면 **시스템 마커가 통째로 LLM 으로 잡힌다** —
// 실기기 실측에서 상위를 전부 먹었다. logcat 의 wakeword 사례와 같은 실패다.
func TestExploreMarkers_BinderTokenIsNotLLM(t *testing.T) {
	res := ExploreMarkers(strings.NewReader(markerFixture), ExploreOptions{})
	for _, c := range res.Candidates {
		if strings.Contains(c.Name, "setTransactionState") || strings.Contains(c.Name, "serviceBind") {
			if c.LLMSignal {
				t.Errorf("Binder 마커가 LLM 신호로 잡혔다: %q", c.Name)
			}
		}
	}
}

// 진짜 LLM 마커는 시스템 마커 사이에서 상위로 올라와야 한다.
func TestExploreMarkers_FindsLLMCounters(t *testing.T) {
	res := ExploreMarkers(strings.NewReader(markerFixture), ExploreOptions{})
	if len(res.Candidates) == 0 {
		t.Fatal("후보가 없다")
	}
	top := res.Candidates[0]
	if top.Name != "decode_ms_per_token" {
		t.Errorf("1위가 %q — 반복되는 LLM 카운터가 와야 한다", top.Name)
	}
	if !top.HasValue || top.Min != 24 || top.Max != 26 {
		t.Errorf("값 범위가 안 잡혔다: %+v", top)
	}
	if res.WeakOnly {
		t.Error("LLM 신호가 있는데 WeakOnly 가 켜졌다")
	}
}

// LLM 마커가 없으면 **그렇다고 말해야 한다.** 조용히 목록만 주면 사용자는
// 그중에 답이 있다고 믿고 시간을 쓴다 (logcat 탐색기와 같은 판단).
func TestExploreMarkers_WeakOnlyWhenNoLLM(t *testing.T) {
	sysOnly := strings.Join(strings.Split(markerFixture, "\n")[:4], "\n")
	res := ExploreMarkers(strings.NewReader(sysOnly), ExploreOptions{})
	if len(res.Candidates) > 0 && !res.WeakOnly {
		t.Error("LLM 신호가 0건인데 WeakOnly 가 꺼져 있다 — 안전장치가 죽는다")
	}
}

// 사용자가 입력한 이름/정규식으로 값을 뽑는다.
func TestParseMarkerPatterns(t *testing.T) {
	p := &MarkerPatterns{
		Counters: []MarkerCounter{
			{Key: "ttft", Name: "llm.ttft_ms", Unit: "ms"},
			{Key: "tpot", Name: "decode_ms_per_token", Unit: "ms"},
			{Key: "heap", Regex: `^Heap size`, Unit: "KB"},
			{Key: "nope", Name: "없는이름"},
		},
		Sections: []MarkerSection{{Key: "prefill", Name: "prefill"}},
	}
	res, err := ParseMarkerPatterns(strings.NewReader(markerFixture), p)
	if err != nil {
		t.Fatal(err)
	}
	if s := res.Series["ttft"]; s.Count != 1 || s.Median != 2840 {
		t.Errorf("ttft: %+v", s)
	}
	if s := res.Series["tpot"]; s.Count != 2 || s.Min != 24 || s.Max != 26 {
		t.Errorf("tpot: %+v", s)
	}
	if s := res.Series["heap"]; s.Count != 1 {
		t.Errorf("정규식 매칭이 안 됐다: %+v", s)
	}
	if len(res.Marks) != 1 || res.Marks[0].Key != "prefill" {
		t.Errorf("구간(B|)이 안 잡혔다: %+v", res.Marks)
	}
	// ⚠ 일부만 걸리면 경고해야 한다. 성공으로 처리하면 반쪽 지표가 정상처럼 뜬다.
	if !res.Partial {
		t.Error("일부만 걸렸는데 partial 이 false 다")
	}
	if len(res.MissingKeys) != 1 || res.MissingKeys[0] != "nope" {
		t.Errorf("missingKeys: %v", res.MissingKeys)
	}
}

// ⚠ 키 중복은 통계가 조용히 틀어진다 (logcat 파서와 같은 이유).
func TestParseMarkerPatterns_RejectsDuplicateKey(t *testing.T) {
	p := &MarkerPatterns{
		Counters: []MarkerCounter{{Key: "x", Name: "a"}},
		Sections: []MarkerSection{{Key: "x", Name: "b"}},
	}
	if _, err := ParseMarkerPatterns(strings.NewReader(markerFixture), p); err == nil {
		t.Error("키 중복인데 통과했다")
	}
}

// name/regex 둘 다 비면 무엇을 찾을지 알 수 없다 — 저장 전에 막는다.
func TestParseMarkerPatterns_RejectsEmptyMatcher(t *testing.T) {
	p := &MarkerPatterns{Counters: []MarkerCounter{{Key: "x"}}}
	if _, err := ParseMarkerPatterns(strings.NewReader(markerFixture), p); err == nil {
		t.Error("name/regex 둘 다 없는데 통과했다")
	}
}
