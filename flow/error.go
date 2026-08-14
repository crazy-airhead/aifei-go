package flow

import "errors"

// Sentinel errors returned by the flow package.
var (
	// ErrNoStartNode is returned when a graph has no start-typed node and no node
	// without incoming links to infer one from.
	ErrNoStartNode = errors.New("flow: no start node found")
	// ErrNodeNotFound is returned when a node lookup by id fails.
	ErrNodeNotFound = errors.New("flow: node not found")
	// ErrGraphNotFound is returned when a graph lookup by id fails.
	ErrGraphNotFound = errors.New("flow: graph not found")
)
