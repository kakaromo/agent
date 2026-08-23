package server

import (
	"encoding/json"
	"strings"
	"testing"

	"agent/ai"
	"agent/trace"
)

// 채팅 히스토리 조립/절삭 테스트.
//
// 여기가 틀리면 조용히 망가진다 — 컨텍스트가 넘쳐 앞 대화를 잃거나, 반대로 직전 집계
// 결과가 빠져 "그 184초 근처" 같은 후속 질문이 무엇을 가리키는지 모르게 된다.

func msg(role, content string) aiChatMessage {
	return aiChatMessage{Role: role, Content: content}
}

func msgWithAgg(content, tool, aggJSON string) aiChatMessage {
	return aiChatMessage{Role: "assistant", Content: content, Tool: tool, AggJSON: json.RawMessage(aggJSON)}
}

func TestLastUserQuestion(t *testing.T) {
	h := []aiChatMessage{
		msg("user", "첫 질문"),
		msg("assistant", "첫 답변"),
		msg("user", "  두번째 질문  "),
	}
	if got := lastUserQuestion(h); got != "두번째 질문" {
		t.Errorf("마지막 질문 추출 실패(공백 trim 포함): %q", got)
	}
	if got := lastUserQuestion(nil); got != "" {
		t.Errorf("빈 히스토리는 빈 문자열이어야 한다: %q", got)
	}
	if got := lastUserQuestion([]aiChatMessage{msg("assistant", "답")}); got != "" {
		t.Errorf("user 없으면 빈 문자열: %q", got)
	}
}

func TestTrimTrailingUser(t *testing.T) {
	h := []aiChatMessage{
		msg("user", "q1"), msg("assistant", "a1"), msg("user", "q2"),
	}
	got := trimTrailingUser(h)
	if len(got) != 2 {
		t.Fatalf("마지막 질문이 히스토리에 남아 중복된다: %d개", len(got))
	}
	if got[len(got)-1].Content != "a1" {
		t.Errorf("잘못 잘렸다: %+v", got)
	}
}

func TestTailTurns(t *testing.T) {
	var h []aiChatMessage
	for i := 0; i < 20; i++ {
		h = append(h, msg("user", "q"), msg("assistant", "a"))
	}
	got := tailTurns(h, 6)
	if len(got) != 12 {
		t.Errorf("최근 6턴(12메시지)이어야 한다: %d", len(got))
	}
	// 짧으면 그대로.
	short := []aiChatMessage{msg("user", "q"), msg("assistant", "a")}
	if len(tailTurns(short, 6)) != 2 {
		t.Error("상한보다 짧으면 그대로 둬야 한다")
	}
}

// 집계 결과 원문은 최근 N개만 유지된다.
func TestAggKeepFromIndex(t *testing.T) {
	h := []aiChatMessage{
		msgWithAgg("a1", "tail_latency", `{"x":1}`), // 오래됨 — 요약으로 대체
		msg("user", "q2"),
		msgWithAgg("a2", "cmd_breakdown", `{"x":2}`), // 유지
		msg("user", "q3"),
		msgWithAgg("a3", "filtered_stats", `{"x":3}`), // 유지
	}
	from := aggKeepFromIndex(h, 2)
	if from != 2 {
		t.Fatalf("최근 2개 집계 유지 시작 인덱스가 2여야 한다: %d", from)
	}
	if from > 0 && len(h[0].AggJSON) == 0 {
		t.Error("테스트 데이터 오류")
	}
}

// 핵심 회귀 테스트: 직전 턴의 집계 결과가 프롬프트에 원문으로 들어가야 한다.
// 이게 빠지면 "그 184초 근처" 같은 후속 질문이 무엇을 가리키는지 모델이 알 수 없다.
func TestBuildChatMessagesKeepsRecentAggData(t *testing.T) {
	history := []aiChatMessage{
		msg("user", "제일 느린 IO 10개"),
		msgWithAgg("가장 느린 요청은 184초에 몰려 있습니다.", "tail_latency", `{"events":[{"time":184.221,"dtoc":312.66}]}`),
		msg("user", "그 184초 근처에 무슨 일이 있었어?"),
	}
	agg := &trace.AggResult{
		Tool: "filtered_stats",
		Data: map[string]any{"scoped": map[string]any{"events": 14882}},
	}

	msgs := buildChatMessages("trace", `{"totalEvents":467230}`, history, "그 184초 근처에 무슨 일이 있었어?", agg)

	joined := ""
	for _, m := range msgs {
		joined += m.Role + ":" + m.Content + "\n"
	}

	if !strings.Contains(joined, "184.221") {
		t.Error("직전 턴의 집계 결과 원문이 빠졌다 — 후속 질문이 맥락을 잃는다")
	}
	if !strings.Contains(joined, "tail_latency") {
		t.Error("이전 근거 집계 이름이 빠졌다")
	}
	if !strings.Contains(joined, "14882") {
		t.Error("이번 턴 집계 결과가 안 들어갔다")
	}
	if !strings.Contains(joined, "467230") {
		t.Error("배경 summary 컨텍스트가 빠졌다")
	}
	if msgs[0].Role != "system" {
		t.Errorf("첫 메시지는 system 이어야 한다: %s", msgs[0].Role)
	}
	if last := msgs[len(msgs)-1]; last.Role != "user" || !strings.Contains(last.Content, "184초") {
		t.Errorf("마지막 메시지는 이번 질문이어야 한다: %+v", last)
	}
	// 이번 질문이 히스토리와 마지막에 두 번 들어가면 안 된다.
	if strings.Count(joined, "그 184초 근처에 무슨 일이 있었어?") != 1 {
		t.Error("이번 질문이 중복 삽입됐다")
	}
}

// none 이 선택되면 집계 JSON 없이 질문만 넘어가고, 모델이 답할 수 없는 이유를 설명한다.
func TestAggForPromptNone(t *testing.T) {
	label, js := aggForPrompt(&trace.AggResult{Tool: trace.AggNone})
	if label != "" || js != "" {
		t.Errorf("none 은 집계 주입이 없어야 한다: %q %q", label, js)
	}
	if l, j := aggForPrompt(nil); l != "" || j != "" {
		t.Errorf("nil 도 마찬가지: %q %q", l, j)
	}
}

// 집계 실행 실패 시 사유를 모델에 넘겨 설명하게 한다(조용히 무시하지 않는다).
func TestAggForPromptNote(t *testing.T) {
	_, js := aggForPrompt(&trace.AggResult{
		Tool: "tail_latency",
		Note: "이 job 의 parquet 결과 파일을 찾을 수 없습니다",
	})
	if !strings.Contains(js, "parquet") {
		t.Errorf("실패 사유가 전달되지 않았다: %q", js)
	}
}

// 문자 수 상한 초과 시 오래된 대화부터 버리되, system/배경/이번 질문은 유지한다.
func TestCapCharsKeepsHeadAndTail(t *testing.T) {
	big := strings.Repeat("가", 5000)
	msgs := []ai.Message{
		{Role: "system", Content: "SYSTEM"},
		{Role: "user", Content: "CONTEXT"},
		{Role: "assistant", Content: "OK"},
		{Role: "user", Content: big},
		{Role: "assistant", Content: big},
		{Role: "user", Content: big},
		{Role: "assistant", Content: big},
		{Role: "user", Content: "LAST_QUESTION"},
	}
	got := capChars(msgs, 6000)

	if got[0].Content != "SYSTEM" {
		t.Error("system 이 잘렸다")
	}
	if got[1].Content != "CONTEXT" {
		t.Error("배경 컨텍스트가 잘렸다")
	}
	if got[len(got)-1].Content != "LAST_QUESTION" {
		t.Error("이번 질문이 잘렸다 — 절대 잘리면 안 된다")
	}
	total := 0
	for _, m := range got {
		total += len(m.Content)
	}
	if len(got) >= len(msgs) {
		t.Errorf("아무것도 안 잘렸다: %d → %d", len(msgs), len(got))
	}
	t.Logf("메시지 %d → %d, 문자 %d", len(msgs), len(got), total)
}

// 상한 이하면 그대로 통과.
func TestCapCharsNoop(t *testing.T) {
	msgs := []ai.Message{
		{Role: "system", Content: "s"},
		{Role: "user", Content: "c"},
		{Role: "assistant", Content: "o"},
		{Role: "user", Content: "q"},
	}
	if got := capChars(msgs, 10000); len(got) != len(msgs) {
		t.Errorf("상한 이하인데 잘렸다: %d", len(got))
	}
}

// benchmark job 은 trace 도메인 지식이 아니라 metrics 설명을 쓴다.
func TestChatSystemPromptByJobType(t *testing.T) {
	tr := ai.ChatSystemPrompt("trace")
	bm := ai.ChatSystemPrompt("benchmark")

	if !strings.Contains(tr, "dtoc") {
		t.Error("trace 프롬프트에 도메인 용어가 없다")
	}
	if !strings.Contains(bm, "read_iops") {
		t.Error("benchmark 프롬프트에 metrics 설명이 없다")
	}
	if strings.Contains(bm, "## I/O 패턴 특징 해석") {
		t.Error("benchmark 에 trace 전용 블록이 섞였다")
	}
	// 대화형은 단발 리포트의 ①~④ 형식을 강제하지 않아야 한다.
	for _, p := range []string{tr, bm} {
		if strings.Contains(p, "④ 다음 확인/개선 제안") {
			t.Error("대화형 프롬프트에 단발 리포트 형식이 남아 있다")
		}
	}
}

// overview 는 "배경 summary 로 답하라"는 뜻이지 "답할 수 없다"가 아니다.
// 이 구분을 놓치면 "전반적으로 해석해줘" 에 답을 거부한다(UI 실측으로 발견).
func TestOverviewUsesSummaryNotRefusal(t *testing.T) {
	agg := &trace.AggResult{Tool: trace.AggOverview}
	msgs := buildChatMessages("trace", `{"totalEvents":4520,"readTotalBytes":19400000}`,
		[]aiChatMessage{msg("user", "이 결과를 전반적으로 해석해줘.")},
		"이 결과를 전반적으로 해석해줘.", agg)

	last := msgs[len(msgs)-1].Content
	if strings.Contains(last, "답할 수 없습니다") {
		t.Error("overview 인데 거절 프롬프트가 붙었다 — 배경 summary 로 답해야 한다")
	}
	if !strings.Contains(last, "전체 집계 통계") {
		t.Errorf("배경 summary 로 답하라는 지시가 없다: %q", last)
	}
	// 배경 summary 자체는 앞쪽에 깔려 있어야 한다.
	joined := ""
	for _, m := range msgs {
		joined += m.Content
	}
	if !strings.Contains(joined, "4520") {
		t.Error("배경 summary 가 프롬프트에 없다")
	}
}

// none 은 반대로 거절 지시가 반드시 붙어야 한다.
func TestNoneGetsRefusalPrompt(t *testing.T) {
	agg := &trace.AggResult{Tool: trace.AggNone}
	msgs := buildChatMessages("trace", `{"totalEvents":4520}`,
		[]aiChatMessage{msg("user", "지난주 잡보다 나쁜가?")}, "지난주 잡보다 나쁜가?", agg)

	last := msgs[len(msgs)-1].Content
	if !strings.Contains(last, "답할 수 없습니다") {
		t.Errorf("none 인데 거절 지시가 없다 — 추측 답변이 나온다: %q", last)
	}
}

// benchmark job 은 집계 선택 자체를 안 하므로 agg=nil 로 들어온다 → summary 경로.
func TestBenchmarkNilAggUsesSummary(t *testing.T) {
	msgs := buildChatMessages("benchmark", `{"devices":[{"metrics":{"read_iops":417000}}]}`,
		[]aiChatMessage{msg("user", "성능 어때?")}, "성능 어때?", nil)

	last := msgs[len(msgs)-1].Content
	if strings.Contains(last, "답할 수 없습니다") {
		t.Error("benchmark job 이 거절 경로를 탔다")
	}
}

// ── AI 생성 시나리오의 loop 인덱스 remap ──
//
// 검증 실패한 step 은 버려지는데 loops 의 인덱스는 모델의 **원본 번호**다. 옮기지
// 않으면 loop 가 엉뚱한 구간을 감싸는데도 경고 없이 통과한다(길이 검사가 필터링 후
// 기준이라 어긋난 걸 못 잡는다).
func TestScenarioLoopIndexRemap(t *testing.T) {
	content := `{
	 "steps":[
	   {"type":"bogus_type","params":{}},
	   {"type":"sleep","params":{"seconds":"1"}},
	   {"type":"sleep","params":{"seconds":"2"}},
	   {"type":"sleep","params":{"seconds":"3"}}
	 ],
	 "loops":[{"startStep":"1","endStep":"3","count":"2"}]
	}`
	steps, loops, _, err := parseAndValidateScenario(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 {
		t.Fatalf("step %d개 생존 (want 3)", len(steps))
	}
	if len(loops) != 1 {
		t.Fatalf("loop 이 사라졌다: %d", len(loops))
	}
	// step0 이 빠졌으므로 원본 1..3 은 0..2 가 되어야 한다.
	if loops[0].StartStep != "0" || loops[0].EndStep != "2" {
		t.Errorf("remap 실패: start=%s end=%s (want 0..2)", loops[0].StartStep, loops[0].EndStep)
	}
}

// loop 가 감싸던 step 이 전부 사라지면 loop 도 버리고 사유를 남긴다.
func TestScenarioLoopDroppedWhenMemberGone(t *testing.T) {
	content := `{
	 "steps":[
	   {"type":"sleep","params":{"seconds":"1"}},
	   {"type":"bogus","params":{}}
	 ],
	 "loops":[{"startStep":"1","endStep":"1","count":"2"}]
	}`
	_, loops, warns, err := parseAndValidateScenario(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(loops) != 0 {
		t.Errorf("사라진 step 을 감싸던 loop 가 남았다: %+v", loops)
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "범위를 확정할 수 없습니다") {
			found = true
		}
	}
	if !found {
		t.Errorf("제외 사유 경고가 없다: %v", warns)
	}
}
