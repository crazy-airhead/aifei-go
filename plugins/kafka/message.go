package kafka

import (
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Header is a single Kafka record header. Key mirrors franz-go's
// RecordHeader.Key (a string); Value is arbitrary bytes. Kafka itself does not
// interpret headers — they are opaque metadata carried alongside the value.
type Header struct {
	Key   string
	Value []byte
}

// Message is a Kafka record, used for both producing and consuming.
//
// For producing, set Topic and Value (and optionally Key/Headers/Timestamp).
// Partition is honored only with the ManualPartitioner; with the default
// partitioner it is assigned by the client. Offset is the broker-assigned
// offset, populated only for consumed messages and in async-produce promises.
type Message struct {
	Topic     string
	Partition int32
	Key       []byte
	Value     []byte
	Headers   []Header
	Timestamp time.Time
	// Offset is the broker-assigned offset; only set on consumed messages and
	// in Produce promise callbacks. Ignored when producing.
	Offset int64
}

// Promise is the completion callback for an asynchronously produced message.
// err is nil when the record was acknowledged by the requested number of
// brokers. It mirrors franz-go's per-record produce promise.
type Promise func(msg *Message, err error)

// NewMessage creates a Message for topic with the given value.
func NewMessage(topic string, value []byte) *Message {
	return &Message{Topic: topic, Value: value}
}

// NewMessageWithKey creates a Message with an explicit key, so all messages
// with the same key hash to the same partition (with the default partitioner).
func NewMessageWithKey(topic string, key, value []byte) *Message {
	return &Message{Topic: topic, Key: key, Value: value}
}

// WithHeader appends a header and returns the message for chaining.
func (m *Message) WithHeader(key string, value []byte) *Message {
	m.Headers = append(m.Headers, Header{Key: key, Value: value})
	return m
}

// toRecord converts a Message into a franz-go Record for producing. Key/Value
// slices are shared (not copied); franz-go does not mutate them.
func toRecord(m *Message) *kgo.Record {
	r := &kgo.Record{
		Topic:     m.Topic,
		Partition: m.Partition,
		Key:       m.Key,
		Value:     m.Value,
		Timestamp: m.Timestamp,
	}
	if len(m.Headers) > 0 {
		r.Headers = make([]kgo.RecordHeader, len(m.Headers))
		for i, h := range m.Headers {
			r.Headers[i] = kgo.RecordHeader{Key: h.Key, Value: h.Value}
		}
	}
	return r
}

// fromRecord converts a consumed (or just-produced) franz-go Record into a
// Message, preserving the assigned Partition/Offset/Timestamp.
func fromRecord(r *kgo.Record) *Message {
	m := &Message{
		Topic:     r.Topic,
		Partition: r.Partition,
		Key:       r.Key,
		Value:     r.Value,
		Timestamp: r.Timestamp,
		Offset:    r.Offset,
	}
	if len(r.Headers) > 0 {
		m.Headers = make([]Header, len(r.Headers))
		for i, h := range r.Headers {
			m.Headers[i] = Header{Key: h.Key, Value: h.Value}
		}
	}
	return m
}
