package server

import (
	"bytes"
	"io"
	"testing"
)

func TestOutOkFailOf(t *testing.T) {
	if ok := Ok(); !ok.IsOk() || ok.Code() != CodeOK {
		t.Fatalf("Ok: code=%d", ok.Code())
	}
	if ok := Ok("hi"); ok.Msg() != "hi" {
		t.Fatalf("Ok msg: %q", ok.Msg())
	}
	fail := Fail("bad: %d", 7)
	if fail.Code() != CodeFail || fail.Msg() != "bad: 7" {
		t.Fatalf("Fail: code=%d msg=%q", fail.Code(), fail.Msg())
	}
	of := Of(42)
	if of.Data() != 42 || !of.IsOk() {
		t.Fatalf("Of: data=%v", of.Data())
	}
}

func TestOutRedirect(t *testing.T) {
	r := Redirect("http://x/y")
	if r.RedirectURL() != "http://x/y" || r.RedirectStatus() != 0 {
		t.Fatalf("Redirect default: url=%q status=%d", r.RedirectURL(), r.RedirectStatus())
	}
	r = Redirect("/z", 301)
	if r.RedirectStatus() != 301 {
		t.Fatalf("Redirect status: %d", r.RedirectStatus())
	}
}

func TestOutForward(t *testing.T) {
	f := Forward("/next")
	if f.ForwardPath() != "/next" {
		t.Fatalf("ForwardPath: %q", f.ForwardPath())
	}
	// Forward must not leak into the view (the old "forward:" hack).
	if f.View() != "" {
		t.Fatalf("Forward should not set view, got %q", f.View())
	}
}

func TestOutFile(t *testing.T) {
	o := OfFile(func(s *FileSender) { s.SetFileName("a.zip") })
	if o.FileSenderOut() == nil {
		t.Fatal("OfFile: fileSender nil")
	}
}

func TestOutRaw(t *testing.T) {
	data := []byte{1, 2, 3}
	o := OfRaw("image/png", data)
	if o.RawContentType() != "image/png" {
		t.Fatalf("OfRaw ct: %q", o.RawContentType())
	}
	if o.RawSize() != int64(len(data)) {
		t.Fatalf("OfRaw size: %d", o.RawSize())
	}
	got, err := io.ReadAll(o.RawBody())
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("OfRaw body: %v %v", got, err)
	}

	r := OfRawReader("video/mp4", bytes.NewReader(data))
	if r.RawSize() != 0 {
		t.Fatalf("OfRawReader size should be unknown(0), got %d", r.RawSize())
	}
	r.SetRawSize(99)
	if r.RawSize() != 99 {
		t.Fatalf("SetRawSize: %d", r.RawSize())
	}
}

func TestOutHeaders(t *testing.T) {
	o := Ok().SetHeaders((&Headers{}).SetHeader("X-A", "1"))
	if o.HeadersOut() == nil {
		t.Fatal("SetHeaders: nil")
	}
}

func TestOutClear(t *testing.T) {
	o := OfRaw("image/png", []byte("x")).SetForward("/f").SetRedirect("/r")
	o.Clear()
	if o.ForwardPath() != "" || o.RedirectURL() != "" || o.RawBody() != nil {
		t.Fatalf("Clear did not reset intent: %+v", o)
	}
}

func TestOutShouldRollback(t *testing.T) {
	if Ok().ShouldRollback() {
		t.Fatal("Ok should not rollback")
	}
	if !Fail("x").ShouldRollback() {
		t.Fatal("Fail should rollback")
	}
}
