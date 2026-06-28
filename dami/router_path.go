package dami

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// PathRouter matches topics with * and ** wildcards: * is one segment not
// containing a separator, ** is any run including separators; '.' and '/' are
// both valid separators. Exact (wildcard-free) expressions use a fast map; only
// patterned expressions fall through to a regex scan. Mirrors Java's
// PathTopicEventRouter / PathRouting.
type PathRouter struct {
	mu       sync.RWMutex
	exact    map[string]*pipeline
	patterns []*pathRouting
}

// NewPathRouter builds an empty PathRouter.
func NewPathRouter() *PathRouter {
	return &PathRouter{exact: make(map[string]*pipeline)}
}

type pathRouting struct {
	expr    string
	holder_ *holder
	pattern *regexp.Regexp // nil for exact expressions (handled via the map)
}

func newPathRouting(expr string, h *holder) *pathRouting {
	pr := &pathRouting{expr: expr, holder_: h}
	if strings.Contains(expr, "*") {
		p := expr
		p = strings.ReplaceAll(p, ".", `\.`)
		p = strings.ReplaceAll(p, "**", "\x00") // placeholder so the single-* step leaves it intact
		p = strings.ReplaceAll(p, "*", `[^/.]*`)
		p = strings.ReplaceAll(p, "\x00", `.*`)
		p = "^" + p + "$"
		pr.pattern = regexp.MustCompile(p)
	}
	return pr
}

func (pr *pathRouting) matches(topic string) bool {
	if pr.expr == topic {
		return true
	}
	return pr.pattern != nil && pr.pattern.MatchString(topic)
}

// Add implements Router.
func (r *PathRouter) Add(topic string, h *holder) {
	if strings.Contains(topic, "*") {
		r.mu.Lock()
		r.patterns = append(r.patterns, newPathRouting(topic, h))
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	p, ok := r.exact[topic]
	if !ok {
		p = newPipeline()
		r.exact[topic] = p
	}
	r.mu.Unlock()
	p.add(h)
}

// Remove implements Router.
func (r *PathRouter) Remove(topic string, h *holder) {
	if strings.Contains(topic, "*") {
		r.mu.Lock()
		r.patterns = slices.DeleteFunc(r.patterns, func(pr *pathRouting) bool {
			return pr.expr == topic && pr.holder_ == h
		})
		r.mu.Unlock()
		return
	}
	r.mu.RLock()
	p := r.exact[topic]
	r.mu.RUnlock()
	if p != nil {
		p.remove(h)
	}
}

// RemoveAll implements Router.
func (r *PathRouter) RemoveAll(topic string) {
	r.mu.Lock()
	delete(r.exact, topic)
	r.patterns = slices.DeleteFunc(r.patterns, func(pr *pathRouting) bool { return pr.expr == topic })
	r.mu.Unlock()
}

// Match implements Router.
func (r *PathRouter) Match(topic string) []*holder {
	r.mu.RLock()
	pats := r.patterns
	r.mu.RUnlock()

	var result []*holder
	for _, pr := range pats {
		if pr.matches(topic) {
			result = append(result, pr.holder_)
		}
	}
	// Exact match is read under its own lock after releasing the router lock.
	if p := r.exactLookup(topic); p != nil {
		result = append(result, p.snapshot()...)
	}
	if len(result) < 2 {
		return result
	}
	slices.SortFunc(result, func(a, b *holder) int { return cmp.Compare(a.index, b.index) })
	return result
}

// Count implements Router.
func (r *PathRouter) Count(topic string) int { return len(r.Match(topic)) }

func (r *PathRouter) exactLookup(topic string) *pipeline {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.exact[topic]
}
