package config_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/config"
)

func TestPropsGetAs(t *testing.T) {
	p := config.NewProps()
	p.Set("name", "aifei")
	p.Set("port", 8080)
	p.Set("ratio", 0.875)
	p.Set("enabled", true)
	p.Set("empty", "")

	// Direct typed reads
	if got := p.GetAs[string]("name"); got != "aifei" {
		t.Errorf("GetAs[string](name) = %q", got)
	}
	if got := p.GetAs[int]("port"); got != 8080 {
		t.Errorf("GetAs[int](port) = %d", got)
	}
	if got := p.GetAs[float64]("ratio"); got != 0.875 {
		t.Errorf("GetAs[float64](ratio) = %v", got)
	}
	if got := p.GetAs[bool]("enabled"); got != true {
		t.Errorf("GetAs[bool](enabled) = %v", got)
	}

	// Numeric-family cross conversion mirrors GetInt/GetFloat64
	if got := p.GetAs[float64]("port"); got != 8080 {
		t.Errorf("GetAs[float64](port) = %v, want 8080 (numeric family)", got)
	}
	if got := p.GetAs[int]("ratio"); got != 0 {
		t.Errorf("GetAs[int](ratio) = %d, want 0", got)
	}

	// Missing key: def, else zero T
	if got := p.GetAs[int]("absent", 80); got != 80 {
		t.Errorf("GetAs[int](absent, 80) = %d", got)
	}
	if got := p.GetAs[string]("absent"); got != "" {
		t.Errorf("GetAs[string](absent) = %q, want empty", got)
	}

	// Mismatch falls back to def / zero — GetAs does NOT coerce across kinds
	if got := p.GetAs[int]("name", -1); got != -1 {
		t.Errorf("GetAs[int](name, -1) = %d, want -1 (no coercion)", got)
	}
	if got := p.GetAs[string]("port"); got != "" {
		t.Errorf("GetAs[string](port) = %q, want empty (no coercion)", got)
	}

	// Unlike GetStr, a present empty string is NOT treated as missing
	if got := p.GetAs[string]("empty", "fallback"); got != "" {
		t.Errorf("GetAs[string](empty) = %q, want \"\" (present empty value)", got)
	}
}

func TestPropsGetAsE(t *testing.T) {
	p := config.NewProps()
	p.Set("port", 8080)
	p.Set("ratio", 0.875)
	p.Set("name", "aifei")

	// Missing key is an error for strict config reads
	if _, err := p.GetAsE[int]("absent"); err == nil {
		t.Error("GetAsE[int](absent) should error on missing key")
	}

	// Numeric-family width conversion passes
	if got, err := p.GetAsE[int64]("port"); err != nil || got != 8080 {
		t.Errorf("GetAsE[int64](port) = (%d, %v)", got, err)
	}
	if got, err := p.GetAsE[float64]("port"); err != nil || got != 8080 {
		t.Errorf("GetAsE[float64](port) = (%v, %v)", got, err)
	}

	// Cross-kind would be silent coercion: rejected
	if _, err := p.GetAsE[string]("port"); err == nil {
		t.Error("GetAsE[string](port) should reject numeric→string coercion")
	}
	if _, err := p.GetAsE[int]("name"); err == nil {
		t.Error("GetAsE[int](name) should reject string→int coercion")
	}
}

func TestGlobalGetAs(t *testing.T) {
	orig := config.NewProps()
	orig.Set("app.name", "demo")
	config.SetProps(orig)
	defer config.SetProps(nil)

	if got := config.GetAs[string]("app.name"); got != "demo" {
		t.Errorf("config.GetAs[string] = %q", got)
	}
	if got := config.GetAs[int]("app.missing", 7); got != 7 {
		t.Errorf("config.GetAs[int](missing, 7) = %d", got)
	}
	if got, err := config.GetAsE[string]("app.name"); err != nil || got != "demo" {
		t.Errorf("config.GetAsE[string] = (%q, %v)", got, err)
	}

	// Nil global: def / zero / error
	config.SetProps(nil)
	if got := config.GetAs[int]("any", 9); got != 9 {
		t.Errorf("nil global GetAs with def = %d, want 9", got)
	}
	if got := config.GetAs[int]("any"); got != 0 {
		t.Errorf("nil global GetAs = %d, want 0", got)
	}
	if _, err := config.GetAsE[int]("any"); err == nil {
		t.Error("nil global GetAsE should error")
	}
}
