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
