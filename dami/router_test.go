package dami

import "testing"

func noopHolder(idx int) *holder {
	return newHolder(idx, Listener[any](func(e *Event[any]) error { return nil }))
}

func TestHashRouter(t *testing.T) {
	r := NewHashRouter()
	h := noopHolder(0)
	r.Add("a", h)
	if r.Count("a") != 1 {
		t.Fatalf("Count(a)=%d want 1", r.Count("a"))
	}
	if r.Count("b") != 0 {
		t.Fatalf("Count(b)=%d want 0", r.Count("b"))
	}
	r.Remove("a", h)
	if r.Count("a") != 0 {
		t.Fatalf("after Remove Count(a)=%d want 0", r.Count("a"))
	}
	// RemoveAll is exercised via UnlistenAll in bus_test.
}

func TestPathRouterWildcards(t *testing.T) {
	r := NewPathRouter()
	r.Add("event/*/created", noopHolder(0)) // * = one segment w/o separator
	r.Add("event/**", noopHolder(0))        // ** = any run incl. separators
	r.Add("exact.topic", noopHolder(0))     // exact (no wildcard) → fast map

	cases := []struct {
		topic string
		want  int
	}{
		{"event/user/created", 2}, // matches * and **
		{"event/order/created", 2},
		{"event/a/b/c", 1}, // only ** spans multiple segments
		{"exact.topic", 1}, // exact
		{"nope", 0},
		{"event/created", 1}, // ** matches; * ("event/*/created") needs a segment between
	}
	for _, c := range cases {
		if got := r.Count(c.topic); got != c.want {
			t.Errorf("topic=%q got=%d want=%d", c.topic, got, c.want)
		}
	}
}

func TestPathRouterRemove(t *testing.T) {
	r := NewPathRouter()
	h := noopHolder(0)
	r.Add("event/*", h)
	if r.Count("event/x") != 1 {
		t.Fatal("expected match before remove")
	}
	r.Remove("event/*", h)
	if r.Count("event/x") != 0 {
		t.Fatal("expected no match after remove")
	}
}

func TestTagRouterMatching(t *testing.T) {
	// Listener registered with tags {id}.
	r := NewTagRouter()
	r.Add("event.user.update:id", noopHolder(0))

	cases := []struct {
		sent string
		want int
	}{
		{"event.user.update", 1},         // sent has no tags → matches (one side empty)
		{"event.user.update:id", 1},      // intersect {id}
		{"event.user.update:id,name", 1}, // intersect {id}
		{"event.user.update:name", 0},    // no intersect with {id}
		{"event.user.update:other", 0},   // no intersect
		{"event.other.update:id", 0},     // topic mismatch
	}
	for _, c := range cases {
		if got := r.Count(c.sent); got != c.want {
			t.Errorf("sent=%q got=%d want=%d", c.sent, got, c.want)
		}
	}
}

func TestTagRouterNoTagListener(t *testing.T) {
	// A listener with no tags matches any sent tags (and plain topics).
	r := NewTagRouter()
	r.Add("event.user.update", noopHolder(0))
	if r.Count("event.user.update") != 1 {
		t.Fatal("plain topic should match")
	}
	if r.Count("event.user.update:id") != 1 {
		t.Fatal("sent-with-tag should match a no-tag listener")
	}
}

func TestTagRouterMultipleIntersectSorted(t *testing.T) {
	r := NewTagRouter()
	r.Add("e:id", noopHolder(3))
	r.Add("e:name", noopHolder(1))
	got := r.Match("e:id,name")
	if len(got) != 2 {
		t.Fatalf("want 2 matches, got %d", len(got))
	}
	if got[0].index != 1 || got[1].index != 3 {
		t.Fatalf("not sorted by index: %d %d", got[0].index, got[1].index)
	}
}
