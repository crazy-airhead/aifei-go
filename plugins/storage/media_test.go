package storage

import (
	"io"
	"strings"
	"testing"
)

func TestOfBytes(t *testing.T) {
	m := OfBytes([]byte("hello"), "text/plain")
	if m.ContentType() != "text/plain" {
		t.Fatalf("ContentType = %q, want text/plain", m.ContentType())
	}
	if m.Size() != 5 {
		t.Fatalf("Size = %d, want 5", m.Size())
	}
	b, err := m.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("Bytes = %q, want hello", b)
	}
}

func TestOfString(t *testing.T) {
	m := OfString("world", "text/plain")
	s, err := m.String()
	if err != nil {
		t.Fatalf("String: %v", err)
	}
	if s != "world" {
		t.Fatalf("String = %q, want world", s)
	}
}

func TestOfFileNameMimeInference(t *testing.T) {
	cases := map[string]string{
		"a.txt":  "text/plain; charset=utf-8",
		"a.json": "application/json",
	}
	for name, want := range cases {
		m := OfFileName(name, strings.NewReader(""))
		if m.ContentType() != want {
			t.Fatalf("%s: ContentType = %q, want %q", name, m.ContentType(), want)
		}
	}
	// Unknown extension falls back to the default content type.
	m := OfFileName("blob.unknownext", strings.NewReader(""))
	if m.ContentType() != defaultContentType {
		t.Fatalf("unknown ext: ContentType = %q, want %q", m.ContentType(), defaultContentType)
	}
}

type closeCounter struct {
	io.Reader
	closed int
}

func (c *closeCounter) Close() error { c.closed++; return nil }

func TestMediaClose(t *testing.T) {
	rc := &closeCounter{Reader: strings.NewReader("x")}
	m := &Media{body: rc}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if rc.closed != 1 {
		t.Fatalf("closed = %d, want 1", rc.closed)
	}

	// Non-closer body: Close is a no-op without panicking.
	m2 := NewMedia(strings.NewReader("y"), "")
	if err := m2.Close(); err != nil {
		t.Fatalf("Close non-closer: %v", err)
	}
}
