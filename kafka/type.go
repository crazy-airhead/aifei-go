package kafka

import "strings"

// defaultClusterName is the cluster name used when none is configured, mirroring
// cache.defaultCacheName / storage.defaultBucketName.
const defaultClusterName = "kafka"

// Acks selects the producer's required acknowledgements.
type Acks string

const (
	// AcksNone requires no acknowledgement (fire and forget).
	AcksNone Acks = "none"
	// AcksOne requires the leader to acknowledge.
	AcksOne Acks = "one"
	// AcksAll requires all in-sync replicas to acknowledge (default).
	AcksAll Acks = "all"
)

// Compression selects the producer batch compression codec.
type Compression string

const (
	CompressionNone   Compression = "none"
	CompressionGzip   Compression = "gzip"
	CompressionSnappy Compression = "snappy" // default
	CompressionLz4    Compression = "lz4"
	CompressionZstd   Compression = "zstd"
)

// SASLMechanism selects a SASL authentication mechanism.
type SASLMechanism string

const (
	SASLPlain       SASLMechanism = "plain"
	SASLScramSHA256 SASLMechanism = "scram-sha-256"
	SASLScramSHA512 SASLMechanism = "scram-sha-512"
)

// OffsetReset selects the behaviour when a consumer group has no committed
// offset (or the committed offset is out of range).
type OffsetReset string

const (
	OffsetEarliest OffsetReset = "earliest"
	OffsetLatest   OffsetReset = "latest" // default
	OffsetNone     OffsetReset = "none"
)

// Balancer selects the consumer group partition balancer.
type Balancer string

const (
	BalancerRoundRobin        Balancer = "roundRobin"
	BalancerRange             Balancer = "range"
	BalancerSticky            Balancer = "sticky"
	BalancerCooperativeSticky Balancer = "cooperativeSticky" // default
)

// lower trims and lowercases a config string for lenient matching.
func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
