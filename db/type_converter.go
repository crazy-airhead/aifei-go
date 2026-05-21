package db

import (
	"fmt"
	"strconv"
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

// ToBool converts interface{} to bool.
func ToBool(v interface{}) bool {
	if v == nil {
		return false
	}
	switch n := v.(type) {
	case bool:
		return n
	case int:
		return n != 0
	case int64:
		return n != 0
	case float64:
		return n != 0
	case string:
		return n == "true" || n == "1" || n == "yes" || n == "TRUE" || n == "YES"
	}
	return false
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
	}
	return fmt.Sprintf("%v", v)
}

// ToTime converts interface{} to time.Time.
func ToTime(v interface{}) time.Time {
	if v == nil {
		return time.Time{}
	}
	switch n := v.(type) {
	case time.Time:
		return n
	case string:
		for _, layout := range []string{
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05Z07:00",
			"2006-01-02",
			time.RFC3339,
			time.RFC3339Nano,
		} {
			if t, err := time.Parse(layout, n); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}
