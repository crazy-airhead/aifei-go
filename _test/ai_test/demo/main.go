// demo P1-d 验收：端到端 aisdk 演示——httptest 假 OpenAI（两轮流式：思考+工具调用
// → 工具结果 → 正文），ai.ChatModel autoToolCall 消费，aisdk.WriteStream 把
// ChatEvent 流转成 Vercel AI SDK UI Message Stream SSE 并打印。
//
// 运行：go run ./demo
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"

	ai "github.com/crazy-airhead/aifei-go/ai"
	_ "github.com/crazy-airhead/aifei-go/ai/dialect/openai" // 注册 openai 方言
	"github.com/crazy-airhead/aifei-go/ai/ui/aisdk"
)

// 两轮流式脚本：第 1 轮思考+工具调用，第 2 轮拿到工具结果后作答。
var rounds = [][]string{
	{
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"需要先查天气"}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_w","type":"function","function":{"name":"weather","arguments":"{\"city\":\"北京\"}"}}]}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	},
	{
		`{"id":"c2","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"北京今天晴，"}}]}`,
		`{"id":"c2","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"25 度。"}}]}`,
		`{"id":"c2","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"c2","model":"gpt-4o","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":6,"total_tokens":15}}`,
	},
}

// fakeOpenAI 顺序回放各轮流式帧。
type fakeOpenAI struct {
	mu   sync.Mutex
	call int
}

func (f *fakeOpenAI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	i := f.call
	f.call++
	f.mu.Unlock()
	if i >= len(rounds) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	for _, frame := range rounds[i] {
		fmt.Fprintf(w, "data: %s\n\n", frame)
		fl.Flush()
	}
}

// demoIDs 演示用确定性 id（每前缀独立计数），输出可复现。
func demoIDs() aisdk.IDGenerator {
	counters := map[string]int{}
	return func(prefix string) string {
		counters[prefix]++
		return fmt.Sprintf("%s%d", prefix, counters[prefix])
	}
}

func main() {
	// 1. 假模型服务
	fake := &fakeOpenAI{}
	modelSrv := httptest.NewServer(fake)
	defer modelSrv.Close()

	m, err := ai.NewChatModel(ai.Config{APIURL: modelSrv.URL, Model: "gpt-4o"})
	if err != nil {
		fmt.Fprintln(os.Stderr, "NewChatModel:", err)
		os.Exit(1)
	}
	weather := ai.NewTool("weather").
		Desc("查询城市天气").
		StringParam("city", "城市名", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return fmt.Sprintf("%s：晴", args["city"]), nil
		}).
		Build()

	// 2. aisdk SSE 端点（useChat 可直接消费的 UI Message Stream）
	auto := true
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		req := m.Prompt("北京今天天气怎么样？").Options(func(o *ai.ChatOptions) {
			o.AutoToolCall = &auto
			o.Tools = []ai.FunctionTool{weather}
		})
		// 演示用确定性 id；生产用 aisdk.WriteStream（随机 id）
		if err := aisdk.NewWrapper(demoIDs()).WriteStream(r.Context(), w, req, map[string]any{"sessionId": "demo"}); err != nil {
			fmt.Fprintln(os.Stderr, "WriteStream:", err) // 协议帧已收口，仅记录
		}
	}))
	defer upstream.Close()

	// 3. 消费 SSE 流并打印
	resp, err := http.Get(upstream.URL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "GET:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	fmt.Println("GET", upstream.URL, "->", resp.Header.Get("Content-Type"))
	body, _ := io.ReadAll(resp.Body)
	fmt.Println(string(body))

	// 4. 基本断言：start 起、含工具输入/输出、finish 收、[DONE] 止
	out := string(body)
	for _, want := range []string{
		`{"type":"start","messageId":"msg_1"}`,
		`{"type":"start-step"}`,
		`{"type":"tool-input-available","toolCallId":"call_w","toolName":"weather","input":{"city":"北京"}}`,
		`{"type":"tool-output-available","toolCallId":"call_w","output":"北京：晴"}`,
		`{"type":"text-delta","id":"txt_1","delta":"25 度。"}`,
		`{"type":"finish","finishReason":"stop","usage":{"promptTokens":9,"completionTokens":6,"totalTokens":15}}`,
		"data: [DONE]",
	} {
		if !strings.Contains(out, want) {
			fmt.Fprintln(os.Stderr, "输出缺少期望帧：", want)
			os.Exit(1)
		}
	}
	fmt.Println("== aisdk 端到端 demo 通过 ==")
}
