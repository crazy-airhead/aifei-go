package dami

// Router routes events to listeners by topic. Implementations decide what "by
// topic" means: exact hash lookup (HashRouter), wildcard patterns (PathRouter),
// or topic+tag matching (TagRouter). Mirrors Java's EventRouter.
type Router interface {
	// Add registers a holder under a topic expression.
	Add(topic string, h *holder)
	// Remove removes a specific holder; safe to call with an absent holder.
	Remove(topic string, h *holder)
	// RemoveAll removes every holder under a topic expression.
	RemoveAll(topic string)
	// Match returns the holders whose expression matches the sent topic, ordered
	// by index ascending. A nil/empty result means "no listener" (handled stays
	// false, so Send runs its fallback).
	Match(topic string) []*holder
	// Count returns the number of matching holders for a topic.
	Count(topic string) int
}
