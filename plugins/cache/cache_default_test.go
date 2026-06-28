package cache

import (
	"context"
	"errors"
	"testing"
)

func TestTopLevelHelpersRequireDefault(t *testing.T) {
	SetDefault(nil)
	ctx := context.Background()

	if _, err := Get(ctx, "x", new(string)); !errors.Is(err, ErrNoDefault) {
		t.Errorf("Get err = %v, want ErrNoDefault", err)
	}
	if err := Set(ctx, "x", "v"); !errors.Is(err, ErrNoDefault) {
		t.Errorf("Set err = %v, want ErrNoDefault", err)
	}
	if err := Delete(ctx, "x"); !errors.Is(err, ErrNoDefault) {
		t.Errorf("Delete err = %v, want ErrNoDefault", err)
	}
	if Exists(ctx, "x") {
		t.Error("Exists should be false without default")
	}
	loader := func(ctx context.Context) (any, error) { return "v", nil }
	if err := GetOrStore(ctx, "x", new(string), loader); !errors.Is(err, ErrNoDefault) {
		t.Errorf("GetOrStore err = %v, want ErrNoDefault", err)
	}
	if Use("x") != nil {
		t.Error("Use should be nil without default")
	}
}
