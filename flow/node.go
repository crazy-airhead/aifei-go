package flow

import (
	"strconv"
	"strings"
)

// Node is an immutable flow node: a point in the graph with a type, optional
// when-condition and task, meta, and outgoing links. Reverse lookups (prevLinks /
// prevNodes / nextNodes) are computed when the owning Graph is built.
// Mirrors Java's Node.
type Node struct {
	graph *Graph
	id    string
	title string
	typ   NodeType
	metas map[string]any
	when  ConditionDesc
	task  TaskDesc

	nextLinks []*Link // sorted by priority descending

	// reverse lookups (computed by Graph; nil for start/end where N/A)
	prevLinks []*Link
	prevNodes []*Node
	nextNodes []*Node

	// Attachment is an optional slot for extension parsing / custom use.
	Attachment any
}

// newNode builds a Node from a spec and its (already built) outgoing links. The
// links are sorted by priority descending (a stable sort keeps equal priorities in
// insertion order).
func newNode(g *Graph, spec *NodeSpec, links []*Link) *Node {
	sorted := append([]*Link(nil), links...)
	// higher priority first
	sortLinksByPriorityDesc(sorted)
	n := &Node{
		graph:     g,
		id:        spec.id,
		title:     spec.title,
		typ:       spec.typ,
		metas:     spec.meta,
		when:      newConditionDesc(g, spec.when, spec.whenComponent),
		nextLinks: sorted,
	}
	n.task = newTaskDesc(n, spec.task, spec.taskComponent)
	return n
}

// Graph returns the owning graph.
func (n *Node) Graph() *Graph { return n.graph }

// ID returns the node id.
func (n *Node) ID() string { return n.id }

// Title returns the node title.
func (n *Node) Title() string { return n.title }

// Type returns the node type.
func (n *Node) Type() NodeType { return n.typ }

// Metas returns the node meta map (may be nil).
func (n *Node) Metas() map[string]any { return n.metas }

// Meta returns a meta value by key.
func (n *Node) Meta(key string) any {
	if n.metas == nil {
		return nil
	}
	return n.metas[key]
}

// MetaAs returns a meta value by key (untyped convenience).
func (n *Node) MetaAs(key string) any { return n.Meta(key) }

// HasMeta reports whether the key exists.
func (n *Node) HasMeta(key string) bool {
	_, ok := n.metas[key]
	return ok
}

// MetaAsString returns a meta value as string ("" if absent).
func (n *Node) MetaAsString(key string) string {
	tmp := n.Meta(key)
	if tmp == nil {
		return ""
	}
	if s, ok := tmp.(string); ok {
		return s
	}
	return toString(tmp)
}

// MetaAsBool returns a meta value as bool. Strings parse via strconv; numbers are
// true when > 0; nil returns false.
func (n *Node) MetaAsBool(key string) bool {
	tmp := n.Meta(key)
	if tmp == nil {
		return false
	}
	if b, ok := tmp.(bool); ok {
		return b
	}
	if s, ok := tmp.(string); ok {
		b, _ := strconv.ParseBool(s)
		return b
	}
	return toFloat64(tmp) > 0
}

// MetaAsNumber returns a meta value as float64. Strings parse via strconv.
func (n *Node) MetaAsNumber(key string) float64 {
	tmp := n.Meta(key)
	if tmp == nil {
		return 0
	}
	if s, ok := tmp.(string); ok {
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
	return toFloat64(tmp)
}

// MetaOrDefault returns a meta value by key, or def when absent.
func (n *Node) MetaOrDefault(key string, def any) any {
	if v := n.Meta(key); v != nil {
		return v
	}
	return def
}

// PrevLinks returns the incoming links (for non-start nodes), in reverse link order.
func (n *Node) PrevLinks() []*Link { return n.prevLinks }

// NextLinks returns the outgoing links, sorted by priority descending.
func (n *Node) NextLinks() []*Link { return n.nextLinks }

// PrevNodes returns the source nodes of the incoming links (for non-start nodes).
func (n *Node) PrevNodes() []*Node { return n.prevNodes }

// NextNodes returns the target nodes of the outgoing links (for non-end nodes).
func (n *Node) NextNodes() []*Node { return n.nextNodes }

// NextNode returns the first target node, or nil if there are none.
func (n *Node) NextNode() *Node {
	if len(n.nextNodes) > 0 {
		return n.nextNodes[0]
	}
	return nil
}

// When returns the node condition.
func (n *Node) When() ConditionDesc { return n.when }

// Task returns the node task.
func (n *Node) Task() TaskDesc { return n.task }

// String renders a compact, Java-compatible representation of the node.
func (n *Node) String() string {
	var b strings.Builder
	b.WriteString("{id='")
	b.WriteString(n.id)
	b.WriteString("', type='")
	b.WriteString(n.typ.String())
	b.WriteByte('\'')
	if n.title != "" {
		b.WriteString(", title='")
		b.WriteString(n.title)
		b.WriteByte('\'')
	}
	if n.when.description != "" {
		b.WriteString(", when='")
		b.WriteString(n.when.description)
		b.WriteByte('\'')
	}
	if n.task.description != "" {
		b.WriteString(", task='")
		b.WriteString(n.task.description)
		b.WriteByte('\'')
	}
	if len(n.nextLinks) > 0 {
		b.WriteString(", link=")
		b.WriteString(formatLinks(n.nextLinks))
	}
	if len(n.metas) > 0 {
		b.WriteString(", meta=")
		b.WriteString(formatMap(n.metas))
	}
	b.WriteByte('}')
	return b.String()
}

// sortLinksByPriorityDesc sorts links in place, higher priority first, stable.
func sortLinksByPriorityDesc(links []*Link) {
	// stable insertion sort keeps equal priorities in original order
	for i := 1; i < len(links); i++ {
		for j := i; j > 0 && links[j].priority > links[j-1].priority; j-- {
			links[j], links[j-1] = links[j-1], links[j]
		}
	}
}
