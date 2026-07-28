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
	resp, err := http.DefaultClient.Do(req)
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
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

// chatStreamChunk — stream=true 시 NDJSON 한 줄. done=true 인 마지막 줄엔 message 가 비어있을 수 있다.
type chatStreamChunk struct {
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
	Error   string      `json:"error"`
}

// Chat 은 system+user 프롬프트로 ollama 에 스트리밍 요청을 보내고, 토큰이 오는 대로 onToken 을 호출한다.
//
//   - ctx 취소 시 요청이 중단된다 (SSE disconnect 전파).
//   - ollama 가 모델을 못 찾으면(pull 안 됨) 응답 body 의 error 필드 또는 non-200 status 로 온다 → 에러 반환.
//   - onToken 은 부분 토큰(단어 조각)을 받을 수 있다. 호출자가 이어붙인다.
func (c *Client) Chat(ctx context.Context, system, user string, onToken func(string)) error {
	body, err := json.Marshal(chatRequest{
		Model: c.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Stream: true,
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
