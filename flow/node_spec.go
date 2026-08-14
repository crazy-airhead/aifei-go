package flow

// NodeSpec is the mutable definition of a node, used while building a graph. It owns
// its outgoing LinkSpecs. Mirrors Java's NodeSpec.
type NodeSpec struct {
	id            string
	title         string
	typ           NodeType
	meta          map[string]any
	links         []*LinkSpec
	when          string
	whenComponent ConditionComponent
	task          string
	taskComponent TaskComponent
}

// NewNodeSpec creates a NodeSpec of the given type.
func NewNodeSpec(id string, typ NodeType) *NodeSpec { return &NodeSpec{id: id, typ: typ} }

// StartOf builds a start NodeSpec.
func StartOf(id string) *NodeSpec { return NewNodeSpec(id, NodeTypeStart) }

// EndOf builds an end NodeSpec.
func EndOf(id string) *NodeSpec { return NewNodeSpec(id, NodeTypeEnd) }

// ActivityOf builds an activity NodeSpec.
func ActivityOf(id string) *NodeSpec { return NewNodeSpec(id, NodeTypeActivity) }

// InclusiveOf builds an inclusive-gateway NodeSpec.
func InclusiveOf(id string) *NodeSpec { return NewNodeSpec(id, NodeTypeInclusive) }

// ExclusiveOf builds an exclusive-gateway NodeSpec.
func ExclusiveOf(id string) *NodeSpec { return NewNodeSpec(id, NodeTypeExclusive) }

// ParallelOf builds a parallel-gateway NodeSpec.
func ParallelOf(id string) *NodeSpec { return NewNodeSpec(id, NodeTypeParallel) }

// LoopOf builds a loop-gateway NodeSpec.
func LoopOf(id string) *NodeSpec { return NewNodeSpec(id, NodeTypeLoop) }

// Then runs configure against this spec and returns it (fluent).
func (n *NodeSpec) Then(configure func(*NodeSpec)) *NodeSpec {
	if configure != nil {
		configure(n)
	}
	return n
}

// Title sets the node title (fluent).
func (n *NodeSpec) Title(title string) *NodeSpec { n.title = title; return n }

// Meta merges entries into the node meta (fluent).
func (n *NodeSpec) Meta(meta map[string]any) *NodeSpec {
	for k, v := range meta {
		n.metaPut(k, v)
	}
	return n
}

// MetaPut adds/overwrites a meta entry (fluent).
func (n *NodeSpec) MetaPut(key string, value any) *NodeSpec { return n.metaPut(key, value) }

func (n *NodeSpec) metaPut(key string, value any) *NodeSpec {
	if key == "" {
		return n
	}
	if n.meta == nil {
		n.meta = make(map[string]any)
	}
	n.meta[key] = value
	return n
}

// LinkAddConfig adds an outgoing link to nextID, optionally configured (fluent).
func (n *NodeSpec) LinkAddConfig(nextID string, configure func(*LinkSpec)) *NodeSpec {
	ls := NewLinkSpec(nextID)
	if configure != nil {
		configure(ls)
	}
	n.links = append(n.links, ls)
	return n
}

// LinkAdd adds a plain outgoing link to nextID (fluent).
func (n *NodeSpec) LinkAdd(nextID string) *NodeSpec { return n.LinkAddConfig(nextID, nil) }

// LinkRemove removes outgoing links targeting nextID (fluent).
func (n *NodeSpec) LinkRemove(nextID string) *NodeSpec {
	kept := n.links[:0]
	for _, l := range n.links {
		if l.nextID != nextID {
			kept = append(kept, l)
		}
	}
	n.links = kept
	return n
}

// LinkClear removes all outgoing links (fluent).
func (n *NodeSpec) LinkClear() *NodeSpec { n.links = nil; return n }

// When sets the node condition expression (fluent).
func (n *NodeSpec) When(condition string) *NodeSpec { n.when = condition; return n }

// WhenCond sets a hard-coded node condition component (fluent).
func (n *NodeSpec) WhenCond(c ConditionComponent) *NodeSpec { n.whenComponent = c; return n }

// Task sets the node task expression (fluent).
func (n *NodeSpec) Task(task string) *NodeSpec { n.task = task; return n }

// TaskComp sets a hard-coded task component (fluent).
func (n *NodeSpec) TaskComp(c TaskComponent) *NodeSpec { n.taskComponent = c; return n }

// GetID returns the node id.
func (n *NodeSpec) GetID() string { return n.id }

// GetTitle returns the node title.
func (n *NodeSpec) GetTitle() string { return n.title }

// GetType returns the node type.
func (n *NodeSpec) GetType() NodeType { return n.typ }

// GetMeta returns the node meta map (may be nil).
func (n *NodeSpec) GetMeta() map[string]any { return n.meta }

// GetLinks returns the outgoing link specs.
func (n *NodeSpec) GetLinks() []*LinkSpec { return n.links }

// GetWhen returns the node condition expression.
func (n *NodeSpec) GetWhen() string { return n.when }

// GetWhenComponent returns the hard-coded node condition component.
func (n *NodeSpec) GetWhenComponent() ConditionComponent { return n.whenComponent }

// GetTask returns the node task expression.
func (n *NodeSpec) GetTask() string { return n.task }

// GetTaskComponent returns the hard-coded task component.
func (n *NodeSpec) GetTaskComponent() TaskComponent { return n.taskComponent }
