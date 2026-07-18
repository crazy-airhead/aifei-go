package storage_test

import (
	"os"
	"testing"

	"github.com/crazy-airhead/aifei-go/plugins/storage"
)

// TestS3LiveRoundTrip exercises Get/Put/Delete against a real S3/Minio service.
// It is skipped unless STORAGE_S3_ENDPOINT is set.
func TestS3LiveRoundTrip(t *testing.T) {
	endpoint := os.Getenv("STORAGE_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("STORAGE_S3_ENDPOINT not set; skipping live S3 round-trip")
	}
	c, err := storage.NewS3Client("aifei-test", storage.S3Options{
		Endpoint:         endpoint,
		Region:           os.Getenv("STORAGE_S3_REGION"),
		AccessKey:        os.Getenv("STORAGE_S3_ACCESS_KEY"),
		SecretKey:        os.Getenv("STORAGE_S3_SECRET_KEY"),
		AutoCreateBucket: true,
	}, nil)
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}

	key := "round-trip.txt"
	if _, err := c.Put(key, storage.OfString("hello s3", "text/plain")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	defer c.Delete(key)

	m, err := c.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m == nil {
		t.Fatal("Get returned nil")
	}
	defer m.Close()
	s, _ := m.String()
	if s != "hello s3" {
		t.Fatalf("content = %q", s)
	}
}
