package nami_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/crazy-airhead/aifei-go/nami"
)

// testEncoder is a simple encoder used in tests.
type testEncoder struct{}

func (e *testEncoder) Enctype() string                { return nami.JSONValue }
func (e *testEncoder) BodyRequired() bool             { return false }
func (e *testEncoder) Encode(obj any) ([]byte, error) { return json.Marshal(obj) }
func (e *testEncoder) Pretreatment(ctx *nami.Context) {}

// testDecoder is a simple decoder used in tests.
type testDecoder struct{}

func (d *testDecoder) Enctype() string { return nami.JSONValue }
func (d *testDecoder) Decode(rst *nami.Result, typ reflect.Type) (any, error) {
	val := reflect.New(typ).Interface()
	if err := json.Unmarshal([]byte(rst.BodyAsString()), val); err != nil {
		return nil, err
	}
	return reflect.ValueOf(val).Elem().Interface(), nil
}
func (d *testDecoder) Pretreatment(ctx *nami.Context) {}

// testChannel is a simple channel that calls an httptest server.
type testChannel struct {
	nami.ChannelBase
	serverURL string
}

func (tc *testChannel) Call(ctx *nami.Context) (*nami.Result, error) {
	tc.Pretreatment(ctx)
	req, err := http.NewRequest(ctx.Action, tc.serverURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return nami.NewResult(resp.StatusCode, body), nil
}

func TestManagerRegistry(t *testing.T) {
	// Register test implementations
	te := &testEncoder{}
	td := &testDecoder{}
	nami.RegEncoder(te)
	nami.RegDecoder(td)
	nami.RegChannel("http", &testChannel{})

	d := nami.GetDecoder(nami.JSONValue)
	if d == nil {
		t.Fatal("decoder not registered")
	}
	if d.Enctype() != nami.JSONValue {
		t.Fatalf("expected enctype %s, got %s", nami.JSONValue, d.Enctype())
	}

	e := nami.GetEncoder(nami.JSONValue)
	if e == nil {
		t.Fatal("encoder not registered")
	}

	ch := nami.GetChannel("http")
	if ch == nil {
		t.Fatal("channel not registered")
	}

	// Test GetDecoderFirst / GetEncoderFirst
	if nami.GetDecoderFirst() != td {
		t.Fatal("expected decoder first")
	}
	if nami.GetEncoderFirst() != te {
		t.Fatal("expected encoder first")
	}
}

func TestUpstreamFixed(t *testing.T) {
	u := nami.NewUpstreamFixed([]string{"http://localhost:8080"})
	for i := 0; i < 5; i++ {
		if u() != "http://localhost:8080" {
			t.Fatal("single server upstream should always return same URL")
		}
	}

	u = nami.NewUpstreamFixed([]string{"http://s1:8080", "http://s2:8080", "http://s3:8080"})
	seen := make(map[string]int)
	for i := 0; i < 6; i++ {
		seen[u()]++
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 unique servers, got %d", len(seen))
	}
	for s, count := range seen {
		if count != 2 {
			t.Fatalf("expected server %s returned 2 times, got %d", s, count)
		}
	}
}

func TestResult(t *testing.T) {
	r := nami.NewResult(200, []byte(`{"ok":true}`))
	if r.Code() != 200 {
		t.Fatalf("expected 200, got %d", r.Code())
	}
	if err := r.AssertSuccess(); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if s := r.BodyAsString(); s != `{"ok":true}` {
		t.Fatalf("expected body %q, got %q", `{"ok":true}`, s)
	}

	r2 := nami.NewResult(500, []byte("internal error"))
	if err := r2.AssertSuccess(); err == nil {
		t.Fatal("expected error for 500 status")
	}
}

func TestResultHeaders(t *testing.T) {
	r := nami.NewResult(200, []byte("ok"))
	r.HeaderAdd("X-Custom", "val1")
	r.HeaderAdd("X-Custom", "val2")
	if r.HeaderGet("X-Custom") != "val1" {
		t.Fatal("expected val1")
	}
}

func TestResultBind(t *testing.T) {
	r := nami.NewResult(200, []byte(`{"name":"test","count":42}`))

	var v struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	if err := r.Bind(&v); err != nil {
		t.Fatalf("Bind failed: %v", err)
	}
	if v.Name != "test" {
		t.Errorf("name = %q, want test", v.Name)
	}
	if v.Count != 42 {
		t.Errorf("count = %d, want 42", v.Count)
	}
}

func TestResultBindNon2xx(t *testing.T) {
	r := nami.NewResult(500, []byte(`{"error":"boom"}`))
	var v any
	if err := r.Bind(&v); err == nil {
		t.Fatal("expected error for 500 status")
	}
}

func TestResultAsAny(t *testing.T) {
	r := nami.NewResult(200, []byte(`{"key":"value","num":1}`))
	v, err := r.AsAny()
	if err != nil {
		t.Fatalf("AsAny failed: %v", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("type = %T, want map[string]any", v)
	}
	if m["key"] != "value" {
		t.Errorf("key = %v", m["key"])
	}
	if m["num"] != float64(1) {
		t.Errorf("num = %v", m["num"])
	}
}

func TestResultAsAnyArray(t *testing.T) {
	r := nami.NewResult(200, []byte(`[{"a":1},{"b":2}]`))
	v, err := r.AsAny()
	if err != nil {
		t.Fatalf("AsAny failed: %v", err)
	}
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("type = %T, want []any", v)
	}
	if len(arr) != 2 {
		t.Fatalf("len = %d, want 2", len(arr))
	}
}

func TestResultAsAnyEmpty(t *testing.T) {
	r := nami.NewResult(200, []byte{})
	v, err := r.AsAny()
	if err != nil {
		t.Fatalf("AsAny failed: %v", err)
	}
	if v != nil {
		t.Errorf("expected nil for empty body, got %v", v)
	}
}

func TestBuilder(t *testing.T) {
	b := nami.NewBuilder().
		Timeout(5).
		URL("http://example.com/api").
		HeaderSet("X-Custom", "value")

	n := b.Build()
	if n.Config().Timeout() != 5 {
		t.Fatalf("expected timeout 5, got %d", n.Config().Timeout())
	}
	if n.Config().URL() != "http://example.com/api" {
		t.Fatalf("expected URL, got %s", n.Config().URL())
	}
	if n.Config().HeaderGet("X-Custom") != "value" {
		t.Fatal("expected X-Custom header")
	}
}

func TestFilterChain(t *testing.T) {
	order := make([]string, 0)

	f1 := nami.FilterFunc(func(inv *nami.Invocation) (*nami.Result, error) {
		order = append(order, "f1")
		return inv.Invoke()
	})
	f2 := nami.FilterFunc(func(inv *nami.Invocation) (*nami.Result, error) {
		order = append(order, "f2")
		return inv.Invoke()
	})

	c := nami.NewConfig()
	c.FilterAdd(f1)
	c.FilterAdd(f2)

	actuator := nami.FilterFunc(func(inv *nami.Invocation) (*nami.Result, error) {
		order = append(order, "actuator")
		return nami.NewResult(200, []byte("ok")), nil
	})

	inv := nami.NewInvocation(c, nil, reflect.Method{}, nami.MethodGet, "http://example.com", nil, actuator)
	result, err := inv.Invoke()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Code() != 200 {
		t.Fatalf("expected 200, got %d", result.Code())
	}

	if len(order) != 3 || order[0] != "f1" || order[1] != "f2" || order[2] != "actuator" {
		t.Fatalf("unexpected filter order: %v", order)
	}
}

func TestBuilderWithUpstream(t *testing.T) {
	upstream := nami.NewUpstreamFixed([]string{"http://localhost:9999"})
	b := nami.NewBuilder().
		Upstream(upstream).
		Name("test-service").
		Path("/api")

	cfg := b.Config()
	if cfg.Upstream() == nil {
		t.Fatal("upstream should be set")
	}
	if cfg.Name() != "test-service" {
		t.Fatalf("expected name test-service, got %s", cfg.Name())
	}
	if cfg.Path() != "/api" {
		t.Fatalf("expected path /api, got %s", cfg.Path())
	}
}

func TestCallWithUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"hello":"world"}`))
	}))
	defer server.Close()

	// Register test encoder/decoder/channel
	nami.RegEncoder(&testEncoder{})
	nami.RegDecoder(&testDecoder{})
	ch := &testChannel{serverURL: server.URL}
	nami.RegChannel("http", ch)
	nami.RegChannel("https", ch)

	n := nami.New().URL(server.URL).Action(nami.MethodGet)
	n.Call(nil, nil, nil)

	var result map[string]string
	if err := n.GetObject(&result); err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if result["hello"] != "world" {
		t.Fatalf("expected world, got %v", result["hello"])
	}
}
