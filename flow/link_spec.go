package flow

// LinkSpec is the mutable definition of a connection between two nodes, used while
// building a graph. The target is nextId; the source is whichever NodeSpec owns it.
// Mirrors Java's LinkSpec.
type LinkSpec struct {
	nextID        string
	title         string
	meta          map[string]any
	when          string
	whenComponent ConditionComponent
	priority      int
}

// NewLinkSpec creates a LinkSpec targeting nextID.
func NewLinkSpec(nextID string) *LinkSpec { return &LinkSpec{nextID: nextID} }

// Then runs configure against this spec and returns it (fluent).
func (l *LinkSpec) Then(configure func(*LinkSpec)) *LinkSpec {
	if configure != nil {
		configure(l)
	}
	return l
}

// Title sets the link title (fluent).
func (l *LinkSpec) Title(title string) *LinkSpec { l.title = title; return l }

// Meta replaces the link meta map (fluent).
func (l *LinkSpec) Meta(meta map[string]any) *LinkSpec { l.meta = meta; return l }

// MetaPut adds/overwrites a meta entry (fluent).
func (l *LinkSpec) MetaPut(key string, value any) *LinkSpec {
	if l.meta == nil {
		l.meta = make(map[string]any)
	}
	l.meta[key] = value
	return l
}

// When sets the branch condition expression (fluent).
func (l *LinkSpec) When(condition string) *LinkSpec { l.when = condition; return l }

// WhenCond sets a hard-coded branch condition component (fluent).
func (l *LinkSpec) WhenCond(c ConditionComponent) *LinkSpec { l.whenComponent = c; return l }

// Priority sets the priority (higher wins; fluent).
func (l *LinkSpec) Priority(priority int) *LinkSpec { l.priority = priority; return l }

// GetNextID returns the target node id.
func (l *LinkSpec) GetNextID() string { return l.nextID }

// GetTitle returns the link title.
func (l *LinkSpec) GetTitle() string { return l.title }

// GetMeta returns the link meta map (may be nil).
func (l *LinkSpec) GetMeta() map[string]any { return l.meta }

// GetWhen returns the branch condition expression.
func (l *LinkSpec) GetWhen() string { return l.when }

// GetWhenComponent returns the hard-coded branch condition component.
func (l *LinkSpec) GetWhenComponent() ConditionComponent { return l.whenComponent }

// GetPriority returns the priority.
func (l *LinkSpec) GetPriority() int { return l.priority }
