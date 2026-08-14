package flow

import (
	"fmt"
	"strconv"
	"strings"
)

// toString renders any scalar value as a string (mirrors Java Object.toString()).
func toString(v any) string { return fmt.Sprintf("%v", v) }

// toFloat64 converts numeric values to float64 (mirrors Java Number.doubleValue()).
func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int8:
		return float64(n)
	case int16:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case uint:
		return float64(n)
	case uint64:
		return float64(n)
	case bool:
		if n {
			return 1
		}
		return 0
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}

// formatLinks renders a slice of links like a Java List toString ("[{...}, {...}]").
func formatLinks(links []*Link) string {
	parts := make([]string, 0, len(links))
	for _, l := range links {
		parts = append(parts, l.String())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// formatMap renders a map like a Java Map toString ("{k=v, k2=v2}").
func formatMap(m map[string]any) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%v=%v", k, v))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
