package ai

import "regexp"

// 기술 용어 후처리 — LLM 출력의 번역어를 영어 원문으로 되돌린다.
//
// 프롬프트에도 용어 지시가 있지만(ai/prompt.go 의 koreanOnly) 로컬 소형 모델은 긴
// 프롬프트 뒤에서 지시를 놓쳐 같은 답변 안에서도 번역어가 섞인다. 용어 통일은 규칙이
// 명확해 결정적으로 처리할 수 있으므로, 프롬프트로 대부분 잡고 남은 것을 여기서 확정한다.
//
// 설계 원칙:
//  1. **뜻이 하나로 고정되는 용어만** 치환한다. "크기"(size/일반명사),
//     "작업"(job/operation/일반), "속도"(speed/rate) 처럼 중의적인 것은 제외한다 —
//     오역이 나느니 안 바꾸는 편이 낫다.
//  2. 긴 표현을 먼저 치환한다("지연 시간"→latency 가 "지연"→latency 보다 앞).
//  3. 영어 병기("병렬성(parallelism)")는 괄호를 지우고 영어만 남긴다.
//  4. 조사는 원문 그대로 둔다. 영어 뒤 조사의 받침 규칙은 철자가 아니라 발음을 따라서
//     ("workload 는" vs "request 가") 코드로 맞추면 예외가 계속 늘어난다. 대신
//     프롬프트에서 영어 뒤에 조사를 붙이지 않도록 지시한다.
//
// 스트리밍 적용은 ai/stream_norm.go 참고(토큰이 단어 중간에서 잘리므로 문장 단위 버퍼링).

// termRule — 치환 규칙 하나.
type termRule struct {
	re   *regexp.Regexp
	repl string
	// termRule=true 면 buildRule 이 만든 "번역어+조사" 규칙(캡처 3개, 조사 교정 필요).
	// false 면 단순 정규식 치환(캡처 개수 자유).
	termRule bool
}

// 한국어 조사 — 치환 시 뒤에 붙어 있으면 살린다.
//
// 긴 것을 앞에 둬야 한다(정규식 교대는 왼쪽 우선). "들이" 가 "들" 보다 먼저 와야
// "요청들이" 가 "request 들 이" 로 쪼개지지 않는다.
// "이며/이고/이라" 같은 서술격 조사도 포함한다 — "명령어이며" 를 놓치면 안 된다.
const josaPattern = `(들에게|들의|들이|들을|들은|들도|이라면|이라는|이라고|이라|이며|이고|이나|으로|에서|에게|은|는|이|가|을|를|의|에|와|과|도|로|나|만|들)?`

// buildRule — "번역어 + (조사)" 를 "영어 + 공백 + 조사" 로 바꾸는 규칙.
//
// 앞뒤에 한글이 더 붙은 합성어는 건드리지 않는다(예: "요청서" 는 "request서" 가 되면 안 됨).
// 그래서 뒤쪽은 조사 또는 비한글이어야 한다.
func buildRule(korean, english string) termRule {
	// (?:^|[^가-힣]) 로 앞이 한글이 아님을 보장하되, 그 문자는 보존해야 하므로 캡처한다.
	re := regexp.MustCompile(`(^|[^가-힣])` + regexp.QuoteMeta(korean) + josaPattern + `(?:$|([^가-힣]))`)
	return termRule{re: re, repl: english, termRule: true}
}

// termRules — 적용 순서가 중요하다. 긴 표현이 먼저.
var termRules = []termRule{
	// ── 병기 형태 먼저 정리 ("병렬성(parallelism)" → "parallelism") ──
	// 한국어(영어) 순서, 영어(한국어) 순서 둘 다.
	{re: regexp.MustCompile(`[가-힣]+\s*\(\s*(latency|QD|parallelism|request|command|device|throughput|bandwidth|workload|queue|sequential|random)\s*\)`), repl: "$1"},
	{re: regexp.MustCompile(`\b(latency|QD|parallelism|request|command|device|throughput|bandwidth|workload|queue|sequential|random)\s*\(\s*[가-힣\s]+\s*\)`), repl: "$1"},

	// ── 모델이 반쪽만 번역한 형태 정리 ──
	// "큐 깊이" 를 "큐 QD" 로 쓰는 경우가 있다(앞말만 한글로 남음).
	{re: regexp.MustCompile(`(?:큐|대기열)\s*(QD)(은|는|이|가|을|를|의|도)`), repl: "$1 $2"},
	{re: regexp.MustCompile(`(?:큐|대기열)\s*(QD)`), repl: "$1"},

	// ── 긴 표현 우선 ──
	buildRule("지연 시간", "latency"),
	buildRule("응답 시간", "response time"),
	buildRule("큐 깊이", "QD"),
	buildRule("대기열 깊이", "QD"),
	buildRule("랜덤 접근", "random access"),
	buildRule("순차 접근", "sequential access"),
	buildRule("소량 쓰기", "small write"),
	buildRule("캐시 비우기", "cache flush"),
	buildRule("병렬 처리", "parallelism"),

	// ── 단일 단어 (이 도메인에서 뜻이 고정되는 것만) ──
	buildRule("요청", "request"),
	buildRule("명령어", "command"),
	buildRule("지연", "latency"),
	buildRule("병렬성", "parallelism"),
	buildRule("디바이스", "device"),
	buildRule("대역폭", "bandwidth"),
	buildRule("처리량", "throughput"),
	buildRule("순차성", "sequentiality"),

	// 의도적 제외: "크기"(size/일반명사), "작업"(job/operation/일반), "속도"(speed/rate),
	// "읽기"·"쓰기"(read/write 지만 "읽기 어렵다" 같은 일반 용법과 충돌), "랜덤"·"순차"
	// (단독으로는 "랜덤하게" 등 부사형이 흔해 조사 규칙으로 안전하게 못 잡는다).
}

// NormalizeTerms — LLM 답변의 번역어를 영어 용어로 통일한다.
//
// 스트리밍 도중에는 쓸 수 없다(토큰이 단어 중간에서 잘린다). 호출자가 **완성된 답변
// 전체**에 대해 한 번 적용해야 한다.
func NormalizeTerms(s string) string {
	for _, r := range termRules {
		s = r.re.ReplaceAllStringFunc(s, func(m string) string {
			sub := r.re.FindStringSubmatch(m)
			if sub == nil {
				return m
			}
			// 규칙 종류로 분기한다. 캡처 개수로 추측하면 규칙을 추가할 때
			// 조용히 깨진다(실제로 "큐 QD" 규칙 추가 시 panic 이 났다).
			if !r.termRule {
				return r.re.ReplaceAllString(m, r.repl)
			}
			// buildRule 형태: [전체, 앞문자, 조사, 뒷문자]
			//
			// 조사는 원문 그대로 보존한다. 영어 뒤 조사는 받침 규칙이 발음을 따라야 해서
			// ("workload 는" vs "request 가") 코드로 맞추려면 예외가 계속 늘어난다.
			// 프롬프트에서 조사를 아예 안 붙이도록 지시하고(사용자 방침), 여기서는
			// 이미 붙어 있는 것만 살린다.
			prefix, josa, suffix := sub[1], sub[2], sub[3]
			out := prefix + r.repl
			if josa != "" {
				out += " " + josa
			}
			return out + suffix
		})
	}
	// 치환으로 생긴 이중 공백만 정리한다.
	//
	// 끝 공백을 자르지 않는 것이 중요하다 — TermStreamer 가 이 함수를 **조각마다**
	// 부르므로, 조각 끝의 공백을 없애면 다음 조각과 단어가 붙어버린다("aaa tail" →
	// "aaatail"). 문서 전체를 한 번에 다듬어야 하는 호출자가 있다면 그쪽에서 자른다.
	return multiSpace.ReplaceAllString(s, " ")
}

var multiSpace = regexp.MustCompile(`[ \t]{2,}`)
