// helpers_test.go P2-d ai/flow 测试基座：脚本化 httptest 假 OpenAI 服务
//（镜像 _test/ai_agent_test 的 scripted 模式，模块边界内精简复刻）。
package ai_flow_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	_ "github.com/crazy-airhead/aifei-go/ai/dialect/openai" // 注册默认方言
)

// scriptedResp 第 i 次请求的回放内容。
type scriptedResp struct {
	status int    // 0 = 200
	body   string // JSON 整体或 SSE 帧串（sse=true）
	sse    bool
}

func (r scriptedResp) statusCode() int {
	if r.status == 0 {
		return 200
	}
	return r.status
}

// scripted 逐请求脚本化假服务。
type scripted struct {
	mu       sync.Mutex
	requests []map[string]any
	srv      *httptest.Server
}

// newScripted 第 i 次请求回放 script[i]（循环最后一项）。
func newScripted(t *testing.T, script ...scriptedResp) *scripted {
	t.Helper()
	s := &scripted{}
	var idx int
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		i := idx
		idx++
		s.requests = append(s.requests, body)
		s.mu.Unlock()

		if i >= len(script) {
			i = len(script) - 1
		}
		resp := script[i]
		if resp.sse {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			for _, line := range splitFrames(resp.body) {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", line)
				if fl != nil {
					fl.Flush()
				}
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.statusCode())
		_, _ = io.WriteString(w, resp.body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// splitFrames 按空行分组的帧串 → 单帧列表。
func splitFrames(body string) []string {
	var frames []string
	cur := ""
	for _, line := range splitLines(body) {
		if line == "" {
			if cur != "" {
				frames = append(frames, cur)
				cur = ""
			}
			continue
		}
		if cur != "" {
			cur += "\n"
		}
		cur += line
	}
	if cur != "" {
		frames = append(frames, cur)
	}
	return frames
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func (s *scripted) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *scripted) request(t *testing.T, i int) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.requests) {
		t.Fatalf("request %d 未捕获（共 %d 次）", i, len(s.requests))
	}
	return s.requests[i]
}

// chatJSON 非流式 chat/completions 响应体。
func chatJSON(content string) string {
	return fmt.Sprintf(`{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`, content)
}

// chatSSE 流式帧串（两段文本 + 收尾 usage）。按 rune 边界对半切分。
func chatSSE(text string) string {
	r := []rune(text)
	f1, f2 := string(r[:len(r)/2]), string(r[len(r)/2:])
	return fmt.Sprintf(`{"id":"c1","object":"chat.completion.chunk","choices":[{"delta":{"role":"assistant","content":%q}}]}

{"id":"c1","object":"chat.completion.chunk","choices":[{"delta":{"content":%q}}]}

{"id":"c1","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}

[DONE]`, f1, f2)
}
