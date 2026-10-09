package ai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// ---- 假 SSE 服务：逐帧回放（flush 模拟 chunked），记录请求体 ----

type sseServer struct {
	mu       sync.Mutex
	requests []map[string]any
	srv      *httptest.Server
}

// newSSEResponder 每次请求回放同一组帧。
func newSSEResponder(t *testing.T, frames ...string) *sseServer {
	t.Helper()
	script := [][]string{frames}
	return newSSEScripted(t, 0, script)
}

// newSSEScripted 第 i 次请求回放第 i 组帧；delay 为帧间隔（0 = 不等待）。
func newSSEScripted(t *testing.T, delay time.Duration, script [][]string) *sseServer {
	t.Helper()
	s := &sseServer{}
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

		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, f := range script[i%len(script)] {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", f)
			if fl != nil {
				fl.Flush()
			}
			if delay > 0 {
				time.Sleep(delay)
			}
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sseServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *sseServer) request(t *testing.T, i int) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.requests) {
		t.Fatalf("request %d 未捕获（共 %d 次）", i, len(s.requests))
	}
	return s.requests[i]
}

func sseModel(t *testing.T, s *sseServer) *ai.ChatModel {
	t.Helper()
	m, err := ai.NewChatModel(ai.Config{APIURL: s.srv.URL, Model: "gpt-4o", APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("NewChatModel: %v", err)
	}
	return m
}

// 收集器 sink：记录事件 + 可选早停。
type sinkRecorder struct {
	mu     sync.Mutex
	events []*ai.ChatEvent
	inCall bool
	stopOn func(*ai.ChatEvent) bool
	err    error
}

func (r *sinkRecorder) OnEvent(ev *ai.ChatEvent) error {
	r.mu.Lock()
	if r.inCall {
		r.mu.Unlock()
		return errors.New("sink 回调并发（违反串行不变量）")
	}
	r.inCall = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.inCall = false; r.mu.Unlock() }()

	r.events = append(r.events, ev)
	if r.stopOn != nil && r.stopOn(ev) {
		return r.err
	}
	return nil
}

func (r *sinkRecorder) types() []ai.EventType {
	out := make([]ai.EventType, 0, len(r.events))
	for _, ev := range r.events {
		out = append(out, ev.Type)
	}
	return out
}

func (r *sinkRecorder) ofType(t ai.EventType) []*ai.ChatEvent {
	var out []*ai.ChatEvent
	for _, ev := range r.events {
		if ev.Type == t {
			out = append(out, ev)
		}
	}
	return out
}

// 标准帧脚本
var (
	sseTextFrames = []string{
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"你"}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"好"}}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"c1","model":"gpt-4o","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`[DONE]`,
	}

	sseToolCallFrames = []string{
		`{"id":"t1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"query-order","arguments":""}}]}}]}`,
		`{"id":"t1","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"or"}}]}}]}`,
		`{"id":"t1","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"derId\":\"A1\"}"}}]}}]}`,
		`{"id":"t1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"id":"t1","model":"gpt-4o","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
		`[DONE]`,
	}

	sseNowTimeFrames = []string{
		`{"id":"n1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"now-time","arguments":"{}"}}]}}]}`,
		`{"id":"n1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
)

func streamTool(t *testing.T, name string, ret any) ai.FunctionTool {
	t.Helper()
	return ai.NewTool(name).Desc(name).
		StringParam("orderId", "订单号", false).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return ret, nil
		}).Build()
}

// ---- 不变量 1：RESPONSE_START/END 各一次；失败时只有 ERROR，无 RESPONSE_END ----

func TestStreamInvariant1LifecycleExactlyOnce(t *testing.T) {
	s := newSSEResponder(t, sseTextFrames...)
	m := sseModel(t, s)

	rec := &sinkRecorder{}
	if err := m.Prompt("你好").Stream(context.Background(), rec); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	starts, ends, errs := rec.ofType(ai.EventResponseStart), rec.ofType(ai.EventResponseEnd), rec.ofType(ai.EventError)
	if len(starts) != 1 || starts[0] != rec.events[0] {
		t.Fatalf("RESPONSE_START 应恰一次且居首: %v", rec.types())
	}
	if len(ends) != 1 || ends[0] != rec.events[len(rec.events)-1] {
		t.Fatalf("RESPONSE_END 应恰一次且居末: %v", rec.types())
	}
	if len(errs) != 0 {
		t.Fatalf("正常完成不应有 ERROR: %v", rec.types())
	}
}

func TestStreamInvariant1FailureOnlyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	t.Cleanup(srv.Close)
	m, _ := ai.NewChatModel(ai.Config{APIURL: srv.URL, Model: "gpt-4o"})

	rec := &sinkRecorder{}
	err := m.Prompt("hi").Stream(context.Background(), rec)
	if err == nil {
		t.Fatal("HTTP 500 应返回错误")
	}
	if got := rec.ofType(ai.EventResponseEnd); len(got) != 0 {
		t.Fatalf("失败路径不应有 RESPONSE_END: %v", rec.types())
	}
	if got := rec.ofType(ai.EventError); len(got) != 1 {
		t.Fatalf("失败路径应恰一个 ERROR: %v", rec.types())
	}
	if ev := rec.ofType(ai.EventError)[0]; ev.Err == nil {
		t.Fatal("ERROR 事件应携带错误载荷")
	}
}

// ---- 不变量 2：STEP 边界随 autoToolCall 递归成对递增 ----

func TestStreamInvariant2StepPairsWithToolRecursion(t *testing.T) {
	s := newSSEScripted(t, 0, [][]string{sseToolCallFrames, sseTextFrames})
	m := sseModel(t, s)

	sess := ai.NewMemorySession("s1")
	rec := &sinkRecorder{}
	err := m.Prompt("查一下订单 A1").Session(sess).
		Options(func(o *ai.ChatOptions) {
			o.AutoToolCall = autoToolOn()
			o.Tools = []ai.FunctionTool{streamTool(t, "query-order", "A1 已发货")}
		}).
		Stream(context.Background(), rec)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// 事件序：TOOL_RESULT 属于第 0 步（在首个 STEP_END 之前）；子流 Step 递增
	var stepSeq []int
	var types []ai.EventType
	toolResultAt := -1
	for i, ev := range rec.events {
		types = append(types, ev.Type)
		switch ev.Type {
		case ai.EventStepStart, ai.EventStepEnd:
			stepSeq = append(stepSeq, ev.Step)
		case ai.EventToolResult:
			toolResultAt = i
		}
	}
	wantSeq := []int{0, 0, 1, 1}
	if len(stepSeq) != len(wantSeq) {
		t.Fatalf("STEP 序列 = %v（events=%v）", stepSeq, types)
	}
	for i := range wantSeq {
		if stepSeq[i] != wantSeq[i] {
			t.Fatalf("STEP 序列 = %v，应为 %v", stepSeq, wantSeq)
		}
	}
	firstStepEnd := indexOf(t, types, ai.EventStepEnd)
	if toolResultAt < 0 || toolResultAt > firstStepEnd {
		t.Fatalf("TOOL_RESULT 应在第 0 步 STEP_END 之前: %v", types)
	}
	if ev := rec.ofType(ai.EventToolResult)[0]; ev.ToolCallID != "call_1" || ev.Text != "A1 已发货" {
		t.Fatalf("TOOL_RESULT = %+v", ev)
	}

	// 两轮请求；第二轮回放 [user, assistant(tool_calls), tool]
	if s.count() != 2 {
		t.Fatalf("请求数 = %d", s.count())
	}
	body := s.request(t, 1)
	list, _ := body["messages"].([]any)
	if len(list) != 3 {
		t.Fatalf("第二轮 messages = %v", list)
	}
	if body["stream"] != true {
		t.Fatalf("流式请求应带 stream=true: %v", body["stream"])
	}

	// 会话表 [user, assistant(tc), tool, assistant(终答)]
	hist := sess.Messages()
	if len(hist) != 4 || hist[3].Content.(*ai.AssistantMessage).Text != "你好" {
		t.Fatalf("会话消息 = %v", hist)
	}

	// 用量跨步累加：RESPONSE_END 携带两步之和
	end := rec.ofType(ai.EventResponseEnd)[0]
	if u := end.Response().Usage(); u == nil || u.TotalTokens != 18 {
		t.Fatalf("totalUsage = %+v（应 15+3）", u)
	}
}

func indexOf(t *testing.T, types []ai.EventType, want ai.EventType) int {
	t.Helper()
	for i, ty := range types {
		if ty == want {
			return i
		}
	}
	t.Fatalf("未找到 %v", want)
	return -1
}

// ---- 不变量 3：sink 串行；返回 error 即消费方中止（无 ERROR、透传错误）----

func TestStreamInvariant3SinkErrorAborts(t *testing.T) {
	s := newSSEResponder(t, sseTextFrames...)
	m := sseModel(t, s)

	sentinel := errors.New("consumer says stop")
	rec := &sinkRecorder{err: sentinel, stopOn: func(ev *ai.ChatEvent) bool {
		return ev.Type == ai.EventTextDelta
	}}
	err := m.Prompt("你好").Stream(context.Background(), rec)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v（应透传 sink 错误）", err)
	}
	if got := rec.ofType(ai.EventError); len(got) != 0 {
		t.Fatalf("消费方中止不发 ERROR: %v", rec.types())
	}
	if got := rec.ofType(ai.EventResponseEnd); len(got) != 0 {
		t.Fatalf("消费方中止不发 RESPONSE_END: %v", rec.types())
	}
	if got := rec.ofType(ai.EventStepEnd); len(got) != 0 {
		t.Fatalf("消费方中止不补 STEP_END: %v", rec.types())
	}
}

// ---- 不变量 4：RESPONSE_END 携带完整聚合；CallStream 聚合帮助 ----

func TestStreamInvariant4FullAggregation(t *testing.T) {
	s := newSSEResponder(t, sseTextFrames...)
	m := sseModel(t, s)

	rec := &sinkRecorder{}
	if err := m.Prompt("你好").Stream(context.Background(), rec); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	end := rec.ofType(ai.EventResponseEnd)[0]
	resp := end.Response()
	if resp == nil {
		t.Fatal("RESPONSE_END 应携带 Response")
	}
	if resp.Text() != "你好" {
		t.Fatalf("聚合 text = %q", resp.Text())
	}
	if resp.FinishReason() != "stop" {
		t.Fatalf("finishReason = %q", resp.FinishReason())
	}
	if u := resp.Usage(); u == nil || u.TotalTokens != 15 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestCallStreamAggregation(t *testing.T) {
	s := newSSEResponder(t, sseTextFrames...)
	m := sseModel(t, s)

	resp, err := ai.CallStream(context.Background(), m.Prompt("你好"))
	if err != nil {
		t.Fatalf("CallStream: %v", err)
	}
	if resp.Text() != "你好" {
		t.Fatalf("text = %q", resp.Text())
	}
	if len(resp.Events()) == 0 {
		t.Fatal("CallStream 应携带事件全轨迹")
	}
	evs := resp.Events()
	if evs[0].Type != ai.EventResponseStart || evs[len(evs)-1].Type != ai.EventResponseEnd {
		t.Fatalf("轨迹首末 = %v / %v", evs[0].Type, evs[len(evs)-1].Type)
	}
}

// ---- 不变量 5：SSE 帧 → 方言 → 事件管线（过滤器 → sink）----

func TestStreamInvariant5PipelineThroughDialectAndFilter(t *testing.T) {
	s := newSSEResponder(t, sseTextFrames...)
	m := sseModel(t, s)

	rec := &sinkRecorder{}
	if err := m.Prompt("你好").
		EventFilter(ai.EventFilterFunc(func(ev *ai.ChatEvent) bool {
			return ev.Type != ai.EventTextDelta
		})).
		Stream(context.Background(), rec); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if got := rec.ofType(ai.EventTextDelta); len(got) != 0 {
		t.Fatalf("TEXT_DELTA 应被过滤: %v", rec.types())
	}
	// 过滤只影响投递，不影响聚合
	end := rec.ofType(ai.EventResponseEnd)[0]
	if end.Response().Text() != "你好" {
		t.Fatalf("聚合不应受过滤器影响: %q", end.Response().Text())
	}

	// 不过滤时方言分片按序到达
	rec2 := &sinkRecorder{}
	if err := m.Prompt("你好").Stream(context.Background(), rec2); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	deltas := rec2.ofType(ai.EventTextDelta)
	if len(deltas) != 2 || deltas[0].Text != "你" || deltas[1].Text != "好" {
		t.Fatalf("TEXT_DELTA = %v", deltas)
	}
	usage := rec2.ofType(ai.EventUsage)
	if len(usage) != 1 || usage[0].Usage == nil || usage[0].Usage.TotalTokens != 15 {
		t.Fatalf("USAGE = %v", usage)
	}
}

// ---- ctx 取消在帧间隙生效 ----

func TestStreamContextCancelBetweenFrames(t *testing.T) {
	s := newSSEScripted(t, 30*time.Millisecond, [][]string{sseTextFrames})
	m := sseModel(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	rec := &sinkRecorder{stopOn: func(ev *ai.ChatEvent) bool {
		if ev.Type == ai.EventTextDelta {
			cancel()
		}
		return false
	}}
	err := m.Prompt("你好").Stream(ctx, rec)
	if err == nil {
		t.Fatal("ctx 取消应返回错误")
	}
	if got := rec.ofType(ai.EventResponseEnd); len(got) != 0 {
		t.Fatalf("取消路径不应有 RESPONSE_END: %v", rec.types())
	}
}

// ---- channel 形态：背压不丢事件 ----

func TestStreamChanBackpressure(t *testing.T) {
	frames := make([]string, 0, 122)
	for i := 0; i < 120; i++ {
		frames = append(frames, fmt.Sprintf(
			`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"字%d"}}]}`, i))
	}
	frames = append(frames,
		`{"id":"c1","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`)
	s := newSSEResponder(t, frames...)
	m := sseModel(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ch, err := ai.StreamChan(ctx, m.Prompt("长文本"))
	if err != nil {
		t.Fatalf("StreamChan: %v", err)
	}

	var texts []string
	var sawEnd bool
	for ev := range ch {
		if ev.Type == ai.EventTextDelta {
			texts = append(texts, ev.Text)
			time.Sleep(2 * time.Millisecond) // 慢消费者：缓冲 64 必然打满，生产者阻塞
		}
		if ev.Type == ai.EventResponseEnd {
			sawEnd = true
		}
	}
	if !sawEnd {
		t.Fatal("channel 应以 RESPONSE_END 收束并关闭")
	}
	if len(texts) != 120 {
		t.Fatalf("慢消费者丢事件: got %d 条（应 120）", len(texts))
	}
	for i, txt := range texts {
		if want := fmt.Sprintf("字%d", i); txt != want {
			t.Fatalf("texts[%d] = %q，应为 %q（顺序错乱即背压损坏）", i, txt, want)
		}
	}
}

// ---- returnDirect 流式合成 ----

func TestStreamReturnDirect(t *testing.T) {
	s := newSSEResponder(t, sseNowTimeFrames...)
	m := sseModel(t, s)

	tool := ai.NewTool("now-time").Desc("当前时间").ReturnDirect(true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return "12:00", nil
		}).Build()

	sess := ai.NewMemorySession()
	rec := &sinkRecorder{}
	err := m.Prompt("现在几点").Session(sess).
		Options(func(o *ai.ChatOptions) {
			o.AutoToolCall = autoToolOn()
			o.Tools = []ai.FunctionTool{tool}
		}).
		Stream(context.Background(), rec)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if s.count() != 1 {
		t.Fatalf("returnDirect 不应递归，请求数 = %d", s.count())
	}
	types := rec.types()
	// 合成正文以 TEXT_DELTA 投递，TOOL_RESULT 属本步（STEP_END 之前）
	ti := indexOf(t, types, ai.EventToolResult)
	si := indexOf(t, types, ai.EventStepEnd)
	if ti < 0 || ti > si {
		t.Fatalf("TOOL_RESULT 应在 STEP_END 之前: %v", types)
	}
	var synthText string
	for _, ev := range rec.ofType(ai.EventTextDelta) {
		synthText += ev.Text
	}
	if synthText != "12:00" {
		t.Fatalf("合成正文 = %q", synthText)
	}

	end := rec.ofType(ai.EventResponseEnd)[0]
	if end.Response().Text() != "12:00" || end.Response().FinishReason() != "tool" {
		t.Fatalf("终态 = %q / %q", end.Response().Text(), end.Response().FinishReason())
	}

	hist := sess.Messages()
	if len(hist) != 4 || hist[3].Content.(*ai.AssistantMessage).Text != "12:00" {
		t.Fatalf("会话消息 = %v", hist)
	}
}

// ---- 关闭 autoToolCall：工具调用经 STEP_END/RESPONSE_END 交付 ----

func TestStreamToolCallsWithoutAuto(t *testing.T) {
	s := newSSEResponder(t, sseToolCallFrames...)
	m := sseModel(t, s)

	sess := ai.NewMemorySession()
	rec := &sinkRecorder{}
	err := m.Prompt("查 A1").Session(sess).
		Options(func(o *ai.ChatOptions) {
			o.Tools = []ai.FunctionTool{streamTool(t, "query-order", "不应执行")}
		}).
		Stream(context.Background(), rec)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	if s.count() != 1 {
		t.Fatalf("不应递归，请求数 = %d", s.count())
	}
	if got := rec.ofType(ai.EventToolResult); len(got) != 0 {
		t.Fatalf("关闭 autoToolCall 不执行工具: %v", rec.types())
	}
	end := rec.ofType(ai.EventResponseEnd)[0]
	if calls := end.Response().ToolCalls(); len(calls) != 1 || calls[0].ID != "call_1" {
		t.Fatalf("RESPONSE_END 应携带工具调用: %+v", calls)
	}
	if fr := end.Response().FinishReason(); fr != "tool" {
		t.Fatalf("finishReason = %q", fr)
	}
	// 记忆恰一条 assistant(tool_calls)（不重复写）
	hist := sess.Messages()
	if len(hist) != 2 || len(hist[1].Content.(*ai.AssistantMessage).ToolCalls) != 1 {
		t.Fatalf("会话消息 = %v", hist)
	}
}

// ---- 响应体守卫 ----

func TestStreamGuards(t *testing.T) {
	t.Run("空流", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
		}))
		t.Cleanup(srv.Close)
		m, _ := ai.NewChatModel(ai.Config{APIURL: srv.URL, Model: "gpt-4o"})
		rec := &sinkRecorder{}
		err := m.Prompt("hi").Stream(context.Background(), rec)
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("err = %v（应报 empty）", err)
		}
	})

	t.Run("无模型帧", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: hello world\n\ndata: plain text\n\n"))
		}))
		t.Cleanup(srv.Close)
		m, _ := ai.NewChatModel(ai.Config{APIURL: srv.URL, Model: "gpt-4o"})
		rec := &sinkRecorder{}
		err := m.Prompt("hi").Stream(context.Background(), rec)
		if err == nil || !strings.Contains(err.Error(), "unrecognizable") {
			t.Fatalf("err = %v（应报 unrecognizable）", err)
		}
	})

	t.Run("截断流（无完成信号）", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"content":"部分"}}]}` + "\n\n"))
		}))
		t.Cleanup(srv.Close)
		m, _ := ai.NewChatModel(ai.Config{APIURL: srv.URL, Model: "gpt-4o"})
		rec := &sinkRecorder{}
		err := m.Prompt("hi").Stream(context.Background(), rec)
		if err == nil || !strings.Contains(err.Error(), "completion signal") {
			t.Fatalf("err = %v（应报截断）", err)
		}
	})

	t.Run("HTML 页面（网关误配）", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><body>home</body></html>"))
		}))
		t.Cleanup(srv.Close)
		m, _ := ai.NewChatModel(ai.Config{APIURL: srv.URL, Model: "gpt-4o"})
		rec := &sinkRecorder{}
		err := m.Prompt("hi").Stream(context.Background(), rec)
		if err == nil {
			t.Fatal("HTML 响应应报错")
		}
		if got := rec.ofType(ai.EventResponseEnd); len(got) != 0 {
			t.Fatalf("守卫失败不应有 RESPONSE_END: %v", rec.types())
		}
	})
}

// ---- StreamInterceptor ----

type streamAuditInterceptor struct {
	ai.InterceptorBase
	name  string
	order *[]string
}

func (a *streamAuditInterceptor) InterceptStream(ctx context.Context, req *ai.CallRequest, chain *ai.StreamChain) error {
	*a.order = append(*a.order, "stream:"+a.name)
	return chain.DoIntercept(ctx, req)
}

func TestStreamInterceptorChain(t *testing.T) {
	s := newSSEResponder(t, sseTextFrames...)
	m := sseModel(t, s)

	var order []string
	rec := &sinkRecorder{}
	err := m.Prompt("你好").
		Options(func(o *ai.ChatOptions) {
			o.Interceptors = []ai.Interceptor{
				&streamAuditInterceptor{name: "a", order: &order},
				&streamAuditInterceptor{name: "b", order: &order},
			}
		}).
		Stream(context.Background(), rec)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if strings.Join(order, "|") != "stream:a|stream:b" {
		t.Fatalf("拦截顺序 = %v", order)
	}
}

// ---- 工具执行失败：ERROR 终态 + 透传 ----

func TestStreamToolNotFound(t *testing.T) {
	s := newSSEResponder(t, sseToolCallFrames...)
	m := sseModel(t, s)

	rec := &sinkRecorder{}
	err := m.Prompt("查 A1").
		Options(func(o *ai.ChatOptions) { o.AutoToolCall = autoToolOn() }).
		Stream(context.Background(), rec)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v（应报 tool not found）", err)
	}
	if got := rec.ofType(ai.EventResponseEnd); len(got) != 0 {
		t.Fatalf("工具失败不应有 RESPONSE_END: %v", rec.types())
	}
	if got := rec.ofType(ai.EventError); len(got) != 1 {
		t.Fatalf("应恰一个 ERROR: %v", rec.types())
	}
}
