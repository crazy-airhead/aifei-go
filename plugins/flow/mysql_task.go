package flowplugin

import (
	"encoding/json"
	"time"

	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/flow"
	"github.com/crazy-airhead/aifei-go/flow/workflow"
	"github.com/crazy-airhead/aifei-go/log"
)

// TaskRecord captures one task transition for the audit/history table.
type TaskRecord struct {
	GraphID  string // proc_def_id
	TaskID   string // task_id (node id)
	Source   *flow.Node
	Target   *flow.Node
	Assignee string // assignee
	State    workflow.TaskState
	FormID   any    // form_id (optional)
	Message  string // processing message / comment
}

// InsertFunc appends a task-history row. Swappable for testing.
type InsertFunc func(ctx flow.Context, rec TaskRecord) error

// TaskHistoryRecorder writes task transitions to the bpm_flow_task table.
type TaskHistoryRecorder struct {
	schema RepoSchema
	log    log.Logger
	insert InsertFunc
}

// NewTaskHistoryRecorder builds a recorder (append-only inserts).
func NewTaskHistoryRecorder(logger log.Logger, opts ...RepoOption) *TaskHistoryRecorder {
	r := &TaskHistoryRecorder{schema: DefaultRepoSchema(), log: logger}
	for _, o := range opts {
		o(&r.schema)
	}
	r.insert = r.insertDefault
	return r
}

// SetInserter injects a custom insert func (for testing or alternate backends).
func (r *TaskHistoryRecorder) SetInserter(f InsertFunc) { r.insert = f }

// Record appends a task-history row.
func (r *TaskHistoryRecorder) Record(ctx flow.Context, rec TaskRecord) error {
	return r.insert(ctx, rec)
}

func (r *TaskHistoryRecorder) insertDefault(ctx flow.Context, rec TaskRecord) error {
	varsJSON, _ := json.Marshal(serializableVars(ctx))
	row := db.NewRow(r.schema.TaskTable).
		Set("flow_ins_id", ctx.InstanceID()).
		Set("proc_def_id", rec.GraphID).
		Set("task_id", rec.TaskID)
	if rec.Source != nil {
		row.Set("source_node_code", rec.Source.ID()).
			Set("source_node_name", rec.Source.Title()).
			Set("source_node_type", rec.Source.Type().String())
	}
	if rec.Target != nil {
		row.Set("target_node_code", rec.Target.ID()).
			Set("target_node_name", rec.Target.Title()).
			Set("target_node_type", rec.Target.Type().String())
	}
	row.Set("assignee", rec.Assignee).
		Set("status", rec.State.Code()).
		SetIfNotNull("form_id", rec.FormID).
		Set("variables", varsJSON).
		SetIfNotBlank("message", rec.Message).
		Set("creator", auditor(ctx)).
		Set("create_time", time.Now())
	_, err := db.WithCtx(goCtx(ctx)).InsertRow(row)
	return err
}

// serializableVars returns JSON-native vars (drops non-serializable like the context
// self-ref / exchanger / funcs) for the task-history snapshot.
func serializableVars(ctx flow.Context) map[string]any {
	out := map[string]any{}
	for k, v := range ctx.Vars() {
		if k == "context" {
			continue
		}
		switch v.(type) {
		case func(), *flow.Exchanger:
			continue
		}
		out[k] = v
	}
	return out
}
