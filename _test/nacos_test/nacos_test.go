package nacos_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/plugins/nacos"
)

func TestNewNamiUpstreamNoConfigReturnsEmpty(t *testing.T) {
	nacos.SetDefaultConfig(nil)
	up := nacos.NewNamiUpstream("svc")
	if got := up(); got != "" {
		t.Fatalf("upstream without default config = %q, want empty", got)
	}
}
