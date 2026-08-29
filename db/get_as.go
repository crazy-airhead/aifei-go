package db

import (
	"fmt"
	"reflect"
	"time"
)

// This file hosts the Go 1.27 generic-method typed accessors (see
// docs/arch/generic-methods.md). Before Go allowed type parameters on
// methods, every typed read had to be split into a per-type method family
// (GetStr/GetInt/...) or a package-level helper with the receiver passed as
// the first argument (RowAs/KvAs). GetAs/GetAsE are the unified entries;
// the old families remain and share the same loose-conversion semantics.

// GetAs returns a field value converted to T with the loose semantics of the
// GetStr/GetInt family: nil, missing and unconvertible values all yield the
// zero T (dispatched to the To* converters, so numbers and strings coerce
// across kinds, exactly like GetStr("age") stringifies 48). Use GetAsE when
// dirty data must surface as an error.
func (r *Row) GetAs[T any](field string) T {
	return asLoose[T](r.Get(field))
}

// GetAsE is the strict GetAs, following the GetTimeE convention: NULL/missing
// yields (zero, nil) — a missing value, not a dirty one — while a value that
// cannot become T without silent coercion (e.g. "abc" into int) is an error.
func (r *Row) GetAsE[T any](field string) (T, error) {
	return asStrict[T](r.Get(field))
}

// GetAsDefault returns a field value converted to T, falling back to def when
// the field is nil (mirrors the *Default family).
func (r *Row) GetAsDefault[T any](field string, def T) T {
	v := r.Get(field)
	if v == nil {
		return def
	}
	return asLoose[T](v)
}

// GetAs returns a value converted to T with the loose semantics of
// Kv.GetStr/GetInt: nil and unconvertible values yield the zero T.
func (k Kv) GetAs[T any](key string) T {
	return asLoose[T](k[key])
}

// GetAsE is the strict Kv.GetAs, following the GetTimeE convention: nil
// yields (zero, nil), while a value that cannot become T without silent
// coercion is an error.
func (k Kv) GetAsE[T any](key string) (T, error) {
	return asStrict[T](k[key])
}

// asLoose converts v to T via the To* converter family (total functions that
// never fail), keyed by T's dynamic type through a pointer type switch.
// Non-scalar Ts fall back to a direct type assertion and the zero T on
// mismatch.
func asLoose[T any](v interface{}) T {
	var out T
	switch p := any(&out).(type) {
	case *string:
		*p = ToString(v)
	case *int:
		*p = ToInt(v)
	case *int64:
		*p = ToInt64(v)
	case *float64:
		*p = ToFloat64(v)
	case *bool:
		*p = ToBool(v)
	case *time.Time:
		*p = ToTime(v)
	case *[]byte:
		if b, ok := v.([]byte); ok {
			*p = b
		} else if s, ok := v.(string); ok {
			*p = []byte(s)
		}
	default:
		if t, ok := v.(T); ok {
			out = t
		}
	}
	return out
}

// asStrict converts v to T rejecting silent coercion: same-type values pass
// untouched; cross-representation conversions are allowed only where the To*
// family reads a live representation (numeric width conversions, string
// from []byte, time via ToTimeE). Everything else is an error.
func asStrict[T any](v interface{}) (T, error) {
	var out T
	if v == nil {
		return out, nil
	}
	if t, ok := v.(T); ok {
		return t, nil
	}
	switch p := any(&out).(type) {
	case *int:
		if n, ok := strictNumber(v); ok {
			*p = int(n)
			return out, nil
		}
	case *int64:
		if n, ok := strictNumber(v); ok {
			*p = n
			return out, nil
		}
	case *float64:
		if n, ok := strictNumber(v); ok {
			*p = float64(n)
			return out, nil
		}
	case *string:
		if b, ok := v.([]byte); ok {
			*p = string(b)
			return out, nil
		}
	case *time.Time:
		if t, err := ToTimeE(v); err == nil {
			*p = t
			return out, nil
		}
	}
	var zero T
	return zero, fmt.Errorf("db: cannot convert %T to %s", v, reflect.TypeFor[T]())
}

// strictNumber reports whether v is one of Go's numeric kinds (no string
// parsing — that would be silent coercion) and returns its int64 value.
func strictNumber(v interface{}) (int64, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return int64(rv.Float()), true
	}
	return 0, false
}
