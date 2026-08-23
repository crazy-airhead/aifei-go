package db

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// ToInt converts interface{} to int.
func ToInt(v interface{}) int {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case int32:
		return int(n)
	case int16:
		return int(n)
	case int8:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	case bool:
		if n {
			return 1
		}
		return 0
	}
	return 0
}

// ToInt64 converts interface{} to int64.
func ToInt64(v interface{}) int64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case int32:
		return int64(n)
	case int16:
		return int64(n)
	case int8:
		return int64(n)
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	case bool:
		if n {
			return 1
		}
		return 0
	}
	return 0
}

// ToFloat64 converts interface{} to float64.
func ToFloat64(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case float64:
		return n
	case float32:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	case bool:
		if n {
			return 1
		}
		return 0
	}
	return 0
}

// ToBool converts interface{} to bool. Numbers are checked before strings
// (对照 Java 828ce12: Number 先于 String), so any numeric type maps 0→false,
// non-zero→true; strings use strconv.ParseBool plus the legacy yes/no forms
// (case-insensitive).
func ToBool(v interface{}) bool {
	if n, ok := toBoolNumber(v); ok {
		return n
	}
	switch n := v.(type) {
	case nil:
		return false
	case bool:
		return n
	case string:
		if b, err := strconv.ParseBool(strings.ToLower(n)); err == nil {
			return b
		}
		switch strings.ToLower(n) {
		case "yes", "y", "on":
			return true
		}
		return false
	case []byte:
		return ToBool(string(n))
	}
	// Remaining numeric kinds not covered by toBoolNumber (other float widths).
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() != 0
	}
	return false
}

// toBoolNumber covers the common concrete numeric types without reflection.
func toBoolNumber(v interface{}) (bool, bool) {
	switch n := v.(type) {
	case int:
		return n != 0, true
	case int8:
		return n != 0, true
	case int16:
		return n != 0, true
	case int32:
		return n != 0, true
	case int64:
		return n != 0, true
	case uint:
		return n != 0, true
	case uint8:
		return n != 0, true
	case uint16:
		return n != 0, true
	case uint32:
		return n != 0, true
	case uint64:
		return n != 0, true
	case float32:
		return n != 0, true
	case float64:
		return n != 0, true
	}
	return false, false
}

// ToString converts interface{} to string.
func ToString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch n := v.(type) {
	case string:
		return n
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(n)
	case []byte:
		return string(n)
	case time.Time:
		return n.Format(TimeFormat)
	}
	return fmt.Sprintf("%v", v)
}

// timeParseLayouts are the layouts tried (in order) when converting a string
// to time.Time. RFC3339Nano also covers RFC3339 and offset-less ISO forms.
var timeParseLayouts = []string{
	TimeFormat,       // "2006-01-02 15:04:05" (SQL DATETIME / TimeFormat)
	time.RFC3339Nano, // "2006-01-02T15:04:05[.frac]Z07:00" (JSON / PG)
	"2006-01-02",     // date-only (SQL DATE)
	"15:04:05",       // time-only (SQL TIME)
}

// ToTime converts interface{} to time.Time. A nil value yields the zero time
// with a nil error (a NULL column is a missing value, not a dirty one); a
// string matching no known layout is an error — dirty data never silently
// becomes the zero time.
func ToTime(v interface{}) (time.Time, error) {
	switch n := v.(type) {
	case nil:
		return time.Time{}, nil
	case time.Time:
		return n, nil
	case string:
		return parseTimeWith(n, timeParseLayouts)
	case []byte:
		return parseTimeWith(string(n), timeParseLayouts)
	}
	return time.Time{}, fmt.Errorf("db: cannot convert %T to time.Time", v)
}

// parseTimeWith parses s with the given layouts (in order).
func parseTimeWith(s string, layouts []string) (time.Time, error) {
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("db: cannot parse %q as time (tried layouts: %q)", s, layouts)
}
