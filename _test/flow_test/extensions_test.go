package flow_test

import (
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/dami"
	flow "github.com/crazy-airhead/aifei-go/flow"
)

// TestGraph_ToPlantuml renders a small graph and checks key fragments.
func TestGraph_ToPlantuml(t *testing.T) {
	g, err := flow.Create("puml", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAddConfig("a", func(l *flow.LinkSpec) { l.When("x > 1") })
		s.AddActivity("a").Task("@DoA").LinkAdd("e")
		s.AddExclusive("x") // unused gateway to exercise the gateway branch
		s.AddEnd("e")
	})
	if err != nil {
		t.Fatal(err)
	}
	out := g.ToPlantuml()
	for _, frag := range []string{"@startuml", "@enduml", "state s <<start>>", "state e <<end>>", "s --> a", "[x > 1]"} {
		if !strings.Contains(out, frag) {
			t.Errorf("plantuml missing %q\n%s", frag, out)
		}
	}
}

// TestContext_EventBus: events sent on the context bus reach listeners.
func TestContext_EventBus(t *testing.T) {
	ctx := flow.NewContext("ev")
	bus := ctx.EventBus()

	got := ""
	dami.ListenOn[string](bus, "topic", func(e *dami.Event[string]) error { got = e.Payload; return nil })

	c := flow.NewMapContainer()
	c.PutComponent("Emit", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error {
		dami.SendOn[string](ctx.EventBus(), "topic", "hello")
		return nil
	}))
	g, _ := flow.Create("ev", func(s *flow.GraphSpec) {
		s.AddStart("s").LinkAdd("a")
		s.AddActivity("a").Task("@Emit").LinkAdd("e")
		s.AddEnd("e")
	})
	engine := flow.NewEngine(flow.NewSimpleDriver(flow.WithContainer(c)))
	if err := engine.Eval(g, ctx); err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Errorf("event payload = %q, want hello", got)
	}
}
