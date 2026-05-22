package db

import (
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
