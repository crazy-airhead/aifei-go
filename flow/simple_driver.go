package flow

// SimpleDriver is the default Driver: HandleTask delegates straight to
// PostHandleTask (i.e. it runs tasks, no extra behavior). Build one with options
// via SimpleDriverBuilder. Mirrors Java's SimpleFlowDriver.
type SimpleDriver struct {
	*AbstractDriver
}

// NewSimpleDriver creates a SimpleDriver with optional evaluation/container/executor.
func NewSimpleDriver(opts ...SimpleDriverOption) *SimpleDriver {
	b := &SimpleDriverBuilder{}
	for _, o := range opts {
		o(b)
	}
	return &SimpleDriver{AbstractDriver: NewAbstractDriver(b.evaluation, b.container, b.executor)}
}

// SimpleDriverOption configures a SimpleDriver.
type SimpleDriverOption func(*SimpleDriverBuilder)

// WithEvaluation sets the evaluation.
func WithEvaluation(e Evaluation) SimpleDriverOption {
	return func(b *SimpleDriverBuilder) { b.evaluation = e }
}

// WithContainer sets the container.
func WithContainer(c Container) SimpleDriverOption {
	return func(b *SimpleDriverBuilder) { b.container = c }
}

// WithExecutor sets the async executor.
func WithExecutor(e func(fn func())) SimpleDriverOption {
	return func(b *SimpleDriverBuilder) { b.executor = e }
}

// HandleTask delegates to PostHandleTask (default task execution).
func (d *SimpleDriver) HandleTask(ex *Exchanger, task TaskDesc) error {
	return d.PostHandleTask(ex, task)
}

// SimpleDriverBuilder is a fluent builder for SimpleDriver.
type SimpleDriverBuilder struct {
	evaluation Evaluation
	container  Container
	executor   func(fn func())
}

// NewSimpleDriverBuilder creates a builder.
func NewSimpleDriverBuilder() *SimpleDriverBuilder { return &SimpleDriverBuilder{} }

// Evaluation sets the evaluation.
func (b *SimpleDriverBuilder) Evaluation(e Evaluation) *SimpleDriverBuilder {
	b.evaluation = e
	return b
}

// Container sets the container.
func (b *SimpleDriverBuilder) Container(c Container) *SimpleDriverBuilder { b.container = c; return b }

// Executor sets the async executor.
func (b *SimpleDriverBuilder) Executor(e func(fn func())) *SimpleDriverBuilder {
	b.executor = e
	return b
}

// Build builds the SimpleDriver.
func (b *SimpleDriverBuilder) Build() *SimpleDriver {
	return &SimpleDriver{AbstractDriver: NewAbstractDriver(b.evaluation, b.container, b.executor)}
}
