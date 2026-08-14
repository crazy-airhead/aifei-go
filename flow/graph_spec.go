package flow

// GraphSpec is the mutable definition of a graph (id/title/driver/meta + ordered
// nodes). Build it programmatically (AddStart/AddActivity/...) or from config
// (GraphSpecFromText), then call Create to produce an immutable Graph.
// Mirrors Java's GraphSpec.
type GraphSpec struct {
	id     string
	title  string
	driver string
	meta   map[string]any
	nodes  map[string]*NodeSpec
	order  []string // preserves insertion order (Go maps are unordered)
}

// NewGraphSpec creates a GraphSpec; title defaults to id, driver to "".
func NewGraphSpec(id string) *GraphSpec { return NewGraphSpecFull(id, "", "") }

// NewGraphSpecWithTitle creates a GraphSpec with a title.
func NewGraphSpecWithTitle(id, title string) *GraphSpec { return NewGraphSpecFull(id, title, "") }

// NewGraphSpecFull creates a GraphSpec with id, title, and driver. A blank title
// defaults to id.
func NewGraphSpecFull(id, title, driver string) *GraphSpec {
	if title == "" {
		title = id
	}
	return &GraphSpec{id: id, title: title, driver: driver, nodes: map[string]*NodeSpec{}}
}

// Then runs definition against this spec and returns it (fluent).
func (s *GraphSpec) Then(definition func(*GraphSpec)) *GraphSpec {
	if definition != nil {
		definition(s)
	}
	return s
}

// Create builds an immutable Graph from this spec.
func (s *GraphSpec) Create() (*Graph, error) { return NewGraph(s) }

// AddNode adds (or replaces) a node spec, preserving position for existing ids.
func (s *GraphSpec) AddNode(nodeSpec *NodeSpec) *NodeSpec {
	if _, ok := s.nodes[nodeSpec.id]; !ok {
		s.order = append(s.order, nodeSpec.id)
	}
	s.nodes[nodeSpec.id] = nodeSpec
	return nodeSpec
}

// RemoveNode removes a node spec by id.
func (s *GraphSpec) RemoveNode(nodeID string) *NodeSpec {
	ns := s.nodes[nodeID]
	delete(s.nodes, nodeID)
	for i, id := range s.order {
		if id == nodeID {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return ns
}

// GetNode returns a node spec by id (for modification).
func (s *GraphSpec) GetNode(id string) *NodeSpec { return s.nodes[id] }

// ClearNodes removes all node specs.
func (s *GraphSpec) ClearNodes() {
	s.nodes = map[string]*NodeSpec{}
	s.order = nil
}

// MetaPut adds/overwrites a graph meta entry (fluent).
func (s *GraphSpec) MetaPut(key string, value any) *GraphSpec {
	if s.meta == nil {
		s.meta = make(map[string]any)
	}
	s.meta[key] = value
	return s
}

// AddStart adds a start node.
func (s *GraphSpec) AddStart(id string) *NodeSpec { return s.AddNode(NewNodeSpec(id, NodeTypeStart)) }

// AddEnd adds an end node.
func (s *GraphSpec) AddEnd(id string) *NodeSpec { return s.AddNode(NewNodeSpec(id, NodeTypeEnd)) }

// AddActivity adds an activity node.
func (s *GraphSpec) AddActivity(id string) *NodeSpec {
	return s.AddNode(NewNodeSpec(id, NodeTypeActivity))
}

// AddInclusive adds an inclusive-gateway node.
func (s *GraphSpec) AddInclusive(id string) *NodeSpec {
	return s.AddNode(NewNodeSpec(id, NodeTypeInclusive))
}

// AddExclusive adds an exclusive-gateway node.
func (s *GraphSpec) AddExclusive(id string) *NodeSpec {
	return s.AddNode(NewNodeSpec(id, NodeTypeExclusive))
}

// AddParallel adds a parallel-gateway node.
func (s *GraphSpec) AddParallel(id string) *NodeSpec {
	return s.AddNode(NewNodeSpec(id, NodeTypeParallel))
}

// AddLoop adds a loop-gateway node.
func (s *GraphSpec) AddLoop(id string) *NodeSpec { return s.AddNode(NewNodeSpec(id, NodeTypeLoop)) }

// addNamed adds a node of typ backed by a NamedTaskComponent (id=name, title=title).
func (s *GraphSpec) addNamed(typ NodeType, c NamedTaskComponent) *NodeSpec {
	return s.AddNode(NewNodeSpec(c.Name(), typ)).TaskComp(c).Title(c.Title())
}

// AddActivityNamed adds an activity node from a NamedTaskComponent.
func (s *GraphSpec) AddActivityNamed(c NamedTaskComponent) *NodeSpec {
	return s.addNamed(NodeTypeActivity, c)
}

// AddInclusiveNamed adds an inclusive-gateway node from a NamedTaskComponent.
func (s *GraphSpec) AddInclusiveNamed(c NamedTaskComponent) *NodeSpec {
	return s.addNamed(NodeTypeInclusive, c)
}

// AddExclusiveNamed adds an exclusive-gateway node from a NamedTaskComponent.
func (s *GraphSpec) AddExclusiveNamed(c NamedTaskComponent) *NodeSpec {
	return s.addNamed(NodeTypeExclusive, c)
}

// AddParallelNamed adds a parallel-gateway node from a NamedTaskComponent.
func (s *GraphSpec) AddParallelNamed(c NamedTaskComponent) *NodeSpec {
	return s.addNamed(NodeTypeParallel, c)
}

// AddLoopNamed adds a loop-gateway node from a NamedTaskComponent.
func (s *GraphSpec) AddLoopNamed(c NamedTaskComponent) *NodeSpec { return s.addNamed(NodeTypeLoop, c) }

// GetID returns the graph id.
func (s *GraphSpec) GetID() string { return s.id }

// GetTitle returns the graph title.
func (s *GraphSpec) GetTitle() string { return s.title }

// GetDriver returns the graph driver name ("" if default).
func (s *GraphSpec) GetDriver() string { return s.driver }

// GetMeta returns the graph meta map (may be nil).
func (s *GraphSpec) GetMeta() map[string]any { return s.meta }

// GetNodes returns the node specs in insertion order.
func (s *GraphSpec) GetNodes() []*NodeSpec {
	out := make([]*NodeSpec, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.nodes[id])
	}
	return out
}
