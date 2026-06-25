package storage

import (
	"os"
	"testing"

	"github.com/minio/minio-go/v7"
)

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		in      string
		host    string
		secure  bool
		wantErr bool
	}{
		{"https://minio.example.com:9000", "minio.example.com:9000", true, false},
		{"http://1.2.3.4:9000", "1.2.3.4:9000", false, false},
		{"minio.example.com:9000", "minio.example.com:9000", false, false},
		{"", "", false, true},
	}
	for _, tc := range cases {
		host, secure, err := parseEndpoint(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseEndpoint(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && (host != tc.host || secure != tc.secure) {
			t.Errorf("parseEndpoint(%q) = (%q,%v), want (%q,%v)", tc.in, host, secure, tc.host, tc.secure)
		}
	}
}

func TestIsNotFound(t *testing.T) {
	if isNotFound(nil) {
		t.Fatal("isNotFound(nil) should be false")
	}
	if !isNotFound(errorResponse("NoSuchKey")) {
		t.Fatal("NoSuchKey should be not-found")
	}
	if isNotFound(errorResponse("AccessDenied")) {
		t.Fatal("AccessDenied should not be not-found")
	}
}

// errorResponse builds a minio ErrorResponse with the given code.
func errorResponse(code string) error {
	return minio.ErrorResponse{Code: code, BucketName: "b", Key: "k"}
}

// TestNewS3ClientConstructs verifies a client can be built without network
// access. It does not perform any remote calls.
func TestNewS3ClientConstructs(t *testing.T) {
	c, err := NewS3Client("bucket", S3Options{
		Endpoint:  "https://minio.example.com:9000",
		Region:    "us-east-1",
		AccessKey: "ak",
		SecretKey: "sk",
	}, nil)
	if err != nil {
		t.Fatalf("NewS3Client: %v", err)
	}
	if c == nil || c.client == nil {
		t.Fatal("client not constructed")
	}
}

// TestS3LiveRoundTrip exercises Get/Put/Delete against a real S3/Minio service.
// It is skipped unless STORAGE_S3_ENDPOINT is set.
func TestS3LiveRoundTrip(t *testing.T) {
	endpoint := os.Getenv("STORAGE_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("STORAGE_S3_ENDPOINT not set; skipping live S3 round-trip")
	}
	c, err := NewS3Client("aifei-test", S3Options{
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
	if _, err := c.Put(key, OfString("hello s3", "text/plain")); err != nil {
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
