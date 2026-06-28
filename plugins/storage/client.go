package storage

import (
	"errors"
	"time"
)

// ErrUnsupported is returned by operations a backend cannot fulfill, e.g.
// LocalClient.TempURL.
var ErrUnsupported = errors.New("storage: operation not supported")

// Client is the storage abstraction over a single bucket. Each implementation
// is bucket-scoped: the bucket is fixed when the client is constructed, so the
// methods only take a key.
type Client interface {
	// Exists reports whether an object exists. Absent objects return
	// (false, nil); errors are returned only for real failures.
	Exists(key string) (bool, error)

	// TempURL returns a short-lived URL for downloading an object. Backends
	// that cannot pre-sign (local storage) return ErrUnsupported.
	TempURL(key string, ttl time.Duration) (string, error)

	// Get fetches an object as a Media. Absent objects return (nil, nil); the
	// caller must close the returned Media.
	Get(key string) (*Media, error)

	// Put stores a Media under key.
	Put(key string, media *Media) (*PutResult, error)

	// Delete removes a single object. Removing a missing object is not an error.
	Delete(key string) error

	// DeleteBatch removes multiple objects. Per-key failures are collected in
	// the returned BatchResult (with a nil error); only hard failures return an
	// error.
	DeleteBatch(keys []string) (*BatchResult, error)
}

// PutResult describes the outcome of a successful Put. It carries the
// identifying information the Java version returned inside its Ret map.
type PutResult struct {
	Driver string // "local" or "s3"
	Bucket string
	Parent string // local: absolute parent directory; s3: empty
	Key    string
}

// BatchResult describes the outcome of a DeleteBatch.
type BatchResult struct {
	// Partial is true when at least one key could not be removed.
	Partial bool
	// Errors holds the per-key failures, in insertion order.
	Errors []KeyError
}

// KeyError pairs a key with the error that affected it.
type KeyError struct {
	Key string
	Err error
}
