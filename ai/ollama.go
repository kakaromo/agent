// Package ai — 로컬 LLM(ollama) 기반 결과 해석.
//
// agent 는 ollama 를 프로세스로 안지 않고 순수 net/http 로만 호출한다. 따라서 CGO 불필요,
// Mac/Windows arm64·x64 어디서든 동일 바이너리로 동작한다. GPU/NPU 가속은 ollama 런타임의
// 몫이며 agent 는 관여하지 않는다 (Snapdragon X 는 현재 CPU 추론 → 3B 급 모델 권장).
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client — ollama HTTP 클라이언트. endpoint/model 은 config([ai])에서 주입.
type Client struct {
	Endpoint string // 예: http://127.0.0.1:11434
	Model    string // 예: qwen2.5:3b
	HTTP     *http.Client
}

// New 는 Client 를 만든다. httpClient 가 nil 이면 기본값을 쓴다.
// 스트리밍 응답은 토큰이 오는 대로 흘러나오므로 전체 타임아웃을 길게 잡되,
// context 로 취소를 전파한다 (SSE 클라이언트 disconnect → 요청 중단).
func New(endpoint, model string) *Client {
	return &Client{
		Endpoint: strings.TrimRight(endpoint, "/"),
		Model:    model,
		HTTP:     &http.Client{Timeout: 10 * time.Minute},
	}
}

// Reachable — ollama 가 응답하는지 가볍게 확인 (/api/tags GET).
// UI 가 AI 버튼 노출 여부를 결정하는 데 쓴다. 짧은 타임아웃으로 빠르게 판정.
func (c *Client) Reachable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint+"/api/tags", nil)
	if err != nil {
		return false
	}
	// c.HTTP 를 쓴다(DefaultClient 아님) — 다른 메서드와 동일한 클라이언트를 타야
	// 테스트에서 스텁으로 교체할 때 여기만 실제 네트워크를 치지 않는다.
	// 전체 타임아웃은 길지만 위 ctx 가 2초로 제한한다.
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// chatMessage — ollama /api/chat 메시지.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest — /api/chat 요청 body.
//
// Format 은 ollama structured output 용 필드다. JSON schema 객체를 넣으면 모델 응답이
// 그 schema 에 맞는 JSON 으로 강제된다 (문자열 "json" 만 넣으면 free-form JSON). nil 이면
// 필드 자체가 생략돼 기존 free-form 동작. Options 는 temperature 등 추론 파라미터.
type chatRequest struct {
	Model    string          `json:"model"`
	Messages []chatMessage   `json:"messages"`
	Stream   bool            `json:"stream"`
	Format   json.RawMessage `json:"format,omitempty"`
	Options  map[string]any  `json:"options,omitempty"`
}

// chatResponse — stream=false 시 단일 응답 body.
type chatResponse struct {
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
	Error   string      `json:"error"`
}

// chatStreamChunk — stream=true 시 NDJSON 한 줄. done=true 인 마지막 줄엔 message 가 비어있을 수 있다.
type chatStreamChunk struct {
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
	Error   string      `json:"error"`
}

// Message — 대화 한 턴. Role 은 "system" | "user" | "assistant".
//
// 멀티턴 채팅(ChatMessages)에서 히스토리를 넘기기 위한 공개 타입이다. 내부 wire 타입
// (chatMessage)과 필드가 같지만, 패키지 외부가 ollama 의 JSON shape 에 직접 의존하지
// 않도록 분리해 둔다.
type Message struct {
	Role    string
	Content string
}

// Chat 은 system+user 프롬프트로 ollama 에 스트리밍 요청을 보내고, 토큰이 오는 대로 onToken 을 호출한다.
//
// 단일 턴 편의 래퍼다. 대화 히스토리가 필요하면 ChatMessages 를 쓴다.
func (c *Client) Chat(ctx context.Context, system, user string, onToken func(string)) error {
	return c.ChatMessages(ctx, []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, onToken)
}

// ChatMessages 는 대화 히스토리를 그대로 ollama 에 넘겨 스트리밍 응답을 받는다.
//
//   - msgs 는 호출자가 구성한 순서대로 전달된다(보통 system 1개 + user/assistant 교대).
//     컨텍스트 상한 관리(오래된 턴 잘라내기)는 호출자 책임이다 — 여기서는 자르지 않는다.
//   - ctx 취소 시 요청이 중단된다 (SSE disconnect 전파).
//   - ollama 가 모델을 못 찾으면(pull 안 됨) 응답 body 의 error 필드 또는 non-200 status 로 온다 → 에러 반환.
//   - onToken 은 부분 토큰(단어 조각)을 받을 수 있다. 호출자가 이어붙인다.
func (c *Client) ChatMessages(ctx context.Context, msgs []Message, onToken func(string)) error {
	if len(msgs) == 0 {
		return fmt.Errorf("빈 메시지 목록")
	}
	wire := make([]chatMessage, 0, len(msgs))
	for _, m := range msgs {
		wire = append(wire, chatMessage{Role: m.Role, Content: m.Content})
	}
	body, err := json.Marshal(chatRequest{
		Model:    c.Model,
		Messages: wire,
		Stream:   true,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("ollama 에 연결할 수 없습니다 (%s): %w", c.Endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 모델 미존재(404) 등 — body 에 ollama 의 안내 메시지가 담긴다.
		msg := readErrorBody(resp)
		return fmt.Errorf("ollama 오류 (status %d): %s", resp.StatusCode, msg)
	}

	// NDJSON: 한 줄에 chunk 하나. 라인이 길 수 있어 buffer 를 키운다.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var chunk chatStreamChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			// 파싱 실패 라인은 건너뛴다 (부분 flush 등).
			continue
		}
		if chunk.Error != "" {
			return fmt.Errorf("ollama 오류: %s", chunk.Error)
		}
		if chunk.Message.Content != "" && onToken != nil {
			onToken(chunk.Message.Content)
		}
		if chunk.Done {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		// ctx 취소로 인한 중단은 정상 종료로 취급.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("스트림 읽기 실패: %w", err)
	}
	return nil
}

// ChatJSON 은 system+user 프롬프트로 ollama 에 non-stream 요청을 보내고, JSON schema 로
// 응답 형식을 강제한 뒤 message.content(JSON 문자열)를 그대로 반환한다.
//
//   - schema 가 nil 이 아니면 /api/chat 의 format 에 JSON schema 를 넣어 structured output 을
//     강제한다. schema 는 map[string]any 등 json.Marshal 가능한 값이어야 한다.
//   - stream=false 이므로 응답은 단일 {message:{content:"...json..."}} body 다.
//   - temperature 0 으로 고정 — schema 준수 JSON 생성은 결정적일수록 안정적이다.
//   - ctx 취소 시 요청 중단. 모델 미존재/오류는 non-200 또는 body 의 error 필드로 온다.
func (c *Client) ChatJSON(ctx context.Context, system, user string, schema any) (string, error) {
	var format json.RawMessage
	if schema != nil {
		b, err := json.Marshal(schema)
		if err != nil {
			return "", fmt.Errorf("marshal schema: %w", err)
		}
		format = b
	}

	body, err := json.Marshal(chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream:  false,
		Format:  format,
		Options: map[string]any{"temperature": 0},
	})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama 에 연결할 수 없습니다 (%s): %w", c.Endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg := readErrorBody(resp)
		return "", fmt.Errorf("ollama 오류 (status %d): %s", resp.StatusCode, msg)
	}

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("응답 파싱 실패: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("ollama 오류: %s", out.Error)
	}
	return out.Message.Content, nil
}

// readErrorBody — non-200 응답에서 ollama 의 {"error":"..."} 를 뽑아낸다. 실패 시 raw 일부 반환.
func readErrorBody(resp *http.Response) string {
	var body struct {
		Error string `json:"error"`
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&body); err == nil && body.Error != "" {
		return body.Error
	}
	return resp.Status
}
