package db_test

import (
	"reflect"
	"testing"

	"github.com/crazy-airhead/aifei-go/db"

	_ "modernc.org/sqlite"
)

// Ported from the former db/db_test.go (ISSUE-0006). These Row behavior tests
// live here as external tests (package db_test) against the exported API.
// Tests that previously touched unexported helpers (decodeRows,
// normalizeSQLValue, the row.data map) are rewritten against exported methods,
// or driven through a real SQLite round-trip where the helper had no exported
// equivalent.

func TestRowTypeConvert(t *testing.T) {
	row := db.NewRow("user")
	row.Set("name", "test")
	row.Set("age", 25)
	row.Set("score", 98.5)
	row.Set("active", true)

	if row.GetStr("name") != "test" {
		t.Fatalf("expected test, got %s", row.GetStr("name"))
	}
	if row.GetInt("age") != 25 {
		t.Fatalf("expected 25, got %d", row.GetInt("age"))
	}
	if row.GetFloat64("score") != 98.5 {
		t.Fatalf("expected 98.5, got %f", row.GetFloat64("score"))
	}
	if !row.GetBool("active") {
		t.Fatal("expected true")
	}

	if row.GetStrDefault("missing", "default") != "default" {
		t.Fatal("expected default value")
	}
	if row.GetIntDefault("missing", 42) != 42 {
		t.Fatal("expected default int")
	}
}

func TestRowJSON(t *testing.T) {
	row := db.NewRow("user")
	row.Set("name", "test")
	row.Set("age", 25)

	data, err := row.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}

	row2 := db.NewRow("user")
	err = row2.UnmarshalJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if row2.GetStr("name") != "test" {
		t.Fatalf("expected test, got %s", row2.GetStr("name"))
	}
}

func TestRowUnmarshalJSONComplexTypes(t *testing.T) {
	// JSON arrays should be serialized to JSON strings
	row := db.NewRow("test")
	err := row.UnmarshalJSON([]byte(`{"extra_feat":["1","2"],"scene_ids":[],"name":"test"}`))
	if err != nil {
		t.Fatal(err)
	}

	// JSON arrays become JSON strings
	extraFeat := row.GetStr("extra_feat")
	if extraFeat != `["1","2"]` {
		t.Fatalf("expected JSON string [\"1\",\"2\"], got %q", extraFeat)
	}

	// Empty arrays become "[]"
	sceneIds := row.GetStr("scene_ids")
	if sceneIds != `[]` {
		t.Fatalf("expected [], got %q", sceneIds)
	}

	// Regular strings are unchanged
	if row.GetStr("name") != "test" {
		t.Fatalf("expected test, got %s", row.GetStr("name"))
	}
}

func TestRowUnmarshalJSONNestedObject(t *testing.T) {
	row := db.NewRow("test")
	err := row.UnmarshalJSON([]byte(`{"scope_data":{"checkedAll":true,"userList":[]}}`))
	if err != nil {
		t.Fatal(err)
	}

	// JSON objects should be serialized to JSON strings
	scopeData := row.GetStr("scope_data")
	if scopeData != `{"checkedAll":true,"userList":[]}` {
		t.Fatalf("expected JSON object string, got %q", scopeData)
	}
}

func TestRowUnmarshalJSONRealWorld(t *testing.T) {
	// Simulate the exact request body with registered table metadata
	db.RegisterTable(&db.Table{
		Name:   "oa_process",
		Fields: "name,extra_feat,valid,scene_ids,scope_data,icon",
		FieldTypes: map[string]reflect.Type{
			"extra_feat": reflect.TypeFor[string](),
			"scene_ids":  reflect.TypeFor[string](),
			"scope_data": reflect.TypeFor[string](),
			"valid":      reflect.TypeOf(int(0)),
			"name":       reflect.TypeFor[string](),
		},
	})

	body := `{"name":"11","categoryId":1,"parentId":"","extraFeat":["1"],"valid":false,"sceneIds":[],"checkedAll":true,"scope":"所有人员","scopeData":"{\"checkedAll\":true,\"userList\":[],\"userInfoList\":[],\"departList\":[],\"departInfoList\":[]}","icon":""}`
	row := db.NewRow("oa_process")
	err := row.UnmarshalJSON([]byte(body))
	if err != nil {
		t.Fatal(err)
	}

	// extra_feat is string → JSON array should be serialized
	extraFeat := row.Get("extra_feat")
	if _, ok := extraFeat.(string); !ok {
		t.Fatalf("extra_feat (string field) should be string, got %T: %v", extraFeat, extraFeat)
	}
	if extraFeat.(string) != `["1"]` {
		t.Fatalf("extra_feat should be [\"1\"], got %q", extraFeat)
	}

	// scene_ids is string → empty array becomes "[]"
	sceneIds := row.Get("scene_ids")
	if _, ok := sceneIds.(string); !ok {
		t.Fatalf("scene_ids (string field) should be string, got %T: %v", sceneIds, sceneIds)
	}
	if sceneIds.(string) != `[]` {
		t.Fatalf("scene_ids should be [], got %q", sceneIds)
	}

	// scope_data is already a JSON string → unchanged
	scopeData := row.GetStr("scope_data")
	if scopeData != `{"checkedAll":true,"userList":[],"userInfoList":[],"departList":[],"departInfoList":[]}` {
		t.Fatalf("scopeData should remain a JSON string, got %q", scopeData)
	}

	// valid is an int field, not string → bool stays as bool (not converted)
	valid := row.Get("valid")
	if _, ok := valid.(bool); !ok {
		t.Fatalf("valid (int field) should stay bool, got %T", valid)
	}

	// No value should be []interface{} or map[string]interface{}
	row.ForEach(func(k string, v interface{}) {
		switch v.(type) {
		case []interface{}, map[string]interface{}:
			t.Fatalf("field %q has unsupported type %T for SQL driver", k, v)
		}
	})
}

func TestRowUnmarshalJSONTypeAware(t *testing.T) {
	db.RegisterTable(&db.Table{
		Name:   "typed_table",
		Fields: "json_col,int_col",
		FieldTypes: map[string]reflect.Type{
			"json_col": reflect.TypeFor[string](),
			"int_col":  reflect.TypeOf(int(0)),
		},
	})

	row := db.NewRow("typed_table")
	err := row.UnmarshalJSON([]byte(`{"jsonCol":[1,2,3],"intCol":[1,2,3]}`))
	if err != nil {
		t.Fatal(err)
	}

	// json_col is string → serialized to JSON string
	jsonCol := row.Get("json_col")
	if s, ok := jsonCol.(string); !ok {
		t.Fatalf("json_col (string field) should be string, got %T", jsonCol)
	} else if s != `[1,2,3]` {
		t.Fatalf("json_col should be [1,2,3], got %q", s)
	}

	// int_col is int → NOT serialized, stays as []interface{}
	intCol := row.Get("int_col")
	if _, ok := intCol.([]interface{}); !ok {
		t.Fatalf("int_col (int field) should remain []interface{}, got %T", intCol)
	}
}

func TestRowUnmarshalJSONNoTable(t *testing.T) {
	// Without table metadata, fall back to safe default (convert to string)
	row := db.NewRow("unknown_table")
	err := row.UnmarshalJSON([]byte(`{"tags":["a","b"],"config":{"key":"val"}}`))
	if err != nil {
		t.Fatal(err)
	}

	// Without type info, arrays become JSON strings (safe default)
	tags := row.Get("tags")
	if _, ok := tags.(string); !ok {
		t.Fatalf("tags should be string (safe default), got %T", tags)
	}

	// Without type info, objects become JSON strings (safe default)
	config := row.Get("config")
	if _, ok := config.(string); !ok {
		t.Fatalf("config should be string (safe default), got %T", config)
	}
}

// testProfile is a struct-typed JSON column used in the composite-type tests.
type testProfile struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

// Declared composite FieldTypes ([]string, struct) are materialized on the
// UnmarshalJSON input path, so typed accessors work on rows built from request
// bodies — not only on rows read from the DB.
func TestUnmarshalJSONCompositeFieldTypes(t *testing.T) {
	db.RegisterTable(&db.Table{
		Name:   "composite_table",
		Fields: "tags,profile,name",
		FieldTypes: map[string]reflect.Type{
			"tags":    reflect.TypeOf([]string{}),
			"profile": reflect.TypeOf(testProfile{}),
			"name":    reflect.TypeFor[string](),
		},
	})

	row := db.NewRow("composite_table")
	err := row.UnmarshalJSON([]byte(`{"tags":["a","b"],"profile":{"name":"n","age":3},"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}

	// []string field → materialized to []string
	tags, ok := row.Get("tags").([]string)
	if !ok {
		t.Fatalf("tags should be []string, got %T: %v", row.Get("tags"), row.Get("tags"))
	}
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Fatalf("tags should be [a b], got %v", tags)
	}

	// struct field → materialized to the declared struct
	profile, ok := row.Get("profile").(testProfile)
	if !ok {
		t.Fatalf("profile should be testProfile, got %T: %v", row.Get("profile"), row.Get("profile"))
	}
	if profile.Name != "n" || profile.Age != 3 {
		t.Fatalf("profile should be {n 3}, got %+v", profile)
	}

	// string field keeps the opaque-JSON-string convention
	if row.GetStr("name") != "x" {
		t.Fatalf("name should be x, got %q", row.GetStr("name"))
	}

	// No raw []interface{}/map[string]interface{} should remain.
	row.ForEach(func(k string, v interface{}) {
		switch v.(type) {
		case []interface{}, map[string]interface{}:
			t.Fatalf("field %q has unmaterialized type %T", k, v)
		}
	})

	// The []string→JSON-string SQL serialization (formerly checked via the
	// unexported normalizeSQLValue) is covered end-to-end by
	// TestCompositeFieldTypeSQLRoundTrip below.
}

// DecodeJSONFields materializes declared composite types on the DB read path.
func TestDecodeJSONFieldsComposite(t *testing.T) {
	db.RegisterTable(&db.Table{
		Name:   "composite_read_table",
		Fields: "tags,profile",
		FieldTypes: map[string]reflect.Type{
			"tags":    reflect.TypeOf([]string{}),
			"profile": reflect.TypeOf(testProfile{}),
		},
	})

	// Values as they arrive from the DB driver: raw JSON strings.
	row := db.NewRow("composite_read_table")
	row.Put("tags", `["a","b"]`)
	row.Put("profile", `{"name":"n","age":3}`)
	db.DecodeJSONFields(row)

	tags, ok := row.Get("tags").([]string)
	if !ok {
		t.Fatalf("tags should decode to []string, got %T: %v", row.Get("tags"), row.Get("tags"))
	}
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Fatalf("tags should be [a b], got %v", tags)
	}

	profile, ok := row.Get("profile").(testProfile)
	if !ok {
		t.Fatalf("profile should decode to testProfile, got %T: %v", row.Get("profile"), row.Get("profile"))
	}
	if profile.Name != "n" || profile.Age != 3 {
		t.Fatalf("profile should be {n 3}, got %+v", profile)
	}
}

// A declared composite field that receives an incompatible scalar falls through
// to the default handling instead of failing the whole unmarshal.
func TestUnmarshalJSONCompositeTypeMismatch(t *testing.T) {
	db.RegisterTable(&db.Table{
		Name:   "composite_mismatch_table",
		Fields: "tags",
		FieldTypes: map[string]reflect.Type{
			"tags": reflect.TypeOf([]string{}),
		},
	})

	row := db.NewRow("composite_mismatch_table")
	if err := row.UnmarshalJSON([]byte(`{"tags":"not-an-array"}`)); err != nil {
		t.Fatal(err)
	}
	// Falls through: a plain string stays a string.
	if row.GetStr("tags") != "not-an-array" {
		t.Fatalf("mismatched tags should fall through to string, got %v", row.Get("tags"))
	}
}

// A []string field unmarshaled from a request body must serialize back to a JSON
// string for SQL and decode again on read. This replaces the former internal
// normalizeSQLValue assertion, driven through a real SQLite round-trip.
func TestCompositeFieldTypeSQLRoundTrip(t *testing.T) {
	setupTestDB(t)
	db.Use().RawSql("CREATE TABLE IF NOT EXISTS composite_table (id INTEGER PRIMARY KEY, tags TEXT, profile TEXT, name TEXT)").Update()
	db.Use().RawSql("DELETE FROM composite_table").Update()
	db.RegisterTable(&db.Table{
		Name:        "composite_table",
		Fields:      "id,tags,profile,name",
		PrimaryKeys: []string{"id"},
		FieldTypes: map[string]reflect.Type{
			"id":      reflect.TypeOf(int64(0)),
			"tags":    reflect.TypeOf([]string{}),
			"profile": reflect.TypeOf(testProfile{}),
			"name":    reflect.TypeFor[string](),
		},
	})

	row := db.NewRow("composite_table")
	if err := row.UnmarshalJSON([]byte(`{"tags":["a","b"],"profile":{"name":"n","age":3},"name":"x"}`)); err != nil {
		t.Fatal(err)
	}
	row.Set("id", int64(1))
	if _, err := db.Insert(row); err != nil {
		t.Fatal(err)
	}

	got, err := db.FindByID("composite_table", int64(1))
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected to read back the inserted row")
	}
	tags, ok := got.Get("tags").([]string)
	if !ok {
		t.Fatalf("tags should round-trip to []string, got %T: %v", got.Get("tags"), got.Get("tags"))
	}
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Fatalf("tags should be [a b], got %v", tags)
	}
	profile, ok := got.Get("profile").(testProfile)
	if !ok {
		t.Fatalf("profile should round-trip to testProfile, got %T: %v", got.Get("profile"), got.Get("profile"))
	}
	if profile.Name != "n" || profile.Age != 3 {
		t.Fatalf("profile should be {n 3}, got %+v", profile)
	}
}

// On the read path the framework binds table/primary-key metadata to result
// rows and decodes declared JSON columns (the former internal decodeRows). This
// is verified end-to-end via FindByID; an existing-but-unregistered table gets
// no binding.
func TestDecodeRowsBindsTableAndDecodes(t *testing.T) {
	setupTestDB(t)
	db.Use().RawSql("CREATE TABLE IF NOT EXISTS decode_rows_table (id INTEGER PRIMARY KEY, tags TEXT)").Update()
	db.Use().RawSql("DELETE FROM decode_rows_table").Update()
	// An existing-but-unregistered table, to assert no binding happens.
	db.Use().RawSql("CREATE TABLE IF NOT EXISTS plain_table (id INTEGER PRIMARY KEY, x INTEGER)").Update()
	db.Use().RawSql("DELETE FROM plain_table").Update()

	db.RegisterTable(&db.Table{
		Name:        "decode_rows_table",
		Fields:      "id,tags",
		PrimaryKeys: []string{"id"},
		FieldTypes: map[string]reflect.Type{
			"id":   reflect.TypeOf(int64(0)),
			"tags": reflect.TypeOf([]string{}),
		},
	})

	db.Insert(db.NewRow("decode_rows_table").Set("id", int64(1)).Set("tags", `["a","b"]`))
	db.Insert(db.NewRow("plain_table").Set("id", int64(1)).Set("x", 1))

	r, err := db.FindByID("decode_rows_table", int64(1))
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("expected to find decode_rows_table row")
	}
	if r.Table() != "decode_rows_table" {
		t.Fatalf("table should be bound, got %q", r.Table())
	}
	if got := r.PrimaryKeys(); len(got) != 1 || got[0] != "id" {
		t.Fatalf("primary keys should be [id], got %v", got)
	}
	tags, ok := r.Get("tags").([]string)
	if !ok {
		t.Fatalf("tags should decode to []string, got %T: %v", r.Get("tags"), r.Get("tags"))
	}
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Fatalf("tags should be [a b], got %v", tags)
	}

	// Idempotent: re-reading keeps tags materialized.
	r2, err := db.FindByID("decode_rows_table", int64(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r2.Get("tags").([]string); !ok {
		t.Fatalf("tags should stay []string after re-decode, got %T", r2.Get("tags"))
	}

	// Unregistered table → no binding, no decode.
	plain, err := db.FindByID("plain_table", int64(1))
	if err != nil {
		t.Fatal(err)
	}
	if plain == nil {
		t.Fatal("expected to find plain_table row")
	}
	if plain.Table() != "" {
		t.Fatalf("unregistered table should not bind, got %q", plain.Table())
	}
}

// Keep must drop removed fields from both data and the change set, so a later
// Update does not generate SQL for fields that no longer exist. See ISSUE-0006.
func TestRowKeepClearsChange(t *testing.T) {
	row := db.NewRow("user").Set("a", 1).Set("b", 2).Keep("a")

	// data keeps only "a"
	if row.Size() != 1 || !row.Has("a") || row.Has("b") {
		t.Fatalf("Keep should retain only field a, got size=%d hasA=%v hasB=%v", row.Size(), row.Has("a"), row.Has("b"))
	}
	// change set must also drop "b"
	changed := row.ChangedFields()
	if len(changed) != 1 || changed[0] != "a" {
		t.Fatalf("change set should be [a] only, got %v", changed)
	}
}
