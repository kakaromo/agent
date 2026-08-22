package ai

import "strings"

// 스트리밍 중 용어 정규화.
//
// NormalizeTerms 는 완성된 텍스트에만 쓸 수 있다 — 토큰은 단어 중간에서 잘려 오므로
// ("요" + "청의") 토큰마다 적용하면 매칭이 안 된다. 그렇다고 답변이 다 끝난 뒤에
// 한꺼번에 고치면 스트리밍의 의미가 없다(사용자가 번역어를 봤다가 갑자기 바뀐다).
//
// 그래서 **문장 단위로 버퍼링**한다: 문장이 끝나는 지점에서 그 문장만 정규화해 내보내고,
// 나머지는 버퍼에 남긴다. 사용자에게는 문장 단위로 흘러가는 것처럼 보인다.

// 문장 종결로 볼 문자. 한국어 서술문은 대부분 "다." 로 끝나지만, 목록/줄바꿈도 경계로 본다.
const sentenceEnders = ".!?\n"

// TermStreamer — 토큰을 받아 문장 단위로 정규화해 내보낸다.
//
// 사용법:
//
//	ts := NewTermStreamer(func(s string) { emit(s) })
//	... 토큰마다 ts.Write(tok) ...
//	ts.Flush()   // 반드시 호출 (마지막 문장이 종결부호 없이 끝날 수 있다)
type TermStreamer struct {
	buf   strings.Builder
	emit  func(string)
	limit int
}

// NewTermStreamer — emit 은 정규화된 조각을 받는다.
func NewTermStreamer(emit func(string)) *TermStreamer {
	return &TermStreamer{emit: emit, limit: 400}
}

// Write — 토큰 하나를 넣는다. 문장이 완성되면 정규화해 emit 한다.
func (t *TermStreamer) Write(token string) {
	if token == "" {
		return
	}
	t.buf.WriteString(token)

	// 종결 부호가 포함된 토큰이면 거기까지 잘라 내보낸다.
	if strings.ContainsAny(token, sentenceEnders) {
		t.flushUpToLastEnder()
		return
	}
	// 종결부호 없이 너무 길어지면(목록·표 등) 그대로 흘려보낸다 — 무한 버퍼 방지.
	if t.buf.Len() >= t.limit {
		t.emitAll()
	}
}

// flushUpToLastEnder — 버퍼에서 마지막 종결부호까지만 정규화해 내보내고 나머지는 남긴다.
func (t *TermStreamer) flushUpToLastEnder() {
	s := t.buf.String()
	idx := strings.LastIndexAny(s, sentenceEnders)
	if idx < 0 {
		return
	}
	head, tail := s[:idx+1], s[idx+1:]
	t.buf.Reset()
	t.buf.WriteString(tail)
	if head != "" {
		t.emit(NormalizeTerms(head))
	}
}

func (t *TermStreamer) emitAll() {
	s := t.buf.String()
	t.buf.Reset()
	if s != "" {
		t.emit(NormalizeTerms(s))
	}
}

// Flush — 남은 버퍼를 내보낸다. 스트림 종료 시 반드시 호출한다.
func (t *TermStreamer) Flush() {
	t.emitAll()
}
