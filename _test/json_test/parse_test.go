package json_test

import (
	"testing"

	"github.com/crazy-airhead/aifei-go/json"
)

type parseUser struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestParse(t *testing.T) {
	v, err := json.Parse[parseUser]([]byte(`{"name":"test","count":42}`))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if v.Name != "test" || v.Count != 42 {
		t.Errorf("Parse = %+v, want {test 42}", v)
	}

	// Slice targets
	list, err := json.Parse[[]parseUser]([]byte(`[{"name":"a","count":1}]`))
	if err != nil || len(list) != 1 || list[0].Name != "a" {
		t.Errorf("Parse[[]parseUser] = (%v, %v)", list, err)
	}
}

func TestParseError(t *testing.T) {
	if _, err := json.Parse[parseUser]([]byte(`{invalid`)); err == nil {
		t.Error("Parse should fail on invalid JSON")
	}
	// Type mismatch is an unmarshal error too
	if _, err := json.Parse[parseUser]([]byte(`"scalar"`)); err == nil {
		t.Error("Parse should fail when JSON shape does not match T")
	}
}

func TestParseString(t *testing.T) {
	v, err := json.ParseString[parseUser](`{"name":"s","count":7}`)
	if err != nil {
		t.Fatalf("ParseString failed: %v", err)
	}
	if v.Name != "s" || v.Count != 7 {
		t.Errorf("ParseString = %+v", v)
	}

	if _, err := json.ParseString[parseUser](`nope`); err == nil {
		t.Error("ParseString should fail on invalid JSON")
	}
}
