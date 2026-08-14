package flow

import (
	"fmt"
	"strings"
)

// nodeTag is the context key under which the current node is exposed to task scripts.
const nodeTag = "node"

// AbstractDriver is a base Driver that resolves task/condition descriptors:
//
//	@name   -> component (TaskComponent / ConditionComponent) from the container
//	#graphId -> subgraph (task only)
//	$metaKey -> resolve the task string from graph meta (dotted key), then eval
//	<else>  -> expression via Evaluation
//
// OnNodeStart/OnNodeEnd are no-ops; subclasses (e.g. the workflow driver) override.
// Mirrors Java's AbstractFlowDriver.
type AbstractDriver struct {
	evaluation Evaluation
	container  Container
	executor   func(fn func())
}

// NewAbstractDriver builds an AbstractDriver. nil evaluation defaults to
// EnjoyEvaluation; nil container to an empty MapContainer; nil executor to sync.
func NewAbstractDriver(evaluation Evaluation, container Container, executor func(fn func())) *AbstractDriver {
	if evaluation == nil {
		evaluation = NewEnjoyEvaluation()
	}
	if container == nil {
		container = NewMapContainer()
	}
	return &AbstractDriver{evaluation: evaluation, container: container, executor: executor}
}

// Evaluation returns the script evaluator.
func (d *AbstractDriver) Evaluation() Evaluation { return d.evaluation }

// Container returns the component container.
func (d *AbstractDriver) Container() Container { return d.container }

// Executor returns the optional async executor (nil = sync).
func (d *AbstractDriver) Executor() func(fn func()) { return d.executor }

// IsGraph reports whether description is a subgraph reference ("#graphId").
func (d *AbstractDriver) IsGraph(description string) bool { return strings.HasPrefix(description, "#") }

// IsComponent reports whether description is a component reference ("@name").
func (d *AbstractDriver) IsComponent(description string) bool {
	return strings.HasPrefix(description, "@")
}

// OnNodeStart is a no-op by default.
func (d *AbstractDriver) OnNodeStart(ex *Exchanger, node *Node) {}

// OnNodeEnd is a no-op by default.
func (d *AbstractDriver) OnNodeEnd(ex *Exchanger, node *Node) {}

// HandleCondition evaluates a condition descriptor.
func (d *AbstractDriver) HandleCondition(ex *Exchanger, cond ConditionDesc) (bool, error) {
	return d.handleConditionDo(ex, cond, cond.Description())
}

func (d *AbstractDriver) handleConditionDo(ex *Exchanger, cond ConditionDesc, description string) (bool, error) {
	if cond.Component() != nil {
		return cond.Component().Test(ex.Context())
	}
	if d.IsComponent(description) {
		return d.tryAsComponentCondition(ex, description)
	}
	return d.tryAsScriptCondition(ex, description)
}

func (d *AbstractDriver) tryAsComponentCondition(ex *Exchanger, description string) (bool, error) {
	beanName := description[1:]
	comp := d.container.GetComponent(beanName)
	if comp == nil {
		return false, fmt.Errorf("flow: the condition component '%s' not exist", beanName)
	}
	cc, ok := comp.(ConditionComponent)
	if !ok {
		return false, fmt.Errorf("flow: the component '%s' is not a ConditionComponent", beanName)
	}
	return cc.Test(ex.Context())
}

func (d *AbstractDriver) tryAsScriptCondition(ex *Exchanger, description string) (bool, error) {
	return d.evaluation.RunCondition(ex.Context(), description)
}

// PostHandleTask runs a task (no-op for empty tasks).
func (d *AbstractDriver) PostHandleTask(ex *Exchanger, task TaskDesc) error {
	if task.IsEmpty() {
		return nil
	}
	return d.handleTaskDo(ex, task)
}

func (d *AbstractDriver) handleTaskDo(ex *Exchanger, task TaskDesc) (err error) {
	defer func() {
		// a task may switch the exchanger (subgraph); restore this one
		ex.ctx.setExchanger(ex)
	}()
	if task.Component() != nil {
		return task.Component().Run(ex.Context(), task.Node())
	}
	desc := task.Description()
	switch {
	case d.IsGraph(desc):
		return d.tryAsGraphTask(ex, desc)
	case d.IsComponent(desc):
		return d.tryAsComponentTask(ex, task, desc)
	default:
		return d.tryAsScriptTask(ex, task, desc)
	}
}

func (d *AbstractDriver) tryAsGraphTask(ex *Exchanger, description string) error {
	graphID := description[1:]
	graph, err := ex.Engine().GetGraphOrThrow(graphID)
	if err != nil {
		return err
	}
	ex.RunGraph(graph)
	return nil
}

func (d *AbstractDriver) tryAsComponentTask(ex *Exchanger, task TaskDesc, description string) error {
	beanName := description[1:]
	comp := d.container.GetComponent(beanName)
	if comp == nil {
		return fmt.Errorf("flow: the task component '%s' not exist", beanName)
	}
	tc, ok := comp.(TaskComponent)
	if !ok {
		return fmt.Errorf("flow: the component '%s' is not a TaskComponent", beanName)
	}
	return tc.Run(ex.Context(), task.Node())
}

func (d *AbstractDriver) tryAsScriptTask(ex *Exchanger, task TaskDesc, description string) error {
	if strings.HasPrefix(description, "$") {
		metaName := description[1:]
		resolved, ok := getDepthMeta(task.Node().Graph().GetMetas(), metaName)
		if !ok || resolved == "" {
			return fmt.Errorf("flow: graph meta not found: %s", metaName)
		}
		description = resolved
	}
	// expose the current node to the script under "node"
	bak := ex.Context().Get(nodeTag)
	ex.ctx.Put(nodeTag, task.Node())
	defer func() {
		if bak == nil {
			ex.ctx.Remove(nodeTag)
		} else {
			ex.ctx.Put(nodeTag, bak)
		}
	}()
	return d.evaluation.RunTask(ex.Context(), description)
}

// getDepthMeta resolves a dotted key (a.b.c) against a meta map.
func getDepthMeta(metas map[string]any, key string) (string, bool) {
	fragments := strings.Split(key, ".")
	var cur any = metas
	for _, f := range fragments {
		mm, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		v, ok := mm[f]
		if !ok {
			return "", false
		}
		cur = v
	}
	if s, ok := cur.(string); ok {
		return s, true
	}
	return fmt.Sprintf("%v", cur), true
}
