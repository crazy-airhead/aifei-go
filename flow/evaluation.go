package flow

// Evaluation evaluates condition and task code strings against a flow Context.
// The default implementation reuses the enjoy expression engine; alternatives can
// be plugged in (see flow/evaluation). Mirrors Java's Evaluation.
type Evaluation interface {
	// RunCondition evaluates a condition expression and returns its truthiness:
	// nil → false, bool → as-is, anything else → true.
	RunCondition(ctx Context, code string) (bool, error)

	// RunTask executes a task statement (expression/assignment/method-call). For
	// multi-statement code, statements are separated by ';'.
	RunTask(ctx Context, code string) error
}
