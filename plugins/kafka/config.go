package kafka

import (
	"fmt"

	"github.com/crazy-airhead/aifei-go/config"
)

// Config holds kafka configuration: a default cluster name and the per-cluster
// settings.
type Config struct {
	Default  string                   `yaml:"default"`
	Clusters map[string]ClusterConfig `yaml:"clusters"`
}

// ClusterConfig describes a single named Kafka cluster. A cluster's brokers and
// authentication are shared by its producer and consumers; the Producer and
// Consumer blocks tune each role independently.
type ClusterConfig struct {
	// Brokers is the list of seed broker host:port addresses.
	Brokers []string `yaml:"brokers"`
	// ClientID is the Kafka client.id used in requests (for server-side logs).
	ClientID string `yaml:"clientId"`
	// SASL configures SASL authentication; non-nil enables it.
	SASL *SASLConfig `yaml:"sasl"`
	// TLS configures TLS; non-nil with Enabled true enables it.
	TLS *TLSConfig `yaml:"tls"`
	// Producer tunes the producer; nil uses sensible defaults.
	Producer *ProducerConfig `yaml:"producer"`
	// Consumer tunes consumers created via Subscribe.
	Consumer *ConsumerConfig `yaml:"consumer"`
}

// SASLConfig configures SASL authentication. Mechanism selects plain,
// scram-sha-256, or scram-sha-512.
type SASLConfig struct {
	Mechanism string `yaml:"mechanism"`
	User      string `yaml:"user"`
	Password  string `yaml:"password"`
}

// TLSConfig configures TLS for broker connections. Enabled must be true to
// activate TLS. CAFile roots the broker certificate; CertFile/KeyFile enable
// mutual TLS (both required together).
type TLSConfig struct {
	Enabled            bool   `yaml:"enabled"`
	CAFile             string `yaml:"caFile"`
	CertFile           string `yaml:"certFile"`
	KeyFile            string `yaml:"keyFile"`
	InsecureSkipVerify bool   `yaml:"insecureSkipVerify"`
}

// ProducerConfig tunes the producer. Empty string fields fall back to defaults:
// acks=all, compression=snappy.
type ProducerConfig struct {
	// Acks: "none", "one", or "all" (default "all").
	Acks string `yaml:"acks"`
	// Compression: "none", "gzip", "snappy", "lz4", "zstd" (default "snappy").
	Compression string `yaml:"compression"`
	// LingerMs is how long to wait for a batch to fill before sending; 0 sends
	// immediately. Mirrors Kafka's linger.ms.
	LingerMs int `yaml:"lingerMs"`
	// MaxAttempts is the number of retries (RecordRetries); 0 leaves the
	// franz-go default (unlimited).
	MaxAttempts int `yaml:"maxAttempts"`
}

// ConsumerConfig tunes consumers started via Subscribe. GroupID is required to
// subscribe.
type ConsumerConfig struct {
	// GroupID is the consumer group id; required to Subscribe.
	GroupID string `yaml:"groupId"`
	// OffsetReset: "earliest", "latest", or "none" (default "latest").
	OffsetReset string `yaml:"offsetReset"`
	// Balancer: "roundRobin", "range", "sticky", or "cooperativeSticky"
	// (default "cooperativeSticky").
	Balancer string `yaml:"balancer"`
	// AutoCommit tunes offset committing; nil defaults to enabled, 5s, with
	// mark-based at-least-once semantics.
	AutoCommit *AutoCommitConfig `yaml:"autoCommit"`
}

// AutoCommitConfig tunes offset auto-committing.
type AutoCommitConfig struct {
	// Enable toggles background auto-committing. When disabled, the consumer
	// never commits automatically — use the KgoClient() escape hatch to commit
	// manually.
	Enable bool `yaml:"enable"`
	// IntervalMs is the auto-commit interval in milliseconds (default 5000).
	IntervalMs int `yaml:"intervalMs"`
}

// LoadConfig reads kafka configuration from the global config under prefix
// (empty defaults to "kafka"). The clusters subtree is bound via YAML round-trip.
func LoadConfig(prefix string) (*Config, error) {
	if prefix == "" {
		prefix = "kafka"
	}
	cfg := &Config{Default: config.GetStr(prefix + ".default")}
	if err := config.SubBind(prefix+".clusters", &cfg.Clusters); err != nil {
		return nil, fmt.Errorf("kafka: bind clusters: %w", err)
	}
	return cfg, nil
}
