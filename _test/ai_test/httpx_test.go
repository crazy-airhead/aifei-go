package ai_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/ai/httpx"
)

// —— SSE 解析器（分帧/多行 data/事件名/[DONE]/注释行/CRLF/尾帧） ——

func TestParseSSE(t *testing.T) {
	input := "event: Chat\n" +
		"data: {\"a\":1}\n" +
		"\n" +
		"data: line1\n" +
		"data: line2\n" +
		"\n" +
		": keep-alive comment\n" +
		"data: [DONE]\n" +
		"\n" +
		"event: Mixed\r\n" +
		"data: crlf\r\n" +
		"\r\n" +
		"data: tail-frame-no-blank"

	var got []struct{ event, data string }
	err := httpx.ParseSSE(context.Background(), strings.NewReader(input), func(event, data string) error {
		got = append(got, struct{ event, data string }{event, data})
		return nil
	})
	if err != nil {
		t.Fatalf("ParseSSE: %v", err)
	}
	want := []struct{ event, data string }{
		{"chat", `{"a":1}`},
		{"", "line1\nline2"},
		{"", "[DONE]"},
		{"mixed", "crlf"},
		{"", "tail-frame-no-blank"},
	}
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events[%d] = (%q,%q), want (%q,%q)", i, got[i].event, got[i].data, want[i].event, want[i].data)
		}
	}
}

func TestParseSSEDataFieldForms(t *testing.T) {
	// 冒号后至多一个前导空格属于分隔符；第二个空格是数据
	input := "data:nospace\n\ndata: one\n\ndata:  two\n\ndata:\n\n"
	var got []string
	err := httpx.ParseSSE(context.Background(), strings.NewReader(input), func(_, data string) error {
		got = append(got, data)
		return nil
	})
	if err != nil {
		t.Fatalf("ParseSSE: %v", err)
	}
	want := []string{"nospace", "one", " two", ""}
	if len(got) != len(want) {
		t.Fatalf("data = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("data[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseSSEAbort(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	err := httpx.ParseSSE(context.Background(), strings.NewReader("data: a\n\ndata: b\n\ndata: c\n\n"),
		func(_, data string) error {
			calls++
			if data == "b" {
				return boom
			}
			return nil
		})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if calls != 2 {
		t.Fatalf("onEvent called %d times, want 2（中止后不再投递）", calls)
	}
}

func TestParseSSEContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := httpx.ParseSSE(ctx, strings.NewReader("data: a\n\ndata: b\n\n"), func(_, _ string) error {
		t.Fatal("取消后不应投递")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestParseSSELineLimits(t *testing.T) {
	// 1MiB 上限：半 MiB 通过，2MiB 报错（bufio.ErrTooLong）
	ok := strings.Repeat("x", 512*1024)
	var got string
	if err := httpx.ParseSSE(context.Background(), strings.NewReader("data: "+ok+"\n\n"),
		func(_, data string) error { got = data; return nil }); err != nil {
		t.Fatalf("512KiB 行应通过: %v", err)
	}
	if len(got) != len(ok) {
		t.Fatalf("512KiB data len = %d, want %d", len(got), len(ok))
	}

	big := strings.Repeat("x", 2*1024*1024)
	err := httpx.ParseSSE(context.Background(), strings.NewReader("data: "+big+"\n\n"),
		func(_, _ string) error { return nil })
	if err == nil {
		t.Fatal("2MiB 行应报错")
	}
}

// —— Client：PostJSON / PostJSONStream（httptest 假服务，不真连厂商） ——

func TestClientPostJSON(t *testing.T) {
	var mu sync.Mutex
	var seen http.Request
	var seenBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = *r
		seenBody = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := httpx.New(httpx.WithHeader("X-Default", "dv"))
	body, status, err := client.PostJSON(context.Background(), srv.URL+"/v1/chat/completions",
		[]byte(`{"model":"gpt-4o"}`), map[string]string{"Authorization": "Bearer sk-test"})
	if err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %s", body)
	}

	mu.Lock()
	defer mu.Unlock()
	if seen.Method != http.MethodPost {
		t.Fatalf("method = %s", seen.Method)
	}
	if seen.URL.Path != "/v1/chat/completions" {
		t.Fatalf("path = %s", seen.URL.Path)
	}
	if string(seenBody) != `{"model":"gpt-4o"}` {
		t.Fatalf("sent body = %s", seenBody)
	}
	if ct := seen.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if au := seen.Header.Get("Authorization"); au != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", au)
	}
	if dv := seen.Header.Get("X-Default"); dv != "dv" {
		t.Fatalf("X-Default = %q（默认头应生效）", dv)
	}
}

func TestClientPostJSONHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"Rate limited"}}`))
	}))
	defer srv.Close()

	client := httpx.New()
	body, status, err := client.PostJSON(context.Background(), srv.URL, []byte(`{}`), nil)
	if err == nil {
		t.Fatal("非 2xx 应返回错误")
	}
	var he *httpx.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err 类型 = %T, want *httpx.HTTPError", err)
	}
	if he.Status != http.StatusTooManyRequests {
		t.Fatalf("HTTPError.Status = %d", he.Status)
	}
	if !strings.Contains(string(he.Body), "Rate limited") {
		t.Fatalf("HTTPError.Body = %s", he.Body)
	}
	if status != http.StatusTooManyRequests {
		t.Fatalf("status = %d", status)
	}
	if !strings.Contains(err.Error(), "429") {
		t.Fatalf("err.Error() = %q，应含状态码", err.Error())
	}
	_ = body
}

func TestClientPostJSONTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := httpx.New(httpx.WithTimeout(20 * time.Millisecond))
	if _, _, err := client.PostJSON(context.Background(), srv.URL, []byte(`{}`), nil); err == nil {
		t.Fatal("超时应报错")
	}
}

func TestClientPostJSONStream(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: chat\n"))
		_, _ = w.Write([]byte("data: {\"d\":1}\n\n"))
		_, _ = w.Write([]byte("data: {\"d\":2}\n"))
		_, _ = w.Write([]byte("\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	client := httpx.New()
	var events []struct{ event, data string }
	err := client.PostJSONStream(context.Background(), srv.URL, []byte(`{}`), nil,
		func(event, data string) error {
			events = append(events, struct{ event, data string }{event, data})
			return nil
		})
	if err != nil {
		t.Fatalf("PostJSONStream: %v", err)
	}
	if gotAccept != "text/event-stream" {
		t.Fatalf("Accept = %q", gotAccept)
	}
	want := []struct{ event, data string }{
		{"chat", `{"d":1}`},
		{"", `{"d":2}`},
		{"", "[DONE]"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events[%d] = (%q,%q), want (%q,%q)", i, events[i].event, events[i].data, want[i].event, want[i].data)
		}
	}
}

func TestClientPostJSONStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()

	client := httpx.New()
	err := client.PostJSONStream(context.Background(), srv.URL, []byte(`{}`), nil,
		func(_, _ string) error { return nil })
	var he *httpx.HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err 类型 = %T, want *httpx.HTTPError", err)
	}
	if he.Status != http.StatusUnauthorized || !strings.Contains(string(he.Body), "bad key") {
		t.Fatalf("HTTPError = %+v", he)
	}
}

func TestClientWithDoer(t *testing.T) {
	var mu sync.Mutex
	var gotAuth, gotPath string
	fake := doerFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		gotAuth = req.Header.Get("Authorization")
		gotPath = req.URL.Path
		mu.Unlock()
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     http.Header{},
		}, nil
	})
	client := httpx.New(httpx.WithDoer(fake))
	if _, _, err := client.PostJSON(context.Background(), "http://fake.test/x",
		[]byte(`{}`), map[string]string{"Authorization": "Bearer inject"}); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer inject" {
		t.Fatalf("Authorization = %q（Doer 注入缝应收到请求头）", gotAuth)
	}
	if gotPath != "/x" {
		t.Fatalf("path = %q", gotPath)
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }
