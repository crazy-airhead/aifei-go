package flow_test

import (
	"errors"
	"testing"

	flow "github.com/crazy-airhead/aifei-go/flow"
)

// TestNodeTypeOf covers name parsing (case-insensitive), the deprecated "iterator"
// alias, and the default-to-activity behaviour.
func TestNodeTypeOf(t *testing.T) {
	cases := []struct {
		in   string
		want flow.NodeType
	}{
		{"start", flow.NodeTypeStart},
		{"START", flow.NodeTypeStart},
		{"end", flow.NodeTypeEnd},
		{"activity", flow.NodeTypeActivity},
		{"EXCLUSIVE", flow.NodeTypeExclusive},
		{"inclusive", flow.NodeTypeInclusive},
		{"parallel", flow.NodeTypeParallel},
		{"loop", flow.NodeTypeLoop},
		{"iterator", flow.NodeTypeLoop}, // deprecated alias
		{"", flow.NodeTypeActivity},     // blank -> default
		{"nonsense", flow.NodeTypeActivity},
	}
	for _, c := range cases {
		if got := flow.NodeTypeOf(c.in); got != c.want {
			t.Errorf("NodeTypeOf(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNodeTypeStringAndGateway(t *testing.T) {
	if flow.NodeTypeStart.String() != "START" {
		t.Errorf("start String = %q", flow.NodeTypeStart.String())
	}
	if !flow.NodeTypeExclusive.IsGateway() {
		t.Error("exclusive should be a gateway")
	}
	if flow.NodeTypeActivity.IsGateway() {
		t.Error("activity should not be a gateway")
	}
}

// TestGraphBuildLinear builds start -> a -> end and checks connectivity + order.
func TestGraphBuildLinear(t *testing.T) {
	g, err := flow.Create("g1", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").Task("@DoA").LinkAdd("e")
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}

	if g.GetStart().ID() != "s" {
		t.Errorf("start = %q, want s", g.GetStart().ID())
	}
	if got := len(g.GetNodes()); got != 3 {
		t.Errorf("node count = %d, want 3", got)
	}
	// order preserved
	if ids := nodeIDs(g.GetNodes()); ids != "s,a,e" {
		t.Errorf("order = %q, want s,a,e", ids)
	}

	s := g.GetNode("s")
	if len(s.NextLinks()) != 1 || s.NextLinks()[0].NextID() != "a" {
		t.Errorf("s next links = %v", s.NextLinks())
	}
	a := g.GetNode("a")
	if a.Task().Description() != "@DoA" {
		t.Errorf("a task = %q", a.Task().Description())
	}
	// reverse lookups
	if len(a.PrevLinks()) != 1 || a.PrevLinks()[0].PrevID() != "s" {
		t.Errorf("a prev links = %v", a.PrevLinks())
	}
	if len(a.PrevNodes()) != 1 || a.PrevNodes()[0].ID() != "s" {
		t.Errorf("a prev nodes = %v", a.PrevNodes())
	}
	if a.NextNode().ID() != "e" {
		t.Errorf("a next node = %v", a.NextNode())
	}
	// start has no incoming links; end has no outgoing nodes
	if len(s.PrevLinks()) != 0 {
		t.Errorf("start prev links = %v", s.PrevLinks())
	}
	e := g.GetNode("e")
	if len(e.NextNodes()) != 0 {
		t.Errorf("end next nodes = %v", e.NextNodes())
	}
}

// TestGraphLinkPriority verifies outgoing links are sorted by priority descending.
func TestGraphLinkPriority(t *testing.T) {
	g, err := flow.Create("g", func(s *flow.GraphSpec) {
		s.AddExclusive("x").
			LinkAddConfig("low", func(l *flow.LinkSpec) { l.Priority(1) }).
			LinkAddConfig("high", func(l *flow.LinkSpec) { l.Priority(100) }).
			LinkAddConfig("mid", func(l *flow.LinkSpec) { l.Priority(50) })
		s.AddEnd("low")
		s.AddEnd("mid")
		s.AddEnd("high")
		// x must be reachable: give it a start
		s.AddStart("s").LinkAdd("x")
	})
	if err != nil {
		t.Fatal(err)
	}
	x := g.GetNode("x")
	got := linkNextIDs(x.NextLinks())
	if got != "high,mid,low" {
		t.Errorf("priority order = %q, want high,mid,low", got)
	}
}

// TestGraphStartInferred: with no START-typed node, the first node lacking incoming
// links becomes the start.
func TestGraphStartInferred(t *testing.T) {
	g, err := flow.Create("g", func(s *flow.GraphSpec) {
		s.AddActivity("a").LinkAdd("b") // a has no incoming link
		s.AddEnd("b")
	})
	if err != nil {
		t.Fatal(err)
	}
	if g.GetStart().ID() != "a" {
		t.Errorf("inferred start = %q, want a", g.GetStart().ID())
	}
}

// TestGraphNoStartNode: a pure cycle has no node without incoming links -> error.
func TestGraphNoStartNode(t *testing.T) {
	_, err := flow.Create("g", func(s *flow.GraphSpec) {
		s.AddActivity("a").LinkAdd("b")
		s.AddActivity("b").LinkAdd("a") // cycle, no start
	})
	if !errors.Is(err, flow.ErrNoStartNode) {
		t.Errorf("err = %v, want ErrNoStartNode", err)
	}
}

// TestNodeMeta covers the typed meta accessors.
func TestNodeMeta(t *testing.T) {
	g, err := flow.Create("g", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").MetaPut("cc", "demo@x").MetaPut("flag", "true").MetaPut("n", 3)
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	a := g.GetNode("a")
	if a.MetaAsString("cc") != "demo@x" {
		t.Errorf("cc = %q", a.MetaAsString("cc"))
	}
	if !a.MetaAsBool("flag") {
		t.Error("flag should be true")
	}
	if a.MetaAsNumber("n") != 3 {
		t.Errorf("n = %v", a.MetaAsNumber("n"))
	}
	if a.MetaAsString("missing") != "" {
		t.Error("missing should be empty")
	}
}

// TestGraphSpecAddReplaceRemove: AddNode replaces by id (keeping position);
// RemoveNode/ClearNodes behave.
func TestGraphSpecAddReplaceRemove(t *testing.T) {
	s := flow.NewGraphSpec("g")
	s.AddActivity("a")
	s.AddActivity("b")
	s.AddActivity("c")
	// replace b in place
	s.AddNode(flow.ActivityOf("b").Task("@B2"))
	if ids := specIDs(s.GetNodes()); ids != "a,b,c" {
		t.Errorf("after replace order = %q", ids)
	}
	if s.GetNode("b").GetTask() != "@B2" {
		t.Error("b not replaced")
	}
	s.RemoveNode("b")
	if ids := specIDs(s.GetNodes()); ids != "a,c" {
		t.Errorf("after remove order = %q", ids)
	}
	s.ClearNodes()
	if len(s.GetNodes()) != 0 {
		t.Error("clearNodes left nodes")
	}
}

func nodeIDs(nodes []*flow.Node) string {
	out := ""
	for i, n := range nodes {
		if i > 0 {
			out += ","
		}
		out += n.ID()
	}
	return out
}

func specIDs(nodes []*flow.NodeSpec) string {
	out := ""
	for i, n := range nodes {
		if i > 0 {
			out += ","
		}
		out += n.GetID()
	}
	return out
}

func linkNextIDs(links []*flow.Link) string {
	out := ""
	for i, l := range links {
		if i > 0 {
			out += ","
		}
		out += l.NextID()
	}
	return out
}
