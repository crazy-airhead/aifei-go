package cache

import "testing"

func TestCacheTypeOf(t *testing.T) {
	tests := []struct {
		name                string
		typ                 string
		hasLocal, hasRemote bool
		want                CacheType
	}{
		{"explicit local", "local", false, false, CacheLocal},
		{"explicit remote", "remote", true, true, CacheRemote},
		{"explicit both", "both", true, true, CacheBoth},
		{"explicit upper", "LOCAL", false, false, CacheLocal},
		{"infer both", "", true, true, CacheBoth},
		{"infer remote", "", false, true, CacheRemote},
		{"infer local", "", true, false, CacheLocal},
		{"infer local when none", "", false, false, CacheLocal},
		{"unknown type inferred", "weird", true, true, CacheBoth},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cacheTypeOf(tt.typ, tt.hasLocal, tt.hasRemote); got != tt.want {
				t.Errorf("cacheTypeOf(%q, %v, %v) = %q, want %q",
					tt.typ, tt.hasLocal, tt.hasRemote, got, tt.want)
			}
		})
	}
}
