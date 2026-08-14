package flow

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// GraphSpecFromText parses graph config text (YAML or JSON — JSON is a YAML subset,
// so a single yaml.v3 decoder handles both, mirroring Solon-Flow's single Yaml.load)
// into a mutable GraphSpec.
func GraphSpecFromText(text string) (*GraphSpec, error) {
	dom := map[string]any{}
	if err := yaml.Unmarshal([]byte(text), &dom); err != nil {
		return nil, fmt.Errorf("flow: parse graph config: %w", err)
	}
	return graphSpecFromDom(dom)
}

// GraphFromText parses graph config text and builds an immutable Graph.
func GraphFromText(text string) (*Graph, error) {
	spec, err := GraphSpecFromText(text)
	if err != nil {
		return nil, err
	}
	return spec.Create()
}

// graphSpecFromDom builds a GraphSpec from a decoded config document. The layout is
// iterated in reverse so that nodes without an explicit link auto-chain to the next
// node in original order (mirrors Solon-Flow's fromDom).
func graphSpecFromDom(dom map[string]any) (*GraphSpec, error) {
	spec := NewGraphSpecFull(getStr(dom, "id"), getStr(dom, "title"), getStr(dom, "driver"))

	if meta := getMap(dom, "meta"); len(meta) > 0 {
		for k, v := range meta {
			spec.MetaPut(k, v)
		}
	}

	var layout []any
	if v, ok := dom["layout"]; ok {
		layout, _ = v.([]any)
	} else if v, ok := dom["nodes"]; ok { // deprecated v3.1 key
		layout, _ = v.([]any)
	}

	nodeSpecList := make([]*NodeSpec, 0, len(layout))
	var nodesLat *NodeSpec // previously processed node (next in original order)
	for i := len(layout); i > 0; i-- {
		n1, _ := layout[i-1].(map[string]any)

		n1id := getStr(n1, "id")
		if n1id == "" { // auto-generate id when missing
			n1id = fmt.Sprintf("n-%d", i)
		}

		ns := NewNodeSpec(n1id, NodeTypeOf(getStr(n1, "type")))
		ns.Title(getStr(n1, "title"))
		ns.Meta(getMap(n1, "meta"))
		ns.When(getStr(n1, "when"))
		ns.Task(getStr(n1, "task"))

		linkVal, hasLink := n1["link"]
		switch lv := linkVal.(type) {
		case []any: // array mode (multiple)
			for _, e := range lv {
				switch ev := e.(type) {
				case map[string]any:
					addLinkFromDom(ns, ev)
				case string:
					ns.LinkAdd(ev)
				default:
					if ev != nil { // single scalar value → target id
						ns.LinkAdd(fmt.Sprintf("%v", ev))
					}
				}
			}
		case map[string]any: // object mode (single)
			addLinkFromDom(ns, lv)
		case string: // single value mode
			ns.LinkAdd(lv)
		default: // null or missing → auto-build chain
			if !hasLink || linkVal == nil {
				if nodesLat != nil {
					ns.LinkAdd(nodesLat.GetID())
				}
			}
		}

		nodesLat = ns
		nodeSpecList = append(nodeSpecList, ns)
	}

	// Add in reverse to preserve original layout order in the spec's node map.
	for i := len(nodeSpecList); i > 0; i-- {
		spec.AddNode(nodeSpecList[i-1])
	}
	return spec, nil
}

// addLinkFromDom adds a link described by an object {nextId, title, meta, when}.
func addLinkFromDom(ns *NodeSpec, l1 map[string]any) {
	whenStr := ""
	if wv, ok := l1["when"]; ok {
		if s, ok := wv.(string); ok {
			whenStr = s
		}
	} else if cv, ok := l1["condition"]; ok { // deprecated v3.3 key
		if s, ok := cv.(string); ok {
			whenStr = s
		}
	}
	ns.LinkAddConfig(getStr(l1, "nextId"), func(ld *LinkSpec) {
		ld.Title(getStr(l1, "title"))
		ld.Meta(getMap(l1, "meta"))
		ld.When(whenStr)
	})
}

// getStr returns a string field, "" when absent or null. Non-string scalars are
// stringified (mirrors Java ONode.getString()).
func getStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// getMap returns a map[string]any field, nil when absent or null.
func getMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok || v == nil {
		return nil
	}
	if mm, ok := v.(map[string]any); ok {
		return mm
	}
	return nil
}

// fmtNodeNotFound wraps ErrNodeNotFound with the offending id.
func fmtNodeNotFound(id string) error {
	return fmt.Errorf("%w, id: %s", ErrNodeNotFound, id)
}
