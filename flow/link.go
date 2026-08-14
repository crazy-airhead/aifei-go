package flow

import (
	"strconv"
	"strings"
)

// Link is an immutable connection between two nodes (prevId → nextId), possibly
// carrying a when-condition, priority, and meta. Outgoing links of a node are kept
// sorted by priority descending. Mirrors Java's Link.
type Link struct {
	graph    *Graph
	nextID   string
	title    string
	metas    map[string]any
	priority int
	prevID   string
	when     ConditionDesc

	// Attachment is an optional slot for extension parsing / custom use.
	Attachment any
}

// newLink builds a Link from prevID and a LinkSpec.
func newLink(g *Graph, prevID string, spec *LinkSpec) *Link {
	return &Link{
		graph:    g,
		prevID:   prevID,
		nextID:   spec.nextID,
		title:    spec.title,
		priority: spec.priority,
		metas:    spec.meta,
		when:     newConditionDesc(g, spec.when, spec.whenComponent),
	}
}

// Graph returns the owning graph.
func (l *Link) Graph() *Graph { return l.graph }

// Title returns the link title.
func (l *Link) Title() string { return l.title }

// Metas returns the link meta map (may be nil).
func (l *Link) Metas() map[string]any { return l.metas }

// Meta returns a meta value by key.
func (l *Link) Meta(key string) any {
	if l.metas == nil {
		return nil
	}
	return l.metas[key]
}

// MetaAs returns a meta value by key (untyped convenience).
func (l *Link) MetaAs(key string) any { return l.Meta(key) }

// MetaOrDefault returns a meta value by key, or def when absent.
func (l *Link) MetaOrDefault(key string, def any) any {
	if v := l.Meta(key); v != nil {
		return v
	}
	return def
}

// When returns the branch out-flow condition.
func (l *Link) When() ConditionDesc { return l.when }

// PrevID returns the source node id.
func (l *Link) PrevID() string { return l.prevID }

// NextID returns the target node id.
func (l *Link) NextID() string { return l.nextID }

// PrevNode returns the source node (looked up in the graph; nil if missing).
func (l *Link) PrevNode() *Node { return l.graph.GetNode(l.prevID) }

// NextNode returns the target node (looked up in the graph; nil if missing).
func (l *Link) NextNode() *Node { return l.graph.GetNode(l.nextID) }

// String renders a compact, Java-compatible representation of the link.
func (l *Link) String() string {
	var b strings.Builder
	b.WriteString("{priority=")
	b.WriteString(strconv.Itoa(l.priority))
	b.WriteString(", prevId='")
	b.WriteString(l.prevID)
	b.WriteString("', nextId='")
	b.WriteString(l.nextID)
	b.WriteByte('\'')
	if l.title != "" {
		b.WriteString(", title='")
		b.WriteString(l.title)
		b.WriteByte('\'')
	}
	if len(l.metas) > 0 {
		b.WriteString(", meta=")
		b.WriteString(formatMap(l.metas))
	}
	if l.when.description != "" {
		b.WriteString(", when=")
		b.WriteString(l.when.description)
	}
	b.WriteByte('}')
	return b.String()
}
