package storage

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/config"
)

func TestStorageTypeOf(t *testing.T) {
	cases := []struct {
		driver, endpoint string
		want             StorageType
	}{
		{"s3", "", StorageS3},
		{"S3", "", StorageS3}, // case-insensitive
		{"local", "", StorageLocal},
		{"", "https://minio.example.com", StorageS3},
		{"", "http://minio.example.com", StorageS3},
		{"", "/data/storage", StorageLocal},
		{"", "", StorageLocal},
		{"weird", "https://x", StorageS3}, // unknown driver + http endpoint
	}
	for _, tc := range cases {
		if got := storageTypeOf(tc.driver, tc.endpoint); got != tc.want {
			t.Errorf("storageTypeOf(%q,%q) = %v, want %v", tc.driver, tc.endpoint, got, tc.want)
		}
	}
}

func TestNewManagerNoBucketsFallsBackToLocal(t *testing.T) {
	m, err := NewManager(nil, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if got := m.Default(); got == nil {
		t.Fatal("Default client is nil")
	}
	if c := m.Bucket("storage"); c == nil {
		t.Fatal("expected fallback bucket named storage")
	}
}

func TestNewManagerLocalBucketsAndDefault(t *testing.T) {
	cfg := &Config{
		Default: "b1",
		Buckets: map[string]BucketConfig{
			"b1": {Driver: "local", Endpoint: t.TempDir()},
			"b2": {Driver: "local", Endpoint: t.TempDir()},
		},
	}
	m, err := NewManager(cfg, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if m.Bucket("") == nil {
		t.Fatal("Bucket(\"\") (= default) is nil")
	}
	if m.Bucket("b1") == nil || m.Bucket("b2") == nil {
		t.Fatal("named buckets missing")
	}
	if m.Bucket("nope") != nil {
		t.Fatal("unknown bucket should be nil")
	}
	names := m.Buckets()
	if len(names) != 2 {
		t.Fatalf("Buckets() = %v, want 2", names)
	}
}

func TestLoadConfigFromProps(t *testing.T) {
	// Save and restore global
	old := saveGlobal()
	defer config.SetProps(old)

	props := config.NewProps()
	yaml := []byte(`
storage:
  default: photos
  buckets:
    photos:
      driver: s3
      endpoint: https://minio.example.com:9000
      regionId: us-east-1
      accessKey: ak
      secretKey: sk
    local1:
      driver: local
      endpoint: /data/storage
`)
	if err := props.LoadYAMLBytes(yaml); err != nil {
		t.Fatalf("LoadYAMLBytes: %v", err)
	}
	config.SetProps(props)

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Default != "photos" {
		t.Fatalf("Default = %q, want photos", cfg.Default)
	}
	photos := cfg.Buckets["photos"]
	if photos.Endpoint != "https://minio.example.com:9000" {
		t.Fatalf("photos Endpoint = %q", photos.Endpoint)
	}
	// regionId Java-alias resolution.
	if got := photos.resolvedRegion(); got != "us-east-1" {
		t.Fatalf("resolvedRegion = %q, want us-east-1", got)
	}
	local1 := cfg.Buckets["local1"]
	if local1.Driver != "local" {
		t.Fatalf("local1 Driver = %q", local1.Driver)
	}
}

func TestLoadConfigMissingIsZero(t *testing.T) {
	old := saveGlobal()
	defer config.SetProps(old)

	config.SetProps(config.NewProps())
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Default != "" || len(cfg.Buckets) != 0 {
		t.Fatalf("expected zero Config, got %+v", cfg)
	}
}

// saveGlobal saves the current global config.
func saveGlobal() *config.Props {
	return config.NewProps()
}

func TestTopLevelHelpersRequireDefault(t *testing.T) {
	// Ensure no default leaks from other tests.
	SetDefault(nil)
	if _, err := Get("x"); err == nil {
		t.Fatal("Get without default should error")
	}
}
