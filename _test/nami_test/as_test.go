package nami_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/nami"
)

func TestResultAs(t *testing.T) {
	r := nami.NewResult(200, []byte(`{"name":"test","count":42}`))

	v, err := r.As[struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}]()
	if err != nil {
		t.Fatalf("As failed: %v", err)
	}
	if v.Name != "test" {
		t.Errorf("name = %q, want test", v.Name)
	}
	if v.Count != 42 {
		t.Errorf("count = %d, want 42", v.Count)
	}
}

func TestResultAsNon2xx(t *testing.T) {
	r := nami.NewResult(500, []byte(`{"error":"boom"}`))
	if _, err := r.As[map[string]any](); err == nil {
		t.Fatal("expected error for 500 status")
	}
}

func TestResultAsEmptyBody(t *testing.T) {
	r := nami.NewResult(200, []byte(""))
	v, err := r.As[struct{ Name string }]()
	if err != nil {
		t.Fatalf("empty body As = (%v, %v), want zero T and nil error", v, err)
	}
	if v.Name != "" {
		t.Errorf("empty body should leave T at zero, got %+v", v)
	}
}

// GetObjectAs needs a live call through a channel+decoder, which lives in
// channel/http tests (importing the json coder here would perturb the global
// registry order TestManagerRegistry relies on).

