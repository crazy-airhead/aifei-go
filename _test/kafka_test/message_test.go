package kafka_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/plugins/kafka"
)

func TestNewMessage(t *testing.T) {
	m := kafka.NewMessage("orders", []byte("v"))
	if m.Topic != "orders" || m.Key != nil || string(m.Value) != "v" {
		t.Fatalf("unexpected message: %+v", m)
	}
	mk := kafka.NewMessageWithKey("orders", []byte("k"), []byte("v"))
	if string(mk.Key) != "k" {
		t.Fatalf("key not set: %+v", mk)
	}
}

func TestMessageWithHeader(t *testing.T) {
	m := kafka.NewMessage("t", nil).WithHeader("h1", []byte("v1")).WithHeader("h2", []byte("v2"))
	if len(m.Headers) != 2 || m.Headers[0].Key != "h1" || string(m.Headers[1].Value) != "v2" {
		t.Fatalf("headers wrong: %+v", m.Headers)
	}
}
