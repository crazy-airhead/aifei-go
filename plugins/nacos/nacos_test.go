package nacos

import (
	"strings"
	"testing"
)

func TestGetLocalIP(t *testing.T) {
	ip := getLocalIP()
	if ip == "" {
		t.Fatal("getLocalIP returned empty")
	}
	if !strings.Contains(ip, ".") {
		t.Fatalf("getLocalIP returned non-IPv4-looking value %q", ip)
	}
}

func TestClientKey(t *testing.T) {
	cfg := &Config{
		ServerAddr: "1.2.3.4:8848",
		Namespace:  "ns",
		Username:   "u",
	}
	got := clientKey(cfg)
	want := "1.2.3.4:8848|ns|u"
	if got != want {
		t.Fatalf("clientKey = %q, want %q", got, want)
	}
}

func TestServerConfig(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		sc, err := serverConfig("127.0.0.1:8848")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sc.Port != 8848 {
			t.Fatalf("Port = %d, want 8848", sc.Port)
		}
	})

	t.Run("missing_port", func(t *testing.T) {
		if _, err := serverConfig("127.0.0.1"); err == nil {
			t.Fatal("expected error for address without port")
		}
	})

	t.Run("bad_port", func(t *testing.T) {
		if _, err := serverConfig("127.0.0.1:notaport"); err == nil {
			t.Fatal("expected error for non-numeric port")
		}
	})

	t.Run("bad_addr", func(t *testing.T) {
		if _, err := serverConfig(string([]byte{0x7f, 0x00})); err == nil {
			t.Fatal("expected error for garbage address")
		}
	})
}

func TestClientConfigMapping(t *testing.T) {
	cfg := &Config{
		Namespace: "ns",
		Username:  "u",
		Password:  "p",
	}
	cc := clientConfig(cfg)
	if cc.NamespaceId != "ns" || cc.Username != "u" || cc.Password != "p" {
		t.Fatalf("clientConfig did not map fields correctly: %+v", cc)
	}
	if cc.TimeoutMs != 10000 || cc.LogLevel != "warn" {
		t.Fatalf("clientConfig has unexpected defaults: %+v", cc)
	}
}

func TestPluginDisabled(t *testing.T) {
	p := NewPlugin(nil)
	if err := p.Start(); err != nil {
		t.Fatalf("disabled Start returned error: %v", err)
	}
	if p.registered || p.listening {
		t.Fatal("disabled plugin should not register or listen")
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestNewNamiUpstreamNoConfigReturnsEmpty(t *testing.T) {
	SetDefaultConfig(nil)
	up := NewNamiUpstream("svc")
	if got := up(); got != "" {
		t.Fatalf("upstream without default config = %q, want empty", got)
	}
}
