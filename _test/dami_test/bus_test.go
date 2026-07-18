package dami_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/crazy-airhead/aifei-go/dami"
)

// Each test builds its own Bus (New) to avoid interference on the package-level
// default — mirroring Java demos using Dami.newBus().

func TestSendBroadcast(t *testing.T) {
	b := dami.New()
	var got string
	dami.ListenOn(b, "demo.hello", func(e *dami.Event[string]) error {
		got = e.Payload
		return nil
	})
	ev, err := dami.SendOn(b, "demo.hello", "world")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "world" {
		t.Fatalf("got %q", got)
	}
	if !ev.Handled() {
		t.Fatal("expected handled")
	}
}

func TestMultipleListenersBroadcast(t *testing.T) {
	b := dami.New()
	hits := 0
	dami.ListenOn(b, "t", func(e *dami.Event[any]) error { hits++; return nil })
	dami.ListenOn(b, "t", func(e *dami.Event[any]) error { hits++; return nil })
	dami.SendOn[any](b, "t", nil)
	if hits != 2 {
		t.Fatalf("hits=%d want 2", hits)
	}
}

func TestListenerOrder(t *testing.T) {
	b := dami.New()
	var order []int
	dami.ListenOn(b, "t", func(e *dami.Event[any]) error { order = append(order, 2); return nil }, 2)
	dami.ListenOn(b, "t", func(e *dami.Event[any]) error { order = append(order, 1); return nil }, 1)
	dami.ListenOn(b, "t", func(e *dami.Event[any]) error { order = append(order, 3); return nil }, 3)
	dami.SendOn[any](b, "t", nil)
	if want := []int{1, 2, 3}; !slices.Equal(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
}

func TestUnlisten(t *testing.T) {
	b := dami.New()
	called := 0
	un := dami.ListenOn(b, "t", func(e *dami.Event[any]) error { called++; return nil })
	dami.SendOn[any](b, "t", nil)
	un()
	dami.SendOn[any](b, "t", nil)
	if called != 1 {
		t.Fatalf("called=%d want 1", called)
	}
}

func TestUnlistenAll(t *testing.T) {
	b := dami.New()
	dami.ListenOn(b, "t", func(e *dami.Event[any]) error { return nil })
	dami.ListenOn(b, "t", func(e *dami.Event[any]) error { return nil })
	b.UnlistenAll("t")
	ev, _ := dami.SendOn[any](b, "t", nil)
	if ev.Handled() {
		t.Fatal("expected not handled after UnlistenAll")
	}
}

func TestHandledAndFallback(t *testing.T) {
	b := dami.New()
	// No listener → fallback runs, Handled false.
	fb := false
	ev, _ := dami.SendOn(b, "nope", "x", func(p string) { fb = true })
	if ev.Handled() {
		t.Fatal("expected not handled")
	}
	if !fb {
		t.Fatal("expected fallback to run")
	}

	// With listener → no fallback, Handled true.
	dami.ListenOn(b, "y", func(e *dami.Event[string]) error { return nil })
	fb2 := false
	ev2, _ := dami.SendOn(b, "y", "x", func(p string) { fb2 = true })
	if !ev2.Handled() {
		t.Fatal("expected handled")
	}
	if fb2 {
		t.Fatal("fallback must not run when handled")
	}
}

func TestAttachCooperation(t *testing.T) {
	b := dami.New()
	var seen string
	dami.ListenOn(b, "t", func(e *dami.Event[any]) error { // writer, index 1
		e.AttachMap()["name"] = "noear"
		return nil
	}, 1)
	dami.ListenOn(b, "t", func(e *dami.Event[any]) error { // reader, index 2
		seen = e.AttachMap()["name"].(string)
		return nil
	}, 2)
	dami.SendOn[any](b, "t", nil)
	if seen != "noear" {
		t.Fatalf("seen=%q", seen)
	}
}

func TestErrorPropagationStopsRemaining(t *testing.T) {
	b := dami.New()
	reached := false
	dami.ListenOn(b, "t", func(e *dami.Event[any]) error { return errors.New("boom") }, 1)
	dami.ListenOn(b, "t", func(e *dami.Event[any]) error { reached = true; return nil }, 2)
	ev, err := dami.SendOn[any](b, "t", nil)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err=%v", err)
	}
	if reached {
		t.Fatal("second listener must not run after first error")
	}
	if !ev.Handled() {
		t.Fatal("should still be handled — a listener matched")
	}
}

func TestTypedPayload(t *testing.T) {
	type User struct{ ID int }
	b := dami.New()
	var got User
	dami.ListenOn(b, "u", func(e *dami.Event[User]) error { got = e.Payload; return nil })
	dami.SendOn(b, "u", User{ID: 7})
	if got.ID != 7 {
		t.Fatalf("got=%v", got)
	}
}

func TestPayloadTypeMismatchPanics(t *testing.T) {
	// Sender and listener disagree on P → runtime assertion panics, just as Java
	// throws ClassCastException. This is the documented runtime-type convention.
	b := dami.New()
	dami.ListenOn(b, "t", func(e *dami.Event[int]) error { return nil })
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on payload type mismatch")
		}
	}()
	dami.SendOn(b, "t", "not-an-int")
}

func TestEmptyTopicPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on empty topic")
		}
	}()
	dami.SendOn(dami.New(), "", "x")
}

func TestPackageDefaultBus(t *testing.T) {
	// Unique topic isolates this from other tests sharing the default bus.
	topic := "test.package.default.bus"
	var got int
	un := dami.Listen(topic, func(e *dami.Event[int]) error { got = e.Payload; return nil })
	defer un()
	ev, err := dami.Send(topic, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 || !ev.Handled() {
		t.Fatalf("got=%d handled=%v", got, ev.Handled())
	}
}
