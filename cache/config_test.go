package cache

import (
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/config"
)

func TestLoadConfig(t *testing.T) {
	old := saveGlobal()
	defer config.SetProps(old)

	props := config.NewProps()
	yaml := []byte(`
cache:
  default: user
  instances:
    user:
      type: both
      ttl: 3600
      codec: msgpack
      keyPrefix: app
      local: { driver: freecache, size: 268435456, ttl: 60 }
      remote:
        ttl: 7200
        redis:
          addr: "127.0.0.1:6379"
          password: secret
          db: 1
          poolSize: 20
      refresh: { duration: 60, concurrency: 4, stopAfter: 600 }
      syncLocal: true
    session:
      type: local
      local: { driver: tinylfu, size: 10000, ttl: 1800 }
    counter:
      remote: { ttl: 60, redis: { addr: "127.0.0.1:6379", db: 2 } }
`)
	if err := props.LoadYAMLBytes(yaml); err != nil {
		t.Fatalf("LoadYAMLBytes: %v", err)
	}
	config.SetProps(props)

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Default != "user" {
		t.Errorf("Default = %q, want user", cfg.Default)
	}
	if len(cfg.Instances) != 3 {
		t.Fatalf("Instances = %d, want 3", len(cfg.Instances))
	}

	user := cfg.Instances["user"]
	if user.Type != "both" || user.TTL != 3600 || user.Codec != "msgpack" || user.KeyPrefix != "app" {
		t.Errorf("user instance = %+v", user)
	}
	if user.Local == nil || user.Local.Driver != "freecache" || user.Local.Size != 268435456 || user.Local.TTL != 60 {
		t.Errorf("user.Local = %+v", user.Local)
	}
	if user.Remote == nil || user.Remote.TTL != 7200 ||
		user.Remote.Redis.Addr != "127.0.0.1:6379" ||
		user.Remote.Redis.Password != "secret" ||
		user.Remote.Redis.DB != 1 || user.Remote.Redis.PoolSize != 20 {
		t.Errorf("user.Remote = %+v", user.Remote)
	}
	if user.Refresh == nil || user.Refresh.Duration != 60 ||
		user.Refresh.Concurrency != 4 || user.Refresh.StopAfter != 600 {
		t.Errorf("user.Refresh = %+v", user.Refresh)
	}
	if !user.SyncLocal {
		t.Error("user.SyncLocal = false, want true")
	}
	if got := user.remoteTTL(); got != 7200*time.Second {
		t.Errorf("user.remoteTTL() = %v, want 7200s", got)
	}
	if got := user.prefixedName("user"); got != "app:user" {
		t.Errorf("user.prefixedName = %q, want app:user", got)
	}

	sess := cfg.Instances["session"]
	if sess.Local == nil || sess.Local.Driver != "tinylfu" || sess.Local.Size != 10000 {
		t.Errorf("session.Local = %+v", sess.Local)
	}

	ctr := cfg.Instances["counter"]
	if ctr.Remote == nil || ctr.Remote.TTL != 60 || ctr.Remote.Redis.DB != 2 {
		t.Errorf("counter.Remote = %+v", ctr.Remote)
	}
	if got := ctr.remoteTTL(); got != 60*time.Second {
		t.Errorf("counter.remoteTTL() = %v, want 60s", got)
	}
}

func TestLoadConfigPrefixOverride(t *testing.T) {
	old := saveGlobal()
	defer config.SetProps(old)

	props := config.NewProps()
	if err := props.LoadYAMLBytes([]byte(`mycache: { default: x }`)); err != nil {
		t.Fatal(err)
	}
	config.SetProps(props)

	cfg, err := LoadConfig("mycache")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default != "x" {
		t.Errorf("Default = %q, want x", cfg.Default)
	}
}

// saveGlobal saves the current global config and returns it so the caller
// can restore it later. Since globalProps is unexported, we approximate
// by creating a fresh props — tests set the global explicitly anyway.
func saveGlobal() *config.Props {
	return config.NewProps()
}
