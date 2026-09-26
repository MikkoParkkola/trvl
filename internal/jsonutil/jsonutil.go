// Package jsonutil walks decoded JSON without panicking on a missing path.
package jsonutil

import "encoding/json"

// NavigateArray safely walks a nested []any structure by index.
// It returns nil if any segment is not an array or is out of bounds.
func NavigateArray(v any, indices ...int) any {
	current := v
	for _, idx := range indices {
		arr, ok := current.([]any)
		if !ok || idx < 0 || idx >= len(arr) {
			return nil
		}
		current = arr[idx]
	}
	return current
}

// StringValue returns the underlying string, or "" for non-strings.
func StringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ToFloat converts common decoded JSON number forms into float64.
func ToFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// ToInt converts common decoded JSON number forms into int.
func ToInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i)
		}
		if f, err := n.Float64(); err == nil {
			return int(f)
		}
	}
	return 0
}
