package json_test

import (
	"reflect"
	"testing"

	"github.com/crazy-airhead/aifei-go/nami"
	json "github.com/crazy-airhead/aifei-go/nami/coder/json"
)

type jsonItem struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestEncoder(t *testing.T) {
	e := json.NewEncoder()
	if e.Enctype() != nami.JSONValue {
		t.Fatalf("enctype = %q, want %q", e.Enctype(), nami.JSONValue)
	}
	if e.BodyRequired() {
		t.Fatal("BodyRequired should be false for JSON")
	}
	b, err := e.Encode(map[string]any{"a": 1})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if string(b) != `{"a":1}` {
		t.Fatalf("encoded = %q", b)
	}
	// Encoder pretreatment is a no-op; ensure it does not panic.
	e.Pretreatment(&nami.Context{Headers: map[string]string{}})
}

func TestDecoderEmptyBody(t *testing.T) {
	d := json.NewDecoder()
	v, err := d.Decode(nami.NewResult(200, nil), reflect.TypeFor[jsonItem]())
	if err != nil || v != nil {
		t.Fatalf("empty body: v=%v err=%v", v, err)
	}
}

func TestDecoderNull(t *testing.T) {
	d := json.NewDecoder()
	v, err := d.Decode(nami.NewResult(200, []byte("null")), reflect.TypeFor[map[string]any]())
	if err != nil || v != nil {
		t.Fatalf("null body: v=%v err=%v", v, err)
	}
}

func TestDecoderObject(t *testing.T) {
	d := json.NewDecoder()
	v, err := d.Decode(nami.NewResult(200, []byte(`{"name":"test","count":7}`)), reflect.TypeFor[jsonItem]())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	item, ok := v.(jsonItem)
	if !ok {
		t.Fatalf("type = %T", v)
	}
	if item.Name != "test" || item.Count != 7 {
		t.Fatalf("decoded = %+v", item)
	}
}

func TestDecoderSlice(t *testing.T) {
	d := json.NewDecoder()
	v, err := d.Decode(nami.NewResult(200, []byte(`[{"name":"a","count":1}]`)), reflect.TypeFor[[]jsonItem]())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	items, ok := v.([]jsonItem)
	if !ok {
		t.Fatalf("type = %T", v)
	}
	if len(items) != 1 || items[0].Name != "a" {
		t.Fatalf("decoded = %+v", items)
	}
}

// Non-JSON text decoded into a string type returns the raw string.
func TestDecoderStringRawFallback(t *testing.T) {
	d := json.NewDecoder()
	v, err := d.Decode(nami.NewResult(200, []byte("hello world")), reflect.TypeFor[string]())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if s, ok := v.(string); !ok || s != "hello world" {
		t.Fatalf("decoded = %v", v)
	}
}

func TestDecoderPointer(t *testing.T) {
	d := json.NewDecoder()
	v, err := d.Decode(nami.NewResult(200, []byte(`{"name":"x","count":3}`)), reflect.TypeFor[*jsonItem]())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	p, ok := v.(*jsonItem)
	if !ok || p == nil || p.Name != "x" || p.Count != 3 {
		t.Fatalf("decoded = %v", v)
	}
}

func TestDecoderPretreatment(t *testing.T) {
	d := json.NewDecoder()
	ctx := &nami.Context{Headers: map[string]string{}}
	d.Pretreatment(ctx)
	if ctx.Headers[nami.HeaderAccept] != nami.JSONValue {
		t.Fatalf("Accept header = %q", ctx.Headers[nami.HeaderAccept])
	}
}

// The package init() registers a JSON encoder/decoder for application/json.
func TestPackageRegistration(t *testing.T) {
	if nami.GetEncoder(nami.JSONValue) == nil {
		t.Fatal("json encoder not registered")
	}
	if nami.GetDecoder(nami.JSONValue) == nil {
		t.Fatal("json decoder not registered")
	}
}
