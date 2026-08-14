package flow

// TaskComponent runs a node's task. The Go equivalent of Java's TaskComponent
// functional interface (run(context, node)). Implement it as a type with a Run
// method, or wrap a function with TaskFunc.
type TaskComponent interface {
	Run(ctx Context, node *Node) error
}

// ConditionComponent tests a branch/node condition. The Go equivalent of Java's
// ConditionComponent functional interface (test(context)). Implement it as a type
// with a Test method, or wrap a function with ConditionFunc.
type ConditionComponent interface {
	Test(ctx Context) (bool, error)
}

// NamedTaskComponent is a TaskComponent with a name and title, used for
// component-driven graph building (GraphSpec.AddActivity(NamedTaskComponent)).
// Mirrors Java's NamedTaskComponent (extends TaskComponent).
type NamedTaskComponent interface {
	TaskComponent
	Name() string
	Title() string
}

// taskFunc adapts a plain function to TaskComponent.
type taskFunc struct{ fn func(Context, *Node) error }

func (t taskFunc) Run(ctx Context, node *Node) error { return t.fn(ctx, node) }

// TaskFunc wraps fn as a TaskComponent.
func TaskFunc(fn func(Context, *Node) error) TaskComponent { return taskFunc{fn: fn} }

// condFunc adapts a plain function to ConditionComponent.
type condFunc struct{ fn func(Context) (bool, error) }

func (c condFunc) Test(ctx Context) (bool, error) { return c.fn(ctx) }

// ConditionFunc wraps fn as a ConditionComponent.
func ConditionFunc(fn func(Context) (bool, error)) ConditionComponent { return condFunc{fn: fn} }
