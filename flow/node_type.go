package flow

import "strings"

// NodeType is the kind of a flow node. Codes mirror Solon-Flow: gateways share the
// property code > NodeTypeActivity.
type NodeType int

const (
	// NodeTypeUnknown is the unknown type.
	NodeTypeUnknown NodeType = 0
	// NodeTypeStart is the (single) start node.
	NodeTypeStart NodeType = 1
	// NodeTypeEnd is an end node.
	NodeTypeEnd NodeType = 2
	// NodeTypeActivity is a plain activity node (runs a task, flows out freely).
	NodeTypeActivity NodeType = 11
	// NodeTypeExclusive is an exclusive/XOR gateway (at most one default branch).
	NodeTypeExclusive NodeType = 21
	// NodeTypeInclusive is an inclusive/OR gateway (selects all true branches).
	NodeTypeInclusive NodeType = 31
	// NodeTypeParallel is a parallel/AND gateway (all branches, then joins).
	NodeTypeParallel NodeType = 32
	// NodeTypeLoop is a loop gateway (iterates $for/$in).
	NodeTypeLoop NodeType = 33
)

// Code returns the numeric code of the type.
func (t NodeType) Code() int { return int(t) }

// String returns the canonical uppercase name (matches Java NodeType.name()).
func (t NodeType) String() string {
	switch t {
	case NodeTypeUnknown:
		return "UNKNOWN"
	case NodeTypeStart:
		return "START"
	case NodeTypeEnd:
		return "END"
	case NodeTypeActivity:
		return "ACTIVITY"
	case NodeTypeExclusive:
		return "EXCLUSIVE"
	case NodeTypeInclusive:
		return "INCLUSIVE"
	case NodeTypeParallel:
		return "PARALLEL"
	case NodeTypeLoop:
		return "LOOP"
	}
	return "UNKNOWN"
}

// IsGateway reports whether the type is a gateway (code > NodeTypeActivity).
func (t NodeType) IsGateway() bool { return t.Code() > int(NodeTypeActivity) }

// NodeTypeOf parses a type name (case-insensitive), defaulting to NodeTypeActivity.
// "iterator" is accepted as a deprecated alias for NodeTypeLoop.
func NodeTypeOf(name string) NodeType { return NodeTypeOfDefault(name, NodeTypeActivity) }

// NodeTypeOfDefault parses a type name (case-insensitive), returning def when blank
// or unrecognized. "iterator" is accepted as a deprecated alias for NodeTypeLoop.
func NodeTypeOfDefault(name string, def NodeType) NodeType {
	if name == "" {
		return def
	}
	upper := strings.ToUpper(name)
	for _, v := range []NodeType{
		NodeTypeUnknown, NodeTypeStart, NodeTypeEnd, NodeTypeActivity,
		NodeTypeExclusive, NodeTypeInclusive, NodeTypeParallel, NodeTypeLoop,
	} {
		if v.String() == upper {
			return v
		}
	}
	if upper == "ITERATOR" { // deprecated alias for LOOP
		return NodeTypeLoop
	}
	return def
}
