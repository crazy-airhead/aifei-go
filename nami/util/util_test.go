package util

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Get — simple GET returning body string
// ---------------------------------------------------------------------------

func TestGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodGet)
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	body, err := Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if body != "hello" {
		t.Fatalf("got %q, want hello", body)
	}
}

func TestGet_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := Get(srv.URL); err == nil {
		t.Fatal("expected error for 404")
	}
}

// ---------------------------------------------------------------------------
// GetResult / GetResultWith — raw Result, no status check
// ---------------------------------------------------------------------------

func TestGetResult_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "val")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("accepted"))
	}))
	defer srv.Close()

	r, err := GetResult(srv.URL)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if r.Code() != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", r.Code())
	}
	if r.BodyAsString() != "accepted" {
		t.Fatalf("body = %q", r.BodyAsString())
	}
	if r.HeaderGet("X-Custom") != "val" {
		t.Fatalf("header = %q", r.HeaderGet("X-Custom"))
	}
}

func TestGetResult_TransportError(t *testing.T) {
	if _, err := GetResult("http://127.0.0.1:0/nope"); err == nil {
		t.Fatal("expected transport error")
	}
}

func TestGetResultWith(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodGet)
		assertQuery(t, r, "q", "search")
		assertHeader(t, r, "X-Token", "s3cr3t")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	r, err := GetResultWith(srv.URL,
		map[string]string{"q": "search"},
		map[string]string{"X-Token": "s3cr3t"},
	)
	if err != nil {
		t.Fatalf("GetResultWith: %v", err)
	}
	if r.Code() != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", r.Code())
	}
}

// ---------------------------------------------------------------------------
// GetJSON — generic return
// ---------------------------------------------------------------------------

func TestGetJSON_Success(t *testing.T) {
	type resp struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp{Name: "alice", Age: 30})
	}))
	defer srv.Close()

	v, err := GetJSON[resp](srv.URL)
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if v.Name != "alice" || v.Age != 30 {
		t.Fatalf("got %+v, want {alice 30}", v)
	}
}

func TestGetJSON_Slice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"a":1},{"a":2}]`))
	}))
	defer srv.Close()

	type item struct {
		A int `json:"a"`
	}
	items, err := GetJSON[[]item](srv.URL)
	if err != nil {
		t.Fatalf("GetJSON slice: %v", err)
	}
	if len(items) != 2 || items[0].A != 1 || items[1].A != 2 {
		t.Fatalf("got %+v, want [{1} {2}]", items)
	}
}

func TestGetJSON_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	if _, err := GetJSON[any](srv.URL); err == nil {
		t.Fatal("expected error for 400")
	}
}

// ---------------------------------------------------------------------------
// GetJSONWith — generic return + params + headers
// ---------------------------------------------------------------------------

func TestGetJSONWith(t *testing.T) {
	type resp struct {
		Page int `json:"page"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodGet)
		assertQuery(t, r, "page", "5")
		assertHeader(t, r, "Accept", "application/json")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp{Page: 5})
	}))
	defer srv.Close()

	v, err := GetJSONWith[resp](srv.URL,
		map[string]string{"page": "5"},
		map[string]string{"Accept": "application/json"},
	)
	if err != nil {
		t.Fatalf("GetJSONWith: %v", err)
	}
	if v.Page != 5 {
		t.Fatalf("page = %d, want 5", v.Page)
	}
}

// ---------------------------------------------------------------------------
// GetWith — params + headers
// ---------------------------------------------------------------------------

func TestGetWith_Params(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodGet)
		assertQuery(t, r, "page", "3")
		assertQuery(t, r, "size", "15")
		w.Write([]byte("paged"))
	}))
	defer srv.Close()

	body, err := GetWith(srv.URL, map[string]string{"page": "3", "size": "15"}, nil)
	if err != nil {
		t.Fatalf("GetWith: %v", err)
	}
	if body != "paged" {
		t.Fatalf("got %q, want paged", body)
	}
}

func TestGetWith_Headers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertHeader(t, r, "Authorization", "Bearer xyz")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	body, err := GetWith(srv.URL, nil, map[string]string{"Authorization": "Bearer xyz"})
	if err != nil {
		t.Fatalf("GetWith: %v", err)
	}
	if body != "ok" {
		t.Fatalf("got %q, want ok", body)
	}
}

func TestGetWith_BothNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("plain"))
	}))
	defer srv.Close()

	body, err := GetWith(srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("GetWith nil,nil: %v", err)
	}
	if body != "plain" {
		t.Fatalf("got %q, want plain", body)
	}
}

// ---------------------------------------------------------------------------
// GetBind
// ---------------------------------------------------------------------------

func TestGetBind_Success(t *testing.T) {
	type resp struct {
		Status string `json:"status"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodGet)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp{Status: "ok"})
	}))
	defer srv.Close()

	var v resp
	if err := GetBind(srv.URL, nil, &v); err != nil {
		t.Fatalf("GetBind: %v", err)
	}
	if v.Status != "ok" {
		t.Fatalf("status = %s, want ok", v.Status)
	}
}

// ---------------------------------------------------------------------------
// GetBindWith — params + headers + bind
// ---------------------------------------------------------------------------

func TestGetBindWith(t *testing.T) {
	type resp struct {
		Items []int `json:"items"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodGet)
		assertQuery(t, r, "page", "1")
		assertHeader(t, r, "X-Token", "secret")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp{Items: []int{1, 2, 3}})
	}))
	defer srv.Close()

	var v resp
	err := GetBindWith(srv.URL, nil,
		map[string]string{"page": "1"},
		map[string]string{"X-Token": "secret"},
		&v)
	if err != nil {
		t.Fatalf("GetBindWith: %v", err)
	}
	if len(v.Items) != 3 {
		t.Fatalf("items = %v, want [1 2 3]", v.Items)
	}
}

// ---------------------------------------------------------------------------
// Post — returns body string, checks status
// ---------------------------------------------------------------------------

func TestPost_WithBody(t *testing.T) {
	type payload struct {
		Key string `json:"key"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodPost)
		body, _ := io.ReadAll(r.Body)
		var p payload
		json.Unmarshal(body, &p)
		if p.Key != "val" {
			t.Errorf("key = %s, want val", p.Key)
		}
		w.Write([]byte(`{"id":"new-1"}`))
	}))
	defer srv.Close()

	body, err := Post(srv.URL, payload{Key: "val"})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if !strings.Contains(body, "new-1") {
		t.Fatalf("body = %q", body)
	}
}

func TestPost_NilBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodPost)
		w.Write([]byte("no-body"))
	}))
	defer srv.Close()

	body, err := Post(srv.URL, nil)
	if err != nil {
		t.Fatalf("Post nil body: %v", err)
	}
	if body != "no-body" {
		t.Fatalf("got %q, want no-body", body)
	}
}

func TestPost_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := Post(srv.URL, nil); err == nil {
		t.Fatal("expected error for 500")
	}
}

// ---------------------------------------------------------------------------
// PostResult — raw Result
// ---------------------------------------------------------------------------

func TestPostResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "v")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("raw"))
	}))
	defer srv.Close()

	r, err := PostResult(srv.URL, nil)
	if err != nil {
		t.Fatalf("PostResult: %v", err)
	}
	if r.Code() != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", r.Code())
	}
	if r.HeaderGet("X-Custom") != "v" {
		t.Fatalf("header missing")
	}
}

func TestPostResultWith(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodPost)
		assertQuery(t, r, "async", "true")
		assertHeader(t, r, "X-Req-Id", "req-1")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	r, err := PostResultWith(srv.URL,
		map[string]string{"data": "x"},
		map[string]string{"async": "true"},
		map[string]string{"X-Req-Id": "req-1"},
	)
	if err != nil {
		t.Fatalf("PostResultWith: %v", err)
	}
	if r.Code() != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", r.Code())
	}
}

// ---------------------------------------------------------------------------
// PostWith — body + params + headers
// ---------------------------------------------------------------------------

func TestPostWith_All(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodPost)
		assertQuery(t, r, "dry_run", "1")
		assertHeader(t, r, "X-Req-Id", "abc")
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "payload") {
			t.Errorf("body missing payload: %s", body)
		}
		w.Write([]byte("done"))
	}))
	defer srv.Close()

	r, err := PostWith(srv.URL,
		map[string]string{"data": "payload"},
		map[string]string{"dry_run": "1"},
		map[string]string{"X-Req-Id": "abc"},
	)
	if err != nil {
		t.Fatalf("PostWith: %v", err)
	}
	if r.BodyAsString() != "done" {
		t.Fatalf("got %q", r.BodyAsString())
	}
}

func TestPostWith_Nils(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	r, err := PostWith(srv.URL, nil, nil, nil)
	if err != nil {
		t.Fatalf("PostWith nils: %v", err)
	}
	if r.BodyAsString() != "ok" {
		t.Fatalf("got %q", r.BodyAsString())
	}
}

// ---------------------------------------------------------------------------
// PostBind
// ---------------------------------------------------------------------------

func TestPostBind(t *testing.T) {
	type resp struct {
		ID string `json:"id"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodPost)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(resp{ID: "id-42"})
	}))
	defer srv.Close()

	var v resp
	if err := PostBind(srv.URL, map[string]string{"name": "x"}, &v); err != nil {
		t.Fatalf("PostBind: %v", err)
	}
	if v.ID != "id-42" {
		t.Fatalf("id = %s, want id-42", v.ID)
	}
}

func TestPostBind_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	var v any
	if err := PostBind(srv.URL, nil, &v); err == nil {
		t.Fatal("expected error for 409")
	}
}

// ---------------------------------------------------------------------------
// PostBindWith
// ---------------------------------------------------------------------------

func TestPostBindWith(t *testing.T) {
	type resp struct {
		Token string `json:"token"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodPost)
		assertQuery(t, r, "op", "login")
		assertHeader(t, r, "X-Device", "mobile")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp{Token: "jwt"})
	}))
	defer srv.Close()

	var v resp
	err := PostBindWith(srv.URL,
		map[string]string{"user": "me"},
		map[string]string{"op": "login"},
		map[string]string{"X-Device": "mobile"},
		&v,
	)
	if err != nil {
		t.Fatalf("PostBindWith: %v", err)
	}
	if v.Token != "jwt" {
		t.Fatalf("token = %s, want jwt", v.Token)
	}
}

// ---------------------------------------------------------------------------
// PostJSON — generic return
// ---------------------------------------------------------------------------

func TestPostJSON(t *testing.T) {
	type resp struct {
		ID string `json:"id"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodPost)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(resp{ID: "p-99"})
	}))
	defer srv.Close()

	v, err := PostJSON[resp](srv.URL, map[string]string{"name": "x"})
	if err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if v.ID != "p-99" {
		t.Fatalf("id = %s, want p-99", v.ID)
	}
}

func TestPostJSON_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	if _, err := PostJSON[any](srv.URL, nil); err == nil {
		t.Fatal("expected error for 409")
	}
}

// ---------------------------------------------------------------------------
// PostJSONWith — generic return + params + headers
// ---------------------------------------------------------------------------

func TestPostJSONWith(t *testing.T) {
	type resp struct {
		Token string `json:"token"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodPost)
		assertQuery(t, r, "op", "login")
		assertHeader(t, r, "X-Device", "mobile")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp{Token: "jwt-xyz"})
	}))
	defer srv.Close()

	v, err := PostJSONWith[resp](srv.URL,
		map[string]string{"user": "me"},
		map[string]string{"op": "login"},
		map[string]string{"X-Device": "mobile"},
	)
	if err != nil {
		t.Fatalf("PostJSONWith: %v", err)
	}
	if v.Token != "jwt-xyz" {
		t.Fatalf("token = %s, want jwt-xyz", v.Token)
	}
}

// ---------------------------------------------------------------------------
// Request — generic method + headers + body
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Request — returns body string
// ---------------------------------------------------------------------------

func TestRequest_String(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodPut)
		w.Write([]byte("updated"))
	}))
	defer srv.Close()

	body, err := Request(http.MethodPut, srv.URL, nil, nil, nil)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if body != "updated" {
		t.Fatalf("got %q, want updated", body)
	}
}

func TestRequest_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := Request(http.MethodGet, srv.URL, nil, nil, nil); err == nil {
		t.Fatal("expected error for 404")
	}
}

// ---------------------------------------------------------------------------
// RequestResult — raw Result
// ---------------------------------------------------------------------------

func TestRequestResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodDelete)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	r, err := RequestResult(http.MethodDelete, srv.URL, nil, nil, nil)
	if err != nil {
		t.Fatalf("RequestResult: %v", err)
	}
	if r.Code() != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", r.Code())
	}
}

func TestRequestResult_Params(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodDelete)
		assertQuery(t, r, "force", "true")
		assertHeader(t, r, "X-Confirm", "yes")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	r, err := RequestResult(http.MethodDelete, srv.URL,
		map[string]string{"id": "1"},
		map[string]string{"force": "true"},
		map[string]string{"X-Confirm": "yes"},
	)
	if err != nil {
		t.Fatalf("RequestResult params: %v", err)
	}
	if r.Code() != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", r.Code())
	}
}

func TestRequestResult_Headers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertHeader(t, r, "X-Auth", "token")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	r, err := RequestResult(http.MethodGet, srv.URL, nil, nil, map[string]string{"X-Auth": "token"})
	if err != nil {
		t.Fatalf("RequestResult headers: %v", err)
	}
	if r.Code() != http.StatusOK {
		t.Fatalf("status = %d", r.Code())
	}
}

// ---------------------------------------------------------------------------
// RequestBind
// ---------------------------------------------------------------------------

func TestRequestBind(t *testing.T) {
	type resp struct {
		Result string `json:"result"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodPatch)
		assertHeader(t, r, "Accept", "application/json")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp{Result: "patched"})
	}))
	defer srv.Close()

	var v resp
	if err := RequestBind(http.MethodPatch, srv.URL, nil, nil, nil, &v); err != nil {
		t.Fatalf("RequestBind: %v", err)
	}
	if v.Result != "patched" {
		t.Fatalf("result = %s, want patched", v.Result)
	}
}

func TestRequestBind_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	var v any
	if err := RequestBind(http.MethodGet, srv.URL, nil, nil, nil, &v); err == nil {
		t.Fatal("expected error for 403")
	}
}

// ---------------------------------------------------------------------------
// RequestJSON — generic return
// ---------------------------------------------------------------------------

func TestRequestJSON(t *testing.T) {
	type resp struct {
		Result string `json:"result"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodPatch)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp{Result: "patched"})
	}))
	defer srv.Close()

	v, err := RequestJSON[resp](http.MethodPatch, srv.URL, nil, nil, nil)
	if err != nil {
		t.Fatalf("RequestJSON: %v", err)
	}
	if v.Result != "patched" {
		t.Fatalf("result = %s, want patched", v.Result)
	}
}

func TestRequestJSON_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	if _, err := RequestJSON[any](http.MethodGet, srv.URL, nil, nil, nil); err == nil {
		t.Fatal("expected error for 403")
	}
}

// ---------------------------------------------------------------------------
// SetBaseURL — resolve relative paths against a base URL
// ---------------------------------------------------------------------------

func TestSetBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodGet)
		if r.URL.Path != "/api/v1/users" {
			t.Errorf("path = %s, want /api/v1/users", r.URL.Path)
		}
		w.Write([]byte("users list"))
	}))
	defer srv.Close()

	SetBaseURL(srv.URL)
	defer SetBaseURL("")

	body, err := Get("/api/v1/users")
	if err != nil {
		t.Fatalf("Get with base URL: %v", err)
	}
	if body != "users list" {
		t.Fatalf("got %q, want users list", body)
	}
}

func TestSetBaseURL_FullURLBypasses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("direct"))
	}))
	defer srv.Close()

	SetBaseURL("http://other.example.com")
	defer SetBaseURL("")

	// Full URL should bypass base URL
	body, err := Get(srv.URL)
	if err != nil {
		t.Fatalf("Get full URL: %v", err)
	}
	if body != "direct" {
		t.Fatalf("got %q, want direct", body)
	}
}

func TestSetBaseURL_PostWithPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertMethod(t, r, http.MethodPost)
		if r.URL.Path != "/api/orders" {
			t.Errorf("path = %s, want /api/orders", r.URL.Path)
		}
		w.Write([]byte("order created"))
	}))
	defer srv.Close()

	SetBaseURL(srv.URL)
	defer SetBaseURL("")

	body, err := Post("/api/orders", map[string]string{"item": "book"})
	if err != nil {
		t.Fatalf("Post with base URL: %v", err)
	}
	if body != "order created" {
		t.Fatalf("got %q", body)
	}
}

func TestBaseURL(t *testing.T) {
	old := BaseURL()
	SetBaseURL("http://example.com/api")
	if BaseURL() != "http://example.com/api" {
		t.Fatalf("got %q", BaseURL())
	}
	SetBaseURL(old)
}

// ---------------------------------------------------------------------------
// SetTimeout — global per-request timeout
// ---------------------------------------------------------------------------

func TestSetTimeout(t *testing.T) {
	old := Timeout()
	defer SetTimeout(old)

	SetTimeout(7)
	if got := Timeout(); got != 7 {
		t.Fatalf("Timeout() = %d, want 7", got)
	}
	// The timeout propagates onto the client built for each request.
	n := newNami()
	if got := n.Config().Timeout(); got != 7 {
		t.Fatalf("client timeout = %d, want 7", got)
	}
}

func TestTimeoutDefaultZero(t *testing.T) {
	old := Timeout()
	defer SetTimeout(old)

	SetTimeout(0)
	if got := Timeout(); got != 0 {
		t.Fatalf("Timeout() = %d, want 0", got)
	}
	n := newNami()
	if got := n.Config().Timeout(); got != 0 {
		t.Fatalf("client timeout = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func assertMethod(t *testing.T, r *http.Request, expected string) {
	t.Helper()
	if r.Method != expected {
		t.Errorf("method = %s, want %s", r.Method, expected)
	}
}

func assertQuery(t *testing.T, r *http.Request, key, expected string) {
	t.Helper()
	if got := r.URL.Query().Get(key); got != expected {
		t.Errorf("query %s = %q, want %q", key, got, expected)
	}
}

func assertHeader(t *testing.T, r *http.Request, key, expected string) {
	t.Helper()
	if got := r.Header.Get(key); got != expected {
		t.Errorf("header %s = %q, want %q", key, got, expected)
	}
}
