// Package kafka_test holds integration tests for the kafka plugin against an
// in-memory franz-go kfake broker, mirroring _test/cache_test (miniredis). It
// verifies produce→consume round-trips, multi-cluster routing, and the
// at-least-once consumer semantics.
package kafka_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/plugins/kafka"
	"github.com/twmb/franz-go/pkg/kfake"
)

const waitTimeout = 15 * time.Second

// newCluster starts an in-memory kfake broker with the given topics (1
// partition each) and returns it plus a Manager whose single cluster points at
// it. The Manager is configured with offsetReset=earliest so consumers always
// read from the beginning (deterministic).
func newCluster(t *testing.T, topics ...string) (*kfake.Cluster, *kafka.Manager, kafka.Client) {
	t.Helper()
	cluster := kfake.MustCluster(kfake.SeedTopics(1, topics...))
	t.Cleanup(cluster.Close)

	mgr, err := kafka.NewManager(&kafka.Config{
		Default: "c",
		Clusters: map[string]kafka.ClusterConfig{
			"c": {
				Brokers: cluster.ListenAddrs(),
				Consumer: &kafka.ConsumerConfig{
					GroupID:     "g",
					OffsetReset: "earliest",
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return cluster, mgr, mgr.Default()
}

func TestProduceConsumeRoundTrip(t *testing.T) {
	_, _, c := newCluster(t, "orders")
	ctx := context.Background()

	// Produce three messages with keys + a header.
	for i := 0; i < 3; i++ {
		msg := kafka.NewMessageWithKey("orders", []byte("k"), []byte(fmt.Sprintf("v%d", i))).
			WithHeader("n", []byte{byte('0' + i)})
		if err := c.ProduceSync(ctx, msg); err != nil {
			t.Fatalf("ProduceSync %d: %v", i, err)
		}
	}

	got := make(chan *kafka.Message, 8)
	sub, err := c.Subscribe(ctx, []string{"orders"}, func(ctx context.Context, m *kafka.Message) error {
		got <- m
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	var values, headers []string
	for i := 0; i < 3; i++ {
		select {
		case m := <-got:
			values = append(values, string(m.Value))
			if len(m.Headers) > 0 {
				headers = append(headers, m.Headers[0].Key+":"+string(m.Headers[0].Value))
			}
		case <-time.After(waitTimeout):
			t.Fatalf("timeout waiting for message %d; got %v", i, values)
		}
	}

	sort.Strings(values)
	if want := []string{"v0", "v1", "v2"}; !reflect.DeepEqual(values, want) {
		t.Errorf("values: got %v want %v", values, want)
	}
	if len(headers) != 3 || headers[0] != "n:0" {
		t.Errorf("headers not preserved: %v", headers)
	}
}

func TestAtLeastOnceRedelivery(t *testing.T) {
	_, _, c := newCluster(t, "work")
	ctx := context.Background()

	if err := c.ProduceSync(ctx, kafka.NewMessage("work", []byte("fail-first"))); err != nil {
		t.Fatalf("ProduceSync: %v", err)
	}

	// First consumer: handler always errors → the record is never marked for
	// commit, so its offset stays uncommitted.
	failAttempts := make(chan struct{}, 4)
	sub1, err := c.Subscribe(ctx, []string{"work"}, func(ctx context.Context, m *kafka.Message) error {
		failAttempts <- struct{}{}
		return errors.New("boom")
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	select {
	case <-failAttempts:
	case <-time.After(waitTimeout):
		t.Fatal("timeout: failing handler never invoked")
	}
	if err := sub1.Close(); err != nil {
		t.Fatalf("sub1.Close: %v", err)
	} // commits nothing (nothing marked)

	// Second consumer, same group, earliest reset → offset 0 is redelivered.
	got := make(chan string, 1)
	sub2, err := c.Subscribe(ctx, []string{"work"}, func(ctx context.Context, m *kafka.Message) error {
		got <- string(m.Value)
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe2: %v", err)
	}
	defer sub2.Close()

	select {
	case v := <-got:
		if v != "fail-first" {
			t.Errorf("redelivered value: got %q want fail-first", v)
		}
	case <-time.After(waitTimeout):
		t.Fatal("timeout: offset 0 was not redelivered (at-least-once broken)")
	}
}

func TestMultipleClusters(t *testing.T) {
	ca := kfake.MustCluster(kfake.SeedTopics(1, "t"))
	t.Cleanup(ca.Close)
	cb := kfake.MustCluster(kfake.SeedTopics(1, "t"))
	t.Cleanup(cb.Close)

	mgr, err := kafka.NewManager(&kafka.Config{
		Default: "a",
		Clusters: map[string]kafka.ClusterConfig{
			"a": {Brokers: ca.ListenAddrs(), Consumer: &kafka.ConsumerConfig{GroupID: "ga", OffsetReset: "earliest"}},
			"b": {Brokers: cb.ListenAddrs(), Consumer: &kafka.ConsumerConfig{GroupID: "gb", OffsetReset: "earliest"}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	ctx := context.Background()
	if err := mgr.Instance("a").ProduceSync(ctx, kafka.NewMessage("t", []byte("from-a"))); err != nil {
		t.Fatalf("produce a: %v", err)
	}
	if err := mgr.Instance("b").ProduceSync(ctx, kafka.NewMessage("t", []byte("from-b"))); err != nil {
		t.Fatalf("produce b: %v", err)
	}

	// Each cluster must only see its own message — confirms routing.
	for name, want := range map[string]string{"a": "from-a", "b": "from-b"} {
		got := make(chan string, 1)
		sub, err := mgr.Instance(name).Subscribe(ctx, []string{"t"}, func(ctx context.Context, m *kafka.Message) error {
			got <- string(m.Value)
			return nil
		})
		if err != nil {
			t.Fatalf("subscribe %s: %v", name, err)
		}
		select {
		case v := <-got:
			if v != want {
				t.Errorf("cluster %s: got %q want %q (routing leak?)", name, v, want)
			}
		case <-time.After(waitTimeout):
			t.Fatalf("cluster %s: timeout waiting for %q", name, want)
		}
		sub.Close()
	}
}

func TestSubscribeRequiresGroup(t *testing.T) {
	cluster := kfake.MustCluster(kfake.SeedTopics(1, "t"))
	defer cluster.Close()

	// Consumer config present but no group id.
	mgr, err := kafka.NewManager(&kafka.Config{
		Default: "c",
		Clusters: map[string]kafka.ClusterConfig{
			"c": {Brokers: cluster.ListenAddrs(), Consumer: &kafka.ConsumerConfig{OffsetReset: "earliest"}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	if _, err := mgr.Default().Subscribe(context.Background(), []string{"t"}, func(context.Context, *kafka.Message) error { return nil }); err == nil {
		t.Error("want error when consumer.groupId is empty")
	}
}
