package json

import "encoding/json"

// Marshal serializes a value to JSON bytes.
func Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// MarshalIndent serializes a value to indented JSON bytes.
func MarshalIndent(v interface{}, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

// Unmarshal deserializes JSON bytes into a value.
func Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// MarshalString serializes a value to JSON string.
func MarshalString(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// UnmarshalString deserializes a JSON string into a value.
func UnmarshalString(s string, v interface{}) error {
	return json.Unmarshal([]byte(s), v)
}

// ToJSON serializes a value to JSON string (alias for MarshalString).
func ToJSON(v interface{}) string {
	return MarshalString(v)
}

// Parse deserializes JSON bytes into a fresh T — the value-returning
// counterpart of Unmarshal's pointer-out form (which exists because
// encoding/json predates generics; there was no way to offer T directly
// through this wrapper's non-generic API until now).
//
//	var cfg MyConfig
//	if err := json.Unmarshal(data, &cfg); err != nil { ... }
//
// becomes:
//
//	cfg, err := json.Parse[MyConfig](data)
func Parse[T any](data []byte) (T, error) {
	var t T
	err := json.Unmarshal(data, &t)
	return t, err
}

// ParseString deserializes a JSON string into a fresh T.
func ParseString[T any](s string) (T, error) {
	return Parse[T]([]byte(s))
}
