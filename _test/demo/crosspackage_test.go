package main_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/crazy-airhead/aifei-go/_test/demo/internal/flow"
	"github.com/crazy-airhead/aifei-go/_test/demo/internal/user"
	"github.com/crazy-airhead/aifei-go/aifei"
)

// stubInput is a minimal aifei.Input built from a parameter map — a
// programmatic request source (the Param half of the interface is what the
// generated service actions actually read).
type stubInput struct {
	params map[string]string
}

var _ aifei.Input = (*stubInput)(nil)

func newStubInput(params map[string]string) *stubInput { return &stubInput{params: params} }

func (f *stubInput) Has(name string) bool              { _, ok := f.params[name]; return ok }
func (f *stubInput) PathPara(index int) string         { return "" }
func (f *stubInput) PathParaByName(name string) string { return f.params[name] }
func (f *stubInput) Param(name string) string          { return f.params[name] }
func (f *stubInput) GetStr(key string, def ...string) string {
	if v, ok := f.params[key]; ok {
		return v
	}
	if len(def) > 0 {
		return def[0]
	}
	return ""
}
func (f *stubInput) GetInt(key string, def ...int) int             { return 0 }
func (f *stubInput) GetInt64(key string, def ...int64) int64       { return 0 }
func (f *stubInput) GetFloat64(key string, def ...float64) float64 { return 0 }
func (f *stubInput) GetBool(key string, def ...bool) bool          { return false }
func (f *stubInput) GetStrs(key string, def ...[]string) []string  { return nil }
func (f *stubInput) GetInts(key string, def ...[]int) []int        { return nil }
func (f *stubInput) GetBean(obj interface{}, keys ...string) error {
	return json.Unmarshal([]byte(f.params["__body"]), obj)
}
func (f *stubInput) GetMap(keys ...string) map[string]interface{} { return map[string]interface{}{} }
func (f *stubInput) Context() context.Context                     { return context.Background() }
func (f *stubInput) Header(name string) string                    { return "" }
func (f *stubInput) Path() string                                 { return "" }
func (f *stubInput) Body() []byte                                 { return nil }

// TestFlowMultiTablePackage exercises the flow domain package: two tables
// (sys_flow_task + sys_flow_log) live in one package with table-qualified
// identifiers, each keeping its own typed Dao and package-level functions.
func TestFlowMultiTablePackage(t *testing.T) {
	setupTest(t)

	owner := user.NewUser().Name_("quinn").Age_(30)
	owner.Insert()

	task := flow.NewFlowTask().Title_("implement generator").UserId_(owner.Id()).State_("doing")
	if _, err := task.Insert(); err != nil {
		t.Fatalf("task Insert failed: %v", err)
	}
	log1 := flow.NewFlowLog().TaskId_(task.Id()).UserId_(owner.Id()).Action_("claim")
	if _, err := log1.Insert(); err != nil {
		t.Fatalf("log Insert failed: %v", err)
	}

	// package-level functions of both tables coexist in one package
	found, err := flow.FlowTaskFindById(task.Id())
	if err != nil || found == nil {
		t.Fatalf("FlowTaskFindById failed: %v", err)
	}
	if found.Title() != "implement generator" {
		t.Errorf("unexpected title %q", found.Title())
	}

	logs, err := flow.FlowLogFindBy("task_id = ?", task.Id())
	if err != nil || len(logs) != 1 {
		t.Fatalf("FlowLogFindBy failed: %v (%d logs)", err, len(logs))
	}
	if logs[0].Action() != "claim" {
		t.Errorf("unexpected action %q", logs[0].Action())
	}

	// typed Dao batch IN, qualified per table
	tasks, err := flow.NewFlowTaskDao().FindByIds(task.Id())
	if err != nil || len(tasks) != 1 {
		t.Fatalf("FlowTaskDao.FindByIds failed: %v", err)
	}
	if tasks[0].State() != "doing" {
		t.Errorf("unexpected state %q", tasks[0].State())
	}
}

// TestServiceCrossPackageModel covers the "service calls another package's
// model" pattern: flow's GetById composes the task with its owner from the
// user package (向下单向，普通 import 即可，无环).
func TestServiceCrossPackageModel(t *testing.T) {
	setupTest(t)

	owner := user.NewUser().Name_("sara").Age_(28)
	owner.Insert()
	task := flow.NewFlowTask().Title_("review PR").UserId_(owner.Id()).State_("pending")
	task.Insert()

	svc := &flow.FlowTaskService{}
	out := svc.GetById(newStubInput(map[string]string{"id": "1"}))

	data, ok := out.Data().(map[string]interface{})
	if !ok {
		t.Fatalf("expected composite map data, got %T: %+v", out.Data(), out.Data())
	}
	userName, _ := data["userName"].(string)
	if userName != "sara" {
		t.Errorf("expected userName 'sara' from user package model, got %q", userName)
	}
	taskOut, ok := data["task"].(*flow.FlowTask)
	if !ok {
		t.Fatalf("expected *flow.FlowTask in data, got %T", data["task"])
	}
	if taskOut.Title() != "review PR" {
		t.Errorf("unexpected task title %q", taskOut.Title())
	}

	// missing id → fail path still works through the same wiring
	badOut := svc.GetById(newStubInput(map[string]string{"id": "x"}))
	if badOut.Data() != nil {
		t.Errorf("expected nil data for invalid id, got %+v", badOut.Data())
	}
}
