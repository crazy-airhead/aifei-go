package flow_test

import (
	"os"
	"path/filepath"
	"testing"

	flow "github.com/crazy-airhead/aifei-go/flow"
)

// TestEngine_LoadYmlFile loads a graph from a .yml resource file (the real deployment
// path: read file -> parse -> eval), exercising file loading + YAML parsing
// (object links with when, exclusive routing) + component dispatch end to end.
func TestEngine_LoadYmlFile(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "rule.yml"))
	if err != nil {
		t.Fatalf("read testdata/rule.yml: %v", err)
	}
	g, err := flow.GraphFromText(string(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if g.GetID() != "rule" {
		t.Errorf("graph id = %q, want rule", g.GetID())
	}

	c := flow.NewMapContainer()
	c.PutComponent("SetAmount", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error { ctx.Put("amount", 150); return nil }))
	c.PutComponent("ScoreHigh", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error { ctx.Put("score", "high"); return nil }))
	c.PutComponent("ScoreLow", flow.TaskFunc(func(ctx flow.Context, _ *flow.Node) error { ctx.Put("score", "low"); return nil }))
	engine := flow.NewEngine(flow.NewSimpleDriver(flow.WithContainer(c)))
	engine.Load(g)

	ctx := flow.NewContext("f1")
	if err := engine.EvalByID("rule", ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Get("amount") == nil {
		t.Error("SetAmount should have run")
	}
	if ctx.Get("score") != "high" {
		t.Errorf("exclusive routed to score=%v, want high (amount>=100)", ctx.Get("score"))
	}
}
