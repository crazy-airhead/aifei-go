package flow

import (
	"strings"
)

// ToPlantuml renders the graph as a PlantUML state-diagram text (matches Solon-Flow's
// Graph.toPlantuml output).
func (g *Graph) ToPlantuml() string {
	var b strings.Builder
	b.WriteString("@startuml\n")
	b.WriteString("skinparam shadowing false\n")
	b.WriteString("skinparam state {\n")
	b.WriteString("  BackgroundColor White\n")
	b.WriteString("  BorderColor #333333\n")
	b.WriteString("  FontName SansSerif\n")
	b.WriteString("  BackgroundColor<<Gateway>> #fff9c4\n")
	b.WriteString("  BorderColor<<Gateway>> #fbc02d\n")
	b.WriteString("}\n")

	if g.title != "" {
		b.WriteString("title ")
		b.WriteString(g.title)
		b.WriteString("\n")
	}

	for _, node := range g.GetNodes() {
		id := node.ID()
		title := node.Title()
		if title == "" {
			title = id
		}
		switch node.Type() {
		case NodeTypeStart:
			b.WriteString("state ")
			b.WriteString(id)
			b.WriteString(" <<start>>\n")
			b.WriteString(id)
			b.WriteString(" : ")
			b.WriteString(title)
			b.WriteString("\n")
		case NodeTypeEnd:
			b.WriteString("state ")
			b.WriteString(id)
			b.WriteString(" <<end>>\n")
			b.WriteString(id)
			b.WriteString(" : ")
			b.WriteString(title)
			b.WriteString("\n")
		case NodeTypeExclusive, NodeTypeInclusive, NodeTypeParallel, NodeTypeLoop:
			b.WriteString("state ")
			b.WriteString(id)
			b.WriteString(" <<choice>> <<Gateway>>\n")
			b.WriteString(id)
			b.WriteString(" : ")
			b.WriteString(node.Type().String())
			b.WriteString("\n")
		default:
			b.WriteString("state \"")
			b.WriteString(title)
			b.WriteString("\" as ")
			b.WriteString(id)
			b.WriteString("\n")
			if node.Task().Description() != "" {
				b.WriteString(id)
				b.WriteString(" : ")
				b.WriteString(node.Task().Description())
				b.WriteString("\n")
			}
		}
	}

	for _, l := range g.links {
		b.WriteString(l.PrevID())
		b.WriteString(" --> ")
		b.WriteString(l.NextID())
		var labels []string
		if l.Title() != "" {
			labels = append(labels, l.Title())
		}
		if l.When().Description() != "" {
			labels = append(labels, "["+l.When().Description()+"]")
		}
		if len(labels) > 0 {
			b.WriteString(" : ")
			b.WriteString(strings.Join(labels, " "))
		}
		b.WriteString("\n")
	}

	b.WriteString("@enduml")
	return b.String()
}
