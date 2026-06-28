package kafka

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/config"
)

const testYAML = `
kafka:
  default: cluster1
  clusters:
    cluster1:
      brokers:
        - localhost:9092
        - localhost:9094
      clientId: my-app
      producer:
        acks: one
        compression: zstd
        lingerMs: 50
      consumer:
        groupId: app-group
        offsetReset: earliest
        balancer: roundRobin
        autoCommit:
          enable: false
    cluster2:
      brokers: [localhost:9093]
      sasl:
        mechanism: scram-sha-512
        user: alice
        password: secret
      tls:
        enabled: true
        caFile: /etc/ca.pem
        insecureSkipVerify: true
`

func setGlobalConfig(t *testing.T, yaml string) {
	t.Helper()
	p := config.NewProps()
	if err := p.LoadYAMLBytes([]byte(yaml)); err != nil {
		t.Fatalf("load yaml: %v", err)
	}
	config.SetProps(p)
	t.Cleanup(func() { config.SetProps(config.NewProps()) })
}

func TestLoadConfig(t *testing.T) {
	setGlobalConfig(t, testYAML)
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Default != "cluster1" {
		t.Errorf("default: want cluster1, got %q", cfg.Default)
	}
	if len(cfg.Clusters) != 2 {
		t.Fatalf("clusters: want 2, got %d", len(cfg.Clusters))
	}

	c1 := cfg.Clusters["cluster1"]
	if len(c1.Brokers) != 2 || c1.Brokers[0] != "localhost:9092" {
		t.Errorf("cluster1 brokers wrong: %v", c1.Brokers)
	}
	if c1.ClientID != "my-app" {
		t.Errorf("clientId: want my-app, got %q", c1.ClientID)
	}
	if c1.Producer == nil || c1.Producer.Acks != "one" || c1.Producer.Compression != "zstd" || c1.Producer.LingerMs != 50 {
		t.Errorf("producer wrong: %+v", c1.Producer)
	}
	if c1.Consumer == nil || c1.Consumer.GroupID != "app-group" || c1.Consumer.OffsetReset != "earliest" || c1.Consumer.Balancer != "roundRobin" {
		t.Errorf("consumer wrong: %+v", c1.Consumer)
	}
	if c1.Consumer.AutoCommit == nil || c1.Consumer.AutoCommit.Enable {
		t.Errorf("autocommit.enable: want false, got %+v", c1.Consumer.AutoCommit)
	}

	c2 := cfg.Clusters["cluster2"]
	if c2.SASL == nil || c2.SASL.Mechanism != "scram-sha-512" || c2.SASL.User != "alice" {
		t.Errorf("sasl wrong: %+v", c2.SASL)
	}
	if c2.TLS == nil || !c2.TLS.Enabled || c2.TLS.CAFile != "/etc/ca.pem" || !c2.TLS.InsecureSkipVerify {
		t.Errorf("tls wrong: %+v", c2.TLS)
	}
}

func TestLoadConfigEmpty(t *testing.T) {
	// No global config: LoadConfig returns an empty Config (no error), which
	// NewManager then rejects.
	setGlobalConfig(t, "")
	cfg, err := LoadConfig("kafka")
	if err != nil {
		t.Fatalf("LoadConfig on empty: %v", err)
	}
	if cfg.Default != "" || len(cfg.Clusters) != 0 {
		t.Errorf("want empty config, got %+v", cfg)
	}
}
