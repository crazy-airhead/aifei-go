package kafka

import (
	"context"
	"errors"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Handler processes a consumed message.
//
// Return nil to acknowledge the message: its offset is marked for commit, so it
// will not be redelivered (modulo rebalances / crashes before the commit lands).
//
// Return a non-nil error to leave the offset uncommitted. Because the poll loop
// always advances the fetch position, the message is NOT retried immediately;
// it is redelivered on the next rebalance or consumer restart (and on any
// rebalance that revokes and re-acquires the partition). This is at-least-once
// delivery. For immediate in-process retry or seek-on-error, use the
// Subscription.KgoClient() escape hatch.
type Handler func(ctx context.Context, msg *Message) error

// Subscription is a running consumer started by Client.Subscribe. Close stops
// the poll loop, commits any marked offsets, and closes the consumer client.
type Subscription struct {
	cl     *kgo.Client
	cancel context.CancelFunc
	done   chan struct{}
	log    log.Logger
}

// KgoClient returns the underlying franz-go consumer client for advanced use
// (manual commits via CommitRecords, seek-on-error, transactions, admin, etc.).
func (s *Subscription) KgoClient() *kgo.Client { return s.cl }

// Close stops the consumer loop, commits marked offsets, and closes the client.
// It waits up to 10s for the loop to exit and 10s for the final commit, so it
// is safe to call during shutdown.
func (s *Subscription) Close() error {
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		s.log.Warn("kafka: subscription did not stop within 10s")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.cl.CommitMarkedOffsets(ctx); err != nil {
		s.log.Warn("kafka: commit marked offsets: %v", err)
	}
	s.cl.Close()
	return nil
}

// loop runs the consume poll loop until ctx is cancelled or the client closes.
// Successfully handled records are marked for commit; handler errors are logged
// and left unmarked (at-least-once).
func (s *Subscription) loop(ctx context.Context, handler Handler) {
	defer close(s.done)
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		fetches := s.cl.PollFetches(ctx)
		if fetches.IsClientClosed() {
			return
		}
		for _, fe := range fetches.Errors() {
			// A cancelled context or closed client on shutdown injects a fake
			// partition error; that is expected, not a warning.
			if ctx.Err() != nil || errors.Is(fe.Err, context.Canceled) || errors.Is(fe.Err, kgo.ErrClientClosed) {
				continue
			}
			s.log.Warn("kafka: consume %s/%d: %v", fe.Topic, fe.Partition, fe.Err)
		}
		iter := fetches.RecordIter()
		for !iter.Done() {
			r := iter.Next()
			if err := handler(ctx, fromRecord(r)); err != nil {
				s.log.Warn("kafka: handler %s offset %d: %v (not committed)", r.Topic, r.Offset, err)
				continue
			}
			s.cl.MarkCommitRecords(r)
		}
	}
}
