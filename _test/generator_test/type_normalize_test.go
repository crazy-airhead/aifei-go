package generator_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/tools/generator"
)

// TestNormalizeDataType covers alias folding and suffix stripping.
func TestNormalizeDataType(t *testing.T) {
	cases := []struct{ in, want string }{
		{"VARCHAR", "VARCHAR"},
		{"varchar(255)", "VARCHAR"},
		{"character varying", "VARCHAR"},
		{"CHARACTER VARYING(100)", "VARCHAR"},
		{"VARCHAR2(50)", "VARCHAR"},
		{"bpchar", "CHAR"},
		{"double precision", "DOUBLE"},
		{"float8", "DOUBLE"},
		{"timestamp with time zone", "TIMESTAMP"},
		{"TIMESTAMP WITHOUT TIME ZONE", "TIMESTAMP"},
		{"timestamptz", "TIMESTAMP"},
		{"time with time zone", "TIME"},
		{"int2", "SMALLINT"},
		{"int4", "INTEGER"},
		{"int8", "BIGINT"},
		{"decimal(10,2)", "DECIMAL"},
		{"enum('a','b')", "ENUM"},
		{"  TEXT  ", "TEXT"},
	}
	for _, c := range cases {
		if got := generator.NormalizeDataType(c.in); got != c.want {
			t.Errorf("NormalizeDataType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTypeMappingNormalizedLookup verifies lookups go through normalization
// and the new canonical entries resolve.
func TestTypeMappingNormalizedLookup(t *testing.T) {
	tm := generator.NewTypeMapping()

	cases := []struct{ in, want string }{
		{"TIMESTAMP WITH TIME ZONE", "time.Time"}, // PG information_schema phrasing
		{"character varying", "string"},
		{"varchar(64)", "string"},
		{"BYTEA", "[]byte"},
		{"uuid", "string"},
		{"CLOB", "string"},
		{"int4", "int"},
		{"int8", "int64"},
	}
	for _, c := range cases {
		if got := tm.GetType(c.in); got != c.want {
			t.Errorf("GetType(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Unmapped names still fall back to string.
	if got := tm.GetType("SOMETHING_WEIRD"); got != "string" {
		t.Errorf("GetType(unmapped) = %q, want string", got)
	}

	// User mappings are canonicalized on add, so alias lookups hit them.
	tm.AddMapping("mytype(10)", "int")
	if got := tm.GetType("MYTYPE"); got != "int" {
		t.Errorf("AddMapping with suffix should canonicalize; got %q", got)
	}
	if _, ok := tm.Lookup("mytype(3)"); !ok {
		t.Error("alias lookup with different suffix should still hit")
	}
	tm.RemoveMapping("mytype(1)")
	if _, ok := tm.Lookup("MYTYPE"); ok {
		t.Error("RemoveMapping should canonicalize too")
	}
}

// TestMetaReaderPGStyleTypeNames verifies PG-style multi-word type names map
// through normalization on the driver (SQLite) path.
func TestMetaReaderPGStyleTypeNames(t *testing.T) {
	pool := setupTestDB(t)
	defer pool.Close()

	if _, err := pool.Exec(`CREATE TABLE pg_style (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created TIMESTAMP WITH TIME ZONE,
		plain_ts timestamptz
	)`); err != nil {
		t.Fatal(err)
	}

	reader := generator.NewMetaReader()
	infos, err := reader.Read(pool, &generator.SQLiteMetaDialect{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	for _, info := range infos {
		if info.Name == "pg_style" {
			fields = map[string]string{}
			for _, f := range info.Fields {
				fields[f.Name] = f.GoType
			}
		}
	}
	if fields == nil {
		t.Fatal("pg_style table not found")
	}
	if fields["created"] != "time.Time" {
		t.Errorf("created: got %q, want time.Time", fields["created"])
	}
	if fields["plain_ts"] != "time.Time" {
		t.Errorf("plain_ts: got %q, want time.Time", fields["plain_ts"])
	}
}
