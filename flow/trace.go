package flow

// NodeRecord is a lightweight record of the last-executed node of a graph (used for
// tracing and snapshot recovery). Mirrors Java's NodeRecord.
type NodeRecord struct {
	GraphID   string   `json:"graphId"`
	ID        string   `json:"id"`
	Title     string   `json:"title,omitempty"`
	Type      NodeType `json:"type"`
	Timestamp int64    `json:"timestamp,omitempty"`
}

// newRecord builds a NodeRecord for a node.
func newRecord(node *Node) NodeRecord {
	return NodeRecord{
		GraphID: node.graph.id,
		ID:      node.id,
		Title:   node.title,
		Type:    node.typ,
	}
}

// IsEnd reports whether the recorded node was an end node.
func (r NodeRecord) IsEnd() bool { return r.Type == NodeTypeEnd }

// Trace is a lightweight per-graph "last executed node" tracker, enabling snapshot
// recovery (re-walk from start to the last node, then continue).
// Mirrors Java's FlowTrace.
type Trace struct {
	enabled     bool
	rootGraphID string
	last        map[string]NodeRecord // graphID -> last record
}

// NewTrace creates an enabled Trace.
func NewTrace() *Trace { return &Trace{enabled: true, last: map[string]NodeRecord{}} }

// IsEnabled reports whether tracing is on.
func (t *Trace) IsEnabled() bool { return t.enabled }

// Enable enables/disables tracing.
func (t *Trace) Enable(enable bool) { t.enabled = enable }

// LastRecords returns all graph last-records.
func (t *Trace) LastRecords() []NodeRecord {
	out := make([]NodeRecord, 0, len(t.last))
	for _, r := range t.last {
		out = append(out, r)
	}
	return out
}

// Clear forgets all records.
func (t *Trace) Clear() {
	t.rootGraphID = ""
	t.last = map[string]NodeRecord{}
}

// RecordNode records node as the last-executed node of its graph.
func (t *Trace) RecordNode(graph *Graph, node *Node) {
	if !t.enabled {
		return
	}
	if t.rootGraphID == "" {
		t.rootGraphID = graph.id
	}
	if node == nil {
		delete(t.last, graph.id)
		return
	}
	t.last[graph.id] = newRecord(node)
}

// LastRecord returns the last record for graphID (root graph when "").
func (t *Trace) LastRecord(graphID string) *NodeRecord {
	if !t.enabled {
		return nil
	}
	if graphID == "" {
		graphID = t.rootGraphID
	}
	if graphID == "" {
		return nil
	}
	if r, ok := t.last[graphID]; ok {
		return &r
	}
	return nil
}

// LastNode returns the last-executed node of graph (graph.start when no record).
func (t *Trace) LastNode(graph *Graph) *Node {
	r := t.LastRecord(graph.id)
	if r == nil {
		return graph.GetStart()
	}
	return graph.GetNode(r.ID)
}

// LastNodeID returns the last node id for graphID ("" when none).
func (t *Trace) LastNodeID(graphID string) string {
	if r := t.LastRecord(graphID); r != nil {
		return r.ID
	}
	return ""
}

// IsEnd reports whether graphID's last node was an end node.
func (t *Trace) IsEnd(graphID string) bool {
	if r := t.LastRecord(graphID); r != nil {
		return r.IsEnd()
	}
	return false
}
