package http_test

import (
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/crazy-airhead/aifei-go/nami"
	http "github.com/crazy-airhead/aifei-go/nami/channel/http"
)

type asUser struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestGetObjectAs(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Write([]byte(`{"name":"test","count":42}`))
	}))
	defer srv.Close()

	n := newClient(t, http.New())
	n.Action(nami.MethodGet).URL(srv.URL).Call(nil, nil, nil)

	v, err := n.GetObjectAs[asUser]()
	if err != nil {
		t.Fatalf("GetObjectAs failed: %v", err)
	}
	if v.Name != "test" || v.Count != 42 {
		t.Errorf("GetObjectAs = %+v, want {test 42}", v)
	}

	// Pointer-out GetObject and value-out GetObjectAs agree — this also
	// covers decoding twice: BodyAsString frees the raw bytes after the
	// first read, so the second decode must rely on the cached string.
	var w asUser
	if err := n.GetObject(&w); err != nil || w != v {
		t.Errorf("GetObject = (%+v, %v), want same as GetObjectAs %+v", w, err, v)
	}
}
