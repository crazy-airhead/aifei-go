package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type txcTestKey struct{}

// TestSetInContextInjectsCtx verifies the transparent-propagation injection
// path: setInContext replaces the request context of an *In (via the embedded
// HttpContext.SetContext) so that a service method's in.Context() observes the
// tx context installed by TxInterceptor. The real transaction behavior is
// covered by _example/db_sqlite_test (TransactionCtx); this test pins the
// injection glue that connects them.
func TestSetInContextInjectsCtx(t *testing.T) {
	in := NewIn(httptest.NewRequest(http.MethodGet, "/", nil))

	txCtx := context.WithValue(in.Context(), txcTestKey{}, "tx")
	setInContext(in, txCtx)

	got := in.Context()
	if got != txCtx {
		t.Fatalf("setInContext did not install the tx ctx: got %#v want %#v", got, txCtx)
	}
	if v, ok := got.Value(txcTestKey{}).(string); !ok || v != "tx" {
		t.Fatalf("injected ctx value not visible via in.Context(): got %v", got.Value(txcTestKey{}))
	}

	// An Input that does not implement ctxSetter (e.g. a test fixture) must not
	// panic and is simply left untouched.
	setInContext(bareInput{}, txCtx)
}

// bareInput is an aifei.Input that intentionally does NOT implement ctxSetter,
// exercising the no-op fallback branch of setInContext.
type bareInput struct{}

func (bareInput) Has(string) bool                         { return false }
func (bareInput) PathPara(int) string                     { return "" }
func (bareInput) PathParaByName(string) string            { return "" }
func (bareInput) Param(string) string                     { return "" }
func (bareInput) GetStr(string, ...string) string         { return "" }
func (bareInput) GetInt(string, ...int) int               { return 0 }
func (bareInput) GetInt64(string, ...int64) int64         { return 0 }
func (bareInput) GetFloat64(string, ...float64) float64   { return 0 }
func (bareInput) GetBool(string, ...bool) bool            { return false }
func (bareInput) GetBean(interface{}, ...string) error    { return nil }
func (bareInput) GetMap(...string) map[string]interface{} { return nil }
func (bareInput) Context() context.Context                { return context.Background() }
func (bareInput) Header(string) string                    { return "" }
func (bareInput) Path() string                            { return "" }
func (bareInput) Body() []byte                            { return nil }
