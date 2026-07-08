package elasticsearch

import (
	"fmt"

	"github.com/crazy-airhead/aifei-go/config"
)

// Config holds elasticsearch configuration: a default cluster name and the
// per-cluster settings.
type Config struct {
	Default  string                   `yaml:"default"`
	Clusters map[string]ClusterConfig `yaml:"clusters"`
}

// ClusterConfig describes a single named Elasticsearch cluster.
type ClusterConfig struct {
	// Addresses is the list of ES node URLs (e.g. "http://localhost:9200").
	Addresses []string `yaml:"addresses"`
	// Username for Basic authentication.
	Username string `yaml:"username"`
	// Password for Basic authentication. Requires Username.
	Password string `yaml:"password"`
	// APIKey is a Base64-encoded API key for authentication.
	APIKey string `yaml:"apiKey"`
	// CACert is a path to a PEM-encoded CA certificate file for TLS.
	CACert string `yaml:"caCert"`
	// InsecureSkipVerify disables TLS certificate verification.
	InsecureSkipVerify bool `yaml:"insecureSkipVerify"`
}

// LoadConfig reads elasticsearch configuration from the global config under prefix
// (empty defaults to "elasticsearch"). The clusters subtree is bound via YAML
// round-trip.
func LoadConfig(prefix string) (*Config, error) {
	if prefix == "" {
		prefix = "elasticsearch"
	}
	cfg := &Config{Default: config.GetStr(prefix + ".default")}
	if err := config.SubBind(prefix+".clusters", &cfg.Clusters); err != nil {
		return nil, fmt.Errorf("elasticsearch: bind clusters: %w", err)
	}
	return cfg, nil
}
