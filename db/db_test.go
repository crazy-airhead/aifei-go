package db

import (
	"reflect"
	"testing"
)

func TestRowTypeConvert(t *testing.T) {
	row := NewRow("user")
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
	row := NewRow("user")
	row.Set("name", "test")
	row.Set("age", 25)

	data, err := row.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}

	row2 := NewRow("user")
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
	row := NewRow("test")
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
	row := NewRow("test")
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
	RegisterTable(&Table{
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
	row := NewRow("oa_process")
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
	for k, v := range row.data {
		switch v.(type) {
		case []interface{}, map[string]interface{}:
			t.Fatalf("field %q has unsupported type %T for SQL driver", k, v)
		}
	}
}

func TestRowUnmarshalJSONTypeAware(t *testing.T) {
	RegisterTable(&Table{
		Name:   "typed_table",
		Fields: "json_col,int_col",
		FieldTypes: map[string]reflect.Type{
			"json_col": reflect.TypeFor[string](),
			"int_col":  reflect.TypeOf(int(0)),
		},
	})

	row := NewRow("typed_table")
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
	row := NewRow("unknown_table")
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
