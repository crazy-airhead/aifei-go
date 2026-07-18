package db

import (
	"sort"
	"time"
)

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

// Keep keeps only the specified fields, dropping the rest from both data and
// the change set. Keeping the change set in sync (like Remove/RemoveNullFields)
// prevents a later Update from generating SQL for fields that no longer exist.
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
	for k := range r.change {
		if !keep[k] {
			delete(r.change, k)
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

// ---- Active Record ----

// Insert inserts this row into the database.
func (r *Row) Insert() (*Row, error) { return Insert(r) }

// InsertOrUpdate inserts or updates this row.
func (r *Row) InsertOrUpdate() (*Row, error) { return InsertOrUpdate(r) }

// Update updates this row in the database.
func (r *Row) Update() (bool, error) { return Update(r) }

// Delete deletes this row from the database.
func (r *Row) Delete() (bool, error) { return Delete(r) }

// ---- JSON (logic lives in json_codec.go; these are interface impls) ----

// MarshalJSON implements json.Marshaler. Delegates to marshalRow.
func (r *Row) MarshalJSON() ([]byte, error) { return marshalRow(r) }

// UnmarshalJSON implements json.Unmarshaler. Delegates to unmarshalRow.
func (r *Row) UnmarshalJSON(data []byte) error { return unmarshalRow(r, data) }

// SetKeyFormat overrides the key format for this row, taking precedence over
// the package-level DefaultKeyFormat.
func (r *Row) SetKeyFormat(f KeyFormat) *Row {
	r.keyFormat = &f
	return r
}
