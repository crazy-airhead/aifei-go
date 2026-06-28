package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	sasl "github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// seedAndAuth builds the options shared by producer and consumer clients for a
// cluster: seed brokers, client id, SASL, and TLS. Auth/TLS errors surface here
// (at construction) rather than on first dial.
func seedAndAuth(cfg ClusterConfig) ([]kgo.Opt, error) {
	opts := []kgo.Opt{kgo.SeedBrokers(cfg.Brokers...)}
	if cfg.ClientID != "" {
		opts = append(opts, kgo.ClientID(cfg.ClientID))
	}
	if cfg.SASL != nil {
		mech, err := buildSASLMechanism(cfg.SASL)
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.SASL(mech))
	}
	if cfg.TLS != nil && cfg.TLS.Enabled {
		tlsc, err := buildTLSConfig(cfg.TLS)
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.DialTLSConfig(tlsc))
	}
	return opts, nil
}

// buildSASLMechanism maps a SASLConfig to a franz-go sasl.Mechanism.
func buildSASLMechanism(c *SASLConfig) (sasl.Mechanism, error) {
	switch lower(c.Mechanism) {
	case "", "plain":
		return plain.Auth{User: c.User, Pass: c.Password}.AsMechanism(), nil
	case "scram-sha-256", "scram-sha256", "scram256":
		return scram.Auth{User: c.User, Pass: c.Password}.AsSha256Mechanism(), nil
	case "scram-sha-512", "scram-sha512", "scram512":
		return scram.Auth{User: c.User, Pass: c.Password}.AsSha512Mechanism(), nil
	default:
		return nil, fmt.Errorf("kafka: unsupported sasl mechanism %q (want plain, scram-sha-256, or scram-sha-512)", c.Mechanism)
	}
}

// buildTLSConfig builds a *tls.Config from files. CAFile roots the broker cert;
// CertFile/KeyFile enable mTLS (both required together).
func buildTLSConfig(c *TLSConfig) (*tls.Config, error) {
	tlsc := &tls.Config{InsecureSkipVerify: c.InsecureSkipVerify}
	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("kafka: read ca file %q: %w", c.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("kafka: no certificates parsed from ca file %q", c.CAFile)
		}
		tlsc.RootCAs = pool
	}
	if c.CertFile != "" || c.KeyFile != "" {
		if c.CertFile == "" || c.KeyFile == "" {
			return nil, fmt.Errorf("kafka: tls requires both certFile and keyFile")
		}
		pair, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("kafka: load key pair: %w", err)
		}
		tlsc.Certificates = []tls.Certificate{pair}
	}
	return tlsc, nil
}

// buildProducerOpts maps a ProducerConfig to franz-go producer options. A nil
// config yields the defaults (acks=all, compression=snappy).
func buildProducerOpts(p *ProducerConfig) []kgo.Opt {
	if p == nil {
		p = &ProducerConfig{}
	}
	opts := []kgo.Opt{
		kgo.RequiredAcks(acksOf(p.Acks)),
		kgo.ProducerBatchCompression(compressionOf(p.Compression)...),
	}
	if p.LingerMs > 0 {
		opts = append(opts, kgo.ProducerLinger(time.Duration(p.LingerMs)*time.Millisecond))
	}
	if p.MaxAttempts > 0 {
		opts = append(opts, kgo.RecordRetries(p.MaxAttempts))
	}
	return opts
}

// buildConsumerOpts maps a ConsumerConfig (plus the topics to consume) to
// franz-go consumer options. With auto-commit enabled it sets AutoCommitMarks
// so MarkCommitRecords controls committed offsets (at-least-once).
func buildConsumerOpts(cc *ConsumerConfig, topics []string) []kgo.Opt {
	reset := offsetResetOf(cc.OffsetReset)
	opts := []kgo.Opt{
		kgo.ConsumerGroup(cc.GroupID),
		kgo.ConsumeTopics(topics...),
		// Set both the initial position and the out-of-range reset so that, e.g.,
		// offsetReset=latest actually starts at the end for a fresh group
		// (franz-go's default start offset is earliest).
		kgo.ConsumeStartOffset(reset),
		kgo.ConsumeResetOffset(reset),
		kgo.Balancers(balancerOf(cc.Balancer)),
	}
	ac := cc.AutoCommit
	if ac == nil {
		ac = &AutoCommitConfig{Enable: true, IntervalMs: 5000}
	}
	if !ac.Enable {
		opts = append(opts, kgo.DisableAutoCommit())
	} else {
		// AutoCommitMarks makes MarkCommitRecords control what is committed,
		// giving at-least-once semantics: records whose handler errored are not
		// marked, so their offsets are not committed and are redelivered on the
		// next rebalance or consumer restart.
		opts = append(opts, kgo.AutoCommitMarks())
		if d := time.Duration(ac.IntervalMs) * time.Millisecond; d > 0 {
			opts = append(opts, kgo.AutoCommitInterval(d))
		}
	}
	return opts
}

// acksOf maps a config string to a franz-go Acks (default AllISRAcks).
func acksOf(s string) kgo.Acks {
	switch lower(s) {
	case "none", "0":
		return kgo.NoAck()
	case "one", "leader", "1":
		return kgo.LeaderAck()
	default: // "", "all", "all_isr", "isr", "-1"
		return kgo.AllISRAcks()
	}
}

// compressionOf maps a config string to compression codec preference (default
// Snappy).
func compressionOf(s string) []kgo.CompressionCodec {
	switch lower(s) {
	case "none":
		return []kgo.CompressionCodec{kgo.NoCompression()}
	case "gzip":
		return []kgo.CompressionCodec{kgo.GzipCompression()}
	case "lz4":
		return []kgo.CompressionCodec{kgo.Lz4Compression()}
	case "zstd":
		return []kgo.CompressionCodec{kgo.ZstdCompression()}
	default: // "", "snappy"
		return []kgo.CompressionCodec{kgo.SnappyCompression()}
	}
}

// offsetResetOf maps a config string to a franz-go Offset (default AtEnd =
// latest).
func offsetResetOf(s string) kgo.Offset {
	switch lower(s) {
	case "earliest", "start", "beginning":
		return kgo.NewOffset().AtStart()
	case "none":
		return kgo.NewOffset().AtCommitted()
	default: // "", "latest", "end"
		return kgo.NewOffset().AtEnd()
	}
}

// balancerOf maps a config string to a franz-go GroupBalancer (default
// CooperativeSticky).
func balancerOf(s string) kgo.GroupBalancer {
	switch lower(s) {
	case "roundrobin", "round-robin":
		return kgo.RoundRobinBalancer()
	case "range":
		return kgo.RangeBalancer()
	case "sticky":
		return kgo.StickyBalancer()
	default: // "", "cooperativesticky", "cooperative-sticky"
		return kgo.CooperativeStickyBalancer()
	}
}
