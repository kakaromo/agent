package trace

import (
	"strings"
	"testing"
)

// 사용자가 제시한 형태 그대로.
const sampleLog = `9571.204   900   900 I Genie   : model load start: llama3-8b-q4.dlc
9573.184   900   900 I Genie   : model load done (1980 ms)
9573.594   900   900 I Genie   : context init done (410 ms)
9573.594   901   901 I QnnHtp  : prefill begin, 512 tokens
9574.044   901   901 I QnnHtp  : prefill done (450 ms)
9574.044   900   900 I Genie   : first token emitted — TTFT 2840 ms
9574.068   900   900 I Genie   : decode 24.1 ms/tok
9574.092   900   900 I Genie   : decode 23.8 ms/tok
9574.120   900   900 I Genie   : decode 31.5 ms/tok`

func fullPatterns() *LogcatPatterns {
	return &LogcatPatterns{
		Tags: []string{"Genie", "QnnHtp"},
		Marks: []LogcatMark{
			{Key: "load_start", Regex: `model load start`},
			{Key: "prefill_begin", Regex: `prefill begin`},
		},
		Series: []LogcatSeries{
			{Key: "load_ms", Regex: `model load done \(([0-9.]+) ms\)`, Unit: "ms"},
			{Key: "ttft_ms", Regex: `TTFT ([0-9.]+) ms`, Unit: "ms"},
			{Key: "tpot_ms", Regex: `decode ([0-9.]+) ms/tok`, Unit: "ms"},
		},
	}
}

func TestParseLogcat_Basic(t *testing.T) {
	res, err := ParseLogcat(strings.NewReader(sampleLog), fullPatterns())
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalHits == 0 {
		t.Fatal("0건 매칭")
	}
	if res.Partial {
		t.Errorf("전 패턴이 맞았는데 Partial 이다 (missing=%v)", res.MissingKeys)
	}

	// mark 는 시각만 쓴다.
	if len(res.Marks) != 2 {
		t.Fatalf("mark %d개, 기대 2: %+v", len(res.Marks), res.Marks)
	}
	if res.Marks[0].Key != "load_start" || res.Marks[0].TimeSec != 9571.204 {
		t.Errorf("첫 mark = %+v", res.Marks[0])
	}
	// 시각 순 정렬
	if res.Marks[1].TimeSec < res.Marks[0].TimeSec {
		t.Error("mark 가 시각 순으로 정렬되지 않았다")
	}

	// series — 단일값
	if ttft := res.Series["ttft_ms"]; ttft.Count != 1 || ttft.Points[0].Value != 2840 {
		t.Errorf("ttft = %+v", ttft)
	}
	if load := res.Series["load_ms"]; load.Count != 1 || load.Points[0].Value != 1980 {
		t.Errorf("load = %+v", load)
	}

	// series — 시계열 + 분포. TPOT 는 평균만 보면 "뒤로 갈수록 느려짐" 을 놓친다.
	tpot := res.Series["tpot_ms"]
	if tpot.Count != 3 {
		t.Fatalf("tpot count = %d, 기대 3", tpot.Count)
	}
	if tpot.Min != 23.8 || tpot.Max != 31.5 {
		t.Errorf("tpot min/max = %v/%v, 기대 23.8/31.5", tpot.Min, tpot.Max)
	}
	if tpot.Median != 24.1 {
		t.Errorf("tpot median = %v, 기대 24.1", tpot.Median)
	}
	if tpot.Unit != "ms" {
		t.Errorf("unit 이 전달되지 않았다: %q", tpot.Unit)
	}

	if strings.Join(res.MatchedTags, ",") != "Genie,QnnHtp" {
		t.Errorf("MatchedTags = %v", res.MatchedTags)
	}
}

// ⚠ 0건은 실패다. 조용히 빈 결과를 내면 화면엔 "측정 완료" 가 뜨는데 값이 없다.
func TestParseLogcat_ZeroMatchIsDiagnosed(t *testing.T) {
	p := &LogcatPatterns{Series: []LogcatSeries{
		{Key: "ttft_ms", Regex: `NOPE ([0-9.]+) ms`}}}
	res, err := ParseLogcat(strings.NewReader(sampleLog), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalHits != 0 {
		t.Fatalf("매칭이 있으면 안 된다: %d", res.TotalHits)
	}
	joined := strings.Join(res.Diagnosis, "\n")
	if !strings.Contains(joined, "0건 매칭") {
		t.Errorf("0건 진단이 없다: %v", res.Diagnosis)
	}
	// stderr 가능성을 반드시 짚어야 한다 — 패턴을 고쳐도 소용없는 경우다.
	if !strings.Contains(joined, "stderr") {
		t.Errorf("stderr 안내가 없다: %v", res.Diagnosis)
	}
	// ⚠ 줄은 있었으므로 "수집 실패" 로 안내하면 안 된다 (원인이 다르다).
	if strings.Contains(joined, "비어 있다") {
		t.Errorf("줄이 있는데 수집 실패로 안내했다: %v", res.Diagnosis)
	}
}

// ⚠ 부분 매칭을 성공으로 처리하면 반쪽 지표가 정상처럼 뜬다.
func TestParseLogcat_PartialIsFlagged(t *testing.T) {
	p := fullPatterns()
	p.Series = append(p.Series, LogcatSeries{
		Key: "never_ms", Regex: `NEVER ([0-9.]+) ms`})
	res, err := ParseLogcat(strings.NewReader(sampleLog), p)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Partial {
		t.Error("일부 패턴이 안 맞았는데 Partial 이 꺼져 있다")
	}
	if len(res.MissingKeys) != 1 || res.MissingKeys[0] != "never_ms" {
		t.Errorf("MissingKeys = %v", res.MissingKeys)
	}
	if !strings.Contains(strings.Join(res.Diagnosis, "\n"), "부분 매칭") {
		t.Errorf("부분 매칭 경고가 없다: %v", res.Diagnosis)
	}
}

// 빈 파일 = 수집 실패. 패턴 문제와 원인이 다르므로 안내가 달라야 한다.
func TestParseLogcat_EmptyIsCollectionFailure(t *testing.T) {
	res, err := ParseLogcat(strings.NewReader(""), fullPatterns())
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Diagnosis, "\n")
	if !strings.Contains(joined, "수집") {
		t.Errorf("수집 실패로 안내해야 한다: %v", res.Diagnosis)
	}
	if strings.Contains(joined, "탐색 모드로 재확인") {
		t.Errorf("빈 파일에 패턴 안내를 했다: %v", res.Diagnosis)
	}
}

// 정규식은 맞았는데 캡처가 숫자가 아닌 경우 — "패턴이 틀렸다" 와 원인이 다르다.
func TestParseLogcat_CaptureNotNumeric(t *testing.T) {
	log := `100.000 900 900 I Genie   : decode fast ms/tok`
	p := &LogcatPatterns{Series: []LogcatSeries{
		{Key: "tpot_ms", Regex: `decode (\w+) ms/tok`}}}
	res, err := ParseLogcat(strings.NewReader(log), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats[0].ParseFailures != 1 {
		t.Errorf("ParseFailures = %d, 기대 1", res.Stats[0].ParseFailures)
	}
	if !strings.Contains(strings.Join(res.Diagnosis, "\n"), "캡처 그룹") {
		t.Errorf("캡처 그룹 안내가 없다: %v", res.Diagnosis)
	}
}

// 저장 단계에서 걸러지지만 파서도 스스로를 지켜야 한다 (다른 경로로 들어올 수 있다).
func TestParseLogcat_RejectsBadPatterns(t *testing.T) {
	if _, err := ParseLogcat(strings.NewReader(sampleLog), nil); err == nil {
		t.Error("nil 패턴이 통과됐다")
	}
	if _, err := ParseLogcat(strings.NewReader(sampleLog), &LogcatPatterns{}); err == nil {
		t.Error("빈 패턴이 통과됐다")
	}
	bad := &LogcatPatterns{Marks: []LogcatMark{{Key: "a", Regex: "([bad"}}}
	if _, err := ParseLogcat(strings.NewReader(sampleLog), bad); err == nil {
		t.Error("잘못된 정규식이 통과됐다")
	}
	noCap := &LogcatPatterns{Series: []LogcatSeries{{Key: "a", Regex: `TTFT [0-9]+ ms`}}}
	if _, err := ParseLogcat(strings.NewReader(sampleLog), noCap); err == nil {
		t.Error("캡처 그룹 없는 series 가 통과됐다 — 조용히 빈 시계열이 된다")
	}
}

func TestParseLogcatPatternsJSON(t *testing.T) {
	p, err := ParseLogcatPatternsJSON(
		`{"tags":["Genie"],"series":[{"key":"ttft_ms","regex":"TTFT ([0-9.]+) ms","unit":"ms"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Series) != 1 || p.Series[0].Key != "ttft_ms" || p.Series[0].Unit != "ms" {
		t.Errorf("파싱 결과 = %+v", p)
	}
	if _, err := ParseLogcatPatternsJSON(`{broken`); err == nil {
		t.Error("깨진 JSON 이 통과됐다")
	}
}

// ⚠ 키 중복은 저장 경로(ValidatePatternsJSON)가 막지만, `POST /logcat/parse` 의
// inline patternsJson 은 그 검증을 안 탄다. 통과시키면 stat map 에서 한쪽이 다른
// 쪽을 덮어써 **Stats 에 같은 항목이 두 번 실리고 TotalHits 가 부풀려진다** —
// 매칭 통계는 진단의 근거라 근거 없이 틀리면 안 된다.
func TestParseLogcat_RejectsDuplicateKeys(t *testing.T) {
	p := &LogcatPatterns{
		Marks:  []LogcatMark{{Key: "x", Regex: "prefill"}},
		Series: []LogcatSeries{{Key: "x", Regex: "TTFT ([0-9.]+)"}},
	}
	_, err := ParseLogcat(strings.NewReader("100.0 1 1 I T: prefill\n"), p)
	if err == nil {
		t.Error("mark 와 series 가 키를 공유하는데 통과했다 — 통계가 조용히 틀어진다")
	}
}

// ⚠ 시간 상한 검사 주기가 파일 길이보다 크면 **상한이 아예 안 걸린다.**
// 1만 줄 미만 파일 + catastrophic backtracking 정규식 = 무한정.
// (사무실 모드는 0.0.0.0·인증 없음이라 요청 하나로 goroutine 을 붙잡을 수 있다.)
func TestParseLogcat_DeadlineCheckCoversShortFiles(t *testing.T) {
	if deadlineCheckEvery > 1000 {
		t.Errorf("상한 검사 주기가 %d 줄 — 그보다 짧은 파일은 검사를 한 번도 안 탄다",
			deadlineCheckEvery)
	}
}
