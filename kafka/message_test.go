package kafka

import (
	"bytes"
	"testing"
)

func TestNewMessage(t *testing.T) {
	m := NewMessage("orders", []byte("v"))
	if m.Topic != "orders" || m.Key != nil || string(m.Value) != "v" {
		t.Fatalf("unexpected message: %+v", m)
	}
	mk := NewMessageWithKey("orders", []byte("k"), []byte("v"))
	if string(mk.Key) != "k" {
		t.Fatalf("key not set: %+v", mk)
	}
}

func TestMessageWithHeader(t *testing.T) {
	m := NewMessage("t", nil).WithHeader("h1", []byte("v1")).WithHeader("h2", []byte("v2"))
	if len(m.Headers) != 2 || m.Headers[0].Key != "h1" || string(m.Headers[1].Value) != "v2" {
		t.Fatalf("headers wrong: %+v", m.Headers)
	}
}

func TestRecordRoundTrip(t *testing.T) {
	src := NewMessageWithKey("t", []byte("k"), []byte("v")).
		WithHeader("h1", []byte("hv"))
	r := toRecord(src)
	if r.Topic != "t" || !bytes.Equal(r.Key, []byte("k")) || !bytes.Equal(r.Value, []byte("v")) {
		t.Fatalf("record fields wrong: %+v", r)
	}
	if len(r.Headers) != 1 || r.Headers[0].Key != "h1" || string(r.Headers[0].Value) != "hv" {
		t.Fatalf("record headers wrong: %+v", r.Headers)
	}

	// Simulate broker assignment on consume.
	r.Partition = 2
	r.Offset = 99
	out := fromRecord(r)
	if out.Topic != "t" || out.Partition != 2 || out.Offset != 99 {
		t.Fatalf("consumed metadata wrong: %+v", out)
	}
	if !bytes.Equal(out.Key, []byte("k")) || !bytes.Equal(out.Value, []byte("v")) {
		t.Fatalf("consumed payload wrong: %+v", out)
	}
	if len(out.Headers) != 1 || out.Headers[0].Key != "h1" {
		t.Fatalf("consumed headers wrong: %+v", out.Headers)
	}
}

func TestToRecordNoHeaders(t *testing.T) {
	// A message with no headers must not allocate a record Headers slice.
	r := toRecord(&Message{Topic: "t"})
	if r.Headers != nil {
		t.Fatalf("expected nil headers, got %v", r.Headers)
	}
}
