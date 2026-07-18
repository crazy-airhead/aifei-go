package dami_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/dami"
)

type userService struct{}

func (s *userService) GetUserID(name string) int64 { return int64(len(name)) }

func (s *userService) Echo(n int, msg string) (string, error) {
	if n < 0 {
		return "", errors.New("negative")
	}
	return msg, nil
}

func (s *userService) Fire(name string) error { return nil }

func lpcCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Second)
}

func TestLpcCall1(t *testing.T) {
	b := dami.New()
	lpc := dami.NewLpc(b)
	if err := lpc.RegisterProvider("user", &userService{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := lpcCtx()
	defer cancel()
	id, err := dami.Call1[int64](b, ctx, "user.GetUserID", "noear")
	if err != nil {
		t.Fatal(err)
	}
	if id != 5 { // len("noear")
		t.Fatalf("id=%d want 5", id)
	}
}

func TestLpcArgsAndError(t *testing.T) {
	b := dami.New()
	lpc := dami.NewLpc(b)
	if err := lpc.RegisterProvider("user", &userService{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := lpcCtx()
	defer cancel()

	out, err := dami.Call1[string](b, ctx, "user.Echo", 2, "hi")
	if err != nil || out != "hi" {
		t.Fatalf("out=%q err=%v", out, err)
	}

	_, err = dami.Call1[string](b, ctx, "user.Echo", -1, "hi")
	if err == nil || err.Error() != "negative" {
		t.Fatalf("err=%v want negative", err)
	}
}

func TestLpcCall0(t *testing.T) {
	b := dami.New()
	lpc := dami.NewLpc(b)
	if err := lpc.RegisterProvider("user", &userService{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := lpcCtx()
	defer cancel()
	if err := dami.Call0(b, ctx, "user.Fire", "x"); err != nil {
		t.Fatalf("err=%v", err)
	}
}

func TestLpcUnregister(t *testing.T) {
	b := dami.New()
	lpc := dami.NewLpc(b)
	if err := lpc.RegisterProvider("user", &userService{}); err != nil {
		t.Fatal(err)
	}
	lpc.UnregisterProvider(&userService{})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// encodeArgs is unexported; the equivalent positional map uses the argument
	// index as its key (see dami.encodeArgs).
	fut := dami.CallOn[map[string]any, int64](b, "user.GetUserID", map[string]any{"0": "noear"})
	_, err := fut.Get(ctx)
	// No handler after unregister → Future never settles → ctx deadline.
	if err != context.DeadlineExceeded {
		t.Fatalf("err=%v want DeadlineExceeded", err)
	}
}

func TestLpcDuplicateRegister(t *testing.T) {
	b := dami.New()
	lpc := dami.NewLpc(b)
	if err := lpc.RegisterProvider("user", &userService{}); err != nil {
		t.Fatal(err)
	}
	if err := lpc.RegisterProvider("user", &userService{}); err == nil {
		t.Fatal("want duplicate-registration error")
	}
}
