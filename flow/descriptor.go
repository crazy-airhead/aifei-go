package flow

import "strings"

// ConditionDesc describes a branch/node condition: either an expression string
// (from config) or a hard-coded ConditionComponent. Mirrors Java's ConditionDesc.
type ConditionDesc struct {
	graph       *Graph
	description string
	component   ConditionComponent

	// Attachment is an optional slot for extension parsing / custom use.
	Attachment any
}

// newConditionDesc builds a ConditionDesc, trimming a blank description to "".
func newConditionDesc(g *Graph, description string, component ConditionComponent) ConditionDesc {
	return ConditionDesc{
		graph:       g,
		description: trimToEmpty(description),
		component:   component,
	}
}

// Graph returns the owning graph.
func (d ConditionDesc) Graph() *Graph { return d.graph }

// Description returns the condition expression string ("" if none).
func (d ConditionDesc) Description() string { return d.description }

// Component returns the hard-coded condition component (nil if none).
func (d ConditionDesc) Component() ConditionComponent { return d.component }

// IsEmpty reports whether neither an expression nor a component is set.
func (d ConditionDesc) IsEmpty() bool { return d.description == "" && d.component == nil }

// TaskDesc describes a node's task: either an expression string (from config) or a
// hard-coded TaskComponent. Mirrors Java's TaskDesc.
type TaskDesc struct {
	node        *Node
	description string
	component   TaskComponent

	// Attachment is an optional slot for extension parsing / custom use.
	Attachment any
}

// newTaskDesc builds a TaskDesc, trimming a blank description to "".
func newTaskDesc(node *Node, description string, component TaskComponent) TaskDesc {
	return TaskDesc{
		node:        node,
		description: trimToEmpty(description),
		component:   component,
	}
}

// Node returns the owning node.
func (t TaskDesc) Node() *Node { return t.node }

// Description returns the task expression string ("" if none).
func (t TaskDesc) Description() string { return t.description }

// Component returns the hard-coded task component (nil if none).
func (t TaskDesc) Component() TaskComponent { return t.component }

// IsEmpty reports whether neither an expression nor a component is set.
func (t TaskDesc) IsEmpty() bool { return t.description == "" && t.component == nil }

// trimToEmpty trims surrounding whitespace; an all-whitespace string becomes "".
func trimToEmpty(s string) string {
	if s == "" {
		return ""
	}
	return strings.TrimSpace(s)
}
