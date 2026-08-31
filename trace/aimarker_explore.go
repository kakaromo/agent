package trace

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// trace_marker 탐색기 — 어떤 카운터/구간 이름이 찍히는지 찾는다.
//
// **왜 필요한가.** logcat 탐색기와 같은 순환을 끊는다: 이름을 알아야 패턴을 쓰는데
// 이름을 알려면 로그를 봐야 한다. 결과는 **후보 제시까지**이고 패턴을 자동 생성하지
// 않는다 — 사람이 원문을 보고 고르는 것이 오탐을 막는 유일한 방법이다.
//
// ⚠ logcat 탐색기와 **점수 기준이 다르다.** logcat 은 자유 문구라 단위·키워드를
// 넓게 훑어야 했지만, 여기서는 `C|` 가 이미 "이름 + 숫자값" 이라 **값이 붙어 있는지
// 자체가 강한 신호**다. 그래서 규칙이 단순하고 오탐이 적다.

// MarkerCandidate — 후보 하나 (카운터/구간 이름 단위).
type MarkerCandidate struct {
	Name string `json:"name"`
	// Kind — "counter"(C|, 값 있음) 또는 "section"(B|, 구간).
	Kind string `json:"kind"`
	// Count — 이 이름이 찍힌 횟수. 시계열이면 크다 (TPOT 처럼 토큰마다).
	Count int   `json:"count"`
	PIDs  []int `json:"pids"`
	// HasValue — C| 로 숫자값이 실렸는가. 지표라면 여기가 true 다.
	HasValue bool `json:"hasValue"`
	// Min/Max — 값 범위. 사람이 "이 값이 ms 스케일인가" 를 판단하는 근거다.
	Min float64 `json:"min"`
	Max float64 `json:"max"`
	// LLMSignal — 이름에 LLM 고유 어휘(토큰·prefill/decode·TTFT)가 있는가.
	// ⚠ logcat 탐색기와 **같은 기준**을 쓴다 — 온디바이스 ML 은 어휘가 겹쳐서
	// "model load"/"inference" 로는 LLM 만 골라낼 수 없다는 그 교훈이 여기도 적용된다.
	LLMSignal bool `json:"llmSignal"`
	// OnlyDuringRun — 유휴 구간엔 없고 추론 구간에만 나타났는가.
	OnlyDuringRun bool `json:"onlyDuringRun"`
	// Samples — 사람이 판단할 근거. 원문 그대로.
	Samples []string `json:"samples"`
	Score   int      `json:"score"`
}

// MarkerExploreResult — 탐색 결과.
type MarkerExploreResult struct {
	TotalLines    int               `json:"totalLines"`
	MarkerLines   int               `json:"markerLines"`
	DistinctNames int               `json:"distinctNames"`
	Candidates    []MarkerCandidate `json:"candidates"`
	// WeakOnly — 후보는 있으나 **LLM 고유 신호가 하나도 없다.**
	WeakOnly  bool     `json:"weakOnly"`
	Diagnosis []string `json:"diagnosis"`
}

const markerCandidateLimit = 40

// ExploreMarkers — trace.log 에서 marker 이름 후보를 찾는다.
//
// 유휴/추론 구간을 주면 차분으로 "추론 때만 나타난 이름" 을 가려낸다 — 벤더 이름을
// 몰라도 걸리는 방법이라 형식이 블랙박스일 때 사실상 유일한 수단이다
// (logcat 탐색기와 같은 원리).
func ExploreMarkers(r io.Reader, opt ExploreOptions) MarkerExploreResult {
	type agg struct {
		kind          string
		count         int
		pids          map[int]bool
		hasVal        bool
		min, max      float64
		samples       []string
		inIdle, inRun bool
	}
	names := map[string]*agg{}
	var res MarkerExploreResult

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		res.TotalLines++
		if res.TotalLines > maxMarkerLines {
			res.Diagnosis = append(res.Diagnosis,
				fmt.Sprintf("줄 수 상한(%d)에 걸려 이후를 읽지 않았다.", maxMarkerLines))
			break
		}
		raw := sc.Text()
		ml, ok := parseMarkerLine(raw)
		if !ok || ml.Name == "" {
			continue
		}
		// E| 는 이름이 없어 후보가 못 된다. I| 는 값이 없어 지표로 쓰기 어렵다.
		if ml.Kind != 'C' && ml.Kind != 'B' {
			continue
		}
		res.MarkerLines++

		a := names[ml.Name]
		if a == nil {
			a = &agg{pids: map[int]bool{}, min: ml.Value, max: ml.Value}
			if ml.Kind == 'C' {
				a.kind = "counter"
			} else {
				a.kind = "section"
			}
			names[ml.Name] = a
		}

		// 유휴 구간 표시 (차분용).
		if opt.IdleFrom != 0 || opt.IdleTo != 0 {
			if inRange(ml.TimeSec, opt.IdleFrom, opt.IdleTo) {
				a.inIdle = true
			}
		}
		runAll := opt.RunFrom == 0 && opt.RunTo == 0
		if !runAll && !inRange(ml.TimeSec, opt.RunFrom, opt.RunTo) {
			continue
		}
		a.inRun = true

		a.count++
		a.pids[ml.PID] = true
		if ml.HasVal {
			if !a.hasVal {
				a.min, a.max, a.hasVal = ml.Value, ml.Value, true
			}
			if ml.Value < a.min {
				a.min = ml.Value
			}
			if ml.Value > a.max {
				a.max = ml.Value
			}
		}
		if len(a.samples) < SampleLimit {
			a.samples = append(a.samples, strings.TrimSpace(raw))
		}
	}

	doDiff := opt.IdleFrom != 0 || opt.IdleTo != 0
	strongTotal := 0
	for name, a := range names {
		if !a.inRun {
			continue
		}
		llm := markerNameLooksLLM(name)
		if llm {
			strongTotal++
		}
		c := MarkerCandidate{
			Name: name, Kind: a.kind, Count: a.count,
			HasValue: a.hasVal, Min: a.min, Max: a.max,
			LLMSignal:     llm,
			OnlyDuringRun: doDiff && !a.inIdle,
			Samples:       a.samples,
		}
		for p := range a.pids {
			c.PIDs = append(c.PIDs, p)
		}
		sort.Ints(c.PIDs)

		// 점수 — LLM 어휘가 압도적으로 중요하다. 값이 붙어 있으면 지표일 가능성이 높고,
		// 추론 구간에만 나타나면 그 둘과 곱해질 때 의미가 있다 (logcat 탐색기와 같은 판단:
		// OnlyDuringRun 단독으로는 일회성 시스템 이벤트가 상위를 먹는다).
		c.Score = c.Count
		if llm {
			c.Score += 10000
		}
		if a.hasVal {
			c.Score += 500
		}
		if c.OnlyDuringRun && (llm || a.hasVal) {
			c.Score += 1000
		}
		res.Candidates = append(res.Candidates, c)
	}
	res.DistinctNames = len(names)

	sort.SliceStable(res.Candidates, func(i, j int) bool {
		return res.Candidates[i].Score > res.Candidates[j].Score
	})
	if len(res.Candidates) > markerCandidateLimit {
		res.Diagnosis = append(res.Diagnosis, fmt.Sprintf(
			"후보가 %d개라 상위 %d개만 보여준다.", len(res.Candidates), markerCandidateLimit))
		res.Candidates = res.Candidates[:markerCandidateLimit]
	}

	res.WeakOnly = len(res.Candidates) > 0 && strongTotal == 0
	res.Diagnosis = append(res.Diagnosis, diagnoseMarkerExplore(res, doDiff)...)
	return res
}

// markerNameLooksLLM — 이름이 LLM 고유 어휘를 담고 있는가.
//
// ⚠ logcat 탐색기의 교훈을 그대로 적용한다: 온디바이스 ML 은 종류를 불문하고
// "모델 로드"·"추론" 이라는 같은 낱말을 쓰므로 그걸로는 LLM 만 골라낼 수 없다.
// **토큰 개념과 prefill/decode 단계명, TTFT** 만 LLM 고유하다.
func markerNameLooksLLM(name string) bool {
	return llmNameRe.MatchString(name)
}

// llmNameRe — **이름 전용** 판정. logcat 의 strongRe 를 그대로 쓰지 않는 이유:
// 저쪽은 자유 문구(문장)를 상대로 하느라 `decode … tokens` 처럼 문맥을 함께 요구하는
// 규칙이 들어 있는데, 여기 대상은 짧은 **식별자**(`llm.ttft_ms`, `decode_tok_per_s`)라
// 그 문맥 규칙이 오히려 안 맞는다.
//
// ⚠ 그래도 원칙은 같다 — 토큰 개념·단계명·TTFT 만 LLM 고유하다.
// `model`/`inference`/`npu` 는 **넣지 않는다**: 음성 wakeword·얼굴인식 등 다른
// 온디바이스 ML 이 같은 낱말을 쓰기 때문이다(logcat 탐격기에서 실측으로 확인).
//
// ⚠⚠ **`token` 단독은 넣지 않는다.** 실기기 실측에서 Android 시스템 마커가 통째로
// 걸렸다 — Binder 가 이름 끝에 핸들을 붙인다:
//
//	B|2396|setTransactionState: transaction(Id:…)-token:0xb40000796fe7eea0
//	B|2992|serviceBind: BindServiceData{token=…}
//
// Binder 토큰과 LLM 토큰은 이름만 같은 **완전히 다른 개념**이다. 476개 후보 중
// 시스템 마커가 상위를 통째로 먹었다(logcat 에서 wakeword 가 1위였던 것과 같은 실패).
// 그래서 토큰은 **LLM 문맥이 붙은 형태**(tok/s, ms/tok, n_tokens, tokens_per_…)만 본다.
var llmNameRe = regexp.MustCompile(
	`(?i)(ttft|time.?to.?first|first.?token|` +
		`tok(en)?s?[_./ ]?(per|/)[_ ]?(s|sec|second)|` + // tok/s, tokens_per_sec
		`(ms|us|ns)[_./ ]?(per)?[_./ ]?tok(en)?s?\b|` + // ms/tok, ms_per_token
		`n_tokens|num_tokens|token_count|` +
		`prefill|decode[_. ]?(step|token|time|tok)|prompt[_. ]?eval|` +
		`kv[_. ]?cache|\bllm\b|llama|gguf)`)

func diagnoseMarkerExplore(res MarkerExploreResult, doDiff bool) []string {
	var out []string
	switch {
	case res.TotalLines == 0:
		out = append(out, "로그가 비어 있다 — trace 수집 자체를 확인할 것.")
	case res.MarkerLines == 0:
		out = append(out, "trace_marker 줄이 하나도 없다. 앱이 마커를 안 찍었거나 atrace 카테고리가 "+
			"안 켜진 것이다 — 수집 시 `atrace <카테고리>` 를 함께 켜야 할 수 있다.")
	case len(res.Candidates) == 0:
		out = append(out, "marker 는 있으나 지정한 구간에 나타난 이름이 없다 — 구간 범위를 확인할 것.")
	}
	if res.WeakOnly && len(res.Candidates) > 0 {
		out = append(out, "⚠ LLM 고유 신호(토큰·prefill/decode·TTFT)가 이름에서 **한 건도 없다**. "+
			"목록이 전부 무관한 시스템 카운터일 수 있다 — 값 범위와 원문을 보고 판단할 것.")
	}
	if !doDiff && len(res.Candidates) > 0 {
		out = append(out, "유휴 구간을 지정하면 \"추론 때만 나타난 이름\" 을 가려낼 수 있어 정확도가 크게 오른다.")
	}
	return out
}
