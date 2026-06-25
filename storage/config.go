package storage

import (
	"fmt"

	"github.com/crazy-airhead/aifei-go/config"
)

// Config holds storage configuration: a default bucket name and the per-bucket
// settings.
type Config struct {
	Default string                  `yaml:"default"`
	Buckets map[string]BucketConfig `yaml:"buckets"`
}

// BucketConfig holds the settings for a single bucket.
type BucketConfig struct {
	// Driver selects the backend: "local", "s3". When empty the
	// driver is inferred from Endpoint (http(s) -> s3, else local).
	Driver string `yaml:"driver"`
	// Endpoint is the local root directory (local) or the service URL (s3).
	Endpoint string `yaml:"endpoint"`
	// Region/RegionID apply to the s3 backend; RegionID is a Java-compatible alias.
	Region   string `yaml:"region"`
	RegionID string `yaml:"regionId"`
	// AccessKey/SecretKey authenticate the s3 backend.
	AccessKey string `yaml:"accessKey"`
	SecretKey string `yaml:"secretKey"`
	// Bucket optionally overrides the bucket name; it defaults to the map key.
	Bucket string `yaml:"bucket"`
	// AutoCreateBucket creates the bucket on first use when true (s3 only).
	AutoCreateBucket bool `yaml:"autoCreateBucket"`
}

// resolvedRegion returns Region, falling back to the Java-style RegionID alias.
func (b BucketConfig) resolvedRegion() string {
	if b.Region != "" {
		return b.Region
	}
	return b.RegionID
}

// LoadConfig reads storage configuration from props under prefix (empty
// defaults to "storage"). The buckets subtree is bound via YAML round-trip.
func LoadConfig(props *config.Props, prefix string) (*Config, error) {
	if prefix == "" {
		prefix = "storage"
	}
	if props == nil {
		return &Config{}, nil
	}
	cfg := &Config{Default: props.GetStr(prefix + ".default")}
	if err := props.SubBind(prefix+".buckets", &cfg.Buckets); err != nil {
		return nil, fmt.Errorf("storage: bind buckets: %w", err)
	}
	return cfg, nil
}
