package dami

import (
	"errors"
	"testing"
)

func TestInterceptorWrapsListener(t *testing.T) {
	b := New()
	var seq []string
	b.Intercept(0, func(ev eventView, next func() error) error {
		seq = append(seq, "before")
		err := next()
		seq = append(seq, "after")
		return err
	})
	ListenOn(b, "t", func(e *Event[any]) error { seq = append(seq, "listener"); return nil })
	SendOn[any](b, "t", nil)
	want := []string{"before", "listener", "after"}
	if len(seq) != 3 || seq[0] != want[0] || seq[1] != want[1] || seq[2] != want[2] {
		t.Fatalf("seq=%v want=%v", seq, want)
	}
}

func TestInterceptorOrder(t *testing.T) {
	b := New()
	var seq []int
	b.Intercept(2, func(ev eventView, next func() error) error { seq = append(seq, 2); return next() })
	b.Intercept(1, func(ev eventView, next func() error) error { seq = append(seq, 1); return next() })
	ListenOn(b, "t", func(e *Event[any]) error { return nil })
	SendOn[any](b, "t", nil)
	if len(seq) != 2 || seq[0] != 1 || seq[1] != 2 {
		t.Fatalf("seq=%v want [1 2]", seq)
	}
}

func TestInterceptorShortCircuit(t *testing.T) {
	b := New()
	called := false
	b.Intercept(0, func(ev eventView, next func() error) error {
		return errors.New("blocked") // intentionally skip next()
	})
	ListenOn(b, "t", func(e *Event[any]) error { called = true; return nil })
	ev, err := SendOn[any](b, "t", nil)
	if err == nil || err.Error() != "blocked" {
		t.Fatalf("err=%v", err)
	}
	if called {
		t.Fatal("listener must not run when short-circuited")
	}
	if ev.Handled() {
		t.Fatal("handled must stay false when distribution never reached")
	}
}

func TestInterceptorMutatesAttach(t *testing.T) {
	b := New()
	var seen any
	b.Intercept(0, func(ev eventView, next func() error) error {
		ev.viewAttach()["k"] = "v"
		return next()
	})
	ListenOn(b, "t", func(e *Event[any]) error { seen = e.AttachMap()["k"]; return nil })
	SendOn[any](b, "t", nil)
	if seen != "v" {
		t.Fatalf("seen=%v", seen)
	}
}

func TestInterceptorSeesTopicAndPayload(t *testing.T) {
	b := New()
	var topic string
	var payload any
	b.Intercept(0, func(ev eventView, next func() error) error {
		topic = ev.viewTopic()
		payload = ev.viewPayload()
		return next()
	})
	ListenOn(b, "evt", func(e *Event[string]) error { return nil })
	SendOn(b, "evt", "hi")
	if topic != "evt" || payload != "hi" {
		t.Fatalf("topic=%q payload=%v", topic, payload)
	}
}
