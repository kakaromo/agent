package ai

import (
	"strings"
	"testing"
)

// 실제 14b 출력에서 나온 문장들 — 이게 통과해야 실사용에서 효과가 있다.
func TestNormalizeTermsRealOutput(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"가장 느린 5개의 request는 모두 WRITE(10) command이며, dtoc latency가 약 2.6ms입니다. 이들 요청의 평균 크기는 매우 작아 2KB 미만이고, QD는 3에서 7 사이로 변동됩니다.",
			"이들 request 의 평균 크기는",
		},
		{
			"평균 QD는 4.865로 중간 수준의 병렬성을 보입니다.",
			"중간 수준의 parallelism 을 보입니다",
		},
		{
			"p99 dtoc 도 2.177ms 로 지연이 상대적으로 긴 편입니다.",
			"latency 이 상대적으로",
		},
		{
			"p99 latency는 2.5242ms 로 가장 느린 요청들이 두드러집니다.",
			"가장 느린 request 들이",
		},
		{
			"이들 요청의 큐 깊이는 다양하지만 대부분 중간 수준입니다.",
			"request 의 QD 는 다양하지만",
		},
		{
			"모두 WRITE(10) 명령어이며, 지연 시간이 2.6ms 입니다.",
			"command 이며, latency 이 2.6ms",
		},
	}
	for _, c := range cases {
		got := NormalizeTerms(c.in)
		if !strings.Contains(got, c.want) {
			t.Errorf("입력: %q\n결과: %q\n기대 포함: %q", c.in, got, c.want)
		}
	}
}

// 영어 병기는 영어만 남긴다.
func TestNormalizeTermsRemovesGloss(t *testing.T) {
	cases := []struct{ in, want string }{
		{"병렬성(parallelism)은 어느 정도 있습니다.", "parallelism"},
		{"큐 깊이(QD)가 낮습니다.", "QD"},
		{"latency(지연 시간)가 깁니다.", "latency"},
		{"QD(큐 깊이)는 3입니다.", "QD"},
	}
	for _, c := range cases {
		got := NormalizeTerms(c.in)
		if !strings.Contains(got, c.want) {
			t.Errorf("%q → %q, %q 를 포함해야 함", c.in, got, c.want)
		}
		// 괄호 안 한국어가 남으면 안 된다.
		if strings.Contains(got, "(지연") || strings.Contains(got, "(큐 깊이") {
			t.Errorf("병기가 안 지워졌다: %q → %q", c.in, got)
		}
	}
}

// ── 여기가 가장 중요하다: 바꾸면 안 되는 것 ──
//
// 오역이 나느니 안 바꾸는 게 낫다. 중의적 단어와 합성어는 손대지 않아야 한다.
func TestNormalizeTermsDoesNotOverreach(t *testing.T) {
	unchanged := []string{
		// 중의적이라 치환표에서 뺀 단어들
		"평균 크기는 4KB 입니다.",
		"이 작업은 카메라 시나리오입니다.",
		"쓰기 속도가 빠릅니다.",
		"읽기 어려운 결과입니다.",
		// 합성어 — 부분 매칭되면 안 된다
		"요청서를 작성했습니다.",
		"지연되었습니다.",
		"명령어집합 구조입니다.",
		// 영어가 이미 쓰인 문장은 그대로
		"이 workload 는 random access 위주입니다.",
		"QD 가 낮고 latency 가 깁니다.",
	}
	for _, s := range unchanged {
		if got := NormalizeTerms(s); got != s {
			t.Errorf("건드리면 안 되는 문장이 바뀌었다:\n  입력: %q\n  결과: %q", s, got)
		}
	}
}

// 조사가 보존되어야 자연스럽다.
func TestNormalizeTermsPreservesJosa(t *testing.T) {
	cases := map[string]string{
		// 조사는 원문 그대로 보존한다(발음 기준 교정은 하지 않는다 — 프롬프트에서
		// 조사를 안 붙이도록 지시하는 것이 방침).
		"요청이 느립니다.":   "request 이 느립니다.",
		"요청을 보냈습니다.":  "request 을 보냈습니다.",
		"요청의 크기":      "request 의 크기",
		"요청들이 몰렸습니다.": "request 들이 몰렸습니다.",
	}
	for in, want := range cases {
		if got := NormalizeTerms(in); got != want {
			t.Errorf("조사 처리: %q → %q, want %q", in, got, want)
		}
	}
}

// 문장 맨 앞/맨 뒤도 처리되어야 한다.
func TestNormalizeTermsBoundaries(t *testing.T) {
	if got := NormalizeTerms("요청이 많습니다."); !strings.HasPrefix(got, "request") {
		t.Errorf("문장 앞: %q", got)
	}
	if got := NormalizeTerms("가장 느린 것은 요청"); !strings.HasSuffix(strings.TrimSpace(got), "request") {
		t.Errorf("문장 끝: %q", got)
	}
}

// 빈 문자열/영어만 있는 문자열에도 안전해야 한다.
func TestNormalizeTermsEdgeCases(t *testing.T) {
	for _, s := range []string{"", "   ", "latency", "QD=3", "1234"} {
		got := NormalizeTerms(s)
		if s == "" && got != "" {
			t.Errorf("빈 문자열이 바뀌었다: %q", got)
		}
	}
}

// 멱등성 — 두 번 적용해도 같아야 한다(스트리밍 재적용 대비).
func TestNormalizeTermsIdempotent(t *testing.T) {
	in := "이들 요청의 지연 시간은 큐 깊이(QD)에 따라 다릅니다."
	once := NormalizeTerms(in)
	twice := NormalizeTerms(once)
	if once != twice {
		t.Errorf("멱등하지 않다:\n  1회: %q\n  2회: %q", once, twice)
	}
}

// 서술격 조사("명령어이며")도 잡아야 한다.
func TestNormalizeTermsPredicateJosa(t *testing.T) {
	got := NormalizeTerms("모두 WRITE 명령어이며, 크기가 작습니다.")
	if !strings.Contains(got, "command") {
		t.Errorf("서술격 조사 앞 용어가 안 바뀌었다: %q", got)
	}
	if strings.Contains(got, "명령어") {
		t.Errorf("번역어가 남았다: %q", got)
	}
}

// 모델이 "큐 깊이" 를 "큐 QD" 로 반쪽만 번역하는 경우가 있다(실측).
func TestNormalizeTermsHalfTranslated(t *testing.T) {
	cases := map[string]string{
		"이들 request 의 큐 QD는 주로 3에서 7 사이입니다.": "QD 는",
		"대기열 QD가 낮습니다.":                      "QD 가",
	}
	for in, want := range cases {
		got := NormalizeTerms(in)
		if !strings.Contains(got, want) {
			t.Errorf("%q → %q, %q 포함해야 함", in, got, want)
		}
		if strings.Contains(got, "큐 QD") || strings.Contains(got, "대기열 QD") {
			t.Errorf("반쪽 번역이 남았다: %q", got)
		}
	}
}
