package db

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// KeyFormat controls the JSON key format used by MarshalJSON.
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

// Row represents a data row with Active Record capabilities.
type Row struct {
	table       string
	primaryKeys []string
	data        map[string]interface{}
	change      map[string]struct{}
	keyFormat   *KeyFormat // per-row override; nil means use DefaultKeyFormat
}

// NewRow creates a Row for the given table with default PK "id".
func NewRow(table string) *Row {
	return &Row{table: table, primaryKeys: []string{"id"}}
}

// NewRowWithPK creates a Row with a specific primary key.
func NewRowWithPK(table, pk string) *Row {
	return &Row{table: table, primaryKeys: []string{pk}}
}

// NewRowWithCompositePK creates a Row with composite primary keys.
func NewRowWithCompositePK(table, pk1, pk2 string) *Row {
	return &Row{table: table, primaryKeys: []string{pk1, pk2}}
}

// Table returns the table name.
func (r *Row) Table() string { return r.table }

// SetTable sets the table name.
func (r *Row) SetTable(table string) *Row {
	r.table = table
	return r
}

// PrimaryKeys returns the primary key names.
func (r *Row) PrimaryKeys() []string { return r.primaryKeys }

// SetPrimaryKeys sets the primary key names.
func (r *Row) SetPrimaryKeys(pks ...string) *Row {
	r.primaryKeys = pks
	return r
}

// ID sets the primary key value.
func (r *Row) ID(id interface{}) *Row {
	r.Set(r.primaryKeys[0], id)
	return r
}

// GetID returns the primary key value.
func (r *Row) GetID() interface{} {
	return r.Get(r.primaryKeys[0])
}

// CompositeID sets composite primary key values.
func (r *Row) CompositeID(id1, id2 interface{}) *Row {
	r.Set(r.primaryKeys[0], id1)
	r.Set(r.primaryKeys[1], id2)
	return r
}

// ---- Set (tracks change) ----

func (r *Row) ensureData() {
	if r.data == nil {
		r.data = make(map[string]interface{})
	}
}

func (r *Row) ensureChange() {
	if r.change == nil {
		r.change = make(map[string]struct{})
	}
}

// Set sets a field value and tracks the change.
func (r *Row) Set(field string, value interface{}) *Row {
	r.ensureData()
	r.data[field] = value
	r.ensureChange()
	r.change[field] = struct{}{}
	return r
}

// SetMap sets multiple fields from a map.
func (r *Row) SetMap(data map[string]interface{}) *Row {
	for k, v := range data {
		r.Set(k, v)
	}
	return r
}

// SetIfNotNull sets a field only if value is not nil.
func (r *Row) SetIfNotNull(field string, value interface{}) *Row {
	if value != nil {
		r.Set(field, value)
	}
	return r
}

// SetIfNotBlank sets a string field only if non-empty.
func (r *Row) SetIfNotBlank(field, value string) *Row {
	if value != "" {
		r.Set(field, value)
	}
	return r
}

// ---- Put (no change tracking) ----

// Put sets a field without tracking change.
func (r *Row) Put(field string, value interface{}) *Row {
	r.ensureData()
	r.data[field] = value
	return r
}

// PutMap sets multiple fields without tracking change.
func (r *Row) PutMap(data map[string]interface{}) *Row {
	for k, v := range data {
		r.Put(k, v)
	}
	return r
}

// ---- Remove ----

// Remove removes fields from the row.
func (r *Row) Remove(fields ...string) *Row {
	for _, f := range fields {
		delete(r.data, f)
		delete(r.change, f)
	}
	return r
}

// RemoveNullFields removes fields with nil values.
func (r *Row) RemoveNullFields() *Row {
	for k, v := range r.data {
		if v == nil {
			delete(r.data, k)
			delete(r.change, k)
		}
	}
	return r
}

// Keep keeps only the specified fields.
func (r *Row) Keep(fields ...string) *Row {
	keep := make(map[string]bool)
	for _, f := range fields {
		keep[f] = true
	}
	for k := range r.data {
		if !keep[k] {
			delete(r.data, k)
		}
	}
	return r
}

// Clear clears all data and changes.
func (r *Row) Clear() *Row {
	r.data = make(map[string]interface{})
	r.change = make(map[string]struct{})
	return r
}

// Has checks if a field exists.
func (r *Row) Has(field string) bool {
	if r.data == nil {
		return false
	}
	_, ok := r.data[field]
	return ok
}

// Size returns the number of fields.
func (r *Row) Size() int {
	if r.data == nil {
		return 0
	}
	return len(r.data)
}

// ---- Getters ----

// Get returns a field value.
func (r *Row) Get(field string) interface{} {
	if r.data == nil {
		return nil
	}
	return r.data[field]
}

// GetDefault returns a field value with default.
func (r *Row) GetDefault(field string, def interface{}) interface{} {
	v := r.Get(field)
	if v == nil {
		return def
	}
	return v
}

// GetStr returns a field as string.
func (r *Row) GetStr(field string) string { return ToString(r.Get(field)) }

// GetStrDefault returns a field as string with default.
func (r *Row) GetStrDefault(field, def string) string {
	v := r.Get(field)
	if v == nil {
		return def
	}
	return ToString(v)
}

// GetInt returns a field as int.
func (r *Row) GetInt(field string) int { return ToInt(r.Get(field)) }

// GetIntDefault returns a field as int with default.
func (r *Row) GetIntDefault(field string, def int) int {
	v := r.Get(field)
	if v == nil {
		return def
	}
	return ToInt(v)
}

// GetInt64 returns a field as int64.
func (r *Row) GetInt64(field string) int64 { return ToInt64(r.Get(field)) }

// GetInt64Default returns a field as int64 with default.
func (r *Row) GetInt64Default(field string, def int64) int64 {
	v := r.Get(field)
	if v == nil {
		return def
	}
	return ToInt64(v)
}

// GetFloat64 returns a field as float64.
func (r *Row) GetFloat64(field string) float64 { return ToFloat64(r.Get(field)) }

// GetFloat64Default returns a field as float64 with default.
func (r *Row) GetFloat64Default(field string, def float64) float64 {
	v := r.Get(field)
	if v == nil {
		return def
	}
	return ToFloat64(v)
}

// GetBool returns a field as bool.
func (r *Row) GetBool(field string) bool { return ToBool(r.Get(field)) }

// GetBoolDefault returns a field as bool with default.
func (r *Row) GetBoolDefault(field string, def bool) bool {
	v := r.Get(field)
	if v == nil {
		return def
	}
	return ToBool(v)
}

// GetTime returns a field as time.Time.
func (r *Row) GetTime(field string) time.Time { return ToTime(r.Get(field)) }

// GetTimeDefault returns a field as time.Time with default.
func (r *Row) GetTimeDefault(field string, def time.Time) time.Time {
	v := r.Get(field)
	if v == nil {
		return def
	}
	return ToTime(v)
}

// GetBytes returns a field as []byte.
func (r *Row) GetBytes(field string) []byte {
	v := r.Get(field)
	if v == nil {
		return nil
	}
	if b, ok := v.([]byte); ok {
		return b
	}
	if s, ok := v.(string); ok {
		return []byte(s)
	}
	return nil
}

// FieldNames returns sorted field names.
func (r *Row) FieldNames() []string {
	if r.data == nil {
		return nil
	}
	names := make([]string, 0, len(r.data))
	for k := range r.data {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// FieldValues returns field values in sorted key order.
func (r *Row) FieldValues() []interface{} {
	names := r.FieldNames()
	vals := make([]interface{}, len(names))
	for i, n := range names {
		vals[i] = r.data[n]
	}
	return vals
}

// ForEach iterates over all fields.
func (r *Row) ForEach(fn func(key string, value interface{})) {
	if r.data == nil {
		return
	}
	for k, v := range r.data {
		fn(k, v)
	}
}

// ChangeSet returns the set of changed fields.
func (r *Row) ChangeSet() map[string]struct{} { return r.change }

// ClearChange clears all tracked changes.
func (r *Row) ClearChange() { r.change = make(map[string]struct{}) }

// ChangedFields returns changed field names.
func (r *Row) ChangedFields() []string {
	if r.change == nil {
		return nil
	}
	names := make([]string, 0, len(r.change))
	for k := range r.change {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// ---- JSON ----

// MarshalJSON implements json.Marshaler. Key format is controlled by
// the row's KeyFormat (or DefaultKeyFormat if not set). When KeyFormatCamel
// is active, database column names (snake_case) are converted to camelCase.
// time.Time values are formatted using the package-level TimeFormat.
func (r *Row) MarshalJSON() ([]byte, error) {
	if r.data == nil {
		return []byte("null"), nil
	}
	kf := r.resolveKeyFormat()
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

// UnmarshalJSON implements json.Unmarshaler. Input keys are always normalized
// from camelCase to snake_case so that database column names stay consistent
// internally, regardless of the configured KeyFormat.
// String values matching TimeFormat (and common fallback layouts) are parsed
// back to time.Time so that time fields survive a JSON round-trip.
func (r *Row) UnmarshalJSON(data []byte) error {
	r.ensureData()
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.ensureChange()
	for k, v := range raw {
		snakeKey := camelToSnake(k)
		r.data[snakeKey] = parseTimeValue(v)
		r.change[snakeKey] = struct{}{}
	}
	return nil
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

// SetKeyFormat overrides the key format for this row. Pass nil to revert to
// the package-level DefaultKeyFormat.
func (r *Row) SetKeyFormat(f KeyFormat) *Row {
	r.keyFormat = &f
	return r
}

// resolveKeyFormat returns the effective key format: row-level override first,
// then package-level DefaultKeyFormat.
func (r *Row) resolveKeyFormat() KeyFormat {
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

// ---- Active Record ----

// Insert inserts this row into the database.
func (r *Row) Insert() (*Row, error) { return Insert(r) }

// InsertOrUpdate inserts or updates this row.
func (r *Row) InsertOrUpdate() (*Row, error) { return InsertOrUpdate(r) }

// Update updates this row in the database.
func (r *Row) Update() (bool, error) { return Update(r) }

// Delete deletes this row from the database.
func (r *Row) Delete() (bool, error) { return Delete(r) }
