package trace

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
)

// 탐색(explore) — 런타임의 로그 형식을 **모를 때** 후보를 찾아준다.
//
// 왜 필요한가: 태그를 알아야 `logcat -s <tag>` 로 좁힐 수 있는데, 태그를 알려면
// 로그를 봐야 한다. 이 순환을 끊는 1회성 단계다.
//
// ⚠ 여기서 하는 일은 **후보 제시까지**다. 프로파일을 자동으로 만들지 않는다.
// 실제로 `Time To First Fix`(GPS 로그)가 TTFT 처럼 보이는 사례가 있었다 —
// 그럴듯한 오탐이 조용히 프로파일에 들어가면 이후 모든 측정이 틀린다.
// 사람이 원문을 보고 고르게 하는 것이 이 설계의 핵심이다.

// unitRe — 숫자 + 시간/처리량 단위. 지표를 찍는 줄은 거의 이 형태다.
//
// 커버하는 형태: `1980 ms` `24.1 ms/tok` `41.5 tokens per second` `0.88ms`
// `450us` `TTFT=2840ms`
//
// ⚠ 맨 처음엔 끝에 `|s\b` 를 넣었다가 뺐다. 실기기 로그로 돌려보니 `status=0`,
// `sockets`, `state` 같은 것에 걸려 Bluetooth/sensor 소음이 상위를 채웠다.
// "초" 단위는 이 정도 이득에 비해 오탐이 압도적으로 크다 — 추론 지표는 거의
// ms 단위로 찍힌다.
var unitRe = regexp.MustCompile(
	`(?i)[0-9]+(?:\.[0-9]+)?\s*(?:ms/tok|ms per token|tokens?/s(?:ec)?\b|tokens? per second|tok/s|ms\b|msec\b|us\b|µs\b)`)

// strongRe — 추론 지표를 **직접** 가리키는 표현. 이게 걸리면 거의 진짜다.
//
// 위 unitRe/keywordRe 는 넓게 훑는 그물이라 소음이 섞인다. 반면 여기 있는 것들은
// 다른 도메인에서 잘 안 쓰는 조합이라 신뢰도가 높아 점수를 크게 준다.
// ⚠ Go 의 RE2 에는 negative lookahead 가 없다. 그래서 `time to first fix`(GPS) 같은
// 알려진 오탐은 정규식 안에서 못 빼고 knownFalseRe 로 따로 거른다.
// ⚠⚠ 여기서 `model load` / `graph prepare` / `context init` 를 **뺐다.**
// 실기기 로그로 돌려보니 이것들이 **음성 wakeword 엔진**을 최상위로 올렸다:
// `loadPhraseSoundModel`, `Loading sound model`, `graph_prepare` 가 전부 걸린다.
// (관측은 삼성 기기였지만 벤더 무관하다 — vivo Jovi, Xiaomi XiaoAI, Google
// Assistant 등 어느 wakeword 엔진이든 같은 낱말을 쓴다.)
//
// 근본 문제는 튜닝이 아니라 **어휘가 겹친다**는 것이다. 온디바이스 ML 은 종류를
// 불문하고 "모델을 로드" 하고 "추론" 한다 — 음성 wakeword, 얼굴인식, 사진 분류가
// 전부 같은 낱말을 쓴다. 낱말로는 LLM 만 골라낼 수 없다.
//
// 그래서 여기 남긴 것은 **LLM 고유의 것들뿐**이다: 토큰이라는 개념(`tok/s`,
// `ms/tok`), prefill/decode 라는 단계 이름, TTFT. 이것들은 다른 ML 도메인에서
// 거의 안 쓴다. 실제로 이 로그에서 아래 표현의 히트는 **0건**이었고(정답이다 —
// 이 앱은 타이밍을 안 찍는다), 반면 soundmodel 계열은 639건이었다.
// ⚠⚠ `decode <숫자> ms` 와 `<숫자> tokens` 를 **단독으로 쓰면 안 된다.**
// 실측으로 확인한 오탐 (전부 흔한 시스템 로그다):
//
//	MediaCodec  `video decode 8 ms per frame`     → `decode.*[0-9]+\s*ms`
//	AudioFlinger `decode 5 ms buffer underrun`    → 〃
//	OAuth       `refreshed 2 tokens for account`  → `[0-9]+\s*tokens?\b`
//
// 이건 위에 적은 것과 **같은 실패**다 — 어휘가 겹친다. 다만 이번엔 다른 ML 이
// 아니라 코덱·인증처럼 아예 무관한 도메인이라 더 흔하다. 동영상 한 번만 틀어도
// 걸리므로 실기기에선 사실상 상시 오탐이고, 그러면 WeakOnly 가 영영 안 뜬다
// (= "LLM 신호 0건" 경고가 죽는다. 이 기능의 안전장치가 통째로 무력화된다).
//
// 그래서 이 둘은 **토큰 문맥을 함께 요구**하도록 좁혔다:
//
//	`decode` 는 token/tok 과 같은 줄에 있을 때만 (양쪽 순서 모두 허용)
//	숫자+tokens 는 prefill/decode/prompt/generate 같은 LLM 단계어가 있을 때만
//
// 코덱의 `decode 8 ms` 에는 token 이 없고, OAuth 의 `2 tokens` 에는 단계어가 없다.
var strongRe = regexp.MustCompile(
	`(?i)(ttft|time.to.first|first.token|ms/tok|ms per token|tok/s|tokens?/s(?:ec)?\b|` +
		`tokens? per second|prefill|prompt.eval|eval.time|` +
		`decode[^\n]*\btokens?\b|\btokens?\b[^\n]*decode|` +
		`(?:prefill|decode|prompt|generat\w*|infer\w*)[^\n]*?[0-9]+\s*tokens?\b|` +
		`[0-9]+\s*tokens?\b[^\n]*?(?:prefill|decode|prompt|generat\w*|infer\w*))`)

// knownFalseRe — 강한 신호처럼 보이지만 추론과 무관한 것들.
//
// ⚠ 실제로 만난 오탐만 넣는다. 짐작으로 넣으면 진짜 신호를 지울 수 있다.
//   - `Time To First Fix` — GPS 의 첫 측위 시간. `time to first` 에 걸린다.
var knownFalseRe = regexp.MustCompile(`(?i)time to first fix`)

// keywordRe — 추론 단계를 가리키는 낱말. 단위가 없는 마커 줄
// (예: `prefill begin`, `model load start`) 을 잡기 위한 보조 축이다.
//
// ⚠ 벤더 이름을 여기 넣지 않는다. vivo/QNN/MTK 마다 이름이 다른데 목록으로
// 쫓아가면 새 벤더가 나올 때마다 코드를 고쳐야 한다. 대신 **동작을 가리키는
// 일반 낱말**만 둔다 — 벤더가 뭘 부르든 이 낱말들은 공통으로 쓴다.
// ⚠ `dsp`/`npu` 단독은 뺐다 — 음성 wakeword 는 상시 대기라 DSP 에서 도는 것이
// 일반적이고, 그래서 그 계열이 통째로 걸렸다. 가속기 이름은 "LLM 이다" 의 근거가
// 못 된다 (어느 온디바이스 ML 이든 같은 가속기를 쓴다).
var keywordRe = regexp.MustCompile(
	`(?i)\b(ttft|time.to.first|first.token|prefill|decode|prompt.eval|eval.time|` +
		`inference|infer|generat\w*|token|llm|llama|gguf|context.(?:init|length)|` +
		`kv.?cache|warm.?up|quantiz\w*)\b`)

// ExploreCandidate — 후보 한 건 (태그 단위로 묶는다).
type ExploreCandidate struct {
	Tag string `json:"tag"`
	// PIDs — 이 태그를 찍은 프로세스들. 여러 개면 시스템 서비스일 가능성이 높다.
	PIDs []int `json:"pids"`
	// Lines — 이 태그가 찍은 전체 줄 수 (구간 안에서).
	Lines int `json:"lines"`
	// UnitHits / KeywordHits — 지표/단계로 보이는 줄 수 (넓은 그물, 소음 섞임).
	UnitHits    int `json:"unitHits"`
	KeywordHits int `json:"keywordHits"`
	// StrongHits — 추론 지표를 직접 가리키는 표현이 걸린 줄 수. 가장 믿을 만한 신호다.
	StrongHits int `json:"strongHits"`
	// OnlyDuringRun — 유휴 구간엔 없고 추론 구간에만 나타났는가.
	// ⚠ 이게 가장 강한 신호다. 벤더 이름을 몰라도 걸린다.
	OnlyDuringRun bool `json:"onlyDuringRun"`
	// Samples — 사람이 판단할 근거. 원문 그대로 (최대 SampleLimit 개).
	Samples []string `json:"samples"`
	// Score — 정렬용. 사람이 볼 순서를 정할 뿐 자동 채택 기준이 아니다.
	Score int `json:"score"`
}

// ExploreResult — 탐색 결과.
type ExploreResult struct {
	TotalLines  int                `json:"totalLines"`
	ParsedLines int                `json:"parsedLines"`
	DistinctTag int                `json:"distinctTags"`
	Candidates  []ExploreCandidate `json:"candidates"`
	// WeakOnly — 후보는 있으나 **LLM 고유 신호가 하나도 없다.**
	// 즉 목록이 전부 "어휘만 겹치는" 것일 수 있다는 뜻이다. 화면에서 이 경우를
	// 눈에 띄게 구분해야 한다 — 목록이 있다는 것만으로 답이 있다고 읽히면 안 된다.
	WeakOnly bool `json:"weakOnly"`
	// Diagnosis — 후보가 적거나 없을 때 **왜 그런지**. 화면에 그대로 보여준다.
	// ⚠ 빈 결과를 조용히 내면 사용자는 "패턴을 더 고쳐볼까" 로 헛수고한다.
	// 원인이 "런타임이 안 찍는다" 면 패턴은 아무 소용이 없다.
	Diagnosis []string `json:"diagnosis"`
}

const (
	// SampleLimit — 후보마다 보여줄 원문 줄 수.
	SampleLimit = 5
	// candidateLimit — 상위 몇 개까지 제시할지. 3000개 태그를 다 보여주면 못 고른다.
	candidateLimit = 40
)

// ExploreOptions — 탐색 입력.
type ExploreOptions struct {
	// IdleFrom/IdleTo — 유휴 구간(추론 전). 이 구간에도 나온 태그는 배경 소음이다.
	// 0,0 이면 차분을 하지 않는다.
	IdleFrom, IdleTo float64
	// RunFrom/RunTo — 추론 구간. 0,0 이면 전체를 대상으로 본다.
	RunFrom, RunTo float64
}

// ExploreLogcat — 로그를 훑어 후보 태그를 뽑는다.
//
// ⚠ PID 로 거르지 않는다. 온디바이스 AI 는 **앱이 아니라 시스템 서비스**에서 도는
// 경우가 흔하고(vivo/OPPO 계열이 그렇다), 앱 PID 로 좁히면 그런 구조에서 아무것도
// 안 잡힌다. 대신 "추론 구간에만 나타나는가" 로 거른다 — 어느 프로세스가 찍든 걸린다.
func ExploreLogcat(r io.Reader, opt ExploreOptions) ExploreResult {
	type agg struct {
		pids                         map[int]bool
		lines, unit, keyword, strong int
		samples                      []string
		inIdle, inRun                bool
	}
	tags := map[string]*agg{}
	res := ExploreResult{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		raw := sc.Text()
		res.TotalLines++
		l, ok := ParseLogcatLine(raw)
		if !ok {
			continue
		}
		res.ParsedLines++

		a := tags[l.Tag]
		if a == nil {
			a = &agg{pids: map[int]bool{}}
			tags[l.Tag] = a
		}
		if inRange(l.TimeSec, opt.IdleFrom, opt.IdleTo) {
			a.inIdle = true
		}
		// 추론 구간 지정이 없으면 전부를 추론 구간으로 본다.
		runAll := opt.RunFrom == 0 && opt.RunTo == 0
		if !runAll && !inRange(l.TimeSec, opt.RunFrom, opt.RunTo) {
			continue
		}
		a.inRun = true
		a.pids[l.PID] = true
		a.lines++

		hasUnit := unitRe.MatchString(l.Message)
		hasKeyword := keywordRe.MatchString(l.Message)
		hasStrong := strongRe.MatchString(l.Message) && !knownFalseRe.MatchString(l.Message)
		if hasUnit {
			a.unit++
		}
		if hasKeyword {
			a.keyword++
		}
		if hasStrong {
			a.strong++
		}
		// 샘플은 강한 신호를 우선 채운다 — 사람이 처음 보는 줄이
		// 판단에 가장 도움 되는 줄이어야 한다.
		if hasStrong && len(a.samples) < SampleLimit {
			a.samples = append([]string{raw}, a.samples...)
			if len(a.samples) > SampleLimit {
				a.samples = a.samples[:SampleLimit]
			}
		} else if (hasUnit || hasKeyword) && len(a.samples) < SampleLimit {
			a.samples = append(a.samples, raw)
		}
	}

	doDiff := opt.IdleFrom != 0 || opt.IdleTo != 0
	for tag, a := range tags {
		res.DistinctTag++
		if !a.inRun || (a.unit == 0 && a.keyword == 0 && a.strong == 0) {
			continue
		}
		c := ExploreCandidate{
			Tag: tag, Lines: a.lines,
			UnitHits: a.unit, KeywordHits: a.keyword, StrongHits: a.strong,
			OnlyDuringRun: doDiff && !a.inIdle,
			Samples:       a.samples,
		}
		for p := range a.pids {
			c.PIDs = append(c.PIDs, p)
		}
		sort.Ints(c.PIDs)

		// 점수 배분 — 실기기 로그로 조정한 값이다.
		//
		// strong(추론 지표를 직접 가리키는 표현) 을 압도적으로 높게 둔다.
		// unit/keyword 는 넓은 그물이라 소음이 섞이므로 낮게 준다.
		//
		// ⚠ OnlyDuringRun 은 **단독으로는 가중치를 주지 않는다.** 처음엔 +50 을
		// 줬다가 뺐다 — 카메라가 닫히거나 스캔이 끝나는 등 그 구간에만 나타나는
		// 일회성 태그가 지표 하나 없이도 상위를 차지했다. 진짜 신호와 곱해질 때만
		// 의미가 있다.
		c.Score = a.strong*40 + a.unit*2 + a.keyword
		if c.OnlyDuringRun && a.strong > 0 {
			c.Score *= 2
		}
		res.Candidates = append(res.Candidates, c)
	}

	sort.Slice(res.Candidates, func(i, j int) bool {
		if res.Candidates[i].Score != res.Candidates[j].Score {
			return res.Candidates[i].Score > res.Candidates[j].Score
		}
		return res.Candidates[i].Tag < res.Candidates[j].Tag
	})
	// ⚠ 잘라낸 사실을 진단에 남긴다. 조용히 자르면 "이게 전부" 로 읽힌다.
	if len(res.Candidates) > candidateLimit {
		res.Diagnosis = append(res.Diagnosis, fmt.Sprintf(
			"후보 %d개 중 상위 %d개만 표시한다 (점수 순). 나머지 %d개는 잘렸다.",
			len(res.Candidates), candidateLimit, len(res.Candidates)-candidateLimit))
		res.Candidates = res.Candidates[:candidateLimit]
	}
	// ⚠ 강한 신호가 하나도 없으면 **그렇다고 말한다.**
	// 이 목록은 어휘가 겹치는 다른 온디바이스 ML(음성 wakeword, 얼굴인식 등)일
	// 가능성이 크다. 조용히 40개를 늘어놓으면 사용자는 그중에 답이 있다고 믿고
	// 시간을 쓴다 — 실제로 이 로그가 그런 경우였다(진짜 지표 0건, 음성 ML 639건).
	strongTotal := 0
	for _, c := range res.Candidates {
		strongTotal += c.StrongHits
	}
	if strongTotal == 0 && len(res.Candidates) > 0 {
		res.WeakOnly = true
	}
	res.Diagnosis = append(res.Diagnosis, diagnose(res, doDiff)...)
	return res
}

func inRange(t, from, to float64) bool {
	if from == 0 && to == 0 {
		return false
	}
	if from != 0 && t < from {
		return false
	}
	if to != 0 && t > to {
		return false
	}
	return true
}

// diagnose — 결과가 빈약할 때 **원인을 구분해** 알린다.
//
// ⚠ "줄 자체가 0" 과 "줄은 있는데 후보 0" 은 원인이 완전히 다르다.
// 전자는 기기/권한/태그 문제, 후자는 런타임이 안 찍는 문제다. 뭉뚱그리면
// 사용자가 엉뚱한 곳을 고치며 시간을 쓴다.
func diagnose(res ExploreResult, doDiff bool) []string {
	var d []string
	switch {
	case res.TotalLines == 0:
		return []string{
			"logcat 이 한 줄도 수집되지 않았다.",
			"→ 기기 연결 / adb 권한 / logcat 버퍼를 확인할 것.",
		}
	case res.ParsedLines == 0:
		return []string{
			"줄은 받았으나 logcat 형식으로 파싱되지 않았다.",
			"→ `-v` 형식이 예상과 다를 수 있다 (monotonic/epoch 를 기대한다).",
		}
	case len(res.Candidates) == 0:
		d = append(d,
			"로그는 수집됐으나 지표로 보이는 후보가 없다.",
			"가능한 원인:",
			"  · 런타임이 타이밍을 아예 안 찍는다",
			"  · 네이티브 엔진이 stderr 로 출력한다 → Android 는 stderr 를 logcat 으로 보내지 않으므로 잡을 수 없다",
			"  · 벤더가 태그를 막아뒀다 → `adb shell getprop | grep log.tag` 확인, `setprop log.tag.<TAG> VERBOSE` 로 해제 시도",
			"  · 추론이 실제로 실행되지 않았다 → 수집 구간에 프롬프트를 한 번 돌렸는지 확인",
		)
		return d
	}
	if res.WeakOnly {
		d = append(d,
			"⚠ LLM 고유 신호(tok/s · ms/tok · TTFT · prefill/decode)가 **한 건도 없다.**",
			"아래 후보는 낱말만 겹치는 다른 온디바이스 ML 일 수 있다 —",
			"음성 wakeword · 얼굴인식 · 사진 분류도 똑같이 '모델 로드'와 '추론'을 찍는다.",
			"→ 원문(samples)을 반드시 눈으로 확인할 것. 토큰 단위가 안 보이면 LLM 이 아닐 가능성이 높다.",
			"→ 런타임이 타이밍을 아예 안 찍는 경우일 수도 있다 (네이티브 엔진은 stderr 로 나가 logcat 에 안 닿는다).")
	}
	if !doDiff {
		d = append(d,
			"유휴 구간이 지정되지 않아 차분을 하지 못했다. 배경 소음이 후보에 섞여 있다.",
			"→ 추론 전 유휴 구간을 함께 수집하면 '추론 때만 나타나는 태그'를 가려낼 수 있다.")
	}
	return d
}
