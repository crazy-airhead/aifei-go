package db_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/db"
)

// TestToBool covers the full numeric/string type matrix (对照 Java 828ce12:
// Number 先于 String,整数族全类型覆盖)。
func TestToBool(t *testing.T) {
	cases := []struct {
		in   interface{}
		want bool
	}{
		// booleans pass through
		{true, true},
		{false, false},
		// full integer family: 0 → false, non-zero → true
		{int(0), false}, {int(2), true},
		{int8(0), false}, {int8(2), true},
		{int16(0), false}, {int16(2), true},
		{int32(0), false}, {int32(2), true},
		{int64(0), false}, {int64(2), true},
		{uint(0), false}, {uint(2), true},
		{uint8(0), false}, {uint8(2), true},
		{uint16(0), false}, {uint16(2), true},
		{uint32(0), false}, {uint32(2), true},
		{uint64(0), false}, {uint64(2), true},
		{float32(0), false}, {float32(0.5), true},
		{float64(0), false}, {float64(0.5), true},
		// strings: strconv.ParseBool forms (case-insensitive) + legacy yes/y/on
		{"true", true}, {"True", true}, {"TRUE", true},
		{"false", false}, {"False", false},
		{"1", true}, {"0", false},
		{"t", true}, {"f", false}, {"T", true},
		{"yes", true}, {"YES", true}, {"Yes", true},
		{"y", true}, {"Y", true},
		{"on", true}, {"ON", true},
		{"no", false}, {"off", false},
		{"", false}, {"junk", false},
		// bytes behave like strings
		{[]byte("true"), true}, {[]byte("0"), false},
		// nil and non-boolean-ish types → false
		{nil, false},
	}
	for _, c := range cases {
		if got := db.ToBool(c.in); got != c.want {
			t.Errorf("ToBool(%#v) = %v, want %v", c.in, got, c.want)
		}
	}
}
