package json

import "testing"

func TestMarshalString(t *testing.T) {
	data := map[string]interface{}{"name": "james", "age": 18}
	s := MarshalString(data)
	if !contains(s, "james") || !contains(s, "18") {
		t.Fatalf("expected json with james and 18, got %s", s)
	}
}

func TestUnmarshalString(t *testing.T) {
	s := `{"name":"james","age":18}`
	var data map[string]interface{}
	err := UnmarshalString(s, &data)
	if err != nil {
		t.Fatal(err)
	}
	if data["name"] != "james" {
		t.Fatalf("expected name=james, got %v", data["name"])
	}
}

func TestMarshalError(t *testing.T) {
	// Channels can't be marshaled
	s := MarshalString(make(chan int))
	if s != "{}" {
		t.Fatalf("expected '{}', got '%s'", s)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && findSubstr(s, sub)))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
