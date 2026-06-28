package cache

import "testing"

func TestBuildRedisClient(t *testing.T) {
	// ring (addrs map) and single-node (addr) are constructed lazily; neither
	// connects, so no live Redis is required.
	ring, err := buildRedisClient(RedisConfig{Addrs: map[string]string{"s1": "127.0.0.1:6379"}})
	if err != nil || ring == nil {
		t.Fatalf("ring: err=%v", err)
	}
	cli, err := buildRedisClient(RedisConfig{Addr: "127.0.0.1:6379"})
	if err != nil || cli == nil {
		t.Fatalf("client: err=%v", err)
	}
	if _, err := buildRedisClient(RedisConfig{}); err == nil {
		t.Fatal("expected error for empty redis config")
	}
}
