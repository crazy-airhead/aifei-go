package flow

// Graph is an immutable flow graph: ordered nodes, links, and a start node. Build it
// from a GraphSpec (NewGraph / Create) or from config text (GraphFromText).
// Mirrors Java's Graph.
type Graph struct {
	id     string
	title  string
	driver string
	metas  map[string]any

	nodes map[string]*Node
	order []string // preserves node insertion order (Go maps are unordered)
	links []*Link
	start *Node
}

// NewGraph builds an immutable Graph from spec, computing reverse lookups and
// resolving the start node. Returns ErrNoStartNode if no start node can be found.
func NewGraph(spec *GraphSpec) (*Graph, error) {
	g := &Graph{
		id:     spec.id,
		title:  spec.title,
		driver: spec.driver,
		metas:  spec.meta,
		nodes:  map[string]*Node{},
	}

	var linkAry []*Link
	var ordered []*Node
	for _, ns := range spec.GetNodes() {
		tmp := make([]*Link, 0, len(ns.links))
		for _, ls := range ns.links {
			tmp = append(tmp, newLink(g, ns.id, ls))
		}
		linkAry = append(linkAry, tmp...)

		node := newNode(g, ns, tmp)
		g.nodes[node.id] = node
		g.order = append(g.order, node.id)
		ordered = append(ordered, node)
		if ns.typ == NodeTypeStart {
			g.start = node
		}
	}
	g.links = linkAry

	// Post-pass: compute reverse lookups eagerly (graph is immutable afterwards, so
	// these are race-free reads).
	for _, node := range ordered {
		if node.typ != NodeTypeEnd {
			nn := make([]*Node, 0, len(node.nextLinks))
			for _, l := range node.nextLinks {
				if t := g.nodes[l.nextID]; t != nil {
					nn = append(nn, t)
				}
			}
			node.nextNodes = nn
		}
		if node.typ != NodeTypeStart {
			var pl []*Link
			for _, l := range linkAry {
				if l.nextID == node.id {
					pl = append(pl, l)
				}
			}
			reverseLinks(pl) // match Java's Collections.reverse
			node.prevLinks = pl

			pn := make([]*Node, 0, len(pl))
			for _, l := range pl {
				if t := g.nodes[l.prevID]; t != nil {
					pn = append(pn, t)
				}
			}
			node.prevNodes = pn
		}
	}

	// Resolve start: an explicit START-typed node wins; otherwise the first node
	// (in order) without incoming links.
	if g.start == nil {
		for _, id := range g.order {
			if len(g.nodes[id].prevLinks) == 0 {
				g.start = g.nodes[id]
				break
			}
		}
	}
	if g.start == nil {
		return nil, ErrNoStartNode
	}
	return g, nil
}

// Create builds a graph from an inline definition: flow.Create("demo", func(s *flow.GraphSpec){...}).
func Create(id string, definition func(*GraphSpec)) (*Graph, error) {
	spec := NewGraphSpec(id)
	definition(spec)
	return spec.Create()
}

// CreateWithTitle builds a graph with a title.
func CreateWithTitle(id, title string, definition func(*GraphSpec)) (*Graph, error) {
	spec := NewGraphSpecWithTitle(id, title)
	definition(spec)
	return spec.Create()
}

// CreateWithDriver builds a graph with a title and driver name.
func CreateWithDriver(id, title, driver string, definition func(*GraphSpec)) (*Graph, error) {
	spec := NewGraphSpecFull(id, title, driver)
	definition(spec)
	return spec.Create()
}

// GetID returns the graph id.
func (g *Graph) GetID() string { return g.id }

// GetTitle returns the graph title.
func (g *Graph) GetTitle() string { return g.title }

// GetDriver returns the graph driver name ("" if default).
func (g *Graph) GetDriver() string { return g.driver }

// GetMetas returns the graph meta map (may be nil).
func (g *Graph) GetMetas() map[string]any { return g.metas }

// Meta returns a graph meta value by key.
func (g *Graph) Meta(key string) any {
	if g.metas == nil {
		return nil
	}
	return g.metas[key]
}

// MetaAs returns a graph meta value by key (untyped convenience).
func (g *Graph) MetaAs(key string) any { return g.Meta(key) }

// MetaOrDefault returns a graph meta value by key, or def when absent.
func (g *Graph) MetaOrDefault(key string, def any) any {
	if v := g.Meta(key); v != nil {
		return v
	}
	return def
}

// GetStart returns the start node.
func (g *Graph) GetStart() *Node { return g.start }

// GetNodes returns all nodes in insertion order.
func (g *Graph) GetNodes() []*Node {
	out := make([]*Node, 0, len(g.order))
	for _, id := range g.order {
		out = append(out, g.nodes[id])
	}
	return out
}

// GetLinks returns all links.
func (g *Graph) GetLinks() []*Link { return g.links }

// GetNode returns a node by id (nil if absent).
func (g *Graph) GetNode(id string) *Node { return g.nodes[id] }

// GetNodeOrThrow returns a node by id, or an error wrapping ErrNodeNotFound.
func (g *Graph) GetNodeOrThrow(id string) (*Node, error) {
	n := g.GetNode(id)
	if n == nil {
		return nil, fmtNodeNotFound(id)
	}
	return n, nil
}

func reverseLinks(a []*Link) {
	for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
		a[i], a[j] = a[j], a[i]
	}
}
