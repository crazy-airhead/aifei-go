package cache_test

import (
	"context"
	"errors"
	"testing"

	"github.com/crazy-airhead/aifei-go/plugins/cache"
)

func TestTopLevelHelpersRequireDefault(t *testing.T) {
	cache.SetDefault(nil)
	ctx := context.Background()

	if _, err := cache.Get(ctx, "x", new(string)); !errors.Is(err, cache.ErrNoDefault) {
		t.Errorf("Get err = %v, want ErrNoDefault", err)
	}
	if err := cache.Set(ctx, "x", "v"); !errors.Is(err, cache.ErrNoDefault) {
		t.Errorf("Set err = %v, want ErrNoDefault", err)
	}
	if err := cache.Delete(ctx, "x"); !errors.Is(err, cache.ErrNoDefault) {
		t.Errorf("Delete err = %v, want ErrNoDefault", err)
	}
	if cache.Exists(ctx, "x") {
		t.Error("Exists should be false without default")
	}
	loader := func(ctx context.Context) (any, error) { return "v", nil }
	if err := cache.GetOrStore(ctx, "x", new(string), loader); !errors.Is(err, cache.ErrNoDefault) {
		t.Errorf("GetOrStore err = %v, want ErrNoDefault", err)
	}
	if cache.Use("x") != nil {
		t.Error("Use should be nil without default")
	}
}
