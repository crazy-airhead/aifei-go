package dami

import (
	"cmp"
	"slices"
	"strings"
	"sync"
)

// TagRouter matches by topic plus optional tags. An expression "topic:tag1,tag2"
// splits at the first ':' into a topic and a tag set (',' separated). Two
// expressions match when their topics are equal AND (either side has no tags, or
// their tag sets intersect). Expressions are bucketed by topic for lookup.
// Mirrors Java's TagTopicEventRouter / TagRouting.
type TagRouter struct {
	mu      sync.RWMutex
	buckets map[string][]*tagRouting // keyed by topic (the part before ':')
}

// NewTagRouter builds an empty TagRouter.
func NewTagRouter() *TagRouter {
	return &TagRouter{buckets: make(map[string][]*tagRouting)}
}

type tagRouting struct {
	expr string
	tags topicTags
	h    *holder
}

// topicTags is a parsed "topic:tag1,tag2" expression: the topic and its tag set.
type topicTags struct {
	topic string
	tags  []string // empty when the expression carried no tags
}

func parseTopicTags(expr string) topicTags {
	idx := strings.IndexByte(expr, ':')
	if idx < 0 {
		return topicTags{topic: expr}
	}
	tt := topicTags{topic: expr[:idx]}
	if idx+1 < len(expr) {
		for _, t := range strings.Split(expr[idx+1:], ",") {
			if t = strings.TrimSpace(t); t != "" {
				tt.tags = append(tt.tags, t)
			}
		}
	}
	return tt
}

// matches reports whether a listener's tags (receiver) match a sent topic+tags.
// Topics must be equal; tags match when either side has none or they intersect.
func (lt topicTags) matches(st topicTags) bool {
	if lt.topic != st.topic {
		return false
	}
	if len(lt.tags) == 0 || len(st.tags) == 0 {
		return true
	}
	for _, a := range lt.tags {
		for _, b := range st.tags {
			if a == b {
				return true
			}
		}
	}
	return false
}

// Add implements Router.
func (r *TagRouter) Add(topic string, h *holder) {
	tt := parseTopicTags(topic)
	r.mu.Lock()
	r.buckets[tt.topic] = append(r.buckets[tt.topic], &tagRouting{expr: topic, tags: tt, h: h})
	r.mu.Unlock()
}

// Remove implements Router.
func (r *TagRouter) Remove(topic string, h *holder) {
	tt := parseTopicTags(topic)
	r.mu.Lock()
	list := r.buckets[tt.topic]
	r.buckets[tt.topic] = slices.DeleteFunc(list, func(tr *tagRouting) bool {
		return tr.expr == topic && tr.h == h
	})
	r.mu.Unlock()
}

// RemoveAll implements Router.
func (r *TagRouter) RemoveAll(topic string) {
	tt := parseTopicTags(topic)
	r.mu.Lock()
	list := r.buckets[tt.topic]
	r.buckets[tt.topic] = slices.DeleteFunc(list, func(tr *tagRouting) bool { return tr.expr == topic })
	r.mu.Unlock()
}

// Match implements Router.
func (r *TagRouter) Match(topic string) []*holder {
	st := parseTopicTags(topic)
	r.mu.RLock()
	list := r.buckets[st.topic]
	r.mu.RUnlock()

	var result []*holder
	for _, tr := range list {
		if tr.tags.matches(st) {
			result = append(result, tr.h)
		}
	}
	if len(result) < 2 {
		return result
	}
	slices.SortFunc(result, func(a, b *holder) int { return cmp.Compare(a.index, b.index) })
	return result
}

// Count implements Router.
func (r *TagRouter) Count(topic string) int { return len(r.Match(topic)) }

// ClearAll implements Router.
func (r *TagRouter) ClearAll() {
	r.mu.Lock()
	clear(r.buckets)
	r.mu.Unlock()
}
