package kafka

import (
	"context"
	"fmt"
	"sync"

	"github.com/crazy-airhead/aifei-go/log"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Client is a Kafka client bound to one cluster. It can produce messages and
// start consumers (Subscribe). Each Client wraps one franz-go producer client;
// each Subscribe creates an independent franz-go consumer client so producing
// and consuming never share a client and can be stopped independently.
//
// For advanced needs not covered by the high-level API (transactions, manual
// commits, seeks, admin operations), KgoClient returns the underlying
// *kgo.Client.
type Client interface {
	// Name returns the cluster/instance name.
	Name() string

	// ProduceSync produces one or more messages and returns once all are
	// acknowledged (or the context expires / a produce fails). It returns the
	// first erroring record's error.
	ProduceSync(ctx context.Context, msgs ...*Message) error

	// Produce produces a single message asynchronously and returns once it is
	// queued. The promise (if non-nil) is invoked when the record is
	// acknowledged (nil err) or fails; because Produce is fire-and-forget at
	// the call site, all errors surface through the promise. Mirrors franz-go's
	// Produce.
	Produce(ctx context.Context, msg *Message, promise Promise)

	// Flush blocks until all buffered records have been flushed (acked or
	// failed), or the context expires.
	Flush(ctx context.Context) error

	// Subscribe starts a background consumer for topics, invoking handler for
	// each fetched record. The returned Subscription must be Closed when done.
	// Subscribe requires the cluster to have a consumer config with a group id.
	Subscribe(ctx context.Context, topics []string, handler Handler) (*Subscription, error)

	// Close stops every Subscription started on this client (committing marked
	// offsets) and closes the producer client.
	Close() error

	// KgoClient returns the underlying franz-go producer client for advanced
	// use. Callers that use it become coupled to franz-go.
	KgoClient() *kgo.Client
}

// kgoClient implements Client, wrapping a franz-go producer *kgo.Client and the
// cluster config (needed to build consumer clients on Subscribe). It tracks the
// Subscriptions it started so Close can stop them.
type kgoClient struct {
	name string
	cfg  ClusterConfig
	cl   *kgo.Client
	log  log.Logger

	mu   sync.Mutex
	subs []*Subscription
}

// newClient builds a producer Client for one cluster from its configuration.
// Because franz-go dials lazily, this does not connect until the first request.
func newClient(name string, cfg ClusterConfig, logger log.Logger) (*kgoClient, error) {
	base, err := seedAndAuth(cfg)
	if err != nil {
		return nil, fmt.Errorf("kafka: cluster %q: %w", name, err)
	}
	opts := append(base, buildProducerOpts(cfg.Producer)...)
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: cluster %q: %w", name, err)
	}
	if logger == nil {
		logger = log.Default()
	}
	return &kgoClient{name: name, cfg: cfg, cl: cl, log: logger}, nil
}

// Name implements Client.
func (c *kgoClient) Name() string { return c.name }

// ProduceSync implements Client.
func (c *kgoClient) ProduceSync(ctx context.Context, msgs ...*Message) error {
	if len(msgs) == 0 {
		return nil
	}
	recs := make([]*kgo.Record, 0, len(msgs))
	for _, m := range msgs {
		recs = append(recs, toRecord(m))
	}
	if err := c.cl.ProduceSync(ctx, recs...).FirstErr(); err != nil {
		return fmt.Errorf("kafka: produce: %w", err)
	}
	return nil
}

// Produce implements Client.
func (c *kgoClient) Produce(ctx context.Context, msg *Message, promise Promise) {
	rec := toRecord(msg)
	var p func(*kgo.Record, error)
	if promise != nil {
		p = func(r *kgo.Record, err error) { promise(fromRecord(r), err) }
	}
	c.cl.Produce(ctx, rec, p)
}

// Flush implements Client.
func (c *kgoClient) Flush(ctx context.Context) error {
	return c.cl.Flush(ctx)
}

// Subscribe implements Client. It builds a dedicated consumer client (separate
// from the producer client) for the configured group, runs the poll loop in a
// goroutine, and tracks the Subscription for cleanup.
func (c *kgoClient) Subscribe(ctx context.Context, topics []string, handler Handler) (*Subscription, error) {
	cc := c.cfg.Consumer
	if cc == nil {
		return nil, fmt.Errorf("kafka: cluster %q has no consumer config", c.name)
	}
	if cc.GroupID == "" {
		return nil, fmt.Errorf("kafka: cluster %q consumer.groupId is required to subscribe", c.name)
	}
	base, err := seedAndAuth(c.cfg)
	if err != nil {
		return nil, err
	}
	opts := append(base, buildConsumerOpts(cc, topics)...)
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("kafka: cluster %q consumer: %w", c.name, err)
	}
	subCtx, cancel := context.WithCancel(ctx)
	sub := &Subscription{cl: cl, cancel: cancel, done: make(chan struct{}), log: c.log}
	go sub.loop(subCtx, handler)

	c.mu.Lock()
	c.subs = append(c.subs, sub)
	c.mu.Unlock()
	return sub, nil
}

// Close implements Client: stop every Subscription, then close the producer
// client.
func (c *kgoClient) Close() error {
	c.mu.Lock()
	subs := c.subs
	c.subs = nil
	c.mu.Unlock()
	for _, s := range subs {
		if err := s.Close(); err != nil {
			c.log.Warn("kafka: close subscription on %q: %v", c.name, err)
		}
	}
	c.cl.Close()
	return nil
}

// KgoClient implements Client.
func (c *kgoClient) KgoClient() *kgo.Client { return c.cl }
