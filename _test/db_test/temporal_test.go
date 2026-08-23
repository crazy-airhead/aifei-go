package db_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/db"

	_ "modernc.org/sqlite"
)

func setupTemporalDB(t *testing.T) {
	t.Helper()
	db.ResetConfigs()
	if err := db.Init("sqlite", "file::memory:?cache=shared"); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string) {
		if _, err := db.Use().RawSql(sql).Update(); err != nil {
			t.Fatal(err)
		}
	}
	exec(`DROP TABLE IF EXISTS temporal_test`)
	exec(`CREATE TABLE temporal_test (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		d DATE,
		dt DATETIME,
		ts TIMESTAMP,
		t TIME)`)
	exec(`DELETE FROM temporal_test`)
}

// TestTemporalColumnScan verifies DATE/DATETIME/TIMESTAMP/TIME columns are
// parsed into time.Time at scan time regardless of what the driver returns.
func TestTemporalColumnScan(t *testing.T) {
	setupTemporalDB(t)
	if _, err := db.Use().RawSql(`INSERT INTO temporal_test (d, dt, ts, t) VALUES
		('2026-08-23', '2026-08-23 10:30:05', '2026-08-23 10:30:05', '10:30:05')`).Update(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Use().RawSql(`SELECT * FROM temporal_test`).Find()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]

	for _, field := range []string{"d", "dt", "ts", "t"} {
		v := r.Get(field)
		if _, ok := v.(time.Time); !ok {
			t.Errorf("field %q: expected time.Time in Row data, got %T (%v)", field, v, v)
		}
	}

	got, err := r.GetTime("dt")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 23, 10, 30, 5, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("GetTime(dt) = %v, want %v", got, want)
	}

	gotD, err := r.GetTime("d")
	if err != nil {
		t.Fatal(err)
	}
	if gotD.Year() != 2026 || gotD.Month() != time.August || gotD.Day() != 23 {
		t.Errorf("GetTime(d) = %v, want 2026-08-23", gotD)
	}

	gotT, err := r.GetTime("t")
	if err != nil {
		t.Fatal(err)
	}
	if gotT.Hour() != 10 || gotT.Minute() != 30 || gotT.Second() != 5 {
		t.Errorf("GetTime(t) = %v, want 10:30:05", gotT)
	}
}

// TestTemporalJSONStableFormat verifies the JSON output of temporal columns
// stays in TimeFormat ("2006-01-02 15:04:05"), matching the previous string
// passthrough behavior.
func TestTemporalJSONStableFormat(t *testing.T) {
	setupTemporalDB(t)
	if _, err := db.Use().RawSql(`INSERT INTO temporal_test (d, dt) VALUES
		('2026-08-23', '2026-08-23 10:30:05')`).Update(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Use().RawSql(`SELECT d, dt FROM temporal_test`).Find()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["dt"] != "2026-08-23 10:30:05" {
		t.Errorf("json dt = %q, want %q", m["dt"], "2026-08-23 10:30:05")
	}
	if m["d"] != "2026-08-23 00:00:00" {
		t.Errorf("json d = %q, want %q", m["d"], "2026-08-23 00:00:00")
	}
}

// TestTemporalNull verifies NULL temporal columns are a missing value, not an
// error: GetTime yields the zero time, GetTimeDefault yields the default.
func TestTemporalNull(t *testing.T) {
	setupTemporalDB(t)
	if _, err := db.Use().RawSql(`INSERT INTO temporal_test (d) VALUES (NULL)`).Update(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Use().RawSql(`SELECT * FROM temporal_test`).Find()
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if v := r.Get("d"); v != nil {
		t.Errorf("expected nil for NULL column, got %#v", v)
	}
	got, err := r.GetTime("d")
	if err != nil {
		t.Fatalf("GetTime on NULL should not error: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("GetTime on NULL = %v, want zero time", got)
	}
	def := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	gotDef, err := r.GetTimeDefault("missing_field", def)
	if err != nil || !gotDef.Equal(def) {
		t.Errorf("GetTimeDefault(missing) = %v, %v; want %v, nil", gotDef, err, def)
	}
	gotDef, err = r.GetTimeDefault("d", def)
	if err != nil || !gotDef.Equal(def) {
		t.Errorf("GetTimeDefault(NULL) = %v, %v; want %v, nil", gotDef, err, def)
	}
}

// TestTemporalDirtyDataFailsLoud verifies a temporal column holding garbage
// fails the query at the scan boundary instead of yielding a silent zero time.
func TestTemporalDirtyDataFailsLoud(t *testing.T) {
	setupTemporalDB(t)
	if _, err := db.Use().RawSql(`INSERT INTO temporal_test (dt) VALUES ('not-a-date')`).Update(); err != nil {
		t.Fatal(err)
	}
	_, err := db.Use().RawSql(`SELECT * FROM temporal_test`).Find()
	if err == nil {
		t.Fatal("expected error for dirty temporal data, got nil")
	}
	if want := `column "dt"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not mention %q", err.Error(), want)
	}

	// GetTime on a dirty string (outside scan, e.g. hand-built Row) errors too.
	if _, err := db.ToTime("2026/08/23 10:00"); err == nil {
		t.Error("ToTime on unparseable string should error")
	}
}

// TestToTimeStrict covers the ToTime contract: nil→zero+nil, native pass
// through, string/[]byte parsing, non-temporal types error.
func TestToTimeStrict(t *testing.T) {
	if got, err := db.ToTime(nil); err != nil || !got.IsZero() {
		t.Errorf("ToTime(nil) = %v, %v; want zero, nil", got, err)
	}
	now := time.Now()
	if got, err := db.ToTime(now); err != nil || !got.Equal(now) {
		t.Errorf("ToTime(time.Time) = %v, %v", got, err)
	}
	if got, err := db.ToTime("2026-08-23 10:30:05"); err != nil || got.Year() != 2026 {
		t.Errorf("ToTime(sql datetime str) = %v, %v", got, err)
	}
	if got, err := db.ToTime([]byte("2026-08-23T10:30:05Z")); err != nil || got.Hour() != 10 {
		t.Errorf("ToTime([]byte) = %v, %v", got, err)
	}
	if _, err := db.ToTime(42); err == nil {
		t.Error("ToTime(int) should error")
	}
	if _, err := db.ToTime("2026-02-30"); err == nil {
		t.Error("ToTime(invalid date) should error")
	}
}

// TestGetTimeOrZero covers the lenient accessor backing generated model
// getters: NULL/missing/dirty all yield the zero time, no error.
func TestGetTimeOrZero(t *testing.T) {
	r := db.NewRow("t").
		Set("ok", "2026-08-23 10:30:05").
		Set("bad", "not-a-date").
		Set("null", nil)
	if got := r.GetTimeOrZero("ok"); got.Year() != 2026 || got.Hour() != 10 {
		t.Errorf("GetTimeOrZero(ok) = %v", got)
	}
	if got := r.GetTimeOrZero("bad"); !got.IsZero() {
		t.Errorf("GetTimeOrZero(dirty) = %v, want zero", got)
	}
	if got := r.GetTimeOrZero("null"); !got.IsZero() {
		t.Errorf("GetTimeOrZero(null) = %v, want zero", got)
	}
	if got := r.GetTimeOrZero("missing"); !got.IsZero() {
		t.Errorf("GetTimeOrZero(missing) = %v, want zero", got)
	}
}
