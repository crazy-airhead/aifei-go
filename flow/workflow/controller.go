package workflow

import "github.com/crazy-airhead/aifei-go/flow"

// StateController decides whether a node requires a human task and who may operate
// it. Mirrors Java's StateController.
type StateController interface {
	// IsOperatable reports whether the current user may operate the node.
	IsOperatable(ctx flow.Context, node *flow.Node) bool
	// IsAutoForward reports whether the node auto-advances (no human task).
	IsAutoForward(ctx flow.Context, node *flow.Node) bool
}

// defaultAutoForward: non-activity nodes auto-advance (the interface default).
func defaultAutoForward(node *flow.Node) bool {
	return node.Type() != flow.NodeTypeActivity
}

// BlockStateController: only ACTIVITY nodes are operatable; auto-forward is the
// default. Mirrors Java's BlockStateController.
type BlockStateController struct{}

// NewBlockStateController creates a BlockStateController.
func NewBlockStateController() *BlockStateController { return &BlockStateController{} }

// IsOperatable: true only for activity nodes.
func (*BlockStateController) IsOperatable(_ flow.Context, node *flow.Node) bool {
	return node.Type() == flow.NodeTypeActivity
}

// IsAutoForward: the default (non-activity auto-advances).
func (*BlockStateController) IsAutoForward(_ flow.Context, node *flow.Node) bool {
	return defaultAutoForward(node)
}

// NotBlockStateController: every node is operatable (no actor restriction);
// auto-forward is the default. Mirrors Java's NotBlockStateController.
type NotBlockStateController struct{}

// NewNotBlockStateController creates a NotBlockStateController.
func NewNotBlockStateController() *NotBlockStateController { return &NotBlockStateController{} }

// IsOperatable always returns true.
func (*NotBlockStateController) IsOperatable(flow.Context, *flow.Node) bool { return true }

// IsAutoForward: the default.
func (*NotBlockStateController) IsAutoForward(_ flow.Context, node *flow.Node) bool {
	return defaultAutoForward(node)
}

// ActorStateController: a node is operatable when its meta[key] matches the
// context var [key]; a node with any actor meta key requires a task, others
// auto-advance. Mirrors Java's ActorStateController.
type ActorStateController struct {
	keys []string
}

// NewActorStateController creates an ActorStateController over the given meta keys
// (default "actor").
func NewActorStateController(keys ...string) *ActorStateController {
	if len(keys) == 0 {
		keys = []string{"actor"}
	}
	return &ActorStateController{keys: keys}
}

// IsOperatable: true when meta[key] == ctx[key] for some key.
func (c *ActorStateController) IsOperatable(ctx flow.Context, node *flow.Node) bool {
	for _, k := range c.keys {
		meta := node.MetaAsString(k)
		if meta != "" && meta == toString(ctx.Get(k)) {
			return true
		}
	}
	return false
}

// IsAutoForward: END auto-advances; a node with any actor meta key requires a task;
// otherwise auto-advance.
func (c *ActorStateController) IsAutoForward(_ flow.Context, node *flow.Node) bool {
	if node.Type() == flow.NodeTypeEnd {
		return true
	}
	for _, k := range c.keys {
		if node.HasMeta(k) {
			return false
		}
	}
	return true
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
