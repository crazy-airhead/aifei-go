package flow_test

import (
	"testing"

	flow "github.com/crazy-airhead/aifei-go/flow"
)

// c1.yml from the Solon-Flow README: explicit links, start -> activity -> end.
const c1YAML = `
id: "c1"
layout:
  - { id: "n1", type: "start", link: "n2"}
  - { id: "n2", type: "activity", link: "n3"}
  - { id: "n3", type: "end"}
`

func TestFromTextYAMLExplicitLinks(t *testing.T) {
	g, err := flow.GraphFromText(c1YAML)
	if err != nil {
		t.Fatal(err)
	}
	if g.GetID() != "c1" {
		t.Errorf("id = %q", g.GetID())
	}
	if g.GetStart().ID() != "n1" {
		t.Errorf("start = %q", g.GetStart().ID())
	}
	if got := linkNextIDs(g.GetNode("n1").NextLinks()); got != "n2" {
		t.Errorf("n1 -> %q", got)
	}
	if got := linkNextIDs(g.GetNode("n2").NextLinks()); got != "n3" {
		t.Errorf("n2 -> %q", got)
	}
	if g.GetNode("n3").Type() != flow.NodeTypeEnd {
		t.Error("n3 not end")
	}
}

// c2.yml from the README: no explicit links and (mostly) no ids — exercises the
// auto-chain and auto-id logic.
const c2YAML = `
id: "c2"
layout:
  - { type: "start"}
  - { when: "order.getAmount() > 100", task: "order.setScore(100);"}
  - { when: "order.getAmount() > 500", task: "order.setScore(500);"}
  - { type: "end"}
`

func TestFromTextYAMLAutoChainAndAutoID(t *testing.T) {
	g, err := flow.GraphFromText(c2YAML)
	if err != nil {
		t.Fatal(err)
	}
	// 4 nodes, auto-named n-1 .. n-4 in layout order
	if got := nodeIDs(g.GetNodes()); got != "n-1,n-2,n-3,n-4" {
		t.Errorf("auto ids = %q", got)
	}
	// start -> n-2 -> n-3 -> n-4(end)
	if g.GetStart().ID() != "n-1" {
		t.Errorf("start = %q", g.GetStart().ID())
	}
	if got := linkNextIDs(g.GetNode("n-1").NextLinks()); got != "n-2" {
		t.Errorf("n-1 -> %q", got)
	}
	if got := linkNextIDs(g.GetNode("n-2").NextLinks()); got != "n-3" {
		t.Errorf("n-2 -> %q", got)
	}
	// when + task carried through
	if g.GetNode("n-2").When().Description() != "order.getAmount() > 100" {
		t.Errorf("n-2 when = %q", g.GetNode("n-2").When().Description())
	}
	if g.GetNode("n-2").Task().Description() != "order.setScore(100);" {
		t.Errorf("n-2 task = %q", g.GetNode("n-2").Task().Description())
	}
	if g.GetNode("n-4").Type() != flow.NodeTypeEnd {
		t.Error("n-4 not end")
	}
}

// JSON is a YAML subset; the single decoder must parse it too.
func TestFromTextJSON(t *testing.T) {
	js := `{"id":"j1","layout":[
		{"id":"s","type":"start","link":"a"},
		{"id":"a","type":"activity","link":"e","task":"@X"},
		{"id":"e","type":"end"}
	]}`
	g, err := flow.GraphFromText(js)
	if err != nil {
		t.Fatal(err)
	}
	if g.GetID() != "j1" {
		t.Errorf("id = %q", g.GetID())
	}
	if g.GetNode("a").Task().Description() != "@X" {
		t.Errorf("a task = %q", g.GetNode("a").Task().Description())
	}
	if got := linkNextIDs(g.GetNode("s").NextLinks()); got != "a" {
		t.Errorf("s -> %q", got)
	}
}

// Link variants: a single string, an array of values, and an array of objects.
func TestFromTextLinkVariants(t *testing.T) {
	yml := `
id: "v"
layout:
  - { id: "s", type: "start", link: "a"}              # single string
  - { id: "a", type: "inclusive", link: ["b", "c"]}   # array of values
  - { id: "b", type: "activity"}                      # no link: auto-chain to next node (c)
  - { id: "c", type: "activity", link: [{nextId: e2, when: "x>1"}]}  # array of objects
  - { id: "e", type: "end"}
  - { id: "e2", type: "end"}
`
	g, err := flow.GraphFromText(yml)
	if err != nil {
		t.Fatal(err)
	}
	if got := linkNextIDs(g.GetNode("a").NextLinks()); got != "b,c" {
		t.Errorf("a branches = %q", got)
	}
	// b auto-chains to the immediately-following layout node (c)
	if got := linkNextIDs(g.GetNode("b").NextLinks()); got != "c" {
		t.Errorf("b auto-link = %q", got)
	}
	// object link carries when
	cLinks := g.GetNode("c").NextLinks()
	if len(cLinks) != 1 || cLinks[0].NextID() != "e2" || cLinks[0].When().Description() != "x>1" {
		t.Errorf("c link = %+v", cLinks)
	}
}

// Graph + node + link meta round-trips through the parser.
func TestFromTextMeta(t *testing.T) {
	yml := `
id: "m"
meta:
  owner: team-a
layout:
  - { id: "s", type: "start", link: {nextId: a, meta: {k: v}}}
  - { id: "a", type: "activity", meta: {cc: "demo@x"}, link: "e"}
  - { id: "e", type: "end"}
`
	g, err := flow.GraphFromText(yml)
	if err != nil {
		t.Fatal(err)
	}
	if g.Meta("owner") != "team-a" {
		t.Errorf("graph meta owner = %v", g.Meta("owner"))
	}
	if g.GetNode("a").MetaAsString("cc") != "demo@x" {
		t.Errorf("node a meta cc = %q", g.GetNode("a").MetaAsString("cc"))
	}
	if g.GetNode("s").NextLinks()[0].Meta("k") != "v" {
		t.Errorf("link meta k = %v", g.GetNode("s").NextLinks()[0].Meta("k"))
	}
}

// Deprecated keys: "nodes" (v3.1) for layout, "condition" (v3.3) for when.
func TestFromTextDeprecatedKeys(t *testing.T) {
	yml := `
id: "d"
nodes:
  - { id: "s", type: "start", link: "a"}
  - { id: "a", type: "exclusive", link: [{nextId: b, condition: "x>1"}]}
  - { id: "b", type: "end"}
`
	g, err := flow.GraphFromText(yml)
	if err != nil {
		t.Fatal(err)
	}
	aLinks := g.GetNode("a").NextLinks()
	if len(aLinks) != 1 || aLinks[0].When().Description() != "x>1" {
		t.Errorf("deprecated condition -> when = %+v", aLinks)
	}
}

// SpecFromText yields a mutable spec the caller can tweak before building.
func TestSpecFromTextMutable(t *testing.T) {
	spec, err := flow.GraphSpecFromText(c1YAML)
	if err != nil {
		t.Fatal(err)
	}
	// mutate: add a branch from n2
	spec.GetNode("n2").LinkAdd("n4")
	spec.AddEnd("n4")
	g, err := spec.Create()
	if err != nil {
		t.Fatal(err)
	}
	if got := linkNextIDs(g.GetNode("n2").NextLinks()); got != "n3,n4" {
		t.Errorf("after mutate n2 -> %q", got)
	}
}
