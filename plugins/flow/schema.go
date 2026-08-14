// Package flowplugin is the aifei-go flow integration plugin: it assembles a flow
// engine + workflow executor with a built-in MySQL state repository and task-history
// recorder. See docs/arch/flow/06-mysql-repository.md.
package flowplugin

// RepoSchema names the persistence tables and key columns (overridable).
type RepoSchema struct {
	RepoTable string // default "bpm_flow_repository"
	TaskTable string // default "bpm_flow_task"
}

// DefaultRepoSchema returns the schema matching the reference DDL.
func DefaultRepoSchema() RepoSchema {
	return RepoSchema{RepoTable: "bpm_flow_repository", TaskTable: "bpm_flow_task"}
}
