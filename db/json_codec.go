package db

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// This file holds the JSON encoding/decoding logic for Row as plain functions.
// Row is passed as a parameter rather than acting as the receiver, keeping the
// codec logic decoupled from the Row type. The json.Marshaler / json.Unmarshaler
// methods on Row (defined in row.go) delegate to marshalRow / unmarshalRow here.

// KeyFormat controls the JSON key format used when marshaling a Row.
type KeyFormat int

const (
	KeyFormatSnake KeyFormat = iota // snake_case (default, backward compatible)
	KeyFormatCamel                  // camelCase
)

// DefaultKeyFormat is the package-level default key format for new Rows.
// Set it to KeyFormatCamel to output camelCase JSON globally.
var DefaultKeyFormat = KeyFormatSnake

// TimeFormat is the layout used to marshal/unmarshal time.Time fields in JSON.
// Default is "2006-01-02 15:04:05" (yyyy-MM-dd HH:mm:ss).
var TimeFormat = time.DateTime

// ---- Encoding: Row → JSON / SQL ----

// marshalRow renders r to JSON. Keys follow r's key format (or DefaultKeyFormat);
// time.Time values are formatted with TimeFormat.
func marshalRow(r *Row) ([]byte, error) {
	if r.data == nil {
		return []byte("null"), nil
	}
	kf := resolveKeyFormat(r)
	converted := make(map[string]interface{}, len(r.data))
	for k, v := range r.data {
		key := k
		if kf == KeyFormatCamel {
			key = snakeToCamel(k)
		}
		converted[key] = formatTimeValue(v)
	}
	return json.Marshal(converted)
}

// formatTimeValue converts time.Time values to formatted strings.
// Other types are returned unchanged.
func formatTimeValue(v interface{}) interface{} {
	if t, ok := v.(time.Time); ok {
		return t.Format(TimeFormat)
	}
	return v
}

// normalizeSQLValue converts complex Go types (maps, slices, structs) to
// JSON string representations suitable for SQL parameter binding.
// []byte values are returned unchanged so that BLOB columns work correctly.
func normalizeSQLValue(v interface{}) interface{} {
	if v == nil {
		return nil
	}

	// []byte is used for BLOB columns — never convert to string.
	if _, ok := v.([]byte); ok {
		return v
	}

	// time.Time must be passed as-is so the SQL driver formats it natively.
	// json.Marshal would produce a quoted RFC 3339 string like
	// `"2026-07-13T17:13:55Z"` (with literal double-quotes), which MySQL
	// rejects as an invalid datetime value.
	if _, ok := v.(time.Time); ok {
		return v
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map, reflect.Slice:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	case reflect.Struct:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	case reflect.Ptr:
		if !rv.IsNil() && rv.Elem().Kind() == reflect.Struct {
			if b, err := json.Marshal(v); err == nil {
				return string(b)
			}
		}
	}

	return v
}

// ---- Decoding: JSON → Row ----

// unmarshalRow parses JSON into r. Input keys are normalized from camelCase to
// snake_case so database column names stay consistent internally regardless of
// the configured key format. String values matching TimeFormat (and common
// fallback layouts) are parsed back to time.Time. Values for columns declared
// as composite types (struct/slice/map/array) are materialized into those types
// via normalizeJSONValue; arrays/objects for string-typed columns are serialized
// back to JSON strings.
func unmarshalRow(r *Row, data []byte) error {
	r.ensureData()
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var fieldTypes map[string]reflect.Type
	if t := GetTableByName(r.table); t != nil {
		fieldTypes = t.FieldTypes
	}

	r.ensureChange()
	for k, v := range raw {
		snakeKey := camelToSnake(k)
		r.data[snakeKey] = normalizeJSONValue(snakeKey, v, fieldTypes)
		r.change[snakeKey] = struct{}{}
	}
	return nil
}

// normalizeJSONValue normalizes a decoded JSON value for storage in r.data:
//   - For integer-typed columns (int, int64, etc.), null/empty-string JSON values
//     are converted to nil so SQL receives NULL instead of an empty string.
//   - For columns whose declared FieldTypes entry is a composite JSON type
//     (struct/slice/map/array, see needsJSONDecode), the incoming value is
//     materialized into that declared type. This makes typed accessors work on
//     rows built from JSON input (via UnmarshalJSON) — not only on rows read
//     from the DB, where DecodeJSONFields does the equivalent on the read path.
//     On a type mismatch it falls through to the default handling below.
//   - For string-typed columns (and rows without table metadata), arrays and
//     objects are serialized back to JSON strings (the opaque-JSON-string
//     convention) so the SQL driver receives a string.
//   - Other scalar fields preserve the original value so the SQL driver can
//     reject it with a clear type-mismatch error.
func normalizeJSONValue(key string, v interface{}, fieldTypes map[string]reflect.Type) interface{} {
	// Handle null values
	if v == nil {
		return nil
	}

	// Handle string values: check for empty string or numeric strings
	if str, ok := v.(string); ok {
		// Empty string handling
		if str == "" {
			// Empty string for integer/numeric types should be nil (SQL NULL)
			if ft, ok := fieldTypes[key]; ok {
				switch ft.Kind() {
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
					reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
					reflect.Float32, reflect.Float64:
					return nil
				}
			}
			// For string columns, keep empty string as-is
			return str
		}

		// Non-empty string: for numeric type columns, try to parse as number
		if ft, ok := fieldTypes[key]; ok {
			switch ft.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				if i, err := strconv.ParseInt(str, 10, 64); err == nil {
					return convertToInt(i, ft)
				}
				return fmt.Errorf("invalid integer value %q for column %q", str, key)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				if i, err := strconv.ParseUint(str, 10, 64); err == nil {
					return convertToUint(i, ft)
				}
				return fmt.Errorf("invalid unsigned integer value %q for column %q", str, key)
			case reflect.Float32, reflect.Float64:
				if f, err := strconv.ParseFloat(str, 64); err == nil {
					if ft.Kind() == reflect.Float32 {
						return float32(f)
					}
					return f
				}
				return fmt.Errorf("invalid float value %q for column %q", str, key)
			}
		}
	}

	// Handle composite types (struct/slice/map/array)
	if ft, ok := fieldTypes[key]; ok && needsJSONDecode(ft) {
		if decoded, ok := decodeToType(v, ft); ok {
			return decoded
		}
	}

	switch val := v.(type) {
	case []interface{}, map[string]interface{}:
		if fieldTypes == nil || fieldTypes[key] == reflect.TypeFor[string]() {
			if b, err := json.Marshal(val); err == nil {
				return string(b)
			}
		}
		return v
	default:
		return parseTimeValue(v)
	}
}

// convertToInt converts an int64 value to the target integer type with range checking.
func convertToInt(i int64, target reflect.Type) interface{} {
	switch target.Kind() {
	case reflect.Int:
		return int(i)
	case reflect.Int8:
		if i < -128 || i > 127 {
			return fmt.Errorf("value %d out of range for int8", i)
		}
		return int8(i)
	case reflect.Int16:
		if i < -32768 || i > 32767 {
			return fmt.Errorf("value %d out of range for int16", i)
		}
		return int16(i)
	case reflect.Int32:
		if i < -2147483648 || i > 2147483647 {
			return fmt.Errorf("value %d out of range for int32", i)
		}
		return int32(i)
	case reflect.Int64:
		return i
	default:
		return i
	}
}

// convertToUint converts a uint64 value to the target unsigned integer type with range checking.
func convertToUint(i uint64, target reflect.Type) interface{} {
	switch target.Kind() {
	case reflect.Uint:
		return uint(i)
	case reflect.Uint8:
		if i > 255 {
			return fmt.Errorf("value %d out of range for uint8", i)
		}
		return uint8(i)
	case reflect.Uint16:
		if i > 65535 {
			return fmt.Errorf("value %d out of range for uint16", i)
		}
		return uint16(i)
	case reflect.Uint32:
		if i > 4294967295 {
			return fmt.Errorf("value %d out of range for uint32", i)
		}
		return uint32(i)
	case reflect.Uint64:
		return i
	default:
		return i
	}
}

// decodeToType marshals v back to JSON and unmarshals it into a new instance of
// typ, returning the typed value. It reports ok=false if v cannot be decoded
// into typ (e.g. a scalar where a slice is declared), letting the caller fall
// back to default handling.
func decodeToType(v interface{}, typ reflect.Type) (interface{}, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	ptr := reflect.New(typ)
	if err := json.Unmarshal(b, ptr.Interface()); err != nil {
		return nil, false
	}
	return ptr.Elem().Interface(), true
}

// parseTimeValue tries to parse a string value as time.Time using TimeFormat
// and common fallback layouts. Non-string values are returned unchanged.
func parseTimeValue(v interface{}) interface{} {
	s, ok := v.(string)
	if !ok {
		return v
	}
	for _, layout := range []string{
		TimeFormat,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02",
		time.RFC3339,
		time.RFC3339Nano,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return v
}

// DecodeJSONFields decodes JSON columns of r from their raw string/[]byte form
// into the composite types declared in the table's FieldTypes.
//
// For each field whose FieldTypes entry is a composite type (struct, slice, map,
// or array — excluding time.Time and []byte), if the value held in r is a
// non-empty string or []byte, it is json.Unmarshal-ed into a new instance of
// that type and stored back. Scalar fields, time.Time/[]byte fields,
// empty/invalid values, and tables without registered metadata are left
// untouched.
//
// It is called by the generated initRow() right after SetTable, so every Row
// returned from a query carries typed values for declared JSON columns. Rows
// built from JSON input get the same treatment via normalizeJSONValue in
// unmarshalRow, so typed accessors work regardless of how the row was built.
// The write path is unaffected: normalizeSQLValue serializes the typed value
// back to a JSON string.
//
// To opt a column in, register its type in an init() next to the model:
//
//	Table.FieldTypes["profile"] = reflect.TypeOf(Profile{})
//	Table.FieldTypes["tags"] = reflect.TypeOf([]string{})
func DecodeJSONFields(r *Row) *Row {
	if r.data == nil {
		return r
	}
	t := GetTableByName(r.table)
	if t == nil {
		return r
	}
	return decodeJSONFieldsWith(r, t.FieldTypes)
}

// DecodeJSONFieldsWith decodes JSON columns using a caller-supplied type map.
// This is the multi-table entry point: mergedFieldTypes contains types from all
// involved tables. Unregistered/unknown columns are left untouched.
func DecodeJSONFieldsWith(r *Row, ft map[string]reflect.Type) *Row {
	return decodeJSONFieldsWith(r, ft)
}

// decodeJSONFieldsWith is the internal implementation.
func decodeJSONFieldsWith(r *Row, ft map[string]reflect.Type) *Row {
	for col, typ := range ft {
		if !needsJSONDecode(typ) {
			continue
		}
		raw, ok := jsonString(r.data[col])
		if !ok || raw == "" {
			continue
		}
		ptr := reflect.New(typ)
		if err := json.Unmarshal([]byte(raw), ptr.Interface()); err == nil {
			r.data[col] = ptr.Elem().Interface()
		}
	}
	return r
}

// needsJSONDecode reports whether a declared field type should be decoded from
// a JSON string into a typed Go value. This covers composite kinds — structs,
// slices, arrays, and maps. time.Time (a struct handled by the SQL driver and
// ToTime) and []byte (BLOB columns) are excluded. Scalar types return false:
// string columns keep the opaque JSON-string convention, and other scalars let
// the SQL driver surface type mismatches with a clear error.
func needsJSONDecode(typ reflect.Type) bool {
	if typ == reflect.TypeFor[time.Time]() || typ == reflect.TypeFor[[]byte]() {
		return false
	}
	switch typ.Kind() {
	case reflect.Struct, reflect.Slice, reflect.Map, reflect.Array:
		return true
	}
	return false
}

// jsonString returns the underlying text for string and []byte values, reporting
// whether v held text suitable for JSON decoding.
func jsonString(v interface{}) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case []byte:
		return string(x), true
	}
	return "", false
}

// ---- Key format helpers ----

// resolveKeyFormat returns the effective key format for r: a row-level override
// first, then the package-level DefaultKeyFormat.
func resolveKeyFormat(r *Row) KeyFormat {
	if r.keyFormat != nil {
		return *r.keyFormat
	}
	return DefaultKeyFormat
}

// snakeToCamel converts a snake_case string to camelCase.
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if len(parts[i]) > 0 {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// camelToSnake converts a camelCase string to snake_case.
// Returns s unchanged if it is already snake_case (no uppercase letters).
func camelToSnake(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	for i, c := range s {
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteByte(byte(c) + 32)
		} else {
			b.WriteByte(byte(c))
		}
	}
	return b.String()
}
